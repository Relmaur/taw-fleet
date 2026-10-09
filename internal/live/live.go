// Package live checks the production sites through their TAW companion:
// the signing key in the macOS Keychain, the sites' pinned keys, a parallel
// probe of /health, /inventory, /vulnerabilities and /logs, and a short cache.
package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Relmaur/taw-fleet/internal/companion"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// CacheTTL is how long a probe result is reused.
const CacheTTL = 5 * time.Minute

// ErrNoKey means no signing key is stored yet.
var ErrNoKey = errors.New("no signing key: import it with `taw-fleet live key import`")

// --- Keychain -----------------------------------------------------------------

const (
	keychainService = "taw-fleet"
	keychainAccount = "companion-signing-key"
)

// Keychain stores the fleet's signing key in the login keychain through
// /usr/bin/security. The secret goes in on stdin (`security -i`), never on
// the command line where other processes could see it.
type Keychain struct{ Exec exec.Runner }

// Save stores the key, replacing an earlier one.
func (k Keychain) Save(ctx context.Context, key companion.Key) error {
	cmd := fmt.Sprintf("add-generic-password -U -s %s -a %s -l \"taw-fleet companion signing key\" -w \"%s|%s\"\n",
		keychainService, keychainAccount, key.ID, key.Encode())
	res, err := k.Exec.Run(ctx, exec.Spec{Name: "/usr/bin/security", Args: []string{"-i"}, Stdin: strings.NewReader(cmd)})
	if err != nil {
		return err
	}
	if res.Code != 0 || strings.Contains(string(res.Stderr), "rror") {
		return fmt.Errorf("keychain: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// Load reads the key; ErrNoKey when there is none.
func (k Keychain) Load(ctx context.Context) (companion.Key, error) {
	res, err := k.Exec.Run(ctx, exec.Spec{Name: "/usr/bin/security", Args: []string{"find-generic-password", "-s", keychainService, "-a", keychainAccount, "-w"}})
	if err != nil {
		return companion.Key{}, err
	}
	if res.Code == 44 { // errSecItemNotFound
		return companion.Key{}, ErrNoKey
	}
	if res.Code != 0 {
		return companion.Key{}, fmt.Errorf("keychain: %s", strings.TrimSpace(string(res.Stderr)))
	}
	id, b64, ok := strings.Cut(strings.TrimSpace(string(res.Stdout)), "|")
	if !ok {
		return companion.Key{}, errors.New("keychain: the stored signing key is unreadable; import it again")
	}
	return companion.ParseKey(id, b64)
}

// --- pinned site keys ---------------------------------------------------------

// Pins are the sites' public keys, like ssh's known_hosts:
// ~/.config/taw-fleet/companion-keys.json, keyed by Local site folder.
type Pins map[string]companion.SiteKey

// PinsFile is where the pins live.
func PinsFile(p paths.Paths) string { return filepath.Join(p.ConfigDir, "companion-keys.json") }

// LoadPins reads the pins; a missing file is no pins.
func LoadPins(p paths.Paths) (Pins, error) {
	data, err := os.ReadFile(PinsFile(p))
	if errors.Is(err, os.ErrNotExist) {
		return Pins{}, nil
	}
	if err != nil {
		return nil, err
	}
	pins := Pins{}
	if err := json.Unmarshal(data, &pins); err != nil {
		return nil, fmt.Errorf("%s: %w", PinsFile(p), err)
	}
	return pins, nil
}

// Save writes the pins.
func (pins Pins) Save(p paths.Paths) error {
	if err := os.MkdirAll(p.ConfigDir, 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(pins, "", "  ")
	tmp := PinsFile(p) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, PinsFile(p))
}

// --- probe --------------------------------------------------------------------

// Target is one production site.
type Target struct {
	Slug string // Local site folder
	URL  string // production_url
}

// Prober checks live sites.
type Prober struct {
	Client   *companion.Client
	Pins     Pins
	CacheDir string // "" = no cache
	Now      func() time.Time
	Logs     int // log lines to fetch (0 = 5)
}

// Probe asks one site's companion for its health, inventory,
// vulnerabilities and recent logs. It never returns an error: what went
// wrong is in the result.
func (pr *Prober) Probe(ctx context.Context, t Target) site.Production {
	began := pr.now()
	out := site.Production{URL: t.URL, CheckedAt: began}
	pin, pinned := pr.Pins[t.Slug]
	s := companion.Site{URL: t.URL}
	if pinned {
		s.Pinned = &pin
	}
	fail := func(err error) site.Production {
		out.Error, out.ErrorKind = err.Error(), kindOf(err)
		out.TookMS = pr.now().Sub(began).Milliseconds()
		return out
	}
	resp, err := pr.Client.Get(ctx, s, "health", nil)
	if err != nil {
		return fail(err)
	}
	h, err := companion.Decode[companion.Health](resp)
	if err != nil {
		return fail(err)
	}
	out.Reachable, out.Verified, out.KeyID = true, resp.Verified, resp.KeyID
	out.WP, out.PHP, out.TawCore, out.Companion = h.WPVersion, h.PHPVersion, h.TawCoreVersion, h.CompanionVersion
	if !pinned {
		out.Error, out.ErrorKind = "no pinned key for this site yet: run `taw-fleet live trust "+t.Slug+"`", "no-key"
	}

	// The rest in parallel; a failure there is kept but doesn't hide health.
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []string
	)
	get := func(route string, q url.Values, use func(companion.Response) error) {
		wg.Go(func() {
			r, err := pr.Client.Get(ctx, s, route, q)
			var api *companion.APIError
			if errors.As(err, &api) && api.Status == 404 {
				err = fmt.Errorf("needs a newer companion (this site has %s)", orUnknown(h.CompanionVersion))
			}
			if err == nil {
				err = use(r)
			}
			if err != nil {
				mu.Lock()
				errs = append(errs, route+": "+err.Error())
				if out.ErrorKind == "" || kindOf(err) == "signature" {
					out.ErrorKind = kindOf(err)
				}
				mu.Unlock()
			}
		})
	}
	get("inventory", nil, func(r companion.Response) error {
		inv, err := companion.Decode[companion.Inventory](r)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		out.HasInventory = true
		out.Plugins = len(inv.Plugins)
		for _, p := range inv.Plugins {
			if p.UpdateVersion != "" && p.UpdateVersion != p.Version {
				out.PluginUpdates = append(out.PluginUpdates, fmt.Sprintf("%s %s → %s", p.Slug, p.Version, p.UpdateVersion))
			}
		}
		sort.Strings(out.PluginUpdates)
		return nil
	})
	get("vulnerabilities", nil, func(r companion.Response) error {
		v, err := companion.Decode[companion.Vulnerabilities](r)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		out.HasVulns = true
		if v.Scanner != nil {
			out.Scanner = v.Scanner.Name
		}
		for _, f := range v.Findings {
			out.Vulns = append(out.Vulns, site.LiveVuln{Component: strings.TrimSpace(f.ComponentType + " " + f.Slug + " " + f.InstalledVersion),
				Severity: strings.ToLower(f.Severity), Title: f.Title, Link: f.Link})
			if rank(f.Severity) > rank(out.WorstSeverity) {
				out.WorstSeverity = strings.ToLower(f.Severity)
			}
		}
		return nil
	})
	n := pr.Logs
	if n <= 0 {
		n = 5
	}
	get("logs", url.Values{"limit": {fmt.Sprint(n)}}, func(r companion.Response) error {
		l, err := companion.Decode[companion.Logs](r)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, e := range l.Entries {
			out.Logs = append(out.Logs, site.LiveLog{TS: e.TS, Level: e.Level, Code: e.Code, Message: e.Message})
		}
		return nil
	})
	wg.Wait()
	if len(errs) > 0 {
		sort.Strings(errs)
		if out.Error != "" {
			errs = append([]string{out.Error}, errs...)
		}
		out.Error = strings.Join(errs, "; ")
	}
	if out.ErrorKind == "signature" {
		out.Verified = false
	}
	out.TookMS = pr.now().Sub(began).Milliseconds()
	return out
}

// ProbeAll checks every target in parallel, using the cache unless fresh is
// set. Results are keyed by slug.
func (pr *Prober) ProbeAll(ctx context.Context, targets []Target, fresh bool) map[string]site.Production {
	out := make(map[string]site.Production, len(targets))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Go(func() {
			p, ok := pr.cached(t)
			if fresh || !ok {
				p = pr.Probe(ctx, t)
				pr.save(t, p)
			}
			mu.Lock()
			out[t.Slug] = p
			mu.Unlock()
		})
	}
	wg.Wait()
	return out
}

// Apply puts the results on the report's sites.
func Apply(sites []site.Site, results map[string]site.Production) {
	for i := range sites {
		if p, ok := results[sites[i].Slug]; ok {
			sites[i].Production = &p
		}
	}
}

func (pr *Prober) cacheFile(slug string) string {
	return filepath.Join(pr.CacheDir, "live", slug+".json")
}

func (pr *Prober) cached(t Target) (site.Production, bool) {
	if pr.CacheDir == "" {
		return site.Production{}, false
	}
	data, err := os.ReadFile(pr.cacheFile(t.Slug))
	if err != nil {
		return site.Production{}, false
	}
	var p site.Production
	if json.Unmarshal(data, &p) != nil || p.URL != t.URL || pr.now().Sub(p.CheckedAt) > CacheTTL {
		return site.Production{}, false
	}
	return p, true
}

// save is best effort; logs aren't cached (they're the live part).
func (pr *Prober) save(t Target, p site.Production) {
	if pr.CacheDir == "" {
		return
	}
	f := pr.cacheFile(t.Slug)
	if os.MkdirAll(filepath.Dir(f), 0o755) != nil {
		return
	}
	data, _ := json.Marshal(p)
	_ = os.WriteFile(f, data, 0o644)
}

func (pr *Prober) now() time.Time {
	if pr.Now == nil {
		return time.Now()
	}
	return pr.Now()
}

func orUnknown(v string) string {
	if v == "" {
		return "an unknown version"
	}
	return v
}

func kindOf(err error) string {
	var api *companion.APIError
	switch {
	case errors.Is(err, companion.ErrBadSignature), errors.Is(err, companion.ErrKeyChanged), errors.Is(err, companion.ErrUnsigned), errors.Is(err, companion.ErrStale):
		return "signature"
	case errors.As(err, &api) && api.Status == 401, errors.As(err, &api) && api.Status == 403:
		return "auth"
	case errors.As(err, &api):
		return "companion"
	}
	return "unreachable"
}

func rank(sev string) int {
	switch strings.ToLower(sev) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}
