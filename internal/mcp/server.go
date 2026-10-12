// Package mcp serves taw-fleet over the Model Context Protocol (ADR-0002):
// each tool is a taw-fleet command a person can run too, and its result is
// that command's --json. It holds no logic of its own: the commands run
// in-process through Deps.Fleet, a theme's own commands through Deps.Taw.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deps is how the server reaches taw-fleet.
type Deps struct {
	Version string
	// Fleet runs a taw-fleet command in-process and returns its stdout.
	Fleet func(ctx context.Context, args []string) (string, error)
	// Ask runs a write command up to its y/N question and declines it:
	// the question ("" when it had none) and what it printed first.
	Ask func(ctx context.Context, args []string) (question, out string, err error)
	// Do runs a write command (with --yes), its progress lines to progress.
	Do func(ctx context.Context, args []string, progress func(string)) (string, error)
	// Taw runs a theme's own command (`taw @<theme> <args…>`) and returns
	// its output.
	Taw func(ctx context.Context, theme string, args []string) (string, error)
	// Now is the clock for confirm tokens; nil = time.Now.
	Now func() time.Time
}

// ToolDoc is a tool as people see it: `taw-fleet mcp tools` prints these.
type ToolDoc struct {
	Name, Title, Command, Key, Description string
}

// Tools are the server's tools, in the order `mcp tools` lists them.
var Tools = []ToolDoc{
	{"list_sites", "List the TAW sites", "taw-fleet list --json", "the table",
		"Every Local site on this Mac with its TAW themes: status, taw/core installed and newest, git branch and state, scaffold sync, production host."},
	{"show_site", "One site in full", "taw-fleet show <site> --json", "enter",
		"One site in full: Local (folder, URL, PHP, running), each theme (taw/core, git, repository, sync), production, BugSmash feedback and doctor's findings."},
	{"doctor", "What needs attention", "taw-fleet doctor [site] --json", "FINDINGS",
		"Findings for every site (or one), most serious first, each with its code and the fix: taw/core behind, uncommitted or unpushed work, wrong branch, failed deploys, production down, open comments."},
	{"pull_requests", "Pull requests and deploys", "taw-fleet prs --json", "PR and DEPLOY columns",
		"Each theme's open pull requests with their CI, and what production has deployed (the commit, when, how far behind the default branch)."},
	{"comments", "Client comments (BugSmash)", "taw-fleet comments [site] --json", "FB column, F",
		"The open review comments clients left on each production site in BugSmash: how many, how old, the latest ones with their page and text."},
	{"production", "Production sites", "taw-fleet live --json", "LIVE column, L",
		"Each production site through its TAW companion: reachable, verified, WordPress/PHP/taw-core versions, plugins, known vulnerabilities, recent errors."},
	{"taw", "A theme's own command", "taw @<theme> <command…>", "a terminal in the theme (t)",
		"Runs a TAW theme's own command line (its taw/core's vendor/bin/taw) with its Local site's PHP and database. Read-only commands run at once: " +
			strings.Join(readOnlyTaw, ", ") + " (sync and upgrade without --apply). Any other command may change files or the database, so it asks first: " +
			"called without confirm it returns the question and a token; ask the user, then call again with the same arguments and confirm."},
}

// readOnlyTaw are the theme commands the taw tool runs at once: they report,
// never write. Any other asks first (ADR-0002, decision 5).
var readOnlyTaw = []string{"list", "help", "inspect", "policy", "sync", "upgrade", "content:diff", "--version"}

func doc(name string) ToolDoc {
	for _, t := range Tools {
		if t.Name == name {
			return t
		}
	}
	panic("mcp: no tool " + name)
}

func tool(name string) *sdk.Tool {
	d := doc(name)
	no := false
	a := &sdk.ToolAnnotations{Title: d.Title, ReadOnlyHint: true, DestructiveHint: &no, IdempotentHint: true}
	if name == "taw" { // read-only commands at once, others after asking
		yes := true
		a = &sdk.ToolAnnotations{Title: d.Title, DestructiveHint: &yes}
	}
	return &sdk.Tool{
		Name:        d.Name,
		Title:       d.Title,
		Description: d.Description + " Same as `" + d.Command + "` (dashboard: " + d.Key + ").",
		Annotations: a,
	}
}

// The tools' inputs.
type (
	listIn struct {
		All     bool `json:"all,omitempty" jsonschema:"also sites and themes that aren't TAW"`
		Offline bool `json:"offline,omitempty" jsonschema:"don't ask GitHub; use the cached newest versions"`
	}
	siteIn struct {
		Site    string `json:"site" jsonschema:"the site: its Local folder, name, domain or a theme folder (as taw-fleet list shows them)"`
		Offline bool   `json:"offline,omitempty" jsonschema:"don't ask GitHub; use the cached newest versions"`
	}
	maybeSiteIn struct {
		Site    string `json:"site,omitempty" jsonschema:"one site (its Local folder, name, domain or a theme folder); empty for every site"`
		Offline bool   `json:"offline,omitempty" jsonschema:"don't ask the network; use the caches"`
	}
	offlineIn struct {
		Offline bool `json:"offline,omitempty" jsonschema:"don't ask the network; use the caches"`
	}
	tawIn struct {
		Theme   string   `json:"theme" jsonschema:"the theme: its folder name (chcapital), a site, or site/theme for a site with two TAW themes"`
		Args    []string `json:"args" jsonschema:"the command and its arguments, e.g. [\"inspect\"] or [\"sync\", \"--json\"]"`
		Confirm string   `json:"confirm,omitempty" jsonschema:"for a command that may write: the token from the first call, after the user said yes"`
	}
)

// NewServer is the taw-fleet MCP server.
func NewServer(d Deps) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "taw-fleet", Title: "taw-fleet", Version: d.Version}, &sdk.ServerOptions{
		Instructions: "taw-fleet: the TAW WordPress sites on this Mac (Local by Flywheel), their themes, GitHub, production and client comments. " +
			"Start with list_sites or doctor; show_site for one site. Every tool is a taw-fleet command a person can run too.",
	})
	sdk.AddTool(s, tool("list_sites"), func(ctx context.Context, _ *sdk.CallToolRequest, in listIn) (*sdk.CallToolResult, any, error) {
		return run(ctx, d, flags([]string{"list", "--json"}, in.All, "--all", in.Offline, "--offline"))
	})
	sdk.AddTool(s, tool("show_site"), func(ctx context.Context, _ *sdk.CallToolRequest, in siteIn) (*sdk.CallToolResult, any, error) {
		if strings.TrimSpace(in.Site) == "" {
			return nil, nil, errors.New("site is required (list_sites shows them)")
		}
		return run(ctx, d, flags([]string{"show", in.Site, "--json"}, in.Offline, "--offline"))
	})
	sdk.AddTool(s, tool("doctor"), func(ctx context.Context, _ *sdk.CallToolRequest, in maybeSiteIn) (*sdk.CallToolResult, any, error) {
		return run(ctx, d, flags(withSite([]string{"doctor"}, in.Site, "--json"), in.Offline, "--offline"))
	})
	sdk.AddTool(s, tool("pull_requests"), func(ctx context.Context, _ *sdk.CallToolRequest, in offlineIn) (*sdk.CallToolResult, any, error) {
		return run(ctx, d, flags([]string{"prs", "--json"}, in.Offline, "--offline"))
	})
	sdk.AddTool(s, tool("comments"), func(ctx context.Context, _ *sdk.CallToolRequest, in maybeSiteIn) (*sdk.CallToolResult, any, error) {
		return run(ctx, d, flags(withSite([]string{"comments"}, in.Site, "--json"), in.Offline, "--offline"))
	})
	sdk.AddTool(s, tool("production"), func(ctx context.Context, _ *sdk.CallToolRequest, in offlineIn) (*sdk.CallToolResult, any, error) {
		return run(ctx, d, flags([]string{"live", "--json"}, in.Offline, "--offline"))
	})
	tk := newTokens(d.Now)
	sdk.AddTool(s, tool("taw"), func(ctx context.Context, _ *sdk.CallToolRequest, in tawIn) (*sdk.CallToolResult, any, error) {
		if strings.TrimSpace(in.Theme) == "" {
			return nil, nil, errors.New("theme is required (list_sites shows them)")
		}
		if readOnly(in.Args) == nil {
			out, err := d.Taw(ctx, in.Theme, in.Args)
			return result(out, err)
		}
		key := "taw\x00" + in.Theme + "\x00" + strings.Join(in.Args, "\x00")
		line := "taw @" + strings.TrimPrefix(in.Theme, "@") + " " + strings.Join(in.Args, " ")
		if in.Confirm == "" {
			tok := tk.issue(key)
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "Question: Run `" + line + "`? It's the theme's own command, and it may change the theme's files or its site's database.\n\n" +
				"Nothing has changed yet. Ask the user this question. If they say yes, call taw again with the same arguments and confirm: \"" + tok + "\" (good for " + ttlWords + ", once)."}}}, nil, nil
		}
		if !tk.take(in.Confirm, key) {
			return nil, nil, errors.New("that confirm token isn't valid for these arguments, was used already, or expired: call taw without confirm to get the question again")
		}
		out, err := d.Taw(ctx, in.Theme, in.Args)
		return result(out, err)
	})
	addWrites(s, d, tk)

	s.AddPrompt(&sdk.Prompt{
		Name:        "update_theme",
		Title:       "Update a theme with Claude",
		Description: "The update prompt taw-fleet hands Claude Code for one theme (h and A in the dashboard, taw-fleet handoff <site>): the theme, its site, PHP, wp-cli, git state and the rules.",
		Arguments:   []*sdk.PromptArgument{{Name: "site", Description: "the site or theme folder", Required: true}},
	}, func(ctx context.Context, req *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		site := req.Params.Arguments["site"]
		if site == "" {
			return nil, errors.New("site is required")
		}
		out, err := d.Fleet(ctx, []string{"handoff", site})
		if err != nil {
			return nil, err
		}
		return &sdk.GetPromptResult{
			Description: "Update " + site + " (taw-fleet handoff " + site + ")",
			Messages:    []*sdk.PromptMessage{{Role: "user", Content: &sdk.TextContent{Text: out}}},
		}, nil
	})
	s.AddPrompt(&sdk.Prompt{
		Name:        "resolve_comments",
		Title:       "Resolve a site's client comments",
		Description: "The prompt X hands Claude Code for one site (taw-fleet comments <site> --prompt): its open BugSmash comments, to resolve with the theme's resolve-comments skill.",
		Arguments:   []*sdk.PromptArgument{{Name: "site", Description: "the site or theme folder", Required: true}},
	}, func(ctx context.Context, req *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		site := req.Params.Arguments["site"]
		if site == "" {
			return nil, errors.New("site is required")
		}
		out, err := d.Fleet(ctx, []string{"comments", site, "--prompt"})
		if err != nil {
			return nil, err
		}
		return &sdk.GetPromptResult{
			Description: "Resolve the open comments on " + site + " (taw-fleet comments " + site + " --prompt)",
			Messages:    []*sdk.PromptMessage{{Role: "user", Content: &sdk.TextContent{Text: out}}},
		}, nil
	})
	return s
}

// flags appends each flag whose condition holds: pairs of (bool, flag).
func flags(args []string, pairs ...any) []string {
	for i := 0; i+1 < len(pairs); i += 2 {
		if on, _ := pairs[i].(bool); on {
			args = append(args, pairs[i+1].(string))
		}
	}
	return args
}

func withSite(args []string, site string, rest ...string) []string {
	if s := strings.TrimSpace(site); s != "" {
		args = append(args, s)
	}
	return append(args, rest...)
}

// run is a taw-fleet command's --json as the tool's result. A command that
// printed JSON and then failed (doctor's exit code, say) still answers.
func run(ctx context.Context, d Deps, args []string) (*sdk.CallToolResult, any, error) {
	out, err := d.Fleet(ctx, args)
	if err != nil && json.Valid([]byte(strings.TrimSpace(out))) && strings.TrimSpace(out) != "" {
		err = nil
	}
	return result(out, err)
}

func result(out string, err error) (*sdk.CallToolResult, any, error) {
	if err != nil {
		msg := err.Error()
		if o := strings.TrimSpace(out); o != "" {
			msg += "\n\n" + o
		}
		return nil, nil, errors.New(msg)
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: out}}}, nil, nil
}

// readOnly is nil for the theme commands that only report.
func readOnly(args []string) error {
	if len(args) == 0 {
		return nil // the command list
	}
	ok := false
	for _, c := range readOnlyTaw {
		if args[0] == c {
			ok = true
		}
	}
	if !ok {
		return fmt.Errorf("%s isn't one of the read-only commands (%s)", args[0], strings.Join(readOnlyTaw, ", "))
	}
	for _, a := range args[1:] {
		if a == "--apply" || strings.HasPrefix(a, "--apply=") || a == "-a" {
			return fmt.Errorf("%s --apply writes", args[0])
		}
	}
	return nil
}
