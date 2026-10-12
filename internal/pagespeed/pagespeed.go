// Package pagespeed asks Google PageSpeed Insights about a page: Lighthouse's
// scores (a lab test run now) and, when Google has enough real visits, the
// Core Web Vitals of the last 28 days. A monthly report's speed section.
package pagespeed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Relmaur/taw-fleet/internal/keychain"
)

// KeyItem is the API key in the Keychain (taw-fleet report key import).
var KeyItem = keychain.Item{Account: "pagespeed-api-key", Label: "taw-fleet PageSpeed Insights API key"}

// Endpoint is the PageSpeed Insights API.
const Endpoint = "https://www.googleapis.com/pagespeedonline/v5/runPagespeed"

// Result is one strategy's answer (mobile or desktop).
type Result struct {
	Strategy      string `json:"strategy"`
	Performance   int    `json:"performance"` // 0–100; -1 = not given
	Accessibility int    `json:"accessibility"`
	BestPractices int    `json:"best_practices"`
	SEO           int    `json:"seo"`
	LCP           string `json:"lcp,omitempty"` // lab: "1.9 s"
	CLS           string `json:"cls,omitempty"`
	TBT           string `json:"tbt,omitempty"`
	Field         *Field `json:"field,omitempty"` // real visitors; nil = not enough traffic
}

// Field is the Chrome UX Report: real visitors over the last 28 days, at the
// 75th percentile.
type Field struct {
	LCPms   int     `json:"lcp_ms"`
	INPms   int     `json:"inp_ms"`
	CLS     float64 `json:"cls"`
	Overall string  `json:"overall"` // FAST, AVERAGE, SLOW
}

// Client asks the API. Key is optional (the API answers a few requests a
// day without one).
type Client struct {
	HTTP     *http.Client
	Key      string
	Endpoint string
}

// Run tests the page with one strategy: "mobile" or "desktop".
func (c Client) Run(ctx context.Context, page, strategy string) (Result, error) {
	q := url.Values{"url": {page}, "strategy": {strategy}}
	for _, cat := range []string{"PERFORMANCE", "ACCESSIBILITY", "BEST_PRACTICES", "SEO"} {
		q.Add("category", cat)
	}
	if c.Key != "" {
		q.Set("key", c.Key)
	}
	end := c.Endpoint
	if end == "" {
		end = Endpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, end+"?"+q.Encode(), nil)
	if err != nil {
		return Result{}, err
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return Result{}, err
	}
	var raw struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Lighthouse struct {
			Categories map[string]struct {
				Score *float64 `json:"score"`
			} `json:"categories"`
			Audits map[string]struct {
				Display string `json:"displayValue"`
			} `json:"audits"`
		} `json:"lighthouseResult"`
		Loading struct {
			Overall string `json:"overall_category"`
			Metrics map[string]struct {
				Percentile float64 `json:"percentile"`
			} `json:"metrics"`
		} `json:"loadingExperience"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Result{}, fmt.Errorf("PageSpeed answered %d: unreadable", resp.StatusCode)
	}
	if raw.Error != nil {
		if raw.Error.Code == http.StatusTooManyRequests {
			return Result{}, fmt.Errorf("PageSpeed's quota is used up: without a key it's shared by everyone (taw-fleet report key import stores a free Google API key)")
		}
		return Result{}, fmt.Errorf("PageSpeed answered %d: %s", raw.Error.Code, raw.Error.Message)
	}
	if resp.StatusCode/100 != 2 {
		return Result{}, fmt.Errorf("PageSpeed answered %d", resp.StatusCode)
	}
	score := func(k string) int {
		if c, ok := raw.Lighthouse.Categories[k]; ok && c.Score != nil {
			return int(*c.Score*100 + 0.5)
		}
		return -1
	}
	r := Result{Strategy: strategy, Performance: score("performance"), Accessibility: score("accessibility"),
		BestPractices: score("best-practices"), SEO: score("seo"),
		LCP: raw.Lighthouse.Audits["largest-contentful-paint"].Display,
		CLS: raw.Lighthouse.Audits["cumulative-layout-shift"].Display,
		TBT: raw.Lighthouse.Audits["total-blocking-time"].Display}
	if m := raw.Loading.Metrics; raw.Loading.Overall != "" && len(m) > 0 {
		r.Field = &Field{Overall: raw.Loading.Overall,
			LCPms: int(m["LARGEST_CONTENTFUL_PAINT_MS"].Percentile),
			INPms: int(m["INTERACTION_TO_NEXT_PAINT"].Percentile),
			CLS:   m["CUMULATIVE_LAYOUT_SHIFT_SCORE"].Percentile / 100}
	}
	return r, nil
}
