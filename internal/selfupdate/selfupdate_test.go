package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarball(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		body []byte
	}{{"README.md", []byte("readme")}, {name, body}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// server publishes v0.7.1 with an arm64 archive; sums overrides checksums.txt.
func server(t *testing.T, archive []byte, sums string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/Relmaur/taw-fleet/releases/latest":
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("auth = %q", r.Header.Get("Authorization"))
			}
			_, _ = fmt.Fprintf(w, `{"tag_name":"v0.7.1","html_url":"https://github.com/Relmaur/taw-fleet/releases/tag/v0.7.1",
				"assets":[{"name":"taw-fleet_0.7.1_darwin_arm64.tar.gz","browser_download_url":"%[1]s/dl/a.tgz"},
				{"name":"checksums.txt","browser_download_url":"%[1]s/dl/checksums.txt"}]}`, srv.URL)
		case "/dl/a.tgz":
			if r.Header.Get("Authorization") != "" {
				t.Error("the token must only go to the API")
			}
			_, _ = w.Write(archive)
		case "/dl/checksums.txt":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func updater(srv *httptest.Server) *Updater {
	u := New(func() string { return "tok" })
	u.BaseURL, u.GOOS, u.GOARCH = srv.URL, "darwin", "arm64"
	return u
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestApplyReplacesTheBinary(t *testing.T) {
	archive := tarball(t, "taw-fleet", []byte("NEW BINARY"))
	srv := server(t, archive, "deadbeef  other.tar.gz\n"+sumOf(archive)+"  taw-fleet_0.7.1_darwin_arm64.tar.gz\n")
	u := updater(srv)
	rel, err := u.Latest(context.Background())
	if err != nil || rel.Version() != "0.7.1" {
		t.Fatalf("rel=%+v err=%v", rel, err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "taw-fleet")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := Target(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Apply(context.Background(), rel, resolved); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	fi, _ := os.Stat(target)
	if string(got) != "NEW BINARY" || fi.Mode().Perm() != 0o755 {
		t.Errorf("got %q %v", got, fi.Mode())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestApplyRefusesABadChecksum(t *testing.T) {
	archive := tarball(t, "taw-fleet", []byte("EVIL"))
	srv := server(t, archive, strings.Repeat("0", 64)+"  taw-fleet_0.7.1_darwin_arm64.tar.gz\n")
	u := updater(srv)
	rel, _ := u.Latest(context.Background())
	target := filepath.Join(t.TempDir(), "taw-fleet")
	_ = os.WriteFile(target, []byte("OLD"), 0o755)
	if err := u.Apply(context.Background(), rel, target); !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "OLD" {
		t.Errorf("binary changed: %q", got)
	}
}

func TestApplyNeedsTheRightAssets(t *testing.T) {
	archive := tarball(t, "something-else", []byte("x"))
	srv := server(t, archive, sumOf(archive)+"  taw-fleet_0.7.1_darwin_arm64.tar.gz\n")
	u := updater(srv)
	rel, _ := u.Latest(context.Background())
	target := filepath.Join(t.TempDir(), "taw-fleet")
	if err := u.Apply(context.Background(), rel, target); err == nil || !strings.Contains(err.Error(), "no taw-fleet in the archive") {
		t.Errorf("missing binary: %v", err)
	}
	u.GOARCH = "amd64"
	if err := u.Apply(context.Background(), rel, target); err == nil || !strings.Contains(err.Error(), "darwin_amd64") {
		t.Errorf("missing arch: %v", err)
	}
}

func TestHomebrewIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Caskroom", "taw-fleet", "0.7.0")
	_ = os.MkdirAll(dir, 0o755)
	exe := filepath.Join(dir, "taw-fleet")
	_ = os.WriteFile(exe, nil, 0o755)
	if _, err := Target(exe); !errors.Is(err, ErrHomebrew) {
		t.Errorf("err = %v", err)
	}
	if !Homebrew("/opt/homebrew/Cellar/taw-fleet/0.7.0/bin/taw-fleet") || Homebrew("/usr/local/bin/taw-fleet") {
		t.Error("Homebrew()")
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		running, tag string
		want         bool
	}{
		{"0.7.0", "v0.7.1", true}, {"v0.7.1", "v0.7.1", false}, {"0.8.0", "v0.7.1", false},
		{"dev", "v0.7.1", true}, {"v0.7.0-rc.1", "v0.7.0", true},
	}
	for _, c := range cases {
		if got := Newer(c.running, c.tag); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.running, c.tag, got)
		}
	}
}

func TestLatestRejectsGarbage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()
	u := updater(srv)
	if _, err := u.Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("err = %v", err)
	}
}
