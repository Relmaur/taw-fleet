// Command taw-fleet shows and manages the TAW sites on this Mac.
package main

import (
	"os"

	"github.com/Relmaur/taw-fleet/internal/cli"
)

// Set by the release build (-ldflags "-X main.version=… -X main.commit=…").
var (
	version = "dev"
	commit  = "none"
)

func main() {
	os.Exit(cli.Execute(cli.BuildInfo{Version: version, Commit: commit}))
}
