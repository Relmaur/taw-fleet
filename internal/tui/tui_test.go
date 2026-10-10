package tui

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/create"
	"github.com/Relmaur/taw-fleet/internal/createform"
	"github.com/Relmaur/taw-fleet/internal/doctor"
	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/taw"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var now = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func gitInfo(branch string, dirty int, upstream bool) *site.GitInfo {
	g := &site.GitInfo{Branch: branch, DefaultBranch: "main", Dirty: dirty, Describe: "abc1234",
		LastCommit: now.Add(-50 * time.Hour), RemoteURL: "git@github.com:Relmaur/x.git",
		Repo: &site.Repo{Host: "github.com", Owner: "Relmaur", Name: "client--theme"}}
	if upstream {
		g.Upstream = "origin/" + branch
	}
	return g
}

func theme(slug, dir string, kind site.ThemeKind, installed string, g *site.GitInfo) site.Theme {
	return site.Theme{
		Dir: dir, Path: "/Users/me/Local Sites/" + slug + "/app/public/wp-content/themes/" + dir, IsTAW: true, Kind: kind, HasBinTaw: true,
		Core: site.CoreInfo{Installed: installed, Locked: installed, Latest: "v1.76.1", Behind: installed != "v1.76.1"},
		Git:  g,
	}
}

func fixtureReport() scan.Report {
	link := theme("taw", "taw-gutenberg", site.KindGutenberg, "v1.76.1", gitInfo("main", 0, true))
	link.Symlink = true
	link.RealPath = "/Users/me/Documents/TAW/taw-gutenberg"
	return scan.Report{
		ScannedAt: now,
		Latest:    map[string]string{scan.LatestCore: "v1.76.1"},
		Sites: []site.Site{
			{ID: "a1", Slug: "acme-shop", Name: "Acme", Domain: "acme.local", URL: "http://acme.local", Status: site.StatusRunning,
				Path: "/Users/me/Local Sites/acme-shop", WebRoot: "/Users/me/Local Sites/acme-shop/app/public", PHPVersion: "8.2.30",
				Themes: []site.Theme{theme("acme-shop", "acme", site.KindClassic, "v1.76.1", gitInfo("main", 0, true))}},
			{ID: "b2", Slug: "bistro", Domain: "bistro.local", URL: "http://bistro.local", Status: site.StatusHalted,
				Path: "/Users/me/Local Sites/bistro", WebRoot: "/Users/me/Local Sites/bistro/app/public", PHPVersion: "8.2.29",
				Themes: []site.Theme{theme("bistro", "bistro-theme", site.KindClassic, "v1.59.2", gitInfo("chore/update-core", 5, false))}},
			{ID: "c3", Slug: "plain", Domain: "plain.local", Status: site.StatusHalted, Themes: []site.Theme{{Dir: "twentytwentyfive"}}},
			{ID: "t4", Slug: "taw", Domain: "taw.local", URL: "http://taw.local", Status: site.StatusRunning, PHPVersion: "8.5.3",
				Path: "/Users/me/Local Sites/taw", WebRoot: "/Users/me/Local Sites/taw/app/public",
				Themes: []site.Theme{link, theme("taw", "taw-theme", site.KindClassic, "v1.76.1", gitInfo("main", 0, true))}},
		},
	}
}

func newModel(t *testing.T, w, h int, rep *scan.Report) Model {
	t.Helper()
	scans := 0
	m := New(context.Background(), Deps{
		Scan: func(context.Context) (scan.Report, error) {
			scans++
			return fixtureReport(), nil
		},
		Doctor:  func(r scan.Report) []site.Finding { return doctor.Run(r, doctor.Options{}) },
		Paths:   paths.ForHome("/Users/me", nil),
		Version: "v0.3.0",
		Dark:    true,
		Now:     func() time.Time { return now },
	})
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	if rep != nil {
		m = step(t, m, scanDoneMsg{rep: *rep})
	}
	return m
}

func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		m = step(t, m, keyMsg(k))
	}
	return m
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

func screen(m Model) string { return ansi.Strip(m.View().Content) }

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/tui -update`)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from %s:\n--- got ---\n%s\n--- want ---\n%s", name, path, got, want)
	}
}

// fleetModel is the fixture with production sites and BugSmash projects: the
// LIVE and FB columns and the Feedback block.
func fleetModel(t *testing.T, w, h int) Model {
	t.Helper()
	rep := fixtureReport()
	m := newModel(t, w, h, &rep)
	m.deps.Live = func(context.Context, bool) (map[string]site.Production, error) { return nil, nil }
	m.deps.Feedback = func(context.Context, bool) (map[string]site.Feedback, error) { return nil, nil }
	m = step(t, m, liveDoneMsg{results: map[string]site.Production{
		"acme-shop": {URL: "https://acme.mx", CheckedAt: now, Reachable: true, Verified: true, TawCore: "v1.76.1"},
		"bistro":    {URL: "https://bistro.mx", CheckedAt: now, Reachable: true, Verified: true, TawCore: "v1.59.2"},
	}})
	return step(t, m, feedbackDoneMsg{results: map[string]site.Feedback{
		"acme-shop": {ProjectID: "p1", Project: "acme.mx", CheckedAt: now, Open: 3, Oldest: now.Add(-72 * time.Hour), Comments: []site.Comment{
			{Number: 108, Page: "https://acme.mx/credito-pyme/", Text: `Actualizar texto por: "Buró de crédito Evaluación flexible."`, Author: "Paola", CreatedAt: now.Add(-3 * time.Hour)},
			{Number: 107, Page: "https://acme.mx/credito-pyme/", Text: "Cambiar texto por: Garantía…", Author: "Paola", CreatedAt: now.Add(-26 * time.Hour)},
			{Number: 99, Page: "https://acme.mx/multimedia/", Text: "Cambiar el nombre por: Despojo de inmuebles… un problema creciente.", Author: "Paola", CreatedAt: now.Add(-72 * time.Hour)},
		}},
		"bistro": {ProjectID: "p2", CheckedAt: now},
	}})
}

func TestGoldenScreens(t *testing.T) {
	rep := fixtureReport()
	cases := map[string]Model{
		"table-80x24":         newModel(t, 80, 24, &rep),
		"wide-140x40":         newModel(t, 140, 40, &rep),
		"detail-100x30":       press(t, newModel(t, 100, 30, &rep), "j", "enter"),
		"help-100x30":         press(t, newModel(t, 100, 30, &rep), "?"),
		"filter-100x20":       press(t, newModel(t, 100, 20, &rep), "/", "b", "e", "h", "i", "n", "d"),
		"nomatch-80x12":       press(t, newModel(t, 80, 12, &rep), "/", "z", "z", "enter"),
		"fleet-120x30":        fleetModel(t, 120, 30),
		"fleet-140x34":        fleetModel(t, 140, 34),
		"fleet-detail-100x40": press(t, fleetModel(t, 100, 40), "enter"),
		"loading-80x12":       newModel(t, 80, 12, nil),
		"tiny-50x8":           newModel(t, 50, 8, &rep),
		"empty-80x12": newModel(t, 80, 12, &scan.Report{ScannedAt: now,
			Errors: []site.SourceError{{Stage: "local", Err: "no Local by Flywheel sites found (sites.json is missing)"}}}),
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			got := screen(m)
			// Every line fits the window.
			for i, l := range strings.Split(got, "\n") {
				if w := ansi.StringWidth(l); w > m.width {
					t.Errorf("line %d is %d wide (window %d): %q", i, w, m.width, l)
				}
			}
			golden(t, name, got)
		})
	}
}

func TestViewIsFullScreen(t *testing.T) {
	rep := fixtureReport()
	v := newModel(t, 80, 24, &rep).View()
	if !v.AltScreen || v.WindowTitle != "taw-fleet" {
		t.Errorf("view = %+v", v)
	}
	if lines := strings.Count(v.Content, "\n") + 1; lines != 24 {
		t.Errorf("lines = %d, want exactly the window height", lines)
	}
}

func TestInlineInItsOwnWindow(t *testing.T) {
	rep := fixtureReport()
	m := newModel(t, 80, 24, &rep)
	m.deps.Inline = true
	if v := m.View(); v.AltScreen {
		t.Error("inline must draw on the normal screen")
	}
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if cmd == nil {
		t.Fatal("a resize must clear the scrollback")
	}
	if raw, ok := cmd().(tea.RawMsg); !ok || raw.Msg != "\x1b[3J" {
		t.Errorf("cmd = %#v", cmd())
	}
}

func TestLiveSitesFirst(t *testing.T) {
	rep := fixtureReport()
	m := newModel(t, 100, 24, nil)
	m.deps.Production = map[string]bool{"bistro": true}
	m = step(t, m, scanDoneMsg{rep, nil})
	var order []string
	for _, ri := range m.visible {
		order = append(order, m.rep.Sites[m.rows[ri].site].Themes[m.rows[ri].theme].Dir)
	}
	if strings.Join(order, ",") != "bistro-theme,acme,taw-gutenberg,taw-theme" {
		t.Errorf("order = %v (live first, then by name)", order)
	}
	m = press(t, m, "/", "l", "i", "v", "e", "enter")
	if len(m.visible) != 1 {
		t.Errorf("/live shows the live sites only: %d rows", len(m.visible))
	}
}

func TestNavigationBounds(t *testing.T) {
	rep := fixtureReport()
	m := newModel(t, 80, 24, &rep)
	if len(m.visible) != 4 {
		t.Fatalf("rows = %d, want the 4 TAW themes", len(m.visible))
	}
	m = press(t, m, "k", "up")
	if m.cursor != 0 {
		t.Errorf("cursor above the top = %d", m.cursor)
	}
	m = press(t, m, "j", "j", "j", "j", "j", "down")
	if m.cursor != 3 {
		t.Errorf("cursor past the end = %d", m.cursor)
	}
	_, th, _ := m.selectedTheme()
	if th.Dir != "taw-theme" {
		t.Errorf("selected = %s", th.Dir)
	}
}

func TestSelectionSurvivesRescan(t *testing.T) {
	rep := fixtureReport()
	m := press(t, newModel(t, 80, 24, &rep), "j", "j") // taw-gutenberg
	// The new scan lists the sites in another order.
	r2 := fixtureReport()
	r2.Sites[0], r2.Sites[3] = r2.Sites[3], r2.Sites[0]
	m = step(t, m, scanDoneMsg{rep: r2})
	if s, th, _ := m.selectedTheme(); s.Slug != "taw" || th.Dir != "taw-gutenberg" {
		t.Errorf("selected after rescan = %s/%s", s.Slug, th.Dir)
	}
}

func TestFilterAndEscape(t *testing.T) {
	rep := fixtureReport()
	m := press(t, newModel(t, 80, 24, &rep), "/", "u", "n", "p", "u", "s", "h", "e", "d")
	if !m.filtering || len(m.visible) != 1 {
		t.Fatalf("filtering=%v visible=%d", m.filtering, len(m.visible))
	}
	m = press(t, m, "enter")
	if m.filtering || len(m.visible) != 1 {
		t.Errorf("enter keeps the filter: filtering=%v visible=%d", m.filtering, len(m.visible))
	}
	m = press(t, m, "esc")
	if len(m.visible) != 4 || m.filter.Value() != "" {
		t.Errorf("esc clears: visible=%d value=%q", len(m.visible), m.filter.Value())
	}
}

func TestFilterBlockMatchesBlockThemes(t *testing.T) {
	rep := fixtureReport()
	m := press(t, newModel(t, 80, 24, &rep), "/", "b", "l", "o", "c", "k")
	if len(m.visible) == 0 {
		t.Fatal("/block matches nothing")
	}
	for _, i := range m.visible {
		r := m.rows[i]
		if k := m.rep.Sites[r.site].Themes[r.theme].Kind; k != site.KindGutenberg {
			t.Errorf("/block shows a %s theme", k)
		}
	}
}

func TestDetailAndHelpModes(t *testing.T) {
	rep := fixtureReport()
	m := press(t, newModel(t, 80, 24, &rep), "enter")
	if m.mode != modeDetail {
		t.Fatal("enter opens the detail")
	}
	m = press(t, m, "j", "j", "k")
	if m.scroll != 1 {
		t.Errorf("scroll = %d", m.scroll)
	}
	m = press(t, m, "esc")
	if m.mode != modeTable || m.scroll != 0 {
		t.Errorf("esc goes back: mode=%v scroll=%d", m.mode, m.scroll)
	}
	m = press(t, m, "?")
	if m.mode != modeHelp {
		t.Fatal("? opens help")
	}
	m = press(t, m, "q")
	if m.mode != modeTable {
		t.Error("q closes help (doesn't quit)")
	}
}

func TestQuit(t *testing.T) {
	rep := fixtureReport()
	for _, k := range []string{"q", "ctrl+c"} {
		_, cmd := newModel(t, 80, 24, &rep).Update(keyMsg(k))
		if cmd == nil {
			t.Fatalf("%s: no command", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s doesn't quit", k)
		}
	}
	// ctrl+c quits even while typing a filter.
	m := press(t, newModel(t, 80, 24, &rep), "/")
	if _, cmd := m.Update(keyMsg("ctrl+c")); cmd == nil {
		t.Error("ctrl+c while filtering")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("ctrl+c while filtering doesn't quit")
	}
}

func TestRefreshAndFailure(t *testing.T) {
	rep := fixtureReport()
	m := press(t, newModel(t, 80, 24, &rep), "r")
	if !m.scanning || !strings.Contains(screen(m), "scanning…") {
		t.Error("r starts a scan")
	}
	m = step(t, m, scanDoneMsg{err: errors.New("boom")})
	out := screen(m)
	if !strings.Contains(out, "scan failed") || !strings.Contains(out, "last refresh failed: boom") || !strings.Contains(out, "acme-shop") {
		t.Errorf("a failed refresh keeps the old data and says so:\n%s", out)
	}
}

func TestAutoRefresh(t *testing.T) {
	rep := fixtureReport()
	m := newModel(t, 80, 24, &rep)
	m.deps.Refresh = time.Minute
	m = step(t, m, tickMsg(now.Add(30*time.Second)))
	if m.scanning {
		t.Error("too early to refresh")
	}
	m = step(t, m, tickMsg(now.Add(61*time.Second)))
	if !m.scanning {
		t.Error("should refresh after a minute")
	}
	if !strings.Contains(screen(m), "scanning…") {
		t.Error("header shows scanning")
	}
}

func TestBackgroundColorSwitchesPalette(t *testing.T) {
	rep := fixtureReport()
	m := newModel(t, 80, 24, &rep)
	m = step(t, m, tea.BackgroundColorMsg{Color: color.White})
	if m.dark {
		t.Error("a white background means the light palette")
	}
	m = step(t, m, tea.BackgroundColorMsg{Color: color.Black})
	if !m.dark {
		t.Error("a black background means the dark palette")
	}
}

type fakeActions struct {
	did      []actions.Kind
	copied   string
	launched string
	beside   string // the tty Launch was given
	plan     actions.FleetPlan
	fleet    *actions.FleetPlan // what LaunchFleet was given
	doneDir  string             // where Launch says its done files go
	noClaude bool
	refuse   error
	created  create.Request
	worked   string // "work acme" / "stop acme", and whether a SiteOp came along
}

func (f *fakeActions) Do(_ context.Context, k actions.Kind, _ site.Site, t site.Theme) (string, error) {
	f.did = append(f.did, k)
	if k == actions.Production {
		return "", errors.New("no production URL")
	}
	return "did " + string(k) + " on " + t.Dir, nil
}

func (f *fakeActions) Handoff(s site.Site, t site.Theme, _ []site.Finding) (handoff.Prompt, error) {
	if f.refuse != nil {
		return handoff.Prompt{}, f.refuse
	}
	return handoff.Prompt{Title: "Update " + t.Dir + " (" + s.Slug + ")", Branch: "chore/taw-core-1.76.1",
		Text: "# Update the TAW theme `" + t.Dir + "`\n\n## The task\n\nRun the **update-theme** skill.\n"}, nil
}

func (f *fakeActions) Copy(_ context.Context, text string) error { f.copied = text; return nil }

func (f *fakeActions) SyncTask(_ site.Site, t site.Theme, apply bool) (actions.Task, error) {
	if f.refuse != nil {
		return actions.Task{}, f.refuse
	}
	return actions.Task{Title: "Sync check: " + t.Dir, Writes: apply, Run: func(_ context.Context, out io.Writer) (actions.Summary, error) {
		_, _ = out.Write([]byte("Cloning taw-theme…\nComparing 9 paths\n"))
		return actions.Summary{Headline: t.Dir + ": 1 Tier 1 path differs", Lines: []string{"Tier 1: bin/"}}, nil
	}}, nil
}

func (f *fakeActions) UpdateTask(s site.Site, t site.Theme) (actions.Task, error) {
	ask := "Update " + t.Dir + "? taw/core, framework files, migrations, checks, then a pull request to merge."
	steps := []taw.Step{{Key: "branch", Label: "New branch"}, {Key: "check:phpstan", Label: "Static analysis (PHPStan)"}, {Key: "commit", Label: "Commit"}, {Key: "deliver", Label: "Pull request"}}
	return actions.Task{Title: "Update " + t.Dir, Writes: true, Ask: ask, Steps: steps, Target: s.ID + "/" + t.Dir, Run: func(_ context.Context, out io.Writer) (actions.Summary, error) {
		_, _ = out.Write([]byte("Working on a new branch, taw/update-20261010-120000 (from main)\nCheck: phpstan\n  ✗ phpstan failed\n"))
		var rep taw.UpdateReport
		rep.Status, rep.Branch, rep.ReportPath = "failed", "taw/update-20261010-120000", "/t/.taw/update-report.md"
		rep.Failure = &struct {
			Step    string `json:"step"`
			Command string `json:"command"`
			Out     string `json:"out"`
			Reason  string `json:"reason"`
		}{Step: "phpstan", Command: "composer run phpstan"}
		return actions.Summary{Failed: true, Headline: t.Dir + ": the update stopped at phpstan; nothing was pushed",
			Lines: []string{"The work so far is on taw/update-20261010-120000."}, Report: actions.UpdateOutcome{Site: s, Theme: t, Report: rep}}, nil
	}}, nil
}

func (f *fakeActions) FixUpdate(_ context.Context, o actions.UpdateOutcome, beside string) (actions.Launched, error) {
	f.launched, f.beside = "fix "+o.Theme.Dir+" on "+o.Report.Branch, beside
	return actions.Launched{Message: "Claude Code is finishing the update of " + o.Theme.Dir}, nil
}

func (f *fakeActions) OpenURL(_ context.Context, url string) (string, error) {
	f.launched = "url " + url
	return "Opened " + url, nil
}

func (f *fakeActions) OpenGuide(_ context.Context, o actions.UpdateOutcome) (string, error) {
	f.launched = "guide " + o.Report.ReportPath
	return "Opened the guide (.taw/update-report.md) in Zed", nil
}

func (f *fakeActions) CreateTask(r create.Request) (actions.Task, error) {
	f.created = r
	return actions.Task{Title: "Create site: " + r.Name, Writes: true, Run: func(_ context.Context, out io.Writer) (actions.Summary, error) {
		_, _ = out.Write([]byte("→ Creating the Local site " + r.Domain + "…\n✓ Site created and running in 18s\n"))
		return actions.Summary{Headline: r.Slug + " is ready: http://" + r.Domain, Lines: []string{"Admin " + r.AdminUser + " · password " + r.AdminPassword}, Secret: r.AdminPassword}, nil
	}}, nil
}

func (f *fakeActions) Launch(_ context.Context, _ site.Site, t site.Theme, _ handoff.Prompt, beside string) (actions.Launched, error) {
	if f.noClaude {
		return actions.Launched{}, actions.ErrNoClaude
	}
	f.launched, f.beside = t.Dir, beside
	return actions.Launched{Message: "Started Claude Code for " + t.Dir, Done: filepath.Join(f.doneDir, t.Dir+".done")}, nil
}

func (f *fakeActions) OpenComments(_ context.Context, s site.Site) (string, error) {
	f.did = append(f.did, "comments")
	return "Opened " + s.Feedback.URL, nil
}

func (f *fakeActions) ResolvePrompt(s site.Site, _ site.Theme) (handoff.Prompt, error) {
	if s.Feedback == nil || s.Feedback.Open == 0 {
		return handoff.Prompt{}, errors.New("no open comments on " + s.Slug)
	}
	return handoff.Prompt{Title: "resolve comments on " + s.Slug, Text: "Use the resolve-comments skill"}, nil
}

func (f *fakeActions) SkillPrompt(s site.Site, _ site.Theme, sk actions.Skill, _ []site.Finding) handoff.Prompt {
	return handoff.Prompt{Title: sk.Name + " on " + s.Slug, Text: "Use the " + sk.Name + " skill on this site."}
}

func (f *fakeActions) LaunchSkill(_ context.Context, s site.Site, t site.Theme, kind string, p handoff.Prompt, beside string) (actions.Launched, error) {
	f.launched, f.beside = s.Slug+" "+kind+" in "+t.Dir+": "+p.Text, beside
	return actions.Launched{Message: "Claude Code is on " + t.Dir, Done: filepath.Join(f.doneDir, s.Slug+"-"+kind+"-1.done")}, nil
}

func (f *fakeActions) PlanFleet(context.Context, []site.Site) actions.FleetPlan { return f.plan }

func (f *fakeActions) PlanUpdateAll(context.Context, []site.Site) actions.FleetPlan { return f.plan }

func (f *fakeActions) UpdateAllTask(p actions.FleetPlan) (actions.Task, error) {
	steps := []taw.Step{{Key: "branch", Label: "New branch"}, {Key: "check:phpstan", Label: "Static analysis (PHPStan)"}, {Key: "commit", Label: "Commit"}, {Key: "deliver", Label: "Pull request"}}
	var items []actions.BatchItem
	for _, e := range p.Themes {
		items = append(items, actions.BatchItem{Target: e.Site.ID + "/" + e.Theme.Dir, Label: e.Site.Slug + " · " + e.Theme.Dir, Steps: steps})
	}
	ask := fmt.Sprintf("Update %d themes, one after another, each as its taw.json says. Leaving out fsspx--theme (2 uncommitted change(s)).", len(items))
	return actions.Task{Title: fmt.Sprintf("Update all · %d themes", len(items)), Writes: true, Ask: ask, Batch: items, Run: func(_ context.Context, out io.Writer) (actions.Summary, error) {
		var res actions.UpdateAllOutcome
		for i, e := range p.Themes {
			_, _ = fmt.Fprintln(out, actions.BatchStart+items[i].Target)
			_, _ = fmt.Fprintln(out, "Working on a new branch, taw/update-"+fmt.Sprint(i+1)+" (from main)\n  ✓ git checkout\nCheck: phpstan")
			var rep taw.UpdateReport
			rep.Branch, rep.ReportPath = "taw/update-"+fmt.Sprint(i+1), "/t/report.md"
			if i == 0 {
				_, _ = fmt.Fprintln(out, "  ✓ phpstan passed\nPushing taw/update-1 and opening a pull request")
				rep.Status = "updated"
				rep.Core.From, rep.Core.To = "v1.89.0", "v1.91.2"
				rep.Delivered = &struct {
					How  string `json:"how"`
					URL  string `json:"url"`
					Note string `json:"note"`
				}{How: "pr", URL: "https://github.com/acme/acme/pull/7"}
			} else {
				_, _ = fmt.Fprintln(out, "  ✗ phpstan failed")
				rep.Status = "failed"
				rep.Failure = &struct {
					Step    string `json:"step"`
					Command string `json:"command"`
					Out     string `json:"out"`
					Reason  string `json:"reason"`
				}{Step: "phpstan", Command: "composer run phpstan"}
			}
			_, _ = fmt.Fprintln(out, actions.BatchEnd+items[i].Target+" "+rep.Status)
			res.Items = append(res.Items, actions.UpdateItemOutcome{UpdateOutcome: actions.UpdateOutcome{Site: e.Site, Theme: e.Theme, Report: rep}, Target: items[i].Target})
		}
		return actions.Summary{Headline: "Update all: 1 updated, 1 stopped", Report: res, Failed: true}, nil
	}}, nil
}

func (f *fakeActions) LaunchFleet(_ context.Context, p actions.FleetPlan, _ map[string][]site.Finding, beside string) (actions.Launched, error) {
	f.fleet, f.beside = &p, beside
	return actions.Launched{Message: "Claude Code is updating 2 themes in the window on the right", Done: filepath.Join(f.doneDir, "coordinator.done")}, nil
}

func (f *fakeActions) WorkTask(_ site.Site, t site.Theme, op actions.SiteOp) (actions.Task, error) {
	f.worked = fmt.Sprintf("work %s op=%v", t.Dir, op != nil)
	return actions.Task{Title: "Work on " + t.Dir, Ask: "Work on " + t.Dir + "? This will open Cursor, run Vite and open the site.", Quiet: true,
		Run: func(context.Context, io.Writer) (actions.Summary, error) {
			return actions.Summary{Headline: "Working on " + t.Dir + ": Cursor, Vite at localhost:5173, site open"}, nil
		}}, nil
}

func (f *fakeActions) StopWorkTask(_ site.Site, t site.Theme, op actions.SiteOp) (actions.Task, error) {
	f.worked = fmt.Sprintf("stop %s op=%v", t.Dir, op != nil)
	return actions.Task{Title: "Stop working on " + t.Dir, Ask: "Stop working on " + t.Dir + "?", Quiet: true,
		Run: func(context.Context, io.Writer) (actions.Summary, error) {
			return actions.Summary{Headline: "Stopped Vite and acme-shop"}, nil
		}}, nil
}

func (f *fakeActions) SiteBranchTask(_ site.Site, t site.Theme) (actions.Task, error) {
	return actions.Task{Title: "Back to main: " + t.Dir, Writes: true, Ask: "Switch " + t.Dir + " to main?", Quiet: true,
		Run: func(context.Context, io.Writer) (actions.Summary, error) {
			return actions.Summary{Headline: t.Dir + ": on main again"}, nil
		}}, nil
}

func (f *fakeActions) MergeTask(_ site.Site, t site.Theme, pr site.PullRequest) (actions.Task, error) {
	if pr.Checks == site.ChecksFailing {
		return actions.Task{}, errors.New("#" + fmt.Sprint(pr.Number) + ": CI failed")
	}
	f.worked = fmt.Sprintf("merge %s #%d", t.Dir, pr.Number)
	return actions.Task{Title: "Merge", Writes: true, Ask: fmt.Sprintf("Merge #%d into main? This deploys https://acme.mx (production).", pr.Number), Quiet: true,
		Run: func(context.Context, io.Writer) (actions.Summary, error) {
			return actions.Summary{Headline: "Merged #12 into main; deploying acme.mx",
				Report: actions.MergeResult{Repo: "Relmaur/client--theme", SHA: "m3rg3d0", Number: pr.Number, Production: "https://acme.mx", Deploys: true}}, nil
		}}, nil
}

func (f *fakeActions) PullTask(s site.Site, _ site.Theme, _ actions.SiteOp) (actions.Task, error) {
	apply := actions.Task{Title: "Pull: acme.mx → " + s.Slug, Writes: true, Ask: "Import acme.mx's content into " + s.Slug + "? 1 new, 2 changed; production wins.",
		Run: func(context.Context, io.Writer) (actions.Summary, error) {
			f.worked = "pulled " + s.Slug
			return actions.Summary{Headline: "Pulled acme.mx into " + s.Slug + ": 1 created, 2 updated, 0 media downloaded"}, nil
		}}
	return actions.Task{Title: "Pull preview: acme.mx → " + s.Slug, Run: func(_ context.Context, out io.Writer) (actions.Summary, error) {
		_, _ = out.Write([]byte("Fetching the published content of https://acme.mx…\n"))
		return actions.Summary{Headline: "acme.mx → " + s.Slug + ": 1 new, 2 changed, 9 unchanged", Lines: []string{"update  post:page:about  (title: changed)"},
			Report: actions.PullPreview{Create: 1, Change: 2, Same: 9, Apply: apply}}, nil
	}}, nil
}

func (f *fakeActions) SyncAllTask(sites []site.Site) (actions.Task, error) {
	return actions.Task{Title: fmt.Sprintf("Sync check: %d sites", len(sites)), Run: func(_ context.Context, out io.Writer) (actions.Summary, error) {
		_, _ = out.Write([]byte("✓ acme: matches taw-theme\n▲ bistro-theme: 2 Tier 1 paths differ\n"))
		return actions.Summary{Headline: "Checked 2 themes: 1 match taw-theme, 1 differ (bistro-theme)"}, nil
	}}, nil
}

// runCmd executes a command returned by Update and feeds its message back.
func runCmd(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, cmd := m.Update(msg)
	m = next.(Model)
	if cmd != nil {
		m = step(t, m, cmd())
	}
	return m
}

func withActions(t *testing.T, h int, f *fakeActions) Model {
	rep := fixtureReport()
	m := newModel(t, 100, h, &rep)
	m.deps.Actions = f
	return m
}

func TestShortcutKeys(t *testing.T) {
	f := &fakeActions{}
	m := withActions(t, 24, f)
	m = runCmd(t, m, keyMsg("e"))
	if !strings.Contains(screen(m), "✓ did editor on acme") {
		t.Errorf("flash missing:\n%s", screen(m))
	}
	m = press(t, m, "j")
	for _, k := range []string{"f", "b", "B", "g", "G", "t"} {
		m = runCmd(t, m, keyMsg(k))
	}
	want := []actions.Kind{actions.Editor, actions.Finder, actions.Browser, actions.Admin, actions.GitHub, actions.PRs, actions.Terminal}
	if len(f.did) != len(want) {
		t.Fatalf("did = %v", f.did)
	}
	m = runCmd(t, m, keyMsg("P"))
	if !strings.Contains(screen(m), "✗ no production URL") {
		t.Errorf("error flash missing:\n%s", screen(m))
	}
	// The message goes away after a while.
	m = step(t, m, tickMsg(now.Add(flashFor+time.Second)))
	if strings.Contains(screen(m), "no production URL") {
		t.Error("flash should expire")
	}
}

func TestHandoffScreen(t *testing.T) {
	f := &fakeActions{}
	m := press(t, withActions(t, 20, f), "j", "h")
	if m.mode != modeHandoff {
		t.Fatal("h opens the handoff")
	}
	golden(t, "handoff-100x20", screen(m))

	m = runCmd(t, m, keyMsg("c"))
	if !strings.Contains(f.copied, "update-theme") || !strings.Contains(screen(m), "Copied the handoff prompt for bistro-theme") {
		t.Errorf("copy: %q\n%s", f.copied, screen(m))
	}
	m = runCmd(t, m, keyMsg("A"))
	if f.launched != "bistro-theme" || m.mode != modeTable || !strings.Contains(screen(m), "Started Claude Code for bistro-theme") {
		t.Errorf("launch: %q mode=%v", f.launched, m.mode)
	}
	m = press(t, m, "h", "esc")
	if m.mode != modeTable {
		t.Error("esc closes the handoff")
	}
}

func TestHandoffRefusedAndNoActions(t *testing.T) {
	f := &fakeActions{refuse: handoff.ErrUmbrella}
	m := press(t, withActions(t, 20, f), "h")
	if m.mode != modeTable || !strings.Contains(screen(m), "canonical scaffold") {
		t.Errorf("refusal:\n%s", screen(m))
	}
	rep := fixtureReport()
	m = newModel(t, 100, 20, &rep)
	m.deps.ActionsErr = errors.New("config.toml: unknown keys: edtor")
	m = press(t, m, "e")
	if !strings.Contains(screen(m), "shortcuts unavailable: config.toml: unknown keys: edtor") {
		t.Errorf("broken config:\n%s", screen(m))
	}
}

func TestStartStopFlow(t *testing.T) {
	rep := fixtureReport()
	m := newModel(t, 100, 24, &rep)
	var gotOp local.Op
	var gotSite string
	release := make(chan struct{})
	m.deps.SiteOp = func(_ context.Context, op local.Op, s site.Site) (time.Duration, error) {
		<-release
		gotOp, gotSite = op, s.Slug
		return 12 * time.Second, nil
	}

	// acme-shop is running: s asks to stop it; n cancels.
	m = press(t, m, "s")
	if !strings.Contains(screen(m), "Stop acme-shop?") {
		t.Fatalf("question missing:\n%s", screen(m))
	}
	m = press(t, m, "n")
	if m.confirm != "" || !strings.Contains(screen(m), "Nothing changed.") {
		t.Error("n cancels")
	}

	// bistro is halted: s asks to start it; y runs it.
	m = press(t, m, "j", "s")
	if !strings.Contains(screen(m), "Start bistro?") {
		t.Fatalf("question missing:\n%s", screen(m))
	}
	next, cmd := m.Update(keyMsg("y"))
	m = next.(Model)
	if _, busy := m.busy["b2"]; !busy || !strings.Contains(screen(m), "Local is working: start bistro…") {
		t.Errorf("busy state:\n%s", screen(m))
	}
	// A second s while busy is refused.
	m = press(t, m, "s")
	if !strings.Contains(screen(m), "bistro is busy") {
		t.Error("busy refusal")
	}
	close(release)
	msgs := cmd()
	if batch, ok := msgs.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if done, ok := c().(siteOpDoneMsg); ok {
				m = step(t, m, done)
			}
		}
	}
	if gotOp != local.Start || gotSite != "bistro" {
		t.Errorf("op = %s %s", gotOp, gotSite)
	}
	if len(m.busy) != 0 || !strings.Contains(screen(m), "✓ bistro is running (12s)") || !m.scanning {
		t.Errorf("after: busy=%v scanning=%v\n%s", m.busy, m.scanning, screen(m))
	}

	// R restarts.
	m = press(t, m, "k", "R")
	if m.pending != local.Restart || !strings.Contains(screen(m), "Restart acme-shop?") {
		t.Error("R asks to restart")
	}
}

func TestStartStopUnavailable(t *testing.T) {
	rep := fixtureReport()
	m := press(t, newModel(t, 100, 24, &rep), "s")
	if m.confirm != "" || !strings.Contains(screen(m), "isn't available") {
		t.Error("no SiteOp: no question, a message instead")
	}
}

// drain runs a task's commands until it finishes, feeding events back.
func drain(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for i := 0; cmd != nil && i < 50; i++ {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			cmd = nil
			for _, c := range batch {
				if c == nil {
					continue
				}
				if ev, ok := c().(taskEventMsg); ok {
					next, nc := m.Update(ev)
					m, cmd = next.(Model), nc
				}
			}
			continue
		}
		ev, ok := msg.(taskEventMsg)
		if !ok {
			return m
		}
		next, nc := m.Update(ev)
		m = next.(Model)
		if ev.done {
			return m
		}
		cmd = nc
	}
	return m
}

func TestSyncCheckRunsAtOnceAndShowsOutput(t *testing.T) {
	f := &fakeActions{}
	m := withActions(t, 24, f)
	next, cmd := m.Update(keyMsg("y"))
	m = next.(Model)
	if m.mode != modeOutput || m.task == nil || !m.task.running {
		t.Fatalf("y opens the output view: mode=%v", m.mode)
	}
	m = drain(t, m, cmd)
	out := screen(m)
	for _, want := range []string{"Sync check: acme", "✓ done", "Cloning taw-theme…", "Comparing 9 paths", "acme: 1 Tier 1 path differs", "Tier 1: bin/"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	golden(t, "output-100x24", out)
	if !m.scanning {
		t.Error("a finished task refreshes the dashboard")
	}
	m = press(t, m, "esc")
	if m.mode != modeTable || !strings.Contains(screen(m), "acme: 1 Tier 1 path differs") {
		t.Error("esc goes back; the headline stays in the footer")
	}
	m = press(t, m, "o")
	if m.mode != modeOutput {
		t.Error("o shows the last output again")
	}
}

func TestApplyAndUpdateAskFirst(t *testing.T) {
	f := &fakeActions{}
	m := press(t, withActions(t, 24, f), "j", "S")
	if !strings.Contains(screen(m), "Write the Tier 1 framework files in bistro-theme? It has 5 uncommitted changes.") {
		t.Fatalf("question:\n%s", screen(m))
	}
	m = press(t, m, "n")
	if m.task != nil {
		t.Error("n runs nothing")
	}
	m = press(t, m, "u")
	if !strings.Contains(m.confirm, "Update bistro-theme? taw/core, framework files") || m.onAgent != nil {
		t.Fatalf("one question, in taw.json's words, and no agent: %q", m.confirm)
	}
	next, cmd := m.Update(keyMsg("y"))
	m = drain(t, next.(Model), cmd)
	if !strings.Contains(screen(m), "✗ stopped") || !strings.Contains(screen(m), "✗  Static analysis (PHPStan)") {
		t.Errorf("the stop is shown on the checklist:\n%s", screen(m))
	}
	if !strings.Contains(m.confirm, "1 Fix with Claude · 2 Do it myself") || len(m.choices) != 2 {
		t.Fatalf("a stopped update offers both ways to finish it: %q", m.confirm)
	}
	m.deps.TTY = "/dev/ttys004"
	m = runCmd(t, m, keyMsg("1"))
	if f.launched != "fix bistro-theme on taw/update-20261010-120000" || f.beside != "/dev/ttys004" || m.confirm != "" {
		t.Errorf("1 hands the report to Claude beside the dashboard: %q %q", f.launched, f.beside)
	}
}

func TestTheListShowsTheUpdateOnItsRow(t *testing.T) {
	f := &fakeActions{}
	m := press(t, withActions(t, 30, f), "j", "u")
	next, cmd := m.Update(keyMsg("y"))
	m = drain(t, next.(Model), cmd)
	m = press(t, m, "n", "esc")
	if m.mode != modeTable || !strings.Contains(screen(m), "✗ bistro-theme  classic") {
		t.Errorf("back on the list, the row keeps a mark:\n%s", screen(m))
	}
	m = press(t, m, "enter")
	if !strings.Contains(screen(m), "⟳ UPDATE") || !strings.Contains(screen(m), "stopped at Static analysis (PHPStan)") {
		t.Errorf("the details say how it ended:\n%s", screen(m))
	}
}

func TestAStoppedUpdateOpensItsGuide(t *testing.T) {
	f := &fakeActions{}
	m := press(t, withActions(t, 24, f), "j", "u")
	next, cmd := m.Update(keyMsg("y"))
	m = drain(t, next.(Model), cmd)
	if !strings.Contains(screen(m), "1 Fix with Claude · 2 Do it myself") {
		t.Errorf("the choice shows under the output:\n%s", screen(m))
	}
	m = runCmd(t, m, keyMsg("2"))
	if f.launched != "guide /t/.taw/update-report.md" {
		t.Errorf("2 opens the guide for a person: %q\n%s", f.launched, screen(m))
	}
}

func TestUpdateWithAgent(t *testing.T) {
	f := &fakeActions{doneDir: t.TempDir()}
	m := withActions(t, 24, f)
	m.deps.TTY = "/dev/ttys004"
	m = press(t, m, "j")
	m = runCmd(t, m, keyMsg("A"))
	if f.launched != "bistro-theme" || f.beside != "/dev/ttys004" || m.confirm != "" || m.task != nil {
		t.Fatalf("A opens Claude beside the dashboard instead of composer: %q %q confirm=%q", f.launched, f.beside, m.confirm)
	}
	if !strings.Contains(screen(m), "Started Claude Code for bistro-theme") || len(m.agents) != 1 {
		t.Errorf("launched:\n%s", screen(m))
	}

	// Nothing happens until Claude exits; then the dashboard rescans.
	m.scanning = false
	m = step(t, m, tickMsg(now))
	if len(m.agents) != 1 || m.scanning {
		t.Error("still running: no rescan")
	}
	if err := os.WriteFile(filepath.Join(f.doneDir, "bistro-theme.done"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	next, cmd := m.Update(tickMsg(now))
	m = next.(Model)
	if !strings.Contains(screen(m), "Claude Code finished: bistro-theme. Rescanning.") || cmd == nil || !m.scanning || len(m.agents) != 0 {
		t.Errorf("finished:\n%s", screen(m))
	}

	// A works from the table and the handoff screen; a sync question doesn't offer it.
	f.launched = ""
	m = runCmd(t, m, keyMsg("A"))
	if f.launched != "bistro-theme" {
		t.Errorf("A in the table: %q", f.launched)
	}
	f.launched = ""
	m = press(t, m, "h")
	m = runCmd(t, m, keyMsg("A"))
	if f.launched != "bistro-theme" || m.mode != modeTable {
		t.Errorf("A on the handoff: %q", f.launched)
	}
	m = press(t, m, "S")
	if strings.Contains(screen(m), "agent") || m.onAgent != nil {
		t.Errorf("only the update offers the agent:\n%s", screen(m))
	}
	m = press(t, m, "A")
	if m.confirm == "" {
		t.Error("A doesn't answer a sync question")
	}

	f.noClaude = true
	m = press(t, m, "n")
	m = runCmd(t, m, keyMsg("A"))
	if !strings.Contains(screen(m), "isn't installed") {
		t.Errorf("no Claude Code:\n%s", screen(m))
	}
}

func TestUpdateAll(t *testing.T) {
	f := &fakeActions{doneDir: t.TempDir()}
	m := withActions(t, 30, f)
	m.deps.TTY = "/dev/ttys004"
	m = runCmd(t, m, keyMsg("U"))
	if !strings.Contains(screen(m), "Nothing to update") || m.confirm != "" {
		t.Fatalf("nothing to update:\n%s", screen(m))
	}

	rep := fixtureReport()
	f.plan = actions.FleetPlan{
		Themes:  []actions.FleetEntry{{Site: rep.Sites[0], Theme: rep.Sites[0].Themes[0]}, {Site: rep.Sites[1], Theme: rep.Sites[1].Themes[0]}},
		Skipped: []handoff.FleetSkip{{Site: "x", Theme: "fsspx--theme", Reason: "2 uncommitted change(s)"}},
	}
	m = runCmd(t, m, keyMsg("U"))
	if !strings.HasPrefix(m.confirm, "Update 2 themes, one after another") || !strings.Contains(m.confirm, "Leaving out fsspx--theme (2 uncommitted change(s))") {
		t.Fatalf("confirm = %q", m.confirm)
	}
	next, cmd := m.Update(keyMsg("y"))
	m = drain(t, next.(Model), cmd)
	got := screen(m)
	for _, want := range []string{"Update all · 2 themes", "2 of 2 themes", "acme-shop · acme", "pull request #7", "stopped at Static analysis (PHPStan)", "1 pull request opened"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(m.confirm, "Finish the 1 update that stopped") {
		t.Fatalf("confirm = %q", m.confirm)
	}
	m = runCmd(t, m, keyMsg("1"))
	if f.launched != "fix bistro-theme on taw/update-2" {
		t.Errorf("1 opens Claude on the stopped one: %q", f.launched)
	}
	// enter shows a theme's own steps; esc goes back to all of them.
	m = press(t, m, "down", "enter")
	if !strings.Contains(screen(m), "theme 2 of 2") || !strings.Contains(screen(m), "✗  Static analysis (PHPStan)") {
		t.Errorf("the theme's steps:\n%s", screen(m))
	}
	m = press(t, m, "esc", "esc")
	if m.mode != modeTable || !strings.Contains(screen(m), "✓ acme  classic") || !strings.Contains(screen(m), "✗ bistro-theme  classic") {
		t.Errorf("the rows keep a mark each:\n%s", screen(m))
	}
}

func TestOnlyOneTaskAtATime(t *testing.T) {
	f := &fakeActions{}
	m := withActions(t, 24, f)
	block := make(chan struct{})
	m.task = &taskState{title: "Sync check: acme", running: true, ch: make(chan taskEvent)}
	_ = block
	m = press(t, m, "j", "y")
	if !strings.Contains(screen(m), "Sync check: acme is still running (o shows it)") {
		t.Errorf("busy:\n%s", screen(m))
	}
}

func TestSyncColumn(t *testing.T) {
	rep := fixtureReport()
	rep.Sites[0].Themes[0].Drift = &site.Drift{Tier1: []string{"bin/", "functions.php"}, At: now}
	rep.Sites[1].Themes[0].Drift = &site.Drift{Tier2: []string{"composer.json"}, At: now}
	out := screen(newModel(t, 120, 20, &rep))
	if !strings.Contains(out, "SYNC") || !strings.Contains(out, "▲2") || !strings.Contains(out, "✓") || !strings.Contains(out, "—") {
		t.Errorf("sync column:\n%s", out)
	}
}

func TestHeaderSaysWhenTawFleetIsOutdated(t *testing.T) {
	rep := fixtureReport()
	rep.Latest[scan.LatestFleet] = "v0.7.1"
	m := newModel(t, 140, 30, &rep)
	if h := ansi.Strip(m.header()); !strings.Contains(h, "v0.3.0 ▲ 0.7.1") {
		t.Errorf("header = %q", h)
	}
	rep.Latest[scan.LatestFleet] = "v0.3.0"
	m = newModel(t, 140, 30, &rep)
	if h := ansi.Strip(m.header()); strings.Contains(h, "▲") {
		t.Errorf("up to date: %q", h)
	}
}

func TestNewSiteForm(t *testing.T) {
	f := &fakeActions{}
	m := withActions(t, 30, f)
	m.deps.Paths.LocalApp = t.TempDir() // the form lists Local's PHP versions
	m.deps.CreateDefaults = config.Create{AdminUser: "marco", AdminEmail: "marco@example.test"}
	next, _ := m.Update(keyMsg("n"))
	m = next.(Model)
	if m.mode != modeCreate || m.form == nil || m.fields.AdminUser != "marco" {
		t.Fatalf("n opens the form: mode=%v", m.mode)
	}
	if out := screen(m); !strings.Contains(out, "New TAW site") || !strings.Contains(out, "Site name") || !strings.Contains(out, "esc cancel") {
		t.Errorf("form screen:\n%s", out)
	}
	m = press(t, m, "esc")
	if m.mode != modeTable || m.form != nil || !strings.Contains(screen(m), "Nothing changed.") {
		t.Error("esc cancels")
	}

	// The form's answers start the task; the summary offers the password.
	next, _ = m.Update(keyMsg("n"))
	m = next.(Model)
	m.fields.Name, m.fields.Kind, m.fields.Confirmed = "Acme Two", "block", true
	next, cmd := m.finishCreate()
	m = next.(Model)
	if m.mode != modeOutput || m.selectAfterScan != "acme-two" || f.created.Kind != create.Block || len(f.created.AdminPassword) != 20 {
		t.Fatalf("mode=%v select=%q created=%+v", m.mode, m.selectAfterScan, f.created)
	}
	m = drain(t, m, cmd)
	out := screen(m)
	for _, want := range []string{"Create site: Acme Two", "Site created and running", "acme-two is ready", "c copy password"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	next, cmd = m.Update(keyMsg("c"))
	m = drain(t, next.(Model), cmd)
	if f.copied != f.created.AdminPassword {
		t.Errorf("copied %q", f.copied)
	}

	m.fields = &createform.Fields{Name: "x", Confirmed: false}
	if next, _ := m.finishCreate(); next.(Model).mode != modeTable {
		t.Error("backing out closes the form")
	}
}

// liveMsg runs a batch and returns its liveDoneMsg (the spinner tick is skipped).
func liveMsg(t *testing.T, cmd tea.Cmd) liveDoneMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if ld, ok := c().(liveDoneMsg); ok {
				return ld
			}
		}
	}
	if ld, ok := msg.(liveDoneMsg); ok {
		return ld
	}
	t.Fatalf("no liveDoneMsg in %T", msg)
	return liveDoneMsg{}
}

func TestProductionView(t *testing.T) {
	var freshCalls int
	results := map[string]site.Production{
		"acme-shop": {URL: "https://acme.mx", Reachable: true, Verified: true, WP: "6.8.3", PHP: "8.2.29", TawCore: "v1.59.2",
			Companion: "0.3.0", HasInventory: true, HasVulns: true, Plugins: 12, Scanner: "Defender",
			Vulns: []site.LiveVuln{{Component: "plugin akismet 5.1", Severity: "high", Title: "RCE"}}, WorstSeverity: "high", CheckedAt: now},
		"bistro": {URL: "https://bistro.mx", Error: "connection refused", ErrorKind: "unreachable", CheckedAt: now},
	}
	m := New(context.Background(), Deps{
		Scan:   func(context.Context) (scan.Report, error) { return fixtureReport(), nil },
		Doctor: func(r scan.Report) []site.Finding { return doctor.Run(r, doctor.Options{}) },
		Paths:  paths.ForHome("/Users/me", nil), Version: "v1.1.0", Dark: true, Now: func() time.Time { return now },
		Live: func(_ context.Context, fresh bool) (map[string]site.Production, error) {
			if fresh {
				freshCalls++
			}
			return results, nil
		},
	})
	m = step(t, m, tea.WindowSizeMsg{Width: 150, Height: 40})
	next, cmd := m.Update(scanDoneMsg{rep: fixtureReport()})
	m = next.(Model)
	if !m.liveFetching {
		t.Fatal("the first scan starts the production check")
	}
	m = step(t, m, liveMsg(t, cmd))
	out := screen(m)
	for _, want := range []string{"LIVE", "production · companion", "https://acme.mx  checked", "WordPress 6.8.3", "1 known vulnerability (worst high) per Defender"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(fmt.Sprint(m.findings), "live.vulnerable") || !strings.Contains(fmt.Sprint(m.findings), "live.core-mismatch") {
		t.Errorf("findings lack live rules: %v", m.findings)
	}
	// A later scan keeps the production results.
	m = step(t, m, scanDoneMsg{rep: fixtureReport()})
	if m.rep.Sites[0].Production == nil {
		t.Error("rescan dropped the production view")
	}
	// L checks again, fresh, and says how it went.
	next, cmd = m.Update(keyMsg("L"))
	m = step(t, next.(Model), liveMsg(t, cmd))
	if freshCalls != 1 || !strings.Contains(screen(m), "1 of 2 production sites verified · no answer from bistro") {
		t.Errorf("fresh=%d\n%s", freshCalls, screen(m))
	}
}

func TestWorkOnASite(t *testing.T) {
	f := &fakeActions{}
	m := withActions(t, 24, f)
	m.deps.SiteOp = func(context.Context, local.Op, site.Site) (time.Duration, error) { return time.Second, nil }
	m = press(t, m, "w")
	if f.worked != "work acme op=true" || !strings.Contains(screen(m), "Work on acme? This will open Cursor") {
		t.Fatalf("w asks first (%s):\n%s", f.worked, screen(m))
	}
	next, cmd := m.Update(keyMsg("y"))
	m = next.(Model)
	if m.mode != modeTable || m.task == nil || !m.task.running {
		t.Fatalf("the work task runs quietly behind the table: mode=%v", m.mode)
	}
	if !strings.Contains(screen(m), "Work on acme…") {
		t.Errorf("footer shows progress:\n%s", screen(m))
	}
	m = drain(t, m, cmd)
	if m.mode != modeTable || !strings.Contains(screen(m), "Working on acme: Cursor, Vite at localhost:5173") {
		t.Errorf("headline in the footer:\n%s", screen(m))
	}

	// With Vite running, the row says so and w stops it.
	rep := fixtureReport()
	rep.Sites[0].Themes[0].Dev = "http://localhost:5173"
	m = step(t, m, scanDoneMsg{rep, nil})
	if !strings.Contains(screen(m), "acme vite") {
		t.Errorf("vite badge:\n%s", screen(m))
	}
	m = press(t, m, "w")
	if f.worked != "stop acme op=true" || !strings.Contains(screen(m), "Stop working on acme?") {
		t.Errorf("w on a running Vite stops it (%s):\n%s", f.worked, screen(m))
	}
	m = press(t, m, "n")
	m.filter.SetValue("vite")
	m.applyFilter()
	if len(m.visible) != 1 {
		t.Errorf("/vite filters to the theme with Vite running: %d rows", len(m.visible))
	}
}

// reposMsg runs the command batch and returns the reposDoneMsg in it.
func reposMsg(t *testing.T, cmd tea.Cmd) reposDoneMsg {
	t.Helper()
	var find func(tea.Msg) (reposDoneMsg, bool)
	find = func(msg tea.Msg) (reposDoneMsg, bool) {
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				if c != nil {
					if r, ok := find(c()); ok {
						return r, true
					}
				}
			}
		}
		r, ok := msg.(reposDoneMsg)
		return r, ok
	}
	if r, ok := find(cmd()); ok {
		return r
	}
	t.Fatal("no reposDoneMsg")
	return reposDoneMsg{}
}

func TestPRsAndDeploys(t *testing.T) {
	var asked []string
	state := site.RepoState{Repo: "Relmaur/client--theme", Default: "main", Head: "ccc3333", HeadCI: site.ChecksPassing,
		PRs:    []site.PullRequest{{Number: 12, Title: "Update taw/core", Branch: "chore/taw-core-1.78.1", Checks: site.ChecksPassing, SameRepo: true}},
		Deploy: &site.Deploy{Workflow: "Deploy", Deployed: "aaa1111", DeployedAt: now.Add(-2 * time.Hour), Behind: 2, Pending: []site.Commit{{SHA: "ccc3333", Title: "Fix the hero"}}}}
	m := New(context.Background(), Deps{
		Scan:   func(context.Context) (scan.Report, error) { return fixtureReport(), nil },
		Doctor: func(r scan.Report) []site.Finding { return doctor.Run(r, doctor.Options{}) },
		Paths:  paths.ForHome("/Users/me", nil), Version: "v1.5.0", Dark: true, Now: func() time.Time { return now },
		GitHub: func(_ context.Context, repos []string) map[string]site.RepoState {
			asked = repos
			return map[string]site.RepoState{"Relmaur/client--theme": state}
		},
	})
	m = step(t, m, tea.WindowSizeMsg{Width: 150, Height: 40})
	next, cmd := m.Update(scanDoneMsg{rep: fixtureReport()})
	m = next.(Model)
	if !m.reposFetching {
		t.Fatal("the first scan reads GitHub")
	}
	m = step(t, m, reposMsg(t, cmd))
	if strings.Join(asked, ",") != "Relmaur/client--theme" {
		t.Errorf("asked for %v", asked)
	}
	out := screen(m)
	for _, want := range []string{"PR   DEPLOY", "1✓   ↑2", "#12 Update taw/core  ✓ CI passed", "↑2 on main, not deployed", "ccc3333 Fix the hero"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(fmt.Sprint(m.findings), "pr.ready") || !strings.Contains(fmt.Sprint(m.findings), "deploy.pending") {
		t.Errorf("findings lack the GitHub rules: %v", m.findings)
	}

	// A rescan keeps the answer; a deploy on its way makes it refresh often.
	m = step(t, m, scanDoneMsg{rep: fixtureReport()})
	if m.rep.Sites[0].Themes[0].GitHub == nil {
		t.Error("rescan dropped the GitHub state")
	}
	if m.reposDue() {
		t.Error("not due right after an answer")
	}
	m.deps.Now = func() time.Time { return now.Add(20 * time.Second) }
	if m.reposDue() {
		t.Error("nothing deploying: every 5 minutes")
	}
	running := state
	running.Deploy = &site.Deploy{Deployed: "aaa1111", Running: &site.Run{SHA: "ccc3333"}}
	m.repos = map[string]site.RepoState{"Relmaur/client--theme": running}
	if !m.reposDue() {
		t.Error("a deploy running: every 15 seconds")
	}

	// L reads GitHub now.
	m = press(t, m, "L")
	if !m.reposFetching {
		t.Error("L refreshes pull requests and deploys")
	}
}

func TestMergeAndFollowTheDeploy(t *testing.T) {
	f := &fakeActions{}
	two := []site.PullRequest{
		{Number: 12, Title: "Update taw/core", Checks: site.ChecksPassing},
		{Number: 13, Title: "Broken", Checks: site.ChecksFailing},
	}
	state := site.RepoState{Repo: "Relmaur/client--theme", Default: "main", Head: "aaa1111", HeadCI: site.ChecksPassing, PRs: two,
		Deploy: &site.Deploy{Workflow: "Deploy", Deployed: "aaa1111"}}
	answer := state
	m := withActions(t, 24, f)
	m.deps.GitHub = func(context.Context, []string) map[string]site.RepoState {
		return map[string]site.RepoState{"Relmaur/client--theme": answer}
	}
	m = step(t, m, reposDoneMsg{states: map[string]site.RepoState{"Relmaur/client--theme": state}})

	m = press(t, m, "M")
	if !strings.Contains(screen(m), "Merge which pull request of acme?  1 #12 Update taw/core · 2 #13 Broken") || !strings.Contains(screen(m), "1–2 pick") {
		t.Fatalf("several PRs: pick one:\n%s", screen(m))
	}
	m = press(t, m, "2")
	if !strings.Contains(screen(m), "#13: CI failed") {
		t.Errorf("a failing PR is refused:\n%s", screen(m))
	}
	m = press(t, m, "M", "1")
	if f.worked != "merge acme #12" || !strings.Contains(screen(m), "This deploys https://acme.mx (production).") {
		t.Fatalf("then asks before merging (%s):\n%s", f.worked, screen(m))
	}
	next, cmd := m.Update(keyMsg("y"))
	m = drain(t, next.(Model), cmd)
	if _, ok := m.followed["Relmaur/client--theme"]; !ok || !strings.Contains(screen(m), "Merged #12 into main; deploying acme.mx") {
		t.Fatalf("the merge is followed:\n%s", screen(m))
	}
	if !m.reposDue() && !m.reposFetching {
		t.Error("following a deploy refreshes often")
	}

	// CI and the deploy run, then production has it.
	running := state
	running.Head, running.HeadCI = "m3rg3d0", site.ChecksPending
	m = step(t, m, reposDoneMsg{states: map[string]site.RepoState{"Relmaur/client--theme": running}})
	if len(m.followed) != 1 || !strings.Contains(screen(m), "⟳") {
		t.Errorf("still following, deploy marked:\n%s", screen(m))
	}
	done := state
	done.Head, done.PRs = "m3rg3d0", two[1:]
	done.Deploy = &site.Deploy{Workflow: "Deploy", Deployed: "m3rg3d0", DeployedAt: now.Add(90 * time.Second)}
	m.deps.Now = func() time.Time { return now.Add(2 * time.Minute) }
	m = step(t, m, reposDoneMsg{states: map[string]site.RepoState{"Relmaur/client--theme": done}})
	if len(m.followed) != 0 || !strings.Contains(screen(m), "acme.mx has #12: deployed") {
		t.Errorf("deployed:\n%s", screen(m))
	}
}

func TestPullPreviewThenImport(t *testing.T) {
	f := &fakeActions{}
	m := withActions(t, 24, f)
	next, cmd := m.Update(keyMsg("C"))
	m = drain(t, next.(Model), cmd)
	out := screen(m)
	for _, want := range []string{"Fetching the published content", "update  post:page:about  (title: changed)", "Import acme.mx's content into acme-shop? 1 new, 2 changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if m.mode != modeOutput || f.worked != "" {
		t.Fatalf("the preview stays on screen and imports nothing yet (mode %v, %q)", m.mode, f.worked)
	}
	next, cmd = m.Update(keyMsg("y"))
	m = drain(t, next.(Model), cmd)
	if f.worked != "pulled acme-shop" || !strings.Contains(screen(m), "Pulled acme.mx into acme-shop") {
		t.Errorf("y imports (%q):\n%s", f.worked, screen(m))
	}
}

func TestOtherAccountTag(t *testing.T) {
	rep := fixtureReport()
	rep.Sites[1].Themes[0].Account = "parallelplus"
	m := newModel(t, 150, 30, &rep)
	if !strings.Contains(screen(m), "@parallelplus chore/u") {
		t.Errorf("the GIT cell leads with the account:\n%s", screen(m))
	}
	m.filter.SetValue("other")
	m.applyFilter()
	if len(m.visible) != 1 {
		t.Errorf("/other filters to themes of other accounts: %d rows", len(m.visible))
	}
}

func TestCheckAllThemes(t *testing.T) {
	m := withActions(t, 24, &fakeActions{})
	next, cmd := m.Update(keyMsg("Y"))
	m = drain(t, next.(Model), cmd)
	out := screen(m)
	for _, want := range []string{"Sync check: 4 sites", "▲ bistro-theme: 2 Tier 1 paths differ", "Checked 2 themes: 1 match taw-theme, 1 differ (bistro-theme)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestCommandMenu(t *testing.T) {
	f := &fakeActions{}
	m := withActions(t, 30, f)
	m = press(t, m, ":")
	if m.mode != modeMenu || !strings.Contains(screen(m), "All actions  on acme") || !strings.Contains(screen(m), "Work on it") {
		t.Fatalf("the menu lists the actions:\n%s", screen(m))
	}
	m = press(t, m, "f", "i", "n", "d")
	if out := screen(m); !strings.Contains(out, "Show in Finder") || strings.Contains(out, "Work on it") {
		t.Errorf("typing filters:\n%s", out)
	}
	m = runCmd(t, m, keyMsg("enter"))
	if m.mode != modeTable || len(f.did) != 1 || f.did[0] != actions.Finder {
		t.Errorf("enter runs the action: mode %v, did %v", m.mode, f.did)
	}
	m = press(t, m, ":", "z", "z", "z")
	if !strings.Contains(screen(m), "No action matches “zzz”.") {
		t.Errorf("no match:\n%s", screen(m))
	}
	m = press(t, m, "esc")
	if m.mode != modeTable {
		t.Error("esc closes the menu")
	}
}

// settle runs commands and feeds their messages back until none is left
// (spinner ticks dropped), for flows that fan out several fetches.
func settle(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for i := 0; len(queue) > 0 && i < 200; i++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case scanDoneMsg, reposDoneMsg, liveDoneMsg, taskEventMsg:
			next, nc := m.Update(msg)
			m = next.(Model)
			queue = append(queue, nc)
		}
	}
	return m
}

func TestRefreshEverything(t *testing.T) {
	m := withActions(t, 30, &fakeActions{})
	var fresh, liveFresh, ghCalls int
	m.deps.ScanFresh = func(context.Context) (scan.Report, error) { fresh++; return fixtureReport(), nil }
	m.deps.GitHub = func(context.Context, []string) map[string]site.RepoState {
		ghCalls++
		return map[string]site.RepoState{}
	}
	m.deps.Live = func(_ context.Context, f bool) (map[string]site.Production, error) {
		if f {
			liveFresh++
		}
		return map[string]site.Production{"acme-shop": {Reachable: true, Verified: true}}, nil
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m = next.(Model)
	if !m.full.active || !m.scanning || !m.reposFetching || !m.liveFetching {
		t.Fatalf("ctrl+r starts everything: %+v scanning=%v repos=%v live=%v", m.full, m.scanning, m.reposFetching, m.liveFetching)
	}
	if out := screen(m); !strings.Contains(out, "Refreshing everything: Local sites, GitHub, production") {
		t.Errorf("the status line shows what's pending:\n%s", out)
	}

	m = settle(t, m, cmd)
	if fresh != 1 || ghCalls < 1 || liveFresh != 1 {
		t.Errorf("fresh scan %d, GitHub %d, fresh production %d", fresh, ghCalls, liveFresh)
	}
	if m.mode != modeTable {
		t.Errorf("the sync check runs quietly, not in the output view: mode %v", m.mode)
	}
	if m.task == nil || m.task.running || !strings.HasPrefix(m.task.title, "Sync check") {
		t.Fatalf("the sync check ran after the scan: %+v", m.task)
	}

	m = step(t, m, tickMsg(now))
	if m.full.active {
		t.Fatal("a tick with nothing pending ends the refresh")
	}
	for _, want := range []string{"refreshed 4 sites", "1 of 1 production sites verified", "Checked 2 themes"} {
		if !strings.Contains(m.flash, want) {
			t.Errorf("summary %q lacks %q", m.flash, want)
		}
	}

	m = press(t, m, ":", "r", "e", "f", "r", "e", "s", "h", " ", "e")
	if !strings.Contains(screen(m), "Refresh everything") {
		t.Fatalf("the menu offers it:\n%s", screen(m))
	}
	next, _ = m.Update(keyMsg("enter"))
	if m = next.(Model); !m.full.active {
		t.Error("enter in the menu runs ctrl+r")
	}
}

func TestCommentKeys(t *testing.T) {
	f := &fakeActions{doneDir: t.TempDir()}
	m := fleetModel(t, 140, 34)
	m.deps.Actions, m.deps.TTY = f, "/dev/ttys004"
	m.rep.Sites[0].Feedback.URL = "https://x.bugsmash.io/review/a" // acme-shop, 3 open
	m = runCmd(t, m, keyMsg("F"))
	if len(f.did) != 1 || f.did[0] != "comments" || !strings.Contains(screen(m), "Opened https://x.bugsmash.io/review/a") {
		t.Errorf("F: %v\n%s", f.did, screen(m))
	}
	m = runCmd(t, m, keyMsg("X"))
	if !strings.HasPrefix(f.launched, "acme-shop comments in acme: Use the resolve-comments skill") || f.beside != "/dev/ttys004" || len(m.agents) != 1 {
		t.Fatalf("X: %q %q agents=%v", f.launched, f.beside, m.agents)
	}

	// When Claude exits, BugSmash is asked again, past the cache.
	for file := range m.agents {
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m.scanning = false
	m = step(t, m, tickMsg(now))
	if !m.feedbackFetching || !m.scanning {
		t.Errorf("after the resolve agent: fetching=%v scanning=%v", m.feedbackFetching, m.scanning)
	}

	m = press(t, m, "j", "X") // bistro: nothing open
	if !strings.Contains(screen(m), "no open comments on bistro") {
		t.Errorf("X without comments:\n%s", screen(m))
	}
}

func TestEcosystemSection(t *testing.T) {
	rep := fixtureReport()
	last := len(rep.Sites) - 1
	taw := &rep.Sites[last]
	if taw.Slug != "taw" {
		t.Fatalf("the fixture's last site is %s", taw.Slug)
	}
	for i, name := range []string{"taw-gutenberg", "taw-theme"} {
		taw.Themes[i].Git.Repo = &site.Repo{Host: "github.com", Owner: "Relmaur", Name: name}
	}
	// Scanned first, the ecosystem still sorts after the client sites.
	rep.Sites[0], rep.Sites[last] = rep.Sites[last], rep.Sites[0]
	m := newModel(t, 140, 40, &rep)
	out := screen(m)
	golden(t, "ecosystem-140x40", out)
	if !strings.Contains(out, "2 sites") || !strings.Contains(out, "2 TAW themes") {
		t.Errorf("the header counts client sites only:\n%s", out)
	}
	label := strings.Index(out, "TAW ECOSYSTEM")
	if label < 0 || label < strings.Index(out, "bistro") || label > strings.Index(out, "taw-gutenberg") {
		t.Errorf("the ecosystem goes last, under its label:\n%s", out)
	}
}

// msgOf runs cmd (and a batch's commands) and returns the first T it sends.
func msgOf[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	var zero T
	if cmd == nil {
		t.Fatalf("no command, want a %T", zero)
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if m, ok := c().(T); ok {
				return m
			}
		}
		t.Fatalf("no %T in the batch", zero)
	}
	m, ok := msg.(T)
	if !ok {
		t.Fatalf("got %T, want %T", msg, zero)
	}
	return m
}

func TestLeftoversAskThenRetry(t *testing.T) {
	rep := fixtureReport()
	m := newModel(t, 120, 24, &rep)
	l := local.Leftovers{{PID: 501, Name: "php-fpm", Holds: "the PHP socket"}}
	var ops []local.Op
	m.deps.SiteOp = func(_ context.Context, op local.Op, s site.Site) (time.Duration, error) {
		ops = append(ops, op)
		if len(ops) == 1 { // refused: something of Local's is in the way
			return 0, &local.LeftoversError{Slug: s.Slug, Op: op, List: l}
		}
		return 3 * time.Second, nil
	}
	var ended local.Leftovers
	m.deps.EndLeftovers = func(_ context.Context, got local.Leftovers) (local.Leftovers, error) {
		ended = got
		return nil, nil
	}

	// bistro is halted: s, y → refused with leftovers → the question.
	m = press(t, m, "j", "s")
	next, cmd := m.Update(keyMsg("y"))
	m = step(t, next.(Model), msgOf[siteOpDoneMsg](t, cmd))
	if !strings.Contains(m.confirm, "1 leftover Local process is in bistro's way (php-fpm 501 (the PHP socket))") ||
		!strings.Contains(m.confirm, "End it and start bistro?") {
		t.Fatalf("question = %q", m.confirm)
	}

	// y ends it, then starts bistro again.
	next, cmd = m.Update(keyMsg("y"))
	m = next.(Model)
	if _, busy := m.busy["b2"]; !busy {
		t.Error("busy while ending them")
	}
	next, cmd = m.Update(msgOf[leftoversEndedMsg](t, cmd))
	m = step(t, next.(Model), msgOf[siteOpDoneMsg](t, cmd))
	if len(ended) != 1 || len(ops) != 2 || ops[1] != local.Start || !strings.Contains(screen(m), "bistro is running (3s)") {
		t.Errorf("ended=%v ops=%v\n%s", ended, ops, screen(m))
	}

	// A stop that left them behind: end them, nothing to do again.
	m = m.askLeftovers(m.rep.Sites[1], &local.LeftoversError{Slug: "bistro", Op: local.Stop, Done: true, List: l})
	if !strings.HasSuffix(m.confirm, "End it? bistro is stopped.") {
		t.Errorf("after a stop: %q", m.confirm)
	}
	next, cmd = m.Update(keyMsg("y"))
	m = step(t, next.(Model), msgOf[leftoversEndedMsg](t, cmd))
	if len(ops) != 2 || !strings.Contains(screen(m), "Ended 1 leftover process; bistro can start again") {
		t.Errorf("ops=%v\n%s", ops, screen(m))
	}
}

func TestAskClaudePicker(t *testing.T) {
	theme := t.TempDir()
	for name, body := range map[string]string{
		"perf-audit":   "---\nname: perf-audit\nowner: taw\ndescription: Use when iterating on a live site's performance. Triggers on: x\n---\n",
		"publish-news": "---\nname: publish-news\nowner: site\ndescription: Publish a parish notice.\n---\n",
	} {
		dir := filepath.Join(theme, ".claude", "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeActions{doneDir: t.TempDir()}
	m := fleetModel(t, 140, 34)
	m.deps.Actions = f
	m.rep.Sites[0].Themes[0].RealPath = theme // acme-shop's theme

	m = press(t, m, "a")
	out := screen(m)
	if m.mode != modeSkills || !strings.Contains(out, "Ask Claude  in acme (acme-shop), with one of its skills") ||
		!strings.Contains(out, "Use when iterating on a live site's performance.") || !strings.Contains(out, "‹site skill›") ||
		!strings.Contains(out, "Ask for it with: x") && !strings.Contains(out, "Ask for it with") {
		t.Fatalf("picker:\n%s", out)
	}
	m = press(t, m, "n", "e", "w", "s") // filter: publish-news
	if got := m.paletteItems(); len(got) != 1 || got[0].title != "publish-news" {
		t.Fatalf("filter: %+v", got)
	}
	m = runCmd(t, m, keyMsg("enter"))
	if m.mode != modeTable || f.launched != "acme-shop skill-publish-news in acme: Use the publish-news skill on this site." {
		t.Errorf("launch: mode=%v %q", m.mode, f.launched)
	}

	// A theme without skills says how to get them.
	m.rep.Sites[0].Themes[0].RealPath = t.TempDir()
	m = press(t, m, "a")
	if m.mode != modeTable || !strings.Contains(screen(m), "has no skills in .claude/skills/ yet") {
		t.Errorf("no skills:\n%s", screen(m))
	}
}

func TestPaletteCatalogCoversTheKeys(t *testing.T) {
	m := newModel(t, 120, 30, nil)
	docs := map[string]bool{}
	for _, d := range actionDocs {
		docs[d.key] = true
	}
	skip := map[string]bool{"up": true, "down": true, ":": true, "c": true} // c only copies on the handoff screen
	for _, col := range m.keys.FullHelp() {
		for _, b := range col {
			if k := b.Keys()[0]; !skip[k] && !docs[k] {
				t.Errorf("key %q (%s) has no entry in the : menu's catalog", k, b.Help().Desc)
			}
		}
	}
}

func TestPaletteSaysWhyNot(t *testing.T) {
	f := &fakeActions{}
	m := withActions(t, 40, f)
	m.width = 160
	m = press(t, m, ":", "m", "e", "r", "g", "e")
	out := screen(m)
	if !strings.Contains(out, "Merge a pull request") || !strings.Contains(out, "Not now: no open pull request") || !strings.Contains(out, "‹action›") {
		t.Fatalf("detail pane:\n%s", out)
	}
	m = press(t, m, "enter")
	if m.mode != modeTable || !strings.Contains(m.flash, "Merge a pull request: no open pull request") {
		t.Errorf("enter on an unavailable entry explains: %q", m.flash)
	}

	// Recent actions come first.
	m = runCmd(t, press(t, m, ":", "f", "i", "n", "d"), keyMsg("enter"))
	m = press(t, m, ":")
	if items := m.paletteItems(); items[0].group != "Recent" || items[0].key != "f" {
		t.Errorf("recent first: %+v", items[0])
	}
}
