// Package tui is the full-screen dashboard: `taw-fleet` with no subcommand.
//
// The model is plain data and View is a pure function of it, so tests drive
// Update with messages and compare View against golden files.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/create"
	"github.com/Relmaur/taw-fleet/internal/createform"
	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/live"
	"github.com/Relmaur/taw-fleet/internal/local"
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
	Agent(ctx context.Context, t site.Theme, p handoff.Prompt) (*exec.Cmd, error)
	SyncTask(s site.Site, t site.Theme, apply bool) (actions.Task, error)
	UpdateTask(s site.Site, t site.Theme) (actions.Task, error)
	CreateTask(r create.Request) (actions.Task, error)
}

// Deps is what the dashboard needs from the outside.
type Deps struct {
	Actions Actions // nil = shortcuts unavailable (e.g. a broken config)
	// SiteOp starts, stops or restarts a site through Local and waits.
	SiteOp     func(ctx context.Context, op local.Op, s site.Site) (time.Duration, error)
	ActionsErr error // why Actions is nil
	Scan       func(ctx context.Context) (scan.Report, error)
	Doctor     func(scan.Report) []site.Finding
	Paths      paths.Paths
	Version    string
	Dark       bool             // first guess; the terminal's answer replaces it
	Now        func() time.Time // nil = time.Now
	Refresh    time.Duration    // re-scan this often; 0 = only on `r`
	// Inline draws on the terminal's normal screen instead of the alternate
	// one, keeping its scrollback empty: for a window of its own, where
	// scrolling up should find nothing behind the dashboard.
	Inline bool

	CreateDefaults config.Create // the config's [create] section, for the n form

	// Live checks the production sites (cached unless fresh). nil = no
	// production view.
	Live func(ctx context.Context, fresh bool) (map[string]site.Production, error)
}

type mode int

const (
	modeTable mode = iota
	modeDetail
	modeHelp
	modeHandoff
	modeOutput
	modeCreate
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

	confirm string                           // question on screen; "" = none
	onYes   func(Model) (tea.Model, tea.Cmd) // what "yes" does
	onAgent func(Model) (tea.Model, tea.Cmd) // what A does instead; nil = not offered
	pending local.Op                         // the site operation asked about, if any

	live         map[string]site.Production // production checks by site slug
	liveFetching bool
	liveAt       time.Time // when the last check finished
	liveErr      error

	task *taskState          // the running or last task (sync, update)
	busy map[string]local.Op // site ID → operation in progress

	form            *huh.Form          // the new-site form (modeCreate)
	fields          *createform.Fields // its answers
	selectAfterScan string             // site slug to select once the next scan lands

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

type siteOpDoneMsg struct {
	id, slug string
	op       local.Op
	took     time.Duration
	err      error
}

// askSiteOp opens the y/N question for start/stop/restart.
func (m Model) askSiteOp(restart bool) (tea.Model, tea.Cmd) {
	s, _, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	if m.deps.SiteOp == nil {
		m.setFlash("starting and stopping sites isn't available here", true)
		return m, nil
	}
	if _, busy := m.busy[s.ID]; busy {
		m.setFlash(s.Slug+" is busy; wait for it to finish", true)
		return m, nil
	}
	op, verb := local.Start, "Start"
	switch {
	case restart:
		op, verb = local.Restart, "Restart"
	case s.Status == site.StatusRunning:
		op, verb = local.Stop, "Stop"
	}
	m.confirm, m.pending = verb+" "+s.Slug+"?", op
	m.onYes = func(m Model) (tea.Model, tea.Cmd) { return m.doSiteOp(s, op) }
	return m, nil
}

// doSiteOp runs a confirmed site operation in the background.
func (m Model) doSiteOp(s site.Site, op local.Op) (tea.Model, tea.Cmd) {
	busy := map[string]local.Op{s.ID: op}
	for id, o := range m.busy {
		busy[id] = o
	}
	m.busy = busy
	fn, ctx := m.deps.SiteOp, m.ctx
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		took, err := fn(ctx, op, s)
		return siteOpDoneMsg{s.ID, s.Slug, op, took, err}
	})
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
	if m.formOwns(msg) {
		return m.onForm(msg)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width - 2)
		m.clamp()
		if m.form != nil {
			m.form = m.form.WithWidth(m.formWidth())
			m = m.sizeForm()
		}
		if m.deps.Inline {
			return m, tea.Raw("\x1b[3J") // a resize can push lines into the scrollback
		}
		return m, nil

	case tea.BackgroundColorMsg:
		m.setDark(msg.IsDark())
		return m, nil

	case scanDoneMsg:
		live.Apply(msg.rep.Sites, m.live)
		m.applyScan(msg.rep, msg.err)
		if m.liveDue() {
			return m.fetchLive(false)
		}
		return m, nil

	case liveDoneMsg:
		m.liveFetching, m.liveAt, m.liveErr = false, m.deps.Now(), msg.err
		if msg.err == nil {
			m.live = msg.results
			live.Apply(m.rep.Sites, m.live)
			m.applyScan(m.rep, nil) // findings include the live.* rules
			if msg.fresh {
				m.setFlash(liveSummary(msg.results), false)
			}
		} else if msg.fresh {
			m.setFlash("production check: "+msg.err.Error(), true)
		}
		return m, nil

	case agentDoneMsg:
		return m.agentDone(msg)

	case siteOpDoneMsg:
		busy := map[string]local.Op{}
		for id, o := range m.busy {
			if id != msg.id {
				busy[id] = o
			}
		}
		m.busy = busy
		if msg.err != nil {
			m.setFlash(msg.slug+": "+msg.err.Error(), true)
		} else {
			m.setFlash(fmt.Sprintf("%s is %s (%s)", msg.slug, msg.op.Target(), msg.took.Round(time.Second)), false)
		}
		if !m.scanning {
			return m, m.startScan()
		}
		return m, nil

	case taskEventMsg:
		return m.onTaskEvent(msg)

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
		if m.liveDue() {
			model, cmd := m.fetchLive(false)
			return model, tea.Batch(tick(), cmd)
		}
		return m, tick()

	case spinner.TickMsg:
		if !m.scanning && !m.liveFetching && len(m.busy) == 0 && (m.task == nil || !m.task.running) {
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
	if m.confirm != "" {
		switch {
		case key.Matches(msg, k.Yes):
			yes := m.onYes
			m.confirm, m.onYes, m.onAgent, m.pending = "", nil, nil, ""
			if yes != nil {
				return yes(m)
			}
		case key.Matches(msg, k.Agent) && m.onAgent != nil:
			agent := m.onAgent
			m.confirm, m.onYes, m.onAgent, m.pending = "", nil, nil, ""
			return agent(m)
		case key.Matches(msg, k.No) || msg.String() == "q":
			m.confirm, m.onYes, m.onAgent, m.pending = "", nil, nil, ""
			m.setFlash("Nothing changed.", false)
		}
		return m, nil
	}
	switch m.mode {
	case modeOutput:
		return m.onOutputKey(msg)
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
		case key.Matches(msg, k.Agent):
			return m.agentWith(m.htheme, m.prompt)
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
		case key.Matches(msg, k.Agent):
			return m.runAgent()
		case key.Matches(msg, k.StartStop):
			return m.askSiteOp(false)
		case key.Matches(msg, k.Restart):
			return m.askSiteOp(true)
		default:
			if model, cmd, ok := m.taskKey(msg); ok {
				return model, cmd
			}
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
	case key.Matches(msg, k.LiveRefresh):
		if m.deps.Live == nil {
			m.setFlash("no production view: add production_url to sites in the config", true)
			return m, nil
		}
		return m.fetchLive(true)
	case key.Matches(msg, k.Help):
		m.mode = modeHelp
	case key.Matches(msg, k.New):
		return m.openCreate()
	case key.Matches(msg, k.Handoff):
		return m.openHandoff()
	case key.Matches(msg, k.Agent):
		return m.runAgent()
	case key.Matches(msg, k.StartStop):
		return m.askSiteOp(false)
	case key.Matches(msg, k.Restart):
		return m.askSiteOp(true)
	default:
		if model, cmd, ok := m.taskKey(msg); ok {
			return model, cmd
		}
		if kind, ok := m.shortcut(msg); ok {
			return m.act(kind)
		}
	}
	return m, nil
}

// taskKey handles y, S, u and o in the table and detail views.
func (m Model) taskKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	k := m.keys
	switch {
	case key.Matches(msg, k.SyncCheck):
		model, cmd := m.syncOrUpdate("check")
		return model, cmd, true
	case key.Matches(msg, k.SyncApply):
		model, cmd := m.syncOrUpdate("apply")
		return model, cmd, true
	case key.Matches(msg, k.UpdateCore):
		model, cmd := m.syncOrUpdate("update")
		return model, cmd, true
	case key.Matches(msg, k.Output):
		if m.task == nil {
			m.setFlash("nothing has run yet (y checks the scaffold, u updates taw/core)", false)
		} else {
			m.mode = modeOutput
		}
		return m, nil, true
	}
	return m, nil, false
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
	if m.selectAfterScan != "" { // a site was just created: select it
		for vi, ri := range m.visible {
			if rep.Sites[m.rows[ri].site].Slug == m.selectAfterScan {
				m.cursor, m.selectAfterScan = vi, ""
				break
			}
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
