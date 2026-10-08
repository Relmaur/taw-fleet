package taw

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// syncJSON is real `bin/taw sync --json` output from a client theme,
// trimmed (chcapital, 2026-10-08).
const syncJSON = `{"taw_core":{"installed":"v1.59.2","latest":"v1.76.1","behind":true,"error":null},
"tier1":[{"path":"functions.php","type":"file","changed":false},
 {"path":"bin/","type":"dir","changed":true},
 {"path":".claude/skills/","type":"skills-dir","changed":true,"reconcile":{"overwrite":["update-theme"],"delete":[],"preserve":["publish-news"],"warn":[],"clash":[]}}],
"tier2":[{"path":"AGENTS.md","type":"file","changed":false},{"path":"composer.json","type":"file","changed":true,"diff":"-a\n+b"}],
"applied":[],"errors":[],"clean":false}`

func classic(dir string) site.Theme {
	return site.Theme{Dir: "chcapital", RealPath: dir, IsTAW: true, Kind: site.KindClassic, HasBinTaw: true,
		Core: site.CoreInfo{Installed: "v1.59.2", Latest: "v1.76.1", Behind: true},
		Git:  &site.GitInfo{Repo: &site.Repo{Host: "github.com", Owner: "Relmaur", Name: "chcapital--theme"}}}
}

func TestParseSync(t *testing.T) {
	rep, err := ParseSync([]byte("Deprecated: something noisy\n" + syncJSON))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.TawCore.Behind || rep.TawCore.Latest != "v1.76.1" || rep.Clean {
		t.Errorf("rep = %+v", rep.TawCore)
	}
	if got := Changed(rep.Tier1); !reflect.DeepEqual(got, []string{"bin/", ".claude/skills/"}) {
		t.Errorf("tier1 changed = %v", got)
	}
	if rep.Tier1[2].Reconcile == nil || rep.Tier1[2].Reconcile.Preserve[0] != "publish-news" {
		t.Errorf("reconcile = %+v", rep.Tier1[2].Reconcile)
	}
	at := time.Date(2026, 10, 8, 16, 0, 0, 0, time.UTC)
	d := rep.Drift(at)
	if len(d.Tier1) != 2 || len(d.Tier2) != 1 || !d.At.Equal(at) {
		t.Errorf("drift = %+v", d)
	}
	if _, err := ParseSync([]byte("PHP Fatal error: nope")); err == nil {
		t.Error("no JSON")
	}
	if _, err := ParseSync([]byte("{broken")); err == nil {
		t.Error("broken JSON")
	}
}

func TestGuard(t *testing.T) {
	th := classic(t.TempDir())
	if err := Guard(th, true); err != nil {
		t.Errorf("client classic: %v", err)
	}
	block := th
	block.Kind = site.KindGutenberg
	if err := Guard(block, true); !errors.Is(err, ErrNoSync) {
		t.Errorf("block sync: %v", err)
	}
	if err := Guard(block, false); err != nil {
		t.Errorf("block update is fine: %v", err)
	}
	umbrella := th
	umbrella.Git = &site.GitInfo{Repo: &site.Repo{Owner: "Relmaur", Name: "taw-theme"}}
	if err := Guard(umbrella, false); !errors.Is(err, ErrUmbrella) {
		t.Errorf("umbrella: %v", err)
	}
	if err := Guard(site.Theme{}, false); err == nil {
		t.Error("not TAW")
	}
}

func TestSyncRunsWithSitePHPAndStreams(t *testing.T) {
	f := &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		_, _ = s.Stderr.Write([]byte("Cloning taw-theme…\n"))
		return exec.Result{Stdout: []byte(syncJSON)}, nil
	}}
	var progress strings.Builder
	r := Runner{Exec: f, PHP: "/L/php"}
	dir := t.TempDir()
	rep, err := r.Sync(context.Background(), classic(dir), true, &progress)
	if err != nil || !rep.TawCore.Behind {
		t.Fatalf("rep=%+v err=%v", rep, err)
	}
	c := f.Calls()[0]
	if c.Name != "/L/php" || c.Dir != dir || !reflect.DeepEqual(c.Args, []string{"bin/taw", "sync", "--json", "--apply"}) {
		t.Errorf("call = %+v", c)
	}
	if progress.String() != "Cloning taw-theme…\n" {
		t.Errorf("progress = %q", progress.String())
	}
}

func TestUpdateCore(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		_, _ = s.Stdout.Write([]byte("Updating dependencies\n"))
		_, _ = s.Stderr.Write([]byte("  - Upgrading taw/core (v1.59.2 => v1.76.1)\n"))
		write("vendor/composer/installed.json", `{"packages":[{"name":"taw/core","version":"v1.76.1"}]}`)
		write("vendor/taw/core/UPGRADING.md", "## Per version\n\n### v1.55.0: old\n### v1.60.0: new thing\n### v1.70.0 – v1.73.0: range\n### v1.80.0: future\n")
		return exec.Result{}, nil
	}}
	var out strings.Builder
	r := Runner{Exec: f, PHP: "/L/php", Composer: "/L/composer.phar"}
	res, err := r.UpdateCore(context.Background(), classic(dir), &out)
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "v1.59.2" || res.To != "v1.76.1" || len(res.Sections) != 2 ||
		res.Sections[0].Heading != "v1.60.0: new thing" || res.Sections[1].Version != "v1.73.0" {
		t.Errorf("res = %+v", res)
	}
	c := f.Calls()[0]
	if c.Name != "/L/php" || !reflect.DeepEqual(c.Args, []string{"/L/composer.phar", "update", "taw/core", "--no-interaction", "--no-progress"}) {
		t.Errorf("call = %+v", c)
	}
	if !strings.Contains(out.String(), "Upgrading taw/core") {
		t.Errorf("out = %q", out.String())
	}

	failing := Runner{Exec: &exec.FakeRunner{Script: func(exec.Spec) (exec.Result, error) { return exec.Result{Code: 2}, nil }}}
	if _, err := failing.UpdateCore(context.Background(), classic(dir), &out); err == nil || !strings.Contains(err.Error(), "exited 2") {
		t.Errorf("failure: %v", err)
	}
}

func TestUpgradeSections(t *testing.T) {
	md := "### v1.24.0: users route\n### v1.33.0 – v1.40.0: forms\n### v1.41.0: tabs\n### How to read this\n### v1.76.1: tabs restyle\n"
	got := UpgradeSections(md, "v1.35.0", "v1.41.0")
	if len(got) != 2 || got[0].Version != "v1.40.0" || got[1].Version != "v1.41.0" {
		t.Errorf("got %+v", got)
	}
	if got := UpgradeSections(md, "v1.76.1", "v1.76.1"); len(got) != 0 {
		t.Errorf("nothing newer: %+v", got)
	}
}

func TestInspectNeedsARunningSite(t *testing.T) {
	r := Runner{Exec: &exec.FakeRunner{}}
	_, err := r.Inspect(context.Background(), site.Site{Slug: "acme"}, classic(t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "taw-fleet start acme") {
		t.Errorf("err = %v", err)
	}
	r.Exec = &exec.FakeRunner{Script: func(exec.Spec) (exec.Result, error) {
		return exec.Result{Stdout: []byte(`{"taw_core_version":"v1.76.1","blocks":[]}`)}, nil
	}}
	raw, err := r.Inspect(context.Background(), site.Site{Slug: "acme", SockLive: true, Socket: "/s"}, classic(t.TempDir()))
	if err != nil || !strings.Contains(string(raw), "v1.76.1") {
		t.Errorf("raw=%s err=%v", raw, err)
	}
}

func TestLineWriter(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	w := NewLineWriter(func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() })
	_, _ = w.Write([]byte("one\ntw"))
	_, _ = w.Write([]byte("o\r\nthree"))
	w.Flush()
	if !reflect.DeepEqual(lines, []string{"one", "two", "three"}) {
		t.Errorf("lines = %q", lines)
	}
}

func TestDriftCache(t *testing.T) {
	dir := t.TempDir()
	if LoadDrift(dir, "s", "t") != nil {
		t.Error("nothing saved yet")
	}
	d := site.Drift{Tier1: []string{"bin/"}, At: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)}
	SaveDrift(dir, "ch-capital---taw", "chcapital", d)
	got := LoadDrift(dir, "ch-capital---taw", "chcapital")
	if got == nil || got.Tier1[0] != "bin/" || !got.At.Equal(d.At) {
		t.Errorf("got %+v", got)
	}
}
