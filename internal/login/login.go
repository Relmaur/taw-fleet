// Package login opens the dashboard when the owner logs in to the Mac: a
// launchd agent that runs `taw-fleet --at-login` once, at login. The
// settings' open_at_login says whether it should be there; Sync makes it so.
package login

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
)

// Label is the launchd agent's name.
const Label = "com.relmaur.taw-fleet.login"

// Flag is what the agent passes taw-fleet: open the dashboard in a window,
// if the settings still say so.
const Flag = "--at-login"

// agentPATH is the agent's PATH: launchd's own is /usr/bin:/bin:/usr/sbin:/sbin,
// without Homebrew's gh, git or claude.
const agentPATH = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// Agent is the launchd agent that opens the dashboard at login.
type Agent struct {
	Paths  paths.Paths
	Runner exec.Runner
	UID    int
	Exe    string // the taw-fleet the agent runs: Homebrew's link, so upgrades keep working
}

// Plist is the agent's file.
func (a Agent) Plist() string {
	return filepath.Join(a.Paths.Home, "Library", "LaunchAgents", Label+".plist")
}

// Log is where the agent's output goes.
func (a Agent) Log() string { return filepath.Join(a.Paths.DataDir, "login", "login.log") }

// Install writes the agent's file. launchd reads it at the next login; it
// isn't loaded now, which would open a window right away. Installing again
// replaces it.
func (a Agent) Install() error {
	if a.Exe == "" {
		return errors.New("no taw-fleet to run")
	}
	if err := os.MkdirAll(filepath.Dir(a.Log()), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.Plist()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(a.Plist(), plist(a.Exe, a.Log()), 0o644)
}

// Uninstall removes the agent's file and unloads it (loaded since the last
// login). Not installed is fine.
func (a Agent) Uninstall(ctx context.Context) error {
	if a.Runner != nil {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, _ = a.Runner.Run(ctx, exec.Spec{Name: "/bin/launchctl", Args: []string{"bootout", fmt.Sprintf("gui/%d/%s", a.UID, Label)}})
	}
	if err := os.Remove(a.Plist()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Installed reports whether the agent's file is there, and the taw-fleet it runs.
func (a Agent) Installed() (bool, string) {
	data, err := os.ReadFile(a.Plist())
	if err != nil {
		return false, ""
	}
	return true, program(data)
}

// Sync puts the agent in line with the setting: installed when on (or
// pointed again at this taw-fleet when the one it runs is gone), removed
// when off. It says whether it changed anything.
func (a Agent) Sync(ctx context.Context, on bool) (bool, error) {
	installed, prog := a.Installed()
	switch {
	case on && (!installed || !exists(prog)):
		return true, a.Install()
	case !on && installed:
		return true, a.Uninstall(ctx)
	}
	return false, nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return p != "" && err == nil
}

// plist is the agent's file: taw-fleet --at-login, once, when it's loaded
// (at login).
func plist(exe, log string) []byte {
	esc := func(s string) string {
		var e bytes.Buffer
		_ = xml.EscapeText(&e, []byte(s))
		return e.String()
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc(exe) + `</string>
		<string>` + Flag + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>` + agentPATH + `</string>
	</dict>
	<key>StandardOutPath</key>
	<string>` + esc(log) + `</string>
	<key>StandardErrorPath</key>
	<string>` + esc(log) + `</string>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`)
}

var plistProgram = regexp.MustCompile(`<key>ProgramArguments</key>\s*<array>\s*<string>([^<]*)</string>`)

// program reads back the taw-fleet plist wrote.
func program(data []byte) string {
	m := plistProgram.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&#39;", "'", "&#34;", `"`).Replace(string(m[1]))
}
