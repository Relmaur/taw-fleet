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
bugsmash_project = "a2f16102-91d0-4968-a010-fca3146f4596"
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Editor != "code" || c.Terminal != "Ghostty" || c.Site("ls-mxico").ProductionURL != "https://lsmexico.mx" ||
		c.Site("ls-mxico").BugSmashProject != "a2f16102-91d0-4968-a010-fca3146f4596" {
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
	write(t, p, "[sites.x]\nbugsmash_project = \"https://emelambda.bugsmash.io/review/z4KWg\"")
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "bugsmash_project must be the project's id") {
		t.Errorf("bad project id: %v", err)
	}
	write(t, p, `icons = "nerds"`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), `icons must be "symbols" or "nerd"`) {
		t.Errorf("bad icons: %v", err)
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
		"# production_url", "production_url", "# notes", "notes", "# bugsmash_project", "bugsmash_project").Replace(Template)
	if _, err := toml.Decode(uncommented, &c); err != nil || c.Site("ls-mxico").ProductionURL == "" || c.Site("ls-mxico").BugSmashProject == "" {
		t.Errorf("examples: %+v %v", c, err)
	}
}

func TestSetTopKeepsTheRest(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	f, err := SetTop(p, "icons", "nerd") // no file yet: the template, then the key
	if err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.Icons != "nerd" {
		t.Fatalf("icons = %q, %v", c.Icons, err)
	}
	data, _ := os.ReadFile(f)
	if !strings.Contains(string(data), "# icons = \"nerd\"\nicons = \"nerd\"") || !strings.Contains(string(data), "# [sites.ls-mxico]") {
		t.Errorf("set under its example, the rest kept:\n%s", data)
	}
	// A config with a table and no example: the key goes above the table.
	if err := os.WriteFile(f, []byte("editor = \"Zed\"\n\n[sites.acme]\nnotes = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetTop(p, "icons", "symbols"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetTop(p, "icons", "nerd"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(f)
	if string(data) != "editor = \"Zed\"\n\nicons = \"nerd\"\n\n[sites.acme]\nnotes = \"x\"\n" {
		t.Errorf("got:\n%s", data)
	}
}
