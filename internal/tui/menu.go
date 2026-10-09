package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// menuEntry is one action in the : menu: the key it stands for.
type menuEntry struct{ key, desc string }

// menuEntries are the dashboard's actions, from the help, once each.
func (m Model) menuEntries() []menuEntry {
	k := m.keys
	skip := map[string]bool{}
	for _, b := range []key.Binding{k.Up, k.Down, k.Menu} {
		skip[b.Help().Key] = true
	}
	var out []menuEntry
	seen := map[string]bool{}
	for _, col := range k.FullHelp() {
		for _, b := range col {
			h := b.Help()
			if skip[h.Key] || seen[h.Key] || len(b.Keys()) == 0 {
				continue
			}
			seen[h.Key] = true
			out = append(out, menuEntry{key: b.Keys()[0], desc: h.Desc})
		}
	}
	return out
}

// menuMatches are the entries matching the typed filter.
func (m Model) menuMatches() []menuEntry {
	q := strings.ToLower(strings.TrimSpace(m.menuInput.Value()))
	all := m.menuEntries()
	if q == "" {
		return all
	}
	var out []menuEntry
	for _, e := range all {
		if strings.Contains(strings.ToLower(e.desc), q) || e.key == q {
			out = append(out, e)
		}
	}
	return out
}

// openMenu shows the : menu over the table or the detail screen.
func (m Model) openMenu() (tea.Model, tea.Cmd) {
	m.menuFrom, m.mode, m.menuCursor = m.mode, modeMenu, 0
	m.menuInput.SetValue("")
	return m, m.menuInput.Focus()
}

// onMenuKey filters, moves, runs (enter) or closes (esc) the menu.
func (m Model) onMenuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	matches := m.menuMatches()
	switch msg.String() {
	case "esc":
		m.mode = m.menuFrom
		m.menuInput.Blur()
		return m, nil
	case "up", "ctrl+p":
		m.menuCursor = max(m.menuCursor-1, 0)
		return m, nil
	case "down", "ctrl+n":
		m.menuCursor = min(m.menuCursor+1, max(len(matches)-1, 0))
		return m, nil
	case "enter":
		m.mode = m.menuFrom
		m.menuInput.Blur()
		if m.menuCursor >= len(matches) {
			return m, nil
		}
		return m.onKey(pressOf(matches[m.menuCursor].key))
	}
	var cmd tea.Cmd
	m.menuInput, cmd = m.menuInput.Update(msg)
	m.menuCursor = 0
	return m, cmd
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

// menuScreen is the menu: the filter, then the matching actions.
func (m Model) menuScreen(h int) string {
	p := m.pal
	w := min(max(m.width-8, 40), 72)
	head := "All actions"
	if _, t, ok := m.selectedTheme(); ok {
		head += p.Fg(p.Muted).Render("  on " + t.Dir)
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render(head), "", m.menuInput.View(), ""}
	matches := m.menuMatches()
	room := max(h-10, 3)
	first := max(0, m.menuCursor-room+1)
	for i := first; i < len(matches) && i < first+room; i++ {
		e := matches[i]
		line := "  " + p.Fg(p.Muted).Render(pad(e.key, 6)) + e.desc
		if i == m.menuCursor {
			line = p.Fg(p.Accent).Render("▌ ") + lipgloss.NewStyle().Bold(true).Render(pad(e.key, 6)+e.desc)
		}
		lines = append(lines, ansi.Truncate(line, w, "…"))
	}
	if len(matches) == 0 {
		lines = append(lines, p.Fg(p.Muted).Render("  No action matches."))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Faint).Padding(1, 2).Width(w + 6)
	return lipgloss.Place(m.width, h, lipgloss.Center, lipgloss.Top, "\n"+box.Render(strings.Join(lines, "\n")))
}

// menuKeys is the help bar under the menu.
type menuKeys struct{}

func (menuKeys) ShortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "run")),
		key.NewBinding(key.WithKeys("up"), key.WithHelp("↑/↓", "choose")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),
	}
}

func (k menuKeys) FullHelp() [][]key.Binding { return [][]key.Binding{k.ShortHelp()} }
