package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/review"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Choices are decisions made outside the browser (`--decisions`, an agent):
// the records to publish by key, and per conflicting value which side wins
// ("keep" production's, "take" Local's). Hashes come from the fresh review.
type Choices struct {
	Include []string                     `json:"include"`
	Sides   map[string]map[string]string `json:"sides"`
}

func newPublishCmd(d Deps, g *globals) *cobra.Command {
	var theme, decisions string
	var yes, noReview, asJSON bool
	cmd := &cobra.Command{
		Use:   "publish <site>",
		Short: "Publish the Local site's content to production, after a review (p in the dashboard)",
		Long: "Compares the Local site's content (pages, posts, TAW fields and options, menus, the media they use)\n" +
			"with production's as Local last had it (the base: saved by a pull, C, and by each publish), asks\n" +
			"production what publishing it would change, and opens the review in your browser: every\n" +
			"change by its field (Fieldset › Label), production's value beside Local's, a value production\n" +
			"changed too marked for you to decide, and a tick per record. Nothing is written until you press\n" +
			"Publish there. Files go up by their bytes; production keeps a rollback copy and undo puts it\n" +
			"back (taw-fleet publish undo). Users, comments and site settings are never published.\n\n" +
			"Production needs taw/core 1.98+, taw/hub-companion 0.5+, its key pinned (taw-fleet live trust),\n" +
			"and publishing enabled in the theme: \"extra\": {\"taw-companion\": {\"publish\": true}} in\n" +
			"composer.json, deployed.\n\n" +
			"Without a browser: --no-review prints the review; with --yes it publishes as reviewed (every\n" +
			"record but deletes; a value only production changed stays production's), refusing while a value\n" +
			"both sides changed is undecided; --decisions <file> says what to publish and which side wins:\n" +
			"{\"include\": [\"post:page:about\"], \"sides\": {\"post:page:about\": {\"fields.hero_heading\": \"take\"}}}.",
		Example: "  taw-fleet publish acme-bakery\n  taw-fleet publish acme-bakery --no-review\n  taw-fleet publish acme-bakery --no-review --json\n" +
			"  taw-fleet publish acme-bakery --no-review --decisions choices.json --yes\n  taw-fleet publish undo acme-bakery\n  taw-fleet publish history acme-bakery",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			offline := *g
			offline.offline = true
			s, t, a, err := resolveForTask(cmd, d, &offline, args[0], theme)
			if err != nil {
				return err
			}
			task, err := a.PublishTask(*s, t, d.runSiteOp)
			if err != nil {
				return fmt.Errorf("%s: %w", s.Slug, err)
			}
			sum, err := runTask(cmd, d, task, yes, "")
			if errors.Is(err, errCancelled) {
				return nil
			}
			if err != nil {
				return err
			}
			pr, ok := sum.Report.(actions.PublishReview)
			browser := ok && !noReview && decisions == "" && !yes
			if browser {
				sum.Lines = nil // the review prints them as it opens
			}
			if asJSON {
				if !ok {
					return writeJSON(cmd.OutOrStdout(), map[string]any{"nothing": sum.Headline})
				}
				if decisions == "" && !yes {
					return writeJSON(cmd.OutOrStdout(), pr.Page)
				}
			} else if err := printSummary(cmd, d, sum); err != nil {
				return err
			}
			if !ok {
				return nil
			}
			if browser {
				sum, err = runTask(cmd, d, pr.Browser, false, "")
			} else {
				dec, missing, err := choose(pr.Page, decisions)
				if err != nil {
					return err
				}
				if len(missing) > 0 {
					return fmt.Errorf("decide these first (--decisions, or the review: taw-fleet publish %s):\n  %s", s.Slug, strings.Join(missing, "\n  "))
				}
				sum, err = runTask(cmd, d, pr.Apply(dec), yes, "")
				if errors.Is(err, errCancelled) {
					return nil
				}
				if err != nil {
					return err
				}
				if asJSON {
					return writeJSON(cmd.OutOrStdout(), map[string]any{"published": sum.Headline, "lines": sum.Lines})
				}
				return printSummary(cmd, d, sum)
			}
			if err != nil {
				return err
			}
			return printSummary(cmd, d, sum)
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	cmd.Flags().BoolVar(&noReview, "no-review", false, "print the review instead of opening it in the browser")
	cmd.Flags().StringVar(&decisions, "decisions", "", "a JSON file: the records to publish and which side wins each conflict")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "publish without asking (as reviewed, or as --decisions says)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the review (or the result) as JSON")
	cmd.AddCommand(newPublishUndoCmd(d, g), newPublishHistoryCmd(d, g))
	return cmd
}

// choose turns --decisions (or, without it, the review's defaults) into
// decisions against the fresh review; what's still undecided is returned.
func choose(p review.Page, file string) (review.Decisions, []string, error) {
	if file == "" {
		d, missing := actions.DefaultDecisions(p)
		return d, missing, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return review.Decisions{}, nil, err
	}
	var c Choices
	if err := json.Unmarshal(data, &c); err != nil {
		return review.Decisions{}, nil, fmt.Errorf("--decisions: %w", err)
	}
	if len(c.Include) == 0 {
		return review.Decisions{}, nil, errors.New(`--decisions: "include" names no record`)
	}
	d, missing := p.Decide(c.Include, c.Sides)
	return d, missing, nil
}

func newPublishUndoCmd(d Deps, g *globals) *cobra.Command {
	var journal string
	var yes bool
	cmd := &cobra.Command{
		Use:   "undo <site>",
		Short: "Undo the last publish on production (x in the dashboard)",
		Long: "Asks production what undoing its newest publish (or --journal) would put back, and, after you\n" +
			"confirm, undoes it: values go back only where they still are what the publish wrote (an edit\n" +
			"made since is kept and listed), records it created are deleted unless edited since.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			offline := *g
			offline.offline = true
			s, a, err := resolveSite(cmd, d, &offline, args[0])
			if err != nil {
				return err
			}
			task, err := a.PublishUndoTask(*s, journal)
			if err != nil {
				return err
			}
			sum, err := runTask(cmd, d, task, yes, "")
			if err != nil {
				return err
			}
			if err := printSummary(cmd, d, sum); err != nil {
				return err
			}
			up, _ := sum.Report.(actions.UndoPreview)
			if up.Apply.Run == nil {
				return nil
			}
			sum, err = runTask(cmd, d, up.Apply, yes, "")
			if errors.Is(err, errCancelled) {
				return nil
			}
			if err != nil {
				return err
			}
			return printSummary(cmd, d, sum)
		},
	}
	cmd.Flags().StringVar(&journal, "journal", "", "the journal to undo (taw-fleet publish history shows them); default: the newest")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}

func newPublishHistoryCmd(d Deps, g *globals) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "history <site>",
		Short: "The site's publishes, newest first, each with its review",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			offline := *g
			offline.offline = true
			s, a, err := resolveSite(cmd, d, &offline, args[0])
			if err != nil {
				return err
			}
			h := a.PublishHistory(s.Slug)
			if asJSON {
				if h == nil {
					h = []actions.Published{}
				}
				return writeJSON(cmd.OutOrStdout(), h)
			}
			if len(h) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No publishes yet: taw-fleet publish "+s.Slug)
				return err
			}
			p := d.palette()
			var b strings.Builder
			for _, e := range h {
				what := e.State
				if e.State == "published" {
					what = fmt.Sprintf("published: %d updated, %d created", e.Updated, e.Created)
				}
				fmt.Fprintf(&b, "%s  %s\n   %s\n", e.When.Format("2006-01-02 15:04"), what, p.Fg(p.Muted).Render(e.Folder+"/review.html"))
			}
			_, err = lipgloss.Fprint(cmd.OutOrStdout(), b.String())
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// resolveSite is a site by any of its names, for what's about the site and
// not one of its themes (its production).
func resolveSite(cmd *cobra.Command, d Deps, g *globals, q string) (*site.Site, *actions.Actions, error) {
	a, err := d.actions()
	if err != nil {
		return nil, nil, err
	}
	rep, err := d.scanner(g).Run(cmd.Context())
	if err != nil {
		return nil, nil, err
	}
	s, err := scan.Resolve(rep.Sites, q)
	return s, a, err
}
