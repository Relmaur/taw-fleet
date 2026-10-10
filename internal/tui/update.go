package tui

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/taw"
)

// stepState is one line of an update's checklist.
type stepState struct {
	taw.Step
	state          string // taw.StepWait…StepNone
	detail         string
	started, ended time.Time
}

// checklist is an update's steps, followed from its progress lines.
type checklist []stepState

func newSteps(plan []taw.Step) checklist {
	if plan == nil {
		return nil
	}
	steps := make(checklist, len(plan))
	for i, s := range plan {
		steps[i] = stepState{Step: s, state: taw.StepWait}
	}
	return steps
}

func (c checklist) step(key string) *stepState {
	for i := range c {
		if c[i].Key == key {
			return &c[i]
		}
	}
	return nil
}

func (c checklist) current() *stepState {
	for i := range c {
		if c[i].state == taw.StepRun {
			return &c[i]
		}
	}
	return nil
}

// done is how many steps have finished, and the fraction of all of them.
func (c checklist) done() (int, float64) {
	n := 0
	for _, s := range c {
		if s.state == taw.StepPass || s.state == taw.StepFail || s.state == taw.StepSkip {
			n++
		}
	}
	return n, float64(n) / float64(max(len(c), 1))
}

func (c checklist) failed() *stepState {
	for i := range c {
		if c[i].state == taw.StepFail {
			return &c[i]
		}
	}
	return nil
}

// progress moves the checklist on one line of output: a step starts (and
// the running one is done), or the running step ends.
func (c checklist) progress(line string, now time.Time) {
	ev, ok := taw.ParseProgress(line)
	if !ok || c == nil {
		return
	}
	if ev.Key == "" {
		if s := c.current(); s != nil {
			s.state, s.ended = ev.State, now
			if ev.Detail != "" {
				s.detail = ev.Detail
			}
		}
		return
	}
	s := c.step(ev.Key)
	if s == nil {
		return
	}
	if r := c.current(); r != nil {
		r.state, r.ended = taw.StepPass, now
	}
	if ev.Key == "deliver" {
		if cm := c.step("commit"); cm != nil && cm.state == taw.StepWait {
			cm.state, cm.started, cm.ended = taw.StepPass, now, now
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

// settle completes the checklist from the update's report, or from the
// error that stopped it before there was one (o nil).
func (c checklist) settle(o *actions.UpdateOutcome, err error, now time.Time) {
	fill := func(running, waiting string) {
		for i := range c {
			switch c[i].state {
			case taw.StepRun:
				c[i].state, c[i].ended = running, now
			case taw.StepWait:
				c[i].state = waiting
			}
		}
	}
	if err != nil || o == nil {
		fill(taw.StepFail, taw.StepNone)
		return
	}
	rep := o.Report
	if s := c.step("core"); s != nil && rep.Core.To != "" {
		from, to := strings.TrimPrefix(rep.Core.From, "v"), strings.TrimPrefix(rep.Core.To, "v")
		if from != to {
			s.detail = from + " → " + to
		} else {
			s.detail = "stays " + to + " (the newest it may take)"
		}
	}
	if s := c.step("prepare"); s != nil && rep.Bridged != "" {
		s.detail = "taw/core " + strings.TrimPrefix(rep.Core.To, "v") + " into vendor/; composer.lock went back"
	}
	if s := c.step("migrations"); s != nil && s.state != taw.StepWait {
		s.detail = "none needed"
		if len(rep.Migrations) > 0 {
			s.detail = strings.Join(rep.Migrations, ", ")
		}
		if n := len(rep.Manual); n > 0 {
			s.detail += fmt.Sprintf("  ·  %d for you", n)
		}
	}
	commit, deliver := c.step("commit"), c.step("deliver")
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
		fill(taw.StepPass, taw.StepPass)
	case "up-to-date":
		if commit != nil {
			commit.state, commit.detail = taw.StepSkip, "nothing changed"
		}
		if deliver != nil {
			deliver.state, deliver.detail = taw.StepSkip, "nothing to deliver"
		}
		fill(taw.StepPass, taw.StepPass)
	case "refused":
		fill(taw.StepNone, taw.StepNone)
	default: // failed
		key := ""
		if rep.Failure != nil {
			key = taw.FailedStep(rep.Failure.Step)
		}
		if s := c.step(key); s != nil {
			s.state = taw.StepFail
			if s.ended.IsZero() {
				s.ended = now
			}
		}
		if commit != nil && len(rep.Changed) > 0 && strings.HasPrefix(key, "check:") {
			commit.state, commit.detail = taw.StepPass, "on "+rep.Branch+", not pushed"
		}
		fill(taw.StepFail, taw.StepNone)
	}
}

// batchItem is one theme of an update-all, with its own checklist.
type batchItem struct {
	actions.BatchItem
	steps          checklist
	state          string // wait, run, done
	status         string // its update's status as the run reported it (updated, failed…)
	pr             string // and its pull request, when one opened
	started, ended time.Time
	lines          []string
	outcome        *actions.UpdateItemOutcome
}

// progress routes one line of an update or an update-all's output.
func (t *taskState) progress(line string, now time.Time) {
	if t.batch == nil {
		t.steps.progress(line, now)
		return
	}
	switch {
	case strings.HasPrefix(line, actions.BatchStart):
		target := strings.TrimPrefix(line, actions.BatchStart)
		for i, it := range t.batch {
			if it.Target == target {
				t.cur, it.state, it.started = i, "run", now
				t.sel = i
			}
		}
	case strings.HasPrefix(line, actions.BatchEnd):
		if t.cur >= 0 {
			it := t.batch[t.cur]
			fields := strings.Fields(strings.TrimPrefix(line, actions.BatchEnd))
			it.state, it.ended = "done", now
			if len(fields) >= 2 {
				it.status = fields[1]
			}
			if len(fields) >= 3 {
				it.pr = fields[2]
			}
			// Until the reports arrive with the summary: settle by the status.
			o := actions.UpdateOutcome{Report: taw.UpdateReport{Status: it.status}}
			it.steps.settle(&o, nil, now)
			t.cur = -1
		}
	case t.cur >= 0:
		it := t.batch[t.cur]
		it.lines = append(it.lines, line)
		it.steps.progress(line, now)
	}
}

// finishSteps settles the checklists with the outcomes.
func (t *taskState) finishSteps(now time.Time) {
	if t.batch == nil {
		if t.steps == nil {
			return
		}
		o, ok := t.summary.Report.(actions.UpdateOutcome)
		if !ok {
			t.steps.settle(nil, t.err, now)
			return
		}
		t.steps.settle(&o, t.err, now)
		return
	}
	res, _ := t.summary.Report.(actions.UpdateAllOutcome)
	for _, it := range t.batch {
		for i := range res.Items {
			if res.Items[i].Target == it.Target {
				it.outcome = &res.Items[i]
			}
		}
		if it.outcome == nil {
			it.steps.settle(nil, t.err, now)
			if it.state == "wait" {
				it.state = "none"
			}
			continue
		}
		it.state = "done"
		it.steps.settle(&it.outcome.UpdateOutcome, it.outcome.Err, now)
	}
}

// updateView is what the update screen shows: one theme's update (alone,
// or one theme of an update-all).
type updateView struct {
	title, sub        string
	info              []string
	steps             checklist
	running           bool
	started, finished time.Time
	outcome           *actions.UpdateOutcome
	err               error
	lines             []string
}

func (m Model) taskView() updateView {
	t := m.task
	v := updateView{title: t.title, info: t.info, steps: t.steps, running: t.running, started: t.started, finished: t.finished, err: t.err, lines: t.lines}
	if o, ok := t.summary.Report.(actions.UpdateOutcome); ok {
		v.outcome = &o
	} else if !t.running && t.err == nil {
		v.err = fmt.Errorf("%s", t.summary.Headline)
	}
	return v
}

func (it *batchItem) view() updateView {
	v := updateView{title: "Update " + it.Label, info: it.Info, steps: it.steps, running: it.state == "run", started: it.started, finished: it.ended, lines: it.lines}
	switch {
	case it.outcome != nil:
		v.outcome, v.err = &it.outcome.UpdateOutcome, it.outcome.Err
	case it.status == "error":
		v.err = fmt.Errorf("the update couldn't run (l the full log)")
	case it.status != "":
		o := &actions.UpdateOutcome{Report: taw.UpdateReport{Status: it.status}}
		if it.pr != "" {
			o.Report.Delivered = &struct {
				How  string `json:"how"`
				URL  string `json:"url"`
				Note string `json:"note"`
			}{How: "pr", URL: it.pr}
		}
		v.outcome = o
	}
	return v
}

func (v updateView) failed() bool {
	return v.err != nil || (v.outcome != nil && v.outcome.Report.Status != "updated" && v.outcome.Report.Status != "up-to-date")
}

// updateScreen is one update: what it's about, a progress bar, the
// checklist, then the latest output while it runs and the result after.
func (m Model) updateScreen(v updateView, height int) string {
	p := m.pal
	muted, faint := p.Fg(p.Muted), p.Fg(p.Faint)
	bold := lipgloss.NewStyle().Bold(true)
	now := m.deps.Now()

	var state string
	switch {
	case v.running:
		state = m.spin.View() + " " + muted.Render("running  "+clock(now.Sub(v.started)))
	case v.started.IsZero():
		state = muted.Render("waiting its turn")
	case v.outcome != nil && v.outcome.Report.Status == "refused":
		state = p.Fg(p.Err).Render("✗ didn't start")
	case v.failed():
		state = p.Fg(p.Err).Render("✗ stopped") + muted.Render(" after "+clock(v.finished.Sub(v.started)))
	default:
		state = p.Fg(p.OK).Render("✓ done") + muted.Render(" in "+clock(v.finished.Sub(v.started)))
	}
	title := " " + bold.Foreground(p.Accent).Render(p.I.Update+" "+v.title)
	if v.sub != "" {
		title += muted.Render("  " + v.sub)
	}
	gap := max(m.width-ansi.StringWidth(title)-ansi.StringWidth(state)-1, 2)
	lines := []string{title + strings.Repeat(" ", gap) + state}
	for _, l := range v.info {
		lines = append(lines, " "+muted.Render(l))
	}
	lines = append(lines, "")

	n, frac := v.steps.done()
	color := p.Accent
	switch {
	case v.failed() && !v.running:
		color = p.Err
	case !v.running && !v.started.IsZero():
		color = p.OK
	}
	lines = append(lines, "  "+m.bar(frac, max(min(m.width-30, 64), 10), color)+"  "+bold.Render(fmt.Sprintf("%3d%%", int(math.Round(frac*100))))+muted.Render(fmt.Sprintf("  ·  %d of %d steps", n, len(v.steps))), "")
	lines = append(lines, m.checklistLines(v.steps)...)
	lines = append(lines, "")

	if v.running || v.started.IsZero() {
		if len(v.lines) > 0 {
			lines = append(lines, " "+faint.Render("── output ")+muted.Render("(l the full log)")+" "+faint.Render(strings.Repeat("─", max(m.width-30, 0))))
			room := max(height-len(lines), 1)
			tail := v.lines
			if len(tail) > room {
				tail = tail[len(tail)-room:]
			}
			for _, l := range tail {
				lines = append(lines, "  "+faint.Render(ansi.Strip(l)))
			}
		}
		return block(strings.Join(lines, "\n"), m.width, height)
	}
	lines = append(lines, m.updateResult(v)...)
	return block(strings.Join(lines, "\n"), m.width, height)
}

// bar is a progress bar w cells wide, frac of it in c.
func (m Model) bar(frac float64, w int, c color.Color) string {
	p := m.pal
	fill := min(max(int(math.Round(frac*float64(w))), 0), w)
	return p.Fg(c).Render(strings.Repeat("━", fill)) + p.Fg(p.Faint).Render(strings.Repeat("━", w-fill))
}

func (m Model) checklistLines(steps checklist) []string {
	p := m.pal
	muted, faint := p.Fg(p.Muted), p.Fg(p.Faint)
	bold := lipgloss.NewStyle().Bold(true)
	now := m.deps.Now()
	labelW := 28
	var lines []string
	for _, s := range steps {
		icon, label := faint.Render("○"), muted.Render(pad(s.Label, labelW))
		dur, detail := "", s.detail
		switch s.state {
		case taw.StepRun:
			icon, label = m.spin.View(), bold.Render(pad(s.Label, labelW))
			dur = clock(now.Sub(s.started))
		case taw.StepPass:
			icon, label = p.Fg(p.OK).Render(p.I.Done), pad(s.Label, labelW)
		case taw.StepFail:
			icon, label = p.Fg(p.Err).Render(p.I.Failed), p.Fg(p.Err).Render(pad(s.Label, labelW))
		case taw.StepSkip:
			icon, label = p.Fg(p.Warn).Render("–"), pad(s.Label, labelW)
			if detail != "nothing changed" && detail != "nothing to deliver" {
				detail = "not run here" + map[bool]string{true: " (" + detail + ")", false: ""}[detail != ""]
			}
		case taw.StepNone:
			icon, label = faint.Render("·"), faint.Render(pad(s.Label, labelW))
		}
		if s.state != taw.StepRun && !s.ended.IsZero() && !s.started.IsZero() {
			dur = clock(s.ended.Sub(s.started))
		}
		lines = append(lines, "  "+icon+"  "+label+" "+muted.Render(fmt.Sprintf("%6s", dur))+"   "+muted.Render(detail))
	}
	return lines
}

// updateResult is the panel under a finished update's checklist.
func (m Model) updateResult(v updateView) []string {
	p := m.pal
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
	out := []string{" " + p.Fg(p.Faint).Render(strings.Repeat("─", max(m.width-2, 0)))}
	if v.err != nil || v.outcome == nil {
		msg := "the update didn't finish"
		if v.err != nil {
			msg = v.err.Error()
		}
		return append(out, wrap(" "+p.Fg(p.Err).Render(p.I.Failed+" "), msg)...)
	}
	rep := v.outcome.Report
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
		out = append(out, " "+p.Fg(p.OK).Render(p.I.Done+" ")+bold.Render(head)+muted.Render("  ·  "+sub))
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
		out = append(out, " "+p.Fg(p.OK).Render(p.I.Done+" ")+bold.Render("Nothing to update")+muted.Render("  ·  taw/core, the framework files and the migrations are current; no branch was left"))
	case "refused":
		out = append(out, " "+p.Fg(p.Err).Render(p.I.Failed+" ")+bold.Render("The update didn't start")+muted.Render("  ·  nothing changed"))
		if rep.Failure != nil {
			out = append(out, wrap("   ", rep.Failure.Reason)...)
		}
	default:
		step := "a step"
		if s := v.steps.failed(); s != nil {
			step = s.Label
		} else if rep.Failure != nil {
			step = rep.Failure.Step
		}
		out = append(out, " "+p.Fg(p.Err).Render(p.I.Failed+" ")+bold.Render("Stopped at "+step)+muted.Render("  ·  nothing was pushed; the work so far is on "+rep.Branch))
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

// batchScreen is an update-all: every theme with its own bar, the one
// running with its current step, and the outcomes.
func (m Model) batchScreen(height int) string {
	p, t := m.pal, m.task
	muted, faint := p.Fg(p.Muted), p.Fg(p.Faint)
	bold := lipgloss.NewStyle().Bold(true)
	now := m.deps.Now()

	done, failed := 0, false
	for _, it := range t.batch {
		if it.state == "done" {
			done++
			if it.view().failed() {
				failed = true
			}
		}
	}
	var state string
	switch {
	case t.running:
		state = m.spin.View() + " " + muted.Render("running  "+clock(now.Sub(t.started)))
	case failed:
		state = p.Fg(p.Warn).Render("▲ done, some stopped") + muted.Render(" in "+clock(t.finished.Sub(t.started)))
	default:
		state = p.Fg(p.OK).Render("✓ done") + muted.Render(" in "+clock(t.finished.Sub(t.started)))
	}
	title := " " + bold.Foreground(p.Accent).Render(p.I.Update+" "+t.title)
	lines := []string{title + strings.Repeat(" ", max(m.width-ansi.StringWidth(title)-ansi.StringWidth(state)-1, 2)) + state}
	for _, l := range t.info {
		lines = append(lines, " "+muted.Render(l))
	}
	lines = append(lines, "")
	frac := float64(done) / float64(max(len(t.batch), 1))
	if t.cur >= 0 { // the running theme counts by its steps
		_, f := t.batch[t.cur].steps.done()
		frac = (float64(done) + f) / float64(max(len(t.batch), 1))
	}
	color := p.Accent
	switch {
	case !t.running && failed:
		color = p.Warn
	case !t.running:
		color = p.OK
	}
	lines = append(lines, "  "+m.bar(frac, max(min(m.width-34, 64), 10), color)+"  "+bold.Render(fmt.Sprintf("%3d%%", int(math.Round(frac*100))))+muted.Render(fmt.Sprintf("  ·  %d of %d themes", done, len(t.batch))), "")

	labelW := 0
	for _, it := range t.batch {
		labelW = max(labelW, ansi.StringWidth(it.Label))
	}
	labelW = min(labelW+2, 40)
	for i, it := range t.batch {
		marker := "  "
		if i == t.sel {
			marker = p.Fg(p.Accent).Render("▌ ")
		}
		icon, label, dur, detail := faint.Render(p.I.Waiting), muted.Render(pad(it.Label, labelW)), "", muted.Render("waiting its turn")
		v := it.view()
		switch it.state {
		case "run":
			_, f := it.steps.done()
			icon, label = m.spin.View(), bold.Render(pad(it.Label, labelW))
			dur = clock(now.Sub(it.started))
			detail = m.bar(f, 14, p.Accent) + fmt.Sprintf(" %3d%%", int(math.Round(f*100)))
			if s := it.steps.current(); s != nil {
				detail += muted.Render("  ·  " + s.Label)
			}
		case "done":
			dur = clock(it.ended.Sub(it.started))
			label = pad(it.Label, labelW)
			icon, detail = m.itemOutcome(v)
		case "none":
			icon, label, detail = faint.Render("·"), faint.Render(pad(it.Label, labelW)), faint.Render("not run")
		}
		lines = append(lines, marker+icon+"  "+label+" "+muted.Render(fmt.Sprintf("%6s", dur))+"   "+detail)
	}
	lines = append(lines, "")

	if t.running {
		lines = append(lines, " "+faint.Render("── output ")+muted.Render("(l the full log  ·  ↑/↓ enter: a theme's steps)")+" "+faint.Render(strings.Repeat("─", max(m.width-60, 0))))
		room := max(height-len(lines), 1)
		var tail []string
		if t.cur >= 0 {
			tail = t.batch[t.cur].lines
		}
		if len(tail) > room {
			tail = tail[len(tail)-room:]
		}
		for _, l := range tail {
			lines = append(lines, "  "+faint.Render(ansi.Strip(l)))
		}
		return block(strings.Join(lines, "\n"), m.width, height)
	}

	// The result: the pull requests, and what stopped.
	lines = append(lines, " "+faint.Render(strings.Repeat("─", max(m.width-2, 0))))
	res, _ := t.summary.Report.(actions.UpdateAllOutcome)
	var prs []string
	dirW := 0
	for _, it := range res.Items {
		dirW = max(dirW, ansi.StringWidth(it.Theme.Dir))
	}
	for _, it := range res.Items {
		if it.Err == nil && it.Report.Delivered != nil && it.Report.Delivered.URL != "" {
			prs = append(prs, "   "+pad(it.Theme.Dir, dirW+3)+p.Fg(p.Info).Render(it.Report.Delivered.URL))
		}
	}
	if len(prs) > 0 {
		lines = append(lines, " "+p.Fg(p.OK).Render(p.I.Done+" ")+bold.Render(fmt.Sprintf("%d %s opened", len(prs), plural(len(prs), "pull request", "pull requests")))+muted.Render("  ·  merge each with M in the table (merging deploys production)"))
		lines = append(lines, prs...)
	}
	if stopped := res.Stopped(); len(stopped) > 0 {
		lines = append(lines, "", " "+p.Fg(p.Err).Render(p.I.Failed+" ")+bold.Render(fmt.Sprintf("%d stopped", len(stopped)))+muted.Render("  ·  nothing of theirs was pushed; enter shows where"))
		lines = append(lines, "   "+bold.Render("1")+" Fix with Claude"+muted.Render(" (a window each, from its report)")+"     "+bold.Render("2")+" Do it myself"+muted.Render(" (their reports, step by step)"))
	}
	if len(prs) == 0 && len(res.Stopped()) == 0 {
		lines = append(lines, " "+muted.Render(t.summary.Headline))
	}
	return block(strings.Join(lines, "\n"), m.width, height)
}

// itemOutcome is a finished theme's icon and one-line outcome.
func (m Model) itemOutcome(v updateView) (string, string) {
	p := m.pal
	muted := p.Fg(p.Muted)
	if v.err != nil || v.outcome == nil {
		msg := "failed"
		if v.err != nil {
			msg = v.err.Error()
		}
		return p.Fg(p.Err).Render(p.I.Failed), p.Fg(p.Err).Render(msg)
	}
	rep := v.outcome.Report
	switch rep.Status {
	case "updated":
		s := "taw/core " + strings.TrimPrefix(rep.Core.From, "v") + " → " + strings.TrimPrefix(rep.Core.To, "v")
		switch {
		case rep.Core.To == "":
			s = "updated"
			if rep.Delivered != nil && rep.Delivered.URL != "" {
				return p.Fg(p.OK).Render(p.I.Done), "pull request " + prNumber(rep.Delivered.URL)
			}
		case rep.Core.From == rep.Core.To:
			s = "framework files and migrations"
		}
		if rep.Delivered != nil && rep.Delivered.URL != "" {
			s += "  ·  pull request " + prNumber(rep.Delivered.URL)
		}
		if n := len(rep.Manual); n > 0 {
			s += muted.Render(fmt.Sprintf("  ·  %d for you", n))
		}
		return p.Fg(p.OK).Render(p.I.Done), s
	case "up-to-date":
		return p.Fg(p.OK).Render(p.I.Done), muted.Render("nothing to update")
	case "refused":
		reason := ""
		if rep.Failure != nil {
			reason = ": " + rep.Failure.Reason
		}
		return p.Fg(p.Err).Render(p.I.Failed), p.Fg(p.Err).Render("didn't start") + muted.Render(reason)
	}
	step := "a step"
	if s := v.steps.failed(); s != nil {
		step = s.Label
	}
	return p.Fg(p.Err).Render(p.I.Failed), p.Fg(p.Err).Render("stopped at "+step) + muted.Render("  ·  nothing pushed")
}

func prNumber(url string) string {
	if i := strings.LastIndex(url, "/"); i >= 0 {
		return "#" + url[i+1:]
	}
	return url
}

// rowShowsResult is how long a finished update's outcome stays on its row.
const rowShowsResult = 15 * time.Minute

// updateFor finds the update of a theme ("siteID/themeDir") the dashboard
// knows about: the one running, a theme of an update-all, or one that
// finished in the last rowShowsResult. waiting: an update-all hasn't reached it.
func (m Model) updateFor(target string) (v updateView, waiting, ok bool) {
	t := m.task
	if t == nil {
		return v, false, false
	}
	switch {
	case t.batch != nil:
		for _, it := range t.batch {
			if it.Target == target && it.state != "none" {
				v, waiting, ok = it.view(), it.state == "wait", true
			}
		}
	case t.steps != nil && t.target == target:
		v, ok = m.taskView(), true
	}
	if ok && !t.running && m.deps.Now().Sub(t.finished) > rowShowsResult {
		return v, false, false
	}
	return v, waiting, ok
}

// rowIcon is the one-cell mark an update leaves on its theme's row: the
// spinner while it runs, then how it ended. The detail pane says the rest.
func (m Model) rowIcon(target string) (string, bool) {
	v, waiting, ok := m.updateFor(target)
	if !ok {
		return "", false
	}
	p := m.pal
	switch {
	case waiting:
		return p.Fg(p.Muted).Render(p.I.Waiting), true
	case v.running:
		return m.spin.View(), true
	case v.failed():
		return p.Fg(p.Err).Render(p.I.Failed), true
	case v.outcome != nil && v.outcome.Report.Status == "up-to-date":
		return p.Fg(p.Muted).Render(p.I.Done), true
	}
	return p.Fg(p.OK).Render(p.I.Done), true
}

// updateSection is the detail pane's UPDATE section for the selected theme:
// its progress, or how it ended.
func (m Model) updateSection(target string, width int) string {
	v, waiting, ok := m.updateFor(target)
	if !ok {
		return ""
	}
	p := m.pal
	muted := p.Fg(p.Muted)
	var lines []string
	switch {
	case waiting:
		lines = append(lines, "  "+muted.Render(p.I.Waiting+" waiting its turn in the update of all themes"))
	case v.running:
		_, frac := v.steps.done()
		lines = append(lines, "  "+m.spin.View()+" "+p.Fg(p.Accent).Render("updating")+muted.Render("  "+clock(m.deps.Now().Sub(v.started))))
		line := "  " + m.bar(frac, min(max(width-16, 10), 30), p.Accent) + " " + fmt.Sprintf("%3d%%", int(math.Round(frac*100)))
		lines = append(lines, line)
		if cur := v.steps.current(); cur != nil {
			lines = append(lines, "  "+muted.Render("now: ")+cur.Label)
		}
	default:
		icon, s := m.itemOutcome(v)
		lines = append(lines, "  "+icon+" "+s)
		if v.outcome != nil {
			rep := v.outcome.Report
			if rep.Delivered != nil && rep.Delivered.URL != "" {
				lines = append(lines, "    "+p.Fg(p.Info).Render(rep.Delivered.URL))
			}
			if n := len(rep.Manual); n > 0 {
				lines = append(lines, "    "+p.Fg(p.Warn).Render(fmt.Sprintf("%d for you", n))+muted.Render(" (in the report)"))
			}
		}
	}
	lines = append(lines, "  "+muted.Render("o shows the update"))
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return render.Section(p, p.I.Update+" UPDATE", p.Accent, "u · this theme", width) + strings.Join(lines, "\n") + "\n\n"
}

// stepSummary is "  64% · Static analysis (PHPStan)" for the status line.
func (t *taskState) stepSummary() string {
	steps := t.steps
	prefix := ""
	if t.batch != nil {
		done := 0
		for _, it := range t.batch {
			if it.state == "done" {
				done++
			}
		}
		prefix = fmt.Sprintf("  %d of %d themes", done, len(t.batch))
		if t.cur < 0 {
			return prefix
		}
		steps = t.batch[t.cur].steps
		prefix += " · " + t.batch[t.cur].Label
	}
	if steps == nil {
		return prefix
	}
	_, frac := steps.done()
	out := prefix + fmt.Sprintf("  %d%%", int(math.Round(frac*100)))
	if s := steps.current(); s != nil {
		out += " · " + s.Label
	}
	return out
}

// clock is a duration as 0:07 or 1:24.
func clock(d time.Duration) string {
	d = d.Round(time.Second)
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}
