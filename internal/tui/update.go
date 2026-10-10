package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/taw"
)

// stepState is one line of a task's checklist (an update's steps).
type stepState struct {
	taw.Step
	state          string // taw.StepWait…StepNone
	detail         string
	started, ended time.Time
}

func newSteps(plan []taw.Step) []stepState {
	if plan == nil {
		return nil
	}
	steps := make([]stepState, len(plan))
	for i, s := range plan {
		steps[i] = stepState{Step: s, state: taw.StepWait}
	}
	return steps
}

func (t *taskState) step(key string) *stepState {
	for i := range t.steps {
		if t.steps[i].Key == key {
			return &t.steps[i]
		}
	}
	return nil
}

func (t *taskState) current() *stepState {
	for i := range t.steps {
		if t.steps[i].state == taw.StepRun {
			return &t.steps[i]
		}
	}
	return nil
}

// progress moves the checklist on one line of output: a step starts (and
// the running one is done), or the running step ends.
func (t *taskState) progress(line string, now time.Time) {
	if t.steps == nil {
		return
	}
	ev, ok := taw.ParseProgress(line)
	if !ok {
		return
	}
	if ev.Key == "" {
		if s := t.current(); s != nil {
			s.state, s.ended = ev.State, now
			if ev.Detail != "" {
				s.detail = ev.Detail
			}
		}
		return
	}
	s := t.step(ev.Key)
	if s == nil {
		return
	}
	if r := t.current(); r != nil {
		r.state, r.ended = taw.StepPass, now
	}
	if ev.Key == "deliver" {
		if c := t.step("commit"); c != nil && c.state == taw.StepWait {
			c.state, c.started, c.ended = taw.StepPass, now, now
		}
	}
	s.state, s.started = ev.State, now
	if ev.State != taw.StepRun {
		s.ended = now
	}
	if ev.Detail != "" {
		s.detail = ev.Detail
	}
}

// finishSteps settles the checklist with the update's report (or the error
// that stopped it before there was one).
func (t *taskState) finishSteps(now time.Time) {
	if t.steps == nil {
		return
	}
	settle := func(running, waiting string) {
		for i := range t.steps {
			s := &t.steps[i]
			switch s.state {
			case taw.StepRun:
				s.state, s.ended = running, now
			case taw.StepWait:
				s.state = waiting
			}
		}
	}
	o, ok := t.summary.Report.(actions.UpdateOutcome)
	if t.err != nil || !ok {
		settle(taw.StepFail, taw.StepNone)
		return
	}
	rep := o.Report
	if c := t.step("core"); c != nil && rep.Core.To != "" {
		from, to := strings.TrimPrefix(rep.Core.From, "v"), strings.TrimPrefix(rep.Core.To, "v")
		if from != to {
			c.detail = from + " → " + to
		} else {
			c.detail = "stays " + to + " (already the newest it may take)"
		}
	}
	if s := t.step("prepare"); s != nil && rep.Bridged != "" {
		s.detail = "taw/core " + strings.TrimPrefix(rep.Core.To, "v") + " into vendor/; composer.lock went back"
	}
	if s := t.step("migrations"); s != nil && s.state != taw.StepWait {
		s.detail = "none needed"
		if len(rep.Migrations) > 0 {
			s.detail = strings.Join(rep.Migrations, ", ")
		}
		if n := len(rep.Manual); n > 0 {
			s.detail += fmt.Sprintf("  ·  %d for you", n)
		}
	}
	commit, deliver := t.step("commit"), t.step("deliver")
	switch rep.Status {
	case "updated":
		if commit != nil {
			commit.detail = fmt.Sprintf("%d %s on %s", len(rep.Changed), plural(len(rep.Changed), "file", "files"), rep.Branch)
		}
		if deliver != nil && rep.Delivered != nil {
			deliver.detail = rep.Delivered.URL
			if rep.Delivered.URL == "" {
				deliver.detail = rep.Delivered.Note
			}
		}
		settle(taw.StepPass, taw.StepPass)
	case "up-to-date":
		if commit != nil {
			commit.state, commit.detail = taw.StepSkip, "nothing changed"
		}
		if deliver != nil {
			deliver.state, deliver.detail = taw.StepSkip, "nothing to deliver"
		}
		settle(taw.StepPass, taw.StepPass)
	case "refused":
		settle(taw.StepNone, taw.StepNone)
	default: // failed
		key := ""
		if rep.Failure != nil {
			key = taw.FailedStep(rep.Failure.Step)
		}
		if s := t.step(key); s != nil {
			s.state = taw.StepFail
			if s.ended.IsZero() {
				s.ended = now
			}
		}
		if commit != nil && len(rep.Changed) > 0 && strings.HasPrefix(key, "check:") {
			commit.state, commit.detail = taw.StepPass, "on "+rep.Branch+", not pushed"
		}
		settle(taw.StepFail, taw.StepNone)
	}
}

// updateScreen is a task with steps: what it's about, a progress bar, the
// checklist, then the latest output while it runs and the result after.
func (m Model) updateScreen(height int) string {
	p, t := m.pal, m.task
	muted, faint := p.Fg(p.Muted), p.Fg(p.Faint)
	bold := lipgloss.NewStyle().Bold(true)
	now := m.deps.Now()
	o, hasOutcome := t.summary.Report.(actions.UpdateOutcome)
	failed := t.err != nil || t.summary.Failed

	var state string
	switch {
	case t.running:
		state = m.spin.View() + " " + muted.Render("running  "+clock(now.Sub(t.started)))
	case hasOutcome && o.Report.Status == "refused":
		state = p.Fg(p.Err).Render("✗ didn't start")
	case failed:
		state = p.Fg(p.Err).Render("✗ stopped") + muted.Render(" after "+clock(t.finished.Sub(t.started)))
	default:
		state = p.Fg(p.OK).Render("✓ done") + muted.Render(" in "+clock(t.finished.Sub(t.started)))
	}
	title := " " + bold.Foreground(p.Accent).Render(t.title)
	gap := max(m.width-ansi.StringWidth(title)-ansi.StringWidth(state)-1, 2)
	lines := []string{title + strings.Repeat(" ", gap) + state}
	for _, l := range t.info {
		lines = append(lines, " "+muted.Render(l))
	}
	lines = append(lines, "")

	// The bar: finished steps of all of them.
	done := 0
	for _, s := range t.steps {
		if s.state == taw.StepPass || s.state == taw.StepFail || s.state == taw.StepSkip {
			done++
		}
	}
	barW := max(min(m.width-30, 64), 10)
	frac := float64(done) / float64(max(len(t.steps), 1))
	fill := int(math.Round(frac * float64(barW)))
	color := p.Accent
	switch {
	case failed:
		color = p.Err
	case !t.running:
		color = p.OK
	}
	bar := p.Fg(color).Render(strings.Repeat("━", fill)) + faint.Render(strings.Repeat("━", barW-fill))
	lines = append(lines, "  "+bar+"  "+bold.Render(fmt.Sprintf("%3d%%", int(math.Round(frac*100))))+muted.Render(fmt.Sprintf("  ·  %d of %d steps", done, len(t.steps))), "")

	// The checklist.
	labelW := 28
	for _, s := range t.steps {
		icon, label := faint.Render("○"), muted.Render(pad(s.Label, labelW))
		dur := ""
		switch s.state {
		case taw.StepRun:
			icon, label = m.spin.View(), bold.Render(pad(s.Label, labelW))
			dur = clock(now.Sub(s.started))
		case taw.StepPass:
			icon, label = p.Fg(p.OK).Render("✓"), pad(s.Label, labelW)
		case taw.StepFail:
			icon, label = p.Fg(p.Err).Render("✗"), p.Fg(p.Err).Render(pad(s.Label, labelW))
		case taw.StepSkip:
			icon, label = p.Fg(p.Warn).Render("–"), pad(s.Label, labelW)
			s.detail = "not run here" + map[bool]string{true: " (" + s.detail + ")", false: ""}[s.detail != ""]
		case taw.StepNone:
			icon, label = faint.Render("·"), faint.Render(pad(s.Label, labelW))
		}
		if s.state != taw.StepRun && !s.ended.IsZero() && !s.started.IsZero() {
			dur = clock(s.ended.Sub(s.started))
		}
		lines = append(lines, "  "+icon+"  "+label+" "+muted.Render(fmt.Sprintf("%6s", dur))+"   "+muted.Render(s.detail))
	}
	lines = append(lines, "")

	if t.running {
		// The latest output, as much as fits.
		lines = append(lines, " "+faint.Render("── output ")+muted.Render("(l the full log)")+" "+faint.Render(strings.Repeat("─", max(m.width-30, 0))))
		room := max(height-len(lines), 1)
		tail := t.lines
		if len(tail) > room {
			tail = tail[len(tail)-room:]
		}
		for _, l := range tail {
			lines = append(lines, "  "+faint.Render(ansi.Strip(l)))
		}
		return block(strings.Join(lines, "\n"), m.width, height)
	}
	lines = append(lines, m.updateResult(o, hasOutcome)...)
	return block(strings.Join(lines, "\n"), m.width, height)
}

// updateResult is the panel under a finished update's checklist.
func (m Model) updateResult(o actions.UpdateOutcome, ok bool) []string {
	p, t := m.pal, m.task
	muted := p.Fg(p.Muted)
	bold := lipgloss.NewStyle().Bold(true)
	wrapW := max(m.width-8, 20)
	wrap := func(prefix, s string) []string {
		var out []string
		for i, l := range strings.Split(lipgloss.NewStyle().Width(wrapW).Render(s), "\n") {
			if i == 0 {
				out = append(out, prefix+l)
			} else {
				out = append(out, strings.Repeat(" ", ansi.StringWidth(prefix))+l)
			}
		}
		return out
	}
	var out []string
	rule := " " + p.Fg(p.Faint).Render(strings.Repeat("─", max(m.width-2, 0)))
	out = append(out, rule)
	if t.err != nil || !ok {
		msg := t.summary.Headline
		if t.err != nil {
			msg = t.err.Error()
		}
		return append(out, wrap(" "+p.Fg(p.Err).Render("✗ "), msg)...)
	}
	rep := o.Report
	switch rep.Status {
	case "updated":
		head := "Updated to taw/core " + strings.TrimPrefix(rep.Core.To, "v")
		sub := ""
		if rep.Delivered != nil {
			switch rep.Delivered.How {
			case "pr+merge":
				sub = "pull request opened; it merges itself when CI passes"
			case "pr":
				sub = "pull request opened"
			default:
				sub = "committed on " + rep.Branch
			}
		}
		out = append(out, " "+p.Fg(p.OK).Render("✓ ")+bold.Render(head)+muted.Render("  ·  "+sub))
		if rep.Delivered != nil && rep.Delivered.URL != "" {
			out = append(out, "   "+p.Fg(p.Info).Render(rep.Delivered.URL)+muted.Render("   o opens it"))
		}
		out = append(out, "   "+fmt.Sprintf("%d %s changed on %s", len(rep.Changed), plural(len(rep.Changed), "file", "files"), rep.Branch)+muted.Render("   r the report"))
		if n := len(rep.Manual); n > 0 {
			out = append(out, "", "   "+p.Fg(p.Warn).Render(fmt.Sprintf("For you (%d)", n))+muted.Render("  what the update left for a person; also in the report"))
			for _, step := range rep.Manual {
				out = append(out, wrap("   • ", step)...)
			}
		}
		for _, h := range rep.Held {
			out = append(out, wrap("   – ", h)...)
		}
		if rep.Delivered != nil && rep.Delivered.How != "branch" {
			out = append(out, "", "   "+muted.Render("Merging deploys production: M in the table, or on GitHub."))
		}
	case "up-to-date":
		out = append(out, " "+p.Fg(p.OK).Render("✓ ")+bold.Render("Nothing to update")+muted.Render("  ·  taw/core, the framework files and the migrations are current; no branch was left"))
	case "refused":
		out = append(out, " "+p.Fg(p.Err).Render("✗ ")+bold.Render("The update didn't start")+muted.Render("  ·  nothing changed"))
		if rep.Failure != nil {
			out = append(out, wrap("   ", rep.Failure.Reason)...)
		}
	default:
		step := "a step"
		if rep.Failure != nil {
			if s := t.step(taw.FailedStep(rep.Failure.Step)); s != nil {
				step = s.Label
			} else {
				step = rep.Failure.Step
			}
		}
		out = append(out, " "+p.Fg(p.Err).Render("✗ ")+bold.Render("Stopped at "+step)+muted.Render("  ·  nothing was pushed; the work so far is on "+rep.Branch))
		if rep.Failure != nil {
			if rep.Failure.Command != "" {
				out = append(out, "   "+muted.Render("$ ")+rep.Failure.Command)
			}
			shown := 0
			for _, l := range strings.Split(rep.Failure.Out, "\n") {
				if l = strings.TrimSpace(l); strings.Trim(l, "- ") == "" || shown == 4 {
					continue // blank, or a table rule
				}
				out = append(out, "   "+p.Fg(p.Err).Render("│ ")+ansi.Truncate(l, wrapW, "…"))
				shown++
			}
		}
		out = append(out, "", "   "+bold.Render("1")+" Fix with Claude"+muted.Render(" (from the report; asks before pushing)")+"     "+bold.Render("2")+" Do it myself"+muted.Render(" (the report, step by step)"))
	}
	return out
}

// rowShowsResult is how long a finished update's outcome stays on its row.
const rowShowsResult = 15 * time.Minute

// rowProgress is an update's progress (or, for a while after, its outcome)
// for its theme's row in the table, w cells wide.
func (m Model) rowProgress(target string, w int) (string, bool) {
	t := m.task
	if t == nil || t.steps == nil || t.target != target || w < 20 {
		return "", false
	}
	p := m.pal
	muted := p.Fg(p.Muted)
	hint := muted.Render("  o shows it")
	if !t.running {
		if m.deps.Now().Sub(t.finished) > rowShowsResult {
			return "", false
		}
		o, ok := t.summary.Report.(actions.UpdateOutcome)
		var s string
		switch {
		case t.err != nil || !ok:
			s = p.Fg(p.Err).Render("✗ update failed")
		case o.Report.Status == "updated":
			s = p.Fg(p.OK).Render("✓ updated to taw/core " + strings.TrimPrefix(o.Report.Core.To, "v"))
			if o.Report.Delivered != nil && o.Report.Delivered.URL != "" {
				s += muted.Render("  ·  pull request opened")
			}
		case o.Report.Status == "up-to-date":
			s = p.Fg(p.OK).Render("✓ nothing to update")
		case o.Report.Status == "refused":
			s = p.Fg(p.Err).Render("✗ the update didn't start")
		default:
			label := "a step"
			for _, st := range t.steps {
				if st.state == taw.StepFail {
					label = st.Label
					break
				}
			}
			s = p.Fg(p.Err).Render("✗ update stopped at "+label) + muted.Render("  ·  nothing pushed")
		}
		return ansi.Truncate(s+hint, w, "…"), true
	}
	done, current := 0, ""
	for _, st := range t.steps {
		switch st.state {
		case taw.StepPass, taw.StepFail, taw.StepSkip:
			done++
		case taw.StepRun:
			current = st.Label
		}
	}
	barW := 14
	frac := float64(done) / float64(max(len(t.steps), 1))
	fill := int(math.Round(frac * float64(barW)))
	bar := p.Fg(p.Accent).Render(strings.Repeat("━", fill)) + p.Fg(p.Faint).Render(strings.Repeat("━", barW-fill))
	s := m.spin.View() + " " + p.Fg(p.Accent).Render("updating") + "  " + bar + " " + fmt.Sprintf("%3d%%", int(math.Round(frac*100)))
	if current != "" {
		s += muted.Render("  ·  " + current)
	}
	return ansi.Truncate(s+hint, w, "…"), true
}

// stepSummary is "  64% · Static analysis (PHPStan)" for the status line,
// or "" for a task without steps.
func (t *taskState) stepSummary() string {
	if t.steps == nil {
		return ""
	}
	done := 0
	for _, s := range t.steps {
		if s.state == taw.StepPass || s.state == taw.StepFail || s.state == taw.StepSkip {
			done++
		}
	}
	out := fmt.Sprintf("  %d%%", done*100/max(len(t.steps), 1))
	if s := t.current(); s != nil {
		out += " · " + s.Label
	}
	return out
}

// clock is a duration as 0:07 or 1:24.
func clock(d time.Duration) string {
	d = d.Round(time.Second)
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}
