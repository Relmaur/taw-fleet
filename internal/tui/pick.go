package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/agent"
)

// Claude's multiple-choice questions in the chat: one at a time, its options
// to pick with the arrows, a digit or enter, and a last one to type your own
// answer. Claude gets the answers as if picked in Claude Code.

type picker struct {
	req     *agent.Request
	qs      []agent.Choice
	at      int          // the question on screen
	cursor  int          // the option under the cursor; len(Options) = type my own
	ticked  map[int]bool // a multiple-choice question's picks
	typing  bool         // writing an answer of one's own
	draft   string       // what was in the chat's input before, and its placeholder
	hint    string
	answers map[string]string
}

func newPicker(r *agent.Request, qs []agent.Choice) *picker {
	return &picker{req: r, qs: qs, ticked: map[int]bool{}, answers: map[string]string{}}
}

func (p *picker) q() agent.Choice { return p.qs[p.at] }

// own is the "type my own answer" option's index.
func (p *picker) own() int { return len(p.q().Options) }

// onPickKey answers Claude's question with the person's keys.
func (m Model) onPickKey(c *chatState, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	pk := c.pick
	k := msg.String()
	switch k {
	case "ctrl+c":
		return m.stopChat(c)
	case "ctrl+n":
		return m.newChat(c)
	}
	if pk.typing {
		switch k {
		case "esc":
			pk.typing = false
			c.input.SetValue(pk.draft)
			c.input.Placeholder = pk.hint
			return m, nil
		case "enter":
			text := strings.TrimSpace(c.input.Value())
			if text == "" {
				return m, nil
			}
			pk.typing = false
			c.input.SetValue(pk.draft)
			c.input.Placeholder = pk.hint
			return m.answerPick(c, text)
		}
		var cmd tea.Cmd
		c.input, cmd = c.input.Update(msg)
		return m, cmd
	}
	q := pk.q()
	switch k {
	case "up", "k":
		pk.cursor = max(pk.cursor-1, 0)
	case "down", "j":
		pk.cursor = min(pk.cursor+1, pk.own())
	case "space":
		if q.Multi && pk.cursor < pk.own() {
			pk.ticked[pk.cursor] = !pk.ticked[pk.cursor]
			return m, nil
		}
		return m.pickCursor(c)
	case "enter":
		return m.pickCursor(c)
	case "esc":
		c.pick, c.ask = nil, nil
		_ = c.sess.Answer(pk.req, false, false, "The person closed the question without answering. Don't ask it again this way: ask in plain words in the chat.")
		c.entries = append(c.entries, chatEntry{kind: entryNote, text: "Not answered: Claude will ask in words instead."})
	default:
		if n, err := strconv.Atoi(k); err == nil && n >= 1 && n <= pk.own()+1 {
			pk.cursor = n - 1
			if q.Multi && pk.cursor < pk.own() {
				pk.ticked[pk.cursor] = !pk.ticked[pk.cursor]
				return m, nil
			}
			return m.pickCursor(c)
		}
	}
	return m, nil
}

// pickCursor takes the option under the cursor (a multiple-choice
// question: what's ticked, or else that one).
func (m Model) pickCursor(c *chatState) (tea.Model, tea.Cmd) {
	pk := c.pick
	q := pk.q()
	if pk.cursor == pk.own() {
		pk.typing, pk.draft, pk.hint = true, c.input.Value(), c.input.Placeholder
		c.input.SetValue("")
		c.input.Placeholder = "Your answer to Claude"
		return m, c.input.Focus()
	}
	if !q.Multi {
		return m.answerPick(c, q.Options[pk.cursor].Label)
	}
	var labels []string
	for i, o := range q.Options {
		if pk.ticked[i] {
			labels = append(labels, o.Label)
		}
	}
	if len(labels) == 0 {
		labels = []string{q.Options[pk.cursor].Label}
	}
	return m.answerPick(c, strings.Join(labels, ", "))
}

// answerPick keeps the answer to the question on screen, then shows the next
// one, or sends them all to Claude.
func (m Model) answerPick(c *chatState, answer string) (tea.Model, tea.Cmd) {
	pk := c.pick
	q := pk.q()
	pk.answers[q.Text] = answer
	c.entries = append(c.entries, chatEntry{kind: entryAnswer, text: q.Text, tool: answer})
	if pk.at+1 < len(pk.qs) {
		pk.at, pk.cursor, pk.ticked = pk.at+1, 0, map[int]bool{}
		return m, nil
	}
	c.pick, c.ask = nil, nil
	if err := c.sess.AnswerQuestions(pk.req, pk.answers); err != nil {
		c.entries = append(c.entries, chatEntry{kind: entryErr, text: err.Error()})
	}
	return m, nil
}

// pickCard is Claude's question, its options and the cursor.
func (m Model) pickCard(c *chatState) []string {
	p := m.pal
	muted := p.Fg(p.Muted)
	pk := c.pick
	q := pk.q()
	w := m.width - 8
	head := " " + p.Fg(p.Warn).Render("? ") + lipgloss.NewStyle().Bold(true).Render("Claude asks")
	if q.Header != "" {
		head += muted.Render(" · " + q.Header)
	}
	if len(pk.qs) > 1 {
		head += muted.Render(fmt.Sprintf(" · %d of %d", pk.at+1, len(pk.qs)))
	}
	if q.Multi {
		head += muted.Render(" · pick any")
	}
	out := []string{head}
	for _, l := range wrap(q.Text, m.width-6, 4) {
		out = append(out, "   "+lipgloss.NewStyle().Bold(true).Render(l))
	}
	out = append(out, "")
	for i := 0; i <= pk.own(); i++ {
		label, desc := "Something else: type my own answer", ""
		if i < pk.own() {
			label, desc = q.Options[i].Label, q.Options[i].Description
		}
		on := i == pk.cursor
		mark := "  "
		if on {
			mark = p.Fg(p.Accent).Render("▸") + " "
		}
		box := ""
		if q.Multi && i < pk.own() {
			box = "[ ] "
			if pk.ticked[i] {
				box = p.Fg(p.OK).Render("[x]") + " "
			}
		}
		text := ansi.Truncate(label, w-6, "…")
		switch {
		case on:
			text = lipgloss.NewStyle().Bold(true).Render(text)
		case i == pk.own():
			text = muted.Render(text)
		}
		out = append(out, " "+mark+muted.Render(strconv.Itoa(i+1))+"  "+box+text)
		if on && desc != "" {
			for _, l := range wrap(desc, w-4, 3) {
				out = append(out, "        "+muted.Render(l))
			}
		}
	}
	if pk.typing {
		out = append(out, "", " "+c.input.View())
	}
	return out
}

// pickKeys are the footer's keys while Claude asks.
type pickKeys struct {
	multi, typing bool
	n             int
}

func (k pickKeys) ShortHelp() []key.Binding {
	if k.typing {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "answer")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back to the options")),
		}
	}
	b := []key.Binding{
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑↓", "move")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "pick")),
	}
	if k.multi {
		b = []key.Binding{
			key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑↓", "move")),
			key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "tick")),
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "done")),
		}
	}
	by := "pick by number"
	if k.multi {
		by = "tick by number"
	}
	return append(b,
		key.NewBinding(key.WithKeys("1"), key.WithHelp(fmt.Sprintf("1–%d", k.n), by)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "don't answer")))
}

func (k pickKeys) FullHelp() [][]key.Binding { return [][]key.Binding{k.ShortHelp()} }
