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
	{"create_site", "Create a site", "taw-fleet create <name> --classic|--block", "n",
		"Creates a Local site with a new TAW theme: classic (taw-theme: PHP blocks, Vite, Tailwind, Alpine) or block (taw-gutenberg: full site editing). Local must be open. The admin user and email come from the settings' [create] section unless given. The admin password isn't shown here; the person can set one with taw-fleet wp <site> user update <user> --prompt=user_pass.", false, false},
	{"create_repo", "Put a theme on GitHub", "taw-fleet repo <site> [--owner X] [--name Y]", "I",
		"For a theme with no GitHub repository yet (a new site): a private repository (owner: the settings' first github_owners, else gh's account; name: <theme folder>--theme), origin, the default and current branches pushed, and the dump check (ci / committed-files) required on the default branch. Needs gh signed in.", false, false},
	{"deploy_key", "Make a deploy key", "taw-fleet deploy key <site>", "I on the Deploy step",
		"Makes the site's ed25519 deploy key (kept in the Keychain until deploy_apply stores it in GitHub) and returns only its public half, for the person to add at the host (deploy_profiles says where). Called again, it returns the same public key.", false, false},
	{"deploy_apply", "Set up the deploy", "taw-fleet deploy apply <site> …", "I on the Deploy step",
		"For a theme on GitHub with no deploy yet: tests the connection again (pinning the server's host key), stores the settings as repository variables and the key as the secret DEPLOY_SSH_KEY, opens a pull request adding taw/core's shared deploy workflow (merging it deploys), and adds production_url to the settings. The person adds the public key at the host first.", false, false},
	{"trust_production", "Trust a production site's key", "taw-fleet live trust <site>", "the Companion step",
		"Asks the live site's companion for the key it signs its answers with, checks the answer is signed by it, and pins it, so taw-fleet verifies the site from then on (trust on first use: the question shows the key's id for the person to confirm). For a site just deployed; a pinned key that changes is replaced only after asking.", true, false},
	{"link_bugsmash", "Link a BugSmash project", "taw-fleet comments link <site> <project>", "the BugSmash step",
		"Checks the project with BugSmash (bugsmash_projects lists them) and sets bugsmash_project for the site in the settings, so its comments show in the dashboard, watch and the reports. The project itself is made in BugSmash.", false, false},
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
	createIn struct {
		Name       string `json:"name" jsonschema:"the site's name, e.g. Acme Bakery (its folder and domain come from it)"`
		Kind       string `json:"kind" jsonschema:"classic (taw-theme: PHP blocks, Tailwind) or block (taw-gutenberg: full site editing)"`
		Domain     string `json:"domain,omitempty" jsonschema:"the Local domain (default <name>.local)"`
		AdminUser  string `json:"admin_user,omitempty" jsonschema:"WordPress admin username (default: the settings' [create] admin_user)"`
		AdminEmail string `json:"admin_email,omitempty" jsonschema:"WordPress admin email (default: the settings' [create] admin_email)"`
		Confirm    string `json:"confirm,omitempty" jsonschema:"the token from the first call, after the user said yes to its question"`
	}
	repoIn struct {
		Site    string `json:"site" jsonschema:"the site: its Local folder, name, domain or a theme folder"`
		Theme   string `json:"theme,omitempty" jsonschema:"which TAW theme, when the site has several"`
		Owner   string `json:"owner,omitempty" jsonschema:"the GitHub account or organization (default: the settings' first github_owners, else gh's account)"`
		Name    string `json:"name,omitempty" jsonschema:"the repository's name (default: <theme folder>--theme)"`
		Confirm string `json:"confirm,omitempty" jsonschema:"the token from the first call, after the user said yes to its question"`
	}
	deployIn struct {
		Site      string `json:"site" jsonschema:"the site: its Local folder, name, domain or a theme folder"`
		Theme     string `json:"theme,omitempty" jsonschema:"which TAW theme, when the site has several"`
		Profile   string `json:"profile,omitempty" jsonschema:"a host profile id from deploy_profiles: pre-fills the method, port and git defaults"`
		Method    string `json:"method,omitempty" jsonschema:"rsync (SSH + rsync) or git (git push)"`
		Host      string `json:"host,omitempty" jsonschema:"rsync: the SSH host"`
		Port      string `json:"port,omitempty" jsonschema:"rsync: the SSH port (default 22)"`
		User      string `json:"user,omitempty" jsonschema:"rsync: the SSH user"`
		Path      string `json:"path,omitempty" jsonschema:"rsync: the theme's folder on the server, ending in /wp-content/themes/<theme>"`
		GitRemote string `json:"git_remote,omitempty" jsonschema:"git: the host's SSH git remote"`
		GitBranch string `json:"git_branch,omitempty" jsonschema:"git: the branch the host deploys (default master)"`
		GitPath   string `json:"git_path,omitempty" jsonschema:"git: where the theme sits in the host's repository (default wp-content/themes/<theme>)"`
		URL       string `json:"url,omitempty" jsonschema:"the production site's URL"`
		Confirm   string `json:"confirm,omitempty" jsonschema:"deploy_apply: the token from the first call, after the user said yes to its question"`
	}
	linkIn struct {
		Site    string `json:"site" jsonschema:"the site: its Local folder, name, domain or a theme folder"`
		Project string `json:"project" jsonschema:"the BugSmash project's id (bugsmash_projects lists them)"`
		Confirm string `json:"confirm,omitempty" jsonschema:"the token from the first call, after the user said yes to its question"`
	}
	mergeIn struct {
		Site    string `json:"site" jsonschema:"the site: its Local folder, name, domain or a theme folder"`
		Theme   string `json:"theme,omitempty" jsonschema:"which TAW theme, when the site has several"`
		PR      int    `json:"pr,omitempty" jsonschema:"the pull request number, when the theme has several open"`
		Confirm string `json:"confirm,omitempty" jsonschema:"the token from the first call, after the user said yes to its question"`
	}
)

// args is the deploy subcommand's argument list.
func (in deployIn) args(sub string) ([]string, error) {
	args, err := siteArgs([]string{"deploy", sub}, in.Site, in.Theme)
	if err != nil {
		return nil, err
	}
	for _, f := range [][2]string{{"--profile", in.Profile}, {"--method", in.Method}, {"--host", in.Host}, {"--port", in.Port},
		{"--user", in.User}, {"--path", in.Path}, {"--git-remote", in.GitRemote}, {"--git-branch", in.GitBranch},
		{"--git-path", in.GitPath}, {"--url", in.URL}} {
		if f[1] != "" {
			args = append(args, f[0], f[1])
		}
	}
	return args, nil
}

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
	sdk.AddTool(s, writeTool("create_site"), func(ctx context.Context, req *sdk.CallToolRequest, in createIn) (*sdk.CallToolResult, any, error) {
		if strings.TrimSpace(in.Name) == "" {
			return nil, nil, errors.New("name is required")
		}
		args := []string{"create", in.Name}
		switch in.Kind {
		case "classic", "block":
			args = append(args, "--"+in.Kind)
		default:
			return nil, nil, errors.New("kind is classic or block")
		}
		for _, f := range [][2]string{{"--domain", in.Domain}, {"--admin-user", in.AdminUser}, {"--admin-email", in.AdminEmail}} {
			if f[1] != "" {
				args = append(args, f[0], f[1])
			}
		}
		args = append(args, "--no-password")
		return ask(ctx, req, d, tk, "create_site", args, in.Confirm)
	})
	sdk.AddTool(s, writeTool("create_repo"), func(ctx context.Context, req *sdk.CallToolRequest, in repoIn) (*sdk.CallToolResult, any, error) {
		args, err := siteArgs([]string{"repo"}, in.Site, in.Theme)
		if err != nil {
			return nil, nil, err
		}
		if in.Owner != "" {
			args = append(args, "--owner", in.Owner)
		}
		if in.Name != "" {
			args = append(args, "--name", in.Name)
		}
		return ask(ctx, req, d, tk, "create_repo", args, in.Confirm)
	})
	sdk.AddTool(s, writeTool("deploy_key"), func(ctx context.Context, req *sdk.CallToolRequest, in siteWriteIn) (*sdk.CallToolResult, any, error) {
		args, err := siteArgs([]string{"deploy", "key"}, in.Site, in.Theme)
		if err != nil {
			return nil, nil, err
		}
		return ask(ctx, req, d, tk, "deploy_key", args, in.Confirm)
	})
	sdk.AddTool(s, writeTool("deploy_apply"), func(ctx context.Context, req *sdk.CallToolRequest, in deployIn) (*sdk.CallToolResult, any, error) {
		args, err := in.args("apply")
		if err != nil {
			return nil, nil, err
		}
		return ask(ctx, req, d, tk, "deploy_apply", args, in.Confirm)
	})
	sdk.AddTool(s, writeTool("trust_production"), func(ctx context.Context, req *sdk.CallToolRequest, in siteWriteIn) (*sdk.CallToolResult, any, error) {
		args, err := siteArgs([]string{"live", "trust"}, in.Site, "")
		if err != nil {
			return nil, nil, err
		}
		return ask(ctx, req, d, tk, "trust_production", args, in.Confirm)
	})
	sdk.AddTool(s, writeTool("link_bugsmash"), func(ctx context.Context, req *sdk.CallToolRequest, in linkIn) (*sdk.CallToolResult, any, error) {
		args, err := siteArgs([]string{"comments", "link"}, in.Site, "")
		if err != nil {
			return nil, nil, err
		}
		if strings.TrimSpace(in.Project) == "" {
			return nil, nil, errors.New("project is required (bugsmash_projects lists them)")
		}
		return ask(ctx, req, d, tk, "link_bugsmash", append(args, strings.TrimSpace(in.Project)), in.Confirm)
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
