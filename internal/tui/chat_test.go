package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/agent"
)

// fakeClaude is a claude process for the chat: it keeps what taw-fleet
// sends; the test plays Claude's side as chat events.
type fakeClaude struct {
	mu   sync.Mutex
	sent []map[string]any
	inW  *io.PipeWriter
	outR *io.PipeReader
	outW *io.PipeWriter
	done chan struct{}
	once sync.Once
}

func newFakeClaude() *fakeClaude {
	f := &fakeClaude{done: make(chan struct{})}
	inR, inW := io.Pipe()
	f.inW = inW
	f.outR, f.outW = io.Pipe()
	go func() {
		sc := bufio.NewScanner(inR)
		for sc.Scan() {
			var m map[string]any
			_ = json.Unmarshal(sc.Bytes(), &m)
			f.mu.Lock()
			f.sent = append(f.sent, m)
			f.mu.Unlock()
		}
	}()
	return f
}

func (f *fakeClaude) Stdin() io.WriteCloser { return f.inW }
func (f *fakeClaude) Stdout() io.Reader     { return f.outR }
func (f *fakeClaude) Wait() error           { <-f.done; return nil }
func (f *fakeClaude) Kill()                 { f.once.Do(func() { close(f.done); _ = f.outW.Close() }) }

// last is the last message of a type taw-fleet sent, waiting a little for it.
func (f *fakeClaude) last(t *testing.T, typ string, n int) map[string]any {
	t.Helper()
	for range 200 {
		f.mu.Lock()
		var got []map[string]any
		for _, m := range f.sent {
			if m["type"] == typ {
				got = append(got, m)
			}
		}
		f.mu.Unlock()
		if len(got) >= n {
			return got[n-1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("taw-fleet sent no %d %s", n, typ)
	return nil
}

func chatModel(t *testing.T, h int) (Model, *fakeClaude, *[]agent.Scope) {
	t.Helper()
	m := withActions(t, h, &fakeActions{})
	f := newFakeClaude()
	var scopes []agent.Scope
	m.deps.Chat = func(ctx context.Context, sc agent.Scope, _ string) (*agent.Session, error) {
		scopes = append(scopes, sc)
		return agent.Start(ctx, func(context.Context, string, []string, string, []string) (agent.Proc, error) { return f, nil },
			agent.Options{Claude: "claude"})
	}
	m.deps.ChatWrites = map[string]bool{"merge_pr": true}
	return m, f, &scopes
}

// send presses enter and lets claude start: the chatStartedMsg in the batch.
func send(t *testing.T, m Model) Model {
	t.Helper()
	next, cmd := m.Update(keyMsg("enter"))
	return startFrom(t, next.(Model), cmd)
}

// startFrom runs cmd until claude has started.
func startFrom(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	msgs := make(chan tea.Msg, 8)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, c := range b {
					run(c)
				}
				return
			}
			msgs <- msg
		}()
	}
	run(cmd)
	for {
		select {
		case msg := <-msgs:
			if s, ok := msg.(chatStartedMsg); ok {
				next, _ := m.Update(s)
				return next.(Model)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("claude didn't start")
		}
	}
}

func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = step(t, m, keyMsg(string(r)))
	}
	return m
}

// say plays Claude's side.
func say(t *testing.T, m Model, k string, evs ...agent.Event) Model {
	t.Helper()
	for _, ev := range evs {
		m = step(t, m, chatEventMsg{key: k, gen: m.chats[k].gen, ev: ev})
	}
	return m
}

func TestChatFleet(t *testing.T) {
	m, f, scopes := chatModel(t, 26)
	m = press(t, m, ">")
	if m.mode != modeChat || !strings.Contains(screen(m), "Claude · the fleet") || !strings.Contains(screen(m), "update every site that's behind") {
		t.Fatalf("chat:\n%s", screen(m))
	}
	golden(t, "chat-empty-100x26", screen(m))

	m = typeText(t, m, "what needs attention?")
	m = send(t, m)
	if len(*scopes) != 1 || (*scopes)[0].Site != "" {
		t.Fatalf("scope: %+v", *scopes)
	}
	if msg := f.last(t, "user", 1); msg["message"].(map[string]any)["content"] != "what needs attention?" {
		t.Errorf("sent %v", msg)
	}
	m = say(t, m, "fleet",
		agent.Event{Kind: agent.Started, Session: "s-1", Model: "claude-opus-5-5"},
		agent.Event{Kind: agent.Tool, ToolID: "t1", Name: agent.ToolName("doctor")},
		agent.Event{Kind: agent.Result, ToolID: "t1", Text: "[]"},
		agent.Event{Kind: agent.Tool, ToolID: "t2", Name: agent.ToolName("comments")},
		agent.Event{Kind: agent.Result, ToolID: "t2", Text: "[]"},
		agent.Event{Kind: agent.Text, Text: "Three things need you:\n\n1. **bistro** is on taw/core 1.59.2, "},
		agent.Event{Kind: agent.Text, Text: "17 versions behind; `u` on it, or ask me to update it.\n2. **acme-shop** has 2 open client comments, one waiting since Friday.\n3. Production is fine: every site answered and verified."},
		agent.Event{Kind: agent.TurnDone, Cost: 0.0412, Took: 9 * time.Second},
	)
	sc := screen(m)
	for _, want := range []string{"you  what needs attention?", "✓ taw-fleet doctor", "✓ taw-fleet comments", "✦    Three things need you:", "1. bistro is on taw/core 1.59.2", "opus-5-5 · $0.04"} {
		if !strings.Contains(sc, want) {
			t.Errorf("lacks %q:\n%s", want, sc)
		}
	}
	golden(t, "chat-fleet-100x26", sc)

	// esc leaves it; the next > comes back to the same conversation.
	m = press(t, m, "esc")
	if m.mode != modeTable {
		t.Fatalf("esc: %v", m.mode)
	}
	m = press(t, m, ">")
	if !strings.Contains(screen(m), "what needs attention?") {
		t.Errorf("not the same chat:\n%s", screen(m))
	}
}

func TestChatSiteAsksTheDashboardsQuestion(t *testing.T) {
	m, f, scopes := chatModel(t, 26)
	m = press(t, m, "enter", ">")
	if !strings.Contains(screen(m), "Claude · acme-shop (acme)") {
		t.Fatalf("site chat:\n%s", screen(m))
	}
	m = typeText(t, m, "merge the update and deploy it")
	m = send(t, m)
	if (*scopes)[0].Site != "acme-shop" || (*scopes)[0].Theme != "acme" {
		t.Errorf("scope %+v", (*scopes)[0])
	}
	f.last(t, "user", 1)

	// The first call only returns the question: it's let through.
	ask1 := &agent.Request{ID: "r1", Tool: agent.ToolName("merge_pr"), Input: map[string]any{"site": "acme-shop"}}
	m = say(t, m, "acme-shop/acme",
		agent.Event{Kind: agent.Started, Session: "s-2", Model: "claude-opus-5-5"},
		agent.Event{Kind: agent.Text, Text: "PR #12 (Update taw/core to 1.95.1) passed CI. Merging it deploys acme.mx."},
		agent.Event{Kind: agent.Tool, ToolID: "t1", Name: ask1.Tool, Summary: "acme-shop", Input: ask1.Input},
		agent.Event{Kind: agent.Ask, Name: ask1.Tool, Request: ask1},
	)
	if r := f.last(t, "control_response", 1)["response"].(map[string]any); r["request_id"] != "r1" ||
		r["response"].(map[string]any)["behavior"] != "allow" {
		t.Errorf("first call: %v", r)
	}
	if m.currentChat().ask != nil {
		t.Error("asked about the question-only call")
	}
	ask2 := &agent.Request{ID: "r2", Tool: agent.ToolName("merge_pr"), Input: map[string]any{"site": "acme-shop", "confirm": "tok"}}
	m = say(t, m, "acme-shop/acme",
		agent.Event{Kind: agent.Result, ToolID: "t1", Text: "Question: Merge #12 into main? This deploys https://acme.mx (production).\n\nNothing has changed yet."},
		agent.Event{Kind: agent.Tool, ToolID: "t2", Name: ask2.Tool, Summary: "acme-shop", Input: ask2.Input},
		agent.Event{Kind: agent.Ask, Name: ask2.Tool, Request: ask2},
	)
	sc := screen(m)
	if !strings.Contains(sc, "? Merge #12 into main? This deploys https://acme.mx (production).") || !strings.Contains(sc, "y yes") {
		t.Errorf("ask card:\n%s", sc)
	}
	golden(t, "chat-ask-100x26", sc)

	m = press(t, m, "y")
	if r := f.last(t, "control_response", 2)["response"].(map[string]any); r["request_id"] != "r2" ||
		r["response"].(map[string]any)["behavior"] != "allow" {
		t.Errorf("yes: %v", r)
	}

	// Anything else asks in Claude Code's words; n refuses.
	bash := &agent.Request{ID: "r3", Tool: "Bash", Description: "Show the theme's uncommitted changes",
		Input: map[string]any{"command": "git status --short"}, Suggestions: []any{map[string]any{"type": "addRules"}}}
	m = say(t, m, "acme-shop/acme", agent.Event{Kind: agent.Result, ToolID: "t2", Text: "Merged #12 into main; deploying acme.mx"},
		agent.Event{Kind: agent.Tool, ToolID: "t3", Name: "Bash", Summary: "git status --short"},
		agent.Event{Kind: agent.Ask, Name: "Bash", Request: bash})
	sc = screen(m)
	if !strings.Contains(sc, "? Claude wants to use Bash: Show the theme's uncommitted changes") || !strings.Contains(sc, "git status --short") ||
		!strings.Contains(sc, "a yes, and don't ask again in this chat") {
		t.Errorf("bash card:\n%s", sc)
	}
	golden(t, "chat-bash-100x26", sc)
	m = press(t, m, "n")
	if r := f.last(t, "control_response", 3)["response"].(map[string]any)["response"].(map[string]any); r["behavior"] != "deny" {
		t.Errorf("no: %v", r)
	}

	// ctrl+c stops it; the next message resumes the session.
	m = press(t, m, "ctrl+c")
	if m.currentChat().sess != nil && m.currentChat().working {
		t.Error("still working")
	}
	if !strings.Contains(screen(m), "Stopped. Your next message carries on from here.") {
		t.Errorf("stop:\n%s", screen(m))
	}
}

func TestChatNeedsClaude(t *testing.T) {
	m := withActions(t, 24, &fakeActions{})
	m = press(t, m, ">")
	if m.mode == modeChat || !strings.Contains(screen(m), "the chat needs Claude Code") {
		t.Errorf("no claude:\n%s", screen(m))
	}
}

func TestFleetSkillsRunInTheFleetChat(t *testing.T) {
	m, f, scopes := chatModel(t, 26)
	m.deps.FleetSkills = []actions.Skill{{Name: "fleet-check", Owner: actions.FleetOwner,
		Description: "The morning check of the whole TAW fleet. Triggers on: \"check the fleet\"."}}
	m = press(t, m, "a")
	sc := screen(m)
	if m.mode != modeSkills || !strings.Contains(sc, "FLEET SKILLS") || !strings.Contains(sc, "fleet-check") {
		t.Fatalf("picker:\n%s", sc)
	}
	m = press(t, m, "f", "l", "e", "e", "t")
	golden(t, "skills-fleet-100x26", screen(m))
	next, cmd := m.Update(keyMsg("enter"))
	m = next.(Model)
	if m.mode != modeChat || m.chat != "fleet" {
		t.Fatalf("not the fleet chat: %v %q", m.mode, m.chat)
	}
	m = startFrom(t, m, cmd)
	if len(*scopes) != 1 || (*scopes)[0].Site != "" {
		t.Errorf("scope %+v", *scopes)
	}
	if msg := f.last(t, "user", 1); msg["message"].(map[string]any)["content"] != "Use the fleet-check skill." {
		t.Errorf("sent %v", msg)
	}
	if !strings.Contains(screen(m), "you  Use the fleet-check skill.") {
		t.Errorf("chat:\n%s", screen(m))
	}
}

func TestChatFollowUpWhileClaudeWorks(t *testing.T) {
	m, f, _ := chatModel(t, 26)
	m = press(t, m, ">")
	m = typeText(t, m, "what needs attention?")
	m = send(t, m)
	m = say(t, m, "fleet", agent.Event{Kind: agent.Started, Session: "s-1", Model: "claude-opus-5-5"},
		agent.Event{Kind: agent.Tool, ToolID: "t1", Name: agent.ToolName("doctor")})

	// Typed while Claude works: on screen at once, sent when the turn ends.
	m = typeText(t, m, "and which sites have comments?")
	m = press(t, m, "enter")
	sc := screen(m)
	for _, want := range []string{"you  and which sites have comments?", "queued: goes to Claude when it finishes this", "enter send after this"} {
		if !strings.Contains(sc, want) {
			t.Errorf("lacks %q:\n%s", want, sc)
		}
	}
	if strings.Contains(sc, "› and which") {
		t.Errorf("still in the input:\n%s", sc)
	}
	golden(t, "chat-queued-100x26", sc)

	m = say(t, m, "fleet", agent.Event{Kind: agent.Result, ToolID: "t1", Text: "[]"},
		agent.Event{Kind: agent.Text, Text: "Nothing needs you."},
		agent.Event{Kind: agent.TurnDone, Cost: 0.02})
	if msg := f.last(t, "user", 2); msg["message"].(map[string]any)["content"] != "and which sites have comments?" {
		t.Errorf("sent %v", msg)
	}
	c := m.currentChat()
	if !c.working || len(c.queued) != 0 {
		t.Errorf("working %v queued %v", c.working, c.queued)
	}
	sc = screen(m)
	if strings.Contains(sc, "queued:") || strings.Index(sc, "Nothing needs you.") > strings.Index(sc, "you  and which sites") {
		t.Errorf("order:\n%s", sc)
	}

	// A message waiting when Claude is stopped goes back to the input.
	m = typeText(t, m, "never mind")
	m = press(t, m, "enter", "ctrl+c")
	if v := m.currentChat().input.Value(); v != "never mind" {
		t.Errorf("input %q", v)
	}
}

func TestChatTakesAPaste(t *testing.T) {
	m, _, _ := chatModel(t, 20)
	m = press(t, m, ">")
	m = step(t, m, tea.PasteMsg{Content: "make me this site: https://example.com/design"})
	if v := m.currentChat().input.Value(); v != "make me this site: https://example.com/design" {
		t.Errorf("input %q", v)
	}
}

func TestChatNewConversationAndCtrlC(t *testing.T) {
	m, f, _ := chatModel(t, 26)
	m = press(t, m, ">")
	m = typeText(t, m, "first")
	m = send(t, m)
	m = say(t, m, "fleet", agent.Event{Kind: agent.Started, Session: "s-1", Model: "claude-opus-5-5"},
		agent.Event{Kind: agent.Text, Text: "An answer."}, agent.Event{Kind: agent.TurnDone, Cost: 0.01})

	// ctrl+c with Claude idle stays in the chat, never quits.
	next, cmd := m.Update(keyMsg("ctrl+c"))
	m = next.(Model)
	if cmd != nil || m.mode != modeChat || !strings.Contains(screen(m), "Nothing to stop: esc goes back to the list, and q quits there.") {
		t.Fatalf("ctrl+c idle:\n%s", screen(m))
	}

	// ctrl+n ends it: claude stops, the screen clears, the next message starts fresh.
	m = press(t, m, "ctrl+n")
	c := m.currentChat()
	if c.sess != nil || c.session != "" || len(c.entries) != 0 || c.cost != 0 {
		t.Fatalf("not fresh: %+v", c)
	}
	sc := screen(m)
	if !strings.Contains(sc, "update every site that's behind") || !strings.Contains(sc, "claude --resume in the fleet folder") {
		t.Errorf("new chat:\n%s", sc)
	}
	golden(t, "chat-new-100x26", sc)
	select {
	case <-f.done:
	case <-time.After(2 * time.Second):
		t.Error("claude still running")
	}
}
