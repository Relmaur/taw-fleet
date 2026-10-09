package actions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/site"
)

func writeSkill(t *testing.T, theme, dir, body string) {
	t.Helper()
	p := filepath.Join(theme, ".claude", "skills", dir, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSkills(t *testing.T) {
	theme := t.TempDir()
	writeSkill(t, theme, "update-theme", "---\nname: update-theme\nowner: taw\ndescription: >\n    Pulls the latest scaffold\n    into this site.\n---\n# body\n")
	writeSkill(t, theme, "publish-news", "---\nname: publish-news\nowner: site\ndescription: \"Publish a parish notice: title, date\"\n---\n")
	writeSkill(t, theme, "bare", "no frontmatter at all\n")
	if err := os.MkdirAll(filepath.Join(theme, ".claude", "skills", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := Skills(site.Theme{RealPath: theme})
	if len(got) != 3 {
		t.Fatalf("skills = %+v", got)
	}
	if got[0].Name != "bare" || got[1].Name != "publish-news" || got[2].Name != "update-theme" {
		t.Errorf("order: %+v", got)
	}
	if got[2].Description != "Pulls the latest scaffold into this site." || got[2].Owner != "taw" {
		t.Errorf("folded description: %+v", got[2])
	}
	if got[1].Description != "Publish a parish notice: title, date" || got[1].Owner != "site" {
		t.Errorf("quoted description: %+v", got[1])
	}
	if Skills(site.Theme{RealPath: filepath.Join(theme, "nope")}) != nil {
		t.Error("no skills folder: none")
	}
	if !HasSkill(site.Theme{RealPath: theme}, "update-theme") || HasSkill(site.Theme{RealPath: theme}, "empty") {
		t.Error("HasSkill")
	}
}

func TestSkillPromptAndLaunch(t *testing.T) {
	a, f := setup(t, config.Config{Sites: map[string]config.Site{"ls-mxico": {ProductionURL: "https://lsmexico.mx"}}})
	mustWrite(t, filepath.Join(a.Paths.Home, ".local", "bin", "claude"))
	s, th := fixture()
	s = withComments(s)
	findings := []site.Finding{{Message: "taw/core v1.59.2, latest is v1.76.1", Code: "core.behind"}}
	p := a.SkillPrompt(s, th, Skill{Name: "perf-audit"}, findings)
	for _, want := range []string{
		"Use the perf-audit skill on this site.", "taw-fleet show ls-mxico --json",
		"- Theme: ls-mexico, this folder (classic, taw/core 1.59.2)",
		"- Repo: https://github.com/Relmaur/ls-mexico--theme, deploy branch main",
		"- Production: https://lsmexico.mx", "- BugSmash: Lsmexico.mx, project p1, 4 open comments, review page https://x.bugsmash.io/review/a",
		"  - taw/core v1.59.2, latest is v1.76.1 (core.behind)",
	} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p.Text)
		}
	}

	l, err := a.LaunchSkill(context.Background(), s, th, "skill-perf-audit", p, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.Base(l.Done), "ls-mxico-skill-perf-audit-") {
		t.Errorf("done = %s", l.Done)
	}
	script, err := os.ReadFile(f.Calls()[0].Args[2])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "cd '"+th.RealPath+"' || exit 1") || strings.Contains(string(script), "--add-dir") {
		t.Errorf("Claude starts in the theme folder, nothing added:\n%s", script)
	}
}
