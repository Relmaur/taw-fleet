package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/site"
)

// Groups (=): the list under headers, by tag, kind, production or a tag
// prefix (client: → by client). A site with several tags shows under each.
// The TAW ecosystem is always the last group. Off, the list is the rows alone
// and lays out as before.

// entry is one line of the list: a group's header, or a card (a row).
type entry struct {
	group *group // a header when row < 0
	row   int    // index into m.rows
}

type group struct {
	key   string // what folds it ("tag:retainer")
	title string // what the header says
	count int    // its cards, folded or not
}

func (e entry) isHeader() bool { return e.row < 0 }

// groupModes is what = cycles through: none, tag, kind, production, then a
// prefix for each "<p>:" the fleet's tags use.
func (m Model) groupModes() []string {
	modes := []string{"", "tag", "kind", "production"}
	seen := map[string]bool{}
	for _, s := range m.rep.Sites {
		for _, t := range s.Tags {
			if p, _, ok := strings.Cut(t, ":"); ok && !seen[p] {
				seen[p] = true
			}
		}
	}
	var ps []string
	for p := range seen {
		ps = append(ps, "prefix:"+p)
	}
	sort.Strings(ps)
	return append(modes, ps...)
}

// groupWords is the grouping as people read it.
func groupWords(g string) string {
	switch g {
	case "", "none":
		return ""
	case "production":
		return "production"
	}
	if p, ok := strings.CutPrefix(g, "prefix:"); ok {
		return p
	}
	return g
}

func (m Model) grouped() bool { return groupWords(m.groupBy) != "" }

// groupsOf are the groups a card goes in (several, for a site's tags).
func (m Model) groupsOf(r row) []string {
	s := m.rep.Sites[r.site]
	t := s.Themes[r.theme]
	if ecosystem(t) {
		return []string{"\x7fTAW ecosystem"} // sorts last
	}
	switch g := m.groupBy; {
	case g == "tag":
		if len(s.Tags) == 0 {
			return []string{"\x7euntagged"}
		}
		return s.Tags
	case g == "kind":
		if t.Kind == site.KindGutenberg {
			return []string{"block"}
		}
		return []string{string(t.Kind)}
	case g == "production":
		if m.deps.Production[s.Slug] {
			return []string{"live"}
		}
		return []string{"\x7elocal only"}
	case strings.HasPrefix(g, "prefix:"):
		p := strings.TrimPrefix(g, "prefix:") + ":"
		var out []string
		for _, tag := range s.Tags {
			if v, ok := strings.CutPrefix(tag, p); ok && v != "" {
				out = append(out, v)
			}
		}
		if len(out) == 0 {
			return []string{"\x7eno " + strings.TrimSuffix(p, ":")}
		}
		return out
	}
	return nil
}

// buildEntries lays the visible rows out: as they are, or under headers.
func (m *Model) buildEntries() {
	m.entries = m.entries[:0]
	if !m.grouped() {
		for _, ri := range m.visible {
			m.entries = append(m.entries, entry{row: ri})
		}
		return
	}
	byKey := map[string][]int{}
	var keys []string
	for _, ri := range m.visible {
		for _, k := range m.groupsOf(m.rows[ri]) {
			if _, ok := byKey[k]; !ok {
				keys = append(keys, k)
			}
			byKey[k] = append(byKey[k], ri)
		}
	}
	sort.Strings(keys) // "\x7e" untagged and "\x7f" the ecosystem come last
	for _, k := range keys {
		title := strings.TrimLeft(k, "\x7e\x7f")
		g := &group{key: m.groupBy + "/" + title, title: title, count: len(byKey[k])}
		m.entries = append(m.entries, entry{group: g, row: -1})
		if m.folded[g.key] {
			continue
		}
		for _, ri := range byKey[k] {
			m.entries = append(m.entries, entry{group: g, row: ri})
		}
	}
}

// groupAt is the group of the entry under the cursor.
func (m Model) groupAt() *group {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return nil
	}
	return m.entries[m.cursor].group
}

// fold folds (or opens) the group under the cursor; the cursor stays on it.
func (m *Model) fold(closed bool) {
	g := m.groupAt()
	if g == nil || m.folded[g.key] == closed {
		return
	}
	if m.folded == nil {
		m.folded = map[string]bool{}
	}
	m.folded[g.key] = closed
	m.buildEntries()
	for i, e := range m.entries { // onto the header
		if e.isHeader() && e.group.key == g.key {
			m.cursor = i
			break
		}
	}
	m.clamp()
}

// setGroup groups the list by g (picked in the = popup), saved in the settings.
func (m Model) setGroup(g string) Model {
	m.popup = nil
	m.groupBy = g
	m.cursor, m.offset = 0, 0
	m.buildEntries()
	m.clamp()
	what := "Not grouped."
	if w := groupWords(m.groupBy); w != "" {
		what = "Grouped by " + w + ". ← folds a group, → opens it; = groups another way."
	}
	if m.deps.SaveGroupBy != nil {
		if err := m.deps.SaveGroupBy(m.groupBy); err != nil {
			m.setFlash("grouping: "+err.Error(), true)
			return m
		}
	}
	m.setTip(what)
	return m
}

// entryLines is how many lines an entry takes in the grouped list: a header
// one, a card two; with air, a blank line before each but the first, and none
// between a header and its first card.
func entryLines(e entry, first, afterHeader, air bool) int {
	n := cardLines
	if e.isHeader() {
		n = 1
	}
	if air && !first && !afterHeader {
		n++
	}
	return n
}

// groupedFit is how many entries from `from` fit in the list's room.
func (m Model) groupedFit(from int, air bool) int {
	room, n := m.bodyHeight()-2, 0
	for i := from; i < len(m.entries); i++ {
		room -= entryLines(m.entries[i], i == from, i > 0 && m.entries[i-1].isHeader(), air)
		if room < 0 {
			break
		}
		n++
	}
	return max(n, 1)
}

// groupedAiry reports whether the whole grouped list fits with air.
func (m Model) groupedAiry() bool {
	return len(m.entries) > 0 && m.groupedFit(0, true) == len(m.entries)
}

// headerLine is a group's header in the list.
func (m Model) headerLine(e entry, sel bool, width int) string {
	p := m.pal
	arrow := "▾"
	if m.folded[e.group.key] {
		arrow = "▸"
	}
	title := e.group.title
	color := p.Accent
	if e.group.title == "TAW ecosystem" {
		title = "TAW ECOSYSTEM"
	}
	line := " " + p.Fg(color).Bold(true).Render(arrow+" "+title) + p.Fg(p.Muted).Render(fmt.Sprintf(" · %d", e.group.count))
	if m.folded[e.group.key] {
		line += p.Fg(p.Muted).Render("   → opens")
	}
	if sel {
		return tint(p.Fg(p.Accent).Render("▌")+line[1:], width, p.Selected)
	}
	return line
}
