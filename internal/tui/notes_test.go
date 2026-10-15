package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/notes"
)

func TestNotesSectionAndPopup(t *testing.T) {
	rep := fixtureReport()
	m := newModel(t, 160, 40, &rep)
	saved := map[string][]notes.Entry{
		"acme-shop": {
			{At: now.Add(-2 * time.Hour), By: notes.You, Text: "Client asked to hold the redesign.\nUntil November."},
			{At: now.Add(-50 * time.Hour), By: notes.TawFleet, Text: "Merged #41 (Update taw/core) into main"},
		},
	}
	m.deps.Notes = func(slug string) ([]notes.Entry, error) { return saved[slug], nil }
	var added []string
	m.deps.AddNote = func(slug, text string) error {
		if text == "fail" {
			return errors.New("disk full")
		}
		added = append(added, slug+": "+text)
		saved[slug] = append([]notes.Entry{{At: now, By: notes.You, Text: text}}, saved[slug]...)
		return nil
	}
	m = step(t, m, m.loadNotes()())

	out := screen(press(t, m, "enter"))
	if !strings.Contains(out, "NOTES") || !strings.Contains(out, "you  Client asked to hold the redesign. …") || !strings.Contains(out, "taw-fleet  Merged #41") {
		t.Fatalf("details:\n%s", out)
	}

	m = press(t, m, "N")
	if m.popup == nil || m.popup.kind != popNote || !strings.Contains(screen(m), "Note · acme-shop") {
		t.Fatalf("N opens the popup:\n%s", screen(m))
	}
	m = press(t, m, "enter")
	if m.popup == nil || !strings.Contains(m.popup.err, "type the note first") {
		t.Error("an empty note isn't saved")
	}
	m = press(t, m, "f", "a", "i", "l", "enter")
	if m.popup == nil || m.popup.err != "disk full" {
		t.Errorf("a failed save stays open and says why: %+v", m.popup)
	}
	m.noteInput.SetValue("")
	m = press(t, m, "D", "N", "S", " ", "f", "i", "x", "e", "d")
	next, cmd := m.Update(keyMsg("enter"))
	m = next.(Model)
	if m.popup != nil || len(added) != 1 || added[0] != "acme-shop: DNS fixed" || !strings.Contains(m.flash, "Note added") {
		t.Fatalf("saved: %v popup=%v flash=%q", added, m.popup, m.flash)
	}
	m = step(t, m, cmd())
	if es := m.notes["acme-shop"]; len(es) != 3 || es[0].Text != "DNS fixed" {
		t.Errorf("reloaded: %+v", es)
	}

	// Letters typed in the popup are the note's, not the dashboard's keys.
	m = press(t, m, "N", "q")
	if m.popup == nil || m.noteInput.Value() != "q" {
		t.Errorf("q typed, not quit: %v %q", m.popup, m.noteInput.Value())
	}
	m = press(t, m, "esc")
	if m.popup != nil {
		t.Error("esc closes")
	}
}
