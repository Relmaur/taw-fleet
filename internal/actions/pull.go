package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// ContentSource fetches a production site's published content snapshot
// (live.Prober.Content).
type ContentSource func(ctx context.Context, slug, url string) ([]byte, error)

// PullPreview is a PullTask's Summary.Report: what importing would change,
// and the task that does it (Apply.Run is nil when nothing would change).
type PullPreview struct {
	File                         string // the snapshot, in the cache folder
	Create, Change, Same, Delete int
	Media                        int // media the snapshot references
	Apply                        Task
}

// PullTask fetches the production site's published content and previews
// importing it into the Local site (taw/core's content:import dry run,
// which writes nothing). It starts the Local site when it's stopped. The
// preview's Report carries the task that imports it.
func (a *Actions) PullTask(s site.Site, t site.Theme, op SiteOp) (Task, error) {
	prod := a.Config.Site(s.Slug).ProductionURL
	switch {
	case prod == "":
		return Task{}, fmt.Errorf("no production URL for %s: add [sites.%s] production_url to the config", s.Slug, s.Slug)
	case a.Content == nil:
		return Task{}, errors.New("production access isn't set up: taw-fleet live key import")
	case !t.HasBinTaw:
		return Task{}, errors.New("this theme has no bin/taw")
	case s.Status != site.StatusRunning && op == nil:
		return Task{}, fmt.Errorf("%s isn't running: start it in Local first", s.Slug)
	}
	from := hostOf(prod)
	running := s.Status == site.StatusRunning
	ask := ""
	if !running {
		ask = fmt.Sprintf("Start %s and preview pulling %s's content into it? Nothing is imported yet.", s.Slug, from)
	}
	return Task{
		Title: "Pull preview: " + from + " → " + s.Slug,
		Ask:   ask,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			say := sayTo(out)
			if !running {
				say("Starting %s in Local…", s.Slug)
				if _, err := op(ctx, local.Start, s); err != nil {
					return Summary{}, fmt.Errorf("start %s: %w", s.Slug, err)
				}
			}
			say("Fetching the published content of %s…", prod)
			body, err := a.Content(ctx, s.Slug, prod)
			if err != nil {
				return Summary{}, err
			}
			var snap struct {
				Media []json.RawMessage `json:"media"`
				Posts []json.RawMessage `json:"posts"`
			}
			_ = json.Unmarshal(body, &snap)
			say("Got %d posts and %d media (%d KB, signature verified)", len(snap.Posts), len(snap.Media), len(body)/1024)
			dir := filepath.Join(a.Paths.CacheDir, "content")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return Summary{}, err
			}
			file := filepath.Join(dir, fmt.Sprintf("%s-%s.json", safeName(s.Slug), a.now().Format("20060102-150405")))
			if err := os.WriteFile(file, body, 0o600); err != nil {
				return Summary{}, err
			}

			say("Comparing with %s (dry run: nothing is written)…", s.Slug)
			plan, err := a.taw(s).ContentPlan(ctx, s, t, file)
			if err != nil {
				return Summary{}, err
			}
			pv := PullPreview{File: file, Media: len(snap.Media)}
			pv.Create, pv.Change, pv.Same, pv.Delete = plan.Counts()
			var lines []string
			for _, r := range plan.Records {
				f := r.Fields()
				if r.Op != "create" && len(f) == 0 && !strings.Contains(r.Op, "delete") {
					continue
				}
				l := fmt.Sprintf("%-7s %s", r.Op, r.Name())
				if len(f) > 0 {
					l += "  (" + strings.Join(f, ", ") + ")"
				}
				lines = append(lines, l)
			}
			for _, w := range append(plan.RegistryDrift, plan.Warnings...) {
				lines = append(lines, "note: "+w)
			}
			if pv.Create+pv.Change+pv.Delete == 0 {
				return Summary{Headline: fmt.Sprintf("%s already matches %s (%d records)", s.Slug, from, pv.Same), Lines: lines, Report: pv}, nil
			}
			pv.Apply = a.applyPull(s, t, file, from, pv)
			head := fmt.Sprintf("%s → %s: %d new, %d changed, %d unchanged", from, s.Slug, pv.Create, pv.Change, pv.Same)
			return Summary{Headline: head, Lines: lines, Report: pv}, nil
		},
	}, nil
}

// applyPull is the task that imports a previewed snapshot.
func (a *Actions) applyPull(s site.Site, t site.Theme, file, from string, pv PullPreview) Task {
	ask := fmt.Sprintf("Import %s's content into %s? %d new, %d changed; production wins.", from, s.Slug, pv.Create, pv.Change)
	if pv.Delete > 0 {
		ask += fmt.Sprintf(" %d would be deleted.", pv.Delete)
	}
	ask += " A rollback snapshot is saved first."
	return Task{
		Title:  "Pull: " + from + " → " + s.Slug,
		Writes: true,
		Ask:    ask,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			say := sayTo(out)
			say("Importing (media is downloaded from %s)…", from)
			rep, err := a.taw(s).ContentApply(ctx, s, t, file, out)
			if err != nil {
				return Summary{}, err
			}
			head := fmt.Sprintf("Pulled %s into %s: %d created, %d updated, %d media downloaded",
				from, s.Slug, len(rep.Created), len(rep.Updated), rep.MediaSideloaded)
			lines := []string{"rollback snapshot: " + orNone(rep.RollbackPath)}
			for _, w := range append(rep.RegistryDrift, rep.Warnings...) {
				lines = append(lines, "note: "+w)
			}
			return Summary{Headline: head, Lines: lines}, nil
		},
	}
}

func hostOf(u string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://"), "/")
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
