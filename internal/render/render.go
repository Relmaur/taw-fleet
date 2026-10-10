// Package render draws the pieces that the CLI and the dashboard share: a
// site's header, its theme cards and a list of findings. Everything returns
// styled strings; callers decide where they go.
package render

import (
	"fmt"
	"image/color"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
)

// Site is the full detail of one site: header, theme cards, findings. width
// caps the cards (0 = as wide as their content).
func Site(p style.Palette, ps paths.Paths, s site.Site, findings []site.Finding, now time.Time, width int) string {
	var b strings.Builder
	b.WriteString(Section(p, p.I.Local+" LOCAL", p.OK, "this Mac · Local", width))
	b.WriteString(Header(p, ps, s, width))
	b.WriteString("\n")
	b.WriteString(Cards(p, ps, s, now, width))
	if s.Production != nil {
		b.WriteString("\n")
		b.WriteString(Section(p, p.I.Live+" LIVE", p.Brand, "production · companion", width))
		b.WriteString(Production(p, s.Production, now, width))
	}
	if s.Feedback != nil {
		b.WriteString("\n")
		b.WriteString(Section(p, p.I.Feedback+" FEEDBACK", p.Accent, "client comments · BugSmash", width))
		b.WriteString(Feedback(p, s.Feedback, now, width))
	}
	b.WriteString("\n")
	b.WriteString(Section(p, p.I.Findings+" FINDINGS", p.Muted, "doctor", width))
	b.WriteString(Findings(p, findings, false, width))
	return b.String()
}

// Section is a labelled rule saying where the lines under it come from:
// " LIVE ──────── production · companion". width 0 draws a 72-cell rule.
func Section(p style.Palette, label string, c color.Color, note string, width int) string {
	if width <= 0 {
		width = 72
	}
	head := " " + lipgloss.NewStyle().Bold(true).Foreground(c).Render(label) + " "
	tail := " " + p.Fg(p.Muted).Render(note)
	fill := width - ansi.StringWidth(head) - ansi.StringWidth(tail)
	if fill < 3 { // too narrow for the note
		tail, fill = "", width-ansi.StringWidth(head)
	}
	return head + p.Fg(p.Faint).Render(strings.Repeat("─", max(fill, 1))) + tail + "\n"
}

// Header is the site's title line and facts.
func Header(p style.Palette, ps paths.Paths, s site.Site, width int) string {
	muted := p.Fg(p.Muted)
	var lines []string
	lines = append(lines, " "+p.Dot(s.Status)+" "+p.Title(s.Slug)+"  "+p.Fg(p.Brand).Render(s.URL)+"  "+p.StatusText(s.Status))
	var facts []string
	add := func(k, v string) {
		if v != "" {
			facts = append(facts, k+" "+v)
		}
	}
	add("PHP", s.PHPVersion)
	add("MySQL", s.MySQLVersion)
	if s.HTTPPort > 0 {
		add(s.WebServer, fmt.Sprintf(":%d", s.HTTPPort))
	}
	if s.MultiSite != "" {
		add("multisite", s.MultiSite)
	}
	add("id", s.ID)
	lines = append(lines, "   "+muted.Render(Tilde(ps, s.Path)), "   "+muted.Render(strings.Join(facts, "  ·  ")))
	for _, h := range s.Hosts {
		lines = append(lines, "   "+muted.Render("connected to "+h.HostID+" "+h.Env))
	}
	if s.Name != "" && s.Name != s.Slug {
		lines = append(lines, "   "+muted.Render("Local name: "+s.Name))
	}
	return fit(lines, width)
}

// Cards draws one bordered card per TAW theme, all the same width.
func Cards(p style.Palette, ps paths.Paths, s site.Site, now time.Time, width int) string {
	muted := p.Fg(p.Muted)
	themes := s.TAWThemes()
	if len(themes) == 0 {
		return " " + muted.Render("No TAW theme in this site.") + "\n"
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Faint).Padding(0, 1).MarginLeft(1)
	key := muted.Width(10)
	var cards []string
	for _, t := range themes {
		var rows []string
		row := func(k, v string) { rows = append(rows, key.Render(k)+" "+v) }

		title := lipgloss.NewStyle().Bold(true).Render(t.Dir) + "  " + p.Kind(t.Kind)
		if s.ActiveTheme == t.Dir {
			title += "  " + p.Fg(p.OK).Render("✓ active")
		}
		if t.Symlink {
			title += "  " + muted.Render("↗ symlink")
		}
		rows = append(rows, title, "")
		row("path", themePath(ps, s, t))
		if t.Symlink {
			row("links to", Tilde(ps, t.RealPath))
		}

		core := p.Core(t.Core)
		var extra []string
		if t.Core.Locked != "" && t.Core.LockMismatch {
			extra = append(extra, "lock "+strings.TrimPrefix(t.Core.Locked, "v"))
		}
		if t.Core.Latest != "" && !t.Core.Behind {
			extra = append(extra, "latest")
		}
		if g := t.Git; g != nil && !g.Detached && g.DefaultBranch != "" && g.Branch != g.DefaultBranch {
			extra = append(extra, "from branch "+g.Branch)
		}
		if len(extra) > 0 {
			core += muted.Render("  (" + strings.Join(extra, ", ") + ")")
		}
		row("taw/core", core)

		if t.Git == nil {
			row("git", muted.Render("not its own repository"))
		} else {
			gi := t.Git
			state := p.Git(gi)
			switch {
			case gi.ForeignUpstream != "":
				state += p.Fg(p.Warn).Render("  tracks " + gi.ForeignUpstream)
			case gi.Upstream != "":
				state += muted.Render("  tracking " + gi.Upstream)
			}
			if gi.DefaultBranch != "" && gi.Branch != gi.DefaultBranch && !gi.Detached {
				state += muted.Render("  (default " + gi.DefaultBranch + ")")
			}
			row("git", state)
			if t.Account != "" {
				row("account", p.Fg(p.Brand).Render("@"+t.Account)+muted.Render("  another GitHub account"))
			}
			if gi.Repo != nil {
				row("repo", p.Fg(p.Brand).Render(strings.TrimPrefix(gi.Repo.WebURL(), "https://")))
			} else if gi.RemoteURL != "" {
				row("remote", gi.RemoteURL)
			}
			ver := gi.Describe
			if !gi.LastCommit.IsZero() {
				ver += muted.Render("  ·  last commit " + Ago(now, gi.LastCommit))
			}
			row("version", ver)
		}
		bin := p.Fg(p.OK).Render("yes")
		if !t.HasBinTaw {
			bin = muted.Render("no")
		}
		row("bin/taw", bin)
		if t.Dev != "" {
			row("vite", p.Fg(p.Accent).Render(t.Dev))
		}
		for i, l := range GitHubLines(p, t.GitHub, now) {
			k := ""
			switch {
			case i == 0 && strings.HasPrefix(l.Key, "pr"):
				k = "PRs"
			case l.Key == "deploy":
				k = "deploy"
			}
			row(k, l.Text)
		}
		cards = append(cards, strings.Join(rows, "\n"))
	}

	// One width for every card, so they line up; never wider than width.
	inner := 0
	for _, c := range cards {
		inner = max(inner, lipgloss.Width(c))
	}
	if width > 0 {
		inner = min(inner, max(width-5, 10)) // margin 1 + border 2 + padding 2
	}
	var b strings.Builder
	for _, c := range cards {
		lines := strings.Split(c, "\n")
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, inner, "…")
		}
		b.WriteString(box.Render(lipgloss.NewStyle().Width(inner).Render(strings.Join(lines, "\n"))) + "\n")
	}
	return b.String()
}

// Findings lists findings: icon, message, where and code, then the fix.
func Findings(p style.Palette, fs []site.Finding, withSite bool, width int) string {
	muted := p.Fg(p.Muted)
	if len(fs) == 0 {
		return " " + p.Fg(p.OK).Render("✓ Nothing to report.") + "\n"
	}
	var lines []string
	for _, f := range fs {
		where := f.Theme
		if withSite && f.Site != "" {
			where = f.Site
			if f.Theme != "" {
				where += "/" + f.Theme
			}
		}
		line := "   " + p.Severity(f.Severity) + " " + f.Message
		if where != "" {
			line += "  " + muted.Render(where)
		}
		line += "  " + p.Fg(p.Faint).Render(f.Code)
		lines = append(lines, line)
		if f.Fix != "" {
			lines = append(lines, "     "+muted.Render("→ "+f.Fix))
		}
	}
	return fit(lines, width)
}

// themePath is relative to the site's WordPress root when it's inside it
// (the header already shows the site folder).
func themePath(ps paths.Paths, s site.Site, t site.Theme) string {
	if s.WebRoot != "" {
		if rel, err := filepath.Rel(s.WebRoot, t.Path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return Tilde(ps, t.Path)
}

// fit joins lines, truncating each to width (0 = no limit), with a final newline.
func fit(lines []string, width int) string {
	if width > 0 {
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, width, "…")
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// Tilde shortens a path under the home folder to ~/… (also when the path
// went through symlinks, e.g. /var → /private/var on macOS).
func Tilde(ps paths.Paths, path string) string {
	if ps.Home == "" {
		return path
	}
	homes := []string{ps.Home}
	if resolved, err := filepath.EvalSymlinks(ps.Home); err == nil && resolved != ps.Home {
		homes = append(homes, resolved)
	}
	for _, h := range homes {
		if strings.HasPrefix(path, h+"/") {
			return "~" + path[len(h):]
		}
	}
	return path
}

// Ago is a short relative time: "just now", "5 minutes ago", "3 days ago".
func Ago(now, then time.Time) string {
	d := now.Sub(then)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return unit(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return unit(int(d.Hours()), "hour")
	case d < 60*24*time.Hour:
		return unit(int(d.Hours()/24), "day")
	case d < 365*24*time.Hour:
		return unit(int(d.Hours()/24/30), "month")
	}
	return unit(int(d.Hours()/24/365), "year")
}

func unit(n int, name string) string {
	if n == 1 {
		return "1 " + name + " ago"
	}
	return fmt.Sprintf("%d %ss ago", n, name)
}

// Feedback is the site's BugSmash block: the open review comments.
func Feedback(p style.Palette, f *site.Feedback, now time.Time, width int) string {
	muted := p.Fg(p.Muted)
	dot := p.Fg(p.OK).Render("●") // nothing open
	switch {
	case f.Error != "":
		dot = p.Fg(p.Faint).Render("?")
	case f.Stale():
		dot = p.Fg(p.Warn).Render("●")
	case f.Open > 0:
		dot = p.Fg(p.Accent).Render("●")
	}
	project := f.Project
	if project == "" {
		project = "BugSmash"
	}
	lines := []string{" " + dot + " " + lipgloss.NewStyle().Bold(true).Render(project) + muted.Render("  checked "+Ago(now, f.CheckedAt))}
	switch {
	case f.Error != "":
		lines = append(lines, "   "+p.Fg(p.Warn).Render(f.Error))
		return fit(lines, width)
	case f.Open == 0:
		lines = append(lines, "   "+muted.Render("no open comments in BugSmash"))
		return fit(lines, width)
	}
	sum := fmt.Sprintf("%d open %s", f.Open, plural(f.Open, "comment", "comments"))
	if !f.Oldest.IsZero() {
		age := muted.Render(", the oldest " + Ago(now, f.Oldest))
		if f.Stale() {
			age = p.Fg(p.Warn).Render(", the oldest " + Ago(now, f.Oldest))
		}
		sum += age
	}
	lines = append(lines, "   "+sum)
	for _, c := range f.Comments[:min(len(f.Comments), 3)] {
		lines = append(lines, Quote(p, c, now)...)
	}
	if more := f.Open - min(len(f.Comments), 3); more > 0 {
		lines = append(lines, "   "+muted.Render(fmt.Sprintf("… %d more: taw-fleet comments", more)))
	}
	return fit(lines, width)
}

// Quote is one comment as a quote: its text after a bar, then its number,
// page and age underneath.
func Quote(p style.Palette, c site.Comment, now time.Time) []string {
	muted := p.Fg(p.Muted)
	meta := p.Fg(p.Accent).Render(fmt.Sprintf("#%d", c.Number))
	if path := pagePath(c.Page); path != "" {
		meta += muted.Render(" · " + path)
	}
	if c.Author != "" {
		meta += muted.Render(" · " + c.Author)
	}
	meta += muted.Render(" · " + Ago(now, c.CreatedAt))
	return []string{"   " + p.Fg(p.Accent).Render("▎") + " " + c.Text, "     " + meta}
}

// CommentLine is one comment in a line: "#108 /credito-pyme/ Actualizar
// texto por… · 2 days ago", the text cut to fit width (0 = no limit).
func CommentLine(p style.Palette, c site.Comment, now time.Time, width int) string {
	muted := p.Fg(p.Muted)
	left := p.Fg(p.Accent).Render(fmt.Sprintf("#%d", c.Number))
	if path := pagePath(c.Page); path != "" {
		left += " " + muted.Render(path)
	}
	right := muted.Render(" · " + Ago(now, c.CreatedAt))
	text := c.Text
	if width > 0 {
		room := width - ansi.StringWidth(left) - ansi.StringWidth(right) - 1
		text = ansi.Truncate(text, max(room, 8), "…")
	}
	return left + " " + text + right
}

// pagePath is a comment's page as a path ("/credito-pyme/"), or the URL as
// given when it doesn't parse.
func pagePath(u string) string {
	pu, err := url.Parse(u)
	if err != nil || pu.Path == "" {
		return u
	}
	return pu.Path
}

// Production is the live site's block: what its companion said.
func Production(p style.Palette, r *site.Production, now time.Time, width int) string {
	muted := p.Fg(p.Muted)
	lines := []string{" " + p.Live(r) + " " + lipgloss.NewStyle().Bold(true).Foreground(p.Brand).Render(r.URL) +
		muted.Render("  checked "+Ago(now, r.CheckedAt))}
	if !r.Reachable {
		lines = append(lines, "   "+p.Fg(p.Err).Render(r.Error))
		return fit(lines, width)
	}
	trust := p.Fg(p.OK).Render("verified")
	if !r.Verified {
		trust = p.Fg(p.Warn).Render("not verified")
	}
	lines = append(lines, "   "+muted.Render("WordPress ")+r.WP+muted.Render("  ·  PHP ")+r.PHP+muted.Render("  ·  taw/core ")+strings.TrimPrefix(r.TawCore, "v"))
	lines = append(lines, "   "+muted.Render("companion ")+r.Companion+muted.Render("  ·  ")+trust)
	if r.HasInventory {
		up := muted.Render("no updates waiting")
		if n := len(r.PluginUpdates); n > 0 {
			up = p.Fg(p.Warn).Render(fmt.Sprintf("%d %s waiting", n, plural(n, "update", "updates")))
		}
		lines = append(lines, "   "+fmt.Sprintf("%d plugins", r.Plugins)+muted.Render("  ·  ")+up)
	}
	if r.HasVulns {
		switch {
		case len(r.Vulns) > 0:
			c := p.Warn
			if r.WorstSeverity == "high" || r.WorstSeverity == "critical" {
				c = p.Err
			}
			lines = append(lines, "   "+p.Fg(c).Render(fmt.Sprintf("%d known %s (worst %s)", len(r.Vulns), plural(len(r.Vulns), "vulnerability", "vulnerabilities"), r.WorstSeverity))+muted.Render(" per "+r.Scanner))
		case r.Scanner != "":
			lines = append(lines, "   "+muted.Render("no known vulnerabilities, per "+r.Scanner))
		}
	}
	for _, l := range r.Logs {
		lines = append(lines, "   "+muted.Render(shortTS(l.TS)+" ")+l.Level+" "+l.Code+muted.Render(" "+l.Message))
	}
	return fit(lines, width)
}

func shortTS(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Local().Format("Jan 2 15:04")
	}
	return ts
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Line is one GitHub line of a theme: Key says what it's about ("pr",
// "deploy", "pending" for an undeployed commit).
type Line struct{ Key, Text string }

// GitHubLines describes a theme's open pull requests and its deploy.
func GitHubLines(p style.Palette, st *site.RepoState, now time.Time) []Line {
	if st == nil {
		return nil
	}
	muted := p.Fg(p.Muted)
	if st.Error != "" {
		return []Line{{"pr", muted.Render("GitHub: " + st.Error)}}
	}
	var out []Line
	for _, pr := range st.PRs {
		var state string
		switch {
		case pr.Draft:
			state = muted.Render("draft")
		case pr.Conflicted:
			state = p.Fg(p.Warn).Render("! conflicts")
		case pr.Checks == site.ChecksFailing:
			state = p.Fg(p.Err).Render("✗ CI failing")
		case pr.Checks == site.ChecksPending:
			state = p.Fg(p.Warn).Render("… CI running")
		case pr.Checks == site.ChecksPassing:
			state = p.Fg(p.OK).Render("✓ CI passed")
		default:
			state = muted.Render("no CI")
		}
		out = append(out, Line{"pr", fmt.Sprintf("#%d %s  %s  %s", pr.Number, pr.Title, state, muted.Render(pr.Branch))})
	}
	if len(st.PRs) == 0 {
		out = append(out, Line{"pr", muted.Render("none open")})
	}
	d := st.Deploy
	if d == nil {
		return append(out, Line{"deploy", muted.Render("no deploy workflow")})
	}
	if d.Running != nil {
		out = append(out, Line{"deploy", p.Fg(p.Accent).Render("⟳ deploying "+short(d.Running.SHA)) + muted.Render(", started "+Ago(now, d.Running.Started))})
	}
	if d.Failed != nil {
		out = append(out, Line{"deploy", p.Fg(p.Err).Render("✗ deploy of "+short(d.Failed.SHA)+" failed") + muted.Render(" "+Ago(now, d.Failed.Started))})
	}
	switch d.Deployed {
	case "":
		out = append(out, Line{"deploy", muted.Render("never deployed successfully")})
	case st.Head:
		out = append(out, Line{"deploy", p.Fg(p.OK).Render("✓ production has "+st.Default) + muted.Render(" ("+short(d.Deployed)+", "+Ago(now, d.DeployedAt)+")")})
	default:
		why := ""
		switch st.HeadCI {
		case site.ChecksPending:
			why = ", CI running"
		case site.ChecksFailing:
			why = ", CI failed"
		}
		out = append(out, Line{"deploy", p.Fg(p.Warn).Render(fmt.Sprintf("↑%d on %s, not deployed%s", d.Behind, st.Default, why)) +
			muted.Render(" (production: "+short(d.Deployed)+", "+Ago(now, d.DeployedAt)+")")})
		for _, c := range d.Pending {
			out = append(out, Line{"pending", muted.Render(short(c.SHA)) + " " + c.Title})
		}
	}
	return out
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
