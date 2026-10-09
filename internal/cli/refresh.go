package cli

import (
	"time"

	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/scan"
)

// freshScanner is the scanner asking GitHub for the latest versions now,
// past the hour-long cache (ctrl+r, taw-fleet refresh).
func (d Deps) freshScanner(g *globals) *scan.Scanner {
	sc := d.scanner(g)
	if d.GitHub == nil && !g.offline {
		c := github.New(d.Paths.CacheDir, github.TokenSource(d.Paths, d.Runner))
		c.TTL = time.Second // any cached answer is too old
		sc.Lookups = []scan.Lookup{scan.GitHubLookup{Client: c}}
	}
	return sc
}

func newRefreshCmd(d Deps, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Fetch the state of every site again, skipping the caches",
		Long: "Everything the dashboard shows, read again for every site (the dashboard's ctrl+r):\n" +
			"the Local sites with the latest versions from GitHub now, the pull requests, CI and\n" +
			"deploys, the production sites through their companion, and the sync check of every\n" +
			"classic TAW theme against the scaffold. Writes nothing.",
		Example: "  taw-fleet refresh",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, out, p := cmd.Context(), cmd.OutOrStdout(), d.palette()
			ok := p.Fg(p.OK).Render("✓")
			line := func(s string) { _, _ = lipgloss.Fprintln(out, s) }

			rep, err := d.freshScanner(g).Run(ctx)
			if err != nil {
				return err
			}
			themes := 0
			for _, s := range rep.Sites {
				themes += len(s.TAWThemes())
			}
			line(fmt.Sprintf("%s Local: %d sites, %d TAW themes", ok, len(rep.Sites), themes))

			if !g.offline {
				d.applyRepos(cmd, g, &rep)
				var repos, prs int
				var failed []string
				for _, s := range rep.Sites {
					for _, t := range s.TAWThemes() {
						if t.GitHub == nil {
							continue
						}
						repos++
						prs += len(t.GitHub.PRs)
						if t.GitHub.Error != "" {
							failed = append(failed, t.GitHub.Repo)
						}
					}
				}
				msg := fmt.Sprintf("%s GitHub: %d repositories, %d open pull requests", ok, repos, prs)
				if len(failed) > 0 {
					msg += p.Fg(p.Warn).Render(" · no answer for " + strings.Join(failed, ", "))
				}
				line(msg)
			}

			if cfg, err := config.Load(d.Paths); err == nil && len(liveTargets(cfg)) > 0 {
				pr, err := d.prober(ctx)
				if err != nil {
					line(p.Fg(p.Err).Render("✗") + " production: " + err.Error())
				} else {
					res := pr.ProbeAll(ctx, liveTargets(cfg), true)
					var verified int
					var bad []string
					for slug, r := range res {
						if r.Reachable && r.Verified {
							verified++
						} else {
							bad = append(bad, slug)
						}
					}
					msg := fmt.Sprintf("%s Production: %d of %d sites verified", ok, verified, len(res))
					if len(bad) > 0 {
						msg += p.Fg(p.Warn).Render(" · check " + strings.Join(bad, ", "))
					}
					line(msg)
				}
			}

			a, err := d.actions()
			if err != nil {
				return err
			}
			task, err := a.SyncAllTask(rep.Sites)
			if err != nil {
				line(p.Fg(p.Muted).Render("· sync check: " + err.Error()))
				return nil
			}
			_, err = runTask(cmd, d, task, true, "")
			return err
		},
	}
}
