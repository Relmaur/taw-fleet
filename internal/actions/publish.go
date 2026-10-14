package actions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/companion"
	"github.com/Relmaur/taw-fleet/internal/live"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/review"
	"github.com/Relmaur/taw-fleet/internal/site"
	"golang.org/x/mod/semver"
)

// Publisher talks to a production site's publish routes (live.Prober;
// companion 0.5+, taw/core 1.98+).
type Publisher interface {
	Plan(ctx context.Context, slug, url string, changeSet []byte) (companion.PublishPlan, error)
	File(ctx context.Context, slug, url, id string, data []byte) error
	Apply(ctx context.Context, slug, url, id string, req companion.ApplyRequest) (companion.ApplyResult, error)
	Journals(ctx context.Context, slug, url string) ([]companion.Journal, error)
	Undo(ctx context.Context, slug, url, journal string, dryRun bool) (companion.UndoResult, error)
}

// PublishReview is a PublishTask's Summary.Report: the review, the task that
// shows it in the browser and publishes what the person decides there, and
// how to publish with decisions made elsewhere (the CLI, an agent).
type PublishReview struct {
	Page    review.Page
	Folder  string // the review's folder: ~/TAW Fleet/sites/<site>/publish/<when>
	Browser Task   // opens the review; publishes when the person presses Publish
	// Apply publishes with decisions made elsewhere; it asks first.
	Apply func(review.Decisions) Task
}

// UndoPreview is a PublishUndoTask's Summary.Report.
type UndoPreview struct {
	Journal companion.Journal
	Result  companion.UndoResult
	Apply   Task
}

// BaseMeta says what the base is: production's content as of when.
type BaseMeta struct {
	SavedAt    time.Time `json:"saved_at"`
	Source     string    `json:"source"` // pull, publish
	Production string    `json:"production_url"`
}

// BaseDir is where a site's base lives: production's content as Local last
// had it (after a pull) or production took it (after a publish). A content
// snapshot, never a database; kept out of every site, theme and git
// repository, like restore points.
func (a *Actions) BaseDir(slug string) string {
	return filepath.Join(a.Paths.DataDir, "content", safeName(slug))
}

// Base is the site's base snapshot's file and what it is; "" when there's none.
func (a *Actions) Base(slug string) (string, BaseMeta) {
	var m BaseMeta
	file := filepath.Join(a.BaseDir(slug), "base.json")
	if !fileExists(file) {
		return "", m
	}
	if b, err := os.ReadFile(filepath.Join(a.BaseDir(slug), "base.meta.json")); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return file, m
}

// SaveBase keeps snapshot as the site's base.
func (a *Actions) SaveBase(ctx context.Context, s site.Site, snapshot []byte, source, production string) error {
	dir := a.BaseDir(s.Slug)
	if err := a.Restores().Guard(ctx, dir, s); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "base.json"), snapshot, 0o600); err != nil {
		return err
	}
	meta, _ := json.Marshal(BaseMeta{SavedAt: a.now(), Source: source, Production: production})
	return os.WriteFile(filepath.Join(dir, "base.meta.json"), meta, 0o600)
}

// PublishFolder is where a site's reviews and what each published are kept.
func (a *Actions) PublishFolder(slug string) string {
	return filepath.Join(a.Paths.FleetDir, "sites", safeName(slug), "publish")
}

// PublishTask compares Local's content with production's (the base: what
// Local had of it), asks production what publishing that would change, and
// returns the review (Report: PublishReview). Nothing on production changes
// until the person decides. The Local site must run (started when op is given).
func (a *Actions) PublishTask(s site.Site, t site.Theme, op SiteOp) (Task, error) {
	prod := a.Config.Site(s.Slug).ProductionURL
	switch {
	case prod == "":
		return Task{}, fmt.Errorf("no production URL for %s: the deploy setup adds it (I), or production_url under [sites.%s]", s.Slug, s.Slug)
	case a.Publisher == nil:
		return Task{}, errors.New("production access isn't set up: taw-fleet live key import")
	case !t.HasBinTaw:
		return Task{}, errors.New("this theme has no bin/taw")
	case s.Status != site.StatusRunning && op == nil:
		return Task{}, fmt.Errorf("%s isn't running: start it in Local first", s.Slug)
	}
	to := hostOf(prod)
	running := s.Status == site.StatusRunning
	ask := ""
	if !running {
		ask = fmt.Sprintf("Start %s and compare its content with %s's? Nothing is published yet.", s.Slug, to)
	}
	return Task{
		Title: "Publish review: " + s.Slug + " → " + to,
		Ask:   ask,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			say := sayTo(out)
			if !running {
				say("Starting %s in Local…", s.Slug)
				if _, err := op(ctx, local.Start, s); err != nil {
					return Summary{}, fmt.Errorf("start %s: %w", s.Slug, err)
				}
			}
			work := filepath.Join(a.Paths.CacheDir, "publish", fmt.Sprintf("%s-%s", safeName(s.Slug), a.now().Format("20060102-150405")))
			if err := os.MkdirAll(work, 0o700); err != nil {
				return Summary{}, err
			}
			localFile, changes := filepath.Join(work, "local.json"), filepath.Join(work, "changes.json")
			say("Reading %s's content…", s.Slug)
			if err := a.taw(s).ContentExport(ctx, s, t, localFile); err != nil {
				return Summary{}, err
			}
			base, meta := a.Base(s.Slug)
			withBase := base != ""
			if !withBase {
				base = filepath.Join(work, "empty.json")
				if err := os.WriteFile(base, []byte(`{"meta":{"schema":"1.8"}}`), 0o600); err != nil {
					return Summary{}, err
				}
				say("No base: taw-fleet hasn't seen %s's content before (C pulls it), so it can't tell who changed what.", to)
			} else {
				say("Comparing with production's content as of %s (%s)…", meta.SavedAt.Local().Format("2 Jan 2006, 15:04"), meta.Source)
			}
			if err := a.taw(s).ContentDiff(ctx, s, t, base, localFile, changes, withBase); err != nil {
				return Summary{}, err
			}
			changeSet, err := os.ReadFile(changes)
			if err != nil {
				return Summary{}, err
			}
			var ops struct {
				Operations []json.RawMessage `json:"operations"`
			}
			_ = json.Unmarshal(changeSet, &ops)
			if len(ops.Operations) == 0 {
				return Summary{Headline: fmt.Sprintf("Nothing to publish: %s's content is as it was when production's was last seen", s.Slug)}, nil
			}
			say("Asking %s what publishing would change (nothing is written)…", to)
			plan, err := a.Publisher.Plan(ctx, s.Slug, prod, changeSet)
			if err != nil {
				return Summary{}, err
			}
			if err := mediaSafe(plan, to); err != nil {
				return Summary{}, err
			}
			baseLabel := ""
			if withBase {
				baseLabel = meta.SavedAt.Local().Format("2 Jan 2006, 15:04")
			}
			page := review.Build(s.Slug, siteName(s), prod, a.now().Format("2 Jan 2006, 15:04"), baseLabel, plan, review.MediaUsers(changeSet))
			if page.Records == 0 {
				return Summary{Headline: fmt.Sprintf("Nothing to publish: %s already has what %s has", to, s.Slug), Lines: page.Warnings}, nil
			}
			folder := filepath.Join(a.PublishFolder(s.Slug), a.now().Format("2006-01-02-150405"))
			if err := os.MkdirAll(folder, 0o755); err != nil {
				return Summary{}, err
			}
			kept := plan
			kept.Token = "" // never on disk
			if b, err := json.MarshalIndent(kept, "", " "); err == nil {
				_ = os.WriteFile(filepath.Join(folder, "plan.json"), b, 0o644)
			}
			pr := PublishReview{Page: page, Folder: folder}
			pr.Browser = a.publishBrowser(s, prod, plan, page, folder)
			pr.Apply = func(d review.Decisions) Task { return a.publishApply(s, prod, plan, page, folder, d) }
			return Summary{Headline: reviewHead(s.Slug, to, page), Lines: reviewLines(page), Report: pr}, nil
		},
	}, nil
}

// mediaFirst is the first taw/core that publishes only the media a person
// picked. Before it, any publish also wrote every picture's details and
// every new file in the change-set.
const mediaFirst = "v1.99.1"

// mediaSafe refuses a plan with media that production's taw/core would
// publish whatever the person picks.
func mediaSafe(plan companion.PublishPlan, to string) error {
	core := "v" + strings.TrimPrefix(plan.TawCore, "v")
	if plan.TawCore == "" || !semver.IsValid(core) || semver.Compare(core, mediaFirst) >= 0 {
		return nil
	}
	for _, r := range plan.Plan.Records {
		if r.Kind == "media" {
			return fmt.Errorf("%s runs taw/core %s, which would publish every picture's details and new file along with any record you pick. "+
				"Update its taw/core to %s or newer (u, then merge and deploy), then publish again. Nothing was published",
				to, strings.TrimPrefix(plan.TawCore, "v"), strings.TrimPrefix(mediaFirst, "v"))
		}
	}
	return nil
}

// whenOf is production's time stamp as people read it, on this Mac's clock.
func whenOf(stamp string) string {
	if t, err := time.Parse(time.RFC3339, stamp); err == nil {
		return t.Local().Format("2 Jan 2006, 15:04")
	}
	return stamp
}

// undoWhat says what an undo would do ("put back 3 values and delete 1
// record it created"), or "" when nothing.
func undoWhat(u companion.UndoResult) string {
	var parts []string
	if u.Restored > 0 {
		parts = append(parts, fmt.Sprintf("put back %d %s", u.Restored, plural(u.Restored, "value", "values")))
	}
	if u.Deleted > 0 {
		parts = append(parts, fmt.Sprintf("delete %d %s it created", u.Deleted, plural(u.Deleted, "record", "records")))
	}
	if u.Recreated > 0 {
		parts = append(parts, fmt.Sprintf("recreate %d %s it deleted", u.Recreated, plural(u.Recreated, "record", "records")))
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// reviewHead is the review in one line.
func reviewHead(slug, to string, page review.Page) string {
	head := fmt.Sprintf("%s → %s: %d %s, %d %s", slug, to, page.Records, plural(page.Records, "record", "records"), page.Values, plural(page.Values, "value", "values"))
	if n := len(page.Going()); n > 0 {
		head += fmt.Sprintf(", %d %s to upload", n, plural(n, "file", "files"))
	}
	if page.Conflicts > 0 {
		head += fmt.Sprintf("; %d changed on production too", page.Conflicts)
	}
	return head
}

// reviewLines is the review in a few lines, for a terminal or the chat.
func reviewLines(p review.Page) []string {
	var lines []string
	for _, g := range p.Groups {
		for _, r := range g.Records {
			l := fmt.Sprintf("%-7s %s (%s)", r.Op, r.Title, r.What)
			var labels []string
			for _, c := range r.Changes {
				lab := c.Label
				if c.Conflict != "" {
					lab += " [changed on production too]"
				}
				labels = append(labels, lab)
			}
			if len(labels) > 0 {
				l += ": " + strings.Join(labels, ", ")
			}
			if !r.Include && r.Op != "delete" {
				l += " [not included unless asked: " + r.Key + "]"
			}
			lines = append(lines, l)
		}
	}
	for _, f := range p.Files {
		with := "goes with " + strings.Join(f.Users, ", ")
		if len(f.Users) == 0 {
			with = "nothing uses it: stays in Local unless asked (" + f.Key + ")"
		}
		lines = append(lines, "file    "+f.Path+" ("+f.Size+"): "+with)
	}
	for _, w := range p.Warnings {
		lines = append(lines, "note: "+w)
	}
	return lines
}

// publishBrowser shows the review in the browser and publishes what the
// person decides there. The browser is the question: pressing Publish is
// the yes.
func (a *Actions) publishBrowser(s site.Site, prod string, plan companion.PublishPlan, page review.Page, folder string) Task {
	return Task{
		Title:     "Publish review: " + s.Slug + " → " + hostOf(prod),
		Stoppable: true,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			say := sayTo(out)
			srv, err := review.Serve(ctx, page, filepath.Join(s.WebRoot, "wp-content", "uploads"))
			if err != nil {
				return Summary{}, err
			}
			defer srv.Close(8 * time.Second)
			say("%s", reviewHead(s.Slug, hostOf(prod), page))
			for _, l := range reviewLines(page) {
				say("  %s", l)
			}
			if _, err := a.OpenURL(ctx, srv.URL); err != nil {
				say("Open the review in your browser: %s", srv.URL)
			} else {
				say("The review is open in your browser: %s", srv.URL)
			}
			say("Decide there: nothing is published until you press Publish.")
			var d review.Decision
			select {
			case d = <-srv.Decisions():
			case <-ctx.Done():
				srv.SetStatus(review.Status{State: "cancelled", Message: "Closed from taw-fleet: nothing was published."})
				a.saveReview(page, folder, "cancelled", nil, nil, s)
				return Summary{Headline: "Review closed: nothing was published"}, nil
			}
			if d.Cancel {
				a.saveReview(page, folder, "cancelled", nil, nil, s)
				return Summary{Headline: "Cancelled in the review: nothing was published"}, nil
			}
			// Decided: stopping the wait no longer stops what was decided.
			return a.publish(context.WithoutCancel(ctx), out, s, prod, plan, page, folder, d.Decisions, srv.SetStatus)
		},
	}
}

// publishApply publishes with decisions made outside the browser (the CLI,
// an agent); it asks first.
func (a *Actions) publishApply(s site.Site, prod string, plan companion.PublishPlan, page review.Page, folder string, d review.Decisions) Task {
	values := 0
	for _, g := range page.Groups {
		for _, r := range g.Records {
			for _, ap := range d.Approved {
				if ap.Key == r.Key {
					values += len(r.Changes)
				}
			}
		}
	}
	ask := fmt.Sprintf("Publish %d %s (%d %s) to %s?", len(d.Approved), plural(len(d.Approved), "record", "records"), values, plural(values, "value", "values"), hostOf(prod))
	if n := len(page.FilesFor(d)); n > 0 {
		ask += fmt.Sprintf(" %d %s %s up with it.", n, plural(n, "file", "files"), plural(n, "goes", "go"))
	}
	ask += " Production keeps a rollback copy first; undo puts it back."
	return Task{
		Title:  "Publish: " + s.Slug + " → " + hostOf(prod),
		Writes: true,
		Ask:    ask,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			return a.publish(ctx, out, s, prod, plan, page, folder, d, func(review.Status) {})
		},
	}
}

// publish uploads the files, applies the decisions, keeps what happened in
// the review's folder, and makes production's new content the base.
func (a *Actions) publish(ctx context.Context, out io.Writer, s site.Site, prod string, plan companion.PublishPlan, page review.Page, folder string,
	d review.Decisions, status func(review.Status)) (Summary, error) {
	say := sayTo(out)
	to := hostOf(prod)
	fail := func(err error) (Summary, error) {
		msg := err.Error()
		var r *live.Refusal
		if errors.As(err, &r) && r.Code == "publish_moved" {
			msg = "Production or Local changed since this review: publish again (p) to review it afresh. Nothing was published."
		}
		status(review.Status{State: "failed", Message: msg})
		a.saveReview(page, folder, "failed", &d, &review.Result{Error: msg}, s)
		return Summary{}, errors.New(msg)
	}

	uploads := filepath.Join(s.WebRoot, "wp-content", "uploads")
	going := map[string]bool{}
	for _, f := range page.FilesFor(d) {
		going[f.Path] = true
	}
	var needed []companion.MediaFile
	for _, f := range plan.Media.Needed {
		if going[f.File] {
			needed = append(needed, f)
		}
	}
	total := len(needed)
	upload := func(files []companion.MediaFile) error {
		for i, f := range files {
			status(review.Status{State: "publishing", Step: "upload", Done: i, Total: len(files), Message: fmt.Sprintf("Uploading %s (%d of %d)", f.File, i+1, len(files))})
			say("Uploading %s (%d of %d)…", f.File, i+1, len(files))
			file, err := review.Within(uploads, f.File)
			if err != nil {
				return fmt.Errorf("%s isn't in %s's uploads any more: %w", f.File, s.Slug, err)
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != f.SHA256 {
				return fmt.Errorf("%s changed in Local since the review: publish again (p)", f.File)
			}
			if err := a.Publisher.File(ctx, s.Slug, prod, plan.ID, data); err != nil {
				return err
			}
		}
		return nil
	}
	if err := upload(needed); err != nil {
		return fail(err)
	}
	status(review.Status{State: "publishing", Step: "apply", Done: total, Total: total, Message: "Publishing on " + to + "…"})
	say("Publishing %d %s on %s…", len(d.Approved), plural(len(d.Approved), "record", "records"), to)
	req := companion.ApplyRequest{Token: plan.Token, Approved: d.Approved, Keep: nonNil(d.Keep), Take: nonNil(d.Take)}
	res, err := a.Publisher.Apply(ctx, s.Slug, prod, plan.ID, req)
	var extra []string // files production wanted that taw-fleet's check didn't see
	if r := (*live.Refusal)(nil); errors.As(err, &r) && r.Code == "publish_media_missing" {
		if plan.TawCore == "" {
			return fail(fmt.Errorf("%s wants files nothing you ticked uses (%s): its taw/core may be older than 1.99.1, which publishes every new file. Update it (u, then merge and deploy), then publish again. Nothing was published",
				to, strings.Join(r.Missing, ", ")))
		}
		// Production decides what the approved records use (apply checks the
		// files before it spends the token): send what it asks for, once.
		want := map[string]bool{}
		for _, m := range r.Missing {
			want[m] = true
		}
		var more []companion.MediaFile
		for _, f := range plan.Media.Needed {
			if want[f.File] && !going[f.File] {
				more = append(more, f)
				extra = append(extra, f.File)
			}
		}
		if len(more) == 0 {
			return fail(err)
		}
		say("%s also needs %s; uploading %s too…", to, strings.Join(extra, ", "), plural(len(more), "it", "them"))
		if err := upload(more); err != nil {
			return fail(err)
		}
		res, err = a.Publisher.Apply(ctx, s.Slug, prod, plan.ID, req)
	}
	if err != nil {
		return fail(err)
	}
	rep := res.Report
	result := &review.Result{Created: len(rep.Created), Updated: len(rep.Updated), Deleted: len(rep.Deleted), Failed: len(rep.Failed),
		Files: rep.MediaSideloaded, Warnings: rep.Warnings}
	if rep.Journal != nil {
		result.Journal = filepath.Base(*rep.Journal)
	}
	if rep.Error != nil {
		result.Error = *rep.Error
	}

	status(review.Status{State: "publishing", Step: "base", Done: total, Total: total, Message: "Saving production's new content as the base…"})
	baseNote := ""
	if a.Content != nil {
		if snap, err := a.Content(ctx, s.Slug, prod); err == nil {
			if err := a.SaveBase(ctx, s, snap, "publish", prod); err != nil {
				baseNote = "the base wasn't saved (" + err.Error() + "): pull (C) before the next publish"
			}
		} else {
			baseNote = "production's new content wasn't read back (" + err.Error() + "): pull (C) before the next publish"
		}
	}
	state := "published"
	if result.Error != "" {
		state = "failed"
	}
	a.saveReview(page, folder, state, &d, result, s)
	if b, err := json.MarshalIndent(res, "", " "); err == nil {
		_ = os.WriteFile(filepath.Join(folder, "report.json"), b, 0o644)
	}
	msg := summaryLine(result)
	if baseNote != "" {
		msg += "; " + baseNote
	}
	status(review.Status{State: state, Message: msg, Result: result, Kept: filepath.Join(folder, "review.html")})
	if result.Error != "" {
		return Summary{}, fmt.Errorf("the publish stopped on %s: %s (undo: x, or taw-fleet publish undo %s)", to, result.Error, s.Slug)
	}
	head := fmt.Sprintf("Published to %s: %s", to, summaryLine(result))
	lines := []string{"review kept: " + filepath.Join(folder, "review.html"), "undo: x on the site, or taw-fleet publish undo " + s.Slug}
	if result.Journal != "" {
		lines = append(lines, "journal on production: "+result.Journal)
	}
	for _, f := range rep.Failed {
		lines = append(lines, "failed: "+strings.Trim(string(f), `"`))
	}
	for _, w := range rep.Warnings {
		lines = append(lines, "note: "+w)
	}
	if baseNote != "" {
		lines = append(lines, "note: "+baseNote)
	}
	if len(extra) > 0 {
		lines = append(lines, "note: production also needed "+strings.Join(extra, ", ")+", which taw-fleet didn't see a ticked record use; please report it (taw-fleet's media check missed a reference)")
	}
	lines = append(lines, "clear the site's page cache now that it's published, if the host has one")
	return Summary{Headline: head, Lines: lines, Failed: result.Failed > 0}, nil
}

func summaryLine(r *review.Result) string {
	s := fmt.Sprintf("%d created, %d updated", r.Created, r.Updated)
	if r.Deleted > 0 {
		s += fmt.Sprintf(", %d deleted", r.Deleted)
	}
	if r.Files > 0 {
		s += fmt.Sprintf(", %d %s uploaded", r.Files, plural(r.Files, "file", "files"))
	}
	if r.Failed > 0 {
		s += fmt.Sprintf(", %d failed", r.Failed)
	}
	return s
}

// saveReview keeps the review as it ended, as a page that opens from disk.
func (a *Actions) saveReview(page review.Page, folder, state string, d *review.Decisions, r *review.Result, s site.Site) {
	page.State, page.Decisions, page.Result = state, d, r
	page.MediaBase = "file://" + filepath.ToSlash(filepath.Join(s.WebRoot, "wp-content", "uploads")) + "/"
	if html, err := page.HTML(); err == nil {
		_ = os.WriteFile(filepath.Join(folder, "review.html"), html, 0o644)
	}
	if d != nil {
		if b, err := json.MarshalIndent(d, "", " "); err == nil {
			_ = os.WriteFile(filepath.Join(folder, "decisions.json"), b, 0o644)
		}
	}
}

// DefaultDecisions are what publishing "as reviewed" means without a
// person in the browser: every record but deletes, each conflict on its
// default side. The conflicts with no default are returned instead.
func DefaultDecisions(p review.Page) (review.Decisions, []string) {
	var include []string
	for _, g := range p.Groups {
		for _, r := range g.Records {
			if r.Include {
				include = append(include, r.Key)
			}
		}
	}
	return p.Decide(include, nil)
}

// Published is one publish kept in the site's folder.
type Published struct {
	When    time.Time `json:"when"`
	Folder  string    `json:"folder"`
	State   string    `json:"state"` // published, failed, cancelled, review (never decided)
	Journal string    `json:"journal,omitempty"`
	Updated int       `json:"updated"`
	Created int       `json:"created"`
}

// PublishHistory is the site's publishes, newest first.
func (a *Actions) PublishHistory(slug string) []Published {
	entries, _ := os.ReadDir(a.PublishFolder(slug))
	var out []Published
	for _, e := range entries {
		when, err := time.ParseInLocation("2006-01-02-150405", e.Name(), time.Local)
		if !e.IsDir() || err != nil {
			continue
		}
		dir := filepath.Join(a.PublishFolder(slug), e.Name())
		p := Published{When: when, Folder: dir, State: "review"}
		var rep companion.ApplyResult
		if b, err := os.ReadFile(filepath.Join(dir, "report.json")); err == nil && json.Unmarshal(b, &rep) == nil {
			p.State, p.Created, p.Updated = "published", len(rep.Report.Created), len(rep.Report.Updated)
			if rep.Report.Journal != nil {
				p.Journal = filepath.Base(*rep.Report.Journal)
			}
			if rep.Report.Error != nil {
				p.State = "failed"
			}
		} else if b, err := os.ReadFile(filepath.Join(dir, "review.html")); err == nil {
			switch {
			case strings.Contains(string(b), "<h1>Not published"):
				p.State = "cancelled"
			case strings.Contains(string(b), "The publish stopped"):
				p.State = "failed"
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].When.After(out[j].When) })
	return out
}

// PublishUndoTask previews undoing the newest publish (or import) production
// received, from its journal; the preview's Report carries the undo.
func (a *Actions) PublishUndoTask(s site.Site, journal string) (Task, error) {
	prod := a.Config.Site(s.Slug).ProductionURL
	switch {
	case prod == "":
		return Task{}, fmt.Errorf("no production URL for %s", s.Slug)
	case a.Publisher == nil:
		return Task{}, errors.New("production access isn't set up: taw-fleet live key import")
	}
	to := hostOf(prod)
	return Task{
		Title: "Undo preview: " + to,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			say := sayTo(out)
			say("Asking %s what it received…", to)
			journals, err := a.Publisher.Journals(ctx, s.Slug, prod)
			if err != nil {
				return Summary{}, err
			}
			var pick *companion.Journal
			for i, j := range journals {
				if (journal == "" && !j.Undone) || j.File == journal {
					pick = &journals[i]
					break
				}
			}
			if pick == nil {
				return Summary{Headline: "Nothing to undo on " + to}, nil
			}
			pre, err := a.Publisher.Undo(ctx, s.Slug, prod, pick.File, true)
			if err != nil {
				return Summary{}, err
			}
			what, when := undoWhat(pre), whenOf(pick.CreatedAt)
			head := fmt.Sprintf("Undoing %s's publish of %s would %s", to, when, what)
			if what == "" {
				head = fmt.Sprintf("Nothing to undo in %s's publish of %s: everything it wrote was edited since", to, when)
			}
			var lines []string
			for _, k := range pre.Kept {
				lines = append(lines, "kept: "+k)
			}
			up := UndoPreview{Journal: *pick, Result: pre}
			if pre.Restored+pre.Deleted+pre.Recreated > 0 {
				up.Apply = Task{
					Title: "Undo: " + to, Writes: true,
					Ask: fmt.Sprintf("Undo the publish of %s on %s? It would %s%s.", when, to, what,
						map[bool]string{true: "; values edited since stay", false: ""}[len(pre.Kept) > 0]),
					Run: func(ctx context.Context, _ io.Writer) (Summary, error) {
						res, err := a.Publisher.Undo(ctx, s.Slug, prod, pick.File, false)
						if err != nil {
							return Summary{}, err
						}
						lines := []string{"clear the site's page cache, if the host has one"}
						if a.Content != nil {
							if snap, err := a.Content(ctx, s.Slug, prod); err == nil {
								_ = a.SaveBase(ctx, s, snap, "publish", prod)
							}
						}
						for _, k := range res.Kept {
							lines = append(lines, "kept: "+k)
						}
						return Summary{Headline: fmt.Sprintf("Undone on %s: %d %s put back, %d deleted, %d recreated", to, res.Restored,
							plural(res.Restored, "value", "values"), res.Deleted, res.Recreated), Lines: lines}, nil
					},
				}
			}
			return Summary{Headline: head, Lines: lines, Report: up}, nil
		},
	}, nil
}

func nonNil(m map[string][]string) map[string][]string {
	if m == nil {
		return map[string][]string{}
	}
	return m
}

func siteName(s site.Site) string {
	name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(s.Name, " - TAW"), " – TAW"))
	if name == "" {
		return s.Slug
	}
	return name
}
