package tui

import (
	"errors"
	"strings"
	"testing"
)

func TestMenuTogglesOpenAtLogin(t *testing.T) {
	rep := fixtureReport()
	m := newModel(t, 160, 40, &rep)
	m = press(t, m, ":", "l", "o", "g", "i", "n")
	if out := screen(m); !strings.Contains(out, "Open at login: off") || !strings.Contains(out, "Not now: the settings file doesn't load") {
		t.Fatalf("read-only:\n%s", out)
	}
	m = press(t, m, "esc")

	var saved []bool
	m.deps.SaveOpenAtLogin = func(on bool) error { saved = append(saved, on); return nil }
	m = press(t, m, ":", "l", "o", "g", "i", "n", "enter")
	if !m.openAtLogin || len(saved) != 1 || !saved[0] || !strings.Contains(m.flash, "at your next login") {
		t.Errorf("on: %v %v %q", m.openAtLogin, saved, m.flash)
	}
	m = press(t, m, ":", "l", "o", "g", "i", "n")
	if !strings.Contains(screen(m), "Open at login: on") {
		t.Errorf("menu shows on:\n%s", screen(m))
	}
	m = press(t, m, "enter")
	if m.openAtLogin || len(saved) != 2 || saved[1] {
		t.Errorf("off: %v %v", m.openAtLogin, saved)
	}

	m.deps.SaveOpenAtLogin = func(bool) error { return errors.New("disk full") }
	m = press(t, m, ":", "l", "o", "g", "i", "n", "enter")
	if m.openAtLogin || !strings.Contains(m.flash, "disk full") {
		t.Errorf("a failed save changes nothing: %v %q", m.openAtLogin, m.flash)
	}
}
