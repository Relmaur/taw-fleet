package actions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/companion"
	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/live"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// fakeProduction is production's publish routes: it answers the real plan
// the fidelity suite made (site A's Local edits against site B, which
// changed things of its own), with the files' hashes made to match ours.
type fakeProduction struct {
	plan     companion.PublishPlan
	files    [][]byte
	applied  []companion.ApplyRequest
	refuse   error
	once     bool // refuse only the first apply
	undone   []bool
	snapshot []byte
}

func (f *fakeProduction) Plan(_ context.Context, _, _ string, changeSet []byte) (companion.PublishPlan, error) {
	if !json.Valid(changeSet) {
		return companion.PublishPlan{}, errors.New("not JSON")
	}
	return f.plan, nil
}

func (f *fakeProduction) File(_ context.Context, _, _, _ string, data []byte) error {
	f.files = append(f.files, data)
	return nil
}

func (f *fakeProduction) Apply(_ context.Context, _, _, _ string, req companion.ApplyRequest) (companion.ApplyResult, error) {
	f.applied = append(f.applied, req)
	var r companion.ApplyResult
	if f.refuse != nil {
		err := f.refuse
		if f.once {
			f.refuse = nil
		}
		return r, err
	}
	journal := "/wp-content/uploads/taw-private/import-20261013-1.json"
	r.Report.Updated = []json.RawMessage{json.RawMessage(`"page:about"`)}
	r.Report.MediaSideloaded = len(f.files)
	r.Report.Journal = &journal
	r.Applied = []string{"post:page:about"}
	return r, nil
}

func (f *fakeProduction) Journals(context.Context, string, string) ([]companion.Journal, error) {
	return []companion.Journal{{File: "import-20261013-1.json", CreatedAt: "2026-10-13T09:00:00+00:00"}}, nil
}

func (f *fakeProduction) Undo(_ context.Context, _, _, _ string, dryRun bool) (companion.UndoResult, error) {
	f.undone = append(f.undone, dryRun)
	return companion.UndoResult{Restored: 4, Deleted: 1, Kept: []string{"post:12 fields.post_title (edited since)"}}, nil
}

// changeSet is what content:diff wrote: the records that use the plan's two
// new files (Local's ids 10 and 30).
const changeSet = `{"taw_changeset":{"schema":"1.8"},
 "media":[{"id":10,"filename":"photo.jpg","url":"http://ls-mexico.local/wp-content/uploads/2025/03/photo.jpg"},
  {"id":30,"filename":"fresh.jpg","url":"http://ls-mexico.local/wp-content/uploads/2026/09/fresh.jpg"}],
 "operations":[
  {"op":"update","target":{"kind":"post","type":"page","slug":"home","path":"home"},"post":{"fields":{"f_files":[5036,10]}}},
  {"op":"update","target":{"kind":"post","type":"page","slug":"about","path":"about"},"post":{"fields":{"f_image":"30"}}}]}`

func publishSetup(t *testing.T) (*Actions, *exec.FakeRunner, *fakeProduction, site.Site, site.Theme) {
	t.Helper()
	a, f := setup(t, config.Config{Sites: map[string]config.Site{"ls-mxico": {ProductionURL: "https://lsmexico.mx"}}})
	s, th := fixture()
	s.Status, s.Socket, th.HasBinTaw = site.StatusRunning, "/run/x/mysqld.sock", true
	s.WebRoot = t.TempDir()
	uploads := filepath.Join(s.WebRoot, "wp-content", "uploads")

	data, err := os.ReadFile("../review/testdata/plan.json")
	if err != nil {
		t.Fatal(err)
	}
	prod := &fakeProduction{snapshot: []byte(`{"meta":{"schema":"1.8"},"posts":[]}`)}
	if err := json.Unmarshal(data, &prod.plan); err != nil {
		t.Fatal(err)
	}
	for i, m := range prod.plan.Media.Needed {
		body := []byte("bytes of " + m.File)
		_ = os.MkdirAll(filepath.Dir(filepath.Join(uploads, m.File)), 0o755)
		_ = os.WriteFile(filepath.Join(uploads, m.File), body, 0o644)
		sum := sha256.Sum256(body)
		prod.plan.Media.Needed[i].SHA256 = hex.EncodeToString(sum[:])
	}
	prod.plan.Token = "t0k3n"
	a.Publisher = prod
	a.Content = func(context.Context, string, string) ([]byte, error) { return prod.snapshot, nil }
	f.Script = func(sp exec.Spec) (exec.Result, error) {
		if sp.Name == "git" {
			return exec.Result{Code: 128}, nil
		}
		for _, arg := range sp.Args {
			if out, ok := strings.CutPrefix(arg, "--output="); ok {
				_ = os.WriteFile(out, []byte(`{"meta":{"schema":"1.8"},"posts":[{"slug":"about"}]}`), 0o600)
			}
			if out, ok := strings.CutPrefix(arg, "--out="); ok {
				_ = os.WriteFile(out, []byte(changeSet), 0o600)
			}
		}
		return exec.Result{}, nil
	}
	return a, f, prod, s, th
}

func TestPublishWithoutABase(t *testing.T) {
	a, f, prod, s, th := publishSetup(t)
	task, err := a.PublishTask(s, th, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.Writes || task.Ask != "" {
		t.Errorf("comparing writes nothing and asks nothing: %v %q", task.Writes, task.Ask)
	}
	sum, err := task.Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var diff exec.Spec
	for _, c := range f.Calls() {
		if slices.Contains(c.Args, "content:diff") {
			diff = c
		}
	}
	if slices.Contains(diff.Args, "--with-base") || !strings.HasSuffix(diff.Args[slices.Index(diff.Args, "content:diff")+1], "empty.json") {
		t.Errorf("no base: compared with nothing, no conflicts asked for: %v", diff.Args)
	}
	pr, ok := sum.Report.(PublishReview)
	if !ok || pr.Page.Base != "" || pr.Page.Conflicts != 0 || pr.Page.Records != 6 {
		t.Fatalf("review %+v", pr.Page)
	}
	if !strings.HasPrefix(sum.Headline, "ls-mxico → lsmexico.mx: 6 records, 13 values, 2 files to upload") {
		t.Errorf("headline %q", sum.Headline)
	}
	if b, err := os.ReadFile(filepath.Join(pr.Folder, "plan.json")); err != nil || strings.Contains(string(b), "t0k3n") {
		t.Errorf("the plan is kept, never its token (%v)", err)
	}

	d, missing := DefaultDecisions(pr.Page)
	if len(missing) != 0 || len(d.Approved) != 6 {
		t.Fatalf("as reviewed: %d records, missing %v", len(d.Approved), missing)
	}
	apply := pr.Apply(d)
	if !apply.Writes || !strings.HasPrefix(apply.Ask, "Publish 6 records (13 values) to lsmexico.mx? 2 files go up with it.") {
		t.Errorf("ask %q", apply.Ask)
	}
	sum, err = apply.Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(prod.files) != 2 || len(prod.applied) != 1 || prod.applied[0].Token != "t0k3n" || len(prod.applied[0].Approved) != 6 {
		t.Errorf("files %d, applied %+v", len(prod.files), prod.applied)
	}
	if sum.Headline != "Published to lsmexico.mx: 0 created, 1 updated, 2 files uploaded" {
		t.Errorf("headline %q", sum.Headline)
	}
	html, err := os.ReadFile(filepath.Join(pr.Folder, "review.html"))
	if err != nil || !strings.Contains(string(html), "<b>Published.</b>") || !strings.Contains(string(html), "file://"+s.WebRoot) {
		t.Errorf("the review is kept as it ended (%v)", err)
	}
	if file, meta := a.Base("ls-mxico"); file == "" || meta.Source != "publish" {
		t.Errorf("production's new content is the base: %q %+v", file, meta)
	}
	if h := a.PublishHistory("ls-mxico"); len(h) != 1 || h[0].State != "published" || h[0].Journal != "import-20261013-1.json" {
		t.Errorf("history %+v", h)
	}
}

func TestPublishWithABaseAsksAboutConflicts(t *testing.T) {
	a, f, prod, s, th := publishSetup(t)
	if err := a.SaveBase(context.Background(), s, []byte(`{"meta":{}}`), "pull", "https://lsmexico.mx"); err != nil {
		t.Fatal(err)
	}
	task, _ := a.PublishTask(s, th, nil)
	sum, err := task.Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	withBase := false
	for _, c := range f.Calls() {
		withBase = withBase || slices.Contains(c.Args, "--with-base")
	}
	pr := sum.Report.(PublishReview)
	if !withBase || pr.Page.Base == "" || pr.Page.Conflicts != 1 || !strings.HasSuffix(sum.Headline, "; 1 changed on production too") {
		t.Fatalf("with a base: %v %q %d %q", withBase, pr.Page.Base, pr.Page.Conflicts, sum.Headline)
	}
	if _, missing := DefaultDecisions(pr.Page); len(missing) != 1 || !strings.Contains(missing[0], "Textarea") {
		t.Errorf("a value both sides changed isn't decided for the person: %v", missing)
	}

	// Production moved since the review: refused, said in the person's words.
	prod.refuse = &live.Refusal{Code: "publish_moved", Message: "1 of the records changed since the review"}
	d, _ := pr.Page.Decide([]string{"post:page:about"}, map[string]map[string]string{"post:page:about": {"fields.f_textarea": "take"}})
	_, err = pr.Apply(d).Run(context.Background(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "publish again (p)") {
		t.Errorf("moved: %v", err)
	}
	if h := a.PublishHistory("ls-mxico"); len(h) != 1 || h[0].State != "failed" {
		t.Errorf("history %+v", h)
	}
	if req := prod.applied[0]; strings.Join(req.Take["post:page:about"], ",") != "fields.f_textarea" || strings.Join(req.Keep["post:page:about"], ",") != "fields.f_url" {
		t.Errorf("decisions sent: keep %v take %v", req.Keep, req.Take)
	}
}

func TestPublishBrowserCancel(t *testing.T) {
	a, _, prod, s, th := publishSetup(t)
	a.open.Exec = &exec.FakeRunner{} // "open" the review: nothing to see in a test
	task, _ := a.PublishTask(s, th, nil)
	sum, _ := task.Run(context.Background(), io.Discard)
	pr := sum.Report.(PublishReview)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the person closed the task: the review ends, nothing published
	sum, err := pr.Browser.Run(ctx, io.Discard)
	if err != nil || sum.Headline != "Review closed: nothing was published" || len(prod.applied) != 0 {
		t.Errorf("%q %v %d", sum.Headline, err, len(prod.applied))
	}
	html, _ := os.ReadFile(filepath.Join(pr.Folder, "review.html"))
	if !strings.Contains(string(html), "<h1>Not published") {
		t.Error("the review is kept as cancelled")
	}
}

func TestPublishUndo(t *testing.T) {
	a, _, prod, s, _ := publishSetup(t)
	task, err := a.PublishUndoTask(s, "")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := task.Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	up := sum.Report.(UndoPreview)
	if !strings.Contains(sum.Headline, "put back 4 values and delete 1 record it created") || len(sum.Lines) != 1 || !up.Apply.Writes {
		t.Errorf("preview %q %v", sum.Headline, sum.Lines)
	}
	if !strings.Contains(up.Apply.Ask, "values edited since stay") {
		t.Errorf("ask %q", up.Apply.Ask)
	}
	if _, err := up.Apply.Run(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(prod.undone) != 2 || !prod.undone[0] || prod.undone[1] {
		t.Errorf("a dry run, then the undo: %v", prod.undone)
	}
}

func TestPublishRefusals(t *testing.T) {
	a, _, _, s, th := publishSetup(t)
	a.Publisher = nil
	if _, err := a.PublishTask(s, th, nil); err == nil || !strings.Contains(err.Error(), "live key import") {
		t.Errorf("no production access: %v", err)
	}
	a.Config.Sites = nil
	if _, err := a.PublishTask(s, th, nil); err == nil || !strings.Contains(err.Error(), "no production URL") {
		t.Errorf("no production: %v", err)
	}
}

// A new file goes up only with a ticked record that uses it (taw/core 1.99.1).
func TestPublishUploadsOnlyTheFilesItsRecordsUse(t *testing.T) {
	a, _, prod, s, th := publishSetup(t)
	task, _ := a.PublishTask(s, th, nil)
	sum, err := task.Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	pr := sum.Report.(PublishReview)
	d, missing := pr.Page.Decide([]string{"post:page:about"}, nil)
	if len(missing) != 0 {
		t.Fatal(missing)
	}
	apply := pr.Apply(d)
	if !strings.Contains(apply.Ask, "1 file goes up with it.") {
		t.Errorf("ask %q", apply.Ask)
	}
	if _, err := apply.Run(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(prod.files) != 1 || string(prod.files[0]) != "bytes of 2026/09/fresh.jpg" {
		t.Errorf("only about's picture goes up: %q", prod.files)
	}
}

func TestPublishRefusesATawCoreThatPublishesEveryPicture(t *testing.T) {
	a, _, prod, s, th := publishSetup(t)
	prod.plan.TawCore = "1.99.0"
	task, _ := a.PublishTask(s, th, nil)
	if _, err := task.Run(context.Background(), io.Discard); err == nil || !strings.Contains(err.Error(), "taw/core 1.99.0") || !strings.Contains(err.Error(), "1.99.1 or newer") {
		t.Errorf("refused, with what to do: %v", err)
	}
	prod.plan.TawCore = "1.99.1"
	task, _ = a.PublishTask(s, th, nil)
	if _, err := task.Run(context.Background(), io.Discard); err != nil {
		t.Errorf("1.99.1 publishes: %v", err)
	}
}

// A featured image named by its path (taw/core 1.100) isn't sent to a
// production that would look it up by name.
func TestPublishRefusesATawCoreThatFindsFeaturedImagesByName(t *testing.T) {
	a, _, prod, s, th := publishSetup(t)
	prod.plan.TawCore = "1.99.2"
	prod.plan.Plan.Records = append(prod.plan.Plan.Records, companion.PlanRecord{Key: "post:post:a", Kind: "post", Type: "post", Slug: "a", Title: "A", Op: "update",
		Changes: map[string]companion.Change{"featured_media": {Status: "changed", Old: json.RawMessage(`"g.jpg"`), New: json.RawMessage(`"2026/06/g.jpg"`)}}})
	task, _ := a.PublishTask(s, th, nil)
	if _, err := task.Run(context.Background(), io.Discard); err == nil || !strings.Contains(err.Error(), "taw/core 1.99.2") || !strings.Contains(err.Error(), "1.100.0 or newer") {
		t.Errorf("refused, with what to do: %v", err)
	}
	prod.plan.TawCore = "1.100.0"
	task, _ = a.PublishTask(s, th, nil)
	if _, err := task.Run(context.Background(), io.Discard); err != nil {
		t.Errorf("1.100.0 publishes: %v", err)
	}
}

// When production needs a file taw-fleet's check didn't tie to a ticked
// record, it sends the file and applies again, once, and says so.
func TestPublishSendsWhatProductionStillNeeds(t *testing.T) {
	a, _, prod, s, th := publishSetup(t)
	prod.plan.TawCore = "1.99.1"
	prod.refuse, prod.once = &live.Refusal{Code: "publish_media_missing", Missing: []string{"2025/03/photo.jpg"}}, true
	task, _ := a.PublishTask(s, th, nil)
	sum, err := task.Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	pr := sum.Report.(PublishReview)
	d, _ := pr.Page.Decide([]string{"post:page:about"}, nil)
	sum, err = pr.Apply(d).Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(prod.files) != 2 || string(prod.files[1]) != "bytes of 2025/03/photo.jpg" || len(prod.applied) != 2 || prod.applied[1].Token != "t0k3n" {
		t.Errorf("the missing file, then apply again: %q, %d applies", prod.files, len(prod.applied))
	}
	if !slices.ContainsFunc(sum.Lines, func(l string) bool { return strings.Contains(l, "production also needed 2025/03/photo.jpg") }) {
		t.Errorf("says so: %v", sum.Lines)
	}

	// Without knowing production's taw/core, it doesn't guess.
	a, _, prod, s, th = publishSetup(t)
	prod.refuse = &live.Refusal{Code: "publish_media_missing", Missing: []string{"2025/03/photo.jpg"}}
	task, _ = a.PublishTask(s, th, nil)
	sum, _ = task.Run(context.Background(), io.Discard)
	pr = sum.Report.(PublishReview)
	d, _ = pr.Page.Decide([]string{"post:page:about"}, nil)
	if _, err := pr.Apply(d).Run(context.Background(), io.Discard); err == nil || !strings.Contains(err.Error(), "older than 1.99.1") || len(prod.applied) != 1 {
		t.Errorf("refused, once: %v (%d applies)", err, len(prod.applied))
	}
}
