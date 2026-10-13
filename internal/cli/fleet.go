package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/fleet"
)

// writeFleet brings the fleet folder up to date (CLAUDE.md, .mcp.json, the
// fleet skills) from the settings.
func (d Deps) writeFleet(version string) (fleet.Folder, fleet.Result, error) {
	f := fleet.Folder{Dir: d.Paths.FleetDir, Version: version, Settings: config.File(d.Paths), Home: d.Paths.Home}
	exe, err := d.exePath()
	if err != nil {
		return f, fleet.Result{}, err
	}
	f.Exe = exe
	cfg, err := config.Load(d.Paths)
	if err != nil {
		return f, fleet.Result{}, err
	}
	var sites []fleet.Site
	for slug, s := range cfg.Sites {
		if s.ProductionURL == "" {
			continue
		}
		sites = append(sites, fleet.Site{Slug: slug, Production: s.ProductionURL, BugSmash: s.BugSmashProject,
			Language: cfg.ReportLang(slug), Notes: s.Notes})
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].Slug < sites[j].Slug })
	r, err := f.Write(sites)
	return f, r, err
}

// fleetSkills are the fleet skills as the a picker lists them.
func fleetSkills(dir string) []actions.Skill {
	var out []actions.Skill
	for _, s := range fleet.Skills() {
		out = append(out, actions.Skill{Name: s.Name, Description: s.Description, Owner: actions.FleetOwner,
			Dir: filepath.Join(dir, ".claude", "skills", s.Name)})
	}
	return out
}

func newInitCmd(info BuildInfo, d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Set up the fleet folder (~/TAW Fleet): CLAUDE.md, taw-fleet's MCP server, the fleet skills",
		Long: "Write the fleet folder, where Claude works on the whole fleet (the dashboard's chat, or\n" +
			"`claude` there): a CLAUDE.md made from the settings, taw-fleet's MCP server in .mcp.json,\n" +
			"and the fleet skills (fleet-check, new-site, update-fleet, onboard-mac). The dashboard\n" +
			"does this too when it opens, so it stays current after an upgrade. Your own notes\n" +
			"(CLAUDE.local.md), skills and reports are left alone.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, r, err := d.writeFleet(info.Version)
			if err != nil {
				return err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "The fleet folder: %s\n", f.Dir)
			if len(r.Wrote) == 0 && len(r.Removed) == 0 {
				b.WriteString("Already up to date.\n")
			}
			for _, w := range r.Wrote {
				fmt.Fprintf(&b, "  wrote   %s\n", w)
			}
			for _, w := range r.Removed {
				fmt.Fprintf(&b, "  removed %s (no longer shipped)\n", w)
			}
			b.WriteString("\nFleet skills:\n")
			for _, s := range fleet.Skills() {
				fmt.Fprintf(&b, "  %s\n", s.Name)
			}
			b.WriteString("\nIn the dashboard: > chats about the fleet here; a lists the fleet skills.\nIn a terminal: cd \"" + f.Dir + "\" && claude\n")
			_, err = fmt.Fprint(cmd.OutOrStdout(), b.String())
			return err
		},
	}
}
