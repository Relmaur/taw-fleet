package actions

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// resolveSkill is the umbrella skill that resolves BugSmash comments.
const resolveSkill = "taw-resolve-comments"

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

// Umbrella finds the TAW umbrella checkout that has the resolve skill: the
// umbrella setting, else the folder that a theme linked into Local from
// taw-theme or taw-gutenberg lives in.
func (a *Actions) Umbrella(sites []site.Site) (string, error) {
	if u := a.Config.Umbrella; u != "" {
		if rest, ok := strings.CutPrefix(u, "~/"); ok {
			u = filepath.Join(a.Paths.Home, rest)
		}
		if !hasResolveSkill(u) {
			return "", fmt.Errorf("the umbrella %s has no %s skill (.claude/skills/%s)", u, resolveSkill, resolveSkill)
		}
		return u, nil
	}
	for _, s := range sites {
		for _, t := range s.Themes {
			if dir := filepath.Dir(t.RealPath); t.Symlink && handoff.IsUmbrella(t) && hasResolveSkill(dir) {
				return dir, nil
			}
		}
	}
	return "", fmt.Errorf("can't find the TAW umbrella with the %s skill: set umbrella = \"~/…/TAW\" in %s", resolveSkill, config.File(a.Paths))
}

func hasResolveSkill(dir string) bool {
	return fileExists(filepath.Join(dir, ".claude", "skills", resolveSkill, "SKILL.md"))
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
	}
	now := a.now()
	project := f.Project
	if project == "" {
		project = "the BugSmash project"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Use the %s skill to resolve the open BugSmash comments on %s.\n\n", resolveSkill, s.Slug)
	fmt.Fprintf(&b, "- Local site: %s (%s), theme %s at %s\n", s.Slug, s.URL, t.Dir, t.RealPath)
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

// LaunchResolve opens Claude Code in the umbrella, with the theme folder
// added, and the prompt as its first message (beside the dashboard, like
// Launch).
func (a *Actions) LaunchResolve(ctx context.Context, s site.Site, t site.Theme, umbrella string, p handoff.Prompt, beside string) (Launched, error) {
	dir := filepath.Join(a.Paths.CacheDir, "handoff")
	base := fmt.Sprintf("%s-comments-%s", safeName(s.Slug), a.now().Format("20060102-150405"))
	ls := LaunchScript{Title: p.Title, Dir: umbrella, Args: []string{"--add-dir", t.RealPath},
		Prompt: filepath.Join(dir, base+".md"), Done: filepath.Join(dir, base+".done")}
	used, placed, err := a.launch(ctx, ls, p.Text, filepath.Join(dir, base+".command"), beside)
	if err != nil {
		return Launched{}, err
	}
	msg := fmt.Sprintf("Started Claude Code in %s on %s's comments", used, s.Slug)
	if placed {
		msg = fmt.Sprintf("Claude Code is on %s's comments in the window on the right", s.Slug)
	}
	return Launched{Message: msg, Done: ls.Done}, nil
}
