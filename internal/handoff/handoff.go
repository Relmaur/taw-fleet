// Package handoff writes the prompt that hands a theme update to a coding
// agent (Claude Code, Cursor…): what to do (the update-theme skill), where
// everything is on this Mac, the state taw-fleet found, and the rules.
//
// The prompt is plain Markdown built from the scan; nothing here runs a
// command. Delivery (clipboard, a new terminal running Claude Code) is the
// caller's job.
package handoff

import (
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/Relmaur/taw-fleet/internal/site"
)

// ErrUmbrella refuses the umbrella's own checkouts of taw-theme and
// taw-gutenberg: they're the canonical scaffolds, updated by the taw-core
// release flow, not by update-theme.
var ErrUmbrella = errors.New("this is the canonical scaffold (an umbrella checkout); it's updated by the taw-core release flow, not by update-theme")

// ErrNotTAW refuses themes that aren't TAW themes.
var ErrNotTAW = errors.New("not a TAW theme")

// Toolchain is how to run things for this site. Empty fields fall back to
// what's on PATH.
type Toolchain struct {
	PHP      string // the site's PHP from Local
	Composer string // Local's composer.phar
	WPCli    string // Local's wp-cli.phar
}

// Input is everything the prompt is built from.
type Input struct {
	Site       site.Site
	Theme      site.Theme
	Findings   []site.Finding // this site's doctor findings
	Tools      Toolchain
	HasSkill   bool   // .claude/skills/update-theme/SKILL.md exists in the theme
	Production string // production URL from the config, if any
	Notes      string // notes from the config, if any
	Now        time.Time
}

// Prompt is the result.
type Prompt struct {
	Title  string
	Branch string // the branch the agent is asked to work on
	Text   string
}

// Build writes the prompt, or refuses with ErrUmbrella / ErrNotTAW.
func Build(in Input) (Prompt, error) {
	t := in.Theme
	if !t.IsTAW {
		return Prompt{}, ErrNotTAW
	}
	if IsUmbrella(t) {
		return Prompt{}, ErrUmbrella
	}
	d := data{Input: in, Branch: Branch(in)}
	d.PHP = orDefault(in.Tools.PHP, "php")
	d.Composer = "composer"
	if in.Tools.Composer != "" {
		d.Composer = shq(d.PHP) + " " + shq(in.Tools.Composer)
	}
	if in.Site.SockLive && in.Tools.WPCli != "" {
		d.WP = fmt.Sprintf("%s -d mysqli.default_socket=%s -d pdo_mysql.default_socket=%s %s --path=%s",
			shq(d.PHP), shq(in.Site.Socket), shq(in.Site.Socket), shq(in.Tools.WPCli), shq(in.Site.WebRoot))
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, d); err != nil {
		return Prompt{}, err
	}
	return Prompt{
		Title:  fmt.Sprintf("Update %s (%s)", t.Dir, in.Site.Slug),
		Branch: d.Branch,
		Text:   collapseBlankLines(b.String()),
	}, nil
}

// IsUmbrella reports whether the theme is the umbrella's own taw-theme or
// taw-gutenberg checkout (not a client fork).
func IsUmbrella(t site.Theme) bool {
	if t.Git == nil || t.Git.Repo == nil {
		return false
	}
	name := strings.ToLower(t.Git.Repo.FullName())
	return name == "relmaur/taw-theme" || name == "relmaur/taw-gutenberg"
}

// Branch is the branch the agent works on: chore/taw-core-<version> when
// taw/core is behind, else chore/update-theme-<date>.
func Branch(in Input) string {
	if in.Theme.Core.Behind && in.Theme.Core.Latest != "" {
		return "chore/taw-core-" + strings.TrimPrefix(in.Theme.Core.Latest, "v")
	}
	return "chore/update-theme-" + in.Now.Format("2006-01-02")
}

type data struct {
	Input
	Branch   string
	PHP      string
	Composer string
	WP       string // full wp-cli invocation, "" when the site isn't running
}

func (d data) Classic() bool { return d.Theme.Kind == site.KindClassic }

func (d data) OnDefault() bool {
	g := d.Theme.Git
	return g != nil && (g.DefaultBranch == "" || g.Branch == g.DefaultBranch)
}

func (d data) Default() string {
	if g := d.Theme.Git; g != nil && g.DefaultBranch != "" {
		return g.DefaultBranch
	}
	return "main"
}

func (d data) Running() bool { return d.Site.Status == site.StatusRunning && d.Site.SockLive }

func (d data) Symlinked() bool { return d.Theme.Symlink }

// Steps are the numbered instructions; which ones apply depends on the theme.
func (d data) Steps() []string {
	t, g := d.Theme, d.Theme.Git
	c := func(s string) string { return "`" + s + "`" }
	var steps []string

	switch {
	case g == nil:
		steps = append(steps, "**Start.** There's no git repository: don't create one. Make the changes and leave them for me to review.")
	case g.Dirty > 0:
		step := fmt.Sprintf("**Start clean.** The working tree had %d uncommitted change(s). Run %s; if they're still there, **stop and ask me** what to do with them. Don't stash, commit or discard them yourself.", g.Dirty, c("git status"))
		if !d.OnDefault() {
			step += fmt.Sprintf(" You're also on %s, not %s: ask me which branch to work from.", c(g.Branch), c(d.Default()))
		}
		steps = append(steps, step)
	case !d.OnDefault():
		steps = append(steps, fmt.Sprintf("**Start clean.** The theme is on %s, not %s. **Ask me** whether to continue on it or start fresh from %s.", c(g.Branch), c(d.Default()), c(d.Default())))
	default:
		steps = append(steps, fmt.Sprintf("**Start clean.** Run %s (expect clean) and %s on %s.", c("git status"), c("git pull --ff-only"), c(d.Default())))
	}
	if g != nil {
		steps = append(steps, fmt.Sprintf("**Branch.** Create %s from an up-to-date %s and do everything on it. If it already exists, ask me.", c(d.Branch), c(d.Default())))
	}
	if d.Classic() {
		steps = append(steps, fmt.Sprintf("**Sync the scaffold** with the skill: %s, then %s for Tier 1 (no confirmation needed). For **Tier 2**, show me each diff and apply only what I approve; edit %s and %s line by line, never overwrite them.",
			c("bin/taw sync --json"), c("bin/taw sync --apply"), c("composer.json"), c("package.json")))
	}
	from, to := strings.TrimPrefix(t.Core.Installed, "v"), strings.TrimPrefix(t.Core.Latest, "v")
	if t.Core.Behind {
		steps = append(steps, fmt.Sprintf("**taw/core.** **You have my approval** to run %s (%s → %s). Then read %s and work through **every section newer than %s**: run each **Check** and note its outcome (\"not applicable\" is fine, skipping one isn't).",
			c("composer update taw/core"), from, to, c("vendor/taw/core/UPGRADING.md"), from))
	} else {
		steps = append(steps, "**taw/core** is current; no update needed. Mention it in the report.")
	}
	verify := fmt.Sprintf("**Verify.** Run what the theme has: %s, %s, %s if assets changed.", c("composer run test"), c("composer run phpstan"), c("npm run build"))
	if d.Running() {
		verify += fmt.Sprintf(" Load %s and a wp-admin screen with metaboxes; look for PHP errors in %s if it exists.", d.Site.URL, c(d.Site.WebRoot+"/wp-content/debug.log"))
	} else {
		verify += " Checks that need WordPress wait until I start the site."
	}
	steps = append(steps, verify)
	if g != nil && (d.Classic() || t.Core.Behind) {
		msg := "Sync theme scaffold"
		switch {
		case t.Core.Behind && d.Classic():
			msg = "Update taw/core to " + to + "; sync theme scaffold"
		case t.Core.Behind:
			msg = "Update taw/core to " + to
		}
		steps = append(steps, fmt.Sprintf("**Commit** on %s with a clear message (e.g. \"%s\"). Then **ask me before pushing** or opening a PR.", c(d.Branch), msg))
	}
	return steps
}

var tmpl = template.Must(template.New("handoff").Funcs(template.FuncMap{
	"shq":  shq,
	"inc":  func(i int) int { return i + 1 },
	"v":    func(s string) string { return strings.TrimPrefix(s, "v") },
	"date": func(t time.Time) string { return t.Format("2006-01-02 15:04") },
}).Parse(promptTemplate))

// shq quotes a value for a POSIX shell command shown in the prompt.
func shq(s string) string {
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@+,", r)
	}
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// collapseBlankLines keeps the template readable without leaving runs of
// empty lines where a section was skipped.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n")) + "\n"
}
