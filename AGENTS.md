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
| `internal/cli` | cobra commands; no subcommand opens the dashboard |

More packages arrive step by step (see the plan): `paths`, `exec`, `local`, `site`, `scan`, `git`,
`composer`, `github`, `tools`, `taw`, `doctor`, `config`, `tui`, `selfupdate`.

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
