package scan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const tawTheme = `{"name":"taw/theme","type":"project","require":{"taw/core":"^1.0"}}`

// fleet builds a fake home with three Local sites:
//
//	client: one client TAW theme (real folder) + twentytwentyfive
//	taw:    taw-theme symlinked from an "umbrella" folder, plus a broken link
//	plain:  no TAW theme
func fleet(t *testing.T) paths.Paths {
	t.Helper()
	home := t.TempDir()
	p := paths.ForHome(home, nil)
	sitesRoot := filepath.Join(home, "Local Sites")
	themes := func(slug string) string {
		return filepath.Join(sitesRoot, slug, "app", "public", "wp-content", "themes")
	}

	write(t, filepath.Join(themes("client"), "clienttheme", "composer.json"), tawTheme)
	write(t, filepath.Join(themes("client"), "clienttheme", "bin", "taw"), "")
	write(t, filepath.Join(themes("client"), "twentytwentyfive", "style.css"), "")
	write(t, filepath.Join(themes("client"), "index.php"), "<?php")

	umbrella := filepath.Join(home, "Documents", "TAW", "taw-theme")
	write(t, filepath.Join(umbrella, "composer.json"), tawTheme)
	if err := os.MkdirAll(themes("taw"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(umbrella, filepath.Join(themes("taw"), "taw-theme")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "gone"), filepath.Join(themes("taw"), "taw-gutenberg")); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(themes("plain"), "plaintheme", "style.css"), "")

	reg := map[string]any{
		"id1": map[string]any{"id": "id1", "name": "Client Site", "path": "~/Local Sites/client", "domain": "client.local"},
		"id2": map[string]any{"id": "id2", "name": "taw", "path": filepath.Join(sitesRoot, "taw"), "domain": "taw.local"},
		"id3": map[string]any{"id": "id3", "name": "Plain", "path": "~/Local Sites/plain", "domain": "plain.local"},
		"id4": map[string]any{"id": "id4", "name": "Ghost", "path": "~/Local Sites/ghost", "domain": "ghost.local"},
	}
	data, _ := json.Marshal(reg)
	write(t, filepath.Join(p.LocalSupport, "sites.json"), string(data))
	write(t, filepath.Join(p.LocalSupport, "site-statuses.json"), `{"id1":"running","id2":"halted"}`)
	return p
}

func find(t *testing.T, sites []site.Site, id string) site.Site {
	t.Helper()
	for _, s := range sites {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("site %s not found", id)
	return site.Site{}
}

func TestLocalSource(t *testing.T) {
	p := fleet(t)
	sites, err := NewLocalSource(p).Sites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 4 {
		t.Fatalf("got %d sites", len(sites))
	}

	c := find(t, sites, "id1")
	if c.Slug != "client" || c.URL != "http://client.local" || c.Status != site.StatusRunning {
		t.Errorf("client = %+v", c)
	}
	if c.WebRoot != filepath.Join(p.Home, "Local Sites", "client", "app", "public") {
		t.Errorf("WebRoot = %q", c.WebRoot)
	}
	if len(c.Themes) != 2 {
		t.Fatalf("client themes = %+v (index.php must be skipped)", c.Themes)
	}
	ct := c.TAWThemes()
	if len(ct) != 1 || ct[0].Dir != "clienttheme" || !ct[0].HasBinTaw || ct[0].Kind != site.KindClassic || ct[0].Symlink {
		t.Errorf("client TAW themes = %+v", ct)
	}
	if len(c.Errors) != 0 {
		t.Errorf("client errors = %+v", c.Errors)
	}

	tw := find(t, sites, "id2")
	if tw.Status != site.StatusHalted {
		t.Errorf("taw status = %q", tw.Status)
	}
	var link, broken site.Theme
	for _, th := range tw.Themes {
		switch th.Dir {
		case "taw-theme":
			link = th
		case "taw-gutenberg":
			broken = th
		}
	}
	if !link.Symlink || !link.IsTAW || !strings.HasSuffix(link.RealPath, filepath.Join("Documents", "TAW", "taw-theme")) {
		t.Errorf("symlinked theme = %+v", link)
	}
	if !broken.Symlink || !broken.Broken || broken.IsTAW {
		t.Errorf("broken link = %+v", broken)
	}

	pl := find(t, sites, "id3")
	if pl.IsTAW() || pl.Status != site.StatusUnknown {
		t.Errorf("plain = %+v", pl)
	}

	g := find(t, sites, "id4")
	if len(g.Errors) != 1 || g.Errors[0].Stage != "themes" {
		t.Errorf("a missing site folder must be a themes error: %+v", g.Errors)
	}
}

func TestLocalSourceWithoutLocal(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	sc := Scanner{Sources: []Source{NewLocalSource(p)}}
	rep, err := sc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Sites) != 0 || len(rep.Errors) != 1 || rep.Errors[0].Stage != "local" {
		t.Errorf("report = %+v", rep)
	}
}

type fakeEnricher struct {
	name string
	fn   func(*site.Theme) error
}

func (f fakeEnricher) Name() string { return f.name }
func (f fakeEnricher) Enrich(_ context.Context, _ *site.Site, th *site.Theme) error {
	return f.fn(th)
}

func TestScannerEnrichesTAWThemesOnly(t *testing.T) {
	p := fleet(t)
	when := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	sc := Scanner{
		Sources: []Source{NewLocalSource(p)},
		Enrichers: []Enricher{
			fakeEnricher{"tag", func(th *site.Theme) error { th.Package += "+seen"; return nil }},
			fakeEnricher{"fail", func(th *site.Theme) error {
				if th.Dir == "clienttheme" {
					return errors.New("boom")
				}
				return nil
			}},
		},
		Now: func() time.Time { return when },
	}
	rep, err := sc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.ScannedAt.Equal(when) {
		t.Errorf("ScannedAt = %v", rep.ScannedAt)
	}
	c := find(t, rep.Sites, "id1")
	for _, th := range c.Themes {
		seen := strings.HasSuffix(th.Package, "+seen")
		if seen != th.IsTAW {
			t.Errorf("%s enriched=%v IsTAW=%v", th.Dir, seen, th.IsTAW)
		}
	}
	if len(c.Errors) != 1 || c.Errors[0].Stage != "fail" || !strings.Contains(c.Errors[0].Err, "clienttheme: boom") {
		t.Errorf("enricher error = %+v", c.Errors)
	}
	tw := find(t, rep.Sites, "id2")
	if len(tw.Errors) != 0 {
		t.Errorf("taw errors = %+v (broken links are never enriched)", tw.Errors)
	}
}

func TestScannerHonoursCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sc := Scanner{Sources: []Source{NewLocalSource(fleet(t))}}
	if _, err := sc.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestResolve(t *testing.T) {
	sites := []site.Site{
		{ID: "Ke02Z80rB", Name: "chcapital", Slug: "ch-capital---taw", Domain: "chcapital.local",
			Themes: []site.Theme{{Dir: "chcapital", IsTAW: true}}},
		{ID: "uxaCle2Pr", Name: "taw", Slug: "taw", Domain: "taw.local",
			Themes: []site.Theme{{Dir: "taw-theme", IsTAW: true}, {Dir: "taw-gutenberg", IsTAW: true}}},
		{ID: "x1", Name: "Twin", Slug: "twin-a", Domain: "twin-a.local"},
		{ID: "x2", Name: "twin", Slug: "twin-b", Domain: "twin-b.local"},
	}
	ok := map[string]string{
		"Ke02Z80rB":        "Ke02Z80rB", // id
		"ch-capital---taw": "Ke02Z80rB", // folder
		"CHCAPITAL":        "Ke02Z80rB", // name, any case (also its theme)
		"chcapital.local":  "Ke02Z80rB", // domain
		"taw-gutenberg":    "uxaCle2Pr", // theme folder
		"taw":              "uxaCle2Pr", // slug wins over the taw-* themes
		"twin-a":           "x1",
	}
	for q, want := range ok {
		s, err := Resolve(sites, q)
		if err != nil || s.ID != want {
			t.Errorf("Resolve(%q) = %v, %v; want %s", q, s, err, want)
		}
	}
	if _, err := Resolve(sites, "twin"); err == nil || !strings.Contains(err.Error(), "twin-a, twin-b") {
		t.Errorf("ambiguous: %v", err)
	}
	if _, err := Resolve(sites, "nope"); err == nil {
		t.Error("want no match")
	}
	if _, err := Resolve(sites, " "); err == nil {
		t.Error("want an error for an empty query")
	}
}

func TestResolveTheme(t *testing.T) {
	sites := []site.Site{
		{ID: "a", Slug: "acme", Themes: []site.Theme{{Dir: "acme-theme", IsTAW: true}, {Dir: "tt5"}}},
		{ID: "t", Slug: "taw", Themes: []site.Theme{{Dir: "taw-gutenberg", IsTAW: true}, {Dir: "taw-theme", IsTAW: true}}},
		{ID: "p", Slug: "plain", Themes: []site.Theme{{Dir: "tt5"}}},
	}
	ok := []struct{ q, theme, want string }{
		{"acme", "", "acme-theme"},
		{"taw-gutenberg", "", "taw-gutenberg"},
		{"taw", "TAW-THEME", "taw-theme"},
	}
	for _, c := range ok {
		_, th, err := ResolveTheme(sites, c.q, c.theme)
		if err != nil || th.Dir != c.want {
			t.Errorf("ResolveTheme(%q, %q) = %s, %v", c.q, c.theme, th.Dir, err)
		}
	}
	for _, c := range []struct{ q, theme, msg string }{
		{"taw", "", "add --theme"},
		{"taw", "nope", "no TAW theme \"nope\""},
		{"plain", "", "has no TAW theme"},
	} {
		if _, _, err := ResolveTheme(sites, c.q, c.theme); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("ResolveTheme(%q, %q): %v", c.q, c.theme, err)
		}
	}
}

func TestLiveStatusesWinAndFallBack(t *testing.T) {
	p := fleet(t)
	src := NewLocalSource(p)
	src.Live = func(context.Context) (map[string]site.Status, error) {
		return map[string]site.Status{"id1": site.StatusHalted, "id2": site.StatusRunning}, nil
	}
	sites, _ := src.Sites(context.Background())
	if find(t, sites, "id1").Status != site.StatusHalted || find(t, sites, "id2").Status != site.StatusRunning {
		t.Error("live statuses should replace site-statuses.json")
	}
	src.Live = func(context.Context) (map[string]site.Status, error) { return nil, errors.New("Local closed") }
	sites, _ = src.Sites(context.Background())
	if find(t, sites, "id1").Status != site.StatusRunning {
		t.Error("fall back to site-statuses.json")
	}
}

func TestActiveThemeCachesAndSkipsHalted(t *testing.T) {
	calls := 0
	r := &exec.FakeRunner{Script: func(exec.Spec) (exec.Result, error) {
		calls++
		return exec.Result{Stdout: []byte("taw-theme\n")}, nil
	}}
	p := paths.ForHome(t.TempDir(), nil)
	p.LookPath = func(n string) (string, error) { return "/usr/bin/" + n, nil }
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	a := &ActiveTheme{Paths: p, Runner: r, TTL: time.Minute, Now: func() time.Time { return now }}

	halted := site.Site{ID: "h", Themes: []site.Theme{{IsTAW: true}}}
	if err := a.EnrichSite(context.Background(), &halted); err != nil || halted.ActiveTheme != "" || calls != 0 {
		t.Errorf("halted: %v %q %d", err, halted.ActiveTheme, calls)
	}
	s := site.Site{ID: "r", SockLive: true, Socket: "/s.sock", WebRoot: "/w"}
	for range 3 {
		if err := a.EnrichSite(context.Background(), &s); err != nil {
			t.Fatal(err)
		}
	}
	if s.ActiveTheme != "taw-theme" || calls != 1 {
		t.Errorf("active=%q calls=%d (cached)", s.ActiveTheme, calls)
	}
	now = now.Add(2 * time.Minute)
	_ = a.EnrichSite(context.Background(), &s)
	a.Forget("r")
	_ = a.EnrichSite(context.Background(), &s)
	if calls != 3 {
		t.Errorf("calls = %d after expiry and Forget", calls)
	}
	if c := r.Calls()[0]; !strings.Contains(strings.Join(c.Args, " "), "option get stylesheet") {
		t.Errorf("args = %v", c.Args)
	}
}

func TestMissingSkills(t *testing.T) {
	theme := t.TempDir()
	if missingSkills(theme) != nil {
		t.Error("a taw/core without skills: none missing")
	}
	for _, rel := range []string{
		"vendor/taw/core/resources/skills/resolve-comments/SKILL.md",
		"vendor/taw/core/resources/skills/perf-audit/SKILL.md",
		".claude/skills/perf-audit/SKILL.md",
	} {
		p := filepath.Join(theme, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("---\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := missingSkills(theme); len(got) != 1 || got[0] != "resolve-comments" {
		t.Errorf("missing = %v", got)
	}
}
