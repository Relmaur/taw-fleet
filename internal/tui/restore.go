package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/restore"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// restoresMsg is every site's restore points, read after a scan.
type restoresMsg map[string][]restore.Point

// loadRestores reads the restore points of the scanned sites (small JSON
// files under taw-fleet's own folder).
func (m Model) loadRestores() tea.Cmd {
	if m.deps.Actions == nil {
		return nil
	}
	st := m.deps.Actions.Restores()
	var slugs []string
	for _, s := range m.rep.Sites {
		slugs = append(slugs, s.Slug)
	}
	return func() tea.Msg {
		out := restoresMsg{}
		for _, slug := range slugs {
			if points, err := st.List(slug); err == nil && len(points) > 0 {
				out[slug] = points
			}
		}
		return out
	}
}

// askRestore is z: which restore point to put back (a numbered choice
// when there are several), then the restore's own question.
func (m Model) askRestore() (tea.Model, tea.Cmd) {
	s, t, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	if m.deps.Actions == nil {
		m.setFlash("restore points aren't available: "+errText(m.deps.ActionsErr), true)
		return m, nil
	}
	points := m.restores[s.Slug] // the database is the site's: every theme's points
	if len(points) == 0 {
		m.setFlash(s.Slug+" has no restore points yet: one is saved before each update, merge and content import", false)
		return m, nil
	}
	put := func(p restore.Point) func(Model) (tea.Model, tea.Cmd) {
		return func(m Model) (tea.Model, tea.Cmd) {
			th := t // the point's own theme, whose branch it holds
			for _, x := range s.Themes {
				if x.Dir == p.Theme {
					th = x
				}
			}
			task, err := m.deps.Actions.RestoreTask(m.ctx, s, th, p)
			if err != nil {
				m.setFlash(s.Slug+": "+err.Error(), true)
				return m, nil
			}
			return m.askTask(task, task.Ask)
		}
	}
	if len(points) == 1 {
		return put(points[0])(m)
	}
	var opts []string
	m.choices = nil
	for i, p := range points[:min(len(points), 9)] {
		opts = append(opts, fmt.Sprintf("%d %s", i+1, p.Label()))
		m.choices = append(m.choices, put(p))
	}
	m.confirm = "Put back which restore point of " + s.Slug + "?  " + strings.Join(opts, " · ")
	return m, nil
}

// restoreSection is the detail pane's RESTORE section: the site's newest
// restore points (the database is the site's; each names its theme).
func (m Model) restoreSection(s site.Site, width int) string {
	points := m.restores[s.Slug]
	if len(points) == 0 {
		return ""
	}
	p := m.pal
	var lines []string
	for i, pt := range points {
		if i == 3 {
			lines = append(lines, "   "+p.Fg(p.Muted).Render(fmt.Sprintf("and %d older: taw-fleet restore %s", len(points)-3, s.Slug)))
			break
		}
		lines = append(lines, " "+p.Fg(p.Accent).Render(p.I.Restore)+" "+pt.Label()+p.Fg(p.Muted).Render("  "+shortAge(m.now.Sub(pt.Created))+" ago"))
		lines = append(lines, "   "+p.Fg(p.Muted).Render(actions.PointContents(pt)))
	}
	return "\n" + render.Section(p, p.I.Restore+" RESTORE", p.Accent, "z · put one back", width) + strings.Join(lines, "\n") + "\n"
}
