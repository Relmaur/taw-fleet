package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/bugsmash"
	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/launch"
	"github.com/Relmaur/taw-fleet/internal/pagespeed"
)

func TestLaunchReport(t *testing.T) {
	p := fixture(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><head><meta name=\"robots\" content=\"noindex,follow\"></head></html>"))
	}))
	defer srv.Close()
	write(t, filepath.Join(p.ConfigDir, "config.toml"), "[sites.acme]\nproduction_url = \""+srv.URL+"\"\n")
	var out bytes.Buffer
	d := Deps{Paths: p, Runner: &exec.FakeRunner{}, Out: &out, Err: &out, GitHub: fakeGitHub(t), WebHTTP: srv.Client(),
		PageSpeed: func(_ context.Context, _, strategy string) (pagespeed.Result, error) {
			return pagespeed.Result{Strategy: strategy, Performance: 93, Screenshot: []byte("img"), ScreenshotType: "webp"}, nil
		}}
	root := NewRoot(BuildInfo{Version: "1"}, d)
	root.SetArgs([]string{"launch-report", "acme-theme", "--json", "--offline"})
	if err := root.Execute(); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	var got struct {
		launch.Data
		Report string `json:"report"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	dir := filepath.Join(p.FleetDir, "sites", "acme")
	if got.Report != filepath.Join(dir, "launch.html") || got.Theme != "acme-theme" || len(got.Setup) == 0 {
		t.Errorf("report %q theme %q setup %d", got.Report, got.Theme, len(got.Setup))
	}
	status := map[string]launch.Status{}
	for _, c := range got.Checks {
		status[c.Name] = c.Status
	}
	if status["HTTPS"] != launch.OK || status["Search engines"] != launch.Fail || status["Speed, mobile"] != launch.OK || status["Watch"] != launch.Warn {
		t.Errorf("checks %+v", got.Checks)
	}
	for _, f := range []string{"launch.html", "launch.md", "production/home-mobile.pagespeed.webp", "production/home-desktop.pagespeed.webp"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("not saved: %s", f)
		}
	}
	if !strings.HasPrefix(got.Left[0], "Search engines: wp-admin") {
		t.Errorf("left %q", got.Left)
	}

	// The terminal version.
	out.Reset()
	root = NewRoot(BuildInfo{Version: "1"}, d)
	root.SetArgs([]string{"launch-report", "acme-theme", "--no-speed", "--offline"})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), "Left for you") || !strings.Contains(out.String(), "launch.html") {
		t.Errorf("%v\n%s", err, out.String())
	}
}

func TestCommentsLinkNeedsTheProject(t *testing.T) {
	p := fixture(t)
	// No BugSmash key: nothing is written.
	if _, err := run(t, p, "comments", "link", "acme", "a15552c8-a3d9-4bce-9a06-7859fbb7f27c", "--yes"); err == nil {
		t.Error("linked without asking BugSmash")
	}
	cfg, _ := config.Load(p)
	if cfg.Site("acme").BugSmashProject != "" {
		t.Error("written")
	}
}

func TestCommentsLink(t *testing.T) {
	p := fixture(t)
	const id = "a15552c8-a3d9-4bce-9a06-7859fbb7f27c"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/project/"+id {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":false,"message":"not found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":true,"data":{"id":"` + id + `","name":"Acme production"}}`))
	}))
	defer srv.Close()
	var out bytes.Buffer
	d := Deps{Paths: p, Runner: &exec.FakeRunner{}, Out: &out, Err: &out, GitHub: fakeGitHub(t),
		Feedback: func(context.Context) (*bugsmash.Prober, error) {
			return &bugsmash.Prober{Client: &bugsmash.Client{Key: "k", BaseURL: srv.URL, HTTP: srv.Client()}, CacheDir: p.CacheDir}, nil
		},
		Confirm: func(q string) (bool, error) {
			if !strings.Contains(q, "Acme production ("+id+") to acme") {
				t.Errorf("question %q", q)
			}
			return true, nil
		}}
	for _, tc := range []struct {
		project string
		ok      bool
	}{{"nope", false}, {id, true}} {
		out.Reset()
		root := NewRoot(BuildInfo{Version: "1"}, d)
		root.SetArgs([]string{"comments", "link", "acme", tc.project})
		if err := root.Execute(); (err == nil) != tc.ok {
			t.Errorf("%s: %v\n%s", tc.project, err, out.String())
		}
	}
	cfg, err := config.Load(p)
	if err != nil || cfg.Site("acme").BugSmashProject != id {
		t.Errorf("settings: %v %+v", err, cfg.Site("acme"))
	}
}
