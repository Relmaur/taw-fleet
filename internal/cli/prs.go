package cli

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

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
