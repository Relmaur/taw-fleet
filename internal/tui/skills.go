package tui

import (
	tea "charm.land/bubbletea/v2"

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
	m.menuInput.Placeholder = "type to find a skill: what you want done, or its name"
	return m, m.menuInput.Focus()
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
