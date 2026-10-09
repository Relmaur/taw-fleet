package companion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client makes signed GET requests to companions.
type Client struct {
	Key   Key
	HTTP  *http.Client
	Now   func() time.Time
	Nonce func() string
}

// NewClient returns a client with a 15 s timeout.
func NewClient(k Key) *Client {
	return &Client{Key: k, HTTP: &http.Client{Timeout: 15 * time.Second}, Now: time.Now, Nonce: NewNonce}
}

// Site is one live site to talk to.
type Site struct {
	URL    string   // https://example.com (no trailing slash needed)
	Pinned *SiteKey // nil = not pinned yet: the response is read but not trusted
}

// Response is a verified (or, without a pinned key, unverified) answer.
type Response struct {
	Status   int
	Body     []byte
	KeyID    string // the key the site signed with
	Verified bool
}

// APIError is a non-2xx answer from the companion.
type APIError struct {
	Status int
	Code   string // e.g. taw_hub_unauthorized
	Reason string // e.g. timestamp_out_of_window
	Body   string
}

func (e *APIError) Error() string {
	switch {
	case e.Status == http.StatusNotFound:
		return "no companion on this site (404)"
	case e.Status == http.StatusNotImplemented:
		return "the companion has no public key configured (501: TAW_HUB_PUBLIC_KEY)"
	case e.Status == http.StatusUnauthorized && e.Reason == "timestamp_out_of_window":
		return "the site refused the request: this Mac's clock is more than 60 s off (401)"
	case e.Status == http.StatusUnauthorized && e.Reason == "unknown_key_id":
		return "the site doesn't trust taw-fleet's signing key yet (401 unknown_key_id): it needs the public key from `taw-fleet live key show`"
	case e.Status == http.StatusUnauthorized && e.Reason == "invalid_signature":
		return "the site doesn't trust taw-fleet's signing key (401 invalid_signature): it has another public key configured"
	case e.Reason != "":
		return fmt.Sprintf("the companion answered %d: %s", e.Status, e.Reason)
	case e.Code != "":
		return fmt.Sprintf("the companion answered %d: %s", e.Status, e.Code)
	}
	return fmt.Sprintf("the companion answered %d", e.Status)
}

// Get calls a read route ("health", "logs") with an optional query. A pinned
// key is required for Verified; a bad signature is an error either way.
func (c *Client) Get(ctx context.Context, s Site, route string, query url.Values) (Response, error) {
	path := Namespace + "/" + strings.TrimPrefix(route, "/")
	u := strings.TrimRight(s.URL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode() // the query isn't signed (ADR-0003)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "taw-fleet")
	c.Key.Sign(req.Header, http.MethodGet, path, c.now().Unix(), c.nonce(), nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return Response{}, err
	}
	out := Response{Status: resp.StatusCode, Body: body, KeyID: resp.Header.Get(HeaderKeyID)}
	if s.Pinned != nil {
		if err := VerifyResponse(resp.Header, path, body, *s.Pinned, c.now()); err != nil {
			return out, err
		}
		out.Verified = true
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return out, apiError(resp.StatusCode, body)
	}
	return out, nil
}

func apiError(status int, body []byte) *APIError {
	e := &APIError{Status: status, Body: strings.TrimSpace(string(body))}
	var wp struct {
		Code  string `json:"code"`
		Error string `json:"error"`
		Data  struct {
			Reason string `json:"reason"`
		} `json:"data"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(body, &wp) == nil {
		e.Code = wp.Code
		if e.Code == "" {
			e.Code = wp.Error
		}
		e.Reason = wp.Data.Reason
		if e.Reason == "" {
			e.Reason = wp.Reason
		}
	}
	return e
}

func (c *Client) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

func (c *Client) nonce() string {
	if c.Nonce == nil {
		return NewNonce()
	}
	return c.Nonce()
}

// Health is GET /health.
type Health struct {
	OK               bool   `json:"ok"`
	PHPVersion       string `json:"php_version"`
	WPVersion        string `json:"wp_version"`
	TawCoreVersion   string `json:"taw_core_version"`
	CompanionVersion string `json:"companion_version"`
	SitePublicKey    string `json:"site_public_key"`
	SiteKeyID        string `json:"site_key_id"`
	ExecAvailable    bool   `json:"exec_available"`
}

// Plugin is one entry of GET /inventory.
type Plugin struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Active        bool   `json:"active"`
	UpdateVersion string `json:"update_version"`
}

// Inventory is GET /inventory (the parts taw-fleet shows).
type Inventory struct {
	WPVersion string   `json:"wp_version"`
	Plugins   []Plugin `json:"plugins"`
	MUPlugins []struct {
		File    string `json:"file"`
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"mu_plugins"`
}

// Finding is one known vulnerability.
type Finding struct {
	Slug             string   `json:"slug"`
	ComponentType    string   `json:"component_type"`
	InstalledVersion string   `json:"installed_version"`
	Severity         string   `json:"severity"`
	CVSS             *float64 `json:"cvss_score"`
	Title            string   `json:"title"`
	Link             string   `json:"link"`
}

// Vulnerabilities is GET /vulnerabilities.
type Vulnerabilities struct {
	Scanner *struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		LastScanAt string `json:"last_scan_at"`
	} `json:"scanner"`
	Count    int       `json:"count"`
	Findings []Finding `json:"findings"`
}

// LogEntry is one line of GET /logs.
type LogEntry struct {
	TS      string `json:"ts"`
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Logs is GET /logs.
type Logs struct {
	Count   int        `json:"count"`
	Entries []LogEntry `json:"entries"`
}

// Decode reads a response body into v.
func Decode[T any](r Response) (T, error) {
	var v T
	if err := json.Unmarshal(r.Body, &v); err != nil {
		return v, fmt.Errorf("unreadable answer: %w", err)
	}
	return v, nil
}

// IsAuth reports whether err is the site refusing the signature.
func IsAuth(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusUnauthorized
}
