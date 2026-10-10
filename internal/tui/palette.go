package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// The : menu and the a picker share one palette: a list on the left (with
// group headings, badges and keys), the highlighted entry's details on the
// right, a fuzzy filter on top. Unavailable entries stay listed, dimmed,
// with the reason.

// paletteItem is one entry.
type paletteItem struct {
	group    string
	title    string
	key      string // the dashboard key it stands for ("" for skills)
	badge    string // action, skill, site skill
	alias    string // the key's short help label, also searched
	detail   string // what it does, in sentences
	extra    []string
	disabled string // why it can't run now ("" = it can)
	skill    *actions.Skill
}

// actionDoc is the : menu's catalog: every action, its group and what it does.
type actionDoc struct {
	key, group, title, detail string
}

var actionDocs = []actionDoc{
	{"enter", "Site · Local", "Details", "The selected site in full: Local, its themes, production, BugSmash and what doctor says. Scroll with ↑/↓, esc goes back."},
	{"w", "Site · Local", "Work on it", "Start the site (when stopped), open the theme in your editor, run Vite in its own window and open the site once Vite answers. Again on a theme whose Vite runs stops Vite and the site."},
	{"s", "Site · Local", "Start or stop", "Start or stop the site through the Local app (it must be open). Asks first. Leftover Local processes in the way are named, and it offers to end them."},
	{"R", "Site · Local", "Restart", "Stop, then start the site through Local. Asks first."},
	{"b", "Site · Local", "Open the site", "The Local site in your browser."},
	{"B", "Site · Local", "Open wp-admin", "The Local site's wp-admin in your browser."},
	{"n", "Site · Local", "New site", "Create a Local site with a TAW theme (classic or block): name, domain, PHP, admin user. The password is generated and shown once."},
	{"P", "Site · Production", "Open production", "The production site in your browser."},
	{"L", "Site · Production", "Check production now", "Ask every production site's companion, BugSmash and GitHub again, past the 5-minute cache."},
	{"C", "Site · Production", "Pull production content", "Bring the production site's content into the Local site: a preview of what would change first, then it asks before importing (a rollback snapshot is kept)."},
	{"F", "Site · Feedback", "Open BugSmash comments", "The site's review page in BugSmash, where the client's comments are."},
	{"X", "Site · Feedback", "Resolve comments with Claude", "Claude Code beside the dashboard, in the theme folder, with the resolve-comments skill on the site's open BugSmash comments. It plans first and asks before anything reaches production."},
	{"e", "Theme · Code", "Open in editor", "The theme folder in your editor (setting: editor)."},
	{"f", "Theme · Code", "Show in Finder", "The theme folder in Finder."},
	{"t", "Theme · Code", "Open a terminal", "A terminal window in the theme folder (setting: terminal)."},
	{"a", "Theme · Code", "Ask Claude…", "Pick one of the theme's skills; Claude Code starts on it in the theme folder, beside the dashboard, with what taw-fleet knows about the site."},
	{"u", "Theme · Update", "Update this site", "The whole update, as the theme's taw.json says (vendor/bin/taw update): on a new branch, taw/core, the framework files, the migrations and the checks, then a pull request. One question first. If it stops, Fix with Claude or Do it myself from the same guide."},
	{"U", "Theme · Update", "Update all", "Every client theme that needs an update, one after another, each as its taw.json says (vendor/bin/taw update), each ending in its own pull request. One question first, listing what it leaves out (uncommitted changes, another branch). Themes that stop: Fix with Claude or Do it myself."},
	{"A", "Theme · Update", "Update with an agent", "Claude Code beside the dashboard updates this theme's taw/core and scaffold with the update prompt (what h shows)."},
	{"h", "Theme · Update", "Hand off the update", "Show the update prompt for this theme; A sends it to Claude Code, c copies it."},
	{"y", "Theme · Update", "Check against the scaffold", "Compare the theme with the taw-theme scaffold (bin/taw sync): which framework files differ (Tier 1) and which to review (Tier 2). Read-only."},
	{"Y", "Theme · Update", "Check every theme", "The scaffold check on every classic TAW theme at once, three at a time. Keeps the SYNC column current."},
	{"S", "Theme · Update", "Apply the scaffold (Tier 1)", "Write the framework-owned files that differ from the scaffold (bin/, workflows, framework skills…). Asks first; Tier 2 stays for review."},
	{"g", "Theme · Git & GitHub", "Open the repository", "The theme's GitHub repository in your browser."},
	{"G", "Theme · Git & GitHub", "Open pull requests", "The theme's open pull requests on GitHub."},
	{"M", "Theme · Git & GitHub", "Merge a pull request", "Merge the theme's pull request (asks which when there are several, then asks again: merging a client theme deploys production), then follow the deploy."},
	{"D", "Theme · Git & GitHub", "Back to the site's branch", "Switch the theme to its default branch (asks first). A branch that tracks another repository (a local copy of the scaffold's) is deleted afterwards when it has nothing of its own; other branches are kept. Refused with uncommitted changes."},
	{"r", "App", "Refresh", "Read Local again now (it also refreshes every minute)."},
	{"ctrl+r", "App", "Refresh everything", "Everything for every site, past the caches: Local, the newest versions, pull requests and deploys, production, BugSmash, then the scaffold check of every theme."},
	{"o", "App", "Last output", "The last sync, update or create output again."},
	{"/", "App", "Filter the list", "Filter by site, theme, branch or version, or by behind, dirty, unpushed, running, live, vite, other, classic, block."},
	{"?", "App", "All keys and symbols", "Every key and what the table's symbols mean."},
	{"q", "App", "Quit", "Close the dashboard."},
}

// paletteGroups is the : menu's group order.
var paletteGroups = []string{"Recent", "Site · Local", "Site · Production", "Site · Feedback", "Theme · Code", "Theme · Update", "Theme · Git & GitHub", "App"}

// actionItems are the : menu's entries, each with why it can't run now.
func (m Model) actionItems() []paletteItem {
	s, t, ok := m.selectedTheme()
	labels := map[string]string{}
	for _, col := range m.keys.FullHelp() {
		for _, b := range col {
			if len(b.Keys()) > 0 {
				labels[b.Keys()[0]] = b.Help().Desc
			}
		}
	}
	out := make([]paletteItem, 0, len(actionDocs))
	for _, d := range actionDocs {
		it := paletteItem{group: d.group, title: d.title, key: d.key, badge: "action", detail: d.detail, alias: labels[d.key]}
		if ok {
			it.disabled = m.unavailable(d.key, s, t)
		} else if d.group != "App" && d.key != "n" {
			it.disabled = "no site selected"
		}
		out = append(out, it)
	}
	return out
}

// unavailable says why an action can't run on the selected theme now.
func (m Model) unavailable(k string, s site.Site, t site.Theme) string {
	prod := m.deps.Production[s.Slug]
	switch k {
	case "M":
		if t.GitHub == nil || len(t.GitHub.PRs) == 0 {
			return "no open pull request"
		}
	case "g", "G":
		if t.Git == nil || t.Git.Repo == nil {
			return "no GitHub repository (no origin remote)"
		}
	case "P", "C":
		if !prod {
			return "no production URL: add production_url to [sites." + s.Slug + "]"
		}
	case "F":
		if s.Feedback == nil {
			return "no BugSmash project: add bugsmash_project to [sites." + s.Slug + "]"
		}
	case "X":
		switch {
		case s.Feedback == nil:
			return "no BugSmash project: add bugsmash_project to [sites." + s.Slug + "]"
		case s.Feedback.Open == 0:
			return "no open comments"
		case !actions.HasSkill(t, actions.ResolveSkill):
			return "the theme hasn't got the resolve-comments skill yet (taw/core 1.89+, then S)"
		}
	case "a":
		if len(actions.Skills(t)) == 0 {
			return "no skills in the theme's .claude/skills/ yet (S syncs them)"
		}
	case "A":
		// u stays available: on a current theme it still checks the
		// framework files and the migrations, and says so.
		if t.Core.Latest != "" && !handoff.Needs(t) {
			return "taw/core is already the newest (" + strings.TrimPrefix(t.Core.Latest, "v") + ") and the scaffold matches"
		}
	case "S", "y":
		if t.Kind == site.KindGutenberg {
			return "block themes don't sync the classic scaffold (php bin/taw skills:sync --apply for skills)"
		}
	case "D":
		switch g := t.Git; {
		case g == nil:
			return "the theme isn't its own git repository"
		case g.DefaultBranch == "":
			return "its default branch isn't known"
		case !g.Detached && g.Branch == g.DefaultBranch:
			return "already on " + g.DefaultBranch
		case g.Dirty > 0:
			return "uncommitted changes: commit or stash first"
		}
	case "o":
		if m.task == nil {
			return "nothing has run yet"
		}
	case "L":
		if m.deps.Live == nil && m.deps.GitHub == nil && m.deps.Feedback == nil {
			return "no production, GitHub or BugSmash in the config"
		}
	}
	return ""
}

// skillItems are the a picker's entries: the theme's skills, with where
// each comes from and when to use it.
func (m Model) skillItems() []paletteItem {
	_, t, _ := m.selectedTheme()
	core := map[string]bool{}
	if es, err := os.ReadDir(filepath.Join(t.RealPath, "vendor", "taw", "core", "resources", "skills")); err == nil {
		for _, e := range es {
			core[e.Name()] = true
		}
	}
	out := make([]paletteItem, 0, len(m.skills))
	for i := range m.skills {
		sk := &m.skills[i]
		what, when := splitTriggers(sk.Description)
		it := paletteItem{group: "This site's skills", title: sk.Name, badge: "skill", detail: what, skill: sk}
		switch {
		case sk.Owner == "site":
			it.badge = "site skill"
			it.extra = append(it.extra, "From: this site (owner: site), written for it")
		case core[filepath.Base(sk.Dir)]:
			it.extra = append(it.extra, "From: taw/core (every TAW site has it)")
		default:
			it.extra = append(it.extra, "From: the theme's scaffold")
		}
		if when != "" {
			it.extra = append(it.extra, "Ask for it with: "+when)
		}
		it.extra = append(it.extra, "Claude Code starts in: "+t.Dir+", beside the dashboard, with the site's URLs, repository, BugSmash project and findings")
		out = append(out, it)
	}
	return out
}

// splitTriggers splits a skill description at "Triggers on:".
func splitTriggers(d string) (what, when string) {
	if i := strings.Index(d, "Triggers on"); i >= 0 {
		when = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(d[i:], "Triggers on"), ":"))
		return strings.TrimSpace(d[:i]), strings.TrimSuffix(when, ".")
	}
	return strings.TrimSpace(d), ""
}

// paletteItems are the open palette's entries, filtered: grouped (recent
// first) with no filter, else ranked by match.
func (m Model) paletteItems() []paletteItem {
	var all []paletteItem
	if m.mode == modeSkills {
		all = m.skillItems()
	} else {
		all = m.actionItems()
	}
	q := strings.ToLower(strings.TrimSpace(m.menuInput.Value()))
	if q == "" {
		if m.mode == modeMenu {
			var recent []paletteItem
			for _, k := range m.recent {
				for _, it := range all {
					if it.key == k {
						it.group = "Recent"
						recent = append(recent, it)
					}
				}
			}
			all = append(recent, all...)
			sort.SliceStable(all, func(i, j int) bool { return groupRank(all[i].group) < groupRank(all[j].group) })
		}
		return all
	}
	type scored struct {
		it    paletteItem
		score int
	}
	var hits []scored
	for _, it := range all {
		if sc, ok := matchScore(q, it); ok {
			hits = append(hits, scored{it, sc})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score < hits[j].score })
	out := make([]paletteItem, len(hits))
	for i, h := range hits {
		h.it.group = ""
		out[i] = h.it
	}
	return out
}

func groupRank(g string) int {
	for i, x := range paletteGroups {
		if x == g {
			return i
		}
	}
	return len(paletteGroups)
}

// matchScore ranks an item for the filter (lower is better): the key itself,
// then the title containing the query, then its letters in order, then the
// description.
func matchScore(q string, it paletteItem) (int, bool) {
	title := strings.ToLower(it.title)
	switch {
	case it.key != "" && q == strings.ToLower(it.key):
		return 0, true
	case strings.HasPrefix(title, q):
		return 1, true
	case strings.Contains(title, q):
		return 2 + strings.Index(title, q), true
	case it.alias != "" && strings.Contains(strings.ToLower(it.alias), q):
		return 50 + strings.Index(strings.ToLower(it.alias), q), true
	}
	if idx := subsequence(title, q); idx != nil {
		return 100 + idx[len(idx)-1] - idx[0], true
	}
	if strings.Contains(strings.ToLower(it.detail+" "+strings.Join(it.extra, " ")), q) {
		return 1000, true
	}
	return 0, false
}

// subsequence is where q's runes appear in order in s (rune indexes), or nil.
func subsequence(s, q string) []int {
	var idx []int
	qr := []rune(q)
	j := 0
	for i, r := range []rune(s) {
		if j < len(qr) && r == qr[j] {
			idx = append(idx, i)
			j++
		}
	}
	if j < len(qr) {
		return nil
	}
	return idx
}

// highlight bolds the query's matched letters in a title.
func (m Model) highlight(title, q string, base lipgloss.Style) string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return base.Render(title)
	}
	marks := map[int]bool{}
	lower := strings.ToLower(title)
	if i := strings.Index(lower, q); i >= 0 {
		start := utf8.RuneCountInString(lower[:i])
		for k := range utf8.RuneCountInString(q) {
			marks[start+k] = true
		}
	} else {
		for _, i := range subsequence(lower, q) {
			marks[i] = true
		}
	}
	hit := base.Foreground(m.pal.Accent).Bold(true).Underline(true)
	var b strings.Builder
	for i, r := range []rune(title) {
		if marks[i] {
			b.WriteString(hit.Render(string(r)))
		} else {
			b.WriteString(base.Render(string(r)))
		}
	}
	return b.String()
}

// paletteScreen draws the open palette.
func (m Model) paletteScreen(h int) string {
	p := m.pal
	muted := p.Fg(p.Muted)
	w := min(max(m.width-6, 50), 150)
	inner := w - 6 // the box's border and padding
	head, placeholder := "All actions", "type to find an action: a word, its key, or letters in order"
	if m.mode == modeSkills {
		head, placeholder = "Ask Claude", "type to find a skill: what you want done, or its name"
	}
	sub := ""
	if s, t, ok := m.selectedTheme(); ok {
		sub = "  on " + s.Slug + " · " + t.Dir
		if m.mode == modeSkills {
			sub = "  in " + t.Dir + " (" + s.Slug + "), with one of its skills"
		}
	}

	items := m.paletteItems()
	m.menuCursor = min(m.menuCursor, max(len(items)-1, 0))
	listW := min(max(inner*9/20, 34), 62)
	sideW := inner - listW - 3
	twoPane := sideW >= 30
	if !twoPane {
		listW = inner
	}
	rows := max(h-9, 6)

	// The list: headings, then entries; scrolled to keep the cursor in view.
	type line struct {
		text string
		item int // -1 for a heading or spacer
	}
	var lines []line
	last := "\x00"
	for i, it := range items {
		if it.group != last && it.group != "" {
			if len(lines) > 0 {
				lines = append(lines, line{"", -1})
			}
			lines = append(lines, line{muted.Bold(true).Render(m.groupIcon(it.group) + " " + strings.ToUpper(it.group)), -1})
		}
		last = it.group
		lines = append(lines, line{m.paletteRow(it, i == m.menuCursor, listW), i})
	}
	cursorLine := 0
	for i, l := range lines {
		if l.item == m.menuCursor {
			cursorLine = i
		}
	}
	first := 0
	if cursorLine >= rows {
		first = cursorLine - rows + 1
	}
	var left []string
	for i := first; i < len(lines) && i < first+rows; i++ {
		left = append(left, lines[i].text)
	}
	if len(items) == 0 {
		what := "action"
		if m.mode == modeSkills {
			what = "skill"
		}
		left = append(left, muted.Render("  No "+what+" matches “"+strings.TrimSpace(m.menuInput.Value())+"”."))
	}
	if more := len(lines) - (first + rows); more > 0 {
		left = append(left, muted.Render("  ↓ more"))
	}

	input := m.menuInput
	input.Placeholder = placeholder
	input.SetWidth(inner - 4)
	top := []string{
		lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render(head) + muted.Render(sub),
		"",
		input.View(),
		p.Fg(p.Faint).Render(strings.Repeat("─", inner)),
	}
	body := strings.Join(left, "\n")
	if twoPane {
		var side string
		if m.menuCursor < len(items) {
			side = m.paletteDetail(items[m.menuCursor], sideW)
		}
		sep := strings.TrimRight(strings.Repeat(p.Fg(p.Faint).Render(" │ ")+"\n", max(len(left), strings.Count(side, "\n")+1)), "\n")
		body = lipgloss.JoinHorizontal(lipgloss.Top, block(body, listW, max(len(left), 1)), sep, side)
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Faint).Padding(1, 2).Width(w)
	return lipgloss.Place(m.width, h, lipgloss.Center, lipgloss.Top, "\n"+box.Render(strings.Join(top, "\n")+"\n"+body))
}

// paletteRow is one entry in the list: marker, title (matches highlighted),
// then the key and badge at the right edge.
func (m Model) paletteRow(it paletteItem, sel bool, w int) string {
	p := m.pal
	base := lipgloss.NewStyle()
	if it.disabled != "" {
		base = p.Fg(p.Muted)
	}
	if sel {
		base = base.Bold(true)
	}
	marker := "  "
	if sel {
		marker = p.Fg(p.Accent).Render("▌ ")
	}
	right := m.badge(it.badge)
	if it.key != "" {
		right = p.Fg(p.Accent).Render(pad(it.key, 6)) + " " + right
	}
	title := m.highlight(it.title, m.menuInput.Value(), base)
	room := w - ansi.StringWidth(marker) - ansi.StringWidth(right) - 1
	title = ansi.Truncate(title, max(room, 8), "…")
	gap := max(w-ansi.StringWidth(marker)-ansi.StringWidth(title)-ansi.StringWidth(right), 1)
	return marker + title + strings.Repeat(" ", gap) + right
}

// badge is a small colored label: action, skill, site skill.
func (m Model) badge(b string) string {
	p := m.pal
	c := p.Muted
	switch b {
	case "skill":
		c = p.Accent
	case "site skill":
		c = p.Brand
	}
	return p.Fg(c).Render("‹" + b + "›")
}

// paletteDetail is the right pane: the entry in words.
func (m Model) paletteDetail(it paletteItem, w int) string {
	p := m.pal
	muted := p.Fg(p.Muted)
	wrap := lipgloss.NewStyle().Width(w)
	head := lipgloss.NewStyle().Bold(true).Render(it.title) + "  " + m.badge(it.badge)
	if it.key != "" {
		head += "  " + muted.Render("key ") + p.Fg(p.Accent).Render(it.key)
	}
	parts := []string{head, ""}
	if it.disabled != "" {
		parts = append(parts, wrap.Render(p.Fg(p.Warn).Render("Not now: "+it.disabled)), "")
	}
	if it.detail != "" {
		parts = append(parts, wrap.Render(it.detail))
	}
	for _, e := range it.extra {
		k, v, ok := strings.Cut(e, ": ")
		if !ok {
			parts = append(parts, "", wrap.Render(muted.Render(e)))
			continue
		}
		parts = append(parts, "", muted.Render(k), wrap.Render(v))
	}
	return strings.Join(parts, "\n")
}

// groupIcon marks a palette group's heading.
func (m Model) groupIcon(group string) string {
	i := m.pal.I
	switch group {
	case "Site · Local":
		return i.Local
	case "Site · Production":
		return i.Production
	case "Site · Feedback":
		return i.Feedback
	case "Theme · Code":
		return i.Editor
	case "Theme · Update":
		return i.Update
	case "Theme · Git & GitHub":
		return i.Branch
	case "App":
		return i.App
	}
	return i.Waiting // recent, and skills
}
