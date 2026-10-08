package doctor

import (
	"strings"
	"testing"

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
