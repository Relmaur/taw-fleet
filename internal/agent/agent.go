// Package agent runs Claude Code for the dashboard's chat: the person's own
// Claude Code, signed in with their account, in print mode with stream-json
// both ways. taw-fleet sends what the person types, shows what Claude says
// and does, and answers Claude Code's permission prompts with the person's
// answer (plan taw-fleet-agent.md § 2 and § 8).
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Kind says what an Event is.
type Kind int

// The kinds of Event.
const (
	Started  Kind = iota // Claude Code is up: Session, Model
	Text                 // Claude's words, a piece at a time
	Tool                 // Claude calls a tool: ToolID, Name, Summary
	Result               // a tool's answer: ToolID, Failed, Summary
	Ask                  // a permission prompt: Request
	TurnDone             // Claude finished answering: Cost, Took, Failed
	Ended                // Claude Code exited: Err
)

// Event is one thing that happened in the conversation.
type Event struct {
	Kind    Kind
	Session string
	Model   string
	Text    string // Text: the words; Result: the answer, shortened
	ToolID  string
	Name    string         // the tool (Tool, Ask)
	Summary string         // Tool: the input in a line
	Input   map[string]any // Tool: the input
	Failed  bool           // Result: the tool failed or was refused; TurnDone: the turn ended in an error
	Request *Request
	Cost    float64 // TurnDone: the conversation's cost so far, in USD
	Took    time.Duration
	Err     error
}

// Request is a permission prompt: Claude Code asking whether a tool may
// run. Answer it with Session.Answer.
type Request struct {
	ID          string
	Tool        string         // e.g. Bash, Write, mcp__taw-fleet__merge_pr
	Display     string         // Claude Code's name for it
	Description string         // what it's for, in Claude Code's words
	Input       map[string]any // exactly what would run
	Suggestions []any          // Claude Code's "always allow" rules, for a yes for the session
}

// Parse turns one line of Claude Code's stream-json into events. Lines it
// doesn't use give none.
func Parse(line []byte) []Event {
	var ev struct {
		Type      string          `json:"type"`
		Subtype   string          `json:"subtype"`
		Session   string          `json:"session_id"`
		Model     string          `json:"model"`
		Event     json.RawMessage `json:"event"`
		Message   json.RawMessage `json:"message"`
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request"`
		Cost      float64         `json:"total_cost_usd"`
		Duration  int64           `json:"duration_ms"`
		IsError   bool            `json:"is_error"`
		Result    string          `json:"result"`
	}
	if json.Unmarshal(line, &ev) != nil {
		return nil
	}
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			return []Event{{Kind: Started, Session: ev.Session, Model: ev.Model}}
		}
	case "stream_event":
		var se struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal(ev.Event, &se) == nil && se.Type == "content_block_delta" && se.Delta.Type == "text_delta" && se.Delta.Text != "" {
			return []Event{{Kind: Text, Text: se.Delta.Text}}
		}
	case "assistant":
		var out []Event
		for _, b := range blocks(ev.Message) {
			if b.Type == "tool_use" {
				out = append(out, Event{Kind: Tool, ToolID: b.ID, Name: b.Name, Summary: Summarize(b.Name, b.Input), Input: b.Input})
			}
		}
		return out
	case "user":
		var out []Event
		for _, b := range blocks(ev.Message) {
			if b.Type == "tool_result" {
				out = append(out, Event{Kind: Result, ToolID: b.ToolUseID, Failed: b.IsError, Text: contentText(b.Content)})
			}
		}
		return out
	case "control_request":
		var r struct {
			Subtype     string         `json:"subtype"`
			Tool        string         `json:"tool_name"`
			Display     string         `json:"display_name"`
			Description string         `json:"description"`
			Input       map[string]any `json:"input"`
			Suggestions []any          `json:"permission_suggestions"`
		}
		if json.Unmarshal(ev.Request, &r) == nil && r.Subtype == "can_use_tool" {
			return []Event{{Kind: Ask, Name: r.Tool, Request: &Request{ID: ev.RequestID, Tool: r.Tool,
				Display: r.Display, Description: r.Description, Input: r.Input, Suggestions: r.Suggestions}}}
		}
	case "result":
		return []Event{{Kind: TurnDone, Session: ev.Session, Cost: ev.Cost, Took: time.Duration(ev.Duration) * time.Millisecond,
			Failed: ev.IsError, Text: ev.Result}}
	}
	return nil
}

type block struct {
	Type      string         `json:"type"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input"`
	ToolUseID string         `json:"tool_use_id"`
	IsError   bool           `json:"is_error"`
	Content   any            `json:"content"`
}

func blocks(raw json.RawMessage) []block {
	var m struct {
		Content []block `json:"content"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Content
}

func contentText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var out []string
		for _, b := range c {
			if bb, ok := b.(map[string]any); ok {
				if t, ok := bb["text"].(string); ok {
					out = append(out, t)
				}
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

// Summarize is a tool call in a line: the command for Bash, the file for an
// edit, the arguments for anything else.
func Summarize(tool string, in map[string]any) string {
	str := func(k string) string { s, _ := in[k].(string); return s }
	switch tool {
	case "Bash":
		return str("command")
	case "Read", "Write", "Edit", "NotebookEdit":
		return str("file_path")
	case "Glob", "Grep":
		return str("pattern")
	case "WebFetch":
		return str("url")
	case "WebSearch":
		return str("query")
	case AskTool:
		var qs []string
		for _, q := range Choices(&Request{Tool: tool, Input: in}) {
			qs = append(qs, q.Text)
		}
		return strings.Join(qs, " · ")
	}
	if len(in) == 0 {
		return ""
	}
	if strings.HasPrefix(tool, "mcp__"+Server+"__") {
		// taw-fleet's tools: what they act on, in order; never the token.
		var parts []string
		for _, k := range []string{"site", "theme", "pr", "point", "month", "language", "args"} {
			switch v := in[k].(type) {
			case nil:
			case string:
				if v != "" {
					parts = append(parts, v)
				}
			case float64:
				parts = append(parts, fmt.Sprintf("#%g", v))
			case []any:
				for _, a := range v {
					parts = append(parts, fmt.Sprint(a))
				}
			default:
				parts = append(parts, fmt.Sprint(v))
			}
		}
		return strings.Join(parts, " ")
	}
	// Another tool: the field that says what it does, else its input as JSON.
	for _, k := range []string{"skill", "query", "description", "prompt", "subject", "url", "path"} {
		if s := str(k); s != "" {
			return s
		}
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(in)
	return strings.TrimSpace(b.String())
}

// Options are how a conversation starts.
type Options struct {
	Claude string   // the claude binary
	Dir    string   // where it runs: the fleet folder, or a theme
	Resume string   // a session to continue; "" = a new one
	Prompt string   // appended to Claude Code's system prompt
	MCP    string   // an MCP config file (taw-fleet's own server); "" = none
	Allow  []string // tools that never ask (taw-fleet's read tools)
	Env    []string // nil = this process's
}

// Args are claude's arguments for o.
func (o Options) Args() []string {
	args := []string{"--print", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--include-partial-messages",
		"--permission-prompts", "host", "--permission-prompt-tool", "stdio"}
	if o.Resume != "" {
		args = append(args, "--resume", o.Resume)
	}
	if o.Prompt != "" {
		args = append(args, "--append-system-prompt", o.Prompt)
	}
	if o.MCP != "" {
		args = append(args, "--mcp-config", o.MCP)
	}
	if len(o.Allow) > 0 {
		args = append(args, "--allowedTools", strings.Join(o.Allow, ","))
	}
	return args
}

// Proc is a running claude.
type Proc interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Wait() error
	Kill()
}

// Starter starts claude; tests replace it.
type Starter func(ctx context.Context, name string, args []string, dir string, env []string) (Proc, error)

// Session is one conversation with Claude Code.
type Session struct {
	events chan Event
	mu     sync.Mutex
	in     io.WriteCloser
	proc   Proc
	closed bool
}

// Start runs claude with o and returns the conversation. Events arrive on
// Events until Ended.
func Start(ctx context.Context, start Starter, o Options) (*Session, error) {
	if start == nil {
		start = execStart
	}
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	// A dashboard started from inside Claude Code mustn't look like a nested session.
	env = without(env, "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT")
	p, err := start(ctx, o.Claude, o.Args(), o.Dir, env)
	if err != nil {
		return nil, err
	}
	s := &Session{events: make(chan Event, 256), in: p.Stdin(), proc: p}
	// Say this side answers permission prompts, as the Agent SDK does.
	if err := s.send(map[string]any{"type": "control_request", "request_id": "taw-fleet-init",
		"request": map[string]any{"subtype": "initialize"}}); err != nil {
		p.Kill()
		return nil, err
	}
	go s.read(p.Stdout())
	return s, nil
}

// Events is the conversation as it happens.
func (s *Session) Events() <-chan Event { return s.events }

func (s *Session) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		for _, ev := range Parse(sc.Bytes()) {
			s.events <- ev
		}
	}
	err := s.proc.Wait()
	s.mu.Lock()
	stopped := s.closed
	s.mu.Unlock()
	if stopped {
		err = nil
	}
	s.events <- Event{Kind: Ended, Err: err}
	close(s.events)
}

// Say sends what the person typed.
func (s *Session) Say(text string) error {
	return s.send(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
}

// Answer replies to a permission prompt: yes, no, or yes and the same from
// now on in this conversation (Claude Code's own suggestion).
func (s *Session) Answer(r *Request, allow, always bool, why string) error {
	resp := map[string]any{"behavior": "deny", "message": why}
	if allow {
		resp = map[string]any{"behavior": "allow", "updatedInput": r.Input}
		if always && len(r.Suggestions) > 0 {
			resp["updatedPermissions"] = r.Suggestions
		}
	}
	return s.send(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": r.ID, "response": resp}})
}

// Stop ends the conversation: claude is killed, and Events ends.
func (s *Session) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	_ = s.in.Close()
	s.proc.Kill()
}

var errStopped = errors.New("the conversation has ended")

func (s *Session) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errStopped
	}
	if _, err := s.in.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	return nil
}

func without(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
outer:
	for _, e := range env {
		for _, k := range keys {
			if strings.HasPrefix(e, k+"=") {
				continue outer
			}
		}
		out = append(out, e)
	}
	return out
}

type proc struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out io.Reader
	err *tail
}

func (p *proc) Stdin() io.WriteCloser { return p.in }
func (p *proc) Stdout() io.Reader     { return p.out }
func (p *proc) Wait() error {
	err := p.cmd.Wait()
	if msg := strings.TrimSpace(p.err.String()); err != nil && msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}
func (p *proc) Kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

func execStart(ctx context.Context, name string, args []string, dir string, env []string) (Proc, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the person's own claude, with fixed arguments
	cmd.Dir, cmd.Env = dir, env
	errs := &tail{}
	cmd.Stderr = errs
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &proc{cmd: cmd, in: in, out: out, err: errs}, nil
}

// tail keeps the end of claude's stderr, to say why it stopped.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > 2000 {
		t.buf = t.buf[len(t.buf)-2000:]
	}
	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
