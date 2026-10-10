package tui

import (
	"fmt"

	"charm.land/bubbles/v2/key"

	"github.com/Relmaur/taw-fleet/internal/actions"
)

// keyMap is every key the dashboard answers to. It also feeds the help bar.
type keyMap struct {
	Up, Down, PageUp, PageDown, Top, Bottom               key.Binding
	Detail, Back, Filter, Refresh, RefreshAll, Help, Quit key.Binding

	// Shortcuts on the selected theme.
	Editor, Finder, Browser, Admin, GitHub, PRs, Terminal, Production key.Binding
	Handoff, Copy, Agent, UpdateAll                                   key.Binding
	StartStop, Restart, Work, Yes, No                                 key.Binding
	SyncCheck, SyncAll, SyncApply, UpdateCore, Output, Menu           key.Binding
	New, CopySecret, LiveRefresh, Merge, Pull                         key.Binding
	Comments, Resolve, Ask                                            key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		PageUp:     key.NewBinding(key.WithKeys("pgup", "ctrl+u"), key.WithHelp("pgup", "page up")),
		PageDown:   key.NewBinding(key.WithKeys("pgdown", "ctrl+d"), key.WithHelp("pgdn", "page down")),
		Top:        key.NewBinding(key.WithKeys("home"), key.WithHelp("home", "first")),
		Bottom:     key.NewBinding(key.WithKeys("end"), key.WithHelp("end", "last")),
		Detail:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "details")),
		Back:       key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		Filter:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Refresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		RefreshAll: key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "refresh all")),
		Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),

		Editor:      key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "editor")),
		Finder:      key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "Finder")),
		Browser:     key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "site")),
		Admin:       key.NewBinding(key.WithKeys("B"), key.WithHelp("B", "wp-admin")),
		GitHub:      key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "GitHub")),
		PRs:         key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "PRs")),
		Terminal:    key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "terminal")),
		Production:  key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "production")),
		Handoff:     key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "hand off")),
		Copy:        key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy prompt")),
		Agent:       key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "update with agent")),
		UpdateAll:   key.NewBinding(key.WithKeys("U"), key.WithHelp("U", "update all")),
		StartStop:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "start/stop")),
		Restart:     key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "restart")),
		Work:        key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "work on it")),
		Yes:         key.NewBinding(key.WithKeys("y", "enter"), key.WithHelp("y", "yes")),
		No:          key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("n", "no")),
		SyncCheck:   key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "sync check")),
		SyncAll:     key.NewBinding(key.WithKeys("Y"), key.WithHelp("Y", "check all themes")),
		Menu:        key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "all actions")),
		SyncApply:   key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "apply Tier 1")),
		UpdateCore:  key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "update this site")),
		Output:      key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "last output")),
		New:         key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new site")),
		CopySecret:  key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy password")),
		LiveRefresh: key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "check production")),
		Merge:       key.NewBinding(key.WithKeys("M"), key.WithHelp("M", "merge PR")),
		Pull:        key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "pull content")),
		Comments:    key.NewBinding(key.WithKeys("F"), key.WithHelp("F", "comments")),
		Resolve:     key.NewBinding(key.WithKeys("X"), key.WithHelp("X", "resolve comments")),
		Ask:         key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "ask Claude…")),
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
	return []key.Binding{h.k.Agent, h.k.Copy, up, h.k.Back}
}

func (h handoffKeys) FullHelp() [][]key.Binding { return h.k.FullHelp() }

// outputKeys is the help bar on the output view.
type outputKeys struct {
	k       keyMap
	running bool
	secret  bool // the finished task has something to copy (c)
}

func (o outputKeys) ShortHelp() []key.Binding {
	scroll := key.NewBinding(key.WithKeys("up"), key.WithHelp("↑/↓", "scroll"))
	follow := key.NewBinding(key.WithKeys("end"), key.WithHelp("end", "follow"))
	back := key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))
	if o.running {
		back = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back (it keeps running)"))
	}
	keys := []key.Binding{scroll, follow, back}
	if o.secret {
		keys = append([]key.Binding{o.k.CopySecret}, keys...)
	}
	return keys
}

func (o outputKeys) FullHelp() [][]key.Binding { return o.k.FullHelp() }

// stepKeys is the help bar on a task with a checklist (an update).
type stepKeys struct{ t *taskState }

func (s stepKeys) ShortHelp() []key.Binding {
	if s.t.batch != nil && !s.t.focus && !s.t.showLog {
		pick := key.NewBinding(key.WithKeys("enter"), key.WithHelp("↑/↓ enter", "a theme's steps"))
		keys := []key.Binding{pick, key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "full log"))}
		if it := s.t.batch[s.t.sel]; it.outcome != nil && it.outcome.Err == nil {
			if d := it.outcome.Report.Delivered; d != nil && d.URL != "" {
				keys = append(keys, key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "its pull request")))
			}
			keys = append(keys, key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "its report")))
		}
		back := "back"
		if s.t.running {
			back = "back (it keeps running)"
		}
		return append(keys, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", back)))
	}
	log := key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "full log"))
	if s.t.showLog {
		log = key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "steps"))
	}
	back := key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))
	if s.t.batch != nil {
		back = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "all themes"))
	}
	if s.t.running {
		return []key.Binding{log, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back (it keeps running)"))}
	}
	keys := []key.Binding{log}
	if o, ok := s.t.summary.Report.(actions.UpdateOutcome); ok {
		if o.Report.Delivered != nil && o.Report.Delivered.URL != "" {
			keys = append(keys, key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open the pull request")))
		}
		keys = append(keys, key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "the report")))
	}
	return append(keys, back)
}

func (s stepKeys) FullHelp() [][]key.Binding { return [][]key.Binding{s.ShortHelp()} }

// confirmKeys is the help bar while a question is open.
type confirmKeys struct {
	k       keyMap
	agent   bool // A is offered too
	choices int  // a numbered choice instead of yes/no
}

func (c confirmKeys) ShortHelp() []key.Binding {
	if c.choices > 0 {
		pick := key.NewBinding(key.WithKeys("1"), key.WithHelp(fmt.Sprintf("1–%d", c.choices), "pick"))
		return []key.Binding{pick, c.k.No}
	}
	if c.agent {
		return []key.Binding{c.k.Yes, c.k.Agent, c.k.No}
	}
	return []key.Binding{c.k.Yes, c.k.No}
}
func (c confirmKeys) FullHelp() [][]key.Binding { return c.k.FullHelp() }

// ShortHelp is the one-line help bar.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Detail, k.Editor, k.Terminal, k.UpdateAll, k.Menu, k.Filter, k.Help, k.Quit}
}

// FullHelp is the help screen, in columns.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Filter, k.Refresh, k.Menu, k.Output, k.New, k.Quit},
		{k.Detail, k.RefreshAll, k.LiveRefresh, k.Work, k.StartStop, k.Restart, k.Merge, k.Pull, k.Ask},
		{k.Editor, k.Finder, k.Terminal, k.Browser, k.Admin, k.Production, k.GitHub, k.PRs, k.Comments},
		{k.SyncCheck, k.SyncAll, k.SyncApply, k.UpdateCore, k.Agent, k.UpdateAll, k.Handoff, k.Copy, k.Resolve},
	}
}

// createKeys is the help bar under the new-site form (the form shows its own
// keys inside the box).
type createKeys struct{}

func (createKeys) ShortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
	}
}

func (c createKeys) FullHelp() [][]key.Binding { return [][]key.Binding{c.ShortHelp()} }
