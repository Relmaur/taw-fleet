package wpremote

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
)

func TestURL(t *testing.T) {
	for route, want := range map[string]string{
		"/wp/v2/pages?slug=nosotros&context=edit": "https://x.mx/?context=edit&rest_route=%2Fwp%2Fv2%2Fpages&slug=nosotros",
		"wp/v2/users/me": "https://x.mx/?rest_route=%2Fwp%2Fv2%2Fusers%2Fme",
	} {
		if got, err := URL("https://x.mx/", route); err != nil || got != want {
			t.Errorf("URL(%q) = %q, %v; want %q", route, got, err, want)
		}
	}
	if _, err := URL("not a url", "/x"); err == nil {
		t.Error("a bad site URL is refused")
	}
}

func TestParseCreds(t *testing.T) {
	c, err := ParseCreds("claude-bot:abcd efgh ijkl\n")
	if err != nil || c.User != "claude-bot" || c.Password != "abcd efgh ijkl" {
		t.Errorf("%+v %v", c, err)
	}
	for _, bad := range []string{"", "nopass", ":pw", "user:"} {
		if _, err := ParseCreds(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDo(t *testing.T) {
	var empties atomic.Int32
	empties.Store(1) // the host's first answer is empty
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "bot" || p != "pw pw" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"code":"rest_not_logged_in","message":"You are not currently logged in."}`)
			return
		}
		switch r.URL.Query().Get("rest_route") {
		case "/wp/v2/users/me":
			if empties.Add(-1) >= 0 {
				return // 200, empty body
			}
			_, _ = io.WriteString(w, `{"name":"Claude Bot","slug":"claude-bot","roles":["editor"]}`)
		case "/wp/v2/pages/7":
			b, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || string(b) != `{"meta":{"_taw_x":"y"}}` {
				t.Errorf("write: %s %s %s", r.Method, r.Header.Get("Content-Type"), b)
			}
			_, _ = io.WriteString(w, `{"id":7}`)
		case "/wp/v2/media":
			b, _ := io.ReadAll(r.Body)
			if r.Header.Get("Content-Type") != "image/webp" || r.Header.Get("Content-Disposition") != `attachment; filename="hero.webp"` || string(b) != "RIFF" {
				t.Errorf("upload: %v %q", r.Header, b)
			}
			_, _ = io.WriteString(w, `{"id":42}`)
		case "/wp/v2/settings":
			w.WriteHeader(403)
			_, _ = io.WriteString(w, `{"code":"rest_forbidden","message":"Sorry, you are not allowed to do that."}`)
		}
	}))
	defer srv.Close()
	c := &Client{Site: srv.URL, Creds: Creds{"bot", "pw pw"}, HTTP: srv.Client(), Pause: time.Millisecond}
	ctx := context.Background()

	me, err := c.WhoAmI(ctx)
	if err != nil || me.Slug != "claude-bot" || me.Roles[0] != "editor" {
		t.Fatalf("WhoAmI after an empty answer: %+v %v", me, err)
	}
	if out, err := c.Do(ctx, Request{Method: "post", Route: "/wp/v2/pages/7", JSON: []byte(`{"meta":{"_taw_x":"y"}}`)}); err != nil || string(out) != `{"id":7}` {
		t.Errorf("write: %s %v", out, err)
	}
	img := filepath.Join(t.TempDir(), "hero.webp")
	_ = os.WriteFile(img, []byte("RIFF"), 0o600)
	if out, err := c.Do(ctx, Request{Method: "POST", Route: "/wp/v2/media", File: img}); err != nil || string(out) != `{"id":42}` {
		t.Errorf("upload: %s %v", out, err)
	}
	var ae *APIError
	if _, err := c.Do(ctx, Request{Method: "GET", Route: "/wp/v2/settings"}); !errors.As(err, &ae) || ae.Code != "rest_forbidden" || !strings.Contains(err.Error(), "403 rest_forbidden: Sorry") {
		t.Errorf("403: %v", err)
	}
	if _, err := c.Do(ctx, Request{Method: "DELETE", Route: "/wp/v2/posts/1"}); err == nil || !strings.Contains(err.Error(), "never deletes") {
		t.Errorf("DELETE: %v", err)
	}
	if _, err := c.Do(ctx, Request{Method: "POST", Route: "/wp/v2/posts", JSON: []byte("{nope")}); err == nil {
		t.Error("invalid JSON is refused before sending")
	}
	bad := &Client{Site: srv.URL, Creds: Creds{"bot", "wrong"}, HTTP: srv.Client(), Pause: time.Millisecond}
	if _, err := bad.WhoAmI(ctx); !errors.As(err, &ae) || ae.Status != 401 {
		t.Errorf("wrong password: %v", err)
	}
}

func TestStore(t *testing.T) {
	var saved string
	f := &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		switch s.Args[0] {
		case "-i":
			b, _ := io.ReadAll(s.Stdin)
			saved = string(b)
		case "find-generic-password":
			if saved == "" {
				return exec.Result{Code: 44}, nil
			}
			return exec.Result{Stdout: []byte("bot:pw pw\n")}, nil
		}
		return exec.Result{}, nil
	}}
	st := Store{Exec: f}
	if _, err := st.Load(context.Background(), "ls-mxico"); !errors.Is(err, ErrNoCreds) {
		t.Errorf("none: %v", err)
	}
	if err := st.Save(context.Background(), "ls-mxico", Creds{"bot", "pw pw"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved, "-a wp-ls-mxico") || !strings.Contains(saved, `"bot:pw pw"`) {
		t.Errorf("stdin = %q", saved)
	}
	if c, err := st.Load(context.Background(), "ls-mxico"); err != nil || c.Password != "pw pw" {
		t.Errorf("%+v %v", c, err)
	}
}
