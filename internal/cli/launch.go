package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/bugsmash"
	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/launch"
	"github.com/Relmaur/taw-fleet/internal/live"
	"github.com/Relmaur/taw-fleet/internal/pagespeed"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/setup"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/wpremote"
)

// siteDir is a site's folder in the fleet folder: its brief, pictures and
// reports from the new-site flow, and its launch report.
func (d Deps) siteDir(slug string) string { return filepath.Join(d.Paths.FleetDir, "sites", slug) }

// makeLaunch gathers a site's launch report and saves it.
func (d Deps) makeLaunch(ctx context.Context, g *globals, q, theme string, speed bool) (launch.Data, string, error) {
	// Each source has its own timeout; this bounds them all, whatever answers slowly.
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	cfg, err := config.Load(d.Paths)
	if err != nil {
		return launch.Data{}, "", err
	}
	rep, err := d.scanner(g).Run(ctx)
	if err != nil {
		return launch.Data{}, "", err
	}
	s, _, err := scan.ResolveTheme(rep.Sites, q, theme)
	if err != nil {
		return launch.Data{}, "", err
	}
	if !g.offline { // its deploy, from GitHub
		github.ApplyRepos(rep.Sites, d.github(g).Repos(ctx, github.RepoNames([]site.Site{*s})))
	}
	s, t, err := scan.ResolveTheme(rep.Sites, q, theme)
	if err != nil {
		return launch.Data{}, "", err
	}
	sc := cfg.Site(s.Slug)
	tg := launch.Target{Site: *s, Theme: t, Production: sc.ProductionURL, Project: sc.BugSmashProject, Dir: d.siteDir(s.Slug)}
	src := launch.Sources{HTTP: d.WebHTTP}
	if tg.Production != "" {
		// The companion, asked now: the SETUP checklist reads it too.
		if pr, err := d.prober(ctx); err == nil {
			p := pr.Probe(ctx, live.Target{Slug: s.Slug, URL: tg.Production})
			s.Production = &p
			src.Live = func(context.Context) (*site.Production, error) { return &p, nil }
		} else {
			src.Live = func(context.Context) (*site.Production, error) { return nil, err }
		}
		if speed {
			src.Speed = d.PageSpeed
			if src.Speed == nil {
				src.Speed = func(ctx context.Context, url, strategy string) (pagespeed.Result, error) {
					key, _ := d.pagespeedKey(ctx)
					return pagespeed.Client{Key: key}.Run(ctx, url, strategy)
				}
			}
		}
		if tg.Project != "" {
			if pr, err := d.feedbackProber(ctx); err == nil {
				tg.ReviewURL = pr.Check(ctx, bugsmash.Target{Slug: s.Slug, Project: tg.Project}).URL
			}
		}
		if a, err := d.agent(); err == nil {
			tg.Watching = a.Status(ctx).Loaded
		}
	}
	in := setup.Input{Site: *s, Theme: t, Config: sc}
	if sc.ProductionURL != "" {
		in.Bot = wpremote.Store{Exec: d.Runner}.Has(ctx, s.Slug)
	}
	tg.Setup = setup.Steps(in)
	data := launch.Gather(ctx, src, tg)
	page, err := launch.Save(&data)
	return data, page, err
}

// launchTask is I in the dashboard once the site is live: make its launch
// report, then open it.
func (d Deps) launchTask(g *globals, s site.Site, theme string) actions.Task {
	return actions.Task{Title: "Launch report: " + s.Slug, Quiet: true, Target: s.ID,
		Run: func(ctx context.Context, out io.Writer) (actions.Summary, error) {
			_, _ = fmt.Fprintln(out, "Checking "+s.Slug+" in production (PageSpeed takes up to a minute)…")
			data, page, err := d.makeLaunch(ctx, g, s.Slug, theme, true)
			if err != nil {
				return actions.Summary{}, err
			}
			if a, err := d.actions(); err == nil {
				_, _ = a.OpenURL(ctx, "file://"+page)
			}
			lines := []string{page}
			for _, l := range data.Left {
				lines = append(lines, "left: "+l)
			}
			return actions.Summary{Headline: data.Name + "'s launch report is open in the browser: " + launchSummary(data), Lines: lines}, nil
		}}
}

func launchSummary(d launch.Data) string {
	s := fmt.Sprintf("%d of %d checks pass", d.Count(launch.OK), len(d.Checks)-d.Count(launch.Skip))
	if n := len(d.Left); n > 0 {
		s += fmt.Sprintf(", %d %s left for you", n, plural(n, "thing", "things"))
	}
	return s
}

func newLaunchReportCmd(d Deps, g *globals) *cobra.Command {
	var theme string
	var asJSON, noSpeed, open bool
	cmd := &cobra.Command{
		Use:   "launch-report <site>",
		Short: "Check a site in production once it's live, and write its launch report",
		Long: "The go-live review (stage 6 of a new site; I in the dashboard once the site is live):\n" +
			"  - production answers over HTTPS, http:// moves to https://, the certificate;\n" +
			"  - search engines are let in (no noindex: WordPress's \"Discourage search engines\");\n" +
			"  - the last deploy, and its companion: verified, the same taw/core, plugins, security;\n" +
			"  - PageSpeed, mobile and desktop, with its pictures of production;\n" +
			"  - taw-fleet watch, BugSmash, and the rest of the SETUP checklist.\n" +
			"Saved as ~/TAW Fleet/sites/<site>/launch.{html,md}, the pictures in production/ beside\n" +
			"the Local build's in build/ (the new-site flow makes those). Production is only read.",
		Example: "  taw-fleet launch-report acme-bakery --open\n  taw-fleet launch-report acme-bakery --json --no-speed",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !asJSON && !noSpeed {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Checking "+args[0]+" in production (PageSpeed takes up to a minute)…")
			}
			data, page, err := d.makeLaunch(cmd.Context(), g, args[0], theme, !noSpeed)
			if err != nil {
				return err
			}
			if open {
				if a, err := d.actions(); err == nil {
					_, _ = a.OpenURL(cmd.Context(), "file://"+page)
				}
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), struct {
					launch.Data
					Report string `json:"report"`
				}{data, page})
			}
			return printLaunch(cmd.OutOrStdout(), d, data, page)
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the report's data as JSON")
	cmd.Flags().BoolVar(&noSpeed, "no-speed", false, "skip PageSpeed (it takes up to a minute)")
	cmd.Flags().BoolVar(&open, "open", false, "open the report in the browser")
	return cmd
}

func printLaunch(w io.Writer, d Deps, r launch.Data, page string) error {
	p := d.palette()
	marks := map[launch.Status]string{launch.OK: p.Fg(p.OK).Render("✓"), launch.Warn: p.Fg(p.Warn).Render("▲"),
		launch.Fail: p.Fg(p.Err).Render("✗"), launch.Skip: p.Fg(p.Muted).Render("·")}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(r.Name+" in production") + "  " + p.Fg(p.Muted).Render(r.Production+" · "+launchSummary(r)) + "\n")
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "  %s %-15s %s\n", marks[c.Status], c.Name, p.Fg(p.Muted).Render(c.Said))
	}
	if len(r.Left) > 0 {
		b.WriteString("\n" + lipgloss.NewStyle().Bold(true).Render("Left for you") + "\n")
		for i, l := range r.Left {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, l)
		}
	}
	b.WriteString("\n  " + p.Fg(p.Muted).Render(page+" (and launch.md)") + "\n")
	_, err := lipgloss.Fprint(w, b.String())
	return err
}

// newCommentsLinkCmd links a site to its BugSmash project.
func newCommentsLinkCmd(d Deps, g *globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "link <site> <project id>",
		Short: "Link a site to its BugSmash project (bugsmash_project in the settings)",
		Long: "Checks the project with BugSmash, then sets bugsmash_project under [sites.<site>] in\n" +
			"the settings, so the FB column, F, X, watch and the reports read its comments. The ids:\n" +
			"taw-fleet comments projects. Asks first.",
		Example: "  taw-fleet comments link acme-bakery a15552c8-a3d9-4bce-9a06-7859fbb7f27c",
		Args:    cobra.ExactArgs(2),
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
			id := strings.TrimSpace(args[1])
			pr, err := d.feedbackProber(cmd.Context())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
			defer cancel()
			pj, err := pr.Client.Project(ctx, id)
			if err != nil {
				return fmt.Errorf("BugSmash doesn't know project %s: %w (taw-fleet comments projects lists them)", id, err)
			}
			out := cmd.OutOrStdout()
			if !yes {
				ok, err := d.confirm(out, "Link BugSmash project "+pj.Name+" ("+id+") to "+s.Slug+"? It sets bugsmash_project in the settings.")
				if err != nil {
					return err
				}
				if !ok {
					_, err := fmt.Fprintln(out, "Nothing changed.")
					return err
				}
			}
			f, err := config.SetSite(d.Paths, s.Slug, "bugsmash_project", id)
			if err != nil {
				return err
			}
			p := d.palette()
			_, err = lipgloss.Fprintln(out, p.Fg(p.OK).Render("✓")+" "+s.Slug+" reads its comments from "+pj.Name+" ("+f+")")
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}
