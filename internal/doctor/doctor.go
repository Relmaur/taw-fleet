// Package doctor turns a scan into findings: what's behind, unsaved,
// unpushed or broken, each with a severity and, where there is one, the fix.
//
// Rules only read the report; they never run commands. Codes are stable and
// dot-namespaced, so scripts can filter on them.
package doctor

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Options carries what the rules can't read from the report.
type Options struct {
	// PHPAvailable reports whether Local has the PHP build a site uses. nil
	// skips the check.
	PHPAvailable func(version string) bool
}

// Run checks every site in the report. Findings come back most severe first,
// then by site and theme.
func Run(rep scan.Report, opts Options) []site.Finding {
	var out []site.Finding
	for _, e := range rep.Errors {
		out = append(out, site.Finding{
			Severity: site.Warn, Code: "scan." + e.Stage,
			Message: e.Err,
			Fix:     fixForSource(e.Stage),
		})
	}
	for _, s := range rep.Sites {
		out = append(out, checkSite(s, opts)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Severity != b.Severity {
			return a.Severity > b.Severity
		}
		if a.Site != b.Site {
			return a.Site < b.Site
		}
		return a.Theme < b.Theme
	})
	return out
}

func fixForSource(stage string) string {
	switch stage {
	case "local":
		return "install Local (localwp.com) and create or import a site"
	case "github":
		return "run once online to fill the cache; if GitHub rate-limits you, set GITHUB_TOKEN or run `gh auth login`"
	}
	return ""
}

func checkSite(s site.Site, opts Options) []site.Finding {
	var out []site.Finding
	add := func(sev site.Severity, code, theme, msg, fix string) {
		out = append(out, site.Finding{Severity: sev, Code: code, SiteID: s.ID, Site: s.Slug, Theme: theme, Message: msg, Fix: fix})
	}

	for _, e := range s.Errors {
		add(site.Warn, "site.read-error", "", fmt.Sprintf("%s: %s", e.Stage, e.Err), "")
	}
	if !s.IsTAW() {
		return out
	}
	if opts.PHPAvailable != nil && s.PHPVersion != "" && !opts.PHPAvailable(s.PHPVersion) {
		add(site.Warn, "tools.php-missing", "", "Local doesn't have PHP "+s.PHPVersion+" for this site",
			"open the site in Local once so it downloads PHP "+s.PHPVersion)
	}
	for _, t := range s.Themes {
		if t.Broken {
			add(site.Error, "theme.symlink-broken", t.Dir, "the theme is a symlink to a folder that no longer exists",
				"recreate the link, or run the taw-new-machine skill in the umbrella")
			continue
		}
		if !t.IsTAW {
			continue
		}
		out = append(out, checkCore(s, t)...)
		out = append(out, checkGit(s, t)...)
		out = append(out, checkGitHub(s, t)...)
		if n := len(t.SkillsMissing); n > 0 {
			fix := "php bin/taw skills:sync --apply in the theme"
			if t.Kind == site.KindClassic {
				fix = "S (sync the scaffold), or " + fix
			}
			add(site.Info, "skills.missing", t.Dir,
				fmt.Sprintf("%d site %s from taw/core %s installed: %s", n, plural(n, "skill", "skills"), plural(n, "isn't", "aren't"), strings.Join(t.SkillsMissing, ", ")), fix)
		}
	}
	out = append(out, checkLive(s)...)
	return append(out, checkFeedback(s)...)
}

// checkFeedback reads the site's BugSmash project (taw-fleet comments).
func checkFeedback(s site.Site) []site.Finding {
	f := s.Feedback
	if f == nil {
		return nil
	}
	finding := func(sev site.Severity, code, msg, fix string) []site.Finding {
		return []site.Finding{{Severity: sev, Code: code, SiteID: s.ID, Site: s.Slug, Message: msg, Fix: fix}}
	}
	switch f.ErrorKind {
	case "":
	case "no-key":
		return finding(site.Info, "comments.no-key", "BugSmash comments aren't checked: no API key",
			"store it with `taw-fleet comments key import` (BugSmash → Settings → API Key)")
	case "not-found":
		return finding(site.Warn, "comments.unreachable", "its BugSmash project "+f.ProjectID+" is gone (deleted or re-created)",
			"`taw-fleet comments projects` lists the projects; put the new id in bugsmash_project (`taw-fleet config path`)")
	default:
		return finding(site.Warn, "comments.unreachable", "BugSmash: "+f.Error,
			"check the key (`taw-fleet comments key show`) and https://bugsmash.io")
	}
	if f.Open == 0 {
		return nil
	}
	age := f.CheckedAt.Sub(f.Oldest)
	sev, when := site.Info, ""
	if !f.Oldest.IsZero() {
		when = ", the oldest " + ageWords(age) + " old"
		if f.Stale() {
			sev = site.Warn
		}
	}
	return finding(sev, "comments.open", fmt.Sprintf("%d open %s in BugSmash%s", f.Open, plural(f.Open, "comment", "comments"), when),
		"X in the dashboard hands them to Claude (skill taw-resolve-comments), or ask Claude in the TAW umbrella: \"resolve comments on "+s.Slug+"\"")
}

func ageWords(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "under an hour"
	case d < 48*time.Hour:
		return fmt.Sprintf("%d %s", int(d.Hours()), plural(int(d.Hours()), "hour", "hours"))
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%d days", days)
}

// checkLive reads what the production site's companion said (taw-fleet live).
func checkLive(s site.Site) []site.Finding {
	p := s.Production
	if p == nil {
		return nil
	}
	var out []site.Finding
	add := func(sev site.Severity, code, msg, fix string) {
		out = append(out, site.Finding{Severity: sev, Code: code, SiteID: s.ID, Site: s.Slug, Message: msg, Fix: fix})
	}
	switch {
	case p.ErrorKind == "signature":
		add(site.Error, "live.signature", p.URL+": "+p.Error,
			"if the site's key was rotated on purpose, pin the new one with `taw-fleet live trust "+s.Slug+"`; otherwise find out who answers for this site")
		return out
	case p.ErrorKind == "auth":
		add(site.Warn, "live.refused", p.URL+": "+p.Error,
			"give the site taw-fleet's public key (`taw-fleet live key show`): the companion mu-plugin rollout, or TAW_HUB_PUBLIC_KEY + TAW_HUB_KEY_ID in wp-config.php")
		return out
	case !p.Reachable:
		add(site.Warn, "live.unreachable", p.URL+": "+p.Error, "check the site in a browser and in WPMUDev")
		return out
	case p.ErrorKind == "no-key":
		add(site.Info, "live.untrusted", p.URL+": answers aren't verified (no pinned key for this site)",
			"`taw-fleet live trust "+s.Slug+"` pins the key it presents")
	}
	if !p.HasInventory && p.Companion != "" {
		add(site.Info, "live.companion-outdated", "production runs companion "+p.Companion+", without the inventory and vulnerability routes",
			"update the companion (it ships with the theme as an mu-plugin from companion 0.3.0)")
	}
	if p.TawCore != "" {
		var local []string
		match := false
		for _, t := range s.TAWThemes() {
			if t.Core.Installed == "" {
				continue
			}
			local = append(local, strings.TrimPrefix(t.Core.Installed, "v"))
			if strings.TrimPrefix(t.Core.Installed, "v") == strings.TrimPrefix(p.TawCore, "v") {
				match = true
			}
		}
		if len(local) > 0 && !match {
			add(site.Info, "live.core-mismatch",
				fmt.Sprintf("production runs taw/core %s; this Mac has %s", strings.TrimPrefix(p.TawCore, "v"), strings.Join(local, ", ")),
				"deploy the theme (push to main), or pull if production is ahead")
		}
	}
	if n := len(p.Vulns); n > 0 {
		sev := site.Warn
		if p.WorstSeverity == "high" || p.WorstSeverity == "critical" {
			sev = site.Error
		}
		add(sev, "live.vulnerable",
			fmt.Sprintf("%d known %s on production (worst: %s), per %s", n, plural(n, "vulnerability", "vulnerabilities"), p.WorstSeverity, p.Scanner),
			"update the affected plugins or themes (taw-fleet live "+s.Slug+" lists them)")
	}
	if n := len(p.PluginUpdates); n > 0 {
		add(site.Info, "live.plugin-updates", fmt.Sprintf("%d %s waiting on production: %s", n, plural(n, "plugin update", "plugin updates"), strings.Join(p.PluginUpdates, ", ")),
			"update them in WPMUDev or wp-admin")
	}
	return out
}

func checkCore(s site.Site, t site.Theme) []site.Finding {
	f := func(sev site.Severity, code, msg, fix string) site.Finding {
		return site.Finding{Severity: sev, Code: code, SiteID: s.ID, Site: s.Slug, Theme: t.Dir, Message: msg, Fix: fix}
	}
	var out []site.Finding
	c := t.Core
	switch {
	case c.Err != "":
		out = append(out, f(site.Error, "core.unreadable", "can't read the installed taw/core: "+c.Err, "composer install"))
	case c.Installed == "":
		out = append(out, f(site.Error, "core.missing", "taw/core isn't installed (no vendor/)", "composer install"))
	case c.Behind:
		out = append(out, f(site.Warn, "core.behind",
			fmt.Sprintf("taw/core %s, latest is %s", c.Installed, c.Latest),
			"composer update taw/core, then read vendor/taw/core/UPGRADING.md"))
	}
	if c.LockMismatch {
		out = append(out, f(site.Warn, "core.lock-mismatch",
			fmt.Sprintf("vendor/ has taw/core %s but composer.lock pins %s", c.Installed, c.Locked),
			"composer install"))
	}
	if d := t.Drift; d != nil {
		when := d.At.Format("2006-01-02")
		switch {
		case len(d.Errors) > 0:
			out = append(out, f(site.Warn, "scaffold.check-failed", "the last sync check failed ("+when+"): "+d.Errors[0], "taw-fleet sync "+s.Slug))
		case len(d.Tier1) > 0:
			out = append(out, f(site.Info, "scaffold.drift",
				fmt.Sprintf("%d framework %s from taw-theme (checked %s): %s", len(d.Tier1), plural(len(d.Tier1), "path differs", "paths differ"), when, strings.Join(d.Tier1, ", ")),
				"taw-fleet sync "+s.Slug+" --apply, or hand the update off: taw-fleet handoff "+s.Slug))
		}
	}
	if isUmbrellaScaffold(t) && t.Git.LastTag != "" && t.Scaffold.Latest != "" &&
		t.Git.LastTag != t.Scaffold.Latest {
		out = append(out, f(site.Info, "scaffold.behind",
			fmt.Sprintf("checkout is at %s, %s %s is released", t.Git.LastTag, t.Scaffold.Name, t.Scaffold.Latest),
			"git pull (the umbrella checkout is behind its own releases)"))
	}
	return out
}

// isUmbrellaScaffold: the theme IS taw-theme or taw-gutenberg (not a fork).
func isUmbrellaScaffold(t site.Theme) bool {
	if t.Git == nil || t.Git.Repo == nil || t.Scaffold.Name == "" {
		return false
	}
	return strings.EqualFold(t.Git.Repo.FullName(), "Relmaur/"+t.Scaffold.Name)
}

func checkGit(s site.Site, t site.Theme) []site.Finding {
	f := func(sev site.Severity, code, msg, fix string) site.Finding {
		return site.Finding{Severity: sev, Code: code, SiteID: s.ID, Site: s.Slug, Theme: t.Dir, Message: msg, Fix: fix}
	}
	g := t.Git
	if g == nil {
		return []site.Finding{f(site.Info, "git.none", "the theme isn't its own git repository", "git init, then push it to GitHub")}
	}
	var out []site.Finding
	if g.Dirty > 0 {
		out = append(out, f(site.Info, "git.dirty", fmt.Sprintf("%d uncommitted %s", g.Dirty, plural(g.Dirty, "change", "changes")), "commit or stash"))
	}
	switch {
	case g.Detached:
		out = append(out, f(site.Warn, "git.detached", "HEAD is detached (not on a branch)", "git switch <branch>"))
	case !g.HasUpstream():
		out = append(out, f(site.Warn, "git.no-upstream", "branch "+g.Branch+" isn't pushed (no upstream)", "git push -u origin "+g.Branch))
	default:
		if g.Behind > 0 {
			out = append(out, f(site.Warn, "git.behind", fmt.Sprintf("%d %s to pull from %s", g.Behind, plural(g.Behind, "commit", "commits"), g.Upstream), "git pull"))
		}
		if g.Ahead > 0 {
			out = append(out, f(site.Info, "git.ahead", fmt.Sprintf("%d %s not pushed", g.Ahead, plural(g.Ahead, "commit", "commits")), "git push"))
		}
	}
	if !g.Detached && g.DefaultBranch != "" && g.Branch != g.DefaultBranch {
		out = append(out, f(site.Info, "git.off-default", fmt.Sprintf("on %s, not %s", g.Branch, g.DefaultBranch), ""))
	}
	if g.RemoteURL == "" {
		out = append(out, f(site.Warn, "git.no-remote", "no origin remote", "git remote add origin <url>"))
	}
	return out
}

// checkGitHub reads the theme's pull requests and deploys (taw-fleet prs).
func checkGitHub(s site.Site, t site.Theme) []site.Finding {
	st := t.GitHub
	if st == nil || st.Error != "" {
		return nil
	}
	f := func(sev site.Severity, code, msg, fix string) site.Finding {
		return site.Finding{Severity: sev, Code: code, SiteID: s.ID, Site: s.Slug, Theme: t.Dir, Message: msg, Fix: fix}
	}
	var out []site.Finding
	for _, pr := range st.PRs {
		name := fmt.Sprintf("PR #%d (%s)", pr.Number, pr.Title)
		switch {
		case pr.Draft:
		case pr.Checks == site.ChecksFailing:
			out = append(out, f(site.Warn, "pr.failing", name+": CI failed", "open it on GitHub (G) and fix the branch"))
		case pr.Conflicted:
			out = append(out, f(site.Warn, "pr.conflict", name+" conflicts with "+st.Default, "merge "+st.Default+" into "+pr.Branch+" and resolve"))
		case pr.Ready():
			out = append(out, f(site.Info, "pr.ready", name+" is ready to merge", "M in the dashboard, or taw-fleet merge "+s.Slug))
		}
	}
	d := st.Deploy
	switch {
	case d == nil:
	case d.Failed != nil && d.Running == nil:
		out = append(out, f(site.Warn, "deploy.failed", "the last deploy ("+shortSHA(d.Failed.SHA)+") failed", "see the run: "+d.Failed.URL))
	case d.Running == nil && d.Deployed != "" && d.Deployed != st.Head && st.HeadCI == site.ChecksFailing:
		out = append(out, f(site.Warn, "deploy.blocked", "CI failed on "+st.Default+", so it isn't deployed", "fix "+st.Default+" (the deploy waits for CI)"))
	case d.Running == nil && d.Deployed != "" && d.Deployed != st.Head && st.HeadCI != site.ChecksPending:
		out = append(out, f(site.Info, "deploy.pending", fmt.Sprintf("%d %s on %s not deployed", d.Behind, plural(d.Behind, "commit", "commits"), st.Default),
			"re-run the deploy workflow on GitHub: "+d.URL))
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Counts tallies findings by severity.
func Counts(fs []site.Finding) (errors, warns, infos int) {
	for _, f := range fs {
		switch f.Severity {
		case site.Error:
			errors++
		case site.Warn:
			warns++
		default:
			infos++
		}
	}
	return errors, warns, infos
}
