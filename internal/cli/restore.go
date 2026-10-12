package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/restore"
	"github.com/Relmaur/taw-fleet/internal/scan"
)

func newRestoreCmd(d Deps, g *globals) *cobra.Command {
	var theme string
	var yes, save, asJSON bool
	cmd := &cobra.Command{
		Use:   "restore <site> [point]",
		Short: "A site's restore points, and putting one back (the database and the theme's branch)",
		Long: "Before an update (u, U), a merge (M) and a content import (C), taw-fleet saves a restore\n" +
			"point: the Local site's database (when it runs) and the theme's branch and commit. It\n" +
			"keeps the newest 5 per site (restore_keep in the settings), in\n" +
			"~/Library/Application Support/taw-fleet/restore/: never in a site, a theme or a git\n" +
			"repository, private to you, compressed.\n\n" +
			"With the site alone it lists them. With a point (its id, or latest) it puts it back after\n" +
			"asking: the theme onto the point's branch (commits made since stay) and the database from\n" +
			"its copy. How things are just before is saved as a point first, so a restore can be\n" +
			"undone too. --save makes one now.",
		Example: "  taw-fleet restore ls-mxico\n  taw-fleet restore ls-mxico latest\n  taw-fleet restore ls-mxico 20261012-183000-update --yes\n  taw-fleet restore ls-mxico --save",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			offline := *g
			offline.offline = true
			a, err := d.actions()
			if err != nil {
				return err
			}
			rep, err := d.scanner(&offline).Run(cmd.Context())
			if err != nil {
				return err
			}
			s, err := scan.Resolve(rep.Sites, args[0])
			if err != nil {
				return err
			}
			st := a.Restores()
			out := cmd.OutOrStdout()
			switch {
			case save:
				if len(args) == 2 {
					return errors.New("--save makes a new point: name no point")
				}
				_, t, err := scan.ResolveTheme(rep.Sites, args[0], theme)
				if err != nil {
					return err
				}
				if st.Keep <= 0 {
					return errors.New("restore points are off (restore_keep = 0 in the settings)")
				}
				p, err := st.Save(cmd.Context(), *s, t, restore.ByHand, "")
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(out, "Saved %s: %s\n", p.ID, actions.PointContents(p))
				return err
			case len(args) == 1:
				points, err := st.List(s.Slug)
				if err != nil {
					return err
				}
				if asJSON {
					if points == nil {
						points = []restore.Point{}
					}
					return writeJSON(out, map[string]any{"site": s.Slug, "keep": st.Keep, "folder": st.Root(), "points": points})
				}
				return printPoints(cmd, d, s.Slug, points, st.Keep)
			}
			p, err := st.Find(s.Slug, args[1])
			if err != nil {
				return err
			}
			_, t, err := scan.ResolveTheme(rep.Sites, args[0], p.Theme)
			if err != nil {
				return fmt.Errorf("%s was saved for the theme %s: %w", p.ID, p.Theme, err)
			}
			task, err := a.RestoreTask(cmd.Context(), *s, t, p)
			if err != nil {
				return err
			}
			sum, err := runTask(cmd, d, task, yes, task.Ask)
			if errors.Is(err, errCancelled) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := printSummary(cmd, d, sum); err != nil {
				return err
			}
			if sum.Failed {
				return exitCode(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "with --save: which TAW theme, when the site has several")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	cmd.Flags().BoolVar(&save, "save", false, "save a restore point now")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the list as JSON")
	return cmd
}

func printPoints(cmd *cobra.Command, d Deps, slug string, points []restore.Point, keep int) error {
	p := d.palette()
	var b strings.Builder
	if keep <= 0 {
		b.WriteString(p.Fg(p.Warn).Render("Restore points are off") + " (restore_keep = 0 in the settings).\n")
	}
	if len(points) == 0 {
		b.WriteString(slug + " has no restore points yet. One is saved before each update, merge and content import.\n")
		_, err := lipgloss.Fprint(cmd.OutOrStdout(), b.String())
		return err
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(slug) + p.Fg(p.Muted).Render(fmt.Sprintf("  ·  %d restore %s, the newest %d kept", len(points), plural(len(points), "point", "points"), keep)) + "\n\n")
	now := time.Now()
	for i, pt := range points {
		id := pt.ID
		if i == 0 {
			id += p.Fg(p.Muted).Render(" (latest)")
		}
		b.WriteString("  " + id + "\n")
		b.WriteString("    " + pt.Label() + p.Fg(p.Muted).Render("  ·  "+agoWords(now.Sub(pt.Created))) + "\n")
		b.WriteString("    " + p.Fg(p.Muted).Render(actions.PointContents(pt)) + "\n")
	}
	b.WriteString("\n" + p.Fg(p.Muted).Render("Put one back: taw-fleet restore "+slug+" <point>   (asks first)") + "\n")
	_, err := lipgloss.Fprint(cmd.OutOrStdout(), b.String())
	return err
}

func agoWords(dur time.Duration) string {
	if dur < time.Minute {
		return "just now"
	}
	return words(dur) + " ago"
}
