package actions

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/site"
)

func withComments(s site.Site) site.Site {
	s.Feedback = &site.Feedback{ProjectID: "p1", Project: "Lsmexico.mx", URL: "https://x.bugsmash.io/review/a", Open: 4,
		Oldest: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC),
		Comments: []site.Comment{
			{Number: 12, Page: "https://lsmexico.mx/nosotros/", Text: "Cambiar texto por: Somos…", Author: "Paola Hernández"},
			{Number: 11, Page: "https://lsmexico.mx/", Text: "Nueva entrada de blog"},
		}}
	return s
}

func TestOpenComments(t *testing.T) {
	a, f := setup(t, config.Config{})
	s, _ := fixture()
	if _, err := a.OpenComments(context.Background(), s); err == nil || !strings.Contains(err.Error(), "bugsmash_project") {
		t.Errorf("no project: %v", err)
	}
	msg, err := a.OpenComments(context.Background(), withComments(s))
	if err != nil || msg != "Opened https://x.bugsmash.io/review/a" {
		t.Fatalf("%q, %v", msg, err)
	}
	if c := f.Calls(); len(c) != 1 || c[0].Args[len(c[0].Args)-1] != "https://x.bugsmash.io/review/a" {
		t.Errorf("open calls = %v", c)
	}
}

func TestResolvePrompt(t *testing.T) {
	a, _ := setup(t, config.Config{Sites: map[string]config.Site{"ls-mxico": {ProductionURL: "https://lsmexico.mx"}}})
	s, th := fixture()
	th.RealPath = t.TempDir()
	if _, err := a.ResolvePrompt(s, th); err == nil {
		t.Error("no project must refuse")
	}
	if _, err := a.ResolvePrompt(withComments(s), th); err == nil || !strings.Contains(err.Error(), "doesn't have the resolve-comments skill yet") {
		t.Errorf("no skill in the theme: %v", err)
	}
	mustWrite(t, filepath.Join(th.RealPath, ".claude", "skills", "resolve-comments", "SKILL.md"))
	none := withComments(s)
	none.Feedback.Open, none.Feedback.Comments = 0, nil
	if _, err := a.ResolvePrompt(none, th); err == nil || !strings.Contains(err.Error(), "no open comments") {
		t.Errorf("nothing open: %v", err)
	}
	p, err := a.ResolvePrompt(withComments(s), th)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Use the resolve-comments skill to resolve the open BugSmash comments on ls-mxico.",
		"theme ls-mexico (this folder)", "- Production: https://lsmexico.mx",
		"Lsmexico.mx, project p1, review page https://x.bugsmash.io/review/a", "4 open comments",
		"  - #12 /nosotros/: Cambiar texto por: Somos… (Paola Hernández)", "  - #11 /: Nueva entrada de blog\n",
		"  - … and 2 more", "ask before anything reaches production",
	} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p.Text)
		}
	}
}
