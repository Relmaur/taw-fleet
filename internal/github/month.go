package github

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// MergedPR is a pull request merged in a month.
type MergedPR struct {
	Number int       `json:"number"`
	Title  string    `json:"title"`
	URL    string    `json:"url"`
	Branch string    `json:"branch"`
	At     time.Time `json:"merged_at"`
}

// DeployRun is a successful run of the deploy workflow.
type DeployRun struct {
	SHA string    `json:"sha"`
	At  time.Time `json:"at"`
	URL string    `json:"url"`
}

// Month is what happened in a repository between two times: the pull
// requests merged and the deploys that succeeded (oldest first).
type Month struct {
	Repo      string      `json:"repo"`
	Merged    []MergedPR  `json:"merged"`
	Deploys   []DeployRun `json:"deploys"`
	HasDeploy bool        `json:"has_deploy_workflow"`
}

// Month reads a repository's merged pull requests and successful deploys
// in [from, to).
func (c *Client) Month(ctx context.Context, repo string, from, to time.Time) (Month, error) {
	m := Month{Repo: repo, Merged: []MergedPR{}, Deploys: []DeployRun{}}
	if c.token() == "" {
		return m, ErrNoToken
	}
	span := from.UTC().Format(time.RFC3339) + ".." + to.UTC().Add(-time.Second).Format(time.RFC3339)
	query := `query($q: String!) { search(query: $q, type: ISSUE, first: 100) { nodes { ... on PullRequest { number title url mergedAt headRefName } } } }`
	var resp struct {
		Data struct {
			Search struct {
				Nodes []struct {
					Number      int       `json:"number"`
					Title       string    `json:"title"`
					URL         string    `json:"url"`
					MergedAt    time.Time `json:"mergedAt"`
					HeadRefName string    `json:"headRefName"`
				} `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	vars := map[string]any{"q": fmt.Sprintf("repo:%s is:pr is:merged merged:%s", repo, span)}
	if err := c.do(ctx, http.MethodPost, "/graphql", map[string]any{"query": query, "variables": vars}, &resp); err != nil {
		return m, err
	}
	if len(resp.Errors) > 0 {
		return m, fmt.Errorf("github: %s", resp.Errors[0].Message)
	}
	for _, n := range resp.Data.Search.Nodes {
		if n.Number == 0 || n.MergedAt.Before(from) || !n.MergedAt.Before(to) {
			continue
		}
		m.Merged = append(m.Merged, MergedPR{Number: n.Number, Title: n.Title, URL: n.URL, Branch: n.HeadRefName, At: n.MergedAt})
	}
	sort.Slice(m.Merged, func(i, j int) bool { return m.Merged[i].At.Before(m.Merged[j].At) })

	id, d, err := c.deployWorkflow(ctx, repo)
	if err != nil || d == nil {
		return m, err
	}
	m.HasDeploy = true
	var runs struct {
		Runs []workflowRun `json:"workflow_runs"`
	}
	created := from.UTC().Format("2006-01-02") + ".." + to.UTC().Add(-time.Second).Format("2006-01-02")
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/actions/workflows/%d/runs?status=success&per_page=100&created=%s", repo, id, created), nil, &runs); err != nil {
		return m, err
	}
	for _, r := range runs.Runs {
		if r.Conclusion != "success" || r.Started.Before(from) || !r.Started.Before(to) {
			continue
		}
		m.Deploys = append(m.Deploys, DeployRun{SHA: r.HeadSHA, At: r.Started, URL: r.URL})
	}
	sort.Slice(m.Deploys, func(i, j int) bool { return m.Deploys[i].At.Before(m.Deploys[j].At) })
	return m, nil
}
