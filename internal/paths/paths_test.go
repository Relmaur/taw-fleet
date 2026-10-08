package paths

import (
	"path/filepath"
	"testing"
)

func TestForHomeLayout(t *testing.T) {
	p := ForHome("/h", nil)
	if want := filepath.Join("/h", "Library", "Application Support", "Local"); p.LocalSupport != want {
		t.Errorf("LocalSupport = %q, want %q", p.LocalSupport, want)
	}
	if want := "/h/.config/taw-fleet"; p.ConfigDir != want {
		t.Errorf("ConfigDir = %q, want %q", p.ConfigDir, want)
	}
	if _, err := p.LookPath("git"); err == nil {
		t.Error("the default LookPath in tests must find nothing")
	}
}

func TestForHomeHonoursXDG(t *testing.T) {
	p := ForHome("/h", func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return "/xdg"
		}
		return ""
	})
	if p.ConfigDir != "/xdg/taw-fleet" {
		t.Errorf("ConfigDir = %q", p.ConfigDir)
	}
}

func TestExpand(t *testing.T) {
	p := ForHome("/Users/me", nil)
	cases := map[string]string{
		"~/Local Sites/taw":      "/Users/me/Local Sites/taw",
		"~":                      "/Users/me",
		"/abs/Local Sites/x/":    "/abs/Local Sites/x",
		"":                       "",
		"/Users/me/./sites/../a": "/Users/me/a",
	}
	for in, want := range cases {
		if got := p.Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultUsesOverride(t *testing.T) {
	t.Setenv(HomeEnv, "/tmp/fake-home")
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if p.Home != "/tmp/fake-home" {
		t.Errorf("Home = %q", p.Home)
	}
}
