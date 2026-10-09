# Update 2 TAW themes in one session

You're coordinating an update of the owner's TAW client themes on this Mac (taw-fleet, 2026-10-08 15:30). Each theme is updated by its own subagent following the update-theme skill's batch mode; you bring every decision back to the owner at once.

## The themes

| Site | Theme | taw/core | Prompt |
|---|---|---|---|
| ls-mxico | ls-mexico | 1.76.1 → 1.78.0 | `/C/fleet/ls-mxico-ls-mexico.md` |
| parallel-plus | parallelplus | current | `/C/fleet/parallel-plus-parallelplus.md` |

Left out (tell the owner in the final report; don't touch them):

- fsspx-taw / fsspx--theme: 2 uncommitted change(s)

## 1. Run the subagents

For each theme, start a subagent with the Agent tool (`subagent_type: "general-purpose"`), prompt: "Read <its prompt file> and follow it exactly." Run **at most 3 at a time** (Composer and npm compete on this Mac): start 3, and start the next as each finishes. Don't do a theme's work yourself, and don't ask the owner anything while they run.

Each subagent ends with a JSON block (`status`, `branch`, `commit`, `taw_core`, `tier1`, `manifests`, `proposals`, `skills`, `upgrading`, `verify`, `notes`). Keep each one. If a reply has no JSON block, record the theme as `failed` with the reply's last lines.

## 2. One round of decisions

When every subagent has finished, ask the owner with AskUserQuestion (multi-select, grouped; several questions per call):

1. **Tier 2 proposals.** Group them by file across themes (e.g. "AGENTS.md in ls-mexico, chcapital…"), with each summary and anything an overwrite would lose. For the approved ones, apply them yourself on that theme's branch the way the skill's Step 3 says (prose files: the canonical file, keeping site-specific sections; a manifest `review` item: that line only), run the theme's tests again, and commit ("Apply approved scaffold docs").
2. **Sites to start**, when any check is `needs-wordpress`: which sites to start for those checks. Start the chosen ones with `taw-fleet start <site> --yes`, run just those checks (with that theme's wp-cli from its prompt file), record the outcomes, then stop the sites you started with `taw-fleet stop <site> --yes`.
3. **Pushes**, last: which branches to push and open a PR for (only themes whose status is `updated` or whose open points are settled). For each chosen theme: `git push -u origin <branch>` and `gh pr create` against its default branch, with the result as the PR body. Nothing is pushed without this answer.

## 3. Final report

One table: site, theme, status, taw/core before → after, Tier 1, manifests applied, UPGRADING checks (passed / not applicable / open), tests · phpstan · build, branch and commit, PR link. Then the left-out themes and why, every `failed` or open item, and the skills the subagents reported under `warn` or `clash`.

## Rules

- Work only in the theme folders listed above. Never touch Blocks/, inc/options.php, inc/performance.php, inc/customizations.php, templates or content (the subagents follow the same rules).
- No push, no PR, no merge without the owner's answer in step 2; never commit to a default branch.
- Don't touch the production sites. Starting and stopping Local sites only as approved in step 2.
- When you're done and the owner has nothing else, say so: they close this window with /exit, and taw-fleet rescans.
