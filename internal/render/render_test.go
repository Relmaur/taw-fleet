package render

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
)

func TestAgo(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	cases := map[time.Duration]string{
		10 * time.Second: "just now", 5 * time.Minute: "5 minutes ago", time.Hour: "1 hour ago",
		3 * 24 * time.Hour: "3 days ago", 90 * 24 * time.Hour: "3 months ago", 800 * 24 * time.Hour: "2 years ago",
	}
	for d, want := range cases {
		if got := Ago(now, now.Add(-d)); got != want {
			t.Errorf("Ago(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestTilde(t *testing.T) {
	ps := paths.ForHome("/Users/me", nil)
	if got := Tilde(ps, "/Users/me/Local Sites/x"); got != "~/Local Sites/x" {
		t.Errorf("got %q", got)
	}
	if got := Tilde(ps, "/Users/meow/x"); got != "/Users/meow/x" {
		t.Errorf("a sibling folder must not be shortened: %q", got)
	}
}

func TestCardsRespectWidth(t *testing.T) {
	s := site.Site{Slug: "s", Themes: []site.Theme{{
		Dir: "theme", IsTAW: true, Kind: site.KindClassic,
		Path: "/Users/me/" + strings.Repeat("very-long-folder/", 10) + "theme",
	}}}
	out := Cards(style.New(true), paths.ForHome("/Users/me", nil), s, time.Now(), 50)
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := ansi.StringWidth(l); w > 50 {
			t.Errorf("line wider than 50 (%d): %q", w, ansi.Strip(l))
		}
	}
	if !strings.Contains(ansi.Strip(out), "…") {
		t.Error("the long path should be truncated with …")
	}
}

func TestFindingsEmptyAndWhere(t *testing.T) {
	p := style.New(true)
	if !strings.Contains(Findings(p, nil, false, 0), "Nothing to report") {
		t.Error("empty")
	}
	out := ansi.Strip(Findings(p, []site.Finding{{Severity: site.Warn, Code: "git.dirty", Site: "a", Theme: "t", Message: "1 change", Fix: "commit"}}, true, 0))
	if !strings.Contains(out, "a/t") || !strings.Contains(out, "→ commit") || !strings.Contains(out, "git.dirty") {
		t.Errorf("out = %q", out)
	}
}
