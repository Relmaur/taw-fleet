package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// launchedMsg is a Claude Code window opening (or failing to).
type launchedMsg struct {
	dir string
	l   actions.Launched
	err error
}

// runAgent hands the selected theme's update to Claude Code (A): the
// handoff prompt, as h shows it, in a window beside the dashboard.
func (m Model) runAgent() (tea.Model, tea.Cmd) {
	s, t, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	if m.deps.Actions == nil {
		m.setFlash("agent unavailable: "+errText(m.deps.ActionsErr), true)
		return m, nil
	}
	p, err := m.deps.Actions.Handoff(s, t, m.findings[s.ID])
	if err != nil {
		m.setFlash(t.Dir+": "+err.Error(), true)
		return m, nil
	}
	return m.agentWith(s, t, p)
}

// agentWith opens Claude Code with the prompt in a new window, placed on
// the right half of the screen with the dashboard on the left (Terminal).
func (m Model) agentWith(s site.Site, t site.Theme, p handoff.Prompt) (tea.Model, tea.Cmd) {
	m.mode, m.scroll = modeTable, 0
	a, ctx, tty := m.deps.Actions, m.ctx, m.deps.TTY
	return m, func() tea.Msg {
		l, err := a.Launch(ctx, s, t, p, tty)
		return launchedMsg{t.Dir, l, err}
	}
}

// agentFinished reports the agents whose Claude Code has exited (their
// done file exists) and rescans: they may have updated taw/core.
func (m Model) agentFinished() (Model, tea.Cmd) {
	var done []string
	comments := false // a resolve-comments agent finished: ask BugSmash again
	for file, dir := range m.agents {
		if _, err := os.Stat(file); err == nil {
			done = append(done, dir)
			comments = comments || strings.Contains(filepath.Base(file), "-comments-")
			delete(m.agents, file)
			_ = os.Remove(file)
		}
	}
	if len(done) == 0 {
		return m, nil
	}
	sort.Strings(done)
	m.setFlash("Claude Code finished: "+strings.Join(done, ", ")+". Rescanning.", false)
	var cmds []tea.Cmd
	if comments {
		model, cmd := m.fetchFeedback(true)
		m, cmds = model.(Model), append(cmds, cmd)
	}
	if !m.scanning {
		cmds = append(cmds, m.startScan())
	}
	return m, tea.Batch(cmds...)
}

// fleetPlannedMsg is the update-all plan, ready to confirm.
type fleetPlannedMsg struct{ plan actions.FleetPlan }

// planFleet works out which themes an update-all would take (U). It asks
// GitHub for the newest taw-core, so it runs in the background.
func (m Model) planFleet() (tea.Model, tea.Cmd) {
	if m.deps.Actions == nil {
		m.setFlash("update-all unavailable: "+errText(m.deps.ActionsErr), true)
		return m, nil
	}
	if !m.loaded {
		m.setFlash("wait for the first scan to finish", false)
		return m, nil
	}
	a, ctx, sites := m.deps.Actions, m.ctx, m.rep.Sites
	m.setFlash("Checking which themes need an update…", false)
	return m, func() tea.Msg { return fleetPlannedMsg{a.PlanUpdateAll(ctx, sites)} }
}

// fleetPlanned asks before updating every theme that needs it (U), one
// after another, with no agent: each as its taw.json says.
func (m Model) fleetPlanned(p actions.FleetPlan) (tea.Model, tea.Cmd) {
	if len(p.Themes) == 0 {
		msg := "Nothing to update: every client TAW theme is current"
		if left := leftOut(p); left != "" {
			msg += "; left out: " + left
		}
		m.setFlash(msg, false)
		return m, nil
	}
	task, err := m.deps.Actions.UpdateAllTask(p)
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	return m.askTask(task, "")
}

func leftOut(p actions.FleetPlan) string {
	var left []string
	for _, s := range p.Skipped {
		left = append(left, s.Theme+" ("+s.Reason+")")
	}
	return strings.Join(left, ", ")
}
