package local

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// fakeLocal mimics Local's API: bearer auth, sites/site queries, and
// mutations that pass through a transitional state for a couple of polls.
type fakeLocal struct {
	*httptest.Server
	mu      sync.Mutex
	status  map[string]string
	pending map[string]int // polls left before the target state
	target  map[string]string
	ops     []string
	fail    string
	slow    time.Duration // how long a mutation's answer takes after the change begins
	hang    bool          // the mutation never answers (as Local's startSite was seen doing)
	steps   int           // polls a change takes; 0 = 2
	stall   time.Duration // how long every query takes to answer
}

func newFakeLocal(t *testing.T) *fakeLocal {
	f := &fakeLocal{
		status:  map[string]string{"s1": "halted", "s2": "running"},
		pending: map[string]int{}, target: map[string]string{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeLocal) setDelays(slow, stall time.Duration, hang bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slow, f.stall, f.hang = slow, stall, hang
}

func (f *fakeLocal) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Invalid Bearer token.","extensions":{"code":"UNAUTHENTICATED"}}]}`))
		return
	}
	f.mu.Lock()
	slow, stall, hang := f.slow, f.stall, f.hang
	f.mu.Unlock()
	time.Sleep(stall)
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	defer f.mu.Unlock()
	id, _ := req.Variables["id"].(string)
	reply := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
	siteObj := func(id string) map[string]any {
		if left := f.pending[id]; left > 0 {
			f.pending[id] = left - 1
		} else if tgt, ok := f.target[id]; ok {
			f.status[id] = tgt
			delete(f.target, id)
		}
		return map[string]any{"id": id, "name": "Site " + id, "status": f.status[id]}
	}
	switch {
	case strings.HasPrefix(req.Query, "mutation"):
		if f.fail != "" {
			_, _ = w.Write([]byte(`{"errors":[{"message":"` + f.fail + `"}]}`))
			return
		}
		// restartSite first: "restartSite(" also contains "startSite(".
		for _, o := range []struct{ op, mid, end string }{
			{"restartSite", "restarting", "running"}, {"startSite", "starting", "running"}, {"stopSite", "stopping", "halted"},
		} {
			op, mid := o.op, [2]string{o.mid, o.end}
			if strings.Contains(req.Query, op+"(") {
				f.ops = append(f.ops, op+":"+id)
				steps := f.steps
				if steps == 0 {
					steps = 2
				}
				f.status[id], f.target[id], f.pending[id] = mid[0], mid[1], steps
				answer := map[string]any{op: siteObj(id)}
				// The change is under way; the answer comes later, or never.
				f.mu.Unlock()
				if hang {
					<-r.Context().Done()
				} else {
					time.Sleep(slow)
				}
				f.mu.Lock()
				reply(answer)
				return
			}
		}
	case strings.Contains(req.Query, "sites"):
		var list []map[string]any
		for _, id := range []string{"s1", "s2"} {
			list = append(list, map[string]any{"id": id, "name": "Site " + id, "status": f.status[id]})
		}
		reply(map[string]any{"sites": list})
	case strings.Contains(req.Query, "site("):
		if _, ok := f.status[id]; !ok {
			reply(map[string]any{"site": nil})
			return
		}
		reply(map[string]any{"site": siteObj(id)})
	}
}

func connect(t *testing.T, f *fakeLocal, token string) *GraphQL {
	t.Helper()
	p := paths.ForHome(t.TempDir(), nil)
	if err := os.MkdirAll(p.LocalSupport, 0o755); err != nil {
		t.Fatal(err)
	}
	info := map[string]any{"port": 4000, "url": f.URL, "subscriptionUrl": "ws://x", "authToken": token}
	data, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(p.LocalSupport, "graphql-connection-info.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	g, err := NewGraphQL(p)
	if err != nil {
		t.Fatal(err)
	}
	g.Poll = time.Millisecond
	return g
}

func TestNewGraphQLErrors(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	if _, err := NewGraphQL(p); !errors.Is(err, ErrLocalNotRunning) {
		t.Errorf("no file: %v", err)
	}
	_ = os.MkdirAll(p.LocalSupport, 0o755)
	f := filepath.Join(p.LocalSupport, "graphql-connection-info.json")
	_ = os.WriteFile(f, []byte(`{"url":"http://evil.example.com/graphql","authToken":"t"}`), 0o600)
	if _, err := NewGraphQL(p); err == nil || !strings.Contains(err.Error(), "non-local") {
		t.Errorf("remote url: %v", err)
	}
}

func TestStatuses(t *testing.T) {
	f := newFakeLocal(t)
	g := connect(t, f, "tok")
	st, err := g.Statuses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st, map[string]site.Status{"s1": site.StatusHalted, "s2": site.StatusRunning}) {
		t.Errorf("statuses = %v", st)
	}
	if s, err := g.Status(context.Background(), "s2"); err != nil || s != site.StatusRunning {
		t.Errorf("status = %v %v", s, err)
	}
	if _, err := g.Status(context.Background(), "nope"); err == nil {
		t.Error("unknown site")
	}
}

func TestBadTokenAndLocalClosed(t *testing.T) {
	f := newFakeLocal(t)
	g := connect(t, f, "wrong")
	if _, err := g.Statuses(context.Background()); err == nil || !strings.Contains(err.Error(), "Invalid Bearer token") {
		t.Errorf("bad token: %v", err)
	}
	f.Close()
	if _, err := connect(t, f, "tok").Statuses(context.Background()); !errors.Is(err, ErrLocalNotRunning) {
		t.Errorf("closed: %v", err)
	}
}

func TestRunWaitsForTheNewState(t *testing.T) {
	f := newFakeLocal(t)
	g := connect(t, f, "tok")
	ctx := context.Background()
	if _, err := g.Run(ctx, Start, "s1"); err != nil {
		t.Fatal(err)
	}
	if f.status["s1"] != "running" {
		t.Errorf("s1 = %s", f.status["s1"])
	}
	if _, err := g.Run(ctx, Stop, "s2"); err != nil || f.status["s2"] != "halted" {
		t.Errorf("stop: %v %s", err, f.status["s2"])
	}
	if _, err := g.Run(ctx, Restart, "s1"); err != nil || f.status["s1"] != "running" {
		t.Errorf("restart: %v", err)
	}
	if !reflect.DeepEqual(f.ops, []string{"startSite:s1", "stopSite:s2", "stopSite:s1", "startSite:s1"}) {
		t.Errorf("ops = %v", f.ops)
	}
}

// Local can answer startSite long after the site is up, or not at all:
// the status says when it's done, not the answer.
func TestRunDoesntWaitForTheAnswer(t *testing.T) {
	f := newFakeLocal(t)
	g := connect(t, f, "tok")
	g.HTTP.Timeout = 50 * time.Millisecond
	f.setDelays(0, 0, true)
	if took, err := g.Run(context.Background(), Start, "s1"); err != nil || took > 2*time.Second {
		t.Fatalf("an unanswered start: %v (%s)", err, took)
	}
	f.setDelays(300*time.Millisecond, 0, false)
	if _, err := g.Run(context.Background(), Restart, "s1"); err != nil || f.status["s1"] != "running" {
		t.Errorf("restart: %v", err)
	}
	if !reflect.DeepEqual(f.ops, []string{"startSite:s1", "stopSite:s1", "startSite:s1"}) {
		t.Errorf("ops = %v", f.ops)
	}

	f.setDelays(0, 0, true)
	f.mu.Lock()
	f.steps = 1_000_000
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := g.Run(ctx, Stop, "s1"); err == nil || !strings.Contains(err.Error(), "still busy") {
		t.Errorf("past the operation's deadline: %v", err)
	}

	f.setDelays(0, 200*time.Millisecond, false)
	if _, err := g.Statuses(context.Background()); !errors.Is(err, ErrLocalSlow) {
		t.Errorf("a stalled query is slow, not closed: %v", err)
	}
}

func TestRunErrorsAndTimeout(t *testing.T) {
	f := newFakeLocal(t)
	g := connect(t, f, "tok")
	f.fail = "Port 10004 is in use"
	if _, err := g.Run(context.Background(), Start, "s1"); err == nil || !strings.Contains(err.Error(), "Port 10004 is in use") {
		t.Errorf("mutation error: %v", err)
	}
	f.fail = ""
	f.mu.Lock()
	f.status["s1"], f.target["s1"], f.pending["s1"] = "starting", "running", 1_000_000
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := g.Wait(ctx, "s1", site.StatusRunning); err == nil || !strings.Contains(err.Error(), "still busy") {
		t.Errorf("timeout: %v", err)
	}
	if _, err := g.Run(context.Background(), Op("deleteSite"), "s1"); err == nil {
		t.Error("only start/stop/restart")
	}
}

func TestWPSpec(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	p.LocalApp = filepath.Join(p.Home, "Local.app")
	s := site.Site{Slug: "acme", WebRoot: "/S/acme/app/public", Socket: "/L/run/a1/mysql/mysqld.sock", PHPVersion: "8.2.30"}
	if _, err := WPSpec(p, s, nil); !errors.Is(err, ErrSiteHalted) || !strings.Contains(err.Error(), "taw-fleet start acme") {
		t.Errorf("halted: %v", err)
	}
	s.SockLive = true
	if _, err := WPSpec(p, s, nil); err == nil {
		t.Error("no PHP anywhere")
	}
	p.LookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }
	spec, err := WPSpec(p, s, []string{"option", "get", "stylesheet"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-d", "mysqli.default_socket=/L/run/a1/mysql/mysqld.sock", "-d", "pdo_mysql.default_socket=/L/run/a1/mysql/mysqld.sock",
		"-d", "display_errors=stderr", "/usr/local/bin/wp", "--path=/S/acme/app/public", "option", "get", "stylesheet"}
	if spec.Name != "/usr/local/bin/php" || !reflect.DeepEqual(spec.Args, want) || spec.Dir != s.WebRoot ||
		!reflect.DeepEqual(spec.Env, []string{"MYSQL_UNIX_PORT=/L/run/a1/mysql/mysqld.sock"}) {
		t.Errorf("spec = %+v", spec)
	}
	// Local's own PHP and wp-cli win over PATH.
	for _, f := range []string{
		filepath.Join(p.LocalSupport, "lightning-services", "php-8.2.30+1", "bin", "darwin-arm64", "bin", "php"),
		filepath.Join(p.LocalSupport, "lightning-services", "php-8.2.30+1", "bin", "darwin", "bin", "php"),
		filepath.Join(p.LocalApp, "Contents", "Resources", "extraResources", "bin", "wp-cli", "wp-cli.phar"),
	} {
		_ = os.MkdirAll(filepath.Dir(f), 0o755)
		_ = os.WriteFile(f, nil, 0o755)
	}
	spec, _ = WPSpec(p, s, nil)
	if !strings.Contains(spec.Name, "php-8.2.30+1") || !strings.HasSuffix(spec.Args[6], "wp-cli.phar") {
		t.Errorf("local tools: %+v", spec)
	}
}
