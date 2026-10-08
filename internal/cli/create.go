package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/create"
	"github.com/Relmaur/taw-fleet/internal/createform"
)

func newCreateCmd(d Deps) *cobra.Command {
	var classic, block, yes, asJSON bool
	var f createform.Fields
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a Local site with a new classic or block TAW theme",
		Long: "Create a Local by Flywheel site, install taw-theme (classic) or taw-gutenberg (block) into it\n" +
			"with taw-create, build its assets, activate it and make it a git repository with a first\n" +
			"commit. The WordPress admin password is generated and shown once. Local must be open.\n\n" +
			"In a terminal, anything missing is asked; otherwise give the name, --classic or --block,\n" +
			"--admin-user and --admin-email (or set them in the config's [create] section) and --yes.",
		Example: "  taw-fleet create\n  taw-fleet create \"Acme Shop\" --block\n" +
			"  taw-fleet create \"Acme Shop\" --classic --php 8.2 --admin-user marco --admin-email me@example.com --yes",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := d.actions()
			if err != nil {
				return err
			}
			p, out := d.palette(), cmd.OutOrStdout()
			given := f
			f = createform.Defaults(a.Config.Create)
			kindGiven := a.Config.Create.Kind != ""
			for dst, src := range map[*string]string{&f.Domain: given.Domain, &f.ThemeDir: given.ThemeDir, &f.PHP: given.PHP,
				&f.WebServer: given.WebServer, &f.AdminUser: given.AdminUser, &f.AdminEmail: given.AdminEmail} {
				if src != "" {
					*dst = src
				}
			}
			if len(args) == 1 {
				f.Name = args[0]
			}
			switch {
			case classic && block:
				return errors.New("--classic or --block, not both")
			case classic:
				f.Kind, kindGiven = string(create.Classic), true
			case block:
				f.Kind, kindGiven = string(create.Block), true
			}

			var missing []string
			for flag, v := range map[string]string{"a name": f.Name, "--admin-user": f.AdminUser, "--admin-email": f.AdminEmail} {
				if strings.TrimSpace(v) == "" {
					missing = append(missing, flag)
				}
			}
			if !kindGiven {
				missing = append(missing, "--classic or --block")
			}
			confirmed := yes
			if len(missing) > 0 {
				if !d.Interactive {
					return fmt.Errorf("not a terminal: give %s (or set them in the config's [create] section)", strings.Join(slices.Sorted(slices.Values(missing)), ", "))
				}
				if _, err := lipgloss.Fprintln(out, "\n"+createform.Header(p)+"\n"); err != nil {
					return err
				}
				form := createform.New(d.Paths, d.Dark, &f)
				if err := form.RunWithContext(cmd.Context()); err != nil {
					if errors.Is(err, huh.ErrUserAborted) {
						_, err := fmt.Fprintln(out, "Nothing changed.")
						return err
					}
					return err
				}
				if !f.Confirmed {
					_, err := fmt.Fprintln(out, "Nothing changed.")
					return err
				}
				confirmed = true
			}

			r, err := create.Normalize(d.Paths, f.Request())
			if err != nil {
				return err
			}
			if err := create.Check(d.Paths, r); err != nil {
				return err
			}
			if !confirmed {
				if _, err := lipgloss.Fprintln(out, p.Fg(p.Muted).Render(createform.Summary(f))); err != nil {
					return err
				}
				ok, err := d.confirm(out, "Create "+r.Name+"?")
				if err != nil {
					return err
				}
				if !ok {
					_, err := fmt.Fprintln(out, "Nothing changed.")
					return err
				}
			}
			task, err := a.CreateTask(r)
			if err != nil {
				return err
			}
			sum, err := runTask(cmd, d, task, true, "")
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(out, sum.Report)
			}
			return printSummary(cmd, d, sum)
		},
	}
	cmd.Flags().BoolVar(&classic, "classic", false, "start from taw-theme (PHP blocks, Vite, Tailwind, Alpine)")
	cmd.Flags().BoolVar(&block, "block", false, "start from taw-gutenberg (block theme, full site editing)")
	cmd.Flags().StringVar(&f.Domain, "domain", "", "the site's domain (default <name>.local)")
	cmd.Flags().StringVar(&f.ThemeDir, "theme-dir", "", "the theme's folder in wp-content/themes (default: from the name)")
	cmd.Flags().StringVar(&f.PHP, "php", "", "PHP version Local has downloaded, e.g. 8.2 or 8.2.30 (default: Local's preferred)")
	cmd.Flags().StringVar(&f.WebServer, "web-server", "", "nginx or apache, optionally with a version (default: Local's preferred)")
	cmd.Flags().StringVar(&f.AdminUser, "admin-user", "", "WordPress admin username")
	cmd.Flags().StringVar(&f.AdminEmail, "admin-email", "", "WordPress admin email")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON (includes the password)")
	return cmd
}
