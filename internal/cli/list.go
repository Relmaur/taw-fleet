package cli

import (
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
)

func newListCmd(d Deps, g *globals) *cobra.Command {
	var asJSON, all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the TAW sites on this Mac",
		Long: "List the Local by Flywheel sites that have a TAW theme, with their status and themes.\n" +
			"--all also shows sites and themes that aren't TAW.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, d, g, asJSON, all)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().BoolVar(&all, "all", false, "include sites and themes that aren't TAW")
	return cmd
}

func runList(cmd *cobra.Command, d Deps, g *globals, asJSON, all bool) error {
	rep, err := d.scanner(g).Run(cmd.Context())
	if err != nil {
		return err
	}
	hidden := 0
	if !all {
		before := len(rep.Sites)
		rep = OnlyTAW(rep)
		hidden = before - len(rep.Sites)
	}
	if asJSON {
		return writeJSON(cmd.OutOrStdout(), rep)
	}
	return renderList(cmd.OutOrStdout(), d.palette(), rep, hidden)
}

// OnlyTAW keeps the TAW sites, each with its TAW themes only. Report.Errors
// (whole-source failures) is kept.
func OnlyTAW(rep scan.Report) scan.Report {
	out := rep
	out.Sites = []site.Site{}
	for _, s := range rep.Sites {
		if !s.IsTAW() {
			continue
		}
		s.Themes = s.TAWThemes()
		out.Sites = append(out.Sites, s)
	}
	return out
}

// renderList prints the sites table. hidden is how many non-TAW sites were
// filtered out (0 with --all).
func renderList(w io.Writer, p style.Palette, rep scan.Report, hidden int) error {
	sites, themes, running := 0, 0, 0
	for _, s := range rep.Sites {
		sites++
		themes += len(s.TAWThemes())
		if s.Status == site.StatusRunning {
			running++
		}
	}

	var b strings.Builder
	b.WriteString("\n " + p.Title("taw-fleet") + p.Fg(p.Muted).Render(fmt.Sprintf(
		"  ·  %d %s  ·  %d TAW %s  ·  %d running", sites, plural(sites, "site", "sites"),
		themes, plural(themes, "theme", "themes"), running)) + "\n\n")

	for _, e := range rep.Errors {
		col := p.Err
		if e.Stage == "github" {
			col = p.Warn // only the "latest" column is missing
		}
		b.WriteString(" " + p.Fg(col).Render("✗ "+e.Stage+": "+e.Err) + "\n")
	}
	if len(rep.Errors) > 0 {
		b.WriteString("\n")
	}
	if len(rep.Sites) == 0 {
		b.WriteString(" " + p.Fg(p.Muted).Render("No TAW sites found.") + "\n")
		_, err := lipgloss.Fprint(w, b.String())
		return err
	}

	muted := p.Fg(p.Muted)
	var rows [][]string
	for _, s := range rep.Sites {
		ts := s.Themes
		if len(ts) == 0 {
			ts = []site.Theme{{}}
		}
		for i, t := range ts {
			dot, name, domain := "", "", ""
			if i == 0 {
				dot, name, domain = p.Dot(s.Status), s.Slug, s.Domain
				if len(s.Errors) > 0 {
					name += " " + p.Fg(p.Warn).Render("⚠")
				}
			}
			theme := t.Dir
			switch {
			case t.Broken:
				theme += " " + p.Fg(p.Err).Render("↯ broken link")
			case t.Symlink:
				theme += " " + muted.Render("↗")
			}
			if theme == "" {
				theme = muted.Render("—")
			}
			kind, core, gitCell := p.Kind(t.Kind), "", ""
			switch {
			case t.IsTAW:
				core, gitCell = p.Core(t.Core), p.Git(t.Git)
				if t.Account != "" {
					gitCell = p.Fg(p.Brand).Render("@"+t.Account) + " " + gitCell
				}
			case t.Dir != "":
				kind = muted.Render("other")
			}
			rows = append(rows, []string{dot, name, theme, kind, core, gitCell, muted.Render(domain)})
		}
	}

	header := lipgloss.NewStyle().Foreground(p.Muted).Bold(true)
	cell := lipgloss.NewStyle().Padding(0, 1)
	t := table.New().
		Headers("", "SITE", "THEME", "KIND", "TAW/CORE", "GIT", "DOMAIN").
		Rows(rows...).
		Border(lipgloss.NormalBorder()).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderHeader(true).
		BorderStyle(p.Fg(p.Faint)).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return header.Padding(0, 1)
			}
			return cell
		})
	b.WriteString(t.Render() + "\n")

	b.WriteString("\n " + muted.Render("● running  ○ halted  ↗ symlink  ▲ taw/core behind  ±uncommitted  ↑to push  ↓to pull") + "\n")
	next := "`taw-fleet doctor` lists what needs attention  ·  `taw-fleet show <site>` for one site"
	if hidden > 0 {
		next += fmt.Sprintf("  ·  %d more %s without a TAW theme (--all)", hidden, plural(hidden, "site", "sites"))
	}
	b.WriteString(" " + muted.Render(next) + "\n")
	errCount := 0
	for _, s := range rep.Sites {
		errCount += len(s.Errors)
	}
	if errCount > 0 {
		b.WriteString(" " + p.Fg(p.Warn).Render(fmt.Sprintf("⚠ %d %s while reading sites (see --json)", errCount, plural(errCount, "problem", "problems"))) + "\n")
	}
	_, err := lipgloss.Fprint(w, b.String())
	return err
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
