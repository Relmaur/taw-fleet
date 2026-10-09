package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// ErrLocalNotRunning means Local's app isn't open (its API doesn't answer).
var ErrLocalNotRunning = errors.New("local by Flywheel isn't open: open the Local app, then try again")

// ErrLocalSlow means Local's API is there but didn't answer in time.
var ErrLocalSlow = errors.New("local by Flywheel didn't answer in time: check the Local app")

// GraphQL talks to the API the Local app serves on localhost. It's how
// Local's own window starts and stops sites. Probed on Local 10.1.2:
// `Authorization: Bearer <authToken>` from graphql-connection-info.json;
// introspection is disabled, so the operations below come from Local's
// bundled schema (startSite/stopSite/restartSite(id: ID!): Site).
type GraphQL struct {
	URL   string
	Token string
	HTTP  *http.Client
	Poll  time.Duration // how often Wait asks for the status
}

// NewGraphQL reads the connection file Local writes while it runs.
func NewGraphQL(p paths.Paths) (*GraphQL, error) {
	data, err := os.ReadFile(filepath.Join(p.LocalSupport, "graphql-connection-info.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrLocalNotRunning
	}
	if err != nil {
		return nil, err
	}
	var info struct {
		URL       string `json:"url"`
		AuthToken string `json:"authToken"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("graphql-connection-info.json: %w", err)
	}
	if info.URL == "" || info.AuthToken == "" {
		return nil, errors.New("graphql-connection-info.json has no url or token")
	}
	if !strings.HasPrefix(info.URL, "http://127.0.0.1:") && !strings.HasPrefix(info.URL, "http://localhost:") {
		// The token must never leave this machine.
		return nil, fmt.Errorf("refusing a non-local Local API address: %s", info.URL)
	}
	return &GraphQL{URL: info.URL, Token: info.AuthToken, HTTP: &http.Client{Timeout: 10 * time.Second}, Poll: 500 * time.Millisecond}, nil
}

type gqlSite struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (g *GraphQL) do(ctx context.Context, query string, vars map[string]any, out any) error {
	return g.doWith(ctx, g.HTTP, query, vars, out)
}

func (g *GraphQL) doWith(ctx context.Context, c *http.Client, query string, vars map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.Token)
	resp, err := c.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			return ErrLocalNotRunning
		}
		var nerr net.Error
		if errors.As(err, &nerr) && nerr.Timeout() {
			return ErrLocalSlow
		}
		return fmt.Errorf("local API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("local API: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	if len(env.Errors) > 0 {
		var msgs []string
		for _, e := range env.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("local API: %s", strings.Join(msgs, "; "))
	}
	return json.Unmarshal(env.Data, out)
}

// Statuses returns every site's state, live.
func (g *GraphQL) Statuses(ctx context.Context) (map[string]site.Status, error) {
	var out struct {
		Sites []gqlSite `json:"sites"`
	}
	if err := g.do(ctx, `query { sites { id name status } }`, nil, &out); err != nil {
		return nil, err
	}
	m := make(map[string]site.Status, len(out.Sites))
	for _, s := range out.Sites {
		m[s.ID] = site.ParseStatus(s.Status)
	}
	return m, nil
}

// Status returns one site's state, live.
func (g *GraphQL) Status(ctx context.Context, id string) (site.Status, error) {
	var out struct {
		Site *gqlSite `json:"site"`
	}
	if err := g.do(ctx, `query ($id: ID!) { site(id: $id) { id name status } }`, map[string]any{"id": id}, &out); err != nil {
		return site.StatusUnknown, err
	}
	if out.Site == nil {
		return site.StatusUnknown, fmt.Errorf("local doesn't know a site %q", id)
	}
	return site.ParseStatus(out.Site.Status), nil
}

// Op is a site operation.
type Op string

// Operations Local supports.
const (
	Start   Op = "startSite"
	Stop    Op = "stopSite"
	Restart Op = "restartSite"
)

// Target is the state an operation ends in.
func (o Op) Target() site.Status {
	if o == Stop {
		return site.StatusHalted
	}
	return site.StatusRunning
}

// Run asks Local to start, stop or restart a site and waits until it's done
// (or ctx ends). It returns how long it took.
//
// Local may not answer the mutation until long after the site is up (a
// startSite was seen never answering), so the answer isn't waited for: the
// site's status is polled meanwhile, and the operation is done when it
// reaches the target. An error in the answer (a port in use…) still ends it.
func (g *GraphQL) Run(ctx context.Context, op Op, id string) (time.Duration, error) {
	began := time.Now()
	switch op {
	case Start, Stop:
	case Restart:
		// Local's restartSite was seen restarting the site but staying
		// "restarting" for good, so a restart is a stop, then a start.
		if _, err := g.Run(ctx, Stop, id); err != nil {
			return time.Since(began), err
		}
		_, err := g.Run(ctx, Start, id)
		return time.Since(began), err
	default:
		return 0, fmt.Errorf("unknown operation %q", op)
	}
	mctx, cancel := context.WithCancel(ctx)
	defer cancel() // drops the mutation if it's still unanswered
	answered := make(chan error, 1)
	go func() {
		long := http.Client{Transport: g.HTTP.Transport} // ctx bounds it, not the per-request timeout
		var out map[string]*gqlSite
		q := fmt.Sprintf(`mutation ($id: ID!) { %s(id: $id) { id name status } }`, op)
		answered <- g.doWith(mctx, &long, q, map[string]any{"id": id}, &out)
	}()

	poll := g.Poll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	want := op.Target()
	last := site.StatusBusy
	for {
		select {
		case err := <-answered:
			if err != nil && ctx.Err() == nil {
				return time.Since(began), err
			}
			answered = nil // answered: only the status matters now
		case <-ctx.Done():
			return time.Since(began), fmt.Errorf("still %s after %s: %w", last, time.Since(began).Round(time.Second), ctx.Err())
		case <-time.After(poll):
			st, err := g.Status(ctx, id)
			if err != nil {
				if ctx.Err() != nil {
					continue // reported by the ctx case
				}
				return time.Since(began), err
			}
			last = st
			if st == want {
				return time.Since(began), nil
			}
		}
	}
}

// Wait polls until the site reaches want.
func (g *GraphQL) Wait(ctx context.Context, id string, want site.Status) error {
	poll := g.Poll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	last := site.StatusBusy
	for {
		st, err := g.Status(ctx, id)
		if err != nil {
			if ctx.Err() != nil { // the deadline hit mid-request
				return fmt.Errorf("still %s after waiting: %w", last, ctx.Err())
			}
			return err
		}
		last = st
		if st == want {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("still %s after waiting: %w", st, ctx.Err())
		case <-time.After(poll):
		}
	}
}
