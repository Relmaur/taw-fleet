package report

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/pagespeed"
)

// The report as a document: a header, tiles, then sections of paragraphs,
// lists and tables. Markdown and HTML both draw it, so they say the same.
type doc struct {
	title, subtitle, prepared string
	tiles                     []tile
	sections                  []section
	footer                    string
}

type tile struct{ label, value, note, tone string } // tone: good, warn, bad, ""

type section struct {
	title  string
	blocks []block
}

type block struct {
	text  string     // a paragraph
	items []item     // a list
	table [][]string // a table; the first row is the head
	muted bool       // a paragraph in a quieter voice
}

type item struct{ date, text, url string }

// words are the report's sentences in each language.
var words = map[string]map[string]string{
	"en": {
		"so far": "%s (so far)", "subtitle": "Monthly website report · %s", "prepared": "Prepared by %s on %s", "made": "Made on %s",
		"glance": "At a glance", "online": "Online", "changes": "Changes published", "comments": "Comments resolved", "speed": "Mobile speed",
		"uptime": "Availability", "uptime.p": "The site answered %s of %s checks, made every 5 minutes from %s.",
		"uptime.since": "since %s", "every5": "checked every 5 minutes",
		"work": "Updates and changes", "maint": "Framework and maintenance: %d %s; the site now runs TAW core %s.", "maint.nov": "Framework and maintenance: %d %s.", "upd.1": "update", "upd.n": "updates",
		"deploys": "Published to the live site %d times this month.", "deploys.1": "Published to the live site once this month.",
		"deploys.0": "Nothing was published to the live site this month.", "nochanges": "No changes this month.",
		"review": "Your review comments", "review.p": "%d received in %s: %d resolved, %d still open.", "review.0": "No new review comments in %s.",
		"speed.t": "Speed", "speed.p": "Google PageSpeed Insights, measured on %s (scores out of 100).", "mobile": "Mobile", "desktop": "Desktop",
		"perf": "Performance", "a11y": "Accessibility", "bp": "Best practices", "seo": "SEO", "lcp": "Largest content shown (lab)",
		"field": "Real visitors (last 28 days, mobile): largest content in %s, response to a tap in %s, layout shift %s. %s.",
		"FAST":  "Good", "AVERAGE": "Needs improvement", "SLOW": "Poor",
		"health": "Security and maintenance", "health.at": "Checked on %s.", "versions": "WordPress %s · PHP %s · TAW core %s",
		"plugins.ok": "%d plugins, all up to date.", "plugins.wait": "%d plugins; updates waiting for: %s.",
		"vulns.0": "No known vulnerabilities", "vulns": "%d known vulnerabilities (most serious: %s)", "by": " (checked by %s)",
		"unverified": "The site answered without its security signature; we're looking into it.",
		"na":         "Not available for this month.", "footer": "This report was made with taw-fleet.", "none": "—",
	},
	"es": {
		"so far": "%s (hasta hoy)", "subtitle": "Reporte mensual del sitio · %s", "prepared": "Preparado por %s el %s", "made": "Hecho el %s",
		"glance": "En resumen", "online": "En línea", "changes": "Cambios publicados", "comments": "Comentarios resueltos", "speed": "Velocidad móvil",
		"uptime": "Disponibilidad", "uptime.p": "El sitio respondió %s de %s comprobaciones, hechas cada 5 minutos desde el %s.",
		"uptime.since": "desde el %s", "every5": "revisado cada 5 minutos",
		"work": "Actualizaciones y cambios", "maint": "Framework y mantenimiento: %d %s; el sitio usa ahora TAW core %s.", "maint.nov": "Framework y mantenimiento: %d %s.", "upd.1": "actualización", "upd.n": "actualizaciones",
		"deploys": "Se publicó en el sitio en vivo %d veces este mes.", "deploys.1": "Se publicó en el sitio en vivo una vez este mes.",
		"deploys.0": "No se publicó nada en el sitio en vivo este mes.", "nochanges": "Sin cambios este mes.",
		"review": "Sus comentarios de revisión", "review.p": "%d recibidos en %s: %d resueltos, %d pendientes.", "review.0": "Sin comentarios nuevos en %s.",
		"speed.t": "Velocidad", "speed.p": "Google PageSpeed Insights, medido el %s (calificaciones sobre 100).", "mobile": "Móvil", "desktop": "Escritorio",
		"perf": "Rendimiento", "a11y": "Accesibilidad", "bp": "Buenas prácticas", "seo": "SEO", "lcp": "Contenido principal visible (laboratorio)",
		"field": "Visitantes reales (últimos 28 días, móvil): contenido principal en %s, respuesta al tocar en %s, movimiento de diseño %s. %s.",
		"FAST":  "Bien", "AVERAGE": "Puede mejorar", "SLOW": "Deficiente",
		"health": "Seguridad y mantenimiento", "health.at": "Revisado el %s.", "versions": "WordPress %s · PHP %s · TAW core %s",
		"plugins.ok": "%d plugins, todos al día.", "plugins.wait": "%d plugins; actualizaciones pendientes: %s.",
		"vulns.0": "Sin vulnerabilidades conocidas", "vulns": "%d vulnerabilidades conocidas (la más seria: %s)", "by": " (revisado por %s)",
		"unverified": "El sitio respondió sin su firma de seguridad; lo estamos revisando.",
		"na":         "No disponible para este mes.", "footer": "Este reporte se hizo con taw-fleet.", "none": "—",
	},
}

var monthsEN = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
var monthsES = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

func (d Data) w(k string, a ...any) string {
	s := words[d.Lang][k]
	if s == "" {
		s = words["en"][k]
	}
	if len(a) == 0 {
		return s
	}
	return fmt.Sprintf(s, a...)
}

// monthName is "October 2026" / "octubre de 2026".
func (d Data) monthName() string {
	m := d.From.Month() - 1
	if d.Lang == "es" {
		return monthsES[m] + " de " + fmt.Sprint(d.From.Year())
	}
	return monthsEN[m] + " " + fmt.Sprint(d.From.Year())
}

// monthOnly is "October" / "octubre".
func (d Data) monthOnly() string {
	if d.Lang == "es" {
		return monthsES[d.From.Month()-1]
	}
	return monthsEN[d.From.Month()-1]
}

// day is "Oct 12" / "12 oct"; long adds the year: "October 12, 2026" / "12 de octubre de 2026".
func (d Data) day(t time.Time, long bool) string {
	t = t.In(d.From.Location())
	m := t.Month() - 1
	if d.Lang == "es" {
		if long {
			return fmt.Sprintf("%d de %s de %d", t.Day(), monthsES[m], t.Year())
		}
		return fmt.Sprintf("%d %s", t.Day(), monthsES[m][:3])
	}
	if long {
		return fmt.Sprintf("%s %d, %d", monthsEN[m], t.Day(), t.Year())
	}
	return fmt.Sprintf("%s %d", monthsEN[m][:3], t.Day())
}

func percent(share float64) string {
	p := share * 100
	switch {
	case p >= 99.995:
		return "100%"
	case p >= 99:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", p), "0"), ".") + "%"
	}
	return fmt.Sprintf("%.1f%%", p)
}

func host(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return u
}

func (d Data) doc() doc {
	var out doc
	month := d.monthName()
	month = strings.ToUpper(month[:1]) + month[1:]
	out.title = d.Name + " — " + month
	if d.SoFar {
		out.title = d.Name + " — " + d.w("so far", month)
	}
	out.subtitle = d.w("subtitle", host(d.Production))
	if d.Production == "" {
		out.subtitle = d.w("subtitle", d.Name)
	}
	if d.PreparedBy != "" {
		out.prepared = d.w("prepared", d.PreparedBy, d.day(d.Made, true))
	} else {
		out.prepared = d.w("made", d.day(d.Made, true))
	}
	out.footer = d.w("footer")

	// At a glance.
	na := d.w("none")
	up := tile{label: d.w("online"), value: na}
	if d.Uptime != nil {
		up.value, up.note = percent(d.Uptime.Share), d.w("every5")
		if d.Uptime.Since.After(d.From.Add(36 * time.Hour)) {
			up.note = d.w("uptime.since", d.day(d.Uptime.Since, false))
		}
		up.tone = tone(d.Uptime.Share >= 0.999, d.Uptime.Share >= 0.99)
	}
	changes := tile{label: d.w("changes"), value: na}
	if d.ChangesNote == "" {
		changes.value = fmt.Sprint(len(d.Updates) + len(d.Maintenance) + len(d.Changes))
	}
	comments := tile{label: d.w("comments"), value: na}
	if d.Comments != nil {
		comments.value = fmt.Sprintf("%d / %d", d.Comments.Resolved, d.Comments.Received)
		if d.Comments.Received == 0 {
			comments.value = "0"
		}
	}
	speed := tile{label: d.w("speed"), value: na}
	if m := d.result("mobile"); m != nil && m.Performance >= 0 {
		speed.value, speed.tone = fmt.Sprint(m.Performance), tone(m.Performance >= 90, m.Performance >= 50)
	}
	out.tiles = []tile{up, changes, comments, speed}

	// Availability.
	sec := section{title: d.w("uptime")}
	if d.Uptime != nil {
		sec.blocks = append(sec.blocks, block{text: d.w("uptime.p", percent(d.Uptime.Share), thousands(d.Uptime.Checks), d.day(d.Uptime.Since, false))})
	} else {
		sec.blocks = append(sec.blocks, block{text: d.w("na"), muted: true})
	}
	out.sections = append(out.sections, sec)

	// Updates and changes: framework and maintenance counted, the site's own
	// changes listed.
	sec = section{title: d.w("work")}
	n := len(d.Updates) + len(d.Maintenance)
	switch {
	case d.ChangesNote != "" && n+len(d.Changes) == 0:
		sec.blocks = append(sec.blocks, block{text: d.w("na"), muted: true})
	case n+len(d.Changes) == 0:
		sec.blocks = append(sec.blocks, block{text: d.w("nochanges")})
	}
	if n > 0 {
		unit := d.w("upd.n")
		if n == 1 {
			unit = d.w("upd.1")
		}
		if v := d.coreNow(); v != "" {
			sec.blocks = append(sec.blocks, block{text: d.w("maint", n, unit, v)})
		} else {
			sec.blocks = append(sec.blocks, block{text: d.w("maint.nov", n, unit)})
		}
	}
	if len(d.Changes) > 0 {
		var items []item
		for _, c := range d.Changes {
			items = append(items, item{date: d.day(c.At, false), text: c.Title, url: c.URL})
		}
		sec.blocks = append(sec.blocks, block{items: items})
	}
	if d.HasDeploy {
		switch n := len(d.Deploys); n {
		case 0:
			sec.blocks = append(sec.blocks, block{text: d.w("deploys.0")})
		case 1:
			sec.blocks = append(sec.blocks, block{text: d.w("deploys.1")})
		default:
			sec.blocks = append(sec.blocks, block{text: d.w("deploys", n)})
		}
	}
	out.sections = append(out.sections, sec)

	// Review comments.
	if d.Comments != nil || d.CommentNote != "no BugSmash project" {
		sec = section{title: d.w("review")}
		switch {
		case d.Comments == nil:
			sec.blocks = append(sec.blocks, block{text: d.w("na"), muted: true})
		case d.Comments.Received == 0:
			sec.blocks = append(sec.blocks, block{text: d.w("review.0", d.monthOnly())})
		default:
			sec.blocks = append(sec.blocks, block{text: d.w("review.p", d.Comments.Received, d.monthOnly(), d.Comments.Resolved, d.Comments.Received-d.Comments.Resolved)})
		}
		out.sections = append(out.sections, sec)
	}

	// Speed.
	sec = section{title: d.w("speed.t")}
	if len(d.Speed) == 0 {
		sec.blocks = append(sec.blocks, block{text: d.w("na"), muted: true})
	} else {
		sec.blocks = append(sec.blocks, block{text: d.w("speed.p", d.day(d.SpeedAt, true))})
		head := []string{""}
		rows := [][]string{{d.w("perf")}, {d.w("a11y")}, {d.w("bp")}, {d.w("seo")}, {d.w("lcp")}}
		for _, r := range d.Speed {
			head = append(head, d.w(r.Strategy))
			rows[0] = append(rows[0], score(r.Performance))
			rows[1] = append(rows[1], score(r.Accessibility))
			rows[2] = append(rows[2], score(r.BestPractices))
			rows[3] = append(rows[3], score(r.SEO))
			rows[4] = append(rows[4], orDash(r.LCP))
		}
		sec.blocks = append(sec.blocks, block{table: append([][]string{head}, rows...)})
		if m := d.result("mobile"); m != nil && m.Field != nil {
			f := m.Field
			sec.blocks = append(sec.blocks, block{text: d.w("field", seconds(f.LCPms), fmt.Sprintf("%d ms", f.INPms), fmt.Sprintf("%.2f", f.CLS), d.w(f.Overall))})
		}
	}
	out.sections = append(out.sections, sec)

	// Security and maintenance.
	sec = section{title: d.w("health")}
	if h := d.Health; h != nil {
		sec.blocks = append(sec.blocks, block{text: d.w("versions", orDash(h.WP), orDash(h.PHP), orDash(h.Core))})
		var lines []string
		if len(h.Updates) == 0 {
			lines = append(lines, d.w("plugins.ok", h.Plugins))
		} else {
			lines = append(lines, d.w("plugins.wait", h.Plugins, strings.Join(h.Updates, ", ")))
		}
		v := d.w("vulns.0")
		if h.Vulns > 0 {
			v = d.w("vulns", h.Vulns, orDash(h.Worst))
		}
		if h.Scanner != "" {
			v += d.w("by", strings.ToUpper(h.Scanner[:1])+h.Scanner[1:])
		}
		lines = append(lines, v+".")
		if !h.Verified {
			lines = append(lines, d.w("unverified"))
		}
		sec.blocks = append(sec.blocks, block{text: strings.Join(lines, " ")}, block{text: d.w("health.at", d.day(d.Made, true)), muted: true})
	} else {
		sec.blocks = append(sec.blocks, block{text: d.w("na"), muted: true})
	}
	out.sections = append(out.sections, sec)
	return out
}

// coreNow is the taw/core the site runs: the live site's, or the newest an
// update this month brought.
func (d Data) coreNow() string {
	if d.Health != nil && d.Health.Core != "" {
		return strings.TrimPrefix(d.Health.Core, "v")
	}
	v := ""
	for _, c := range d.Updates {
		if c.To != "" {
			v = c.To // oldest first: the last is the newest
		}
	}
	return v
}

func (d Data) result(strategy string) *pagespeed.Result {
	for i := range d.Speed {
		if d.Speed[i].Strategy == strategy {
			return &d.Speed[i]
		}
	}
	return nil
}

func tone(good, ok bool) string {
	switch {
	case good:
		return "good"
	case ok:
		return "warn"
	}
	return "bad"
}

func score(n int) string {
	if n < 0 {
		return "—"
	}
	return fmt.Sprint(n)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func seconds(ms int) string { return fmt.Sprintf("%.1f s", float64(ms)/1000) }

func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// Markdown is the report as Markdown, to paste into an email.
func Markdown(d Data) string {
	doc := d.doc()
	var b strings.Builder
	b.WriteString("# " + doc.title + "\n\n" + doc.subtitle + " · " + doc.prepared + "\n\n")
	b.WriteString("## " + d.w("glance") + "\n\n")
	for _, t := range doc.tiles {
		line := "- **" + t.label + ":** " + t.value
		if t.note != "" {
			line += " (" + t.note + ")"
		}
		b.WriteString(line + "\n")
	}
	for _, s := range doc.sections {
		b.WriteString("\n## " + s.title + "\n\n")
		for _, bl := range s.blocks {
			switch {
			case len(bl.items) > 0:
				for _, it := range bl.items {
					text := it.text
					if it.url != "" {
						text = "[" + text + "](" + it.url + ")"
					}
					b.WriteString("- " + it.date + ": " + text + "\n")
				}
				b.WriteString("\n")
			case len(bl.table) > 0:
				for i, row := range bl.table {
					b.WriteString("| " + strings.Join(row, " | ") + " |\n")
					if i == 0 {
						b.WriteString("|" + strings.Repeat("---|", len(row)) + "\n")
					}
				}
				b.WriteString("\n")
			case bl.muted:
				b.WriteString("_" + bl.text + "_\n\n")
			default:
				b.WriteString(bl.text + "\n\n")
			}
		}
	}
	b.WriteString("---\n\n_" + doc.footer + "_\n")
	return strings.ReplaceAll(b.String(), "\n\n\n", "\n\n")
}

// HTML is the report as one self-contained page (inline styles, no
// scripts, nothing fetched), to attach or print.
func HTML(d Data) (string, error) {
	doc := d.doc()
	e := html.EscapeString
	var b strings.Builder
	lang := d.Lang
	b.WriteString(`<!doctype html>
<html lang="` + lang + `">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + e(doc.title) + `</title>
<style>
:root { --ink: #1d232b; --muted: #677281; --line: #e3e7ec; --bg: #ffffff; --soft: #f5f7f9; --good: #18794e; --warn: #a35c00; --bad: #b42318; --accent: #2d5bd7; }
* { box-sizing: border-box; }
body { margin: 0; background: var(--soft); color: var(--ink); font: 16px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; }
main { max-width: 760px; margin: 32px auto; background: var(--bg); border: 1px solid var(--line); border-radius: 14px; padding: 40px 44px; }
h1 { font-size: 28px; line-height: 1.2; margin: 0 0 6px; letter-spacing: -0.01em; }
.sub { color: var(--muted); margin: 0; font-size: 15px; }
.tiles { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin: 28px 0 8px; }
.tile { border: 1px solid var(--line); border-radius: 10px; padding: 14px 14px 12px; }
.tile .label { color: var(--muted); font-size: 13px; }
.tile .value { font-size: 26px; font-weight: 650; margin-top: 2px; font-variant-numeric: tabular-nums; }
.tile .note { color: var(--muted); font-size: 12px; }
.good .value { color: var(--good); } .warn .value { color: var(--warn); } .bad .value { color: var(--bad); }
h2 { font-size: 18px; margin: 32px 0 10px; padding-top: 22px; border-top: 1px solid var(--line); }
p { margin: 0 0 10px; }
.muted { color: var(--muted); }
ul { margin: 0 0 12px; padding: 0; list-style: none; }
li { padding: 6px 0; border-bottom: 1px dashed var(--line); display: flex; gap: 14px; }
li:last-child { border-bottom: 0; }
li .date { color: var(--muted); min-width: 64px; font-variant-numeric: tabular-nums; }
a { color: var(--accent); text-decoration: none; }
table { border-collapse: collapse; width: 100%; margin: 6px 0 12px; font-variant-numeric: tabular-nums; }
th, td { text-align: left; padding: 8px 10px; border-bottom: 1px solid var(--line); }
th { color: var(--muted); font-weight: 600; font-size: 14px; }
td:not(:first-child), th:not(:first-child) { text-align: right; }
footer { color: var(--muted); font-size: 13px; margin-top: 32px; padding-top: 16px; border-top: 1px solid var(--line); }
@media (max-width: 640px) { main { margin: 0; border-radius: 0; border: 0; padding: 24px 18px; } .tiles { grid-template-columns: repeat(2, 1fr); } }
@media print { body { background: #fff; } main { margin: 0; border: 0; padding: 0; max-width: none; } a { color: inherit; } }
</style>
</head>
<body>
<main>
<h1>` + e(doc.title) + `</h1>
<p class="sub">` + e(doc.subtitle) + ` · ` + e(doc.prepared) + `</p>
<div class="tiles">
`)
	for _, t := range doc.tiles {
		b.WriteString(`<div class="tile ` + t.tone + `"><div class="label">` + e(t.label) + `</div><div class="value">` + e(t.value) + `</div>`)
		if t.note != "" {
			b.WriteString(`<div class="note">` + e(t.note) + `</div>`)
		}
		b.WriteString("</div>\n")
	}
	b.WriteString("</div>\n")
	for _, s := range doc.sections {
		b.WriteString("<h2>" + e(s.title) + "</h2>\n")
		for _, bl := range s.blocks {
			switch {
			case len(bl.items) > 0:
				b.WriteString("<ul>\n")
				for _, it := range bl.items {
					text := e(it.text)
					if it.url != "" {
						text = `<a href="` + e(it.url) + `">` + text + `</a>`
					}
					b.WriteString(`<li><span class="date">` + e(it.date) + `</span><span>` + text + "</span></li>\n")
				}
				b.WriteString("</ul>\n")
			case len(bl.table) > 0:
				b.WriteString("<table>\n")
				for i, row := range bl.table {
					cell := "td"
					if i == 0 {
						cell = "th"
					}
					b.WriteString("<tr>")
					for _, c := range row {
						b.WriteString("<" + cell + ">" + e(c) + "</" + cell + ">")
					}
					b.WriteString("</tr>\n")
				}
				b.WriteString("</table>\n")
			case bl.muted:
				b.WriteString(`<p class="muted">` + e(bl.text) + "</p>\n")
			default:
				b.WriteString("<p>" + e(bl.text) + "</p>\n")
			}
		}
	}
	b.WriteString("<footer>" + e(doc.footer) + "</footer>\n</main>\n</body>\n</html>\n")
	return b.String(), nil
}
