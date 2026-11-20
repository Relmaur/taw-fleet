package tui

import (
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/site"
)

func TestMovedSite(t *testing.T) {
	rep := fixtureReport()
	rep.Sites[0].Moved = &site.Moved{To: "Laravel on Forge", URL: "https://acme.example"}
	m := newModel(t, 130, 40, &rep)

	// It sorts after the sites still on WordPress.
	var order []string
	for _, r := range m.rows {
		order = append(order, m.rep.Sites[r.site].Slug)
	}
	if got := strings.Join(order, ","); !strings.HasSuffix(got, ",acme-shop") {
		t.Errorf("order %s", got)
	}
	if sc := screen(m); !strings.Contains(sc, "acme  classic  moved") {
		t.Errorf("card:\n%s", sc)
	}
	if sc := screen(newModel(t, 200, 40, &rep)); !strings.Contains(sc, "acme  classic  moved → Laravel on Forge") {
		t.Errorf("wide card:\n%s", sc)
	}

	m.filter.SetValue("is:moved")
	m.applyFilter()
	if len(m.visible) != 1 {
		t.Fatalf("is:moved: %d cards", len(m.visible))
	}
	sc := screen(m)
	for _, want := range []string{"MOVED", "https://acme.example", "taw-fleet moved acme-shop --clear undoes it", "moved off WordPress: Laravel on Forge"} {
		if !strings.Contains(sc, want) {
			t.Errorf("details lack %q:\n%s", want, sc)
		}
	}
	if strings.Contains(sc, "SETUP") {
		t.Errorf("a moved site has no SETUP checklist:\n%s", sc)
	}
	m.groupBy = "production"
	if g := m.groupsOf(m.rows[m.visible[0]]); len(g) != 1 || g[0] != "\x7emoved" {
		t.Errorf("production group %q", g)
	}
}
