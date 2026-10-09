package cli

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/companion"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/live"
	"github.com/Relmaur/taw-fleet/internal/paths"
)

func seedKey(id string, b byte) companion.Key {
	k, err := companion.ParseKey(id, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)))
	if err != nil {
		panic(err)
	}
	return k
}

// signedSite answers every route with a signed health-like body.
func signedSite(t *testing.T, site companion.Key) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := `{"ok":true,"wp_version":"6.8.3","php_version":"8.2.29","taw_core_version":"v1.76.1","companion_version":"0.3.0","site_public_key":"` +
			site.Public() + `","site_key_id":"` + site.ID + `","plugins":[],"count":0,"findings":[],"entries":[]}`
		site.Sign(w.Header(), "RESPONSE", r.URL.Path, time.Now().Unix(), "resp-nonce-1", []byte(body))
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runLive(t *testing.T, p paths.Paths, in string, r *exec.FakeRunner, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	d := Deps{Paths: p, Runner: r, In: strings.NewReader(in), Out: &out, Err: &out, Dark: true, GitHub: fakeGitHub(t)}
	root := NewRoot(BuildInfo{Version: "1.2.3", Commit: "abc123"}, d)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// keychainFake stores what `security -i` is given.
func keychainFake() *exec.FakeRunner {
	var stored string
	return &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		switch {
		case s.Name == "/usr/bin/security" && s.Args[0] == "-i":
			b, _ := io.ReadAll(s.Stdin)
			_, rest, _ := strings.Cut(string(b), ` -w "`)
			stored, _, _ = strings.Cut(rest, `"`)
			return exec.Result{}, nil
		case s.Name == "/usr/bin/security" && s.Args[0] == "find-generic-password":
			if stored == "" {
				return exec.Result{Code: 44}, nil
			}
			return exec.Result{Stdout: []byte(stored + "\n")}, nil
		}
		return exec.Result{}, nil
	}}
}

func TestLiveKeyAndTrust(t *testing.T) {
	p := fixture(t)
	site := seedKey("site-acme", 0x5b)
	srv := signedSite(t, site)
	write(t, filepath.Join(p.ConfigDir, "config.toml"), "[sites.acme]\nproduction_url = \""+srv.URL+"\"\n")
	kc := keychainFake()

	if _, err := runLive(t, p, "", kc, "live"); err == nil || !strings.Contains(err.Error(), "live key import") {
		t.Errorf("no key yet: %v", err)
	}
	hub := seedKey("taw-fleet", 0x2a)
	out, err := runLive(t, p, hub.Encode()+"\n", kc, "live", "key", "import", "--key-id", "taw-fleet")
	if err != nil || !strings.Contains(out, "Public key: "+hub.Public()) {
		t.Fatalf("import: %q %v", out, err)
	}
	if out, _ := runLive(t, p, "", kc, "live", "key", "show"); !strings.Contains(out, "taw-fleet") || strings.Contains(out, hub.Encode()) {
		t.Errorf("show must print the public key only: %q", out)
	}
	if _, err := runLive(t, p, "", kc, "live", "key", "new"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("new without --force: %v", err)
	}

	out, err = runLive(t, p, "", kc, "live", "acme")
	if err != nil || !strings.Contains(out, "▲ not verified") || !strings.Contains(out, "taw-fleet live trust acme") {
		t.Errorf("unpinned: %q %v", out, err)
	}
	out, err = runLive(t, p, "", kc, "live", "trust", "acme", "--yes")
	if err != nil || !strings.Contains(out, "presents key") || !strings.Contains(out, "Pinned site-acme") {
		t.Fatalf("trust: %q %v", out, err)
	}
	out, err = runLive(t, p, "", kc, "live")
	if err != nil || !strings.Contains(out, "✓ verified site-acme") || !strings.Contains(out, "WordPress 6.8.3") {
		t.Errorf("verified: %q %v", out, err)
	}
	if _, err := os.Stat(live.PinsFile(p)); err != nil {
		t.Error(err)
	}
	if out, err := runLive(t, p, "", kc, "live", "trust", "acme", "--key", site.Public(), "--key-id", "site-acme", "--yes"); err != nil || !strings.Contains(out, "already pinned") {
		t.Errorf("same key: %q %v", out, err)
	}
	out, err = runLive(t, p, "", kc, "doctor")
	if err != nil || !strings.Contains(out, "acme") {
		t.Errorf("doctor: %q %v", out, err)
	}
}

func TestLiveNeedsProductionURLs(t *testing.T) {
	p := fixture(t)
	if _, err := runLive(t, p, "", keychainFake(), "live"); err == nil || !strings.Contains(err.Error(), "production_url") {
		t.Errorf("err = %v", err)
	}
}
