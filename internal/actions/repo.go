package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// DumpCheck is the status check every TAW theme's CI runs on its committed
// files (taw/core 1.93): the new repository requires it before anything
// reaches the default branch, as the fleet's other repositories do.
const DumpCheck = "ci / committed-files"

// RepoName is the GitHub repository a theme folder gets: the fleet's
// "<site>--theme" convention, unless the folder already ends in -theme.
func RepoName(dir string) string {
	if strings.HasSuffix(dir, "-theme") {
		return dir
	}
	return dir + "--theme"
}

// RepoOwner is where a new repository goes: the first of the settings'
// github_owners, or "" for the account gh is signed in to.
func (a *Actions) RepoOwner() string {
	if len(a.Config.GitHubOwners) > 0 {
		return a.Config.GitHubOwners[0]
	}
	return ""
}

// RepoTask puts a theme that has no GitHub repository yet on GitHub (I, the
// SETUP checklist's first step): a private repository (owner "" = gh's own
// account), origin, its branches pushed, and the dump check required on the
// default branch. Nothing is deleted or force-pushed.
func (a *Actions) RepoTask(s site.Site, t site.Theme, owner, name string) (Task, error) {
	g := t.Git
	switch {
	case g == nil:
		return Task{}, errors.New("the theme isn't a git repository yet (git init, then a first commit)")
	case g.Repo != nil:
		return Task{}, errors.New("it's on GitHub already: " + g.Repo.FullName())
	case g.Detached:
		return Task{}, errors.New("HEAD is detached: switch to a branch first")
	case strings.TrimSpace(name) == "":
		name = RepoName(t.Dir)
	}
	full, where := name, "your GitHub account"
	if owner != "" {
		full, where = owner+"/"+name, owner
	}
	def := g.DefaultBranch
	if def == "" {
		def = "main"
	}
	pushes := def
	if g.Branch != "" && g.Branch != def {
		pushes += " and " + g.Branch
	}
	return Task{
		Title:  "GitHub repository: " + t.Dir,
		Writes: true,
		Ask: fmt.Sprintf("Create the private repository %s on %s and push %s? Merging into %s will then require the dump check (%s).",
			name, where, pushes, def, DumpCheck),
		Info:   []string{"gh repo create " + full + " --private, origin, push, a ruleset on " + def},
		Target: s.ID + "/" + t.Dir,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			say := sayTo(out)
			ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			defer cancel()
			gh, err := a.Paths.LookPath("gh")
			if err != nil {
				return Summary{}, errors.New("GitHub's CLI isn't installed: brew install gh, then gh auth login")
			}
			run := func(name string, args ...string) (string, error) {
				res, err := a.Exec.Run(ctx, exec.Spec{Name: name, Dir: t.RealPath, Args: args})
				if err == nil && res.Code != 0 {
					err = errors.New(strings.TrimSpace(string(res.Stderr) + " " + string(res.Stdout)))
				}
				return strings.TrimSpace(string(res.Stdout)), err
			}
			desc := s.Name
			if desc == "" {
				desc = s.Slug
			}
			if _, err := run(gh, "repo", "create", full, "--private", "--source", t.RealPath, "--remote", "origin",
				"--description", desc+" (TAW theme)"); err != nil {
				return Summary{}, fmt.Errorf("gh repo create: %w", err)
			}
			repo, err := run(gh, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
			if err != nil || repo == "" {
				repo = full
			}
			say("Created %s (private)", repo)
			sum := Summary{Headline: t.Dir + " is on GitHub: " + repo}
			branches := []string{def}
			if g.Branch != "" && g.Branch != def {
				branches = append(branches, g.Branch)
			}
			for _, b := range branches {
				if _, err := run("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err != nil {
					continue // no such branch here
				}
				if _, err := run("git", "push", "-u", "origin", b); err != nil {
					return sum, fmt.Errorf("git push %s: %w", b, err)
				}
				say("Pushed %s", b)
				sum.Lines = append(sum.Lines, "pushed "+b)
			}
			_, _ = run("git", "remote", "set-head", "origin", "--auto")
			if err := a.requireDumpCheck(ctx, gh, t.RealPath, repo); err != nil {
				sum.Lines = append(sum.Lines, "the dump check isn't required yet ("+err.Error()+"): GitHub → Settings → Rules → add "+DumpCheck)
			} else {
				say("Required %s on %s", DumpCheck, def)
				sum.Lines = append(sum.Lines, "merging into "+def+" requires "+DumpCheck)
			}
			sum.Lines = append(sum.Lines, "https://github.com/"+repo)
			return sum, nil
		},
	}, nil
}

// requireDumpCheck adds the ruleset the fleet's repositories have: the dump
// check required on the default branch.
func (a *Actions) requireDumpCheck(ctx context.Context, gh, dir, repo string) error {
	body, err := json.Marshal(map[string]any{
		"name": "Required checks", "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []string{"~DEFAULT_BRANCH"}, "exclude": []string{}}},
		"rules": []any{map[string]any{"type": "required_status_checks", "parameters": map[string]any{
			"strict_required_status_checks_policy": false,
			"required_status_checks":               []any{map[string]any{"context": DumpCheck}},
		}}},
	})
	if err != nil {
		return err
	}
	res, err := a.Exec.Run(ctx, exec.Spec{Name: gh, Dir: dir, Args: []string{"api", "-X", "POST", "repos/" + repo + "/rulesets", "--input", "-"},
		Stdin: strings.NewReader(string(body))})
	if err != nil {
		return err
	}
	if res.Code != 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if msg == "" {
			msg = strings.TrimSpace(string(res.Stdout))
		}
		return errors.New(firstLine(msg))
	}
	return nil
}
