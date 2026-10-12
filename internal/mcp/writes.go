package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Writes ask first, as the dashboard does (ADR-0002, decision 5): called
// without confirm, a write tool runs its command up to the question, says
// no, and returns the question with a one-time token. Called again with the
// token and the same arguments, it runs the command with --yes.

// tokenTTL is how long a question's token stays good.
const (
	tokenTTL = 10 * time.Minute
	ttlWords = "10 minutes"
)

type tokens struct {
	mu   sync.Mutex
	now  func() time.Time
	open map[string]pending
}

type pending struct {
	key     string // the tool and its arguments
	expires time.Time
}

func newTokens(now func() time.Time) *tokens {
	if now == nil {
		now = time.Now
	}
	return &tokens{now: now, open: map[string]pending{}}
}

func (t *tokens) issue(key string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, p := range t.open {
		if t.now().After(p.expires) {
			delete(t.open, k)
		}
	}
	t.open[tok] = pending{key: key, expires: t.now().Add(tokenTTL)}
	return tok
}

// take spends a token: good once, for the same tool and arguments.
func (t *tokens) take(tok, key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.open[tok]
	delete(t.open, tok)
	return ok && p.key == key && !t.now().After(p.expires)
}

// WriteDoc is a write tool as people see it.
type WriteDoc struct {
	Name, Title, Command, Key, Description string
	Production                             bool // it can reach production (destructiveHint)
	Replaces                               bool // it replaces data in place, like a database (destructiveHint)
}

// WriteTools are the tools that change something, after asking.
var WriteTools = []WriteDoc{
	{"update_site", "Update a site", "taw-fleet update <site>", "u",
		"The whole update of one theme, as its taw.json says (vendor/bin/taw update): a new branch, taw/core, the framework files, the migrations, the checks, one commit, then a pull request.", false, false},
	{"update_all", "Update every theme", "taw-fleet update --all", "U",
		"Every client TAW theme that needs an update, one after another, each as its taw.json says, each ending in its own pull request. A theme that stops doesn't stop the others.", false, false},
	{"merge_pr", "Merge a pull request", "taw-fleet merge <site> [--pr N]", "M",
		"Merges the theme's pull request on GitHub, which deploys production when the theme has a deploy workflow; then brings this Mac onto the default branch.", true, false},
	{"site_branch", "Back to the site's branch", "taw-fleet branch <site>", "D",
		"Switches the theme to its default branch; a branch that tracks another repository is deleted when it has nothing of its own. Refused with uncommitted changes.", false, false},
	{"start_site", "Start a site", "taw-fleet start <site>", "s",
		"Starts the site in Local by Flywheel (the Local app must be open).", false, false},
	{"stop_site", "Stop a site", "taw-fleet stop <site>", "s",
		"Stops the site in Local by Flywheel.", false, false},
	{"sync_apply", "Apply the scaffold", "taw-fleet sync <site> --apply", "S",
		"Writes the framework files that differ from the taw-theme scaffold (Tier 1); files to review (Tier 2) stay as they are.", false, false},
	{"pull_content", "Pull production content", "taw-fleet pull <site>", "C",
		"Brings the production site's content into the Local site. The first call shows the preview of what would change; confirming imports it (a rollback snapshot is kept).", false, false},
	{"restore_site", "Put back a restore point", "taw-fleet restore <site> <point>", "z",
		"Puts a restore point back (restore_points lists them; point defaults to the latest): the theme onto the point's branch (commits since stay) and the Local database from its copy, replacing the current one. How things are just before is saved as a new point first, so it can be undone.", false, true},
}

type (
	siteWriteIn struct {
		Site    string `json:"site" jsonschema:"the site: its Local folder, name, domain or a theme folder"`
		Theme   string `json:"theme,omitempty" jsonschema:"which TAW theme, when the site has several"`
		Confirm string `json:"confirm,omitempty" jsonschema:"the token from the first call, after the user said yes to its question"`
	}
	allWriteIn struct {
		Confirm string `json:"confirm,omitempty" jsonschema:"the token from the first call, after the user said yes to its question"`
	}
	restoreIn struct {
		Site    string `json:"site" jsonschema:"the site: its Local folder, name, domain or a theme folder"`
		Point   string `json:"point,omitempty" jsonschema:"the restore point's id (restore_points lists them); empty or latest for the newest"`
		Confirm string `json:"confirm,omitempty" jsonschema:"the token from the first call, after the user said yes to its question"`
	}
	mergeIn struct {
		Site    string `json:"site" jsonschema:"the site: its Local folder, name, domain or a theme folder"`
		Theme   string `json:"theme,omitempty" jsonschema:"which TAW theme, when the site has several"`
		PR      int    `json:"pr,omitempty" jsonschema:"the pull request number, when the theme has several open"`
		Confirm string `json:"confirm,omitempty" jsonschema:"the token from the first call, after the user said yes to its question"`
	}
)

func writeDoc(name string) WriteDoc {
	for _, t := range WriteTools {
		if t.Name == name {
			return t
		}
	}
	panic("mcp: no write tool " + name)
}

func writeTool(name string) *sdk.Tool {
	d := writeDoc(name)
	prod := d.Production || d.Replaces
	return &sdk.Tool{
		Name:  d.Name,
		Title: d.Title,
		Description: d.Description + " Same as `" + d.Command + "` (dashboard: " + d.Key + "). " +
			"Asks first: call it without confirm to get the question and a token, ask the user, then call it again with the same arguments and confirm set to the token.",
		Annotations: &sdk.ToolAnnotations{Title: d.Title, DestructiveHint: &prod},
	}
}

func siteArgs(cmd []string, site, theme string) ([]string, error) {
	if strings.TrimSpace(site) == "" {
		return nil, errors.New("site is required (list_sites shows them)")
	}
	args := append(append([]string{}, cmd...), site)
	if theme != "" {
		args = append(args, "--theme", theme)
	}
	return args, nil
}

func addWrites(s *sdk.Server, d Deps, tk *tokens) {
	site := func(name, cmd string, flags ...string) {
		sdk.AddTool(s, writeTool(name), func(ctx context.Context, req *sdk.CallToolRequest, in siteWriteIn) (*sdk.CallToolResult, any, error) {
			args, err := siteArgs([]string{cmd}, in.Site, in.Theme)
			if err != nil {
				return nil, nil, err
			}
			return ask(ctx, req, d, tk, name, append(args, flags...), in.Confirm)
		})
	}
	site("update_site", "update")
	site("site_branch", "branch")
	site("start_site", "start")
	site("stop_site", "stop")
	site("sync_apply", "sync", "--apply")
	site("pull_content", "pull")
	sdk.AddTool(s, writeTool("update_all"), func(ctx context.Context, req *sdk.CallToolRequest, in allWriteIn) (*sdk.CallToolResult, any, error) {
		return ask(ctx, req, d, tk, "update_all", []string{"update", "--all"}, in.Confirm)
	})
	sdk.AddTool(s, writeTool("restore_site"), func(ctx context.Context, req *sdk.CallToolRequest, in restoreIn) (*sdk.CallToolResult, any, error) {
		args, err := siteArgs([]string{"restore"}, in.Site, "")
		if err != nil {
			return nil, nil, err
		}
		point := strings.TrimSpace(in.Point)
		if point == "" {
			point = "latest"
		}
		return ask(ctx, req, d, tk, "restore_site", append(args, point), in.Confirm)
	})
	sdk.AddTool(s, writeTool("merge_pr"), func(ctx context.Context, req *sdk.CallToolRequest, in mergeIn) (*sdk.CallToolResult, any, error) {
		args, err := siteArgs([]string{"merge"}, in.Site, in.Theme)
		if err != nil {
			return nil, nil, err
		}
		if in.PR > 0 {
			args = append(args, "--pr", fmt.Sprint(in.PR))
		}
		return ask(ctx, req, d, tk, "merge_pr", args, in.Confirm)
	})
}

// ask is a write's two calls: the question, then the command with --yes.
func ask(ctx context.Context, req *sdk.CallToolRequest, d Deps, tk *tokens, tool string, args []string, confirm string) (*sdk.CallToolResult, any, error) {
	key := tool + "\x00" + strings.Join(args, "\x00")
	if confirm == "" {
		q, out, err := d.Ask(ctx, args)
		if err != nil {
			return result(out, err)
		}
		if q == "" { // it had nothing to ask: nothing to do, or it said why
			return result(out, nil)
		}
		tok := tk.issue(key)
		text := strings.TrimSpace(out)
		if text != "" {
			text += "\n\n"
		}
		text += "Question: " + q + "\n\nNothing has changed yet. Ask the user this question. If they say yes, call " +
			tool + " again with the same arguments and confirm: \"" + tok + "\" (good for " + ttlWords + ", once)."
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, nil, nil
	}
	if !tk.take(confirm, key) {
		return nil, nil, errors.New("that confirm token isn't valid for these arguments, was used already, or expired: call " + tool + " without confirm to get the question again")
	}
	out, err := d.Do(ctx, append(args, "--yes"), progress(ctx, req))
	return result(out, err)
}

// progress forwards a command's progress lines as MCP progress
// notifications, when the client asked for them.
func progress(ctx context.Context, req *sdk.CallToolRequest) func(string) {
	if req == nil || req.Params == nil || req.Session == nil {
		return func(string) {}
	}
	tok := req.Params.GetProgressToken()
	if tok == nil {
		return func(string) {}
	}
	var mu sync.Mutex
	n := 0.0
	return func(line string) {
		if strings.TrimSpace(line) == "" {
			return
		}
		mu.Lock()
		n++
		p := n
		mu.Unlock()
		_ = req.Session.NotifyProgress(ctx, &sdk.ProgressNotificationParams{ProgressToken: tok, Progress: p, Message: strings.TrimSpace(line)})
	}
}
