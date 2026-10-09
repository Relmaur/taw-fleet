package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// fakeGitHub answers every tags request with taw-core's newest being v1.76.1.
func fakeGitHub(t *testing.T) *github.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/taw-core/tags"):
			_, _ = w.Write([]byte(`[{"name":"v1.76.1"},{"name":"v1.59.2"}]`))
		case strings.HasSuffix(r.URL.Path, "/taw-theme/tags"):
			_, _ = w.Write([]byte(`[{"name":"v1.12.43"}]`))
		default:
			_, _ = w.Write([]byte(`[{"name":"v0.3.41"}]`))
		}
	}))
	t.Cleanup(srv.Close)
	c := github.New("", nil)
	c.BaseURL = srv.URL
	return c
}

func run(t *testing.T, p paths.Paths, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	// The fake runner answers git with nothing: no theme is a repo here.
	d := Deps{Paths: p, Runner: &exec.FakeRunner{}, Out: &out, Err: &out, Dark: true, GitHub: fakeGitHub(t)}
	root := NewRoot(BuildInfo{Version: "1.2.3", Commit: "abc123"}, d)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture: one TAW site (running, two themes, one symlinked) and one plain site.
func fixture(t *testing.T) paths.Paths {
	t.Helper()
	home := t.TempDir()
	p := paths.ForHome(home, nil)
	themes := func(slug string) string {
		return filepath.Join(home, "Local Sites", slug, "app", "public", "wp-content", "themes")
	}
	write(t, filepath.Join(themes("acme"), "acme-theme", "composer.json"), `{"name":"taw/theme","require":{"taw/core":"^1.0"}}`)
	write(t, filepath.Join(themes("acme"), "acme-theme", "vendor", "composer", "installed.json"), `{"packages":[{"name":"taw/core","version":"v1.59.2"}]}`)
	gut := filepath.Join(home, "umbrella", "taw-gutenberg")
	write(t, filepath.Join(gut, "composer.json"), `{"name":"taw/gutenberg","type":"wordpress-theme","require":{"taw/core":"^1.50"}}`)
	write(t, filepath.Join(gut, "vendor", "composer", "installed.json"), `{"packages":[{"name":"taw/core","version":"v1.76.1"}]}`)
	if err := os.Symlink(gut, filepath.Join(themes("acme"), "taw-gutenberg")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(themes("plain"), "plain", "style.css"), "")
	write(t, filepath.Join(p.LocalSupport, "sites.json"), `{
	  "a1": {"id":"a1","name":"Acme","path":"~/Local Sites/acme","domain":"acme.local",
	         "services":{"php":{"name":"php","version":"8.2.30"}}},
	  "p1": {"id":"p1","name":"Plain","path":"~/Local Sites/plain","domain":"plain.local"}
	}`)
	write(t, filepath.Join(p.LocalSupport, "site-statuses.json"), `{"a1":"running","p1":"halted"}`)
	return p
}

func TestVersionPrintsBuildInfo(t *testing.T) {
	out, err := run(t, paths.ForHome(t.TempDir(), nil), "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if want := "taw-fleet 1.2.3 (abc123)\n"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestVersionRejectsArguments(t *testing.T) {
	if _, err := run(t, paths.ForHome(t.TempDir(), nil), "version", "extra"); err == nil {
		t.Fatal("expected an error for an extra argument")
	}
}

func TestListTable(t *testing.T) {
	out, err := run(t, fixture(t), "list")
	if err != nil {
		t.Fatal(err)
	}
	// Output to a buffer is never colored.
	if strings.Contains(out, "\x1b[") {
		t.Errorf("escape codes in non-terminal output:\n%s", out)
	}
	for _, want := range []string{
		"1 site", "2 TAW themes", "1 running",
		"acme", "acme-theme", "taw-gutenberg ↗", "classic", "block", "acme.local",
		"1.59.2 ▲ 1.76.1", "1.76.1", "no git", "TAW/CORE",
		"1 more site without a TAW theme (--all)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "plain.local") {
		t.Errorf("the non-TAW site must be hidden:\n%s", out)
	}
}

func TestListAll(t *testing.T) {
	out, err := run(t, fixture(t), "list", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plain.local") || !strings.Contains(out, "other") {
		t.Errorf("--all must show the plain site:\n%s", out)
	}
}

func TestListJSON(t *testing.T) {
	out, err := run(t, fixture(t), "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rep scan.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rep.Sites) != 1 || rep.Sites[0].Slug != "acme" || len(rep.Sites[0].Themes) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	var symlinks int
	for _, th := range rep.Sites[0].Themes {
		if !th.IsTAW {
			t.Errorf("non-TAW theme in default JSON: %+v", th)
		}
		if th.Symlink {
			symlinks++
		}
	}
	if symlinks != 1 {
		t.Errorf("symlinks = %d", symlinks)
	}
	// snake_case keys are the scripting contract.
	for _, key := range []string{`"scanned_at"`, `"is_taw"`, `"web_root"`, `"socket_live"`} {
		if !strings.Contains(out, key) {
			t.Errorf("missing key %s", key)
		}
	}
}

func TestListWithoutLocal(t *testing.T) {
	out, err := run(t, paths.ForHome(t.TempDir(), nil), "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no Local by Flywheel sites found") || !strings.Contains(out, "No TAW sites found") {
		t.Errorf("out:\n%s", out)
	}
}

func TestShow(t *testing.T) {
	out, err := run(t, fixture(t), "show", "acme")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"acme", "http://acme.local", "running", "PHP 8.2.30", "id a1",
		"acme-theme", "classic", "taw-gutenberg", "block", "↗ symlink", "links to   ~/umbrella/taw-gutenberg", "wp-content/themes/acme-theme",
		"1.59.2 ▲ 1.76.1", "1.76.1  (latest)", "not its own repository",
		"taw/core v1.59.2, latest is v1.76.1", "core.behind", "→ composer update taw/core",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestShowResolvesAndFails(t *testing.T) {
	p := fixture(t)
	if out, err := run(t, p, "show", "acme.local"); err != nil || !strings.Contains(out, "acme-theme") {
		t.Errorf("by domain: %v", err)
	}
	if _, err := run(t, p, "show", "nope"); err == nil || !strings.Contains(err.Error(), "no site matches") {
		t.Errorf("err = %v", err)
	}
	if _, err := run(t, p, "show"); err == nil {
		t.Error("show needs a site")
	}
}

func TestShowJSON(t *testing.T) {
	out, err := run(t, fixture(t), "show", "acme", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Site     site.Site      `json:"site"`
		Findings []site.Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	codes := map[string]bool{}
	for _, f := range v.Findings {
		codes[f.Code] = true
	}
	if v.Site.Slug != "acme" || !codes["core.behind"] || !codes["tools.php-missing"] {
		t.Errorf("show json = %+v", v)
	}
}

func TestDoctor(t *testing.T) {
	out, err := run(t, fixture(t), "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"taw-fleet doctor", "0 errors", "2 warnings", "2 notes", "acme", "tools.php-missing",
		"taw/core v1.59.2, latest is v1.76.1", "the theme isn't its own git repository", "git.none"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDoctorJSONAndOffline(t *testing.T) {
	out, err := run(t, fixture(t), "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var v doctorJSON
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v.Counts["warn"] != 2 || v.Counts["info"] != 2 || len(v.Clean) != 0 {
		t.Errorf("doctor json = %+v", v)
	}

	// Offline with no cache: latest is unknown, so nothing is "behind", and
	// the doctor says why.
	out, err = run(t, fixture(t), "doctor", "--json", "--offline")
	if err != nil {
		t.Fatal(err)
	}
	v = doctorJSON{}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	var behind, github int
	for _, f := range v.Findings {
		switch {
		case f.Code == "core.behind":
			behind++
		case strings.HasPrefix(f.Code, "scan.github"):
			github++
		}
	}
	if behind != 0 || github == 0 {
		t.Errorf("offline findings = %+v", v.Findings)
	}
}

func runWith(t *testing.T, p paths.Paths, r *exec.FakeRunner, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	p.Applications = []string{filepath.Join(p.Home, "Applications")} // no real apps in tests
	d := Deps{Paths: p, Runner: r, Out: &out, Err: &out, Dark: true, GitHub: fakeGitHub(t)}
	root := NewRoot(BuildInfo{Version: "1.2.3", Commit: "abc123"}, d)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestOpenFinderAndErrors(t *testing.T) {
	p := fixture(t)
	r := &exec.FakeRunner{}
	out, err := runWith(t, p, r, "open", "acme-theme", "--finder")
	if err != nil || !strings.Contains(out, "Showed acme-theme in Finder") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	var opened bool
	for _, c := range r.Calls() {
		if c.Name == "/usr/bin/open" && c.Args[0] == "-R" && strings.HasSuffix(c.Args[1], "acme-theme") {
			opened = true
		}
	}
	if !opened {
		t.Errorf("calls = %+v", r.Calls())
	}
	if _, err := runWith(t, p, r, "open", "acme"); err == nil || !strings.Contains(err.Error(), "add --theme") {
		t.Errorf("two TAW themes need --theme: %v", err)
	}
	if _, err := runWith(t, p, r, "open", "acme", "--theme", "acme-theme"); err == nil || !strings.Contains(err.Error(), "editor") {
		t.Errorf("no editor installed: %v", err)
	}
}

func TestHandoffPrintsPrompt(t *testing.T) {
	out, err := runWith(t, fixture(t), &exec.FakeRunner{}, "handoff", "acme", "--theme", "acme-theme")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Update the TAW theme `acme-theme` (site `acme`)", "update-theme",
		"installed `1.59.2`, newest `1.76.1` → **behind**", "**You have my approval**", "isn't its own git repository"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHandoffCopy(t *testing.T) {
	var copied string
	r := &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		if s.Name == "/usr/bin/pbcopy" {
			b, _ := io.ReadAll(s.Stdin)
			copied = string(b)
		}
		return exec.Result{}, nil
	}}
	out, err := runWith(t, fixture(t), r, "handoff", "taw-gutenberg", "--copy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Copied the prompt for taw-gutenberg") || !strings.Contains(copied, "block theme") {
		t.Errorf("out=%q copied=%q", out, copied)
	}
}

func TestConfigCommands(t *testing.T) {
	p := fixture(t)
	out, err := runWith(t, p, &exec.FakeRunner{}, "config", "path")
	if err != nil || strings.TrimSpace(out) != filepath.Join(p.ConfigDir, "config.toml") {
		t.Errorf("path: %q %v", out, err)
	}
	if out, err = runWith(t, p, &exec.FakeRunner{}, "config", "init"); err != nil || !strings.Contains(out, "Wrote") {
		t.Errorf("init: %q %v", out, err)
	}
	if _, err = runWith(t, p, &exec.FakeRunner{}, "config", "init"); err == nil {
		t.Error("init twice must refuse")
	}
	if out, err = runWith(t, p, &exec.FakeRunner{}, "config", "show"); err != nil || !strings.Contains(out, "(loaded)") || !strings.Contains(out, "none installed") {
		t.Errorf("show: %q %v", out, err)
	}
	write(t, filepath.Join(p.ConfigDir, "config.toml"), `edtor = "x"`)
	if _, err = runWith(t, p, &exec.FakeRunner{}, "open", "acme-theme"); err == nil || !strings.Contains(err.Error(), "unknown keys") {
		t.Errorf("a broken config is reported: %v", err)
	}
}

func TestSiteOpsRefuseAndExplain(t *testing.T) {
	p := fixture(t)
	r := &exec.FakeRunner{}
	if out, err := runWith(t, p, r, "start", "acme"); err != nil || !strings.Contains(out, "acme is already running") {
		t.Errorf("already running: %q %v", out, err)
	}
	if _, err := runWith(t, p, r, "stop", "acme"); err == nil || !strings.Contains(err.Error(), "add --yes") {
		t.Errorf("no terminal, no --yes: %v", err)
	}
	if _, err := runWith(t, p, r, "stop", "acme", "--yes"); err == nil || !strings.Contains(err.Error(), "isn't open") {
		t.Errorf("Local closed: %v", err)
	}
}

func TestConfirmReadsAnswer(t *testing.T) {
	var out bytes.Buffer
	d := Deps{Interactive: true, In: strings.NewReader("y\n")}
	if ok, err := d.confirm(&out, "Stop acme?"); !ok || err != nil || out.String() != "Stop acme? [y/N] " {
		t.Errorf("ok=%v err=%v out=%q", ok, err, out.String())
	}
	d.In = strings.NewReader("\n")
	if ok, _ := d.confirm(&out, "Stop?"); ok {
		t.Error("enter means no")
	}
}

func TestWPRefusesHaltedSite(t *testing.T) {
	_, err := runWith(t, fixture(t), &exec.FakeRunner{}, "wp", "acme", "option", "get", "stylesheet", "--format=json")
	if err == nil || !strings.Contains(err.Error(), "taw-fleet start acme") {
		t.Errorf("err = %v (flags after the site go to wp-cli, not to cobra)", err)
	}
}

func TestDashboardOpensInItsOwnWindow(t *testing.T) {
	p := fixture(t)
	p.Applications = []string{filepath.Join(p.Home, "Applications")}
	if err := os.MkdirAll(filepath.Join(p.Home, "Applications", "Terminal.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	open := func(args ...string) (string, *exec.FakeRunner, bool) {
		var out bytes.Buffer
		r := &exec.FakeRunner{}
		d := Deps{Paths: p, Runner: r, Out: &out, Err: &out, Interactive: true, GitHub: fakeGitHub(t),
			Executable: func() (string, error) { return "/bin/taw-fleet", nil }}
		root := NewRoot(BuildInfo{Version: "1.2.3"}, d)
		root.SetContext(context.Background())
		g := &globals{}
		if err := root.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		g.window, _ = root.Flags().GetBool("window")
		g.offline, _ = root.Flags().GetBool("offline")
		opened := d.openWindow(root, g)
		return out.String(), r, opened
	}

	out, r, opened := open("--offline")
	if !opened || !strings.Contains(out, "new Terminal window") {
		t.Fatalf("default: opened=%v out=%q", opened, out)
	}
	script, _ := os.ReadFile(r.Calls()[0].Args[2])
	if !strings.Contains(string(script), "'/bin/taw-fleet' '--window=false' '--in-window' '--offline'") {
		t.Errorf("script:\n%s", script)
	}
	if _, r, opened := open("--window=false"); opened || len(r.Calls()) != 0 {
		t.Error("--window=false must stay here")
	}

	write(t, filepath.Join(p.ConfigDir, "config.toml"), "window = false\n")
	if _, _, opened := open(); opened {
		t.Error("config window = false must stay here")
	}
	if _, _, opened := open("--window"); !opened {
		t.Error("--window wins over the config")
	}

	write(t, filepath.Join(p.ConfigDir, "config.toml"), "")
	if err := os.RemoveAll(filepath.Join(p.Home, "Applications", "Terminal.app")); err != nil {
		t.Fatal(err)
	}
	if out, _, opened := open(); opened || !strings.Contains(out, "opening the dashboard here") {
		t.Errorf("no terminal: opened=%v out=%q", opened, out)
	}
}

// leftoverRunner answers like a Mac where an orphaned php-fpm worker (501,
// parent launchd, Local's program) holds acme's PHP socket.
func leftoverRunner(p paths.Paths) *exec.FakeRunner {
	return &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		args := strings.Join(s.Args, " ")
		switch s.Name {
		case "/usr/sbin/lsof":
			switch {
			case strings.HasSuffix(args, "php-fpm.socket"):
				return exec.Result{Stdout: []byte("501\n")}, nil
			case strings.Contains(args, "-p 501 "):
				return exec.Result{Stdout: []byte("p501\nftxt\nn" + filepath.Join(p.LocalSupport, "lightning-services", "php-8.2.30+1", "sbin", "php-fpm") + "\n")}, nil
			}
			return exec.Result{Code: 1}, nil
		case "/bin/ps":
			return exec.Result{Stdout: []byte("  501     1 php-fpm: pool www\n")}, nil
		case "/bin/kill":
			if s.Args[0] == "-0" {
				return exec.Result{Code: 1}, nil // gone
			}
		}
		return exec.Result{}, nil
	}}
}

func TestLeftoversStopSiteOps(t *testing.T) {
	p := fixture(t)
	write(t, filepath.Join(p.LocalSupport, "run", "a1", "php", "php-fpm.socket"), "")
	r := leftoverRunner(p)
	_, err := runWith(t, p, r, "stop", "acme", "--yes")
	if err == nil || !strings.Contains(err.Error(), "stop acme: 1 leftover Local process holds what acme needs: php-fpm 501 (the PHP socket)") ||
		!strings.Contains(err.Error(), "taw-fleet unstick acme") {
		t.Fatalf("stop: %v", err)
	}

	out, err := runWith(t, p, r, "unstick", "acme", "--yes")
	if err != nil || !strings.Contains(out, "php-fpm 501  holds the PHP socket") || !strings.Contains(out, "ended 1 leftover process") {
		t.Fatalf("unstick: %q %v", out, err)
	}
	var term bool
	for _, c := range r.Calls() {
		term = term || (c.Name == "/bin/kill" && strings.Join(c.Args, " ") == "-TERM 501")
	}
	if !term {
		t.Error("unstick sends TERM to the leftover")
	}
	if out, err := runWith(t, p, &exec.FakeRunner{}, "unstick", "acme"); err != nil || !strings.Contains(out, "nothing of Local's is left over") {
		t.Errorf("nothing left: %q %v", out, err)
	}
}
