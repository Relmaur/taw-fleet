# AGENTS.md — taw-fleet

A Go terminal app that finds the TAW sites on a Mac (through Local by Flywheel's own files) and
shows their status, versions and git state. It also has shortcuts: open the theme in an editor,
open the repo, start or stop the site. Part of the TAW umbrella (`TAW/taw-fleet`, a submodule).
Read the umbrella's AGENTS.md and `docs/STATE.md` first. Plan: umbrella `docs/plans/taw-fleet.md`.
Decision record: `docs/adr/0001-go-tui-for-the-local-taw-fleet.md`.

## Layout

| Path | Purpose |
|---|---|
| `cmd/taw-fleet/main.go` | Entry point; `version`/`commit` are set by the release build |
| `internal/cli` | cobra commands (`list`, `show`, `doctor`, `version`); table or `--json`; global `--offline` |
| `internal/paths` | Every filesystem root (`Home`, `LocalSupport`, `LocalApp`, …); `$TAW_FLEET_HOME` overrides `~` |
| `internal/exec` | `Runner` (argument lists + context timeout), `OSRunner`, `FakeRunner` |
| `internal/local` | Local's `sites.json`, `site-statuses.json`, MySQL sockets, bundled PHP/Composer |
| `internal/composer` | `Detect` (TAW? CLASSIC or BLOCK?), installed/locked versions, semver helpers |
| `internal/site` | The model: `Site`, `Theme`, `Status`, `ThemeKind`, `SourceError` |
| `internal/scan` | `Source` (Local today), theme discovery, `Scanner` with `Enricher`s (git, core) and `Lookup`s (GitHub), `Resolve` |
| `internal/git` | `Info` (read-only state of a theme repo), `ParseRemote` (incl. SSH host aliases) |
| `internal/github` | Newest release tags, 1 h disk cache, stale-on-error, `--offline`, token discovery |
| `internal/doctor` | Rules → `site.Finding` (stable codes, severity, fix). Rules read the report only |
| `internal/style` | Palette (light/dark), status dots, badges, `Core`/`GitFit` cells; shared by CLI and dashboard |
| `internal/render` | Site header, theme cards, findings: the `show` output and the dashboard's detail pane |
| `internal/tui` | The dashboard (Bubble Tea v2): `Model`/`Update`/`View`, keymap, golden tests in `testdata/` |

Still to come (see the plan): `tools`, `config`, `taw`, `selfupdate`.

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
- **Tests never touch the real `~/Library`, `~/Local Sites`, GitHub or Local's API.** Use
  `t.TempDir()`, `testdata/`, `FakeRunner` and `httptest`. CI runs on a clean macOS runner, which
  would catch it.
- **Every subprocess gets an argument list and a context timeout. Never a shell string.**
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
