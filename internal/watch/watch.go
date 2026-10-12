// Package watch finds what changed in the fleet since the last check: a
// failed deploy, CI failing on a default branch, a production site down or
// unverified, new client comments, an update ready to merge. Each change is
// an Event, told once; the State remembers what was already told.
package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Event kinds: stable codes, like doctor's.
const (
	DeployFailed         = "deploy.failed"
	CIFailed             = "ci.failed"
	UpdateReady          = "pr.ready"
	ProductionDown       = "production.down"
	ProductionUp         = "production.up"
	ProductionUnverified = "production.unverified"
	CommentsNew          = "comments.new"
	ReportReady          = "report.ready" // last month's report was made (taw-fleet's cli makes it)
)

// DownAfter is how many checks in a row a production site must fail before
// it's told as down: one failed check is often a blip.
const DownAfter = 2

// UpdateBranch is the prefix of the branches taw update makes.
const UpdateBranch = "taw/update-"

// keepDays is how long the uptime history is kept.
const keepDays = 400

// keepRecent is how many events State.Recent keeps.
const keepRecent = 50

// Event is one change worth telling.
type Event struct {
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	Site  string    `json:"site"`            // the site's folder
	Name  string    `json:"name"`            // the site's name
	Theme string    `json:"theme,omitempty"` // the theme folder, when it's about one
	Title string    `json:"title"`           // "Deploy failed"
	Body  string    `json:"body"`            // one or two sentences
	URL   string    `json:"url,omitempty"`   // where to look
}

// Loud reports whether the event is bad news (a sound with the notification).
func (e Event) Loud() bool {
	return e.Kind == DeployFailed || e.Kind == ProductionDown || e.Kind == CIFailed
}

// State is what the last check saw, and what was told.
type State struct {
	Checked time.Time            `json:"checked_at,omitzero"`
	Sites   map[string]SiteState `json:"sites"`
	// Uptime is each production site's checks per day ("2026-10-12"): the
	// monthly report's uptime.
	Uptime map[string]map[string]Day `json:"uptime,omitempty"`
	Recent []Event                   `json:"recent,omitempty"` // newest first
	// Problems are what the last check couldn't ask (GitHub, BugSmash): the
	// log repeats them only when they change.
	Problems []string `json:"problems,omitempty"`
}

// SiteState is one site as the last check saw it.
type SiteState struct {
	Themes map[string]ThemeState `json:"themes,omitempty"` // by theme folder; present = seen

	ProdSeen   bool      `json:"prod_seen,omitempty"`
	DownChecks int       `json:"down_checks,omitempty"` // failed checks in a row
	DownSince  time.Time `json:"down_since,omitzero"`
	Unverified bool      `json:"unverified,omitempty"`

	CommentsSeen bool     `json:"comments_seen,omitempty"`
	Comments     []string `json:"comments,omitempty"` // open comment ids
}

// ThemeState is a theme's repository as the last check saw it.
type ThemeState struct {
	FailedDeploy string   `json:"failed_deploy,omitempty"` // the failed run's URL
	FailedCI     string   `json:"failed_ci,omitempty"`     // the default branch's commit whose CI failed
	Ready        []string `json:"ready,omitempty"`         // update PRs ready to merge: "12@sha"
}

// Day is one day of a production site's checks.
type Day struct {
	Checks int `json:"checks"`
	Up     int `json:"up"`
}

// File is where the state lives.
func File(p paths.Paths) string { return filepath.Join(p.DataDir, "watch", "state.json") }

// Load reads the state. No file is an empty state: the first check only
// notes what it sees.
func Load(p paths.Paths) (State, error) {
	var st State
	data, err := os.ReadFile(File(p))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("%s: %w", File(p), err)
	}
	return st, nil
}

// Save writes the state atomically (a check from launchd and one by hand
// may end at the same time).
func Save(p paths.Paths, st State) error {
	f := File(p)
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f), ".state-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), f)
}

// Check compares the sites (with GitHub, production and comments applied)
// against the last state, and returns the new state and what changed. Sites,
// themes or checks seen for the first time only set the baseline: nothing is
// told about them. A check that failed (GitHub, the companion's key, BugSmash)
// keeps what the last one saw, so nothing is told twice after an outage.
func Check(prev State, sites []site.Site, now time.Time) (State, []Event) {
	next := State{Checked: now, Sites: map[string]SiteState{}, Uptime: prev.Uptime}
	var events []Event
	for _, s := range sites {
		ps, seen := prev.Sites[s.Slug]
		ns := SiteState{Themes: map[string]ThemeState{}}
		add := func(e Event) {
			e.At, e.Site, e.Name = now, s.Slug, siteName(s)
			if seen {
				events = append(events, e)
			}
		}

		for _, t := range s.TAWThemes() {
			pt, themeSeen := ps.Themes[t.Dir]
			gh := t.GitHub
			if gh == nil || gh.Error != "" {
				if themeSeen {
					ns.Themes[t.Dir] = pt
				}
				continue
			}
			nt := ThemeState{}
			tell := func(e Event) {
				if themeSeen {
					e.Theme = t.Dir
					add(e)
				}
			}
			if d := gh.Deploy; d != nil && d.Failed != nil {
				nt.FailedDeploy = d.Failed.URL
				if pt.FailedDeploy != d.Failed.URL {
					tell(Event{Kind: DeployFailed, Title: "Deploy failed",
						Body: fmt.Sprintf("%s: the %s run for %s failed. Production still has the last good deploy.", siteName(s), workflow(d), short(d.Failed.SHA)),
						URL:  d.Failed.URL})
				}
			}
			if gh.HeadCI == site.ChecksFailing && gh.Head != "" {
				nt.FailedCI = gh.Head
				if pt.FailedCI != gh.Head {
					tell(Event{Kind: CIFailed, Title: "CI failed on " + branch(gh),
						Body: fmt.Sprintf("%s: %s on %s didn't pass its checks.", siteName(s), short(gh.Head), branch(gh)),
						URL:  "https://github.com/" + gh.Repo + "/commit/" + gh.Head})
				}
			}
			for _, pr := range gh.PRs {
				if !strings.HasPrefix(pr.Branch, UpdateBranch) || !pr.Ready() {
					continue
				}
				key := fmt.Sprintf("%d@%s", pr.Number, pr.HeadSHA)
				nt.Ready = append(nt.Ready, key)
				if !slices.Contains(pt.Ready, key) {
					tell(Event{Kind: UpdateReady, Title: "Update ready to merge",
						Body: fmt.Sprintf("%s #%d: %s. Its checks passed; M in the dashboard merges it.", siteName(s), pr.Number, pr.Title),
						URL:  pr.URL})
				}
			}
			ns.Themes[t.Dir] = nt
		}

		if p := s.Production; p != nil && p.ErrorKind != "no-key" {
			ns.ProdSeen = true
			host := hostOf(p.URL)
			if p.Reachable {
				if ps.DownChecks >= DownAfter {
					body := host + " answers again."
					if !ps.DownSince.IsZero() {
						body = fmt.Sprintf("%s answers again, after %s.", host, ago(now.Sub(ps.DownSince)))
					}
					add(Event{Kind: ProductionUp, Title: "Production is back", Body: body, URL: p.URL})
				}
			} else {
				ns.DownChecks, ns.DownSince = ps.DownChecks+1, ps.DownSince
				if ns.DownSince.IsZero() {
					ns.DownSince = p.CheckedAt
					if ns.DownSince.IsZero() {
						ns.DownSince = now
					}
				}
				if ns.DownChecks == DownAfter {
					add(Event{Kind: ProductionDown, Title: "Production is down",
						Body: fmt.Sprintf("%s hasn't answered for %s: %s", host, ago(now.Sub(ns.DownSince)), firstLine(p.Error)),
						URL:  p.URL})
				}
			}
			ns.Unverified = p.Reachable && !p.Verified
			if ns.Unverified && !ps.Unverified && ps.ProdSeen {
				add(Event{Kind: ProductionUnverified, Title: "Production not verified",
					Body: fmt.Sprintf("%s answered without its pinned key's signature: %s", host, firstLine(p.Error)),
					URL:  p.URL})
			}
			next.Uptime = record(next.Uptime, s.Slug, now, p.Reachable)
		} else {
			ns.ProdSeen, ns.DownChecks, ns.DownSince, ns.Unverified = ps.ProdSeen, ps.DownChecks, ps.DownSince, ps.Unverified
		}

		if f := s.Feedback; f != nil && f.Error == "" {
			ns.CommentsSeen = true
			var fresh []site.Comment
			for _, c := range f.Comments {
				ns.Comments = append(ns.Comments, c.ID)
				if !slices.Contains(ps.Comments, c.ID) {
					fresh = append(fresh, c)
				}
			}
			if len(fresh) > 0 && ps.CommentsSeen {
				add(commentsEvent(s, f, fresh))
			}
		} else {
			ns.CommentsSeen, ns.Comments = ps.CommentsSeen, ps.Comments
		}

		next.Sites[s.Slug] = ns
	}
	next.Uptime = prune(next.Uptime, now)
	sort.SliceStable(events, func(i, j int) bool { return events[i].Loud() && !events[j].Loud() })
	next.Recent = append(slices.Clone(events), prev.Recent...)
	if len(next.Recent) > keepRecent {
		next.Recent = next.Recent[:keepRecent]
	}
	return next, events
}

func commentsEvent(s site.Site, f *site.Feedback, fresh []site.Comment) Event {
	title := "1 new comment"
	if len(fresh) > 1 {
		title = fmt.Sprintf("%d new comments", len(fresh))
	}
	c := fresh[0] // the newest
	who := c.Author
	if who == "" {
		who = "A reviewer"
	}
	body := fmt.Sprintf("%s, #%d: %s", who, c.Number, clip(firstLine(c.Text), 90))
	if len(fresh) > 1 {
		body += fmt.Sprintf(" (and %d more)", len(fresh)-1)
	}
	return Event{Kind: CommentsNew, Title: title, Body: siteName(s) + ": " + body, URL: f.URL}
}

// UptimeSince is UptimeBetween and the first day in it with checks.
func (st State) UptimeSince(slug string, from, to time.Time) (share float64, checks int, since time.Time) {
	share, checks = st.UptimeBetween(slug, from, to)
	for day := range st.Uptime[slug] {
		t, err := time.ParseInLocation(time.DateOnly, day, from.Location())
		if err != nil || t.Before(from) || !t.Before(to) {
			continue
		}
		if since.IsZero() || t.Before(since) {
			since = t
		}
	}
	return share, checks, since
}

// UptimeBetween is a site's share of checks that found production answering, over
// the days from (inclusive) to to (exclusive), and how many checks that was.
func (st State) UptimeBetween(slug string, from, to time.Time) (share float64, checks int) {
	up := 0
	for day, d := range st.Uptime[slug] {
		t, err := time.ParseInLocation(time.DateOnly, day, from.Location())
		if err != nil || t.Before(from) || !t.Before(to) {
			continue
		}
		checks += d.Checks
		up += d.Up
	}
	if checks == 0 {
		return 0, 0
	}
	return float64(up) / float64(checks), checks
}

func record(u map[string]map[string]Day, slug string, now time.Time, up bool) map[string]map[string]Day {
	if u == nil {
		u = map[string]map[string]Day{}
	}
	if u[slug] == nil {
		u[slug] = map[string]Day{}
	}
	day := now.Format(time.DateOnly)
	d := u[slug][day]
	d.Checks++
	if up {
		d.Up++
	}
	u[slug][day] = d
	return u
}

func prune(u map[string]map[string]Day, now time.Time) map[string]map[string]Day {
	cut := now.AddDate(0, 0, -keepDays).Format(time.DateOnly)
	for _, days := range u {
		for day := range days {
			if day < cut {
				delete(days, day)
			}
		}
	}
	return u
}

func siteName(s site.Site) string {
	if s.Name != "" {
		return s.Name
	}
	return s.Slug
}

func workflow(d *site.Deploy) string {
	if d.Workflow != "" {
		return d.Workflow
	}
	return "deploy"
}

func branch(gh *site.RepoState) string {
	if gh.Default != "" {
		return gh.Default
	}
	return "the default branch"
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func hostOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	return strings.TrimSuffix(u, "/")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "no answer"
	}
	return s
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// ago is a duration in words: "10 minutes", "2 hours".
func ago(d time.Duration) string {
	switch m := int(d.Round(time.Minute).Minutes()); {
	case m < 1:
		return "a moment"
	case m == 1:
		return "1 minute"
	case m < 120:
		return fmt.Sprintf("%d minutes", m)
	case m < 48*60:
		return fmt.Sprintf("%d hours", m/60)
	default:
		return fmt.Sprintf("%d days", m/(24*60))
	}
}
