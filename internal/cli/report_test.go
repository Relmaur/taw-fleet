package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/pagespeed"
	"github.com/Relmaur/taw-fleet/internal/report"
	"github.com/Relmaur/taw-fleet/internal/watch"
)

func TestReportPrintSavesNothing(t *testing.T) {
	p := fixture(t)
	out, err := run(t, p, "report", "acme", "--print", "--no-speed", "--language", "es")
	if err != nil || !strings.Contains(out, "Reporte mensual del sitio") || !strings.Contains(out, "(hasta hoy)") {
		t.Fatalf("print: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(p.FleetDir, "reports")); !os.IsNotExist(err) {
		t.Error("--print must not save")
	}
	out, err = run(t, p, "report", "acme", "--no-speed", "--json", "--month", "2026-09")
	var d report.Data
	if err != nil || json.Unmarshal([]byte(out), &d) != nil || d.Month != "2026-09" || d.SoFar {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if got := report.List(filepath.Join(p.FleetDir, "reports"), "acme"); len(got) != 1 || got[0].Month != "2026-09" {
		t.Errorf("saved = %+v", got)
	}
	if _, err := run(t, p, "report", "acme", "--language", "fr"); err == nil {
		t.Error("--language fr accepted")
	}
	if _, err := run(t, p, "report", "--all"); err == nil || !strings.Contains(err.Error(), "production") {
		t.Errorf("--all without production sites: %v", err)
	}
}

func TestWatchMakesLastMonthsReportsEarlyInTheMonth(t *testing.T) {
	p := fixture(t)
	write(t, filepath.Join(p.ConfigDir, "config.toml"), "[sites.acme]\nproduction_url = \"https://acme.example\"\n")
	var out bytes.Buffer
	var measured atomic.Int32
	d := Deps{Paths: p, Runner: &exec.FakeRunner{}, Out: &out, Err: &out, GitHub: fakeGitHub(t),
		PageSpeed: func(context.Context, string, string) (pagespeed.Result, error) {
			measured.Add(1)
			return pagespeed.Result{Performance: 90}, nil
		}}
	root := NewRoot(BuildInfo{Version: "1"}, d)
	root.SetContext(context.Background())
	rep, err := d.scanner(&globals{offline: true}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var st watch.State
	sep := time.Date(2026, 9, 15, 12, 0, 0, 0, time.Local)
	st, _ = watch.Check(st, rep.Sites, sep) // watch ran in September (no production probe here: record it by hand)
	st.Uptime = map[string]map[string]watch.Day{"acme": {"2026-09-15": {Checks: 10, Up: 10}}}

	if ev, _ := d.monthlyReports(root, &globals{offline: true}, st, rep.Sites, time.Date(2026, 10, 5, 9, 0, 0, 0, time.Local)); len(ev) != 0 {
		t.Errorf("the 5th made %d", len(ev))
	}
	oct2 := time.Date(2026, 10, 2, 9, 0, 0, 0, time.Local)
	ev, problems := d.monthlyReports(root, &globals{offline: true}, st, rep.Sites, oct2)
	if len(ev) != 1 || ev[0].Kind != watch.ReportReady || ev[0].Site != "acme" || len(problems) != 0 || measured.Load() != 2 {
		t.Fatalf("events=%+v problems=%v measured=%d", ev, problems, measured.Load())
	}
	if _, err := os.Stat(report.File(filepath.Join(p.FleetDir, "reports"), "acme", "2026-09", "html")); err != nil {
		t.Errorf("not saved: %v", err)
	}
	if ev, _ := d.monthlyReports(root, &globals{offline: true}, st, rep.Sites, oct2.Add(time.Hour)); len(ev) != 0 {
		t.Error("made twice")
	}
	st.Uptime = nil // watch wasn't running in September: no report
	_ = os.RemoveAll(filepath.Join(p.FleetDir, "reports"))
	if ev, _ := d.monthlyReports(root, &globals{offline: true}, st, rep.Sites, oct2); len(ev) != 0 {
		t.Error("a report without watch's history")
	}
}
