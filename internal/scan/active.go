package scan

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// ActiveTheme asks WordPress which theme is active (`wp option get
// stylesheet`) on running sites. Booting WordPress costs about a second, so
// answers are cached for TTL; the dashboard re-scans every minute.
type ActiveTheme struct {
	Paths  paths.Paths
	Runner exec.Runner
	TTL    time.Duration
	Now    func() time.Time

	mu    sync.Mutex
	cache map[string]activeEntry
}

type activeEntry struct {
	theme string
	at    time.Time
}

// Name implements SiteEnricher.
func (*ActiveTheme) Name() string { return "active-theme" }

// EnrichSite implements SiteEnricher. A halted site is skipped (no error).
func (a *ActiveTheme) EnrichSite(ctx context.Context, s *site.Site) error {
	if !s.SockLive {
		return nil
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	ttl := a.TTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	a.mu.Lock()
	if e, ok := a.cache[s.ID]; ok && now().Sub(e.at) < ttl {
		a.mu.Unlock()
		s.ActiveTheme = e.theme
		return nil
	}
	a.mu.Unlock()

	spec, err := local.WPSpec(a.Paths, *s, []string{"option", "get", "stylesheet", "--skip-plugins", "--skip-themes"})
	if err != nil {
		return err
	}
	res, err := a.Runner.Run(ctx, spec)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return nil // WordPress not answering isn't worth a warning in every scan
	}
	// The value is the last line: anything before it is noise a PHP build
	// printed despite display_errors=stderr.
	lines := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
	theme := strings.TrimSpace(lines[len(lines)-1])
	s.ActiveTheme = theme
	a.mu.Lock()
	if a.cache == nil {
		a.cache = map[string]activeEntry{}
	}
	a.cache[s.ID] = activeEntry{theme, now()}
	a.mu.Unlock()
	return nil
}

// Forget drops a site's cached answer (after starting or stopping it).
func (a *ActiveTheme) Forget(id string) {
	a.mu.Lock()
	delete(a.cache, id)
	a.mu.Unlock()
}
