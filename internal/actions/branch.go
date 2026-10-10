package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// SiteBranchTask brings the theme back onto its default branch (D): the
// fix for doctor's git.foreign-upstream and git.off-default. A branch that
// tracks another repository (a local copy of the scaffold's, say) is deleted
// afterwards when it has nothing of its own; the site's other branches are
// kept. Uncommitted changes refuse it: a switch would carry them along.
func (a *Actions) SiteBranchTask(_ site.Site, t site.Theme) (Task, error) {
	g := t.Git
	switch {
	case g == nil:
		return Task{}, errors.New("the theme isn't its own git repository")
	case g.DefaultBranch == "":
		return Task{}, errors.New("its default branch isn't known (git remote set-head origin --auto)")
	case !g.Detached && g.Branch == g.DefaultBranch:
		return Task{}, errors.New("already on " + g.DefaultBranch)
	case g.Dirty > 0:
		return Task{}, fmt.Errorf("%d uncommitted %s: commit or stash first", g.Dirty, plural(g.Dirty, "change", "changes"))
	}
	def, from := g.DefaultBranch, g.Branch
	drop := g.ForeignUpstream != "" && !g.Detached
	ask := fmt.Sprintf("Switch %s to %s?", t.Dir, def)
	if drop {
		ask += fmt.Sprintf(" Then delete the local branch %s (it tracks %s), unless it has commits of its own.", from, g.ForeignUpstream)
	}
	return Task{
		Title:  "Back to " + def + ": " + t.Dir,
		Writes: true,
		Ask:    ask,
		Quiet:  true,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			say := sayTo(out)
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			git := func(args ...string) (string, error) {
				res, err := a.Exec.Run(ctx, exec.Spec{Name: "git", Dir: t.RealPath, Args: args})
				if err == nil && res.Code != 0 {
					err = errors.New(strings.TrimSpace(string(res.Stderr)))
				}
				return strings.TrimSpace(string(res.Stdout)), err
			}
			if _, err := git("switch", def); err != nil {
				return Summary{}, fmt.Errorf("git switch %s: %w", def, err)
			}
			say("Switched %s to %s", t.Dir, def)
			sum := Summary{Headline: t.Dir + ": on " + def + " again"}
			if !drop {
				if from != "" {
					sum.Lines = append(sum.Lines, "the branch "+from+" is kept")
				}
				return sum, nil
			}
			// Commits on the branch that its upstream hasn't: the only work
			// a delete could lose.
			own, err := git("rev-list", "--count", from+"@{upstream}.."+from)
			if err != nil || own != "0" {
				sum.Lines = append(sum.Lines, "the branch "+from+" is kept: it has commits of its own (or its upstream is gone)")
				return sum, nil
			}
			if _, err := git("branch", "-D", from); err != nil {
				sum.Lines = append(sum.Lines, "the branch "+from+" wasn't deleted: "+err.Error())
				return sum, nil
			}
			say("Deleted the local branch %s (a copy of %s)", from, g.Upstream)
			sum.Lines = append(sum.Lines, "deleted the local branch "+from+" (a copy of "+g.Upstream+", nothing of its own)")
			return sum, nil
		},
	}, nil
}
