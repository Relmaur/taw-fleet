package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/site"
)

const graphqlAnswer = `{"data":{
 "r0":{"nameWithOwner":"Relmaur/ls-mexico--theme",
  "defaultBranchRef":{"name":"main","target":{"oid":"ccc","statusCheckRollup":{"state":"PENDING"}}},
  "pullRequests":{"nodes":[
   {"number":12,"title":"Update taw/core","url":"https://github.com/Relmaur/ls-mexico--theme/pull/12","isDraft":false,"mergeable":"MERGEABLE",
    "updatedAt":"2026-10-08T10:00:00Z","headRefName":"chore/taw-core-1.78.1","headRefOid":"p12","author":{"login":"Relmaur"},
    "headRepository":{"nameWithOwner":"Relmaur/ls-mexico--theme"},
    "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}},
   {"number":13,"title":"From a fork","url":"u","isDraft":true,"mergeable":"CONFLICTING","updatedAt":"2026-10-07T10:00:00Z",
    "headRefName":"patch","headRefOid":"p13","author":null,"headRepository":{"nameWithOwner":"someone/fork"},
    "commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}}]}},
 "r1":null},
 "errors":[{"path":["r1"],"message":"Could not resolve to a Repository with the name 'other/private'."}]}`

func TestReposReadsPRsAndDeploys(t *testing.T) {
	var gotQuery struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/graphql":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotQuery)
			_, _ = w.Write([]byte(graphqlAnswer))
		case "/repos/Relmaur/ls-mexico--theme/actions/workflows":
			_, _ = w.Write([]byte(`{"workflows":[
			  {"id":1,"name":"CI","path":".github/workflows/ci.yml","state":"active","html_url":"ci"},
			  {"id":2,"name":"Deploy","path":".github/workflows/deploy.yml","state":"active","html_url":"https://github.com/x/actions/workflows/deploy.yml"}]}`))
		case "/repos/Relmaur/ls-mexico--theme/actions/workflows/2/runs":
			// Newest first: one running, one skipped (CI failed), one failed, then the last success.
			_, _ = w.Write([]byte(`{"workflow_runs":[
			  {"head_sha":"ccc","status":"in_progress","conclusion":null,"run_started_at":"2026-10-08T12:00:00Z","html_url":"r4"},
			  {"head_sha":"bb2","status":"completed","conclusion":"skipped","run_started_at":"2026-10-08T11:30:00Z","html_url":"r3"},
			  {"head_sha":"bb1","status":"completed","conclusion":"failure","run_started_at":"2026-10-08T11:00:00Z","html_url":"r2"},
			  {"head_sha":"aaa","status":"completed","conclusion":"success","run_started_at":"2026-10-08T09:00:00Z","html_url":"r1"},
			  {"head_sha":"old","status":"completed","conclusion":"failure","run_started_at":"2026-10-07T09:00:00Z","html_url":"r0"}]}`))
		case "/repos/Relmaur/ls-mexico--theme/compare/aaa...ccc":
			_, _ = w.Write([]byte(`{"ahead_by":3,"commits":[
			  {"sha":"bb1","commit":{"message":"First\n\nbody"}},{"sha":"bb2","commit":{"message":"Second"}},{"sha":"ccc","commit":{"message":"Third"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	now := time.Date(2026, 10, 8, 12, 1, 0, 0, time.UTC)
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Token: func() string { return "tok" }, Now: func() time.Time { return now }}

	got := c.Repos(context.Background(), []string{"Relmaur/ls-mexico--theme", "other/private"})
	if gotQuery.Variables["o0"] != "Relmaur" || gotQuery.Variables["n1"] != "private" || !strings.Contains(gotQuery.Query, "r1: repository(owner: $o1, name: $n1)") {
		t.Errorf("query: %+v", gotQuery)
	}
	st := got["Relmaur/ls-mexico--theme"]
	if st.Error != "" || st.Default != "main" || st.Head != "ccc" || st.HeadCI != site.ChecksPending || !st.CheckedAt.Equal(now) {
		t.Fatalf("state = %+v", st)
	}
	if len(st.PRs) != 2 {
		t.Fatalf("PRs = %+v", st.PRs)
	}
	p, f := st.PRs[0], st.PRs[1]
	if p.Number != 12 || p.Checks != site.ChecksPassing || !p.SameRepo || p.Author != "Relmaur" || !p.Ready() {
		t.Errorf("PR 12 = %+v", p)
	}
	if f.Checks != site.ChecksNone || f.SameRepo || !f.Draft || !f.Conflicted || f.Ready() {
		t.Errorf("PR 13 = %+v", f)
	}
	d := st.Deploy
	if d == nil || d.Workflow != "Deploy" || d.Deployed != "aaa" || d.Running == nil || d.Running.SHA != "ccc" ||
		d.Failed == nil || d.Failed.SHA != "bb1" || d.Behind != 3 {
		t.Fatalf("deploy = %+v", d)
	}
	if len(d.Pending) != 3 || d.Pending[0].SHA != "ccc" || d.Pending[2].Title != "First" {
		t.Errorf("pending (newest first, first line only) = %+v", d.Pending)
	}
	if e := got["other/private"].Error; !strings.Contains(e, "can't see it") {
		t.Errorf("unreadable repo: %q", e)
	}
}

func TestReposWithoutToken(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", HTTP: http.DefaultClient}
	got := c.Repos(context.Background(), []string{"a/b"})
	if got["a/b"].Error != ErrNoToken.Error() {
		t.Errorf("no token: %+v", got)
	}
}

func TestRepoNamesAndApply(t *testing.T) {
	repo := func(owner, name string) *site.GitInfo {
		return &site.GitInfo{Repo: &site.Repo{Host: "github.com", Owner: owner, Name: name}}
	}
	sites := []site.Site{
		{Slug: "a", Themes: []site.Theme{{Dir: "a", IsTAW: true, Git: repo("Relmaur", "b--theme")}, {Dir: "x", Git: repo("x", "y")}}},
		{Slug: "taw", Themes: []site.Theme{{Dir: "t", IsTAW: true, Git: repo("Relmaur", "a--theme")}, {Dir: "t2", IsTAW: true, Git: repo("Relmaur", "b--theme")}}},
	}
	if got := strings.Join(RepoNames(sites), ","); got != "Relmaur/a--theme,Relmaur/b--theme" {
		t.Errorf("RepoNames = %s (TAW themes only, once each, sorted)", got)
	}
	ApplyRepos(sites, map[string]site.RepoState{"Relmaur/b--theme": {Repo: "Relmaur/b--theme", Head: "h"}})
	if sites[0].Themes[0].GitHub == nil || sites[1].Themes[1].GitHub == nil || sites[1].Themes[0].GitHub != nil || sites[0].Themes[1].GitHub != nil {
		t.Error("ApplyRepos puts the state on every theme of that repository only")
	}
}
