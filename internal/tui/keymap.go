package tui

import "charm.land/bubbles/v2/key"

// keyMap is every key the dashboard answers to. It also feeds the help bar.
type keyMap struct {
	Up, Down, PageUp, PageDown, Top, Bottom   key.Binding
	Detail, Back, Filter, Refresh, Help, Quit key.Binding

	// Shortcuts on the selected theme.
	Editor, Finder, Browser, Admin, GitHub, PRs, Terminal, Production key.Binding
	Handoff, Copy, Launch                                             key.Binding
	StartStop, Restart, Yes, No                                       key.Binding
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

		Editor:     key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "editor")),
		Finder:     key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "Finder")),
		Browser:    key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "site")),
		Admin:      key.NewBinding(key.WithKeys("B"), key.WithHelp("B", "wp-admin")),
		GitHub:     key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "GitHub")),
		PRs:        key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "pull requests")),
		Terminal:   key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "terminal")),
		Production: key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "production")),
		Handoff:    key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "hand off update")),
		Copy:       key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy prompt")),
		Launch:     key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "launch Claude Code")),
		StartStop:  key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "start/stop site")),
		Restart:    key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "restart site")),
		Yes:        key.NewBinding(key.WithKeys("y", "enter"), key.WithHelp("y", "yes")),
		No:         key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("n", "no")),
	}
}

// detailKeys is the help bar on the full-screen detail.
type detailKeys struct{ k keyMap }

func (d detailKeys) ShortHelp() []key.Binding {
	up := key.NewBinding(key.WithKeys("up"), key.WithHelp("↑/↓", "scroll"))
	return []key.Binding{up, d.k.PageDown, d.k.Back, d.k.Help, d.k.Quit}
}

func (d detailKeys) FullHelp() [][]key.Binding { return d.k.FullHelp() }

// handoffKeys is the help bar on the handoff screen.
type handoffKeys struct{ k keyMap }

func (h handoffKeys) ShortHelp() []key.Binding {
	up := key.NewBinding(key.WithKeys("up"), key.WithHelp("↑/↓", "scroll"))
	return []key.Binding{h.k.Copy, h.k.Launch, up, h.k.Back}
}

func (h handoffKeys) FullHelp() [][]key.Binding { return h.k.FullHelp() }

// confirmKeys is the help bar while a question is open.
type confirmKeys struct{ k keyMap }

func (c confirmKeys) ShortHelp() []key.Binding  { return []key.Binding{c.k.Yes, c.k.No} }
func (c confirmKeys) FullHelp() [][]key.Binding { return c.k.FullHelp() }

// ShortHelp is the one-line help bar.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Detail, k.Editor, k.GitHub, k.Browser, k.StartStop, k.Handoff, k.Filter, k.Help, k.Quit}
}

// FullHelp is the help screen, in columns.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom},
		{k.Detail, k.Back, k.Filter, k.Refresh, k.StartStop, k.Restart, k.Help, k.Quit},
		{k.Editor, k.Finder, k.Terminal, k.Browser, k.Admin, k.Production},
		{k.GitHub, k.PRs, k.Handoff, k.Copy, k.Launch},
	}
}
