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
