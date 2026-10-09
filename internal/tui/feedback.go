package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Relmaur/taw-fleet/internal/bugsmash"
	"github.com/Relmaur/taw-fleet/internal/site"
)

type feedbackDoneMsg struct {
	results map[string]site.Feedback
	err     error
	fresh   bool
}

// feedbackDue reports whether the BugSmash comments should refresh on their
// own: first after the first scan, then when the cache would have expired.
func (m Model) feedbackDue() bool {
	return m.deps.Feedback != nil && m.loaded && !m.feedbackFetching &&
		(m.feedbackAt.IsZero() || m.deps.Now().Sub(m.feedbackAt) >= bugsmash.CacheTTL)
}

// fetchFeedback reads the open comments in the background (fresh skips the
// cache).
func (m Model) fetchFeedback(fresh bool) (tea.Model, tea.Cmd) {
	if m.feedbackFetching || m.deps.Feedback == nil {
		return m, nil
	}
	m.feedbackFetching = true
	f, ctx := m.deps.Feedback, m.ctx
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		res, err := f(ctx, fresh)
		return feedbackDoneMsg{results: res, err: err, fresh: fresh}
	})
}

func (m Model) onFeedback(msg feedbackDoneMsg) (tea.Model, tea.Cmd) {
	m.feedbackFetching, m.feedbackAt, m.feedbackErr = false, m.deps.Now(), msg.err
	if msg.err == nil {
		m.feedback = msg.results
		bugsmash.Apply(m.rep.Sites, m.feedback)
		m.applyScan(m.rep, nil) // findings include the comments.* rules
		if msg.fresh && !m.full.active {
			m.setFlash(feedbackSummary(msg.results), false)
		}
	} else if msg.fresh {
		m.setFlash("BugSmash: "+msg.err.Error(), true)
	}
	return m, nil
}

// feedbackSummary is the footer line after a fresh check.
func feedbackSummary(res map[string]site.Feedback) string {
	if len(res) == 0 {
		return "no BugSmash projects in the config"
	}
	open := 0
	var sites, bad []string
	for slug, f := range res {
		switch {
		case f.Error != "":
			bad = append(bad, slug)
		case f.Open > 0:
			open += f.Open
			sites = append(sites, fmt.Sprintf("%s %d", slug, f.Open))
		}
	}
	sort.Strings(sites)
	sort.Strings(bad)
	parts := []string{"no open comments in BugSmash"}
	if open > 0 {
		parts = []string{fmt.Sprintf("%d open %s in BugSmash (%s)", open, plural(open, "comment", "comments"), strings.Join(sites, ", "))}
	}
	if len(bad) > 0 {
		parts = append(parts, "BugSmash didn't answer for "+strings.Join(bad, ", "))
	}
	return strings.Join(parts, " · ")
}
