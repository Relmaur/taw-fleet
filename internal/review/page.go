package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/companion"
)

// Page is what the review shows. Build makes it from production's plan.
type Page struct {
	Site       string `json:"site"`       // the site's folder
	Name       string `json:"name"`       // the site's name
	Production string `json:"production"` // its production URL
	Made       string `json:"made"`       // when the plan was made, for people
	// State: review (waiting for the person), publishing, published, cancelled, failed.
	State string `json:"state"`
	// Base: what the comparison started from ("" = none: no telling who changed what).
	Base      string   `json:"base"`
	Groups    []Group  `json:"groups"`
	Files     []File   `json:"files"`
	Warnings  []string `json:"warnings"`
	Records   int      `json:"records"`
	Values    int      `json:"values"`
	Conflicts int      `json:"conflicts"`
	// MediaBase is where Local's files load from: "media/" served by the
	// review, or a file:// folder in the saved copy.
	MediaBase string `json:"media_base"`
	// After the publish (the saved copy): what was decided and what happened.
	Decisions *Decisions `json:"decisions,omitempty"`
	Result    *Result    `json:"result,omitempty"`
}

// Group is records of one kind: pages, posts, a custom type, menus…
type Group struct {
	Title   string   `json:"title"`
	Records []Record `json:"records"`
}

// Record is one page, post, term, option or menu the publish would change.
type Record struct {
	Key       string   `json:"key"`
	Hash      string   `json:"hash"`
	Title     string   `json:"title"`
	What      string   `json:"what"` // "page · about/team"
	Op        string   `json:"op"`   // new, update, delete
	EditURL   string   `json:"edit_url,omitempty"`
	Include   bool     `json:"include"` // ticked by default (a delete isn't)
	Conflicts int      `json:"conflicts"`
	Changes   []Change `json:"changes"`
}

// Change is one value: production's now and Local's, how they differ, and
// whether production changed it since the base.
type Change struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Context string `json:"context,omitempty"` // the fieldset, options page or block it's in
	Status  string `json:"status"`            // new, changed
	// Kind: short (a word diff), long (a line diff), media (pictures), empty.
	Kind string `json:"kind"`
	Old  string `json:"old,omitempty"`
	New  string `json:"new,omitempty"`
	Base string `json:"base,omitempty"`
	// Spans is a short value's word diff; Rows a long one's line diff.
	Spans    []Span      `json:"spans,omitempty"`
	Rows     []Row       `json:"rows,omitempty"`
	OldMedia []MediaView `json:"old_media,omitempty"`
	NewMedia []MediaView `json:"new_media,omitempty"`
	// Conflict: "" none, "yes" production changed it since the base, "unknown" no telling.
	Conflict     string `json:"conflict,omitempty"`
	LocalChanged bool   `json:"local_changed"`
	// Default side for a conflict: keep (production's), take (Local's), "" = the person picks.
	Default string `json:"default,omitempty"`
}

// MediaView is one picture or file on one side.
type MediaView struct {
	Name     string `json:"name"`
	Src      string `json:"src,omitempty"` // a URL, or Local's path under uploads (Incoming)
	Incoming bool   `json:"incoming,omitempty"`
	Image    bool   `json:"image"`
}

// File is one file the publish uploads.
type File struct {
	Path  string `json:"path"` // under uploads
	Name  string `json:"name"`
	Size  string `json:"size"`
	Image bool   `json:"image"`
}

// Decisions are what the person chose: the records to publish (with the
// hash each had), and per conflicting value which side wins.
type Decisions struct {
	Approved []companion.Approval `json:"approved"`
	Keep     map[string][]string  `json:"keep"`
	Take     map[string][]string  `json:"take"`
}

// Result is what the publish did, for the saved copy.
type Result struct {
	Created, Updated, Deleted, Failed int
	Files                             int
	Journal                           string
	Warnings                          []string
	Error                             string
}

// shortLimit is the longest one-line value shown as a word diff.
const shortLimit = 400

// Build makes the review from production's plan.
func Build(siteSlug, name, production, made, base string, plan companion.PublishPlan) Page {
	p := Page{Site: siteSlug, Name: name, Production: production, Made: made, State: "review", Base: base, MediaBase: "media/",
		Warnings: append(append([]string{}, plan.Plan.RegistryDrift...), plan.Plan.Warnings...)}
	for _, f := range plan.Media.Needed {
		p.Files = append(p.Files, File{Path: f.File, Name: path.Base(f.File), Size: size(f.Bytes), Image: isImage(f.File)})
	}
	groups := map[string]*Group{}
	var order []string
	for _, r := range plan.Plan.Records {
		if r.Kind == "media" {
			continue // files go up with the records that use them
		}
		rec := record(r, base != "")
		if len(rec.Changes) == 0 && rec.Op != "delete" {
			continue
		}
		title := groupTitle(r)
		g, ok := groups[title]
		if !ok {
			g = &Group{Title: title}
			groups[title] = g
			order = append(order, title)
		}
		g.Records = append(g.Records, rec)
		p.Records++
		p.Values += len(rec.Changes)
		p.Conflicts += rec.Conflicts
	}
	sort.SliceStable(order, func(i, j int) bool { return groupRank(order[i]) < groupRank(order[j]) })
	for _, t := range order {
		g := *groups[t]
		// What needs a decision first, then new records, then the rest as planned.
		sort.SliceStable(g.Records, func(i, j int) bool {
			a, b := g.Records[i], g.Records[j]
			if (a.Conflicts > 0) != (b.Conflicts > 0) {
				return a.Conflicts > 0
			}
			return a.Op == "new" && b.Op != "new"
		})
		p.Groups = append(p.Groups, g)
	}
	return p
}

func record(r companion.PlanRecord, withBase bool) Record {
	rec := Record{Key: r.Key, Hash: r.Hash, Title: r.Title, EditURL: r.EditURL, Include: true}
	switch {
	case r.Op == "create":
		rec.Op = "new"
	case strings.Contains(r.Op, "delete"):
		rec.Op, rec.Include = "delete", false // never by default: it removes a page from production
	default:
		rec.Op = "update"
	}
	where := r.Path
	if where == "" {
		where = r.Slug
	}
	switch r.Kind {
	case "post":
		rec.What = strings.TrimSpace(typeName(r.Type) + " · " + where)
	case "term":
		rec.What = r.Type + " · " + r.Slug
	default:
		rec.What = r.Name
	}
	if rec.Title == "" {
		rec.Title = rec.What
	}
	keys := make([]string, 0, len(r.Changes))
	for k := range r.Changes {
		keys = append(keys, k)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		return changeRank(keys[i]) < changeRank(keys[j]) || (changeRank(keys[i]) == changeRank(keys[j]) && keys[i] < keys[j])
	})
	for _, k := range keys {
		c := change(k, r.Changes[k], rec.Op == "new" || !withBase)
		if c.Conflict != "" && c.Default == "" {
			rec.Conflicts++
		}
		rec.Changes = append(rec.Changes, c)
	}
	return rec
}

// change makes one value's view. noConflict: a record new on production, or
// a publish with no base (nothing to tell conflicts by).
func change(key string, c companion.Change, noConflict bool) Change {
	out := Change{Key: key, Label: c.Label, Status: c.Status, LocalChanged: c.LocalChanged == nil || *c.LocalChanged}
	if out.Label == "" {
		out.Label = key
	}
	switch {
	case c.MetaboxTitle != "":
		out.Context = c.MetaboxTitle
	case c.OptionPage != "":
		out.Context = c.OptionPage
	}
	if c.BlockID != "" && !strings.EqualFold(c.BlockID, c.MetaboxTitle) {
		out.Context = strings.Trim(out.Context+" · block "+c.BlockID, " ·")
	}
	if !noConflict {
		switch {
		case c.Conflict == nil:
			out.Conflict = "unknown"
		case *c.Conflict:
			out.Conflict = "yes"
		}
		if out.Conflict == "yes" && !out.LocalChanged {
			out.Default = "keep" // only production changed it: it stays unless the person says otherwise
		}
	}
	out.Old, out.New, out.Base = display(c.Old), display(c.New), display(c.Base)
	switch {
	case c.Media != nil:
		out.Kind = "media"
		out.OldMedia = mediaViews(c.Media.Old)
		out.NewMedia = mediaViews(c.Media.New)
	case out.Old == "" && out.New == "":
		out.Kind = "empty"
	case isShort(out.Old) && isShort(out.New):
		out.Kind = "short"
		out.Spans = Words(out.Old, out.New)
	default:
		out.Kind = "long"
		out.Rows = Lines(out.Old, out.New, 2)
	}
	return out
}

func mediaViews(refs []companion.MediaRef) []MediaView {
	var out []MediaView
	for _, r := range refs {
		name := path.Base(r.File)
		if r.File == "" {
			name = path.Base(r.URL)
		}
		v := MediaView{Name: name, Incoming: r.Incoming, Image: isImage(name)}
		if r.Incoming {
			v.Src = r.File
		} else {
			v.Src = r.URL
		}
		out = append(out, v)
	}
	return out
}

// display is a value as people read it: text as it is, numbers and
// booleans as written, lists and rows as indented JSON; empty as "".
func display(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` || string(raw) == "[]" || string(raw) == "{}" || string(raw) == "false" {
		return ""
	}
	var s string
	if raw[0] == '"' && json.Unmarshal(raw, &s) == nil {
		return s
	}
	if raw[0] == '[' || raw[0] == '{' {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			if b, err := json.MarshalIndent(v, "", "  "); err == nil {
				return string(b)
			}
		}
	}
	return string(raw)
}

func isShort(s string) bool { return len(s) <= shortLimit && !strings.Contains(s, "\n") }

func isImage(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".svg":
		return true
	}
	return false
}

func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", (n+512)>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// typeName is a post type as people say it.
func typeName(t string) string {
	switch t {
	case "page":
		return "page"
	case "post":
		return "post"
	case "wp_block":
		return "reusable block"
	case "wp_navigation":
		return "navigation menu"
	}
	return strings.ReplaceAll(strings.TrimPrefix(t, "taw_"), "_", " ")
}

func groupTitle(r companion.PlanRecord) string {
	switch r.Kind {
	case "post":
		switch r.Type {
		case "page":
			return "Pages"
		case "post":
			return "Posts"
		}
		name := typeName(r.Type)
		return strings.ToUpper(name[:1]) + name[1:] + "s"
	case "term":
		return "Categories and tags"
	case "option":
		return "Site options"
	case "menu":
		return "Menus"
	case "theme_mod":
		return "Logo and menu places"
	}
	return "Other"
}

func groupRank(title string) int {
	switch title {
	case "Pages":
		return 0
	case "Posts":
		return 1
	case "Menus":
		return 7
	case "Categories and tags":
		return 8
	case "Site options":
		return 9
	case "Logo and menu places":
		return 10
	}
	return 5
}

// changeRank puts a record's own values first (title, then content), then
// its fields, then the rest.
func changeRank(key string) int {
	switch {
	case key == "title" || key == "name":
		return 0
	case key == "content" || key == "excerpt" || key == "description":
		return 1
	case strings.HasPrefix(key, "fields."):
		return 2
	case key == "featured_media":
		return 3
	case strings.HasPrefix(key, "terms."):
		return 4
	}
	return 5
}

// Decide checks the person's decisions against the page: every approved
// record is one the page shows, with its hash; every conflict without a
// default in an approved record is decided. It returns the decisions with
// the defaults filled in, or what's missing.
func (p Page) Decide(include []string, sides map[string]map[string]string) (Decisions, []string) {
	d := Decisions{Keep: map[string][]string{}, Take: map[string][]string{}}
	in := map[string]bool{}
	for _, k := range include {
		in[k] = true
	}
	var missing []string
	for _, g := range p.Groups {
		for _, r := range g.Records {
			if !in[r.Key] {
				continue
			}
			d.Approved = append(d.Approved, companion.Approval{Key: r.Key, Hash: r.Hash})
			for _, c := range r.Changes {
				if c.Conflict == "" {
					continue
				}
				side := sides[r.Key][c.Key]
				if side == "" {
					side = c.Default
				}
				switch side {
				case "keep":
					d.Keep[r.Key] = append(d.Keep[r.Key], c.Key)
				case "take":
					d.Take[r.Key] = append(d.Take[r.Key], c.Key)
				default:
					missing = append(missing, r.Title+" › "+c.Label)
				}
			}
			delete(in, r.Key)
		}
	}
	for k := range in {
		missing = append(missing, "unknown record "+k)
	}
	sort.Strings(missing)
	return d, missing
}
