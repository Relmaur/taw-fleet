package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/taw"
)

// maxTaskLines bounds the output kept in memory.
const maxTaskLines = 2000

// taskState is a sync or update in progress (or finished), and its output.
type taskState struct {
	title    string
	lines    []string
	running  bool
	summary  actions.Summary
	err      error
	started  time.Time
	finished time.Time
	ch       chan taskEvent
	scroll   int  // first line on screen when not following
	follow   bool // stick to the newest output
}

type taskEvent struct {
	line string
	done bool
	sum  actions.Summary
	err  error
}

// taskEventMsg carries one event of the current task.
type taskEventMsg taskEvent

func listen(ch <-chan taskEvent) tea.Cmd {
	return func() tea.Msg { return taskEventMsg(<-ch) }
}

// startTask runs a task in the background and opens the output view.
func (m Model) startTask(task actions.Task) (tea.Model, tea.Cmd) {
	if m.task != nil && m.task.running {
		m.setFlash(m.task.title+" is still running (o shows it)", true)
		return m, nil
	}
	ch := make(chan taskEvent, 256)
	m.task = &taskState{title: task.Title, running: true, started: m.deps.Now(), ch: ch, follow: true}
	if !task.Quiet {
		m.mode = modeOutput
	}
	ctx := m.ctx
	go func() {
		w := taw.NewLineWriter(func(l string) { ch <- taskEvent{line: l} })
		sum, err := task.Run(ctx, w)
		w.Flush()
		ch <- taskEvent{done: true, sum: sum, err: err}
	}()
	return m, tea.Batch(listen(ch), m.spin.Tick)
}

// onTaskEvent appends output or finishes the task.
func (m Model) onTaskEvent(ev taskEventMsg) (tea.Model, tea.Cmd) {
	t := m.task
	if t == nil {
		return m, nil
	}
	if !ev.done {
		t.lines = append(t.lines, ev.line)
		if len(t.lines) > maxTaskLines {
			t.lines = t.lines[len(t.lines)-maxTaskLines:]
		}
		return m, listen(t.ch)
	}
	t.running, t.summary, t.err, t.finished = false, ev.sum, ev.err, m.deps.Now()
	if m.full.active && t.title == m.full.syncTitle {
		m.full.syncResult = ev.sum.Headline
		if ev.err != nil {
			m.full.syncResult = "sync check: " + ev.err.Error()
		}
	}
	if ev.err != nil {
		m.setFlash(t.title+": "+ev.err.Error(), true)
	} else {
		m.setFlash(ev.sum.Headline, ev.sum.Failed)
	}
	var cmds []tea.Cmd
	if pv, ok := ev.sum.Report.(actions.PullPreview); ok && ev.err == nil && pv.Apply.Run != nil {
		apply := pv.Apply
		m.confirm = apply.Ask
		m.onYes = func(m Model) (tea.Model, tea.Cmd) { return m.startTask(apply) }
	}
	if o, ok := ev.sum.Report.(actions.UpdateOutcome); ok && ev.err == nil {
		if o.Stopped() {
			m = m.offerFinish(o)
		}
		if o.Report.Delivered != nil && o.Report.Delivered.URL != "" && m.deps.GitHub != nil {
			model, cmd := m.fetchRepos(true) // the new pull request in the PR column
			m, cmds = model.(Model), append(cmds, cmd)
		}
	}
	if res, ok := ev.sum.Report.(actions.MergeResult); ok && ev.err == nil {
		if res.Deploys {
			m.follow(res)
		}
		model, cmd := m.fetchRepos(false)
		m, cmds = model.(Model), append(cmds, cmd)
	}
	if !m.scanning {
		cmds = append(cmds, m.startScan())
	}
	return m, tea.Batch(cmds...)
}

// askTask asks before a task that writes; read-only tasks start at once.
func (m Model) askTask(task actions.Task, question string) (tea.Model, tea.Cmd) {
	if task.Ask != "" {
		question = task.Ask
	}
	if !task.Writes && task.Ask == "" {
		return m.startTask(task)
	}
	m.confirm = question
	m.onYes = func(m Model) (tea.Model, tea.Cmd) { return m.startTask(task) }
	return m, nil
}

// syncOrUpdate builds the task for y (sync check), S (apply) or u (update).
func (m Model) syncOrUpdate(which string) (tea.Model, tea.Cmd) {
	s, t, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	if m.deps.Actions == nil {
		m.setFlash("unavailable: "+errText(m.deps.ActionsErr), true)
		return m, nil
	}
	dirty := ""
	if t.Git != nil && t.Git.Dirty > 0 {
		dirty = fmt.Sprintf(" It has %d uncommitted %s.", t.Git.Dirty, plural(t.Git.Dirty, "change", "changes"))
	}
	var (
		task     actions.Task
		err      error
		question string
	)
	switch which {
	case "check":
		task, err = m.deps.Actions.SyncTask(s, t, false)
	case "apply":
		task, err = m.deps.Actions.SyncTask(s, t, true)
		question = "Write the Tier 1 framework files in " + t.Dir + "?" + dirty
	case "update":
		task, err = m.deps.Actions.UpdateTask(s, t) // asks its own question (taw.json's words)
	}
	if err != nil {
		m.setFlash(t.Dir+": "+err.Error(), true)
		return m, nil
	}
	return m.askTask(task, question)
}

// offerFinish asks how to finish an update that stopped: Claude works from
// its report, or the report opens as the guide for a person (ADR-0004).
func (m Model) offerFinish(o actions.UpdateOutcome) Model {
	step := "a step"
	if o.Report.Failure != nil {
		step = o.Report.Failure.Step
	}
	m.confirm = o.Theme.Dir + " stopped at " + step + ":  1 Fix with Claude · 2 Do it myself (open the guide)"
	m.choices = []func(Model) (tea.Model, tea.Cmd){
		func(m Model) (tea.Model, tea.Cmd) {
			a, ctx, tty := m.deps.Actions, m.ctx, m.deps.TTY
			m.mode = modeTable
			return m, func() tea.Msg {
				l, err := a.FixUpdate(ctx, o, tty)
				return launchedMsg{o.Theme.Dir, l, err}
			}
		},
		func(m Model) (tea.Model, tea.Cmd) {
			a, ctx := m.deps.Actions, m.ctx
			return m, m.run(func() (string, error) { return a.OpenGuide(ctx, o) })
		},
	}
	return m
}

// onOutputKey handles keys in the output view.
func (m Model) onOutputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := m.task
	if t == nil {
		m.mode = modeTable
		return m, nil
	}
	page := max(m.bodyHeight()-4, 1)
	switch {
	case msg.String() == "esc" || msg.String() == "q":
		m.mode = modeTable // the task keeps running; o comes back
	case msg.String() == "up" || msg.String() == "k":
		t.follow = false
		t.scroll = max(0, t.scroll-1)
	case msg.String() == "down" || msg.String() == "j":
		t.scroll++
	case msg.String() == "pgup":
		t.follow = false
		t.scroll = max(0, t.scroll-page)
	case msg.String() == "pgdown":
		t.scroll += page
	case msg.String() == "end":
		t.follow = true
	case msg.String() == "home":
		t.follow, t.scroll = false, 0
	case msg.String() == "c" && !t.running && t.summary.Secret != "" && m.deps.Actions != nil:
		a, ctx, secret := m.deps.Actions, m.ctx, t.summary.Secret
		return m, m.run(func() (string, error) {
			if err := a.Copy(ctx, secret); err != nil {
				return "", err
			}
			return "Copied the password. Save it in your password manager.", nil
		})
	case msg.String() == "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// outputScreen is the live output of the current task, then its summary.
func (m Model) outputScreen(height int) string {
	p, t := m.pal, m.task
	muted := p.Fg(p.Muted)
	if t == nil {
		return block("", m.width, height)
	}
	var state string
	switch {
	case t.running:
		state = m.spin.View() + " " + muted.Render("running for "+m.deps.Now().Sub(t.started).Round(time.Second).String())
	case t.err != nil, t.summary.Failed:
		state = p.Fg(p.Err).Render("✗ failed")
	default:
		state = p.Fg(p.OK).Render("✓ done") + muted.Render(" in "+t.finished.Sub(t.started).Round(time.Second).String())
	}
	head := " " + lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render(t.title) + "  " + state
	rule := p.Fg(p.Faint).Render(strings.Repeat("─", m.width))

	// The summary (when done) sits under the output and always shows.
	var foot []string
	if !t.running {
		foot = append(foot, rule)
		if t.err != nil {
			foot = append(foot, " "+p.Fg(p.Err).Render("✗ "+t.err.Error()))
		} else {
			mark := p.Fg(p.OK).Render("✓ ")
			if t.summary.Failed {
				mark = p.Fg(p.Err).Render("✗ ")
			}
			foot = append(foot, " "+mark+lipgloss.NewStyle().Bold(true).Render(t.summary.Headline))
			for _, l := range t.summary.Lines {
				foot = append(foot, "   "+l)
			}
		}
	}
	outH := max(height-2-len(foot), 1)
	lines := t.lines
	if len(lines) == 0 {
		empty := "(no output yet)"
		if !t.running {
			empty = "(the command printed nothing)"
		}
		lines = []string{muted.Render(empty)}
	}
	maxScroll := max(len(lines)-outH, 0)
	from := maxScroll
	if !t.follow {
		from = min(t.scroll, maxScroll)
	}
	shown := make([]string, 0, outH)
	for _, l := range lines[from:min(from+outH, len(lines))] {
		shown = append(shown, "  "+p.Fg(p.Muted).Render(ansi.Strip(l)))
	}
	for len(shown) < outH {
		shown = append(shown, "")
	}
	all := append([]string{head, rule}, shown...)
	all = append(all, foot...)
	return block(strings.Join(all, "\n"), m.width, height)
}

// askWork asks before w: get the theme ready to work on, or, when its Vite
// is running, stop it and the site.
func (m Model) askWork() (tea.Model, tea.Cmd) {
	s, t, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	if m.deps.Actions == nil {
		m.setFlash("unavailable: "+errText(m.deps.ActionsErr), true)
		return m, nil
	}
	if _, busy := m.busy[s.ID]; busy {
		m.setFlash(s.Slug+" is busy; wait for it to finish", true)
		return m, nil
	}
	var op actions.SiteOp
	if m.deps.SiteOp != nil {
		op = m.deps.SiteOp
	}
	work := m.deps.Actions.WorkTask
	if t.Dev != "" {
		work = m.deps.Actions.StopWorkTask
	}
	task, err := work(s, t, op)
	if err != nil {
		m.setFlash(t.Dir+": "+err.Error(), true)
		return m, nil
	}
	return m.askTask(task, "")
}

// askPull previews pulling the production site's content into the Local
// site (C); importing it is asked once the preview is on screen.
func (m Model) askPull() (tea.Model, tea.Cmd) {
	s, t, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	if m.deps.Actions == nil {
		m.setFlash("unavailable: "+errText(m.deps.ActionsErr), true)
		return m, nil
	}
	if _, busy := m.busy[s.ID]; busy {
		m.setFlash(s.Slug+" is busy; wait for it to finish", true)
		return m, nil
	}
	var op actions.SiteOp
	if m.deps.SiteOp != nil {
		op = m.deps.SiteOp
	}
	task, err := m.deps.Actions.PullTask(s, t, op)
	if err != nil {
		m.setFlash(t.Dir+": "+err.Error(), true)
		return m, nil
	}
	return m.askTask(task, "")
}

// syncAll checks every classic theme against the scaffold (Y).
func (m Model) syncAll() (tea.Model, tea.Cmd) {
	if m.deps.Actions == nil {
		m.setFlash("unavailable: "+errText(m.deps.ActionsErr), true)
		return m, nil
	}
	task, err := m.deps.Actions.SyncAllTask(m.rep.Sites)
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	return m.startTask(task)
}
