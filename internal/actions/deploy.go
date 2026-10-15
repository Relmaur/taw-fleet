package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/deploy"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/keychain"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// DeployStub is taw/core's deploy workflow stub inside a theme (1.97+).
const DeployStub = "vendor/taw/core/resources/deploy/deploy.yml"

// DeployBranch is the branch the stub is proposed on.
const DeployBranch = "deploy/setup"

// DeployKeys are the deploy keys kept between the setup's steps.
func (a *Actions) DeployKeys() deploy.Keys {
	return deploy.Keys{Keychain: keychain.Keychain{Exec: a.Exec}}
}

// DeployKey is the site's deploy key: the one made earlier in this setup, or
// a new one (made says which).
func (a *Actions) DeployKey(ctx context.Context, slug string) (key deploy.Key, made bool, err error) {
	keys := a.DeployKeys()
	key, err = keys.Load(ctx, slug)
	if err == nil {
		return key, false, nil
	}
	if !errors.Is(err, keychain.ErrNotFound) {
		return key, false, err
	}
	key, err = deploy.NewKey("taw-fleet deploy " + slug)
	if err != nil {
		return key, false, err
	}
	return key, true, keys.Save(ctx, slug, key)
}

// DeployTester tests a target with the deploy key.
func (a *Actions) DeployTester() deploy.Tester {
	return deploy.Tester{Exec: a.Exec, Dir: filepath.Join(a.Paths.CacheDir, "deploy")}
}

// hasDeployWorkflow is the theme's own deploy workflow, if it has one.
func hasDeployWorkflow(t site.Theme) string {
	matches, _ := filepath.Glob(filepath.Join(t.RealPath, ".github", "workflows", "deploy*.y*ml"))
	if len(matches) > 0 {
		return filepath.Base(matches[0])
	}
	return ""
}

// DeployTask sets up the theme's deploy to target (I on the Deploy step, taw-fleet deploy apply):
// the connection test again (pinning the host key), the repository's variables and its secret
// DEPLOY_SSH_KEY on GitHub (the key on stdin), taw/core's stub on a branch with a pull request,
// and production_url in the settings. The key's local copy is deleted once GitHub has it.
// Merging the pull request is the first deploy.
func (a *Actions) DeployTask(s site.Site, t site.Theme, target deploy.Target) (Task, error) {
	g := t.Git
	switch {
	case g == nil || g.Repo == nil:
		return Task{}, errors.New("the theme isn't on GitHub yet: I (taw-fleet repo) puts it there first")
	case hasDeployWorkflow(t) != "":
		return Task{}, fmt.Errorf("the theme deploys already (.github/workflows/%s): it keeps its own", hasDeployWorkflow(t))
	case g.Dirty > 0:
		return Task{}, fmt.Errorf("%d uncommitted %s: commit or stash first", g.Dirty, plural(g.Dirty, "change", "changes"))
	}
	if err := target.Check(); err != nil {
		return Task{}, err
	}
	stub, err := os.ReadFile(filepath.Join(t.RealPath, DeployStub))
	if err != nil {
		return Task{}, errors.New("the theme's taw/core has no shared deploy yet (1.97+): u updates the site first")
	}
	repo, def := g.Repo.FullName(), g.DefaultBranch
	if def == "" {
		def = "main"
	}
	return a.noted(s.Slug, Task{
		Title:  "Deploy setup: " + s.Slug,
		Writes: true,
		Ask: fmt.Sprintf("Set up %s's deploy to %s? It stores the deploy key and settings in %s (a secret and variables), "+
			"opens a pull request adding the deploy workflow (merging it deploys), and adds production_url = %s to your settings.",
			s.Slug, target.Where(), repo, target.URL),
		Target: s.ID + "/" + t.Dir,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			say := sayTo(out)
			ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
			defer cancel()
			gh, err := a.Paths.LookPath("gh")
			if err != nil {
				return Summary{}, errors.New("GitHub's CLI isn't installed: brew install gh, then gh auth login")
			}
			key, err := a.DeployKeys().Load(ctx, s.Slug)
			if err != nil {
				return Summary{}, errors.New("no deploy key for " + s.Slug + " yet: taw-fleet deploy key " + s.Slug + " makes one, to add at the host first")
			}
			res, err := a.DeployTester().Test(ctx, target, key)
			if err != nil {
				return Summary{}, fmt.Errorf("the connection test failed, nothing changed: %w", err)
			}
			say("Connection test: %s", res.Said)
			target.KnownHosts = res.KnownHosts

			run := func(stdin string, name string, args ...string) error {
				spec := exec.Spec{Name: name, Dir: t.RealPath, Args: args}
				if stdin != "" {
					spec.Stdin = strings.NewReader(stdin)
				}
				r, err := a.Exec.Run(ctx, spec)
				if err == nil && r.Code != 0 {
					err = errors.New(firstLine(string(r.Stderr) + "\n" + string(r.Stdout)))
				}
				return err
			}
			vars := target.Vars()
			for _, name := range deploy.VarNames(vars) {
				if err := run(vars[name], gh, "variable", "set", name, "--repo", repo); err != nil {
					return Summary{}, fmt.Errorf("gh variable set %s: %w", name, err)
				}
			}
			say("Set the variables %s on %s", strings.Join(deploy.VarNames(vars), ", "), repo)
			if err := run(string(key.Private), gh, "secret", "set", "DEPLOY_SSH_KEY", "--repo", repo); err != nil {
				return Summary{}, fmt.Errorf("gh secret set DEPLOY_SSH_KEY: %w", err)
			}
			say("Stored the deploy key as the secret DEPLOY_SSH_KEY")
			if err := a.DeployKeys().Delete(ctx, s.Slug); err != nil {
				say("The key's copy in the Keychain wasn't removed (%v): Keychain Access → taw-fleet → deploy-%s", err, s.Slug)
			}

			sum := Summary{Headline: s.Slug + " deploys to " + target.Where() + " once the pull request is merged"}
			back := g.Branch
			if err := run("", "git", "switch", "-c", DeployBranch, def); err != nil {
				return sum, fmt.Errorf("git switch -c %s %s: %w", DeployBranch, def, err)
			}
			restore := func() {
				if back != "" && back != DeployBranch {
					_ = run("", "git", "switch", back)
				}
			}
			wf := filepath.Join(t.RealPath, ".github", "workflows", "deploy.yml")
			if err := os.MkdirAll(filepath.Dir(wf), 0o755); err != nil {
				restore()
				return sum, err
			}
			if err := os.WriteFile(wf, stub, 0o644); err != nil {
				restore()
				return sum, err
			}
			steps := [][]string{
				{"git", "add", ".github/workflows/deploy.yml"},
				{"git", "commit", "-m", "Deploy with taw/core's shared workflow (" + string(target.Method) + ")"},
				{"git", "push", "-u", "origin", DeployBranch},
			}
			for _, st := range steps {
				if err := run("", st[0], st[1:]...); err != nil {
					restore()
					return sum, fmt.Errorf("%s: %w", strings.Join(st[:2], " "), err)
				}
			}
			body := "Adds `.github/workflows/deploy.yml`: taw/core's shared deploy (" + string(target.Method) + "), run after CI passes on " + def +
				". The repository's DEPLOY_* variables and DEPLOY_SSH_KEY secret were set by `taw-fleet deploy setup`. **Merging this deploys " + target.URL + ".**"
			prRes, err := a.Exec.Run(ctx, exec.Spec{Name: gh, Dir: t.RealPath, Args: []string{"pr", "create", "--base", def, "--head", DeployBranch,
				"--title", "Deploy to " + strings.TrimPrefix(strings.TrimPrefix(target.URL, "https://"), "http://"), "--body", body}})
			restore()
			if err != nil || prRes.Code != 0 {
				sum.Lines = append(sum.Lines, "the pull request wasn't opened: gh pr create --head "+DeployBranch)
			} else {
				pr := strings.TrimSpace(string(prRes.Stdout))
				say("Opened %s", pr)
				sum.Lines = append(sum.Lines, "pull request: "+pr+" (M merges it; merging deploys)")
			}

			if f, err := config.SetSite(a.Paths, s.Slug, "production_url", target.URL); err != nil {
				sum.Lines = append(sum.Lines, "production_url wasn't added to the settings ("+err.Error()+"): add it under [sites."+s.Slug+"]")
			} else {
				a.Config.Sites = withSite(a.Config.Sites, s.Slug, target.URL)
				say("Added production_url = %s to %s", target.URL, f)
				sum.Lines = append(sum.Lines, "production_url = "+target.URL+" in the settings")
			}
			sum.Lines = append(sum.Lines, "after the first deploy: taw-fleet live trust "+s.Slug+", then I for the rest of the SETUP checklist")
			return sum, nil
		},
	}), nil
}

func withSite(sites map[string]config.Site, slug, url string) map[string]config.Site {
	if sites == nil {
		sites = map[string]config.Site{}
	}
	c := sites[slug]
	c.ProductionURL = url
	sites[slug] = c
	return sites
}
