package style

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/site"
)

// Core renders a theme's taw/core: "1.76.1" when current, "1.59.2 ▲ 1.76.1"
// when behind, "missing" when vendor/ isn't installed.
func (p Palette) Core(c site.CoreInfo) string {
	switch {
	case c.Err != "":
		return p.Fg(p.Err).Render("unreadable")
	case c.Installed == "":
		return p.Fg(p.Err).Render("missing")
	}
	v := strings.TrimPrefix(c.Installed, "v")
	var out string
	switch {
	case c.Behind:
		out = p.Fg(p.Warn).Render(v) + " " + p.Fg(p.Err).Bold(true).Render("▲") + " " + p.Fg(p.Muted).Render(strings.TrimPrefix(c.Latest, "v"))
	case c.Latest != "":
		out = p.Fg(p.OK).Render(v)
	default:
		out = v // latest unknown (offline, rate limited)
	}
	if c.LockMismatch {
		out += " " + p.Fg(p.Warn).Render("≠lock")
	}
	return out
}

// Git renders a repo's state compactly: "main ±5 ↑2 ↓1". The branch is
// accented when it isn't the default one.
func (p Palette) Git(g *site.GitInfo) string { return p.GitFit(g, 0) }

// GitFit is Git within width cells (0 = no limit). The branch name is
// shortened first, so the markers (±, ↑, ↓, unpushed) always stay visible.
func (p Palette) GitFit(g *site.GitInfo, width int) string {
	if g == nil {
		return p.Fg(p.Muted).Render("no git")
	}
	markers := p.gitMarkers(g, false)
	if width > 0 && ansi.StringWidth(markers)+5 > width {
		markers = p.gitMarkers(g, true)
	}
	name := g.Branch
	if g.Detached {
		name = "detached"
	}
	if width > 0 {
		budget := width
		if markers != "" {
			budget -= ansi.StringWidth(markers) + 1
		}
		name = ansi.Truncate(name, max(budget, 1), "…")
	}
	switch {
	case g.Detached:
		name = p.Fg(p.Warn).Render(name)
	case g.DefaultBranch != "" && g.Branch != g.DefaultBranch:
		name = p.Fg(p.Accent).Render(name)
	}
	if markers == "" {
		return name
	}
	return name + " " + markers
}

// gitMarkers: compact drops the word "unpushed" from ◇.
func (p Palette) gitMarkers(g *site.GitInfo, compact bool) string {
	var parts []string
	if g.Dirty > 0 {
		parts = append(parts, p.Fg(p.Warn).Render(fmt.Sprintf("±%d", g.Dirty)))
	}
	if g.Ahead > 0 {
		parts = append(parts, p.Fg(p.Info).Render(fmt.Sprintf("↑%d", g.Ahead)))
	}
	if g.Behind > 0 {
		parts = append(parts, p.Fg(p.Warn).Render(fmt.Sprintf("↓%d", g.Behind)))
	}
	if !g.Detached && !g.HasUpstream() {
		unpushed := "◇ unpushed"
		if compact {
			unpushed = "◇"
		}
		parts = append(parts, p.Fg(p.Warn).Render(unpushed))
	}
	return strings.Join(parts, " ")
}

// Sync is the last scaffold sync check: — never run, ✓ framework files
// match, ▲N that many Tier 1 paths differ, ✗ the check failed.
func (p Palette) Sync(d *site.Drift) string {
	switch {
	case d == nil:
		return p.Fg(p.Faint).Render("—")
	case len(d.Errors) > 0:
		return p.Fg(p.Err).Render("✗")
	case len(d.Tier1) > 0:
		return p.Fg(p.Warn).Render(fmt.Sprintf("▲%d", len(d.Tier1)))
	}
	return p.Fg(p.OK).Render("✓")
}

// Severity is the icon for a finding: ✗ error, ▲ warning, • info.
func (p Palette) Severity(s site.Severity) string {
	switch s {
	case site.Error:
		return p.Fg(p.Err).Bold(true).Render("✗")
	case site.Warn:
		return p.Fg(p.Warn).Bold(true).Render("▲")
	}
	return p.Fg(p.Info).Render("•")
}
