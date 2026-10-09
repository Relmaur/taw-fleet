# Update the TAW theme `ls-mexico` (site `ls-mxico`)

You're picking up a theme update on this Mac. taw-fleet gathered the context below on 2026-10-08 15:30; re-check anything that matters before acting on it.

## The task

Run the **update-theme** skill (`.claude/skills/update-theme/SKILL.md` in this theme) to sync the framework-owned parts of the taw-theme scaffold.

## Where everything is

| What | Where |
|---|---|
| Theme (work here) | `/Users/me/Local Sites/ls-mxico/app/public/wp-content/themes/ls-mexico` |
| Site folder (Local by Flywheel) | `/Users/me/Local Sites/ls-mxico` |
| WordPress root | `/Users/me/Local Sites/ls-mxico/app/public` |
| Local site | LS Mexico - TAW (id `EQLMLW4r9`), **running** |
| Site URL | http://ls-mexico.local (admin: http://ls-mexico.local/wp-admin/) |
| Repository | https://github.com/Relmaur/ls-mexico--theme |

## How to run things

Use the site's own PHP, not whatever `php` is on PATH:

```bash
cd '/Users/me/Local Sites/ls-mxico/app/public/wp-content/themes/ls-mexico'
'/Users/me/Library/Application Support/Local/lightning-services/php-8.2.30+1/bin/darwin-arm64/bin/php' --version
'/Users/me/Library/Application Support/Local/lightning-services/php-8.2.30+1/bin/darwin-arm64/bin/php' bin/taw sync --json          # the skill's Step 1
'/Users/me/Library/Application Support/Local/lightning-services/php-8.2.30+1/bin/darwin-arm64/bin/php' /Applications/Local.app/Contents/Resources/extraResources/bin/composer/composer.phar update taw/core --with-dependencies
```

wp-cli for this site (Local's MySQL socket; only while the site runs):

```bash
'/Users/me/Library/Application Support/Local/lightning-services/php-8.2.30+1/bin/darwin-arm64/bin/php' -d mysqli.default_socket='/Users/me/Library/Application Support/Local/run/EQLMLW4r9/mysql/mysqld.sock' -d pdo_mysql.default_socket='/Users/me/Library/Application Support/Local/run/EQLMLW4r9/mysql/mysqld.sock' /Applications/Local.app/Contents/Resources/extraResources/bin/wp-cli/wp-cli.phar --path='/Users/me/Local Sites/ls-mxico/app/public' option get stylesheet
```

## What taw-fleet found

- **taw/core:** installed `1.76.1`, newest `1.76.1` → current
- **git:** on `chore/taw-core-1.76.1` (default `main`), no upstream, **5 uncommitted change(s)**
- warn `core.behind` (ls-mexico): taw/core v1.59.2, latest is v1.76.1

## Steps

1. **Start clean.** The working tree had 5 uncommitted change(s). Run `git status`; if they're still there, **stop and ask me** what to do with them. Don't stash, commit or discard them yourself. You're also on `chore/taw-core-1.76.1`, not `main`: ask me which branch to work from.
2. **Branch.** Create `chore/update-theme-2026-10-08` from an up-to-date `main` and do everything on it. If it already exists, ask me.
3. **Sync the scaffold** with the skill: `bin/taw sync --json`, then `bin/taw sync --apply` for Tier 1 (no confirmation needed). For **Tier 2**, show me each diff and apply only what I approve; edit `composer.json` and `package.json` line by line, never overwrite them.
4. **taw/core** is current; no update needed. Mention it in the report.
5. **Verify.** Run what the theme has: `composer run test`, `composer run phpstan`, `npm run build` if assets changed. Load http://ls-mexico.local and a wp-admin screen with metaboxes; look for PHP errors in `/Users/me/Local Sites/ls-mxico/app/public/wp-content/debug.log` if it exists.
6. **Commit** on `chore/update-theme-2026-10-08` with a clear message (e.g. "Sync theme scaffold"). Then **ask me before pushing** or opening a PR.

## Rules

- Never touch `Blocks/`, `inc/options.php`, `inc/performance.php`, `inc/customizations.php`, page templates or content.
- No `git push`, no PR, no merge, no force anything without asking me. Never commit to `main` directly.
- The skill says not to commit; for this handoff I'm asking you to commit on `chore/update-theme-2026-10-08` (step "Commit"). Everything else in the skill applies as written.
- Don't touch the production site or any other Local site.
- If a command fails, stop and tell me what happened instead of working around it.

## Report back

taw/core before → after; what Tier 1 changed; which Tier 2 diffs I approved or declined; each UPGRADING.md check and its outcome; test results; the branch and commit. Then wait for me.
