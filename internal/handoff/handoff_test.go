package handoff

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/site"
)

var update = flag.Bool("update", false, "rewrite the golden prompts")

var now = time.Date(2026, 10, 8, 15, 30, 0, 0, time.UTC)

func lsMexico() Input {
	return Input{
		Site: site.Site{
			ID: "EQLMLW4r9", Name: "LS Mexico - TAW", Slug: "ls-mxico", Status: site.StatusHalted,
			Path: "/Users/me/Local Sites/ls-mxico", WebRoot: "/Users/me/Local Sites/ls-mxico/app/public",
			URL: "http://ls-mexico.local", Socket: "/Users/me/Library/Application Support/Local/run/EQLMLW4r9/mysql/mysqld.sock",
		},
		Theme: site.Theme{
			Dir: "ls-mexico", IsTAW: true, Kind: site.KindClassic, HasBinTaw: true,
			Path:     "/Users/me/Local Sites/ls-mxico/app/public/wp-content/themes/ls-mexico",
			RealPath: "/Users/me/Local Sites/ls-mxico/app/public/wp-content/themes/ls-mexico",
			Core:     site.CoreInfo{Installed: "v1.59.2", Locked: "v1.59.2", Latest: "v1.76.1", Behind: true},
			Git: &site.GitInfo{Branch: "main", DefaultBranch: "main", Upstream: "origin/main",
				Repo: &site.Repo{Host: "github.com", Owner: "Relmaur", Name: "ls-mexico--theme"}},
		},
		Findings: []site.Finding{{Severity: site.Warn, Code: "core.behind", Theme: "ls-mexico", Message: "taw/core v1.59.2, latest is v1.76.1"}},
		Tools: Toolchain{
			PHP:      "/Users/me/Library/Application Support/Local/lightning-services/php-8.2.30+1/bin/darwin-arm64/bin/php",
			Composer: "/Applications/Local.app/Contents/Resources/extraResources/bin/composer/composer.phar",
			WPCli:    "/Applications/Local.app/Contents/Resources/extraResources/bin/wp-cli/wp-cli.phar",
		},
		HasSkill:   true,
		Production: "https://lsmexico.mx",
		Now:        now,
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".md")
	if *update {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs:\n--- got ---\n%s", name, got)
	}
}

func TestBehindHaltedClean(t *testing.T) {
	p, err := Build(lsMexico())
	if err != nil {
		t.Fatal(err)
	}
	if p.Branch != "chore/taw-core-1.76.1" || p.Title != "Update ls-mexico (ls-mxico)" {
		t.Errorf("prompt = %+v", p)
	}
	for _, want := range []string{
		"update-theme", "**You have my approval**", "1.59.2 → 1.76.1", "every section newer than 1.59.2",
		"'/Users/me/Local Sites/ls-mxico/app/public/wp-content/themes/ls-mexico'", // quoted: has a space
		"composer.phar update taw/core", "**not running**", "Local by Flywheel → LS Mexico - TAW → Start site",
		"https://lsmexico.mx (don't touch it)", "ask me before pushing", "chore/taw-core-1.76.1",
	} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(p.Text, "option get stylesheet") {
		t.Error("no wp-cli command while the site is halted")
	}
	golden(t, "behind-halted", p.Text)
}

func TestDirtyOffDefaultRunning(t *testing.T) {
	in := lsMexico()
	in.Site.Status, in.Site.SockLive = site.StatusRunning, true
	in.Theme.Core = site.CoreInfo{Installed: "v1.76.1", Locked: "v1.76.1", Latest: "v1.76.1"}
	in.Theme.Git.Branch, in.Theme.Git.Upstream, in.Theme.Git.Dirty = "chore/taw-core-1.76.1", "", 5
	in.Production = ""
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Branch != "chore/update-theme-2026-10-08" {
		t.Errorf("branch = %s", p.Branch)
	}
	for _, want := range []string{"**stop and ask me**", "5 uncommitted change(s)", "is current; no update needed",
		"mysqli.default_socket='/Users/me/Library/Application Support/Local/run/EQLMLW4r9/mysql/mysqld.sock'", "option get stylesheet", "**running**"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("missing %q", want)
		}
	}
	golden(t, "current-dirty-running", p.Text)
}

func TestBlockThemeAndNoSkill(t *testing.T) {
	in := lsMexico()
	in.Theme.Kind, in.HasSkill = site.KindGutenberg, false
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Text, "bin/taw sync --json") || strings.Contains(p.Text, "Sync the scaffold") || !strings.Contains(p.Text, "block theme") {
		t.Errorf("a block theme has no scaffold sync:\n%s", p.Text)
	}
	if !strings.Contains(p.Text, `"Update taw/core to 1.76.1"`) {
		t.Error("block theme commit message")
	}
	// Steps are numbered without gaps.
	for i := 1; i <= 5; i++ {
		if !strings.Contains(p.Text, "\n"+string(rune('0'+i))+". ") {
			t.Errorf("step %d missing", i)
		}
	}

	in.Theme.Kind = site.KindClassic
	p, _ = Build(in)
	if !strings.Contains(p.Text, "raw.githubusercontent.com/Relmaur/taw-theme/main/.claude/skills/update-theme/SKILL.md") {
		t.Error("a classic theme without the skill gets the canonical one")
	}
}

func TestNoGit(t *testing.T) {
	in := lsMexico()
	in.Theme.Git = nil
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Text, "isn't its own git repository") || strings.Contains(p.Text, "**Branch.**") || strings.Contains(p.Text, "**Commit**") {
		t.Errorf("no-git prompt:\n%s", p.Text)
	}
}

func TestRefusals(t *testing.T) {
	in := lsMexico()
	in.Theme.Git.Repo = &site.Repo{Host: "github.com", Owner: "Relmaur", Name: "taw-theme"}
	if _, err := Build(in); !errors.Is(err, ErrUmbrella) {
		t.Errorf("umbrella: %v", err)
	}
	in = lsMexico()
	in.Theme.IsTAW = false
	if _, err := Build(in); !errors.Is(err, ErrNotTAW) {
		t.Errorf("not TAW: %v", err)
	}
}

func TestShq(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/php":          "/usr/bin/php",
		"/Users/me/Local Sites": "'/Users/me/Local Sites'",
		"it's":                  `'it'\''s'`,
		"":                      "''",
		"$(rm -rf ~)":           "'$(rm -rf ~)'",
	}
	for in, want := range cases {
		if got := shq(in); got != want {
			t.Errorf("shq(%q) = %s, want %s", in, got, want)
		}
	}
}
