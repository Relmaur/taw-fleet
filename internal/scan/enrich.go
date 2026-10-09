package scan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/Relmaur/taw-fleet/internal/composer"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/git"
	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/taw"
	"github.com/Relmaur/taw-fleet/internal/vite"
)

// GitEnricher reads the theme's repository state.
type GitEnricher struct{ Runner exec.Runner }

// Name implements Enricher.
func (GitEnricher) Name() string { return "git" }

// Enrich implements Enricher.
func (g GitEnricher) Enrich(ctx context.Context, _ *site.Site, t *site.Theme) error {
	info, err := git.Info(ctx, g.Runner, t.RealPath)
	if err != nil {
		return err
	}
	t.Git = info
	if info != nil {
		t.Version = info.Describe
	}
	return nil
}

// CoreEnricher reads the theme's installed and locked taw/core. A missing
// vendor/ or lock is not an error here; the doctor reports it.
type CoreEnricher struct{}

// Name implements Enricher.
func (CoreEnricher) Name() string { return "core" }

// Enrich implements Enricher.
func (CoreEnricher) Enrich(_ context.Context, _ *site.Site, t *site.Theme) error {
	switch t.Kind {
	case site.KindClassic:
		t.Scaffold.Name = "taw-theme"
	case site.KindGutenberg:
		t.Scaffold.Name = "taw-gutenberg"
	}
	installed, err := composer.InstalledVersion(t.RealPath, composer.CorePackage)
	if err != nil && !errors.Is(err, composer.ErrNotInstalled) {
		t.Core.Err = err.Error()
		return err
	}
	locked, err := composer.LockedVersion(t.RealPath, composer.CorePackage)
	if err != nil && !errors.Is(err, composer.ErrNotInstalled) {
		t.Core.Err = err.Error()
		return err
	}
	t.Core.Installed, t.Core.Locked = installed, locked
	t.Core.LockMismatch = installed != "" && locked != "" && !composer.SameVersion(installed, locked)
	return nil
}

// Keys of Report.Latest.
const (
	LatestCore      = "taw/core"
	LatestTheme     = "taw-theme"
	LatestGutenberg = "taw-gutenberg"
	LatestFleet     = "taw-fleet" // this tool: the dashboard says when a newer release is out
)

// Lookup answers once per scan (not per theme), e.g. the newest releases.
type Lookup interface {
	Name() string
	Lookup(ctx context.Context) (map[string]string, []error)
}

// GitHubLookup finds the newest taw-core, taw-theme, taw-gutenberg and
// taw-fleet tags.
type GitHubLookup struct{ Client *github.Client }

// Name implements Lookup.
func (GitHubLookup) Name() string { return "github" }

// Lookup implements Lookup.
func (l GitHubLookup) Lookup(ctx context.Context) (map[string]string, []error) {
	repos := map[string]string{LatestCore: "taw-core", LatestTheme: "taw-theme", LatestGutenberg: "taw-gutenberg", LatestFleet: "taw-fleet"}
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		out    = map[string]string{}
		failed []string
		first  error
	)
	for key, repo := range repos {
		wg.Go(func() {
			latest, err := l.Client.LatestTag(ctx, "Relmaur", repo)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, repo)
				if first == nil || errors.Is(first, github.ErrOffline) {
					first = err
				}
				return
			}
			out[key] = latest.Tag
		})
	}
	wg.Wait()
	if len(failed) == 0 {
		return out, nil
	}
	sort.Strings(failed)
	// One error for the whole lookup: which repos, and the first reason.
	return out, []error{fmt.Errorf("newest version unknown for %s: %w", strings.Join(failed, ", "), first)}
}

// applyLatest fills Core.Latest/Behind and Scaffold.Latest from the lookups.
func applyLatest(sites []site.Site, latest map[string]string) {
	for si := range sites {
		for ti := range sites[si].Themes {
			t := &sites[si].Themes[ti]
			if !t.IsTAW {
				continue
			}
			t.Core.Latest = latest[LatestCore]
			t.Core.Behind = composer.Older(t.Core.Installed, t.Core.Latest)
			if t.Scaffold.Name != "" {
				t.Scaffold.Latest = latest[t.Scaffold.Name]
			}
		}
	}
}

// DriftEnricher loads each theme's last `bin/taw sync` result from the cache.
type DriftEnricher struct{ CacheDir string }

// Name implements Enricher.
func (DriftEnricher) Name() string { return "drift" }

// Enrich implements Enricher.
func (d DriftEnricher) Enrich(_ context.Context, s *site.Site, t *site.Theme) error {
	t.Drift = taw.LoadDrift(d.CacheDir, s.Slug, t.Dir)
	return nil
}

// DevEnricher finds the theme's running Vite dev server.
type DevEnricher struct{}

// Name implements Enricher.
func (DevEnricher) Name() string { return "dev" }

// Enrich implements Enricher.
func (DevEnricher) Enrich(ctx context.Context, _ *site.Site, t *site.Theme) error {
	t.Dev = vite.Running(ctx, t.RealPath)
	return nil
}

// Accounts marks themes that belong to another GitHub account: the
// site's github_account in the config, a remote through an SSH host alias
// (github.com-parallel, the usual way to use a second account), or an
// owner that isn't one of Mine.
type Accounts struct {
	Mine  []string          // the owner's accounts and organizations; empty = unknown
	Sites map[string]string // site folder → github_account
}

// Apply marks every theme. It runs after the git enricher (Scanner.After).
func (a Accounts) Apply(sites []site.Site) {
	for i := range sites {
		for j := range sites[i].Themes {
			a.mark(&sites[i], &sites[i].Themes[j])
		}
	}
}

func (a Accounts) mark(s *site.Site, t *site.Theme) {
	acct := a.Sites[s.Slug]
	if acct == "" && t.Git != nil && t.Git.Repo != nil {
		r := t.Git.Repo
		if r.Alias != "" || (len(a.Mine) > 0 && !slices.ContainsFunc(a.Mine, func(m string) bool { return strings.EqualFold(m, r.Owner) })) {
			acct = r.Owner
		}
	}
	if acct != "" && slices.ContainsFunc(a.Mine, func(m string) bool { return strings.EqualFold(m, acct) }) {
		acct = ""
	}
	t.Account = acct
}
