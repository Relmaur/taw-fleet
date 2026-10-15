package cli

import (
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/doctor"
	"github.com/Relmaur/taw-fleet/internal/notes"
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
			es, _ := notes.Read(d.notesFile(s.Slug)) // notes are extra: a broken file doesn't stop show
			if len(es) > showNotes {
				es = es[:showNotes]
			}
			if asJSON {
				if es == nil {
					es = []notes.Entry{}
				}
				return writeJSON(cmd.OutOrStdout(), struct {
					Site     site.Site         `json:"site"`
					Findings []site.Finding    `json:"findings"`
					Latest   map[string]string `json:"latest,omitempty"`
					Notes    []notes.Entry     `json:"notes"`
				}{*s, nonNil(findings), rep.Latest, es})
			}
			p := d.palette()
			out := "\n" + render.Site(p, d.Paths, *s, findings, time.Now(), 0)
			if len(es) > 0 {
				out += "\n" + render.Section(p, p.I.Notes+" NOTES", p.Accent, "taw-fleet notes "+s.Slug, 0) + renderNotes(d, es, time.Now())
			}
			_, err = lipgloss.Fprint(cmd.OutOrStdout(), out)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// showNotes is how many of the newest notes show (and show --json) carry.
const showNotes = 5

func nonNil(fs []site.Finding) []site.Finding {
	if fs == nil {
		return []site.Finding{}
	}
	return fs
}
