package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// tagged is the fixture with tags: acme-shop retainer + es + client:acme,
// bistro retainer; saving tags or the grouping is recorded.
func tagged(t *testing.T, h int) (Model, *[]string, map[string][]string) {
	t.Helper()
	rep := fixtureReport()
	rep.Sites[0].Tags = []string{"retainer", "es", "client:acme"}
	rep.Sites[1].Tags = []string{"retainer"}
	m := newModel(t, 100, h, &rep)
	m.deps.Actions = &fakeActions{}
	var groups []string
	saved := map[string][]string{}
	m.deps.SaveGroupBy = func(g string) error { groups = append(groups, g); return nil }
	m.deps.SaveTags = func(slug string, tags []string) error { saved[slug] = tags; return nil }
	return m, &groups, saved
}

func TestGroupByTag(t *testing.T) {
	m, groups, _ := tagged(t, 34)
	m = press(t, m, "=", "2")
	if m.groupBy != "tag" || strings.Join(*groups, ",") != "tag" {
		t.Fatalf("group %q, saved %v", m.groupBy, *groups)
	}
	sc := screen(m)
	for _, want := range []string{"▾ client:acme · 1", "▾ es · 1", "▾ retainer · 2", "▾ untagged · 2", "grouped by tag", "#retainer #es"} {
		if !strings.Contains(sc, want) {
			t.Errorf("lacks %q:\n%s", want, sc)
		}
	}
	if strings.Count(sc, "acme-shop") != 3 { // under each of its three tags
		t.Errorf("acme-shop shows %d times:\n%s", strings.Count(sc, "acme-shop"), sc)
	}
	if strings.Index(sc, "retainer · 2") > strings.Index(sc, "untagged · 2") {
		t.Error("untagged isn't last")
	}
	golden(t, "group-tag-100x34", sc)

	// The cursor starts on the first header: a site's key says to pick a site.
	if !m.onHeader() {
		t.Fatal("not on a header")
	}
	m = press(t, m, "e")
	if !strings.Contains(screen(m), "That's the client:acme group: pick a site in it") {
		t.Errorf("site key on a header:\n%s", screen(m))
	}

	// ← folds it, → opens it, enter does both.
	m = press(t, m, "left")
	if sc = screen(m); !strings.Contains(sc, "▸ client:acme · 1   → opens") || strings.Count(sc, "acme-shop") != 2 {
		t.Errorf("folded:\n%s", sc)
	}
	golden(t, "group-folded-100x34", sc)
	m = press(t, m, "right")
	if !strings.Contains(screen(m), "▾ client:acme · 1") {
		t.Errorf("opened:\n%s", screen(m))
	}
	m = press(t, m, "enter")
	if !m.folded["tag/client:acme"] || m.mode != modeTable {
		t.Errorf("enter on a header: folded %v, mode %v", m.folded, m.mode)
	}

	// Down onto a card: it's a site again.
	m = press(t, m, "right", "down")
	if s, _, ok := m.selectedTheme(); !ok || s.Slug != "acme-shop" {
		t.Errorf("selected %v %v", s.Slug, ok)
	}
}

// = opens a list of the groupings; a digit, or the arrows and enter, picks.
func TestGroupByPopup(t *testing.T) {
	m, groups, _ := tagged(t, 30)
	m = press(t, m, "=")
	sc := screen(m)
	for _, want := range []string{"Group the list by", "▸ 1  ● None", "2  ○ Tag", "3  ○ Kind", "4  ○ Production", "5  ○ Client", "by its client: tags", "enter group"} {
		if !strings.Contains(sc, want) {
			t.Errorf("lacks %q:\n%s", want, sc)
		}
	}
	golden(t, "group-popup-100x30", sc)
	m = press(t, m, "esc")
	if m.popup != nil || m.groupBy != "" || len(*groups) != 0 {
		t.Fatalf("esc changes nothing: %q %v", m.groupBy, *groups)
	}
	m = press(t, m, "=", "down", "down", "enter")
	if m.groupBy != "kind" || m.popup != nil {
		t.Fatalf("arrows and enter: %q", m.groupBy)
	}
	m = press(t, m, "=")
	if !strings.Contains(screen(m), "▸ 3  ● Kind") {
		t.Errorf("opens on the grouping in use:\n%s", screen(m))
	}
	m = press(t, m, "5") // by client
	if strings.Join(*groups, ",") != "kind,prefix:client" {
		t.Errorf("saved %v", *groups)
	}
	if sc := screen(m); !strings.Contains(sc, "▾ acme · 1") || !strings.Contains(sc, "▾ no client · 3") || !strings.Contains(sc, "grouped by client") {
		t.Errorf("by client:\n%s", sc)
	}
}

func TestGroupingOffLeavesTheListAlone(t *testing.T) {
	m, _, _ := tagged(t, 24)
	if m.grouped() || len(m.entries) != len(m.visible) {
		t.Fatalf("entries %d visible %d", len(m.entries), len(m.visible))
	}
	if strings.Contains(screen(m), "▾") {
		t.Errorf("headers while not grouped:\n%s", screen(m))
	}
}

// T (or #) opens the site's tags: the fleet's to tick, new ones typed below.
func TestTagsPopup(t *testing.T) {
	m, _, saved := tagged(t, 24)
	for _, k := range []string{"T", "#"} {
		m = press(t, m, k)
		if m.popup == nil || m.popup.kind != popTags {
			t.Fatalf("%s opens the tags", k)
		}
		m = press(t, m, "esc")
	}
	m = step(t, m, tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt}) // Option+3 on a Spanish keyboard
	sc := screen(m)
	for _, want := range []string{"Tags · acme-shop", "▸ [x] #client:acme", "[x] #es", "[x] #retainer", "2 sites", "new:", "space tick"} {
		if !strings.Contains(sc, want) {
			t.Errorf("lacks %q:\n%s", want, sc)
		}
	}
	m = press(t, m, " ") // untick client:acme
	m = typeText(t, m, "bad!tag")
	m = press(t, m, "enter")
	if !strings.Contains(screen(m), `✗ tag "bad!tag"`) {
		t.Errorf("bad tag:\n%s", screen(m))
	}
	m.tagInput.SetValue("on-hold, #Retainer")
	m = press(t, m, "enter")
	golden(t, "tags-popup-100x24", screen(m))
	if !strings.Contains(screen(m), "[x] #on-hold") || m.tagInput.Value() != "" {
		t.Fatalf("added:\n%s", screen(m))
	}
	m = press(t, m, "enter")
	if m.popup != nil || strings.Join(saved["acme-shop"], " ") != "es on-hold retainer" {
		t.Fatalf("saved %v", saved)
	}
	if sc := screen(m); !strings.Contains(sc, "Tags saved for acme-shop: #es #on-hold #retainer") {
		t.Errorf("after:\n%s", sc)
	}
	// esc leaves them as they are.
	m = press(t, m, "T", " ", "esc")
	if m.popup != nil || len(saved["acme-shop"]) != 3 {
		t.Errorf("esc saved %v", saved)
	}
}

func TestFilterByTag(t *testing.T) {
	m, _, _ := tagged(t, 24)
	for q, want := range map[string]int{"#retainer": 2, "tag:client:": 1, "-#retainer": 2, "es": 1, "#ret": 2, "#nobody": 0} {
		m.filter.SetValue(q)
		m.applyFilter()
		if len(m.visible) != want {
			t.Errorf("%q: %d cards, want %d", q, len(m.visible), want)
		}
	}
}

func TestGroupedListScrolls(t *testing.T) {
	m, _, _ := tagged(t, 14) // room for a few entries only
	m = press(t, m, "=", "2")
	last := len(m.entries) - 1
	for range last {
		m = press(t, m, "down")
		sc := screen(m)
		if !strings.Contains(sc, "▌") {
			t.Fatalf("cursor %d off screen (offset %d):\n%s", m.cursor, m.offset, sc)
		}
	}
	if m.cursor != last || !strings.Contains(screen(m), "of 10") {
		t.Errorf("cursor %d offset %d:\n%s", m.cursor, m.offset, screen(m))
	}
	m = press(t, m, "home")
	if m.cursor != 0 || m.offset != 0 {
		t.Errorf("home: cursor %d offset %d", m.cursor, m.offset)
	}
}
