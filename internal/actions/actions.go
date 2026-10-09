// Package actions does what a dashboard key or an `open`/`handoff` flag asks:
// open the theme in an editor, the site in a browser, the repo on GitHub…,
// and hand a theme update to an agent. The CLI and the dashboard share it.
package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/create"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/tools"
)

// Kind is one shortcut.
type Kind string

// Shortcuts.
const (
	Editor     Kind = "editor"
	Finder     Kind = "finder"
	Browser    Kind = "browser"
	Admin      Kind = "admin"
	GitHub     Kind = "github"
	PRs        Kind = "prs"
	Terminal   Kind = "terminal"
	Production Kind = "production"
)

// All lists the shortcuts in display order.
var All = []Kind{Editor, Finder, Browser, Admin, GitHub, PRs, Terminal, Production}

// Actions runs shortcuts. Build it with New.
type Actions struct {
	Paths  paths.Paths
	Exec   exec.Runner
	Config config.Config
	Tools  tools.Inventory
	Now    func() time.Time
	open   tools.Opener

	LocalAPI   create.API    // create's Local API; nil = the running Local app
	CreatePoll time.Duration // create's polling interval; 0 = 2 s
}

// New detects the installed apps and returns ready Actions.
func New(p paths.Paths, r exec.Runner, cfg config.Config) *Actions {
	return &Actions{Paths: p, Exec: r, Config: cfg, Tools: tools.Detect(p), Now: time.Now, open: tools.Opener{Exec: r}}
}

// Do runs one shortcut on a theme and says what it did.
func (a *Actions) Do(ctx context.Context, k Kind, s site.Site, t site.Theme) (string, error) {
	switch k {
	case Editor:
		app, err := tools.Pick(a.Tools.Editors, a.Config.Editor)
		if err != nil {
			return "", fmt.Errorf("editor: %w", err)
		}
		return fmt.Sprintf("Opened %s in %s", t.Dir, app.Name), a.open.With(ctx, app, t.RealPath)
	case Finder:
		return "Showed " + t.Dir + " in Finder", a.open.Reveal(ctx, t.RealPath)
	case Browser:
		if s.URL == "" {
			return "", errors.New("this site has no domain in Local")
		}
		return "Opened " + s.URL, a.open.URL(ctx, s.URL)
	case Admin:
		if s.URL == "" {
			return "", errors.New("this site has no domain in Local")
		}
		u := strings.TrimRight(s.URL, "/") + "/wp-admin/"
		return "Opened " + u, a.open.URL(ctx, u)
	case GitHub, PRs:
		if t.Git == nil || t.Git.Repo == nil {
			return "", errors.New("no GitHub repository (no origin remote)")
		}
		u := t.Git.Repo.WebURL()
		if k == PRs {
			u += "/pulls"
		}
		return "Opened " + u, a.open.URL(ctx, u)
	case Terminal:
		app, err := tools.Pick(a.Tools.Terminals, a.Config.Terminal)
		if err != nil {
			return "", fmt.Errorf("terminal: %w", err)
		}
		return fmt.Sprintf("Opened %s at %s", app.Name, t.Dir), a.open.TerminalAt(ctx, app, t.RealPath)
	case Production:
		u := a.Config.Site(s.Slug).ProductionURL
		if u == "" {
			return "", fmt.Errorf("no production URL for %s: add [sites.%s] production_url to %s", s.Slug, s.Slug, config.File(a.Paths))
		}
		return "Opened " + u, a.open.URL(ctx, u)
	}
	return "", fmt.Errorf("unknown shortcut %q", k)
}

// Handoff builds the agent prompt for a theme.
func (a *Actions) Handoff(s site.Site, t site.Theme, findings []site.Finding) (handoff.Prompt, error) {
	in := handoff.Input{
		Site: s, Theme: t, Findings: findings, Now: a.now(),
		HasSkill: fileExists(filepath.Join(t.RealPath, ".claude", "skills", "update-theme", "SKILL.md")),
	}
	if php, ok := local.PHPBinary(a.Paths, s.PHPVersion); ok {
		in.Tools.PHP = php
	}
	if c, ok := local.ComposerPhar(a.Paths); ok {
		in.Tools.Composer = c
	}
	if w, ok := local.WPCliPhar(a.Paths); ok {
		in.Tools.WPCli = w
	}
	cs := a.Config.Site(s.Slug)
	in.Production, in.Notes = cs.ProductionURL, cs.Notes
	return handoff.Build(in)
}

// Copy puts text on the clipboard.
func (a *Actions) Copy(ctx context.Context, text string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := a.Exec.Run(ctx, exec.Spec{Name: "/usr/bin/pbcopy", Stdin: strings.NewReader(text)})
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("pbcopy: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// ErrNoClaude means Claude Code isn't installed.
var ErrNoClaude = errors.New("Claude Code (`claude`) isn't installed; see https://claude.com/claude-code, or copy the prompt instead")

// Claude finds the Claude Code CLI.
func (a *Actions) Claude() (string, error) {
	if p, err := a.Paths.LookPath("claude"); err == nil {
		return p, nil
	}
	for _, p := range []string{
		filepath.Join(a.Paths.Home, ".local", "bin", "claude"),
		filepath.Join(a.Paths.Home, ".claude", "local", "claude"),
		"/opt/homebrew/bin/claude", "/usr/local/bin/claude",
	} {
		if fileExists(p) {
			return p, nil
		}
	}
	return "", ErrNoClaude
}

// Launch opens a new terminal window in the theme folder running Claude Code
// with the prompt as its first message. The prompt and a small launcher
// script are written to the cache folder; nothing else is changed.
func (a *Actions) Launch(ctx context.Context, s site.Site, t site.Theme, p handoff.Prompt) (string, error) {
	claude, err := a.Claude()
	if err != nil {
		return "", err
	}
	term, fallback, err := a.scriptTerminal()
	if err != nil {
		return "", err
	}

	dir := filepath.Join(a.Paths.CacheDir, "handoff")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	base := fmt.Sprintf("%s-%s-%s", safeName(s.Slug), safeName(t.Dir), a.now().Format("20060102-150405"))
	promptFile := filepath.Join(dir, base+".md")
	script := filepath.Join(dir, base+".command")
	if err := os.WriteFile(promptFile, []byte(p.Text), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(script, []byte(LauncherScript(p.Title, t.RealPath, claude, promptFile)), 0o700); err != nil {
		return "", err
	}
	if err := a.open.RunScript(ctx, term, script, fallback); err != nil {
		return "", err
	}
	return fmt.Sprintf("Started Claude Code in %s for %s (branch %s)", ranIn(term, fallback), t.Dir, p.Branch), nil
}

// Window opens a new terminal window running the dashboard: exe with args
// (which must keep it from opening yet another window). It says which
// terminal it used.
func (a *Actions) Window(ctx context.Context, exe string, args []string) (string, error) {
	term, fallback, err := a.scriptTerminal()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(a.Paths.CacheDir, "window")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	script := filepath.Join(dir, "taw-fleet.command")
	used := ranIn(term, fallback)
	if err := os.WriteFile(script, []byte(WindowScript(exe, args, used == "Terminal")), 0o700); err != nil {
		return "", err
	}
	if err := a.open.RunScript(ctx, term, script, fallback); err != nil {
		return "", err
	}
	return used, nil
}

// scriptTerminal is the terminal that runs a script, and the one used
// instead when it can't (see tools.Opener.RunScript).
func (a *Actions) scriptTerminal() (term, fallback tools.App, err error) {
	term, err = tools.Pick(a.Tools.Terminals, a.Config.Terminal)
	if err != nil {
		return term, fallback, fmt.Errorf("terminal: %w", err)
	}
	fallback, ferr := tools.Pick(a.Tools.Terminals, "Terminal")
	if ferr != nil {
		fallback = tools.App{Name: "Terminal", Path: "/System/Applications/Utilities/Terminal.app", Kind: tools.Terminal}
	}
	return term, fallback, nil
}

// ranIn names the terminal RunScript used.
func ranIn(term, fallback tools.App) string {
	switch term.Name {
	case "Terminal", "iTerm2", "Ghostty", "kitty":
		return term.Name
	}
	return fallback.Name
}

// WindowScript is the .command file the new window runs. Terminal keeps a
// window open after its shell exits unless the profile says otherwise, so
// with closeTerminal the window closes itself once the dashboard quits
// cleanly; after a crash it stays open with the error.
func WindowScript(exe string, args []string, closeTerminal bool) string {
	line := shq(exe)
	for _, a := range args {
		line += " " + shq(a)
	}
	if !closeTerminal {
		return "#!/bin/sh\n# taw-fleet dashboard window\nexec " + line + "\n"
	}
	return "#!/bin/sh\n# taw-fleet dashboard window\n" + line + " || exit\n" +
		`tty=$(tty)` + "\n" +
		`osascript -e "tell application \"Terminal\" to close (every window whose tty is \"$tty\")" >/dev/null 2>&1 &` + "\n"
}

// LauncherScript is the .command file a terminal runs: go to the theme and
// start Claude Code with the prompt file's contents. Every value is
// single-quoted for the shell.
func LauncherScript(title, dir, claude, promptFile string) string {
	return "#!/bin/sh\n" +
		"# taw-fleet handoff: " + strings.ReplaceAll(title, "\n", " ") + "\n" +
		"cd " + shq(dir) + " || exit 1\n" +
		"exec " + shq(claude) + ` "$(cat ` + shq(promptFile) + `)"` + "\n"
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeName(s string) string { return strings.Trim(unsafe.ReplaceAllString(s, "-"), "-") }

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func (a *Actions) now() time.Time {
	if a.Now == nil {
		return time.Now()
	}
	return a.Now()
}
