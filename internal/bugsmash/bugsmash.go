// Package bugsmash reads the open review comments of each site's BugSmash
// project (https://bugsmash.io): how many, how old, and the latest ones. It
// only reads; resolving comments happens elsewhere (the umbrella's
// taw-resolve-comments skill).
package bugsmash

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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/keychain"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// BaseURL is BugSmash's REST API.
const BaseURL = "https://api.bugsmash.io/api/v2"

// CacheTTL is how long a result is reused.
const CacheTTL = 5 * time.Minute

// KeyEnv is the environment variable read when no key is in the Keychain.
const KeyEnv = "BUGSMASH_API_KEY"

// keep is how many open comments a result carries.
const keep = 5

// ErrNoKey means no API key is stored.
var ErrNoKey = errors.New("no BugSmash API key: store it with `taw-fleet comments key import`")

// --- key ----------------------------------------------------------------------

var keyItem = keychain.Item{Account: "bugsmash-api-key", Label: "taw-fleet BugSmash API key"}

// KeyStore keeps the API key in the login keychain.
type KeyStore struct{ Exec exec.Runner }

// Save stores the key, replacing an earlier one.
func (k KeyStore) Save(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("empty key")
	}
	return keychain.Keychain{Exec: k.Exec}.Save(ctx, keyItem, key)
}

// Load reads the key from the Keychain, else from $BUGSMASH_API_KEY. It says
// where it came from ("Keychain" or the variable's name); ErrNoKey when
// neither has one.
func (k KeyStore) Load(ctx context.Context, p paths.Paths) (key, from string, err error) {
	key, err = keychain.Keychain{Exec: k.Exec}.Load(ctx, keyItem)
	switch {
	case err == nil && key != "":
		return key, "Keychain", nil
	case err != nil && !errors.Is(err, keychain.ErrNotFound):
		return "", "", err
	}
	if p.Getenv != nil {
		if v := strings.TrimSpace(p.Getenv(KeyEnv)); v != "" {
			return v, KeyEnv, nil
		}
	}
	return "", "", ErrNoKey
}

// --- client -------------------------------------------------------------------

// Client calls the BugSmash API.
type Client struct {
	BaseURL   string       // "" = BaseURL
	HTTP      *http.Client // nil = a client with a 15 s timeout
	Key       string
	UserAgent string // some agents are refused by BugSmash's CDN; ours isn't
}

// APIError is a non-2xx answer.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "BugSmash refused the API key (" + e.Message + ")"
	case http.StatusNotFound:
		return "the BugSmash project isn't there any more (" + e.Message + ")"
	}
	return fmt.Sprintf("BugSmash answered %d: %s", e.Status, e.Message)
}

// page is a paginated answer's position.
type page struct {
	Current int `json:"current_page"`
	Last    int `json:"last_page"`
}

func (c *Client) get(ctx context.Context, path string, q url.Values, into any) error {
	_, err := c.getPage(ctx, path, q, into)
	return err
}

func (c *Client) getPage(ctx context.Context, path string, q url.Values, into any) (page, error) {
	base := c.BaseURL
	if base == "" {
		base = BaseURL
	}
	u := strings.TrimRight(base, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return page{}, err
	}
	req.Header.Set("X-API-Key", c.Key)
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return page{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return page{}, err
	}
	var env struct {
		page
		Status  bool            `json:"status"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if jerr := json.Unmarshal(body, &env); jerr != nil {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 120 {
			msg = msg[:120] + "…"
		}
		if resp.StatusCode/100 != 2 {
			return page{}, &APIError{Status: resp.StatusCode, Message: msg}
		}
		return page{}, fmt.Errorf("BugSmash: unreadable answer: %w", jerr)
	}
	if resp.StatusCode/100 != 2 || !env.Status {
		return page{}, &APIError{Status: resp.StatusCode, Message: env.Message}
	}
	return env.page, json.Unmarshal(env.Data, into)
}

// Project is what taw-fleet needs of a project.
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type,omitempty"`
	ShortURL    string    `json:"short_url,omitempty"`
	FrontendURL string    `json:"frontend_url,omitempty"` // the workspace's BugSmash site
	Versions    []Version `json:"project_versions,omitempty"`
}

// Version is one version of a project (a website review has one per round).
type Version struct {
	ShortURL string `json:"short_url"` // its review page, with the comments
	IsLatest bool   `json:"is_latest"`
}

// ReviewURL is the page where the project's comments are: the latest
// version's review page, else any version's, else the workspace's site.
func (p Project) ReviewURL() string {
	var first string
	for _, v := range p.Versions {
		if v.IsLatest && v.ShortURL != "" {
			return v.ShortURL
		}
		if first == "" {
			first = v.ShortURL
		}
	}
	for _, u := range []string{first, p.ShortURL, p.FrontendURL} {
		if u != "" {
			return u
		}
	}
	return ""
}

// Project reads one project; a 404 *APIError when it was deleted.
func (c *Client) Project(ctx context.Context, id string) (Project, error) {
	var p Project
	err := c.get(ctx, "/project/"+url.PathEscape(id), nil, &p)
	return p, err
}

// Projects lists every project the key can see, oldest page first.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var all []Project
	for n := 1; n <= 50; n++ { // 50 pages is far more than a fleet has
		var batch []Project
		pg, err := c.getPage(ctx, "/projects", url.Values{"page": {fmt.Sprint(n)}}, &batch)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) == 0 || pg.Last <= n {
			break
		}
	}
	return all, nil
}

type apiComment struct {
	ID        string `json:"comment_id"`
	Number    int    `json:"comment_number"`
	Comment   string `json:"comment"`
	Status    string `json:"status"`
	Author    string `json:"comment_by_name"`
	CreatedAt string `json:"created_at"`
	Location  *struct {
		PageURL string `json:"page_url"`
	} `json:"location_metadata"`
}

// OpenComments lists the project's unresolved comments, newest first.
func (c *Client) OpenComments(ctx context.Context, projectID string) ([]site.Comment, error) {
	var raw []apiComment
	q := url.Values{"projectId": {projectID}, "status": {"active"}, "plainText": {"true"}, "locationMetadata": {"true"}}
	if err := c.get(ctx, "/comments", q, &raw); err != nil {
		return nil, err
	}
	out := make([]site.Comment, 0, len(raw))
	for _, r := range raw {
		if !strings.EqualFold(r.Status, "active") {
			continue // the filter is honoured today; don't rely on it
		}
		created, _ := time.Parse(time.RFC3339Nano, r.CreatedAt)
		cm := site.Comment{ID: r.ID, Number: r.Number, Author: r.Author, Text: strings.Join(strings.Fields(r.Comment), " "), CreatedAt: created}
		if r.Location != nil {
			cm.Page = r.Location.PageURL
		}
		out = append(out, cm)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// --- prober -------------------------------------------------------------------

// Target is one site's project.
type Target struct {
	Slug    string // Local site folder
	Project string // BugSmash project id
}

// Prober checks the projects.
type Prober struct {
	Client   *Client
	CacheDir string // "" = no cache
	Now      func() time.Time
}

// Check reads one project. It never returns an error: what went wrong is in
// the result.
func (pr *Prober) Check(ctx context.Context, t Target) site.Feedback {
	out := site.Feedback{ProjectID: t.Project, CheckedAt: pr.now()}
	fail := func(err error) site.Feedback {
		out.Error, out.ErrorKind = err.Error(), kindOf(err)
		return out
	}
	p, err := pr.Client.Project(ctx, t.Project)
	if err != nil {
		return fail(err)
	}
	out.Project, out.URL = p.Name, p.ReviewURL()
	comments, err := pr.Client.OpenComments(ctx, t.Project)
	if err != nil {
		return fail(err)
	}
	out.Open = len(comments)
	for _, c := range comments {
		if !c.CreatedAt.IsZero() && (out.Oldest.IsZero() || c.CreatedAt.Before(out.Oldest)) {
			out.Oldest = c.CreatedAt
		}
	}
	out.Comments = comments[:min(len(comments), keep)]
	return out
}

// CheckAll checks every target in parallel, using the cache unless fresh is
// set. Results are keyed by slug.
func (pr *Prober) CheckAll(ctx context.Context, targets []Target, fresh bool) map[string]site.Feedback {
	out := make(map[string]site.Feedback, len(targets))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Go(func() {
			f, ok := pr.cached(t)
			if fresh || !ok {
				f = pr.Check(ctx, t)
				pr.save(t, f)
			}
			mu.Lock()
			out[t.Slug] = f
			mu.Unlock()
		})
	}
	wg.Wait()
	return out
}

// NoKey is every target's result when there is no API key: doctor and the
// dashboard say so per site instead of staying silent.
func NoKey(targets []Target, now time.Time) map[string]site.Feedback {
	out := make(map[string]site.Feedback, len(targets))
	for _, t := range targets {
		out[t.Slug] = site.Feedback{ProjectID: t.Project, CheckedAt: now, Error: ErrNoKey.Error(), ErrorKind: "no-key"}
	}
	return out
}

// Apply puts the results on the report's sites.
func Apply(sites []site.Site, results map[string]site.Feedback) {
	for i := range sites {
		if f, ok := results[sites[i].Slug]; ok {
			sites[i].Feedback = &f
		}
	}
}

func (pr *Prober) cacheFile(slug string) string {
	return filepath.Join(pr.CacheDir, "bugsmash", slug+".json")
}

func (pr *Prober) cached(t Target) (site.Feedback, bool) {
	if pr.CacheDir == "" {
		return site.Feedback{}, false
	}
	data, err := os.ReadFile(pr.cacheFile(t.Slug))
	if err != nil {
		return site.Feedback{}, false
	}
	var f site.Feedback
	if json.Unmarshal(data, &f) != nil || f.ProjectID != t.Project || pr.now().Sub(f.CheckedAt) > CacheTTL {
		return site.Feedback{}, false
	}
	return f, true
}

// save is best effort, and skips failures so the next look tries again.
func (pr *Prober) save(t Target, f site.Feedback) {
	if pr.CacheDir == "" || f.Error != "" {
		return
	}
	file := pr.cacheFile(t.Slug)
	if os.MkdirAll(filepath.Dir(file), 0o755) != nil {
		return
	}
	data, _ := json.Marshal(f)
	_ = os.WriteFile(file, data, 0o644)
}

func (pr *Prober) now() time.Time {
	if pr.Now == nil {
		return time.Now()
	}
	return pr.Now()
}

func kindOf(err error) string {
	var api *APIError
	switch {
	case errors.Is(err, ErrNoKey):
		return "no-key"
	case errors.As(err, &api) && (api.Status == http.StatusUnauthorized || api.Status == http.StatusForbidden):
		return "auth"
	case errors.As(err, &api) && api.Status == http.StatusNotFound:
		return "not-found"
	case errors.As(err, &api):
		return "bugsmash"
	}
	return "unreachable"
}
