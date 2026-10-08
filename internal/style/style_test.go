package style

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/site"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestIsDark(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"default", nil, true},
		{"forced light", map[string]string{ThemeEnv: "Light"}, false},
		{"forced dark beats COLORFGBG", map[string]string{ThemeEnv: "dark", "COLORFGBG": "0;15"}, true},
		{"COLORFGBG white bg", map[string]string{"COLORFGBG": "0;15"}, false},
		{"COLORFGBG black bg", map[string]string{"COLORFGBG": "15;0"}, true},
		{"COLORFGBG three parts", map[string]string{"COLORFGBG": "15;default;7"}, false},
		{"COLORFGBG garbage", map[string]string{"COLORFGBG": "x"}, true},
	}
	for _, c := range cases {
		if got := IsDark(env(c.env)); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMarkersHaveText(t *testing.T) {
	p := New(true)
	for st, want := range map[site.Status]string{
		site.StatusRunning: "●", site.StatusHalted: "○", site.StatusBusy: "◐", site.StatusUnknown: "·",
	} {
		if got := p.Dot(st); !strings.Contains(got, want) {
			t.Errorf("Dot(%s) = %q", st, got)
		}
	}
	if !strings.Contains(p.Kind(site.KindGutenberg), "BLOCK") || !strings.Contains(p.Kind(site.KindClassic), "CLASSIC") {
		t.Error("kind badges")
	}
	if !strings.Contains(New(false).StatusText(site.StatusHalted), "halted") {
		t.Error("status text")
	}
}

func TestGitFitKeepsMarkers(t *testing.T) {
	p := New(true)
	g := &site.GitInfo{Branch: "chore/taw-core-1.76.1", DefaultBranch: "main", Dirty: 5}
	got := ansi.Strip(p.GitFit(g, 16))
	if !strings.Contains(got, "±5 ◇") || strings.Contains(got, "unpushed") || ansi.StringWidth(got) > 16 {
		t.Errorf("narrow GitFit = %q (compact markers, branch shortened)", got)
	}
	if wide := ansi.Strip(p.GitFit(g, 40)); wide != "chore/taw-core-1.76.1 ±5 ◇ unpushed" {
		t.Errorf("wide GitFit = %q", wide)
	}
	if !strings.HasPrefix(got, "ch…") && !strings.Contains(got, "…") {
		t.Errorf("branch should be shortened: %q", got)
	}
	if ansi.Strip(p.Git(g)) != "chore/taw-core-1.76.1 ±5 ◇ unpushed" {
		t.Errorf("Git = %q", ansi.Strip(p.Git(g)))
	}
	if ansi.Strip(p.GitFit(nil, 5)) != "no git" {
		t.Error("nil")
	}
}
