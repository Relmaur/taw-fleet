package tui

import "charm.land/bubbles/v2/key"

// keyMap is every key the dashboard answers to. It also feeds the help bar.
type keyMap struct {
	Up, Down, PageUp, PageDown, Top, Bottom   key.Binding
	Detail, Back, Filter, Refresh, Help, Quit key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		PageUp:   key.NewBinding(key.WithKeys("pgup", "ctrl+u"), key.WithHelp("pgup", "page up")),
		PageDown: key.NewBinding(key.WithKeys("pgdown", "ctrl+d"), key.WithHelp("pgdn", "page down")),
		Top:      key.NewBinding(key.WithKeys("home"), key.WithHelp("home", "first")),
		Bottom:   key.NewBinding(key.WithKeys("end"), key.WithHelp("end", "last")),
		Detail:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "details")),
		Back:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Refresh:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// detailKeys is the help bar on the full-screen detail.
type detailKeys struct{ k keyMap }

func (d detailKeys) ShortHelp() []key.Binding {
	up := key.NewBinding(key.WithKeys("up"), key.WithHelp("↑/↓", "scroll"))
	return []key.Binding{up, d.k.PageDown, d.k.Back, d.k.Help, d.k.Quit}
}

func (d detailKeys) FullHelp() [][]key.Binding { return d.k.FullHelp() }

// ShortHelp is the one-line help bar.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Detail, k.Filter, k.Refresh, k.Help, k.Quit}
}

// FullHelp is the help screen, in columns.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom},
		{k.Detail, k.Back, k.Filter, k.Refresh},
		{k.Help, k.Quit},
	}
}
