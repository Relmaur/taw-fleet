package tools

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
)

func fakeApps(t *testing.T, names ...string) paths.Paths {
	t.Helper()
	home := t.TempDir()
	p := paths.ForHome(home, nil)
	sys := filepath.Join(home, "Applications-system")
	user := filepath.Join(home, "Applications")
	p.Applications = []string{sys, user}
	for i, n := range names {
		dir := sys
		if i%2 == 1 {
			dir = user // some in ~/Applications
		}
		if err := os.MkdirAll(filepath.Join(dir, n+".app"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func names(apps []App) []string {
	var out []string
	for _, a := range apps {
		out = append(out, a.Name)
	}
	return out
}

func TestDetectOrdersByPreference(t *testing.T) {
	p := fakeApps(t, "Terminal", "PhpStorm", "iTerm", "Cursor", "GitKraken", "Visual Studio Code")
	inv := Detect(p)
	if got := names(inv.Editors); !reflect.DeepEqual(got, []string{"Cursor", "Visual Studio Code", "PhpStorm"}) {
		t.Errorf("editors = %v", got)
	}
	if got := names(inv.Terminals); !reflect.DeepEqual(got, []string{"iTerm2", "Terminal"}) {
		t.Errorf("terminals = %v (iTerm.app is iTerm2)", got)
	}
	if !strings.HasSuffix(inv.Terminals[0].Path, "iTerm.app") {
		t.Errorf("path = %s", inv.Terminals[0].Path)
	}
}

func TestPick(t *testing.T) {
	apps := []App{{Name: "Cursor"}, {Name: "Visual Studio Code"}, {Name: "iTerm2"}}
	cases := map[string]string{"": "Cursor", "cursor": "Cursor", "code": "Visual Studio Code", "VSCode": "Visual Studio Code", "iterm": "iTerm2"}
	for in, want := range cases {
		if a, err := Pick(apps, in); err != nil || a.Name != want {
			t.Errorf("Pick(%q) = %v, %v", in, a.Name, err)
		}
	}
	if _, err := Pick(apps, "Emacs"); err == nil || !strings.Contains(err.Error(), "found: Cursor") {
		t.Errorf("missing app: %v", err)
	}
	if _, err := Pick(nil, ""); err == nil {
		t.Error("nothing installed")
	}
}

func TestOpenerArgs(t *testing.T) {
	f := &exec.FakeRunner{}
	o := Opener{Exec: f}
	ctx := context.Background()
	cursor := App{Name: "Cursor", Path: "/Applications/Cursor.app"}
	ghostty := App{Name: "Ghostty", Path: "/Applications/Ghostty.app"}
	term := App{Name: "Terminal", Path: "/System/Applications/Utilities/Terminal.app"}
	warp := App{Name: "Warp", Path: "/Applications/Warp.app"}

	steps := []struct {
		run  func() error
		want []string
	}{
		{func() error { return o.With(ctx, cursor, "/t/theme dir") }, []string{"-a", "/Applications/Cursor.app", "/t/theme dir"}},
		{func() error { return o.Reveal(ctx, "/t/x") }, []string{"-R", "/t/x"}},
		{func() error { return o.URL(ctx, "http://acme.local/wp-admin/") }, []string{"http://acme.local/wp-admin/"}},
		{func() error { return o.TerminalAt(ctx, ghostty, "/t/x") }, []string{"-na", "/Applications/Ghostty.app", "--args", "--working-directory=/t/x"}},
		{func() error { return o.TerminalAt(ctx, term, "/t/x") }, []string{"-a", term.Path, "/t/x"}},
		{func() error { return o.RunScript(ctx, warp, "/c/s.command", term) }, []string{"-a", term.Path, "/c/s.command"}},
		{func() error { return o.RunScript(ctx, ghostty, "/c/s.command", term) }, []string{"-na", ghostty.Path, "--args", "-e", "/c/s.command"}},
	}
	for i, s := range steps {
		if err := s.run(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		c := f.Calls()[i]
		if c.Name != "/usr/bin/open" || !reflect.DeepEqual(c.Args, s.want) {
			t.Errorf("step %d: %s %v, want %v", i, c.Name, c.Args, s.want)
		}
	}
	if err := o.URL(ctx, "file:///etc/passwd"); err == nil {
		t.Error("only web addresses")
	}
}

func TestOpenFailureIsReported(t *testing.T) {
	f := &exec.FakeRunner{Script: func(exec.Spec) (exec.Result, error) {
		return exec.Result{Code: 1, Stderr: []byte("Unable to find application named 'Nope'\n")}, nil
	}}
	err := Opener{Exec: f}.With(context.Background(), App{Name: "Nope", Path: "/x/Nope.app"}, "/t")
	if err == nil || !strings.Contains(err.Error(), "Unable to find application") {
		t.Errorf("err = %v", err)
	}
}
