package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/Relmaur/taw-fleet/internal/paths"
)

// NewSite is what addSite needs. Probed on Local 10.1.2: with Environment
// "preferred" the service fields may be empty. PHP is a plain version
// ("8.5.3"); WebServer and Database are "<service>-<version>"
// ("nginx-1.26.1", "mysql-8.4.0"). Local's API doesn't check them, so pick
// them from Services.
type NewSite struct {
	Name, Path, Domain string
	PHP                string // "" = preferred
	WebServer          string // "" = preferred
	Database           string // "" = preferred
	AdminUser          string
	AdminPassword      string
	AdminEmail         string
}

// Job is one of Local's background jobs (addSite returns one).
type Job struct {
	ID     string          `json:"id"`
	Status string          `json:"status"` // created | running | successful | failed
	Logs   string          `json:"logs"`
	Error  json.RawMessage `json:"error"`
}

// Done reports whether the job finished, either way.
func (j Job) Done() bool { return j.Status == "successful" || j.Status == "failed" }

// Err is the job's failure, or nil.
func (j Job) Err() error {
	if j.Status != "failed" {
		return nil
	}
	msg := strings.TrimSpace(string(j.Error))
	var s string
	if json.Unmarshal(j.Error, &s) == nil {
		msg = s
	} else {
		var o struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(j.Error, &o) == nil && o.Message != "" {
			msg = o.Message
		}
	}
	if msg == "" || msg == "null" {
		msg = "no reason given"
	}
	return fmt.Errorf("local couldn't create the site: %s", msg)
}

// AddSite asks Local to create a site (provision, install WordPress, start
// it) and returns the job. The job's id is not the site's: find the new
// site in sites.json by its path.
func (g *GraphQL) AddSite(ctx context.Context, s NewSite) (Job, error) {
	in := map[string]any{
		"name": s.Name, "path": s.Path, "domain": s.Domain,
		"environment":     "preferred",
		"wpAdminUsername": s.AdminUser, "wpAdminPassword": s.AdminPassword, "wpAdminEmail": s.AdminEmail,
		"goToSite": false,
	}
	if s.PHP != "" || s.WebServer != "" || s.Database != "" {
		in["environment"] = "custom"
	}
	for k, v := range map[string]string{"phpVersion": s.PHP, "webServer": s.WebServer, "database": s.Database} {
		if v != "" {
			in[k] = v
		}
	}
	var out struct {
		AddSite *Job `json:"addSite"`
	}
	if err := g.do(ctx, `mutation ($input: AddSiteInput!) { addSite(input: $input) { id status } }`, map[string]any{"input": in}, &out); err != nil {
		return Job{}, err
	}
	if out.AddSite == nil {
		return Job{}, errors.New("local API: addSite returned no job")
	}
	return *out.AddSite, nil
}

// Job returns a job's state.
func (g *GraphQL) Job(ctx context.Context, id string) (Job, error) {
	var out struct {
		Job *Job `json:"job"`
	}
	if err := g.do(ctx, `query ($id: ID!) { job(id: $id) { id status logs error } }`, map[string]any{"id": id}, &out); err != nil {
		return Job{}, err
	}
	if out.Job == nil {
		return Job{}, fmt.Errorf("local doesn't know a job %q", id)
	}
	return *out.Job, nil
}

// WaitJob polls a job every poll until it's done; progress gets each new
// status.
func (g *GraphQL) WaitJob(ctx context.Context, id string, poll time.Duration, progress func(Job)) (Job, error) {
	last := ""
	for {
		j, err := g.Job(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return j, fmt.Errorf("local is still creating the site: %w", ctx.Err())
			}
			return j, err
		}
		if j.Status != last && progress != nil {
			progress(j)
		}
		last = j.Status
		if j.Done() {
			return j, j.Err()
		}
		select {
		case <-ctx.Done():
			return j, fmt.Errorf("local is still creating the site: %w", ctx.Err())
		case <-time.After(poll):
		}
	}
}

// Services lists the versions Local has downloaded, newest first, by service
// ("php" → ["8.5.3", "8.2.30"], "nginx" → ["1.26.1"]). Folders are
// lightning-services/<service>-<version>[+<build>].
func Services(p paths.Paths) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, dir := range []string{filepath.Join(p.LocalSupport, "lightning-services"), filepath.Join(p.LocalApp, "Contents", "Resources", "extraResources", "lightning-services")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name, ver, ok := strings.Cut(e.Name(), "-")
			if !ok {
				continue
			}
			ver, _, _ = strings.Cut(ver, "+")
			if !semver.IsValid("v"+ver) || seen[name+"-"+ver] {
				continue
			}
			seen[name+"-"+ver] = true
			out[name] = append(out[name], ver)
		}
	}
	for _, vs := range out {
		sort.Slice(vs, func(i, j int) bool { return semver.Compare("v"+vs[i], "v"+vs[j]) > 0 })
	}
	return out
}

// WebServers and Databases are the service names of each role.
var (
	WebServers = []string{"nginx", "apache"}
	Databases  = []string{"mysql", "mariadb"}
)
