package review

import (
	"bytes"
	_ "embed"
	"html/template"
	"net/url"
	"strings"
)

//go:embed review.html
var pageHTML string

var tmpl = template.Must(template.New("review").Funcs(template.FuncMap{
	"host": func(u string) string {
		p, err := url.Parse(u)
		if err != nil || p.Host == "" {
			return u
		}
		return p.Host
	},
	"plural": func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	},
	"lower": strings.ToLower,
	"clip": func(s string, n int) string {
		r := []rune(strings.Join(strings.Fields(s), " "))
		if len(r) <= n {
			return string(r)
		}
		return string(r[:n-1]) + "…"
	},
	// included: whether a record is ticked (its default, or what was decided).
	"included": func(d *Decisions, r Record) bool {
		if d == nil {
			return r.Include
		}
		for _, a := range d.Approved {
			if a.Key == r.Key {
				return true
			}
		}
		return false
	},
	// side: the side picked for a conflicting value (its default, or what was decided).
	"side": func(d *Decisions, r Record, c Change) string {
		if d == nil {
			return c.Default
		}
		for _, k := range d.Keep[r.Key] {
			if k == c.Key {
				return "keep"
			}
		}
		for _, k := range d.Take[r.Key] {
			if k == c.Key {
				return "take"
			}
		}
		return ""
	},
	// src: where a picture loads from: production's URL (sanitized as any
	// URL), or Local's file, under MediaBase (built here: "media/…" or file://).
	"src": func(p Page, m MediaView) any {
		if !m.Incoming {
			return m.Src
		}
		return local(p, m.Src)
	},
	"local": local,
	"image": isImage,
	"join":  strings.Join,
	// goes: whether a new file goes up (with what's ticked, or was decided).
	"goes": func(p Page, f File) bool {
		for _, g := range p.Going() {
			if g.Key == f.Key {
				return true
			}
		}
		return false
	},
}).Parse(pageHTML))

// local is one of Local's files, by its path under uploads, as the page loads it.
func local(p Page, rel string) template.URL {
	parts := strings.Split(rel, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return template.URL(p.MediaBase + strings.Join(parts, "/")) //nolint:gosec // our own media/ path or file:// folder, the path escaped
}

// HTML is the review as one page: inline styles and script, nothing loaded
// from elsewhere but the pictures.
func (p Page) HTML() ([]byte, error) {
	var b bytes.Buffer
	err := tmpl.Execute(&b, p)
	return b.Bytes(), err
}
