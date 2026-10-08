package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/Relmaur/taw-fleet/internal/paths"
)

func write(t *testing.T, p paths.Paths, body string) {
	t.Helper()
	if err := os.MkdirAll(p.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(File(p), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	c, err := Load(paths.ForHome(t.TempDir(), nil))
	if err != nil || c.Editor != "" || c.Site("x").ProductionURL != "" {
		t.Errorf("c=%+v err=%v", c, err)
	}
}

func TestLoad(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	write(t, p, `
editor = "code"
terminal = "Ghostty"
[sites.ls-mxico]
production_url = "https://lsmexico.mx"
notes = "deploys from main"
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Editor != "code" || c.Terminal != "Ghostty" || c.Site("ls-mxico").ProductionURL != "https://lsmexico.mx" {
		t.Errorf("c = %+v", c)
	}
}

func TestLoadRejectsTyposAndBadURLs(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	write(t, p, `edtor = "Cursor"`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "unknown keys: edtor") {
		t.Errorf("typo: %v", err)
	}
	write(t, p, "[sites.x]\nproduction_url = \"lsmexico.mx\"")
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "must start with https://") {
		t.Errorf("bad url: %v", err)
	}
	write(t, p, `editor = `)
	if _, err := Load(p); err == nil {
		t.Error("invalid TOML")
	}
}

func TestInitWritesAValidTemplateOnce(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	f, err := Init(p)
	if err != nil || f != filepath.Join(p.ConfigDir, "config.toml") {
		t.Fatalf("f=%s err=%v", f, err)
	}
	if _, err := Load(p); err != nil {
		t.Errorf("the template must load: %v", err)
	}
	if _, err := Init(p); err == nil {
		t.Error("Init must not overwrite")
	}
	// Uncommented, the examples are valid too.
	var c Config
	uncommented := strings.NewReplacer("# editor", "editor", "# terminal", "terminal", "# [sites", "[sites",
		"# production_url", "production_url", "# notes", "notes").Replace(Template)
	if _, err := toml.Decode(uncommented, &c); err != nil || c.Site("ls-mxico").ProductionURL == "" {
		t.Errorf("examples: %+v %v", c, err)
	}
}
