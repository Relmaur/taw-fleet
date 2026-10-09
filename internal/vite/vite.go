// Package vite finds a theme's Vite dev server and says how to run and stop
// it. Both TAW scaffolds' vite.config write a hot file holding the server's
// URL while it runs and remove it when it stops; that file plus a port check
// is how taw-fleet knows a theme is "being worked on".
package vite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
)

// hotFiles are where the scaffolds write the hot file: taw-theme, then
// taw-gutenberg.
var hotFiles = []string{"public/build/hot", "dist/hot"}

// dialTimeout bounds the port check. The server is on this Mac.
const dialTimeout = 300 * time.Millisecond

// Running is the dev server's URL when the theme's hot file names one that
// answers, "" otherwise: not running, or a hot file left behind by a Vite
// that was killed before it could clean up.
func Running(ctx context.Context, dir string) string {
	u, ok := hotURL(dir)
	if !ok {
		return ""
	}
	d := net.Dialer{Timeout: dialTimeout}
	c, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return ""
	}
	_ = c.Close()
	return u.String()
}

// hotURL reads the first hot file that names a local http URL with a port.
func hotURL(dir string) (*url.URL, bool) {
	for _, f := range hotFiles {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(string(b)))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Port() == "" {
			continue
		}
		return u, true
	}
	return nil, false
}

// ErrNoDevScript means the theme's package.json has no "dev" script.
var ErrNoDevScript = errors.New(`no "dev" script in package.json`)

// Dev is how to run a theme's dev server.
type Dev struct {
	Manager string // npm, pnpm or yarn, from the lockfile
	Install bool   // node_modules is missing: install first
}

// Detect reads the theme's package.json and lockfile.
func Detect(dir string) (Dev, error) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Dev{}, errors.New("no package.json")
		}
		return Dev{}, err
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return Dev{}, fmt.Errorf("package.json: %w", err)
	}
	if pkg.Scripts["dev"] == "" {
		return Dev{}, ErrNoDevScript
	}
	d := Dev{Manager: "npm"}
	switch {
	case exists(filepath.Join(dir, "pnpm-lock.yaml")):
		d.Manager = "pnpm"
	case exists(filepath.Join(dir, "yarn.lock")):
		d.Manager = "yarn"
	}
	d.Install = !exists(filepath.Join(dir, "node_modules"))
	return d, nil
}

// Commands are the shell lines that run the dev server: install when
// needed, then the "dev" script.
func (d Dev) Commands() []string {
	var out []string
	if d.Install {
		out = append(out, d.Manager+" install")
	}
	return append(out, d.Manager+" run dev")
}

// Stop ends the theme's dev server: the process listening on the hot file's
// port, only when its working directory is the theme (so another app on
// that port is never touched). It waits until the port is closed.
func Stop(ctx context.Context, r exec.Runner, dir string) error {
	u, ok := hotURL(dir)
	if !ok {
		return errors.New("no hot file: Vite isn't running")
	}
	themeDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	pids, err := listeners(ctx, r, u.Port())
	if err != nil {
		return err
	}
	var mine []int
	for _, pid := range pids {
		if cwd, err := processCwd(ctx, r, pid); err == nil && cwd == themeDir {
			mine = append(mine, pid)
		}
	}
	if len(mine) == 0 {
		if Running(ctx, dir) == "" {
			return nil // already gone
		}
		return fmt.Errorf("port %s is in use by a process outside %s; stop Vite in its window", u.Port(), filepath.Base(dir))
	}
	for _, pid := range mine {
		res, err := r.Run(ctx, exec.Spec{Name: "/bin/kill", Args: []string{"-TERM", strconv.Itoa(pid)}})
		if err != nil {
			return err
		}
		if res.Code != 0 {
			return fmt.Errorf("kill %d: %s", pid, strings.TrimSpace(string(res.Stderr)))
		}
	}
	for Running(ctx, dir) != "" {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for Vite to stop: %w", ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil
}

// listeners are the processes listening on a TCP port.
func listeners(ctx context.Context, r exec.Runner, port string) ([]int, error) {
	res, err := r.Run(ctx, exec.Spec{Name: "/usr/sbin/lsof", Args: []string{"-nP", "-iTCP:" + port, "-sTCP:LISTEN", "-t"}})
	if err != nil {
		return nil, err
	}
	// lsof exits 1 when nothing matches.
	var pids []int
	for _, f := range strings.Fields(string(res.Stdout)) {
		if pid, err := strconv.Atoi(f); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// processCwd is a process's working directory.
func processCwd(ctx context.Context, r exec.Runner, pid int) (string, error) {
	res, err := r.Run(ctx, exec.Spec{Name: "/usr/sbin/lsof", Args: []string{"-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn"}})
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		if strings.HasPrefix(line, "n") {
			return line[1:], nil
		}
	}
	return "", fmt.Errorf("no working directory for %d", pid)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
