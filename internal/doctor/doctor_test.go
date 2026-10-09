package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
)

func clean() site.Theme {
	return site.Theme{
		Dir: "theme", IsTAW: true, Kind: site.KindClassic,
		Core:     site.CoreInfo{Installed: "v1.76.1", Locked: "v1.76.1", Latest: "v1.76.1"},
		Scaffold: site.ScaffoldInfo{Name: "taw-theme", Latest: "v1.12.43"},
		Git: &site.GitInfo{
			Branch: "main", DefaultBranch: "main", Upstream: "origin/main",
			RemoteURL: "git@github.com:Relmaur/client--theme.git",
			Repo:      &site.Repo{Host: "github.com", Owner: "Relmaur", Name: "client--theme"},
		},
	}
}

func codes(fs []site.Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Code)
	}
	return out
}

func one(t *testing.T, th site.Theme) []site.Finding {
	t.Helper()
	rep := scan.Report{Sites: []site.Site{{ID: "s1", Slug: "client", PHPVersion: "8.2.30", Themes: []site.Theme{th}}}}
	return Run(rep, Options{})
}

func TestCleanThemeHasNoFindings(t *testing.T) {
	if fs := one(t, clean()); len(fs) != 0 {
		t.Errorf("findings = %+v", fs)
	}
}

func TestRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*site.Theme)
		want   []string
	}{
		{"behind", func(th *site.Theme) { th.Core.Installed, th.Core.Locked, th.Core.Behind = "v1.59.2", "v1.59.2", true }, []string{"core.behind"}},
		{"missing", func(th *site.Theme) { th.Core.Installed, th.Core.Locked = "", "" }, []string{"core.missing"}},
		{"unreadable", func(th *site.Theme) { th.Core.Err = "bad json" }, []string{"core.unreadable"}},
		{"lock mismatch", func(th *site.Theme) { th.Core.Locked, th.Core.LockMismatch = "v1.75.0", true }, []string{"core.lock-mismatch"}},
		{"dirty", func(th *site.Theme) { th.Git.Dirty = 5 }, []string{"git.dirty"}},
		{"ahead and behind", func(th *site.Theme) { th.Git.Ahead, th.Git.Behind = 2, 1 }, []string{"git.behind", "git.ahead"}},
		{"no upstream, off default", func(th *site.Theme) { th.Git.Branch, th.Git.Upstream = "chore/taw-core-1.76.1", "" }, []string{"git.no-upstream", "git.off-default"}},
		{"staging tracked", func(th *site.Theme) { th.Git.Branch, th.Git.Upstream = "staging", "origin/staging" }, []string{"git.off-default"}},
		{"detached", func(th *site.Theme) { th.Git.Branch, th.Git.Detached = "", true }, []string{"git.detached"}},
		{"no remote", func(th *site.Theme) { th.Git.RemoteURL, th.Git.Repo = "", nil }, []string{"git.no-remote"}},
		{"not a repo", func(th *site.Theme) { th.Git = nil }, []string{"git.none"}},
		{"fork with an old inherited tag is fine", func(th *site.Theme) { th.Git.LastTag = "v1.2.5" }, nil},
		{"scaffold drift from the last sync", func(th *site.Theme) {
			th.Drift = &site.Drift{Tier1: []string{"bin/"}, Tier2: []string{"composer.json"}, At: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)}
		}, []string{"scaffold.drift"}},
		{"only Tier 2 after a sync is fine", func(th *site.Theme) {
			th.Drift = &site.Drift{Tier2: []string{"composer.json"}, At: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)}
		}, nil},
		{"failed sync check", func(th *site.Theme) {
			th.Drift = &site.Drift{Errors: []string{"could not clone"}, At: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)}
		}, []string{"scaffold.check-failed"}},
		{"umbrella checkout behind its release", func(th *site.Theme) {
			th.Git.Repo = &site.Repo{Host: "github.com", Owner: "Relmaur", Name: "taw-theme"}
			th.Git.LastTag = "v1.12.40"
		}, []string{"scaffold.behind"}},
	}
	for _, c := range cases {
		th := clean()
		c.mutate(&th)
		got := codes(one(t, th))
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: codes = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBehindMessageAndFix(t *testing.T) {
	th := clean()
	th.Core.Installed, th.Core.Locked, th.Core.Behind = "v1.59.2", "v1.59.2", true
	f := one(t, th)[0]
	if f.Severity != site.Warn || f.Site != "client" || f.Theme != "theme" || f.SiteID != "s1" ||
		f.Message != "taw/core v1.59.2, latest is v1.76.1" || !strings.Contains(f.Fix, "composer update taw/core") {
		t.Errorf("finding = %+v", f)
	}
}

func TestSiteLevelRulesAndOrder(t *testing.T) {
	behind := clean()
	behind.Core.Installed, behind.Core.Locked, behind.Core.Behind = "v1.59.2", "v1.59.2", true
	dirty := clean()
	dirty.Git.Dirty = 1
	rep := scan.Report{
		Errors: []site.SourceError{{Stage: "github", Err: "rate limited"}},
		Sites: []site.Site{
			{ID: "b", Slug: "bravo", PHPVersion: "8.5.3", Themes: []site.Theme{dirty}},
			{ID: "a", Slug: "alpha", PHPVersion: "8.2.30", Themes: []site.Theme{behind, {Dir: "old-link", Symlink: true, Broken: true}}},
			{ID: "p", Slug: "plain", Errors: []site.SourceError{{Stage: "themes", Err: "missing"}}},
			{ID: "n", Slug: "not-taw", PHPVersion: "7.4.0", Themes: []site.Theme{{Dir: "tt5"}}},
		},
	}
	fs := Run(rep, Options{PHPAvailable: func(v string) bool { return v != "8.5.3" && v != "7.4.0" }})
	got := codes(fs)
	// scan.* findings have no site, so they sort first within their severity.
	want := []string{"theme.symlink-broken", "scan.github", "core.behind", "tools.php-missing", "site.read-error", "git.dirty"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("codes = %v\nwant    %v", got, want)
	}
	if !strings.Contains(fs[1].Fix, "GITHUB_TOKEN") {
		t.Errorf("github fix = %q", fs[1].Fix)
	}
	e, w, i := Counts(fs)
	if e != 1 || w != 4 || i != 1 {
		t.Errorf("counts = %d/%d/%d", e, w, i)
	}
}

func TestSeverityJSON(t *testing.T) {
	b, _ := site.Warn.MarshalText()
	var s site.Severity
	if string(b) != "warn" || s.UnmarshalText([]byte("error")) != nil || s != site.Error || s.UnmarshalText([]byte("x")) == nil {
		t.Error("severity text")
	}
}

func TestLiveRules(t *testing.T) {
	ok := site.Production{URL: "https://client.mx", Reachable: true, Verified: true, TawCore: "v1.76.1", HasInventory: true, HasVulns: true, Companion: "0.3.0"}
	cases := []struct {
		name   string
		mutate func(*site.Production)
		want   []string
	}{
		{"healthy", func(*site.Production) {}, nil},
		{"unreachable", func(p *site.Production) { p.Reachable, p.Error, p.ErrorKind = false, "timeout", "unreachable" }, []string{"live.unreachable"}},
		{"signature", func(p *site.Production) { p.Reachable, p.ErrorKind = false, "signature" }, []string{"live.signature"}},
		{"refused", func(p *site.Production) { p.Reachable, p.ErrorKind = false, "auth" }, []string{"live.refused"}},
		{"old companion", func(p *site.Production) { p.HasInventory, p.Companion = false, "0.1.2" }, []string{"live.companion-outdated"}},
		{"untrusted", func(p *site.Production) { p.Verified, p.ErrorKind = false, "no-key" }, []string{"live.untrusted"}},
		{"core mismatch", func(p *site.Production) { p.TawCore = "v1.59.2" }, []string{"live.core-mismatch"}},
		{"vulnerable high", func(p *site.Production) {
			p.Vulns, p.WorstSeverity, p.Scanner = []site.LiveVuln{{Severity: "high"}}, "high", "Defender"
		}, []string{"live.vulnerable"}},
		{"plugin updates", func(p *site.Production) { p.PluginUpdates = []string{"akismet 5.1 → 5.3"} }, []string{"live.plugin-updates"}},
	}
	for _, c := range cases {
		p := ok
		c.mutate(&p)
		rep := scan.Report{Sites: []site.Site{{ID: "s1", Slug: "client", PHPVersion: "8.2.30", Themes: []site.Theme{clean()}, Production: &p}}}
		got := codes(Run(rep, Options{}))
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	p := ok
	p.Vulns, p.WorstSeverity = []site.LiveVuln{{Severity: "medium"}}, "medium"
	fs := Run(scan.Report{Sites: []site.Site{{ID: "s1", Slug: "client", Themes: []site.Theme{clean()}, Production: &p}}}, Options{})
	if len(fs) != 1 || fs[0].Severity != site.Warn {
		t.Errorf("medium is a warning: %+v", fs)
	}
}

func TestGitHubRules(t *testing.T) {
	ok := site.RepoState{Repo: "Relmaur/client--theme", Default: "main", Head: "bbb", HeadCI: site.ChecksPassing, PRs: []site.PullRequest{},
		Deploy: &site.Deploy{Workflow: "Deploy", Deployed: "bbb"}}
	pr := site.PullRequest{Number: 7, Title: "Update taw/core", Branch: "chore/taw-core", Checks: site.ChecksPassing}
	cases := []struct {
		name   string
		mutate func(*site.RepoState)
		want   []string
	}{
		{"deployed, nothing open", func(*site.RepoState) {}, nil},
		{"ready PR", func(s *site.RepoState) { s.PRs = []site.PullRequest{pr} }, []string{"pr.ready"}},
		{"failing PR", func(s *site.RepoState) { p := pr; p.Checks = site.ChecksFailing; s.PRs = []site.PullRequest{p} }, []string{"pr.failing"}},
		{"conflict", func(s *site.RepoState) { p := pr; p.Conflicted = true; s.PRs = []site.PullRequest{p} }, []string{"pr.conflict"}},
		{"draft and pending say nothing", func(s *site.RepoState) {
			d, p := pr, pr
			d.Draft, p.Checks = true, site.ChecksPending
			s.PRs = []site.PullRequest{d, p}
		}, nil},
		{"behind", func(s *site.RepoState) { s.Head, s.Deploy.Behind = "ccc", 2 }, []string{"deploy.pending"}},
		{"behind, CI running", func(s *site.RepoState) { s.Head, s.HeadCI = "ccc", site.ChecksPending }, nil},
		{"behind, CI failed", func(s *site.RepoState) { s.Head, s.HeadCI = "ccc", site.ChecksFailing }, []string{"deploy.blocked"}},
		{"deploying", func(s *site.RepoState) { s.Head, s.Deploy.Running = "ccc", &site.Run{SHA: "ccc"} }, nil},
		{"failed", func(s *site.RepoState) { s.Head, s.Deploy.Failed = "ccc", &site.Run{SHA: "ccc", URL: "https://x"} }, []string{"deploy.failed"}},
		{"unreadable", func(s *site.RepoState) { s.Error = "no token" }, nil},
	}
	for _, c := range cases {
		st := ok
		d := *ok.Deploy
		st.Deploy = &d
		c.mutate(&st)
		th := clean()
		th.GitHub = &st
		if got := codes(one(t, th)); strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFeedbackRules(t *testing.T) {
	checked := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		f    site.Feedback
		want string
		sev  site.Severity
		msg  string
	}{
		{"none open", site.Feedback{CheckedAt: checked}, "", 0, ""},
		{"fresh", site.Feedback{CheckedAt: checked, Open: 3, Oldest: checked.Add(-5 * time.Hour)}, "comments.open", site.Info, "3 open comments in BugSmash, the oldest 5 hours old"},
		{"stale", site.Feedback{CheckedAt: checked, Open: 1, Oldest: checked.Add(-72 * time.Hour)}, "comments.open", site.Warn, "1 open comment in BugSmash, the oldest 3 days old"},
		{"no key", site.Feedback{ErrorKind: "no-key", Error: "x"}, "comments.no-key", site.Info, ""},
		{"gone", site.Feedback{ProjectID: "p1", ErrorKind: "not-found", Error: "x"}, "comments.unreachable", site.Warn, "its BugSmash project p1 is gone (deleted or re-created)"},
		{"down", site.Feedback{ErrorKind: "unreachable", Error: "timeout"}, "comments.unreachable", site.Warn, "BugSmash: timeout"},
	}
	for _, c := range cases {
		f := c.f
		rep := scan.Report{Sites: []site.Site{{ID: "s1", Slug: "client", PHPVersion: "8.2.30", Themes: []site.Theme{clean()}, Feedback: &f}}}
		fs := Run(rep, Options{})
		if strings.Join(codes(fs), ",") != c.want {
			t.Errorf("%s: %v, want %s", c.name, codes(fs), c.want)
			continue
		}
		if c.want == "" {
			continue
		}
		if fs[0].Severity != c.sev || (c.msg != "" && fs[0].Message != c.msg) {
			t.Errorf("%s: %v %q", c.name, fs[0].Severity, fs[0].Message)
		}
	}
	if fs := Run(scan.Report{Sites: []site.Site{{ID: "s1", Slug: "client", Themes: []site.Theme{clean()}, Feedback: &site.Feedback{Open: 2, CheckedAt: checked, Oldest: checked}}}}, Options{}); !strings.Contains(fs[0].Fix, `"resolve comments on client"`) {
		t.Errorf("fix = %q", fs[0].Fix)
	}
}

func TestSkillsMissing(t *testing.T) {
	th := clean()
	th.SkillsMissing = []string{"perf-audit", "resolve-comments"}
	fs := Run(scan.Report{Sites: []site.Site{{ID: "s1", Slug: "client", Themes: []site.Theme{th}}}}, Options{})
	var got *site.Finding
	for i := range fs {
		if fs[i].Code == "skills.missing" {
			got = &fs[i]
		}
	}
	if got == nil || got.Severity != site.Info || got.Message != "2 site skills from taw/core aren't installed: perf-audit, resolve-comments" ||
		!strings.HasPrefix(got.Fix, "S (sync the scaffold)") {
		t.Errorf("finding = %+v", got)
	}
}
