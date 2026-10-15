# AGENTS.md — taw-fleet

A Go terminal app that finds the TAW sites on a Mac (through Local by Flywheel's own files) and
shows their status, versions and git state. It also has shortcuts: open the theme in an editor,
open the repo, start or stop the site. Part of the TAW umbrella (`TAW/taw-fleet`, a submodule).
Read the umbrella's AGENTS.md and `docs/STATE.md` first. Plan: umbrella `docs/plans/taw-fleet.md`.
Decision record: `docs/adr/0001-go-tui-for-the-local-taw-fleet.md`.

## Layout

| Path | Purpose |
|---|---|
| `cmd/taw-fleet/main.go` | Entry point; `version`/`commit` are set by the release build, else read from Go's build info (`go install`) |
| `internal/cli` | cobra commands (`list`, `show`, `doctor`, `version`); table or `--json`; global `--offline` |
| `internal/paths` | Every filesystem root (`Home`, `LocalSupport`, `LocalApp`, `CacheDir`, `DataDir` for what must outlive a cache, …); `$TAW_FLEET_HOME` overrides `~` |
| `internal/exec` | `Runner` (argument lists + context timeout), `OSRunner`, `FakeRunner` |
| `internal/local` | Local's `sites.json`, `site-statuses.json`, MySQL sockets, bundled PHP/Composer/wp-cli; `GraphQL` (Local app API: live statuses, start/stop/restart); `WPSpec` |
| `internal/composer` | `Detect` (TAW? CLASSIC or BLOCK?), installed/locked versions, semver helpers |
| `internal/site` | The model: `Site`, `Theme`, `Status`, `ThemeKind`, `SourceError` |
| `internal/scan` | `Source` (Local today), theme discovery, `Scanner` with `Enricher`s (git, core) and `Lookup`s (GitHub), `Resolve` |
| `internal/git` | `Info` (read-only state of a theme repo), `ParseRemote` (incl. SSH host aliases) |
| `internal/github` | Newest release tags, 1 h disk cache, stale-on-error, `--offline`, token discovery |
| `internal/doctor` | Rules → `site.Finding` (stable codes, severity, fix). Rules read the report only |
| `internal/style` | Palette (light/dark), status dots, badges, `Core`/`GitFit` cells; shared by CLI and dashboard |
| `internal/render` | Site header, theme cards, findings: the `show` output and the dashboard's detail pane |
| `internal/tui` | The dashboard (Bubble Tea v2): `Model`/`Update`/`View`, keymap, golden tests in `testdata/`. Rows are two-line cards (`cardLines`): site + columns, then theme/kind/host + ages, with a blank line between them while all fit (`airy()`); `tableRows()` counts cards. The TAW ecosystem's themes (`ecosystem()`: the umbrella's taw-theme/taw-gutenberg) sort last under their own label and aren't counted in the header. `stress_test.go` draws a 20-site fleet at several sizes. The cursor walks `m.entries` (`groups.go`): the filtered rows (`m.visible`), under group headers when `=` groups them (by tag, a row may show in several groups; folded groups keep only their header); with grouping off `entries` is the rows and the layout code is the old one. `tags.go`: `#` input, chips on the card |
| `internal/tools` | Installed editors/terminals (`Detect`, `Pick`) and `Opener` (`/usr/bin/open` with argument lists) |
| `internal/config` | Optional `~/.config/taw-fleet/config.toml` (editor, terminal, `group_by`, per-site production URL/notes/BugSmash project/`tags`); unknown keys are errors; `SetSite`/`SetSiteList`/`SetTop` edit one line and roll back if the file no longer loads; `CleanTags`/`TagMatches` in `tags.go` |
| `internal/wpremote` | Production WordPress REST as the site's bot user (Keychain item `wp-<slug>`, `?rest_route=`, HTTP/1.1, retries empty answers, no DELETE) — `taw-fleet wp-remote` |
| `internal/actions` | Shortcuts (`Do`), agent handoff (`Handoff`, `Copy`, `Launch`), BugSmash comments (`OpenComments`, `ResolvePrompt`), site skills (`Skills` reads a theme's `.claude/skills/*/SKILL.md` frontmatter; `SkillPrompt` + `LaunchSkill`: Claude Code in the theme folder); shared by CLI and dashboard |
| `internal/handoff` | The handoff prompt (`Build`): template + steps; golden prompts in `testdata/` |
| `internal/create` | New site: `Normalize`/`Check` a `Request`, `Creator.Run` (Local `addSite` → taw-create → npm build → `wp theme activate` → git), `Password` |
| `internal/createform` | The new-site form (charm.land/huh): `New`, `Defaults`, `Summary`, `Theme` (huh in taw-fleet's palette); shared by `create` and the dashboard's `n` |
| `internal/companion` | The companion's wire protocol (TAW-HUB-v1): `Canonical`, `Key` (sign), `VerifyResponse`, a signed `Client` (`Get`, and `Post`/`Put` for publishing: the body's sha256 is signed, so files go as raw PUT bodies, never multipart), typed answers (`publish.go`: the plan, records, changes, apply, journals, undo). Tested against taw-hub's `hub-signing-vectors.json` (in `testdata/`) |
| `internal/keychain` | Secrets in the login keychain (`Item`, `Save` via `security -i` on stdin, never argv; `Load`, `ErrNotFound`); used by `live` and `bugsmash` |
| `internal/live` | Production checks: `Keychain` (the signing key, through `internal/keychain`), `Pins` (site keys), `Prober.Probe`/`ProbeAll` (parallel, 5-min cache), `Apply`. `publish.go`: `Prober.PublishPlan/PublishFile/PublishApply/PublishJournals/PublishUndo` (pinned key and a verified answer required; own timeouts), `PublishError` (companion < 0.5, publishing off, taw/core < 1.98, a host firewall → what to do; other `publish_*` codes → `*Refusal` with `moved`/`conflicts`/`missing`) |
| `internal/review` | The publish review (umbrella `docs/plans/content-publish.md`): `Words`/`Lines` (LCS diff, folded context), `Build` (plan + base → `Page`: records by kind, changes by `Fieldset › Label`, conflicts first), `Page.Decide` (ticks + sides → approved `{key, hash}` + `keep`; what's undecided), `HTML` (`review.html`, self-contained, light/dark), `Serve` (127.0.0.1, random token path, Host/Origin checks, CSP, only the Local files the plan names via `Within`; one decision; `/status` for the progress) |
| `internal/bugsmash` | Open BugSmash review comments per site (read-only REST, `X-API-Key`): `KeyStore` (Keychain, then `$BUGSMASH_API_KEY`), `Client` (`Project`, `Projects`, `OpenComments`), `Prober.Check`/`CheckAll` (5-min cache), `NoKey`, `Apply` → `Site.Feedback` |
| `internal/selfupdate` | Newest GitHub release, sha256 check against `checksums.txt`, atomic replace; refuses Homebrew paths |
| `internal/report` | Monthly client reports: `Gather` (Sources: GitHub `Month`, BugSmash `MonthComments`, the companion, watch's uptime, PageSpeed; a failed source is a Note, never a failed report; housekeeping PRs counted, not listed), `Markdown`/`HTML` from one doc model (en/es; notes stay out of the client's text), `Save`/`List` (`~/TAW Fleet/reports/<site>/<month>.{md,html}`, `paths.FleetDir`) |
| `internal/agent` | The chat's engine (`>`): `Start` runs the person's own `claude --print` with stream-json both ways, `--permission-prompts host --permission-prompt-tool stdio` and an `initialize` control request; `Parse` turns its lines into `Event`s; `Session.Say`/`Answer`/`Stop`; `Prompt`/`ScopeOf` (what Claude is told, printed by `chat --prompt`); `OnlyAsks` (a taw-fleet write without its token only returns the question, so it's let through and the person answers that question when the token comes back). Plan: umbrella `docs/plans/taw-fleet-agent.md` § 8 |
| `internal/setup` | The SETUP checklist (`Steps`: repository, secrets check, deploy, production, companion, bot, BugSmash, skills; `Next`, `Count`) from what the scan, settings, production check and Keychain (`wpremote.Store.Has`, no password read) know; the details' SETUP section, `I`, `taw-fleet ready`, MCP `site_setup`. `actions.RepoTask` (`I`, `taw-fleet repo`, MCP `create_repo`): gh repo create (private), push, ruleset requiring `ci / committed-files` |
| `internal/deploy` | A theme's deploy to any host (taw/core 1.97's shared `theme-deploy.yml`): `Target` (rsync or git; `Check` mirrors `deploy.sh`'s guards; `Vars` = the repo's `DEPLOY_*`), `Profiles` (pre-fill + where each value is; "ssh"/"git" for any host), `NewKey` (ed25519, x/crypto/ssh) + `Keys` (Keychain `deploy-<slug>`, base64), `Tester` (ssh-keyscan → pinned known_hosts, `ssh -o StrictHostKeyChecking=yes`, or `git ls-remote`). `actions.DeployTask` applies (gh variable/secret set on stdin, the stub from the theme's vendor on `deploy/setup` + PR, `config.SetSite` production_url). The dashboard's `I` hands the terminal to `taw-fleet deploy setup` (`tea.ExecProcess`: the cli builds that one `*exec.Cmd`) |
| `internal/launch` | The launch report (stage 6; `I` once live, `taw-fleet launch-report`, MCP `launch_report`): `Gather` (production's own pages over HTTPS: the redirect, the certificate, noindex / `X-Robots-Tag` / robots.txt; the deploy from `RepoState`; the companion; PageSpeed; watch; BugSmash; never fails, a check says why), `left` (fails, then warnings, then SETUP steps the checks don't cover), `Save` (PageSpeed's first-screen pictures as `production/home-<strategy>.pagespeed.<ext>`, paired with `build/` by page; a browser's picture of the same page wins) into `~/TAW Fleet/sites/<slug>/launch.{html,md}`. Only reads production |
| `internal/fleet` | The fleet folder (`paths.FleetDir`, `~/TAW Fleet`): `Folder.Write` (fleet skills: fleet-check, new-site (+ `review-template.html`; the whole flow, link to launch report), add-production, update-fleet, onboard-mac) makes `CLAUDE.md` from the settings (rules from `agent.Rules(false)`), `.mcp.json` (merges in taw-fleet's server), and the fleet skills embedded from `skills/` (`owner: taw-fleet`; replaced when changed, removed when no longer shipped; anything else left alone). `taw-fleet init` and the dashboard's start call it |
| `internal/pagespeed` | PageSpeed Insights v5 (`Client.Run`: Lighthouse scores + CrUX field data); key in the Keychain (`KeyItem`, `report key import`) or `$PAGESPEED_API_KEY` |
| `internal/restore` | Restore points: `Store.Save` (Guard first: outside the site, every theme and any git repository, else `ErrUnsafe`; `wp db export -` → 0600 gzip via the site's Local `mysqldump`; theme branch + commit; merge = git only; prune to `restore_keep`), `List`/`Find`, `Check`/`Restore` (saves a `before-restore` point, `git switch`, `wp db import -` from the gzip stream). Database copies live only in `DataDir/restore/` |
| `internal/login` | Open at login (`open_at_login`, `taw-fleet config open-at-login`): `Agent` (launchd, `com.relmaur.taw-fleet.login`): `Install` only writes the file (loading it would run `RunAtLoad` now, opening a window), launchd reads it at the next login; `Uninstall` boots it out and removes it; `Sync` matches the setting (the dashboard's start calls it). The agent runs `taw-fleet --at-login` (hidden): a window whatever `window` says, or nothing when the setting is off by then |
| `internal/watch` | `taw-fleet watch`: `Check` (last `State` + the fleet → `Event`s, each told once; first sight only notes; failed checks keep the last state; uptime per day), `Load`/`Save` (`DataDir/watch/state.json`), `Notifier` (terminal-notifier with `-execute taw-fleet --site <slug>`, osascript fallback, `ErrNotAllowed`), `Agent` (launchd: `Install`/`Uninstall`/`Status`, `gui/<uid>`) |
| `internal/actions/publish.go` | `PublishTask` (export `--media-hashes` → `ContentDiff --with-base` against the base → `PublishPlan` → `review.Build`; `plan.json` saved without the token) returning a `PublishReview` (`Browser` task: serves the page, `Stoppable`; `Apply(Decisions)` for the CLI/MCP), `publish` (uploads checked by sha256, apply, base refreshed from production, `report.json`), `PublishUndoTask` (preview → `UndoPreview.Apply`), `PublishHistory`, `Base`/`SaveBase` (`DataDir/content/<slug>/base.json`, guarded by `restore.Store.Guard`; `pull.go` saves it after an import) |
| `internal/taw` | The theme's own tools: `Runner.Sync` / `Inspect` (`bin/taw`), `UpdateCore` (composer), `UpgradeSections` (UPGRADING.md), `Guard`, drift cache, `LineWriter` |

Releases: `.goreleaser.yaml` + `.github/workflows/release.yml` on `v*` tags. The archive names
(`taw-fleet_<version>_darwin_<arch>.tar.gz`) and `checksums.txt` are a contract with
`selfupdate.ArchiveName`; change both together. The cask goes to `Relmaur/homebrew-tap` with the
`HOMEBREW_TAP_GITHUB_TOKEN` secret. Create the release notes (`gh release create`) first: GoReleaser
runs with `mode: keep-existing` and only adds the files. Check a config change with
`goreleaser release --snapshot --clean`.

Local's API (probed on Local 10.1.2): `Authorization: Bearer <authToken>` from
`graphql-connection-info.json`; introspection is disabled, so operations come from Local's
bundled schema (`startSite/stopSite/restartSite(id: ID!): Site`, `site(id)`, `sites`). The client
refuses a non-localhost URL so the token never leaves the machine. Test against the fake in
`internal/local/graphql_test.go`; on a real Mac, only start/stop sites nobody is working on, and
put them back the way they were. `addSite(input: AddSiteInput!): Job` + `job(id)` create sites
(the job id is not the site id: find the site in `sites.json` by path); there is **no delete
mutation**. `create` tests use fakes; a real `create` makes a site the owner has to delete in
Local by hand, so ask before running one.

Dashboard rules: `View` is a pure function of the model (no I/O); work happens in `tea.Cmd`s.
Golden screens: `go test ./internal/tui -update` rewrites `internal/tui/testdata/*.golden`; read
the diff before committing. To look at the real thing: `tmux new -d -s tf -x 150 -y 40
taw-fleet; tmux capture-pane -pt tf` (add `-e` for colors; tmux turns resets into `\e[49m`).
Use glyphs from common fonts only: `◇` not `⇡`.

Doctor codes are a contract like `--json`: add new ones freely, don't rename existing ones.

`--json` output uses snake_case keys and is the scripting contract: add fields freely, don't
rename or remove them without a major version.

## Commands

```bash
go test -race ./...
golangci-lint run
go run ./cmd/taw-fleet version
```

## Rules

- **Filesystem and environment come in through `paths.Paths`. Commands run through
  `exec.Runner`.** No package calls `os.UserHomeDir()` or `exec.Command` directly, except the two
  default implementations, the dashboard's deploy wizard (`tea.ExecProcess` needs an `*exec.Cmd`),
  and `agent`'s `execStart` (a conversation streams both ways, which
  `Runner` can't do; tests replace it through `agent.Starter`).
- **Tests never touch the real `~/Library`, `~/Local Sites`, GitHub, BugSmash or Local's API.** Use
  `t.TempDir()`, `testdata/`, `FakeRunner` and `httptest`. CI runs on a clean macOS runner, which
  would catch it.
- **Every subprocess gets an argument list and a context timeout. Never a shell string.** The one
  exception is the handoff launcher (`actions.LauncherScript`): a terminal can only run a script, so
  it's a three-line `sh` file in which every value is single-quoted, and a test runs it with a
  prompt full of quotes, `$( )` and backticks.
- **Never run `update`/`sync --apply` on a real site to test.** They change a client repo. Test on a
  copy of the theme (`rsync -a --exclude node_modules --exclude .git <theme>/ <scratch>/`) through
  `taw.Runner` directly; read-only `sync` and `inspect` are fine on real sites.
- **The handoff prompt is a contract with the owner** (docs/plans § Step 4): branch + commit, ask
  before push; taw/core update approved; Tier 2 needs confirmation; dirty tree or unexpected
  branch = stop and ask. Change those rules only when the owner asks. Review the golden prompts in
  `internal/handoff/testdata/` whenever the template changes.
- **The production view only reads.** `live` uses the companion's GET routes; the write routes
  (`/framework/sync`, `/taw`, `/keys/rotate`) are deliberately unused. The one exception is
  publishing (`content/publish/*`, companion ADR-0006): only through `actions.PublishTask`/
  `PublishUndoTask`, only after the person decided in the review (the browser's Publish, a
  dashboard question, `--yes`, or an MCP confirm), never an approval made up by code. The signing key lives only
  in the Keychain: never print it, never pass it on a command line, never write it to a file.
  Change the wire format only together with the companion, and keep the vectors test passing.
- **BugSmash is read-only too.** `internal/bugsmash` only GETs (`/project/{id}`, `/projects`,
  `/comments?status=active`); resolving and replying belong to the umbrella's
  `taw-resolve-comments` skill. The API key follows the signing key's rules (Keychain only; the
  `BUGSMASH_API_KEY` fallback is read, never written). A deleted project still answers `/comments`,
  so `Check` asks `/project/{id}` first: its 404 is how a re-created project shows up.
- **A database copy never goes where git or a sync can reach it.** Only `internal/restore` makes
  them, only under `DataDir/restore/`, after `Guard`; `wp db export` writes to stdout, never to a
  file in a site. Tests never dump a real site's database (the owner's rule after a client theme's
  `.sync/` leak); a live check uses the `taw` dev site, with the owner's OK.
- **Read-only by default.** Anything that changes a site, a theme or a file asks first in the
  dashboard and needs `--yes` on the command line.
- **Never use `style.css` `Version:` as a theme's version.** It is stale in every TAW theme. Use
  `git describe --tags`.
- Parse Local's files leniently. An unknown or missing field is an error on that site, never a
  crash.
- Umbrella rules apply:
  - confirm every state-changing git operation (scope and actor);
  - bump the umbrella pointer after committing here;
  - push this repo before the umbrella.
