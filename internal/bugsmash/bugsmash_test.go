package bugsmash

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
)

const (
	projOK   = "a15552c8-a3d9-4bce-9a06-7859fbb7f27c"
	projGone = "a2623439-9da9-48ed-b032-67e4c6b4bcd5"
	apiKey   = "test-key"
)

var now = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// fakeBugSmash answers like the real API (shapes recorded 2026-10-09).
func fakeBugSmash(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-API-Key") != apiKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"status":false,"message":"Invalid X-API-Key"}`)
			return
		}
		switch r.URL.Path {
		case "/project/" + projOK:
			_, _ = io.WriteString(w, `{"status":true,"message":"Project Details","data":{"id":"`+projOK+`","name":"chcapital.mx","frontend_url":"https://x.bugsmash.io",
				"project_versions":[{"short_url":"https://x.bugsmash.io/review/old","is_latest":false},{"short_url":"https://x.bugsmash.io/review/new","is_latest":true}]}}`)
		case "/project/" + projGone:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"status":false,"message":"Project not found","data":[]}`)
		case "/projects":
			if r.URL.Query().Get("page") == "2" {
				_, _ = io.WriteString(w, `{"status":true,"message":"All Projects","current_page":2,"last_page":2,"data":[{"id":"`+projGone+`","name":"Sandbox","type":"web"}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"status":true,"message":"All Projects","current_page":1,"last_page":2,"data":[{"id":"`+projOK+`","name":"chcapital.mx","type":"web","short_url":"https://x.bugsmash.io/review/a"}]}`)
		case "/comments":
			q := r.URL.Query()
			if q.Get("projectId") != projOK || q.Get("status") != "active" {
				t.Errorf("comments query = %v", q)
			}
			_, _ = io.WriteString(w, `{"status":true,"message":"Comment data","active":3,"resolved":101,"data":[
				{"comment_id":"c1","comment_number":106,"comment":"Cambiar texto:\n \"Tasa…\"","status":"Active","comment_by_name":"Paola Hernández","created_at":"2026-10-07T18:00:00.000000Z","location_metadata":{"page_url":"https://chcapital.mx/credito-pyme/"}},
				{"comment_id":"c2","comment_number":108,"comment":"Actualizar texto","status":"Active","comment_by_name":"Paola Hernández","created_at":"2026-10-08T18:00:00.000000Z","location_metadata":{"page_url":"https://chcapital.mx/credito-pyme/"}},
				{"comment_id":"c3","comment_number":90,"comment":"Old","status":"Resolved","created_at":"2026-09-09T10:00:00Z"},
				{"comment_id":"c4","comment_number":107,"comment":"No location","status":"Active","created_at":"2026-10-07T19:00:00.000000Z","location_metadata":null}
			]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"status":false,"message":"no route"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheck(t *testing.T) {
	var calls atomic.Int32
	srv := fakeBugSmash(t, &calls)
	pr := &Prober{Client: &Client{BaseURL: srv.URL, Key: apiKey}, Now: func() time.Time { return now }}

	f := pr.Check(context.Background(), Target{Slug: "ch-capital---taw", Project: projOK})
	if f.Error != "" {
		t.Fatalf("error: %s", f.Error)
	}
	if f.Project != "chcapital.mx" || f.URL != "https://x.bugsmash.io/review/new" || f.Open != 3 || len(f.Comments) != 3 {
		t.Fatalf("got %+v", f)
	}
	if f.Comments[0].Number != 108 || f.Comments[2].Number != 106 {
		t.Errorf("not newest first: %d, %d", f.Comments[0].Number, f.Comments[2].Number)
	}
	if want := time.Date(2026, 10, 7, 18, 0, 0, 0, time.UTC); !f.Oldest.Equal(want) {
		t.Errorf("oldest = %v", f.Oldest)
	}
	if f.Comments[2].Text != `Cambiar texto: "Tasa…"` || f.Comments[2].Page != "https://chcapital.mx/credito-pyme/" || f.Comments[2].Author != "Paola Hernández" {
		t.Errorf("comment = %+v", f.Comments[2])
	}

	gone := pr.Check(context.Background(), Target{Slug: "ls-mxico", Project: projGone})
	if gone.ErrorKind != "not-found" || !strings.Contains(gone.Error, "isn't there any more") {
		t.Errorf("deleted project: %+v", gone)
	}

	bad := &Prober{Client: &Client{BaseURL: srv.URL, Key: "wrong"}, Now: func() time.Time { return now }}
	if f := bad.Check(context.Background(), Target{Slug: "x", Project: projOK}); f.ErrorKind != "auth" {
		t.Errorf("bad key: %+v", f)
	}

	down := &Prober{Client: &Client{BaseURL: "http://127.0.0.1:1", Key: apiKey}, Now: func() time.Time { return now }}
	if f := down.Check(context.Background(), Target{Slug: "x", Project: projOK}); f.ErrorKind != "unreachable" {
		t.Errorf("down: %+v", f)
	}
}

func TestCheckAllCaches(t *testing.T) {
	var calls atomic.Int32
	srv := fakeBugSmash(t, &calls)
	clock := now
	pr := &Prober{Client: &Client{BaseURL: srv.URL, Key: apiKey}, CacheDir: t.TempDir(), Now: func() time.Time { return clock }}
	targets := []Target{{Slug: "ch-capital---taw", Project: projOK}, {Slug: "ls-mxico", Project: projGone}}

	first := pr.CheckAll(context.Background(), targets, false)
	if first["ch-capital---taw"].Open != 3 || first["ls-mxico"].ErrorKind != "not-found" {
		t.Fatalf("first = %+v", first)
	}
	n := calls.Load()
	pr.CheckAll(context.Background(), targets, false)
	if got := calls.Load() - n; got != 1 {
		t.Errorf("cached run made %d calls; want 1 (only the failed project is asked again)", got)
	}
	clock = clock.Add(CacheTTL + time.Second)
	n = calls.Load()
	pr.CheckAll(context.Background(), targets, false)
	if got := calls.Load() - n; got != 3 {
		t.Errorf("expired cache made %d calls; want 3", got)
	}
	n = calls.Load()
	pr.CheckAll(context.Background(), targets, true)
	if got := calls.Load() - n; got != 3 {
		t.Errorf("fresh run made %d calls; want 3", got)
	}
}

func TestKeyStore(t *testing.T) {
	stored := ""
	f := &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		switch s.Args[0] {
		case "-i":
			b, _ := io.ReadAll(s.Stdin)
			if strings.Contains(string(b), "-a bugsmash-api-key") {
				stored = "kc-key"
			}
			return exec.Result{}, nil
		case "find-generic-password":
			if stored == "" {
				return exec.Result{Code: 44}, nil
			}
			return exec.Result{Stdout: []byte(stored + "\n")}, nil
		}
		return exec.Result{Code: 1}, nil
	}}
	env := map[string]string{}
	p := paths.ForHome(t.TempDir(), func(k string) string { return env[k] })
	ks := KeyStore{Exec: f}

	if _, _, err := ks.Load(context.Background(), p); !errors.Is(err, ErrNoKey) {
		t.Errorf("nothing stored: %v", err)
	}
	env[KeyEnv] = "env-key"
	if k, from, err := ks.Load(context.Background(), p); err != nil || k != "env-key" || from != KeyEnv {
		t.Errorf("env: %q %q %v", k, from, err)
	}
	if err := ks.Save(context.Background(), "  kc-key \n"); err != nil {
		t.Fatal(err)
	}
	if k, from, err := ks.Load(context.Background(), p); err != nil || k != "kc-key" || from != "Keychain" {
		t.Errorf("keychain wins: %q %q %v", k, from, err)
	}
	if err := ks.Save(context.Background(), "  "); err == nil {
		t.Error("an empty key must be refused")
	}
}

func TestProjectsPages(t *testing.T) {
	var calls atomic.Int32
	srv := fakeBugSmash(t, &calls)
	ps, err := (&Client{BaseURL: srv.URL, Key: apiKey}).Projects(context.Background())
	if err != nil || len(ps) != 2 || ps[0].Name != "chcapital.mx" || ps[1].ID != projGone || ps[0].ShortURL == "" {
		t.Errorf("projects = %+v, %v", ps, err)
	}
}

func TestNoKeyAndApply(t *testing.T) {
	res := NoKey([]Target{{Slug: "a", Project: projOK}}, now)
	if res["a"].ErrorKind != "no-key" {
		t.Errorf("NoKey = %+v", res)
	}
}

func TestReviewURL(t *testing.T) {
	for _, c := range []struct {
		p    Project
		want string
	}{
		{Project{Versions: []Version{{ShortURL: "a"}, {ShortURL: "b", IsLatest: true}}, FrontendURL: "f"}, "b"},
		{Project{Versions: []Version{{ShortURL: "a"}}, FrontendURL: "f"}, "a"},
		{Project{FrontendURL: "f"}, "f"},
		{Project{}, ""},
	} {
		if got := c.p.ReviewURL(); got != c.want {
			t.Errorf("ReviewURL(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
}
