package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/config"
)

// Tags (#): a one-line input in the footer for the selected site's tags,
// saved in the settings. They show on the card, the filter finds them
// (#retainer, tag:client:) and = groups by them.

func (m Model) openTags() (tea.Model, tea.Cmd) {
	s, _, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	if m.deps.SaveTags == nil {
		m.setFlash("tags can't be saved: the settings didn't load (taw-fleet config show)", true)
		return m, nil
	}
	m.tagging, m.tagSite = true, s.Slug
	m.tagInput.Prompt = "# " + s.Slug + ": "
	m.tagInput.SetValue(strings.Join(s.Tags, " "))
	m.tagInput.CursorEnd()
	// Room after it for what's wrong with a tag.
	m.tagInput.SetWidth(min(max(m.width-ansi.StringWidth(m.tagInput.Prompt)-4, 10), 48))
	return m, m.tagInput.Focus()
}

// onTagKey handles keys while the tag input is open.
func (m Model) onTagKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.tagging = false
		m.tagInput.Blur()
		return m, nil
	case "enter":
		tags, err := config.CleanTags(strings.Fields(strings.ReplaceAll(m.tagInput.Value(), ",", " ")))
		if err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		if err := m.deps.SaveTags(m.tagSite, tags); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		for i := range m.rep.Sites {
			if m.rep.Sites[i].Slug == m.tagSite {
				m.rep.Sites[i].Tags = tags
			}
		}
		m.tagging = false
		m.tagInput.Blur()
		m.applyFilter()
		if len(tags) == 0 {
			m.setFlash(m.tagSite+" has no tags now.", false)
		} else {
			m.setFlash("Tags saved for "+m.tagSite+": #"+strings.Join(tags, " #"), false)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.tagInput, cmd = m.tagInput.Update(msg)
	return m, cmd
}

// tagChips are a card's tags after its second line, cut to fit in width.
func (m Model) tagChips(line string, tags []string, width int) string {
	if len(tags) == 0 {
		return line
	}
	room := width - ansi.StringWidth(line) - 2
	if room < 4 {
		return line
	}
	chips := "#" + strings.Join(tags, " #")
	return line + "  " + m.pal.Fg(m.pal.Muted).Render(ansi.Truncate(chips, room, "…"))
}
