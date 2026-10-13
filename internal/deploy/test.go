package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
)

// Tester connects to the host the way the deploy will, with the deploy key.
type Tester struct {
	Exec exec.Runner
	Dir  string // where the key's file lives for the test (a 0700 folder under it, removed after)
}

// Result is what the test found.
type Result struct {
	KnownHosts string // the server's host keys, to pin (DEPLOY_KNOWN_HOSTS)
	Said       string // what the server answered
}

// Test reads the server's host keys (ssh-keyscan), then logs in with the key
// and checks the theme's folder can be reached and rsync is there (rsync),
// or lists the remote (git). Nothing on the server changes.
func (t Tester) Test(ctx context.Context, target Target, key Key) (Result, error) {
	if err := target.Check(); err != nil {
		return Result{}, err
	}
	host, port, err := target.SSHHost()
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return Result{}, err
	}
	dir, err := os.MkdirTemp(t.Dir, "deploy-test-")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	keyFile, known := filepath.Join(dir, "key"), filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(keyFile, key.Private, 0o600); err != nil {
		return Result{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	scan, err := t.Exec.Run(ctx, exec.Spec{Name: "ssh-keyscan", Args: []string{"-T", "10", "-p", port, host}})
	if err != nil {
		return Result{}, err
	}
	var keys []string
	for _, l := range strings.Split(string(scan.Stdout), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			keys = append(keys, l)
		}
	}
	if len(keys) == 0 {
		return Result{}, fmt.Errorf("%s didn't answer on port %s (ssh-keyscan): check the host and port", host, port)
	}
	res := Result{KnownHosts: strings.Join(keys, "\n")}
	if err := os.WriteFile(known, []byte(res.KnownHosts+"\n"), 0o600); err != nil {
		return res, err
	}
	sshOpts := []string{"-i", keyFile, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + known, "-o", "ConnectTimeout=15"}

	if target.Method == Git {
		cmd := "ssh " + strings.Join(quoteAll(sshOpts), " ")
		out, err := t.Exec.Run(ctx, exec.Spec{Name: "git", Args: []string{"ls-remote", "--heads", target.Remote},
			Env: []string{"GIT_SSH_COMMAND=" + cmd}})
		if err != nil {
			return res, err
		}
		if out.Code != 0 {
			return res, fmt.Errorf("the git remote refused the key: %s (is the public key added at the host?)", firstLine(string(out.Stderr)))
		}
		res.Said = "the remote answered"
		return res, nil
	}

	// The theme's folder may not exist yet on a new site: its parent must.
	parent := path.Dir(strings.TrimSuffix(target.Path, "/"))
	remote := "test -d " + shq(parent) + " && command -v rsync"
	args := append(append([]string{}, sshOpts...), "-p", port, target.User+"@"+host, remote)
	out, err := t.Exec.Run(ctx, exec.Spec{Name: "ssh", Args: args})
	if err != nil {
		return res, err
	}
	switch {
	case out.Code == 255:
		return res, fmt.Errorf("SSH refused the key: %s (is the public key added at the host, for user %s?)", firstLine(string(out.Stderr)), target.User)
	case out.Code != 0 && strings.TrimSpace(string(out.Stdout)) == "":
		return res, fmt.Errorf("logged in, but %s isn't there, or rsync isn't installed on the server", parent)
	case out.Code != 0:
		return res, errors.New(firstLine(string(out.Stderr)))
	}
	res.Said = "logged in; " + parent + " is there; rsync at " + strings.TrimSpace(string(out.Stdout))
	return res, nil
}

// shq single-quotes a word for the remote shell.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func quoteAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = shq(x)
	}
	return out
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return "no answer"
}
