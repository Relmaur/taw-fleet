package local

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Leftover is one of Local's nginx or php-fpm processes whose master is gone
// (its parent is launchd) but which still holds what a site needs: its PHP
// socket, its nginx port, or the router's ports 80 and 443. Local can't stop
// or replace it, so the site's start or stop hangs, or Local reports a port
// conflict; Local restarting doesn't help. Seen after heavy start/stop churn
// (Local 10.1.2).
type Leftover struct {
	PID   int
	Name  string // nginx or php-fpm
	Holds string // what it holds: "the PHP socket", "port 10003", "port 443 (Local's router)"
}

// Router reports whether it holds the router's port 80 or 443 (every
// site's), rather than something of the site's own.
func (l Leftover) Router() bool { return strings.HasSuffix(l.Holds, "(Local's router)") }

// Leftovers are a site's leftover processes, by PID.
type Leftovers []Leftover

// PIDs are the processes' ids.
func (l Leftovers) PIDs() []int {
	out := make([]int, len(l))
	for i, p := range l {
		out[i] = p.PID
	}
	return out
}

// String lists them: "nginx 123 (port 10003), php-fpm 456 (the PHP socket)".
func (l Leftovers) String() string {
	parts := make([]string, len(l))
	for i, p := range l {
		parts[i] = fmt.Sprintf("%s %d (%s)", p.Name, p.PID, p.Holds)
	}
	return strings.Join(parts, ", ")
}

// LeftoversError is a start, stop or restart that taw-fleet didn't try, or
// that ended, with leftover processes in the way. Done says the operation
// itself finished (a stop that left them behind).
type LeftoversError struct {
	Slug string
	Op   Op
	Done bool
	List Leftovers
}

func (e *LeftoversError) Error() string {
	n := len(e.List)
	what := "process holds"
	if n > 1 {
		what = "processes hold"
	}
	lead := fmt.Sprintf("%d leftover Local %s what %s needs", n, what, e.Slug)
	if e.Done {
		lead = fmt.Sprintf("%s is stopped, but %d leftover Local %s what it needs to start again", e.Slug, n, what)
	}
	return fmt.Sprintf("%s: %s; their master is gone, so Local can't stop them (taw-fleet unstick %s ends them)", lead, e.List, e.Slug)
}

// Process tools, by absolute path (no PATH lookups).
const (
	lsofBin = "/usr/sbin/lsof"
	psBin   = "/bin/ps"
	killBin = "/bin/kill"
)

// routerPorts are where Local's router nginx listens for every site.
var routerPorts = []int{80, 443}

// FindLeftovers looks for leftover processes in s's way. Only Local's own
// nginx and php-fpm (their program lives in Local's lightning-services) whose
// parent is launchd count; a running site's processes have Local as parent.
func FindLeftovers(ctx context.Context, r exec.Runner, p paths.Paths, s site.Site) (Leftovers, error) {
	holds := map[int]string{} // pid → what it holds (the first found)
	add := func(pids []int, what string) {
		for _, pid := range pids {
			if _, ok := holds[pid]; !ok {
				holds[pid] = what
			}
		}
	}
	if s.ID != "" {
		sock := filepath.Join(p.LocalSupport, "run", s.ID, "php", "php-fpm.socket")
		if _, err := os.Stat(sock); err == nil {
			pids, err := lsofPIDs(ctx, r, "--", sock)
			if err != nil {
				return nil, err
			}
			add(pids, "the PHP socket")
		}
	}
	ports := append([]int(nil), routerPorts...)
	if s.HTTPPort > 0 {
		ports = append([]int{s.HTTPPort}, ports...)
	}
	for _, port := range ports {
		pids, err := lsofPIDs(ctx, r, "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN")
		if err != nil {
			return nil, err
		}
		what := "port " + strconv.Itoa(port)
		if port == 80 || port == 443 {
			what += " (Local's router)"
		}
		add(pids, what)
	}
	if len(holds) == 0 {
		return nil, nil
	}

	pids := make([]string, 0, len(holds))
	for pid := range holds {
		pids = append(pids, strconv.Itoa(pid))
	}
	// ps exits 1 when a process is already gone; what it printed still counts.
	res, err := r.Run(ctx, exec.Spec{Name: psBin, Args: []string{"-o", "pid=,ppid=,comm=", "-p", strings.Join(pids, ",")}})
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var out Leftovers
	sc := bufio.NewScanner(bytes.NewReader(res.Stdout))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		name := strings.TrimSuffix(f[2], ":") // "nginx:" / "php-fpm:" for workers and masters
		if err1 != nil || err2 != nil || ppid != 1 || (name != "nginx" && name != "php-fpm") {
			continue
		}
		if !localProgram(ctx, r, p, pid) {
			continue // another nginx (Herd, Homebrew…), not Local's
		}
		out = append(out, Leftover{PID: pid, Name: name, Holds: holds[pid]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

// lsofPIDs are the processes lsof finds (none is exit status 1, not an error).
func lsofPIDs(ctx context.Context, r exec.Runner, args ...string) ([]int, error) {
	res, err := r.Run(ctx, exec.Spec{Name: lsofBin, Args: append([]string{"-nP", "-t"}, args...)})
	if err != nil {
		return nil, fmt.Errorf("lsof: %w", err)
	}
	var pids []int
	for _, l := range strings.Fields(string(res.Stdout)) {
		if pid, err := strconv.Atoi(l); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// localProgram reports whether the process runs a program from Local's
// lightning-services (workers show no path on their command line, so the
// open program file is what tells).
func localProgram(ctx context.Context, r exec.Runner, p paths.Paths, pid int) bool {
	res, err := r.Run(ctx, exec.Spec{Name: lsofBin, Args: []string{"-a", "-p", strconv.Itoa(pid), "-d", "txt", "-Fn"}})
	if err != nil {
		return false
	}
	services := filepath.Join(p.LocalSupport, "lightning-services") + string(filepath.Separator)
	for _, l := range strings.Split(string(res.Stdout), "\n") {
		if name, ok := strings.CutPrefix(l, "n"); ok {
			return strings.HasPrefix(name, services) // the first txt is the program
		}
	}
	return false
}

// endWait is how long EndLeftovers waits for the processes to go.
var endWait = 5 * time.Second

// EndLeftovers asks each process to end (TERM) and waits up to 5 seconds for
// them to go. It says which are still there.
func EndLeftovers(ctx context.Context, r exec.Runner, l Leftovers) (Leftovers, error) {
	if len(l) == 0 {
		return nil, nil
	}
	args := []string{"-TERM"}
	for _, pid := range l.PIDs() {
		args = append(args, strconv.Itoa(pid))
	}
	if _, err := r.Run(ctx, exec.Spec{Name: killBin, Args: args}); err != nil {
		return l, fmt.Errorf("kill: %w", err)
	}
	deadline := time.Now().Add(endWait)
	for {
		var left Leftovers
		for _, p := range l {
			res, err := r.Run(ctx, exec.Spec{Name: killBin, Args: []string{"-0", strconv.Itoa(p.PID)}})
			if err == nil && res.Code == 0 { // still there
				left = append(left, p)
			}
		}
		if len(left) == 0 || time.Now().After(deadline) {
			return left, nil
		}
		select {
		case <-ctx.Done():
			return left, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
