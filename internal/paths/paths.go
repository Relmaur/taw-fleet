// Package paths holds every filesystem root and environment lookup taw-fleet
// uses, so tests can point the whole program at a temporary directory.
package paths

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// HomeEnv overrides the home directory. A temporary home with a copy of
// Local's sites.json is enough to run taw-fleet against fake data.
const HomeEnv = "TAW_FLEET_HOME"

// Paths are the roots taw-fleet reads from.
type Paths struct {
	Home         string   // the user's home directory
	LocalSupport string   // ~/Library/Application Support/Local
	LocalApp     string   // /Applications/Local.app
	CacheDir     string   // ~/Library/Caches/taw-fleet
	ConfigDir    string   // $XDG_CONFIG_HOME/taw-fleet or ~/.config/taw-fleet
	Applications []string // where apps are installed, searched in order

	Getenv   func(string) string
	LookPath func(string) (string, error)
}

// Default resolves the paths for the current user. $TAW_FLEET_HOME replaces
// the home directory when set.
func Default() (Paths, error) {
	home := os.Getenv(HomeEnv)
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		home = h
	}
	if home == "" {
		return Paths{}, errors.New("no home directory")
	}
	p := ForHome(home, os.Getenv)
	p.LookPath = exec.LookPath
	return p, nil
}

// ForHome builds the paths under a given home directory. Tests use it with a
// t.TempDir() and a fake getenv.
func ForHome(home string, getenv func(string) string) Paths {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	config := filepath.Join(home, ".config", "taw-fleet")
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		config = filepath.Join(xdg, "taw-fleet")
	}
	return Paths{
		Home:         home,
		LocalSupport: filepath.Join(home, "Library", "Application Support", "Local"),
		LocalApp:     "/Applications/Local.app",
		CacheDir:     filepath.Join(home, "Library", "Caches", "taw-fleet"),
		ConfigDir:    config,
		Applications: []string{"/Applications", filepath.Join(home, "Applications"), "/System/Applications/Utilities"},
		Getenv:       getenv,
		LookPath:     func(string) (string, error) { return "", exec.ErrNotFound },
	}
}

// Expand turns a "~/..." path into an absolute one under Home and cleans it.
func (p Paths) Expand(path string) string {
	switch {
	case path == "~":
		return p.Home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(p.Home, path[2:])
	case path == "":
		return ""
	}
	return filepath.Clean(path)
}
