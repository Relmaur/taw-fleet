package vite

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// serve listens on a free local port, like Vite.
func serve(t *testing.T) (net.Listener, string) {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, "http://" + l.Addr().String()
}

func TestRunning(t *testing.T) {
	dir := t.TempDir()
	if Running(context.Background(), dir) != "" {
		t.Error("no hot file: not running")
	}
	l, u := serve(t)
	write(t, filepath.Join(dir, "public/build/hot"), u+"\n")
	if got := Running(context.Background(), dir); got != u {
		t.Errorf("Running = %q, want %q", got, u)
	}
	_ = l.Close()
	if Running(context.Background(), dir) != "" {
		t.Error("a hot file left behind by a killed Vite isn't a running server")
	}

	// taw-gutenberg's place, and junk in the file.
	g := t.TempDir()
	_, u2 := serve(t)
	write(t, filepath.Join(g, "dist/hot"), u2)
	if Running(context.Background(), g) != u2 {
		t.Error("dist/hot is taw-gutenberg's hot file")
	}
	write(t, filepath.Join(g, "dist/hot"), "not a url")
	if Running(context.Background(), g) != "" {
		t.Error("junk isn't a server")
	}
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	if _, err := Detect(dir); err == nil || !strings.Contains(err.Error(), "no package.json") {
		t.Errorf("no package.json: %v", err)
	}
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"build":"vite build"}}`)
	if _, err := Detect(dir); !errors.Is(err, ErrNoDevScript) {
		t.Errorf("no dev script: %v", err)
	}
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"dev":"vite"}}`)
	d, err := Detect(dir)
	if err != nil || d != (Dev{Manager: "npm", Install: true}) {
		t.Errorf("Detect = %+v, %v", d, err)
	}
	if got := d.Commands(); !reflect.DeepEqual(got, []string{"npm install", "npm run dev"}) {
		t.Errorf("Commands = %v", got)
	}
	write(t, filepath.Join(dir, "pnpm-lock.yaml"), "")
	write(t, filepath.Join(dir, "node_modules/.keep"), "")
	if d, _ := Detect(dir); d != (Dev{Manager: "pnpm"}) || !reflect.DeepEqual(d.Commands(), []string{"pnpm run dev"}) {
		t.Errorf("pnpm with node_modules: %+v", d)
	}
}

func TestStopKillsOnlyTheThemesVite(t *testing.T) {
	dir := t.TempDir()
	themeDir, _ := filepath.EvalSymlinks(dir)
	l, u := serve(t)
	write(t, filepath.Join(dir, "public/build/hot"), u)
	_, port, _ := net.SplitHostPort(l.Addr().String())

	cwd := themeDir
	r := &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		switch {
		case s.Name == "/usr/sbin/lsof" && s.Args[1] == "-iTCP:"+port:
			return exec.Result{Stdout: []byte("4242\n")}, nil
		case s.Name == "/usr/sbin/lsof":
			return exec.Result{Stdout: []byte("p4242\nfcwd\nn" + cwd + "\n")}, nil
		case s.Name == "/bin/kill":
			_ = l.Close() // Vite exits on SIGTERM
		}
		return exec.Result{}, nil
	}}

	// Another app on the port: never killed.
	cwd = "/Applications/Other.app"
	if err := Stop(context.Background(), r, dir); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("foreign process: %v", err)
	}
	for _, c := range r.Calls() {
		if c.Name == "/bin/kill" {
			t.Fatal("killed a process outside the theme")
		}
	}

	cwd = themeDir
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Stop(ctx, r, dir); err != nil {
		t.Fatal(err)
	}
	last := r.Calls()[len(r.Calls())-1]
	if last.Name != "/bin/kill" || !reflect.DeepEqual(last.Args, []string{"-TERM", "4242"}) {
		t.Errorf("last call = %s %v", last.Name, last.Args)
	}
	if Running(context.Background(), dir) != "" {
		t.Error("Stop waits until the port is closed")
	}
}
