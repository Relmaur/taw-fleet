package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// leftoversEndedMsg is Local's leftover processes ended (or not).
type leftoversEndedMsg struct {
	site site.Site
	le   *local.LeftoversError
	left local.Leftovers // still there
	err  error
}

var opVerbs = map[local.Op]string{local.Start: "start", local.Stop: "stop", local.Restart: "restart"}

// askLeftovers asks to end the leftover processes in a site's way, and to
// do again what they kept from happening, instead of fighting them.
func (m Model) askLeftovers(s site.Site, le *local.LeftoversError) Model {
	if m.deps.EndLeftovers == nil {
		m.setFlash(s.Slug+": "+le.Error(), true)
		return m
	}
	n := len(le.List)
	q := fmt.Sprintf("%d leftover Local %s in %s's way (%s): their master is gone, so Local can't stop them. End %s",
		n, plural(n, "process is", "processes are"), s.Slug, le.List, plural(n, "it", "them"))
	if le.Done {
		q += "? " + s.Slug + " is stopped."
	} else {
		q += " and " + opVerbs[le.Op] + " " + s.Slug + "?"
	}
	m.confirm = q
	m.onYes = func(m Model) (tea.Model, tea.Cmd) {
		m.busyWith(s.ID, le.Op)
		end, ctx := m.deps.EndLeftovers, m.ctx
		return m, tea.Batch(m.spin.Tick, func() tea.Msg {
			left, err := end(ctx, le.List)
			return leftoversEndedMsg{s, le, left, err}
		})
	}
	return m
}

// onLeftoversEnded reports, and does the start, stop or restart again when
// they kept it from happening.
func (m Model) onLeftoversEnded(msg leftoversEndedMsg) (tea.Model, tea.Cmd) {
	m.notBusy(msg.site.ID)
	switch {
	case msg.err != nil:
		m.setFlash(msg.site.Slug+": "+msg.err.Error(), true)
		return m, nil
	case len(msg.left) > 0:
		m.setFlash(fmt.Sprintf("still there after 5 s: %s (kill -KILL %s)", msg.left, pidList(msg.left)), true)
		return m, nil
	}
	n := len(msg.le.List)
	done := fmt.Sprintf("Ended %d leftover %s", n, plural(n, "process", "processes"))
	if msg.le.Done {
		m.setFlash(done+"; "+msg.site.Slug+" can start again", false)
		return m, nil
	}
	m.setFlash(done+"; "+opDoing[msg.le.Op]+" "+msg.site.Slug+"…", false)
	return m.doSiteOp(msg.site, msg.le.Op)
}

var opDoing = map[local.Op]string{local.Start: "starting", local.Stop: "stopping", local.Restart: "restarting"}

func pidList(l local.Leftovers) string {
	parts := make([]string, 0, len(l))
	for _, pid := range l.PIDs() {
		parts = append(parts, fmt.Sprint(pid))
	}
	return strings.Join(parts, " ")
}

// busyWith marks a site busy with an operation.
func (m *Model) busyWith(id string, op local.Op) {
	busy := map[string]local.Op{id: op}
	for k, o := range m.busy {
		busy[k] = o
	}
	m.busy = busy
}

// notBusy marks a site done.
func (m *Model) notBusy(id string) {
	busy := map[string]local.Op{}
	for k, o := range m.busy {
		if k != id {
			busy[k] = o
		}
	}
	m.busy = busy
}
