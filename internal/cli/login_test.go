package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/login"
)

func TestOpenAtLogin(t *testing.T) {
	p := fixture(t)
	a := login.Agent{Paths: p}
	if out, err := run(t, p, "config", "open-at-login"); err != nil || !strings.Contains(out, "open_at_login: off") {
		t.Errorf("status: %v\n%s", err, out)
	}
	out, err := run(t, p, "config", "open-at-login", "on")
	if err != nil || !strings.Contains(out, "open_at_login = true") {
		t.Fatalf("on: %v\n%s", err, out)
	}
	if cfg, err := config.Load(p); err != nil || !cfg.OpenAtLogin {
		t.Errorf("settings: %+v %v", cfg, err)
	}
	if ok, _ := a.Installed(); !ok {
		t.Error("on must add the agent")
	}
	if out, _ := run(t, p, "config", "open-at-login"); !strings.Contains(out, "open_at_login: on (") {
		t.Errorf("status:\n%s", out)
	}
	if out, _ := run(t, p, "config", "show"); !strings.Contains(out, "at login") {
		t.Errorf("show:\n%s", out)
	}
	if _, err := run(t, p, "config", "open-at-login", "maybe"); err == nil {
		t.Error("maybe must be refused")
	}
	if out, err := run(t, p, "config", "open-at-login", "off"); err != nil || !strings.Contains(out, "open_at_login = false") {
		t.Errorf("off: %v\n%s", err, out)
	}
	if ok, _ := a.Installed(); ok {
		t.Error("off must remove the agent")
	}
	data, _ := os.ReadFile(config.File(p))
	if strings.Count(string(data), "\nopen_at_login = false\n") != 1 || strings.Contains(string(data), "\nopen_at_login = true") {
		t.Errorf("one line, replaced:\n%s", data)
	}
}

func TestAtLogin(t *testing.T) {
	p := fixture(t)
	p.Applications = []string{filepath.Join(p.Home, "Applications")}
	if err := os.MkdirAll(filepath.Join(p.Home, "Applications", "Terminal.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Turned off by hand since: no window, and the agent goes.
	if err := (login.Agent{Paths: p, Exe: "/bin/taw-fleet"}).Install(); err != nil {
		t.Fatal(err)
	}
	r := &exec.FakeRunner{}
	out, err := runWith(t, p, r, "--at-login")
	if err != nil || !strings.Contains(out, "not opening") {
		t.Errorf("off: %v\n%s", err, out)
	}
	if ok, _ := (login.Agent{Paths: p}).Installed(); ok {
		t.Error("off: the agent must go")
	}

	// On: a window, whatever window = says (there's no terminal at login).
	write(t, config.File(p), "open_at_login = true\nwindow = false\n")
	r = &exec.FakeRunner{}
	out, err = runWith(t, p, r, "--at-login", "--offline")
	if err != nil || !strings.Contains(out, "new Terminal window") {
		t.Fatalf("on: %v\n%s", err, out)
	}
	script, _ := os.ReadFile(r.Calls()[0].Args[2])
	if !strings.Contains(string(script), "'--window=false' '--in-window' '--offline'") || strings.Contains(string(script), "at-login") {
		t.Errorf("script:\n%s", script)
	}
}
