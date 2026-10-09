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
	VitePoll   time.Duration // how often Work checks for Vite; 0 = 300 ms

	// CoreLatest asks GitHub for the newest taw-core, skipping the cache, for
	// an update-all; nil = keep the scan's answer.
	CoreLatest func(ctx context.Context) (string, error)
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
	return handoff.Build(a.handoffInput(s, t, findings))
}

func (a *Actions) handoffInput(s site.Site, t site.Theme, findings []site.Finding) handoff.Input {
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
	return in
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

// Launched is a Claude Code window that was opened.
type Launched struct {
	Message string
	Done    string // a file that appears when Claude Code exits
}

// Launch opens a new terminal window in the theme folder running Claude Code
// with the prompt as its first message. The prompt and a small launcher
// script are written to the cache folder; nothing else is changed.
//
// beside is the dashboard's own tty ("" = don't arrange windows): in
// Terminal the dashboard window then takes the left half of the screen and
// Claude's window the right half.
func (a *Actions) Launch(ctx context.Context, s site.Site, t site.Theme, p handoff.Prompt, beside string) (Launched, error) {
	dir := filepath.Join(a.Paths.CacheDir, "handoff")
	base := fmt.Sprintf("%s-%s-%s", safeName(s.Slug), safeName(t.Dir), a.now().Format("20060102-150405"))
	ls := LaunchScript{Title: p.Title, Dir: t.RealPath,
		Prompt: filepath.Join(dir, base+".md"), Done: filepath.Join(dir, base+".done")}
	used, placed, err := a.launch(ctx, ls, p.Text, filepath.Join(dir, base+".command"), beside)
	if err != nil {
		return Launched{}, err
	}
	msg := fmt.Sprintf("Started Claude Code in %s for %s (branch %s)", used, t.Dir, p.Branch)
	if placed {
		msg = fmt.Sprintf("Claude Code is working on %s in the window on the right (branch %s)", t.Dir, p.Branch)
	}
	return Launched{Message: msg, Done: ls.Done}, nil
}

// launch writes the prompt and the script, and opens a terminal running
// Claude Code with it, beside the dashboard when beside is its tty. It says
// which terminal ran it and whether the windows were arranged.
func (a *Actions) launch(ctx context.Context, ls LaunchScript, prompt, script, beside string) (used string, placed bool, err error) {
	if ls.Claude, err = a.Claude(); err != nil {
		return "", false, err
	}
	term, fallback, err := a.scriptTerminal()
	if err != nil {
		return "", false, err
	}
	used = ranIn(term, fallback)
	ls.CloseTerminal = used == "Terminal"
	if err := os.MkdirAll(filepath.Dir(ls.Prompt), 0o700); err != nil {
		return "", false, err
	}
	if err := os.WriteFile(ls.Prompt, []byte(prompt), 0o600); err != nil {
		return "", false, err
	}
	_ = os.Remove(ls.Done)
	if beside != "" && used == "Terminal" {
		if left, right, ok := a.halves(ctx, beside); ok {
			ls.Bounds = right
			placed = a.place(ctx, beside, left) == nil
		}
	}
	if err := os.WriteFile(script, []byte(ls.String()), 0o700); err != nil {
		return "", false, err
	}
	if err := a.open.RunScript(ctx, term, script, fallback); err != nil {
		return "", false, err
	}
	return used, placed, nil
}

// Bounds is a window's {left, top, right, bottom} in screen points.
type Bounds [4]int

// halves splits the usable area (below the menu bar, beside the Dock) of
// the screen showing the Terminal window on tty into a left and a right
// half. With several displays, that's the one the dashboard is on.
func (a *Actions) halves(ctx context.Context, tty string) (left, right Bounds, ok bool) {
	res, err := a.Exec.Run(ctx, exec.Spec{Name: "/usr/bin/osascript", Args: []string{"-l", "JavaScript", "-e", screenScript, tty}})
	if err != nil || res.Code != 0 {
		return left, right, false
	}
	var x, y, w, h int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(res.Stdout)), "%d,%d,%d,%d", &x, &y, &w, &h); err != nil || w < 200 || h < 200 {
		return left, right, false
	}
	mid := x + w/2
	return Bounds{x, y, mid, y + h}, Bounds{mid, y, x + w, y + h}, true
}

// screenScript prints the usable area of the screen holding the Terminal
// window on the tty it's given (the first screen when none matches), as
// left,top,width,height in the top-left coordinates window bounds use.
const screenScript = `function run(argv) {
  ObjC.import("AppKit");
  var b = null;
  Application("Terminal").windows().forEach(function (w) {
    try { if (w.tabs[0].tty() === argv[0]) b = w.bounds(); } catch (e) {}
  });
  var screens = $.NSScreen.screens, ph = screens.objectAtIndex(0).frame.size.height, best = null;
  for (var i = 0; i < screens.count; i++) {
    var s = screens.objectAtIndex(i), f = s.frame, v = s.visibleFrame, top = ph - f.origin.y - f.size.height;
    var vis = [v.origin.x, ph - v.origin.y - v.size.height, v.size.width, v.size.height];
    if (best === null) best = vis;
    if (b) {
      var cx = b.x + b.width / 2, cy = b.y + b.height / 2;
      if (cx >= f.origin.x && cx < f.origin.x + f.size.width && cy >= top && cy < top + f.size.height) best = vis;
    }
  }
  return best.map(Math.round).join(",");
}`

// place moves the Terminal window on tty to b.
func (a *Actions) place(ctx context.Context, tty string, b Bounds) error {
	res, err := a.Exec.Run(ctx, exec.Spec{Name: "/usr/bin/osascript", Args: []string{"-e", placeScript(tty, b)}})
	if err == nil && res.Code != 0 {
		err = fmt.Errorf("osascript: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return err
}

func placeScript(tty string, b Bounds) string {
	return fmt.Sprintf(`tell application "Terminal" to set bounds of (first window whose tty is %q) to {%d, %d, %d, %d}`, tty, b[0], b[1], b[2], b[3])
}

// TTY is the terminal the process runs in ("" when it isn't one).
func (a *Actions) TTY(ctx context.Context) string {
	res, err := a.Exec.Run(ctx, exec.Spec{Name: "/usr/bin/tty", Stdin: os.Stdin})
	if err != nil || res.Code != 0 {
		return ""
	}
	return strings.TrimSpace(string(res.Stdout))
}

// LaunchScript is the .command file a terminal runs for a handoff.
type LaunchScript struct {
	Title, Dir, Claude, Prompt string
	Args                       []string // more claude arguments (--add-dir …), before the prompt
	Done                       string   // touched when Claude Code exits
	Bounds                     Bounds   // where the window goes (Terminal); zero = leave it
	CloseTerminal              bool     // close the Terminal window after a clean exit
}

// String is the script: go to the theme, place the window, start Claude
// Code with the prompt file's contents, then say it's done. Every value is
// single-quoted for the shell.
func (l LaunchScript) String() string {
	out := "#!/bin/sh\n" +
		"# taw-fleet handoff: " + strings.ReplaceAll(l.Title, "\n", " ") + "\n" +
		"cd " + shq(l.Dir) + " || exit 1\n"
	if l.Bounds != (Bounds{}) {
		b := l.Bounds
		out += fmt.Sprintf(`osascript -e "tell application \"Terminal\" to set bounds of (first window whose tty is \"$(tty)\") to {%d, %d, %d, %d}" >/dev/null 2>&1`+"\n", b[0], b[1], b[2], b[3])
	}
	out += shq(l.Claude)
	for _, arg := range l.Args {
		out += " " + shq(arg)
	}
	out += ` "$(cat ` + shq(l.Prompt) + `)"` + "\n" +
		"status=$?\n" +
		"touch " + shq(l.Done) + "\n"
	if l.CloseTerminal {
		out += "[ $status -eq 0 ] || exit $status\n" + closeOwnWindow
	}
	return out
}

// closeOwnWindow closes the Terminal window the script runs in. Terminal
// keeps a window open after its shell exits unless the profile says
// otherwise.
const closeOwnWindow = `tty=$(tty)` + "\n" +
	`osascript -e "tell application \"Terminal\" to close (every window whose tty is \"$tty\")" >/dev/null 2>&1 &` + "\n"

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
	// Clear the screen and its scrollback (the login banner, this command),
	// so scrolling up in the window finds nothing behind the dashboard.
	head := "#!/bin/sh\n# taw-fleet dashboard window\nprintf '\\033[H\\033[2J\\033[3J'\n"
	if !closeTerminal {
		return head + "exec " + line + "\n"
	}
	return head + line + " || exit\n" + closeOwnWindow
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
