package site

import (
	"errors"
	"testing"
)

func TestParseStatus(t *testing.T) {
	cases := map[string]Status{
		"running":  StatusRunning,
		"RUNNING":  StatusRunning,
		"Halted":   StatusHalted,
		"halted":   StatusHalted,
		"stopped":  StatusHalted,
		"starting": StatusBusy,
		"":         StatusUnknown,
	}
	for in, want := range cases {
		if got := ParseStatus(in); got != want {
			t.Errorf("ParseStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTAWThemes(t *testing.T) {
	s := Site{Themes: []Theme{{Dir: "a", IsTAW: true}, {Dir: "b"}, {Dir: "c", IsTAW: true}}}
	got := s.TAWThemes()
	if len(got) != 2 || got[0].Dir != "a" || got[1].Dir != "c" {
		t.Errorf("TAWThemes = %+v", got)
	}
	if !s.IsTAW() || (Site{}).IsTAW() {
		t.Error("IsTAW wrong")
	}
}

func TestAddError(t *testing.T) {
	var s Site
	s.AddError("themes", nil)
	s.AddError("themes", errors.New("boom"))
	if len(s.Errors) != 1 || s.Errors[0].Err != "boom" {
		t.Errorf("Errors = %+v", s.Errors)
	}
}
