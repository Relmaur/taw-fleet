package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/exec"
)

func TestWPRemote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "claude-bot" || p != "ab cd" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Query().Get("rest_route") {
		case "/wp/v2/users/me":
			_, _ = io.WriteString(w, `{"name":"Claude Bot","roles":["editor"]}`)
		case "/wp/v2/pages":
			_, _ = io.WriteString(w, `[{"id":12,"slug":"`+r.URL.Query().Get("slug")+`"}]`)
		case "/wp/v2/pages/12":
			b, _ := io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"id":12,"got":`+string(b)+`}`)
		}
	}))
	defer srv.Close()

	p := fixture(t)
	write(t, config.File(p), "[sites.acme]\nproduction_url = \""+srv.URL+"\"\n")
	var stored string
	r := &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		if s.Name == "/usr/bin/security" && len(s.Args) > 0 {
			switch s.Args[0] {
			case "-i":
				b, _ := io.ReadAll(s.Stdin)
				stored = string(b)
			case "find-generic-password":
				if !strings.Contains(stored, "-a wp-acme") {
					return exec.Result{Code: 44}, nil
				}
				return exec.Result{Stdout: []byte("claude-bot:ab cd\n")}, nil
			}
		}
		return exec.Result{}, nil
	}}
	run := func(in string, args ...string) (string, error) {
		var out bytes.Buffer
		p.Applications = []string{filepath.Join(p.Home, "Applications")}
		d := Deps{Paths: p, Runner: r, In: strings.NewReader(in), Out: &out, Err: &out, Dark: true, GitHub: fakeGitHub(t), WPHTTP: srv.Client()}
		root := NewRoot(BuildInfo{Version: "1.2.3"}, d)
		root.SetArgs(args)
		err := root.Execute()
		return out.String(), err
	}

	if _, err := run("", "wp-remote", "acme", "GET", "/wp/v2/users/me"); err == nil || !strings.Contains(err.Error(), "wp-remote key import acme") {
		t.Fatalf("no creds: %v", err)
	}
	if out, err := run("claude-bot:ab cd\n", "wp-remote", "key", "import", "acme"); err != nil || !strings.Contains(out, "Stored acme's bot credentials (claude-bot)") {
		t.Fatalf("import: %q %v", out, err)
	}
	if strings.Contains(strings.Join(r.Calls()[len(r.Calls())-1].Args, " "), "ab cd") {
		t.Error("the password must never be on a command line")
	}
	if out, err := run("", "wp-remote", "key", "show", "acme"); err != nil || !strings.Contains(out, "Claude Bot (editor)") || strings.Contains(out, "ab cd") {
		t.Errorf("show: %q %v", out, err)
	}
	if out, err := run("", "wp-remote", "acme", "GET", "/wp/v2/pages?slug=nosotros&context=edit"); err != nil || !strings.Contains(out, `"slug":"nosotros"`) {
		t.Errorf("GET: %q %v", out, err)
	}
	if out, err := run(`{"meta":{"_taw_x":"y"}}`, "wp-remote", "acme", "POST", "/wp/v2/pages/12", "--data", "-"); err != nil || !strings.Contains(out, `"got":{"meta":{"_taw_x":"y"}}`) {
		t.Errorf("POST: %q %v", out, err)
	}
	if _, err := run("", "wp-remote", "acme", "DELETE", "/wp/v2/pages/12"); err == nil || !strings.Contains(err.Error(), "never deletes") {
		t.Errorf("DELETE: %v", err)
	}
}
