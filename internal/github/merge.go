package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/site"
)

// Merged is a merged pull request.
type Merged struct {
	SHA    string // the commit it made on the base branch
	Method string // merge, squash or rebase
}

// Merge merges a pull request with the repository's default merge method
// for this user, but only if its branch is still at pr.HeadSHA: a push after
// taw-fleet read it means someone should look again.
func (c *Client) Merge(ctx context.Context, repo string, pr site.PullRequest) (Merged, error) {
	if c.token() == "" {
		return Merged{}, ErrNoToken
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return Merged{}, fmt.Errorf("not owner/name: %q", repo)
	}
	var q struct {
		Data struct {
			Repository *struct {
				Method string `json:"viewerDefaultMergeMethod"`
			} `json:"repository"`
		} `json:"data"`
	}
	err := c.do(ctx, http.MethodPost, "/graphql", map[string]any{
		"query":     "query($o: String!, $n: String!) { repository(owner: $o, name: $n) { viewerDefaultMergeMethod } }",
		"variables": map[string]any{"o": owner, "n": name},
	}, &q)
	if err != nil {
		return Merged{}, err
	}
	method := "merge"
	if r := q.Data.Repository; r != nil && r.Method != "" {
		method = strings.ToLower(r.Method)
	}
	var res struct {
		SHA string `json:"sha"`
	}
	err = c.do(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, pr.Number),
		map[string]any{"merge_method": method, "sha": pr.HeadSHA}, &res)
	var api *APIError
	if errors.As(err, &api) && api.Status == http.StatusConflict {
		return Merged{}, fmt.Errorf("#%d changed since taw-fleet read it (%s); refresh with L and look again", pr.Number, api.Message)
	}
	if err != nil {
		return Merged{}, fmt.Errorf("merge #%d: %w", pr.Number, err)
	}
	return Merged{SHA: res.SHA, Method: method}, nil
}

// DeleteBranch deletes a branch. One that's already gone (the repository
// deletes merged branches itself) is fine.
func (c *Client) DeleteBranch(ctx context.Context, repo, branch string) error {
	segs := strings.Split(branch, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	err := c.do(ctx, http.MethodDelete, "/repos/"+repo+"/git/refs/heads/"+strings.Join(segs, "/"), nil, nil)
	var api *APIError
	if errors.As(err, &api) && (api.Status == http.StatusUnprocessableEntity || api.Status == http.StatusNotFound) {
		return nil
	}
	return err
}
