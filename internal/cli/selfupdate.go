package cli

import (
	"errors"
	"fmt"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/selfupdate"
)

func (d Deps) updater() *selfupdate.Updater {
	if d.Updater != nil {
		return d.Updater
	}
	return selfupdate.New(github.TokenSource(d.Paths, d.Runner))
}

func (d Deps) executable() (string, error) {
	if d.Executable != nil {
		return d.Executable()
	}
	return os.Executable()
}

func newSelfUpdateCmd(info BuildInfo, d Deps) *cobra.Command {
	var check, yes bool
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Replace taw-fleet with the newest release",
		Long: "Download the newest taw-fleet release for this Mac from GitHub, check it against the\n" +
			"release's checksums.txt and replace this binary. A Homebrew install is left to\n" +
			"`brew upgrade taw-fleet`.",
		Example: "  taw-fleet self-update --check\n  taw-fleet self-update",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, out := d.palette(), cmd.OutOrStdout()
			u := d.updater()
			rel, err := u.Latest(cmd.Context())
			if err != nil {
				return err
			}
			if !selfupdate.Newer(info.Version, rel.Tag) {
				_, err := lipgloss.Fprintln(out, p.Fg(p.OK).Render("✓")+" taw-fleet "+rel.Version()+" is the newest release")
				return err
			}
			if _, err := lipgloss.Fprintln(out, p.Fg(p.Warn).Render("▲")+" taw-fleet "+rel.Version()+" is out (this is "+info.Version+")  "+p.Fg(p.Muted).Render(rel.URL)); err != nil {
				return err
			}
			if check {
				return nil
			}
			exe, err := d.executable()
			if err != nil {
				return err
			}
			target, err := selfupdate.Target(exe)
			if errors.Is(err, selfupdate.ErrHomebrew) {
				return fmt.Errorf("this taw-fleet was %w", err)
			}
			if err != nil {
				return err
			}
			if !yes {
				ok, err := d.confirm(out, "Replace "+target+" with "+rel.Version()+"?")
				if err != nil {
					return err
				}
				if !ok {
					_, err := fmt.Fprintln(out, "Nothing changed.")
					return err
				}
			}
			if err := u.Apply(cmd.Context(), rel, target); err != nil {
				return err
			}
			_, err = lipgloss.Fprintln(out, p.Fg(p.OK).Render("✓")+" taw-fleet is now "+rel.Version()+p.Fg(p.Muted).Render(" (checksum verified)"))
			return err
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only say whether a newer release exists")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}
