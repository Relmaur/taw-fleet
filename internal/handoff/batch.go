package handoff

import (
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/Relmaur/taw-fleet/internal/site"
)

// SkillURL is the canonical update-theme skill, for a theme that doesn't
// have it yet.
const SkillURL = "https://raw.githubusercontent.com/Relmaur/taw-theme/main/.claude/skills/update-theme/SKILL.md"

// BuildBatch writes the prompt for one theme of an update-all: a subagent
// runs the update-theme skill's batch mode, which holds the rules; the
// prompt only adds the facts (paths, PHP, Composer, branch) and the owner's
// approvals.
func BuildBatch(in Input) (Prompt, error) {
	t := in.Theme
	if !t.IsTAW {
		return Prompt{}, ErrNotTAW
	}
	if IsUmbrella(t) {
		return Prompt{}, ErrUmbrella
	}
	d := newData(in)
	d.Batch = true
	var b strings.Builder
	if err := batchTmpl.Execute(&b, d); err != nil {
		return Prompt{}, err
	}
	return Prompt{
		Title:  fmt.Sprintf("Update %s (%s), batch", t.Dir, in.Site.Slug),
		Branch: d.Branch,
		Text:   collapseBlankLines(b.String()),
	}, nil
}

// Skip says why a theme is left out of an update-all, or "" when it's in.
// These are the batch mode's own preconditions (update-theme B1), checked
// up front so no subagent starts just to stop.
func Skip(t site.Theme) string {
	switch {
	case !t.IsTAW:
		return "not a TAW theme"
	case IsUmbrella(t):
		return "the canonical scaffold (updated by the taw-core release flow)"
	case t.Git == nil:
		return "not a git repository"
	case t.Git.Dirty > 0:
		return fmt.Sprintf("%d uncommitted change(s)", t.Git.Dirty)
	case t.Git.Detached || (t.Git.DefaultBranch != "" && t.Git.Branch != t.Git.DefaultBranch && !strings.HasPrefix(t.Git.Branch, "chore/")):
		return "on " + orDefault(t.Git.Branch, "a detached HEAD") + ", not " + orDefault(t.Git.DefaultBranch, "main")
	}
	return ""
}

// Needs reports whether a theme has anything to update: taw/core behind, or
// scaffold drift from the last check (or no check yet).
func Needs(t site.Theme) bool {
	if t.Core.Behind {
		return true
	}
	if t.Kind != site.KindClassic {
		return false
	}
	return t.Drift == nil || len(t.Drift.Tier1) > 0 || len(t.Drift.Tier2) > 0
}

// FleetTheme is one theme of an update-all, with its batch prompt saved to
// PromptFile.
type FleetTheme struct {
	Site       string // Local site folder
	Theme      string // theme folder name
	Path       string // the theme folder (real path)
	PromptFile string
	From, To   string // taw/core installed and newest ("" when current)
}

// FleetSkip is a theme left out, and why.
type FleetSkip struct {
	Site, Theme, Reason string
}

// Fleet writes the coordinator's prompt: one Claude Code session that runs
// a subagent per theme and brings every decision back to the owner at once.
func Fleet(themes []FleetTheme, skipped []FleetSkip, parallel int, now time.Time) Prompt {
	if parallel <= 0 {
		parallel = 3
	}
	var b strings.Builder
	_ = fleetTmpl.Execute(&b, struct {
		Themes   []FleetTheme
		Skipped  []FleetSkip
		Parallel int
		Now      time.Time
	}{themes, skipped, parallel, now})
	return Prompt{
		Title: fmt.Sprintf("Update %d TAW %s", len(themes), plural(len(themes), "theme", "themes")),
		Text:  collapseBlankLines(b.String()),
	}
}

func newData(in Input) data {
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
	return d
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

const batchTemplate = `# Update the TAW theme ` + "`{{.Theme.Dir}}`" + ` (site ` + "`{{.Site.Slug}}`" + `), batch mode

You're one of several agents updating TAW themes at once; taw-fleet gathered the facts below on {{date .Now}}. Nobody can answer questions during your run: decide by the rules, or stop with a reason.

## The task
{{if and .Classic .HasSkill}}
Read ` + "`.claude/skills/update-theme/SKILL.md`" + ` in this theme and follow its **§ "Batch mode"** (B1–B9) exactly. If that copy has no § "Batch mode" yet (the theme hasn't been synced since taw-theme v1.12.45), read the canonical skill instead ({{skillURL}}) until its step B4 refreshes the theme's copy.
{{else if .Classic}}
This theme doesn't have the update-theme skill yet: read the canonical one ({{skillURL}}) and follow its **§ "Batch mode"** (B1–B9) exactly. Its Tier 1 sync installs the skill in the theme.
{{else}}
This is a block theme built on taw-gutenberg, with no scaffold sync. Follow the update-theme skill's **§ "Batch mode"** ({{skillURL}}) without its sync steps (B4, B5): branch, ` + "`composer update taw/core --with-dependencies`" + `, the UPGRADING checks, verify, commit, and the result block.
{{end}}
- **Branch:** ` + "`{{.Branch}}`" + ` from ` + "`{{.Default}}`" + `.
- **Approved:** {{if .Theme.Core.Behind}}` + "`composer update taw/core --with-dependencies`" + ` ({{v .Theme.Core.Installed}} → {{v .Theme.Core.Latest}}; it may move taw/core's own dependencies too), and {{end}}committing on that branch. Nothing else: never push or open a PR.
- **Site:** {{if .Running}}running, so checks that need WordPress can run (don't stop it).{{else}}not running: mark checks that need WordPress ` + "`needs-wordpress`" + ` and don't start it.{{end}}
- Work only in the theme folder below; don't touch any other theme or site.

{{template "facts" .}}
## Report

End with the skill's B9 JSON block (` + "`\"theme\": \"{{.Theme.Dir}}\"`" + `, ` + "`\"site\": \"{{.Site.Slug}}\"`" + `). The coordinator reads only that block, so put every decision you left open in ` + "`proposals`" + ` or ` + "`notes`" + `.
`

const fleetTemplate = `# Update {{len .Themes}} TAW {{if eq (len .Themes) 1}}theme{{else}}themes{{end}} in one session

You're coordinating an update of the owner's TAW client themes on this Mac (taw-fleet, {{date .Now}}). Each theme is updated by its own subagent following the update-theme skill's batch mode; you bring every decision back to the owner at once.

## The themes

| Site | Theme | taw/core | Prompt |
|---|---|---|---|
{{- range .Themes}}
| {{.Site}} | {{.Theme}} | {{if .To}}{{v .From}} → {{v .To}}{{else}}current{{end}} | ` + "`{{.PromptFile}}`" + ` |
{{- end}}
{{if .Skipped}}
Left out (tell the owner in the final report; don't touch them):
{{range .Skipped}}
- {{.Site}} / {{.Theme}}: {{.Reason}}
{{- end}}
{{end}}
## 1. Run the subagents

For each theme, start a subagent with the Agent tool (` + "`subagent_type: \"general-purpose\"`" + `), prompt: "Read <its prompt file> and follow it exactly." Run **at most {{.Parallel}} at a time** (Composer and npm compete on this Mac): start {{.Parallel}}, and start the next as each finishes. Don't do a theme's work yourself, and don't ask the owner anything while they run.

Each subagent ends with a JSON block (` + "`status`" + `, ` + "`branch`" + `, ` + "`commit`" + `, ` + "`taw_core`" + `, ` + "`tier1`" + `, ` + "`manifests`" + `, ` + "`proposals`" + `, ` + "`skills`" + `, ` + "`upgrading`" + `, ` + "`verify`" + `, ` + "`notes`" + `). Keep each one. If a reply has no JSON block, record the theme as ` + "`failed`" + ` with the reply's last lines.

## 2. One round of decisions

When every subagent has finished, ask the owner with AskUserQuestion (multi-select, grouped; several questions per call):

1. **Tier 2 proposals.** Group them by file across themes (e.g. "AGENTS.md in ls-mexico, chcapital…"), with each summary and anything an overwrite would lose. For the approved ones, apply them yourself on that theme's branch the way the skill's Step 3 says (prose files: the canonical file, keeping site-specific sections; a manifest ` + "`review`" + ` item: that line only), run the theme's tests again, and commit ("Apply approved scaffold docs").
2. **Sites to start**, when any check is ` + "`needs-wordpress`" + `: which sites to start for those checks. Start the chosen ones with ` + "`taw-fleet start <site> --yes`" + `, run just those checks (with that theme's wp-cli from its prompt file), record the outcomes, then stop the sites you started with ` + "`taw-fleet stop <site> --yes`" + `.
3. **Pushes**, last: which branches to push and open a PR for (only themes whose status is ` + "`updated`" + ` or whose open points are settled). For each chosen theme: ` + "`git push -u origin <branch>`" + ` and ` + "`gh pr create`" + ` against its default branch, with the result as the PR body. Nothing is pushed without this answer.

## 3. Final report

One table: site, theme, status, taw/core before → after, Tier 1, manifests applied, UPGRADING checks (passed / not applicable / open), tests · phpstan · build, branch and commit, PR link. Then the left-out themes and why, every ` + "`failed`" + ` or open item, and the skills the subagents reported under ` + "`warn`" + ` or ` + "`clash`" + `.

## Rules

- Work only in the theme folders listed above. Never touch Blocks/, inc/options.php, inc/performance.php, inc/customizations.php, templates or content (the subagents follow the same rules).
- No push, no PR, no merge without the owner's answer in step 2; never commit to a default branch.
- Don't touch the production sites. Starting and stopping Local sites only as approved in step 2.
- When you're done and the owner has nothing else, say so: they close this window with /exit, and taw-fleet rescans.
`

var (
	batchTmpl = template.Must(template.New("batch").Funcs(funcs()).Parse(factsTemplate + batchTemplate))
	fleetTmpl = template.Must(template.New("fleet").Funcs(funcs()).Parse(fleetTemplate))
)

func funcs() template.FuncMap {
	return template.FuncMap{
		"shq":      shq,
		"inc":      func(i int) int { return i + 1 },
		"v":        func(s string) string { return strings.TrimPrefix(s, "v") },
		"date":     func(t time.Time) string { return t.Format("2006-01-02 15:04") },
		"skillURL": func() string { return SkillURL },
	}
}
