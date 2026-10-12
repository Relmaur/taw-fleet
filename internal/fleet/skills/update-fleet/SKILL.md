---
name: update-fleet
description: Bring every TAW site up to date, end to end. Updates every theme that's behind (what U does), deals with the ones that stop, merges the update pull requests that pass and follows each deploy, one site at a time, asking before each merge. Triggers on: "update the fleet", "update every site", "update all the sites", "bring everything up to date", "roll out the new taw/core".
owner: taw-fleet
---

# Update the fleet

`U` updates every theme that's behind and opens one pull request each. This skill does the rest
with judgement: the themes that stop, the merges and the deploys. Every write asks first, and
merging deploys production, so ask before each merge, one site at a time.

## 1. Look first

- `doctor` (or `taw-fleet doctor --json`): which themes are behind, and which `U` will leave out
  (uncommitted changes, on another branch, an earlier `taw/update-*` branch not merged yet).
- `pull_requests`: earlier update pull requests still open. Merge or close those first; an update
  refuses while one is waiting.

Tell the person what will be updated and what's left out, and why.

## 2. Update

Call `update_all` (`U`, `taw-fleet update --all`). It asks once, then updates one theme after
another: a new branch, taw/core, the framework files, the migrations, the checks, a commit and a
pull request. A theme that stops doesn't stop the others. A restore point is saved before each.

For each theme that stopped, read its report (`.taw/update-report.md` in the theme):

- **A check failed on the theme's own code** (a test, PHPStan, a build): fix it on the update's
  branch, commit, push, and say what you changed.
- **The theme needs a decision** (a migration asks for a person, a conflict with the site's own
  changes): say what it is and stop for that theme.
- **Something outside the theme** (the network, GitHub's rate limit, Local): say what failed;
  `u` on that site retries.

## 3. Merge and deploy, one at a time

For each update pull request whose CI passed:

1. `merge_pr` for that site. It asks the dashboard's question ("Merge #12 into main? This deploys
   …"); the person answers. Merging deploys production.
2. Follow the deploy (`pull_requests` shows it running, then done or failed). Wait for it to
   finish before the next merge.
3. Then check production (`production`): the site answers and is verified.
4. Remind the person to clear that site's page cache now that the deploy has finished (Hummingbird,
   WP Rocket, the host's cache), not while it ran.

If CI failed on a pull request, read why, fix it on its branch if it's the theme's own code, and
come back to it.

## 4. Report

One line per site: updated from → to and deployed, waiting for a decision (what), or left out
(why). End with what's left for the person.

## Don't

- Don't merge more than one site at a time, or before CI passes.
- Don't commit to a branch that deploys; fixes go on the update's branch.
- Don't force-push, and don't close or delete someone's pull request without asking.
