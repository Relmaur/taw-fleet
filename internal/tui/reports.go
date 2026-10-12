package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/report"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// reportsMsg is every site's saved reports, read after a scan.
type reportsMsg map[string][]report.Saved

func (m Model) loadReports() tea.Cmd {
	dir := m.deps.ReportDir
	if dir == "" {
		return nil
	}
	var slugs []string
	for _, s := range m.rep.Sites {
		slugs = append(slugs, s.Slug)
	}
	return func() tea.Msg {
		out := reportsMsg{}
		for _, slug := range slugs {
			if saved := report.List(dir, slug); len(saved) > 0 {
				out[slug] = saved
			}
		}
		return out
	}
}

// askReport is m: which month's report to make (this one so far, or last
// month), or the folder with the saved ones.
func (m Model) askReport() tea.Model {
	s, _, ok := m.selectedTheme()
	if !ok {
		return m
	}
	if m.deps.ReportTask == nil {
		m.setFlash("reports aren't available here", true)
		return m
	}
	now := m.deps.Now()
	this := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	last := this.AddDate(0, -1, 0)
	mk := func(month string) func(Model) (tea.Model, tea.Cmd) {
		return func(m Model) (tea.Model, tea.Cmd) {
			return m.startTask(m.deps.ReportTask(s, month))
		}
	}
	m.choices = []func(Model) (tea.Model, tea.Cmd){mk(this.Format("2006-01")), mk(last.Format("2006-01"))}
	opts := []string{"1 " + this.Format("January") + " so far", "2 " + last.Format("January")}
	if len(m.reports[s.Slug]) > 0 && m.deps.Actions != nil {
		dir := m.deps.ReportDir + "/" + s.Slug
		m.choices = append(m.choices, func(m Model) (tea.Model, tea.Cmd) {
			a, ctx := m.deps.Actions, m.ctx
			return m, m.run(func() (string, error) { return a.OpenURL(ctx, "file://"+dir) })
		})
		opts = append(opts, "3 Open the folder")
	}
	m.confirm = "Monthly report for " + s.Slug + ":  " + strings.Join(opts, " · ")
	return m
}

// reportSection is the detail pane's REPORTS section: the site's saved
// monthly reports.
func (m Model) reportSection(s site.Site, width int) string {
	saved := m.reports[s.Slug]
	if len(saved) == 0 {
		return ""
	}
	p := m.pal
	var lines []string
	for i, r := range saved {
		if i == 4 {
			lines = append(lines, "   "+p.Fg(p.Muted).Render("and older: taw-fleet report list "+s.Slug))
			break
		}
		t, err := time.Parse("2006-01", r.Month)
		name := r.Month
		if err == nil {
			name = t.Format("January 2006")
		}
		lines = append(lines, " "+p.Fg(p.Accent).Render(p.I.Report)+" "+pad(name, 16)+p.Fg(p.Muted).Render(tildePath(m.deps.Paths.Home, r.HTML)))
	}
	return "\n" + render.Section(p, p.I.Report+" REPORTS", p.Accent, "m · make one", width) + strings.Join(lines, "\n") + "\n"
}

func tildePath(home, path string) string {
	if home != "" && strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	return path
}
