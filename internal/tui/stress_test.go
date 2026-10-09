package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
)

// stressReport is 20 client sites in every state the table shows, and the
// TAW ecosystem.
func stressReport() (scan.Report, map[string]site.Production, map[string]site.Feedback) {
	names := []string{"ch-capital---taw", "ls-mxico", "fsspx-taw", "eme-lambda-taw", "ml-portfolio-taw", "foto-estudio-silvana-taw",
		"parallel-plus", "parallelstaff", "clinica-dental-sonrisa-perfecta-guadalajara", "bistro", "acme-shop", "notaria-17",
		"despacho-juridico-mx", "inmobiliaria-lomas", "taller-mecanico-hdz", "cafe-origen", "yoga-studio-cdmx", "colegio-montessori",
		"ferreteria-el-tornillo", "agencia-viajes-sol"}
	rep := scan.Report{ScannedAt: now, Latest: map[string]string{scan.LatestCore: "v1.87.2"}}
	live := map[string]site.Production{}
	fb := map[string]site.Feedback{}
	cores := []string{"v1.87.2", "v1.87.0", "v1.78.1", "v1.59.2", "v1.87.1"}
	for i, n := range names {
		st := site.StatusRunning
		if i%3 == 1 {
			st = site.StatusHalted
		}
		s := site.Site{ID: fmt.Sprintf("s%d", i), Slug: n, Domain: n + ".local", URL: "http://" + n + ".local", Status: st,
			Path: "/Users/me/Local Sites/" + n, WebRoot: "/Users/me/Local Sites/" + n + "/app/public", PHPVersion: "8.2.30"}
		dir := strings.TrimSuffix(strings.TrimSuffix(n, "-taw"), "---taw")
		g := gitInfo("main", 0, true)
		switch i % 5 {
		case 1:
			g = gitInfo("feat/hero-carousel-and-blog-redesign", 3, false)
		case 2:
			g.Dirty, g.Ahead = 2, 1
		case 3:
			g.Behind = 4
		}
		g.LastCommit = now.Add(-time.Duration(i*7+1) * time.Hour)
		kind := site.KindClassic
		if i%7 == 4 {
			kind = site.KindGutenberg
		}
		t := theme(n, dir+"-theme", kind, cores[i%len(cores)], g)
		t.Core.Latest, t.Core.Behind = "v1.87.2", t.Core.Installed != "v1.87.2"
		t.RealPath = t.Path
		if i%4 == 0 {
			t.Drift = &site.Drift{Tier1: []string{"bin/", "lib/"}[:i%3], At: now}
		}
		gh := &site.RepoState{Repo: "Relmaur/" + dir, CheckedAt: now, Default: "main",
			Head: "abc", Deploy: &site.Deploy{Workflow: "Deploy", Deployed: "abc", DeployedAt: now.Add(-3 * time.Hour)}}
		switch i % 6 {
		case 1:
			gh.PRs = []site.PullRequest{{Number: 12, Checks: site.ChecksPassing}, {Number: 13, Checks: site.ChecksFailing}}
		case 3:
			gh.Deploy.Behind = 2
			gh.Head = "def"
		case 5:
			gh.Deploy.Failed = &site.Run{}
		}
		t.GitHub = gh
		s.Themes = []site.Theme{t}
		if i == 2 || i == 9 { // two themes, one active
			t2 := theme(n, dir+"-child", site.KindClassic, "v1.87.2", gitInfo("main", 0, true))
			t2.RealPath, t2.Core.Latest, t2.Core.Behind = t2.Path, "v1.87.2", false
			s.Themes = append(s.Themes, t2)
			s.ActiveTheme = t.Dir
		}
		rep.Sites = append(rep.Sites, s)
		if i%5 != 4 {
			live[n] = site.Production{URL: "https://" + dir + ".mx", CheckedAt: now, Reachable: i%9 != 8, Verified: i%7 != 6, TawCore: t.Core.Installed}
		}
		if i%3 == 0 {
			f := site.Feedback{ProjectID: "p", Project: dir + ".mx", CheckedAt: now}
			if i%2 == 0 {
				f.Open, f.Oldest = i%4+1, now.Add(-time.Duration(i*5)*time.Hour)
				for k := 0; k < f.Open; k++ {
					f.Comments = append(f.Comments, site.Comment{Number: 100 + k, Page: "https://" + dir + ".mx/servicios/", Text: "Cambiar el texto del encabezado por: Atención personalizada en toda la república", Author: "Paola", CreatedAt: now.Add(-time.Duration(k*9) * time.Hour)})
				}
			}
			fb[n] = f
		}
	}
	link := theme("taw", "taw-gutenberg", site.KindGutenberg, "v1.87.2", gitInfo("main", 0, true))
	link.Symlink, link.RealPath = true, "/Users/me/Documents/TAW/taw-gutenberg"
	link.Git.Repo = &site.Repo{Host: "github.com", Owner: "Relmaur", Name: "taw-gutenberg"}
	tt := theme("taw", "taw-theme", site.KindClassic, "v1.87.2", gitInfo("main", 0, true))
	tt.Symlink, tt.RealPath = true, "/Users/me/Documents/TAW/taw-theme"
	tt.Git.Repo = &site.Repo{Host: "github.com", Owner: "Relmaur", Name: "taw-theme"}
	for _, x := range []*site.Theme{&link, &tt} {
		x.Core.Latest, x.Core.Behind = "v1.87.2", false
	}
	rep.Sites = append(rep.Sites, site.Site{ID: "taw", PHPVersion: "8.5.3", Path: "/Users/me/Local Sites/taw", Slug: "taw", URL: "http://taw.local", Status: site.StatusRunning, Themes: []site.Theme{link, tt}})
	return rep, live, fb
}

func stressModel(t *testing.T, w, h int) Model {
	rep, live, fb := stressReport()
	m := newModel(t, w, h, &rep)
	m.pal = style.New(true)
	m.deps.Live = func(context.Context, bool) (map[string]site.Production, error) { return nil, nil }
	m.deps.Feedback = func(context.Context, bool) (map[string]site.Feedback, error) { return nil, nil }
	m.deps.GitHub = func(context.Context, []string) map[string]site.RepoState { return nil }
	m = step(t, m, liveDoneMsg{results: live})
	return step(t, m, feedbackDoneMsg{results: fb})
}

// TestManySites draws a fleet of 20 client sites (24 rows with the TAW
// ecosystem) at several sizes: every frame fills the window exactly, no line
// is wider than it, and the selected site stays on screen.
func TestManySites(t *testing.T) {
	for _, c := range []struct {
		name string
		w, h int
		keys []string
	}{
		{"160x46", 160, 46, nil},
		{"160x46 scrolled", 160, 46, []string{"j", "j", "j", "j", "j", "j", "j", "j", "j", "j", "j", "j"}},
		{"160x46 at the end", 160, 46, []string{"end"}},
		{"110x34", 110, 34, []string{"j", "j", "j", "j", "j", "j"}},
		{"220x60", 220, 60, nil},
		{"90x24", 90, 24, []string{"j", "j", "j"}},
		{"filtered", 160, 46, []string{"/", "b", "e", "h", "i", "n", "d", "enter"}},
	} {
		m := stressModel(t, c.w, c.h)
		for _, k := range c.keys {
			m = step(t, m, keyMsg(k))
		}
		v := m.View().Content
		lines := strings.Split(v, "\n")
		if len(lines) != c.h {
			t.Errorf("%s: %d lines, want %d", c.name, len(lines), c.h)
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > c.w {
				t.Errorf("%s: line %d is %d wide", c.name, i, w)
			}
		}
		if !strings.Contains(ansi.Strip(v), m.rep.Sites[m.rows[m.visible[m.cursor]].site].Slug) {
			t.Errorf("%s: the selected site is off screen", c.name)
		}
	}
}

func TestManySitesTight(t *testing.T) {
	m := stressModel(t, 160, 46)
	if m.airy() || m.tableRows() != 19 {
		t.Errorf("24 rows in 42 lines sit tight: airy=%v rows=%d", m.airy(), m.tableRows())
	}
	if out := screen(m); !strings.Contains(out, "1–19 of 24") {
		t.Errorf("the rule says where the list is:\n%s", out)
	}
	if m = stressModel(t, 160, 80); !m.airy() || m.tableRows() != 24 {
		t.Errorf("everything fits with air: airy=%v rows=%d", m.airy(), m.tableRows())
	}
}
