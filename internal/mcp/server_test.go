package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect runs the server against a client in-process.
func connect(t *testing.T, d Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := sdk.NewInMemoryTransports()
	if _, err := NewServer(d).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

type calls struct{ fleet, taw [][]string }

func fake(c *calls) Deps {
	return Deps{
		Version: "v1.18.0",
		Fleet: func(_ context.Context, args []string) (string, error) {
			c.fleet = append(c.fleet, args)
			switch args[0] {
			case "doctor":
				return `[{"code":"core.behind"}]`, errors.New("exit status 1") // JSON, then a failing exit
			case "show":
				if args[1] == "nope" {
					return "", errors.New(`no site matches "nope"`)
				}
			case "handoff":
				return "Use the update-theme skill on " + args[1] + ".", nil
			}
			return `{"sites":[]}`, nil
		},
		Taw: func(_ context.Context, theme string, args []string) (string, error) {
			c.taw = append(c.taw, append([]string{theme}, args...))
			return "TAW CLI v1.93.1", nil
		},
	}
}

func text(t *testing.T, r *sdk.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (*sdk.CallToolResult, string) {
	t.Helper()
	r, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return r, text(t, r)
}

func TestToolsAreReadOnlyAndNameTheirCommand(t *testing.T) {
	cs := connect(t, fake(&calls{}))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != len(Tools) {
		t.Fatalf("%d tools, want %d", len(res.Tools), len(Tools))
	}
	for _, tl := range res.Tools {
		a := tl.Annotations
		if a == nil || !a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint {
			t.Errorf("%s: not marked read-only: %+v", tl.Name, a)
		}
		if !strings.Contains(tl.Description, "Same as `") {
			t.Errorf("%s: its description doesn't name the command a person runs", tl.Name)
		}
	}
}

func TestToolsRunTheCommands(t *testing.T) {
	c := &calls{}
	cs := connect(t, fake(c))
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"list_sites", map[string]any{"all": true, "offline": true}, "list --json --all --offline"},
		{"show_site", map[string]any{"site": "chcapital"}, "show chcapital --json"},
		{"doctor", map[string]any{}, "doctor --json"},
		{"doctor", map[string]any{"site": "ls-mxico"}, "doctor ls-mxico --json"},
		{"pull_requests", map[string]any{}, "prs --json"},
		{"comments", map[string]any{"site": "fsspx-taw", "offline": true}, "comments fsspx-taw --json --offline"},
		{"production", map[string]any{}, "live --json"},
	} {
		c.fleet = nil
		r, out := call(t, cs, tc.tool, tc.args)
		if r.IsError || len(c.fleet) != 1 || strings.Join(c.fleet[0], " ") != tc.want {
			t.Errorf("%s: ran %v (error %v: %s), want %q", tc.tool, c.fleet, r.IsError, out, tc.want)
		}
	}

	// doctor's JSON comes through even though the command exits 1.
	_, out := call(t, cs, "doctor", map[string]any{})
	if out != `[{"code":"core.behind"}]` {
		t.Errorf("doctor = %q", out)
	}
	// A command that fails without JSON is a tool error, with the reason.
	r, out := call(t, cs, "show_site", map[string]any{"site": "nope"})
	if !r.IsError || !strings.Contains(out, "no site matches") {
		t.Errorf("show nope: error=%v %q", r.IsError, out)
	}
}

func TestTawRunsReadOnlyThemeCommandsOnly(t *testing.T) {
	c := &calls{}
	cs := connect(t, fake(c))
	r, out := call(t, cs, "taw", map[string]any{"theme": "chcapital", "args": []string{"--version"}})
	if r.IsError || out != "TAW CLI v1.93.1" || strings.Join(c.taw[0], " ") != "chcapital --version" {
		t.Errorf("taw --version: %v %q %v", r.IsError, out, c.taw)
	}
	for _, args := range [][]string{{"update"}, {"sync", "--apply"}, {"upgrade", "--apply"}, {"make:block", "Hero"}} {
		c.taw = nil
		r, out := call(t, cs, "taw", map[string]any{"theme": "chcapital", "args": args})
		if !r.IsError || len(c.taw) != 0 || !strings.Contains(out, "yourself") {
			t.Errorf("%v must be refused: error=%v ran=%v %q", args, r.IsError, c.taw, out)
		}
	}
}

func TestUpdatePrompt(t *testing.T) {
	c := &calls{}
	cs := connect(t, fake(c))
	res, err := cs.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "update_theme", Arguments: map[string]string{"site": "ls-mxico"}})
	if err != nil {
		t.Fatal(err)
	}
	tc, ok := res.Messages[0].Content.(*sdk.TextContent)
	if !ok || tc.Text != "Use the update-theme skill on ls-mxico." || strings.Join(c.fleet[0], " ") != "handoff ls-mxico" {
		t.Errorf("prompt: %+v %v", res.Messages[0], c.fleet)
	}
}
