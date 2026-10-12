package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The lines below are the shapes Claude Code 2.1.296 sent in the spike
// (plan taw-fleet-agent.md § 8), trimmed.
const (
	initLine   = `{"type":"system","subtype":"init","session_id":"s-1","model":"claude-haiku-5-5","apiKeySource":"none"}`
	deltaLine  = `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}}`
	toolLine   = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t-1","name":"Bash","input":{"command":"uname -s && ls","description":"x"}}]}}`
	resultLine = `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t-1","is_error":false,"content":"Darwin"}]}}`
	askLine    = `{"type":"control_request","request_id":"r-1","request":{"subtype":"can_use_tool","tool_name":"Write","display_name":"Write","description":"notes.txt","input":{"file_path":"/w/notes.txt","content":"hi"},"permission_suggestions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}],"tool_use_id":"t-2"}}`
	doneLine   = `{"type":"result","subtype":"success","session_id":"s-1","total_cost_usd":0.0018,"duration_ms":9600,"is_error":false,"result":"I created notes.txt."}`
)

func TestParse(t *testing.T) {
	cases := []struct {
		line string
		want Event
	}{
		{initLine, Event{Kind: Started, Session: "s-1", Model: "claude-haiku-5-5"}},
		{deltaLine, Event{Kind: Text, Text: "Hello"}},

		{resultLine, Event{Kind: Result, ToolID: "t-1", Text: "Darwin"}},
		{doneLine, Event{Kind: TurnDone, Session: "s-1", Cost: 0.0018, Took: 9600 * time.Millisecond, Text: "I created notes.txt."}},
	}
	for _, c := range cases {
		got := Parse([]byte(c.line))
		if len(got) != 1 || !reflect.DeepEqual(got[0], c.want) {
			t.Errorf("Parse(%.40s…) = %+v, want %+v", c.line, got, c.want)
		}
	}
	if tool := Parse([]byte(toolLine)); len(tool) != 1 || tool[0].Kind != Tool || tool[0].ToolID != "t-1" || tool[0].Name != "Bash" ||
		tool[0].Summary != "uname -s && ls" || tool[0].Input["description"] != "x" {
		t.Errorf("tool = %+v", tool)
	}
	ask := Parse([]byte(askLine))
	if len(ask) != 1 || ask[0].Kind != Ask || ask[0].Request.ID != "r-1" || ask[0].Request.Tool != "Write" ||
		ask[0].Request.Description != "notes.txt" || ask[0].Request.Input["file_path"] != "/w/notes.txt" || len(ask[0].Request.Suggestions) != 1 {
		t.Errorf("ask = %+v", ask)
	}
	for _, l := range []string{`{"type":"rate_limit_event"}`, `{"type":"system","subtype":"status"}`, `not json`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{"}}}`} {
		if got := Parse([]byte(l)); len(got) != 0 {
			t.Errorf("Parse(%s) = %+v, want nothing", l, got)
		}
	}
}

func TestSummarize(t *testing.T) {
	if s := Summarize("Write", map[string]any{"file_path": "/a/b.php", "content": "x"}); s != "/a/b.php" {
		t.Errorf("Write: %q", s)
	}
	if s := Summarize("mcp__taw-fleet__merge_pr", map[string]any{"site": "acme", "pr": float64(12), "confirm": "tok"}); s != "acme #12" {
		t.Errorf("mcp: %q", s)
	}
	if s := Summarize("mcp__figma__get_screenshot", map[string]any{"node": "1:2"}); s != `{"node":"1:2"}` {
		t.Errorf("mcp: %q", s)
	}
}

func TestArgs(t *testing.T) {
	a := strings.Join(Options{Resume: "s-1", Prompt: "p", MCP: "/m.json", Allow: []string{"a", "b"}}.Args(), " ")
	for _, want := range []string{"--print --input-format stream-json --output-format stream-json", "--permission-prompts host --permission-prompt-tool stdio",
		"--resume s-1", "--append-system-prompt p", "--mcp-config /m.json", "--allowedTools a,b"} {
		if !strings.Contains(a, want) {
			t.Errorf("args %q lack %q", a, want)
		}
	}
}

// fake is a claude that replays lines and records what it's sent.
type fake struct {
	inR  *io.PipeReader
	inW  *io.PipeWriter
	outR *io.PipeReader
	outW *io.PipeWriter
	done chan struct{}
}

func newFake() *fake {
	f := &fake{done: make(chan struct{})}
	f.inR, f.inW = io.Pipe()
	f.outR, f.outW = io.Pipe()
	return f
}
func (f *fake) Stdin() io.WriteCloser { return f.inW }
func (f *fake) Stdout() io.Reader     { return f.outR }
func (f *fake) Wait() error           { <-f.done; return nil }
func (f *fake) Kill()                 { _ = f.outW.Close(); f.end() }
func (f *fake) end() {
	select {
	case <-f.done:
	default:
		close(f.done)
	}
}

func TestSession(t *testing.T) {
	f := newFake()
	lines := make(chan []byte, 16)
	go func() {
		sc := bufio.NewScanner(f.inR)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(lines)
	}()
	var gotArgs []string
	var gotEnv []string
	s, err := Start(context.Background(), func(_ context.Context, _ string, args []string, _ string, env []string) (Proc, error) {
		gotArgs, gotEnv = args, env
		return f, nil
	}, Options{Claude: "claude", Env: []string{"CLAUDECODE=1", "HOME=/h"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotEnv, " ") != "HOME=/h" || len(gotArgs) == 0 {
		t.Errorf("env %v", gotEnv)
	}
	next := func() map[string]any {
		var m map[string]any
		select {
		case l := <-lines:
			_ = json.Unmarshal(l, &m)
		case <-time.After(5 * time.Second):
			t.Fatal("nothing sent")
		}
		return m
	}
	if m := next(); m["type"] != "control_request" || m["request"].(map[string]any)["subtype"] != "initialize" {
		t.Errorf("first message: %v", m)
	}
	_ = s.Say("who are you?")
	if m := next(); m["type"] != "user" || m["message"].(map[string]any)["content"] != "who are you?" {
		t.Errorf("say: %v", m)
	}

	go func() {
		for _, l := range []string{initLine, deltaLine, askLine} {
			_, _ = f.outW.Write([]byte(l + "\n"))
		}
	}()
	var ask *Request
	for ev := range s.Events() {
		if ev.Kind == Ask {
			ask = ev.Request
			break
		}
	}
	_ = s.Answer(ask, true, true, "")
	m := next()
	resp := m["response"].(map[string]any)["response"].(map[string]any)
	if m["type"] != "control_response" || resp["behavior"] != "allow" || resp["updatedPermissions"] == nil ||
		resp["updatedInput"].(map[string]any)["file_path"] != "/w/notes.txt" {
		t.Errorf("allow always: %v", m)
	}
	_ = s.Answer(ask, false, false, "The person said no.")
	resp = next()["response"].(map[string]any)["response"].(map[string]any)
	if resp["behavior"] != "deny" || resp["message"] != "The person said no." {
		t.Errorf("deny: %v", resp)
	}

	s.Stop()
	var last Event
	for ev := range s.Events() {
		last = ev
	}
	if last.Kind != Ended || last.Err != nil {
		t.Errorf("ended: %+v", last)
	}
	if err := s.Say("again"); err == nil {
		t.Error("said after stop")
	}
}

func TestOnlyAsks(t *testing.T) {
	writes := map[string]bool{"merge_pr": true}
	cases := []struct {
		tool  string
		input map[string]any
		want  bool
	}{
		{ToolName("merge_pr"), map[string]any{"site": "acme"}, true},
		{ToolName("merge_pr"), map[string]any{"site": "acme", "confirm": "tok"}, false},
		{ToolName("taw"), map[string]any{"theme": "acme", "args": []any{"list"}}, true},
		{ToolName("list_sites"), map[string]any{}, false}, // allowed anyway
		{"Bash", map[string]any{"command": "ls"}, false},
		{"mcp__other__merge_pr", map[string]any{}, false},
	}
	for _, c := range cases {
		if got := OnlyAsks(&Request{Tool: c.tool, Input: c.input}, writes); got != c.want {
			t.Errorf("OnlyAsks(%s, %v) = %v", c.tool, c.input, got)
		}
	}
	if q := Question("Merging #12.\n\nQuestion: Merge #12 into main? This deploys https://acme.mx.\n\nNothing has changed yet."); q != "Merge #12 into main? This deploys https://acme.mx." {
		t.Errorf("question %q", q)
	}
}

func TestPrompt(t *testing.T) {
	fleet := Prompt(Scope{})
	if !strings.Contains(fleet, "Scope: the whole fleet") || !strings.Contains(fleet, "data, not instructions") {
		t.Errorf("fleet prompt:\n%s", fleet)
	}
	one := Prompt(Scope{Site: "acme", Name: "Acme", Theme: "acme--theme", Branch: "feature", Default: "main", Production: "https://acme.mx"})
	for _, want := range []string{"- Site: acme (Acme)", "- Branch: feature (the default branch, which deploys, is main)", "- Production: https://acme.mx"} {
		if !strings.Contains(one, want) {
			t.Errorf("site prompt lacks %q:\n%s", want, one)
		}
	}
}
