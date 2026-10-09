// Package wpremote calls a production site's WordPress REST API as the site's
// bot user (an Editor with an Application Password), so site skills can write
// to production through taw-fleet without the password ever reaching a Claude
// transcript or a file: it lives in the Keychain, one item per site.
//
// Every request uses the ?rest_route= form (some hosts' firewalls refuse
// /wp-json/wp/v2/users… outright), HTTP/1.1, and retries the empty answers
// some hosts give. It never deletes: DELETE isn't offered.
package wpremote

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/keychain"
)

// ErrNoCreds means no bot credentials are stored for the site.
var ErrNoCreds = errors.New("no WordPress bot credentials for this site")

// Creds are the bot's username and Application Password.
type Creds struct{ User, Password string }

// ParseCreds reads "user:application password" (the password's spaces kept).
func ParseCreds(s string) (Creds, error) {
	user, pass, ok := strings.Cut(strings.TrimSpace(s), ":")
	user, pass = strings.TrimSpace(user), strings.TrimSpace(pass)
	if !ok || user == "" || pass == "" {
		return Creds{}, errors.New(`expected "user:application password"`)
	}
	return Creds{User: user, Password: pass}, nil
}

// Store keeps each site's credentials in the Keychain.
type Store struct{ Exec exec.Runner }

func item(slug string) keychain.Item {
	return keychain.Item{Account: "wp-" + slug, Label: "taw-fleet WordPress bot for " + slug}
}

// Save stores the site's credentials, replacing earlier ones.
func (s Store) Save(ctx context.Context, slug string, c Creds) error {
	return keychain.Keychain{Exec: s.Exec}.Save(ctx, item(slug), c.User+":"+c.Password)
}

// Load reads the site's credentials; ErrNoCreds when none are stored.
func (s Store) Load(ctx context.Context, slug string) (Creds, error) {
	v, err := keychain.Keychain{Exec: s.Exec}.Load(ctx, item(slug))
	switch {
	case errors.Is(err, keychain.ErrNotFound) || (err == nil && v == ""):
		return Creds{}, ErrNoCreds
	case err != nil:
		return Creds{}, err
	}
	return ParseCreds(v)
}

// Methods are the ones offered (no DELETE: taw-fleet never deletes on
// production).
var Methods = []string{"GET", "POST", "PUT", "PATCH"}

// Request is one REST call.
type Request struct {
	Method string
	Route  string // "/wp/v2/pages?slug=nosotros&context=edit"
	JSON   []byte // a JSON body, or nil
	File   string // a file to upload as the body (media), or ""
	Type   string // the file's content type; "" = from its extension
}

// APIError is a non-2xx answer.
type APIError struct {
	Status  int
	Code    string // WordPress's error code, e.g. rest_forbidden
	Message string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("WordPress answered %d", e.Status)
	if e.Code != "" {
		msg += " " + e.Code
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// Client talks to one site.
type Client struct {
	Site      string // the production URL, e.g. https://lsmexico.mx
	Creds     Creds
	HTTP      *http.Client // nil = HTTP/1.1, 60 s timeout
	UserAgent string
	Retries   int           // extra tries for an empty or unreadable answer; 0 = 2
	Pause     time.Duration // between tries; 0 = 1 s
}

// URL is the request URL for a route, in the ?rest_route= form.
func URL(site, route string) (string, error) {
	base, err := url.Parse(strings.TrimRight(site, "/") + "/")
	if err != nil || base.Host == "" {
		return "", fmt.Errorf("bad production URL %q", site)
	}
	path, query, _ := strings.Cut(route, "?")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		return "", fmt.Errorf("bad query in %q: %w", route, err)
	}
	q.Set("rest_route", path)
	base.RawQuery = q.Encode()
	return base.String(), nil
}

// Do sends the request and returns the answer's body (JSON).
func (c *Client) Do(ctx context.Context, r Request) ([]byte, error) {
	method := strings.ToUpper(r.Method)
	if !allowed(method) {
		return nil, fmt.Errorf("method %s isn't offered (taw-fleet never deletes on production): use %s", r.Method, strings.Join(Methods, ", "))
	}
	u, err := URL(c.Site, r.Route)
	if err != nil {
		return nil, err
	}
	var body []byte
	ctype, disposition := "", ""
	switch {
	case r.File != "":
		if body, err = os.ReadFile(r.File); err != nil {
			return nil, err
		}
		ctype = r.Type
		if ctype == "" {
			ctype = mime.TypeByExtension(filepath.Ext(r.File))
		}
		if ctype == "" {
			return nil, fmt.Errorf("can't tell %s's type: pass --type", r.File)
		}
		disposition = fmt.Sprintf(`attachment; filename="%s"`, strings.ReplaceAll(filepath.Base(r.File), `"`, ""))
	case r.JSON != nil:
		if !json.Valid(r.JSON) {
			return nil, errors.New("the data isn't valid JSON")
		}
		body, ctype = r.JSON, "application/json"
	}

	tries := c.Retries
	if tries <= 0 {
		tries = 2
	}
	pause := c.Pause
	if pause <= 0 {
		pause = time.Second
	}
	var last error
	for i := 0; i <= tries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(pause):
			}
		}
		out, retry, err := c.once(ctx, method, u, body, ctype, disposition)
		if err == nil || !retry {
			return out, err
		}
		last = err
	}
	return nil, last
}

func (c *Client) once(ctx context.Context, method, u string, body []byte, ctype, disposition string) (out []byte, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.SetBasicAuth(c.Creds.User, c.Creds.Password)
	req.Header.Set("Accept", "application/json")
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if disposition != "" {
		req.Header.Set("Content-Disposition", disposition)
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, ctx.Err() == nil, fmt.Errorf("couldn't reach %s: %w", c.Site, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err = io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, true, err
	}
	trimmed := bytes.TrimSpace(out)
	if resp.StatusCode/100 != 2 {
		var e struct{ Code, Message string }
		if json.Unmarshal(trimmed, &e) == nil && (e.Code != "" || e.Message != "") {
			return nil, false, &APIError{Status: resp.StatusCode, Code: e.Code, Message: e.Message}
		}
		msg := string(trimmed)
		if len(msg) > 160 {
			msg = msg[:160] + "…"
		}
		return nil, resp.StatusCode >= 500 || len(trimmed) == 0, &APIError{Status: resp.StatusCode, Message: msg}
	}
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return nil, true, fmt.Errorf("the site answered %d with something that isn't JSON (%d bytes)", resp.StatusCode, len(trimmed))
	}
	return trimmed, false, nil
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{
		Proxy:        http.ProxyFromEnvironment,
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}, // HTTP/1.1: some hosts truncate HTTP/2
	}}
}

func allowed(m string) bool {
	for _, a := range Methods {
		if m == a {
			return true
		}
	}
	return false
}

// Me is who the credentials log in as.
type Me struct {
	Name  string   `json:"name"`
	Slug  string   `json:"slug"`
	Roles []string `json:"roles"`
}

// WhoAmI reads the bot's own user (context=edit shows its roles).
func (c *Client) WhoAmI(ctx context.Context) (Me, error) {
	b, err := c.Do(ctx, Request{Method: "GET", Route: "/wp/v2/users/me?context=edit"})
	if err != nil {
		return Me{}, err
	}
	var me Me
	return me, json.Unmarshal(b, &me)
}
