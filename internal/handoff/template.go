package handoff

// promptTemplate is the handoff prompt. It's written for an agent that has
// never seen this Mac: every path is absolute, every command is copy-ready,
// and every decision the owner already made is stated as a rule.
const promptTemplate = `# Update the TAW theme ` + "`{{.Theme.Dir}}`" + ` (site ` + "`{{.Site.Slug}}`" + `)

You're picking up a theme update on this Mac. taw-fleet gathered the context below on {{date .Now}}; re-check anything that matters before acting on it.

## The task
{{if and .Classic .HasSkill}}
Run the **update-theme** skill (` + "`.claude/skills/update-theme/SKILL.md`" + ` in this theme) to sync the framework-owned parts of the taw-theme scaffold{{if .Theme.Core.Behind}}, then update taw/core{{end}}.
{{else if .Classic}}
This theme doesn't have the update-theme skill yet. Read the canonical one first and follow it exactly: https://raw.githubusercontent.com/Relmaur/taw-theme/main/.claude/skills/update-theme/SKILL.md
{{else}}
This is a block theme built on taw-gutenberg. It has no scaffold sync (no ` + "`bin/taw sync`" + `), so the task is only {{if .Theme.Core.Behind}}the taw/core update below{{else}}to confirm it's current and report{{end}}.
{{end}}
{{template "facts" .}}
## What taw-fleet found

- **taw/core:** installed ` + "`{{v .Theme.Core.Installed}}`" + `{{if .Theme.Core.Latest}}, newest ` + "`{{v .Theme.Core.Latest}}`" + `{{end}}{{if .Theme.Core.Behind}} → **behind**{{else if .Theme.Core.Latest}} → current{{end}}{{if .Theme.Core.LockMismatch}}; composer.lock says ` + "`{{v .Theme.Core.Locked}}`" + ` (vendor/ and the lock disagree){{end}}
{{- with .Theme.Git}}
- **git:** on ` + "`{{if .Detached}}(detached){{else}}{{.Branch}}{{end}}`" + `{{if .DefaultBranch}} (default ` + "`{{.DefaultBranch}}`" + `){{end}}, {{if .Upstream}}tracking ` + "`{{.Upstream}}`" + `{{else}}no upstream{{end}}, {{if .Dirty}}**{{.Dirty}} uncommitted change(s)**{{else}}clean{{end}}{{if .Ahead}}, {{.Ahead}} unpushed commit(s){{end}}{{if .Behind}}, {{.Behind}} commit(s) to pull{{end}}
{{- else}}
- **git:** this theme isn't its own git repository
{{- end}}
{{- range .Findings}}
- {{.Severity}} ` + "`{{.Code}}`" + `{{if .Theme}} ({{.Theme}}){{end}}: {{.Message}}
{{- end}}

## Steps
{{range $i, $step := .Steps}}
{{inc $i}}. {{$step}}
{{- end}}

## Rules

- Never touch ` + "`Blocks/`" + `, ` + "`inc/options.php`" + `, ` + "`inc/performance.php`" + `, ` + "`inc/customizations.php`" + `, page templates or content.
- No ` + "`git push`" + `, no PR, no merge, no force anything without asking me. Never commit to ` + "`{{.Default}}`" + ` directly.{{if and .Theme.Git .Classic}}
- The skill says not to commit; for this handoff I'm asking you to commit on ` + "`{{.Branch}}`" + ` (step "Commit"). Everything else in the skill applies as written.{{end}}
- Don't touch the production site or any other Local site.
- If a command fails, stop and tell me what happened instead of working around it.

## Report back

taw/core before → after; what Tier 1 changed; which Tier 2 diffs I approved or declined; each UPGRADING.md check and its outcome; test results; the branch and commit. Then wait for me.
`

// factsTemplate is where everything is and how to run things, shared by the
// handoff and the batch prompt.
const factsTemplate = `{{define "facts"}}## Where everything is

| What | Where |
|---|---|
| Theme (work here) | ` + "`{{.Theme.RealPath}}`" + ` |{{if .Symlinked}}
| Linked from | ` + "`{{.Theme.Path}}`" + ` |{{end}}
| Site folder (Local by Flywheel) | ` + "`{{.Site.Path}}`" + ` |
| WordPress root | ` + "`{{.Site.WebRoot}}`" + ` |
| Local site | {{.Site.Name}} (id ` + "`{{.Site.ID}}`" + `), {{if .Running}}**running**{{else}}**not running**{{end}} |
| Site URL | {{.Site.URL}} (admin: {{.Site.URL}}/wp-admin/) |{{if .Production}}
| Production | {{.Production}} (don't touch it) |{{end}}{{if .Theme.Git}}{{if .Theme.Git.Repo}}
| Repository | {{.Theme.Git.Repo.WebURL}} |{{end}}{{end}}

{{- if .Notes}}

Notes about this site: {{.Notes}}
{{- end}}

## How to run things

Use the site's own PHP, not whatever ` + "`php`" + ` is on PATH:

` + "```bash" + `
cd {{shq .Theme.RealPath}}
{{shq .PHP}} --version{{if .Classic}}
{{shq .PHP}} bin/taw sync --json          # the skill's Step 1{{end}}
{{.Composer}} update taw/core --with-dependencies
` + "```" + `
{{if .WP}}
wp-cli for this site (Local's MySQL socket; only while the site runs):

` + "```bash" + `
{{.WP}} option get stylesheet
` + "```" + `
{{else}}
The site isn't running, so wp-cli and anything that boots WordPress won't work.{{if .Batch}} Mark checks that need WordPress ` + "`needs-wordpress`" + `; the coordinator starts sites once, for every theme.{{else}} When a check needs WordPress, ask me to start the site (` + "`taw-fleet start {{.Site.Slug}}`" + `, or in Local).{{end}}
{{end}}
{{end}}`
