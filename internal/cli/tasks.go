package cli

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/taw"
)

// runTask runs a task with its progress on stderr (dimmed) and prints the
// summary. writes tasks ask first unless yes.
func runTask(cmd *cobra.Command, d Deps, task actions.Task, yes bool, question string) (actions.Summary, error) {
	p, out := d.palette(), cmd.OutOrStdout()
	if task.Ask != "" {
		question = task.Ask
	}
	if (task.Writes || task.Ask != "") && !yes {
		ok, err := d.confirm(out, question)
		if err != nil {
			return actions.Summary{}, err
		}
		if !ok {
			_, err := fmt.Fprintln(out, "Nothing changed.")
			return actions.Summary{}, errors.Join(err, errCancelled)
		}
	}
	if _, err := lipgloss.Fprintln(cmd.ErrOrStderr(), p.Fg(p.Muted).Render(task.Title+"…")); err != nil {
		return actions.Summary{}, err
	}
	progress := taw.NewLineWriter(func(line string) {
		_, _ = lipgloss.Fprintln(cmd.ErrOrStderr(), p.Fg(p.Faint).Render("  "+line))
	})
	sum, err := task.Run(cmd.Context(), progress)
	progress.Flush()
	return sum, err
}

var errCancelled = errors.New("cancelled")

func printSummary(cmd *cobra.Command, d Deps, sum actions.Summary) error {
	p := d.palette()
	var b strings.Builder
	b.WriteString(p.Fg(p.OK).Render("✓") + " " + lipgloss.NewStyle().Bold(true).Render(sum.Headline) + "\n")
	for _, l := range sum.Lines {
		b.WriteString("  " + l + "\n")
	}
	_, err := lipgloss.Fprint(cmd.OutOrStdout(), b.String())
	return err
}

// resolveForTask scans (offline: tasks don't need GitHub's newest versions)
// and picks the site and theme.
func resolveForTask(cmd *cobra.Command, d Deps, g *globals, q, theme string) (*site.Site, site.Theme, *actions.Actions, error) {
	a, err := d.actions()
	if err != nil {
		return nil, site.Theme{}, nil, err
	}
	rep, err := d.scanner(g).Run(cmd.Context())
	if err != nil {
		return nil, site.Theme{}, nil, err
	}
	s, t, err := scan.ResolveTheme(rep.Sites, q, theme)
	return s, t, a, err
}

func dirtyNote(t site.Theme) string {
	if t.Git != nil && t.Git.Dirty > 0 {
		return fmt.Sprintf(" (it has %d uncommitted %s)", t.Git.Dirty, plural(t.Git.Dirty, "change", "changes"))
	}
	return ""
}

func newSyncCmd(d Deps, g *globals) *cobra.Command {
	var theme string
	var apply, yes, asJSON bool
	cmd := &cobra.Command{
		Use:   "sync <site>",
		Short: "Check a theme's framework files against the taw-theme scaffold (bin/taw sync)",
		Long: "Run the theme's own `bin/taw sync`: compare its framework files (Tier 1) and docs/config\n" +
			"(Tier 2) with the newest taw-theme. Reads only, unless --apply: then Tier 1 is written\n" +
			"(Tier 2 is never written; review it by hand or with the update-theme skill).",
		Example: "  taw-fleet sync chcapital\n  taw-fleet sync ls-mxico --apply",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, t, a, err := resolveForTask(cmd, d, g, args[0], theme)
			if err != nil {
				return err
			}
			task, err := a.SyncTask(*s, t, apply)
			if err != nil {
				return fmt.Errorf("%s: %w", t.Dir, err)
			}
			sum, err := runTask(cmd, d, task, yes, fmt.Sprintf("Write the Tier 1 framework files in %s%s?", t.Dir, dirtyNote(t)))
			if errors.Is(err, errCancelled) {
				return nil
			}
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), sum.Report)
			}
			return printSummary(cmd, d, sum)
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	cmd.Flags().BoolVar(&apply, "apply", false, "write the Tier 1 changes")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask (with --apply)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print bin/taw sync's JSON report")
	return cmd
}

func newUpdateCmd(d Deps, g *globals) *cobra.Command {
	var theme string
	var yes bool
	cmd := &cobra.Command{
		Use:   "update <site>",
		Short: "Update a theme's taw/core (composer update taw/core) and list what to check",
		Long: "Run `composer update taw/core` in the theme with the site's PHP and Local's Composer,\n" +
			"then list the vendor/taw/core/UPGRADING.md sections between the old and new version.\n" +
			"composer.lock and vendor/ change; commit them yourself. For the full flow (branch,\n" +
			"scaffold sync, checks, commit) hand it to an agent: taw-fleet handoff <site>.",
		Example: "  taw-fleet update ls-mxico\n  taw-fleet update ls-mxico --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, t, a, err := resolveForTask(cmd, d, g, args[0], theme)
			if err != nil {
				return err
			}
			if !t.Core.Behind && t.Core.Latest != "" {
				p := d.palette()
				_, err := lipgloss.Fprintln(cmd.OutOrStdout(), p.Fg(p.OK).Render("✓")+" "+t.Dir+" already has the newest taw/core ("+strings.TrimPrefix(t.Core.Installed, "v")+")")
				return err
			}
			task, err := a.UpdateTask(*s, t)
			if err != nil {
				return fmt.Errorf("%s: %w", t.Dir, err)
			}
			q := fmt.Sprintf("Update taw/core in %s from %s to %s%s?", t.Dir, strings.TrimPrefix(t.Core.Installed, "v"), latestOr(t.Core.Latest), dirtyNote(t))
			sum, err := runTask(cmd, d, task, yes, q)
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
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}

func latestOr(v string) string {
	if v == "" {
		return "the newest"
	}
	return strings.TrimPrefix(v, "v")
}

func newInspectCmd(d Deps, g *globals) *cobra.Command {
	var theme string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "inspect <site>",
		Short: "What a running site has: blocks, fields, forms (bin/taw inspect)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			offline := *g
			offline.offline = true
			s, t, _, err := resolveForTask(cmd, d, &offline, args[0], theme)
			if err != nil {
				return err
			}
			r := taw.Runner{Exec: d.Runner}
			if php, ok := local.PHPBinary(d.Paths, s.PHPVersion); ok {
				r.PHP = php
			}
			raw, err := r.Inspect(cmd.Context(), *s, t)
			if err != nil {
				return err
			}
			if asJSON {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}
			return renderInspect(cmd, d, t, raw)
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print bin/taw inspect's JSON")
	return cmd
}

func newWorkCmd(d Deps, g *globals) *cobra.Command {
	var theme string
	var stop, yes bool
	cmd := &cobra.Command{
		Use:   "work <site>",
		Short: "Get a theme ready to work on: site, editor, Vite and browser",
		Long: "Start the site in Local (when it's stopped), open the theme in your editor, run Vite\n" +
			"(the theme's `npm run dev`) in its own terminal window, and open the site once Vite\n" +
			"answers. --stop stops the theme's Vite and the site again. The dashboard's key: w.",
		Example: "  taw-fleet work chcapital\n  taw-fleet work chcapital --stop",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			offline := *g
			offline.offline = true
			s, t, a, err := resolveForTask(cmd, d, &offline, args[0], theme)
			if err != nil {
				return err
			}
			work := a.WorkTask
			if stop {
				work = a.StopWorkTask
			}
			task, err := work(*s, t, d.runSiteOp)
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
			return printSummary(cmd, d, sum)
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	cmd.Flags().BoolVar(&stop, "stop", false, "stop the theme's Vite and the site")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}
