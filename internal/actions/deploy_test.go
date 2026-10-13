package actions

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/deploy"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/site"
)

func TestDeployTask(t *testing.T) {
	a, f := setup(t, config.Config{})
	a.Paths.LookPath = func(n string) (string, error) { return "/opt/homebrew/bin/" + n, nil }
	theme := t.TempDir()
	stub := "name: Deploy\n# taw/core's stub\n"
	if err := os.MkdirAll(filepath.Join(theme, filepath.Dir(DeployStub)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(theme, DeployStub), []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}
	key, _ := deploy.NewKey("taw-fleet deploy acme")
	keychainValue := ""
	stdin := map[string]string{}
	var cmds []string
	f.Script = func(s exec.Spec) (exec.Result, error) {
		line := strings.TrimPrefix(s.Name, "/opt/homebrew/bin/") + " " + strings.Join(s.Args, " ")
		if s.Name == "/usr/bin/security" {
			switch {
			case len(s.Args) > 0 && s.Args[0] == "-i":
				b, _ := io.ReadAll(s.Stdin)
				_, after, _ := strings.Cut(string(b), ` -w "`)
				keychainValue = strings.TrimSuffix(strings.TrimSpace(after), `"`)
			case s.Args[0] == "find-generic-password":
				return exec.Result{Stdout: []byte(keychainValue)}, nil
			case s.Args[0] == "delete-generic-password":
				keychainValue = ""
				cmds = append(cmds, "security delete")
			}
			return exec.Result{}, nil
		}
		cmds = append(cmds, line)
		if s.Stdin != nil {
			b, _ := io.ReadAll(s.Stdin)
			stdin[strings.Join(s.Args[:min(3, len(s.Args))], " ")] = string(b)
		}
		switch {
		case s.Name == "ssh-keyscan":
			return exec.Result{Stdout: []byte("[1.2.3.4]:40123 ssh-ed25519 AAAAC3Nza\n")}, nil
		case s.Name == "ssh":
			return exec.Result{Stdout: []byte("/usr/bin/rsync\n")}, nil
		case strings.HasPrefix(line, "gh pr create"):
			return exec.Result{Stdout: []byte("https://github.com/Relmaur/acme--theme/pull/1\n")}, nil
		}
		return exec.Result{}, nil
	}
	if err := a.DeployKeys().Save(context.Background(), "acme", key); err != nil {
		t.Fatal(err)
	}
	s := site.Site{ID: "a1", Slug: "acme"}
	th := site.Theme{Dir: "acme", RealPath: theme, Git: &site.GitInfo{Branch: "build/site", DefaultBranch: "main",
		Repo: &site.Repo{Owner: "Relmaur", Name: "acme--theme"}}}
	target := deploy.Target{Method: deploy.Rsync, Host: "1.2.3.4", Port: "40123", User: "acme", Path: "/www/acme/public/wp-content/themes/acme", URL: "https://acme.mx"}
	task, err := a.DeployTask(s, th, target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task.Ask, "to acme@1.2.3.4:/www/acme/public/wp-content/themes/acme?") || !strings.Contains(task.Ask, "production_url = https://acme.mx") {
		t.Errorf("ask %q", task.Ask)
	}
	var out bytes.Buffer
	sum, err := task.Run(context.Background(), &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	all := strings.Join(cmds, "\n")
	for _, want := range []string{
		"gh variable set DEPLOY_HOST --repo Relmaur/acme--theme",
		"gh variable set DEPLOY_KNOWN_HOSTS --repo Relmaur/acme--theme",
		"gh secret set DEPLOY_SSH_KEY --repo Relmaur/acme--theme",
		"security delete",
		"git switch -c deploy/setup main",
		"git add .github/workflows/deploy.yml",
		"git push -u origin deploy/setup",
		"gh pr create --base main --head deploy/setup --title Deploy to acme.mx",
		"git switch build/site",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("didn't run %q:\n%s", want, all)
		}
	}
	// Values go on stdin, never argv.
	if strings.Contains(all, "PRIVATE KEY") || strings.Contains(all, "AAAAC3Nza") {
		t.Errorf("a value on a command line:\n%s", all)
	}
	if !strings.Contains(stdin["secret set DEPLOY_SSH_KEY"], "OPENSSH PRIVATE KEY") || stdin["variable set DEPLOY_KNOWN_HOSTS"] != "[1.2.3.4]:40123 ssh-ed25519 AAAAC3Nza" ||
		stdin["variable set DEPLOY_PATH"] != "/www/acme/public/wp-content/themes/acme" {
		t.Errorf("stdin %v", stdin)
	}
	if b, _ := os.ReadFile(filepath.Join(theme, ".github/workflows/deploy.yml")); string(b) != stub {
		t.Errorf("workflow %q", b)
	}
	c, err := config.Load(a.Paths)
	if err != nil || c.Site("acme").ProductionURL != "https://acme.mx" {
		t.Errorf("settings: %v %+v", err, c.Sites)
	}
	if !strings.Contains(strings.Join(sum.Lines, "\n"), "pull request: https://github.com/Relmaur/acme--theme/pull/1") {
		t.Errorf("summary %+v", sum)
	}

	// Refusals: its own deploy workflow, not on GitHub, taw/core too old.
	if _, err := a.DeployTask(s, th, target); err == nil || !strings.Contains(err.Error(), "keeps its own") {
		t.Errorf("own workflow: %v", err)
	}
	if _, err := a.DeployTask(s, site.Theme{Dir: "x", RealPath: t.TempDir(), Git: &site.GitInfo{Branch: "main"}}, target); err == nil {
		t.Error("no repository")
	}
	old := site.Theme{Dir: "x", RealPath: t.TempDir(), Git: th.Git}
	if _, err := a.DeployTask(s, old, target); err == nil || !strings.Contains(err.Error(), "1.97+") {
		t.Errorf("old taw/core: %v", err)
	}
}
