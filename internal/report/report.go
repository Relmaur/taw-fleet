// Package report makes a site's monthly report for its client: whether the
// site was up, what changed and went live, the client's review comments,
// how fast it is, and its security and maintenance. Markdown and HTML, for
// the owner to read and send; nothing is sent from here.
package report

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/pagespeed"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Data is one month of one site, everything the report says.
type Data struct {
	Site       string    `json:"site"`
	Name       string    `json:"name"`
	Production string    `json:"production_url,omitempty"`
	Month      string    `json:"month"` // "2026-10"
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	Made       time.Time `json:"made_at"`
	SoFar      bool      `json:"so_far"` // the month isn't over yet
	PreparedBy string    `json:"prepared_by,omitempty"`
	Lang       string    `json:"language"`

	Uptime     *Uptime `json:"uptime,omitempty"`
	UptimeNote string  `json:"uptime_note,omitempty"`

	Updates     []Change           `json:"updates"`     // framework updates (taw/update-* pull requests)
	Maintenance []Change           `json:"maintenance"` // framework and housekeeping work (taw/core, scaffold, companion, CI, ignores): counted, not listed
	Changes     []Change           `json:"changes"`     // every other merged pull request
	Deploys     []github.DeployRun `json:"deploys"`
	HasDeploy   bool               `json:"has_deploy_workflow"`
	ChangesNote string             `json:"changes_note,omitempty"`
	Comments    *Comments          `json:"comments,omitempty"`
	CommentNote string             `json:"comments_note,omitempty"`
	Speed       []pagespeed.Result `json:"speed,omitempty"`
	SpeedAt     time.Time          `json:"speed_measured_at,omitzero"`
	SpeedNote   string             `json:"speed_note,omitempty"`
	Health      *Health            `json:"health,omitempty"`
	HealthNote  string             `json:"health_note,omitempty"`
	byRepo      map[string]github.Month
}

// Uptime is the share of watch's checks that found the site answering.
type Uptime struct {
	Share  float64   `json:"share"`
	Checks int       `json:"checks"`
	Since  time.Time `json:"since"` // the first day with checks in the month
}

// Change is a merged pull request.
type Change struct {
	Title  string    `json:"title"`
	Number int       `json:"number"`
	URL    string    `json:"url"`
	Repo   string    `json:"repo"`
	At     time.Time `json:"at"`
	From   string    `json:"from,omitempty"` // an update's taw/core versions
	To     string    `json:"to,omitempty"`
}

// Comments are the client's review comments made in the month.
type Comments struct {
	Received int `json:"received"`
	Resolved int `json:"resolved"`
}

// Health is the live site now, through its companion.
type Health struct {
	Verified  bool     `json:"verified"`
	WP        string   `json:"wordpress"`
	PHP       string   `json:"php"`
	Core      string   `json:"taw_core"`
	Plugins   int      `json:"plugins"`
	Updates   []string `json:"plugin_updates"`
	Vulns     int      `json:"vulnerabilities"`
	Worst     string   `json:"worst_severity,omitempty"`
	Scanner   string   `json:"scanner,omitempty"`
	CheckedAt string   `json:"-"`
}

// Target is the site a report is about.
type Target struct {
	Site       site.Site
	Production string   // its production URL
	Project    string   // its BugSmash project id ("" = none)
	Repos      []string // its themes' repositories (owner/name)
	PreparedBy string
	Lang       string // "en" or "es"
}

// Sources are where the numbers come from. A nil source is a section that
// says it wasn't measured.
type Sources struct {
	GitHub   func(ctx context.Context, repo string, from, to time.Time) (github.Month, error)
	Comments func(ctx context.Context, project string, from, to time.Time) (received, resolved int, err error)
	Live     func(ctx context.Context) (*site.Production, error)
	Uptime   func(slug string, from, to time.Time) (share float64, checks int, since time.Time)
	Speed    func(ctx context.Context, url, strategy string) (pagespeed.Result, error)
	Now      func() time.Time
}

// MonthOf parses "2026-10" (or "" for this month) into its first instant and
// the next month's, in loc.
func MonthOf(month string, now time.Time) (time.Time, time.Time, error) {
	if month == "" {
		month = now.Format("2006-01")
	}
	from, err := time.ParseInLocation("2006-01", month, now.Location())
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("a month is written 2026-10")
	}
	if from.After(now) {
		return time.Time{}, time.Time{}, errors.New("that month hasn't started")
	}
	return from, from.AddDate(0, 1, 0), nil
}

// housekeeping is a pull request about the framework or the repository
// rather than the site: the report counts them, it doesn't list them.
var housekeeping = regexp.MustCompile(`(?i)taw/core|taw core|framework|scaffold|companion|\bci\b|workflow|ignore|phpunit|composer|dependabot|\bbump\b|\.sync|lint|phpstan`)

// clientName is Local's site name without the " - TAW" it often ends with.
func clientName(name string) string {
	for _, suffix := range []string{" - TAW", " – TAW", " — TAW", " (TAW)"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return strings.TrimSpace(name)
}

// updateTitle reads an update pull request's versions: "Update taw/core 1.93.1 → 1.93.3".
var updateTitle = regexp.MustCompile(`taw/core\s+v?([\d.]+)\s*(?:→|->)\s*v?([\d.]+)`)

// Gather asks every source about the month. Sections that can't be
// answered say why (a Note); nothing fails the report.
func Gather(ctx context.Context, src Sources, t Target, from, to time.Time) Data {
	now := time.Now()
	if src.Now != nil {
		now = src.Now()
	}
	lang := t.Lang
	if lang != "es" {
		lang = "en"
	}
	d := Data{Site: t.Site.Slug, Name: clientName(t.Site.Name), Production: t.Production, Month: from.Format("2006-01"), From: from, To: to,
		Made: now, SoFar: now.Before(to), PreparedBy: t.PreparedBy, Lang: lang, Updates: []Change{}, Maintenance: []Change{}, Changes: []Change{}, Deploys: []github.DeployRun{}}
	if d.Name == "" {
		d.Name = t.Site.Slug
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	run := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}

	// What changed and went live.
	switch {
	case src.GitHub == nil:
		d.ChangesNote = "no GitHub token"
	case len(t.Repos) == 0:
		d.ChangesNote = "no GitHub repository"
	default:
		for _, repo := range t.Repos {
			run(func() {
				m, err := src.GitHub(ctx, repo, from, to)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					d.ChangesNote = repo + ": " + err.Error()
					return
				}
				if d.byRepo == nil {
					d.byRepo = map[string]github.Month{}
				}
				d.byRepo[repo] = m
			})
		}
	}
	// Comments.
	switch {
	case t.Project == "":
		d.CommentNote = "no BugSmash project"
	case src.Comments == nil:
		d.CommentNote = "no BugSmash key"
	default:
		run(func() {
			rec, res, err := src.Comments(ctx, t.Project, from, to)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				d.CommentNote = err.Error()
				return
			}
			d.Comments = &Comments{Received: rec, Resolved: res}
		})
	}
	// Health now (a report of a past month still shows today's state; it says when).
	if src.Live != nil && t.Production != "" {
		run(func() {
			p, err := src.Live(ctx)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				d.HealthNote = err.Error()
			case p == nil:
				d.HealthNote = "not checked"
			case !p.Reachable:
				d.HealthNote = "the site's companion didn't answer: " + p.Error
			default:
				d.Health = &Health{Verified: p.Verified, WP: p.WP, PHP: p.PHP, Core: p.TawCore, Plugins: p.Plugins,
					Updates: append([]string{}, p.PluginUpdates...), Vulns: len(p.Vulns), Worst: p.WorstSeverity, Scanner: p.Scanner}
			}
		})
	} else {
		d.HealthNote = "no production check set up"
	}
	// Speed: measured when the report is made, so only for this month or
	// the one that just ended.
	recent := !now.After(to.AddDate(0, 0, 7))
	switch {
	case src.Speed == nil:
		d.SpeedNote = "not measured"
	case t.Production == "":
		d.SpeedNote = "no production URL"
	case !recent:
		d.SpeedNote = "measured only in the report made at the month's end"
	default:
		for _, strategy := range []string{"mobile", "desktop"} {
			run(func() {
				r, err := src.Speed(ctx, t.Production, strategy)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					d.SpeedNote = err.Error()
					return
				}
				d.Speed = append(d.Speed, r)
			})
		}
		d.SpeedAt = now
	}
	wg.Wait()
	sort.Slice(d.Speed, func(i, j int) bool { return d.Speed[i].Strategy == "mobile" && d.Speed[j].Strategy != "mobile" })
	if len(d.Speed) == 0 {
		d.SpeedAt = time.Time{}
	}

	for _, repo := range t.Repos {
		m, ok := d.byRepo[repo]
		if !ok {
			continue
		}
		d.HasDeploy = d.HasDeploy || m.HasDeploy
		d.Deploys = append(d.Deploys, m.Deploys...)
		for _, pr := range m.Merged {
			c := Change{Title: pr.Title, Number: pr.Number, URL: pr.URL, Repo: repo, At: pr.At}
			if strings.HasPrefix(pr.Branch, "taw/update-") {
				if v := updateTitle.FindStringSubmatch(pr.Title); v != nil {
					c.From, c.To = v[1], v[2]
				}
				d.Updates = append(d.Updates, c)
				continue
			}
			if housekeeping.MatchString(pr.Title) || strings.HasPrefix(pr.Branch, "dependabot/") {
				d.Maintenance = append(d.Maintenance, c)
				continue
			}
			d.Changes = append(d.Changes, c)
		}
	}
	sort.Slice(d.Deploys, func(i, j int) bool { return d.Deploys[i].At.Before(d.Deploys[j].At) })

	// Uptime from watch's history.
	if src.Uptime != nil && t.Production != "" {
		share, checks, since := src.Uptime(t.Site.Slug, from, to)
		if checks > 0 {
			d.Uptime = &Uptime{Share: share, Checks: checks, Since: since}
		} else {
			d.UptimeNote = "not measured (taw-fleet watch wasn't running)"
		}
	} else {
		d.UptimeNote = "not measured"
	}
	return d
}

// File is where a month's report goes: <dir>/<site>/<2026-10>.<ext>.
func File(dir, slug, month, ext string) string {
	return filepath.Join(dir, slug, month+"."+ext)
}

// Save writes the Markdown and the HTML, and returns their paths.
func Save(dir string, d Data) (md, html string, err error) {
	md, html = File(dir, d.Site, d.Month, "md"), File(dir, d.Site, d.Month, "html")
	if err := os.MkdirAll(filepath.Dir(md), 0o755); err != nil {
		return md, html, err
	}
	if err := os.WriteFile(md, []byte(Markdown(d)), 0o644); err != nil {
		return md, html, err
	}
	h, err := HTML(d)
	if err != nil {
		return md, html, err
	}
	return md, html, os.WriteFile(html, []byte(h), 0o644)
}

// Saved is a report on disk.
type Saved struct {
	Month string    `json:"month"` // "2026-10"
	HTML  string    `json:"html"`
	MD    string    `json:"markdown"`
	Made  time.Time `json:"made_at"`
}

// List is a site's saved reports, newest month first.
func List(dir, slug string) []Saved {
	entries, err := os.ReadDir(filepath.Join(dir, slug))
	if err != nil {
		return nil
	}
	var out []Saved
	for _, e := range entries {
		month, ok := strings.CutSuffix(e.Name(), ".html")
		if !ok || len(month) != 7 {
			continue
		}
		s := Saved{Month: month, HTML: File(dir, slug, month, "html"), MD: File(dir, slug, month, "md")}
		if fi, err := e.Info(); err == nil {
			s.Made = fi.ModTime()
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Month > out[j].Month })
	return out
}
