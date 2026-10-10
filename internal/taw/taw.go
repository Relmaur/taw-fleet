// Package taw runs a theme's own TAW tooling, shipped by taw/core:
// `bin/taw sync`, `bin/taw inspect` and `vendor/bin/taw update`.
package taw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Timeouts. sync shallow-clones taw-theme from GitHub, composer downloads.
const (
	SyncTimeout    = 2 * time.Minute
	InspectTimeout = time.Minute
)

// ErrUmbrella refuses the umbrella's own taw-theme and taw-gutenberg: they
// are the canonical scaffolds, changed through the taw-core release flow.
var ErrUmbrella = errors.New("this is an umbrella checkout of the canonical scaffold; it changes through the taw-core release flow")

// ErrNoSync refuses sync where there's no scaffold sync (block themes).
var ErrNoSync = errors.New("this theme has no scaffold sync (`bin/taw sync` is for classic TAW themes)")

// Entry is one path in a sync report.
type Entry struct {
	Path      string     `json:"path"`
	Type      string     `json:"type"`
	Changed   bool       `json:"changed"`
	Diff      string     `json:"diff,omitempty"`
	Reconcile *Reconcile `json:"reconcile,omitempty"`
}

// Reconcile is what happens to skill folders (skills-dir entries).
type Reconcile struct {
	Overwrite []string `json:"overwrite"`
	Delete    []string `json:"delete"`
	Preserve  []string `json:"preserve"`
	Warn      []string `json:"warn"`
	Clash     []string `json:"clash"`
}

// SyncReport is `bin/taw sync --json`.
type SyncReport struct {
	TawCore struct {
		Installed string  `json:"installed"`
		Latest    string  `json:"latest"`
		Behind    bool    `json:"behind"`
		Error     *string `json:"error"`
	} `json:"taw_core"`
	Tier1   []Entry  `json:"tier1"`
	Tier2   []Entry  `json:"tier2"`
	Applied []string `json:"applied"`
	Errors  []string `json:"errors"`
	Clean   bool     `json:"clean"`
}

// Changed lists the changed paths of a tier.
func Changed(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		if e.Changed {
			out = append(out, e.Path)
		}
	}
	return out
}

// Drift is the short form kept per theme.
func (r SyncReport) Drift(at time.Time) site.Drift {
	return site.Drift{Tier1: Changed(r.Tier1), Tier2: Changed(r.Tier2), Errors: r.Errors, At: at}
}

// Guard refuses what taw-fleet must not run on a theme.
func Guard(t site.Theme, needSync bool) error {
	if !t.IsTAW {
		return errors.New("not a TAW theme")
	}
	if t.Git != nil && t.Git.Repo != nil {
		switch strings.ToLower(t.Git.Repo.FullName()) {
		case "relmaur/taw-theme", "relmaur/taw-gutenberg":
			return ErrUmbrella
		}
	}
	if needSync && (t.Kind != site.KindClassic || !t.HasBinTaw) {
		return ErrNoSync
	}
	return nil
}

// Runner runs the tooling with the site's PHP and Local's Composer.
type Runner struct {
	Exec     exec.Runner
	PHP      string // the site's PHP (else "php")
	Composer string // Local's composer.phar ("" = `composer` on PATH)
}

func (r Runner) php() string {
	if r.PHP == "" {
		return "php"
	}
	return r.PHP
}

// Sync runs `bin/taw sync --json` (with --apply when apply). Its progress
// (stderr) goes to out as it's written.
func (r Runner) Sync(ctx context.Context, t site.Theme, apply bool, out io.Writer) (SyncReport, error) {
	if err := Guard(t, true); err != nil {
		return SyncReport{}, err
	}
	args := []string{"bin/taw", "sync", "--json"}
	if apply {
		args = append(args, "--apply")
	}
	ctx, cancel := context.WithTimeout(ctx, SyncTimeout)
	defer cancel()
	res, err := r.Exec.Run(ctx, exec.Spec{Dir: t.RealPath, Name: r.php(), Args: args, Stderr: out})
	if err != nil {
		return SyncReport{}, err
	}
	rep, perr := ParseSync(res.Stdout)
	if perr != nil {
		return SyncReport{}, fmt.Errorf("bin/taw sync exited %d: %w", res.Code, perr)
	}
	return rep, nil
}

// ParseSync reads sync's JSON. Anything printed before the JSON (a PHP
// notice) is skipped.
func ParseSync(stdout []byte) (SyncReport, error) {
	var rep SyncReport
	s := string(stdout)
	i := strings.Index(s, "{")
	if i < 0 {
		return rep, fmt.Errorf("no JSON in the output: %q", tail(s, 300))
	}
	if err := json.Unmarshal([]byte(s[i:]), &rep); err != nil {
		return rep, fmt.Errorf("unreadable JSON: %w (%q)", err, tail(s, 300))
	}
	return rep, nil
}

// Inspect runs `bin/taw inspect --json` (it boots WordPress: the site must
// run) and returns the raw JSON.
func (r Runner) Inspect(ctx context.Context, s site.Site, t site.Theme) (json.RawMessage, error) {
	if err := Guard(t, false); err != nil && !errors.Is(err, ErrUmbrella) {
		return nil, err
	}
	if !t.HasBinTaw {
		return nil, errors.New("this theme has no bin/taw")
	}
	if !s.SockLive {
		return nil, fmt.Errorf("%s isn't running; start it with `taw-fleet start %s`", s.Slug, s.Slug)
	}
	ctx, cancel := context.WithTimeout(ctx, InspectTimeout)
	defer cancel()
	res, err := r.Exec.Run(ctx, exec.Spec{
		Dir: t.RealPath, Name: r.php(),
		Args: []string{"-d", "mysqli.default_socket=" + s.Socket, "-d", "pdo_mysql.default_socket=" + s.Socket, "-d", "display_errors=stderr", "bin/taw", "inspect", "--json"},
		Env:  []string{"MYSQL_UNIX_PORT=" + s.Socket},
	})
	if err != nil {
		return nil, err
	}
	out := string(res.Stdout)
	i := strings.Index(out, "{")
	if res.Code != 0 || i < 0 || !json.Valid([]byte(out[i:])) {
		return nil, fmt.Errorf("bin/taw inspect exited %d: %s", res.Code, tail(strings.TrimSpace(string(res.Stderr)+"\n"+out), 400))
	}
	return json.RawMessage(out[i:]), nil
}

// LineWriter calls fn with each complete line written to it. Safe for the
// stdout and stderr of one command at once.
type LineWriter struct {
	fn  func(string)
	mu  sync.Mutex
	buf []byte
}

// NewLineWriter returns a writer that calls fn per line.
func NewLineWriter(fn func(string)) *LineWriter { return &LineWriter{fn: fn} }

func (w *LineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexAny(string(w.buf), "\n\r")
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		if line != "" {
			w.fn(line)
		}
	}
	return len(p), nil
}

// Flush sends a last line without a newline.
func (w *LineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.fn(string(w.buf))
		w.buf = nil
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
