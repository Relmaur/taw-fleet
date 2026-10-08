// Package tui is the full-screen dashboard: `taw-fleet` with no subcommand.
//
// The model is plain data and View is a pure function of it, so tests drive
// Update with messages and compare View against golden files.
package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
)

// Actions are the shortcuts and the agent handoff (internal/actions).
type Actions interface {
	Do(ctx context.Context, k actions.Kind, s site.Site, t site.Theme) (string, error)
	Handoff(s site.Site, t site.Theme, findings []site.Finding) (handoff.Prompt, error)
	Copy(ctx context.Context, text string) error
	Launch(ctx context.Context, s site.Site, t site.Theme, p handoff.Prompt) (string, error)
}

// Deps is what the dashboard needs from the outside.
type Deps struct {
	Actions    Actions // nil = shortcuts unavailable (e.g. a broken config)
	ActionsErr error   // why Actions is nil
	Scan       func(ctx context.Context) (scan.Report, error)
	Doctor     func(scan.Report) []site.Finding
	Paths      paths.Paths
	Version    string
	Dark       bool             // first guess; the terminal's answer replaces it
	Now        func() time.Time // nil = time.Now
	Refresh    time.Duration    // re-scan this often; 0 = only on `r`
}

type mode int

const (
	modeTable mode = iota
	modeDetail
	modeHelp
	modeHandoff
)

// row is one line of the table: a TAW theme of a site.
type row struct{ site, theme int }

// Model is the dashboard state.
type Model struct {
	deps Deps
	ctx  context.Context
	keys keyMap
	pal  style.Palette
	dark bool

	width, height int
	now           time.Time

	rep      scan.Report
	findings map[string][]site.Finding // by site ID; "" = not about one site
	loaded   bool
	scanning bool
	err      error

	rows    []row
	visible []int // indexes into rows that match the filter
	cursor  int   // index into visible
	offset  int   // first visible row on screen
	mode    mode
	scroll  int // detail screen scroll

	flash    string // last action's result, shown in the footer
	flashErr bool
	flashAt  time.Time

	prompt handoff.Prompt // the handoff on screen (modeHandoff)
	hsite  site.Site
	htheme site.Theme

	filtering bool
	filter    textinput.Model
	spin      spinner.Model
	help      help.Model
}

// New builds the dashboard. Nothing runs until Init.
func New(ctx context.Context, d Deps) Model {
	if d.Now == nil {
		d.Now = time.Now
	}
	m := Model{deps: d, ctx: ctx, keys: newKeyMap(), now: d.Now()}
	m.filter = textinput.New()
	m.filter.Prompt = "/ "
	m.filter.Placeholder = "site, theme, branch, version, or: behind, dirty, unpushed, running"
	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot))
	m.help = help.New()
	m.setDark(d.Dark)
	return m
}

func (m *Model) setDark(dark bool) {
	m.dark = dark
	m.pal = style.New(dark)
	m.help.Styles = help.DefaultStyles(dark)
	m.help.Styles.ShortKey = lipgloss.NewStyle().Foreground(m.pal.Accent)
	m.help.Styles.FullKey = m.help.Styles.ShortKey
	m.filter.SetStyles(textinput.DefaultStyles(dark))
	m.spin.Style = lipgloss.NewStyle().Foreground(m.pal.Accent)
}

type scanDoneMsg struct {
	rep scan.Report
	err error
}

type tickMsg time.Time

type actionDoneMsg struct {
	msg string
	err error
}

// flashFor is how long an action's message stays in the footer.
const flashFor = 6 * time.Second

func (m *Model) setFlash(msg string, isErr bool) {
	m.flash, m.flashErr, m.flashAt = msg, isErr, m.deps.Now()
}

// run does an action in the background and reports back.
func (m Model) run(f func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		msg, err := f()
		return actionDoneMsg{msg, err}
	}
}

// shortcut maps a key to an action kind.
func (m Model) shortcut(msg tea.KeyPressMsg) (actions.Kind, bool) {
	k := m.keys
	for _, s := range []struct {
		b    key.Binding
		kind actions.Kind
	}{
		{k.Editor, actions.Editor}, {k.Finder, actions.Finder}, {k.Browser, actions.Browser}, {k.Admin, actions.Admin},
		{k.GitHub, actions.GitHub}, {k.PRs, actions.PRs}, {k.Terminal, actions.Terminal}, {k.Production, actions.Production},
	} {
		if key.Matches(msg, s.b) {
			return s.kind, true
		}
	}
	return "", false
}

// act runs a shortcut on the selected theme.
func (m Model) act(kind actions.Kind) (tea.Model, tea.Cmd) {
	s, t, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	if m.deps.Actions == nil {
		m.setFlash("shortcuts unavailable: "+errText(m.deps.ActionsErr), true)
		return m, nil
	}
	a, ctx := m.deps.Actions, m.ctx
	return m, m.run(func() (string, error) { return a.Do(ctx, kind, s, t) })
}

// openHandoff builds the prompt for the selected theme and shows it.
func (m Model) openHandoff() (tea.Model, tea.Cmd) {
	s, t, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	if m.deps.Actions == nil {
		m.setFlash("handoff unavailable: "+errText(m.deps.ActionsErr), true)
		return m, nil
	}
	p, err := m.deps.Actions.Handoff(s, t, m.findings[s.ID])
	if err != nil {
		m.setFlash(t.Dir+": "+err.Error(), true)
		return m, nil
	}
	m.prompt, m.hsite, m.htheme = p, s, t
	m.mode, m.scroll = modeHandoff, 0
	return m, nil
}

func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) startScan() tea.Cmd {
	m.scanning = true
	ctx, scanFn := m.ctx, m.deps.Scan
	return tea.Batch(m.spin.Tick, func() tea.Msg {
		rep, err := scanFn(ctx)
		return scanDoneMsg{rep, err}
	})
}

// Init starts the first scan and asks the terminal for its background.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.startScan(), tea.RequestBackgroundColor, tick())
}

// Update handles one message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width - 2)
		m.clamp()
		return m, nil

	case tea.BackgroundColorMsg:
		m.setDark(msg.IsDark())
		return m, nil

	case scanDoneMsg:
		m.applyScan(msg.rep, msg.err)
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.setFlash(msg.err.Error(), true)
		} else {
			m.setFlash(msg.msg, false)
		}
		return m, nil

	case tickMsg:
		m.now = time.Time(msg)
		if m.flash != "" && m.now.Sub(m.flashAt) > flashFor {
			m.flash = ""
		}
		if m.deps.Refresh > 0 && !m.scanning && m.loaded && m.now.Sub(m.rep.ScannedAt) >= m.deps.Refresh {
			return m, tea.Batch(tick(), m.startScan())
		}
		return m, tick()

	case spinner.TickMsg:
		if !m.scanning {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m Model) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.filtering {
		switch msg.String() {
		case "esc":
			m.filtering = false
			m.filter.Blur()
			m.filter.SetValue("")
			m.applyFilter()
			return m, nil
		case "enter", "down", "up":
			m.filtering = false
			m.filter.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.applyFilter()
		return m, cmd
	}

	k := m.keys
	switch m.mode {
	case modeHandoff:
		switch {
		case key.Matches(msg, k.Back) || msg.String() == "q":
			m.mode, m.scroll = modeTable, 0
		case key.Matches(msg, k.Copy):
			a, ctx, text, dir := m.deps.Actions, m.ctx, m.prompt.Text, m.htheme.Dir
			return m, m.run(func() (string, error) {
				if err := a.Copy(ctx, text); err != nil {
					return "", err
				}
				return "Copied the handoff prompt for " + dir + ". Paste it into your agent.", nil
			})
		case key.Matches(msg, k.Launch):
			a, ctx, s, t, p := m.deps.Actions, m.ctx, m.hsite, m.htheme, m.prompt
			m.mode, m.scroll = modeTable, 0
			return m, m.run(func() (string, error) { return a.Launch(ctx, s, t, p) })
		case key.Matches(msg, k.Up):
			m.scroll = max(0, m.scroll-1)
		case key.Matches(msg, k.Down):
			m.scroll++
		case key.Matches(msg, k.PageUp):
			m.scroll = max(0, m.scroll-m.bodyHeight())
		case key.Matches(msg, k.PageDown):
			m.scroll += m.bodyHeight()
		}
		return m, nil
	case modeHelp:
		if key.Matches(msg, k.Quit) && msg.String() == "q" || key.Matches(msg, k.Help, k.Back) {
			m.mode = modeTable
		}
		return m, nil
	case modeDetail:
		switch {
		case key.Matches(msg, k.Quit):
			return m, tea.Quit
		case key.Matches(msg, k.Back, k.Detail):
			m.mode, m.scroll = modeTable, 0
		case key.Matches(msg, k.Up):
			m.scroll = max(0, m.scroll-1)
		case key.Matches(msg, k.Down):
			m.scroll++
		case key.Matches(msg, k.PageUp):
			m.scroll = max(0, m.scroll-m.bodyHeight())
		case key.Matches(msg, k.PageDown):
			m.scroll += m.bodyHeight()
		case key.Matches(msg, k.Help):
			m.mode = modeHelp
		case key.Matches(msg, k.Handoff):
			return m.openHandoff()
		default:
			if kind, ok := m.shortcut(msg); ok {
				return m.act(kind)
			}
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	case key.Matches(msg, k.Up):
		m.move(-1)
	case key.Matches(msg, k.Down):
		m.move(1)
	case key.Matches(msg, k.PageUp):
		m.move(-m.tableRows())
	case key.Matches(msg, k.PageDown):
		m.move(m.tableRows())
	case key.Matches(msg, k.Top):
		m.move(-len(m.visible))
	case key.Matches(msg, k.Bottom):
		m.move(len(m.visible))
	case key.Matches(msg, k.Detail):
		if _, ok := m.selected(); ok {
			m.mode, m.scroll = modeDetail, 0
		}
	case key.Matches(msg, k.Back):
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.applyFilter()
		}
	case key.Matches(msg, k.Filter):
		m.filtering = true
		return m, m.filter.Focus()
	case key.Matches(msg, k.Refresh):
		if !m.scanning {
			return m, m.startScan()
		}
	case key.Matches(msg, k.Help):
		m.mode = modeHelp
	case key.Matches(msg, k.Handoff):
		return m.openHandoff()
	default:
		if kind, ok := m.shortcut(msg); ok {
			return m.act(kind)
		}
	}
	return m, nil
}

// applyScan installs a new report, keeping the selected theme selected.
func (m *Model) applyScan(rep scan.Report, err error) {
	m.scanning = false
	m.err = err
	if err != nil {
		return
	}
	prevSite, prevTheme := "", ""
	if s, t, ok := m.selectedTheme(); ok {
		prevSite, prevTheme = s.ID, t.Dir
	}

	m.rep = rep
	m.loaded = true
	m.now = m.deps.Now()
	m.findings = map[string][]site.Finding{}
	if m.deps.Doctor != nil {
		for _, f := range m.deps.Doctor(rep) {
			m.findings[f.SiteID] = append(m.findings[f.SiteID], f)
		}
	}
	m.rows = m.rows[:0]
	for si, s := range rep.Sites {
		for ti, t := range s.Themes {
			if t.IsTAW {
				m.rows = append(m.rows, row{si, ti})
			}
		}
	}
	m.applyFilter()
	for vi, ri := range m.visible {
		r := m.rows[ri]
		if rep.Sites[r.site].ID == prevSite && rep.Sites[r.site].Themes[r.theme].Dir == prevTheme {
			m.cursor = vi
		}
	}
	m.clamp()
}

func (m *Model) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.visible = m.visible[:0]
	for i, r := range m.rows {
		if q == "" || m.matches(r, q) {
			m.visible = append(m.visible, i)
		}
	}
	m.clamp()
}

func (m Model) matches(r row, q string) bool {
	s := m.rep.Sites[r.site]
	t := s.Themes[r.theme]
	hay := []string{s.Slug, s.Name, s.Domain, t.Dir, string(t.Kind), string(s.Status), t.Core.Installed}
	if t.Core.Behind {
		hay = append(hay, "behind")
	}
	if g := t.Git; g != nil {
		hay = append(hay, g.Branch)
		if g.Dirty > 0 {
			hay = append(hay, "dirty", "uncommitted")
		}
		if !g.HasUpstream() || g.Ahead > 0 {
			hay = append(hay, "unpushed")
		}
	}
	for _, h := range hay {
		if strings.Contains(strings.ToLower(h), q) {
			return true
		}
	}
	return false
}

func (m *Model) move(delta int) {
	m.cursor += delta
	m.clamp()
}

// clamp keeps the cursor on a row and the row on screen.
func (m *Model) clamp() {
	if len(m.visible) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	m.cursor = min(max(m.cursor, 0), len(m.visible)-1)
	rows := m.tableRows()
	if rows < 1 {
		rows = 1
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	m.offset = min(max(m.offset, 0), max(len(m.visible)-rows, 0))
}

func (m Model) selected() (row, bool) {
	if len(m.visible) == 0 {
		return row{}, false
	}
	return m.rows[m.visible[m.cursor]], true
}

func (m Model) selectedTheme() (site.Site, site.Theme, bool) {
	r, ok := m.selected()
	if !ok {
		return site.Site{}, site.Theme{}, false
	}
	s := m.rep.Sites[r.site]
	return s, s.Themes[r.theme], true
}

// Run opens the dashboard and returns when the user quits.
func Run(ctx context.Context, d Deps) error {
	_, err := tea.NewProgram(New(ctx, d), tea.WithContext(ctx)).Run()
	return err
}
