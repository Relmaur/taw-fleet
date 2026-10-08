package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Layout constants.
const (
	minWidth    = 60
	minHeight   = 10
	sideBySide  = 120 // at this width and up, the detail pane sits beside the table
	headerLines = 2
	footerLines = 2
)

// View renders the whole screen.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "taw-fleet"
	return v
}

func (m Model) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		msg := fmt.Sprintf("taw-fleet needs at least %d×%d.\nMake the window bigger.", minWidth, minHeight)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.pal.Fg(m.pal.Muted).Render(msg))
	}
	body := m.body()
	return m.header() + "\n\n" + body + "\n" + m.footer()
}

func (m Model) bodyHeight() int { return max(m.height-headerLines-footerLines, 1) }

// tableRows is how many data rows fit under the column header and rule.
func (m Model) tableRows() int { return max(m.bodyHeight()-2, 1) }

func (m Model) wide() bool { return m.width >= sideBySide }

// --- header -----------------------------------------------------------------

func (m Model) header() string {
	p := m.pal
	muted := p.Fg(p.Muted)
	left := " " + lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render("◆ taw-fleet")
	if m.deps.Version != "" {
		left += " " + muted.Render(m.deps.Version)
	}

	var right string
	switch {
	case m.scanning:
		right = m.spin.View() + " " + muted.Render("scanning…")
	case m.err != nil:
		right = p.Fg(p.Err).Render("✗ scan failed")
	case m.loaded:
		right = muted.Render("updated " + render.Ago(m.now, m.rep.ScannedAt))
	}
	right += " "

	if m.loaded {
		sites, themes, running, behind := 0, 0, 0, 0
		for _, s := range m.rep.Sites {
			if !s.IsTAW() {
				continue
			}
			sites++
			if s.Status == site.StatusRunning {
				running++
			}
			for _, t := range s.TAWThemes() {
				themes++
				if t.Core.Behind {
					behind++
				}
			}
		}
		stats := func(compact bool) string {
			sep, themeWord, behindWord := "  ·  ", "TAW ", " on taw/core"
			if compact {
				sep, themeWord, behindWord = " · ", "", ""
			}
			parts := []string{
				muted.Render(fmt.Sprintf("%d %s", sites, plural(sites, "site", "sites"))),
				muted.Render(fmt.Sprintf("%d %s%s", themes, themeWord, plural(themes, "theme", "themes"))),
				p.Fg(p.OK).Render(fmt.Sprintf("%d running", running)),
			}
			if behind > 0 {
				parts = append(parts, p.Fg(p.Warn).Render(fmt.Sprintf("%d behind%s", behind, behindWord)))
			}
			return muted.Render(sep) + strings.Join(parts, muted.Render(sep))
		}
		full := stats(false)
		if ansi.StringWidth(left+full+right)+2 > m.width {
			full = stats(true)
		}
		if ansi.StringWidth(left+full+right)+2 > m.width && m.loaded && !m.scanning && m.err == nil {
			right = muted.Render(render.Ago(m.now, m.rep.ScannedAt)) + " "
		}
		left += full
	}
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return ansi.Truncate(left, m.width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

// --- body -------------------------------------------------------------------

func (m Model) body() string {
	h := m.bodyHeight()
	p := m.pal
	switch {
	case m.mode == modeHelp:
		return block(m.helpScreen(), m.width, h)
	case !m.loaded && m.err != nil:
		return block(center(p.Fg(p.Err).Render("✗ "+m.err.Error()), m.width, h), m.width, h)
	case !m.loaded:
		return block(center(m.spin.View()+" "+p.Fg(p.Muted).Render("Reading Local's sites…"), m.width, h), m.width, h)
	case len(m.rows) == 0:
		return block(m.emptyState(), m.width, h)
	case m.mode == modeDetail:
		return m.detailScreen(h)
	}

	if !m.wide() {
		return block(m.table(m.width), m.width, h)
	}
	detailW := min(max(m.width*36/100, 48), 80)
	tableW := m.width - detailW - 3
	sep := strings.TrimRight(strings.Repeat(p.Fg(p.Faint).Render(" │ ")+"\n", h), "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top,
		block(m.table(tableW), tableW, h),
		sep,
		block(m.detailPane(detailW, h), detailW, h),
	)
}

func (m Model) emptyState() string {
	p := m.pal
	lines := []string{"", " " + lipgloss.NewStyle().Bold(true).Render("No TAW sites found on this Mac.")}
	for _, e := range m.rep.Errors {
		lines = append(lines, " "+p.Fg(p.Warn).Render("▲ "+e.Err))
	}
	lines = append(lines, "",
		" "+p.Fg(p.Muted).Render("taw-fleet reads Local by Flywheel's sites. A TAW theme is one whose composer.json requires taw/core."))
	return strings.Join(lines, "\n")
}

// --- table ------------------------------------------------------------------

type columns struct{ site, theme, kind, core, git int }

func (m Model) columns(width int) columns {
	c := columns{site: 4, theme: 5, kind: 7, core: 8}
	for _, r := range m.rows {
		s := m.rep.Sites[r.site]
		t := s.Themes[r.theme]
		c.site = max(c.site, ansi.StringWidth(s.Slug))
		c.theme = max(c.theme, ansi.StringWidth(themeLabel(t)))
		c.kind = max(c.kind, ansi.StringWidth(m.pal.Kind(t.Kind)))
		c.core = max(c.core, ansi.StringWidth(m.pal.Core(t.Core)))
	}
	c.site = min(c.site, 24)
	c.theme = min(c.theme, 20)
	// marker + dot + spaces + four gaps of two.
	fixed := 4 + c.site + c.theme + c.kind + c.core + 8
	c.git = width - fixed
	if c.git < 8 { // squeeze the names before the git column vanishes
		over := 8 - c.git
		cut := min(over, c.site-10)
		c.site -= max(cut, 0)
		over -= max(cut, 0)
		c.theme -= min(max(over, 0), c.theme-10)
		c.git = max(width-(4+c.site+c.theme+c.kind+c.core+8), 4)
	}
	return c
}

func themeLabel(t site.Theme) string {
	if t.Symlink {
		return t.Dir + " ↗"
	}
	return t.Dir
}

func (m Model) table(width int) string {
	p := m.pal
	c := m.columns(width)
	head := p.Fg(p.Muted).Bold(true)
	lines := []string{
		"    " + head.Render(pad("SITE", c.site)+"  "+pad("THEME", c.theme)+"  "+pad("KIND", c.kind)+"  "+pad("TAW/CORE", c.core)+"  GIT"),
		p.Fg(p.Faint).Render(strings.Repeat("─", width)),
	}
	if len(m.visible) == 0 {
		lines = append(lines, "", "  "+p.Fg(p.Muted).Render(fmt.Sprintf("No match for “%s”.  esc clears the filter.", m.filter.Value())))
		return strings.Join(lines, "\n")
	}

	end := min(m.offset+m.tableRows(), len(m.visible))
	for vi := m.offset; vi < end; vi++ {
		r := m.rows[m.visible[vi]]
		s := m.rep.Sites[r.site]
		t := s.Themes[r.theme]
		sel := vi == m.cursor

		// The site is named once per group of rows, like `list`.
		first := vi == m.offset || m.rows[m.visible[vi-1]].site != r.site
		dot, name := " ", ""
		if first {
			dot, name = p.Dot(s.Status), s.Slug
		}
		theme := t.Dir
		marker := " "
		nameStyle := lipgloss.NewStyle()
		if sel {
			marker = p.Fg(p.Accent).Render("▌")
			nameStyle = nameStyle.Bold(true).Foreground(p.Accent)
		}
		themeCell := nameStyle.Render(theme)
		if t.Symlink {
			themeCell += p.Fg(p.Muted).Render(" ↗")
		}
		line := marker + " " + dot + " " +
			pad(nameStyle.Render(name), c.site) + "  " +
			pad(themeCell, c.theme) + "  " +
			pad(p.Kind(t.Kind), c.kind) + "  " +
			pad(p.Core(t.Core), c.core) + "  " +
			ansi.Truncate(p.GitFit(t.Git, c.git), c.git, "…")
		lines = append(lines, line)
	}
	if len(m.visible) > m.tableRows() {
		lines = append(lines[:min(len(lines), m.tableRows()+1)],
			p.Fg(p.Muted).Render(fmt.Sprintf("    %d–%d of %d", m.offset+1, end, len(m.visible))))
	}
	return strings.Join(lines, "\n")
}

// --- detail -----------------------------------------------------------------

func (m Model) detailContent(width int) string {
	s, _, ok := m.selectedTheme()
	if !ok {
		return ""
	}
	return render.Site(m.pal, m.deps.Paths, s, m.findings[s.ID], m.now, width)
}

// detailPane is the right-hand pane on wide terminals.
func (m Model) detailPane(width, height int) string {
	lines := strings.Split(strings.TrimRight(m.detailContent(width), "\n"), "\n")
	if len(lines) > height {
		lines = append(lines[:height-1], m.pal.Fg(m.pal.Muted).Render(" ↓ more: enter"))
	}
	return strings.Join(lines, "\n")
}

// detailScreen is the full-screen detail (enter), scrollable.
func (m Model) detailScreen(height int) string {
	lines := strings.Split(strings.TrimRight(m.detailContent(m.width), "\n"), "\n")
	maxScroll := max(len(lines)-height, 0)
	from := min(m.scroll, maxScroll)
	return block(strings.Join(lines[from:min(from+height, len(lines))], "\n"), m.width, height)
}

// --- help -------------------------------------------------------------------

func (m Model) helpScreen() string {
	p := m.pal
	h := m.help
	h.ShowAll = true
	muted := p.Fg(p.Muted)
	legend := []string{
		p.Dot(site.StatusRunning) + " running   " + p.Dot(site.StatusHalted) + " halted   " + p.Dot(site.StatusBusy) + " starting/stopping",
		muted.Render("↗") + " theme is a symlink (the umbrella's taw-theme / taw-gutenberg)",
		p.Core(site.CoreInfo{Installed: "v1.59.2", Latest: "v1.76.1", Behind: true}) + "  taw/core installed ▲ newest",
		p.Git(&site.GitInfo{Branch: "main", DefaultBranch: "main", Upstream: "origin/main", Dirty: 3, Ahead: 1, Behind: 2}) +
			"  uncommitted ±, to push ↑, to pull ↓",
		p.Git(&site.GitInfo{Branch: "feature", DefaultBranch: "main"}) + "  not the default branch, not pushed",
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Faint).Padding(1, 2)
	content := lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render("Keys") + "\n\n" + h.View(m.keys) +
		"\n\n" + lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render("Symbols") + "\n\n" + strings.Join(legend, "\n") +
		"\n\n" + muted.Render("esc or ? to go back")
	return lipgloss.Place(m.width, m.bodyHeight(), lipgloss.Center, lipgloss.Center, box.Render(content))
}

// --- footer -----------------------------------------------------------------

func (m Model) footer() string {
	p := m.pal
	muted := p.Fg(p.Muted)
	var status string
	switch {
	case m.filtering:
		status = " " + m.filter.View()
	case m.filter.Value() != "":
		status = " " + p.Fg(p.Accent).Render("/ "+m.filter.Value()) + muted.Render(fmt.Sprintf("  ·  %d of %d  ·  esc clears", len(m.visible), len(m.rows)))
	case m.err != nil && m.loaded:
		status = " " + p.Fg(p.Err).Render("✗ last refresh failed: "+m.err.Error())
	case len(m.rep.Errors) > 0 && len(m.rows) > 0: // the empty state already lists them
		status = " " + p.Fg(p.Warn).Render("▲ "+m.rep.Errors[0].Err)
	}
	var keys help.KeyMap = m.keys
	if m.mode == modeDetail {
		keys = detailKeys{m.keys}
	}
	return ansi.Truncate(status, m.width, "…") + "\n" + " " + m.help.View(keys)
}

// --- helpers ----------------------------------------------------------------

// pad truncates or pads s to exactly w cells.
func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if gap := w - ansi.StringWidth(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

// block makes s exactly w×h: lines truncated/padded, rows cut or added.
func block(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = pad(l, w)
	}
	return strings.Join(lines, "\n")
}

func center(s string, w, h int) string {
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, s)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
