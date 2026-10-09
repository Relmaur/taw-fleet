package actions

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/taw"
)

// Task is a longer job with live output: a scaffold sync or a taw/core
// update. Run writes progress to out and returns a summary.
type Task struct {
	Title  string // "Sync check: chcapital"
	Writes bool   // changes files in the theme (asks first)
	Ask    string // the question to ask first, when it isn't Writes' generic one
	Quiet  bool   // the dashboard stays on the table (the footer shows progress)
	Run    func(ctx context.Context, out io.Writer) (Summary, error)
}

// Summary is what a task found or did, for people.
type Summary struct {
	Headline string   // one line, for the footer
	Lines    []string // the details
	Report   any      // the raw result (sync report, update result)
	Secret   string   // something to copy once and not keep (a new site's password)
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

// UpdateTask runs `composer update taw/core` in the theme and lists the
// UPGRADING.md sections that now apply.
func (a *Actions) UpdateTask(s site.Site, t site.Theme) (Task, error) {
	if err := taw.Guard(t, false); err != nil {
		return Task{}, err
	}
	r := a.taw(s)
	return Task{Title: "Update taw/core: " + t.Dir, Writes: true, Run: func(ctx context.Context, out io.Writer) (Summary, error) {
		res, err := r.UpdateCore(ctx, t, out)
		if err != nil {
			return Summary{}, err
		}
		from, to := strings.TrimPrefix(res.From, "v"), strings.TrimPrefix(res.To, "v")
		sum := Summary{Report: res}
		if from == to {
			sum.Headline = t.Dir + ": taw/core already " + to
			sum.Lines = []string{"taw/core is " + to + "; Composer changed nothing."}
			return sum, nil
		}
		sum.Headline = fmt.Sprintf("%s: taw/core %s → %s; %d UPGRADING.md %s to check", t.Dir, from, to, len(res.Sections), plural(len(res.Sections), "section", "sections"))
		sum.Lines = append(sum.Lines, "taw/core "+from+" → "+to+" (composer.lock and vendor/ changed; commit them on a branch)")
		if len(res.Sections) > 0 {
			sum.Lines = append(sum.Lines, "Work through these sections of vendor/taw/core/UPGRADING.md, each has a Check:")
			for _, sec := range res.Sections {
				sum.Lines = append(sum.Lines, "  • "+sec.Heading)
			}
		}
		return sum, nil
	}}, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
