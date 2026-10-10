package actions

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/taw"
)

// The lines an update-all writes around each theme, so the dashboard knows
// whose progress follows: "▶ update <target>" and "■ <target> <status> [<pull request>]".
const (
	BatchStart = "▶ update "
	BatchEnd   = "■ "
)

// BatchItem is one theme of an update-all, as the dashboard lists it.
type BatchItem struct {
	Target string // "siteID/themeDir"
	Label  string // "ml-portfolio · ml-theme"
	Steps  []taw.Step
	Info   []string
}

// UpdateAllOutcome is an update-all's Summary.Report: every theme's outcome,
// in order (Err when it couldn't run at all).
type UpdateAllOutcome struct {
	Items []UpdateItemOutcome
}

// UpdateItemOutcome is one theme's result in an update-all.
type UpdateItemOutcome struct {
	UpdateOutcome
	Target string
	Err    error
}

// Stopped lists the themes whose update stopped part-way (work on a branch,
// a report to finish from).
func (o UpdateAllOutcome) Stopped() []UpdateOutcome {
	var out []UpdateOutcome
	for _, it := range o.Items {
		if it.Err == nil && it.Stopped() {
			out = append(out, it.UpdateOutcome)
		}
	}
	return out
}

// PlanUpdateAll picks the themes for an update-all without agents: every
// client TAW theme with something to update (as for the agents' update-all),
// on its default branch with a clean tree.
func (a *Actions) PlanUpdateAll(ctx context.Context, sites []site.Site) FleetPlan {
	plan := a.PlanFleet(ctx, sites)
	var keep []FleetEntry
	for _, e := range plan.Themes {
		g := e.Theme.Git
		if g.DefaultBranch != "" && g.Branch != g.DefaultBranch {
			plan.Skipped = append(plan.Skipped, handoff.FleetSkip{Site: e.Site.Slug, Theme: e.Theme.Dir,
				Reason: "on " + g.Branch + ", not " + g.DefaultBranch})
			continue
		}
		keep = append(keep, e)
	}
	plan.Themes = keep
	return plan
}

// UpdateAllTask updates the plan's themes one after another, each as its
// taw.json says (`bin/taw update`, as u does). A theme that stops doesn't
// stop the others.
func (a *Actions) UpdateAllTask(plan FleetPlan) (Task, error) {
	if len(plan.Themes) == 0 {
		return Task{}, ErrNothingToUpdate
	}
	type job struct {
		e    FleetEntry
		item BatchItem
	}
	var jobs []job
	var names []string
	for _, e := range plan.Themes {
		pol, err := taw.ReadPolicy(e.Theme.RealPath)
		if err != nil {
			return Task{}, fmt.Errorf("%s: %w", e.Theme.Dir, err)
		}
		item := BatchItem{Target: e.Site.ID + "/" + e.Theme.Dir, Label: e.Site.Slug + " · " + e.Theme.Dir,
			Steps: taw.UpdatePlan(e.Theme, pol, taw.NeedsBridge(e.Theme)), Info: updateInfo(e.Site, e.Theme, pol)}
		jobs = append(jobs, job{e, item})
		names = append(names, e.Theme.Dir)
	}
	n := len(jobs)
	ask := fmt.Sprintf("Update %d %s, one after another, each as its taw.json says, each ending in its own pull request: %s?",
		n, plural(n, "theme", "themes"), strings.Join(names, ", "))
	if left := skippedText(plan.Skipped); left != "" {
		ask += " Leaving out " + left + "."
	}
	info := []string{"one theme at a time, each as its taw.json says  ·  a theme that stops doesn't stop the others"}
	if left := skippedText(plan.Skipped); left != "" {
		info = append(info, "left out: "+left)
	}
	items := make([]BatchItem, n)
	for i, j := range jobs {
		items[i] = j.item
	}
	return Task{
		Title: fmt.Sprintf("Update all · %d %s", n, plural(n, "theme", "themes")), Writes: true, Ask: ask, Info: info, Batch: items,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			var res UpdateAllOutcome
			for _, j := range jobs {
				if ctx.Err() != nil {
					break
				}
				_, _ = fmt.Fprintln(out, BatchStart+j.item.Target)
				rep, err := a.taw(j.e.Site).Update(ctx, j.e.Theme, out)
				it := UpdateItemOutcome{UpdateOutcome: UpdateOutcome{Site: j.e.Site, Theme: j.e.Theme, Report: rep}, Target: j.item.Target, Err: err}
				res.Items = append(res.Items, it)
				end := BatchEnd + j.item.Target + " " + rep.Status
				switch {
				case err != nil:
					end = BatchEnd + j.item.Target + " error"
				case rep.Delivered != nil && rep.Delivered.URL != "":
					end += " " + rep.Delivered.URL
				}
				_, _ = fmt.Fprintln(out, end)
			}
			return summarizeUpdateAll(res), nil
		}}, nil
}

func skippedText(skipped []handoff.FleetSkip) string {
	var parts []string
	for _, s := range skipped {
		parts = append(parts, s.Theme+" ("+s.Reason+")")
	}
	return strings.Join(parts, ", ")
}

func summarizeUpdateAll(res UpdateAllOutcome) Summary {
	var updated, current, stopped, other int
	var lines []string
	for _, it := range res.Items {
		switch {
		case it.Err != nil:
			other++
			lines = append(lines, "✗ "+it.Theme.Dir+": "+it.Err.Error())
		case it.Report.Status == "updated":
			updated++
			line := "✓ " + it.Theme.Dir + ": taw/core " + strings.TrimPrefix(it.Report.Core.To, "v")
			if it.Report.Delivered != nil && it.Report.Delivered.URL != "" {
				line += "  " + it.Report.Delivered.URL
			}
			lines = append(lines, line)
		case it.Report.Status == "up-to-date":
			current++
			lines = append(lines, "– "+it.Theme.Dir+": nothing to update")
		case it.Report.Status == "refused":
			other++
			reason := ""
			if it.Report.Failure != nil {
				reason = ": " + it.Report.Failure.Reason
			}
			lines = append(lines, "✗ "+it.Theme.Dir+": didn't start"+reason)
		default:
			stopped++
			step := ""
			if it.Report.Failure != nil {
				step = " at " + it.Report.Failure.Step
			}
			lines = append(lines, "✗ "+it.Theme.Dir+": stopped"+step+", nothing pushed (the steps: "+taw.ReportFile+")")
		}
	}
	var parts []string
	if updated > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", updated))
	}
	if stopped > 0 {
		parts = append(parts, fmt.Sprintf("%d stopped", stopped))
	}
	if other > 0 {
		parts = append(parts, fmt.Sprintf("%d didn't run", other))
	}
	if current > 0 {
		parts = append(parts, fmt.Sprintf("%d had nothing to update", current))
	}
	return Summary{Headline: "Update all: " + strings.Join(parts, ", "), Lines: lines, Report: res, Failed: stopped+other > 0}
}
