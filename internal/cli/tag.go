package cli

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/scan"
)

// newTagCmd shows or sets a site's tags (tags under [sites.<site>]).
func newTagCmd(d Deps, g *globals) *cobra.Command {
	var add, remove []string
	var yes, clearAll bool
	cmd := &cobra.Command{
		Use:   "tag <site> [tag…]",
		Short: "Show or set a site's tags (# in the dashboard)",
		Long: "Tags organise the fleet: they show on the site's card, the filter finds them (tag:retainer,\n" +
			"or #retainer) and the dashboard groups by them (=). With only the site, prints its tags.\n" +
			"With tags, sets them, replacing the old ones; --add and --remove change a few, and --clear\n" +
			"removes them all. A tag is lower case letters, digits, \":\", \"_\" or \"-\"; client:parallel\n" +
			"lets the dashboard group by client. Asks first.",
		Example: "  taw-fleet tag ls-mxico\n  taw-fleet tag ls-mxico retainer es client:ls\n  taw-fleet tag ls-mxico --add on-hold --remove retainer",
		Args:    cobra.MinimumNArgs(1),
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
			old := s.Tags
			if len(args) == 1 && len(add) == 0 && len(remove) == 0 && !clearAll {
				if len(old) == 0 {
					_, err := fmt.Fprintln(out, s.Slug+" has no tags. taw-fleet tag "+s.Slug+" <tag…> sets them.")
					return err
				}
				_, err := fmt.Fprintln(out, strings.Join(old, " "))
				return err
			}
			tags := slices.Clone(old)
			switch {
			case clearAll && len(args) > 1:
				return fmt.Errorf("--clear removes every tag: don't name tags too")
			case clearAll:
				tags = nil
			case len(args) > 1:
				tags = args[1:]
			}
			tags = append(tags, add...)
			tags = slices.DeleteFunc(tags, func(t string) bool { return slices.Contains(remove, t) })
			tags, err = config.CleanTags(tags)
			if err != nil {
				return err
			}
			if slices.Equal(tags, old) {
				_, err := fmt.Fprintln(out, "Nothing changed: "+s.Slug+" already has those tags.")
				return err
			}
			what := "remove all of " + s.Slug + "'s tags"
			if len(tags) > 0 {
				what = "tag " + s.Slug + " " + strings.Join(tags, " ")
			}
			if !yes {
				ok, err := d.confirm(out, strings.ToUpper(what[:1])+what[1:]+"? It sets tags in the settings.")
				if err != nil {
					return err
				}
				if !ok {
					_, err := fmt.Fprintln(out, "Nothing changed.")
					return err
				}
			}
			if _, err := config.SetSiteList(d.Paths, s.Slug, "tags", tags); err != nil {
				return err
			}
			msg := s.Slug + " has no tags now"
			if len(tags) > 0 {
				msg = s.Slug + ": " + strings.Join(tags, " ")
			}
			_, err = lipgloss.Fprintln(out, p.Fg(p.OK).Render("✓")+" "+msg)
			return err
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "add these tags")
	cmd.Flags().StringSliceVar(&remove, "remove", nil, "remove these tags")
	cmd.Flags().BoolVar(&clearAll, "clear", false, "remove every tag")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}
