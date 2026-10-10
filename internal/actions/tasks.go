package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/taw"
)

// Task is a longer job with live output: a scaffold sync or a taw/core
// update. Run writes progress to out and returns a summary.
type Task struct {
	Title  string      // "Sync check: chcapital"
	Writes bool        // changes files in the theme (asks first)
	Ask    string      // the question to ask first, when it isn't Writes' generic one
	Quiet  bool        // the dashboard stays on the table (the footer shows progress)
	Steps  []taw.Step  // a checklist the output follows (taw.ParseProgress); nil = a plain log
	Info   []string    // lines under the title (what the task is about)
	Target string      // the site ID and theme dir it works on ("id/dir"), for the table's row
	Batch  []BatchItem // an update-all: its themes, in order (their progress follows BatchStart lines)
	Run    func(ctx context.Context, out io.Writer) (Summary, error)
}

// Summary is what a task found or did, for people.
type Summary struct {
	Headline string   // one line, for the footer
	Lines    []string // the details
	Report   any      // the raw result (sync report, update result)
	Secret   string   // something to copy once and not keep (a new site's password)
	Failed   bool     // it ran, and what it did failed or was refused (Lines say why)
}

func (a *Actions) taw(s site.Site) taw.Runner {
	r := taw.Runner{Exec: a.Exec}
	if php, ok := local.PHPBinary(a.Paths, s.PHPVersion); ok {
		r.PHP = php
	}
	if c, ok := local.ComposerPhar(a.Paths); ok {
		r.Composer = c
	}
	return r
}

// SyncTask checks (or with apply, writes) the theme's framework files
// against the taw-theme scaffold with `bin/taw sync`. The result is cached
// for the dashboard's SYNC column.
func (a *Actions) SyncTask(s site.Site, t site.Theme, apply bool) (Task, error) {
	if err := taw.Guard(t, true); err != nil {
		return Task{}, err
	}
	title := "Sync check: " + t.Dir
	if apply {
		title = "Sync (apply Tier 1): " + t.Dir
	}
	r := a.taw(s)
	return Task{Title: title, Writes: apply, Run: func(ctx context.Context, out io.Writer) (Summary, error) {
		rep, err := r.Sync(ctx, t, apply, out)
		if err != nil {
			return Summary{}, err
		}
		if !apply || len(rep.Applied) > 0 {
			// After --apply the report still lists what *was* different;
			// a fresh check is what the column should show next time.
			d := rep.Drift(a.now())
			if apply {
				d.Tier1 = nil
			}
			taw.SaveDrift(a.Paths.CacheDir, s.Slug, t.Dir, d)
		}
		return summarizeSync(t, rep, apply), nil
	}}, nil
}

func summarizeSync(t site.Theme, rep taw.SyncReport, apply bool) Summary {
	t1, t2 := taw.Changed(rep.Tier1), taw.Changed(rep.Tier2)
	var lines []string
	core := "taw/core " + strings.TrimPrefix(rep.TawCore.Installed, "v")
	if rep.TawCore.Behind {
		core += ", newest " + strings.TrimPrefix(rep.TawCore.Latest, "v") + " (update with u, or hand it off with h)"
	} else if rep.TawCore.Latest != "" {
		core += " (newest)"
	}
	lines = append(lines, core)
	switch {
	case apply && len(rep.Applied) > 0:
		lines = append(lines, fmt.Sprintf("Tier 1: applied %d: %s", len(rep.Applied), strings.Join(rep.Applied, ", ")))
	case len(t1) > 0:
		lines = append(lines, fmt.Sprintf("Tier 1: %d framework %s: %s (S or `sync --apply` writes them; the skill applies Tier 1 without asking)", len(t1), plural(len(t1), "path differs", "paths differ"), strings.Join(t1, ", ")))
	default:
		lines = append(lines, "Tier 1: framework files match the scaffold")
	}
	for _, e := range rep.Tier1 {
		if e.Reconcile != nil && len(e.Reconcile.Warn) > 0 {
			lines = append(lines, fmt.Sprintf("  %s: unmarked skills %s (add `owner: site` to keep them)", e.Path, strings.Join(e.Reconcile.Warn, ", ")))
		}
	}
	if len(t2) > 0 {
		note := ""
		if onlyManifests(t2) {
			note = " (usually just this site's own dependencies)"
		}
		lines = append(lines, fmt.Sprintf("Tier 2: %d to review by hand: %s%s", len(t2), strings.Join(t2, ", "), note))
	} else {
		lines = append(lines, "Tier 2: nothing to review")
	}
	for _, e := range rep.Errors {
		lines = append(lines, "error: "+e)
	}

	headline := t.Dir + ": "
	switch {
	case len(rep.Errors) > 0:
		headline += "sync check failed: " + rep.Errors[0]
	case apply:
		headline += fmt.Sprintf("applied %d Tier 1 %s", len(rep.Applied), plural(len(rep.Applied), "path", "paths"))
	case len(t1) == 0:
		headline += "scaffold up to date"
		if len(t2) > 0 {
			headline += fmt.Sprintf(", %d Tier 2 to review", len(t2))
		}
	default:
		headline += fmt.Sprintf("%d Tier 1 %s", len(t1), plural(len(t1), "path differs", "paths differ"))
	}
	return Summary{Headline: headline, Lines: lines, Report: rep}
}

func onlyManifests(paths []string) bool {
	for _, p := range paths {
		if p != "composer.json" && p != "package.json" {
			return false
		}
	}
	return true
}

// UpdateOutcome is an UpdateTask's Summary.Report: the theme and what
// `bin/taw update` reported, for "Fix with Claude" or "Do it myself".
type UpdateOutcome struct {
	Site   site.Site
	Theme  site.Theme
	Report taw.UpdateReport
}

// Stopped is an update that failed part-way: its work is on a branch, and
// the report holds the steps to finish it.
func (o UpdateOutcome) Stopped() bool { return o.Report.Status == "failed" }

// UpdateTask is "Update this site": the theme's whole update, as its
// taw.json says, with no questions after this one (`bin/taw update`). A
// theme with uncommitted changes is refused before anything runs.
func (a *Actions) UpdateTask(s site.Site, t site.Theme) (Task, error) {
	if err := taw.Guard(t, false); err != nil {
		return Task{}, err
	}
	if t.Git == nil {
		return Task{}, errors.New("the theme isn't a git repository, so an update can't go on its own branch (git init, commit, then update)")
	}
	if t.Git.Dirty > 0 {
		return Task{}, fmt.Errorf("it has %d uncommitted %s: commit or stash %s first (the update works on its own branch)",
			t.Git.Dirty, plural(t.Git.Dirty, "change", "changes"), plural(t.Git.Dirty, "it", "them"))
	}
	if g := t.Git; g.DefaultBranch != "" && g.Branch != g.DefaultBranch {
		return Task{}, fmt.Errorf("it's on %s, not %s: an update starts from %s (git switch %s). If %s is an earlier update waiting, merge it first (M)",
			orBlank(g.Branch, "a detached HEAD"), g.DefaultBranch, g.DefaultBranch, g.DefaultBranch, orBlank(g.Branch, "it"))
	}
	pol, err := taw.ReadPolicy(t.RealPath)
	if err != nil {
		return Task{}, err
	}
	r := a.taw(s)
	bridge := taw.NeedsBridge(t)
	return Task{Title: "Update " + t.Dir, Writes: true, Ask: UpdateQuestion(t, pol),
		Steps: taw.UpdatePlan(t, pol, bridge), Info: updateInfo(s, t, pol), Target: s.ID + "/" + t.Dir,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			rep, err := r.Update(ctx, t, out)
			if err != nil {
				return Summary{}, err
			}
			sum := summarizeUpdate(t, rep)
			sum.Report = UpdateOutcome{Site: s, Theme: t, Report: rep}
			return sum, nil
		}}, nil
}

// updateInfo is the update's header: the site, the versions, and the
// policy it follows.
func updateInfo(s site.Site, t site.Theme, pol taw.Policy) []string {
	from := strings.TrimPrefix(t.Core.Locked, "v")
	if from == "" {
		from = strings.TrimPrefix(t.Core.Installed, "v")
	}
	versions := "taw/core " + orBlank(from, "?")
	if latest := strings.TrimPrefix(t.Core.Latest, "v"); latest != "" && latest != from {
		versions += " → " + latest + " (newest)"
	} else if latest != "" {
		versions += " (newest)"
	}
	rng := map[string]string{"minor": "any 1.x release", "patch": "bug fixes only"}[pol.Core]
	if rng == "" {
		rng = strings.Replace(pol.Core, "pinned:", "pinned at ", 1)
	}
	ends := map[string]string{"pr": "a pull request for you to merge", "pr+merge": "a pull request that merges itself when CI passes (deploys)", "branch": "a commit on a branch, nothing pushed"}[pol.Deliver]
	if t.Git != nil && t.Git.Remote != "" && t.Git.Repo != nil {
		ends += " on " + t.Git.Repo.FullName()
	}
	source := "taw.json"
	if !pol.File {
		source = "defaults (no taw.json)"
	}
	return []string{
		s.Slug + "  ·  " + t.Dir + "  ·  " + versions,
		source + ": " + rng + "  ·  checks " + strings.Join(pol.Checks, ", ") + "  ·  ends in " + ends,
	}
}

// UpdateQuestion is the one question before an update, in the policy's words.
func UpdateQuestion(t site.Theme, pol taw.Policy) string {
	core := "taw/core"
	switch {
	case pol.Core == "patch":
		core = "taw/core (bug fixes)"
	case strings.HasPrefix(pol.Core, "pinned:"):
		core = "taw/core (pinned " + strings.TrimPrefix(pol.Core, "pinned:") + ")"
	}
	deliver := "a pull request to merge"
	switch pol.Deliver {
	case "pr+merge":
		deliver = "a pull request that merges itself (deploys)"
	case "branch":
		deliver = "a commit on a branch"
	}
	steps := core + ", framework files, migrations, checks"
	if t.Kind != site.KindClassic || pol.Scaffold == "off" {
		steps = core + ", migrations, checks" // as taw.UpdatePlan: no framework-files step
	}
	return fmt.Sprintf("Update %s? %s, then %s.", t.Dir, steps, deliver)
}

func summarizeUpdate(t site.Theme, rep taw.UpdateReport) Summary {
	from, to := strings.TrimPrefix(rep.Core.From, "v"), strings.TrimPrefix(rep.Core.To, "v")
	var lines []string
	if from != "" && to != "" && from != to {
		lines = append(lines, "taw/core "+from+" → "+to)
	}
	if rep.Bridged != "" {
		lines = append(lines, "  (its taw/core had no one-step update: vendor/ got the newest first, so this one was a single step too)")
	}
	if len(rep.Changed) > 0 {
		lines = append(lines, fmt.Sprintf("%d %s changed, committed on %s", len(rep.Changed), plural(len(rep.Changed), "file", "files"), rep.Branch))
	}
	if len(rep.Migrations) > 0 {
		lines = append(lines, "Migrations: "+strings.Join(rep.Migrations, ", "))
	}
	for _, m := range rep.Manual {
		lines = append(lines, "For you: "+firstSentence(m)+" (steps in the report)")
	}
	for _, h := range rep.Held {
		lines = append(lines, "Off in taw.json: "+h)
	}
	if len(rep.Checks) > 0 {
		var cs []string
		for _, c := range rep.Checks {
			switch c.Status {
			case "pass":
				cs = append(cs, c.Name+" ✓")
			case "fail":
				cs = append(cs, c.Name+" ✗")
			default:
				cs = append(cs, c.Name+" – not run here")
			}
		}
		lines = append(lines, "Checks: "+strings.Join(cs, " · "))
	}

	sum := Summary{}
	switch rep.Status {
	case "updated":
		sum.Headline = t.Dir + ": updated"
		if to != "" {
			sum.Headline += " to taw/core " + to
		}
		if rep.Delivered != nil {
			if rep.Delivered.URL != "" {
				sum.Headline += "; pull request " + rep.Delivered.URL
			}
			lines = append(lines, rep.Delivered.Note)
		}
	case "up-to-date":
		files := ", framework files and migrations current)"
		if t.Kind != site.KindClassic {
			files = ", migrations current)"
		}
		sum.Headline = t.Dir + ": nothing to update (taw/core " + orBlank(from, "current") + files
	case "refused":
		sum.Failed = true
		sum.Headline = t.Dir + ": the update didn't start; nothing changed"
		if rep.Failure != nil {
			lines = append(lines, rep.Failure.Reason)
		}
	default: // failed
		sum.Failed = true
		step := "a step"
		if rep.Failure != nil {
			step = rep.Failure.Step
		}
		sum.Headline = t.Dir + ": the update stopped at " + step + "; nothing was pushed"
		if rep.Failure != nil && rep.Failure.Command != "" {
			lines = append(lines, "Failed: "+rep.Failure.Command)
			if first := firstLine(rep.Failure.Out); first != "" {
				lines = append(lines, "  "+first)
			}
		}
		if rep.Branch != "" {
			lines = append(lines, "The work so far is on "+rep.Branch+".")
		}
		lines = append(lines, "To finish it: the steps are in "+taw.ReportFile+", for a person or for Claude (the same guide).")
	}
	if rep.ReportPath != "" && rep.Status != "failed" && rep.Status != "up-to-date" {
		lines = append(lines, "The report: "+taw.ReportFile)
	}
	sum.Lines = lines
	return sum
}

// firstSentence is a migration's manual step up to its first period: the
// summary line; the whole step is in the report.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

func orBlank(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// syncAllParallel is how many scaffold checks run at once: each clones
// taw-theme.
const syncAllParallel = 3

// SyncAllTask checks every classic TAW theme against the taw-theme scaffold
// (`bin/taw sync`, read-only), a few at a time, and caches each result for
// the SYNC column. A theme that fails doesn't stop the others.
func (a *Actions) SyncAllTask(sites []site.Site) (Task, error) {
	type job struct {
		s site.Site
		t site.Theme
	}
	var jobs []job
	for _, s := range sites {
		for _, t := range s.TAWThemes() {
			if taw.Guard(t, true) == nil {
				jobs = append(jobs, job{s, t})
			}
		}
	}
	if len(jobs) == 0 {
		return Task{}, errors.New("no theme to check (only classic TAW themes with bin/taw, not the umbrella's)")
	}
	return Task{
		Title: fmt.Sprintf("Sync check: %d themes", len(jobs)),
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			say := sayTo(out)
			var differ, failed []string
			var mu sync.Mutex
			g, gctx := errgroup.WithContext(ctx)
			g.SetLimit(syncAllParallel)
			for _, j := range jobs {
				g.Go(func() error {
					rep, err := a.taw(j.s).Sync(gctx, j.t, false, io.Discard)
					mu.Lock()
					defer mu.Unlock()
					var line string
					switch {
					case err != nil:
						line = "✗ " + j.t.Dir + ": " + err.Error()
						failed = append(failed, j.t.Dir)
					case len(rep.Errors) > 0:
						line = "✗ " + j.t.Dir + ": " + rep.Errors[0]
						failed = append(failed, j.t.Dir)
					default:
						d := rep.Drift(a.now())
						taw.SaveDrift(a.Paths.CacheDir, j.s.Slug, j.t.Dir, d)
						if len(d.Tier1) > 0 {
							line = fmt.Sprintf("▲ %s: %d Tier 1 %s (%s)", j.t.Dir, len(d.Tier1), plural(len(d.Tier1), "path differs", "paths differ"), strings.Join(d.Tier1, ", "))
							differ = append(differ, j.t.Dir)
						} else {
							line = "✓ " + j.t.Dir + ": matches taw-theme"
							if len(d.Tier2) > 0 {
								line += fmt.Sprintf(" (%d Tier 2 to review)", len(d.Tier2))
							}
						}
					}
					say("%s", line)
					return nil
				})
			}
			_ = g.Wait()
			if err := ctx.Err(); err != nil {
				return Summary{}, err
			}
			head := fmt.Sprintf("Checked %d themes: %d match taw-theme", len(jobs), len(jobs)-len(differ)-len(failed))
			if len(differ) > 0 {
				sort.Strings(differ)
				head += fmt.Sprintf(", %d differ (%s)", len(differ), strings.Join(differ, ", "))
			}
			if len(failed) > 0 {
				head += fmt.Sprintf(", %d failed", len(failed))
			}
			return Summary{Headline: head}, nil // the lines are the progress
		},
	}, nil
}
