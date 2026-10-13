package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// publishesMsg is every production site's publishes, read after a scan.
type publishesMsg map[string][]actions.Published

func (m Model) loadPublishes() tea.Cmd {
	if m.deps.Actions == nil || m.deps.Settings == nil {
		return nil
	}
	var slugs []string
	for _, s := range m.rep.Sites {
		if m.deps.Settings(s.Slug).ProductionURL != "" {
			slugs = append(slugs, s.Slug)
		}
	}
	a := m.deps.Actions
	return func() tea.Msg {
		out := publishesMsg{}
		for _, slug := range slugs {
			if h := a.PublishHistory(slug); len(h) > 0 {
				out[slug] = h
			}
		}
		return out
	}
}

// publishSection is the details' PUBLISHED section: the site's last
// publishes, each with its review kept on disk.
func (m Model) publishSection(s site.Site, width int) string {
	if m.deps.Settings == nil || m.deps.Settings(s.Slug).ProductionURL == "" {
		return ""
	}
	h := m.publishes[s.Slug]
	p := m.pal
	note := "p · publish"
	if len(h) > 0 {
		note = "p · publish · x · undo the last"
	}
	var lines []string
	for i, e := range h {
		if i == 3 {
			lines = append(lines, "   "+p.Fg(p.Muted).Render("and older: taw-fleet publish history "+s.Slug))
			break
		}
		mark, what := p.Fg(p.Muted).Render("·"), e.State
		switch e.State {
		case "published":
			mark = p.Fg(p.OK).Render(p.I.Done)
			what = fmt.Sprintf("published: %d updated", e.Updated)
			if e.Created > 0 {
				what += fmt.Sprintf(", %d created", e.Created)
			}
		case "failed":
			mark = p.Fg(p.Err).Render("✗")
			what = "stopped"
		case "cancelled":
			what = "cancelled in the review"
		case "review":
			what = "reviewed, not published"
		}
		lines = append(lines, " "+mark+" "+pad(e.When.Format("2 Jan 15:04"), 14)+what)
		if i == 0 { // the newest one's review, its end kept when it doesn't fit
			path := tildePath(m.deps.Paths.Home, e.Folder+"/review.html")
			if w := width - 3; w > 10 && ansi.StringWidth(path) > w {
				path = "…" + ansi.TruncateLeft(path, ansi.StringWidth(path)-w+1, "")
			}
			lines = append(lines, "   "+p.Fg(p.Muted).Render(path))
		}
	}
	// hint is a muted line, wrapped, under a mark.
	hint := func(mark, text string) {
		for j, l := range wrap(text, width-3, 3) {
			lead := "   "
			if j == 0 {
				lead = " " + mark + " "
			}
			lines = append(lines, lead+p.Fg(p.Muted).Render(l))
		}
	}
	if pr := s.Production; pr != nil && pr.Reachable {
		switch {
		case !pr.PublishAvailable:
			hint(p.Fg(p.Warn).Render("○"), "production can't take a publish yet: it needs taw/core 1.98+ and taw/hub-companion 0.5+ (u updates the theme; deploy)")
		case !pr.PublishEnabled:
			hint(p.Fg(p.Warn).Render("○"), `publishing is off: "publish": true under extra.taw-companion in the theme's composer.json turns it on (commit, deploy)`)
		}
	}
	if len(h) == 0 {
		hint(" ", "Nothing published from here yet. p compares Local's content with production's and opens the review.")
	}
	return "\n" + render.Section(p, p.I.Live+" PUBLISHED", p.Accent, note, width) + strings.Join(lines, "\n") + "\n"
}
