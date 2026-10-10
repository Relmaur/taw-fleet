package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/doctor"
	"github.com/Relmaur/taw-fleet/internal/live"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
	"github.com/Relmaur/taw-fleet/internal/tools"
)

func (d Deps) actions() (*actions.Actions, error) {
	cfg, err := config.Load(d.Paths)
	if err != nil {
		return nil, err
	}
	a := actions.New(d.Paths, d.Runner, cfg)
	a.Merger = d.github(&globals{})
	a.Content = func(ctx context.Context, slug, url string) ([]byte, error) {
		pr, err := d.prober(ctx)
		if err != nil {
			return nil, err
		}
		return pr.Content(ctx, live.Target{Slug: slug, URL: url})
	}
	return a, nil
}

// runFleet lists (or, with launch, starts) an update of every client TAW
// theme that needs one.
func runFleet(cmd *cobra.Command, d Deps, g *globals, a *actions.Actions, rep scan.Report, launch bool) error {
	a.CoreLatest = d.coreLatest(g)
	plan := a.PlanFleet(cmd.Context(), rep.Sites)
	w, p := cmd.OutOrStdout(), d.palette()
	for _, e := range plan.Themes {
		core := "current"
		if e.Theme.Core.Behind {
			core = strings.TrimPrefix(e.Theme.Core.Installed, "v") + " → " + strings.TrimPrefix(e.Theme.Core.Latest, "v")
		}
		_, _ = lipgloss.Fprintln(w, p.Fg(p.OK).Render("+")+" "+e.Site.Slug+" / "+e.Theme.Dir+"  "+p.Fg(p.Muted).Render("taw/core "+core))
	}
	for _, s := range plan.Skipped {
		_, _ = lipgloss.Fprintln(w, p.Fg(p.Warn).Render("−")+" "+s.Site+" / "+s.Theme+"  "+p.Fg(p.Muted).Render("left out: "+s.Reason))
	}
	if len(plan.Themes) == 0 {
		_, err := fmt.Fprintln(w, "Nothing to update: every client TAW theme is current.")
		return err
	}
	if !launch {
		_, err := fmt.Fprintln(w, "\nRun with --launch to update these in one Claude Code session.")
		return err
	}
	findings := map[string][]site.Finding{}
	for _, f := range doctor.Run(rep, d.doctorOptions()) {
		findings[f.SiteID] = append(findings[f.SiteID], f)
	}
	l, err := a.LaunchFleet(cmd.Context(), plan, findings, "")
	if err != nil {
		return err
	}
	_, err = lipgloss.Fprintln(w, p.Fg(p.OK).Render("✓")+" "+l.Message)
	return err
}

var openFlags = []struct {
	kind  actions.Kind
	flag  string
	usage string
}{
	{actions.Editor, "editor", "the theme in your editor (default)"},
	{actions.Finder, "finder", "the theme folder in Finder"},
	{actions.Browser, "browser", "the site in your browser"},
	{actions.Admin, "admin", "the site's wp-admin"},
	{actions.GitHub, "github", "the theme's repository on GitHub"},
	{actions.PRs, "prs", "the repository's pull requests"},
	{actions.Terminal, "terminal", "a terminal in the theme folder"},
	{actions.Production, "production", "the production site (production_url in the config)"},
}

func newOpenCmd(d Deps, g *globals) *cobra.Command {
	picked := map[actions.Kind]*bool{}
	var theme string
	cmd := &cobra.Command{
		Use:   "open <site>",
		Short: "Open a theme in your editor, Finder, a terminal, or its site / repo in the browser",
		Long: "Open a site's TAW theme somewhere. With no flag it opens in your editor.\n" +
			"Editors and terminals are detected; choose them in `taw-fleet config`.",
		Example: "  taw-fleet open chcapital\n  taw-fleet open ls-mxico --github\n  taw-fleet open taw --theme taw-gutenberg --terminal",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var kinds []actions.Kind
			for _, f := range openFlags {
				if *picked[f.kind] {
					kinds = append(kinds, f.kind)
				}
			}
			if len(kinds) == 0 {
				kinds = []actions.Kind{actions.Editor}
			}
			a, err := d.actions()
			if err != nil {
				return err
			}
			// Opening needs no newest versions from GitHub.
			offline := *g
			offline.offline = true
			rep, err := d.scanner(&offline).Run(cmd.Context())
			if err != nil {
				return err
			}
			s, t, err := scan.ResolveTheme(rep.Sites, args[0], theme)
			if err != nil {
				return err
			}
			p := d.palette()
			for _, k := range kinds {
				msg, err := a.Do(cmd.Context(), k, *s, t)
				if err != nil {
					return err
				}
				if _, err := lipgloss.Fprintln(cmd.OutOrStdout(), p.Fg(p.OK).Render("✓")+" "+msg); err != nil {
					return err
				}
			}
			return nil
		},
	}
	for _, f := range openFlags {
		picked[f.kind] = cmd.Flags().Bool(f.flag, false, f.usage)
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	return cmd
}

func newHandoffCmd(d Deps, g *globals) *cobra.Command {
	var theme, out string
	var copyIt, launch, all bool
	cmd := &cobra.Command{
		Use:   "handoff <site> | --all",
		Short: "Hand a theme update to a coding agent: a ready prompt with all the context",
		Long: "Write the prompt that hands this theme's update to an agent: the update-theme skill,\n" +
			"where everything is on this Mac, what taw-fleet found, and the rules (work on a\n" +
			"chore/taw-core-<version> branch, taw/core update approved, Tier 2 diffs need your OK,\n" +
			"ask before pushing). It prints the prompt; --copy puts it on the clipboard and\n" +
			"--launch opens a terminal running Claude Code in the theme folder with it.\n\n" +
			"--all updates every client TAW theme that needs it in one Claude Code session:\n" +
			"a coordinator runs a subagent per theme (the update-theme skill's batch mode) and\n" +
			"asks you once about docs changes, sites to start and pushes. Without --launch it\n" +
			"lists the themes it would take and the ones it leaves out.",
		Example: "  taw-fleet handoff ls-mxico\n  taw-fleet handoff ls-mxico --launch\n  taw-fleet handoff emelambda --copy\n  taw-fleet handoff --all --launch",
		Args: func(cmd *cobra.Command, args []string) error {
			if all {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := d.actions()
			if err != nil {
				return err
			}
			rep, err := d.scanner(g).Run(cmd.Context())
			if err != nil {
				return err
			}
			if all {
				return runFleet(cmd, d, g, a, rep, launch)
			}
			s, t, err := scan.ResolveTheme(rep.Sites, args[0], theme)
			if err != nil {
				return err
			}
			findings := doctor.Run(scan.Report{Sites: []site.Site{*s}}, d.doctorOptions())
			prompt, err := a.Handoff(*s, t, findings)
			if err != nil {
				return fmt.Errorf("%s: %w", t.Dir, err)
			}
			w, p := cmd.OutOrStdout(), d.palette()
			done := func(msg string) error {
				_, err := lipgloss.Fprintln(w, p.Fg(p.OK).Render("✓")+" "+msg)
				return err
			}
			if !copyIt && !launch && out == "" {
				_, err := fmt.Fprint(w, prompt.Text)
				return err
			}
			if out != "" {
				if err := os.WriteFile(out, []byte(prompt.Text), 0o644); err != nil {
					return err
				}
				if err := done("Wrote the prompt to " + out); err != nil {
					return err
				}
			}
			if copyIt {
				if err := a.Copy(cmd.Context(), prompt.Text); err != nil {
					return err
				}
				if err := done(fmt.Sprintf("Copied the prompt for %s (%d lines). Paste it into your agent.", t.Dir, strings.Count(prompt.Text, "\n"))); err != nil {
					return err
				}
			}
			if launch {
				l, err := a.Launch(cmd.Context(), *s, t, prompt, "")
				if err != nil {
					return err
				}
				if err := done(l.Message); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	cmd.Flags().BoolVar(&copyIt, "copy", false, "copy the prompt to the clipboard")
	cmd.Flags().BoolVar(&launch, "launch", false, "open a terminal running Claude Code in the theme folder with the prompt")
	cmd.Flags().BoolVar(&all, "all", false, "every client TAW theme that needs an update, in one Claude Code session (with --launch)")
	cmd.Flags().StringVar(&out, "out", "", "also write the prompt to this file")
	return cmd
}

func newConfigCmd(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Where the settings file is, what's in effect, and a starter file",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use: "path", Short: "Print the settings file's path", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), config.File(d.Paths))
				return err
			},
		},
		&cobra.Command{
			Use: "show", Short: "Show the settings in effect and the apps found", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				a, err := d.actions()
				if err != nil {
					return err
				}
				return renderConfig(cmd, d, a)
			},
		},
		&cobra.Command{
			Use:   "icons [symbols|nerd]",
			Short: "The dashboard's icons: plain symbols (any font) or Nerd Font icons",
			Long: "symbols (the default) works with every font. nerd needs the terminal set to a Nerd Font:\n" +
				"  brew install --cask font-jetbrains-mono-nerd-font\n" +
				"then choose \"JetBrainsMono Nerd Font\" in the terminal's settings (Terminal: Settings → Profiles → Text).\n" +
				"Without a Nerd Font, nerd icons show as empty boxes: switch back with taw-fleet config icons symbols.",
			Args: cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				p := d.palette()
				if len(args) == 0 {
					cfg, _ := config.Load(d.Paths)
					set := cfg.Icons
					if set == "" {
						set = "symbols"
					}
					_, err := lipgloss.Fprintln(cmd.OutOrStdout(), "icons: "+set+"  "+p.I.Logo+" "+p.I.Local+" "+p.I.Live+" "+p.I.Feedback+" "+p.I.Findings+" "+p.I.Update+" "+p.I.Branch)
					return err
				}
				if _, ok := style.IconSets[args[0]]; !ok {
					return fmt.Errorf("icons is symbols or nerd, not %q", args[0])
				}
				f, err := config.SetTop(d.Paths, "icons", args[0])
				if err != nil {
					return err
				}
				p = p.WithIcons(args[0])
				msg := p.Fg(p.OK).Render("✓") + " icons = " + args[0] + " in " + f + "  " + p.I.Logo + " " + p.I.Local + " " + p.I.Live + " " + p.I.Feedback + " " + p.I.Update
				if args[0] == "nerd" {
					msg += "\n  " + p.Fg(p.Muted).Render("Boxes instead of icons? The terminal needs a Nerd Font: taw-fleet config icons --help")
				}
				_, err = lipgloss.Fprintln(cmd.OutOrStdout(), msg)
				return err
			},
		},
		&cobra.Command{
			Use: "init", Short: "Write a commented starter settings file (never overwrites)", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				f, err := config.Init(d.Paths)
				if err != nil {
					return err
				}
				p := d.palette()
				_, err = lipgloss.Fprintln(cmd.OutOrStdout(), p.Fg(p.OK).Render("✓")+" Wrote "+f)
				return err
			},
		},
	)
	return cmd
}

func renderConfig(cmd *cobra.Command, d Deps, a *actions.Actions) error {
	p := d.palette()
	muted := p.Fg(p.Muted)
	key := muted.Width(12)
	var b strings.Builder
	file := config.File(d.Paths)
	state := muted.Render("(not created: defaults in use; `taw-fleet config init` writes one)")
	if _, err := os.Stat(file); err == nil {
		state = p.Fg(p.OK).Render("(loaded)")
	}
	b.WriteString("\n " + p.Title("taw-fleet config") + "\n\n")
	b.WriteString(" " + key.Render("file") + " " + file + "  " + state + "\n")

	pick := func(apps []string, want string, chosen string, err error) string {
		var s string
		switch {
		case err != nil:
			s = p.Fg(p.Err).Render(err.Error())
		case want == "":
			s = chosen + muted.Render("  (first found)")
		default:
			s = chosen
		}
		if len(apps) > 0 {
			s += muted.Render("  ·  installed: " + strings.Join(apps, ", "))
		}
		return s
	}
	names := func(kind string) []string {
		var out []string
		list := a.Tools.Editors
		if kind == "terminal" {
			list = a.Tools.Terminals
		}
		for _, app := range list {
			out = append(out, app.Name)
		}
		return out
	}
	ed, edErr := pickName(a, "editor")
	tm, tmErr := pickName(a, "terminal")
	b.WriteString(" " + key.Render("editor") + " " + pick(names("editor"), a.Config.Editor, ed, edErr) + "\n")
	b.WriteString(" " + key.Render("terminal") + " " + pick(names("terminal"), a.Config.Terminal, tm, tmErr) + "\n")
	claude, err := a.Claude()
	if err != nil {
		claude = muted.Render("not installed (handoff --launch needs it; --copy works without)")
	}
	b.WriteString(" " + key.Render("claude") + " " + claude + "\n")
	if len(a.Config.Sites) > 0 {
		b.WriteString("\n " + muted.Render("sites") + "\n")
		slugs := make([]string, 0, len(a.Config.Sites))
		for slug := range a.Config.Sites {
			slugs = append(slugs, slug)
		}
		sort.Strings(slugs)
		for _, slug := range slugs {
			s := a.Config.Sites[slug]
			line := "   " + slug
			if s.ProductionURL != "" {
				line += "  " + p.Fg(p.Brand).Render(s.ProductionURL)
			}
			if s.Notes != "" {
				line += "  " + muted.Render(s.Notes)
			}
			b.WriteString(line + "\n")
		}
	}
	_, err = lipgloss.Fprint(cmd.OutOrStdout(), b.String())
	return err
}

func pickName(a *actions.Actions, kind string) (string, error) {
	list, want := a.Tools.Editors, a.Config.Editor
	if kind == "terminal" {
		list, want = a.Tools.Terminals, a.Config.Terminal
	}
	// The same choice the shortcuts make, aliases ("vscode") included.
	app, err := tools.Pick(list, want)
	if err != nil {
		return "", err
	}
	return app.Name, nil
}
