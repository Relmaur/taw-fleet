package actions

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/site"
)

type fakeMerger struct {
	merged  int
	deleted string
	err     error
}

func (f *fakeMerger) Merge(_ context.Context, _ string, pr site.PullRequest) (github.Merged, error) {
	if f.err != nil {
		return github.Merged{}, f.err
	}
	f.merged = pr.Number
	return github.Merged{SHA: "abcdef123", Method: "merge"}, nil
}

func (f *fakeMerger) DeleteBranch(_ context.Context, _, branch string) error {
	f.deleted = branch
	return nil
}

func mergeFixture() (site.Site, site.Theme, site.PullRequest) {
	s, th := fixture()
	pr := site.PullRequest{Number: 12, Title: "Update taw/core", Branch: "chore/taw-core-1.78.1", Checks: site.ChecksPassing, SameRepo: true}
	th.Git.Branch = pr.Branch
	th.GitHub = &site.RepoState{Repo: "Relmaur/ls-mexico--theme", Default: "main", PRs: []site.PullRequest{pr}, Deploy: &site.Deploy{Workflow: "Deploy"}}
	return s, th, pr
}

func TestMergeTask(t *testing.T) {
	a, f := setup(t, config.Config{Sites: map[string]config.Site{"ls-mxico": {ProductionURL: "https://lsmexico.mx"}}})
	m := &fakeMerger{}
	a.Merger = m
	s, th, pr := mergeFixture()

	task, err := a.MergeTask(s, th, pr)
	if err != nil {
		t.Fatal(err)
	}
	if task.Ask != "Merge #12 “Update taw/core” into main? CI passed. This deploys https://lsmexico.mx (production)." || !task.Writes {
		t.Errorf("Ask = %q", task.Ask)
	}
	sum, err := task.Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if m.merged != 12 || m.deleted != pr.Branch {
		t.Errorf("merged %d, deleted %q", m.merged, m.deleted)
	}
	var git []string
	for _, c := range f.Calls() {
		git = append(git, strings.Join(c.Args, " "))
		if c.Dir != th.RealPath {
			t.Errorf("git ran in %s", c.Dir)
		}
	}
	if strings.Join(git, " | ") != "switch main | pull --ff-only | branch -d chore/taw-core-1.78.1" {
		t.Errorf("local git = %v", git)
	}
	res, _ := sum.Report.(MergeResult)
	if sum.Headline != "Merged #12 into main; deploying lsmexico.mx" || res.SHA != "abcdef123" || !res.Deploys || res.Repo != "Relmaur/ls-mexico--theme" {
		t.Errorf("summary = %q, %+v", sum.Headline, res)
	}
}

func TestMergeTaskRefusesAndLeavesDirtyThemesAlone(t *testing.T) {
	a, f := setup(t, config.Config{})
	a.Merger = &fakeMerger{}
	s, th, pr := mergeFixture()
	for _, c := range []struct {
		mutate func(*site.PullRequest)
		want   string
	}{
		{func(p *site.PullRequest) { p.Draft = true }, "draft"},
		{func(p *site.PullRequest) { p.Conflicted = true }, "conflicts"},
		{func(p *site.PullRequest) { p.Checks = site.ChecksFailing }, "CI failed"},
		{func(p *site.PullRequest) { p.Checks = site.ChecksPending }, "still running"},
	} {
		p := pr
		c.mutate(&p)
		if _, err := a.MergeTask(s, th, p); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %q, got %v", c.want, err)
		}
	}

	th.Git.Dirty = 2
	th.GitHub.Deploy = nil
	task, _ := a.MergeTask(s, th, pr)
	if task.Ask != "Merge #12 “Update taw/core” into main? CI passed." {
		t.Errorf("no deploy workflow: %q", task.Ask)
	}
	sum, _ := task.Run(context.Background(), io.Discard)
	if len(f.Calls()) != 0 || !strings.Contains(strings.Join(sum.Lines, "\n"), "2 uncommitted change(s)") {
		t.Errorf("a dirty theme is left alone: calls %d, lines %v", len(f.Calls()), sum.Lines)
	}

	a.Merger = &fakeMerger{err: errors.New("405 Base branch was modified")}
	task, _ = a.MergeTask(s, th, pr)
	if _, err := task.Run(context.Background(), io.Discard); err == nil {
		t.Error("a refused merge fails the task")
	}
}
