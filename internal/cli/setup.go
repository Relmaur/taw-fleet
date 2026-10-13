package cli

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/setup"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/wpremote"
)

// readyTheme is one theme's checklist, for --json.
type readyTheme struct {
	Site  string       `json:"site"`
	Theme string       `json:"theme"`
	Steps []setup.Step `json:"steps"`
}

func newReadyCmd(d Deps, g *globals) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ready [site]",
		Short: "What a site has and still needs: repository, secrets check, deploy, production, companion, bot, BugSmash, skills",
		Long: "The SETUP checklist from the dashboard's details (I there does the next step): for one\n" +
			"site, or every client site. Each step says what's there, or how to do it.",
		Example: "  taw-fleet ready acme-bakery\n  taw-fleet ready --json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rep, err := d.scanner(g).Run(cmd.Context())
			if err != nil {
				return err
			}
			if !g.offline {
				d.applyLive(cmd.Context(), &rep, false)
				d.applyRepos(cmd, g, &rep)
			}
			cfg, err := config.Load(d.Paths)
			if err != nil {
				return err
			}
			sites := rep.Sites
			if len(args) == 1 {
				s, err := scan.Resolve(rep.Sites, args[0])
				if err != nil {
					return err
				}
				sites = []site.Site{*s}
			}
			bots := wpremote.Store{Exec: d.Runner}
			var out []readyTheme
			for _, s := range sites {
				for _, t := range s.TAWThemes() {
					if handoff.IsUmbrella(t) { // the TAW ecosystem's own themes aren't client sites
						continue
					}
					in := setup.Input{Site: s, Theme: t, Config: cfg.Site(s.Slug)}
					if in.Config.ProductionURL != "" {
						in.Bot = bots.Has(cmd.Context(), s.Slug)
					}
					out = append(out, readyTheme{Site: s.Slug, Theme: t.Dir, Steps: setup.Steps(in)})
				}
			}
			if len(out) == 0 && len(args) == 1 {
				return errors.New(args[0] + " has no client TAW theme")
			}
			if asJSON {
				if out == nil {
					out = []readyTheme{}
				}
				return writeJSON(cmd.OutOrStdout(), out)
			}
			p := d.palette()
			var b strings.Builder
			for i, r := range out {
				if i > 0 {
					b.WriteString("\n")
				}
				done, all := setup.Count(r.Steps)
				fmt.Fprintf(&b, "%s · %s  %s\n", p.Fg(p.Accent).Render(r.Site), r.Theme, p.Fg(p.Muted).Render(fmt.Sprintf("%d of %d set up", done, all)))
				for _, st := range r.Steps {
					mark := p.Fg(p.OK).Render("✓")
					switch {
					case st.Later:
						mark = p.Fg(p.Muted).Render("·")
					case !st.Done:
						mark = p.Fg(p.Warn).Render("○")
					}
					fmt.Fprintf(&b, "  %s %-14s %s\n", mark, st.Name, p.Fg(p.Muted).Render(st.State))
					if !st.Done && !st.Later && st.Fix != "" {
						fmt.Fprintf(&b, "                   %s\n", p.Fg(p.Accent).Render(st.Fix))
					}
				}
			}
			_, err = lipgloss.Fprint(cmd.OutOrStdout(), b.String())
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func newRepoCmd(d Deps, g *globals) *cobra.Command {
	var theme, owner, name string
	var yes bool
	cmd := &cobra.Command{
		Use:   "repo <site>",
		Short: "Put a theme on GitHub: a private repository, its branches pushed, the dump check required (I in the dashboard)",
		Long: "For a theme with no GitHub repository yet (a new site): creates a private repository\n" +
			"(gh repo create; owner: --owner, else the settings' first github_owners, else your gh\n" +
			"account; name: --name, else <theme folder>--theme), adds it as origin, pushes the default\n" +
			"branch and the current one, and requires the dump check (" + "ci / committed-files" + ") on the default\n" +
			"branch, as the fleet's other repositories do. Asks first; needs gh (gh auth login).",
		Example: "  taw-fleet repo acme-bakery\n  taw-fleet repo acme-bakery --owner Relmaur --name acme-bakery--theme --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, t, a, err := resolveForTask(cmd, d, g, args[0], theme)
			if err != nil {
				return err
			}
			if owner == "" {
				owner = a.RepoOwner()
			}
			task, err := a.RepoTask(*s, t, owner, name)
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
			return printSummary(cmd, d, sum)
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	cmd.Flags().StringVar(&owner, "owner", "", "the GitHub account or organization (default: github_owners' first, else gh's account)")
	cmd.Flags().StringVar(&name, "name", "", "the repository's name (default: <theme folder>--theme)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}
