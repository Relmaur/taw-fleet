# taw-fleet

**Every TAW site on your Mac, in one terminal screen.**

`taw-fleet` finds the TAW themes in your Local by Flywheel sites without any setup. It shows each
one's status, taw/core version and git state, and opens the theme in your editor, the site in your
browser or the repo on GitHub.

> **Status: early development (v0.1).** `list` works; the full-screen dashboard and the shortcuts
> come next. The plan is in the TAW umbrella at `docs/plans/taw-fleet.md`.

## Use

```bash
taw-fleet list            # the TAW sites on this Mac, their status and themes
taw-fleet list --all      # also sites and themes that aren't TAW
taw-fleet list --json     # the same, for scripts and Claude sessions
taw-fleet version
```

```
 taw-fleet  ·  8 sites  ·  9 TAW themes  ·  3 running

    SITE                      THEME            KIND       PHP     DOMAIN
──────────────────────────────────────────────────────────────────────────────────
 ●  ch-capital---taw          chcapital         CLASSIC   8.2.30  chcapital.local
 ○  eme-lambda-taw            emelambda         CLASSIC   8.2.29  emelambda.local
 ●  taw                       taw-gutenberg ↗   BLOCK     8.5.3   taw.local
                              taw-theme ↗       CLASSIC
```

### How it finds sites

No setup. It reads Local by Flywheel's own files for the current user:

| What | Where |
|---|---|
| Sites | `~/Library/Application Support/Local/sites.json` |
| Running or halted | `…/Local/site-statuses.json` |
| Database socket | `…/Local/run/<site id>/mysql/mysqld.sock` |
| TAW themes | each `wp-content/themes/*/composer.json` that requires `taw/core` (symlinks followed) |

A theme named `taw/gutenberg`, or a block theme (`wordpress-theme` with `theme.json` and
`templates/`), is **BLOCK**; any other TAW theme is **CLASSIC**.

### Environment

| Variable | Effect |
|---|---|
| `TAW_FLEET_HOME` | Use another home folder (e.g. a copy of `sites.json` for testing) |
| `TAW_FLEET_THEME` | `light` or `dark`: the color palette (default: from `COLORFGBG`, else dark) |
| `NO_COLOR` | No colors. Piped output and `--json` are never colored. |

## Develop

```bash
brew install go golangci-lint
go test -race ./...
golangci-lint run
go run ./cmd/taw-fleet list
```

See [AGENTS.md](AGENTS.md) for the rules and
[ADR-0001](docs/adr/0001-go-tui-for-the-local-taw-fleet.md) for why it's built this way.
