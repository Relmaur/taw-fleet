package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

type calls struct{ fleet, taw, ask, do [][]string }

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
		Ask: func(_ context.Context, args []string) (string, string, error) {
			c.ask = append(c.ask, args)
			if args[0] == "branch" {
				return "", "", errors.New("already on master")
			}
			return "Merge #12 “Update taw/core” into main? This deploys https://lsmexico.mx (production).", "", nil
		},
		Do: func(_ context.Context, args []string, progress func(string)) (string, error) {
			c.do = append(c.do, args)
			progress("Merged #12")
			return "✓ Merged #12 into main; deploying lsmexico.mx", nil
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

func TestToolsNameTheirCommandAndWritesAreMarked(t *testing.T) {
	cs := connect(t, fake(&calls{}))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != len(Tools)+len(WriteTools) {
		t.Fatalf("%d tools, want %d", len(res.Tools), len(Tools)+len(WriteTools))
	}
	writes := map[string]bool{"taw": true}
	for _, w := range WriteTools {
		writes[w.Name] = true
	}
	for _, tl := range res.Tools {
		a := tl.Annotations
		if !strings.Contains(tl.Description, "Same as `") {
			t.Errorf("%s: its description doesn't name the command a person runs", tl.Name)
		}
		switch {
		case writes[tl.Name] && (a == nil || a.ReadOnlyHint || !strings.Contains(tl.Description, "confirm")):
			t.Errorf("%s writes: it must not be read-only, and must say it asks: %+v", tl.Name, a)
		case !writes[tl.Name] && (a == nil || !a.ReadOnlyHint):
			t.Errorf("%s: not marked read-only: %+v", tl.Name, a)
		}
	}
	for _, tl := range res.Tools {
		if tl.Name == "merge_pr" && (tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint) {
			t.Error("merge_pr reaches production: destructiveHint")
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

func TestTawAsksBeforeACommandThatWrites(t *testing.T) {
	c := &calls{}
	cs := connect(t, fake(c))
	r, out := call(t, cs, "taw", map[string]any{"theme": "chcapital", "args": []string{"--version"}})
	if r.IsError || out != "TAW CLI v1.93.1" || strings.Join(c.taw[0], " ") != "chcapital --version" {
		t.Errorf("taw --version runs at once: %v %q %v", r.IsError, out, c.taw)
	}
	for _, args := range [][]string{{"update"}, {"sync", "--apply"}, {"make:block", "Hero"}} {
		c.taw = nil
		r, out := call(t, cs, "taw", map[string]any{"theme": "chcapital", "args": args})
		if r.IsError || len(c.taw) != 0 || !strings.Contains(out, "Question: Run `taw @chcapital "+strings.Join(args, " ")+"`?") {
			t.Fatalf("%v asks first: error=%v ran=%v %q", args, r.IsError, c.taw, out)
		}
		tok := token(t, out)
		r, _ = call(t, cs, "taw", map[string]any{"theme": "chcapital", "args": args, "confirm": tok})
		if r.IsError || len(c.taw) != 1 {
			t.Errorf("%v with its token runs: %v %v", args, r.IsError, c.taw)
		}
	}
}

// token is the confirm token a question carries.
func token(t *testing.T, out string) string {
	t.Helper()
	_, after, ok := strings.Cut(out, `confirm: "`)
	tok, _, ok2 := strings.Cut(after, `"`)
	if !ok || !ok2 || tok == "" {
		t.Fatalf("no token in %q", out)
	}
	return tok
}

func TestWritesAskFirst(t *testing.T) {
	c := &calls{}
	cs := connect(t, fake(c))
	args := map[string]any{"site": "ls-mxico", "pr": 12}

	r, out := call(t, cs, "merge_pr", args)
	if r.IsError || len(c.do) != 0 || strings.Join(c.ask[0], " ") != "merge ls-mxico --pr 12" ||
		!strings.Contains(out, "Question: Merge #12") || !strings.Contains(out, "Nothing has changed yet") {
		t.Fatalf("the first call only asks: do=%v ask=%v %q", c.do, c.ask, out)
	}
	tok := token(t, out)

	// A token is for the same arguments only.
	r, out = call(t, cs, "merge_pr", map[string]any{"site": "ls-mxico", "pr": 13, "confirm": tok})
	if !r.IsError || len(c.do) != 0 || !strings.Contains(out, "isn't valid") {
		t.Errorf("another PR with the token: error=%v do=%v %q", r.IsError, c.do, out)
	}
	// That spent it: ask again, then confirm.
	_, out = call(t, cs, "merge_pr", args)
	tok = token(t, out)
	args["confirm"] = tok
	r, out = call(t, cs, "merge_pr", args)
	if r.IsError || len(c.do) != 1 || strings.Join(c.do[0], " ") != "merge ls-mxico --pr 12 --yes" || !strings.Contains(out, "Merged #12") {
		t.Errorf("confirmed: error=%v do=%v %q", r.IsError, c.do, out)
	}
	// Once only.
	r, _ = call(t, cs, "merge_pr", args)
	if !r.IsError || len(c.do) != 1 {
		t.Errorf("a token works once: do=%v", c.do)
	}

	// A command that refuses before asking says why, with no token.
	r, out = call(t, cs, "site_branch", map[string]any{"site": "chcapital"})
	if !r.IsError || !strings.Contains(out, "already on master") || strings.Contains(out, "confirm") {
		t.Errorf("site_branch: error=%v %q", r.IsError, out)
	}

	// Each tool runs its own command.
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"update_site", map[string]any{"site": "taw", "theme": "taw-theme"}, "update taw --theme taw-theme"},
		{"update_all", map[string]any{}, "update --all"},
		{"start_site", map[string]any{"site": "chcapital"}, "start chcapital"},
		{"stop_site", map[string]any{"site": "chcapital"}, "stop chcapital"},
		{"sync_apply", map[string]any{"site": "chcapital"}, "sync chcapital --apply"},
		{"pull_content", map[string]any{"site": "chcapital"}, "pull chcapital"},
	} {
		c.ask = nil
		call(t, cs, tc.tool, tc.args)
		if len(c.ask) != 1 || strings.Join(c.ask[0], " ") != tc.want {
			t.Errorf("%s asked with %v, want %q", tc.tool, c.ask, tc.want)
		}
	}
}

func TestTokensExpire(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	tk := newTokens(func() time.Time { return now })
	tok := tk.issue("k")
	now = now.Add(tokenTTL + time.Second)
	if tk.take(tok, "k") {
		t.Error("an expired token must not work")
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

func TestResolvePrompt(t *testing.T) {
	c := &calls{}
	cs := connect(t, fake(c))
	if _, err := cs.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "resolve_comments", Arguments: map[string]string{"site": "fsspx-taw"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.fleet[0], " ") != "comments fsspx-taw --prompt" {
		t.Errorf("ran %v", c.fleet)
	}
}

func TestAConfirmedWriteSendsItsProgress(t *testing.T) {
	ctx := context.Background()
	st, ct := sdk.NewInMemoryTransports()
	if _, err := NewServer(fake(&calls{})).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 4)
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, &sdk.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *sdk.ProgressNotificationClientRequest) {
			got <- r.Params.Message
		},
	}).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	_, out := call(t, cs, "merge_pr", map[string]any{"site": "ls-mxico"})
	p := &sdk.CallToolParams{Name: "merge_pr", Arguments: map[string]any{"site": "ls-mxico", "confirm": token(t, out)}}
	p.SetProgressToken("pt-1")
	if _, err := cs.CallTool(ctx, p); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-got:
		if msg != "Merged #12" {
			t.Errorf("progress = %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Error("no progress notification")
	}
}
