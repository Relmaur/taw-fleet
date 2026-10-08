# taw-fleet

**Every TAW site on your Mac, in one terminal screen.**

`taw-fleet` finds the TAW themes in your Local by Flywheel sites without any setup. It shows each
one's status, taw/core version and git state, and opens the theme in your editor, the site in your
browser or the repo on GitHub.

> **Status: early development (v0.2).** `list`, `show` and `doctor` work; the full-screen
> dashboard and the shortcuts come next. The plan is in the TAW umbrella at `docs/plans/taw-fleet.md`.

## Use

```bash
taw-fleet list            # every TAW site: status, themes, taw/core, git
taw-fleet show chcapital  # one site in detail (folder, name, domain, Local id or theme)
taw-fleet doctor          # what needs attention, most serious first, with the fix
taw-fleet doctor ls-mxico # the same for one site
taw-fleet list --all      # also sites and themes that aren't TAW
taw-fleet version
```

Every read command takes `--json` (for scripts and Claude sessions) and `--offline` (don't ask
GitHub; use the cached newest versions).

```
 taw-fleet  ·  8 sites  ·  9 TAW themes  ·  3 running

    SITE                 THEME            KIND      TAW/CORE         GIT
 ●  ch-capital---taw     chcapital        CLASSIC   1.76.1           master
 ○  eme-lambda-taw       emelambda        CLASSIC   1.59.2 ▲ 1.76.1  main
 ●  fsspx-taw            fsspx--theme     CLASSIC   1.76.1           chore/taw-core-1.76.1 ±5 ⇡ unpushed
 ○  parallel-plus        parallelplus     CLASSIC   1.59.2 ▲ 1.76.1  staging
 ●  taw                  taw-gutenberg ↗  BLOCK     1.76.1           main
                         taw-theme ↗      CLASSIC   1.76.1           main
```

### What `doctor` checks

| Code | Severity | Means |
|---|---|---|
| `core.missing` / `core.unreadable` | error | no `vendor/` (run `composer install`), or `installed.json` is broken |
| `core.behind` | warning | taw/core is older than the newest release |
| `core.lock-mismatch` | warning | `vendor/` and `composer.lock` disagree |
| `git.no-upstream`, `git.behind`, `git.detached`, `git.no-remote` | warning | branch not pushed, commits to pull, not on a branch, no `origin` |
| `git.dirty`, `git.ahead`, `git.off-default`, `git.none` | note | uncommitted changes, commits to push, not on the default branch, not a repo |
| `theme.symlink-broken` | error | a symlinked theme points at a folder that's gone |
| `scaffold.behind` | note | an umbrella checkout of taw-theme/taw-gutenberg is behind its own release |
| `tools.php-missing` | warning | Local doesn't have the PHP build the site uses |
| `scan.local`, `scan.github`, `site.read-error` | warning | Local not found, newest versions unknown, a site couldn't be fully read |

The codes are stable: filter `doctor --json` on them. `doctor` only reads; it never changes
anything.

### Newest versions

The newest taw-core, taw-theme and taw-gutenberg releases come from GitHub's tag list and are
cached for an hour in `~/Library/Caches/taw-fleet/github/`. Without a token GitHub allows 60
requests an hour, plenty with the cache. If `GITHUB_TOKEN`/`GH_TOKEN` is set, or the `gh` CLI is
signed in, that token is used (sent only to api.github.com). When GitHub can't be reached, the
last cached answer is used.

### How it finds sites

No setup. It reads Local by Flywheel's own files for the current user:

| What | Where |
|---|---|
| Sites | `~/Library/Application Support/Local/sites.json` |
| Running or halted | `…/Local/site-statuses.json` |
| Database socket | `…/Local/run/<site id>/mysql/mysqld.sock` |
| TAW themes | each `wp-content/themes/*/composer.json` that requires `taw/core` (symlinks followed) |
| taw/core installed / locked | the theme's `vendor/composer/installed.json` / `composer.lock` |
| Git state | `git` in the theme folder (read-only: no fetch, no locks) |

A theme's version is `git describe --tags`, never `style.css` (stale in every TAW theme). Client
themes forked from taw-theme inherit its old tags, so their describe is informational only.

A theme named `taw/gutenberg`, or a block theme (`wordpress-theme` with `theme.json` and
`templates/`), is **BLOCK**; any other TAW theme is **CLASSIC**.

### Environment

| Variable | Effect |
|---|---|
| `TAW_FLEET_HOME` | Use another home folder (e.g. a copy of `sites.json` for testing) |
| `TAW_FLEET_THEME` | `light` or `dark`: the color palette (default: from `COLORFGBG`, else dark) |
| `NO_COLOR` | No colors. Piped output and `--json` are never colored. |
| `GITHUB_TOKEN` / `GH_TOKEN` | Token for the newest-version lookup (else `gh auth token`, else none) |

## Develop

```bash
brew install go golangci-lint
go test -race ./...
golangci-lint run
go run ./cmd/taw-fleet list
```

See [AGENTS.md](AGENTS.md) for the rules and
[ADR-0001](docs/adr/0001-go-tui-for-the-local-taw-fleet.md) for why it's built this way.
