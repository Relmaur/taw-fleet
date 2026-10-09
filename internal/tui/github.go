package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Relmaur/taw-fleet/internal/actions"
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
	if deploying(m.repos) || len(m.followed) > 0 {
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

// onRepos installs a GitHub answer and checks on the deploys it follows.
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
	return m.checkFollowed()
}

// followFor is how long a merged pull request's deploy is followed.
const followFor = 30 * time.Minute

// following is a merged pull request whose deploy the dashboard watches.
type following struct {
	res   actions.MergeResult
	since time.Time
}

// follow starts watching a merge's deploy.
func (m *Model) follow(res actions.MergeResult) {
	f := map[string]following{res.Repo: {res, m.deps.Now()}}
	for k, v := range m.followed {
		if k != res.Repo {
			f[k] = v
		}
	}
	m.followed = f
}

// checkFollowed says when production has a merged pull request, or why it
// won't, and re-checks the live site once it's deployed.
func (m Model) checkFollowed() (tea.Model, tea.Cmd) {
	if len(m.followed) == 0 {
		return m, nil
	}
	left := map[string]following{}
	deployed := false
	for repo, f := range m.followed {
		st, ok := m.repos[repo]
		d := st.Deploy
		where := "production"
		if f.res.Production != "" {
			where = strings.TrimPrefix(strings.TrimPrefix(f.res.Production, "https://"), "http://")
		}
		switch {
		case !ok || d == nil:
			left[repo] = f
		case d.Deployed == f.res.SHA:
			took := d.DeployedAt.Sub(f.since)
			msg := fmt.Sprintf("%s has #%d: deployed", where, f.res.Number)
			if r := m.deps.Now().Sub(f.since); took <= 0 || took > r {
				took = r
			}
			m.setFlash(fmt.Sprintf("%s (%s after the merge)", msg, took.Round(time.Second)), false)
			deployed = true
		case d.Failed != nil && d.Failed.SHA == f.res.SHA:
			m.setFlash(fmt.Sprintf("the deploy of #%d to %s failed: %s", f.res.Number, where, d.Failed.URL), true)
		case st.Head == f.res.SHA && st.HeadCI == site.ChecksFailing:
			m.setFlash(fmt.Sprintf("CI failed on %s after merging #%d, so %s wasn't deployed", st.Default, f.res.Number, where), true)
		case m.deps.Now().Sub(f.since) > followFor:
			m.setFlash(fmt.Sprintf("#%d isn't on %s after %s; see %s", f.res.Number, where, followFor, d.URL), true)
		default:
			left[repo] = f
		}
	}
	m.followed = left
	if deployed && m.deps.Live != nil {
		return m.fetchLive(true)
	}
	return m, nil
}

// askMerge merges the selected theme's pull request (M), asking which one
// when there are several.
func (m Model) askMerge() (tea.Model, tea.Cmd) {
	s, t, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	switch {
	case m.deps.Actions == nil:
		m.setFlash("unavailable: "+errText(m.deps.ActionsErr), true)
		return m, nil
	case m.deps.GitHub == nil:
		m.setFlash("GitHub isn't read in this session (--offline?)", true)
		return m, nil
	case t.GitHub == nil:
		m.setFlash(t.Dir+": GitHub hasn't been read yet (L reads it)", true)
		return m, nil
	case t.GitHub.Error != "":
		m.setFlash(t.Dir+": "+t.GitHub.Error, true)
		return m, nil
	case len(t.GitHub.PRs) == 0:
		m.setFlash(t.Dir+" has no open pull request", false)
		return m, nil
	}
	merge := func(pr site.PullRequest) func(Model) (tea.Model, tea.Cmd) {
		return func(m Model) (tea.Model, tea.Cmd) {
			task, err := m.deps.Actions.MergeTask(s, t, pr)
			if err != nil {
				m.setFlash(t.Dir+": "+err.Error(), true)
				return m, nil
			}
			return m.askTask(task, "")
		}
	}
	prs := t.GitHub.PRs
	if len(prs) == 1 {
		return merge(prs[0])(m)
	}
	var opts []string
	m.choices = nil
	for i, pr := range prs[:min(len(prs), 9)] {
		opts = append(opts, fmt.Sprintf("%d #%d %s", i+1, pr.Number, clip(pr.Title, 28)))
		m.choices = append(m.choices, merge(pr))
	}
	m.confirm = "Merge which pull request of " + t.Dir + "?  " + strings.Join(opts, " · ")
	return m, nil
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
