package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// How often the PR and DEPLOY columns refresh on their own: every few
// minutes, and often while a deploy is on its way.
const (
	reposEvery     = 5 * time.Minute
	reposEveryBusy = 15 * time.Second
)

type reposDoneMsg struct {
	states map[string]site.RepoState
	fresh  bool
}

// reposDue reports whether the GitHub view should refresh on its own.
func (m Model) reposDue() bool {
	if m.deps.GitHub == nil || !m.loaded || m.reposFetching {
		return false
	}
	if m.reposAt.IsZero() {
		return true
	}
	every := reposEvery
	if deploying(m.repos) {
		every = reposEveryBusy
	}
	return m.deps.Now().Sub(m.reposAt) >= every
}

// deploying reports whether any deploy is running or waiting for CI.
func deploying(states map[string]site.RepoState) bool {
	for _, st := range states {
		d := st.Deploy
		if d == nil {
			continue
		}
		if d.Running != nil || (d.Deployed != "" && d.Deployed != st.Head && st.HeadCI == site.ChecksPending) {
			return true
		}
	}
	return false
}

// fetchRepos reads the pull requests and deploys in the background.
func (m Model) fetchRepos(fresh bool) (tea.Model, tea.Cmd) {
	if m.reposFetching || m.deps.GitHub == nil {
		return m, nil
	}
	m.reposFetching = true
	f, ctx, names := m.deps.GitHub, m.ctx, github.RepoNames(m.rep.Sites)
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		return reposDoneMsg{states: f(ctx, names), fresh: fresh}
	})
}

// onRepos installs a GitHub answer.
func (m Model) onRepos(msg reposDoneMsg) (tea.Model, tea.Cmd) {
	m.reposFetching, m.reposAt, m.repos = false, m.deps.Now(), msg.states
	github.ApplyRepos(m.rep.Sites, m.repos)
	m.applyScan(m.rep, nil) // findings include the pr.* and deploy.* rules
	if msg.fresh && !m.flashErr {
		for _, st := range msg.states {
			if st.Error != "" && st.Error == github.ErrNoToken.Error() {
				m.setFlash("pull requests and deploys: "+st.Error, true)
				break
			}
		}
	}
	return m, nil
}
