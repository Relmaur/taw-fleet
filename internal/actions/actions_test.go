package actions

import (
	"context"
	"errors"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/tools"
)

func setup(t *testing.T, cfg config.Config) (*Actions, *exec.FakeRunner) {
	t.Helper()
	home := t.TempDir()
	p := paths.ForHome(home, nil)
	f := &exec.FakeRunner{}
	a := New(p, f, cfg)
	a.Tools = tools.Inventory{
		Editors:   []tools.App{{Name: "Cursor", Path: "/Applications/Cursor.app"}, {Name: "PhpStorm", Path: "/Applications/PhpStorm.app"}},
		Terminals: []tools.App{{Name: "Warp", Path: "/Applications/Warp.app"}, {Name: "Terminal", Path: "/T/Terminal.app"}},
	}
	a.Now = func() time.Time { return time.Date(2026, 10, 8, 15, 30, 0, 0, time.UTC) }
	return a, f
}

func fixture() (site.Site, site.Theme) {
	s := site.Site{ID: "x1", Slug: "ls-mxico", URL: "http://ls-mexico.local", PHPVersion: "8.2.30",
		WebRoot: "/S/ls-mxico/app/public", Status: site.StatusHalted}
	t := site.Theme{Dir: "ls-mexico", RealPath: "/S/ls-mxico/app/public/wp-content/themes/ls-mexico", IsTAW: true, Kind: site.KindClassic,
		Core: site.CoreInfo{Installed: "v1.59.2", Latest: "v1.76.1", Behind: true},
		Git:  &site.GitInfo{Branch: "main", DefaultBranch: "main", Upstream: "origin/main", Repo: &site.Repo{Host: "github.com", Owner: "Relmaur", Name: "ls-mexico--theme"}}}
	return s, t
}

func TestShortcuts(t *testing.T) {
	a, f := setup(t, config.Config{Editor: "phpstorm", Sites: map[string]config.Site{"ls-mxico": {ProductionURL: "https://lsmexico.mx"}}})
	s, th := fixture()
	ctx := context.Background()
	cases := []struct {
		k    Kind
		msg  string
		args []string
	}{
		{Editor, "Opened ls-mexico in PhpStorm", []string{"-a", "/Applications/PhpStorm.app", th.RealPath}},
		{Finder, "Showed ls-mexico in Finder", []string{"-R", th.RealPath}},
		{Browser, "Opened http://ls-mexico.local", []string{"http://ls-mexico.local"}},
		{Admin, "Opened http://ls-mexico.local/wp-admin/", []string{"http://ls-mexico.local/wp-admin/"}},
		{GitHub, "Opened https://github.com/Relmaur/ls-mexico--theme", []string{"https://github.com/Relmaur/ls-mexico--theme"}},
		{PRs, "Opened https://github.com/Relmaur/ls-mexico--theme/pulls", []string{"https://github.com/Relmaur/ls-mexico--theme/pulls"}},
		{Terminal, "Opened Warp at ls-mexico", []string{"-a", "/Applications/Warp.app", th.RealPath}},
		{Production, "Opened https://lsmexico.mx", []string{"https://lsmexico.mx"}},
	}
	for i, c := range cases {
		msg, err := a.Do(ctx, c.k, s, th)
		if err != nil || msg != c.msg {
			t.Errorf("%s: %q, %v", c.k, msg, err)
			continue
		}
		if got := f.Calls()[i].Args; !reflect.DeepEqual(got, c.args) {
			t.Errorf("%s: args %v, want %v", c.k, got, c.args)
		}
	}
}

func TestShortcutErrors(t *testing.T) {
	a, _ := setup(t, config.Config{})
	s, th := fixture()
	th.Git = nil
	if _, err := a.Do(context.Background(), GitHub, s, th); err == nil {
		t.Error("no repo")
	}
	if _, err := a.Do(context.Background(), Production, s, th); err == nil || !strings.Contains(err.Error(), "[sites.ls-mxico] production_url") {
		t.Errorf("production: %v", err)
	}
	a.Tools.Editors = nil
	if _, err := a.Do(context.Background(), Editor, s, th); err == nil {
		t.Error("no editor installed")
	}
}

func TestCopyUsesPbcopyStdin(t *testing.T) {
	var got string
	a, _ := setup(t, config.Config{})
	a.Exec = &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		b, _ := io.ReadAll(s.Stdin)
		got = s.Name + ":" + string(b)
		return exec.Result{}, nil
	}}
	if err := a.Copy(context.Background(), "hello prompt"); err != nil || got != "/usr/bin/pbcopy:hello prompt" {
		t.Errorf("got %q err %v", got, err)
	}
}

func TestHandoffUsesLocalToolsAndConfig(t *testing.T) {
	a, _ := setup(t, config.Config{Sites: map[string]config.Site{"ls-mxico": {ProductionURL: "https://lsmexico.mx", Notes: "Deploys from main."}}})
	s, th := fixture()
	// Local's PHP for 8.2.30 in the fake home, and the skill in a real temp theme.
	php := filepath.Join(a.Paths.LocalSupport, "lightning-services", "php-8.2.30+1", "bin", "darwin-arm64", "bin", "php")
	mustWrite(t, php)
	mustWrite(t, filepath.Join(a.Paths.LocalSupport, "lightning-services", "php-8.2.30+1", "bin", "darwin", "bin", "php"))
	th.RealPath = t.TempDir()
	mustWrite(t, filepath.Join(th.RealPath, ".claude", "skills", "update-theme", "SKILL.md"))

	p, err := a.Handoff(s, th, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"php-8.2.30+1", "https://lsmexico.mx", "Deploys from main.", "Run the **update-theme** skill"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestLaunchWritesScriptAndOpensTerminal(t *testing.T) {
	a, f := setup(t, config.Config{})
	claude := filepath.Join(a.Paths.Home, ".local", "bin", "claude")
	mustWrite(t, claude)
	s, th := fixture()
	p := handoff.Prompt{Title: "Update ls-mexico (ls-mxico)", Branch: "chore/taw-core-1.76.1", Text: "# do it\n"}

	l, err := a.Launch(context.Background(), s, th, p, "")
	if err != nil {
		t.Fatal(err)
	}
	msg := l.Message
	// Warp can't run a script, so Terminal does.
	if !strings.Contains(msg, "Started Claude Code in Terminal") || !strings.Contains(msg, "chore/taw-core-1.76.1") {
		t.Errorf("msg = %q", msg)
	}
	call := f.Calls()[0]
	if call.Args[0] != "-a" || call.Args[1] != "/T/Terminal.app" || !strings.HasSuffix(call.Args[2], ".command") {
		t.Fatalf("open args = %v", call.Args)
	}
	script, err := os.ReadFile(call.Args[2])
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(call.Args[2]); st.Mode().Perm() != 0o700 {
		t.Errorf("script mode = %v", st.Mode().Perm())
	}
	promptFile := strings.TrimSuffix(call.Args[2], ".command") + ".md"
	if b, _ := os.ReadFile(promptFile); string(b) != "# do it\n" {
		t.Errorf("prompt file = %q", b)
	}
	if !strings.Contains(string(script), "cd '"+th.RealPath+"' || exit 1") || !strings.Contains(string(script), "'"+claude+"' -- \"$(cat ") {
		t.Errorf("script:\n%s", script)
	}
}

func TestLaunchWithoutClaude(t *testing.T) {
	a, _ := setup(t, config.Config{})
	s, th := fixture()
	if _, err := a.Launch(context.Background(), s, th, handoff.Prompt{}, ""); !errors.Is(err, ErrNoClaude) {
		t.Errorf("err = %v", err)
	}
}

// The launcher really runs: a fake `claude` prints its arguments, and the
// prompt arrives as one argument even with quotes, $ and spaces in it.
func TestLauncherScriptRuns(t *testing.T) {
	dir := t.TempDir()
	theme := filepath.Join(dir, "Local Sites", "it's a theme")
	if err := os.MkdirAll(theme, 0o755); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(dir, "fake claude")
	if err := os.WriteFile(claude, []byte("#!/bin/sh\npwd\nprintf '%s|' \"$#\" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(dir, "prompt.md")
	text := "line one\nit's \"quoted\" $(nope) `nope`"
	if err := os.WriteFile(prompt, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "run.command")
	done := filepath.Join(dir, "it.done")
	if err := os.WriteFile(script, []byte(LaunchScript{Title: "t", Dir: theme, Claude: claude, Args: []string{"--add-dir", "/other theme"}, Prompt: prompt, Done: done}.String()), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := osexec.CommandContext(context.Background(), script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The prompt arrives whole, after "--": --add-dir would take it as a folder.
	want := theme + "\n4|--add-dir|/other theme|--|" + text + "|"
	if string(out) != want {
		t.Errorf("out = %q\nwant  %q", out, want)
	}
	if _, err := os.Stat(done); err != nil {
		t.Errorf("the done file tells the dashboard Claude exited: %v", err)
	}
}

func TestLaunchBesideTheDashboard(t *testing.T) {
	a, f := setup(t, config.Config{Terminal: "Terminal"})
	mustWrite(t, filepath.Join(a.Paths.Home, ".local", "bin", "claude"))
	f.Script = func(s exec.Spec) (exec.Result, error) {
		if s.Name == "/usr/bin/osascript" && s.Args[0] == "-l" && s.Args[len(s.Args)-1] == "/dev/ttys004" {
			return exec.Result{Stdout: []byte("-1728,32,1728,1085\n")}, nil // the second display
		}
		return exec.Result{}, nil
	}
	s, th := fixture()
	l, err := a.Launch(context.Background(), s, th, handoff.Prompt{Text: "x", Branch: "chore/b"}, "/dev/ttys004")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(l.Message, "window on the right") || !strings.HasSuffix(l.Done, ".done") {
		t.Errorf("launched = %+v", l)
	}
	var placed bool
	var script string
	for _, c := range f.Calls() {
		if c.Name == "/usr/bin/osascript" && c.Args[0] == "-e" &&
			c.Args[1] == `tell application "Terminal" to set bounds of (first window whose tty is "/dev/ttys004") to {-1728, 32, -864, 1117}` {
			placed = true
		}
		if c.Name == "/usr/bin/open" {
			script = c.Args[len(c.Args)-1]
		}
	}
	if !placed {
		t.Errorf("the dashboard takes the left half: %+v", f.Calls())
	}
	body, _ := os.ReadFile(script)
	for _, want := range []string{`to {-864, 32, 0, 1117}`, "touch '" + l.Done + "'", "close (every window"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("script lacks %q:\n%s", want, body)
		}
	}

	// Without the dashboard's tty, nothing is arranged.
	f2 := &exec.FakeRunner{}
	a.Exec, a.open = f2, tools.Opener{Exec: f2}
	if _, err := a.Launch(context.Background(), s, th, handoff.Prompt{Text: "x"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range f2.Calls() {
		if c.Name == "/usr/bin/osascript" {
			t.Errorf("no arranging without a tty: %v", c.Args)
		}
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestSyncTaskSummaryAndCache(t *testing.T) {
	a, _ := setup(t, config.Config{})
	s, th := fixture()
	th.HasBinTaw = true
	a.Exec = &exec.FakeRunner{Script: func(sp exec.Spec) (exec.Result, error) {
		_, _ = sp.Stderr.Write([]byte("cloning\n"))
		return exec.Result{Stdout: []byte(`{"taw_core":{"installed":"v1.59.2","latest":"v1.76.1","behind":true,"error":null},
			"tier1":[{"path":"bin/","type":"dir","changed":true},{"path":".claude/skills/","type":"skills-dir","changed":false,"reconcile":{"warn":["old-skill"]}}],
			"tier2":[{"path":"composer.json","type":"file","changed":true}],"applied":[],"errors":[],"clean":false}`)}, nil
	}}
	task, err := a.SyncTask(s, th, false)
	if err != nil || task.Writes {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	var out strings.Builder
	sum, err := task.Run(context.Background(), &out)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Headline != "ls-mexico: 1 Tier 1 path differs" {
		t.Errorf("headline = %q", sum.Headline)
	}
	joined := strings.Join(sum.Lines, "\n")
	for _, want := range []string{"newest 1.76.1", "path differs: bin/", "unmarked skills old-skill", "usually just this site's own dependencies"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	if d := a.Paths.CacheDir; d == "" {
		t.Fatal("no cache dir")
	}
	if got := strings.TrimSpace(out.String()); got != "cloning" {
		t.Errorf("out = %q", got)
	}

	th.Git.Repo = &site.Repo{Owner: "Relmaur", Name: "taw-gutenberg"}
	if _, err := a.SyncTask(s, th, false); err == nil {
		t.Error("umbrella refused")
	}
}

func TestUpdateRefusesADirtyTree(t *testing.T) {
	a, _ := setup(t, config.Config{})
	s, th := fixture()
	th.Git.Dirty = 2
	if _, err := a.UpdateTask(s, th); err == nil || !strings.Contains(err.Error(), "2 uncommitted changes: commit or stash them first") {
		t.Errorf("err = %v", err)
	}
}

// A theme on taw/core 1.89: vendor/ gets the one-step update first, the lock
// goes back, then vendor/bin/taw update runs as the policy says.
func TestUpdateTaskBridgesAnOldThemeAndOpensAPullRequest(t *testing.T) {
	a, f := setup(t, config.Config{})
	s, th := fixture()
	th.RealPath = t.TempDir()
	installed := filepath.Join(th.RealPath, "vendor", "composer", "installed.json")
	mustWriteBody(t, installed, `{"packages":[{"name":"taw/core","version":"v1.89.0"}]}`)
	mustWriteBody(t, filepath.Join(th.RealPath, "taw.json"), `{"update": {"deliver": "pr+merge"}}`)
	f.Script = func(sp exec.Spec) (exec.Result, error) {
		args := strings.Join(sp.Args, " ")
		switch {
		case strings.Contains(args, "update taw/core"): // composer, or Local's php composer.phar
			mustWriteBody(t, installed, `{"packages":[{"name":"taw/core","version":"v1.91.2"}]}`)
		case strings.HasPrefix(args, "vendor/bin/taw update"):
			_, _ = sp.Stderr.Write([]byte("Working on a new branch, taw/update-1 (from main)\n"))
			mustWriteBody(t, filepath.Join(th.RealPath, ".taw", "update-report.md"), "# TAW update\n")
			return exec.Result{Stdout: []byte(`{"status":"updated","branch":"taw/update-1","core":{"from":"v1.89.0","to":"v1.91.2"},"changed":["AGENTS.md","composer.lock"],"migrations":["1.91.0/agent-docs"],"manual":["CLAUDE.md has this site's own changes"],"held":[],"checks":[{"name":"lint","status":"pass"},{"name":"build","status":"skip","reason":"no node_modules"}],"failure":null,"delivered":{"how":"pr+merge","url":"https://github.com/Relmaur/ls-mexico--theme/pull/9","note":"Pull request opened; it merges by itself when CI passes."}}`)}, nil
		}
		return exec.Result{}, nil
	}
	task, err := a.UpdateTask(s, th)
	if err != nil || !task.Writes || task.Ask != "Update ls-mexico? taw/core, framework files, migrations, checks, then a pull request that merges itself (deploys)." {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	var out strings.Builder
	sum, err := task.Run(context.Background(), &out)
	if err != nil {
		t.Fatal(err)
	}
	calls := f.Calls()
	if len(calls) != 3 || !strings.Contains(strings.Join(calls[0].Args, " "), "update taw/core") || strings.Join(calls[1].Args, " ") != "checkout -- composer.lock" || !strings.HasPrefix(strings.Join(calls[2].Args, " "), "vendor/bin/taw update --json --composer=") {
		t.Fatalf("composer → git checkout composer.lock → update, got %+v", calls)
	}
	if !slices.Contains(calls[0].Env, "TAW_NO_UPGRADE=1") {
		t.Error("the bridge holds the theme's own hook back")
	}
	if !strings.Contains(out.String(), "has no one-step update") || !strings.Contains(out.String(), "Working on a new branch") {
		t.Errorf("progress:\n%s", out.String())
	}
	joined := strings.Join(sum.Lines, "\n")
	if sum.Failed || sum.Headline != "ls-mexico: updated to taw/core 1.91.2; pull request https://github.com/Relmaur/ls-mexico--theme/pull/9" ||
		!strings.Contains(joined, "taw/core 1.89.0 → 1.91.2") || !strings.Contains(joined, "vendor/ got the newest first") ||
		!strings.Contains(joined, "For you: CLAUDE.md") || !strings.Contains(joined, "Checks: lint ✓ · build – not run here") {
		t.Errorf("sum = %+v", sum)
	}
}

func TestAStoppedUpdateHandsItsReportToClaude(t *testing.T) {
	a, f := setup(t, config.Config{})
	s, th := fixture()
	th.RealPath = t.TempDir()
	mustWriteBody(t, filepath.Join(th.RealPath, "vendor", "composer", "installed.json"), `{"packages":[{"name":"taw/core","version":"v1.91.2"}]}`)
	f.Script = func(exec.Spec) (exec.Result, error) {
		mustWriteBody(t, filepath.Join(th.RealPath, ".taw", "update-report.md"), "# TAW update: ls-mexico\n\n## What failed: phpstan\n")
		return exec.Result{Code: 1, Stdout: []byte(`{"status":"failed","branch":"taw/update-2","core":{"from":"v1.91.2","to":"v1.92.0"},"failure":{"step":"phpstan","command":"composer run phpstan","out":"Line 4"}}`)}, nil
	}
	task, _ := a.UpdateTask(s, th)
	sum, err := task.Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Calls()) != 1 {
		t.Errorf("a current taw/core runs update directly: %d calls", len(f.Calls()))
	}
	o, ok := sum.Report.(UpdateOutcome)
	if !ok || !o.Stopped() || !sum.Failed || sum.Headline != "ls-mexico: the update stopped at phpstan; nothing was pushed" {
		t.Fatalf("sum = %+v", sum)
	}
	p, err := FixPrompt(o)
	if err != nil || !strings.Contains(p.Text, "## What failed: phpstan") || !strings.Contains(p.Text, "Work on the branch taw/update-2") || !strings.Contains(p.Text, "Never merge") {
		t.Errorf("the prompt is the report plus what Claude may do:\n%s %v", p.Text, err)
	}
}

func mustWriteBody(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWindowRunsTheDashboard(t *testing.T) {
	a, f := setup(t, config.Config{})
	used, err := a.Window(context.Background(), "/opt/homebrew/bin/taw-fleet", []string{"--window=false", "--offline"})
	if err != nil || used != "Terminal" {
		t.Fatalf("used=%q err=%v", used, err)
	}
	call := f.Calls()[0]
	if call.Args[0] != "-a" || call.Args[1] != "/T/Terminal.app" || !strings.HasSuffix(call.Args[2], "taw-fleet.command") {
		t.Fatalf("open args = %v", call.Args)
	}
	script, _ := os.ReadFile(call.Args[2])
	if !strings.Contains(string(script), "\n'/opt/homebrew/bin/taw-fleet' '--window=false' '--offline' || exit\n") ||
		!strings.Contains(string(script), `close (every window whose tty is \"$tty\")`) {
		t.Errorf("Terminal closes its window after a clean quit:\n%s", script)
	}
	if !strings.Contains(string(script), `printf '\033[H\033[2J\033[3J'`) {
		t.Errorf("the window starts with an empty scrollback:\n%s", script)
	}
	if got := WindowScript("/bin/tf", []string{"--window=false"}, false); !strings.HasSuffix(got, "\nexec '/bin/tf' '--window=false'\n") {
		t.Errorf("other terminals close on their own:\n%s", got)
	}
}

func TestPlanFleetPicksAndSkips(t *testing.T) {
	a, _ := setup(t, config.Config{})
	a.CoreLatest = func(context.Context) (string, error) { return "v1.78.0", nil }
	s, th := fixture() // ls-mexico, behind on the scan's v1.76.1
	repo := func(name string) *site.GitInfo {
		return &site.GitInfo{Branch: "main", DefaultBranch: "main", Repo: &site.Repo{Host: "github.com", Owner: "Relmaur", Name: name}}
	}
	current := th
	current.Dir, current.Core = "current", site.CoreInfo{Installed: "v1.78.0"}
	current.Drift = &site.Drift{}
	current.Git = repo("current--theme")
	dirty := th
	dirty.Dir, dirty.Git = "dirty", repo("dirty--theme")
	dirty.Git.Dirty = 2
	umbrella := th
	umbrella.Dir, umbrella.Git = "taw-theme", repo("taw-theme")
	s.Themes = []site.Theme{th, current, dirty, umbrella}

	plan := a.PlanFleet(context.Background(), []site.Site{s})
	if plan.Latest != "v1.78.0" || len(plan.Themes) != 1 || plan.Themes[0].Theme.Dir != "ls-mexico" {
		t.Fatalf("plan = %+v", plan)
	}
	if got := plan.Themes[0].Theme.Core; got.Latest != "v1.78.0" || !got.Behind {
		t.Errorf("the fresh newest replaces the scan's: %+v", got)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Theme != "dirty" || plan.Skipped[0].Reason != "2 uncommitted change(s)" {
		t.Errorf("skipped = %+v (current and umbrella themes aren't listed)", plan.Skipped)
	}

	a.CoreLatest = func(context.Context) (string, error) { return "", errors.New("offline") }
	if plan := a.PlanFleet(context.Background(), []site.Site{s}); plan.Latest != "" || plan.Themes[0].Theme.Core.Latest != "v1.76.1" {
		t.Errorf("GitHub unreachable: the scan's answer stands: %+v", plan)
	}
}

func TestLaunchFleetWritesPromptsAndAddsDirs(t *testing.T) {
	a, f := setup(t, config.Config{})
	mustWrite(t, filepath.Join(a.Paths.Home, ".local", "bin", "claude"))
	s, th := fixture()
	plan := FleetPlan{Themes: []FleetEntry{{Site: s, Theme: th}}, Skipped: []handoff.FleetSkip{{Site: "x", Theme: "dirty", Reason: "2 uncommitted change(s)"}}}

	l, err := a.LaunchFleet(context.Background(), plan, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(l.Message, "to update 1 theme") || !strings.HasSuffix(l.Done, "coordinator.done") {
		t.Errorf("launched = %+v", l)
	}
	dir := filepath.Dir(l.Done)
	coord, _ := os.ReadFile(filepath.Join(dir, "coordinator.md"))
	batch, _ := os.ReadFile(filepath.Join(dir, "ls-mxico-ls-mexico.md"))
	if !strings.Contains(string(coord), filepath.Join(dir, "ls-mxico-ls-mexico.md")) || !strings.Contains(string(coord), "dirty: 2 uncommitted change(s)") {
		t.Errorf("coordinator:\n%s", coord)
	}
	if !strings.Contains(string(batch), `§ "Batch mode"`) {
		t.Errorf("batch prompt:\n%s", batch)
	}
	call := f.Calls()[len(f.Calls())-1]
	script, _ := os.ReadFile(call.Args[len(call.Args)-1])
	if !strings.Contains(string(script), "cd '"+dir+"'") || !strings.Contains(string(script), "'--add-dir' '"+th.RealPath+"'") {
		t.Errorf("script:\n%s", script)
	}

	if _, err := a.LaunchFleet(context.Background(), FleetPlan{}, nil, ""); !errors.Is(err, ErrNothingToUpdate) {
		t.Errorf("empty plan: %v", err)
	}
}
