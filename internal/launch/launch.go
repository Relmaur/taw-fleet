// Package launch reviews a site in production once it's live: stage 6 of the
// fleet agent's flow (umbrella docs/plans/taw-fleet-agent.md § 4). The
// go-live checks (HTTPS, the redirect to it, the certificate, search engines
// let in, the deploy, the companion, plugins and security, speed, watch,
// BugSmash), the links, PageSpeed's pictures of production beside the Local
// build, and what's left for the person. It writes only in the site's folder
// under ~/TAW Fleet, and only reads production.
package launch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Relmaur/taw-fleet/internal/pagespeed"
	"github.com/Relmaur/taw-fleet/internal/setup"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Status is a check's outcome.
type Status string

// The outcomes, best first.
const (
	OK   Status = "ok"
	Warn Status = "warn" // works, but worth a look before telling the client
	Fail Status = "fail" // not live as it should be
	Skip Status = "skip" // not checked, and why
)

// Check is one go-live check.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Said   string `json:"said"`          // what it found
	Fix    string `json:"fix,omitempty"` // how, when it isn't OK
}

// Link is somewhere the person goes next.
type Link struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Shot is one picture of production, with the Local build's beside it when
// there's one by the same name: paths relative to the site's folder.
type Shot struct {
	Name       string `json:"name"` // "home-mobile"
	Production string `json:"production"`
	Build      string `json:"build,omitempty"`
	PageSpeed  bool   `json:"pagespeed,omitempty"` // PageSpeed's picture of the first screen, not a browser's of the whole page
}

// Data is everything the launch report says.
type Data struct {
	Site       string             `json:"site"`
	Name       string             `json:"name"`
	Theme      string             `json:"theme"`
	Production string             `json:"production_url"`
	Made       time.Time          `json:"made_at"`
	Checks     []Check            `json:"checks"`
	Speed      []pagespeed.Result `json:"speed,omitempty"`
	Setup      []setup.Step       `json:"setup"`
	Links      []Link             `json:"links"`
	Shots      []Shot             `json:"screenshots,omitempty"`
	Left       []string           `json:"left"` // what's left for the person, most important first
	Dir        string             `json:"folder"`
}

// Count is how many checks came out each way.
func (d Data) Count(st Status) int {
	n := 0
	for _, c := range d.Checks {
		if c.Status == st {
			n++
		}
	}
	return n
}

// Target is the site the report is about.
type Target struct {
	Site       site.Site
	Theme      site.Theme
	Production string       // its production URL
	Project    string       // its BugSmash project id ("" = none)
	ReviewURL  string       // the project's review page, when known
	Setup      []setup.Step // its SETUP checklist
	Watching   bool         // taw-fleet watch's agent runs
	Dir        string       // the site's folder: ~/TAW Fleet/sites/<slug>
}

// Sources are where the answers come from. A nil source is a check that
// says it wasn't run.
type Sources struct {
	HTTP  *http.Client // production's own pages
	Live  func(ctx context.Context) (*site.Production, error)
	Speed func(ctx context.Context, url, strategy string) (pagespeed.Result, error)
	Now   func() time.Time
}

// Gather runs the checks. Nothing fails the report: a check that can't be
// run says why.
func Gather(ctx context.Context, src Sources, t Target) Data {
	now := time.Now()
	if src.Now != nil {
		now = src.Now()
	}
	name := t.Site.Name
	if name == "" {
		name = t.Site.Slug
	}
	d := Data{Site: t.Site.Slug, Name: name, Theme: t.Theme.Dir, Production: t.Production, Made: now, Setup: t.Setup, Dir: t.Dir}
	if t.Production == "" {
		d.Checks = []Check{{Name: "Production", Status: Fail, Said: "no production URL",
			Fix: "set up the deploy first (I on the Deploy step, or the add-production skill); it adds production_url"}}
		d.Links = links(t)
		d.Left = left(d)
		return d
	}

	var (
		wg   sync.WaitGroup
		web  webResult
		prod *site.Production
		perr error
		mu   sync.Mutex
	)
	wg.Add(1)
	go func() { defer wg.Done(); web = probe(ctx, src.HTTP, t.Production, now) }()
	if src.Live != nil {
		wg.Add(1)
		go func() { defer wg.Done(); prod, perr = src.Live(ctx) }()
	}
	var speedErr error
	if src.Speed != nil {
		for _, strategy := range []string{"mobile", "desktop"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := src.Speed(ctx, t.Production, strategy)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					speedErr = err
					return
				}
				d.Speed = append(d.Speed, r)
			}()
		}
	}
	wg.Wait()
	sort.Slice(d.Speed, func(i, j int) bool { return d.Speed[i].Strategy == "mobile" && d.Speed[j].Strategy != "mobile" })

	d.Checks = append(d.Checks, web.checks...)
	d.Checks = append(d.Checks, deployCheck(t.Theme))
	d.Checks = append(d.Checks, liveChecks(t, prod, perr)...)
	d.Checks = append(d.Checks, speedChecks(d.Speed, src.Speed != nil, speedErr)...)
	d.Checks = append(d.Checks, watchCheck(t.Watching), bugsmashCheck(t))
	d.Links = links(t)
	d.Left = left(d)
	return d
}

// --- production's own pages -------------------------------------------------

type webResult struct{ checks []Check }

var (
	metaTag  = regexp.MustCompile(`(?is)<meta\s[^>]*>`)
	robotsNm = regexp.MustCompile(`(?i)name\s*=\s*["']?(robots|googlebot)["'\s/>]`)
)

// probe asks production for its home page over HTTPS, its http:// address
// and its robots.txt.
func probe(ctx context.Context, hc *http.Client, prod string, now time.Time) webResult {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	var res webResult
	u, err := url.Parse(prod)
	if err != nil || u.Host == "" {
		res.checks = append(res.checks, Check{Name: "HTTPS", Status: Fail, Said: "the production URL isn't a URL: " + prod,
			Fix: "production_url under [sites.<site>] in the settings"})
		return res
	}
	if u.Scheme != "https" {
		res.checks = append(res.checks, Check{Name: "HTTPS", Status: Fail, Said: "the production URL is " + u.Scheme + "://",
			Fix: "a certificate at the host (most give one free: Let's Encrypt), then production_url with https://"})
		return res
	}
	home := u.Scheme + "://" + u.Host + "/"

	// The home page, over HTTPS, redirects followed.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	start := time.Now()
	resp, body, err := get(ctx, hc, home, true)
	took := time.Since(start)
	switch {
	case err != nil:
		res.checks = append(res.checks, Check{Name: "HTTPS", Status: Fail, Said: "no answer: " + plainErr(err),
			Fix: "the domain's DNS points at the host, and the host has the site and its certificate"})
		return res
	case resp.StatusCode >= 400:
		res.checks = append(res.checks, Check{Name: "HTTPS", Status: Fail, Said: fmt.Sprintf("the home page answers %d", resp.StatusCode),
			Fix: "the host's error log; a 5xx after a deploy is often the theme's PHP"})
	default:
		said := fmt.Sprintf("the home page answers %d in %.1f s", resp.StatusCode, took.Seconds())
		if final := resp.Request.URL; final.Host != u.Host {
			said += ", at " + final.Host
		}
		res.checks = append(res.checks, Check{Name: "HTTPS", Status: OK, Said: said})
	}
	res.checks = append(res.checks, certCheck(resp, now))

	// http:// moves to https://.
	plain := "http://" + u.Host + "/"
	if r, _, err := get(ctx, hc, plain, false); err != nil {
		res.checks = append(res.checks, Check{Name: "Redirect", Status: Warn, Said: "http:// doesn't answer (" + plainErr(err) + ")",
			Fix: "links and old bookmarks with http:// fail: the host's \"force HTTPS\" setting"})
	} else {
		loc := r.Header.Get("Location")
		switch {
		case (r.StatusCode == 301 || r.StatusCode == 308) && strings.HasPrefix(loc, "https://"):
			res.checks = append(res.checks, Check{Name: "Redirect", Status: OK, Said: "http:// moves to https:// for good (" + fmt.Sprint(r.StatusCode) + ")"})
		case (r.StatusCode == 302 || r.StatusCode == 307) && strings.HasPrefix(loc, "https://"):
			res.checks = append(res.checks, Check{Name: "Redirect", Status: Warn, Said: fmt.Sprintf("http:// moves to https://, but as temporary (%d)", r.StatusCode),
				Fix: "make it permanent (301) in the host's \"force HTTPS\" setting, so search engines keep one address"})
		default:
			res.checks = append(res.checks, Check{Name: "Redirect", Status: Warn, Said: fmt.Sprintf("http:// answers %d without moving to https://", r.StatusCode),
				Fix: "the host's \"force HTTPS\" setting"})
		}
	}

	// Search engines: noindex in the page or its headers, or robots.txt
	// shutting everyone out. WordPress's "Discourage search engines" is the
	// usual leftover of a site built on a staging copy.
	if resp.StatusCode < 400 {
		var blocked []string
		if strings.Contains(strings.ToLower(resp.Header.Get("X-Robots-Tag")), "noindex") {
			blocked = append(blocked, "the X-Robots-Tag header says noindex")
		}
		for _, tag := range metaTag.FindAllString(string(body), -1) {
			if robotsNm.MatchString(tag) && strings.Contains(strings.ToLower(tag), "noindex") {
				blocked = append(blocked, "the page says noindex")
				break
			}
		}
		if r, txt, err := get(ctx, hc, u.Scheme+"://"+u.Host+"/robots.txt", true); err == nil && r.StatusCode == 200 && disallowsAll(string(txt)) {
			blocked = append(blocked, "robots.txt disallows everything")
		}
		if len(blocked) > 0 {
			res.checks = append(res.checks, Check{Name: "Search engines", Status: Fail, Said: "kept out: " + strings.Join(blocked, "; "),
				Fix: "wp-admin → Settings → Reading → untick \"Discourage search engines from indexing this site\" (and robots.txt, if it's a file)"})
		} else {
			res.checks = append(res.checks, Check{Name: "Search engines", Status: OK, Said: "let in: no noindex, robots.txt doesn't shut them out"})
		}
	}
	return res
}

// get fetches a page (its first MB), following redirects or not.
func get(ctx context.Context, hc *http.Client, u string, follow bool) (*http.Response, []byte, error) {
	c := *hc
	if !follow {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "taw-fleet launch report")
	resp, err := c.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp, body, err
}

// plainErr is a request's error without Go's "Get \"…\":" prefix.
func plainErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var cv *tls.CertificateVerificationError
	if errors.As(err, &cv) {
		return "the certificate isn't valid: " + cv.Err.Error()
	}
	return err.Error()
}

// certCheck is the certificate production presented.
func certCheck(resp *http.Response, now time.Time) Check {
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return Check{Name: "Certificate", Status: Skip, Said: "not seen"}
	}
	c := resp.TLS.PeerCertificates[0]
	days := int(c.NotAfter.Sub(now).Hours() / 24)
	issuer := c.Issuer.Organization
	by := ""
	if len(issuer) > 0 {
		by = ", from " + issuer[0]
	}
	said := fmt.Sprintf("valid until %s (%d days)%s", c.NotAfter.Format("2 Jan 2006"), days, by)
	if days < 21 {
		return Check{Name: "Certificate", Status: Warn, Said: said, Fix: "it renews by itself at most hosts; if not, renew it at the host"}
	}
	return Check{Name: "Certificate", Status: OK, Said: said}
}

// disallowsAll reports whether robots.txt shuts every crawler out.
func disallowsAll(txt string) bool {
	everyone, inGroup := false, false
	for _, line := range strings.Split(txt, "\n") {
		line, _, _ = strings.Cut(line, "#")
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			if !inGroup {
				everyone = false
			}
			inGroup = true
			if v == "*" {
				everyone = true
			}
		case "disallow", "allow":
			inGroup = false
			if everyone && k == "disallow" && v == "/" {
				return true
			}
		}
	}
	return false
}

// --- the rest ---------------------------------------------------------------

// deployCheck is the theme's deploy workflow on GitHub.
func deployCheck(t site.Theme) Check {
	c := Check{Name: "Deploy"}
	switch g := t.GitHub; {
	case g == nil:
		c.Status, c.Said, c.Fix = Skip, "GitHub not checked", "L checks it"
	case g.Error != "":
		c.Status, c.Said, c.Fix = Skip, "GitHub didn't answer: "+g.Error, "gh auth status"
	case g.Deploy == nil:
		c.Status, c.Said, c.Fix = Fail, "no deploy workflow", "I sets it up (taw-fleet deploy setup)"
	case g.Deploy.Failed != nil:
		c.Status, c.Said, c.Fix = Fail, "the last deploy failed ("+short(g.Deploy.Failed.SHA)+")", "its log: "+g.Deploy.Failed.URL
	case g.Deploy.Running != nil:
		c.Status, c.Said, c.Fix = Warn, "a deploy is running ("+short(g.Deploy.Running.SHA)+")", "make the report again once it's finished"
	case g.Deploy.Deployed == "":
		c.Status, c.Said, c.Fix = Fail, "nothing deployed yet", "merge the deploy pull request (M); merging deploys"
	case g.Deploy.Behind > 0:
		c.Status, c.Said, c.Fix = Warn, fmt.Sprintf("deployed %s, %d %s on %s since", short(g.Deploy.Deployed), g.Deploy.Behind, plural(g.Deploy.Behind, "commit", "commits"), g.Default),
			"the next merge deploys them"
	default:
		c.Status, c.Said = OK, "deployed "+short(g.Deploy.Deployed)+day(g.Deploy.DeployedAt)+", nothing waiting"
	}
	return c
}

// liveChecks are the companion's answers: verified, the same taw/core as
// the theme, plugins and security.
func liveChecks(t Target, p *site.Production, err error) []Check {
	comp := Check{Name: "Companion"}
	switch {
	case err != nil:
		comp.Status, comp.Said = Skip, err.Error()
		return []Check{comp}
	case p == nil:
		comp.Status, comp.Said = Skip, "not checked"
		return []Check{comp}
	case !p.Reachable:
		comp.Status, comp.Said, comp.Fix = Fail, "doesn't answer: "+p.Error, "the theme ships the companion: deploy it, then L"
		if p.ErrorKind == "no-key" {
			comp.Fix = "taw-fleet live key: the signing key the sites trust"
		}
		return []Check{comp}
	case !p.Verified:
		comp.Status, comp.Said, comp.Fix = Warn, "answers, not verified", "taw-fleet live trust "+t.Site.Slug+" pins its key (it shows the key and asks)"
	default:
		comp.Status, comp.Said = OK, "answers, verified ("+p.KeyID+"): WordPress "+p.WP+", PHP "+p.PHP
	}
	out := []Check{comp}

	code := Check{Name: "Same code"}
	local := strings.TrimPrefix(t.Theme.Core.Locked, "v")
	live := strings.TrimPrefix(p.TawCore, "v")
	switch {
	case live == "" || local == "":
		code.Status, code.Said = Skip, "taw/core's version not known on both sides"
	case live == local:
		code.Status, code.Said = OK, "taw/core "+live+" here and in production"
	default:
		code.Status, code.Said, code.Fix = Warn, "taw/core "+live+" in production, "+local+" in the theme", "a deploy on its way, or one that didn't run: the Deploy check"
	}
	out = append(out, code)

	if p.HasInventory {
		pl := Check{Name: "Plugins", Status: OK, Said: fmt.Sprintf("%d, all up to date", p.Plugins)}
		if n := len(p.PluginUpdates); n > 0 {
			pl.Status, pl.Said, pl.Fix = Warn, fmt.Sprintf("%d, %d with an update: %s", p.Plugins, n, strings.Join(p.PluginUpdates, ", ")), "update in production's wp-admin → Plugins, after the host's backup"
		}
		out = append(out, pl)
	}
	if p.HasVulns {
		sec := Check{Name: "Security", Status: OK, Said: "no known vulnerabilities"}
		if p.Scanner != "" {
			sec.Said += " (" + p.Scanner + " runs)"
		}
		if n := len(p.Vulns); n > 0 {
			sec.Status, sec.Said, sec.Fix = Fail, fmt.Sprintf("%d known %s, the worst %s", n, plural(n, "vulnerability", "vulnerabilities"), p.WorstSeverity),
				"taw-fleet live "+t.Site.Slug+" lists them; most go with a plugin update"
		}
		out = append(out, sec)
	}
	return out
}

// speedChecks are PageSpeed's performance, mobile and desktop.
func speedChecks(rs []pagespeed.Result, asked bool, err error) []Check {
	if !asked {
		return []Check{{Name: "Speed", Status: Skip, Said: "not measured (--no-speed)"}}
	}
	if len(rs) == 0 {
		said := "PageSpeed didn't answer"
		if err != nil {
			said = err.Error()
		}
		return []Check{{Name: "Speed", Status: Skip, Said: said}}
	}
	var out []Check
	for _, r := range rs {
		c := Check{Name: "Speed, " + r.Strategy, Said: fmt.Sprintf("performance %d, accessibility %d, best practices %d, SEO %d", r.Performance, r.Accessibility, r.BestPractices, r.SEO)}
		switch {
		case r.Performance >= 90:
			c.Status = OK
		case r.Performance >= 50:
			c.Status, c.Fix = Warn, "the perf-audit site skill finds what slows it (a in the dashboard)"
		default:
			c.Status, c.Fix = Fail, "the perf-audit site skill finds what slows it (a in the dashboard)"
		}
		out = append(out, c)
	}
	return out
}

func watchCheck(on bool) Check {
	if on {
		return Check{Name: "Watch", Status: OK, Said: "taw-fleet watch checks it every few minutes and tells you when it's down"}
	}
	return Check{Name: "Watch", Status: Warn, Said: "taw-fleet watch isn't running: nobody's told when the site is down",
		Fix: "taw-fleet watch install"}
}

func bugsmashCheck(t Target) Check {
	if t.Project != "" {
		said := "project linked"
		if t.ReviewURL != "" {
			said += ": " + t.ReviewURL
		}
		return Check{Name: "BugSmash", Status: OK, Said: said}
	}
	return Check{Name: "BugSmash", Status: Warn, Said: "no project: the client has nowhere to leave comments",
		Fix: "a website project in BugSmash for " + t.Production + ", then taw-fleet comments link " + t.Site.Slug + " <project>"}
}

// links are where the person goes next.
func links(t Target) []Link {
	var out []Link
	add := func(name, u string) {
		if u != "" {
			out = append(out, Link{name, u})
		}
	}
	if t.Production != "" {
		add("Production", t.Production)
		add("wp-admin", strings.TrimSuffix(t.Production, "/")+"/wp-admin/")
	}
	if g := t.Theme.Git; g != nil && g.Repo != nil {
		add("GitHub", g.Repo.WebURL())
	}
	if g := t.Theme.GitHub; g != nil && g.Deploy != nil {
		add("Deploy workflow", g.Deploy.URL)
	}
	add("BugSmash", t.ReviewURL)
	add("Local", t.Site.URL)
	return out
}

// left is what the person still has to do: the checks that aren't OK, then
// the SETUP steps the checks don't cover.
func left(d Data) []string {
	var out []string
	seen := map[string]bool{}
	for _, st := range []Status{Fail, Warn} {
		for _, c := range d.Checks {
			if c.Status == st && c.Fix != "" {
				out = append(out, c.Name+": "+c.Fix)
				seen[c.Name] = true
			}
		}
	}
	for _, c := range d.Checks {
		seen[c.Name] = true
	}
	for _, s := range d.Setup {
		if !s.Done && !s.Later && s.Fix != "" && !seen[s.Name] {
			out = append(out, s.Name+": "+s.Fix)
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

func day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return " on " + t.Format("2 Jan 2006")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
