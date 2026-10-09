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

func TestGoldenScreens(t *testing.T) {
	rep := fixtureReport()
	cases := map[string]Model{
		"table-80x24":   newModel(t, 80, 24, &rep),
		"wide-140x40":   newModel(t, 140, 40, &rep),
		"detail-100x30": press(t, newModel(t, 100, 30, &rep), "j", "enter"),
		"help-100x30":   press(t, newModel(t, 100, 30, &rep), "?"),
		"filter-100x20": press(t, newModel(t, 100, 20, &rep), "/", "b", "e", "h", "i", "n", "d"),
		"nomatch-80x12": press(t, newModel(t, 80, 12, &rep), "/", "z", "z", "enter"),
		"loading-80x12": newModel(t, 80, 12, nil),
		"tiny-50x8":     newModel(t, 50, 8, &rep),
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
	refuse   error
	created  create.Request
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

func (f *fakeActions) UpdateTask(_ site.Site, t site.Theme) (actions.Task, error) {
	return actions.Task{Title: "Update taw/core: " + t.Dir, Writes: true, Run: func(context.Context, io.Writer) (actions.Summary, error) {
		return actions.Summary{}, errors.New("composer exited 2 (output above)")
	}}, nil
}

func (f *fakeActions) CreateTask(r create.Request) (actions.Task, error) {
	f.created = r
	return actions.Task{Title: "Create site: " + r.Name, Writes: true, Run: func(_ context.Context, out io.Writer) (actions.Summary, error) {
		_, _ = out.Write([]byte("→ Creating the Local site " + r.Domain + "…\n✓ Site created and running in 18s\n"))
		return actions.Summary{Headline: r.Slug + " is ready: http://" + r.Domain, Lines: []string{"Admin " + r.AdminUser + " · password " + r.AdminPassword}, Secret: r.AdminPassword}, nil
	}}, nil
}

func (f *fakeActions) Launch(_ context.Context, _ site.Site, t site.Theme, _ handoff.Prompt) (string, error) {
	f.launched = t.Dir
	return "Started Claude Code for " + t.Dir, nil
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
	m = runCmd(t, m, keyMsg("l"))
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
	if !strings.Contains(screen(m), "Update taw/core in bistro-theme from 1.59.2 to 1.76.1?") {
		t.Fatalf("question:\n%s", screen(m))
	}
	next, cmd := m.Update(keyMsg("y"))
	m = drain(t, next.(Model), cmd)
	if !strings.Contains(screen(m), "✗ failed") || !strings.Contains(screen(m), "composer exited 2") {
		t.Errorf("failure shown:\n%s", screen(m))
	}
	// acme is current: u says so instead of asking.
	m = press(t, m, "esc", "k", "u")
	if m.confirm != "" || !strings.Contains(screen(m), "acme already has the newest taw/core") {
		t.Errorf("current theme:\n%s", screen(m))
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
	for _, want := range []string{"LIVE", "Production  https://acme.mx", "WordPress 6.8.3", "1 known vulnerability (worst high) per Defender"} {
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
