package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Relmaur/taw-fleet/internal/live"
	"github.com/Relmaur/taw-fleet/internal/site"
)

type liveDoneMsg struct {
	results map[string]site.Production
	err     error
	fresh   bool
}

// liveDue reports whether the production view should refresh on its own:
// first after the first scan, then when the cache would have expired.
func (m Model) liveDue() bool {
	return m.deps.Live != nil && m.loaded && !m.liveFetching &&
		(m.liveAt.IsZero() || m.deps.Now().Sub(m.liveAt) >= live.CacheTTL)
}

// fetchLive checks the production sites in the background (L: fresh).
func (m Model) fetchLive(fresh bool) (tea.Model, tea.Cmd) {
	if m.liveFetching {
		return m, nil
	}
	m.liveFetching = true
	f, ctx := m.deps.Live, m.ctx
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		res, err := f(ctx, fresh)
		return liveDoneMsg{results: res, err: err, fresh: fresh}
	})
}

// liveSummary is the footer line after L.
func liveSummary(res map[string]site.Production) string {
	if len(res) == 0 {
		return "no production sites in the config"
	}
	var ok, unverified int
	var bad []string
	for slug, p := range res {
		switch {
		case !p.Reachable:
			bad = append(bad, slug)
		case !p.Verified:
			unverified++
		default:
			ok++
		}
	}
	sort.Strings(bad)
	parts := []string{fmt.Sprintf("%d of %d production sites verified", ok, len(res))}
	if unverified > 0 {
		parts = append(parts, fmt.Sprintf("%d unverified", unverified))
	}
	if len(bad) > 0 {
		parts = append(parts, "no answer from "+strings.Join(bad, ", "))
	}
	return strings.Join(parts, " · ")
}
