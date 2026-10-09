package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/companion"
)

// contentTimeout bounds a content snapshot: the site builds it on request.
const contentTimeout = 3 * time.Minute

// Content fetches the site's published content (companion 0.4+): a
// taw/core Content Interchange snapshot. Only a verified answer is
// returned, since it goes into a local database; the snapshot must say it
// comes from this site.
func (pr *Prober) Content(ctx context.Context, t Target) ([]byte, error) {
	pin, ok := pr.Pins[t.Slug]
	if !ok {
		return nil, fmt.Errorf("no pinned key for %s: run `taw-fleet live trust %s` first", t.Slug, t.Slug)
	}
	c := *pr.Client
	c.HTTP = &http.Client{Timeout: contentTimeout}
	resp, err := c.Get(ctx, companion.Site{URL: t.URL, Pinned: &pin}, "content", nil)
	var api *companion.APIError
	switch {
	case errors.As(err, &api) && api.Status == http.StatusNotFound && strings.Contains(api.Body, "rest_no_route"):
		return nil, fmt.Errorf("%s's companion has no content route: it needs taw/hub-companion 0.4+ (bump it in the theme and deploy)", t.URL)
	case errors.As(err, &api) && api.Code == "content_unavailable":
		return nil, fmt.Errorf("%s doesn't load taw/core's Content Interchange", t.URL)
	case err != nil:
		return nil, err
	case !resp.Verified:
		return nil, errors.New("the answer isn't verified")
	}
	var snap struct {
		Meta struct {
			Schema string `json:"schema"`
			Source struct {
				URL string `json:"url"`
			} `json:"source"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(resp.Body, &snap); err != nil {
		return nil, fmt.Errorf("the snapshot isn't JSON: %w", err)
	}
	if snap.Meta.Schema == "" {
		return nil, errors.New("not a content snapshot (no meta.schema)")
	}
	if host(snap.Meta.Source.URL) != host(t.URL) {
		return nil, fmt.Errorf("the snapshot says it comes from %s, not %s", snap.Meta.Source.URL, t.URL)
	}
	return resp.Body, nil
}

func host(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(p.Hostname()), "www.")
}
