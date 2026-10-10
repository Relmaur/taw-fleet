package actions

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/exec"
)

func TestSiteBranchTask(t *testing.T) {
	s, th := fixture()
	g := *th.Git
	g.Branch, g.DefaultBranch, g.Upstream, g.ForeignUpstream, g.Dirty = "main", "master", "upstream/main", "Relmaur/taw-theme", 0
	th.Git = &g

	a, _ := setup(t, config.Config{})
	for _, c := range []struct {
		mutate func()
		want   string
	}{
		{func() { th.Git.Dirty = 2 }, "2 uncommitted changes: commit or stash first"},
		{func() { th.Git.Branch = "master" }, "already on master"},
		{func() { th.Git.DefaultBranch = "" }, "default branch isn't known"},
	} {
		saved := *th.Git
		c.mutate()
		if _, err := a.SiteBranchTask(s, th); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %q, got %v", c.want, err)
		}
		*th.Git = saved
	}

	// A local copy of the scaffold's branch, nothing of its own: deleted.
	a, f := setup(t, config.Config{})
	f.Script = func(sp exec.Spec) (exec.Result, error) {
		if sp.Args[0] == "rev-list" {
			return exec.Result{Stdout: []byte("0\n")}, nil
		}
		return exec.Result{}, nil
	}
	task, err := a.SiteBranchTask(s, th)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task.Ask, "Switch "+th.Dir+" to master? Then delete the local branch main (it tracks Relmaur/taw-theme)") || !task.Writes {
		t.Errorf("Ask = %q", task.Ask)
	}
	sum, err := task.Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var git []string
	for _, c := range f.Calls() {
		git = append(git, strings.Join(c.Args, " "))
	}
	if strings.Join(git, " | ") != "switch master | rev-list --count main@{upstream}..main | branch -D main" {
		t.Errorf("git = %v", git)
	}
	if sum.Headline != th.Dir+": on master again" || !strings.Contains(strings.Join(sum.Lines, "\n"), "deleted the local branch main") {
		t.Errorf("summary = %q %v", sum.Headline, sum.Lines)
	}

	// Commits of its own: kept.
	a, f = setup(t, config.Config{})
	f.Script = func(sp exec.Spec) (exec.Result, error) {
		if sp.Args[0] == "rev-list" {
			return exec.Result{Stdout: []byte("3\n")}, nil
		}
		return exec.Result{}, nil
	}
	task, _ = a.SiteBranchTask(s, th)
	sum, _ = task.Run(context.Background(), io.Discard)
	if len(f.Calls()) != 2 || !strings.Contains(strings.Join(sum.Lines, "\n"), "is kept: it has commits of its own") {
		t.Errorf("calls %d, lines %v", len(f.Calls()), sum.Lines)
	}

	// Off the default branch on the site's own repository: switch only.
	th.Git.ForeignUpstream, th.Git.Branch, th.Git.Upstream = "", "staging", "origin/staging"
	a, f = setup(t, config.Config{})
	task, _ = a.SiteBranchTask(s, th)
	sum, _ = task.Run(context.Background(), io.Discard)
	if len(f.Calls()) != 1 || strings.Contains(task.Ask, "delete") || !strings.Contains(strings.Join(sum.Lines, "\n"), "the branch staging is kept") {
		t.Errorf("staging: ask %q, calls %d, lines %v", task.Ask, len(f.Calls()), sum.Lines)
	}
}
