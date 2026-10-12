package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMonth(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	var search string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/graphql":
			var body struct {
				Variables map[string]string `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			search = body.Variables["q"]
			_, _ = w.Write([]byte(`{"data":{"search":{"nodes":[
				{"number":15,"title":"Hero","url":"u15","mergedAt":"2026-10-20T10:00:00Z","headRefName":"feat/hero"},
				{"number":14,"title":"Update taw/core 1.93.1 → 1.93.3","url":"u14","mergedAt":"2026-10-12T10:00:00Z","headRefName":"taw/update-1"},
				{"number":9,"title":"Late","url":"u9","mergedAt":"2026-11-01T00:00:00Z","headRefName":"x"},
				{}]}}}`))
		case strings.HasSuffix(r.URL.Path, "/actions/workflows"):
			_, _ = w.Write([]byte(`{"workflows":[{"id":7,"name":"Deploy","path":".github/workflows/deploy.yml","state":"active"}]}`))
		case strings.HasSuffix(r.URL.Path, "/actions/workflows/7/runs"):
			if r.URL.Query().Get("status") != "success" || r.URL.Query().Get("created") != "2026-10-01..2026-10-31" {
				t.Errorf("runs query %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"workflow_runs":[
				{"head_sha":"b","status":"completed","conclusion":"success","run_started_at":"2026-10-20T10:05:00Z","html_url":"r2"},
				{"head_sha":"a","status":"completed","conclusion":"success","run_started_at":"2026-10-12T10:05:00Z","html_url":"r1"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Token: func() string { return "tok" }}
	m, err := c.Month(context.Background(), "Relmaur/ls-mexico--theme", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if search != "repo:Relmaur/ls-mexico--theme is:pr is:merged merged:2026-10-01T00:00:00Z..2026-10-31T23:59:59Z" {
		t.Errorf("search %q", search)
	}
	if len(m.Merged) != 2 || m.Merged[0].Number != 14 || m.Merged[1].Number != 15 || m.Merged[0].Branch != "taw/update-1" {
		t.Errorf("merged %+v", m.Merged)
	}
	if !m.HasDeploy || len(m.Deploys) != 2 || m.Deploys[0].SHA != "a" {
		t.Errorf("deploys %+v", m.Deploys)
	}
	if _, err := (&Client{BaseURL: srv.URL, Token: func() string { return "" }}).Month(context.Background(), "x/y", from, to); !errors.Is(err, ErrNoToken) {
		t.Errorf("no token: %v", err)
	}
}
