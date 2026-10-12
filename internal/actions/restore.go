package actions

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/restore"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Restores is the site's restore points (restore_keep in the config).
func (a *Actions) Restores() restore.Store {
	return restore.Store{Paths: a.Paths, Runner: a.Exec, Keep: a.Config.Restores(), Now: a.Now}
}

// checkpoint saves a restore point before a risky step and says so. When it
// can't, the step doesn't run: nothing has changed yet.
func (a *Actions) checkpoint(ctx context.Context, s site.Site, t site.Theme, reason, note string, out io.Writer) error {
	p, err := a.Restores().Save(ctx, s, t, reason, note)
	if err != nil {
		return fmt.Errorf("couldn't save a restore point, so nothing was changed: %w", err)
	}
	if p.ID != "" {
		_, _ = fmt.Fprintln(out, "Restore point "+p.ID+": "+PointContents(p))
	}
	return nil
}

// PointContents is what a point holds, in a few words.
func PointContents(p restore.Point) string {
	var parts []string
	if p.Database {
		parts = append(parts, "the database ("+restore.Size(p.DBBytes)+")")
	} else if p.NoDB != "" {
		parts = append(parts, "no database ("+p.NoDB+")")
	}
	if p.Commit != "" {
		at := p.Branch
		if at == "" {
			at = "a detached HEAD"
		}
		parts = append(parts, p.Theme+" on "+at+" at "+short(p.Commit))
	}
	return strings.Join(parts, ", ")
}

// RestoreTask puts a restore point back, after asking: the theme onto the
// point's branch and the database from its copy, keeping a point of how
// things are now first.
func (a *Actions) RestoreTask(ctx context.Context, s site.Site, t site.Theme, p restore.Point) (Task, error) {
	st := a.Restores()
	pl, err := st.Check(ctx, s, t, p)
	if err != nil {
		return Task{}, err
	}
	var does []string
	if pl.Database {
		does = append(does, "replaces "+s.Slug+"'s database with its copy")
	}
	if pl.Switch != "" {
		does = append(does, "switches "+t.Dir+" to "+pl.Switch)
	}
	if len(does) == 0 {
		return Task{}, fmt.Errorf("%s holds nothing to put back: %s", p.ID, PointContents(p))
	}
	ask := "Put back " + p.Label() + "? It " + strings.Join(does, " and ") + "; how things are now is saved first."
	if pl.Moved {
		ask += " Commits made since stay."
	}
	return Task{
		Title: "Restore " + s.Slug + ": " + p.Label(), Writes: true, Ask: ask, Target: s.ID + "/" + t.Dir,
		Info: []string{s.Slug + "  ·  " + t.Dir + "  ·  " + PointContents(p)},
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			before, err := st.Restore(ctx, s, t, p, out)
			if err != nil {
				return Summary{Headline: "Restore stopped: " + err.Error(), Lines: []string{err.Error()}, Failed: true}, nil
			}
			lines := []string{"put back " + p.ID + ": " + PointContents(p), "how it was just before: " + before.ID + "; taw-fleet restore " + s.Slug + " " + before.ID + " undoes this"}
			if pl.Moved {
				lines = append(lines, "note: "+orBlank(p.Branch, "the branch")+" has commits since the point; they're still there")
			}
			return Summary{Headline: "Restored " + s.Slug + " to " + p.Label(), Lines: lines}, nil
		}}, nil
}
