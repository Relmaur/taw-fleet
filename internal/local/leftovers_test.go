package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// fakeProcs is a machine where Local's parallelstaff site (id run1, port
// 10003) had heavy churn: an orphaned php-fpm worker holds its socket, an
// orphaned router worker holds 443; the site's own running nginx (parent
// Local) holds 10003 and Herd's nginx (parent launchd, not Local's) holds 80.
// alive lists the processes kill -0 still finds.
func fakeProcs(p paths.Paths, alive map[string]bool) *exec.FakeRunner {
	services := filepath.Join(p.LocalSupport, "lightning-services")
	return &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		args := strings.Join(s.Args, " ")
		switch s.Name {
		case lsofBin:
			switch {
			case strings.HasSuffix(args, "php-fpm.socket"):
				return exec.Result{Stdout: []byte("501\n")}, nil
			case strings.Contains(args, "-iTCP:10003"):
				return exec.Result{Stdout: []byte("600\n601\n")}, nil
			case strings.Contains(args, "-iTCP:80 "):
				return exec.Result{Stdout: []byte("700\n")}, nil
			case strings.Contains(args, "-iTCP:443"):
				return exec.Result{Stdout: []byte("502\n700\n")}, nil
			case strings.Contains(args, "-p 501 "):
				return exec.Result{Stdout: []byte("p501\nftxt\nn" + services + "/php-8.2.30+1/bin/darwin-arm64/sbin/php-fpm\n")}, nil
			case strings.Contains(args, "-p 502 "):
				return exec.Result{Stdout: []byte("p502\nftxt\nn" + services + "/nginx-1.26.1+3/bin/darwin-arm64/sbin/nginx\n")}, nil
			case strings.Contains(args, "-p 700 "):
				return exec.Result{Stdout: []byte("p700\nftxt\nn/Applications/Herd.app/Contents/Resources/bin/nginx\n")}, nil
			}
			return exec.Result{Code: 1}, nil // lsof found nothing
		case psBin:
			return exec.Result{Code: 1, Stdout: []byte( // 999 is gone: ps exits 1 but still answers for the rest
				"  501     1 php-fpm: pool www\n" +
					"  502     1 nginx: worker process\n" +
					"  600   777 nginx: master process /x/nginx -c conf\n" +
					"  601   600 nginx: worker process\n" +
					"  700     1 nginx: master process /Applications/Herd.app/Contents/Resources/bin/nginx\n")}, nil
		case killBin:
			if s.Args[0] == "-0" && !alive[s.Args[1]] {
				return exec.Result{Code: 1}, nil
			}
			return exec.Result{}, nil
		}
		return exec.Result{Code: 1}, nil
	}}
}

func leftoverSite(t *testing.T) (paths.Paths, site.Site) {
	t.Helper()
	p := paths.ForHome(t.TempDir(), nil)
	sock := filepath.Join(p.LocalSupport, "run", "run1", "php", "php-fpm.socket")
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, site.Site{ID: "run1", Slug: "parallelstaff", HTTPPort: 10003}
}

func TestFindLeftovers(t *testing.T) {
	p, s := leftoverSite(t)
	l, err := FindLeftovers(context.Background(), fakeProcs(p, nil), p, s)
	if err != nil {
		t.Fatal(err)
	}
	want := "php-fpm 501 (the PHP socket), nginx 502 (port 443 (Local's router))"
	if l.String() != want {
		t.Fatalf("leftovers = %q, want %q", l, want)
	}
	if l[0].Router() || !l[1].Router() {
		t.Errorf("router: %+v", l)
	}

	// A site with no socket file and nothing in the way.
	none := &exec.FakeRunner{Script: func(exec.Spec) (exec.Result, error) { return exec.Result{Code: 1}, nil }}
	if l, err := FindLeftovers(context.Background(), none, p, site.Site{ID: "other", HTTPPort: 10010}); err != nil || l != nil {
		t.Errorf("nothing: %v, %v", l, err)
	}
}

func TestEndLeftovers(t *testing.T) {
	p, s := leftoverSite(t)
	r := fakeProcs(p, nil)
	l, _ := FindLeftovers(context.Background(), r, p, s)
	left, err := EndLeftovers(context.Background(), r, l)
	if err != nil || len(left) != 0 {
		t.Fatalf("left = %v, %v", left, err)
	}
	var term string
	for _, c := range r.Calls() {
		if c.Name == killBin && c.Args[0] == "-TERM" {
			term = strings.Join(c.Args, " ")
		}
	}
	if term != "-TERM 501 502" {
		t.Errorf("kill = %q (only the leftovers, TERM)", term)
	}

	stubborn := fakeProcs(p, map[string]bool{"502": true})
	endWait = 0 // don't wait the 5 s
	t.Cleanup(func() { endWait = 5 * time.Second })
	if left, _ := EndLeftovers(context.Background(), stubborn, l); len(left) != 1 || left[0].PID != 502 {
		t.Errorf("still there = %v", left)
	}
}

func TestLeftoversError(t *testing.T) {
	l := Leftovers{{PID: 501, Name: "php-fpm", Holds: "the PHP socket"}}
	e := &LeftoversError{Slug: "parallelstaff", Op: Start, List: l}
	if !strings.Contains(e.Error(), "1 leftover Local process holds what parallelstaff needs: php-fpm 501 (the PHP socket)") ||
		!strings.Contains(e.Error(), "taw-fleet unstick parallelstaff") {
		t.Errorf("%s", e)
	}
	e.Done = true
	if !strings.HasPrefix(e.Error(), "parallelstaff is stopped, but 1 leftover") {
		t.Errorf("%s", e)
	}
}
