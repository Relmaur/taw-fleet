package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkills(t *testing.T) {
	var names []string
	for _, s := range Skills() {
		names = append(names, s.Name)
		if s.Description == "" || !strings.Contains(s.Description, "Triggers on:") {
			t.Errorf("%s: description %q", s.Name, s.Description)
		}
	}
	if strings.Join(names, " ") != "fleet-check new-site onboard-mac update-fleet" {
		t.Errorf("skills: %v", names)
	}
}

func TestWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "TAW Fleet")
	f := Folder{Dir: dir, Exe: "/opt/homebrew/bin/taw-fleet", Version: "1.24.0", Settings: "/Users/me/.config/taw-fleet/config.toml", Home: "/Users/me"}
	mustWrite := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Already there: someone's MCP server, their notes, their own skill,
	// a report, and a fleet skill taw-fleet no longer ships.
	mustWrite(".mcp.json", `{"mcpServers":{"figma":{"command":"figma-mcp"}}}`)
	mustWrite("CLAUDE.local.md", "my notes")
	mustWrite(".claude/skills/mine/SKILL.md", "---\nname: mine\ndescription: x\n---\n")
	mustWrite(".claude/skills/old-one/SKILL.md", "---\nname: old-one\nowner: taw-fleet\n---\n")
	mustWrite("reports/acme/2026-09.md", "report")

	r, err := f.Write([]Site{{Slug: "acme", Production: "https://acme.mx", BugSmash: "p-1", Language: "es", Notes: "Deploys | from main."}})
	if err != nil {
		t.Fatal(err)
	}
	wrote := strings.Join(r.Wrote, " ")
	for _, want := range []string{"CLAUDE.md", ".mcp.json", ".claude/skills/fleet-check/SKILL.md", ".claude/skills/update-fleet/SKILL.md", ".claude/skills/onboard-mac/SKILL.md", ".claude/skills/new-site/review-template.html"} {
		if !strings.Contains(wrote, want) {
			t.Errorf("didn't write %s: %v", want, r.Wrote)
		}
	}
	if strings.Join(r.Removed, " ") != ".claude/skills/old-one" {
		t.Errorf("removed %v", r.Removed)
	}
	for _, keep := range []string{"CLAUDE.local.md", ".claude/skills/mine/SKILL.md", "reports/acme/2026-09.md"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s is gone", keep)
		}
	}

	var mcp struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err := json.Unmarshal(b, &mcp); err != nil || mcp.Servers["figma"].Command != "figma-mcp" ||
		mcp.Servers["taw-fleet"].Command != "/opt/homebrew/bin/taw-fleet" || strings.Join(mcp.Servers["taw-fleet"].Args, " ") != "mcp" {
		t.Errorf(".mcp.json: %s", b)
	}

	md, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	for _, want := range []string{"Written by taw-fleet 1.24.0", "CLAUDE.local.md", "Ask the person that question",
		"| acme | https://acme.mx | p-1 | es | Deploys / from main. |", "- **fleet-check**: The morning check",
		"`~/.config/taw-fleet/config.toml`", "never copy, move or read them"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("CLAUDE.md lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(string(md), "Triggers on") || strings.Contains(string(md), "Don't ask it in the chat") {
		t.Errorf("CLAUDE.md:\n%s", md)
	}

	// Again: nothing changes.
	r, err = f.Write([]Site{{Slug: "acme", Production: "https://acme.mx", BugSmash: "p-1", Language: "es", Notes: "Deploys | from main."}})
	if err != nil || len(r.Wrote) != 0 || len(r.Removed) != 0 {
		t.Errorf("second write: %+v %v", r, err)
	}

	// A broken .mcp.json is reported, not replaced.
	mustWrite(".mcp.json", "{")
	if _, err := f.Write(nil); err == nil || !strings.Contains(err.Error(), "isn't valid JSON") {
		t.Errorf("broken .mcp.json: %v", err)
	}
}
