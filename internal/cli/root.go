// Package cli holds the taw-fleet commands.
package cli

import (
	"fmt"
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/doctor"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/local"
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
	Dark   bool           // the terminal has a dark background
	GitHub *github.Client // newest-release lookups; nil = built from Paths
}

func (d Deps) palette() style.Palette { return style.New(d.Dark) }

// globals are the flags every command shares.
type globals struct {
	offline bool
}

func (d Deps) github(g *globals) *github.Client {
	c := d.GitHub
	if c == nil {
		c = github.New(d.Paths.CacheDir, github.TokenSource(d.Paths, d.Runner))
	}
	c.Offline = g.offline
	return c
}

func (d Deps) scanner(g *globals) *scan.Scanner {
	return &scan.Scanner{
		Sources:   []scan.Source{scan.NewLocalSource(d.Paths)},
		Enrichers: []scan.Enricher{scan.GitEnricher{Runner: d.Runner}, scan.CoreEnricher{}},
		Lookups:   []scan.Lookup{scan.GitHubLookup{Client: d.github(g)}},
	}
}

func (d Deps) doctorOptions() doctor.Options {
	return doctor.Options{PHPAvailable: func(v string) bool {
		_, ok := local.PHPBinary(d.Paths, v)
		return ok
	}}
}

// NewRoot builds the command tree.
func NewRoot(info BuildInfo, d Deps) *cobra.Command {
	root := &cobra.Command{
		Use:           "taw-fleet",
		Short:         "The TAW sites on this Mac: status, versions and shortcuts",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	g := &globals{}
	root.PersistentFlags().BoolVar(&g.offline, "offline", false, "don't ask GitHub for the newest versions (use the cache)")
	root.SetOut(d.Out)
	root.SetErr(d.Err)
	root.AddCommand(newVersionCmd(info), newListCmd(d, g), newShowCmd(d, g), newDoctorCmd(d, g))
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
