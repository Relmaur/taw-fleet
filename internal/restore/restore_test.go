package restore

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

const dump = "-- MySQL dump\nCREATE TABLE wp_options (option_name varchar(191));\nINSERT INTO wp_options VALUES ('siteurl');\n"

type fixture struct {
	p        paths.Paths
	s        site.Site
	t        site.Theme
	r        *exec.FakeRunner
	imported string // what wp db import read
	branch   string
	head     string
	dirty    bool
	inRepo   string // what git rev-parse --show-toplevel answers for the store ("" = no repo)
	failDump bool
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	p := paths.ForHome(home, func(k string) string {
		if k == "PATH" {
			return "/usr/bin:/bin"
		}
		return ""
	})
	p.LocalApp = filepath.Join(home, "Local.app")
	plat := "darwin"
	if runtime.GOARCH == "arm64" {
		plat = "darwin-arm64"
	}
	ls := filepath.Join(p.LocalSupport, "lightning-services")
	touch(t, filepath.Join(ls, "php-8.2.30+1", "bin", plat, "bin", "php"))
	touch(t, filepath.Join(ls, "mysql-8.4.0+2", "bin", plat, "bin", "mysqldump"))
	touch(t, filepath.Join(p.LocalApp, "Contents", "Resources", "extraResources", "bin", "wp-cli", "wp-cli.phar"))
	sitePath := filepath.Join(home, "Local Sites", "acme")
	theme := filepath.Join(sitePath, "app", "public", "wp-content", "themes", "acme-theme")
	if err := os.MkdirAll(theme, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fixture{p: p, branch: "main", head: "1111111aaaa"}
	f.s = site.Site{Slug: "acme", Path: sitePath, WebRoot: filepath.Join(sitePath, "app", "public"), SockLive: true,
		Socket: "/tmp/mysqld.sock", PHPVersion: "8.2.30", MySQLVersion: "8.4.0"}
	f.t = site.Theme{Dir: "acme-theme", Path: theme, RealPath: theme, IsTAW: true, Git: &site.GitInfo{Branch: "main"}}
	f.s.Themes = []site.Theme{f.t}
	f.r = &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "git" && strings.Contains(args, "--show-toplevel"):
			if f.inRepo != "" {
				return exec.Result{Stdout: []byte(f.inRepo + "\n")}, nil
			}
			return exec.Result{Code: 128, Stderr: []byte("fatal: not a git repository")}, nil
		case s.Name == "git" && strings.HasSuffix(args, "rev-parse HEAD"):
			return exec.Result{Stdout: []byte(f.head + "\n")}, nil
		case s.Name == "git" && strings.Contains(args, "symbolic-ref"):
			return exec.Result{Stdout: []byte(f.branch + "\n")}, nil
		case s.Name == "git" && strings.Contains(args, "rev-parse main"):
			return exec.Result{Stdout: []byte("1111111aaaa\n")}, nil
		case s.Name == "git" && strings.Contains(args, "status --porcelain"):
			if f.dirty {
				return exec.Result{Stdout: []byte(" M style.css\n")}, nil
			}
			return exec.Result{}, nil
		case s.Name == "git" && strings.Contains(args, "switch"):
			f.branch = s.Args[len(s.Args)-1]
			return exec.Result{}, nil
		case strings.Contains(args, "db export -"):
			if f.failDump {
				_, _ = io.WriteString(s.Stderr, "mysqldump: Got error: 2002\n")
				return exec.Result{Code: 1}, nil
			}
			_, _ = io.WriteString(s.Stdout, dump)
			return exec.Result{}, nil
		case strings.Contains(args, "db import -"):
			b, _ := io.ReadAll(s.Stdin)
			f.imported = string(b)
			return exec.Result{}, nil
		}
		return exec.Result{}, nil
	}}
	return f
}

func (f *fixture) store(keep int) Store {
	n := 0
	return Store{Paths: f.p, Runner: f.r, Keep: keep, Now: func() time.Time {
		n++
		return time.Date(2026, 10, 12, 18, 0, n, 0, time.UTC)
	}}
}

func TestSaveKeepsTheDatabaseOnlyInItsPrivateFolder(t *testing.T) {
	f := newFixture(t)
	st := f.store(5)
	p, err := st.Save(context.Background(), f.s, f.t, BeforeUpdate, "before taw/core 1.93.3")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Database || p.Branch != "main" || p.Commit != "1111111aaaa" || p.ID != "20261012-180001-update" {
		t.Fatalf("point = %+v", p)
	}
	// The copy is gzip, private, and the only database file anywhere.
	file := filepath.Join(p.Dir, dbFile)
	fi, err := os.Stat(file)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("copy: %v %v", fi, err)
	}
	for _, d := range []string{st.Root(), st.siteDir("acme"), p.Dir} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, fi, err)
		}
	}
	zf, _ := os.Open(file)
	zr, err := gzip.NewReader(zf)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(zr)
	_ = zf.Close()
	if string(b) != dump {
		t.Errorf("copy = %q", b)
	}
	if g, _ := os.ReadFile(filepath.Join(st.Root(), ".gitignore")); !strings.Contains(string(g), "*") {
		t.Error("no .gitignore in the restore folder")
	}
	var sql []string
	_ = filepath.WalkDir(f.p.Home, func(path string, d fs.DirEntry, _ error) error {
		if d != nil && !d.IsDir() && (strings.Contains(path, ".sql") || strings.HasSuffix(path, ".partial")) {
			sql = append(sql, path)
		}
		return nil
	})
	if len(sql) != 1 || sql[0] != file {
		t.Errorf("database files on disk: %v", sql)
	}
	// wp db export wrote to stdout, with Local's own mysqldump first on the PATH.
	for _, c := range f.r.Calls() {
		if strings.Contains(strings.Join(c.Args, " "), "db export") {
			if c.Args[len(c.Args)-4] != "-" || c.Stdout == nil {
				t.Errorf("export args %v", c.Args)
			}
			if !strings.Contains(strings.Join(c.Env, " "), "PATH="+filepath.Join(f.p.LocalSupport, "lightning-services", "mysql-8.4.0+2")) {
				t.Errorf("export env %v", c.Env)
			}
		}
	}
}

func TestGuardRefusesRepositoriesAndSites(t *testing.T) {
	f := newFixture(t)
	st := f.store(5)
	ctx := context.Background()

	f.inRepo = f.p.Home // a dotfiles repository at ~
	if _, err := st.Save(ctx, f.s, f.t, BeforeUpdate, ""); !errors.Is(err, ErrUnsafe) || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("in a repo: %v", err)
	}
	if !strings.Contains(strings.Join(f.r.Calls()[0].Unset, " "), "GIT_DIR") {
		t.Error("the git check must ignore GIT_DIR")
	}
	f.inRepo = ""
	for _, dir := range []string{f.s.Path, f.t.Path, filepath.Join(f.t.Path, ".taw", "restore"), "relative/dir"} {
		if err := st.Guard(ctx, dir, f.s); !errors.Is(err, ErrUnsafe) {
			t.Errorf("%s: %v", dir, err)
		}
	}
	// A theme symlinked from elsewhere (the umbrella's): inside its real
	// folder is refused too.
	elsewhere := filepath.Join(t.TempDir(), "taw-theme")
	f.s.Themes = append(f.s.Themes, site.Theme{Dir: "taw-theme", Path: filepath.Join(f.s.WebRoot, "wp-content", "themes", "taw-theme"), RealPath: elsewhere})
	if err := st.Guard(ctx, filepath.Join(elsewhere, "restore"), f.s); !errors.Is(err, ErrUnsafe) {
		t.Errorf("inside the symlinked theme: %v", err)
	}
	if err := st.Guard(ctx, st.Root(), f.s); err != nil {
		t.Errorf("the store itself: %v", err)
	}
	// Nothing was saved by the refused attempt.
	if points, _ := st.List("acme"); len(points) != 0 {
		t.Errorf("points = %v", points)
	}
}

func TestStoppedSiteAndFailedDump(t *testing.T) {
	f := newFixture(t)
	st := f.store(5)
	f.s.SockLive = false
	p, err := st.Save(context.Background(), f.s, f.t, BeforeUpdate, "")
	if err != nil || p.Database || p.NoDB != "the site was stopped" || p.Commit == "" {
		t.Fatalf("stopped: %+v %v", p, err)
	}
	f.s.SockLive, f.failDump = true, true
	if _, err := st.Save(context.Background(), f.s, f.t, BeforeUpdate, ""); err == nil || !strings.Contains(err.Error(), "2002") {
		t.Fatalf("failed dump: %v", err)
	}
	points, _ := st.List("acme")
	if len(points) != 1 {
		t.Errorf("a failed copy left a point: %d", len(points))
	}
	entries, _ := os.ReadDir(st.siteDir("acme"))
	if len(entries) != 1 {
		t.Errorf("a failed copy left files: %v", entries)
	}
}

func TestKeepsTheNewest(t *testing.T) {
	f := newFixture(t)
	st := f.store(3)
	var ids []string
	for range 5 {
		p, err := st.Save(context.Background(), f.s, f.t, BeforeUpdate, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	points, _ := st.List("acme")
	if len(points) != 3 || points[0].ID != ids[4] || points[2].ID != ids[2] {
		t.Errorf("kept %v", points)
	}
	if p, _ := (Store{Paths: f.p, Runner: f.r}).Save(context.Background(), f.s, f.t, BeforeUpdate, ""); p.ID != "" {
		t.Error("restore_keep = 0 must save nothing")
	}
}

func TestRestore(t *testing.T) {
	f := newFixture(t)
	st := f.store(5)
	ctx := context.Background()
	p, err := st.Save(ctx, f.s, f.t, BeforeUpdate, "")
	if err != nil {
		t.Fatal(err)
	}
	// The update moved the theme onto its branch.
	f.branch, f.head = "taw/update-20261012", "2222222bbbb"

	f.dirty = true
	if _, err := st.Restore(ctx, f.s, f.t, p, io.Discard); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("dirty: %v", err)
	}
	f.dirty = false
	f.s.SockLive = false
	if _, err := st.Restore(ctx, f.s, f.t, p, io.Discard); err == nil || !strings.Contains(err.Error(), "start it first") {
		t.Fatalf("stopped: %v", err)
	}
	f.s.SockLive = true

	pl, err := st.Check(ctx, f.s, f.t, p)
	if err != nil || pl.Switch != "main" || pl.Moved || !pl.Database {
		t.Fatalf("plan = %+v %v", pl, err)
	}
	var out strings.Builder
	before, err := st.Restore(ctx, f.s, f.t, p, &out)
	if err != nil {
		t.Fatal(err)
	}
	if f.branch != "main" || f.imported != dump || before.Reason != BeforeRestore || before.Branch != "taw/update-20261012" {
		t.Errorf("branch=%s imported=%q before=%+v", f.branch, f.imported, before)
	}
	if !strings.Contains(out.String(), "is on main again") || !strings.Contains(out.String(), "database is back") {
		t.Errorf("out:\n%s", out.String())
	}
	// Both are still there: the one put back, and how it was before.
	if points, _ := st.List("acme"); len(points) != 2 {
		t.Errorf("points = %d", len(points))
	}
	if got, err := st.Find("acme", "latest"); err != nil || got.ID != before.ID {
		t.Errorf("latest = %v %v", got.ID, err)
	}
	if _, err := st.Find("acme", "nope"); err == nil {
		t.Error("found a missing point")
	}
}
