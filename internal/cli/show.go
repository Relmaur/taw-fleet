package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/doctor"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
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
			return renderShow(cmd.OutOrStdout(), d.palette(), d.Paths, *s, findings, time.Now())
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func renderShow(w io.Writer, p style.Palette, ps paths.Paths, s site.Site, findings []site.Finding, now time.Time) error {
	muted := p.Fg(p.Muted)
	var b strings.Builder

	b.WriteString("\n " + p.Dot(s.Status) + " " + p.Title(s.Slug) + "  " + p.Fg(p.Brand).Render(s.URL) +
		"  " + p.StatusText(s.Status) + "\n")
	var facts []string
	add := func(k, v string) {
		if v != "" {
			facts = append(facts, k+" "+v)
		}
	}
	add("PHP", s.PHPVersion)
	add("MySQL", s.MySQLVersion)
	if s.HTTPPort > 0 {
		add(s.WebServer, fmt.Sprintf(":%d", s.HTTPPort))
	}
	if s.MultiSite != "" {
		add("multisite", s.MultiSite)
	}
	add("id", s.ID)
	b.WriteString("   " + muted.Render(tilde(ps, s.Path)+"  ·  "+strings.Join(facts, "  ·  ")) + "\n")
	for _, h := range s.Hosts {
		b.WriteString("   " + muted.Render("connected to "+h.HostID+" "+h.Env) + "\n")
	}
	if s.Name != "" && s.Name != s.Slug {
		b.WriteString("   " + muted.Render("Local name: "+s.Name) + "\n")
	}
	b.WriteString("\n")

	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Faint).Padding(0, 1).MarginLeft(1)
	key := muted.Width(10)
	themes := s.TAWThemes()
	var cards []string
	if len(themes) == 0 {
		b.WriteString(" " + muted.Render("No TAW theme in this site.") + "\n")
	}
	for _, t := range themes {
		var rows []string
		row := func(k, v string) { rows = append(rows, key.Render(k)+" "+v) }

		title := lipgloss.NewStyle().Bold(true).Render(t.Dir) + "  " + p.Kind(t.Kind)
		if t.Symlink {
			title += "  " + muted.Render("↗ symlink")
		}
		rows = append(rows, title, "")
		row("path", tilde(ps, t.Path))
		if t.Symlink {
			row("links to", tilde(ps, t.RealPath))
		}

		core := p.Core(t.Core)
		var extra []string
		if t.Core.Locked != "" && t.Core.LockMismatch {
			extra = append(extra, "lock "+strings.TrimPrefix(t.Core.Locked, "v"))
		}
		if t.Core.Latest != "" && !t.Core.Behind {
			extra = append(extra, "latest")
		}
		if len(extra) > 0 {
			core += muted.Render("  (" + strings.Join(extra, ", ") + ")")
		}
		row("taw/core", core)

		if t.Git == nil {
			row("git", muted.Render("not its own repository"))
		} else {
			gi := t.Git
			state := p.Git(gi)
			if gi.Upstream != "" {
				state += muted.Render("  tracking " + gi.Upstream)
			}
			if gi.DefaultBranch != "" && gi.Branch != gi.DefaultBranch && !gi.Detached {
				state += muted.Render("  (default " + gi.DefaultBranch + ")")
			}
			row("git", state)
			if gi.Repo != nil {
				row("repo", p.Fg(p.Brand).Render(gi.Repo.WebURL()))
			} else if gi.RemoteURL != "" {
				row("remote", gi.RemoteURL)
			}
			ver := gi.Describe
			if !gi.LastCommit.IsZero() {
				ver += muted.Render("  ·  last commit " + ago(now, gi.LastCommit))
			}
			row("version", ver)
		}
		bin := p.Fg(p.OK).Render("yes")
		if !t.HasBinTaw {
			bin = muted.Render("no")
		}
		row("bin/taw", bin)
		cards = append(cards, strings.Join(rows, "\n"))
	}
	// One width for every card, so they line up.
	widest := 0
	for _, c := range cards {
		widest = max(widest, lipgloss.Width(c))
	}
	for _, c := range cards {
		b.WriteString(box.Render(lipgloss.NewStyle().Width(widest).Render(c)) + "\n")
	}

	b.WriteString("\n")
	writeFindings(&b, p, findings, false)
	_, err := lipgloss.Fprint(w, b.String())
	return err
}

// writeFindings prints findings as icon + message, theme and code, then the fix.
func writeFindings(b *strings.Builder, p style.Palette, fs []site.Finding, withSite bool) {
	muted := p.Fg(p.Muted)
	if len(fs) == 0 {
		b.WriteString(" " + p.Fg(p.OK).Render("✓ Nothing to report.") + "\n")
		return
	}
	for _, f := range fs {
		where := f.Theme
		if withSite && f.Site != "" {
			where = f.Site
			if f.Theme != "" {
				where += "/" + f.Theme
			}
		}
		line := "   " + p.Severity(f.Severity) + " " + f.Message
		if where != "" {
			line += "  " + muted.Render(where)
		}
		line += "  " + p.Fg(p.Faint).Render(f.Code)
		b.WriteString(line + "\n")
		if f.Fix != "" {
			b.WriteString("     " + muted.Render("→ "+f.Fix) + "\n")
		}
	}
}

// tilde shortens a path under the home folder to ~/… (also when the path
// went through symlinks, e.g. /var → /private/var on macOS).
func tilde(ps paths.Paths, path string) string {
	if ps.Home == "" {
		return path
	}
	homes := []string{ps.Home}
	if resolved, err := filepath.EvalSymlinks(ps.Home); err == nil && resolved != ps.Home {
		homes = append(homes, resolved)
	}
	for _, h := range homes {
		if strings.HasPrefix(path, h+"/") {
			return "~" + path[len(h):]
		}
	}
	return path
}

// ago is a short relative time: "just now", "5 minutes ago", "3 days ago".
func ago(now, then time.Time) string {
	d := now.Sub(then)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return unit(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return unit(int(d.Hours()), "hour")
	case d < 60*24*time.Hour:
		return unit(int(d.Hours()/24), "day")
	case d < 365*24*time.Hour:
		return unit(int(d.Hours()/24/30), "month")
	}
	return unit(int(d.Hours()/24/365), "year")
}

func unit(n int, name string) string {
	if n == 1 {
		return "1 " + name + " ago"
	}
	return fmt.Sprintf("%d %ss ago", n, name)
}

func nonNil(fs []site.Finding) []site.Finding {
	if fs == nil {
		return []site.Finding{}
	}
	return fs
}
