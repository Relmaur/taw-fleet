package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit(t *testing.T) {
	p := fixture(t)
	write(t, filepath.Join(p.ConfigDir, "config.toml"), "[sites.acme]\nproduction_url = \"https://acme.mx\"\n")
	out, err := run(t, p, "init")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"The fleet folder: " + p.FleetDir, "wrote   CLAUDE.md", "wrote   .claude/skills/fleet-check/SKILL.md", "fleet-check", "update-fleet", "onboard-mac"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	md, _ := os.ReadFile(filepath.Join(p.FleetDir, "CLAUDE.md"))
	if !strings.Contains(string(md), "| acme | https://acme.mx |") {
		t.Errorf("CLAUDE.md:\n%s", md)
	}
	out, err = run(t, p, "init")
	if err != nil || !strings.Contains(out, "Already up to date.") {
		t.Errorf("again: %v\n%s", err, out)
	}
}

func TestChatPrompt(t *testing.T) {
	out, err := run(t, fixture(t), "chat", "--prompt")
	if err != nil || !strings.Contains(out, "Scope: the whole fleet") || !strings.Contains(out, "taw-fleet shows the person that question") {
		t.Errorf("%v\n%s", err, out)
	}
	if _, err := run(t, fixture(t), "chat"); err == nil {
		t.Error("chat without --prompt")
	}
}

func TestReady(t *testing.T) {
	p := fixture(t)
	out, err := run(t, p, "--offline", "ready", "acme")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"acme · acme-theme", "Repository", "not a git repository", "Production", "no production URL", "Companion"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	out, err = run(t, p, "--offline", "ready", "--json")
	if err != nil || !strings.Contains(out, `"name": "Repository"`) {
		t.Errorf("%v\n%s", err, out)
	}
}
