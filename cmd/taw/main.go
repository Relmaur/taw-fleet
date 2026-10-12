// Command taw runs a TAW theme's own `vendor/bin/taw`: in the theme folder
// (or any folder under it), or for a site from anywhere with `taw @<site>`.
// It finds the theme, and on a Local site uses the site's PHP and MySQL
// socket; the theme's taw/core does the rest. Its version is taw-fleet's
// (`taw-fleet version`): both ship in the same release.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/tawcmd"
)

func main() {
	// run returns only when it can't hand over: the theme's command exits itself.
	os.Exit(run(os.Args[1:]))
}

//nolint:unparam // every return is a failure; success never returns (syscall.Exec)
func run(args []string) int {
	p, err := paths.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, "taw:", err)
		return 1
	}
	env := tawcmd.Env{
		Paths: p,
		Getwd: os.Getwd,
		Sites: func(ctx context.Context) ([]site.Site, error) { return scan.NewLocalSource(p).Sites(ctx) },
		Err:   os.Stderr,
	}
	cmd, err := tawcmd.Resolve(context.Background(), env, args)
	if errors.Is(err, tawcmd.ErrNotInTheme) {
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "taw:", err)
		return 1
	}
	if os.Getenv("TAW_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "taw: in %s\ntaw: %s\n", cmd.Dir, cmd.Line())
	}
	if err := os.Chdir(cmd.Dir); err != nil {
		fmt.Fprintln(os.Stderr, "taw:", err)
		return 1
	}
	// The theme's command takes over this process: its terminal, its exit code.
	err = syscall.Exec(cmd.Path, cmd.Args, append(os.Environ(), cmd.Env...))
	fmt.Fprintln(os.Stderr, "taw:", err)
	return 1
}
