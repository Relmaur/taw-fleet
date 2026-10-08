// Package local reads Local by Flywheel's own files: the site registry, the
// running states, the per-site MySQL sockets and the bundled tools.
//
// None of these files are a public contract, so parsing is lenient: a field
// with an unexpected type is ignored, and a site that can't be read at all is
// still returned with ParseErr set.
package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/paths"
)

// ErrNoLocal means Local isn't installed for this user, or has never run.
var ErrNoLocal = errors.New("no Local by Flywheel sites found (sites.json is missing)")

// RawSite is one entry of sites.json, reduced to what taw-fleet uses.
type RawSite struct {
	ID           string
	Name         string
	Path         string // absolute, "~/" expanded
	Domain       string
	PHPVersion   string
	MySQLVersion string
	HTTPPort     int
	MySQLPort    int
	WebServer    string
	MultiSite    string
	Xdebug       bool
	Hosts        []Host
	ParseErr     error // set when the entry couldn't be read; other fields may be empty
}

// Host is a hostConnections entry.
type Host struct {
	HostID string
	Env    string
}

// RegistryFile is where Local keeps its sites.
func RegistryFile(p paths.Paths) string {
	return filepath.Join(p.LocalSupport, "sites.json")
}

// LoadRegistry reads sites.json and returns the sites sorted by name.
func LoadRegistry(p paths.Paths) ([]RawSite, error) {
	data, err := os.ReadFile(RegistryFile(p))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoLocal
	}
	if err != nil {
		return nil, err
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("sites.json: %w", err)
	}
	sites := make([]RawSite, 0, len(entries))
	for id, raw := range entries {
		sites = append(sites, parseSite(p, id, raw))
	}
	sort.Slice(sites, func(i, j int) bool {
		a, b := strings.ToLower(sites[i].Name), strings.ToLower(sites[j].Name)
		if a != b {
			return a < b
		}
		return sites[i].ID < sites[j].ID
	})
	return sites, nil
}

// rawEntry mirrors the parts of a sites.json entry we read. Fields whose type
// varies between Local versions are json.RawMessage and decoded by hand.
type rawEntry struct {
	ID            string                     `json:"id"`
	Name          string                     `json:"name"`
	Path          string                     `json:"path"`
	Domain        string                     `json:"domain"`
	PHPVersion    string                     `json:"phpVersion"`
	Services      map[string]json.RawMessage `json:"services"`
	WebServer     json.RawMessage            `json:"webServer"`
	MultiSite     json.RawMessage            `json:"multiSite"`
	Xdebug        json.RawMessage            `json:"xdebugEnabled"`
	HostConnected []json.RawMessage          `json:"hostConnections"`
}

type rawService struct {
	Name    string           `json:"name"`
	Version string           `json:"version"`
	Role    string           `json:"role"`
	Ports   map[string][]int `json:"ports"`
}

func parseSite(p paths.Paths, id string, raw json.RawMessage) RawSite {
	s := RawSite{ID: id}
	var e rawEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		// Keep whatever identifies the site, so it's still listed with its error.
		var named struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(raw, &named)
		s.Name = named.Name
		s.ParseErr = fmt.Errorf("sites.json entry %s: %w", id, err)
		return s
	}
	if e.ID != "" {
		s.ID = e.ID
	}
	s.Name = e.Name
	s.Path = p.Expand(e.Path)
	s.Domain = e.Domain
	s.PHPVersion = e.PHPVersion
	s.WebServer = webServerName(asString(e.WebServer))
	s.MultiSite = asString(e.MultiSite)
	s.Xdebug = asBool(e.Xdebug)

	for key, rawSvc := range e.Services {
		var svc rawService
		if json.Unmarshal(rawSvc, &svc) != nil {
			continue
		}
		role, name := svc.Role, svc.Name
		if name == "" {
			name = key
		}
		switch {
		case role == "php" || name == "php":
			if svc.Version != "" {
				s.PHPVersion = svc.Version
			}
		case role == "db" || name == "mysql" || name == "mariadb":
			s.MySQLVersion = svc.Version
			s.MySQLPort = firstPort(svc.Ports, "MYSQL")
		case role == "http" || name == "nginx" || name == "apache":
			s.HTTPPort = firstPort(svc.Ports, "HTTP")
			if s.WebServer == "" {
				s.WebServer = name
			}
		}
	}
	for _, rawHost := range e.HostConnected {
		var h struct {
			HostID string `json:"hostId"`
			Env    string `json:"remoteSiteEnv"`
		}
		if json.Unmarshal(rawHost, &h) == nil && h.HostID != "" {
			s.Hosts = append(s.Hosts, Host(h))
		}
	}
	if s.Path == "" {
		s.ParseErr = fmt.Errorf("sites.json entry %s has no path", id)
	}
	return s
}

// webServerName turns Local's "nginx-1.26.1" into "nginx 1.26.1".
func webServerName(s string) string {
	if i := strings.Index(s, "-"); i > 0 && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
		return s[:i] + " " + s[i+1:]
	}
	return s
}

func firstPort(ports map[string][]int, key string) int {
	if p := ports[key]; len(p) > 0 {
		return p[0]
	}
	return 0
}

// asString returns a JSON string's value, or "" for null, numbers, objects…
func asString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func asBool(raw json.RawMessage) bool {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	return false
}
