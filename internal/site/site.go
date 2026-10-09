// Package site is the model every other package fills in or reads: a site,
// its themes, and what's wrong with them.
package site

import (
	"strings"
	"time"
)

// Status is a site's running state as Local reports it.
type Status string

// The states taw-fleet distinguishes. Local reports more (starting, stopping,
// …); those become StatusBusy.
const (
	StatusRunning Status = "running"
	StatusHalted  Status = "halted"
	StatusBusy    Status = "busy"
	StatusUnknown Status = "unknown"
)

// ParseStatus maps Local's state strings onto Status.
func ParseStatus(s string) Status {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "running":
		return StatusRunning
	case "halted", "stopped":
		return StatusHalted
	case "":
		return StatusUnknown
	}
	return StatusBusy
}

// ThemeKind says which TAW starter a theme is built on.
type ThemeKind string

// Theme kinds.
const (
	KindClassic   ThemeKind = "classic"   // taw-theme and its client forks
	KindGutenberg ThemeKind = "gutenberg" // taw-gutenberg (block theme)
	KindOther     ThemeKind = "other"     // not a TAW theme
)

// HostConnection is a Local "Connect" link to a host (e.g. WP Engine).
type HostConnection struct {
	HostID string `json:"host_id"`
	Env    string `json:"env,omitempty"`
}

// SourceError is a problem found while reading one part of a site. A site
// with errors is still listed.
type SourceError struct {
	Stage string `json:"stage"`
	Err   string `json:"error"`
}

// Site is one WordPress install.
type Site struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"` // the site folder's name
	Path     string `json:"path"` // absolute site folder
	Domain   string `json:"domain"`
	URL      string `json:"url"`
	Status   Status `json:"status"`
	Source   string `json:"source"` // "local"; other sources come later
	WebRoot  string `json:"web_root"`
	Socket   string `json:"socket,omitempty"`
	SockLive bool   `json:"socket_live"`

	PHPVersion   string `json:"php_version,omitempty"`
	MySQLVersion string `json:"mysql_version,omitempty"`
	HTTPPort     int    `json:"http_port,omitempty"`
	MySQLPort    int    `json:"mysql_port,omitempty"`
	WebServer    string `json:"web_server,omitempty"`
	MultiSite    string `json:"multisite,omitempty"`
	Xdebug       bool   `json:"xdebug"`

	ActiveTheme string `json:"active_theme,omitempty"` // the theme WordPress uses; known only while the site runs

	Production *Production `json:"production,omitempty"` // the live site, through its companion (nil = not checked)

	Hosts  []HostConnection `json:"hosts,omitempty"`
	Themes []Theme          `json:"themes"`
	Errors []SourceError    `json:"errors,omitempty"`
}

// Theme is one folder in wp-content/themes.
type Theme struct {
	Dir       string    `json:"dir"`
	Path      string    `json:"path"`
	RealPath  string    `json:"real_path"`
	Symlink   bool      `json:"symlink"`
	Broken    bool      `json:"broken,omitempty"` // a symlink whose target is gone
	Package   string    `json:"package,omitempty"`
	Kind      ThemeKind `json:"kind"`
	IsTAW     bool      `json:"is_taw"`
	HasBinTaw bool      `json:"has_bin_taw"`

	// Version is `git describe --tags --always`. Never style.css, which is
	// stale in every TAW theme. Client forks inherit taw-theme's old tags, so
	// for them it's informational only.
	Version  string       `json:"version,omitempty"`
	Core     CoreInfo     `json:"core"`
	Scaffold ScaffoldInfo `json:"scaffold"`
	Git      *GitInfo     `json:"git,omitempty"`        // nil when the folder isn't its own git repo
	Drift    *Drift       `json:"drift,omitempty"`      // the last `bin/taw sync` taw-fleet ran; nil = never
	Dev      string       `json:"dev_server,omitempty"` // the running Vite dev server's URL; "" = not running
}

// Production is what the live site's companion said.
type Production struct {
	URL       string    `json:"url"`
	CheckedAt time.Time `json:"checked_at"`
	TookMS    int64     `json:"took_ms"`

	Reachable bool   `json:"reachable"` // the companion answered /health
	Verified  bool   `json:"verified"`  // its answers carried the pinned key's signature
	KeyID     string `json:"key_id,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorKind string `json:"error_kind,omitempty"` // unreachable | signature | auth | companion | no-key

	WP        string `json:"wp_version,omitempty"`
	PHP       string `json:"php_version,omitempty"`
	TawCore   string `json:"taw_core_version,omitempty"`
	Companion string `json:"companion_version,omitempty"`

	HasInventory  bool     `json:"has_inventory"` // /inventory answered (companion ≥ 0.2)
	HasVulns      bool     `json:"has_vulnerabilities"`
	Plugins       int      `json:"plugins"`
	PluginUpdates []string `json:"plugin_updates,omitempty"` // "slug 1.2 → 1.3"

	Scanner       string     `json:"scanner,omitempty"` // Defender, Wordfence; "" = none
	Vulns         []LiveVuln `json:"vulnerabilities,omitempty"`
	WorstSeverity string     `json:"worst_severity,omitempty"`

	Logs []LiveLog `json:"logs,omitempty"`
}

// LiveVuln is one known vulnerability on a live site.
type LiveVuln struct {
	Component string `json:"component"` // "plugin akismet 5.1"
	Severity  string `json:"severity"`
	Title     string `json:"title"`
	Link      string `json:"link,omitempty"`
}

// LiveLog is one taw/core log line from a live site.
type LiveLog struct {
	TS      string `json:"ts"`
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Drift is what the last scaffold sync check found.
type Drift struct {
	Tier1  []string  `json:"tier1"` // framework files that differ (applied without asking)
	Tier2  []string  `json:"tier2"` // files to review (often just the site's own dependencies)
	Errors []string  `json:"errors,omitempty"`
	At     time.Time `json:"at"`
}

// CoreInfo is the theme's taw/core.
type CoreInfo struct {
	Installed    string `json:"installed,omitempty"` // vendor/composer/installed.json
	Locked       string `json:"locked,omitempty"`    // composer.lock
	Latest       string `json:"latest,omitempty"`    // newest stable tag on GitHub
	Behind       bool   `json:"behind"`
	LockMismatch bool   `json:"lock_mismatch"`
	Err          string `json:"error,omitempty"`
}

// ScaffoldInfo is the TAW starter a theme comes from and its newest release.
type ScaffoldInfo struct {
	Name   string `json:"name,omitempty"` // taw-theme | taw-gutenberg
	Latest string `json:"latest,omitempty"`
}

// GitInfo is a theme repository's state.
type GitInfo struct {
	Branch        string    `json:"branch"`
	Detached      bool      `json:"detached"`
	DefaultBranch string    `json:"default_branch,omitempty"` // from origin/HEAD
	Upstream      string    `json:"upstream,omitempty"`       // e.g. origin/main
	Ahead         int       `json:"ahead"`
	Behind        int       `json:"behind"`
	Dirty         int       `json:"dirty"` // changed + untracked files
	Describe      string    `json:"describe,omitempty"`
	LastTag       string    `json:"last_tag,omitempty"`
	LastCommit    time.Time `json:"last_commit,omitzero"`
	RemoteURL     string    `json:"remote_url,omitempty"`
	Repo          *Repo     `json:"repo,omitempty"`
}

// HasUpstream reports whether the branch tracks a remote branch.
func (g GitInfo) HasUpstream() bool { return g.Upstream != "" }

// Repo is a hosted repository parsed from a remote URL.
type Repo struct {
	Host  string `json:"host"` // github.com (SSH aliases like github.com-work are mapped back)
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

// FullName is "owner/name".
func (r Repo) FullName() string { return r.Owner + "/" + r.Name }

// WebURL is the repository's page.
func (r Repo) WebURL() string { return "https://" + r.Host + "/" + r.Owner + "/" + r.Name }

// TAWThemes returns the site's TAW themes only.
func (s Site) TAWThemes() []Theme {
	var out []Theme
	for _, t := range s.Themes {
		if t.IsTAW {
			out = append(out, t)
		}
	}
	return out
}

// IsTAW reports whether the site has at least one TAW theme.
func (s Site) IsTAW() bool { return len(s.TAWThemes()) > 0 }

// AddError records a problem with the site without dropping it.
func (s *Site) AddError(stage string, err error) {
	if err != nil {
		s.Errors = append(s.Errors, SourceError{Stage: stage, Err: err.Error()})
	}
}
