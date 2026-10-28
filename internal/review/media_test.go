package review

import (
	"reflect"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/companion"
)

// The cases ChangeSet::referencedMedia matches (taw/core 1.99.1): IDs in
// fields and an option's value, block attributes and classes, a file name,
// and URLs with size variants and -scaled; never from a delete.
func TestMediaUsers(t *testing.T) {
	cs := `{"media":[
	  {"id":10,"filename":"a.jpg","url":"http://x.local/wp-content/uploads/2026/09/a.jpg"},
	  {"id":11,"filename":"b.webp","url":"http://x.local/wp-content/uploads/2026/09/b-scaled.webp"},
	  {"id":12,"ref":"c.png","url":"http://x.local/wp-content/uploads/2026/09/c.png?v=2"},
	  {"id":13,"filename":"d.avif","url":"http://x.local/wp-content/uploads/2026/09/d.avif"},
	  {"id":14,"filename":"e.jpg","url":"http://x.local/wp-content/uploads/2026/09/e.jpg"},
	  {"id":15,"filename":"f.jpg"}],
	 "operations":[
	  {"op":"update","target":{"kind":"post","type":"page","slug":"home","path":"home"},"post":{"content":"<!-- wp:image {\"id\":10} -->"}},
	  {"op":"create","target":{"kind":"post","type":"post","slug":"news"},"post":{"content":"<img src=\"http://x.local/wp-content/uploads/2026/09/b-300x200.webp\">"}},
	  {"op":"update","target":{"kind":"post","type":"page","slug":"team","path":"about/team"},"post":{"fields":{"gallery":"12"},"featured":"f.jpg"}},
	  {"op":"create","target":{"kind":"option","key":"site_icon"},"fields":{"value":13}},
	  {"op":"delete","target":{"kind":"post","type":"page","slug":"old"},"post":{"content":"wp-image-14"}},
	  {"op":"update","target":{"kind":"post","type":"post","slug":"","match_key":"draft-7"},"post":{"content":"<p>a.jpg2 and e.jpg-ish</p>","title":"14"}}]}`
	got := MediaUsers([]byte(cs))
	want := map[string][]string{
		"media:2026/09/a.jpg":         {"post:page:home"},
		"media:2026/09/b-scaled.webp": {"post:post:news"},
		"media:2026/09/c.png":         {"post:page:about/team"},
		"media:2026/09/d.avif":        {"option:site_icon"},
		"media:f.jpg":                 {"post:page:about/team"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("users\n got %v\nwant %v", got, want)
	}
	if MediaUsers([]byte("not json")) != nil {
		t.Error("a change-set it can't read: nil")
	}
}

// A featured image named by its path (taw/core 1.100, schema 1.9) uses that
// file, not another of the same name in another month folder.
func TestFeaturedImageByPath(t *testing.T) {
	cs := `{"media":[
	  {"id":20,"filename":"g.jpg","url":"http://x.local/wp-content/uploads/2026/06/g.jpg"},
	  {"id":21,"filename":"g.jpg","url":"http://x.local/wp-content/uploads/2026/08/g.jpg"},
	  {"id":22,"filename":"h.jpg","url":"https://cdn.example/h.jpg","file":"2026/07/h.jpg"}],
	 "operations":[
	  {"op":"update","target":{"kind":"post","type":"post","slug":"a"},"post":{"featured_media":"2026/06/g.jpg"}},
	  {"op":"update","target":{"kind":"post","type":"post","slug":"b"},"post":{"featured_media":"2026/07/h.jpg"}}]}`
	got := MediaUsers([]byte(cs))
	want := map[string][]string{
		"media:2026/06/g.jpg": {"post:post:a"},
		"media:h.jpg":         {"post:post:b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("users\n got %v\nwant %v", got, want)
	}
}

func TestPicturesDetailsAreTheirOwnChoice(t *testing.T) {
	pl, no := plan(t), false
	pl.Plan.Records = append(pl.Plan.Records, companion.PlanRecord{Key: "media:2026/09/old.webp", Hash: "h1", Kind: "media", Name: "2026/09/old.webp",
		Title: "2026/09/old.webp", Op: "update", Changes: map[string]companion.Change{"alt": {Status: "new", New: []byte(`"A team photo"`), Label: "Alt text", Conflict: &no}}})
	users := map[string][]string{"media:2026/09/fresh.jpg": {"post:page:about"}}
	p := Build("fid-b", "Fidelity B", "https://b.test", "now", "base", pl, users)

	last := p.Groups[len(p.Groups)-1]
	if last.Title != "Pictures and files" || last.Note == "" || len(last.Records) != 1 {
		t.Fatalf("pictures' details last, in their own group: %+v", last)
	}
	r := last.Records[0]
	if r.Include || r.Title != "old.webp" || r.File != "2026/09/old.webp" || r.Key != "media:2026/09/old.webp" {
		t.Errorf("a picture's details: unticked, by its file: %+v", r)
	}
	var fresh, photo File
	for _, f := range p.Files {
		switch f.Path {
		case "2026/09/fresh.jpg":
			fresh = f
		case "2025/03/photo.jpg":
			photo = f
		}
	}
	if fresh.Key != "media:2026/09/fresh.jpg" || fresh.Hash == "" || !reflect.DeepEqual(fresh.UsedBy, []string{"post:page:about"}) || len(fresh.Users) != 1 {
		t.Errorf("a new file knows what uses it: %+v", fresh)
	}
	if len(photo.UsedBy) != 0 {
		t.Errorf("nothing uses photo.jpg here: %+v", photo)
	}
	if g := p.Going(); len(g) != 1 || g[0].Path != "2026/09/fresh.jpg" {
		t.Errorf("by default, only the file a ticked record uses goes: %+v", g)
	}

	d, missing := p.Decide([]string{"post:post:a-story"}, nil)
	if len(missing) != 0 || len(p.FilesFor(d)) != 0 {
		t.Errorf("a record that uses no new file takes none: %v %+v", missing, p.FilesFor(d))
	}
	d, missing = p.Decide([]string{"media:2026/09/old.webp", "media:2025/03/photo.jpg"}, nil)
	if len(missing) != 0 || len(d.Approved) != 2 || d.Approved[1].Hash == "" {
		t.Fatalf("details and a file by their keys: %v %+v", missing, d)
	}
	if f := p.FilesFor(d); len(f) != 1 || f[0].Path != "2025/03/photo.jpg" {
		t.Errorf("a file asked for by its key goes: %+v", f)
	}
}
