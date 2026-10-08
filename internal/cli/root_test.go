package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/scan"
)

func run(t *testing.T, p paths.Paths, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	d := Deps{Paths: p, Runner: &exec.FakeRunner{}, Out: &out, Err: &out, Dark: true}
	root := NewRoot(BuildInfo{Version: "1.2.3", Commit: "abc123"}, d)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture: one TAW site (running, two themes, one symlinked) and one plain site.
func fixture(t *testing.T) paths.Paths {
	t.Helper()
	home := t.TempDir()
	p := paths.ForHome(home, nil)
	themes := func(slug string) string {
		return filepath.Join(home, "Local Sites", slug, "app", "public", "wp-content", "themes")
	}
	write(t, filepath.Join(themes("acme"), "acme-theme", "composer.json"), `{"name":"taw/theme","require":{"taw/core":"^1.0"}}`)
	gut := filepath.Join(home, "umbrella", "taw-gutenberg")
	write(t, filepath.Join(gut, "composer.json"), `{"name":"taw/gutenberg","type":"wordpress-theme","require":{"taw/core":"^1.50"}}`)
	if err := os.Symlink(gut, filepath.Join(themes("acme"), "taw-gutenberg")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(themes("plain"), "plain", "style.css"), "")
	write(t, filepath.Join(p.LocalSupport, "sites.json"), `{
	  "a1": {"id":"a1","name":"Acme","path":"~/Local Sites/acme","domain":"acme.local",
	         "services":{"php":{"name":"php","version":"8.2.30"}}},
	  "p1": {"id":"p1","name":"Plain","path":"~/Local Sites/plain","domain":"plain.local"}
	}`)
	write(t, filepath.Join(p.LocalSupport, "site-statuses.json"), `{"a1":"running","p1":"halted"}`)
	return p
}

func TestVersionPrintsBuildInfo(t *testing.T) {
	out, err := run(t, paths.ForHome(t.TempDir(), nil), "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if want := "taw-fleet 1.2.3 (abc123)\n"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestVersionRejectsArguments(t *testing.T) {
	if _, err := run(t, paths.ForHome(t.TempDir(), nil), "version", "extra"); err == nil {
		t.Fatal("expected an error for an extra argument")
	}
}

func TestListTable(t *testing.T) {
	out, err := run(t, fixture(t), "list")
	if err != nil {
		t.Fatal(err)
	}
	// Output to a buffer is never colored.
	if strings.Contains(out, "\x1b[") {
		t.Errorf("escape codes in non-terminal output:\n%s", out)
	}
	for _, want := range []string{
		"1 site", "2 TAW themes", "1 running",
		"acme", "acme-theme", "taw-gutenberg ↗", "CLASSIC", "BLOCK", "8.2.30", "acme.local",
		"1 more site without a TAW theme (--all)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "plain.local") {
		t.Errorf("the non-TAW site must be hidden:\n%s", out)
	}
}

func TestListAll(t *testing.T) {
	out, err := run(t, fixture(t), "list", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plain.local") || !strings.Contains(out, "other") {
		t.Errorf("--all must show the plain site:\n%s", out)
	}
}

func TestListJSON(t *testing.T) {
	out, err := run(t, fixture(t), "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rep scan.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rep.Sites) != 1 || rep.Sites[0].Slug != "acme" || len(rep.Sites[0].Themes) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	var symlinks int
	for _, th := range rep.Sites[0].Themes {
		if !th.IsTAW {
			t.Errorf("non-TAW theme in default JSON: %+v", th)
		}
		if th.Symlink {
			symlinks++
		}
	}
	if symlinks != 1 {
		t.Errorf("symlinks = %d", symlinks)
	}
	// snake_case keys are the scripting contract.
	for _, key := range []string{`"scanned_at"`, `"is_taw"`, `"web_root"`, `"socket_live"`} {
		if !strings.Contains(out, key) {
			t.Errorf("missing key %s", key)
		}
	}
}

func TestListWithoutLocal(t *testing.T) {
	out, err := run(t, paths.ForHome(t.TempDir(), nil), "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no Local by Flywheel sites found") || !strings.Contains(out, "No TAW sites found") {
		t.Errorf("out:\n%s", out)
	}
}
