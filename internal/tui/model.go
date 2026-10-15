// Package tui is the full-screen dashboard: `taw-fleet` with no subcommand.
//
// The model is plain data and View is a pure function of it, so tests drive
// Update with messages and compare View against golden files.
package tui

import (
	"context"
	"errors"
	"fmt"
	osexec "os/exec"
	"sort"
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
	"github.com/Relmaur/taw-fleet/internal/agent"
	"github.com/Relmaur/taw-fleet/internal/bugsmash"
	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/create"
	"github.com/Relmaur/taw-fleet/internal/createform"
	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/live"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/notes"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/report"
	"github.com/Relmaur/taw-fleet/internal/restore"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
)

// Actions are the shortcuts and the agent handoff (internal/actions).
type Actions interface {
	Do(ctx context.Context, k actions.Kind, s site.Site, t site.Theme) (string, error)
	Handoff(s site.Site, t site.Theme, findings []site.Finding) (handoff.Prompt, error)
	Copy(ctx context.Context, text string) error
	Launch(ctx context.Context, s site.Site, t site.Theme, p handoff.Prompt, beside string) (actions.Launched, error)
	PlanFleet(ctx context.Context, sites []site.Site) actions.FleetPlan
	PlanUpdateAll(ctx context.Context, sites []site.Site) actions.FleetPlan
	UpdateAllTask(plan actions.FleetPlan) (actions.Task, error)
	LaunchFleet(ctx context.Context, plan actions.FleetPlan, findings map[string][]site.Finding, beside string) (actions.Launched, error)
	SyncTask(s site.Site, t site.Theme, apply bool) (actions.Task, error)
	SyncAllTask(sites []site.Site) (actions.Task, error)
	UpdateTask(s site.Site, t site.Theme) (actions.Task, error)
	FixUpdate(ctx context.Context, o actions.UpdateOutcome, beside string) (actions.Launched, error)
	OpenGuide(ctx context.Context, o actions.UpdateOutcome) (string, error)
	OpenURL(ctx context.Context, url string) (string, error)
	WorkTask(s site.Site, t site.Theme, op actions.SiteOp) (actions.Task, error)
	StopWorkTask(s site.Site, t site.Theme, op actions.SiteOp) (actions.Task, error)
	MergeTask(s site.Site, t site.Theme, pr site.PullRequest) (actions.Task, error)
	SiteBranchTask(s site.Site, t site.Theme) (actions.Task, error)
	PullTask(s site.Site, t site.Theme, op actions.SiteOp) (actions.Task, error)
	PublishTask(s site.Site, t site.Theme, op actions.SiteOp) (actions.Task, error)
	PublishUndoTask(s site.Site, journal string) (actions.Task, error)
	PublishHistory(slug string) []actions.Published
	CreateTask(r create.Request) (actions.Task, error)
	OpenComments(ctx context.Context, s site.Site) (string, error)
	ResolvePrompt(s site.Site, t site.Theme) (handoff.Prompt, error)
	SkillPrompt(s site.Site, t site.Theme, sk actions.Skill, findings []site.Finding) handoff.Prompt
	LaunchSkill(ctx context.Context, s site.Site, t site.Theme, kind string, p handoff.Prompt, beside string) (actions.Launched, error)
	Restores() restore.Store
	RestoreTask(ctx context.Context, s site.Site, t site.Theme, p restore.Point) (actions.Task, error)
	RepoTask(s site.Site, t site.Theme, owner, name string) (actions.Task, error)
	RepoOwner() string
}

// Deps is what the dashboard needs from the outside.
type Deps struct {
	Actions Actions // nil = shortcuts unavailable (e.g. a broken config)
	// SiteOp starts, stops or restarts a site through Local and waits.
	SiteOp func(ctx context.Context, op local.Op, s site.Site) (time.Duration, error)
	// EndLeftovers ends Local's leftover processes in a site's way (after
	// a *local.LeftoversError) and says which are still there.
	EndLeftovers func(ctx context.Context, l local.Leftovers) (local.Leftovers, error)
	ActionsErr   error // why Actions is nil
	Scan         func(ctx context.Context) (scan.Report, error)
	// ScanFresh is Scan asking GitHub for the latest versions now, past the
	// hour-long cache (ctrl+r). nil = Scan.
	ScanFresh func(ctx context.Context) (scan.Report, error)
	Doctor    func(scan.Report) []site.Finding
	Paths     paths.Paths
	Version   string
	Dark      bool             // first guess; the terminal's answer replaces it
	Icons     string           // the config's icon set ("symbols", "nerd"); "" = symbols
	Now       func() time.Time // nil = time.Now
	Refresh   time.Duration    // re-scan this often; 0 = only on `r`
	// Inline draws on the terminal's normal screen instead of the alternate
	// one, keeping its scrollback empty: for a window of its own, where
	// scrolling up should find nothing behind the dashboard.
	Inline bool
	// ReportTask makes a site's monthly report ("2026-10") and opens it;
	// ReportDir is where reports are saved. nil = no m.
	ReportTask func(s site.Site, month string) actions.Task
	// LaunchTask checks a live site's production and opens its launch report
	// (I once the deploy is set up); nil hides it.
	LaunchTask func(s site.Site, theme string) actions.Task
	ReportDir  string
	// Select is the site to open the dashboard on, its details showing: a
	// folder, name or domain ("" = the first).
	Select string
	// Production is the sites (by folder) with a production URL in the
	// config: they're listed first, and `/live` filters to them.
	Production map[string]bool
	TTY        string // the dashboard's terminal, to put Claude's window beside it; "" = unknown

	CreateDefaults config.Create // the config's [create] section, for the n form

	// GitHub reads the repositories' pull requests and deploys. nil = no
	// PR and DEPLOY columns.
	GitHub func(ctx context.Context, repos []string) map[string]site.RepoState

	// Live checks the production sites (cached unless fresh). nil = no
	// production view.
	Live func(ctx context.Context, fresh bool) (map[string]site.Production, error)

	// Feedback reads the open BugSmash comments (cached unless fresh). nil =
	// no FB column.
	Feedback func(ctx context.Context, fresh bool) (map[string]site.Feedback, error)

	// Chat starts Claude Code for the chat (>), resuming a session when
	// resume isn't "". nil = no chat (Claude Code isn't installed).
	Chat func(ctx context.Context, sc agent.Scope, resume string) (*agent.Session, error)
	// ChatWrites are taw-fleet's MCP write tools: called without their
	// token they only return the question, so the chat lets them through.
	ChatWrites map[string]bool
	// Settings is a site's settings (production URL, BugSmash project…), for
	// the SETUP checklist. nil = none.
	Settings func(slug string) config.Site
	// SaveTags sets a site's tags in the settings (#). nil = read-only.
	SaveTags func(slug string, tags []string) error
	// GroupBy is the list's grouping from the settings; SaveGroupBy keeps a
	// new one there (=). See groups.go.
	GroupBy     string
	SaveGroupBy func(string) error
	// OpenAtLogin is the settings' open_at_login; SaveOpenAtLogin sets it
	// and adds or removes the login agent (the : menu's Settings). nil =
	// read-only.
	OpenAtLogin     bool
	SaveOpenAtLogin func(bool) error
	// Notes reads a site's notes, newest first; AddNote writes one from the
	// person (N). nil = none.
	Notes   func(slug string) ([]notes.Entry, error)
	AddNote func(slug, text string) error
	// HasBot says whether the site's bot is in the Keychain (never reading
	// the password). nil = unknown.
	HasBot func(ctx context.Context, slug string) bool
	// DeploySetup is the deploy wizard (taw-fleet deploy setup <site>) for
	// the terminal to run while the dashboard waits. nil = none.
	DeploySetup func(slug, theme string) *osexec.Cmd
	// FleetSkills are the fleet folder's skills: the a picker lists them,
	// and one runs in the fleet's chat.
	FleetSkills []actions.Skill
}

type mode int

const (
	modeTable mode = iota
	modeDetail
	modeHelp
	modeHandoff
	modeOutput
	modeCreate
	modeMenu
	modeSkills
	modeChat
)

// row is one line of the table: a TAW theme of a site.
type row struct{ site, theme int }

// ecosystem reports whether the theme is one of the TAW ecosystem's own
// repositories (the umbrella's taw-theme or taw-gutenberg): the scaffolds
// client themes are made from, not a client site.
func ecosystem(t site.Theme) bool { return handoff.IsUmbrella(t) }

// ecosystemSite reports whether every TAW theme of the site is one.
func ecosystemSite(s site.Site) bool {
	themes := s.TAWThemes()
	for _, t := range themes {
		if !ecosystem(t) {
			return false
		}
	}
	return len(themes) > 0
}

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

	flash      string // last action's result, shown in the footer
	flashErr   bool
	filterHint string // the filter's terms it didn't understand

	entries     []entry         // the list as shown: the visible rows, under group headers when grouped
	groupBy     string          // "" | tag | kind | production | prefix:<p> (=)
	openAtLogin bool            // open_at_login (the : menu's Settings)
	folded      map[string]bool // group key → folded (←/→)
	popup       *popup          // the = or T popup, over the list
	tagInput    textinput.Model // the tag popup's line for new tags
	noteInput   textinput.Model // the note popup's line (N)
	flashTip    bool            // a hint, not a result: muted, no ✓
	flashAt     time.Time

	confirm string                             // question on screen; "" = none
	onYes   func(Model) (tea.Model, tea.Cmd)   // what "yes" does
	onAgent func(Model) (tea.Model, tea.Cmd)   // what A does instead; nil = not offered
	choices []func(Model) (tea.Model, tea.Cmd) // a numbered question: what each digit does
	pending local.Op                           // the site operation asked about, if any

	live         map[string]site.Production // production checks by site slug
	liveFetching bool
	liveAt       time.Time // when the last check finished
	liveErr      error

	feedback         map[string]site.Feedback // open BugSmash comments by site slug
	feedbackFetching bool
	feedbackAt       time.Time
	feedbackErr      error

	full fullRefresh // a ctrl+r in progress

	repos         map[string]site.RepoState // pull requests and deploys by owner/name
	reposFetching bool
	reposAt       time.Time
	followed      map[string]following   // merged pull requests whose deploy is watched, by repo
	merges        map[string]*mergeState // M, by "siteID/themeDir": running, or ended lately

	restores map[string][]restore.Point // restore points by site slug, newest first
	notes    map[string][]notes.Entry   // each site's notes, newest first (N, NOTES)
	reports  map[string][]report.Saved  // saved monthly reports by site slug, newest first
	// publishes are each production site's publishes, newest first.
	publishes map[string][]actions.Published
	bots      map[string]bool // production sites whose bot is in the Keychain

	task *taskState          // the running or last task (sync, update)
	busy map[string]local.Op // site ID → operation in progress

	form            *huh.Form          // the new-site form (modeCreate)
	fields          *createform.Fields // its answers
	selectAfterScan string             // site slug to select once the next scan lands
	detailAfterScan bool               // and show its details (Deps.Select)

	agents map[string]string // Claude Code windows still open: done file → theme dir

	prompt handoff.Prompt // the handoff on screen (modeHandoff)
	hsite  site.Site
	htheme site.Theme

	menuInput  textinput.Model // the : menu's filter
	menuCursor int
	menuFrom   mode            // where the menu goes back to
	skills     []actions.Skill // the skills the a picker offers
	recent     []string        // the : menu's last-run action keys

	chats    map[string]*chatState // conversations by scope ("fleet", "slug/theme")
	chat     string                // the one on screen (modeChat)
	chatFrom mode                  // where esc goes back to

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
	m.selectAfterScan, m.detailAfterScan = d.Select, d.Select != ""
	m.filter = textinput.New()
	m.filter.Prompt = "/ "
	m.menuInput = textinput.New()
	m.menuInput.Prompt = ": "
	m.menuInput.Placeholder = "type to find an action"
	m.filter.Placeholder = "words, -word, #tag, is:behind, has:comments, kind:block, core:<1.95 (all must match)"
	m.tagInput = textinput.New()
	m.tagInput.Placeholder = "type tags, then enter"
	m.noteInput = textinput.New()
	m.noteInput.Prompt = "note: "
	m.noteInput.Placeholder = "what happened, what was decided; enter saves"
	m.noteInput.CharLimit = notes.MaxText
	m.groupBy = d.GroupBy
	m.openAtLogin = d.OpenAtLogin
	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot))
	m.help = help.New()
	m.setDark(d.Dark)
	return m
}

func (m *Model) setDark(dark bool) {
	m.dark = dark
	m.pal = style.New(dark).WithIcons(m.deps.Icons)
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
	site site.Site
	op   local.Op
	took time.Duration
	err  error
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
	m.busyWith(s.ID, op)
	fn, ctx := m.deps.SiteOp, m.ctx
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		took, err := fn(ctx, op, s)
		return siteOpDoneMsg{s, op, took, err}
	})
}

// flashFor is how long an action's message stays in the footer.
const flashFor = 6 * time.Second

func (m *Model) setFlash(msg string, isErr bool) {
	m.flash, m.flashErr, m.flashTip, m.flashAt = msg, isErr, false, m.deps.Now()
}

// setTip is a footer hint: how to do something, not the result of an action.
func (m *Model) setTip(msg string) {
	m.setFlash(msg, false)
	m.flashTip = true
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

func (m *Model) startScan() tea.Cmd { return m.scanWith(m.deps.Scan) }

// freshScan is the scan that skips the latest-version cache.
func (m Model) freshScan() func(context.Context) (scan.Report, error) {
	if m.deps.ScanFresh != nil {
		return m.deps.ScanFresh
	}
	return m.deps.Scan
}

func (m *Model) scanWith(scanFn func(context.Context) (scan.Report, error)) tea.Cmd {
	m.scanning = true
	ctx := m.ctx
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
		for _, c := range m.chats {
			c.input.SetWidth(max(msg.Width-4, 10))
		}
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
		bugsmash.Apply(msg.rep.Sites, m.feedback)
		github.ApplyRepos(msg.rep.Sites, m.repos)
		m.applyScan(msg.rep, msg.err)
		var cmds []tea.Cmd
		var syncCmd tea.Cmd
		if m, syncCmd = m.afterRefreshScan(); syncCmd != nil {
			cmds = append(cmds, syncCmd)
		}
		if m.liveDue() {
			model, cmd := m.fetchLive(false)
			m, cmds = model.(Model), append(cmds, cmd)
		}
		if m.feedbackDue() {
			model, cmd := m.fetchFeedback(false)
			m, cmds = model.(Model), append(cmds, cmd)
		}
		if m.reposDue() {
			model, cmd := m.fetchRepos(false)
			m, cmds = model.(Model), append(cmds, cmd)
		}
		cmds = append(cmds, m.loadRestores(), m.loadReports(), m.loadBots(), m.loadPublishes(), m.loadNotes())
		return m, tea.Batch(cmds...)

	case notesMsg:
		m.notes = msg
		return m, nil

	case restoresMsg:
		m.restores = msg
		return m, nil

	case reportsMsg:
		m.reports = msg
		return m, nil

	case publishesMsg:
		m.publishes = msg
		return m, nil

	case deploySetupDoneMsg:
		return m.onDeploySetupDone(msg)

	case botsMsg:
		m.bots = msg
		return m, nil

	case reposDoneMsg:
		return m.onRepos(msg)

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

	case feedbackDoneMsg:
		return m.onFeedback(msg)

	case fleetPlannedMsg:
		return m.fleetPlanned(msg.plan)

	case launchedMsg:
		if msg.err != nil {
			m.setFlash(msg.dir+": "+msg.err.Error(), true)
			return m, nil
		}
		if m.agents == nil {
			m.agents = map[string]string{}
		}
		m.agents[msg.l.Done] = msg.dir
		m.setFlash(msg.l.Message, false)
		return m, nil

	case siteOpDoneMsg:
		m.notBusy(msg.site.ID)
		var le *local.LeftoversError
		switch {
		case errors.As(msg.err, &le):
			m = m.askLeftovers(msg.site, le)
		case msg.err != nil:
			m.setFlash(msg.site.Slug+": "+msg.err.Error(), true)
		default:
			m.setFlash(fmt.Sprintf("%s is %s (%s)", msg.site.Slug, msg.op.Target(), msg.took.Round(time.Second)), false)
		}
		if !m.scanning {
			return m, m.startScan()
		}
		return m, nil

	case leftoversEndedMsg:
		return m.onLeftoversEnded(msg)

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
		m = m.finishRefresh()
		if len(m.agents) > 0 {
			var cmd tea.Cmd
			if m, cmd = m.agentFinished(); cmd != nil {
				return m, tea.Batch(tick(), cmd)
			}
		}
		if m.deps.Refresh > 0 && !m.scanning && m.loaded && m.now.Sub(m.rep.ScannedAt) >= m.deps.Refresh {
			return m, tea.Batch(tick(), m.startScan())
		}
		cmds := []tea.Cmd{tick()}
		if m.liveDue() {
			model, cmd := m.fetchLive(false)
			m, cmds = model.(Model), append(cmds, cmd)
		}
		if m.feedbackDue() {
			model, cmd := m.fetchFeedback(false)
			m, cmds = model.(Model), append(cmds, cmd)
		}
		if m.reposDue() {
			model, cmd := m.fetchRepos(false)
			m, cmds = model.(Model), append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case mergeLineMsg:
		return m, listenMerge(msg.ch)

	case mergeDoneMsg:
		model, cmd := m.onMergeDone(msg)
		return model, tea.Batch(cmd, model.(Model).loadNotes()) // the merge noted itself

	case chatStartedMsg:
		return m.onChatStarted(msg)

	case chatEventMsg:
		return m.onChatEvent(msg)

	case spinner.TickMsg:
		if !m.scanning && !m.liveFetching && !m.feedbackFetching && !m.reposFetching && !m.full.active && len(m.busy) == 0 && (m.task == nil || !m.task.running) && len(m.runningMerges()) == 0 && !m.chatWorking() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		return m.onKey(msg)

	case tea.PasteMsg:
		return m.onPaste(msg)
	}
	return m, nil
}

// onPaste puts pasted text where typing goes: the chat's input, or the filter.
func (m Model) onPaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch {
	case m.mode == modeChat:
		if c := m.currentChat(); c != nil && (c.ask == nil || c.pick != nil && c.pick.typing) {
			c.input, cmd = c.input.Update(msg)
		}
	case m.popup != nil && m.popup.kind == popTags:
		m.tagInput, cmd = m.tagInput.Update(msg)
	case m.popup != nil && m.popup.kind == popNote:
		m.noteInput, cmd = m.noteInput.Update(msg)
	case m.filtering:
		m.filter, cmd = m.filter.Update(msg)
		m.applyFilter()
	}
	return m, cmd
}

func (m Model) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeChat {
		return m.onChatKey(msg)
	}
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.popup != nil {
		return m.onPopupKey(msg)
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
		if len(m.choices) > 0 {
			if n := int(msg.Code - '0'); msg.Text != "" && n >= 1 && n <= len(m.choices) {
				pick := m.choices[n-1]
				m.confirm, m.onYes, m.onAgent, m.choices, m.pending = "", nil, nil, nil, ""
				return pick(m)
			}
			if key.Matches(msg, k.No) || msg.String() == "q" {
				m.confirm, m.onYes, m.onAgent, m.choices, m.pending = "", nil, nil, nil, ""
				m.setFlash("Nothing changed.", false)
			}
			return m, nil
		}
		switch {
		case key.Matches(msg, k.Yes):
			yes := m.onYes
			m.confirm, m.onYes, m.onAgent, m.choices, m.pending = "", nil, nil, nil, ""
			if yes != nil {
				return yes(m)
			}
		case key.Matches(msg, k.Agent) && m.onAgent != nil:
			agent := m.onAgent
			m.confirm, m.onYes, m.onAgent, m.choices, m.pending = "", nil, nil, nil, ""
			return agent(m)
		case key.Matches(msg, k.No) || msg.String() == "q":
			m.confirm, m.onYes, m.onAgent, m.choices, m.pending = "", nil, nil, nil, ""
			m.setFlash("Nothing changed.", false)
		}
		return m, nil
	}
	switch m.mode {
	case modeMenu, modeSkills:
		return m.onPaletteKey(msg)
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
		case key.Matches(msg, k.Agent):
			return m.agentWith(m.hsite, m.htheme, m.prompt)
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
		case key.Matches(msg, k.Menu):
			return m.openMenu()
		case key.Matches(msg, k.Agent):
			return m.runAgent()
		case key.Matches(msg, k.Chat):
			return m.openChat()
		case key.Matches(msg, k.Setup):
			return m.setupNext()
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

	// On a group's header only the list's own keys work; a site's need a site.
	if m.onHeader() && !key.Matches(msg, k.Quit, k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom, k.Detail, k.Back,
		k.Filter, k.Refresh, k.RefreshAll, k.LiveRefresh, k.Help, k.Menu, k.New, k.Chat, k.UpdateAll, k.SyncAll, k.Output,
		k.Group, k.Fold, k.Unfold) {
		m.setTip("That's the " + m.groupAt().title + " group: pick a site in it (← folds the group, → opens it).")
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
		m.move(-len(m.entries))
	case key.Matches(msg, k.Bottom):
		m.move(len(m.entries))
	case key.Matches(msg, k.Detail):
		if m.onHeader() {
			m.fold(!m.folded[m.groupAt().key])
		} else if _, ok := m.selected(); ok {
			m.mode, m.scroll = modeDetail, 0
		}
	case key.Matches(msg, k.Fold):
		m.fold(true)
	case key.Matches(msg, k.Unfold):
		m.fold(false)
	case key.Matches(msg, k.Group):
		return m.openGroupBy()
	case key.Matches(msg, k.Tags):
		return m.openTags()
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
	case key.Matches(msg, k.RefreshAll):
		return m.refreshAll()
	case key.Matches(msg, k.LiveRefresh):
		if m.deps.Live == nil && m.deps.GitHub == nil && m.deps.Feedback == nil {
			m.setFlash("no production view: add production_url to sites in the config", true)
			return m, nil
		}
		var cmds []tea.Cmd
		if m.deps.GitHub != nil {
			model, cmd := m.fetchRepos(true)
			m, cmds = model.(Model), append(cmds, cmd)
		}
		if m.deps.Live != nil {
			model, cmd := m.fetchLive(true)
			m, cmds = model.(Model), append(cmds, cmd)
		}
		if m.deps.Feedback != nil {
			model, cmd := m.fetchFeedback(true)
			m, cmds = model.(Model), append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	case key.Matches(msg, k.Help):
		m.mode = modeHelp
	case key.Matches(msg, k.Menu):
		return m.openMenu()
	case key.Matches(msg, k.New):
		return m.openCreate()
	case key.Matches(msg, k.Handoff):
		return m.openHandoff()
	case key.Matches(msg, k.Agent):
		return m.runAgent()
	case key.Matches(msg, k.Chat):
		return m.openChat()
	case key.Matches(msg, k.Setup):
		return m.setupNext()
	case key.Matches(msg, k.UpdateAll):
		return m.planFleet()
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

// taskKey handles the task keys (y, S, u, w, M, C, F, X, o) in the table
// and detail views.
func (m Model) taskKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	k := m.keys
	switch {
	case key.Matches(msg, k.SyncAll):
		model, cmd := m.syncAll()
		return model, cmd, true
	case key.Matches(msg, k.SyncCheck):
		model, cmd := m.syncOrUpdate("check")
		return model, cmd, true
	case key.Matches(msg, k.SyncApply):
		model, cmd := m.syncOrUpdate("apply")
		return model, cmd, true
	case key.Matches(msg, k.UpdateCore):
		model, cmd := m.syncOrUpdate("update")
		return model, cmd, true
	case key.Matches(msg, k.Work):
		model, cmd := m.askWork()
		return model, cmd, true
	case key.Matches(msg, k.Merge):
		model, cmd := m.askMerge()
		return model, cmd, true
	case key.Matches(msg, k.Report):
		return m.askReport(), nil, true
	case key.Matches(msg, k.Restore):
		model, cmd := m.askRestore()
		return model, cmd, true
	case key.Matches(msg, k.Note):
		model, cmd := m.openNote()
		return model, cmd, true
	case key.Matches(msg, k.SiteBranch):
		model, cmd := m.askSiteBranch()
		return model, cmd, true
	case key.Matches(msg, k.Pull):
		model, cmd := m.askPull()
		return model, cmd, true
	case key.Matches(msg, k.Publish):
		model, cmd := m.askPublish()
		return model, cmd, true
	case key.Matches(msg, k.PublishUndo):
		model, cmd := m.askPublishUndo()
		return model, cmd, true
	case key.Matches(msg, k.Comments):
		model, cmd := m.openComments()
		return model, cmd, true
	case key.Matches(msg, k.Resolve):
		model, cmd := m.resolveComments()
		return model, cmd, true
	case key.Matches(msg, k.Ask):
		model, cmd := m.openSkills()
		return model, cmd, true
	case key.Matches(msg, k.Output):
		if m.task == nil {
			m.setFlash("nothing has run yet (y checks the scaffold, u updates the site)", false)
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
	// Live sites first, the TAW ecosystem last; otherwise the scan's order
	// (by name).
	rank := func(r row) int {
		switch s := rep.Sites[r.site]; {
		case ecosystem(s.Themes[r.theme]):
			return 2
		case m.deps.Production[s.Slug]:
			return 0
		}
		return 1
	}
	sort.SliceStable(m.rows, func(i, j int) bool { return rank(m.rows[i]) < rank(m.rows[j]) })
	m.applyFilter()
	for ei, e := range m.entries {
		if e.isHeader() {
			continue
		}
		r := m.rows[e.row]
		if rep.Sites[r.site].ID == prevSite && rep.Sites[r.site].Themes[r.theme].Dir == prevTheme {
			m.cursor = ei
			break
		}
	}
	if m.selectAfterScan != "" { // a site was just created, or Deps.Select: select it
		want := strings.ToLower(m.selectAfterScan)
		for vi, e := range m.entries {
			if e.isHeader() {
				continue
			}
			s := rep.Sites[m.rows[e.row].site]
			if strings.ToLower(s.Slug) == want || strings.ToLower(s.Name) == want || strings.ToLower(s.Domain) == want {
				m.cursor, m.selectAfterScan = vi, ""
				if m.detailAfterScan {
					m.mode, m.scroll, m.detailAfterScan = modeDetail, 0, false
				}
				break
			}
		}
	}
	m.clamp()
}

func (m *Model) applyFilter() {
	terms, problems := parseFilter(m.filter.Value())
	m.filterHint = filterHint(problems)
	m.visible = m.visible[:0]
	for i, r := range m.rows {
		if m.matches(r, terms) {
			m.visible = append(m.visible, i)
		}
	}
	m.buildEntries()
	m.clamp()
}

// matches reports whether a card passes every term of the filter.
func (m Model) matches(r row, terms []filterTerm) bool {
	if len(terms) == 0 {
		return true
	}
	s := m.rep.Sites[r.site]
	f := m.filterFactsOf(s, s.Themes[r.theme])
	for _, t := range terms {
		if !t.matches(f) {
			return false
		}
	}
	return true
}

func (m *Model) move(delta int) {
	m.cursor += delta
	m.clamp()
}

// clamp keeps the cursor on an entry and the entry on screen.
func (m *Model) clamp() {
	if len(m.entries) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	m.cursor = min(max(m.cursor, 0), len(m.entries)-1)
	if m.grouped() { // headers and cards differ in height: fit by lines
		air := m.groupedAiry()
		if m.cursor < m.offset {
			m.offset = m.cursor
		}
		for m.cursor >= m.offset+m.groupedFit(m.offset, air) {
			m.offset++
		}
		for m.offset > 0 && m.groupedFit(m.offset-1, air) >= len(m.entries)-m.offset+1 {
			m.offset-- // no empty room at the bottom
		}
		return
	}
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
	m.offset = min(max(m.offset, 0), max(len(m.entries)-rows, 0))
}

// selected is the card under the cursor; none on a group's header.
func (m Model) selected() (row, bool) {
	if len(m.entries) == 0 || m.entries[m.cursor].isHeader() {
		return row{}, false
	}
	return m.rows[m.entries[m.cursor].row], true
}

// onHeader reports whether the cursor is on a group's header.
func (m Model) onHeader() bool {
	return m.cursor < len(m.entries) && m.entries[m.cursor].isHeader()
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
