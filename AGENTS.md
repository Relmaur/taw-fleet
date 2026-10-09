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
| `internal/paths` | Every filesystem root (`Home`, `LocalSupport`, `LocalApp`, …); `$TAW_FLEET_HOME` overrides `~` |
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
| `internal/tui` | The dashboard (Bubble Tea v2): `Model`/`Update`/`View`, keymap, golden tests in `testdata/`. Rows are two-line cards (`cardLines`): site + columns, then theme/kind/host + ages, with a blank line between them while all fit (`airy()`); `tableRows()` counts cards. The TAW ecosystem's themes (`ecosystem()`: the umbrella's taw-theme/taw-gutenberg) sort last under their own label and aren't counted in the header. `stress_test.go` draws a 20-site fleet at several sizes |
| `internal/tools` | Installed editors/terminals (`Detect`, `Pick`) and `Opener` (`/usr/bin/open` with argument lists) |
| `internal/config` | Optional `~/.config/taw-fleet/config.toml` (editor, terminal, per-site production URL/notes/BugSmash project); unknown keys are errors |
| `internal/wpremote` | Production WordPress REST as the site's bot user (Keychain item `wp-<slug>`, `?rest_route=`, HTTP/1.1, retries empty answers, no DELETE) — `taw-fleet wp-remote` |
| `internal/actions` | Shortcuts (`Do`), agent handoff (`Handoff`, `Copy`, `Launch`), BugSmash comments (`OpenComments`, `ResolvePrompt`), site skills (`Skills` reads a theme's `.claude/skills/*/SKILL.md` frontmatter; `SkillPrompt` + `LaunchSkill`: Claude Code in the theme folder); shared by CLI and dashboard |
| `internal/handoff` | The handoff prompt (`Build`): template + steps; golden prompts in `testdata/` |
| `internal/create` | New site: `Normalize`/`Check` a `Request`, `Creator.Run` (Local `addSite` → taw-create → npm build → `wp theme activate` → git), `Password` |
| `internal/createform` | The new-site form (charm.land/huh): `New`, `Defaults`, `Summary`, `Theme` (huh in taw-fleet's palette); shared by `create` and the dashboard's `n` |
| `internal/companion` | The companion's wire protocol (TAW-HUB-v1): `Canonical`, `Key` (sign), `VerifyResponse`, a signed GET `Client`, typed answers. Tested against taw-hub's `hub-signing-vectors.json` (in `testdata/`) |
| `internal/keychain` | Secrets in the login keychain (`Item`, `Save` via `security -i` on stdin, never argv; `Load`, `ErrNotFound`); used by `live` and `bugsmash` |
| `internal/live` | Production checks: `Keychain` (the signing key, through `internal/keychain`), `Pins` (site keys), `Prober.Probe`/`ProbeAll` (parallel, 5-min cache), `Apply` |
| `internal/bugsmash` | Open BugSmash review comments per site (read-only REST, `X-API-Key`): `KeyStore` (Keychain, then `$BUGSMASH_API_KEY`), `Client` (`Project`, `Projects`, `OpenComments`), `Prober.Check`/`CheckAll` (5-min cache), `NoKey`, `Apply` → `Site.Feedback` |
| `internal/selfupdate` | Newest GitHub release, sha256 check against `checksums.txt`, atomic replace; refuses Homebrew paths |
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
  default implementations.
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
  (`/framework/sync`, `/taw`, `/keys/rotate`) are deliberately unused. The signing key lives only
  in the Keychain: never print it, never pass it on a command line, never write it to a file.
  Change the wire format only together with the companion, and keep the vectors test passing.
- **BugSmash is read-only too.** `internal/bugsmash` only GETs (`/project/{id}`, `/projects`,
  `/comments?status=active`); resolving and replying belong to the umbrella's
  `taw-resolve-comments` skill. The API key follows the signing key's rules (Keychain only; the
  `BUGSMASH_API_KEY` fallback is read, never written). A deleted project still answers `/comments`,
  so `Check` asks `/project/{id}` first: its 404 is how a re-created project shows up.
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
