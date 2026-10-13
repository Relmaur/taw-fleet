package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/agent"
)

// The chat (>): the person's own Claude Code, driven through package agent,
// about the whole fleet (from the table) or one site (from its details).
// Each scope keeps its conversation while the dashboard runs; esc leaves it
// running.

type entryKind int

const (
	entryYou entryKind = iota
	entryClaude
	entryTool
	entryNote
	entryErr
)

type chatEntry struct {
	kind    entryKind
	text    string
	tool    string // entryTool: the tool, as people read it
	id      string // entryTool: Claude Code's tool_use id
	running bool   // entryTool: no answer yet
	failed  bool
	hidden  bool // entryTool: a taw-fleet write that only returned its question
	open    bool // entryClaude: still being written
}

type chatState struct {
	key, title string
	scope      agent.Scope

	sess    *agent.Session
	gen     int    // which claude the events are from: a stopped one's are dropped
	session string // Claude Code's session id, to resume after a stop
	model   string

	entries   []chatEntry
	ask       *agent.Request
	question  string            // what the person is asked about ask
	questions map[string]string // a taw-fleet call in words (tool and what it acts on) → the question it returned
	working   bool
	since     time.Time
	cost      float64
	queued    []string // typed while Claude works: sent as the next message when it finishes

	input  textinput.Model
	scroll int // lines up from the bottom
}

// chatEventMsg is one event of a conversation.
type chatEventMsg struct {
	key string
	gen int
	ev  agent.Event
	ch  <-chan agent.Event
}

// chatStartedMsg is a conversation's claude up, or not.
type chatStartedMsg struct {
	key  string
	gen  int
	sess *agent.Session
	err  error
}

func listenChat(key string, gen int, ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return chatEventMsg{key, gen, ev, ch}
	}
}

// openChat opens the conversation about the fleet (table) or the selected
// site (details), starting a new one the first time.
func (m Model) openChat() (tea.Model, tea.Cmd) {
	if m.deps.Chat == nil {
		m.setFlash("the chat needs Claude Code (`claude`): see https://claude.com/claude-code", true)
		return m, nil
	}
	sc, title := agent.Scope{}, "the fleet"
	if m.mode == modeDetail {
		s, t, ok := m.selectedTheme()
		if !ok {
			return m, nil
		}
		sc, title = agent.ScopeOf(s, t), s.Slug
	}
	k := "fleet"
	if sc.Site != "" {
		k = sc.Site + "/" + sc.Theme
	}
	if m.chats == nil {
		m.chats = map[string]*chatState{}
	}
	c := m.chats[k]
	if c == nil {
		in := textinput.New()
		in.Prompt = "› "
		in.Placeholder = "Ask Claude to do anything TAW on " + title
		if sc.Site == "" {
			in.Placeholder = "Ask Claude about any site, or the whole fleet"
		}
		in.SetStyles(textinput.DefaultStyles(m.dark))
		c = &chatState{key: k, title: title, scope: sc, input: in, questions: map[string]string{}}
		m.chats[k] = c
	}
	m.chatFrom, m.chat, m.mode = m.mode, k, modeChat
	c.input.SetWidth(max(m.width-4, 10))
	return m, c.input.Focus()
}

func (m Model) currentChat() *chatState { return m.chats[m.chat] }

// chatWorking reports whether Claude is answering in any conversation.
func (m Model) chatWorking() bool {
	for _, c := range m.chats {
		if c.working {
			return true
		}
	}
	return false
}

// chatStatus is the footer's line while a conversation works off screen.
func (m Model) chatStatus() string {
	var on []string
	hint := "  (> on its details opens it)"
	for _, c := range m.chats {
		if c.working && (m.mode != modeChat || c.key != m.chat) {
			if c.scope.Site == "" {
				hint = "  (> opens it)"
			}
			w := c.title
			if c.ask != nil {
				w += " (waiting for your answer)"
			}
			on = append(on, w)
		}
	}
	if len(on) == 0 {
		return ""
	}
	sort.Strings(on)
	return "Claude is working on " + strings.Join(on, ", ") + hint
}

// sendChat sends what the person typed, starting claude (or resuming the
// conversation) when it isn't running. While Claude works, the message waits
// in the queue, on screen, and goes when the turn ends.
func (m Model) sendChat() (tea.Model, tea.Cmd) {
	c := m.currentChat()
	text := strings.TrimSpace(c.input.Value())
	if text == "" {
		return m, nil
	}
	c.input.SetValue("")
	if c.working {
		c.queued, c.scroll = append(c.queued, text), 0
		return m, nil
	}
	return m.say(c, text)
}

// say puts text in the conversation and sends it.
func (m Model) say(c *chatState, text string) (tea.Model, tea.Cmd) {
	c.entries = append(c.entries, chatEntry{kind: entryYou, text: text})
	c.working, c.since, c.scroll = true, m.deps.Now(), 0
	if c.sess != nil {
		if err := c.sess.Say(text); err != nil {
			c.entries = append(c.entries, chatEntry{kind: entryErr, text: err.Error()})
			c.working = false
		}
		return m, m.spin.Tick
	}
	c.gen++
	start, ctx, sc, resume, k, gen := m.deps.Chat, m.ctx, c.scope, c.session, c.key, c.gen
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		s, err := start(ctx, sc, resume)
		if err == nil {
			err = s.Say(text)
		}
		return chatStartedMsg{k, gen, s, err}
	})
}

func (m Model) onChatStarted(msg chatStartedMsg) (tea.Model, tea.Cmd) {
	c := m.chats[msg.key]
	if c == nil || msg.gen != c.gen {
		if msg.sess != nil {
			msg.sess.Stop()
		}
		return m, nil
	}
	if msg.err != nil {
		c.working = false
		c.unqueue()
		c.entries = append(c.entries, chatEntry{kind: entryErr, text: "Claude Code didn't start: " + msg.err.Error()})
		if msg.sess != nil {
			msg.sess.Stop()
		}
		return m, nil
	}
	c.sess = msg.sess
	return m, listenChat(msg.key, msg.gen, msg.sess.Events())
}

func (m Model) onChatEvent(msg chatEventMsg) (tea.Model, tea.Cmd) {
	c := m.chats[msg.key]
	if c == nil || msg.gen != c.gen || c.sess == nil {
		return m, nil // a stopped claude's last words
	}
	next := listenChat(msg.key, msg.gen, msg.ch)
	ev := msg.ev
	switch ev.Kind {
	case agent.Started:
		c.session, c.model = ev.Session, ev.Model
	case agent.Text:
		if n := len(c.entries); n > 0 && c.entries[n-1].kind == entryClaude && c.entries[n-1].open {
			c.entries[n-1].text += ev.Text
		} else {
			c.entries = append(c.entries, chatEntry{kind: entryClaude, text: ev.Text, open: true})
		}
	case agent.Tool:
		c.closeText()
		// A taw-fleet write without its token only fetches the question,
		// which comes next: the line is the call that does it.
		quiet := agent.OnlyAsks(&agent.Request{Tool: ev.Name, Input: ev.Input}, m.deps.ChatWrites) && m.deps.ChatWrites[agent.FleetTool(ev.Name)]
		c.entries = append(c.entries, chatEntry{kind: entryTool, id: ev.ToolID, tool: toolWords(ev.Name, ev.Summary), running: true, hidden: quiet})
	case agent.Result:
		for i := range c.entries {
			e := &c.entries[i]
			if e.kind == entryTool && e.id == ev.ToolID {
				e.running, e.failed = false, ev.Failed
				if ev.Failed {
					e.text = firstLine(ev.Text)
				}
			}
		}
	case agent.Ask:
		r := ev.Request
		if agent.OnlyAsks(r, m.deps.ChatWrites) {
			// It only returns the dashboard's question; the person answers
			// that one when the token comes back.
			_ = c.sess.Answer(r, true, false, "")
			return m, next
		}
		c.closeText()
		c.ask, c.question = r, askWords(r, c.questions)
	case agent.TurnDone:
		c.closeText()
		c.working, c.cost = false, ev.Cost
		if ev.Failed && ev.Text != "" {
			c.entries = append(c.entries, chatEntry{kind: entryErr, text: ev.Text})
		}
		if len(c.queued) > 0 {
			text := strings.Join(c.queued, "\n\n")
			c.queued = nil
			model, cmd := m.say(c, text)
			return model, tea.Batch(next, cmd)
		}
	case agent.Ended:
		c.closeText()
		c.sess, c.working, c.ask = nil, false, nil
		c.unqueue()
		if ev.Err != nil {
			c.entries = append(c.entries, chatEntry{kind: entryErr, text: "Claude Code stopped: " + ev.Err.Error()})
		}
		return m, nil
	}
	// A question a taw-fleet write returned: shown again when its token comes back.
	if ev.Kind == agent.Result {
		if q := agent.Question(ev.Text); q != "" {
			for _, e := range c.entries {
				if e.id == ev.ToolID {
					c.questions[e.tool] = q
				}
			}
		}
	}
	return m, next
}

// unqueue puts the messages that were waiting back in the input, for the
// person to send again: nothing typed is lost when Claude stops.
func (c *chatState) unqueue() {
	if len(c.queued) == 0 {
		return
	}
	text := strings.Join(c.queued, " ")
	if v := strings.TrimSpace(c.input.Value()); v != "" {
		text += " " + v
	}
	c.queued = nil
	c.input.SetValue(text)
	c.input.CursorEnd()
}

func (c *chatState) closeText() {
	if n := len(c.entries); n > 0 && c.entries[n-1].kind == entryClaude {
		c.entries[n-1].open = false
	}
}

// toolWords is a tool call as people read it: "taw-fleet merge pr  acme"
// or "Bash  git status".
func toolWords(name, summary string) string {
	if t := agent.FleetTool(name); t != "" {
		name = "taw-fleet " + strings.ReplaceAll(t, "_", " ")
		if summary == "" {
			return name
		}
		return name + "  " + summary
	}
	if i := strings.LastIndex(name, "__"); strings.HasPrefix(name, "mcp__") && i > 0 {
		name = name[i+2:]
	}
	if summary == "" {
		return name
	}
	return name + "  " + summary
}

// askWords is the question for a permission prompt: the dashboard's own
// question for a taw-fleet write, otherwise what Claude Code would run.
func askWords(r *agent.Request, questions map[string]string) string {
	if agent.FleetTool(r.Tool) != "" {
		if q := questions[toolWords(r.Tool, agent.Summarize(r.Tool, r.Input))]; q != "" {
			return q
		}
	}
	what := agent.Summarize(r.Tool, r.Input)
	name := toolWords(r.Tool, "")
	if r.Description != "" && r.Description != what {
		return fmt.Sprintf("Claude wants to use %s: %s", name, r.Description) + muteSep + what
	}
	return fmt.Sprintf("Claude wants to use %s", name) + muteSep + what
}

// muteSep splits a question from its detail, shown dimmer.
const muteSep = "\x00"

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// onChatKey handles keys in the chat.
func (m Model) onChatKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := m.currentChat()
	if c == nil {
		m.mode = modeTable
		return m, nil
	}
	if c.ask != nil {
		r := c.ask
		switch msg.String() {
		case "y", "a":
			always := msg.String() == "a" && len(r.Suggestions) > 0
			c.ask, c.question = nil, ""
			if err := c.sess.Answer(r, true, always, ""); err != nil {
				c.entries = append(c.entries, chatEntry{kind: entryErr, text: err.Error()})
			}
		case "n", "esc":
			c.ask, c.question = nil, ""
			_ = c.sess.Answer(r, false, false, "The person said no. Don't try another way to do the same; ask what they want instead.")
		case "ctrl+c":
			return m.stopChat(c)
		case "ctrl+n":
			return m.newChat(c)
		}
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c":
		// Stops Claude; never quits the dashboard from here (esc, then q).
		if c.working {
			return m.stopChat(c)
		}
		m.setTip("Nothing to stop: esc goes back to the list, and q quits there.")
		return m, nil
	case "ctrl+n":
		return m.newChat(c)
	case "esc":
		m.mode = m.chatFrom
		c.input.Blur()
		return m, nil
	case "enter":
		return m.sendChat()
	case "pgup":
		c.scroll += max(m.bodyHeight()-4, 1)
		return m, nil
	case "pgdown":
		c.scroll = max(c.scroll-max(m.bodyHeight()-4, 1), 0)
		return m, nil
	}
	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg)
	return m, cmd
}

// newChat ends this conversation: claude stops, the screen clears, and the next
// message starts a new one. The old one stays in Claude Code's history.
func (m Model) newChat(c *chatState) (tea.Model, tea.Cmd) {
	if len(c.entries) == 0 && c.sess == nil && len(c.queued) == 0 {
		return m, nil
	}
	if c.sess != nil {
		c.sess.Stop()
	}
	c.unqueue()
	c.gen++
	c.sess, c.session, c.model, c.cost = nil, "", "", 0
	c.entries, c.ask, c.question, c.working, c.scroll = nil, nil, "", false, 0
	c.questions = map[string]string{}
	where := "the fleet folder"
	if c.scope.Site != "" {
		where = "the theme folder"
	}
	m.setFlash("New chat. The last one stays in Claude Code's history: claude --resume in "+where+".", false)
	return m, nil
}

// stopChat ends claude; the next message resumes the conversation.
func (m Model) stopChat(c *chatState) (tea.Model, tea.Cmd) {
	if c.sess != nil {
		c.sess.Stop()
	}
	c.gen++
	c.sess, c.ask, c.question, c.working = nil, nil, "", false
	c.unqueue()
	c.entries = append(c.entries, chatEntry{kind: entryNote, text: "Stopped. Your next message carries on from here."})
	return m, nil
}

// --- view -------------------------------------------------------------------

func (m Model) chatScreen(height int) string {
	c := m.currentChat()
	if c == nil {
		return block("", m.width, height)
	}
	p := m.pal
	muted := p.Fg(p.Muted)
	rule := p.Fg(p.Faint).Render(strings.Repeat("─", m.width))

	head := " " + lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render(p.I.Claude+" Claude") + muted.Render(" · ") +
		lipgloss.NewStyle().Bold(true).Render(c.title)
	if c.scope.Site != "" && c.scope.Theme != "" {
		head += muted.Render(" (" + c.scope.Theme + ")")
	}
	var info []string
	if c.model != "" {
		info = append(info, strings.TrimPrefix(c.model, "claude-"))
	}
	if c.cost > 0 {
		info = append(info, fmt.Sprintf("$%.2f", c.cost))
	}
	if r := strings.Join(info, " · "); r != "" {
		gap := m.width - ansi.StringWidth(head) - ansi.StringWidth(r) - 1
		if gap > 1 {
			head += strings.Repeat(" ", gap) + muted.Render(r)
		}
	}

	var bottom []string
	if c.ask != nil {
		bottom = m.askCard(c)
	} else {
		bottom = []string{" " + c.input.View()}
	}
	lines := m.transcript(c, m.width-2)
	room := max(height-3-len(bottom), 1)
	end := max(len(lines)-c.scroll, 0)
	start := max(end-room, 0)
	shown := lines[start:end]
	if len(lines) == 0 {
		shown = m.chatWelcome(c)
	}
	body := make([]string, 0, room)
	for len(body)+len(shown) < room {
		body = append(body, "")
	}
	body = append(body, shown...)
	out := []string{head, rule}
	out = append(out, body...)
	out = append(out, rule)
	out = append(out, bottom...)
	return block(strings.Join(out, "\n"), m.width, height)
}

// chatWelcome is what an empty conversation shows: what to ask.
func (m Model) chatWelcome(c *chatState) []string {
	p := m.pal
	muted := p.Fg(p.Muted)
	ex := []string{"what needs attention across the fleet?", "update every site that's behind", "which sites have open comments?",
		"make me a site for a bakery: <link to a design, Figma, a website, or a brief>"}
	intro := "Your Claude Code, with taw-fleet's tools. It asks before it changes anything."
	if c.scope.Site != "" {
		ex = []string{"what's wrong with this site?", "move the hero image above the title on the home page",
			"resolve the client's comments", "why is it slow on mobile?"}
	}
	out := []string{"", "  " + intro, "", "  " + muted.Render("For example:")}
	for _, e := range ex {
		out = append(out, "  "+muted.Render("  “"+e+"”"))
	}
	return out
}

// askCard is a permission prompt, above the keys that answer it.
func (m Model) askCard(c *chatState) []string {
	p := m.pal
	muted := p.Fg(p.Muted)
	q, detail, _ := strings.Cut(c.question, muteSep)
	out := []string{" " + p.Fg(p.Warn).Render("? ") + lipgloss.NewStyle().Bold(true).Render(ansi.Truncate(q, m.width-4, "…"))}
	if detail != "" {
		for _, l := range wrap(detail, m.width-6, 3) {
			out = append(out, "   "+muted.Render(l))
		}
	}
	return out
}

var (
	boldRE = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	codeRE = regexp.MustCompile("`([^`]+)`")
)

// transcript is the conversation as screen lines, w wide.
func (m Model) transcript(c *chatState, w int) []string {
	p := m.pal
	muted := p.Fg(p.Muted)
	var out []string
	for i, e := range c.entries {
		switch e.kind {
		case entryYou:
			if i > 0 {
				out = append(out, "")
			}
			for j, l := range wrap(e.text, w-6, 0) {
				lead := "      "
				if j == 0 {
					lead = " " + lipgloss.NewStyle().Bold(true).Foreground(p.Brand).Render("you") + "  "
				}
				out = append(out, lead+lipgloss.NewStyle().Bold(true).Render(l))
			}
			out = append(out, "")
		case entryClaude:
			first := true
			for _, para := range strings.Split(strings.TrimSpace(e.text), "\n") {
				if strings.TrimSpace(para) == "" {
					out = append(out, "")
					continue
				}
				for _, l := range wrap(para, w-6, 0) {
					lead := "      "
					if first {
						lead, first = " "+p.Fg(p.Accent).Render(p.I.Claude)+"    ", false
					}
					out = append(out, lead+m.inline(l))
				}
			}
		case entryTool:
			if e.hidden {
				continue
			}
			mark := p.Fg(p.OK).Render(p.I.Done)
			switch {
			case e.running:
				mark = m.spin.View()
			case e.failed:
				mark = p.Fg(p.Err).Render(p.I.Failed)
			}
			out = append(out, "      "+mark+" "+muted.Render(ansi.Truncate(e.tool, w-9, "…")))
			if e.failed && e.text != "" {
				out = append(out, "        "+p.Fg(p.Err).Render(ansi.Truncate(e.text, w-9, "…")))
			}
		case entryNote:
			out = append(out, "", "      "+muted.Render(e.text))
		case entryErr:
			for _, l := range wrap(e.text, w-8, 0) {
				out = append(out, "      "+p.Fg(p.Err).Render(l))
			}
		}
	}
	if c.working && c.ask == nil {
		out = append(out, "", "      "+m.spin.View()+" "+muted.Render("working… "+clock(m.deps.Now().Sub(c.since))))
	}
	for i, q := range c.queued {
		out = append(out, "")
		for j, l := range wrap(q, w-6, 0) {
			lead := "      "
			if j == 0 {
				lead = " " + p.Fg(p.Brand).Render("you") + "  "
			}
			out = append(out, lead+l)
		}
		if i == len(c.queued)-1 {
			out = append(out, "      "+muted.Render("queued: goes to Claude when it finishes this"))
		}
	}
	return out
}

// inline styles Markdown's **bold** and `code` in one line.
func (m Model) inline(s string) string {
	s = boldRE.ReplaceAllStringFunc(s, func(x string) string {
		return lipgloss.NewStyle().Bold(true).Render(boldRE.FindStringSubmatch(x)[1])
	})
	return codeRE.ReplaceAllStringFunc(s, func(x string) string {
		return m.pal.Fg(m.pal.Accent).Render(codeRE.FindStringSubmatch(x)[1])
	})
}

// wrap breaks s into lines of at most w cells; limit > 0 keeps that many.
func wrap(s string, w, limit int) []string {
	if w < 10 {
		w = 10
	}
	lines := strings.Split(ansi.Wordwrap(s, w, " "), "\n")
	var out []string
	for _, l := range lines {
		out = append(out, strings.Split(ansi.Hardwrap(l, w, true), "\n")...)
	}
	if limit > 0 && len(out) > limit {
		out = append(out[:limit-1], ansi.Truncate(out[limit-1], w-1, "")+"…")
	}
	return out
}

// chatKeys are the chat's footer keys.
type chatKeys struct {
	k       keyMap
	asking  bool
	always  bool
	working bool
	started bool // there's a conversation to end (ctrl+n)
}

func (c chatKeys) ShortHelp() []key.Binding {
	if c.asking {
		b := []key.Binding{
			key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yes")),
			key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "no")),
		}
		if c.always {
			b = append(b, key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "yes, and don't ask again in this chat")))
		}
		return b
	}
	b := []key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send"))}
	if c.working {
		b = []key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send after this")),
			key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "stop"))}
	}
	if c.started {
		b = append(b, key.NewBinding(key.WithKeys("ctrl+n"), key.WithHelp("ctrl+n", "new chat")))
	}
	return append(b,
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back (keeps going)")),
		key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup/pgdn", "scroll")))
}

func (c chatKeys) FullHelp() [][]key.Binding { return [][]key.Binding{c.ShortHelp()} }
