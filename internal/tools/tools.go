// Package tools finds the editors and terminals installed on this Mac and
// opens things with them. Everything goes through `open`, macOS's own
// launcher, with argument lists.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
)

// Kind is what an app is for.
type Kind string

// App kinds.
const (
	Editor   Kind = "editor"
	Terminal Kind = "terminal"
)

// App is an installed application.
type App struct {
	Name string // as people say it: "Cursor", "iTerm2"
	Path string // /Applications/Cursor.app
	Kind Kind
}

// known lists the apps taw-fleet can use, in order of preference. bundle is
// the .app folder name when it differs from Name.
var known = []struct {
	name, bundle string
	kind         Kind
}{
	{"Cursor", "", Editor},
	{"Visual Studio Code", "", Editor},
	{"PhpStorm", "", Editor},
	{"Zed", "", Editor},
	{"Sublime Text", "", Editor},
	{"Nova", "", Editor},
	{"Ghostty", "", Terminal},
	{"iTerm2", "iTerm", Terminal},
	{"Warp", "", Terminal},
	{"kitty", "", Terminal},
	{"Terminal", "", Terminal},
}

// Inventory is what's installed, preferred first within each kind.
type Inventory struct {
	Editors   []App
	Terminals []App
}

// Detect looks for the known apps in p.Applications.
func Detect(p paths.Paths) Inventory {
	var inv Inventory
	for _, k := range known {
		bundle := k.bundle
		if bundle == "" {
			bundle = k.name
		}
		for _, dir := range p.Applications {
			path := filepath.Join(dir, bundle+".app")
			if st, err := os.Stat(path); err == nil && st.IsDir() {
				app := App{Name: k.name, Path: path, Kind: k.kind}
				if k.kind == Editor {
					inv.Editors = append(inv.Editors, app)
				} else {
					inv.Terminals = append(inv.Terminals, app)
				}
				break
			}
		}
	}
	return inv
}

// Pick returns the app named want (case-insensitive, "code"/"vscode" and
// "iterm" accepted), or the first installed one when want is empty.
func Pick(apps []App, want string) (App, error) {
	if len(apps) == 0 {
		return App{}, fmt.Errorf("none installed")
	}
	if want == "" {
		return apps[0], nil
	}
	w := strings.ToLower(strings.TrimSpace(want))
	aliases := map[string]string{"code": "visual studio code", "vscode": "visual studio code", "iterm": "iterm2", "sublime": "sublime text"}
	if a, ok := aliases[w]; ok {
		w = a
	}
	for _, a := range apps {
		if strings.ToLower(a.Name) == w {
			return a, nil
		}
	}
	var names []string
	for _, a := range apps {
		names = append(names, a.Name)
	}
	return App{}, fmt.Errorf("%q isn't installed (found: %s)", want, strings.Join(names, ", "))
}

const openTimeout = 10 * time.Second

// Opener runs `open`.
type Opener struct{ Exec exec.Runner }

func (o Opener) open(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	res, err := o.Exec.Run(ctx, exec.Spec{Name: "/usr/bin/open", Args: args})
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("open %s: %s", strings.Join(args, " "), strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// With opens target (a folder or file) in app.
func (o Opener) With(ctx context.Context, app App, target string) error {
	return o.open(ctx, "-a", app.Path, target)
}

// Reveal shows path selected in Finder.
func (o Opener) Reveal(ctx context.Context, path string) error {
	return o.open(ctx, "-R", path)
}

// URL opens a web address in the default browser.
func (o Opener) URL(ctx context.Context, url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("not a web address: %q", url)
	}
	return o.open(ctx, url)
}

// TerminalAt opens a new terminal window in dir.
func (o Opener) TerminalAt(ctx context.Context, app App, dir string) error {
	switch app.Name {
	case "Ghostty":
		return o.open(ctx, "-na", app.Path, "--args", "--working-directory="+dir)
	case "kitty":
		return o.open(ctx, "-na", app.Path, "--args", "--directory", dir)
	}
	// Terminal, iTerm2 and Warp open a window at a folder they're given.
	return o.open(ctx, "-a", app.Path, dir)
}

// RunScript opens a new terminal window running an executable script. Warp
// can't run a script it's handed, so Terminal is used for it instead.
func (o Opener) RunScript(ctx context.Context, app App, script string, fallback App) error {
	switch app.Name {
	case "Ghostty":
		return o.open(ctx, "-na", app.Path, "--args", "-e", script)
	case "kitty":
		return o.open(ctx, "-na", app.Path, "--args", script)
	case "Terminal", "iTerm2":
		return o.open(ctx, "-a", app.Path, script)
	}
	return o.open(ctx, "-a", fallback.Path, script)
}
