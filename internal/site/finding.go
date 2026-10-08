package site

import (
	"fmt"
	"strings"
)

// Severity orders findings: Error > Warn > Info.
type Severity int

// Severities.
const (
	Info Severity = iota
	Warn
	Error
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warn:
		return "warn"
	}
	return "info"
}

// MarshalText makes severities read as words in JSON.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText reads a severity word.
func (s *Severity) UnmarshalText(b []byte) error {
	switch strings.ToLower(string(b)) {
	case "error":
		*s = Error
	case "warn", "warning":
		*s = Warn
	case "info":
		*s = Info
	default:
		return fmt.Errorf("unknown severity %q", b)
	}
	return nil
}

// Finding is one thing worth knowing about a site or theme.
type Finding struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"` // stable, dot-namespaced: core.behind, git.dirty…
	SiteID   string   `json:"site_id"`
	Site     string   `json:"site"` // the site's folder name
	Theme    string   `json:"theme,omitempty"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
}
