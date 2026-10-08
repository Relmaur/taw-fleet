// Package cli holds the taw-fleet commands.
package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// BuildInfo is what the release build stamps into the binary.
type BuildInfo struct {
	Version string
	Commit  string
}

// NewRoot builds the command tree. Output goes to out so tests can read it.
func NewRoot(info BuildInfo, out io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "taw-fleet",
		Short:         "The TAW sites on this Mac: status, versions and shortcuts",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	root.SetErr(out)
	root.AddCommand(newVersionCmd(info))
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
	if err := NewRoot(info, os.Stdout).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "taw-fleet:", err)
		return 1
	}
	return 0
}
