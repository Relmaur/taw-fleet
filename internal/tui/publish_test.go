package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/config"
)

func TestPublishOpensTheReviewAndUndoAsks(t *testing.T) {
	f := &fakeActions{}
	m := withActions(t, 24, f)
	next, cmd := m.Update(keyMsg("p"))
	m = drain(t, next.(Model), cmd)
	if m.task == nil || m.task.title != "Publish review: acme-shop → acme.mx" || !m.task.running {
		t.Fatal("the comparison is followed at once by the review (no question: the browser's Publish is the yes)")
	}
	m = drain(t, m, listen(m.task.ch))
	if f.worked != "published acme-shop" || !strings.Contains(screen(m), "Published to acme.mx") {
		t.Errorf("p compares, then the review (the browser's yes) publishes (%q):\n%s", f.worked, screen(m))
	}

	f.worked = ""
	m = press(t, m, "esc")
	next, cmd = m.Update(keyMsg("x"))
	m = drain(t, next.(Model), cmd)
	if f.worked != "" || !strings.Contains(m.confirm, "Undo the publish of today on acme.mx?") {
		t.Fatalf("x previews and asks first (%q, %q)", f.worked, m.confirm)
	}
	next, cmd = m.Update(keyMsg("y"))
	drain(t, next.(Model), cmd)
	if f.worked != "undone acme-shop" {
		t.Errorf("y undoes: %q", f.worked)
	}
}

func TestStopWaitingOnTheReview(t *testing.T) {
	f := &fakeActions{waitReview: true}
	m := withActions(t, 24, f)
	next, cmd := m.Update(keyMsg("p"))
	m = drain(t, next.(Model), cmd)
	if m.task == nil || !m.task.running || m.task.stop == nil {
		t.Fatal("the review waits on the person and can be stopped")
	}
	if !strings.Contains(screen(m), "x stop waiting") {
		t.Errorf("the output view offers x:\n%s", screen(m))
	}
	m = press(t, m, "x")
	m = drain(t, m, listen(m.task.ch))
	if m.task.running || !strings.Contains(screen(m), "Review closed: nothing was published") || f.worked != "" {
		t.Errorf("x stops the wait, nothing published (%q):\n%s", f.worked, screen(m))
	}
}

func TestPublishedSection(t *testing.T) {
	rep := fixtureReport()
	m := newModel(t, 120, 60, &rep)
	f := &fakeActions{published: []actions.Published{
		{When: time.Date(2026, 10, 13, 9, 30, 0, 0, time.Local), Folder: "/Users/me/TAW Fleet/sites/acme-shop/publish/2026-10-13-093000", State: "published", Updated: 3, Created: 1},
		{When: time.Date(2026, 10, 12, 18, 0, 0, 0, time.Local), Folder: "/x", State: "cancelled"},
	}}
	m.deps.Actions = f
	m.deps.Paths.Home = "/Users/me"
	m.deps.Settings = func(slug string) config.Site {
		if slug == "acme-shop" {
			return config.Site{ProductionURL: "https://acme.mx"}
		}
		return config.Site{}
	}
	m = step(t, m, m.loadPublishes()())
	m = press(t, m, "enter")
	sc := screen(m)
	for _, want := range []string{"PUBLISHED", "p · publish · x · undo the last", "13 Oct 09:30  published: 3 updated, 1 created",
		"~/TAW Fleet/sites/acme-shop/publish/2026-10-13-093000/review.html", "cancelled in the review"} {
		if !strings.Contains(sc, want) {
			t.Errorf("lacks %q:\n%s", want, sc)
		}
	}
}
