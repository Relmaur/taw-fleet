# Update the TAW theme `ls-mexico` (site `ls-mxico`)

You're picking up a theme update on this Mac. taw-fleet gathered the context below on 2026-10-08 15:30; re-check anything that matters before acting on it.

## The task

Run the **update-theme** skill (`.claude/skills/update-theme/SKILL.md` in this theme) to sync the framework-owned parts of the taw-theme scaffold, then update taw/core.

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
'/Users/me/Library/Application Support/Local/lightning-services/php-8.2.30+1/bin/darwin-arm64/bin/php' /Applications/Local.app/Contents/Resources/extraResources/bin/composer/composer.phar update taw/core
```

The site isn't running, so wp-cli and anything that boots WordPress won't work. When a check needs WordPress, ask me to start the site in Local (Local by Flywheel → LS Mexico - TAW → Start site).

## What taw-fleet found

- **taw/core:** installed `1.59.2`, newest `1.76.1` → **behind**
- **git:** on `main` (default `main`), tracking `origin/main`, clean
- warn `core.behind` (ls-mexico): taw/core v1.59.2, latest is v1.76.1

## Steps

1. **Start clean.** Run `git status` (expect clean) and `git pull --ff-only` on `main`.
2. **Branch.** Create `chore/taw-core-1.76.1` from an up-to-date `main` and do everything on it. If it already exists, ask me.
3. **Sync the scaffold** with the skill: `bin/taw sync --json`, then `bin/taw sync --apply` for Tier 1 (no confirmation needed). For **Tier 2**, show me each diff and apply only what I approve; edit `composer.json` and `package.json` line by line, never overwrite them.
4. **taw/core.** **You have my approval** to run `composer update taw/core` (1.59.2 → 1.76.1). Then read `vendor/taw/core/UPGRADING.md` and work through **every section newer than 1.59.2**: run each **Check** and note its outcome ("not applicable" is fine, skipping one isn't).
5. **Verify.** Run what the theme has: `composer run test`, `composer run phpstan`, `npm run build` if assets changed. Checks that need WordPress wait until I start the site.
6. **Commit** on `chore/taw-core-1.76.1` with a clear message (e.g. "Update taw/core to 1.76.1; sync theme scaffold"). Then **ask me before pushing** or opening a PR.

## Rules

- Never touch `Blocks/`, `inc/options.php`, `inc/performance.php`, `inc/customizations.php`, page templates or content.
- No `git push`, no PR, no merge, no force anything without asking me. Never commit to `main` directly.
- The skill says not to commit; for this handoff I'm asking you to commit on `chore/taw-core-1.76.1` (step "Commit"). Everything else in the skill applies as written.
- Don't touch the production site or any other Local site.
- If a command fails, stop and tell me what happened instead of working around it.

## Report back

taw/core before → after; what Tier 1 changed; which Tier 2 diffs I approved or declined; each UPGRADING.md check and its outcome; test results; the branch and commit. Then wait for me.
