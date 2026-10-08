package github

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
)

// TokenSource finds a GitHub token once, lazily: $GITHUB_TOKEN, $GH_TOKEN, or
// `gh auth token` when the gh CLI is installed and signed in. No token is
// fine; the cache keeps taw-fleet inside the anonymous limit. The token is
// only ever sent to the GitHub API.
func TokenSource(p paths.Paths, r exec.Runner) func() string {
	var once sync.Once
	var tok string
	return func() string {
		once.Do(func() { tok = findToken(p, r) })
		return tok
	}
}

func findToken(p paths.Paths, r exec.Runner) string {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(p.Getenv(k)); v != "" {
			return v
		}
	}
	gh, err := p.LookPath("gh")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := r.Run(ctx, exec.Spec{Name: gh, Args: []string{"auth", "token", "--hostname", "github.com"}})
	if err != nil || res.Code != 0 {
		return ""
	}
	return strings.TrimSpace(string(res.Stdout))
}
