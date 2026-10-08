# ADR-0001: taw-fleet is a Go terminal app (Bubble Tea + cobra) that reads Local by Flywheel's own files

## Status

Proposed

## Context

A developer Mac holds many Local by Flywheel sites, each with one or more TAW themes. On the
machine that started this repo: 10 Local sites, 9 TAW themes. Seven are client themes (forks of
taw-theme, real git repos inside their Local site). The other two are taw-theme and taw-gutenberg,
symlinked from the umbrella into the `taw` site (umbrella ADR-0002). Five of the nine were behind on
taw/core (v1.59.2 against v1.76.1).

Today, nothing answers these questions in one place:
- Which sites are TAW sites, and which are running?
- Which taw/core version does each theme have, and is it behind?
- Which themes have uncommitted or unpushed work, or sit on a non-default branch?

People and Claude sessions answer them by hand, folder by folder, every time. The TAW Hub only sees
production sites, through the taw-hub-companion plugin.

The owner asked for a terminal app with three requirements:
- **portable:** copy it to a new Mac and it finds the sites;
- **beautiful:** a dashboard, not just plain output;
- **scriptable:** Claude sessions can use it too.

## Decision

1. **Go, with Bubble Tea v2, Lip Gloss v2 and Bubbles v2** (`charm.land/*/v2`) for the dashboard,
   and **cobra** for subcommands. Run with no arguments, `taw-fleet` opens the full-screen
   dashboard. Subcommands (`list`, `show`, `doctor`, `open`, `start`/`stop`, `sync`, `wp`, …) print
   tables, or JSON with `--json`.
2. **Local's own files are the source of truth. There is no config to set up.** The app reads:
   - `~/Library/Application Support/Local/sites.json` (sites);
   - `site-statuses.json` (running state);
   - `run/<id>/mysql/mysqld.sock` (database socket);
   - each theme's `composer.json` and `vendor/composer/installed.json` (TAW detection, taw/core
     version).

   Theme versions come from git tags, never from `style.css`, which is stale in every TAW theme. An
   optional `~/.config/taw-fleet/config.toml` holds preferences only.
3. **Read-only by default.** Starting or stopping a site, `sync --apply` and `composer update`
   need confirmation in the dashboard, or `--yes` on the command line. Every subprocess gets an
   argument list and a timeout. Never a shell string.
4. **Starting and stopping sites goes through Local's GraphQL API** (`graphql-connection-info.json`).
   A probe confirms it works before the feature is built. If the probe fails, the app tells you to
   use Local instead.
5. **Distribution:**
   - single static binaries for darwin/arm64 and darwin/amd64, built by GoReleaser on `v*` tags;
   - a Homebrew tap (`Relmaur/homebrew-tap`);
   - `taw-fleet self-update`, which checks the sha256 against `checksums.txt` before replacing
     anything.
6. **Production comes later, through a seam:** sites come from a `Source` interface. `local` is
   the only source in v1. A Hub-backed source can be added without changing the model.
7. **Testability rule:** filesystem roots go through a `paths.Paths` value (`$TAW_FLEET_HOME`
   overrides `~`), and commands run through an `exec.Runner` interface. `go test ./...` never reads
   the real `~/Library` or the network. CI on a clean macOS runner enforces this.

## Trade-offs

- **A new language in the ecosystem.** Everything else is PHP or TS. Accepted, because one static
  binary with no runtime to install is the point of "portable", and Charm is the strongest TUI
  toolkit available.
- **Local's file formats and GraphQL API are not public contracts** and may change with Local
  updates. Mitigation: parsing is lenient, unknown fields are ignored, and a failed source becomes
  a visible error on that site rather than a crash.
- Rejected:
  - **PHP + Symfony Console inside taw-core:** it needs a PHP runtime and a theme to run from,
    can't do a full-screen dashboard well, and would tie fleet tooling to taw-core releases.
  - **Node + Ink:** needs Node on every machine, and starts slowly.
  - **A shell script:** can't be made beautiful or tested to this standard.
  - **A SwiftUI app:** an earlier idea; the owner chose a terminal app instead (2026-10-08).

## Consequences

- New repo `Relmaur/taw-fleet`, the 7th umbrella submodule. Plan: umbrella
  `docs/plans/taw-fleet.md`.
- A Go toolchain is needed to develop here (`brew install go golangci-lint goreleaser`). End users
  need nothing.
- The Hub, a production view and a menu-bar or desktop app are deliberately not built. They come
  when the owner asks, on top of the `Source` seam.
