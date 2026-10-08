// Command taw-fleet shows and manages the TAW sites on this Mac.
package main

import (
	"os"
	"runtime/debug"

	"github.com/Relmaur/taw-fleet/internal/cli"
)

// Set by the release build (-ldflags "-X main.version=… -X main.commit=…").
var (
	version = "dev"
	commit  = "none"
)

func main() {
	os.Exit(cli.Execute(stamp(version, commit, debug.ReadBuildInfo)))
}

// stamp fills in what the release build didn't: `go install …@v0.7.0`
// records the module version, `go build` in a checkout the git commit.
func stamp(v, c string, read func() (*debug.BuildInfo, bool)) cli.BuildInfo {
	info := cli.BuildInfo{Version: v, Commit: c}
	if v != "dev" {
		return info
	}
	bi, ok := read()
	if !ok {
		return info
	}
	if mv := bi.Main.Version; mv != "" && mv != "(devel)" {
		info.Version = mv
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && c == "none" && len(s.Value) >= 7 {
			info.Commit = s.Value[:7]
		}
	}
	return info
}
