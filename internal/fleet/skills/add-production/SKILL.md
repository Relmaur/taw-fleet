---
name: add-production
description: Put a TAW site on its production host, any host, and set up its deploy: SSH + rsync (WPMU DEV, WP Engine's SSH gateway, Kinsta, SiteGround, Cloudways, a VPS…) or git push (WP Engine's git push, other git hosts). Asks for the host's details, makes a deploy key, waits while the person adds it at the host, tests the connection, then stores everything in GitHub and opens the deploy pull request. Triggers on: "add production", "set up the deploy", "deploy this site", "put it live", "connect the host".
owner: taw-fleet
---

# Add production

The site deploys through taw/core's shared deploy, to whatever host the person uses. taw-fleet's
tools do the work and ask first; this skill is the conversation around them. Never pick the host for
the person, and never handle a password or a private key: the deploy key is made and stored by
taw-fleet, and only its public half is ever shown.

## 0. Before

`site_setup` for the site. The Deploy step needs:
- **Repository** done: the theme on GitHub. If not, `create_repo` first (it asks).
- **No deploy yet.** A theme with its own deploy workflow keeps it; stop and say so.
- **taw/core 1.97 or newer** in the theme; else `update_site` first.

## 1. The host

Ask which host the site goes to. `deploy_profiles` lists the ones taw-fleet knows, what each
pre-fills and where in its panel each value is; "Other SSH host" and "Other git remote" cover any
other. If the person hasn't got hosting yet, stop: that's their choice to make, and any host with SSH
or a git remote works.

Then the details, from the person (the profile says where they find each):
- **rsync:** host, port, user, and the theme's folder on the server (ending in
  `/wp-content/themes/<theme>`; when unsure, they can SSH in and run `pwd` in that folder).
- **git:** the remote, the branch the host deploys, and where the theme sits in its repository.
- The **production URL**.

## 2. The key

`deploy_key` makes the site's deploy key (it asks) and returns its public half. Give the person
that line and where it goes (the profile's "public key"), and wait until they say it's added.

## 3. Test

`deploy_test` with the same details connects as the deploy will. If it fails, say what it said in
plain words (wrong port, key not added yet, the folder's parent missing, rsync not on the server)
and what to check; test again after each fix.

## 4. Apply

`deploy_apply` with the same details. It asks the person, then stores the settings and the key in
GitHub, opens the pull request adding the deploy workflow, and adds `production_url` to the
settings. Merging that pull request deploys: offer `merge_pr` once its CI passes (it asks again).

## 5. After the first deploy

Remind the person to clear the site's page cache after a deploy has finished, not during it. Then:
- `trust_production` once the site answers: it pins the companion's key (it asks);
- `bugsmash_projects` and `link_bugsmash`, if the person has a BugSmash project for the site;
- `launch_report`: production checked (HTTPS, search engines let in, the deploy, the companion,
  PageSpeed) and what's left for the person, in `~/TAW Fleet/sites/<site>/launch.html`. Give them
  its summary and what's left.

## Don't

- Don't ask for or accept a password, a private key or an API token in the chat.
- Don't guess host details: the profile's hints and the test decide.
- Don't replace a theme's own deploy workflow.
