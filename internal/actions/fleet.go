package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Relmaur/taw-fleet/internal/composer"
	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// FleetParallel is how many subagents an update-all runs at once: Composer
// and npm compete for the Mac beyond that.
const FleetParallel = 3

// FleetEntry is one theme an update-all will update.
type FleetEntry struct {
	Site  site.Site
	Theme site.Theme
}

// FleetPlan is what an update-all would do: the themes it updates, and the
// ones it leaves out with the reason.
type FleetPlan struct {
	Themes  []FleetEntry
	Skipped []handoff.FleetSkip
	Latest  string // the newest taw-core, asked fresh ("" = the scan's)
}

// ErrNothingToUpdate means no theme needs an update-all.
var ErrNothingToUpdate = errors.New("every TAW theme is up to date")

// PlanFleet picks the themes for an update-all. It asks GitHub for the
// newest taw-core first (a release may be minutes old, newer than the
// scan's hour-long cache), then takes every client TAW theme with taw/core
// behind or scaffold drift, leaving out the ones the batch mode would stop
// on (uncommitted changes, an unexpected branch). The umbrella's own
// scaffolds and current themes aren't listed at all.
func (a *Actions) PlanFleet(ctx context.Context, sites []site.Site) FleetPlan {
	var plan FleetPlan
	if a.CoreLatest != nil {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if latest, err := a.CoreLatest(ctx); err == nil {
			plan.Latest = latest
		}
		cancel()
	}
	for _, s := range sites {
		for _, t := range s.Themes {
			if !t.IsTAW || handoff.IsUmbrella(t) {
				continue
			}
			if plan.Latest != "" && t.Core.Installed != "" {
				t.Core.Latest = plan.Latest
				t.Core.Behind = composer.Older(t.Core.Installed, plan.Latest)
			}
			if !handoff.Needs(t) {
				continue
			}
			if reason := handoff.Skip(t); reason != "" {
				plan.Skipped = append(plan.Skipped, handoff.FleetSkip{Site: s.Slug, Theme: t.Dir, Reason: reason})
				continue
			}
			plan.Themes = append(plan.Themes, FleetEntry{Site: s, Theme: t})
		}
	}
	return plan
}

// LaunchFleet writes a batch prompt per theme and the coordinator's prompt,
// then opens Claude Code with it (beside the dashboard when beside is its
// tty), with every theme folder added to the session.
func (a *Actions) LaunchFleet(ctx context.Context, plan FleetPlan, findings map[string][]site.Finding, beside string) (Launched, error) {
	if len(plan.Themes) == 0 {
		return Launched{}, ErrNothingToUpdate
	}
	dir := filepath.Join(a.Paths.CacheDir, "handoff", "fleet-"+a.now().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Launched{}, err
	}
	var (
		themes []handoff.FleetTheme
		args   []string
	)
	for _, e := range plan.Themes {
		p, err := handoff.BuildBatch(a.handoffInput(e.Site, e.Theme, findings[e.Site.ID]))
		if err != nil {
			return Launched{}, fmt.Errorf("%s: %w", e.Theme.Dir, err)
		}
		file := filepath.Join(dir, safeName(e.Site.Slug)+"-"+safeName(e.Theme.Dir)+".md")
		if err := os.WriteFile(file, []byte(p.Text), 0o600); err != nil {
			return Launched{}, err
		}
		ft := handoff.FleetTheme{Site: e.Site.Slug, Theme: e.Theme.Dir, Path: e.Theme.RealPath, PromptFile: file}
		if e.Theme.Core.Behind {
			ft.From, ft.To = e.Theme.Core.Installed, e.Theme.Core.Latest
		}
		themes = append(themes, ft)
		args = append(args, "--add-dir", e.Theme.RealPath)
	}
	p := handoff.Fleet(themes, plan.Skipped, FleetParallel, a.now())
	ls := LaunchScript{Title: p.Title, Dir: dir, Args: args,
		Prompt: filepath.Join(dir, "coordinator.md"), Done: filepath.Join(dir, "coordinator.done")}
	used, placed, err := a.launch(ctx, ls, p.Text, filepath.Join(dir, "coordinator.command"), beside)
	if err != nil {
		return Launched{}, err
	}
	msg := fmt.Sprintf("Started Claude Code in %s to update %d %s", used, len(themes), themeWord(len(themes)))
	if placed {
		msg = fmt.Sprintf("Claude Code is updating %d %s in the window on the right", len(themes), themeWord(len(themes)))
	}
	return Launched{Message: msg, Done: ls.Done}, nil
}

func themeWord(n int) string {
	if n == 1 {
		return "theme"
	}
	return "themes"
}
