---
name: onboard-mac
description: Set up a Mac to run the TAW fleet with taw-fleet. Checks the tools (Local, Claude Code, GitHub's gh, git), the fleet's sites, and the keys taw-fleet keeps in the Keychain (production signing key, BugSmash, bots, PageSpeed), then the background watch. Explains each step and asks before anything is installed or stored. Triggers on: "set up this Mac", "new Mac", "onboard", "first time with taw-fleet", "install the fleet tools".
owner: taw-fleet
---

# Onboard a Mac

Get a Mac ready to look after the fleet. Go step by step, say what each step is for, and ask
before installing anything or storing a key. Secrets go in through taw-fleet's own commands, which
read them from the person's paste on standard input: never put a key on a command line, in a file,
or in this chat.

## 1. Tools

Check each, and say how to get what's missing:

| Tool | Check | Get it |
|---|---|---|
| Local | `/Applications/Local.app` exists | localwp.com; open it once |
| taw-fleet | `taw-fleet version` | `brew install Relmaur/tap/taw-fleet` |
| Claude Code | `claude --version` | claude.com/claude-code, then sign in |
| GitHub CLI | `gh auth status` | `brew install gh`, then `gh auth login` |
| git | `git --version` | Xcode's command line tools |

Local brings its own PHP, Composer and MySQL; nothing else is needed for the sites.

## 2. The sites

- `taw-fleet doctor`: every TAW site Local knows, and what needs attention. A theme missing
  `vendor/` needs `composer install` in its folder (`core.missing`); a PHP version Local hasn't
  downloaded shows as `tools.php-missing` (start the site once in Local).
- Settings: `taw-fleet config path` shows the file; `taw-fleet config init` writes a commented
  starter. Each production site needs its `production_url` there; `bugsmash_project` links its
  client comments (`taw-fleet comments projects` lists the ids).

## 3. Keys (each asks, each goes in the Keychain)

| What | Command | Needed for |
|---|---|---|
| Production signing key | `taw-fleet live key show` to check; `taw-fleet live key import` (paste) on a second Mac, or `taw-fleet live key new` for a new fleet | `L`, production checks, pulling content |
| Each site's key | `taw-fleet live trust <site>` | verified answers from production |
| BugSmash | `taw-fleet comments key import` (paste) | client comments, `F`, `X` |
| A site's bot (production writes) | `taw-fleet wp-remote key import <site>` (paste) | resolving comments on production |
| PageSpeed Insights | `taw-fleet report key import` (paste) | the speed section of monthly reports |

A new signing key means every site's companion must trust it: say so before making one, and only
make one for a new fleet.

## 4. In the background

- `taw-fleet watch install`: a check every 5 minutes, and a Mac notification when something needs
  the person (a failed deploy, a site down, new comments). `taw-fleet watch test` sends one to see
  that they arrive.
- `taw-fleet mcp`: Claude Code reads the fleet with typed tools. `claude mcp add taw-fleet --
  taw-fleet mcp` adds it for every folder; the fleet folder (`~/TAW Fleet/`) already has it.

## 5. Check

`taw-fleet doctor` again, then open the dashboard (`taw-fleet`) and press `L`: every production
site should answer and be verified. Report what's done and what's left.
