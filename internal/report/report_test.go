package report

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/pagespeed"
	"github.com/Relmaur/taw-fleet/internal/site"
)

var oct = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func sources() Sources {
	at := func(day int) time.Time { return oct.AddDate(0, 0, day-1).Add(15 * time.Hour) }
	return Sources{
		Now: func() time.Time { return time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC) },
		GitHub: func(_ context.Context, repo string, _, _ time.Time) (github.Month, error) {
			return github.Month{Repo: repo, HasDeploy: true,
				Merged: []github.MergedPR{
					{Number: 14, Title: "Update taw/core 1.93.1 → 1.93.3", Branch: "taw/update-20261012-180233", At: at(12), URL: "https://github.com/x/y/pull/14"},
					{Number: 15, Title: "Ignore .sync/", Branch: "chore/ignore", At: at(13)},
					{Number: 16, Title: "New <b>team</b> page & photos", Branch: "feat/team", At: at(20), URL: "https://github.com/x/y/pull/16"},
				},
				Deploys: []github.DeployRun{{At: at(12)}, {At: at(20)}}}, nil
		},
		Comments: func(context.Context, string, time.Time, time.Time) (int, int, error) { return 5, 4, nil },
		Live: func(context.Context) (*site.Production, error) {
			return &site.Production{Reachable: true, Verified: true, WP: "7.1.3", PHP: "8.3.35", TawCore: "1.93.3", Plugins: 6, Scanner: "defender"}, nil
		},
		Uptime: func(string, time.Time, time.Time) (float64, int, time.Time) { return 0.99965, 8640, oct },
		Speed: func(_ context.Context, _ string, strategy string) (pagespeed.Result, error) {
			r := pagespeed.Result{Strategy: strategy, Performance: 92, Accessibility: 97, BestPractices: 100, SEO: 100, LCP: "2.1 s"}
			if strategy == "desktop" {
				r.Performance = 99
			} else {
				r.Field = &pagespeed.Field{LCPms: 1900, INPms: 120, CLS: 0.02, Overall: "FAST"}
			}
			return r, nil
		},
	}
}

func target() Target {
	return Target{Site: site.Site{Slug: "ls-mxico", Name: "LS México - TAW"}, Production: "https://lsmexico.mx", Project: "p1",
		Repos: []string{"Relmaur/ls-mexico--theme"}, PreparedBy: "EmeLambda", Lang: "en"}
}

func TestGatherSortsWhatHappened(t *testing.T) {
	d := Gather(context.Background(), sources(), target(), oct, oct.AddDate(0, 1, 0))
	if d.Name != "LS México" || d.SoFar || d.Month != "2026-10" {
		t.Errorf("header: %+v", d)
	}
	if len(d.Updates) != 1 || d.Updates[0].From != "1.93.1" || d.Updates[0].To != "1.93.3" {
		t.Errorf("updates = %+v", d.Updates)
	}
	if len(d.Maintenance) != 1 || len(d.Changes) != 1 || d.Changes[0].Number != 16 || len(d.Deploys) != 2 {
		t.Errorf("maintenance=%+v changes=%+v deploys=%d", d.Maintenance, d.Changes, len(d.Deploys))
	}
	if d.Comments.Received != 5 || d.Health.Core != "1.93.3" || len(d.Speed) != 2 || d.Speed[0].Strategy != "mobile" {
		t.Errorf("comments=%+v health=%+v speed=%+v", d.Comments, d.Health, d.Speed)
	}
}

func TestMarkdownAndHTMLSayTheSame(t *testing.T) {
	d := Gather(context.Background(), sources(), target(), oct, oct.AddDate(0, 1, 0))
	md := Markdown(d)
	for _, want := range []string{
		"# LS México — October 2026\n", "Monthly website report · lsmexico.mx · Prepared by EmeLambda on November 1, 2026",
		"- **Online:** 99.97% (checked every 5 minutes)", "- **Changes published:** 3", "- **Comments resolved:** 4 / 5", "- **Mobile speed:** 92",
		"The site answered 99.97% of 8,640 checks", "Framework and maintenance: 2 updates; the site now runs TAW core 1.93.3.",
		"- Oct 20: [New <b>team</b> page & photos](https://github.com/x/y/pull/16)", "Published to the live site 2 times this month.",
		"5 received in October: 4 resolved, 1 still open.", "| Performance | 92 | 99 |",
		"largest content in 1.9 s, response to a tap in 120 ms, layout shift 0.02. Good.",
		"6 plugins, all up to date. No known vulnerabilities (checked by Defender).",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, ".sync") {
		t.Error("housekeeping pull requests are counted, not listed")
	}
	h, err := HTML(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<html lang="en">`, "<h1>LS México — October 2026</h1>", "New &lt;b&gt;team&lt;/b&gt; page &amp; photos",
		`<div class="tile good"><div class="label">Online</div><div class="value">99.97%</div>`, "<td>Performance</td><td>92</td><td>99</td>"} {
		if !strings.Contains(h, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if strings.Contains(h, "<script") || strings.Contains(h, "http://") || strings.Contains(h, "<link") {
		t.Error("the HTML must stand alone: no scripts, nothing fetched")
	}
}

func TestSpanishAndMissingSources(t *testing.T) {
	src := sources()
	src.Now = func() time.Time { return time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC) }
	src.Comments = func(context.Context, string, time.Time, time.Time) (int, int, error) { return 0, 0, errors.New("401") }
	src.Speed = nil
	src.Uptime = func(string, time.Time, time.Time) (float64, int, time.Time) { return 0, 0, time.Time{} }
	tg := target()
	tg.Lang, tg.PreparedBy = "es", ""
	d := Gather(context.Background(), src, tg, oct, oct.AddDate(0, 1, 0))
	md := Markdown(d)
	for _, want := range []string{"# LS México — Octubre de 2026 (hasta hoy)", "Hecho el 12 de octubre de 2026", "Framework y mantenimiento: 2 actualizaciones",
		"## Sus comentarios de revisión\n\n_No disponible para este mes._", "## Velocidad\n\n_No disponible para este mes._", "- **En línea:** —"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if d.CommentNote != "401" || d.SpeedNote != "not measured" || !strings.Contains(d.UptimeNote, "watch") {
		t.Errorf("notes: %q %q %q", d.CommentNote, d.SpeedNote, d.UptimeNote)
	}
	if strings.Contains(md, "401") {
		t.Error("technical reasons stay out of the client's report")
	}
}

func TestOldMonthsSkipSpeed(t *testing.T) {
	src := sources()
	src.Now = func() time.Time { return time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC) }
	d := Gather(context.Background(), src, target(), oct, oct.AddDate(0, 1, 0))
	if len(d.Speed) != 0 || !strings.Contains(d.SpeedNote, "month's end") {
		t.Errorf("speed=%v note=%q", d.Speed, d.SpeedNote)
	}
}

func TestMonthOf(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	if from, to, err := MonthOf("", now); err != nil || from != oct || to != oct.AddDate(0, 1, 0) {
		t.Errorf("this month: %v %v %v", from, to, err)
	}
	for _, bad := range []string{"2026-11", "Oct", "2026-13"} {
		if _, _, err := MonthOf(bad, now); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestSaveAndList(t *testing.T) {
	dir := t.TempDir()
	d := Gather(context.Background(), sources(), target(), oct, oct.AddDate(0, 1, 0))
	md, html, err := Save(dir, d)
	if err != nil {
		t.Fatal(err)
	}
	if md != filepath.Join(dir, "ls-mxico", "2026-10.md") || html != filepath.Join(dir, "ls-mxico", "2026-10.html") {
		t.Errorf("paths %s %s", md, html)
	}
	sep := d
	sep.Month = "2026-09"
	if _, _, err := Save(dir, sep); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "ls-mxico", "notes.html"), nil, 0o644)
	got := List(dir, "ls-mxico")
	if len(got) != 2 || got[0].Month != "2026-10" || got[1].Month != "2026-09" {
		t.Errorf("list = %+v", got)
	}
}
