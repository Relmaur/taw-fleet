package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/setup"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// botsMsg is which production sites have their bot in the Keychain.
type botsMsg map[string]bool

// loadBots asks the Keychain which production sites have a bot: a look,
// never the password.
func (m Model) loadBots() tea.Cmd {
	if m.deps.HasBot == nil || m.deps.Settings == nil {
		return nil
	}
	var slugs []string
	for _, s := range m.rep.Sites {
		if m.deps.Settings(s.Slug).ProductionURL != "" {
			slugs = append(slugs, s.Slug)
		}
	}
	has, ctx := m.deps.HasBot, m.ctx
	return func() tea.Msg {
		out := botsMsg{}
		for _, slug := range slugs {
			out[slug] = has(ctx, slug)
		}
		return out
	}
}

// setupSteps is the SETUP checklist for a site's theme; nil for the TAW
// ecosystem's own themes, which aren't client sites.
func (m Model) setupSteps(s site.Site, t site.Theme) []setup.Step {
	if ecosystem(t) || !t.IsTAW {
		return nil
	}
	in := setup.Input{Site: s, Theme: t, Bot: m.bots[s.Slug]}
	if m.deps.Settings != nil {
		in.Config = m.deps.Settings(s.Slug)
	}
	return setup.Steps(in)
}

// setupSection is the details' SETUP section: what the site has, and what
// it still needs, with the key or the command that does it.
func (m Model) setupSection(s site.Site, t site.Theme, width int) string {
	steps := m.setupSteps(s, t)
	if steps == nil {
		return ""
	}
	p := m.pal
	muted := p.Fg(p.Muted)
	done, all := setup.Count(steps)
	note := "I · set up the next one"
	if done == all {
		return "\n" + render.Section(p, p.I.App+" SETUP", p.Accent, "all set", width) +
			" " + p.Fg(p.OK).Render(p.I.Done) + muted.Render(" repository, secrets check, deploy, production, companion, bot, BugSmash, skills") + "\n"
	}
	next, _ := setup.Next(steps)
	var lines []string
	for _, st := range steps {
		mark, name := p.Fg(p.OK).Render(p.I.Done), st.Name
		switch {
		case st.Done:
		case st.Later:
			mark = muted.Render("·")
			name = muted.Render(name)
		case st.Name == next.Name:
			mark = p.Fg(p.Warn).Render("→")
			name = lipgloss.NewStyle().Bold(true).Render(name)
		default:
			mark = p.Fg(p.Warn).Render("○")
		}
		line := " " + mark + " " + pad(name, 15) + muted.Render(st.State)
		lines = append(lines, line)
		if !st.Done && !st.Later && st.Fix != "" {
			lines = append(lines, strings.Repeat(" ", 18)+p.Fg(p.Accent).Render(st.Fix))
		}
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return "\n" + render.Section(p, p.I.App+" SETUP", p.Accent, note, width) + strings.Join(lines, "\n") + "\n"
}

// setupNext is I: the next step of the selected site's setup, by its key,
// or how to do it by hand.
func (m Model) setupNext() (tea.Model, tea.Cmd) {
	s, t, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	steps := m.setupSteps(s, t)
	next, ok := setup.Next(steps)
	switch {
	case steps == nil:
		m.setFlash(t.Dir+" is the TAW ecosystem's own theme, not a client site", false)
		return m, nil
	case !ok:
		m.setFlash(s.Slug+" is all set up (what's left waits on production)", false)
		return m, nil
	case next.Key == "I":
		return m.askRepo(s, t)
	case next.Key != "":
		return m.onKey(pressOf(next.Key))
	}
	m.setFlash(next.Name+": "+next.Fix, false)
	return m, nil
}

// askRepo asks before putting the theme on GitHub.
func (m Model) askRepo(s site.Site, t site.Theme) (tea.Model, tea.Cmd) {
	if m.deps.Actions == nil {
		m.setFlash("unavailable: "+errText(m.deps.ActionsErr), true)
		return m, nil
	}
	task, err := m.deps.Actions.RepoTask(s, t, m.deps.Actions.RepoOwner(), "")
	if err != nil {
		m.setFlash(t.Dir+": "+err.Error(), true)
		return m, nil
	}
	return m.askTask(task, "")
}
