package tui

import (
	"context"
	"strings"
	"testing"

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
