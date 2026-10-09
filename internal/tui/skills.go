package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/actions"
)

// openSkills shows the a picker: the selected theme's Claude Code skills.
func (m Model) openSkills() (tea.Model, tea.Cmd) {
	_, t, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	if m.deps.Actions == nil {
		m.setFlash("agent unavailable: "+errText(m.deps.ActionsErr), true)
		return m, nil
	}
	m.skills = actions.Skills(t)
	if len(m.skills) == 0 {
		m.setFlash(t.Dir+" has no skills in .claude/skills/ yet: S syncs a classic theme's scaffold; a block theme: php bin/taw skills:sync --apply", true)
		return m, nil
	}
	m.menuFrom, m.mode, m.menuCursor = m.mode, modeSkills, 0
	m.menuInput.SetValue("")
	m.menuInput.Placeholder = "type to find a skill"
	return m, m.menuInput.Focus()
}

// skillMatches are the skills matching the typed filter (name or description).
func (m Model) skillMatches() []actions.Skill {
	q := strings.ToLower(strings.TrimSpace(m.menuInput.Value()))
	if q == "" {
		return m.skills
	}
	var out []actions.Skill
	for _, sk := range m.skills {
		if strings.Contains(strings.ToLower(sk.Name+" "+sk.Description), q) {
			out = append(out, sk)
		}
	}
	return out
}

// onSkillsKey filters, moves, starts Claude on a skill (enter) or closes (esc).
func (m Model) onSkillsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	matches := m.skillMatches()
	switch msg.String() {
	case "esc":
		return m.closeSkills(), nil
	case "up", "ctrl+p":
		m.menuCursor = max(m.menuCursor-1, 0)
		return m, nil
	case "down", "ctrl+n":
		m.menuCursor = min(m.menuCursor+1, max(len(matches)-1, 0))
		return m, nil
	case "enter":
		m = m.closeSkills()
		if m.menuCursor >= len(matches) {
			return m, nil
		}
		return m.runSkill(matches[m.menuCursor])
	}
	var cmd tea.Cmd
	m.menuInput, cmd = m.menuInput.Update(msg)
	m.menuCursor = 0
	return m, cmd
}

func (m Model) closeSkills() Model {
	m.mode = m.menuFrom
	m.menuInput.Blur()
	m.menuInput.Placeholder = "type to find an action"
	return m
}

// runSkill starts Claude Code in the theme folder on the skill, beside the
// dashboard, with what taw-fleet knows about the site.
func (m Model) runSkill(sk actions.Skill) (tea.Model, tea.Cmd) {
	s, t, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	a, ctx, tty := m.deps.Actions, m.ctx, m.deps.TTY
	p := a.SkillPrompt(s, t, sk, m.findings[s.ID])
	kind := "skill-" + sk.Name
	if sk.Name == actions.ResolveSkill {
		kind = "comments" // BugSmash is asked again when it exits
	}
	m.mode, m.scroll = modeTable, 0
	return m, func() tea.Msg {
		l, err := a.LaunchSkill(ctx, s, t, kind, p, tty)
		return launchedMsg{sk.Name + " on " + s.Slug, l, err}
	}
}

// skillsScreen is the picker: the filter, then the matching skills with
// their descriptions; the site's own are marked.
func (m Model) skillsScreen(h int) string {
	p := m.pal
	w := min(max(m.width-8, 40), 90)
	head := "Ask Claude"
	if _, t, ok := m.selectedTheme(); ok {
		head += p.Fg(p.Muted).Render("  in " + t.Dir + ", with one of its skills")
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render(head), "", m.menuInput.View(), ""}
	matches := m.skillMatches()
	room := max((h-10)/2, 3)
	first := max(0, m.menuCursor-room+1)
	for i := first; i < len(matches) && i < first+room; i++ {
		sk := matches[i]
		name := sk.Name
		if sk.Owner == "site" {
			name += p.Fg(p.Brand).Render("  this site's own")
		}
		desc := p.Fg(p.Muted).Render("    " + firstSentence(sk.Description))
		if i == m.menuCursor {
			name = p.Fg(p.Accent).Render("▌ ") + lipgloss.NewStyle().Bold(true).Render(sk.Name) + strings.TrimPrefix(name, sk.Name)
		} else {
			name = "  " + name
		}
		lines = append(lines, ansi.Truncate(name, w, "…"), ansi.Truncate(desc, w, "…"))
	}
	if len(matches) == 0 {
		lines = append(lines, p.Fg(p.Muted).Render("  No skill matches."))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Faint).Padding(1, 2).Width(w + 6)
	return lipgloss.Place(m.width, h, lipgloss.Center, lipgloss.Top, "\n"+box.Render(strings.Join(lines, "\n")))
}

// firstSentence is a description's opening, up to its first ". " or "Triggers".
func firstSentence(d string) string {
	if i := strings.Index(d, "Triggers on"); i > 0 {
		d = d[:i]
	}
	if i := strings.Index(d, ". "); i > 0 {
		d = d[:i+1]
	}
	return strings.TrimSpace(d)
}
