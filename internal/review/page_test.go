package review

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/companion"
)

// plan is a real plan production answered (the fidelity suite's publish of
// site A's Local edits to site B, which changed things of its own).
func plan(t *testing.T) companion.PublishPlan {
	t.Helper()
	data, err := os.ReadFile("testdata/plan.json")
	if err != nil {
		t.Fatal(err)
	}
	var p companion.PublishPlan
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func find(t *testing.T, p Page, key string) Record {
	t.Helper()
	for _, g := range p.Groups {
		for _, r := range g.Records {
			if r.Key == key {
				return r
			}
		}
	}
	t.Fatalf("no record %s", key)
	return Record{}
}

func changeOf(t *testing.T, r Record, key string) Change {
	t.Helper()
	for _, c := range r.Changes {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("%s has no change %s", r.Key, key)
	return Change{}
}

func TestBuild(t *testing.T) {
	p := Build("fid-b", "Fidelity B", "https://b.test", "13 Oct 2026, 08:00", "13 Oct 2026, 07:58", plan(t))

	var titles []string
	for _, g := range p.Groups {
		titles = append(titles, g.Title)
	}
	if strings.Join(titles, ",") != "Pages,Posts,Reusable blocks,Fid books" {
		t.Errorf("groups %v", titles)
	}
	if len(p.Files) != 2 || p.Files[1].Path != "2026/09/fresh.jpg" || !p.Files[1].Image || p.Files[1].Size == "" {
		t.Errorf("files %+v", p.Files)
	}

	about := find(t, p, "post:page:about")
	if about.Title != "About" || about.Op != "update" || !about.Include || about.Conflicts != 1 {
		t.Errorf("about %+v", about)
	}
	text := changeOf(t, about, "fields.f_text")
	if text.Label != "Text" || text.Context != "Every field type" || text.Conflict != "" || text.Kind != "short" {
		t.Errorf("f_text %+v", text)
	}
	url := changeOf(t, about, "fields.f_url")
	if url.Conflict != "yes" || url.LocalChanged || url.Default != "keep" {
		t.Errorf("f_url: only production changed it, kept by default: %+v", url)
	}
	area := changeOf(t, about, "fields.f_textarea")
	if area.Conflict != "yes" || !area.LocalChanged || area.Default != "" || area.Base != "Textarea" {
		t.Errorf("f_textarea: both changed it, the person picks: %+v", area)
	}
	img := changeOf(t, about, "fields.f_image")
	if img.Kind != "media" || len(img.NewMedia) != 1 || !img.NewMedia[0].Incoming || img.NewMedia[0].Src != "2026/09/fresh.jpg" || len(img.OldMedia) != 0 {
		t.Errorf("f_image %+v", img)
	}
	home := find(t, p, "post:page:home")
	if c := changeOf(t, home, "content"); c.Kind != "long" || len(c.Rows) == 0 {
		t.Errorf("content is a line diff: %+v", c.Kind)
	}
	if p.Records != 6 || p.Conflicts != 1 {
		t.Errorf("counts %d records, %d to decide", p.Records, p.Conflicts)
	}
}

func TestWithoutABaseNothingIsAConflict(t *testing.T) {
	p := Build("fid-b", "Fidelity B", "https://b.test", "now", "", plan(t))
	if p.Conflicts != 0 || changeOf(t, find(t, p, "post:page:about"), "fields.f_url").Conflict != "" {
		t.Error("no base: no conflicts to decide, a banner says why")
	}
	html, err := p.HTML()
	if err != nil || !bytes.Contains(html, []byte("No base to compare with")) {
		t.Errorf("banner missing (%v)", err)
	}
}

func TestDecide(t *testing.T) {
	p := Build("fid-b", "Fidelity B", "https://b.test", "now", "base", plan(t))

	_, missing := p.Decide([]string{"post:page:about"}, nil)
	if len(missing) != 1 || !strings.Contains(missing[0], "Textarea") {
		t.Errorf("missing %v", missing)
	}
	d, missing := p.Decide([]string{"post:page:about", "post:page:home"}, map[string]map[string]string{"post:page:about": {"fields.f_textarea": "take"}})
	if len(missing) != 0 {
		t.Fatal(missing)
	}
	if len(d.Approved) != 2 || d.Approved[0].Hash == "" {
		t.Errorf("approved %+v", d.Approved)
	}
	if strings.Join(d.Keep["post:page:about"], ",") != "fields.f_url" || strings.Join(d.Take["post:page:about"], ",") != "fields.f_textarea" {
		t.Errorf("keep %v take %v", d.Keep, d.Take)
	}
	if _, missing := p.Decide([]string{"post:page:nope"}, nil); len(missing) != 1 {
		t.Error("an unknown record is refused")
	}
}

func TestHTMLShowsTheChoicesAndTheDiff(t *testing.T) {
	html, err := Build("fid-b", "Fidelity <B>", "https://b.test", "now", "base", plan(t)).HTML()
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{
		"Publish Fidelity &lt;B&gt;",                                    // escaped
		`name="pick:post:page:about:fields.f_url" value="keep" checked`, // only production changed it: kept
		"Changed on production too since your last pull",
		"<ins>, edited in Local</ins>",
		`src="media/2026/09/fresh.jpg"`,
		"Publish to production",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, `value="keep" checked> Keep production's</label>
            <label><input type="radio" name="pick:post:page:about:fields.f_textarea"`) {
		t.Error("a value both sides changed has no default")
	}
}

func TestSavedCopyShowsWhatWasDecided(t *testing.T) {
	p := Build("fid-b", "Fidelity B", "https://b.test", "now", "base", plan(t))
	d, _ := p.Decide([]string{"post:page:about"}, map[string]map[string]string{"post:page:about": {"fields.f_textarea": "take"}})
	p.State, p.Decisions, p.Result, p.MediaBase = "published", &d, &Result{Updated: 1, Files: 1}, "file:///uploads/"
	html, _ := p.HTML()
	s := string(html)
	if !strings.Contains(s, "<b>Published.</b> 0 created, 1 updated, 1 file uploaded") || strings.Contains(s, "<footer") {
		t.Error("the saved copy says what happened, with nothing to press")
	}
	if !strings.Contains(s, `value="take" checked`) || !strings.Contains(s, `class="rec off"`) {
		t.Error("the saved copy shows the decisions: Local's textarea, the records left out")
	}
	if !strings.Contains(s, `src="file:///uploads/2026/09/fresh.jpg"`) {
		t.Error("Local's pictures load from disk in the saved copy")
	}
}

func TestServer(t *testing.T) {
	uploads := t.TempDir()
	if err := os.MkdirAll(filepath.Join(uploads, "2026", "09"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(uploads, "2026", "09", "fresh.jpg"), []byte("jpeg"), 0o644)
	_ = os.WriteFile(filepath.Join(uploads, "secret.txt"), []byte("no"), 0o644)

	page := Build("fid-b", "Fidelity B", "https://b.test", "now", "base", plan(t))
	s, err := Serve(context.Background(), page, uploads)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(0)
	get := func(u string) (int, string) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, body := get(s.URL); code != 200 || !strings.Contains(body, "Publish Fidelity B") {
		t.Fatalf("page %d", code)
	}
	if code, body := get(s.URL + "media/2026/09/fresh.jpg"); code != 200 || body != "jpeg" {
		t.Errorf("a file the plan uses: %d %q", code, body)
	}
	for _, path := range []string{"media/secret.txt", "media/../secret.txt", "media/%2e%2e/secret.txt"} {
		if code, _ := get(s.URL + path); code == 200 {
			t.Errorf("%s served", path)
		}
	}
	if code, _ := get(strings.Replace(s.URL, "/"+s.token+"/", "/nope/", 1)); code == 200 {
		t.Error("served without the token")
	}

	post := func(path, origin string, body any) int {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, s.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if code := post("publish", "https://evil.example", map[string]any{"include": []string{"post:page:home"}}); code != http.StatusForbidden {
		t.Errorf("another origin: %d", code)
	}
	if code := post("publish", "", map[string]any{"include": []string{"post:page:about"}}); code != http.StatusBadRequest {
		t.Errorf("an undecided conflict: %d", code)
	}
	origin := strings.TrimSuffix(strings.TrimSuffix(s.URL, "/"), "/"+s.token)
	if code := post("publish", origin, map[string]any{"include": []string{"post:page:about"}, "sides": map[string]map[string]string{"post:page:about": {"fields.f_textarea": "keep"}}}); code != http.StatusAccepted {
		t.Fatalf("publish: %d", code)
	}
	select {
	case d := <-s.Decisions():
		if d.Cancel || len(d.Decisions.Approved) != 1 || len(d.Decisions.Keep["post:page:about"]) != 2 {
			t.Errorf("decision %+v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("no decision")
	}
	if code := post("cancel", "", nil); code != http.StatusOK {
		t.Errorf("cancel after deciding: %d", code)
	}
	select {
	case d := <-s.Decisions():
		t.Errorf("a second decision got through: %+v", d)
	default:
	}
	s.SetStatus(Status{State: "publishing", Step: "upload", Done: 1, Total: 2})
	if _, body := get(s.URL + "status"); !strings.Contains(body, `"done":1`) {
		t.Errorf("status %s", body)
	}
}

func TestWithin(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.jpg"), []byte("x"), 0o644)
	if _, err := Within(root, "a.jpg"); err != nil {
		t.Error(err)
	}
	outside := filepath.Join(t.TempDir(), "x.txt")
	_ = os.WriteFile(outside, []byte("x"), 0o644)
	_ = os.Symlink(outside, filepath.Join(root, "link.jpg"))
	for _, bad := range []string{"../a.jpg", "/etc/passwd", "link.jpg", "", ".", "sub\\..\\a.jpg"} {
		if _, err := Within(root, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
