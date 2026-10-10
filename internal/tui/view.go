package tui

import (
	"fmt"
	"image/color"
	"regexp"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/selfupdate"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
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
	v.AltScreen = !m.deps.Inline
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

func (m Model) wide() bool { return m.detailWidth() > 0 }

// detailWidth is the side pane's width: about a third of the window, less
// when the table needs the room, none under sideBySide or 40 columns.
func (m Model) detailWidth() int {
	if m.width < sideBySide {
		return 0
	}
	w := min(max(m.width*36/100, 48), 80)
	if need := m.columns(m.width).need(); m.width-w-3 < need {
		w = m.width - 3 - need
	}
	if w < 40 {
		return 0
	}
	return w
}

// --- header -----------------------------------------------------------------

func (m Model) header() string {
	p := m.pal
	muted := p.Fg(p.Muted)
	left := " " + lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render("◆ taw-fleet")
	if m.deps.Version != "" {
		left += " " + muted.Render(m.deps.Version)
		if tag := m.rep.Latest[scan.LatestFleet]; tag != "" && m.deps.Version != "dev" && selfupdate.Newer(m.deps.Version, tag) {
			left += " " + p.Fg(p.Warn).Render("▲ "+strings.TrimPrefix(tag, "v"))
		}
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
		for _, s := range m.rep.Sites { // client sites: not the TAW ecosystem
			if !s.IsTAW() || ecosystemSite(s) {
				continue
			}
			sites++
			if s.Status == site.StatusRunning {
				running++
			}
			for _, t := range s.TAWThemes() {
				if ecosystem(t) {
					continue
				}
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
	case m.mode == modeMenu, m.mode == modeSkills:
		return block(m.paletteScreen(h), m.width, h)
	case !m.loaded && m.err != nil:
		return block(center(p.Fg(p.Err).Render("✗ "+m.err.Error()), m.width, h), m.width, h)
	case m.mode == modeCreate && m.form != nil:
		return m.createScreen(h)
	case m.mode == modeOutput:
		return m.outputScreen(h)
	case !m.loaded:
		return block(center(m.spin.View()+" "+p.Fg(p.Muted).Render("Reading Local's sites…"), m.width, h), m.width, h)
	case len(m.rows) == 0:
		return block(m.emptyState(), m.width, h)
	case m.mode == modeDetail:
		return m.detailScreen(h)
	case m.mode == modeHandoff:
		return m.handoffScreen(h)
	}

	if !m.wide() {
		return block(m.table(m.width), m.width, h)
	}
	detailW := m.detailWidth()
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
		" "+p.Fg(p.Muted).Render("taw-fleet reads Local by Flywheel's sites. A TAW theme is one whose composer.json requires taw/core."),
		"", " "+p.Fg(p.Accent).Render("n")+p.Fg(p.Muted).Render(" creates one: a Local site with taw-theme or taw-gutenberg."))
	return strings.Join(lines, "\n")
}

// --- table ------------------------------------------------------------------

// cardLines is how tall a row is: the site on top, its theme underneath.
// While every row fits, a blank line separates them too (airy).
const cardLines = 2

// minGit is the narrowest GIT column before the cards give up room.
const minGit = 14

// Gaps between columns: at least minGap; spare width widens them up to
// maxGap so the table spans its panel instead of bunching up on the left.
const (
	minGap = 2
	maxGap = 8
)

type columns struct {
	card, core, sync, live, fb, pr, deploy, git int
	gap                                         int  // spaces between columns
	base                                        int  // the cards without the production host
	host                                        bool // the cards have room for the production host
}

func (m Model) columns(width int) columns {
	c := columns{card: 16, core: 8, sync: 4, gap: minGap}
	if m.deps.Live != nil { // only with production sites configured
		c.live = 4
	}
	if m.deps.Feedback != nil { // only with BugSmash projects configured
		c.fb = 3
	}
	if m.deps.GitHub != nil {
		c.pr, c.deploy = 3, 6
	}
	full := c.card // the cards with the production host
	git := minGit  // the GIT column's own width (branch and markers)
	for _, r := range m.rows {
		s := m.rep.Sites[r.site]
		t := s.Themes[r.theme]
		c.card = max(c.card, ansi.StringWidth(s.Slug), ansi.StringWidth(m.subline(s, t, false, false)))
		full = max(full, c.card, ansi.StringWidth(m.subline(s, t, false, true)))
		c.core = max(c.core, ansi.StringWidth(m.pal.Core(t.Core)))
		gw := ansi.StringWidth(m.pal.GitFit(t.Git, 0))
		if t.Account != "" {
			gw += ansi.StringWidth("@" + t.Account + " ")
		}
		git = max(git, gw)
	}
	c.card, full, git = min(c.card, 40), min(full, 40), min(git, 48)
	c.base = c.card
	if width-c.fixed()-(full-c.card) >= minGit { // room for the hosts too
		c.card, c.host = full, true
	}
	if spare := width - c.fixed() - git; spare > 0 {
		c.gap = min(minGap+spare/c.gaps(), maxGap)
	}
	c.git = width - c.fixed()
	if c.git < minGit { // narrow the cards before the git column vanishes
		c.card = max(c.card-(minGit-c.git), 16)
		c.git = max(width-c.fixed(), 4)
	}
	return c
}

// gaps is how many gaps there are between the columns.
func (c columns) gaps() int {
	n := 3 // after the card, TAW/CORE and SYNC
	if c.live > 0 {
		n++
	}
	if c.fb > 0 {
		n++
	}
	if c.pr > 0 {
		n += 2
	}
	return n
}

// need is the narrowest table that keeps the cards (without hosts) and GIT
// readable, with the narrowest gaps.
func (c columns) need() int {
	c.card, c.gap = c.base, minGap
	return c.fixed() + minGit
}

// fixed is every column but GIT, with the marker, the dot and the gaps.
func (c columns) fixed() int {
	return 4 + c.card + c.core + c.sync + 3*c.gap + c.liveWidth() + c.fbWidth() + c.ghWidth()
}

// sep is the space between two columns.
func (c columns) sep() string { return strings.Repeat(" ", c.gap) }

// liveWidth is the LIVE column with its gap, or nothing.
func (c columns) liveWidth() int {
	if c.live == 0 {
		return 0
	}
	return c.live + c.gap
}

// fbWidth is the FB column with its gap, or nothing.
func (c columns) fbWidth() int {
	if c.fb == 0 {
		return 0
	}
	return c.fb + c.gap
}

// ghWidth is the PR and DEPLOY columns with their gaps, or nothing.
func (c columns) ghWidth() int {
	if c.pr == 0 {
		return 0
	}
	return c.pr + c.deploy + 2*c.gap
}

// ghCols are the PR and DEPLOY cells with their gaps, when shown.
func (c columns) ghCols(pr, deploy string) string {
	if c.pr == 0 {
		return ""
	}
	return pad(pr, c.pr) + c.sep() + pad(deploy, c.deploy) + c.sep()
}

// liveCol is a LIVE cell with its gap, when the column is shown.
func (c columns) liveCol(s string) string {
	if c.live == 0 {
		return ""
	}
	return pad(s, c.live) + c.sep()
}

// fbCol is an FB cell with its gap, when the column is shown.
func (c columns) fbCol(s string) string {
	if c.fb == 0 {
		return ""
	}
	return pad(s, c.fb) + c.sep()
}

// subline is a row's second line: the theme (↗ symlink, ✓ the active one of
// several, vite running), its kind, and, with host, the production host.
func (m Model) subline(s site.Site, t site.Theme, sel, host bool) string {
	p := m.pal
	name := p.Fg(p.Muted)
	if sel {
		name = p.Fg(p.Accent)
	}
	out := name.Render(t.Dir)
	if t.Symlink {
		out += p.Fg(p.Muted).Render(" ↗")
	}
	if s.ActiveTheme == t.Dir && len(s.TAWThemes()) > 1 {
		out += p.Fg(p.OK).Render(" ✓") // the one WordPress uses
	}
	if t.Dev != "" {
		out += p.Fg(p.Accent).Render(" vite")
	}
	out += "  " + p.Kind(t.Kind)
	if h := prodHost(s); host && h != "" {
		out += "  " + p.Fg(p.Brand).Render(h)
	}
	return out
}

// prodHost is the production site's host, when there is one.
func prodHost(s site.Site) string {
	if s.Production == nil || s.Production.URL == "" {
		return ""
	}
	h := strings.TrimPrefix(strings.TrimPrefix(s.Production.URL, "https://"), "http://")
	return strings.TrimSuffix(h, "/")
}

// tableRows is how many rows fit under the column header and rule, keeping a
// line for "x–y of n" when they don't all fit. The last row needs no blank
// line after it.
func (m Model) tableRows() int {
	n := len(m.visible)
	if m.airy() {
		return n
	}
	return max((m.bodyHeight()-2-m.labelLines())/cardLines, 1)
}

// airy reports whether every row fits with a blank line between the cards;
// otherwise the cards sit tight, so more of them fit.
func (m Model) airy() bool {
	n := len(m.visible)
	return n*(cardLines+1)-1+m.labelLines() <= m.bodyHeight()-2
}

// labelLines is the room the TAW ecosystem's label takes in the list: one
// line when client sites come before it (the rows sort it last).
func (m Model) labelLines() int {
	if n := len(m.visible); n > 1 && !m.ecosystemRow(0) && m.ecosystemRow(n-1) {
		return 1
	}
	return 0
}

func (m Model) table(width int) string {
	p := m.pal
	c := m.columns(width)
	head := p.Fg(p.Muted).Bold(true)
	sep := c.sep()
	names := "    " + head.Render(pad("SITE · THEME", c.card)+sep+pad("TAW/CORE", c.core)+sep+pad("SYNC", c.sync)+sep+c.liveCol("LIVE")+c.fbCol("FB")+c.ghCols("PR", "DEPLOY")+"GIT")
	end := min(m.offset+m.tableRows(), len(m.visible))
	rule := p.Fg(p.Faint).Render(strings.Repeat("─", width))
	if len(m.visible) > end-m.offset && width > 24 { // where the list is, set into the rule
		pos := fmt.Sprintf(" %d–%d of %d ", m.offset+1, end, len(m.visible))
		rule = p.Fg(p.Faint).Render(strings.Repeat("─", max(width-ansi.StringWidth(pos)-2, 1))) + p.Fg(p.Muted).Render(pos) + p.Fg(p.Faint).Render("──")
	}
	lines := []string{names, rule}
	if len(m.visible) == 0 {
		lines = append(lines, "", "  "+p.Fg(p.Muted).Render(fmt.Sprintf("No match for “%s”.  esc clears the filter.", m.filter.Value())))
		return strings.Join(lines, "\n")
	}

	air := m.airy()
	selAt := 0 // the selected card's first line
	for vi := m.offset; vi < end; vi++ {
		r := m.rows[m.visible[vi]]
		s := m.rep.Sites[r.site]
		t := s.Themes[r.theme]
		sel := vi == m.cursor
		eco := ecosystem(t)
		if eco && (vi == m.offset || !m.ecosystemRow(vi-1)) {
			// The TAW ecosystem starts: its label goes under the air above
			// it, or takes the place of the rule under the column names.
			label := strings.TrimSuffix(render.Section(p, "TAW ECOSYSTEM", p.Accent, "the scaffolds every TAW site is made from", width), "\n")
			switch {
			case vi == m.offset:
				lines[1] = label
			case air:
				lines = append(lines, "", label)
			default:
				lines = append(lines, label)
			}
		} else if vi > m.offset && air {
			lines = append(lines, "") // air between the cards
		}

		// Site-wide cells (dot, LIVE, FB) once per group of rows, like
		// `list`; a site's other themes repeat its name, quieter.
		first := vi == m.offset || m.rows[m.visible[vi-1]].site != r.site
		dot, liveCell, fbCell, fbAge := " ", "", "", ""
		nameStyle := lipgloss.NewStyle().Bold(true)
		if first {
			if !eco { // the scaffolds have no production site or reviewers
				liveCell, fbCell = p.Live(s.Production), p.Feedback(s.Feedback)
			}
			if f := s.Feedback; f != nil && f.Error == "" && f.Open > 0 && !f.Oldest.IsZero() {
				fbAge = p.Fg(p.Muted).Render(shortAge(m.now.Sub(f.Oldest)))
			}
			dot = p.Dot(s.Status)
			if _, busy := m.busy[s.ID]; busy {
				dot = p.Dot(site.StatusBusy)
			}
		} else {
			nameStyle = p.Fg(p.Muted)
		}
		syncCell, deploy := p.Sync(t.Drift), p.Deploy(t.GitHub)
		if eco {
			syncCell, deploy = "", "" // the scaffold itself; released as tags, never deployed
		}
		marker := " " // the selection bar, on the card's first line only
		if sel {
			marker = p.Fg(p.Accent).Render("▌")
			nameStyle = nameStyle.Bold(true).Foreground(p.Accent)
		}
		lastCommit := ""
		if t.Git != nil && !t.Git.LastCommit.IsZero() {
			lastCommit = p.Fg(p.Muted).Render(shortAge(m.now.Sub(t.Git.LastCommit)) + " ago") // the last commit
		}
		top := marker + " " + dot + " " +
			pad(nameStyle.Render(s.Slug), c.card) + sep +
			pad(p.Core(t.Core), c.core) + sep +
			pad(syncCell, c.sync) + sep +
			c.liveCol(liveCell) +
			c.fbCol(fbCell) +
			c.ghCols(p.PRs(t.GitHub), deploy) +
			gitCell(p, t, c.git)
		bottom := "    " +
			pad(m.subline(s, t, sel, c.host), c.card) + sep +
			pad("", c.core) + sep +
			pad("", c.sync) + sep +
			c.liveCol("") +
			c.fbCol(fbAge) +
			c.ghCols("", "") +
			ansi.Truncate(lastCommit, c.git, "…")
		if prog, ok := m.rowProgress(s.ID+"/"+t.Dir, width-4-c.card-ansi.StringWidth(sep)); ok {
			// An update of this theme: its progress takes the rest of the card's second line.
			bottom = "    " + pad(m.subline(s, t, sel, c.host), c.card) + sep + prog
		}
		if sel {
			selAt = len(lines)
			top, bottom = tint(top, width, p.Selected), tint(bottom, width, p.Selected)
		}
		lines = append(lines, top, strings.TrimRight(bottom, " "))
	}
	// The selected card stands out: tinted, with half a line of the tint
	// above and below it, taken from the air between the cards.
	if selAt > 0 && lines[selAt-1] == "" {
		lines[selAt-1] = p.Fg(p.Selected).Render(strings.Repeat("▄", width))
	}
	if selAt > 0 && selAt+2 < len(lines) && lines[selAt+2] == "" {
		lines[selAt+2] = p.Fg(p.Selected).Render(strings.Repeat("▀", width))
	}
	return strings.Join(lines, "\n")
}

// sgr matches one SGR (style) escape sequence.
var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// tint lays bg under a styled line, padded to width. bg goes in again after
// every style sequence, so the cells' own resets don't punch holes in it.
func tint(s string, width int, bg color.Color) string {
	on := ansi.Style{}.BackgroundColor(bg).String()
	return on + sgr.ReplaceAllStringFunc(pad(s, width), func(q string) string { return q + on }) + ansi.ResetStyle
}

// ecosystemRow reports whether the visible row vi is a TAW ecosystem theme.
func (m Model) ecosystemRow(vi int) bool {
	r := m.rows[m.visible[vi]]
	return ecosystem(m.rep.Sites[r.site].Themes[r.theme])
}

// shortAge is an age in one short word: 40m, 5h, 3d.
func shortAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(int(d.Minutes()), 1))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
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

func confirmHint(agent bool, choices int) string {
	if choices > 0 {
		return fmt.Sprintf("  1–%d pick · n no", choices)
	}
	if agent {
		return "  y yes · A let an agent do it · n no"
	}
	return "  y yes · n no"
}

// --- handoff ----------------------------------------------------------------

// handoffScreen shows the agent prompt, with Markdown headings accented.
func (m Model) handoffScreen(height int) string {
	p := m.pal
	muted := p.Fg(p.Muted)
	head := " " + lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render("Hand off to an agent") +
		muted.Render("  ·  "+m.prompt.Title+"  ·  branch "+m.prompt.Branch)
	sub := " " + muted.Render("A opens Claude Code with it in a window beside this one  ·  c copies it")
	rule := p.Fg(p.Faint).Render(strings.Repeat("─", m.width))

	var lines []string
	for _, l := range strings.Split(strings.TrimRight(m.prompt.Text, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "# "):
			l = lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render(strings.TrimPrefix(l, "# "))
		case strings.HasPrefix(l, "## "):
			l = lipgloss.NewStyle().Bold(true).Foreground(p.Brand).Render(strings.TrimPrefix(l, "## "))
		case strings.HasPrefix(l, "```"):
			l = p.Fg(p.Faint).Render(l)
		}
		// Wrap rather than cut: the prompt should be readable before it's sent.
		for _, w := range strings.Split(lipgloss.Wrap(l, max(m.width-4, 20), " /"), "\n") {
			lines = append(lines, "  "+w)
		}
	}
	bodyH := max(height-3, 1)
	from := min(m.scroll, max(len(lines)-bodyH, 0))
	body := strings.Join(lines[from:min(from+bodyH, len(lines))], "\n")
	return block(head+"\n"+sub+"\n"+rule+"\n"+body, m.width, height)
}

// --- help -------------------------------------------------------------------

func (m Model) helpScreen() string {
	p := m.pal
	h := m.help
	h.ShowAll = true
	muted := p.Fg(p.Muted)
	legend := []string{
		p.Dot(site.StatusRunning) + " running   " + p.Dot(site.StatusHalted) + " halted   " + p.Dot(site.StatusBusy) + " starting/stopping   " +
			muted.Render("↗") + " symlink   " + p.Fg(p.Accent).Render("vite") + " Vite is running",
		p.Core(site.CoreInfo{Installed: "v1.59.2", Latest: "v1.76.1", Behind: true}) + "  taw/core installed ▲ newest",
		p.Git(&site.GitInfo{Branch: "main", DefaultBranch: "main", Upstream: "origin/main", Dirty: 3, Ahead: 1, Behind: 2}) +
			"  uncommitted ±, to push ↑, to pull ↓   " + p.Fg(p.Brand).Render("@acct") + " another GitHub account",
		p.Git(&site.GitInfo{Branch: "feature", DefaultBranch: "main"}) + "  not the default branch, not pushed",
		p.Sync(nil) + " " + p.Sync(&site.Drift{}) + " " + p.Sync(&site.Drift{Tier1: []string{"a", "b"}}) + " " + p.Sync(&site.Drift{Errors: []string{"x"}}) +
			"  sync: not checked, matches taw-theme, Tier 1 paths differ, check failed",
		p.Live(&site.Production{Reachable: true, Verified: true}) + " " + p.Live(&site.Production{Reachable: true}) + " " + p.Live(&site.Production{}) + " " + p.Live(nil) +
			"  live: verified, answering but unverified, refused or down, no production URL",
		p.Feedback(&site.Feedback{Open: 3}) + " " +
			p.Feedback(&site.Feedback{Open: 2, Oldest: time.Unix(0, 0), CheckedAt: time.Unix(0, 0).Add(72 * time.Hour)}) + " " +
			p.Feedback(&site.Feedback{}) + " " + p.Feedback(&site.Feedback{Error: "x"}) +
			"  FB: open BugSmash comments, one waiting over 2 days, none open, check failed",
		p.Fg(p.OK).Render("2✓") + " " + p.Fg(p.Err).Render("2✗") + "  open PRs: CI passed, failed   " +
			p.Fg(p.OK).Render("✓") + " " + p.Fg(p.Warn).Render("↑2") + " " + p.Fg(p.Accent).Render("⟳") + " " + p.Fg(p.Err).Render("✗") + "  deploy: current, behind, running, failed",
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
	case m.confirm != "":
		status = " " + lipgloss.NewStyle().Bold(true).Foreground(p.Warn).Render(m.confirm) + muted.Render(confirmHint(m.onAgent != nil, len(m.choices)))
	case m.flash != "" && m.flashErr: // a refusal the user just caused beats the busy line
		status = " " + p.Fg(p.Err).Render("✗ "+m.flash)
	case m.full.active && m.mode != modeOutput:
		status = " " + m.spin.View() + " " + muted.Render("Refreshing everything: "+strings.Join(m.refreshPending(), ", ")+"…")
	case m.task != nil && m.task.running && m.mode != modeOutput:
		status = " " + m.spin.View() + " " + muted.Render(m.task.title+"…"+m.task.stepSummary()+"  (o shows the output)")
	case len(m.busy) > 0:
		var parts []string
		for id, op := range m.busy {
			for _, s := range m.rep.Sites {
				if s.ID == id {
					parts = append(parts, strings.TrimSuffix(string(op), "Site")+" "+s.Slug)
				}
			}
		}
		sort.Strings(parts)
		status = " " + m.spin.View() + " " + muted.Render("Local is working: "+strings.Join(parts, ", ")+"…")
	case m.liveFetching && m.flash == "":
		status = " " + m.spin.View() + " " + muted.Render("checking the production sites…")
	case m.feedbackFetching && m.flash == "":
		status = " " + m.spin.View() + " " + muted.Render("reading the BugSmash comments…")
	case m.flash != "" && m.mode != modeOutput: // the output view shows the result itself
		status = " " + p.Fg(p.OK).Render("✓ "+m.flash)
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
	switch {
	case m.confirm != "":
		keys = confirmKeys{m.keys, m.onAgent != nil, len(m.choices)}
	case m.mode == modeDetail:
		keys = detailKeys{m.keys}
	case m.mode == modeHandoff:
		keys = handoffKeys{m.keys}
	case m.mode == modeOutput:
		keys = outputKeys{m.keys, m.task != nil && m.task.running, m.task != nil && !m.task.running && m.task.summary.Secret != ""}
		if m.task != nil && m.task.steps != nil {
			keys = stepKeys{m.task}
		}
	case m.mode == modeCreate:
		keys = createKeys{}
	case m.mode == modeMenu, m.mode == modeSkills:
		keys = menuKeys{}
	}
	// bubbles' help can overflow when the ellipsis itself doesn't fit: cut it.
	return ansi.Truncate(status, m.width, "…") + "\n" + ansi.Truncate(" "+m.help.View(keys), m.width, "…")
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

// gitCell is the GIT cell, led by @account for a theme of another GitHub
// account.
func gitCell(p style.Palette, t site.Theme, width int) string {
	if t.Account == "" {
		return ansi.Truncate(p.GitFit(t.Git, width), width, "…")
	}
	tag := "@" + t.Account + " "
	rest := max(width-ansi.StringWidth(tag), 4)
	return ansi.Truncate(p.Fg(p.Brand).Render(tag)+p.GitFit(t.Git, rest), width, "…")
}
