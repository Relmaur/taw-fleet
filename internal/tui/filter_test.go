package tui

import (
	"strings"
	"testing"
)

func TestFilterTerms(t *testing.T) {
	bistro := filterFacts{text: []string{"bistro", "bistro.mx", "bistro-theme", "classic", "behind", "dirty"},
		is: map[string]bool{"behind": true, "dirty": true, "live": true, "comments": true}, kind: "classic", core: "1.59.2"}
	acme := filterFacts{text: []string{"acme-shop", "acme.mx", "acme", "gutenberg", "block"},
		is: map[string]bool{"live": true, "prs": true}, kind: "block", core: "1.98.0"}
	cases := []struct {
		q            string
		bistro, acme bool
	}{
		{"", true, true},
		{"bistro", true, false},
		{"mx", true, true},
		{"mx -bistro", false, true},           // every word must match; - leaves out
		{"is:behind", true, false},            // a condition
		{"is:beh", true, false},               // its start, while typed
		{"-is:behind", false, true},           // left out
		{"is:live has:comments", true, false}, // both
		{"has:prs", false, true},
		{"kind:block", false, true},
		{"kind:classic is:dirty", true, false},
		{"core:<1.95", true, false},
		{"core:>=1.98", false, true},
		{"core:1.98", false, true}, // a prefix: every 1.98.x
		{"core:1.5", false, false}, // not 1.59.2
		{"core:=1.59.2", true, false},
		{"is:", true, true}, // still being typed: ignored
	}
	for _, c := range cases {
		terms, problems := parseFilter(c.q)
		if len(problems) > 0 {
			t.Errorf("%q: problems %v", c.q, problems)
		}
		all := func(f filterFacts) bool {
			for _, term := range terms {
				if !term.matches(f) {
					return false
				}
			}
			return true
		}
		if got := all(bistro); got != c.bistro {
			t.Errorf("%q on bistro: %v", c.q, got)
		}
		if got := all(acme); got != c.acme {
			t.Errorf("%q on acme: %v", c.q, got)
		}
	}
}

func TestFilterSaysWhatItDidntUnderstand(t *testing.T) {
	terms, problems := parseFilter("bistro is:sleepy colour:red core:<abc")
	if len(terms) != 1 || terms[0].value != "bistro" {
		t.Errorf("terms %+v", terms)
	}
	if h := filterHint(problems); h != "not understood: colour:red, core:<abc, is:sleepy" {
		t.Errorf("hint %q", h)
	}
}

func TestFilterInTheDashboard(t *testing.T) {
	m := withActions(t, 20, &fakeActions{})
	m = press(t, m, "/")
	m = typeText(t, m, "is:behind is:nope")
	sc := screen(m)
	if !strings.Contains(sc, "1 of 4") || !strings.Contains(sc, "not understood: is:nope") || !strings.Contains(sc, "bistro") {
		t.Errorf("filter:\n%s", sc)
	}
	m = press(t, m, "enter")
	if !strings.Contains(screen(m), "/ is:behind is:nope  ·  1 of 4  ·  esc clears") {
		t.Errorf("after enter:\n%s", screen(m))
	}
}
