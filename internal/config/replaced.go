package config

import "os"

// A long-running taw-fleet (an MCP server, the dashboard) can outlive its
// binary: an upgrade replaces it, and the new one may write settings this one
// doesn't know. Replaced tells that apart from a typo in the settings.

var (
	exePath string
	exeInfo os.FileInfo
	// statExe is os.Stat; tests replace it.
	statExe = os.Stat
)

func init() {
	if p, err := os.Executable(); err == nil {
		exePath = p
		exeInfo, _ = os.Stat(p)
	}
}

// Replaced reports whether this program's binary was removed or replaced
// since it started (Homebrew removes the old version's folder).
func Replaced() bool {
	if exePath == "" || exeInfo == nil {
		return false
	}
	now, err := statExe(exePath)
	return err != nil || !os.SameFile(exeInfo, now)
}
