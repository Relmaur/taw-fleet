// Package create makes a new Local site with a TAW theme: Local's API
// creates and starts the site, taw-create installs the classic or block
// theme, wp-cli activates it and git records the first commit.
package create

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/composer"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Kind is the TAW theme a site starts from.
type Kind string

// Kinds.
const (
	Classic Kind = "classic" // taw-theme
	Block   Kind = "block"   // taw-gutenberg
)

// ParseKind accepts classic/block and the package names people use.
func ParseKind(s string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "classic", "theme", "taw-theme":
		return Classic, nil
	case "block", "gutenberg", "taw-gutenberg":
		return Block, nil
	}
	return "", fmt.Errorf("theme kind %q: use classic or block", s)
}

// Starter is taw-create's TAW_STARTER value.
func (k Kind) Starter() string {
	if k == Block {
		return "gutenberg"
	}
	return "theme"
}

// Scaffold is the repository the theme comes from.
func (k Kind) Scaffold() string {
	if k == Block {
		return "taw-gutenberg"
	}
	return "taw-theme"
}

// TawCreateRepo is where taw-create is installed from.
const TawCreateRepo = `{"type":"vcs","url":"https://github.com/Relmaur/taw-create"}`

// Timeouts per step.
const (
	SiteTimeout  = 10 * time.Minute // Local: provision + WordPress install (18 s when probed)
	ThemeTimeout = 10 * time.Minute // composer create-project + npm install
	BuildTimeout = 5 * time.Minute
)

// Request is what the user asked for. Normalize fills in the rest.
type Request struct {
	Name          string `json:"name"`
	Slug          string `json:"slug"`   // site folder in ~/Local Sites; from Name
	Domain        string `json:"domain"` // <slug>.local
	Kind          Kind   `json:"kind"`
	ThemeDir      string `json:"theme_dir"`  // folder in wp-content/themes; = Slug
	PHP           string `json:"php"`        // "" = Local's preferred; "8.2" = newest 8.2.x installed
	WebServer     string `json:"web_server"` // "" = preferred; "nginx" or "nginx-1.26.1"
	AdminUser     string `json:"admin_user"`
	AdminEmail    string `json:"admin_email"`
	AdminPassword string `json:"-"` // "" = generated
}

var (
	nonSlug  = regexp.MustCompile(`[^a-z0-9]+`)
	slugRe   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	domainRe = regexp.MustCompile(`^[a-z0-9]+([.-][a-z0-9]+)*\.[a-z]+$`)
	emailRe  = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	userRe   = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,60}$`)
)

// Slugify turns a site name into a folder name: "CH Capital – TAW" →
// "ch-capital-taw".
func Slugify(name string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(asciiFold(name)), "-"), "-")
}

func asciiFold(s string) string {
	r := strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n",
		"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u", "Ü", "u", "Ñ", "n", "ç", "c", "à", "a", "è", "e", "ò", "o")
	return r.Replace(s)
}

// Normalize fills the defaults (slug, domain, theme folder, password) and
// checks every value, including that PHP and the web server are versions
// Local has downloaded. It doesn't look at existing sites; Check does.
func Normalize(p paths.Paths, r Request) (Request, error) {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return r, errors.New("the site needs a name")
	}
	if r.Slug == "" {
		r.Slug = Slugify(r.Name)
	}
	if !slugRe.MatchString(r.Slug) {
		return r, fmt.Errorf("site folder %q: use lowercase letters, digits and dashes", r.Slug)
	}
	if r.Domain == "" {
		r.Domain = r.Slug + ".local"
	}
	r.Domain = strings.ToLower(strings.TrimSpace(r.Domain))
	if !domainRe.MatchString(r.Domain) {
		return r, fmt.Errorf("domain %q isn't a host name like acme.local", r.Domain)
	}
	if r.ThemeDir == "" {
		r.ThemeDir = r.Slug
	}
	if !slugRe.MatchString(r.ThemeDir) {
		return r, fmt.Errorf("theme folder %q: use lowercase letters, digits and dashes", r.ThemeDir)
	}
	if r.Kind != Classic && r.Kind != Block {
		return r, errors.New("choose a theme: classic (taw-theme) or block (taw-gutenberg)")
	}
	if !userRe.MatchString(r.AdminUser) {
		return r, fmt.Errorf("admin username %q: letters, digits and . _ @ - only", r.AdminUser)
	}
	if !emailRe.MatchString(r.AdminEmail) {
		return r, fmt.Errorf("admin email %q doesn't look like an email address", r.AdminEmail)
	}
	services := local.Services(p)
	if r.PHP != "" {
		v, err := pick(services["php"], r.PHP)
		if err != nil {
			return r, fmt.Errorf("PHP %s: %w", r.PHP, err)
		}
		r.PHP = v
	}
	if r.WebServer != "" {
		name, ver, _ := strings.Cut(strings.ToLower(r.WebServer), "-")
		if name != "nginx" && name != "apache" {
			return r, fmt.Errorf("web server %q: nginx or apache", r.WebServer)
		}
		v, err := pick(services[name], ver)
		if err != nil {
			return r, fmt.Errorf("%s: %w", name, err)
		}
		r.WebServer = name + "-" + v
	}
	if r.AdminPassword == "" {
		pw, err := Password(20)
		if err != nil {
			return r, err
		}
		r.AdminPassword = pw
	}
	return r, nil
}

// pick returns the newest installed version matching want ("" = newest,
// "8.2" = newest 8.2.x, "8.2.30" exactly).
func pick(installed []string, want string) (string, error) {
	if len(installed) == 0 {
		return "", errors.New("not downloaded in Local (Local → Preferences → Advanced, or create a site with it once)")
	}
	for _, v := range installed { // newest first
		if want == "" || v == want || strings.HasPrefix(v, want+".") {
			return v, nil
		}
	}
	return "", fmt.Errorf("not downloaded in Local; installed: %s", strings.Join(installed, ", "))
}

const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789-_"

// Password returns n characters from crypto/rand, without look-alikes
// (0/O, 1/l/I).
func Password(n int) (string, error) {
	var b strings.Builder
	size := big.NewInt(int64(len(passwordAlphabet)))
	for range n {
		i, err := rand.Int(rand.Reader, size)
		if err != nil {
			return "", err
		}
		b.WriteByte(passwordAlphabet[i.Int64()])
	}
	return b.String(), nil
}

// Check refuses a request that collides with what exists: the site folder,
// or a Local site with the same name or domain.
func Check(p paths.Paths, r Request) error {
	dir := SitePath(p, r.Slug)
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("%s already exists; pick another name or folder", render.Tilde(p, dir))
	}
	raws, err := local.LoadRegistry(p)
	if err != nil && !errors.Is(err, local.ErrNoLocal) {
		return err
	}
	for _, s := range raws {
		switch {
		case strings.EqualFold(s.Name, r.Name):
			return fmt.Errorf("there is already a Local site named %q", s.Name)
		case strings.EqualFold(s.Domain, r.Domain):
			return fmt.Errorf("%s is already used by Local site %q", r.Domain, s.Name)
		case s.Path == dir:
			return fmt.Errorf("there is already a Local site at %s", render.Tilde(p, dir))
		}
	}
	return nil
}

// SitePath is where Local puts a new site.
func SitePath(p paths.Paths, slug string) string { return filepath.Join(p.Home, "Local Sites", slug) }

// API is the part of Local's API create uses.
type API interface {
	AddSite(ctx context.Context, s local.NewSite) (local.Job, error)
	WaitJob(ctx context.Context, id string, poll time.Duration, progress func(local.Job)) (local.Job, error)
}

// Creator runs the steps.
type Creator struct {
	Paths paths.Paths
	Exec  exec.Runner
	Local API
	Poll  time.Duration // job and socket polling (default 2 s)
	Now   func() time.Time
}

// Result is what was made. The password is in it so the caller can show
// it once; it's never written anywhere by taw-fleet.
type Result struct {
	Name          string   `json:"name"`
	Slug          string   `json:"slug"`
	SiteID        string   `json:"site_id"`
	URL           string   `json:"url"`
	AdminURL      string   `json:"admin_url"`
	AdminUser     string   `json:"admin_user"`
	AdminPassword string   `json:"admin_password"`
	Kind          Kind     `json:"kind"`
	ThemeDir      string   `json:"theme_dir"`
	ThemePath     string   `json:"theme_path"`
	TawCore       string   `json:"taw_core,omitempty"`
	Commit        string   `json:"commit,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

// Run creates the site. Progress goes to out, one line per event. On an
// error the message says what exists so far.
func (c *Creator) Run(ctx context.Context, r Request, out io.Writer) (Result, error) {
	say := func(format string, a ...any) { _, _ = fmt.Fprintf(out, format+"\n", a...) }
	now := c.Now
	if now == nil {
		now = time.Now
	}
	poll := c.Poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	res := Result{Name: r.Name, Slug: r.Slug, URL: "http://" + r.Domain, AdminURL: "http://" + r.Domain + "/wp-admin/",
		AdminUser: r.AdminUser, AdminPassword: r.AdminPassword, Kind: r.Kind, ThemeDir: r.ThemeDir}
	sitePath := SitePath(c.Paths, r.Slug)

	// 1. The Local site.
	env := "Local's preferred PHP and web server"
	if r.PHP != "" || r.WebServer != "" {
		env = strings.Trim(strings.Join([]string{phpLabel(r.PHP), r.WebServer}, ", "), ", ")
	}
	say("→ Creating the Local site %s (%s)…", r.Domain, env)
	began := now()
	sctx, cancel := context.WithTimeout(ctx, SiteTimeout)
	defer cancel()
	job, err := c.Local.AddSite(sctx, local.NewSite{Name: r.Name, Path: sitePath, Domain: r.Domain, PHP: r.PHP, WebServer: r.WebServer,
		AdminUser: r.AdminUser, AdminPassword: r.AdminPassword, AdminEmail: r.AdminEmail})
	if err != nil {
		return res, err
	}
	if _, err := c.Local.WaitJob(sctx, job.ID, poll, func(j local.Job) { say("  Local: %s", j.Status) }); err != nil {
		return res, fmt.Errorf("%w (check Local's window; anything it made is at %s)", err, render.Tilde(c.Paths, sitePath))
	}
	s, err := c.findSite(sctx, sitePath, poll)
	if err != nil {
		return res, err
	}
	res.SiteID = s.ID
	say("✓ Site created and running in %s (PHP %s, %s)", now().Sub(began).Round(time.Second), s.PHPVersion, s.WebServer)
	stuck := func(err error) error {
		return fmt.Errorf("%w\nThe site %s exists and runs; finish by hand or delete it in Local", err, r.Slug)
	}

	// 2. The theme, through taw-create.
	themes := filepath.Join(s.WebRoot, "wp-content", "themes")
	res.ThemePath = filepath.Join(themes, r.ThemeDir)
	if _, err := os.Lstat(res.ThemePath); err == nil {
		return res, stuck(fmt.Errorf("wp-content/themes/%s already exists", r.ThemeDir))
	}
	php, ok := local.PHPBinary(c.Paths, s.PHPVersion)
	if !ok {
		return res, stuck(fmt.Errorf("no PHP %s from Local", s.PHPVersion))
	}
	phar, ok := local.ComposerPhar(c.Paths)
	if !ok {
		return res, stuck(errors.New("no composer.phar in the Local app"))
	}
	say("→ Installing %s into wp-content/themes/%s (taw-create)…", r.Kind.Scaffold(), r.ThemeDir)
	began = now()
	if err := c.stream(ctx, ThemeTimeout, out, exec.Spec{Dir: themes, Name: php,
		Args: []string{phar, "create-project", "taw/create", r.ThemeDir, "--no-interaction", "--no-progress", "--repository=" + TawCreateRepo},
		Env:  []string{"TAW_STARTER=" + r.Kind.Starter(), "COMPOSER_NO_INTERACTION=1"}}); err != nil {
		return res, stuck(fmt.Errorf("installing the theme: %w", err))
	}
	if v, err := composer.InstalledVersion(res.ThemePath, composer.CorePackage); err == nil {
		res.TawCore = v
	}
	say("✓ %s installed in %s (taw/core %s)", r.Kind.Scaffold(), now().Sub(began).Round(time.Second), strings.TrimPrefix(orUnknown(res.TawCore), "v"))

	// 3. Front-end assets.
	if built(res.ThemePath) {
		say("✓ front-end assets already built by the installer")
	} else if hasBuildScript(res.ThemePath) {
		if w := c.build(ctx, res.ThemePath, out, say); w != "" {
			res.Warnings = append(res.Warnings, w)
		}
	}

	// 4. Activate it.
	spec, err := local.WPSpec(c.Paths, s, []string{"theme", "activate", r.ThemeDir})
	if err == nil {
		err = c.stream(ctx, time.Minute, out, spec)
	}
	if err != nil {
		res.Warnings = append(res.Warnings, "the theme isn't active yet: "+err.Error()+" (activate it in wp-admin → Appearance → Themes)")
		say("! couldn't activate the theme: %v", err)
	} else {
		say("✓ %s is the active theme", r.ThemeDir)
	}

	// 5. git.
	commit, w := c.gitInit(ctx, res.ThemePath, r, res.TawCore)
	res.Commit = commit
	if w != "" {
		res.Warnings = append(res.Warnings, w)
		say("! %s", w)
	} else {
		say("✓ git repository with a first commit (%s)", commit)
	}
	return res, nil
}

func phpLabel(v string) string {
	if v == "" {
		return ""
	}
	return "PHP " + v
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

// findSite waits for the new site in sites.json (by path) and its socket.
func (c *Creator) findSite(ctx context.Context, path string, poll time.Duration) (site.Site, error) {
	src := scan.NewLocalSource(c.Paths)
	for {
		sites, err := src.Sites(ctx)
		if err == nil {
			for _, s := range sites {
				if s.Path == path && s.SockLive {
					return s, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return site.Site{}, fmt.Errorf("local says the site is ready, but it isn't in sites.json or its database isn't up (%s)", render.Tilde(c.Paths, path))
		case <-time.After(poll):
		}
	}
}

// stream runs a command with its output going to out.
func (c *Creator) stream(ctx context.Context, timeout time.Duration, out io.Writer, spec exec.Spec) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	spec.Stdout, spec.Stderr = out, out
	res, err := c.Exec.Run(ctx, spec)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("%s exited %d (output above)", filepath.Base(spec.Name), res.Code)
	}
	return nil
}

// built reports whether Vite's manifest exists: the installer built the
// assets already (taw-gutenberg's does).
func built(dir string) bool {
	for _, m := range []string{filepath.Join("dist", ".vite", "manifest.json"), filepath.Join("public", "build", "manifest.json")} {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

func hasBuildScript(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	return json.Unmarshal(data, &pkg) == nil && pkg.Scripts["build"] != ""
}

// build runs npm install (when needed) and npm run build. A missing npm or
// a failed build is a warning: the site works, the assets come later.
func (c *Creator) build(ctx context.Context, dir string, out io.Writer, say func(string, ...any)) string {
	npm, err := c.Paths.LookPath("npm")
	if err != nil {
		return "npm isn't on PATH: run `npm install && npm run build` in the theme for its assets"
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); err != nil {
		say("→ npm install…")
		if err := c.stream(ctx, BuildTimeout, out, exec.Spec{Dir: dir, Name: npm, Args: []string{"install", "--no-audit", "--no-fund"}}); err != nil {
			return "npm install failed (" + err.Error() + "): run `npm install && npm run build` in the theme"
		}
	}
	say("→ npm run build…")
	if err := c.stream(ctx, BuildTimeout, out, exec.Spec{Dir: dir, Name: npm, Args: []string{"run", "build"}}); err != nil {
		return "npm run build failed (" + err.Error() + "): run it in the theme"
	}
	say("✓ front-end assets built")
	return ""
}

// gitInit makes the theme a repository with one commit. Without a git
// identity it stops after init and says so.
func (c *Creator) gitInit(ctx context.Context, dir string, r Request, core string) (commit, warning string) {
	git := func(args ...string) (exec.Result, error) {
		gctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		return c.Exec.Run(gctx, exec.Spec{Dir: dir, Name: "git", Args: args})
	}
	if res, err := git("init", "-b", "main"); err != nil || res.Code != 0 {
		return "", "git init failed: " + gitErr(res, err)
	}
	if res, err := git("config", "user.email"); err != nil || res.Code != 0 || strings.TrimSpace(string(res.Stdout)) == "" {
		return "", "git has no identity (git config --global user.email): the theme is a repository with no commit yet"
	}
	if res, err := git("add", "-A"); err != nil || res.Code != 0 {
		return "", "git add failed: " + gitErr(res, err)
	}
	msg := "Start " + r.Name + " from " + r.Kind.Scaffold()
	if core != "" {
		msg += " (taw/core " + strings.TrimPrefix(core, "v") + ")"
	}
	if res, err := git("commit", "-q", "-m", msg); err != nil || res.Code != 0 {
		return "", "git commit failed: " + gitErr(res, err)
	}
	res, err := git("rev-parse", "--short", "HEAD")
	if err != nil || res.Code != 0 {
		return "", "git rev-parse failed: " + gitErr(res, err)
	}
	return strings.TrimSpace(string(res.Stdout)), ""
}

func gitErr(res exec.Result, err error) string {
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(res.Stderr) + string(res.Stdout))
}
