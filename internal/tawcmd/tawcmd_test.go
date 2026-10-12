package tawcmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
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

// theme makes a TAW theme: composer.json requiring taw/core, vendor/bin/taw.
func theme(t *testing.T, dir string) string {
	t.Helper()
	write(t, filepath.Join(dir, "composer.json"), `{"name":"acme/theme","require":{"taw/core":"^1.93"}}`)
	write(t, filepath.Join(dir, "vendor", "bin", "taw"), "<?php\n")
	return dir
}

type fixture struct {
	env   Env
	err   *bytes.Buffer
	sites []site.Site
	wd    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	p := paths.ForHome(home, nil)
	p.LookPath = func(name string) (string, error) {
		if name == "php" {
			return "/usr/local/bin/php", nil
		}
		return "", errors.New("not found")
	}
	f := &fixture{err: &bytes.Buffer{}}
	f.env = Env{
		Paths: p,
		Getwd: func() (string, error) { return f.wd, nil },
		Sites: func(context.Context) ([]site.Site, error) { return f.sites, nil },
		Err:   f.err,
	}
	return f
}

// localPHP installs a fake PHP from Local for the version.
func localPHP(t *testing.T, p paths.Paths, version string) string {
	t.Helper()
	var bin string
	for _, plat := range []string{"darwin-arm64", "darwin", "darwin-x64"} {
		bin = filepath.Join(p.LocalSupport, "lightning-services", "php-"+version+"+1", "bin", plat, "bin", "php")
		write(t, bin, "")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(bin)))
}

func TestInAThemeFolderOutsideLocal(t *testing.T) {
	f := newFixture(t)
	root := theme(t, filepath.Join(t.TempDir(), "acme"))
	f.wd = filepath.Join(root, "Blocks", "Hero")
	write(t, filepath.Join(f.wd, "Hero.php"), "")

	cmd, err := Resolve(context.Background(), f.env, []string{"make:block", "Hero", "--type=meta"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"php", filepath.Join(root, "vendor", "bin", "taw"), "make:block", "Hero", "--type=meta"}
	if cmd.Path != "/usr/local/bin/php" || strings.Join(cmd.Args, " ") != strings.Join(want, " ") || cmd.Dir != f.wd || len(cmd.Env) != 0 || cmd.Site != "" {
		t.Errorf("%+v", cmd)
	}
}

func TestALocalSiteGetsItsPHPAndSocket(t *testing.T) {
	f := newFixture(t)
	root := theme(t, filepath.Join(t.TempDir(), "ls-mexico"))
	php := localPHP(t, f.env.Paths, "8.2.30")
	f.sites = []site.Site{{ID: "x1", Slug: "ls-mxico", PHPVersion: "8.2.30", Socket: "/run/x1/mysqld.sock",
		Themes: []site.Theme{{Dir: "ls-mexico", RealPath: root, IsTAW: true}}}}
	f.wd = root

	cmd, err := Resolve(context.Background(), f.env, []string{"wp", "option", "get", "home"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cmd.Path, php) || cmd.Site != "ls-mxico" {
		t.Errorf("the site's PHP: %s (site %q)", cmd.Path, cmd.Site)
	}
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "-d mysqli.default_socket=/run/x1/mysqld.sock -d pdo_mysql.default_socket=/run/x1/mysqld.sock") ||
		!strings.HasSuffix(args, "vendor/bin/taw wp option get home") || strings.Join(cmd.Env, " ") != "MYSQL_UNIX_PORT=/run/x1/mysqld.sock" {
		t.Errorf("socket: %s %v", args, cmd.Env)
	}
	if !strings.Contains(f.err.String(), "ls-mxico is halted") || !strings.Contains(f.err.String(), "taw-fleet start ls-mxico") {
		t.Errorf("a halted site gets one hint: %q", f.err.String())
	}

	f.err.Reset()
	f.sites[0].SockLive = true
	if _, err := Resolve(context.Background(), f.env, nil); err != nil || f.err.Len() != 0 {
		t.Errorf("a running site: no hint (%v, %q)", err, f.err.String())
	}
}

func TestASymlinkedThemeMatchesByRealPath(t *testing.T) {
	f := newFixture(t)
	target := theme(t, filepath.Join(t.TempDir(), "umbrella", "taw-theme"))
	link := filepath.Join(t.TempDir(), "themes", "taw-theme")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	f.sites = []site.Site{{Slug: "taw", Socket: "/s", SockLive: true, Themes: []site.Theme{{Dir: "taw-theme", Path: link, RealPath: target, IsTAW: true}}}}

	f.wd = target // the umbrella's checkout
	if cmd, err := Resolve(context.Background(), f.env, nil); err != nil || cmd.Site != "taw" {
		t.Errorf("from the real folder: %v %+v", err, cmd)
	}
	f.wd = link // through the Local site's link
	if cmd, err := Resolve(context.Background(), f.env, nil); err != nil || cmd.Site != "taw" {
		t.Errorf("through the link: %v %+v", err, cmd)
	}
}

func TestAtSite(t *testing.T) {
	f := newFixture(t)
	a := theme(t, filepath.Join(t.TempDir(), "chcapital"))
	g := theme(t, filepath.Join(t.TempDir(), "taw-gutenberg"))
	c := theme(t, filepath.Join(t.TempDir(), "taw-theme"))
	f.sites = []site.Site{
		{ID: "1", Slug: "ch-capital---taw", Socket: "/s1", SockLive: true, Themes: []site.Theme{{Dir: "chcapital", RealPath: a, IsTAW: true}}},
		{ID: "2", Slug: "taw", Socket: "/s2", SockLive: true, Themes: []site.Theme{{Dir: "taw-gutenberg", RealPath: g, IsTAW: true}, {Dir: "taw-theme", RealPath: c, IsTAW: true}}},
	}
	f.wd = "/"
	for _, tc := range []struct{ arg, dir string }{
		{"@ch-capital---taw", a}, {"@chcapital", a}, {"@taw-theme", c}, {"@taw/taw-gutenberg", g},
	} {
		cmd, err := Resolve(context.Background(), f.env, []string{tc.arg, "list"})
		if err != nil || cmd.Dir != tc.dir || cmd.Args[len(cmd.Args)-1] != "list" || cmd.Args[len(cmd.Args)-2] != filepath.Join(tc.dir, "vendor", "bin", "taw") {
			t.Errorf("%s: %v %+v", tc.arg, err, cmd)
		}
	}
	if _, err := Resolve(context.Background(), f.env, []string{"@taw", "list"}); err == nil || !strings.Contains(err.Error(), "use @taw/<theme>") {
		t.Errorf("two themes: %v", err)
	}
}

func TestNotInATheme(t *testing.T) {
	f := newFixture(t)
	a := theme(t, filepath.Join(t.TempDir(), "chcapital"))
	f.sites = []site.Site{
		{Slug: "ch-capital---taw", Themes: []site.Theme{{Dir: "chcapital", RealPath: a, IsTAW: true}}},
		{Slug: "taw", Themes: []site.Theme{{Dir: "taw-gutenberg", IsTAW: true}, {Dir: "twentytwentyfive"}}},
	}
	f.wd = t.TempDir()
	if _, err := Resolve(context.Background(), f.env, nil); !errors.Is(err, ErrNotInTheme) {
		t.Fatalf("err = %v", err)
	}
	out := f.err.String()
	for _, want := range []string{"not in a TAW theme", "taw @chcapital", "ch-capital---taw", "taw @taw-gutenberg", "For example: taw @chcapital list"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "twentytwentyfive") {
		t.Error("only TAW themes are listed")
	}
}

func TestOlderThemesAndMissingVendor(t *testing.T) {
	f := newFixture(t)

	// taw/core before 1.90: bin/taw in the theme, vendor/ without bin/taw.
	old := filepath.Join(t.TempDir(), "old")
	write(t, filepath.Join(old, "composer.json"), `{"require":{"taw/core":"^1.80"}}`)
	write(t, filepath.Join(old, "bin", "taw"), "<?php\n")
	write(t, filepath.Join(old, "vendor", "autoload.php"), "<?php\n")
	f.wd = filepath.Join(old, "inc")
	write(t, filepath.Join(f.wd, "x.php"), "")
	cmd, err := Resolve(context.Background(), f.env, []string{"list"})
	if err != nil || cmd.Args[1] != filepath.Join(old, "bin", "taw") || cmd.Dir != old {
		t.Errorf("pre-1.90: %v %+v", err, cmd)
	}

	// No vendor/ yet.
	fresh := filepath.Join(t.TempDir(), "fresh")
	write(t, filepath.Join(fresh, "composer.json"), `{"require":{"taw/core":"^1.93"}}`)
	f.wd = fresh
	if _, err := Resolve(context.Background(), f.env, nil); err == nil || !strings.Contains(err.Error(), "run composer install") {
		t.Errorf("no vendor/: %v", err)
	}

	// taw/core's own package (it has a bin/taw too) is never taken for a theme.
	core := theme(t, filepath.Join(t.TempDir(), "acme"))
	pkg := filepath.Join(core, "vendor", "taw", "core")
	write(t, filepath.Join(pkg, "composer.json"), `{"name":"taw/core"}`)
	write(t, filepath.Join(pkg, "bin", "taw"), "<?php\n")
	f.wd = filepath.Join(pkg, "src")
	write(t, filepath.Join(f.wd, "A.php"), "")
	if cmd, err := Resolve(context.Background(), f.env, nil); err != nil || cmd.Args[1] != filepath.Join(core, "vendor", "bin", "taw") {
		t.Errorf("inside vendor/taw/core: %v %+v", err, cmd)
	}
}

func TestLineIsAShellLine(t *testing.T) {
	c := Command{Path: "/App Support/php", Args: []string{"php", "-d", "x=/a b/s.sock", "it's"}, Env: []string{"MYSQL_UNIX_PORT=/a b/s.sock"}}
	want := `MYSQL_UNIX_PORT='/a b/s.sock' '/App Support/php' -d 'x=/a b/s.sock' 'it'\''s'`
	if got := c.Line(); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
