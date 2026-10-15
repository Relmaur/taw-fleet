package cli

import (
	"errors"
	"fmt"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/login"
)

func (d Deps) loginAgent() (login.Agent, error) {
	exe, err := d.exePath()
	return login.Agent{Paths: d.Paths, Runner: d.Runner, UID: os.Getuid(), Exe: exe}, err
}

// syncLogin puts the login agent in line with the settings' open_at_login
// (set by hand in the file, or a taw-fleet moved since): the dashboard's
// start calls it. Quiet: a failure only means the next start tries again.
func (d Deps) syncLogin(cmd *cobra.Command, cfg config.Config) {
	if a, err := d.loginAgent(); err == nil {
		_, _ = a.Sync(cmd.Context(), cfg.OpenAtLogin)
	}
}

// atLogin is the login agent's run: the dashboard in its own window, unless
// open_at_login was turned off in the file since (then the agent goes too).
func (d Deps) atLogin(cmd *cobra.Command, g *globals) error {
	cfg, err := config.Load(d.Paths)
	if err != nil {
		return err
	}
	if !cfg.OpenAtLogin {
		d.syncLogin(cmd, cfg)
		_, _ = fmt.Fprintln(d.Out, "open_at_login is off: not opening the dashboard.")
		return nil
	}
	if d.openWindow(cmd, g, true) {
		return nil
	}
	return errors.New("couldn't open the dashboard in a new window")
}

// setOpenAtLogin saves open_at_login and adds or removes the login agent
// to match (the config command and the dashboard's : menu).
func (d Deps) setOpenAtLogin(cmd *cobra.Command, on bool) error {
	a, err := d.loginAgent()
	if err != nil {
		return err
	}
	f, err := config.SetTopBool(d.Paths, "open_at_login", on)
	if err != nil {
		return err
	}
	if on {
		err = a.Install() // again: points it at this taw-fleet
	} else {
		err = a.Uninstall(cmd.Context())
	}
	if err != nil {
		return fmt.Errorf("open_at_login = %t is saved in %s, but the login agent: %w", on, f, err)
	}
	return nil
}

func newOpenAtLoginCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "open-at-login [on|off]",
		Short: "Open the dashboard in its own window when you log in to this Mac",
		Long: "on sets open_at_login = true in the settings and adds a login agent\n" +
			"(~/Library/LaunchAgents/" + login.Label + ".plist) that opens the dashboard in a\n" +
			"new window of your terminal at each login. off sets it to false and removes the agent.\n" +
			"macOS may show a \"Background Items Added\" notice the first time; the agent is listed\n" +
			"under System Settings → General → Login Items & Extensions.\n" +
			"Without on or off, it says whether it's on.",
		Example:   "  taw-fleet config open-at-login on\n  taw-fleet config open-at-login off",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			p := d.palette()
			a, err := d.loginAgent()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				cfg, err := config.Load(d.Paths)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "open_at_login: "+loginState(cfg.OpenAtLogin, a))
				return err
			}
			var on bool
			switch args[0] {
			case "on", "true", "yes":
				on = true
			case "off", "false", "no":
			default:
				return fmt.Errorf("open-at-login is on or off, not %q", args[0])
			}
			if err := d.setOpenAtLogin(cmd, on); err != nil {
				return err
			}
			f := config.File(d.Paths)
			msg := p.Fg(p.OK).Render("✓") + fmt.Sprintf(" open_at_login = %t in %s", on, f)
			if on {
				msg += "\n  " + p.Fg(p.Muted).Render("The dashboard opens in its own window at your next login. Turn it off: taw-fleet config open-at-login off")
			} else {
				msg += "\n  " + p.Fg(p.Muted).Render("The login agent is removed.")
			}
			_, err = lipgloss.Fprintln(cmd.OutOrStdout(), msg)
			return err
		},
	}
}

// loginState says whether the dashboard opens at login, and whether the
// agent agrees.
func loginState(on bool, a login.Agent) string {
	installed, _ := a.Installed()
	switch {
	case on && installed:
		return "on (the dashboard opens at login)"
	case on:
		return "on, but the login agent is missing (the dashboard's next start adds it, or taw-fleet config open-at-login on)"
	case installed:
		return "off, but the login agent is still there (taw-fleet config open-at-login off removes it)"
	}
	return "off (taw-fleet config open-at-login on)"
}
