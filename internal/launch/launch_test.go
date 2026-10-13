package launch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/pagespeed"
	"github.com/Relmaur/taw-fleet/internal/setup"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// fakeProduction is example.com over HTTPS and plain HTTP, each served by
// its own test server; the client dials them by port.
type fakeProduction struct {
	page, robots, xrobots string
	plain                 func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeProduction) client(t *testing.T) *http.Client {
	t.Helper()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			if f.robots == "" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(f.robots))
		default:
			if f.xrobots != "" {
				w.Header().Set("X-Robots-Tag", f.xrobots)
			}
			_, _ = w.Write([]byte(f.page))
		}
	}))
	t.Cleanup(secure.Close)
	plainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.plain != nil {
			f.plain(w, r)
			return
		}
		http.Redirect(w, r, "https://example.com"+r.URL.Path, http.StatusMovedPermanently)
	}))
	t.Cleanup(plainSrv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(secure.Certificate())
	dialer := &net.Dialer{}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "example.com"},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			to := plainSrv.Listener.Addr().String()
			if strings.HasSuffix(addr, ":443") {
				to = secure.Listener.Addr().String()
			}
			return dialer.DialContext(ctx, network, to)
		},
	}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func check(d Data, name string) Check {
	for _, c := range d.Checks {
		if c.Name == name {
			return c
		}
	}
	return Check{}
}

func target() Target {
	s := site.Site{Slug: "acme", Name: "Acme Bakery", URL: "http://acme.local"}
	t := site.Theme{Dir: "acme", Core: site.CoreInfo{Locked: "v1.97.0"},
		Git:    &site.GitInfo{Repo: &site.Repo{Host: "github.com", Owner: "Relmaur", Name: "acme--theme"}},
		GitHub: &site.RepoState{Repo: "Relmaur/acme--theme", Default: "main", Deploy: &site.Deploy{URL: "https://github.com/Relmaur/acme--theme/actions/workflows/deploy.yml", Deployed: "abcdef1234", DeployedAt: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)}}}
	return Target{Site: s, Theme: t, Production: "https://example.com", Setup: []setup.Step{
		{Name: "Repository", Done: true, State: "Relmaur/acme--theme"},
		{Name: "Bot", State: "no bot", Fix: "taw-fleet wp-remote key import acme"},
		{Name: "BugSmash", State: "no project", Fix: "bugsmash_project under [sites.acme]"},
	}}
}

func live() (*site.Production, error) {
	return &site.Production{Reachable: true, Verified: true, KeyID: "site-1", WP: "6.8", PHP: "8.3", TawCore: "1.97.0",
		HasInventory: true, Plugins: 4, HasVulns: true, Scanner: "Defender"}, nil
}

func TestGatherLive(t *testing.T) {
	f := &fakeProduction{page: `<html><head><meta name="viewport" content="width=device-width"></head></html>`, robots: "User-agent: *\nDisallow: /wp-admin/\n"}
	src := Sources{HTTP: f.client(t), Live: func(context.Context) (*site.Production, error) { return live() },
		Speed: func(_ context.Context, _, strategy string) (pagespeed.Result, error) {
			return pagespeed.Result{Strategy: strategy, Performance: map[string]int{"mobile": 72, "desktop": 96}[strategy]}, nil
		}}
	tg := target()
	tg.Watching = true
	d := Gather(context.Background(), src, tg)
	for name, want := range map[string]Status{"HTTPS": OK, "Certificate": OK, "Redirect": OK, "Search engines": OK, "Deploy": OK,
		"Companion": OK, "Same code": OK, "Plugins": OK, "Security": OK, "Speed, mobile": Warn, "Speed, desktop": OK, "Watch": OK, "BugSmash": Warn} {
		if c := check(d, name); c.Status != want {
			t.Errorf("%s: %s (%q), want %s", name, c.Status, c.Said, want)
		}
	}
	if d.Speed[0].Strategy != "mobile" {
		t.Errorf("speed order %+v", d.Speed)
	}
	// Left: the warnings' fixes, then the SETUP steps the checks don't cover (Bot; BugSmash is a check).
	if len(d.Left) != 3 || !strings.HasPrefix(d.Left[0], "Speed, mobile: ") || !strings.HasPrefix(d.Left[1], "BugSmash: a website project") || !strings.HasPrefix(d.Left[2], "Bot: ") {
		t.Errorf("left %q", d.Left)
	}
	if len(d.Links) != 5 || d.Links[1].URL != "https://example.com/wp-admin/" {
		t.Errorf("links %+v", d.Links)
	}
}

func TestGatherKeptOut(t *testing.T) {
	f := &fakeProduction{
		page:   `<html><head><meta name='robots' content='noindex, nofollow' />`,
		robots: "User-agent: *\nDisallow: /\n",
		plain: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://example.com/", http.StatusFound)
		},
	}
	tg := target()
	tg.Theme.GitHub.Deploy = &site.Deploy{Failed: &site.Run{SHA: "deadbeef99", URL: "https://github.com/x/runs/1"}}
	d := Gather(context.Background(), Sources{HTTP: f.client(t), Live: func(context.Context) (*site.Production, error) {
		p, _ := live()
		p.Verified, p.TawCore, p.PluginUpdates = false, "1.96.0", []string{"akismet 5.3 → 5.4"}
		p.Vulns, p.WorstSeverity = []site.LiveVuln{{}}, "high"
		return p, nil
	}}, tg)
	se := check(d, "Search engines")
	if se.Status != Fail || !strings.Contains(se.Said, "the page says noindex") || !strings.Contains(se.Said, "robots.txt disallows everything") {
		t.Errorf("search engines %+v", se)
	}
	for name, want := range map[string]Status{"Redirect": Warn, "Deploy": Fail, "Companion": Warn, "Same code": Warn, "Plugins": Warn, "Security": Fail, "Speed": Skip, "Watch": Warn} {
		if c := check(d, name); c.Status != want {
			t.Errorf("%s: %s (%q), want %s", name, c.Status, c.Said, want)
		}
	}
	// Failures come before warnings in what's left.
	if !strings.HasPrefix(d.Left[0], "Search engines: ") || !strings.HasPrefix(d.Left[1], "Deploy: ") {
		t.Errorf("left %q", d.Left)
	}

	// The header alone keeps them out too.
	f2 := &fakeProduction{page: "<html></html>", xrobots: "noindex"}
	if c := check(Gather(context.Background(), Sources{HTTP: f2.client(t)}, target()), "Search engines"); c.Status != Fail {
		t.Errorf("X-Robots-Tag: %+v", c)
	}
}

func TestGatherNotLive(t *testing.T) {
	tg := target()
	tg.Production = ""
	d := Gather(context.Background(), Sources{}, tg)
	if len(d.Checks) != 1 || d.Checks[0].Status != Fail || !strings.HasPrefix(d.Left[0], "Production: set up the deploy") {
		t.Errorf("no production: %+v %q", d.Checks, d.Left)
	}
	tg.Production = "http://example.com"
	if c := check(Gather(context.Background(), Sources{}, tg), "HTTPS"); c.Status != Fail {
		t.Errorf("http only: %+v", c)
	}
	// Nobody home.
	hc := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("no such host") })}
	tg.Production = "https://example.com"
	if c := check(Gather(context.Background(), Sources{HTTP: hc}, tg), "HTTPS"); c.Status != Fail || !strings.Contains(c.Said, "no such host") || strings.Contains(c.Said, "Get ") {
		t.Errorf("no answer: %+v", c)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDisallowsAll(t *testing.T) {
	for txt, want := range map[string]bool{
		"User-agent: *\nDisallow: /\n":                              true,
		"User-agent: *\nDisallow: /wp-admin/\nAllow: /wp-admin/a\n": false,
		"User-agent: Googlebot\nDisallow: /\n":                      false,
		"User-agent: Bingbot\nUser-agent: *\nDisallow: / # all\n":   true,
		"User-agent: *\nDisallow:\n":                                false,
		"User-agent: *\nAllow: /\n\nUser-agent: Bad\nDisallow: /\n": false,
	} {
		if got := disallowsAll(txt); got != want {
			t.Errorf("%q: %v", txt, got)
		}
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "build", "home-mobile.png"), []byte("png"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "production"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "production", "home-mobile.jpg"), []byte("old"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "production", "about-desktop.png"), []byte("png"), 0o644)
	d := Data{Site: "acme", Name: "Acme <Bakery>", Production: "https://example.com", Dir: dir, Made: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC),
		Checks: []Check{{Name: "HTTPS", Status: OK, Said: "200"}, {Name: "Watch", Status: Warn, Said: "off", Fix: "taw-fleet watch install"}},
		Left:   []string{"Watch: taw-fleet watch install"},
		Speed:  []pagespeed.Result{{Strategy: "mobile", Performance: 91, Screenshot: []byte("webp"), ScreenshotType: "webp"}, {Strategy: "desktop", Performance: 40}}}
	_ = os.WriteFile(filepath.Join(dir, "production", "home-desktop.pagespeed.webp"), []byte("old"), 0o644)
	d.Speed = append(d.Speed[:1], pagespeed.Result{Strategy: "desktop", Performance: 40, Screenshot: []byte("jpg"), ScreenshotType: "jpeg"})
	page, err := Save(&d)
	if err != nil {
		t.Fatal(err)
	}
	// The agent's picture of the page stays, and is the one shown; PageSpeed's
	// of another type replaces its older one.
	if b, _ := os.ReadFile(filepath.Join(dir, "production", "home-mobile.jpg")); string(b) != "old" {
		t.Error("the browser's picture was touched")
	}
	if _, err := os.Stat(filepath.Join(dir, "production", "home-desktop.pagespeed.webp")); err == nil {
		t.Error("PageSpeed's older picture stayed")
	}
	want := []Shot{
		{"home-desktop", "production/home-desktop.pagespeed.jpg", "", true},
		{"home-mobile", "production/home-mobile.jpg", "build/home-mobile.png", false},
		{"about-desktop", "production/about-desktop.png", "", false},
	}
	if len(d.Shots) != 3 || d.Shots[0] != want[0] || d.Shots[1] != want[1] || d.Shots[2] != want[2] {
		t.Errorf("shots %+v", d.Shots)
	}
	h, _ := os.ReadFile(page)
	for _, want := range []string{"Acme &lt;Bakery&gt; is live", `src="production/home-mobile.jpg"`, `src="build/home-mobile.png"`, "first screen (PageSpeed)", "No build picture", "1 / 2", "<strong>Watch</strong>: taw-fleet watch install"} {
		if !strings.Contains(string(h), want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	md, _ := os.ReadFile(filepath.Join(dir, MDFile))
	if !strings.Contains(string(md), "- ▲ **Watch**: off") || !strings.Contains(string(md), "| mobile | 91 |") {
		t.Errorf("markdown:\n%s", md)
	}
}

func TestHTMLWithoutBuild(t *testing.T) {
	d := Data{Name: "Acme", Shots: []Shot{{Name: "home-desktop", Production: "production/home-desktop.pagespeed.jpg", PageSpeed: true}}}
	h := HTML(d)
	if !strings.Contains(h, "<h2>Production</h2>") || strings.Contains(h, "No build picture") || !strings.Contains(h, "as PageSpeed saw it") {
		t.Errorf("no build:\n%s", h)
	}
}
