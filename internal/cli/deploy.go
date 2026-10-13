package cli

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/createform"
	"github.com/Relmaur/taw-fleet/internal/deploy"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
)

// targetFlags are a deploy target on the command line, a profile pre-filling it.
type targetFlags struct {
	profile string
	t       deploy.Target
	method  string
}

func (f *targetFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.profile, "profile", "", "a host profile (taw-fleet deploy profiles): pre-fills the method, port and git defaults")
	cmd.Flags().StringVar(&f.method, "method", "", "rsync (SSH + rsync) or git (git push)")
	cmd.Flags().StringVar(&f.t.Host, "host", "", "rsync: the SSH host")
	cmd.Flags().StringVar(&f.t.Port, "port", "", "rsync: the SSH port (default 22)")
	cmd.Flags().StringVar(&f.t.User, "user", "", "rsync: the SSH user")
	cmd.Flags().StringVar(&f.t.Path, "path", "", "rsync: the theme's folder on the server (…/wp-content/themes/<theme>)")
	cmd.Flags().StringVar(&f.t.Remote, "git-remote", "", "git: the host's SSH git remote")
	cmd.Flags().StringVar(&f.t.Branch, "git-branch", "", "git: the branch the host deploys (default master)")
	cmd.Flags().StringVar(&f.t.GitPath, "git-path", "", "git: where the theme sits in the host's repository (default wp-content/themes/<theme>)")
	cmd.Flags().StringVar(&f.t.URL, "url", "", "the production site's URL")
}

// target is the flags' target for a theme, the profile filling what's left.
func (f targetFlags) target(themeDir string) (deploy.Target, error) {
	t := f.t
	if f.profile != "" {
		p, ok := deploy.FindProfile(f.profile)
		if !ok {
			return t, fmt.Errorf("no host profile %q (taw-fleet deploy profiles)", f.profile)
		}
		t = withProfile(t, p, themeDir)
	}
	if f.method != "" {
		t.Method = deploy.Method(f.method)
	}
	if t.Method == deploy.Git && t.GitPath == "" {
		t.GitPath = "wp-content/themes/" + themeDir
	}
	return t, nil
}

// withProfile fills what the profile knows for sure; the rest stays the person's.
func withProfile(t deploy.Target, p deploy.Profile, themeDir string) deploy.Target {
	if t.Method == "" {
		t.Method = p.Method
	}
	if t.Port == "" && isNumber(p.Port) {
		t.Port = p.Port
	}
	if t.Branch == "" {
		t.Branch = p.Branch
	}
	if t.GitPath == "" && p.GitPath != "" {
		t.GitPath = strings.ReplaceAll(p.GitPath, "<theme>", themeDir)
	}
	return t
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func newDeployCmd(d Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Set up a site's deploy to its host, any host: SSH + rsync or git push (I in the dashboard)",
		Long: "For a theme on GitHub with no deploy yet: taw/core's shared deploy (1.97+), configured in the\n" +
			"repository, after CI passes on the default branch. `deploy setup` asks for everything in a form;\n" +
			"the steps on their own (for an agent): `deploy key`, add its public half at the host, `deploy\n" +
			"test`, `deploy apply`. Host profiles only pre-fill and say where each value is.",
	}
	cmd.AddCommand(newDeployProfilesCmd(d), newDeployKeyCmd(d, g), newDeployTestCmd(d, g), newDeployApplyCmd(d, g), newDeploySetupCmd(d, g))
	return cmd
}

func newDeployProfilesCmd(d Deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "profiles",
		Short: "The host profiles: what each pre-fills, and where each value is",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), deploy.Profiles)
			}
			p := d.palette()
			var b strings.Builder
			for _, pr := range deploy.Profiles {
				fmt.Fprintf(&b, "%s  %s  %s\n", p.Fg(p.Accent).Render(pr.ID), pr.Name, p.Fg(p.Muted).Render(string(pr.Method)))
				for _, l := range profileLines(pr) {
					fmt.Fprintf(&b, "    %s\n", p.Fg(p.Muted).Render(l))
				}
			}
			_, err := lipgloss.Fprint(cmd.OutOrStdout(), b.String())
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func profileLines(p deploy.Profile) []string {
	var out []string
	for _, kv := range [][2]string{{"host", p.Host}, {"port", p.Port}, {"user", p.User}, {"path", p.Path},
		{"remote", p.Remote}, {"branch", p.Branch}, {"git path", p.GitPath}, {"public key", p.Key}} {
		if kv[1] != "" {
			out = append(out, kv[0]+": "+kv[1])
		}
	}
	return out
}

func newDeployKeyCmd(d Deps, g *globals) *cobra.Command {
	var theme string
	cmd := &cobra.Command{
		Use:   "key <site>",
		Short: "The site's deploy key: its public half, to add at the host (made the first time)",
		Long: "Makes an ed25519 deploy key the first time and keeps it in the Keychain until `deploy apply`\n" +
			"stores it in GitHub. Only the public half is ever printed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, a, err := resolveForTask(cmd, d, g, args[0], theme)
			if err != nil {
				return err
			}
			if _, err := a.DeployKeys().Load(cmd.Context(), s.Slug); err != nil {
				ok, err := d.confirm(cmd.OutOrStdout(), "Make a deploy key for "+s.Slug+"? Only its public half is shown; it stays in the Keychain until the setup stores it in GitHub.")
				if err != nil {
					return err
				}
				if !ok {
					_, err := fmt.Fprintln(cmd.OutOrStdout(), "Nothing changed.")
					return err
				}
			}
			key, _, err := a.DeployKey(cmd.Context(), s.Slug)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\nfingerprint %s\nAdd it at the host (taw-fleet deploy profiles says where), then: taw-fleet deploy test %s …\n",
				key.Public, key.Fingerprint(), s.Slug)
			return err
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	return cmd
}

func newDeployTestCmd(d Deps, g *globals) *cobra.Command {
	var theme string
	var f targetFlags
	cmd := &cobra.Command{
		Use:   "test <site>",
		Short: "Connect to the host with the deploy key, as the deploy will; nothing changes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, t, a, err := resolveForTask(cmd, d, g, args[0], theme)
			if err != nil {
				return err
			}
			target, err := f.target(t.Dir)
			if err != nil {
				return err
			}
			key, err := a.DeployKeys().Load(cmd.Context(), s.Slug)
			if err != nil {
				return errors.New("no deploy key for " + s.Slug + " yet: taw-fleet deploy key " + s.Slug)
			}
			res, err := a.DeployTester().Test(cmd.Context(), target, key)
			if err != nil {
				return err
			}
			p := d.palette()
			_, err = lipgloss.Fprintf(cmd.OutOrStdout(), "%s %s\n  host key pinned: %d line(s)\n", p.Fg(p.OK).Render("✓"), res.Said, strings.Count(res.KnownHosts, "\n")+1)
			return err
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	f.add(cmd)
	return cmd
}

func newDeployApplyCmd(d Deps, g *globals) *cobra.Command {
	var theme string
	var yes bool
	var f targetFlags
	cmd := &cobra.Command{
		Use:   "apply <site>",
		Short: "Store the deploy in GitHub and propose the workflow (asks first; merging it deploys)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, t, a, err := resolveForTask(cmd, d, g, args[0], theme)
			if err != nil {
				return err
			}
			target, err := f.target(t.Dir)
			if err != nil {
				return err
			}
			return runDeploy(cmd, d, a, *s, t, target, yes)
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	f.add(cmd)
	return cmd
}

func runDeploy(cmd *cobra.Command, d Deps, a *actions.Actions, s site.Site, t site.Theme, target deploy.Target, yes bool) error {
	task, err := a.DeployTask(s, t, target)
	if err != nil {
		return err
	}
	sum, err := runTask(cmd, d, task, yes, task.Ask)
	if errors.Is(err, errCancelled) {
		return nil
	}
	if err != nil {
		return err
	}
	return printSummary(cmd, d, sum)
}

func newDeploySetupCmd(d Deps, g *globals) *cobra.Command {
	var theme string
	cmd := &cobra.Command{
		Use:   "setup <site>",
		Short: "The whole deploy setup in a form: host, details, key, test, apply",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !d.Interactive {
				return errors.New("not a terminal: use deploy key, deploy test and deploy apply (taw-fleet help deploy)")
			}
			s, t, a, err := resolveForTask(cmd, d, g, args[0], theme)
			if err != nil {
				return err
			}
			if _, err := a.DeployTask(*s, t, deploy.Target{Method: deploy.Rsync}); err != nil && !strings.HasPrefix(err.Error(), "missing or not right") {
				return err // not on GitHub, deploys already, uncommitted changes, taw/core too old
			}
			return deployWizard(cmd, d, a, *s, t)
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "which TAW theme, when the site has several")
	return cmd
}

// deployWizard asks for the host and its details, makes the key, waits for it
// to be added at the host, tests, then applies (asking first).
func deployWizard(cmd *cobra.Command, d Deps, a *actions.Actions, s site.Site, t site.Theme) error {
	out := cmd.OutOrStdout()
	p := d.palette()
	theme := createform.Theme(style.New(d.Dark))
	if _, err := lipgloss.Fprintln(out, "\n"+lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render("⇪ Deploy "+s.Slug)+
		p.Fg(p.Muted).Render("   to any host: SSH + rsync, or git push")+"\n"); err != nil {
		return err
	}
	var profileID string
	opts := make([]huh.Option[string], 0, len(deploy.Profiles))
	for _, pr := range deploy.Profiles {
		opts = append(opts, huh.NewOption(pr.Name+"  ("+string(pr.Method)+")", pr.ID))
	}
	if err := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Title("Where does it go?").
		Description("A profile only pre-fills and says where each value is; the test decides.").Options(opts...).Value(&profileID))).
		WithTheme(theme).RunWithContext(cmd.Context()); err != nil {
		return cancelled(out, err)
	}
	pr, _ := deploy.FindProfile(profileID)
	target := withProfile(deploy.Target{}, pr, t.Dir)
	hint := func(h string) string { return strings.ReplaceAll(h, "<theme>", t.Dir) }
	var fields []huh.Field
	if target.Method == deploy.Git {
		fields = append(fields,
			huh.NewInput().Title("Git remote").Description(hint(pr.Remote)).Value(&target.Remote),
			huh.NewInput().Title("Branch").Description("the branch the host deploys").Value(&target.Branch),
			huh.NewInput().Title("Path in the host's repository").Description(hint(pr.GitPath)).Value(&target.GitPath))
	} else {
		fields = append(fields,
			huh.NewInput().Title("SSH host").Description(hint(pr.Host)).Value(&target.Host),
			huh.NewInput().Title("SSH port").Description(hint(orText(pr.Port, "22"))).Value(&target.Port),
			huh.NewInput().Title("SSH user").Description(hint(pr.User)).Value(&target.User),
			huh.NewInput().Title("The theme's folder on the server").Description(hint(pr.Path)).Value(&target.Path))
	}
	target.URL = a.Config.Site(s.Slug).ProductionURL
	fields = append(fields, huh.NewInput().Title("Production URL").Description("https://… (it goes in your settings)").Value(&target.URL))
	for {
		if err := huh.NewForm(huh.NewGroup(fields...).Title(pr.Name)).WithTheme(theme).RunWithContext(cmd.Context()); err != nil {
			return cancelled(out, err)
		}
		target.Host, target.User, target.Path = strings.TrimSpace(target.Host), strings.TrimSpace(target.User), strings.TrimSpace(target.Path)
		target.Remote, target.URL = strings.TrimSpace(target.Remote), strings.TrimRight(strings.TrimSpace(target.URL), "/")
		err := target.Check()
		if err == nil {
			break
		}
		if _, err := lipgloss.Fprintln(out, p.Fg(p.Err).Render("✗ "+err.Error())); err != nil {
			return err
		}
	}

	key, _, err := a.DeployKey(cmd.Context(), s.Slug)
	if err != nil {
		return err
	}
	copied := ""
	if a.Copy(cmd.Context(), key.Public) == nil {
		copied = p.Fg(p.Muted).Render("  (copied)")
	}
	if _, err := lipgloss.Fprintf(out, "\n%s\n%s%s\n%s\n\n%s %s\n\n", lipgloss.NewStyle().Bold(true).Render("Add this public key at the host:"),
		key.Public, copied, p.Fg(p.Muted).Render("fingerprint "+key.Fingerprint()), p.Fg(p.Accent).Render("Where:"), pr.Key); err != nil {
		return err
	}
	for {
		ready := true
		if err := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("Added it? Test the connection").
			Affirmative("Test").Negative("Stop here").Value(&ready))).WithTheme(theme).RunWithContext(cmd.Context()); err != nil || !ready {
			_, err := fmt.Fprintf(out, "Stopped. The key stays in the Keychain: taw-fleet deploy setup %s carries on.\n", s.Slug)
			return err
		}
		res, err := a.DeployTester().Test(cmd.Context(), target, key)
		if err == nil {
			if _, err := lipgloss.Fprintln(out, p.Fg(p.OK).Render("✓ ")+res.Said); err != nil {
				return err
			}
			break
		}
		if _, err := lipgloss.Fprintln(out, p.Fg(p.Err).Render("✗ ")+err.Error()); err != nil {
			return err
		}
	}
	return runDeploy(cmd, d, a, s, t, target, false)
}

func orText(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func cancelled(out interface{ Write([]byte) (int, error) }, err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		_, err := fmt.Fprintln(out, "Nothing changed.")
		return err
	}
	return err
}
