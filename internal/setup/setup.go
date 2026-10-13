// Package setup is a site's readiness: the SETUP checklist in the details
// (plan taw-fleet-product.md § B). Each step says what's there, or how to
// do it, by key or by hand. It reads what the scan, the settings and the
// production check already know; it asks nothing itself.
package setup

import (
	"strings"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Step is one line of the checklist.
type Step struct {
	Name  string `json:"name"` // Repository, Deploy…
	Done  bool   `json:"done"`
	Later bool   `json:"later,omitempty"` // not yet: it comes after an earlier step (production before its companion)
	State string `json:"state"`           // what's there, or why it waits
	Fix   string `json:"fix,omitempty"`   // how to do it
	Key   string `json:"key,omitempty"`   // the dashboard key that does it ("" = by hand)
}

// Input is what the checklist reads.
type Input struct {
	Site   site.Site
	Theme  site.Theme
	Config config.Site
	Bot    bool // the site's bot credentials are in the Keychain
}

// Steps is the checklist for a site's theme, in the order things get set up.
func Steps(in Input) []Step {
	s, t, c := in.Site, in.Theme, in.Config
	g := t.Git
	var out []Step

	repo := Step{Name: "Repository"}
	switch {
	case g == nil:
		repo.State, repo.Fix = "not a git repository", "git init and a first commit in the theme folder"
	case g.Repo == nil:
		repo.State, repo.Fix, repo.Key = "not on GitHub", "I creates a private GitHub repository and pushes it", "I"
	case !g.HasUpstream() || g.Ahead > 0:
		repo.State, repo.Fix = g.Repo.FullName()+", not pushed", "git push -u origin "+g.Branch
	default:
		repo.Done, repo.State = true, g.Repo.FullName()
	}
	out = append(out, repo)

	hook := Step{Name: "Secrets check"}
	switch {
	case g == nil:
		hook.Later, hook.State = true, "after the repository"
	case g.Hook == "on" || g.Hook == "own":
		hook.Done, hook.State = true, "commits with a database, .sync/, .env or a key are refused"
	case g.Hook == "off":
		hook.State, hook.Fix = "off", "git config core.hooksPath vendor/taw/core/resources/git-hooks"
	default:
		hook.State, hook.Fix, hook.Key = "comes with taw/core 1.94", "u updates the site", "u"
	}
	out = append(out, hook)

	deploy := Step{Name: "Deploy"}
	switch {
	case g == nil || g.Repo == nil:
		deploy.Later, deploy.State = true, "after the repository"
	case t.GitHub == nil:
		deploy.State, deploy.Fix, deploy.Key = "not checked yet", "L checks GitHub", "L"
	case t.GitHub.Error != "":
		deploy.State, deploy.Fix = "GitHub didn't answer for "+g.Repo.FullName(), "gh auth status: is it another account's repository?"
	case t.GitHub.Deploy != nil:
		deploy.Done, deploy.State = true, "workflow "+t.GitHub.Deploy.Workflow
	default:
		deploy.State, deploy.Fix = "no deploy workflow", "by hand for now: a .github/workflows/deploy.yml and its secrets (taw-fleet's deploy setup comes with hosting)"
	}
	out = append(out, deploy)

	prod := Step{Name: "Production"}
	if c.ProductionURL != "" {
		prod.Done, prod.State = true, c.ProductionURL
	} else {
		prod.State, prod.Fix = "no production URL", "production_url under [sites."+s.Slug+"] in the settings (taw-fleet config path)"
	}
	out = append(out, prod)

	comp := Step{Name: "Companion"}
	p := s.Production
	switch {
	case c.ProductionURL == "":
		comp.Later, comp.State = true, "after production"
	case p == nil:
		comp.State, comp.Fix, comp.Key = "not checked yet", "L checks production", "L"
	case p.Verified:
		comp.Done, comp.State = true, "answers, verified"
	case p.ErrorKind == "no-key":
		comp.State, comp.Fix = "answers, not verified", "taw-fleet live trust "+s.Slug
	case !p.Reachable:
		comp.State, comp.Fix = "doesn't answer", "the theme ships the companion: deploy it; then L"
	default:
		comp.State, comp.Fix = "refused ("+p.ErrorKind+")", "taw-fleet live "+s.Slug+" says why"
	}
	out = append(out, comp)

	bot := Step{Name: "Bot"}
	switch {
	case c.ProductionURL == "":
		bot.Later, bot.State = true, "after production"
	case in.Bot:
		bot.Done, bot.State = true, "stored in the Keychain"
	default:
		bot.State, bot.Fix = "no bot", "a WordPress user with an application password, then taw-fleet wp-remote key import "+s.Slug
	}
	out = append(out, bot)

	fb := Step{Name: "BugSmash"}
	switch {
	case c.BugSmashProject != "":
		fb.Done, fb.State = true, "project linked"
	case c.ProductionURL == "":
		fb.Later, fb.State = true, "after production"
	default:
		fb.State, fb.Fix = "no project", "bugsmash_project under [sites."+s.Slug+"] (taw-fleet comments projects lists them)"
	}
	out = append(out, fb)

	skills := Step{Name: "Skills"}
	if n := len(t.SkillsMissing); n == 0 {
		skills.Done, skills.State = true, "taw/core's site skills installed"
	} else {
		skills.State = "missing: " + strings.Join(t.SkillsMissing, ", ")
		skills.Fix = "taw skills:sync --apply in the theme folder"
		if t.Kind == site.KindClassic {
			skills.Fix, skills.Key = "S applies the scaffold, skills included", "S"
		}
	}
	out = append(out, skills)
	return out
}

// Next is the first step that isn't done and can be done now.
func Next(steps []Step) (Step, bool) {
	for _, s := range steps {
		if !s.Done && !s.Later {
			return s, true
		}
	}
	return Step{}, false
}

// Count is how many steps are done, of those that can be now or later.
func Count(steps []Step) (done, all int) {
	for _, s := range steps {
		if s.Done {
			done++
		}
	}
	return done, len(steps)
}
