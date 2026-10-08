package composer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"
)

// ErrNotInstalled means the package isn't in the file (or the file is
// missing): vendor/ was never installed, or the lock doesn't have it.
var ErrNotInstalled = errors.New("not installed")

type pkg struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InstalledVersion reads vendor/composer/installed.json: what's really on
// disk. Composer 2 writes {"packages": [...]}, Composer 1 a bare array.
func InstalledVersion(themeDir, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(themeDir, "vendor", "composer", "installed.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotInstalled
	}
	if err != nil {
		return "", err
	}
	var v2 struct {
		Packages []pkg `json:"packages"`
	}
	if err := json.Unmarshal(data, &v2); err != nil {
		var v1 []pkg
		if err1 := json.Unmarshal(data, &v1); err1 != nil {
			return "", fmt.Errorf("installed.json: %w", err)
		}
		v2.Packages = v1
	}
	return find(v2.Packages, name)
}

// LockedVersion reads composer.lock: what the theme's repo pins.
func LockedVersion(themeDir, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(themeDir, "composer.lock"))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotInstalled
	}
	if err != nil {
		return "", err
	}
	var lock struct {
		Packages    []pkg `json:"packages"`
		PackagesDev []pkg `json:"packages-dev"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return "", fmt.Errorf("composer.lock: %w", err)
	}
	return find(append(lock.Packages, lock.PackagesDev...), name)
}

func find(pkgs []pkg, name string) (string, error) {
	for _, p := range pkgs {
		if p.Name == name {
			return p.Version, nil
		}
	}
	return "", ErrNotInstalled
}

// Canonical turns "1.76.1" or "v1.76.1" into "v1.76.1", and returns "" for
// anything that isn't a release version ("dev-main", "1.x-dev").
func Canonical(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return semver.Canonical(v)
}

// Older reports whether installed is a lower release than latest. Versions
// that aren't releases never count as older.
func Older(installed, latest string) bool {
	a, b := Canonical(installed), Canonical(latest)
	return a != "" && b != "" && semver.Compare(a, b) < 0
}

// SameVersion compares two versions ignoring a leading "v"; non-release
// versions compare as strings.
func SameVersion(a, b string) bool {
	ca, cb := Canonical(a), Canonical(b)
	if ca != "" && cb != "" {
		return ca == cb
	}
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}
