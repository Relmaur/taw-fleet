package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// applyRepos reads every theme's pull requests and deploys onto the report.
func (d Deps) applyRepos(cmd *cobra.Command, g *globals, rep *scan.Report) {
	github.ApplyRepos(rep.Sites, d.github(g).Repos(cmd.Context(), github.RepoNames(rep.Sites)))
}

func newPRsCmd(d Deps, g *globals) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "prs [site]",
		Short: "Open pull requests, their CI, and what each theme has deployed",
		Long: "For every TAW theme with a GitHub repository: its open pull requests with their CI\n" +
			"result, and its deploy workflow: the commit production has, a deploy running or\n" +
			"failed, and the commits on the default branch not deployed yet. Needs a GitHub\n" +
			"token (`gh auth login`).",
		Example: "  taw-fleet prs\n  taw-fleet prs chcapital\n  taw-fleet prs --json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			offline := *g
			offline.offline = true
			rep, err := d.scanner(&offline).Run(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 {
				s, err := scan.Resolve(rep.Sites, args[0])
				if err != nil {
					return err
				}
				rep.Sites = []site.Site{*s}
			}
			d.applyRepos(cmd, g, &rep)

			type entry struct {
				Site   string          `json:"site"`
				Theme  string          `json:"theme"`
				GitHub *site.RepoState `json:"github"`
			}
			var entries []entry
			for _, s := range rep.Sites {
				for _, t := range s.TAWThemes() {
					if t.GitHub != nil {
						entries = append(entries, entry{s.Slug, t.Dir, t.GitHub})
					}
				}
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"themes": entries})
			}
			p, now := d.palette(), time.Now()
			var b strings.Builder
			for _, e := range entries {
				b.WriteString(lipgloss.NewStyle().Bold(true).Render(e.Site) + p.Fg(p.Muted).Render(" / ") + e.Theme +
					"  " + p.Fg(p.Muted).Render(e.GitHub.Repo) + "\n")
				for _, l := range render.GitHubLines(p, e.GitHub, now) {
					indent := "  "
					if l.Key == "pending" {
						indent = "      "
					}
					b.WriteString(indent + l.Text + "\n")
				}
				b.WriteString("\n")
			}
			if len(entries) == 0 {
				b.WriteString("No TAW theme with a GitHub repository.\n")
			}
			_, err = lipgloss.Fprint(cmd.OutOrStdout(), b.String())
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func newMergeCmd(d Deps, g *globals) *cobra.Command {
	var theme string
	var number int
	var yes bool
	cmd := &cobra.Command{
		Use:   "merge <site>",
		Short: "Merge a theme's pull request (which deploys production)",
		Long: "Merge an open pull request of the theme with the repository's default merge method,\n" +
			"delete its branch, and pull the default branch into the local theme when its tree is\n" +
			"clean. Refused for a draft, a conflict, or CI that failed or is still running.\n" +
			"Merging a client theme deploys its production site. The dashboard's key: M.",
		Example: "  taw-fleet merge ls-mxico\n  taw-fleet merge ls-mxico --pr 12 --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			offline := *g
			offline.offline = true
			s, t, a, err := resolveForTask(cmd, d, &offline, args[0], theme)
			if err != nil {
				return err
			}
			rep := scan.Report{Sites: []site.Site{{Slug: s.Slug, Themes: []site.Theme{t}}}}
			d.applyRepos(cmd, g, &rep)
			t = rep.Sites[0].Themes[0]
			st := t.GitHub
			switch {
			case st == nil:
				return fmt.Errorf("%s has no GitHub repository", t.Dir)
			case st.Error != "":
				return fmt.Errorf("%s: %s", t.Dir, st.Error)
			case len(st.PRs) == 0:
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s has no open pull request.\n", t.Dir)
				return err
			}
			var pr *site.PullRequest
			for i := range st.PRs {
				if st.PRs[i].Number == number || (number == 0 && len(st.PRs) == 1) {
					pr = &st.PRs[i]
				}
			}
			if pr == nil {
				var list []string
				for _, p := range st.PRs {
					list = append(list, fmt.Sprintf("  #%d %s", p.Number, p.Title))
				}
				return fmt.Errorf("%s has %d open pull requests; pick one with --pr:\n%s", t.Dir, len(st.PRs), strings.Join(list, "\n"))
			}
			task, err := a.MergeTask(*s, t, *pr)
			if err != nil {
				return fmt.Errorf("%s: %w", t.Dir, err)
			}
			sum, err := runTask(cmd, d, task, yes, "")
			if errors.Is(err, errCancelled) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := printSummary(cmd, d, sum); err != nil {
				return err
			}
			if res, ok := sum.Report.(actions.MergeResult); ok && res.Deploys {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Follow the deploy: taw-fleet prs %s (or the dashboard)\n", s.Slug)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	cmd.Flags().IntVar(&number, "pr", 0, "the pull request number, when there are several")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}
