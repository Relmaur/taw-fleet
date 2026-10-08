package composer

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestInstalledVersion(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstalledVersion(dir, CorePackage); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("no vendor: err = %v", err)
	}

	write(t, filepath.Join(dir, "vendor", "composer", "installed.json"),
		`{"packages":[{"name":"symfony/yaml","version":"v7.1.0"},{"name":"taw/core","version":"v1.76.1"}],"dev":true}`)
	if v, err := InstalledVersion(dir, CorePackage); err != nil || v != "v1.76.1" {
		t.Errorf("v2 format: %q %v", v, err)
	}
	if _, err := InstalledVersion(dir, "taw/missing"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("missing package: %v", err)
	}

	old := t.TempDir()
	write(t, filepath.Join(old, "vendor", "composer", "installed.json"), `[{"name":"taw/core","version":"1.2.0"}]`)
	if v, err := InstalledVersion(old, CorePackage); err != nil || v != "1.2.0" {
		t.Errorf("v1 format: %q %v", v, err)
	}

	bad := t.TempDir()
	write(t, filepath.Join(bad, "vendor", "composer", "installed.json"), `{"packages":`)
	if _, err := InstalledVersion(bad, CorePackage); err == nil || errors.Is(err, ErrNotInstalled) {
		t.Errorf("invalid JSON: %v", err)
	}
}

func TestLockedVersion(t *testing.T) {
	dir := t.TempDir()
	if _, err := LockedVersion(dir, CorePackage); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("no lock: %v", err)
	}
	write(t, filepath.Join(dir, "composer.lock"),
		`{"packages":[{"name":"reactiph/taw-bridge","version":"v0.4.0"}],"packages-dev":[{"name":"taw/core","version":"v1.59.2"}]}`)
	if v, err := LockedVersion(dir, CorePackage); err != nil || v != "v1.59.2" {
		t.Errorf("lock (dev section): %q %v", v, err)
	}
}

func TestVersionHelpers(t *testing.T) {
	canon := map[string]string{"1.76.1": "v1.76.1", "v1.59.2": "v1.59.2", "v2": "v2.0.0", "dev-main": "", "": "", "1.x-dev": ""}
	for in, want := range canon {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
	if !Older("v1.59.2", "v1.76.1") || Older("v1.76.1", "v1.76.1") || Older("v1.77.0", "v1.76.1") {
		t.Error("Older on releases")
	}
	if !Older("1.9.0", "v1.10.0") {
		t.Error("Older must compare numerically, not as strings")
	}
	if Older("dev-main", "v1.76.1") || Older("v1.0.0", "") {
		t.Error("non-releases are never older")
	}
	if !SameVersion("1.76.1", "v1.76.1") || SameVersion("v1.76.1", "v1.76.0") || !SameVersion("dev-main", "dev-main") {
		t.Error("SameVersion")
	}
}
