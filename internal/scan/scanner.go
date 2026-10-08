package scan

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Relmaur/taw-fleet/internal/site"
)

// Enricher fills in one kind of detail on a TAW theme (git state, versions…).
// It runs once per (site, theme), concurrently with other enrichers, and only
// touches the Theme it's given and nothing shared.
type Enricher interface {
	Name() string
	Enrich(ctx context.Context, s *site.Site, t *site.Theme) error
}

// Report is the result of a scan.
type Report struct {
	Sites     []site.Site        `json:"sites"`
	ScannedAt time.Time          `json:"scanned_at"`
	Latest    map[string]string  `json:"latest,omitempty"` // newest releases (see Latest* keys)
	Errors    []site.SourceError `json:"errors,omitempty"` // a whole source or lookup failed
}

// Scanner runs the sources, then the enrichers.
type Scanner struct {
	Sources   []Source
	Enrichers []Enricher
	Lookups   []Lookup
	Limit     int           // concurrent enrichers; default 8
	Timeout   time.Duration // per enricher call; default 5s
	Now       func() time.Time
}

// Run scans. It only fails when the context is cancelled; anything else is
// recorded in the report.
func (sc *Scanner) Run(ctx context.Context) (Report, error) {
	now := sc.Now
	if now == nil {
		now = time.Now
	}
	rep := Report{Sites: []site.Site{}}
	for _, src := range sc.Sources {
		sites, err := src.Sites(ctx)
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		if err != nil {
			rep.Errors = append(rep.Errors, site.SourceError{Stage: src.Name(), Err: err.Error()})
		}
		rep.Sites = append(rep.Sites, sites...)
	}

	// Lookups (network) run while the enrichers (disk, git) do.
	type lookupResult struct {
		name   string
		latest map[string]string
		errs   []error
	}
	results := make(chan lookupResult, len(sc.Lookups))
	for _, l := range sc.Lookups {
		go func() {
			latest, errs := l.Lookup(ctx)
			results <- lookupResult{l.Name(), latest, errs}
		}()
	}
	enrichErr := sc.enrich(ctx, rep.Sites)
	for range sc.Lookups {
		r := <-results
		for k, v := range r.latest {
			if rep.Latest == nil {
				rep.Latest = map[string]string{}
			}
			rep.Latest[k] = v
		}
		for _, e := range r.errs {
			rep.Errors = append(rep.Errors, site.SourceError{Stage: r.name, Err: e.Error()})
		}
	}
	if enrichErr != nil {
		return rep, enrichErr
	}
	applyLatest(rep.Sites, rep.Latest)
	rep.ScannedAt = now()
	return rep, nil
}

func (sc *Scanner) enrich(ctx context.Context, sites []site.Site) error {
	if len(sc.Enrichers) == 0 {
		return nil
	}
	limit, timeout := sc.Limit, sc.Timeout
	if limit <= 0 {
		limit = 8
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	// Each job writes its error into its own slot, so no locking is needed;
	// errors are attached to their site after the group finishes.
	type job struct {
		site, theme int
		enricher    Enricher
		err         error
	}
	var jobs []*job
	for si := range sites {
		for ti := range sites[si].Themes {
			if !sites[si].Themes[ti].IsTAW || sites[si].Themes[ti].Broken {
				continue
			}
			for _, e := range sc.Enrichers {
				jobs = append(jobs, &job{site: si, theme: ti, enricher: e})
			}
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)
	for _, j := range jobs {
		g.Go(func() error {
			cctx, cancel := context.WithTimeout(gctx, timeout)
			defer cancel()
			s := &sites[j.site]
			j.err = j.enricher.Enrich(cctx, s, &s.Themes[j.theme])
			return nil
		})
	}
	_ = g.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, j := range jobs {
		if j.err != nil {
			s := &sites[j.site]
			s.AddError(j.enricher.Name(), fmt.Errorf("%s: %w", s.Themes[j.theme].Dir, j.err))
		}
	}
	return nil
}

// Resolve finds one site by id, folder name, name, domain or theme folder
// (case-insensitive). An exact id/slug match wins over the rest.
func Resolve(sites []site.Site, q string) (*site.Site, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, fmt.Errorf("no site given")
	}
	for i := range sites {
		if sites[i].ID == q || sites[i].Slug == q {
			return &sites[i], nil
		}
	}
	var hits []int
	for i, s := range sites {
		if strings.EqualFold(s.Name, q) || strings.EqualFold(s.Domain, q) ||
			strings.EqualFold(s.Slug, q) || strings.EqualFold(strings.TrimSuffix(s.Domain, ".local"), q) {
			hits = append(hits, i)
			continue
		}
		for _, t := range s.Themes {
			if t.IsTAW && strings.EqualFold(t.Dir, q) {
				hits = append(hits, i)
				break
			}
		}
	}
	switch len(hits) {
	case 1:
		return &sites[hits[0]], nil
	case 0:
		return nil, fmt.Errorf("no site matches %q (try `taw-fleet list`)", q)
	}
	var names []string
	for _, i := range hits {
		names = append(names, sites[i].Slug)
	}
	sort.Strings(names)
	return nil, fmt.Errorf("%q matches several sites: %s", q, strings.Join(names, ", "))
}
