package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/doctor"
	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
)

// doctorJSON is the `doctor --json` shape.
type doctorJSON struct {
	ScannedAt time.Time      `json:"scanned_at"`
	Counts    map[string]int `json:"counts"`
	Findings  []site.Finding `json:"findings"`
	Clean     []string       `json:"clean"` // TAW sites with nothing to report
}

func newDoctorCmd(d Deps, g *globals) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor [site]",
		Short: "What needs attention: taw/core behind, unsaved or unpushed work, broken links",
		Long: "Check every TAW site (or one) and list what needs attention, most serious first,\n" +
			"each with the fix. It only reads; it never changes anything.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rep, err := d.scanner(g).Run(cmd.Context())
			if err != nil {
				return err
			}
			if !g.offline {
				d.applyLive(cmd.Context(), &rep, false)
				d.applyFeedback(cmd.Context(), &rep, false)
				d.applyRepos(cmd, g, &rep)
			}
			if len(args) == 1 {
				s, err := scan.Resolve(rep.Sites, args[0])
				if err != nil {
					return err
				}
				rep.Sites = []site.Site{*s}
			}
			findings := doctor.Run(rep, d.doctorOptions())
			clean := cleanSites(rep, findings)
			if asJSON {
				e, w, i := doctor.Counts(findings)
				return writeJSON(cmd.OutOrStdout(), doctorJSON{
					ScannedAt: rep.ScannedAt,
					Counts:    map[string]int{"error": e, "warn": w, "info": i},
					Findings:  nonNil(findings),
					Clean:     clean,
				})
			}
			return renderDoctor(cmd.OutOrStdout(), d.palette(), findings, clean)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// cleanSites are the TAW sites without a single finding.
func cleanSites(rep scan.Report, fs []site.Finding) []string {
	flagged := map[string]bool{}
	for _, f := range fs {
		flagged[f.SiteID] = true
	}
	clean := []string{}
	for _, s := range rep.Sites {
		if s.IsTAW() && !flagged[s.ID] {
			clean = append(clean, s.Slug)
		}
	}
	return clean
}

func renderDoctor(w io.Writer, p style.Palette, fs []site.Finding, clean []string) error {
	muted := p.Fg(p.Muted)
	e, wn, i := doctor.Counts(fs)
	var b strings.Builder
	summary := []string{
		p.Fg(p.Err).Render(fmt.Sprintf("%d %s", e, plural(e, "error", "errors"))),
		p.Fg(p.Warn).Render(fmt.Sprintf("%d %s", wn, plural(wn, "warning", "warnings"))),
		p.Fg(p.Info).Render(fmt.Sprintf("%d %s", i, plural(i, "note", "notes"))),
	}
	b.WriteString("\n " + p.Title("taw-fleet doctor") + muted.Render("  ·  ") + strings.Join(summary, muted.Render("  ·  ")) + "\n\n")

	// Group by site, keeping the most-severe-first order of first appearance.
	var order []string
	groups := map[string][]site.Finding{}
	for _, f := range fs {
		key := f.Site
		if key == "" {
			key = "taw-fleet"
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], f)
	}
	head := lipgloss.NewStyle().Bold(true)
	for _, k := range order {
		b.WriteString(" " + head.Render(k) + "\n")
		b.WriteString(render.Findings(p, groups[k], false, 0))
		b.WriteString("\n")
	}
	if len(fs) == 0 {
		b.WriteString(" " + p.Fg(p.OK).Render("✓ Nothing to report.") + "\n")
	} else if len(clean) > 0 {
		b.WriteString(" " + p.Fg(p.OK).Render("✓ Nothing to report for "+strings.Join(clean, ", ")) + "\n")
	}
	_, err := lipgloss.Fprint(w, b.String())
	return err
}
