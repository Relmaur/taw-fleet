package create

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/paths"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture: a home with Local's PHP 8.2.29 and 8.5.3, nginx, composer and
// wp-cli, and one existing site "Old Site" (old-site.local).
func fixture(t *testing.T) paths.Paths {
	t.Helper()
	home := t.TempDir()
	p := paths.ForHome(home, nil)
	p.LocalApp = filepath.Join(home, "Local.app")
	plat := "darwin-arm64"
	if runtime.GOARCH != "arm64" {
		plat = "darwin"
	}
	for _, v := range []string{"php-8.2.29+0", "php-8.5.3+1"} {
		write(t, filepath.Join(p.LocalSupport, "lightning-services", v, "bin", plat, "bin", "php"), "")
	}
	_ = os.MkdirAll(filepath.Join(p.LocalSupport, "lightning-services", "nginx-1.26.1+3"), 0o755)
	write(t, filepath.Join(p.LocalApp, "Contents", "Resources", "extraResources", "bin", "composer", "composer.phar"), "")
	write(t, filepath.Join(p.LocalApp, "Contents", "Resources", "extraResources", "bin", "wp-cli", "wp-cli.phar"), "")
	write(t, filepath.Join(p.LocalSupport, "sites.json"), `{"old1":{"id":"old1","name":"Old Site","path":"~/Local Sites/old-site","domain":"old-site.local","services":{"php":{"version":"8.2.29"}}}}`)
	p.LookPath = func(name string) (string, error) {
		if name == "npm" {
			return "/usr/local/bin/npm", nil
		}
		return "", errors.New("not found")
	}
	return p
}

func request() Request {
	return Request{Name: "Acme Shop", Kind: Classic, AdminUser: "marco", AdminEmail: "marco@example.test", AdminPassword: "pw-given"}
}

func TestNormalize(t *testing.T) {
	p := fixture(t)
	r, err := Normalize(p, request())
	if err != nil {
		t.Fatal(err)
	}
	if r.Slug != "acme-shop" || r.Domain != "acme-shop.local" || r.ThemeDir != "acme-shop" || r.PHP != "" || r.AdminPassword != "pw-given" {
		t.Errorf("r = %+v", r)
	}

	in := request()
	in.AdminPassword, in.PHP, in.WebServer = "", "8.5", "nginx"
	r, err = Normalize(p, in)
	if err != nil {
		t.Fatal(err)
	}
	if r.PHP != "8.5.3" || r.WebServer != "nginx-1.26.1" || len(r.AdminPassword) != 20 {
		t.Errorf("r = %+v", r)
	}

	bad := []struct {
		edit func(*Request)
		want string
	}{
		{func(r *Request) { r.Name = " " }, "needs a name"},
		{func(r *Request) { r.Kind = "" }, "classic (taw-theme) or block"},
		{func(r *Request) { r.Domain = "no spaces.local" }, "isn't a host name"},
		{func(r *Request) { r.ThemeDir = "../evil" }, "theme folder"},
		{func(r *Request) { r.AdminEmail = "nope" }, "email"},
		{func(r *Request) { r.AdminUser = "" }, "username"},
		{func(r *Request) { r.PHP = "7.4" }, "installed: 8.5.3, 8.2.29"},
		{func(r *Request) { r.WebServer = "apache" }, "not downloaded in Local"},
		{func(r *Request) { r.WebServer = "caddy" }, "nginx or apache"},
	}
	for _, b := range bad {
		in := request()
		b.edit(&in)
		if _, err := Normalize(p, in); err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%+v: err = %v, want %q", in, err, b.want)
		}
	}
}

func TestSlugifyAndKind(t *testing.T) {
	for in, want := range map[string]string{"CH Capital – TAW": "ch-capital-taw", "LS México": "ls-mexico", "  acme  ": "acme", "Año Nuevo 2027": "ano-nuevo-2027"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]Kind{"classic": Classic, "taw-theme": Classic, "Block": Block, "gutenberg": Block} {
		if got, err := ParseKind(in); err != nil || got != want {
			t.Errorf("ParseKind(%q) = %q %v", in, got, err)
		}
	}
	if _, err := ParseKind("hybrid"); err == nil {
		t.Error("hybrid")
	}
	if Block.Starter() != "gutenberg" || Classic.Starter() != "theme" {
		t.Error("starters")
	}
}

func TestPassword(t *testing.T) {
	a, _ := Password(20)
	b, _ := Password(20)
	if len(a) != 20 || a == b {
		t.Errorf("%q %q", a, b)
	}
	for _, c := range a + b {
		if !strings.ContainsRune(passwordAlphabet, c) || strings.ContainsRune("0O1lI", c) {
			t.Errorf("character %q", c)
		}
	}
}

func TestCheckRefusesCollisions(t *testing.T) {
	p := fixture(t)
	r, _ := Normalize(p, request())
	if err := Check(p, r); err != nil {
		t.Fatalf("free: %v", err)
	}
	for _, edit := range []func(*Request){
		func(r *Request) { r.Name = "old site" },
		func(r *Request) { r.Domain = "old-site.local" },
	} {
		in := r
		edit(&in)
		if err := Check(p, in); err == nil {
			t.Errorf("%+v: no error", in)
		}
	}
	_ = os.MkdirAll(SitePath(p, "acme-shop"), 0o755)
	if err := Check(p, r); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("folder: %v", err)
	}
}

// fakeAPI creates the site in sites.json (and its socket) when the job ends.
type fakeAPI struct {
	t    *testing.T
	p    paths.Paths
	got  local.NewSite
	fail error
}

func (f *fakeAPI) AddSite(_ context.Context, s local.NewSite) (local.Job, error) {
	f.got = s
	return local.Job{ID: "job1", Status: "running"}, nil
}

func (f *fakeAPI) WaitJob(_ context.Context, _ string, _ time.Duration, progress func(local.Job)) (local.Job, error) {
	progress(local.Job{Status: "running"})
	if f.fail != nil {
		return local.Job{Status: "failed"}, f.fail
	}
	write(f.t, filepath.Join(f.p.LocalSupport, "sites.json"), `{"new1":{"id":"new1","name":"Acme Shop","path":"`+f.got.Path+`","domain":"acme-shop.local",
		"services":{"php":{"version":"8.2.29"},"nginx":{"version":"1.26.1"}}}}`)
	write(f.t, filepath.Join(f.p.LocalSupport, "site-statuses.json"), `{"new1":"running"}`)
	write(f.t, local.SocketPath(f.p, "new1"), "")
	progress(local.Job{Status: "successful"})
	return local.Job{Status: "successful"}, nil
}

// fakeTools plays composer (creates the theme), npm, wp-cli and git.
func fakeTools(t *testing.T, gitIdentity bool) *exec.FakeRunner {
	return &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case strings.Contains(args, "create-project taw/create"):
			dir := filepath.Join(s.Dir, s.Args[3])
			write(t, filepath.Join(dir, "composer.json"), `{"name":"taw/theme","require":{"taw/core":"^1.76"}}`)
			write(t, filepath.Join(dir, "vendor", "composer", "installed.json"), `{"packages":[{"name":"taw/core","version":"v1.76.1"}]}`)
			write(t, filepath.Join(dir, "package.json"), `{"scripts":{"build":"vite build"}}`)
			_ = os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755)
			if slices.Contains(s.Env, "TAW_STARTER=gutenberg") { // taw-gutenberg's installer builds
				write(t, filepath.Join(dir, "dist", ".vite", "manifest.json"), "{}")
			}
			_, _ = s.Stdout.Write([]byte("Installing taw/theme\n"))
		case s.Name == "git" && args == "config user.email":
			if !gitIdentity {
				return exec.Result{Code: 1}, nil
			}
			return exec.Result{Stdout: []byte("me@example.test\n")}, nil
		case s.Name == "git" && args == "rev-parse --short HEAD":
			return exec.Result{Stdout: []byte("abc1234\n")}, nil
		}
		return exec.Result{}, nil
	}}
}

func TestRunCreatesEverything(t *testing.T) {
	p := fixture(t)
	r, err := Normalize(p, request())
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{t: t, p: p}
	runner := fakeTools(t, true)
	c := &Creator{Paths: p, Exec: runner, Local: api, Poll: time.Millisecond}
	var out strings.Builder
	res, err := c.Run(context.Background(), r, &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if api.got.Path != filepath.Join(p.Home, "Local Sites", "acme-shop") || api.got.Domain != "acme-shop.local" || api.got.AdminPassword != "pw-given" {
		t.Errorf("addSite = %+v", api.got)
	}
	themePath := filepath.Join(p.Home, "Local Sites", "acme-shop", "app", "public", "wp-content", "themes", "acme-shop")
	if res.SiteID != "new1" || res.ThemePath != themePath || res.TawCore != "v1.76.1" || res.Commit != "abc1234" || len(res.Warnings) != 0 ||
		res.AdminURL != "http://acme-shop.local/wp-admin/" || res.AdminPassword != "pw-given" {
		t.Errorf("res = %+v", res)
	}

	calls := runner.Calls()
	var names []string
	for _, c := range calls {
		names = append(names, filepath.Base(c.Name)+" "+strings.Join(c.Args, " "))
	}
	php := calls[0]
	if !strings.HasSuffix(php.Name, "/php") || php.Args[1] != "create-project" || php.Args[3] != "acme-shop" ||
		!slices.Contains(php.Env, "TAW_STARTER=theme") || php.Dir != filepath.Dir(themePath) {
		t.Errorf("composer = %+v", php)
	}
	wantOrder := []string{"npm run build", "theme activate acme-shop", "git init -b main", "git config user.email", "git add -A",
		"git commit -q -m Start Acme Shop from taw-theme (taw/core 1.76.1)", "git rev-parse --short HEAD"}
	joined := strings.Join(names, "\n")
	last := -1
	for _, w := range wantOrder {
		i := strings.Index(joined, w)
		if i < 0 || i < last {
			t.Errorf("%q missing or out of order in\n%s", w, joined)
		}
		last = i
	}
	if strings.Contains(joined, "npm install") {
		t.Error("node_modules exists: no npm install")
	}
	for _, want := range []string{"Creating the Local site acme-shop.local (Local's preferred PHP and web server)", "Local: running", "✓ Site created and running", "taw-theme installed", "front-end assets built", "acme-shop is the active theme", "first commit (abc1234)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "pw-given") {
		t.Error("the password must not be in the progress output")
	}
}

func TestRunBlockWithoutGitIdentity(t *testing.T) {
	p := fixture(t)
	in := request()
	in.Kind = Block
	r, _ := Normalize(p, in)
	runner := fakeTools(t, false)
	c := &Creator{Paths: p, Exec: runner, Local: &fakeAPI{t: t, p: p}, Poll: time.Millisecond}
	var out strings.Builder
	res, err := c.Run(context.Background(), r, &out)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range runner.Calls() {
		if strings.Contains(c.Name, "npm") {
			t.Errorf("assets already built, yet %s %v ran", c.Name, c.Args)
		}
	}
	if !strings.Contains(out.String(), "already built by the installer") {
		t.Errorf("output:\n%s", out.String())
	}
	if !slices.Contains(runner.Calls()[0].Env, "TAW_STARTER=gutenberg") {
		t.Error("block → gutenberg starter")
	}
	if res.Commit != "" || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no identity") {
		t.Errorf("res = %+v", res)
	}
}

func TestRunStopsWhenLocalFails(t *testing.T) {
	p := fixture(t)
	r, _ := Normalize(p, request())
	runner := fakeTools(t, true)
	c := &Creator{Paths: p, Exec: runner, Local: &fakeAPI{t: t, p: p, fail: errors.New("local couldn't create the site: Port 10010 is in use")}, Poll: time.Millisecond}
	_, err := c.Run(context.Background(), r, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "Port 10010") || !strings.Contains(err.Error(), "Local Sites/acme-shop") {
		t.Errorf("err = %v", err)
	}
	if len(runner.Calls()) != 0 {
		t.Error("nothing runs after Local fails")
	}
}

func TestRunStopsWhenTheThemeFails(t *testing.T) {
	p := fixture(t)
	r, _ := Normalize(p, request())
	runner := &exec.FakeRunner{Script: func(exec.Spec) (exec.Result, error) { return exec.Result{Code: 1}, nil }}
	c := &Creator{Paths: p, Exec: runner, Local: &fakeAPI{t: t, p: p}, Poll: time.Millisecond}
	_, err := c.Run(context.Background(), r, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "installing the theme") || !strings.Contains(err.Error(), "exists and runs") {
		t.Errorf("err = %v", err)
	}
	if n := len(runner.Calls()); n != 1 {
		t.Errorf("%d calls after a failed install", n)
	}
}
