// Package cli holds the taw-fleet commands.
package cli

import (
	"fmt"
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/style"
)

// BuildInfo is what the release build stamps into the binary.
type BuildInfo struct {
	Version string
	Commit  string
}

// Deps is everything the commands touch outside the process. Tests build
// their own; Execute builds the real ones.
type Deps struct {
	Paths  paths.Paths
	Runner exec.Runner
	Out    io.Writer
	Err    io.Writer
	Dark   bool // the terminal has a dark background
}

func (d Deps) palette() style.Palette { return style.New(d.Dark) }

func (d Deps) scanner() *scan.Scanner {
	return &scan.Scanner{Sources: []scan.Source{scan.NewLocalSource(d.Paths)}}
}

// NewRoot builds the command tree.
func NewRoot(info BuildInfo, d Deps) *cobra.Command {
	root := &cobra.Command{
		Use:           "taw-fleet",
		Short:         "The TAW sites on this Mac: status, versions and shortcuts",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(d.Out)
	root.SetErr(d.Err)
	root.AddCommand(newVersionCmd(info), newListCmd(d))
	return root
}

func newVersionCmd(info BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the taw-fleet version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "taw-fleet %s (%s)\n", info.Version, info.Commit)
			return err
		},
	}
}

// Execute runs the CLI and returns the process exit code.
func Execute(info BuildInfo) int {
	p, err := paths.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, "taw-fleet:", err)
		return 1
	}
	dark := style.IsDark(os.Getenv)
	d := Deps{Paths: p, Runner: exec.OSRunner{}, Out: os.Stdout, Err: os.Stderr, Dark: dark}
	if err := NewRoot(info, d).Execute(); err != nil {
		pal := style.New(dark)
		_, _ = lipgloss.Fprintln(os.Stderr, pal.Fg(pal.Err).Render("taw-fleet:"), err)
		return 1
	}
	return 0
}
