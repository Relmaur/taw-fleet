package tui

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/site"
)

func TestSetupChecklist(t *testing.T) {
	rep := fixtureReport()
	// acme-shop is a new site: a git repository with no GitHub remote yet.
	rep.Sites[0].Themes[0].Git.Repo, rep.Sites[0].Themes[0].Git.Upstream = nil, ""
	m := newModel(t, 100, 40, &rep)
	m.deps.Actions = &fakeActions{}
	m.deps.Settings = func(string) config.Site { return config.Site{} }
	m.deps.HasBot = func(context.Context, string) bool { return false }
	m = press(t, m, "enter")
	sc := screen(m)
	for _, want := range []string{"SETUP", "I · set up the next one", "→ Repository", "not on GitHub",
		"I creates a private GitHub repository and pushes it", "· Companion", "after production", "Production     no production URL"} {
		if !strings.Contains(sc, want) {
			t.Errorf("lacks %q:\n%s", want, sc)
		}
	}
	golden(t, "setup-detail-100x40", sc)

	m = press(t, m, "I")
	if !strings.Contains(m.confirm, "Create the private repository acme--theme on Relmaur and push main?") {
		t.Errorf("I asked %q", m.confirm)
	}

	// The TAW ecosystem's own themes get no checklist.
	eco := rep.Sites[3].Themes[0]
	g := *eco.Git
	eco.Git = &g
	eco.Git.Repo = &site.Repo{Owner: "Relmaur", Name: "taw-gutenberg"}
	if steps := m.setupSteps(rep.Sites[3], eco); steps != nil {
		t.Errorf("ecosystem steps: %+v", steps)
	}
}

func TestSetupLaunchReport(t *testing.T) {
	rep := fixtureReport()
	th := &rep.Sites[0].Themes[0]
	th.Core.Installed, th.Core.Locked = "v1.97.0", "v1.97.0"
	g := *th.Git
	g.Hook = "on"
	th.Git = &g
	th.GitHub = &site.RepoState{Repo: "Relmaur/client--theme", Default: "main", Head: "abc1234", HeadCI: site.ChecksPassing, CheckedAt: now,
		Deploy: &site.Deploy{Workflow: "Deploy", Deployed: "abc1234", DeployedAt: now.Add(-2 * time.Hour)}}
	m := newModel(t, 100, 44, &rep)
	m.deps.Actions = &fakeActions{}
	m.deps.Settings = func(slug string) config.Site {
		if slug == "acme-shop" {
			return config.Site{ProductionURL: "https://acme.mx"}
		}
		return config.Site{}
	}
	m.deps.HasBot = func(context.Context, string) bool { return false }
	var made []string
	m.deps.LaunchTask = func(s site.Site, theme string) actions.Task {
		made = append(made, s.Slug+" "+theme)
		return actions.Task{Title: "Launch report: " + s.Slug, Quiet: true, Run: func(context.Context, io.Writer) (actions.Summary, error) {
			return actions.Summary{Headline: "open"}, nil
		}}
	}
	m.rep.Sites[0].Production = &site.Production{URL: "https://acme.mx", CheckedAt: now, Reachable: true, Verified: true, WP: "6.8.3", PHP: "8.3", TawCore: "v1.97.0", Companion: "0.3.0"}
	m = press(t, m, "enter")
	sc := screen(m)
	// Live, and what's left (the bot, BugSmash) is done by hand: I makes the launch report.
	if !strings.Contains(sc, "I · launch report") || !strings.Contains(sc, "→ Bot") {
		t.Errorf("SETUP:\n%s", sc)
	}
	if os.Getenv("SHOT") != "" {
		_ = os.WriteFile(os.Getenv("SHOT"), []byte(m.View().Content), 0o644)
	}
	m = press(t, m, "I")
	if len(made) != 1 || made[0] != "acme-shop "+th.Dir {
		t.Errorf("I made %v", made)
	}

	// Not live yet: I's note stays, and a step done by hand is said.
	m.deps.Settings = func(string) config.Site { return config.Site{} }
	if strings.Contains(m.setupSection(m.rep.Sites[0], *th, 100), "launch report") {
		t.Error("launch report offered before production")
	}
}
