package style

import (
	"fmt"
	"strings"

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
func (p Palette) Git(g *site.GitInfo) string {
	if g == nil {
		return p.Fg(p.Muted).Render("no git")
	}
	branch := g.Branch
	switch {
	case g.Detached:
		branch = p.Fg(p.Warn).Render("detached")
	case g.DefaultBranch != "" && g.Branch != g.DefaultBranch:
		branch = p.Fg(p.Accent).Render(branch)
	}
	parts := []string{branch}
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
		parts = append(parts, p.Fg(p.Warn).Render("⇡ unpushed"))
	}
	return strings.Join(parts, " ")
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
