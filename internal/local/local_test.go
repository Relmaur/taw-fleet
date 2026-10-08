package local

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// fixtureHome builds a fake home with Local's files copied from testdata.
func fixtureHome(t *testing.T, files ...string) paths.Paths {
	t.Helper()
	home := t.TempDir()
	p := paths.ForHome(home, nil)
	p.LocalApp = filepath.Join(home, "Applications", "Local.app")
	if err := os.MkdirAll(p.LocalSupport, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p.LocalSupport, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func byID(sites []RawSite) map[string]RawSite {
	m := map[string]RawSite{}
	for _, s := range sites {
		m[s.ID] = s
	}
	return m
}

func TestLoadRegistryMissingIsErrNoLocal(t *testing.T) {
	p := fixtureHome(t)
	if _, err := LoadRegistry(p); !errors.Is(err, ErrNoLocal) {
		t.Errorf("err = %v, want ErrNoLocal", err)
	}
}

func TestLoadRegistryInvalidJSON(t *testing.T) {
	p := fixtureHome(t)
	if err := os.WriteFile(RegistryFile(p), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(p); err == nil || errors.Is(err, ErrNoLocal) {
		t.Errorf("err = %v, want a parse error", err)
	}
}

func TestLoadRegistry(t *testing.T) {
	p := fixtureHome(t, "sites.json")
	sites, err := LoadRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 4 {
		t.Fatalf("got %d sites, want 4", len(sites))
	}
	// Sorted by name, case-insensitively.
	var names []string
	for _, s := range sites {
		names = append(names, s.Name)
	}
	want := []string{"Alpha Theme Site", "beta", "Broken", "No path"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}

	m := byID(sites)
	a := m["aaa111"]
	if a.Path != filepath.Join(p.Home, "Local Sites", "alpha") {
		t.Errorf("~ not expanded: %q", a.Path)
	}
	if a.PHPVersion != "8.2.30" || a.MySQLVersion != "8.4.0" || a.MySQLPort != 10004 || a.HTTPPort != 10003 {
		t.Errorf("services = %+v", a)
	}
	if a.WebServer != "nginx" {
		t.Errorf("WebServer from the http service = %q", a.WebServer)
	}
	if len(a.Hosts) != 1 || a.Hosts[0] != (Host{HostID: "wpe", Env: "staging"}) {
		t.Errorf("Hosts = %+v", a.Hosts)
	}
	if a.ParseErr != nil {
		t.Errorf("ParseErr = %v", a.ParseErr)
	}

	b := m["bbb222"]
	if webServerName("nginx-1.26.1") != "nginx 1.26.1" || webServerName("apache") != "apache" || webServerName("x-") != "x-" {
		t.Error("webServerName")
	}
	if b.Path != "/abs/Local Sites/beta" || b.MultiSite != "ms-subdir" || !b.Xdebug {
		t.Errorf("beta = %+v", b)
	}
	if b.MySQLVersion != "10.11.18" || b.MySQLPort != 10014 || b.PHPVersion != "8.5.3" {
		t.Errorf("mariadb/php = %+v", b)
	}

	if m["ccc333"].ParseErr == nil {
		t.Error("a path of the wrong type must set ParseErr")
	}
	if d := m["ddd444"]; d.ParseErr == nil || d.WebServer != "" {
		t.Errorf("no path must set ParseErr, odd webServer ignored: %+v", d)
	}
}

func TestLoadStatuses(t *testing.T) {
	p := fixtureHome(t, "site-statuses.json")
	st, err := LoadStatuses(p)
	if err != nil {
		t.Fatal(err)
	}
	if st["aaa111"] != site.StatusRunning || st["bbb222"] != site.StatusHalted || st["ddd444"] != site.StatusBusy {
		t.Errorf("statuses = %v", st)
	}
	if _, ok := st["zzz"]; ok {
		t.Error("unknown id present")
	}
}

func TestLoadStatusesMissingFile(t *testing.T) {
	st, err := LoadStatuses(fixtureHome(t))
	if err != nil || len(st) != 0 {
		t.Errorf("st=%v err=%v", st, err)
	}
}

func TestSocket(t *testing.T) {
	p := fixtureHome(t)
	path := SocketPath(p, "aaa111")
	if want := filepath.Join(p.LocalSupport, "run", "aaa111", "mysql", "mysqld.sock"); path != want {
		t.Errorf("SocketPath = %q", path)
	}
	if SocketLive(path) {
		t.Error("no socket yet")
	}
	// A real unix socket, like Local's: SocketLive must not require a regular file.
	// The temp dir path can exceed the 104-byte sun_path limit, so listen in a short one.
	dir, err := os.MkdirTemp("/tmp", "tf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", sock)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	defer func() { _ = l.Close() }()
	if !SocketLive(sock) {
		t.Error("a live socket must count")
	}
}

func TestPHPBinary(t *testing.T) {
	p := fixtureHome(t)
	plat := "darwin"
	if runtime.GOARCH == "arm64" {
		plat = "darwin-arm64"
	}
	mk := func(root, dir string) string {
		bin := filepath.Join(root, dir, "bin", plat, "bin", "php")
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return bin
	}
	user := filepath.Join(p.LocalSupport, "lightning-services")
	bundle := filepath.Join(p.LocalApp, "Contents", "Resources", "extraResources", "lightning-services")

	mk(user, "php-8.2.30+0")
	newer := mk(user, "php-8.2.30+1")
	bundled := mk(bundle, "php-8.2.29+0")

	if got, ok := PHPBinary(p, "8.2.30"); !ok || got != newer {
		t.Errorf("8.2.30 = %q %v, want the highest build %q", got, ok, newer)
	}
	if got, ok := PHPBinary(p, "8.2.29"); !ok || got != bundled {
		t.Errorf("8.2.29 = %q %v, want the bundle's %q", got, ok, bundled)
	}
	if _, ok := PHPBinary(p, "7.4.0"); ok {
		t.Error("7.4.0 must not be found")
	}
	if _, ok := PHPBinary(p, ""); ok {
		t.Error("an empty version must not be found")
	}
}

func TestComposerPhar(t *testing.T) {
	p := fixtureHome(t)
	if _, ok := ComposerPhar(p); ok {
		t.Error("no composer yet")
	}
	f := filepath.Join(p.LocalApp, "Contents", "Resources", "extraResources", "bin", "composer", "composer.phar")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := ComposerPhar(p); !ok || got != f {
		t.Errorf("ComposerPhar = %q %v", got, ok)
	}
}
