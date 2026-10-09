package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// agentDoneMsg is Claude Code exiting and handing the terminal back.
type agentDoneMsg struct {
	dir string
	err error
}

// runAgent hands the selected theme's update to Claude Code in this
// terminal (A): the handoff prompt, as h shows it.
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
	return m.agentWith(t, p)
}

// agentWith pauses the dashboard and runs Claude Code full screen in the
// theme folder with the prompt; the dashboard comes back when it exits.
func (m Model) agentWith(t site.Theme, p handoff.Prompt) (tea.Model, tea.Cmd) {
	cmd, err := m.deps.Actions.Agent(m.ctx, t, p)
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	m.mode, m.scroll = modeTable, 0
	dir := t.Dir
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return agentDoneMsg{dir, err} })
}

// agentDone reports how the agent ended and rescans: it may have updated
// taw/core, committed or pushed.
func (m Model) agentDone(msg agentDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.setFlash("Claude Code in "+msg.dir+": "+msg.err.Error(), true)
	} else {
		m.setFlash("Back from Claude Code in "+msg.dir+". Rescanning.", false)
	}
	var cmds []tea.Cmd
	if m.deps.Inline {
		// What Claude printed is on the normal screen: clear it and its
		// scrollback so the window holds only the dashboard again.
		cmds = append(cmds, tea.Raw("\x1b[H\x1b[2J\x1b[3J"), tea.ClearScreen)
	}
	if !m.scanning {
		cmds = append(cmds, m.startScan())
	}
	return m, tea.Batch(cmds...)
}
