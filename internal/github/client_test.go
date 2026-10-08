package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
)

const tagsJSON = `[
  {"name":"v1.9.0"},{"name":"v1.76.1"},{"name":"v1.10.0"},
  {"name":"v2.0.0-beta.1"},{"name":"nightly"},{"name":"1.76.0"}
]`

type fakeGitHub struct {
	*httptest.Server
	hits   atomic.Int32
	status atomic.Int32
	auth   atomic.Value
}

func newFake(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{}
	f.status.Store(http.StatusOK)
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.auth.Store(r.Header.Get("Authorization"))
		if r.URL.Path != "/repos/Relmaur/taw-core/tags" {
			http.NotFound(w, r)
			return
		}
		if s := int(f.status.Load()); s != http.StatusOK {
			w.WriteHeader(s)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
			return
		}
		_, _ = w.Write([]byte(tagsJSON))
	}))
	t.Cleanup(f.Close)
	return f
}

func client(f *fakeGitHub, cache string, now *time.Time) *Client {
	c := New(cache, func() string { return "tok123" })
	c.BaseURL = f.URL
	c.Now = func() time.Time { return *now }
	return c
}

func TestLatestTagPicksHighestStable(t *testing.T) {
	f := newFake(t)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	l, err := client(f, "", &now).LatestTag(context.Background(), "Relmaur", "taw-core")
	if err != nil {
		t.Fatal(err)
	}
	if l.Tag != "v1.76.1" || l.Stale {
		t.Errorf("latest = %+v (pre-releases and non-semver tags are skipped)", l)
	}
	if got := f.auth.Load(); got != "Bearer tok123" {
		t.Errorf("Authorization = %v", got)
	}
}

func TestLatestTagCaches(t *testing.T) {
	f := newFake(t)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	c := client(f, t.TempDir(), &now)
	ctx := context.Background()

	for range 3 {
		if _, err := c.LatestTag(ctx, "Relmaur", "taw-core"); err != nil {
			t.Fatal(err)
		}
	}
	if f.hits.Load() != 1 {
		t.Errorf("hits within the TTL = %d, want 1", f.hits.Load())
	}

	now = now.Add(DefaultTTL + time.Minute)
	if _, err := c.LatestTag(ctx, "Relmaur", "taw-core"); err != nil {
		t.Fatal(err)
	}
	if f.hits.Load() != 2 {
		t.Errorf("hits after the TTL = %d, want 2", f.hits.Load())
	}
}

func TestLatestTagStaleOnError(t *testing.T) {
	f := newFake(t)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	c := client(f, t.TempDir(), &now)
	ctx := context.Background()
	if _, err := c.LatestTag(ctx, "Relmaur", "taw-core"); err != nil {
		t.Fatal(err)
	}

	f.status.Store(http.StatusForbidden)
	now = now.Add(2 * DefaultTTL)
	l, err := c.LatestTag(ctx, "Relmaur", "taw-core")
	if err != nil || !l.Stale || l.Tag != "v1.76.1" {
		t.Errorf("stale fallback = %+v, %v", l, err)
	}
}

func TestLatestTagErrorWithoutCache(t *testing.T) {
	f := newFake(t)
	f.status.Store(http.StatusForbidden)
	now := time.Now()
	_, err := client(f, t.TempDir(), &now).LatestTag(context.Background(), "Relmaur", "taw-core")
	if err == nil || !strings.Contains(err.Error(), "API rate limit exceeded") {
		t.Errorf("err = %v", err)
	}
}

func TestLatestTagOffline(t *testing.T) {
	f := newFake(t)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	cache := t.TempDir()
	c := client(f, cache, &now)
	c.Offline = true
	if _, err := c.LatestTag(context.Background(), "Relmaur", "taw-core"); !errors.Is(err, ErrOffline) {
		t.Errorf("offline without cache: %v", err)
	}
	if f.hits.Load() != 0 {
		t.Error("offline must not hit the network")
	}

	online := client(f, cache, &now)
	if _, err := online.LatestTag(context.Background(), "Relmaur", "taw-core"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * DefaultTTL)
	l, err := c.LatestTag(context.Background(), "Relmaur", "taw-core")
	if err != nil || l.Tag != "v1.76.1" || !l.Stale {
		t.Errorf("offline with an old cache = %+v, %v", l, err)
	}
	if f.hits.Load() != 1 {
		t.Errorf("hits = %d", f.hits.Load())
	}
}

func TestTokenSource(t *testing.T) {
	envs := map[string]string{}
	p := paths.ForHome(t.TempDir(), func(k string) string { return envs[k] })

	r := &exec.FakeRunner{Script: func(exec.Spec) (exec.Result, error) {
		return exec.Result{Stdout: []byte("gho_fromgh\n")}, nil
	}}
	if tok := TokenSource(p, r)(); tok != "" {
		t.Errorf("no env, no gh: %q", tok)
	}

	p.LookPath = func(string) (string, error) { return "/opt/homebrew/bin/gh", nil }
	src := TokenSource(p, r)
	if tok := src(); tok != "gho_fromgh" {
		t.Errorf("gh: %q", tok)
	}
	_ = src()
	if n := len(r.Calls()); n != 1 {
		t.Errorf("gh called %d times, want once", n)
	}

	envs["GH_TOKEN"] = "env_tok"
	if tok := TokenSource(p, r)(); tok != "env_tok" {
		t.Errorf("env wins: %q", tok)
	}
}
