# Update the TAW theme `ls-mexico` (site `ls-mxico`), batch mode

You're one of several agents updating TAW themes at once; taw-fleet gathered the facts below on 2026-10-08 15:30. Nobody can answer questions during your run: decide by the rules, or stop with a reason.

## The task

Read `.claude/skills/update-theme/SKILL.md` in this theme and follow its **§ "Batch mode"** (B1–B9) exactly. If that copy has no § "Batch mode" yet (the theme hasn't been synced since taw-theme v1.12.45), read the canonical skill instead (https://raw.githubusercontent.com/Relmaur/taw-theme/main/.claude/skills/update-theme/SKILL.md) until its step B4 refreshes the theme's copy.

- **Branch:** `chore/taw-core-1.76.1` from `main`.
- **Approved:** `composer update taw/core --with-dependencies` (1.59.2 → 1.76.1; it may move taw/core's own dependencies too), and committing on that branch. Nothing else: never push or open a PR.
- **Site:** not running: mark checks that need WordPress `needs-wordpress` and don't start it.
- Work only in the theme folder below; don't touch any other theme or site.

## Where everything is

| What | Where |
|---|---|
| Theme (work here) | `/Users/me/Local Sites/ls-mxico/app/public/wp-content/themes/ls-mexico` |
| Site folder (Local by Flywheel) | `/Users/me/Local Sites/ls-mxico` |
| WordPress root | `/Users/me/Local Sites/ls-mxico/app/public` |
| Local site | LS Mexico - TAW (id `EQLMLW4r9`), **not running** |
| Site URL | http://ls-mexico.local (admin: http://ls-mexico.local/wp-admin/) |
| Production | https://lsmexico.mx (don't touch it) |
| Repository | https://github.com/Relmaur/ls-mexico--theme |

## How to run things

Use the site's own PHP, not whatever `php` is on PATH:

```bash
cd '/Users/me/Local Sites/ls-mxico/app/public/wp-content/themes/ls-mexico'
'/Users/me/Library/Application Support/Local/lightning-services/php-8.2.30+1/bin/darwin-arm64/bin/php' --version
'/Users/me/Library/Application Support/Local/lightning-services/php-8.2.30+1/bin/darwin-arm64/bin/php' bin/taw sync --json          # the skill's Step 1
'/Users/me/Library/Application Support/Local/lightning-services/php-8.2.30+1/bin/darwin-arm64/bin/php' /Applications/Local.app/Contents/Resources/extraResources/bin/composer/composer.phar update taw/core --with-dependencies
```

The site isn't running, so wp-cli and anything that boots WordPress won't work. Mark checks that need WordPress `needs-wordpress`; the coordinator starts sites once, for every theme.

## Report

End with the skill's B9 JSON block (`"theme": "ls-mexico"`, `"site": "ls-mxico"`). The coordinator reads only that block, so put every decision you left open in `proposals` or `notes`.
