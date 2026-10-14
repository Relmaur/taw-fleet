package review

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Which records use which media, as production decides it (taw/core 1.99.1:
// ChangeSet::referencedMedia, Publish::mediaFor). A new file goes up only
// with a ticked record that uses it; a picture's details (alt, title…) only
// when its own media record is ticked.

var (
	idRE      = regexp.MustCompile(`"(?:id|mediaId)":(\d+)|wp-image-(\d+)|data-id=["'](\d+)`)
	idsRE     = regexp.MustCompile(`"ids":\[([\d,]+)\]`)
	urlPartRE = regexp.MustCompile(`^(.*/[^/]+?)\.([A-Za-z0-9]{1,5})$`)
	uploadsRE = regexp.MustCompile(`/wp-content/uploads/(.+)$`)
)

// MediaUsers maps each media record key ("media:2026/09/a.webp") to the keys
// of the change-set's records that use it. A change-set it can't read gives nil.
func MediaUsers(changeSet []byte) map[string][]string {
	var cs struct {
		Media      []map[string]any `json:"media"`
		Operations []map[string]any `json:"operations"`
	}
	dec := json.NewDecoder(bytes.NewReader(changeSet))
	dec.UseNumber()
	if dec.Decode(&cs) != nil {
		return nil
	}
	users := map[string][]string{}
	for _, op := range cs.Operations {
		if str(op["op"]) == "delete" {
			continue
		}
		key := keyOf(op)
		for _, m := range referenced(cs.Media, op) {
			mk := "media:" + mediaPath(m)
			users[mk] = append(users[mk], key)
		}
	}
	return users
}

// referenced is the media one operation uses (ChangeSet::referencedMedia
// with one operation).
func referenced(media []map[string]any, op map[string]any) []map[string]any {
	ids := map[int64]bool{}
	var strs []string
	var record any
	if p, ok := op["post"].(map[string]any); ok {
		record = p
	} else if f, ok := op["fields"].(map[string]any); ok {
		record = f
	} else if f, ok := op["fields"].([]any); ok {
		record = f
	}
	scan(record, ids, &strs, false)
	text := strings.Join(strs, "\n")
	for _, m := range idRE.FindAllStringSubmatch(text, -1) {
		for _, g := range m[1:] {
			if n, err := strconv.ParseInt(g, 10, 64); err == nil {
				ids[n] = true
			}
		}
	}
	for _, m := range idsRE.FindAllStringSubmatch(text, -1) {
		for _, s := range strings.Split(m[1], ",") {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				ids[n] = true
			}
		}
	}
	names := map[string]bool{}
	for _, s := range strs {
		names[s] = true
	}
	var out []map[string]any
	for _, e := range media {
		id := intOf(e["id"])
		name := str(first(e, "filename", "ref"))
		if (id > 0 && ids[id]) || (name != "" && names[name]) || urlIn(str(e["url"]), text) {
			out = append(out, e)
		}
	}
	return out
}

// scan collects a record's strings, and the integers under fields (and an
// option's value): where an attachment ID is kept.
func scan(v any, ids map[int64]bool, strs *[]string, inFields bool) {
	switch x := v.(type) {
	case string:
		*strs = append(*strs, x)
		if inFields && x != "" && strings.Trim(x, "0123456789") == "" {
			if n, err := strconv.ParseInt(x, 10, 64); err == nil {
				ids[n] = true
			}
		}
	case json.Number:
		if n, err := strconv.ParseInt(string(x), 10, 64); err == nil && inFields {
			ids[n] = true
		}
	case map[string]any:
		for k, item := range x {
			scan(item, ids, strs, inFields || k == "fields" || k == "value")
		}
	case []any:
		for _, item := range x {
			scan(item, ids, strs, inFields)
		}
	}
}

// urlIn reports whether text links to the file at url, a size variant of it
// (-300x200) or its unscaled original (-scaled).
func urlIn(url, text string) bool {
	m := urlPartRE.FindStringSubmatch(url)
	if url == "" || m == nil {
		return url != "" && strings.Contains(text, url)
	}
	base := strings.TrimSuffix(m[1], "-scaled")
	for _, form := range []string{base, strings.ReplaceAll(base, "/", `\/`)} {
		re := regexp.MustCompile(regexp.QuoteMeta(form) + `(-scaled|-\d+x\d+)?\.` + regexp.QuoteMeta(m[2]))
		for _, at := range re.FindAllStringIndex(text, -1) {
			if at[1] == len(text) || !isWordOrDash(text[at[1]]) {
				return true
			}
		}
	}
	return false
}

func isWordOrDash(c byte) bool {
	return c == '_' || c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// mediaPath is a media entry's path under uploads, as its record key has it
// (Publish::mediaFor): from its URL, else its file name.
func mediaPath(e map[string]any) string {
	u := str(e["url"])
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if m := uploadsRE.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return str(first(e, "filename", "ref"))
}

// keyOf is an operation's record key (Importer::keyOf).
func keyOf(op map[string]any) string {
	t, ok := op["target"].(map[string]any)
	if !ok {
		t = op
	}
	kind := str(t["kind"])
	switch kind {
	case "option", "user", "comment", "media", "menu", "theme_mod":
		return kind + ":" + str(t["key"])
	}
	id := str(t["path"])
	if id == "" {
		id = str(t["slug"])
	}
	if id == "" {
		id = str(t["match_key"])
	}
	return kind + ":" + str(t["type"]) + ":" + id
}

// first is the first of keys that's set and not null (PHP's ??).
func first(e map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := e[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return string(x)
	case bool:
		if x {
			return "1"
		}
	}
	return ""
}

func intOf(v any) int64 {
	switch x := v.(type) {
	case json.Number:
		if n, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return n
		}
		if f, err := x.Float64(); err == nil {
			return int64(f)
		}
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	}
	return 0
}
