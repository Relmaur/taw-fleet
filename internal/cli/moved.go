package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/notes"
	"github.com/Relmaur/taw-fleet/internal/scan"
)

// newMovedCmd shows, sets or clears a site's moved_to: the site left
// WordPress, and taw-fleet stops checking and updating it.
func newMovedCmd(d Deps, g *globals) *cobra.Command {
	var yes, unmark bool
	cmd := &cobra.Command{
		Use:   "moved <site> [what it is now]",
		Short: "Mark a site that left WordPress (moved_to): taw-fleet stops checking and updating it",
		Long: "For a site rebuilt on another stack, its domain now answers from something that isn't\n" +
			"WordPress. Marking it moved keeps the Local site and its theme in the list, with \"moved\" on\n" +
			"the card and a MOVED section in the details, and stops everything aimed at its WordPress\n" +
			"production: the live check, watch, monthly reports, publishing, deploys, wp-remote, the\n" +
			"SETUP checklist and update-all. production_url stays in the settings as where it answers\n" +
			"now. With only the site, says whether it's moved; --clear puts it back in the fleet.\n" +
			"Asks first, and writes a site note.",
		Example: "  taw-fleet moved acme-bakery \"Laravel on Forge\"\n  taw-fleet moved acme-bakery\n  taw-fleet moved acme-bakery --clear",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			offline := *g
			offline.offline = true
			rep, err := d.scanner(&offline).Run(cmd.Context())
			if err != nil {
				return err
			}
			s, err := scan.Resolve(rep.Sites, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			p := d.palette()
			if len(args) == 1 && !unmark {
				if s.Moved == nil {
					_, err := fmt.Fprintln(out, s.Slug+" is a WordPress site in the fleet. taw-fleet moved "+s.Slug+" \"<what it is now>\" marks it moved.")
					return err
				}
				line := s.Slug + " moved to " + s.Moved.To
				if s.Moved.URL != "" {
					line += " (" + s.Moved.URL + ")"
				}
				_, err := fmt.Fprintln(out, line)
				return err
			}
			if unmark && len(args) > 1 {
				return errors.New("--clear removes the mark: don't say where it went too")
			}
			to := ""
			if !unmark {
				to = strings.TrimSpace(args[1])
				if to == "" {
					return errors.New("say what the site is now, e.g. \"Laravel on Forge\"")
				}
				if err := config.CheckMovedTo(to); err != nil {
					return err
				}
			}
			old := ""
			if s.Moved != nil {
				old = s.Moved.To
			}
			if to == old {
				msg := s.Slug + " isn't marked moved."
				if old != "" {
					msg = s.Slug + " is already marked moved to " + old + "."
				}
				_, err := fmt.Fprintln(out, "Nothing changed: "+msg)
				return err
			}
			question := "Mark " + s.Slug + " moved to " + to + "? taw-fleet stops checking, reporting on, publishing to and updating its WordPress production."
			if to == "" {
				question = "Put " + s.Slug + " back in the fleet as a WordPress site? Its production_url is checked again."
			}
			if !yes {
				ok, err := d.confirm(out, question)
				if err != nil {
					return err
				}
				if !ok {
					_, err := fmt.Fprintln(out, "Nothing changed.")
					return err
				}
			}
			if to == "" {
				_, err = config.SetSiteList(d.Paths, s.Slug, "moved_to", nil) // "" removes the line
			} else {
				_, err = config.SetSite(d.Paths, s.Slug, "moved_to", to)
			}
			if err != nil {
				return err
			}
			note, msg := "Marked moved to "+to+": taw-fleet no longer checks or updates its WordPress production.", s.Slug+" moved to "+to
			if to == "" {
				note, msg = "No longer marked moved (was: "+old+"): back in the fleet as a WordPress site.", s.Slug+" is back in the fleet"
			}
			if err := notes.Add(d.notesFile(s.Slug), s.Slug, notes.Entry{At: time.Now(), By: notes.TawFleet, Text: note}); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "the site note wasn't written: "+err.Error())
			}
			_, err = lipgloss.Fprintln(out, p.Fg(p.OK).Render("✓")+" "+msg)
			return err
		},
	}
	cmd.Flags().BoolVar(&unmark, "clear", false, "remove the mark: a WordPress site in the fleet again")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}
