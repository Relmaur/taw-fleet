package watch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
)

// Notifier shows events as macOS notifications. With terminal-notifier
// installed (Homebrew; the cask installs it), clicking one opens the
// dashboard on its site; without it, osascript shows the same notification,
// and clicking it opens Script Editor.
type Notifier struct {
	Runner   exec.Runner
	LookPath func(string) (string, error)
	// Open is the command a click runs for a site's event ("" = none):
	// taw-fleet --site <folder>. nil = clicking opens the event's URL.
	Open func(site string) []string
}

// Clickable reports whether clicking a notification opens the dashboard
// (terminal-notifier is installed).
func (n Notifier) Clickable() bool {
	_, err := n.notifier()
	return err == nil
}

func (n Notifier) notifier() (string, error) {
	if n.LookPath == nil {
		return "", fmt.Errorf("terminal-notifier: not found")
	}
	for _, p := range []string{"terminal-notifier", "/opt/homebrew/bin/terminal-notifier", "/usr/local/bin/terminal-notifier"} {
		if path, err := n.LookPath(p); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("terminal-notifier: not found")
}

// ErrNotAllowed means macOS doesn't let terminal-notifier show
// notifications: the event went out as a plain notification instead.
var ErrNotAllowed = errors.New("notifications are off for terminal-notifier, so this one can't open the dashboard when clicked: turn them on in System Settings → Notifications → terminal-notifier")

// Send shows one event. When macOS refuses terminal-notifier, it's sent
// with osascript instead and Send returns ErrNotAllowed.
func (n Notifier) Send(ctx context.Context, e Event) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := n.send(ctx, e, true)
	if errors.Is(err, ErrNotAllowed) {
		if err := n.send(ctx, e, false); err != nil {
			return err
		}
		return ErrNotAllowed
	}
	return err
}

func (n Notifier) send(ctx context.Context, e Event, clickable bool) error {
	title, subtitle, message := "taw-fleet", e.Title, e.Body
	var spec exec.Spec
	path, err := n.notifier()
	if clickable && err == nil {
		args := []string{"-title", title, "-subtitle", subtitle, "-message", escapeFirst(message),
			"-group", "taw-fleet." + e.Site + "." + e.Kind}
		if e.Loud() {
			args = append(args, "-sound", "default")
		}
		if n.Open != nil {
			args = append(args, "-execute", shellLine(n.Open(e.Site)))
		} else if e.URL != "" {
			args = append(args, "-open", e.URL)
		}
		spec = exec.Spec{Name: path, Args: args}
	} else {
		script := []string{"on run argv", "display notification (item 1 of argv) with title (item 2 of argv) subtitle (item 3 of argv)"}
		if e.Loud() {
			script[1] += ` sound name "default"`
		}
		args := []string{}
		for _, l := range append(script, "end run") {
			args = append(args, "-e", l)
		}
		spec = exec.Spec{Name: "/usr/bin/osascript", Args: append(args, message, title, subtitle)}
	}
	res, err := n.Runner.Run(ctx, spec)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		msg := strings.TrimSpace(string(res.Stderr) + string(res.Stdout))
		if strings.Contains(msg, "not allowed") || strings.Contains(msg, "denied") {
			return ErrNotAllowed
		}
		return fmt.Errorf("%s exited %d: %s", spec.Name, res.Code, msg)
	}
	return nil
}

// escapeFirst keeps terminal-notifier from reading a message that starts
// with "[" or "-" as something else.
func escapeFirst(s string) string {
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "-") {
		return `\` + s
	}
	return s
}

// shellLine is a command line for terminal-notifier's -execute (which runs it
// with sh): every word single-quoted.
func shellLine(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}
