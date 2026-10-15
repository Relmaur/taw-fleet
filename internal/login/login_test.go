package login

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
)

func agent(t *testing.T) (Agent, *exec.FakeRunner, string) {
	t.Helper()
	p := paths.ForHome(t.TempDir(), nil)
	exe := filepath.Join(p.Home, "bin", "taw & fleet")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &exec.FakeRunner{}
	return Agent{Paths: p, Runner: r, UID: 501, Exe: exe}, r, exe
}

func TestInstallWritesTheFileWithoutLoadingIt(t *testing.T) {
	a, r, exe := agent(t)
	if err := a.Install(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(a.Plist())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<string>" + Label + "</string>", "taw &amp; fleet", "<string>--at-login</string>", "<key>RunAtLoad</key>"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("plist lacks %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "StartInterval") || strings.Contains(string(data), "KeepAlive") {
		t.Errorf("once at login, never again:\n%s", data)
	}
	if len(r.Calls()) != 0 {
		t.Errorf("loading it now would open a window now: %+v", r.Calls())
	}
	if ok, prog := a.Installed(); !ok || prog != exe {
		t.Errorf("Installed() = %v, %q", ok, prog)
	}
}

func TestUninstall(t *testing.T) {
	a, r, _ := agent(t)
	if err := a.Uninstall(context.Background()); err != nil {
		t.Fatalf("not installed is fine: %v", err)
	}
	if err := a.Install(); err != nil {
		t.Fatal(err)
	}
	if err := a.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.Installed(); ok {
		t.Error("still installed")
	}
	c := r.Calls()[len(r.Calls())-1]
	if c.Name != "/bin/launchctl" || strings.Join(c.Args, " ") != "bootout gui/501/"+Label {
		t.Errorf("call = %+v", c)
	}
}

func TestSync(t *testing.T) {
	a, _, _ := agent(t)
	ctx := context.Background()
	if changed, err := a.Sync(ctx, false); changed || err != nil {
		t.Errorf("off and absent: %v %v", changed, err)
	}
	if changed, err := a.Sync(ctx, true); !changed || err != nil {
		t.Errorf("on: %v %v", changed, err)
	}
	if changed, _ := a.Sync(ctx, true); changed {
		t.Error("on and installed: nothing to do")
	}
	// The taw-fleet it ran is gone (moved, uninstalled): point it at this one.
	moved := a
	moved.Exe = filepath.Join(a.Paths.Home, "new-taw-fleet")
	if err := os.WriteFile(moved.Exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if changed, _ := moved.Sync(ctx, true); changed {
		t.Error("the old one still exists: leave it")
	}
	if err := os.Remove(a.Exe); err != nil {
		t.Fatal(err)
	}
	if changed, _ := moved.Sync(ctx, true); !changed {
		t.Error("the old one is gone: replace it")
	}
	if _, prog := a.Installed(); prog != moved.Exe {
		t.Errorf("program = %q", prog)
	}
	if changed, err := a.Sync(ctx, false); !changed || err != nil {
		t.Errorf("off: %v %v", changed, err)
	}
	if ok, _ := a.Installed(); ok {
		t.Error("off must remove it")
	}
}
