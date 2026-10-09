package cli

import (
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/doctor"
	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
)

func newShowCmd(d Deps, g *globals) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <site>",
		Short: "Everything about one site: themes, taw/core, git, findings",
		Long: "Show one site. <site> is its folder name, name, domain, Local id or a theme folder,\n" +
			"e.g. `taw-fleet show chcapital`.",
		Args: cobra.ExactArgs(1),
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
			s, err := scan.Resolve(rep.Sites, args[0])
			if err != nil {
				return err
			}
			findings := doctor.Run(scan.Report{Sites: []site.Site{*s}}, d.doctorOptions())
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), struct {
					Site     site.Site         `json:"site"`
					Findings []site.Finding    `json:"findings"`
					Latest   map[string]string `json:"latest,omitempty"`
				}{*s, nonNil(findings), rep.Latest})
			}
			_, err = lipgloss.Fprint(cmd.OutOrStdout(), "\n"+render.Site(d.palette(), d.Paths, *s, findings, time.Now(), 0))
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func nonNil(fs []site.Finding) []site.Finding {
	if fs == nil {
		return []site.Finding{}
	}
	return fs
}
