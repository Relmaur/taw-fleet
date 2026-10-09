package actions

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// ResolveSkill is the site skill that resolves BugSmash comments (shipped by
// taw/core, installed in the theme by bin/taw sync or skills:sync).
const ResolveSkill = "resolve-comments"

// OpenComments opens the site's BugSmash review page, where its comments are.
func (a *Actions) OpenComments(ctx context.Context, s site.Site) (string, error) {
	f := s.Feedback
	switch {
	case f == nil:
		return "", fmt.Errorf("no BugSmash project for %s: add [sites.%s] bugsmash_project to %s", s.Slug, s.Slug, config.File(a.Paths))
	case f.URL == "":
		return "", errors.New("BugSmash hasn't said where this project's comments are yet; L checks again")
	}
	return "Opened " + f.URL, a.open.URL(ctx, f.URL)
}

// ResolvePrompt is the first message for Claude: resolve the site's open
// comments with the skill, with what taw-fleet already knows as a head start.
func (a *Actions) ResolvePrompt(s site.Site, t site.Theme) (handoff.Prompt, error) {
	f := s.Feedback
	switch {
	case f == nil:
		return handoff.Prompt{}, fmt.Errorf("no BugSmash project for %s: add [sites.%s] bugsmash_project to %s", s.Slug, s.Slug, config.File(a.Paths))
	case f.Error != "":
		return handoff.Prompt{}, fmt.Errorf("BugSmash: %s", f.Error)
	case f.Open == 0:
		return handoff.Prompt{}, fmt.Errorf("no open comments on %s", s.Slug)
	case !HasSkill(t, ResolveSkill):
		return handoff.Prompt{}, fmt.Errorf("%s doesn't have the %s skill yet: it comes with taw/core 1.89+ (S syncs a classic theme; a block theme: php bin/taw skills:sync --apply)", t.Dir, ResolveSkill)
	}
	now := a.now()
	project := f.Project
	if project == "" {
		project = "the BugSmash project"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Use the %s skill to resolve the open BugSmash comments on %s.\n\n", ResolveSkill, s.Slug)
	fmt.Fprintf(&b, "- Local site: %s (%s), theme %s (this folder)\n", s.Slug, s.URL, t.Dir)
	if u := a.Config.Site(s.Slug).ProductionURL; u != "" {
		fmt.Fprintf(&b, "- Production: %s\n", u)
	}
	fmt.Fprintf(&b, "- BugSmash: %s, project %s", project, f.ProjectID)
	if f.URL != "" {
		fmt.Fprintf(&b, ", review page %s", f.URL)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- %d open %s", f.Open, plural(f.Open, "comment", "comments"))
	if !f.Oldest.IsZero() {
		fmt.Fprintf(&b, ", the oldest from %s", f.Oldest.Local().Format("Jan 2 15:04"))
	}
	fmt.Fprintf(&b, " (taw-fleet's snapshot at %s; read the full list from BugSmash):\n", now.Format("Jan 2 15:04"))
	for _, c := range f.Comments {
		page := c.Page
		if pu, err := url.Parse(c.Page); err == nil && pu.Path != "" {
			page = pu.Path
		}
		fmt.Fprintf(&b, "  - #%d %s: %s", c.Number, page, c.Text)
		if c.Author != "" {
			fmt.Fprintf(&b, " (%s)", c.Author)
		}
		b.WriteString("\n")
	}
	if more := f.Open - len(f.Comments); more > 0 {
		fmt.Fprintf(&b, "  - … and %d more\n", more)
	}
	b.WriteString("\nFollow the skill's owner rules: triage first and show me the plan, ask before anything reaches production, and resolve a comment only once its change is live.\n")
	return handoff.Prompt{Title: "resolve comments on " + s.Slug, Text: b.String()}, nil
}
