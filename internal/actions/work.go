package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/tools"
	"github.com/Relmaur/taw-fleet/internal/vite"
)

// SiteOp starts or stops a site through Local and waits.
type SiteOp func(ctx context.Context, op local.Op, s site.Site) (time.Duration, error)

// How long Work waits for Vite before opening the site anyway.
const (
	viteWait        = time.Minute
	viteWaitInstall = 5 * time.Minute // when it installs node_modules first
)

// WorkTask gets a theme ready to work on: starts the site (when it's
// halted), opens the theme in the editor, runs Vite in its own terminal
// window, and opens the site once Vite is up, so the first page load has
// hot reload. Steps that can't run are reported and skipped.
func (a *Actions) WorkTask(s site.Site, t site.Theme, op SiteOp) (Task, error) {
	if s.URL == "" {
		return Task{}, errors.New("this site has no domain in Local")
	}
	running := s.Status == site.StatusRunning
	if !running && op == nil {
		return Task{}, fmt.Errorf("%s isn't running: start it in Local first", s.Slug)
	}
	editor, editorErr := tools.Pick(a.Tools.Editors, a.Config.Editor)
	steps := []string{}
	if !running {
		steps = append(steps, "start "+s.Slug)
	}
	if editorErr == nil {
		steps = append(steps, "open "+editor.Name)
	}
	viteURL := vite.Running(context.Background(), t.RealPath)
	dev, devErr := vite.Detect(t.RealPath)
	switch {
	case viteURL != "":
	case devErr == nil && dev.Install:
		steps = append(steps, dev.Manager+" install, then Vite")
	case devErr == nil:
		steps = append(steps, "run Vite")
	}
	steps = append(steps, "open the site")

	return Task{
		Title: "Work on " + t.Dir,
		Ask:   fmt.Sprintf("Work on %s? This will %s.", t.Dir, joinAnd(steps)),
		Quiet: true,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			say := sayTo(out)
			var done, notes []string
			if !running {
				say("Starting %s in Local…", s.Slug)
				took, err := op(ctx, local.Start, s)
				if err != nil {
					return Summary{}, fmt.Errorf("start %s: %w", s.Slug, err)
				}
				say("%s is running (%s)", s.Slug, took.Round(time.Second))
				done = append(done, s.Slug+" running")
			}
			if editorErr != nil {
				notes = append(notes, "editor: "+editorErr.Error())
			} else if err := a.open.With(ctx, editor, t.RealPath); err != nil {
				notes = append(notes, "editor: "+err.Error())
			} else {
				say("Opened %s in %s", t.Dir, editor.Name)
				done = append(done, editor.Name)
			}

			switch {
			case viteURL != "":
				say("Vite is already running at %s", viteURL)
				done = append(done, "Vite at "+hostPort(viteURL))
			case devErr != nil:
				notes = append(notes, "Vite: "+devErr.Error())
			default:
				used, err := a.startVite(ctx, t, dev)
				if err != nil {
					notes = append(notes, "Vite: "+err.Error())
					break
				}
				say("Vite is starting in a %s window…", used)
				wait := viteWait
				if dev.Install {
					wait = viteWaitInstall
				}
				if u := a.waitVite(ctx, t.RealPath, wait); u != "" {
					say("Vite is ready at %s", u)
					done = append(done, "Vite at "+hostPort(u))
				} else {
					notes = append(notes, fmt.Sprintf("Vite didn't answer within %s; its window says why", wait))
				}
			}

			if err := a.open.URL(ctx, s.URL); err != nil {
				notes = append(notes, "browser: "+err.Error())
			} else {
				say("Opened %s", s.URL)
				done = append(done, "site open")
			}
			head := "Working on " + t.Dir + ": " + strings.Join(done, ", ")
			if len(notes) > 0 {
				head += " (" + notes[0] + ")"
			}
			return Summary{Headline: head, Lines: append(done, notes...)}, nil
		},
	}, nil
}

// StopWorkTask undoes WorkTask: stops the theme's Vite, then the site.
func (a *Actions) StopWorkTask(s site.Site, t site.Theme, op SiteOp) (Task, error) {
	running := s.Status == site.StatusRunning && op != nil
	what := "Vite"
	if running {
		what += " and " + s.Slug
	}
	return Task{
		Title: "Stop working on " + t.Dir,
		Ask:   fmt.Sprintf("Stop working on %s? This stops %s.", t.Dir, what),
		Quiet: true,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			say := sayTo(out)
			var done []string
			if vite.Running(ctx, t.RealPath) != "" {
				vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
				err := vite.Stop(vctx, a.Exec, t.RealPath)
				cancel()
				if err != nil {
					return Summary{}, err
				}
				say("Stopped Vite")
				done = append(done, "Vite")
			}
			if running {
				say("Stopping %s in Local…", s.Slug)
				if _, err := op(ctx, local.Stop, s); err != nil {
					return Summary{}, fmt.Errorf("stop %s: %w", s.Slug, err)
				}
				done = append(done, s.Slug)
			}
			if len(done) == 0 {
				return Summary{Headline: "Nothing to stop for " + t.Dir}, nil
			}
			return Summary{Headline: "Stopped " + joinAnd(done), Lines: done}, nil
		},
	}, nil
}

// startVite opens a terminal window running the theme's dev server and
// says which terminal it used.
func (a *Actions) startVite(ctx context.Context, t site.Theme, dev vite.Dev) (string, error) {
	bin, err := a.Paths.LookPath(dev.Manager)
	if err != nil {
		return "", fmt.Errorf("%s isn't on your PATH", dev.Manager)
	}
	term, fallback, err := a.scriptTerminal()
	if err != nil {
		return "", err
	}
	used := ranIn(term, fallback)
	dir := filepath.Join(a.Paths.CacheDir, "work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	script := filepath.Join(dir, safeName(t.Dir)+"-vite.command")
	body := ViteScript(t.RealPath, "Vite · "+t.Dir, filepath.Dir(bin), dev.Commands(), used == "Terminal")
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		return "", err
	}
	return used, a.open.RunScript(ctx, term, script, fallback)
}

// waitVite polls for the dev server until it answers or wait runs out.
func (a *Actions) waitVite(ctx context.Context, dir string, wait time.Duration) string {
	every := a.VitePoll
	if every <= 0 {
		every = 300 * time.Millisecond
	}
	deadline := time.After(wait)
	for {
		if u := vite.Running(ctx, dir); u != "" {
			return u
		}
		select {
		case <-ctx.Done():
			return ""
		case <-deadline:
			return ""
		case <-time.After(every):
		}
	}
}

// ViteScript is the .command file that runs a theme's dev server in its own
// window. binDir (where npm and node are) goes first on PATH: a new window's
// shell may not have the version manager's PATH. With closeTerminal the
// window closes itself when Vite stops cleanly (taw-fleet stopping it);
// after an error it stays open with the output.
func ViteScript(dir, title, binDir string, commands []string, closeTerminal bool) string {
	out := "#!/bin/sh\n" +
		"# taw-fleet: " + strings.ReplaceAll(title, "\n", " ") + "\n" +
		`printf '\033]0;%s\007' ` + shq(title) + "\n" +
		"cd " + shq(dir) + " || exit 1\n" +
		"PATH=" + shq(binDir) + `:"$PATH"; export PATH` + "\n"
	for _, c := range commands {
		out += c + " || exit\n"
	}
	if closeTerminal {
		out += closeOwnWindow
	}
	return out
}

func hostPort(u string) string {
	return strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "https://")
}

// joinAnd lists items as "a, b and c".
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// sayTo writes progress lines to out.
func sayTo(out io.Writer) func(format string, args ...any) {
	return func(format string, args ...any) { _, _ = fmt.Fprintf(out, format+"\n", args...) }
}
