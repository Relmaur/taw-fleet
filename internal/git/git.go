// Package git reads a theme repository's state through exec.Runner. It only
// reads: no fetch, no pull, nothing that touches the network or the index.
package git

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Info returns the state of the repository whose top level is dir. It
// returns nil, nil when dir isn't the top of its own repository (for example
// a theme folder inside a site that is itself a repo), so a theme is never
// credited with another repo's state.
func Info(ctx context.Context, r exec.Runner, dir string) (*site.GitInfo, error) {
	g := gitRunner{ctx: ctx, r: r, dir: dir}

	top, ok, err := g.out("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	if !ok || !samePath(top, dir) {
		return nil, nil
	}

	info := &site.GitInfo{}
	if branch, ok, err := g.out("rev-parse", "--abbrev-ref", "HEAD"); err != nil {
		return nil, err
	} else if ok {
		info.Branch = branch
		if branch == "HEAD" {
			info.Detached = true
			info.Branch = ""
		}
	}
	if head, ok, err := g.out("symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err != nil {
		return nil, err
	} else if ok {
		info.DefaultBranch = strings.TrimPrefix(head, "origin/")
	}
	if up, ok, err := g.out("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err != nil {
		return nil, err
	} else if ok && up != "" {
		info.Upstream = up
		if counts, ok, err := g.out("rev-list", "--left-right", "--count", "HEAD...@{upstream}"); err != nil {
			return nil, err
		} else if ok {
			info.Ahead, info.Behind = parseCounts(counts)
		}
	}
	if status, ok, err := g.out("status", "--porcelain=v1", "--untracked-files=normal"); err != nil {
		return nil, err
	} else if ok {
		info.Dirty = countLines(status)
	}
	if d, ok, err := g.out("describe", "--tags", "--always"); err != nil {
		return nil, err
	} else if ok {
		info.Describe = d
	}
	if tag, ok, err := g.out("describe", "--tags", "--abbrev=0"); err != nil {
		return nil, err
	} else if ok {
		info.LastTag = tag
	}
	if when, ok, err := g.out("log", "-1", "--format=%cI"); err != nil {
		return nil, err
	} else if ok {
		if t, perr := time.Parse(time.RFC3339, when); perr == nil {
			info.LastCommit = t
		}
	}
	if url, ok, err := g.out("remote", "get-url", "origin"); err != nil {
		return nil, err
	} else if ok {
		info.RemoteURL = url
		if repo, perr := ParseRemote(url); perr == nil {
			info.Repo = repo
		}
	}
	return info, nil
}

type gitRunner struct {
	ctx context.Context
	r   exec.Runner
	dir string
}

// out runs one git command. ok is false when git exits non-zero (no upstream,
// no tags…), which is an answer, not an error; err is for git not running at
// all or the context ending.
func (g gitRunner) out(args ...string) (string, bool, error) {
	res, err := g.r.Run(g.ctx, exec.Spec{
		Dir:  g.dir,
		Name: "git",
		Args: append([]string{"-c", "core.quotepath=off"}, args...),
		// Don't take index.lock for status: an editor may be using the repo.
		Env: []string{"GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"},
	})
	if err != nil {
		return "", false, err
	}
	if res.Code != 0 {
		return "", false, nil
	}
	return strings.TrimRight(string(res.Stdout), "\r\n"), true, nil
}

func parseCounts(s string) (ahead, behind int) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return 0, 0
	}
	ahead, _ = strconv.Atoi(f[0])
	behind, _ = strconv.Atoi(f[1])
	return ahead, behind
}

func countLines(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	return len(strings.Split(strings.TrimRight(s, "\n"), "\n"))
}

// samePath compares two paths after resolving symlinks (macOS /var vs
// /private/var, and the umbrella's symlinked themes).
func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}
