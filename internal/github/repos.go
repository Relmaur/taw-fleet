package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Relmaur/taw-fleet/internal/site"
)

// ErrNoToken means there's no GitHub token: pull requests and deploys need
// one (GitHub's GraphQL API doesn't answer anonymous requests).
var ErrNoToken = errors.New("no GitHub token: sign in with `gh auth login` (or set GITHUB_TOKEN)")

// pendingShown is how many undeployed commits a Deploy lists.
const pendingShown = 5

// Repos reads the open pull requests and the deploy workflow of each
// repository ("owner/name"): one GraphQL call for all of them, then the
// Actions runs of each. A repository that fails has Error set; the others
// still answer.
func (c *Client) Repos(ctx context.Context, repos []string) map[string]site.RepoState {
	out := make(map[string]site.RepoState, len(repos))
	now := c.now()
	fail := func(err error) map[string]site.RepoState {
		for _, r := range repos {
			out[r] = site.RepoState{Repo: r, CheckedAt: now, Error: err.Error()}
		}
		return out
	}
	if len(repos) == 0 {
		return out
	}
	if c.token() == "" {
		return fail(ErrNoToken)
	}
	states, err := c.pullRequests(ctx, repos)
	if err != nil {
		return fail(err)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range repos {
		st := states[r]
		st.Repo, st.CheckedAt = r, now
		if st.Error != "" || st.Head == "" {
			mu.Lock()
			out[r] = st
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := c.deploy(ctx, r, st.Head)
			if err != nil {
				st.Error = err.Error()
			}
			st.Deploy = d
			mu.Lock()
			out[r] = st
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

const prFields = `nameWithOwner
defaultBranchRef { name target { oid ... on Commit { statusCheckRollup { state } } } }
pullRequests(states: OPEN, first: 20, orderBy: {field: UPDATED_AT, direction: DESC}) {
  nodes {
    number title url isDraft mergeable updatedAt headRefName headRefOid
    author { login }
    headRepository { nameWithOwner }
    commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
  }
}`

type rollup struct {
	State string `json:"state"`
}

type gqlRepo struct {
	NameWithOwner    string `json:"nameWithOwner"`
	DefaultBranchRef *struct {
		Name   string `json:"name"`
		Target struct {
			OID               string  `json:"oid"`
			StatusCheckRollup *rollup `json:"statusCheckRollup"`
		} `json:"target"`
	} `json:"defaultBranchRef"`
	PullRequests struct {
		Nodes []struct {
			Number      int       `json:"number"`
			Title       string    `json:"title"`
			URL         string    `json:"url"`
			IsDraft     bool      `json:"isDraft"`
			Mergeable   string    `json:"mergeable"`
			UpdatedAt   time.Time `json:"updatedAt"`
			HeadRefName string    `json:"headRefName"`
			HeadRefOID  string    `json:"headRefOid"`
			Author      *struct {
				Login string `json:"login"`
			} `json:"author"`
			HeadRepository *struct {
				NameWithOwner string `json:"nameWithOwner"`
			} `json:"headRepository"`
			Commits struct {
				Nodes []struct {
					Commit struct {
						StatusCheckRollup *rollup `json:"statusCheckRollup"`
					} `json:"commit"`
				} `json:"nodes"`
			} `json:"commits"`
		} `json:"nodes"`
	} `json:"pullRequests"`
}

// pullRequests asks GraphQL for every repository at once, one alias each.
func (c *Client) pullRequests(ctx context.Context, repos []string) (map[string]site.RepoState, error) {
	var q strings.Builder
	vars := map[string]any{}
	var decl []string
	for i, r := range repos {
		owner, name, ok := strings.Cut(r, "/")
		if !ok {
			return nil, fmt.Errorf("not owner/name: %q", r)
		}
		decl = append(decl, fmt.Sprintf("$o%d: String!, $n%d: String!", i, i))
		vars[fmt.Sprintf("o%d", i)], vars[fmt.Sprintf("n%d", i)] = owner, name
		fmt.Fprintf(&q, "r%d: repository(owner: $o%d, name: $n%d) { %s }\n", i, i, i, prFields)
	}
	query := "query(" + strings.Join(decl, ", ") + ") {\n" + q.String() + "}"
	var resp struct {
		Data   map[string]*gqlRepo `json:"data"`
		Errors []struct {
			Path    []any  `json:"path"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.do(ctx, http.MethodPost, "/graphql", map[string]any{"query": query, "variables": vars}, &resp); err != nil {
		return nil, err
	}
	failed := map[string]string{}
	for _, e := range resp.Errors {
		if len(e.Path) > 0 {
			if alias, ok := e.Path[0].(string); ok {
				failed[alias] = e.Message
				continue
			}
		}
		if resp.Data == nil {
			return nil, fmt.Errorf("github: %s", e.Message)
		}
	}
	out := map[string]site.RepoState{}
	for i, r := range repos {
		alias := fmt.Sprintf("r%d", i)
		g := resp.Data[alias]
		if g == nil {
			msg := failed[alias]
			if msg == "" || strings.HasPrefix(msg, "Could not resolve") {
				msg = "not found, or your GitHub token can't see it (another account?)"
			}
			out[r] = site.RepoState{Error: msg}
			continue
		}
		st := site.RepoState{PRs: []site.PullRequest{}}
		if b := g.DefaultBranchRef; b != nil {
			st.Default, st.Head, st.HeadCI = b.Name, b.Target.OID, checks(b.Target.StatusCheckRollup)
		}
		for _, n := range g.PullRequests.Nodes {
			pr := site.PullRequest{Number: n.Number, Title: n.Title, URL: n.URL, Branch: n.HeadRefName, HeadSHA: n.HeadRefOID,
				Draft: n.IsDraft, Conflicted: n.Mergeable == "CONFLICTING", Updated: n.UpdatedAt, Checks: site.ChecksNone}
			if n.Author != nil {
				pr.Author = n.Author.Login
			}
			pr.SameRepo = n.HeadRepository != nil && strings.EqualFold(n.HeadRepository.NameWithOwner, g.NameWithOwner)
			if len(n.Commits.Nodes) > 0 {
				pr.Checks = checks(n.Commits.Nodes[0].Commit.StatusCheckRollup)
			}
			st.PRs = append(st.PRs, pr)
		}
		out[r] = st
	}
	return out, nil
}

// checks turns GitHub's rollup state into ours.
func checks(r *rollup) string {
	if r == nil {
		return site.ChecksNone
	}
	switch r.State {
	case "SUCCESS":
		return site.ChecksPassing
	case "FAILURE", "ERROR":
		return site.ChecksFailing
	}
	return site.ChecksPending // PENDING, EXPECTED
}

type workflowRun struct {
	HeadSHA    string    `json:"head_sha"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	Started    time.Time `json:"run_started_at"`
	URL        string    `json:"html_url"`
}

// deploy finds the repository's deploy workflow (a workflow file named
// deploy*) and reads its newest runs. nil, nil = no deploy workflow.
func (c *Client) deploy(ctx context.Context, repo, head string) (*site.Deploy, error) {
	var wf struct {
		Workflows []struct {
			ID    int64  `json:"id"`
			Name  string `json:"name"`
			Path  string `json:"path"`
			State string `json:"state"`
			URL   string `json:"html_url"`
		} `json:"workflows"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/actions/workflows?per_page=100", nil, &wf); err != nil {
		return nil, err
	}
	d := (*site.Deploy)(nil)
	var id int64
	for _, w := range wf.Workflows {
		if w.State == "active" && strings.HasPrefix(path.Base(w.Path), "deploy") {
			d, id = &site.Deploy{Workflow: w.Name, URL: w.URL}, w.ID
			break
		}
	}
	if d == nil {
		return nil, nil
	}
	var runs struct {
		Runs []workflowRun `json:"workflow_runs"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/actions/workflows/%d/runs?per_page=20", repo, id), nil, &runs); err != nil {
		return d, err
	}
	for _, r := range runs.Runs { // newest first
		run := &site.Run{SHA: r.HeadSHA, Status: r.Status, Result: r.Conclusion, Started: r.Started, URL: r.URL}
		if r.Status != "completed" {
			if d.Running == nil {
				d.Running = run
			}
			continue
		}
		if r.Conclusion == "success" {
			d.Deployed, d.DeployedAt = r.HeadSHA, r.Started
			break
		}
		// skipped: CI didn't pass, so the job didn't run; cancelled: someone
		// stopped it. Neither is a failed deploy.
		if d.Failed == nil && r.Conclusion != "skipped" && r.Conclusion != "cancelled" {
			d.Failed = run
		}
	}
	if d.Deployed == "" || d.Deployed == head {
		return d, nil
	}
	var cmp struct {
		AheadBy int `json:"ahead_by"`
		Commits []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
			} `json:"commit"`
		} `json:"commits"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/compare/"+d.Deployed+"..."+head, nil, &cmp); err != nil {
		return d, err
	}
	d.Behind = cmp.AheadBy
	for i := len(cmp.Commits) - 1; i >= 0 && len(d.Pending) < pendingShown; i-- { // newest first
		title, _, _ := strings.Cut(cmp.Commits[i].Commit.Message, "\n")
		d.Pending = append(d.Pending, site.Commit{SHA: cmp.Commits[i].SHA, Title: title})
	}
	return d, nil
}

// do sends one API request with the token and decodes the JSON answer.
func (c *Client) do(ctx context.Context, method, p string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+p, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "taw-fleet")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := c.token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Status: resp.StatusCode, Message: apiMessage(data, resp.Status)}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// APIError is a GitHub API refusal.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("github: %d %s", e.Status, e.Message) }

func apiMessage(body []byte, fallback string) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return e.Message
	}
	return fallback
}

func (c *Client) token() string {
	if c.Token == nil {
		return ""
	}
	return c.Token()
}
