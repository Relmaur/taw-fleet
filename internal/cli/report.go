package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/keychain"
	"github.com/Relmaur/taw-fleet/internal/live"
	"github.com/Relmaur/taw-fleet/internal/pagespeed"
	"github.com/Relmaur/taw-fleet/internal/report"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/watch"
)

// reportSources are where a report's numbers come from: GitHub, BugSmash,
// the companion, watch's history and PageSpeed. speed false skips PageSpeed.
func (d Deps) reportSources(g *globals, speed bool) report.Sources {
	src := report.Sources{}
	gh := d.github(g)
	src.GitHub = func(ctx context.Context, repo string, from, to time.Time) (github.Month, error) {
		return gh.Month(ctx, repo, from, to)
	}
	src.Comments = func(ctx context.Context, project string, from, to time.Time) (int, int, error) {
		pr, err := d.feedbackProber(ctx)
		if err != nil {
			return 0, 0, err
		}
		return pr.Client.MonthComments(ctx, project, from, to)
	}
	src.Uptime = func(slug string, from, to time.Time) (float64, int, time.Time) {
		st, err := watch.Load(d.Paths)
		if err != nil {
			return 0, 0, time.Time{}
		}
		return st.UptimeSince(slug, from, to)
	}
	if speed && d.PageSpeed != nil {
		src.Speed = d.PageSpeed
	} else if speed {
		src.Speed = func(ctx context.Context, url, strategy string) (pagespeed.Result, error) {
			key, _ := d.pagespeedKey(ctx)
			return pagespeed.Client{Key: key}.Run(ctx, url, strategy)
		}
	}
	return src
}

// reportTarget is the site with what the config says about it.
func reportTarget(cfg config.Config, s site.Site) report.Target {
	t := report.Target{Site: s, Production: cfg.Site(s.Slug).ProductionURL, Project: cfg.Site(s.Slug).BugSmashProject,
		PreparedBy: cfg.ReportFrom, Lang: cfg.ReportLang(s.Slug)}
	t.Repos = github.RepoNames([]site.Site{s}) // the repositories the PR and DEPLOY columns read
	return t
}

// makeReport gathers one site's month (and saves it unless dryRun).
func (d Deps) makeReport(ctx context.Context, g *globals, cfg config.Config, s site.Site, month, lang string, speed, dryRun bool, now time.Time) (report.Data, string, error) {
	from, to, err := report.MonthOf(month, now)
	if err != nil {
		return report.Data{}, "", err
	}
	src := d.reportSources(g, speed)
	src.Now = func() time.Time { return now }
	t := reportTarget(cfg, s)
	if lang != "" {
		t.Lang = lang
	}
	if t.Production != "" {
		src.Live = func(ctx context.Context) (*site.Production, error) {
			pr, err := d.prober(ctx)
			if err != nil {
				return nil, err
			}
			p := pr.Probe(ctx, live.Target{Slug: s.Slug, URL: t.Production})
			return &p, nil
		}
	}
	data := report.Gather(ctx, src, t, from, to)
	if dryRun {
		return data, "", nil
	}
	_, html, err := report.Save(d.reportDir(), data)
	return data, html, err
}

// pagespeedKey is the PageSpeed Insights API key: the Keychain's, else
// $PAGESPEED_API_KEY, else "" (the API's small shared quota). It says where
// it came from.
func (d Deps) pagespeedKey(ctx context.Context) (string, string) {
	if k, err := (keychain.Keychain{Exec: d.Runner}).Load(ctx, pagespeed.KeyItem); err == nil && k != "" {
		return k, "the Keychain"
	}
	if d.Paths.Getenv != nil {
		if k := d.Paths.Getenv("PAGESPEED_API_KEY"); k != "" {
			return k, "$PAGESPEED_API_KEY"
		}
	}
	return "", ""
}

func newReportKeyCmd(d Deps) *cobra.Command {
	key := &cobra.Command{Use: "key", Short: "The PageSpeed Insights API key for the speed section (kept in the macOS Keychain)"}
	key.AddCommand(&cobra.Command{
		Use:   "import",
		Short: "Store a Google API key (from stdin) for PageSpeed Insights in the Keychain",
		Long: "Without a key, PageSpeed Insights shares a small quota with everyone and often refuses.\n" +
			"A free key: Google Cloud console → APIs & Services → enable the PageSpeed Insights API →\n" +
			"Credentials → Create credentials → API key (restrict it to that API). It's kept in the\n" +
			"login Keychain, never in a file or shown again.",
		Example: "  pbpaste | taw-fleet report key import\n  taw-fleet report key import   # asks, without echo",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			secret, err := readSecret(d, cmd.OutOrStdout(), "PageSpeed Insights API key (not shown): ")
			if err != nil {
				return err
			}
			if err := (keychain.Keychain{Exec: d.Runner}).Save(cmd.Context(), pagespeed.KeyItem, secret); err != nil {
				return err
			}
			p := d.palette()
			_, err = lipgloss.Fprintln(cmd.OutOrStdout(), p.Fg(p.OK).Render("✓")+" Stored the PageSpeed Insights key in the Keychain.")
			return err
		},
	}, &cobra.Command{
		Use:   "show",
		Short: "Say where the key comes from (never the key)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, from := d.pagespeedKey(cmd.Context())
			msg := "key from   " + from
			if from == "" {
				msg = "No key: PageSpeed uses its small shared quota (taw-fleet report key import)."
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), msg)
			return err
		},
	})
	return key
}

// reportTask is m in the dashboard: make a site's report for a month, then
// open it in the browser.
func (d Deps) reportTask(g *globals, s site.Site, month string) actions.Task {
	return actions.Task{Title: "Report: " + s.Slug + " " + month, Quiet: true, Target: s.ID,
		Run: func(ctx context.Context, out io.Writer) (actions.Summary, error) {
			_, _ = fmt.Fprintln(out, "Gathering "+month+" for "+s.Slug+" (PageSpeed takes up to a minute)…")
			cfg, err := config.Load(d.Paths)
			if err != nil {
				return actions.Summary{}, err
			}
			data, html, err := d.makeReport(ctx, g, cfg, s, month, "", true, false, time.Now())
			if err != nil {
				return actions.Summary{}, err
			}
			if a, err := d.actions(); err == nil {
				_, _ = a.OpenURL(ctx, "file://"+html)
			}
			lines := []string{html + " (and .md)"}
			for _, n := range []struct{ k, v string }{{"availability", data.UptimeNote}, {"changes", data.ChangesNote}, {"comments", data.CommentNote}, {"speed", data.SpeedNote}, {"security", data.HealthNote}} {
				if n.v != "" {
					lines = append(lines, "not in it: "+n.k+": "+n.v)
				}
			}
			return actions.Summary{Headline: data.Name + "'s " + month + " report is open in the browser", Lines: lines}, nil
		}}
}

func (d Deps) reportDir() string { return d.Paths.FleetDir + "/reports" }

func newReportCmd(d Deps, g *globals) *cobra.Command {
	var month, lang string
	var asJSON, printMD, noSpeed, open, all bool
	cmd := &cobra.Command{
		Use:   "report <site> | --all",
		Short: "A site's monthly report for its client: uptime, changes, comments, speed, security",
		Long: "Makes a site's monthly report, for you to read and send (nothing is sent):\n" +
			"  - availability, from taw-fleet watch's checks every 5 minutes;\n" +
			"  - updates and changes: pull requests merged, and how often the live site was published;\n" +
			"  - the client's review comments in BugSmash: received, resolved, still open;\n" +
			"  - speed: Google PageSpeed Insights, mobile and desktop, measured as the report is made;\n" +
			"  - security and maintenance: the live site's versions, plugins and known vulnerabilities.\n" +
			"It's saved as Markdown and HTML in ~/TAW Fleet/reports/<site>/<month>.{md,html}. Without\n" +
			"--month it's this month so far; watch makes last month's for every production site on the\n" +
			"1st. report_language (\"en\", \"es\") and report_from in the settings.",
		Example: "  taw-fleet report ls-mxico\n  taw-fleet report ls-mxico --month 2026-09 --open\n  taw-fleet report --all --month 2026-09\n  taw-fleet report ls-mxico --print",
		Args: func(cmd *cobra.Command, args []string) error {
			if all {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(d.Paths)
			if err != nil {
				return err
			}
			if lang != "" && lang != "en" && lang != "es" {
				return errors.New("--language: en or es")
			}
			offline := *g
			offline.offline = true
			rep, err := d.scanner(&offline).Run(cmd.Context())
			if err != nil {
				return err
			}
			var sites []site.Site
			if all {
				for _, s := range rep.Sites {
					if cfg.Site(s.Slug).ProductionURL != "" {
						sites = append(sites, s)
					}
				}
				if len(sites) == 0 {
					return errors.New("no production sites: reports are for sites with production_url in the settings")
				}
			} else {
				s, err := scan.Resolve(rep.Sites, args[0])
				if err != nil {
					return err
				}
				sites = []site.Site{*s}
			}
			var made []report.Data
			for _, s := range sites {
				data, html, err := d.makeReport(cmd.Context(), g, cfg, s, month, lang, !noSpeed, printMD, time.Now())
				if err != nil {
					return fmt.Errorf("%s: %w", s.Slug, err)
				}
				made = append(made, data)
				switch {
				case asJSON:
				case printMD:
					_, _ = fmt.Fprint(cmd.OutOrStdout(), report.Markdown(data))
				default:
					printReport(cmd, d, data, html)
				}
				if open && html != "" {
					if a, err := d.actions(); err == nil {
						_, _ = a.OpenURL(cmd.Context(), "file://"+html)
					}
				}
			}
			if asJSON {
				if len(made) == 1 {
					return writeJSON(cmd.OutOrStdout(), made[0])
				}
				return writeJSON(cmd.OutOrStdout(), map[string]any{"reports": made})
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&month, "month", "", "the month, e.g. 2026-09 (default: this month so far)")
	cmd.Flags().BoolVar(&all, "all", false, "every site with a production URL")
	cmd.Flags().StringVar(&lang, "language", "", "en or es (default: report_language in the settings)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the report's data as JSON")
	cmd.Flags().BoolVar(&printMD, "print", false, "print the Markdown instead of saving")
	cmd.Flags().BoolVar(&noSpeed, "no-speed", false, "skip PageSpeed (it takes up to a minute)")
	cmd.Flags().BoolVar(&open, "open", false, "open the HTML in the browser")
	cmd.AddCommand(newReportKeyCmd(d), &cobra.Command{
		Use:   "list [site]",
		Short: "The saved reports",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var slugs []string
			if len(args) == 1 {
				slugs = []string{args[0]}
			} else if entries, err := listDirs(d.reportDir()); err == nil {
				slugs = entries
			}
			n := 0
			for _, slug := range slugs {
				for _, s := range report.List(d.reportDir(), slug) {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s\n", slug, s.Month, s.HTML)
					n++
				}
			}
			if n == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No reports yet: taw-fleet report <site>")
			}
			return nil
		},
	})
	return cmd
}

// printReport is a made report in the terminal: where it is, the numbers,
// and what couldn't be measured and why (the report itself only says
// "not available").
func printReport(cmd *cobra.Command, d Deps, r report.Data, html string) {
	p := d.palette()
	var b strings.Builder
	title := r.Name + " · " + r.Month
	if r.SoFar {
		title += " (so far)"
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(title) + "\n")
	if html != "" {
		b.WriteString("  " + p.Fg(p.Muted).Render(html+" (and .md)") + "\n")
	}
	line := func(k, v string) { b.WriteString("  " + p.Fg(p.Muted).Render(fmt.Sprintf("%-10s", k)) + v + "\n") }
	if r.Uptime != nil {
		line("online", fmt.Sprintf("%.2f%% of %d checks", r.Uptime.Share*100, r.Uptime.Checks))
	}
	line("changes", fmt.Sprintf("%d framework updates, %d maintenance, %d site changes, %d deploys", len(r.Updates), len(r.Maintenance), len(r.Changes), len(r.Deploys)))
	if r.Comments != nil {
		line("comments", fmt.Sprintf("%d received, %d resolved", r.Comments.Received, r.Comments.Resolved))
	}
	for _, s := range r.Speed {
		line(s.Strategy, fmt.Sprintf("performance %d · accessibility %d · best practices %d · SEO %d", s.Performance, s.Accessibility, s.BestPractices, s.SEO))
	}
	if h := r.Health; h != nil {
		line("live", fmt.Sprintf("WordPress %s · PHP %s · taw/core %s · %d plugins, %d updates waiting · %d vulnerabilities", h.WP, h.PHP, h.Core, h.Plugins, len(h.Updates), h.Vulns))
	}
	notes := map[string]string{"availability": r.UptimeNote, "changes": r.ChangesNote, "comments": r.CommentNote, "speed": r.SpeedNote, "security": r.HealthNote}
	var keys []string
	for k, v := range notes {
		if v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString("  " + p.Fg(p.Warn).Render("not in it: ") + k + ": " + notes[k] + "\n")
	}
	_, _ = lipgloss.Fprint(cmd.OutOrStdout(), b.String()+"\n")
}

func listDirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
