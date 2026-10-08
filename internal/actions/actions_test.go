package actions

import (
	"context"
	"errors"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"reflect"
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

	msg, err := a.Launch(context.Background(), s, th, p)
	if err != nil {
		t.Fatal(err)
	}
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
	if !strings.Contains(string(script), "cd '"+th.RealPath+"' || exit 1") || !strings.Contains(string(script), "exec '"+claude+"'") {
		t.Errorf("script:\n%s", script)
	}
}

func TestLaunchWithoutClaude(t *testing.T) {
	a, _ := setup(t, config.Config{})
	s, th := fixture()
	if _, err := a.Launch(context.Background(), s, th, handoff.Prompt{}); !errors.Is(err, ErrNoClaude) {
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
	if err := os.WriteFile(claude, []byte("#!/bin/sh\npwd\nprintf '%s|' \"$#\" \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(dir, "prompt.md")
	text := "line one\nit's \"quoted\" $(nope) `nope`"
	if err := os.WriteFile(prompt, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "run.command")
	if err := os.WriteFile(script, []byte(LauncherScript("t", theme, claude, prompt)), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := osexec.CommandContext(context.Background(), script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := theme + "\n1|" + text + "|"
	if string(out) != want {
		t.Errorf("out = %q\nwant  %q", out, want)
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
