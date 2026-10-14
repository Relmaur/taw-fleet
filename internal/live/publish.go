package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/companion"
)

// Publishing content to a production site (companion 0.5+, taw/core 1.98+;
// companion ADR-0006). Every call needs the site's pinned key and a verified
// answer, like Content: what production answers decides what's written there.

const (
	planTimeout  = 3 * time.Minute
	fileTimeout  = 2 * time.Minute
	applyTimeout = 10 * time.Minute
)

// PublishPlan sends the change-set (made with the base) and returns the
// field-level plan, the files production lacks and a one-time token.
func (pr *Prober) PublishPlan(ctx context.Context, t Target, changeSet []byte) (companion.PublishPlan, error) {
	var plan companion.PublishPlan
	resp, err := pr.publishCall(ctx, t, planTimeout, func(c *companion.Client, s companion.Site) (companion.Response, error) {
		return c.Post(ctx, s, "content/publish/plan", changeSet)
	})
	if err != nil {
		return plan, err
	}
	if err := json.Unmarshal(resp.Body, &plan); err != nil {
		return plan, fmt.Errorf("the plan isn't readable: %w", err)
	}
	if plan.ID == "" || plan.Token == "" {
		return plan, errors.New("production answered no publish id or token")
	}
	// Which taw/core made it: what it does with media depends on it.
	if h, err := pr.publishCall(ctx, t, fileTimeout, func(c *companion.Client, s companion.Site) (companion.Response, error) {
		return c.Get(ctx, s, "health", nil)
	}); err == nil {
		if health, err := companion.Decode[companion.Health](h); err == nil {
			plan.TawCore = health.TawCoreVersion
		}
	}
	return plan, nil
}

// PublishFile uploads one file's bytes for a planned publish.
func (pr *Prober) PublishFile(ctx context.Context, t Target, id string, data []byte) error {
	_, err := pr.publishCall(ctx, t, fileTimeout, func(c *companion.Client, s companion.Site) (companion.Response, error) {
		return c.Put(ctx, s, "content/publish/"+id+"/media", data, "application/octet-stream")
	})
	return err
}

// PublishApply publishes the approved records with the decisions.
func (pr *Prober) PublishApply(ctx context.Context, t Target, id string, req companion.ApplyRequest) (companion.ApplyResult, error) {
	var out companion.ApplyResult
	body, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	resp, err := pr.publishCall(ctx, t, applyTimeout, func(c *companion.Client, s companion.Site) (companion.Response, error) {
		return c.Post(ctx, s, "content/publish/"+id+"/apply", body)
	})
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return out, fmt.Errorf("the report isn't readable: %w", err)
	}
	return out, nil
}

// PublishJournals lists what production imported, newest first.
func (pr *Prober) PublishJournals(ctx context.Context, t Target) ([]companion.Journal, error) {
	resp, err := pr.publishCall(ctx, t, planTimeout, func(c *companion.Client, s companion.Site) (companion.Response, error) {
		return c.Get(ctx, s, "content/publish/journals", nil)
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Journals []companion.Journal `json:"journals"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, fmt.Errorf("the journals aren't readable: %w", err)
	}
	return out.Journals, nil
}

// PublishUndo undoes a publish from its journal ("" = the newest), or says
// what it would do (dryRun).
func (pr *Prober) PublishUndo(ctx context.Context, t Target, journal string, dryRun bool) (companion.UndoResult, error) {
	var out companion.UndoResult
	body, _ := json.Marshal(map[string]any{"journal": journal, "dry_run": dryRun})
	resp, err := pr.publishCall(ctx, t, applyTimeout, func(c *companion.Client, s companion.Site) (companion.Response, error) {
		return c.Post(ctx, s, "content/publish/undo", body)
	})
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return out, fmt.Errorf("the undo's answer isn't readable: %w", err)
	}
	return out, nil
}

// publishCall makes one call with the pinned key and its own timeout, and
// says what a refusal means.
func (pr *Prober) publishCall(_ context.Context, t Target, timeout time.Duration, call func(*companion.Client, companion.Site) (companion.Response, error)) (companion.Response, error) {
	pin, ok := pr.Pins[t.Slug]
	if !ok {
		return companion.Response{}, fmt.Errorf("no pinned key for %s: run `taw-fleet live trust %s` first", t.Slug, t.Slug)
	}
	c := *pr.Client
	c.HTTP = &http.Client{Timeout: timeout}
	resp, err := call(&c, companion.Site{URL: t.URL, Pinned: &pin})
	if err != nil {
		return resp, PublishError(t.URL, err)
	}
	if !resp.Verified {
		return resp, errors.New("production's answer isn't verified")
	}
	return resp, nil
}

// Refusal is production refusing a publish step, with what to act on.
type Refusal struct {
	Code    string
	Message string
	Status  int
	// Moved: records that changed since the review.
	Moved []struct {
		Key string `json:"key"`
	} `json:"moved"`
	// Conflicts: values both sides changed, undecided.
	Conflicts []struct {
		Key    string `json:"key"`
		Change string `json:"change"`
	} `json:"conflicts"`
	// Missing: files not uploaded yet.
	Missing []string `json:"missing"`
}

func (r *Refusal) Error() string { return r.Message }

// PublishError says what a failed publish call means, in the person's words.
func PublishError(siteURL string, err error) error {
	var api *companion.APIError
	if !errors.As(err, &api) {
		return err
	}
	switch {
	case api.Status == http.StatusNotFound && strings.Contains(api.Body, "rest_no_route"):
		return fmt.Errorf("%s's companion can't take a publish: it needs taw/hub-companion 0.5+ (bump it in the theme's composer.json to ^0.5, and deploy)", siteURL)
	case api.Code == "publish_disabled":
		return fmt.Errorf("publishing isn't enabled for %s: set extra.taw-companion.publish to true in the theme's composer.json, and deploy", siteURL)
	case api.Code == "publish_unavailable":
		return fmt.Errorf("%s needs taw/core 1.98 or newer to take a publish: update the theme (u), and deploy", siteURL)
	case strings.HasPrefix(api.Code, "publish_"):
		r := &Refusal{Code: api.Code, Message: api.Message, Status: api.Status}
		_ = json.Unmarshal([]byte(api.Body), r)
		if r.Message == "" {
			r.Message = api.Code
		}
		return r
	case api.Status == http.StatusForbidden || api.Status == http.StatusNotAcceptable:
		if !strings.HasPrefix(strings.TrimSpace(api.Body), "{") {
			return fmt.Errorf("%s's host refused the request before WordPress saw it (%d): its firewall may block uploads to /wp-json/taw-hub/v1/content/publish/*", siteURL, api.Status)
		}
	}
	return err
}
