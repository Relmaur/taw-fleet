package watch

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
)

// Label is the launchd agent's name.
const Label = "com.relmaur.taw-fleet.watch"

// Every is how often the agent checks, unless install says otherwise.
const Every = 5 * time.Minute

// agentPATH is the agent's PATH: launchd's own is /usr/bin:/bin:/usr/sbin:/sbin,
// without Homebrew's gh, git or terminal-notifier.
const agentPATH = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// Agent is the launchd agent that runs `taw-fleet watch --once`.
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
func (a Agent) Log() string { return filepath.Join(a.Paths.DataDir, "watch", "watch.log") }

func (a Agent) target() string { return fmt.Sprintf("gui/%d", a.UID) }

// Install writes the agent and loads it: the first check runs now, then one
// every `every`. Installing again replaces it.
func (a Agent) Install(ctx context.Context, every time.Duration) error {
	if every < time.Minute {
		return errors.New("checking more often than once a minute isn't allowed")
	}
	if a.Exe == "" {
		return errors.New("no taw-fleet to run")
	}
	if err := os.MkdirAll(filepath.Dir(a.Log()), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.Plist()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(a.Plist(), plist(a.Exe, a.Log(), every), 0o644); err != nil {
		return err
	}
	_, _ = a.launchctl(ctx, "bootout", a.target()+"/"+Label) // not loaded is fine
	if out, err := a.launchctl(ctx, "bootstrap", a.target(), a.Plist()); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w%s", err, out)
	}
	return nil
}

// Uninstall unloads the agent and removes its file. Not installed is fine.
func (a Agent) Uninstall(ctx context.Context) error {
	_, _ = a.launchctl(ctx, "bootout", a.target()+"/"+Label)
	if err := os.Remove(a.Plist()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// AgentStatus is what launchd says about the agent.
type AgentStatus struct {
	Installed bool          `json:"installed"` // its file is there
	Loaded    bool          `json:"loaded"`    // launchd runs it
	Every     time.Duration `json:"-"`
	Program   string        `json:"program,omitempty"`
	LastExit  string        `json:"last_exit,omitempty"`
}

// Status reads the agent's file and asks launchd about it.
func (a Agent) Status(ctx context.Context) AgentStatus {
	var st AgentStatus
	data, err := os.ReadFile(a.Plist())
	if err != nil {
		return st
	}
	st.Installed = true
	st.Program, st.Every = readPlist(data)
	out, err := a.launchctl(ctx, "print", a.target()+"/"+Label)
	if err == nil {
		st.Loaded = true
		if m := lastExit.FindStringSubmatch(out); m != nil {
			st.LastExit = strings.TrimSpace(m[1])
		}
	}
	return st
}

var lastExit = regexp.MustCompile(`(?m)last exit code = (.+)$`)

func (a Agent) launchctl(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := a.Runner.Run(ctx, exec.Spec{Name: "/bin/launchctl", Args: args})
	if err != nil {
		return "", err
	}
	out := string(res.Stdout) + string(res.Stderr)
	if res.Code != 0 {
		return "\n" + strings.TrimSpace(out), fmt.Errorf("exit %d", res.Code)
	}
	return out, nil
}

// plist is the agent's file: taw-fleet watch --once every `every`, and once
// when it's loaded (at install and at login).
func plist(exe, log string, every time.Duration) []byte {
	var b bytes.Buffer
	esc := func(s string) string {
		var e bytes.Buffer
		_ = xml.EscapeText(&e, []byte(s))
		return e.String()
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc(exe) + `</string>
		<string>watch</string>
		<string>--once</string>
	</array>
	<key>StartInterval</key>
	<integer>` + strconv.Itoa(int(every.Seconds())) + `</integer>
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
	<string>Background</string>
</dict>
</plist>
`)
	return b.Bytes()
}

var (
	plistProgram  = regexp.MustCompile(`<key>ProgramArguments</key>\s*<array>\s*<string>([^<]*)</string>`)
	plistInterval = regexp.MustCompile(`<key>StartInterval</key>\s*<integer>(\d+)</integer>`)
)

// readPlist reads back what plist wrote: the program and the interval.
func readPlist(data []byte) (string, time.Duration) {
	var prog string
	var every time.Duration
	if m := plistProgram.FindSubmatch(data); m != nil {
		prog = unescape(string(m[1]))
	}
	if m := plistInterval.FindSubmatch(data); m != nil {
		n, _ := strconv.Atoi(string(m[1]))
		every = time.Duration(n) * time.Second
	}
	return prog, every
}

func unescape(s string) string {
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&#39;", "'", "&#34;", `"`).Replace(s)
}
