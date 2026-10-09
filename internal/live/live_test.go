package live

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/companion"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Test keys from fixed seeds (the same as taw-hub's signing vectors).
var (
	hubKey  = mustKey("hub-local", 0x2a)
	siteKey = mustKey("site-acme", 0x5b)
)

func mustKey(id string, b byte) companion.Key {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = b
	}
	k, err := companion.ParseKey(id, base64.StdEncoding.EncodeToString(seed))
	if err != nil {
		panic(err)
	}
	return k
}

// fakeCompanion checks requests like the PHP plugin (signature over the
// path without query, ±60 s) and signs every answer with the site key.
func fakeCompanion(t *testing.T, signWith companion.Key, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		reply := func(status int, body string) {
			signWith.Sign(w.Header(), "RESPONSE", r.URL.Path, time.Now().Unix(), "resp-nonce-1", []byte(body))
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
		ts, _ := strconv.ParseInt(r.Header.Get(companion.HeaderTimestamp), 10, 64)
		sig, _ := base64.StdEncoding.DecodeString(r.Header.Get(companion.HeaderSignature))
		canon := companion.Canonical(r.Method, r.URL.Path, ts, r.Header.Get(companion.HeaderNonce), nil)
		if r.Header.Get(companion.HeaderKeyID) != hubKey.ID || !ed25519.Verify(hubKey.Private.Public().(ed25519.PublicKey), []byte(canon), sig) {
			reply(401, `{"code":"taw_hub_unauthorized","message":"unauthorized","data":{"status":401,"reason":"invalid_signature"}}`)
			return
		}
		switch strings.TrimPrefix(r.URL.Path, companion.Namespace) {
		case "/health":
			reply(200, `{"ok":true,"php_version":"8.2.29","wp_version":"6.8.3","taw_core_version":"v1.76.1","companion_version":"0.2.0","site_public_key":"`+siteKey.Public()+`","site_key_id":"site-acme","exec_available":true}`)
		case "/inventory":
			reply(200, `{"schema_version":1,"wp_version":"6.8.3","plugins":[{"slug":"akismet","version":"5.1","active":true,"update_version":"5.3"},{"slug":"wpmudev","version":"4.0","active":true,"update_version":""}],"mu_plugins":[]}`)
		case "/vulnerabilities":
			reply(200, `{"scanner":{"name":"Defender","version":"4.1","last_scan_at":"2026-10-08T00:00:00Z"},"count":2,"findings":[{"component_type":"plugin","slug":"akismet","installed_version":"5.1","severity":"medium","title":"XSS"},{"component_type":"plugin","slug":"old","installed_version":"1.0","severity":"HIGH","title":"RCE"}]}`)
		case "/content":
			src := "http://" + r.Host
			if v := contentFrom.Load(); v != nil && v.(string) != "" {
				src = v.(string)
			}
			reply(200, `{"meta":{"schema":"1.2","source":{"url":"`+src+`"}},"posts":[{"slug":"about"}],"media":[]}`)
		case "/logs":
			if r.URL.Query().Get("limit") != "5" {
				t.Errorf("limit = %q", r.URL.Query().Get("limit"))
			}
			reply(200, `{"count":1,"entries":[{"ts":"2026-10-08T10:00:00Z","level":"error","code":"mail.failed","message":"SMTP refused"}]}`)
		default:
			reply(404, `{"code":"rest_no_route"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// contentFrom overrides the snapshot's meta.source.url in fakeCompanion.
var contentFrom atomic.Value

func prober(pins Pins) *Prober {
	return &Prober{Client: companion.NewClient(hubKey), Pins: pins}
}

func TestProbeVerified(t *testing.T) {
	var calls atomic.Int32
	srv := fakeCompanion(t, siteKey, &calls)
	pins := Pins{"acme": {ID: "site-acme", Public: siteKey.Public()}}
	p := prober(pins).Probe(context.Background(), Target{Slug: "acme", URL: srv.URL})
	if !p.Reachable || !p.Verified || p.Error != "" || p.TawCore != "v1.76.1" || p.WP != "6.8.3" || p.Companion != "0.2.0" {
		t.Fatalf("p = %+v", p)
	}
	if p.Plugins != 2 || len(p.PluginUpdates) != 1 || p.PluginUpdates[0] != "akismet 5.1 → 5.3" {
		t.Errorf("plugins: %d %v", p.Plugins, p.PluginUpdates)
	}
	if p.Scanner != "Defender" || len(p.Vulns) != 2 || p.WorstSeverity != "high" {
		t.Errorf("vulns: %+v", p)
	}
	if len(p.Logs) != 1 || p.Logs[0].Code != "mail.failed" {
		t.Errorf("logs: %+v", p.Logs)
	}
	if calls.Load() != 4 {
		t.Errorf("%d calls", calls.Load())
	}
}

func TestProbeProblems(t *testing.T) {
	var calls atomic.Int32
	srv := fakeCompanion(t, siteKey, &calls)

	// Not pinned: readable, flagged.
	p := prober(Pins{}).Probe(context.Background(), Target{Slug: "acme", URL: srv.URL})
	if !p.Reachable || p.Verified || p.ErrorKind != "no-key" || !strings.Contains(p.Error, "taw-fleet live trust acme") {
		t.Errorf("unpinned: %+v", p)
	}

	// The site signs with another key than the pinned one.
	impostor := mustKey("site-acme", 0x11)
	bad := fakeCompanion(t, impostor, &calls)
	p = prober(Pins{"acme": {ID: "site-acme", Public: siteKey.Public()}}).Probe(context.Background(), Target{Slug: "acme", URL: bad.URL})
	if p.Reachable || p.ErrorKind != "signature" {
		t.Errorf("impostor: %+v", p)
	}

	// Our key isn't trusted by the site.
	pr := prober(Pins{})
	pr.Client = companion.NewClient(mustKey("hub-local", 0x33))
	p = pr.Probe(context.Background(), Target{Slug: "acme", URL: srv.URL})
	if p.Reachable || p.ErrorKind != "auth" || !strings.Contains(p.Error, "doesn't trust taw-fleet's signing key") {
		t.Errorf("untrusted key: %+v", p)
	}

	// Nothing listening.
	p = prober(Pins{}).Probe(context.Background(), Target{Slug: "acme", URL: "http://127.0.0.1:1"})
	if p.Reachable || p.ErrorKind != "unreachable" {
		t.Errorf("down: %+v", p)
	}
}

func TestProbeAllUsesTheCache(t *testing.T) {
	var calls atomic.Int32
	srv := fakeCompanion(t, siteKey, &calls)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	pr := prober(Pins{"acme": {ID: "site-acme", Public: siteKey.Public()}})
	pr.CacheDir = t.TempDir()
	pr.Now = func() time.Time { return now }
	targets := []Target{{Slug: "acme", URL: srv.URL}}
	if r := pr.ProbeAll(context.Background(), targets, false); !r["acme"].Verified {
		t.Fatalf("first: %+v", r)
	}
	pr.ProbeAll(context.Background(), targets, false)
	if calls.Load() != 4 {
		t.Errorf("cached: %d calls", calls.Load())
	}
	pr.ProbeAll(context.Background(), targets, true)
	if calls.Load() != 8 {
		t.Errorf("fresh: %d calls", calls.Load())
	}
	now = now.Add(CacheTTL + time.Second)
	pr.ProbeAll(context.Background(), targets, false)
	if calls.Load() != 12 {
		t.Errorf("expired: %d calls", calls.Load())
	}
	sites := []site.Site{{Slug: "acme"}, {Slug: "other"}}
	Apply(sites, map[string]site.Production{"acme": {URL: srv.URL}})
	if sites[0].Production == nil || sites[1].Production != nil {
		t.Error("Apply")
	}
}

func TestPins(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	pins, err := LoadPins(p)
	if err != nil || len(pins) != 0 {
		t.Fatalf("empty: %v %v", pins, err)
	}
	pins["acme"] = companion.SiteKey{ID: "site-acme", Public: siteKey.Public()}
	if err := pins.Save(p); err != nil {
		t.Fatal(err)
	}
	again, err := LoadPins(p)
	if err != nil || again["acme"].ID != "site-acme" {
		t.Errorf("round trip: %v %v", again, err)
	}
	if _, err := os.Stat(filepath.Join(p.ConfigDir, "companion-keys.json")); err != nil {
		t.Error(err)
	}
}

func TestKeychain(t *testing.T) {
	var stdin string
	f := &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		switch s.Args[0] {
		case "-i":
			b, _ := io.ReadAll(s.Stdin)
			stdin = string(b)
			return exec.Result{}, nil
		case "find-generic-password":
			if stdin == "" {
				return exec.Result{Code: 44}, nil
			}
			return exec.Result{Stdout: []byte("hub-local|" + hubKey.Encode() + "\n")}, nil
		}
		return exec.Result{Code: 1}, nil
	}}
	kc := Keychain{Exec: f}
	if _, err := kc.Load(context.Background()); !errors.Is(err, ErrNoKey) {
		t.Errorf("empty: %v", err)
	}
	if err := kc.Save(context.Background(), hubKey); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		if strings.Contains(strings.Join(c.Args, " "), hubKey.Encode()) {
			t.Error("the secret must never be on the command line")
		}
	}
	if !strings.Contains(stdin, "-s taw-fleet -a companion-signing-key") || !strings.Contains(stdin, "hub-local|"+hubKey.Encode()) {
		t.Errorf("stdin = %q", stdin)
	}
	k, err := kc.Load(context.Background())
	if err != nil || k.Public() != hubKey.Public() || k.ID != "hub-local" {
		t.Errorf("load: %v", err)
	}
}

func TestContent(t *testing.T) {
	var calls atomic.Int32
	srv := fakeCompanion(t, siteKey, &calls)
	pins := Pins{"acme": {ID: "site-acme", Public: siteKey.Public()}}
	body, err := prober(pins).Content(context.Background(), Target{Slug: "acme", URL: srv.URL})
	if err != nil || !strings.Contains(string(body), `"slug":"about"`) {
		t.Fatalf("Content = %s, %v", body, err)
	}
	if _, err := prober(Pins{}).Content(context.Background(), Target{Slug: "acme", URL: srv.URL}); err == nil || !strings.Contains(err.Error(), "live trust") {
		t.Errorf("unpinned: %v", err)
	}
	if _, err := prober(Pins{"acme": {ID: "site-acme", Public: hubKey.Public()}}).Content(context.Background(), Target{Slug: "acme", URL: srv.URL}); err == nil {
		t.Error("a bad signature isn't imported")
	}
	contentFrom.Store("https://elsewhere.example")
	defer contentFrom.Store("")
	if _, err := prober(pins).Content(context.Background(), Target{Slug: "acme", URL: srv.URL}); err == nil || !strings.Contains(err.Error(), "comes from https://elsewhere.example") {
		t.Errorf("another site's snapshot: %v", err)
	}
}
