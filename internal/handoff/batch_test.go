package handoff

import (
	"errors"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/site"
)

func TestBatchPrompt(t *testing.T) {
	p, err := BuildBatch(lsMexico())
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "batch-behind-halted", p.Text)
	for _, want := range []string{`§ "Batch mode"`, SkillURL, "`chore/taw-core-1.76.1` from `main`",
		"`composer update taw/core --with-dependencies` (1.59.2 → 1.76.1", "`needs-wordpress`", "B9 JSON block"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Nothing that waits for an answer: a batch agent can't get one.
	for _, never := range []string{"ask me", "Ask me", "wait for me"} {
		if strings.Contains(p.Text, never) {
			t.Errorf("a batch prompt must not say %q", never)
		}
	}

	in := lsMexico()
	in.Theme.Kind = site.KindGutenberg
	p, _ = BuildBatch(in)
	if !strings.Contains(p.Text, "without its sync steps (B4, B5)") {
		t.Error("a block theme has no scaffold sync")
	}
	in = lsMexico()
	in.Theme.Git.Repo.Name = "taw-theme"
	if _, err := BuildBatch(in); !errors.Is(err, ErrUmbrella) {
		t.Errorf("umbrella: %v", err)
	}
}

func TestSkipAndNeeds(t *testing.T) {
	base := lsMexico().Theme
	cases := []struct {
		name string
		edit func(*site.Theme)
		want string
	}{
		{"ready", func(*site.Theme) {}, ""},
		{"dirty", func(t *site.Theme) { t.Git.Dirty = 2 }, "2 uncommitted change(s)"},
		{"other branch", func(t *site.Theme) { t.Git.Branch = "feature/x" }, "on feature/x, not main"},
		{"a batch branch resumes", func(t *site.Theme) { t.Git.Branch = "chore/taw-core-1.76.1" }, ""},
		{"no git", func(t *site.Theme) { t.Git = nil }, "not a git repository"},
	}
	for _, c := range cases {
		th := base
		g := *base.Git
		th.Git = &g
		c.edit(&th)
		if got := Skip(th); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}

	current := base
	current.Core.Behind = false
	if !Needs(current) {
		t.Error("never checked the scaffold: needs a run")
	}
	current.Drift = &site.Drift{}
	if Needs(current) {
		t.Error("current and no drift: nothing to do")
	}
	current.Drift.Tier1 = []string{"bin/"}
	if !Needs(current) {
		t.Error("drift: needs a run")
	}
}

func TestFleetPrompt(t *testing.T) {
	p := Fleet([]FleetTheme{
		{Site: "ls-mxico", Theme: "ls-mexico", Path: "/T/ls-mexico", PromptFile: "/C/fleet/ls-mxico-ls-mexico.md", From: "v1.76.1", To: "v1.78.0"},
		{Site: "parallel-plus", Theme: "parallelplus", Path: "/T/parallelplus", PromptFile: "/C/fleet/parallel-plus-parallelplus.md"},
	}, []FleetSkip{{Site: "fsspx-taw", Theme: "fsspx--theme", Reason: "2 uncommitted change(s)"}}, 3, now)

	golden(t, "fleet", p.Text)
	if p.Title != "Update 2 TAW themes" {
		t.Errorf("title = %q", p.Title)
	}
	for _, want := range []string{"| ls-mxico | ls-mexico | 1.76.1 → 1.78.0 | `/C/fleet/ls-mxico-ls-mexico.md` |", "| parallel-plus | parallelplus | current |",
		"fsspx--theme: 2 uncommitted change(s)", "**at most 3 at a time**", "AskUserQuestion", "Nothing is pushed without this answer"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("missing %q", want)
		}
	}
}
