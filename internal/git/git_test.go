package git

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// These tests build real repositories in temp dirs with the real git (every
// Mac with the Command Line Tools, and every CI runner, has it). No network.

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := osexec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commit(t *testing.T, dir, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", file)
	gitCmd(t, dir, "commit", "-q", "-m", file)
}

// setup: a bare "origin" with main + a tag, and a clone of it.
func setup(t *testing.T) (origin, clone string) {
	t.Helper()
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	origin = filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	clone = filepath.Join(root, "theme")
	gitCmd(t, root, "init", "-q", "--bare", "-b", "main", origin)
	gitCmd(t, root, "init", "-q", "-b", "main", seed)
	commit(t, seed, "a.txt")
	gitCmd(t, seed, "tag", "v1.0.0")
	commit(t, seed, "b.txt")
	gitCmd(t, seed, "remote", "add", "origin", origin)
	gitCmd(t, seed, "push", "-q", "origin", "main", "--tags")
	gitCmd(t, root, "clone", "-q", origin, clone)
	return origin, clone
}

func info(t *testing.T, dir string) *site.GitInfo {
	t.Helper()
	g, err := Info(context.Background(), exec.OSRunner{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestInfoCleanClone(t *testing.T) {
	_, clone := setup(t)
	g := info(t, clone)
	if g == nil {
		t.Fatal("nil info")
	}
	if g.Branch != "main" || g.DefaultBranch != "main" || g.Upstream != "origin/main" {
		t.Errorf("branches: %+v", g)
	}
	if g.Ahead != 0 || g.Behind != 0 || g.Dirty != 0 || g.Detached {
		t.Errorf("state: %+v", g)
	}
	if g.LastTag != "v1.0.0" || g.Describe == "" || g.LastCommit.IsZero() {
		t.Errorf("tags: %+v", g)
	}
	if g.RemoteURL == "" || g.Repo != nil {
		t.Errorf("a local path remote has no Repo: %+v", g)
	}
}

func TestInfoDirtyAheadBehind(t *testing.T) {
	origin, clone := setup(t)
	// Someone else pushes one commit; we make one locally and leave changes.
	other := filepath.Join(t.TempDir(), "other")
	gitCmd(t, filepath.Dir(other), "clone", "-q", origin, other)
	commit(t, other, "theirs.txt")
	gitCmd(t, other, "push", "-q", "origin", "main")
	gitCmd(t, clone, "fetch", "-q")
	commit(t, clone, "mine.txt")
	if err := os.WriteFile(filepath.Join(clone, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "new file.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	g := info(t, clone)
	if g.Ahead != 1 || g.Behind != 1 || g.Dirty != 2 {
		t.Errorf("ahead/behind/dirty = %d/%d/%d", g.Ahead, g.Behind, g.Dirty)
	}
}

func TestInfoBranchWithoutUpstream(t *testing.T) {
	_, clone := setup(t)
	gitCmd(t, clone, "switch", "-q", "-c", "chore/update")
	g := info(t, clone)
	if g.Branch != "chore/update" || g.HasUpstream() || g.DefaultBranch != "main" {
		t.Errorf("%+v", g)
	}
}

func TestInfoDetached(t *testing.T) {
	_, clone := setup(t)
	gitCmd(t, clone, "checkout", "-q", "v1.0.0")
	if g := info(t, clone); !g.Detached || g.Branch != "" {
		t.Errorf("%+v", g)
	}
}

func TestInfoNotARepo(t *testing.T) {
	if g := info(t, t.TempDir()); g != nil {
		t.Errorf("want nil, got %+v", g)
	}
}

func TestInfoSubfolderOfAnotherRepo(t *testing.T) {
	_, clone := setup(t)
	sub := filepath.Join(clone, "themes", "child")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if g := info(t, sub); g != nil {
		t.Errorf("a folder inside another repo must not get that repo's state: %+v", g)
	}
}

func TestInfoThroughSymlink(t *testing.T) {
	_, clone := setup(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(clone, link); err != nil {
		t.Fatal(err)
	}
	if g := info(t, link); g == nil || g.Branch != "main" {
		t.Errorf("symlinked repo: %+v", g)
	}
}

func TestInfoUsesArgvAndNoLocks(t *testing.T) {
	f := &exec.FakeRunner{Script: func(exec.Spec) (exec.Result, error) {
		return exec.Result{Code: 128}, nil // "not a git repository"
	}}
	g, err := Info(context.Background(), f, "/somewhere")
	if err != nil || g != nil {
		t.Fatalf("g=%v err=%v", g, err)
	}
	c := f.Calls()[0]
	if c.Name != "git" || c.Dir != "/somewhere" || c.Args[len(c.Args)-1] != "--show-toplevel" {
		t.Errorf("call = %+v", c)
	}
	found := false
	for _, e := range c.Env {
		found = found || e == "GIT_OPTIONAL_LOCKS=0"
	}
	if !found {
		t.Error("GIT_OPTIONAL_LOCKS=0 missing")
	}
}

func TestParseCountsAndLines(t *testing.T) {
	if a, b := parseCounts("3\t7"); a != 3 || b != 7 {
		t.Errorf("%d %d", a, b)
	}
	if a, b := parseCounts("junk"); a != 0 || b != 0 {
		t.Errorf("%d %d", a, b)
	}
	if countLines("") != 0 || countLines(" M a\n?? b\n") != 2 {
		t.Error("countLines")
	}
}

func TestParseRemote(t *testing.T) {
	ok := map[string]site.Repo{
		"https://github.com/Relmaur/taw-core.git":                        {Host: "github.com", Owner: "Relmaur", Name: "taw-core"},
		"https://github.com/Relmaur/taw-core":                            {Host: "github.com", Owner: "Relmaur", Name: "taw-core"},
		"git@github.com:Relmaur/chcapital--theme.git":                    {Host: "github.com", Owner: "Relmaur", Name: "chcapital--theme"},
		"git@github.com-parallel:parallelplus/parallel-plus-website.git": {Host: "github.com", Owner: "parallelplus", Name: "parallel-plus-website", Alias: "github.com-parallel"},
		"ssh://git@github.com:22/Relmaur/taw-fleet.git":                  {Host: "github.com", Owner: "Relmaur", Name: "taw-fleet"},
		"https://user:tok@gitlab.com/acme/site.git":                      {Host: "gitlab.com", Owner: "acme", Name: "site"},
		"git@bitbucket.org:acme/theme.git":                               {Host: "bitbucket.org", Owner: "acme", Name: "theme"},
		"https://git.example.com/acme/theme/":                            {Host: "git.example.com", Owner: "acme", Name: "theme"},
	}
	for in, want := range ok {
		got, err := ParseRemote(in)
		if err != nil || *got != want {
			t.Errorf("ParseRemote(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "/local/path/origin.git", "https://github.com/onlyowner", "git@github.com:a/b/c.git"} {
		if _, err := ParseRemote(bad); err == nil {
			t.Errorf("ParseRemote(%q) should fail", bad)
		}
	}
	r := site.Repo{Host: "github.com", Owner: "Relmaur", Name: "taw-fleet"}
	if r.WebURL() != "https://github.com/Relmaur/taw-fleet" || r.FullName() != "Relmaur/taw-fleet" {
		t.Error("WebURL/FullName")
	}
}

func TestDeliveryRemote(t *testing.T) {
	dir := t.TempDir()
	if got := DeliveryRemote(dir); got != "origin" {
		t.Errorf("no taw.json: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "taw.json"), []byte(`{"update": {"remote": "origin-agency"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DeliveryRemote(dir); got != "origin-agency" {
		t.Errorf("taw.json: %q", got)
	}
}
