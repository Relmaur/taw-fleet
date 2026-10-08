package local

import (
	"context"
	"encoding/json"
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
)

// fakeJobs answers addSite with a job that runs for two polls, then ends
// with result (and errJSON when it fails).
func fakeJobs(t *testing.T, result, errJSON string) (*GraphQL, *map[string]any) {
	t.Helper()
	var mu sync.Mutex
	polls := 0
	input := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(req.Query, "addSite("):
			for k, v := range req.Variables["input"].(map[string]any) {
				input[k] = v
			}
			_, _ = w.Write([]byte(`{"data":{"addSite":{"id":"job1","status":"running"}}}`))
		case strings.Contains(req.Query, "job("):
			polls++
			status, e := "running", "null"
			if polls > 2 {
				status, e = result, errJSON
			}
			_, _ = w.Write([]byte(`{"data":{"job":{"id":"job1","status":"` + status + `","logs":"","error":` + e + `}}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return &GraphQL{URL: srv.URL, Token: "tok", HTTP: srv.Client()}, &input
}

func TestAddSiteAndWait(t *testing.T) {
	g, input := fakeJobs(t, "successful", "null")
	ctx := context.Background()
	job, err := g.AddSite(ctx, NewSite{Name: "Acme", Path: "/L/acme", Domain: "acme.local", AdminUser: "me", AdminPassword: "pw", AdminEmail: "me@x.test"})
	if err != nil || job.ID != "job1" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	want := map[string]any{"name": "Acme", "path": "/L/acme", "domain": "acme.local", "environment": "preferred",
		"wpAdminUsername": "me", "wpAdminPassword": "pw", "wpAdminEmail": "me@x.test", "goToSite": false}
	if !reflect.DeepEqual(*input, want) {
		t.Errorf("input = %v", *input)
	}
	var seen []string
	done, err := g.WaitJob(ctx, job.ID, time.Millisecond, func(j Job) { seen = append(seen, j.Status) })
	if err != nil || done.Status != "successful" || !reflect.DeepEqual(seen, []string{"running", "successful"}) {
		t.Errorf("done=%+v err=%v seen=%v", done, err, seen)
	}
}

func TestAddSiteCustomServices(t *testing.T) {
	g, input := fakeJobs(t, "successful", "null")
	if _, err := g.AddSite(context.Background(), NewSite{Name: "A", PHP: "8.5.3", WebServer: "nginx-1.26.1"}); err != nil {
		t.Fatal(err)
	}
	in := *input
	if in["environment"] != "custom" || in["phpVersion"] != "8.5.3" || in["webServer"] != "nginx-1.26.1" {
		t.Errorf("input = %v", in)
	}
	if _, ok := in["database"]; ok {
		t.Error("database left to Local when empty")
	}
}

func TestWaitJobFailure(t *testing.T) {
	g, _ := fakeJobs(t, "failed", `{"message":"Port 10010 is in use"}`)
	_, err := g.WaitJob(context.Background(), "job1", time.Millisecond, nil)
	if err == nil || !strings.Contains(err.Error(), "Port 10010 is in use") {
		t.Errorf("err = %v", err)
	}
	if got := (Job{Status: "failed", Error: json.RawMessage(`"disk full"`)}).Err(); got == nil || !strings.Contains(got.Error(), "disk full") {
		t.Errorf("string error: %v", got)
	}
	if got := (Job{Status: "failed", Error: json.RawMessage(`null`)}).Err(); got == nil || !strings.Contains(got.Error(), "no reason given") {
		t.Errorf("null error: %v", got)
	}
}

func TestServices(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	p.LocalApp = filepath.Join(t.TempDir(), "Local.app")
	for _, d := range []string{
		filepath.Join(p.LocalSupport, "lightning-services", "php-8.2.29+0"),
		filepath.Join(p.LocalSupport, "lightning-services", "php-8.5.3+1"),
		filepath.Join(p.LocalSupport, "lightning-services", "mysql-8.4.0"),
		filepath.Join(p.LocalSupport, "lightning-services", "mysql-8.4.0+2"),
		filepath.Join(p.LocalSupport, "lightning-services", "nginx-1.26.1+3"),
		filepath.Join(p.LocalSupport, "lightning-services", "notes"),
		filepath.Join(p.LocalApp, "Contents", "Resources", "extraResources", "lightning-services", "php-8.2.30+1"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := Services(p)
	want := map[string][]string{"php": {"8.5.3", "8.2.30", "8.2.29"}, "mysql": {"8.4.0"}, "nginx": {"1.26.1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}
