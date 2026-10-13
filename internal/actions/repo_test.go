package actions

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/site"
)

func TestRepoName(t *testing.T) {
	for dir, want := range map[string]string{"acme-bakery": "acme-bakery--theme", "fsspx--theme": "fsspx--theme", "ls-mexico": "ls-mexico--theme"} {
		if got := RepoName(dir); got != want {
			t.Errorf("RepoName(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestRepoTask(t *testing.T) {
	a, f := setup(t, config.Config{GitHubOwners: []string{"Relmaur", "EmeLambda"}})
	a.Paths.LookPath = func(n string) (string, error) { return "/opt/homebrew/bin/" + n, nil }
	var ruleset string
	f.Script = func(s exec.Spec) (exec.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case strings.HasPrefix(args, "repo view"):
			return exec.Result{Stdout: []byte("Relmaur/acme-bakery--theme\n")}, nil
		case strings.HasPrefix(args, "api -X POST"):
			b, _ := io.ReadAll(s.Stdin)
			ruleset = string(b)
		}
		return exec.Result{}, nil
	}
	s := site.Site{ID: "n1", Slug: "acme-bakery", Name: "Acme Bakery"}
	th := site.Theme{Dir: "acme-bakery", RealPath: "/S/acme-bakery/app/public/wp-content/themes/acme-bakery", IsTAW: true,
		Git: &site.GitInfo{Branch: "build/site"}}
	task, err := a.RepoTask(s, th, a.RepoOwner(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !task.Writes || !strings.Contains(task.Ask, "private repository acme-bakery--theme on Relmaur and push main and build/site") ||
		!strings.Contains(task.Ask, "ci / committed-files") {
		t.Errorf("ask %q", task.Ask)
	}
	var out bytes.Buffer
	sum, err := task.Run(context.Background(), &out)
	if err != nil {
		t.Fatal(err)
	}
	var cmds []string
	for _, c := range f.Calls() {
		cmds = append(cmds, strings.TrimPrefix(c.Name, "/opt/homebrew/bin/")+" "+strings.Join(c.Args, " "))
	}
	want := []string{
		"gh repo create Relmaur/acme-bakery--theme --private --source /S/acme-bakery/app/public/wp-content/themes/acme-bakery --remote origin --description Acme Bakery (TAW theme)",
		"gh repo view --json nameWithOwner --jq .nameWithOwner",
		"git rev-parse --verify --quiet refs/heads/main",
		"git push -u origin main",
		"git rev-parse --verify --quiet refs/heads/build/site",
		"git push -u origin build/site",
		"git remote set-head origin --auto",
		"gh api -X POST repos/Relmaur/acme-bakery--theme/rulesets --input -",
	}
	if strings.Join(cmds, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(cmds, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(ruleset, `"context":"ci / committed-files"`) || !strings.Contains(ruleset, "~DEFAULT_BRANCH") {
		t.Errorf("ruleset %s", ruleset)
	}
	if sum.Headline != "acme-bakery is on GitHub: Relmaur/acme-bakery--theme" || !strings.Contains(strings.Join(sum.Lines, "\n"), "requires ci / committed-files") {
		t.Errorf("summary %+v", sum)
	}

	// Refusals.
	for _, g := range []*site.GitInfo{nil, {Branch: "main", Repo: &site.Repo{Owner: "Relmaur", Name: "x"}}, {Detached: true}} {
		th.Git = g
		if _, err := a.RepoTask(s, th, "", ""); err == nil {
			t.Errorf("no refusal for %+v", g)
		}
	}
}

func TestRepoTaskRulesetRefused(t *testing.T) {
	a, f := setup(t, config.Config{})
	a.Paths.LookPath = func(n string) (string, error) { return "/bin/" + n, nil }
	f.Script = func(s exec.Spec) (exec.Result, error) {
		if strings.Contains(strings.Join(s.Args, " "), "rulesets") {
			return exec.Result{Code: 1, Stderr: []byte("Upgrade to GitHub Pro or make this repository public to enable this feature. (HTTP 403)")}, nil
		}
		if strings.HasPrefix(strings.Join(s.Args, " "), "repo view") {
			return exec.Result{Stdout: []byte("me/x--theme")}, nil
		}
		return exec.Result{}, nil
	}
	task, err := a.RepoTask(site.Site{Slug: "x"}, site.Theme{Dir: "x", RealPath: "/t/x", Git: &site.GitInfo{Branch: "main"}}, "", "")
	if err != nil || !strings.Contains(task.Ask, "on your GitHub account") {
		t.Fatalf("%v %q", err, task.Ask)
	}
	sum, err := task.Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(sum.Lines, "\n"), "the dump check isn't required yet (Upgrade to GitHub Pro") {
		t.Errorf("lines %q", sum.Lines)
	}
	if calls := f.Calls(); !strings.HasPrefix(strings.Join(calls[0].Args, " "), "repo create x--theme --private") {
		t.Errorf("owner: %v", calls[0].Args)
	}
}
