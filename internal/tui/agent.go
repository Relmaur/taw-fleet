package tui

import (
	"fmt"
	"os"
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
	for file, dir := range m.agents {
		if _, err := os.Stat(file); err == nil {
			done = append(done, dir)
			delete(m.agents, file)
			_ = os.Remove(file)
		}
	}
	if len(done) == 0 {
		return m, nil
	}
	sort.Strings(done)
	m.setFlash("Claude Code finished: "+strings.Join(done, ", ")+". Rescanning.", false)
	if m.scanning {
		return m, nil
	}
	return m, m.startScan()
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
	return m, func() tea.Msg { return fleetPlannedMsg{a.PlanFleet(ctx, sites)} }
}

// fleetPlanned asks before launching: the themes it updates and the ones
// it leaves out.
func (m Model) fleetPlanned(p actions.FleetPlan) (tea.Model, tea.Cmd) {
	var left []string
	for _, s := range p.Skipped {
		left = append(left, s.Theme+" ("+s.Reason+")")
	}
	if len(p.Themes) == 0 {
		msg := "Nothing to update: every client TAW theme is current"
		if len(left) > 0 {
			msg += "; left out: " + strings.Join(left, ", ")
		}
		m.setFlash(msg, false)
		return m, nil
	}
	var names []string
	for _, e := range p.Themes {
		names = append(names, e.Theme.Dir)
	}
	n := len(p.Themes)
	q := fmt.Sprintf("Update %d %s with agents: %s?", n, plural(n, "theme", "themes"), strings.Join(names, ", "))
	if len(left) > 0 {
		q += " Leaving out " + strings.Join(left, ", ") + "."
	}
	m.confirm = q
	m.onYes = func(m Model) (tea.Model, tea.Cmd) {
		a, ctx, tty, findings := m.deps.Actions, m.ctx, m.deps.TTY, m.findings
		label := fmt.Sprintf("the update of %d %s", n, plural(n, "theme", "themes"))
		return m, func() tea.Msg {
			l, err := a.LaunchFleet(ctx, p, findings, tty)
			return launchedMsg{label, l, err}
		}
	}
	return m, nil
}
