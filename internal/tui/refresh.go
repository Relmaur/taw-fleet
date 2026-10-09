package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// fullRefresh is a ctrl+r in progress: everything fetched again for every
// site, skipping the caches.
type fullRefresh struct {
	active      bool
	syncPending bool   // the sync check starts once the scan is in
	syncTitle   string // the sync check's task, while it runs
	syncResult  string // its headline, for the summary
}

// refreshAll re-reads every site (ctrl+r): the Local scan with fresh latest
// versions, GitHub's pull requests and deploys, the production sites, the
// BugSmash comments, and then the sync check of every classic theme.
func (m Model) refreshAll() (tea.Model, tea.Cmd) {
	if m.full.active {
		m.setFlash("already refreshing everything", true)
		return m, nil
	}
	m.full = fullRefresh{active: true, syncPending: m.deps.Actions != nil}
	var cmds []tea.Cmd
	if !m.scanning {
		cmds = append(cmds, m.scanWith(m.freshScan()))
	}
	if m.deps.GitHub != nil {
		model, cmd := m.fetchRepos(true)
		m, cmds = model.(Model), append(cmds, cmd)
	}
	if m.deps.Live != nil {
		model, cmd := m.fetchLive(true)
		m, cmds = model.(Model), append(cmds, cmd)
	}
	if m.deps.Feedback != nil {
		model, cmd := m.fetchFeedback(true)
		m, cmds = model.(Model), append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// afterRefreshScan starts the sync check once a full refresh's scan is in,
// so it checks the themes that scan found. Quiet: o shows its output.
func (m Model) afterRefreshScan() (Model, tea.Cmd) {
	if !m.full.active || !m.full.syncPending {
		return m, nil
	}
	m.full.syncPending = false
	if m.task != nil && m.task.running {
		return m, nil // a task already holds the output view; skip the check
	}
	task, err := m.deps.Actions.SyncAllTask(m.rep.Sites)
	if err != nil {
		return m, nil // no classic theme to check
	}
	task.Quiet = true
	m.full.syncTitle = task.Title
	model, cmd := m.startTask(task)
	return model.(Model), cmd
}

// refreshPending lists what a full refresh still waits for.
func (m Model) refreshPending() []string {
	var parts []string
	if m.scanning {
		parts = append(parts, "Local sites")
	}
	if m.reposFetching {
		parts = append(parts, "GitHub")
	}
	if m.liveFetching {
		parts = append(parts, "production")
	}
	if m.feedbackFetching {
		parts = append(parts, "BugSmash")
	}
	if m.full.syncPending || (m.full.syncTitle != "" && m.task != nil && m.task.running && m.task.title == m.full.syncTitle) {
		parts = append(parts, "sync check")
	}
	return parts
}

// finishRefresh ends a full refresh once nothing is pending, with a summary.
func (m Model) finishRefresh() Model {
	if !m.full.active || len(m.refreshPending()) > 0 {
		return m
	}
	themes := 0
	for _, s := range m.rep.Sites {
		themes += len(s.TAWThemes())
	}
	parts := []string{fmt.Sprintf("refreshed %d sites, %d TAW themes", len(m.rep.Sites), themes)}
	if m.live != nil {
		parts = append(parts, liveSummary(m.live))
	}
	if m.feedback != nil {
		parts = append(parts, feedbackSummary(m.feedback))
	}
	if m.full.syncResult != "" {
		parts = append(parts, m.full.syncResult)
	}
	m.full = fullRefresh{}
	m.setFlash(strings.Join(parts, " · "), m.err != nil)
	return m
}
