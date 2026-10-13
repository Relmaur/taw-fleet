package launch

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Files are the report's names in the site's folder.
const (
	HTMLFile = "launch.html"
	MDFile   = "launch.md"
	ShotsDir = "production" // pictures of production; the Local build's are in build/
	BuildDir = "build"
)

var imageExt = []string{".png", ".jpg", ".jpeg", ".webp"}

// Save writes PageSpeed's pictures of production's first screen into
// production/ (home-<mobile|desktop>.pagespeed.<ext>), pairs every picture
// there with the build's of the same page, and writes the report as Markdown
// and HTML. It returns the HTML's path.
func Save(d *Data) (string, error) {
	if d.Dir == "" {
		return "", fmt.Errorf("no folder for %s's report", d.Site)
	}
	shots := filepath.Join(d.Dir, ShotsDir)
	for _, r := range d.Speed {
		if len(r.Screenshot) == 0 {
			continue
		}
		if err := os.MkdirAll(shots, 0o755); err != nil {
			return "", err
		}
		base := "home-" + r.Strategy + PageSpeedMark
		for _, ext := range imageExt {
			_ = os.Remove(filepath.Join(shots, base+ext))
		}
		ext := "." + r.ScreenshotType
		if r.ScreenshotType == "jpeg" {
			ext = ".jpg"
		}
		if err := os.WriteFile(filepath.Join(shots, base+ext), r.Screenshot, 0o644); err != nil {
			return "", err
		}
	}
	d.Shots = pair(d.Dir)
	if err := os.WriteFile(filepath.Join(d.Dir, MDFile), []byte(Markdown(*d)), 0o644); err != nil {
		return "", err
	}
	page := filepath.Join(d.Dir, HTMLFile)
	return page, os.WriteFile(page, []byte(HTML(*d)), 0o644)
}

// PageSpeedMark ends the names of PageSpeed's pictures: a page's picture
// taken in a browser (the new-site flow's, full page) is used instead.
const PageSpeedMark = ".pagespeed"

// pair is every page's picture in production/ with the build's of the same
// page: a browser's picture over PageSpeed's.
func pair(dir string) []Shot {
	entries, _ := os.ReadDir(filepath.Join(dir, ShotsDir))
	byPage := map[string]Shot{}
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || !isImage(ext) {
			continue
		}
		base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		name, fromPS := strings.CutSuffix(base, PageSpeedMark)
		if had, ok := byPage[name]; ok && (fromPS || !had.PageSpeed) {
			continue
		}
		s := Shot{Name: name, Production: ShotsDir + "/" + e.Name(), PageSpeed: fromPS}
		for _, x := range imageExt {
			if _, err := os.Stat(filepath.Join(dir, BuildDir, name+x)); err == nil {
				s.Build = BuildDir + "/" + name + x
				break
			}
		}
		byPage[name] = s
	}
	out := make([]Shot, 0, len(byPage))
	for _, s := range byPage {
		out = append(out, s)
	}
	// home first, then by name; desktop before mobile for each page.
	sort.Slice(out, func(i, j int) bool {
		hi, hj := strings.HasPrefix(out[i].Name, "home-"), strings.HasPrefix(out[j].Name, "home-")
		if hi != hj {
			return hi
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func isImage(ext string) bool {
	for _, x := range imageExt {
		if ext == x {
			return true
		}
	}
	return false
}

var marks = map[Status]string{OK: "✓", Warn: "▲", Fail: "✗", Skip: "·"}

func (d Data) title() string { return d.Name + " is live: launch report" }

func (d Data) summary() string {
	n := len(d.Checks) - d.Count(Skip)
	s := fmt.Sprintf("%d of %d checks pass", d.Count(OK), n)
	if f := d.Count(Fail); f > 0 {
		s += fmt.Sprintf(", %d %s", f, plural(f, "fails", "fail"))
	}
	if w := d.Count(Warn); w > 0 {
		s += fmt.Sprintf(", %d worth a look", w)
	}
	return s
}

func (d Data) footer() string {
	return "Made by taw-fleet on " + d.Made.Format("2 Jan 2006, 15:04") + ". Again: taw-fleet launch-report " + d.Site +
		" (I in the dashboard once the site is live). Only production's public pages and its companion were read; nothing was changed."
}

// Markdown is the report as text.
func Markdown(d Data) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s · %s · %s\n\n", d.title(), d.Production, d.Theme, d.summary())
	b.WriteString("## What's left for you\n\n")
	if len(d.Left) == 0 {
		b.WriteString("Nothing: it's ready to tell the client.\n\n")
	}
	for _, l := range d.Left {
		b.WriteString("- " + l + "\n")
	}
	if len(d.Left) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("## Checks\n\n")
	for _, c := range d.Checks {
		fmt.Fprintf(&b, "- %s **%s**: %s\n", marks[c.Status], c.Name, c.Said)
	}
	if len(d.Speed) > 0 {
		b.WriteString("\n## Speed (PageSpeed Insights)\n\n| | Performance | Accessibility | Best practices | SEO | LCP | CLS |\n|---|---|---|---|---|---|---|\n")
		for _, r := range d.Speed {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", r.Strategy, score(r.Performance), score(r.Accessibility), score(r.BestPractices), score(r.SEO), dash(r.LCP), dash(r.CLS))
		}
	}
	if len(d.Shots) > 0 {
		b.WriteString("\n## Production beside the build\n\n")
		for _, s := range d.Shots {
			fmt.Fprintf(&b, "- %s: %s", s.Name, s.Production)
			if s.Build != "" {
				fmt.Fprintf(&b, " beside %s", s.Build)
			}
			b.WriteString("\n")
		}
	}
	if len(d.Links) > 0 {
		b.WriteString("\n## Links\n\n")
		for _, l := range d.Links {
			fmt.Fprintf(&b, "- %s: %s\n", l.Name, l.URL)
		}
	}
	if len(d.Setup) > 0 {
		b.WriteString("\n## Setup\n\n")
		for _, s := range d.Setup {
			mark := "✓"
			if !s.Done {
				mark = "○"
			}
			fmt.Fprintf(&b, "- %s %s: %s\n", mark, s.Name, s.State)
		}
	}
	b.WriteString("\n---\n\n_" + d.footer() + "_\n")
	return b.String()
}

func score(n int) string {
	if n < 0 {
		return "–"
	}
	return fmt.Sprint(n)
}

func dash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

func tone(n int) string {
	switch {
	case n < 0:
		return ""
	case n >= 90:
		return "good"
	case n >= 50:
		return "warn"
	}
	return "bad"
}

// HTML is the report as one page: inline styles, no scripts, nothing
// fetched; the pictures are files beside it.
func HTML(d Data) string {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + e(d.Name) + ` launch report</title>
<style>
:root { --ink: #1d232b; --muted: #677281; --line: #e3e7ec; --bg: #ffffff; --soft: #f5f7f9; --good: #18794e; --warn: #a35c00; --bad: #b42318; --accent: #2d5bd7; }
@media (prefers-color-scheme: dark) { :root { --ink: #e6e9ee; --muted: #9aa4b2; --line: #2c333d; --bg: #171b21; --soft: #0f1216; --good: #4cc38a; --warn: #f1a33c; --bad: #f2786c; --accent: #7aa2ff; } }
* { box-sizing: border-box; }
body { margin: 0; background: var(--soft); color: var(--ink); font: 16px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; }
main { max-width: 980px; margin: 32px auto; background: var(--bg); border: 1px solid var(--line); border-radius: 14px; padding: 40px 44px; }
h1 { font-size: 28px; line-height: 1.2; margin: 0 0 6px; letter-spacing: -0.01em; }
.sub { color: var(--muted); margin: 0; font-size: 15px; }
.tiles { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin: 28px 0 8px; }
.tile { border: 1px solid var(--line); border-radius: 10px; padding: 14px 14px 12px; }
.tile .label { color: var(--muted); font-size: 13px; }
.tile .value { font-size: 26px; font-weight: 650; margin-top: 2px; font-variant-numeric: tabular-nums; }
.good .value, .good.mark { color: var(--good); } .warn .value, .warn.mark { color: var(--warn); } .bad .value, .bad.mark { color: var(--bad); } .skip.mark { color: var(--muted); }
h2 { font-size: 18px; margin: 32px 0 10px; padding-top: 22px; border-top: 1px solid var(--line); }
p { margin: 0 0 10px; }
.muted { color: var(--muted); }
ol, ul { margin: 0 0 12px; padding-left: 22px; } li { padding: 3px 0; }
a { color: var(--accent); text-decoration: none; overflow-wrap: anywhere; }
table { border-collapse: collapse; width: 100%; margin: 6px 0 12px; font-variant-numeric: tabular-nums; }
th, td { text-align: left; padding: 8px 10px; border-bottom: 1px solid var(--line); vertical-align: top; }
th { color: var(--muted); font-weight: 600; font-size: 14px; }
.checks td:first-child { width: 22px; padding-right: 0; font-weight: 700; }
.checks td:nth-child(2) { white-space: nowrap; font-weight: 600; }
.speed td:not(:first-child), .speed th:not(:first-child) { text-align: right; }
.pair { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin: 10px 0 22px; }
.pair figure { margin: 0; } .pair figcaption { color: var(--muted); font-size: 13px; margin-bottom: 6px; }
.pair img { width: 100%; max-height: 900px; object-fit: cover; object-position: top; border: 1px solid var(--line); border-radius: 8px; display: block; }
.pair.mobile { grid-template-columns: 1fr 1fr; max-width: 560px; }
.pair.first img { aspect-ratio: 1350 / 940; max-height: none; }
.pair.mobile.first img { aspect-ratio: 412 / 823; }
.alone { display: flex; gap: 14px; align-items: flex-start; margin: 10px 0 22px; }
.alone figure { margin: 0; flex: 3; } .alone figure.mobile { flex: 1; } .alone figcaption { color: var(--muted); font-size: 13px; margin-bottom: 6px; }
.alone img { width: 100%; border: 1px solid var(--line); border-radius: 8px; display: block; }
.pair .none { border: 1px dashed var(--line); border-radius: 8px; padding: 18px; color: var(--muted); font-size: 14px; }
h3 { font-size: 15px; margin: 18px 0 4px; }
footer { color: var(--muted); font-size: 13px; margin-top: 32px; padding-top: 16px; border-top: 1px solid var(--line); }
@media (max-width: 640px) { main { margin: 0; border-radius: 0; border: 0; padding: 24px 16px; } .tiles { grid-template-columns: repeat(2, 1fr); } .checks td:nth-child(2) { white-space: normal; } }
</style>
</head>
<body>
<main>
`)
	fmt.Fprintf(&b, "<h1>%s</h1>\n<p class=\"sub\"><a href=\"%s\">%s</a> · %s · %s</p>\n", e(d.title()), e(d.Production), e(d.Production), e(d.Theme), e(d.Made.Format("2 Jan 2006, 15:04")))

	// Tiles: the checks, speed, what's left.
	b.WriteString("<div class=\"tiles\">\n")
	tile := func(label, value, tn string) {
		fmt.Fprintf(&b, "<div class=\"tile %s\"><div class=\"label\">%s</div><div class=\"value\">%s</div></div>\n", tn, e(label), e(value))
	}
	n := len(d.Checks) - d.Count(Skip)
	ct := "good"
	if d.Count(Fail) > 0 {
		ct = "bad"
	} else if d.Count(Warn) > 0 {
		ct = "warn"
	}
	tile("Checks passed", fmt.Sprintf("%d / %d", d.Count(OK), n), ct)
	for _, strategy := range []string{"mobile", "desktop"} {
		v, tn := "–", ""
		for _, r := range d.Speed {
			if r.Strategy == strategy {
				v, tn = score(r.Performance), tone(r.Performance)
			}
		}
		tile("Speed, "+strategy, v, tn)
	}
	lt := "good"
	if len(d.Left) > 0 {
		lt = "warn"
	}
	tile("Left for you", fmt.Sprint(len(d.Left)), lt)
	b.WriteString("</div>\n")

	b.WriteString("<h2>What's left for you</h2>\n")
	if len(d.Left) == 0 {
		b.WriteString("<p>Nothing: it's ready to tell the client.</p>\n")
	} else {
		b.WriteString("<ol>\n")
		for _, l := range d.Left {
			name, fix, _ := strings.Cut(l, ": ")
			fmt.Fprintf(&b, "<li><strong>%s</strong>: %s</li>\n", e(name), e(fix))
		}
		b.WriteString("</ol>\n")
	}

	b.WriteString("<h2>Checks</h2>\n<table class=\"checks\">\n")
	cls := map[Status]string{OK: "good", Warn: "warn", Fail: "bad", Skip: "skip"}
	for _, c := range d.Checks {
		fmt.Fprintf(&b, "<tr><td class=\"%s mark\">%s</td><td>%s</td><td>%s</td></tr>\n", cls[c.Status], marks[c.Status], e(c.Name), e(c.Said))
	}
	b.WriteString("</table>\n")

	if len(d.Speed) > 0 {
		b.WriteString("<h2>Speed</h2>\n<p class=\"muted\">Google PageSpeed Insights, the home page, measured as this report was made.</p>\n<table class=\"speed\">\n<tr><th></th><th>Performance</th><th>Accessibility</th><th>Best practices</th><th>SEO</th><th>LCP</th><th>CLS</th></tr>\n")
		for _, r := range d.Speed {
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
				e(r.Strategy), score(r.Performance), score(r.Accessibility), score(r.BestPractices), score(r.SEO), e(dash(r.LCP)), e(dash(r.CLS)))
		}
		b.WriteString("</table>\n")
	}

	builds := false
	for _, s := range d.Shots {
		builds = builds || s.Build != ""
	}
	switch {
	case len(d.Shots) > 0 && !builds:
		// No Local build to compare with (a site made before the new-site flow): production alone.
		b.WriteString("<h2>Production</h2>\n<p class=\"muted\">")
		if d.Shots[0].PageSpeed {
			b.WriteString("The first screen as PageSpeed saw it. ")
		}
		b.WriteString("Click a picture for all of it.</p>\n<div class=\"alone\">\n")
		for _, s := range d.Shots {
			kind := ""
			if strings.HasSuffix(s.Name, "-mobile") {
				kind = " mobile"
			}
			fmt.Fprintf(&b, "<figure class=\"%s\"><figcaption>%s</figcaption><a href=\"%s\"><img src=\"%s\" alt=\"%s in production\"></a></figure>\n",
				strings.TrimSpace(kind), e(s.Name), e(s.Production), e(s.Production), e(s.Name))
		}
		b.WriteString("</div>\n")
	case len(d.Shots) > 0:
		b.WriteString("<h2>Production beside the build</h2>\n<p class=\"muted\">Production on the left, the Local build on the right where there's one of the same page in build/. PageSpeed's pictures show the first screen, and the build beside them is cut to match; click a picture for all of it.</p>\n")
		for _, s := range d.Shots {
			kind := ""
			if strings.HasSuffix(s.Name, "-mobile") {
				kind = " mobile"
			}
			caption := "Production"
			if s.PageSpeed {
				kind += " first"
				caption = "Production, first screen (PageSpeed)"
			}
			fmt.Fprintf(&b, "<h3>%s</h3>\n<div class=\"pair%s\">\n<figure><figcaption>%s</figcaption><a href=\"%s\"><img src=\"%s\" alt=\"%s in production\"></a></figure>\n",
				e(s.Name), kind, e(caption), e(s.Production), e(s.Production), e(s.Name))
			if s.Build != "" {
				fmt.Fprintf(&b, "<figure><figcaption>Local build</figcaption><a href=\"%s\"><img src=\"%s\" alt=\"%s in the Local build\"></a></figure>\n", e(s.Build), e(s.Build), e(s.Name))
			} else {
				b.WriteString("<figure><figcaption>Local build</figcaption><div class=\"none\">No build picture by this name.</div></figure>\n")
			}
			b.WriteString("</div>\n")
		}
	}

	if len(d.Links) > 0 {
		b.WriteString("<h2>Links</h2>\n<ul>\n")
		for _, l := range d.Links {
			fmt.Fprintf(&b, "<li>%s: <a href=\"%s\">%s</a></li>\n", e(l.Name), e(l.URL), e(l.URL))
		}
		b.WriteString("</ul>\n")
	}
	if len(d.Setup) > 0 {
		b.WriteString("<h2>Setup</h2>\n<table class=\"checks\">\n")
		for _, s := range d.Setup {
			c, m := "good", "✓"
			if !s.Done {
				c, m = "warn", "○"
			}
			fmt.Fprintf(&b, "<tr><td class=\"%s mark\">%s</td><td>%s</td><td>%s</td></tr>\n", c, m, e(s.Name), e(s.State))
		}
		b.WriteString("</table>\n")
	}
	b.WriteString("<footer>" + e(d.footer()) + "</footer>\n</main>\n</body>\n</html>\n")
	return b.String()
}
