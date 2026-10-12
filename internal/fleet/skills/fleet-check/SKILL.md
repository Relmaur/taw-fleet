---
name: fleet-check
description: The morning check of the whole TAW fleet. Reads what taw-fleet knows (doctor, production, client comments, pull requests and deploys, watch) and writes one short summary, most urgent first, with the next action for each item. Changes nothing. Triggers on: "check the fleet", "what needs attention", "morning check", "how are the sites", "anything broken".
owner: taw-fleet
---

# Fleet check

A read-only look at every TAW site, ending in one summary the person can act on. Nothing changes:
no updates, merges, restarts or replies. If something should be done, say so and name the key or
the request that does it.

## 1. Gather

Use taw-fleet's MCP tools (or the same commands with `--json` in a shell):

| Tool | Command | What it tells |
|---|---|---|
| `doctor` | `taw-fleet doctor --json` | findings per site, with codes, severity and the fix |
| `production` | `taw-fleet live --json` | each production site through its companion: up, verified, WordPress and plugin versions, vulnerabilities |
| `comments` | `taw-fleet comments --json` | open BugSmash comments per site, and how long they've waited |
| `pull_requests` | `taw-fleet prs --json` | open pull requests with CI, and what each theme has deployed |

Also read `taw-fleet watch status`: when watch last ran and what it told (a site that went down
overnight, a failed deploy).

If a source fails (no network, a missing key), say which and go on with the rest.

## 2. Sort

Most urgent first:

1. **Down or wrong on production:** a site that doesn't answer or isn't verified, a failed deploy,
   a known vulnerability in an active plugin.
2. **Clients waiting:** open comments, the oldest first; more than 2 days is late.
3. **Ready to ship:** pull requests whose CI passed (often `taw/update-*`), and commits on a
   default branch that aren't deployed.
4. **Behind:** themes behind on taw/core, the scaffold check out of date.
5. **Housekeeping:** uncommitted or unpushed work, a theme on the wrong branch, a broken symlink.

Skip what's fine. One line per item: the site, what's wrong, and the next action.

## 3. Report

```
Fleet check, <date>

Needs you now
- fsspx: production didn't answer at 06:10 and 06:15 (watch). → `L` to check again; then the host.

Clients waiting
- chcapital: 3 open comments, the oldest 4 days. → `X` resolves them with Claude.

Ready to ship
- ls-mexico #14 (taw/core 1.95.1) passed CI. → `M` merges and deploys; clear the page cache after.

Behind
- emelambda, mlizardo on taw/core 1.93.x. → `U` updates every theme that's behind.

All fine: <the sites with nothing to report>
```

End with the one or two things worth doing first. Don't do them: the person decides.
