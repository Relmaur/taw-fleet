package setup

import (
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/site"
)

func byName(steps []Step) map[string]Step {
	out := map[string]Step{}
	for _, s := range steps {
		out[s.Name] = s
	}
	return out
}

func TestNewSite(t *testing.T) {
	// Just created: a git repo with a first commit, nothing else.
	steps := Steps(Input{Site: site.Site{Slug: "acme"}, Theme: site.Theme{Dir: "acme", Kind: site.KindClassic, Git: &site.GitInfo{Branch: "main"}}})
	st := byName(steps)
	if r := st["Repository"]; r.Done || r.Key != "I" || r.State != "not on GitHub" {
		t.Errorf("repository %+v", r)
	}
	if d := st["Deploy"]; !d.Later {
		t.Errorf("deploy %+v", d)
	}
	for _, n := range []string{"Companion", "Bot"} {
		if !st[n].Later {
			t.Errorf("%s should wait for production: %+v", n, st[n])
		}
	}
	if p := st["Production"]; p.Done || p.Later || p.Fix == "" {
		t.Errorf("production %+v", p)
	}
	if !st["Skills"].Done {
		t.Errorf("skills %+v", st["Skills"])
	}
	if next, ok := Next(steps); !ok || next.Name != "Repository" {
		t.Errorf("next %+v", next)
	}
	if done, all := Count(steps); done != 1 || all != 8 {
		t.Errorf("count %d/%d", done, all)
	}
}

func TestLiveSite(t *testing.T) {
	g := &site.GitInfo{Branch: "main", DefaultBranch: "main", Upstream: "origin/main", Hook: "on", Repo: &site.Repo{Owner: "Relmaur", Name: "ls-mexico--theme"}}
	in := Input{
		Site:   site.Site{Slug: "ls-mxico", Production: &site.Production{URL: "https://lsmexico.mx", Reachable: true, Verified: true}},
		Theme:  site.Theme{Dir: "ls-mexico", Git: g, GitHub: &site.RepoState{Deploy: &site.Deploy{Workflow: "Deploy"}}},
		Config: config.Site{ProductionURL: "https://lsmexico.mx", BugSmashProject: "p-1"},
		Bot:    true,
	}
	steps := Steps(in)
	if done, all := Count(steps); done != all {
		t.Errorf("%d of %d: %+v", done, all, steps)
	}
	if _, ok := Next(steps); ok {
		t.Error("nothing should be next")
	}

	// GitHub can't read the repository: that isn't "no deploy workflow".
	in.Theme.GitHub = &site.RepoState{Error: "HTTP 404"}
	if d := byName(Steps(in))["Deploy"]; d.Done || !strings.Contains(d.State, "GitHub didn't answer") {
		t.Errorf("deploy %+v", d)
	}
	in.Theme.GitHub = &site.RepoState{Deploy: &site.Deploy{Workflow: "Deploy"}}

	// Answers, not pinned; no bot; unpushed.
	in.Site.Production = &site.Production{URL: "https://lsmexico.mx", Reachable: true, ErrorKind: "no-key"}
	in.Bot = false
	g.Ahead = 2
	st := byName(Steps(in))
	if c := st["Companion"]; c.Done || c.Fix != "taw-fleet live trust ls-mxico" {
		t.Errorf("companion %+v", c)
	}
	if b := st["Bot"]; b.Done || b.Fix == "" {
		t.Errorf("bot %+v", b)
	}
	if r := st["Repository"]; r.Done || r.Fix != "git push -u origin main" {
		t.Errorf("repository %+v", r)
	}
}
