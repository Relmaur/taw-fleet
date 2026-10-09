package actions

import (
	"context"
	"io"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/site"
)

func TestWorkStartsEverythingInOrder(t *testing.T) {
	a, f := setup(t, config.Config{})
	a.VitePoll = 10 * time.Millisecond
	a.Paths.LookPath = func(name string) (string, error) { return "/nvm/bin/" + name, nil }
	s, th := fixture()
	th.RealPath = t.TempDir()
	mustWriteBody(t, filepath.Join(th.RealPath, "package.json"), `{"scripts":{"dev":"vite"}}`)
	mustWriteBody(t, filepath.Join(th.RealPath, "node_modules/.keep"), "")

	// Opening the Vite window starts a "server" and writes the hot file.
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	f.Script = func(sp exec.Spec) (exec.Result, error) {
		if strings.HasSuffix(sp.Args[len(sp.Args)-1], "-vite.command") {
			mustWriteBody(t, filepath.Join(th.RealPath, "public/build/hot"), "http://"+l.Addr().String())
		}
		return exec.Result{}, nil
	}
	var ops []local.Op
	op := func(_ context.Context, o local.Op, _ site.Site) (time.Duration, error) {
		ops = append(ops, o)
		return 9 * time.Second, nil
	}

	task, err := a.WorkTask(s, th, op)
	if err != nil {
		t.Fatal(err)
	}
	if task.Ask != "Work on ls-mexico? This will start ls-mxico, open Cursor, run Vite and open the site." || !task.Quiet {
		t.Errorf("Ask = %q", task.Ask)
	}
	sum, err := task.Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0] != local.Start {
		t.Errorf("ops = %v", ops)
	}
	calls := f.Calls()
	var opened []string
	for _, c := range calls {
		opened = append(opened, c.Args[len(c.Args)-1])
	}
	script := filepath.Join(a.Paths.CacheDir, "work", "ls-mexico-vite.command")
	want := []string{th.RealPath, script, "http://ls-mexico.local"}
	if strings.Join(opened, " | ") != strings.Join(want, " | ") {
		t.Errorf("opened %v\nwant   %v", opened, want)
	}
	if calls[1].Args[1] != "/T/Terminal.app" {
		t.Errorf("Vite runs in Terminal (Warp can't run a script): %v", calls[1].Args)
	}
	if !strings.Contains(sum.Headline, "ls-mxico running, Cursor, Vite at 127.0.0.1:") || !strings.HasSuffix(sum.Headline, "site open") {
		t.Errorf("headline = %q", sum.Headline)
	}
	body, _ := os.ReadFile(script)
	if !strings.Contains(string(body), "PATH='/nvm/bin':\"$PATH\"") || !strings.Contains(string(body), "npm run dev || exit\n") ||
		strings.Contains(string(body), "npm install") {
		t.Errorf("script:\n%s", body)
	}
}

func TestWorkWhenViteIsRunningOrMissing(t *testing.T) {
	a, f := setup(t, config.Config{})
	s, th := fixture()
	s.Status = site.StatusRunning
	th.RealPath = t.TempDir()

	// No package.json: the rest still happens, and the note says why.
	task, err := a.WorkTask(s, th, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.Ask != "Work on ls-mexico? This will open Cursor and open the site." {
		t.Errorf("Ask = %q", task.Ask)
	}
	sum, _ := task.Run(context.Background(), io.Discard)
	if sum.Headline != "Working on ls-mexico: Cursor, site open (Vite: no package.json)" || len(f.Calls()) != 2 {
		t.Errorf("headline = %q, calls %d", sum.Headline, len(f.Calls()))
	}

	// A halted site and no way to start it: refused up front.
	s.Status = site.StatusHalted
	if _, err := a.WorkTask(s, th, nil); err == nil {
		t.Error("a halted site needs Local to start it")
	}
}

func TestViteScriptRuns(t *testing.T) {
	dir := t.TempDir()
	theme := filepath.Join(dir, "it's a theme")
	bin := filepath.Join(dir, "bin")
	mustWrite(t, filepath.Join(theme, ".keep"))
	mustWriteBody(t, filepath.Join(bin, "npm"), "#!/bin/sh\necho \"npm $* in $(basename \"$PWD\")\"\n")
	if err := os.Chmod(filepath.Join(bin, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "vite.command")
	if err := os.WriteFile(script, []byte(ViteScript(theme, "Vite · it's", bin, []string{"npm install", "npm run dev"}, false)), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := osexec.CommandContext(context.Background(), script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if want := "\x1b]0;Vite · it's\a" + "npm install in it's a theme\nnpm run dev in it's a theme\n"; string(out) != want {
		t.Errorf("out = %q\nwant  %q", out, want)
	}
}

func TestStopWork(t *testing.T) {
	a, _ := setup(t, config.Config{})
	s, th := fixture()
	s.Status = site.StatusRunning
	th.RealPath = t.TempDir()
	var ops []local.Op
	op := func(_ context.Context, o local.Op, _ site.Site) (time.Duration, error) {
		ops = append(ops, o)
		return 0, nil
	}
	task, _ := a.StopWorkTask(s, th, op)
	if task.Ask != "Stop working on ls-mexico? This stops Vite and ls-mxico." {
		t.Errorf("Ask = %q", task.Ask)
	}
	sum, err := task.Run(context.Background(), io.Discard)
	if err != nil || sum.Headline != "Stopped ls-mxico" || len(ops) != 1 || ops[0] != local.Stop {
		t.Errorf("headline %q, ops %v, %v", sum.Headline, ops, err)
	}
}
