package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// openMenu shows the : menu (the palette of every action) over the table or
// the detail screen.
func (m Model) openMenu() (tea.Model, tea.Cmd) {
	m.menuFrom, m.mode, m.menuCursor = m.mode, modeMenu, 0
	m.menuInput.SetValue("")
	m.menuInput.Placeholder = "type to find an action: a word, a key, or letters in order"
	return m, m.menuInput.Focus()
}

// onPaletteKey filters, moves, runs (enter) or closes (esc) the : menu or the
// a picker.
func (m Model) onPaletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	items := m.paletteItems()
	switch msg.String() {
	case "esc":
		return m.closePalette(), nil
	case "up", "ctrl+p", "shift+tab":
		m.menuCursor = max(m.menuCursor-1, 0)
		return m, nil
	case "down", "ctrl+n", "tab":
		m.menuCursor = min(m.menuCursor+1, max(len(items)-1, 0))
		return m, nil
	case "pgup":
		m.menuCursor = max(m.menuCursor-8, 0)
		return m, nil
	case "pgdown":
		m.menuCursor = min(m.menuCursor+8, max(len(items)-1, 0))
		return m, nil
	case "enter":
		skills := m.mode == modeSkills
		m = m.closePalette()
		if m.menuCursor >= len(items) {
			return m, nil
		}
		it := items[m.menuCursor]
		if it.disabled != "" {
			m.setFlash(it.title+": "+it.disabled, true)
			return m, nil
		}
		if skills {
			return m.runSkill(*it.skill)
		}
		m.remember(it.key)
		return m.onKey(pressOf(it.key))
	}
	var cmd tea.Cmd
	m.menuInput, cmd = m.menuInput.Update(msg)
	m.menuCursor = 0
	return m, cmd
}

func (m Model) closePalette() Model {
	m.mode = m.menuFrom
	m.menuInput.Blur()
	return m
}

// remember puts an action first among the : menu's recent ones (three kept).
func (m *Model) remember(k string) {
	recent := []string{k}
	for _, r := range m.recent {
		if r != k && len(recent) < 3 {
			recent = append(recent, r)
		}
	}
	m.recent = recent
}

// pressOf is the key press for a binding's key name.
func pressOf(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	if rest, ok := strings.CutPrefix(k, "ctrl+"); ok {
		return tea.KeyPressMsg{Code: []rune(rest)[0], Mod: tea.ModCtrl}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

// menuKeys is the help bar under the : menu and the a picker.
type menuKeys struct{}

func (menuKeys) ShortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "run")),
		key.NewBinding(key.WithKeys("up"), key.WithHelp("↑/↓ tab", "choose")),
		key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgup/pgdn", "jump")),
		key.NewBinding(key.WithKeys("x"), key.WithHelp("type", "filter")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),
	}
}

func (k menuKeys) FullHelp() [][]key.Binding { return [][]key.Binding{k.ShortHelp()} }
