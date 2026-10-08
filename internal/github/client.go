// Package github finds the newest release tag of the TAW repositories, with a
// disk cache so taw-fleet stays fast and inside GitHub's anonymous rate limit
// (60 requests an hour).
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// DefaultTTL is how long a cached answer is used without asking GitHub.
const DefaultTTL = time.Hour

// ErrOffline means the answer wasn't cached and the client may not use the
// network.
var ErrOffline = errors.New("--offline and no cached answer yet")

// Client looks up tags. The zero value isn't usable; use New.
type Client struct {
	BaseURL  string       // https://api.github.com
	HTTP     *http.Client // with a timeout
	CacheDir string       // ""= no cache
	TTL      time.Duration
	Offline  bool // use the cache only, even when stale
	Token    func() string
	Now      func() time.Time
}

// New returns a client for api.github.com caching under cacheDir.
func New(cacheDir string, token func() string) *Client {
	return &Client{
		BaseURL:  "https://api.github.com",
		HTTP:     &http.Client{Timeout: 5 * time.Second},
		CacheDir: cacheDir,
		TTL:      DefaultTTL,
		Token:    token,
		Now:      time.Now,
	}
}

// Latest is a lookup result.
type Latest struct {
	Tag       string    `json:"tag"`
	FetchedAt time.Time `json:"fetched_at"`
	Stale     bool      `json:"-"` // from an expired cache because GitHub couldn't be reached
}

// LatestTag returns the highest stable semver tag of owner/repo. A fresh cache
// entry answers without the network. If GitHub fails, an expired cache entry
// is returned with Stale set rather than an error.
func (c *Client) LatestTag(ctx context.Context, owner, repo string) (Latest, error) {
	cached, haveCache := c.readCache(owner, repo)
	if haveCache && (c.Offline || c.now().Sub(cached.FetchedAt) < c.ttl()) {
		cached.Stale = c.Offline && c.now().Sub(cached.FetchedAt) >= c.ttl()
		return cached, nil
	}
	if c.Offline {
		return Latest{}, ErrOffline
	}

	tag, err := c.fetch(ctx, owner, repo)
	if err != nil {
		if haveCache {
			cached.Stale = true
			return cached, nil
		}
		return Latest{}, err
	}
	l := Latest{Tag: tag, FetchedAt: c.now()}
	c.writeCache(owner, repo, l)
	return l, nil
}

func (c *Client) fetch(ctx context.Context, owner, repo string) (string, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/tags?per_page=100", strings.TrimRight(c.BaseURL, "/"),
		url.PathEscape(owner), url.PathEscape(repo))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "taw-fleet")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != nil {
		if tok := c.Token(); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("github %s/%s: %w", owner, repo, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &e) == nil && e.Message != "" {
			msg = e.Message
		}
		return "", fmt.Errorf("github %s/%s: %s: %s", owner, repo, resp.Status, msg)
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &tags); err != nil {
		return "", fmt.Errorf("github %s/%s: %w", owner, repo, err)
	}
	best := ""
	for _, t := range tags {
		v := t.Name
		if !strings.HasPrefix(v, "v") {
			v = "v" + v
		}
		if !semver.IsValid(v) || semver.Prerelease(v) != "" {
			continue
		}
		if best == "" || semver.Compare(v, best) > 0 {
			best = v
		}
	}
	if best == "" {
		return "", fmt.Errorf("github %s/%s: no release tags", owner, repo)
	}
	return best, nil
}

func (c *Client) cacheFile(owner, repo string) string {
	return filepath.Join(c.CacheDir, "github", strings.ToLower(owner+"-"+repo)+".json")
}

func (c *Client) readCache(owner, repo string) (Latest, bool) {
	if c.CacheDir == "" {
		return Latest{}, false
	}
	data, err := os.ReadFile(c.cacheFile(owner, repo))
	if err != nil {
		return Latest{}, false
	}
	var l Latest
	if json.Unmarshal(data, &l) != nil || l.Tag == "" {
		return Latest{}, false
	}
	return l, true
}

// writeCache is best effort: a read-only cache only costs speed.
func (c *Client) writeCache(owner, repo string, l Latest) {
	if c.CacheDir == "" {
		return
	}
	f := c.cacheFile(owner, repo)
	if os.MkdirAll(filepath.Dir(f), 0o755) != nil {
		return
	}
	data, _ := json.Marshal(l)
	tmp := f + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, f)
	}
}

func (c *Client) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

func (c *Client) ttl() time.Duration {
	if c.TTL <= 0 {
		return DefaultTTL
	}
	return c.TTL
}
