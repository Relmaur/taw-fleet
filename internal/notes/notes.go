// Package notes keeps a site's notes: what happened to it, written by the
// owner, by Claude and by taw-fleet itself (an update, a merge, a publish…),
// so whoever looks at the site next knows. One Markdown file per site,
// ~/TAW Fleet/sites/<site>/notes.md, newest first, never in a theme or a
// repository. People may edit it by hand: an entry is a "## <date> <time> ·
// <who>" heading and the lines under it; anything else is kept as it is.
package notes

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Who wrote an entry.
const (
	You      = "you"
	Claude   = "Claude"
	TawFleet = "taw-fleet"
)

// MaxText is the longest an entry can be.
const MaxText = 4000

// layout is an entry's time in its heading (local time, to the minute).
const layout = "2006-01-02 15:04"

// Entry is one note.
type Entry struct {
	At   time.Time `json:"at"`
	By   string    `json:"by"`
	Text string    `json:"text"`
}

// File is a site's notes file under the fleet folder (the site's folder
// name, kept to letters, digits, ".", "_" and "-").
func File(fleetDir, slug string) string {
	safe := strings.Trim(unsafe.ReplaceAllString(slug, "-"), "-.")
	if safe == "" {
		safe = "site"
	}
	return filepath.Join(fleetDir, "sites", safe, "notes.md")
}

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// heading is an entry's first line.
var heading = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2} \d{2}:\d{2}) · (.+?)\s*$`)

// Read is the file's entries, newest first as written. A missing file has none.
func Read(file string) ([]Entry, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, entries := parse(string(data))
	return entries, nil
}

// parse splits the file into what comes before the first entry and the
// entries. A heading that doesn't parse is text of the entry above it.
func parse(s string) (string, []Entry) {
	lines := strings.Split(s, "\n")
	var head []string
	var out []Entry
	var cur *Entry
	var body []string
	flush := func() {
		if cur != nil {
			cur.Text = unescape(strings.TrimSpace(strings.Join(body, "\n")))
			out = append(out, *cur)
		}
	}
	for _, l := range lines {
		if m := heading.FindStringSubmatch(l); m != nil {
			if at, err := time.ParseInLocation(layout, m[1], time.Local); err == nil {
				flush()
				cur, body = &Entry{At: at, By: m[2]}, nil
				continue
			}
		}
		if cur == nil {
			head = append(head, l)
		} else {
			body = append(body, l)
		}
	}
	flush()
	return strings.Join(head, "\n"), out
}

// Add writes an entry at the top of the site's notes (after the file's
// title), making the file when there's none. Other writers (the dashboard,
// the MCP server, a CLI run) wait for each other.
func Add(file, slug string, e Entry) error {
	e.Text = strings.TrimSpace(e.Text)
	switch {
	case e.Text == "":
		return errors.New("the note is empty")
	case len(e.Text) > MaxText:
		return fmt.Errorf("the note is too long (%d characters, %d at most)", len(e.Text), MaxText)
	case strings.TrimSpace(e.By) == "" || strings.ContainsAny(e.By, "\n·"):
		return fmt.Errorf("who wrote it: %q", e.By)
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	unlock, err := lock(file + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	old, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	head, rest := split(string(old), slug)
	entry := "## " + e.At.Local().Format(layout) + " · " + strings.TrimSpace(e.By) + "\n" + escape(e.Text) + "\n"
	body := strings.TrimRight(head, "\n") + "\n\n" + entry
	if r := strings.TrimLeft(rest, "\n"); r != "" {
		body += "\n" + r
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// split is the file's title part and its entries, as written. A new file
// gets a title.
func split(s, slug string) (string, string) {
	if strings.TrimSpace(s) == "" {
		return "# " + slug + ": notes\n\n" +
			"What happened on this site, newest first: written by you, by Claude and by taw-fleet\n" +
			"(updates, merges, publishes). Edit it freely; each entry starts with a \"## <date> · <who>\" line.", ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if m := heading.FindStringSubmatch(l); m != nil {
			if _, err := time.ParseInLocation(layout, m[1], time.Local); err == nil {
				return strings.Join(lines[:i], "\n"), strings.Join(lines[i:], "\n")
			}
		}
	}
	return s, ""
}

// escape keeps a line of the text that looks like an entry's heading from
// being read as one ("\## …" shows as "## …" in Markdown).
func escape(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "## ") || strings.HasPrefix(l, `\## `) {
			lines[i] = `\` + l
		}
	}
	return strings.Join(lines, "\n")
}

func unescape(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, `\## `) || strings.HasPrefix(l, `\\## `) {
			lines[i] = l[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// lock takes an exclusive lock on path (made if missing) until unlock.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { //nolint:gosec // a file descriptor fits an int
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // a file descriptor fits an int
		_ = f.Close()
	}, nil
}

// Line is an entry on one line: its first line, cut to width runes.
func Line(e Entry, width int) string {
	first, _, more := strings.Cut(e.Text, "\n")
	r := []rune(first)
	if len(r) > width && width > 1 {
		return string(r[:width-1]) + "…"
	}
	if more {
		return first + " …"
	}
	return first
}
