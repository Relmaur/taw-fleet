package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/taw"
)

// Merges (M) run on their own, outside the one task slot: several sites
// can merge at once, each followed on its row and in its MERGE section,
// from the merge to production.

// mergeState is one site's merge, from the click to production.
type mergeState struct {
	target  string // "siteID/themeDir"
	label   string // "ls-mxico · ls-mexico"
	number  int
	title   string
	phase   string // merging, deploying, deployed, merged (no deploy), failed
	reason  string // why it failed
	where   string // the production host, when configured
	started time.Time
	merged  time.Time
	ended   time.Time
	notes   []string // what happened locally (pulled, branch deleted, left alone…)
	res     actions.MergeResult
}

func (st *mergeState) running() bool { return st.phase == "merging" || st.phase == "deploying" }

// mergeLineMsg is one line a running merge said; mergeDoneMsg its end.
type mergeLineMsg struct {
	target string
	line   string
	ch     chan tea.Msg
}

type mergeDoneMsg struct {
	target string
	sum    actions.Summary
	err    error
}

func listenMerge(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// startMerge runs a merge task in the background, beside anything else.
func (m Model) startMerge(s site.Site, t site.Theme, pr site.PullRequest, task actions.Task) (tea.Model, tea.Cmd) {
	target := s.ID + "/" + t.Dir
	if cur := m.merges[target]; cur != nil && cur.running() && cur.phase == "merging" {
		m.setFlash(t.Dir+": #"+fmt.Sprint(cur.number)+" is still merging", true)
		return m, nil
	}
	st := &mergeState{target: target, label: s.Slug + " · " + t.Dir, number: pr.Number, title: pr.Title,
		phase: "merging", started: m.deps.Now()}
	merges := map[string]*mergeState{target: st}
	for k, v := range m.merges {
		if k != target {
			merges[k] = v
		}
	}
	m.merges = merges
	ch := make(chan tea.Msg, 64)
	ctx := m.ctx
	go func() {
		w := taw.NewLineWriter(func(l string) { ch <- mergeLineMsg{target: target, line: l, ch: ch} })
		sum, err := task.Run(ctx, w)
		w.Flush()
		ch <- mergeDoneMsg{target: target, sum: sum, err: err}
	}()
	return m, tea.Batch(listenMerge(ch), m.spin.Tick)
}

// onMergeDone records how a merge ended and starts following its deploy.
func (m Model) onMergeDone(msg mergeDoneMsg) (tea.Model, tea.Cmd) {
	st := m.merges[msg.target]
	if st == nil {
		return m, nil
	}
	now := m.deps.Now()
	if msg.err != nil {
		st.phase, st.reason, st.ended = "failed", msg.err.Error(), now
		m.setFlash(fmt.Sprintf("%s: merging #%d failed: %s", st.label, st.number, msg.err), true)
		return m, nil
	}
	st.merged = now
	st.notes = append(st.notes, msg.sum.Lines...)
	res, _ := msg.sum.Report.(actions.MergeResult)
	st.res = res
	st.where = strings.TrimPrefix(strings.TrimPrefix(res.Production, "https://"), "http://")
	if res.Deploys {
		st.phase = "deploying"
		m.follow(res)
	} else {
		st.phase, st.ended = "merged", now
	}
	m.setFlash(msg.sum.Headline, false)
	var cmds []tea.Cmd
	model, cmd := m.fetchRepos(false)
	m, cmds = model.(Model), append(cmds, cmd)
	if !m.scanning {
		cmds = append(cmds, m.startScan())
	}
	return m, tea.Batch(cmds...)
}

// deployEnded closes the merge whose deploy the dashboard followed.
func (m *Model) deployEnded(repo string, sha string, failed string) {
	for _, st := range m.merges {
		if st.phase == "deploying" && st.res.Repo == repo && st.res.SHA == sha {
			st.ended = m.deps.Now()
			if failed != "" {
				st.phase, st.reason = "failed", failed
			} else {
				st.phase = "deployed"
			}
		}
	}
}

// mergeFor is the merge of a theme the dashboard knows about: running, or
// ended in the last rowShowsResult.
func (m Model) mergeFor(target string) (*mergeState, bool) {
	st := m.merges[target]
	if st == nil {
		return nil, false
	}
	if !st.running() && m.deps.Now().Sub(st.ended) > rowShowsResult {
		return nil, false
	}
	return st, true
}

// runningMerges are the merges still going, oldest first.
func (m Model) runningMerges() []*mergeState {
	var out []*mergeState
	for _, st := range m.merges {
		if st.running() {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].started.Before(out[j].started) })
	return out
}

// mergeIcon is a merge's mark on its row: the spinner until production has
// it, then how it ended.
func (m Model) mergeIcon(st *mergeState) string {
	p := m.pal
	switch st.phase {
	case "merging", "deploying":
		return m.spin.View()
	case "failed":
		return p.Fg(p.Err).Render(p.I.Failed)
	}
	return p.Fg(p.OK).Render(p.I.Done)
}

// short is a running merge on its card: "merging #8…", "deploying…".
func (st *mergeState) short() string {
	if st.phase == "deploying" {
		return "deploying…"
	}
	return fmt.Sprintf("merging #%d…", st.number)
}

// mergeWords is what a merge is doing, in a few words.
func (st *mergeState) words() string {
	to := "production"
	if st.where != "" {
		to = st.where
	}
	switch st.phase {
	case "merging":
		return fmt.Sprintf("merging #%d", st.number)
	case "deploying":
		return "deploying to " + to
	case "deployed":
		return fmt.Sprintf("#%d is on %s", st.number, to)
	case "merged":
		return fmt.Sprintf("#%d merged", st.number)
	}
	return fmt.Sprintf("#%d: %s", st.number, st.reason)
}

// mergeSection is the detail pane's MERGE section: each step of the merge,
// from GitHub to production.
func (m Model) mergeSection(target string, width int) string {
	st, ok := m.mergeFor(target)
	if !ok {
		return ""
	}
	p := m.pal
	muted := p.Fg(p.Muted)
	now := m.deps.Now()
	ok1 := p.Fg(p.OK).Render(p.I.Done)
	var lines []string
	lines = append(lines, "  "+p.Fg(p.Brand).Render(fmt.Sprintf("#%d", st.number))+" "+st.title)
	switch {
	case st.phase == "merging":
		lines = append(lines, "  "+m.spin.View()+" merging on GitHub"+muted.Render("  "+clock(now.Sub(st.started))))
	case st.phase == "failed" && st.merged.IsZero():
		lines = append(lines, "  "+p.Fg(p.Err).Render(p.I.Failed)+" not merged: "+st.reason)
	default:
		lines = append(lines, "  "+ok1+" merged"+muted.Render(fmt.Sprintf("  %s, %s", st.res.SHA[:min(7, len(st.res.SHA))], clock(st.merged.Sub(st.started)))))
		local := ok1 + " this Mac on the site's branch"
		for _, n := range st.notes {
			if strings.Contains(n, "left alone") || strings.Contains(n, "wasn't") || strings.Contains(n, "git pull") || strings.Contains(n, "git switch") {
				local = p.Fg(p.Warn).Render("▲") + " " + n
			}
		}
		lines = append(lines, "  "+local)
	}
	to := "production"
	if st.where != "" {
		to = st.where
	}
	switch st.phase {
	case "deploying":
		lines = append(lines, "  "+m.spin.View()+" deploying to "+to+muted.Render("  "+clock(now.Sub(st.merged))+" · CI, then Deploy"),
			"    "+muted.Render("clear the page cache once it's on "+to+", not before"))
	case "deployed":
		lines = append(lines, "  "+ok1+" on "+to+muted.Render("  "+clock(st.ended.Sub(st.merged))+" after the merge"),
			"  "+p.Fg(p.Warn).Render("▲")+" clear the page cache now"+muted.Render(": Hummingbird, WP Rocket, the host's"))
	case "merged":
		lines = append(lines, "  "+muted.Render("no deploy workflow: nothing reaches production on its own"))
	case "failed":
		if !st.merged.IsZero() {
			lines = append(lines, "  "+p.Fg(p.Err).Render(p.I.Failed)+" "+st.reason)
		}
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return render.Section(p, p.I.Deploy+" MERGE", p.Accent, "M · its pull request", width) + strings.Join(lines, "\n") + "\n\n"
}

// mergeStatus is the footer's line while merges run.
func (m Model) mergeStatus() string {
	run := m.runningMerges()
	if len(run) == 0 {
		return ""
	}
	var parts []string
	for _, st := range run {
		parts = append(parts, st.label+" "+st.words())
	}
	return strings.Join(parts, "  ·  ")
}
