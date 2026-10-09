package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Merger merges pull requests (internal/github's Client).
type Merger interface {
	Merge(ctx context.Context, repo string, pr site.PullRequest) (github.Merged, error)
	DeleteBranch(ctx context.Context, repo, branch string) error
}

// MergeResult is a MergeTask's Summary.Report: what to follow next.
type MergeResult struct {
	Repo       string // owner/name
	SHA        string // the commit on the default branch
	Number     int
	Production string // the production URL, when configured
	Deploys    bool   // the repository has a deploy workflow
}

// MergeTask merges one of the theme's open pull requests, deletes its
// branch, and brings the local theme onto the updated default branch when
// that's safe (a clean tree on the merged branch or the default one).
func (a *Actions) MergeTask(s site.Site, t site.Theme, pr site.PullRequest) (Task, error) {
	st := t.GitHub
	if st == nil {
		return Task{}, errors.New("GitHub hasn't been read yet (L reads it)")
	}
	if a.Merger == nil {
		return Task{}, errors.New("GitHub isn't available here")
	}
	switch {
	case pr.Draft:
		return Task{}, fmt.Errorf("#%d is a draft", pr.Number)
	case pr.Conflicted:
		return Task{}, fmt.Errorf("#%d conflicts with %s: resolve it first", pr.Number, st.Default)
	case pr.Checks == site.ChecksFailing:
		return Task{}, fmt.Errorf("#%d: CI failed", pr.Number)
	case pr.Checks == site.ChecksPending:
		return Task{}, fmt.Errorf("#%d: CI is still running; wait for it", pr.Number)
	}
	prod := a.Config.Site(s.Slug).ProductionURL
	ci := "CI passed"
	if pr.Checks == site.ChecksNone {
		ci = "It has no CI"
	}
	ask := fmt.Sprintf("Merge #%d “%s” into %s? %s.", pr.Number, pr.Title, st.Default, ci)
	switch {
	case st.Deploy != nil && prod != "":
		ask += " This deploys " + prod + " (production)."
	case st.Deploy != nil:
		ask += " This starts the " + st.Deploy.Workflow + " workflow."
	}
	res := MergeResult{Repo: st.Repo, Number: pr.Number, Production: prod, Deploys: st.Deploy != nil}

	return Task{
		Title:  fmt.Sprintf("Merge #%d: %s", pr.Number, t.Dir),
		Writes: true,
		Ask:    ask,
		Quiet:  true,
		Run: func(ctx context.Context, out io.Writer) (Summary, error) {
			say := sayTo(out)
			m, err := a.Merger.Merge(ctx, st.Repo, pr)
			if err != nil {
				return Summary{}, err
			}
			res.SHA = m.SHA
			say("Merged #%d into %s (%s, %s)", pr.Number, st.Default, m.Method, short(m.SHA))
			lines := []string{fmt.Sprintf("merged #%d (%s %s)", pr.Number, m.Method, short(m.SHA))}
			if pr.SameRepo {
				if err := a.Merger.DeleteBranch(ctx, st.Repo, pr.Branch); err != nil {
					lines = append(lines, "branch not deleted: "+err.Error())
				} else {
					say("Deleted the branch %s on GitHub", pr.Branch)
				}
			}
			if note := a.pullDefault(ctx, t, st.Default, pr.Branch, say); note != "" {
				lines = append(lines, note)
			}
			head := fmt.Sprintf("Merged #%d into %s", pr.Number, st.Default)
			switch {
			case res.Deploys && prod != "":
				head += "; deploying " + strings.TrimPrefix(strings.TrimPrefix(prod, "https://"), "http://")
			case res.Deploys:
				head += "; the " + st.Deploy.Workflow + " workflow runs next"
			}
			return Summary{Headline: head, Lines: lines, Report: res}, nil
		},
	}, nil
}

// pullDefault switches the local theme to the default branch and pulls,
// then deletes the merged branch, when the tree is clean and on one of
// those two branches. It says what it left alone and why.
func (a *Actions) pullDefault(ctx context.Context, t site.Theme, def, merged string, say func(string, ...any)) string {
	g := t.Git
	switch {
	case g == nil:
		return ""
	case g.Dirty > 0:
		return fmt.Sprintf("local theme left alone: %d uncommitted change(s); pull %s yourself", g.Dirty, def)
	case g.Branch != def && g.Branch != merged:
		return "local theme left alone: it's on " + g.Branch
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	git := func(args ...string) error {
		res, err := a.Exec.Run(ctx, exec.Spec{Name: "git", Dir: t.RealPath, Args: args})
		if err == nil && res.Code != 0 {
			err = errors.New(strings.TrimSpace(string(res.Stderr)))
		}
		return err
	}
	if g.Branch != def {
		if err := git("switch", def); err != nil {
			return "local theme: git switch " + def + ": " + err.Error()
		}
	}
	if err := git("pull", "--ff-only"); err != nil {
		return "local theme: git pull: " + err.Error()
	}
	say("Pulled %s in %s", def, t.Dir)
	if merged != def && git("branch", "-d", merged) == nil {
		say("Deleted the local branch %s", merged)
	}
	return ""
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
