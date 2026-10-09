package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/wpremote"
)

func newWPRemoteCmd(d Deps, g *globals) *cobra.Command {
	var data, file, ctype string
	cmd := &cobra.Command{
		Use:   "wp-remote <site> <GET|POST|PUT|PATCH> <rest-route>",
		Short: "Call the production site's WordPress REST API as its bot user",
		Long: "Call the production site's WordPress REST API as the site's bot user (an Editor with an\n" +
			"Application Password, stored in the Keychain with `wp-remote key import`). The password never\n" +
			"shows: site skills (resolve-comments) write to production through this command.\n\n" +
			"The route is WordPress's, with its query: /wp/v2/pages?slug=nosotros&context=edit. Requests use\n" +
			"the ?rest_route= form and HTTP/1.1, and retry a host's empty answers. The answer's JSON goes to\n" +
			"stdout; a WordPress error exits non-zero with its code and message. There is no DELETE:\n" +
			"taw-fleet never deletes on production.",
		Example: "  taw-fleet wp-remote ls-mxico GET '/wp/v2/pages?slug=nosotros&context=edit'\n" +
			"  taw-fleet wp-remote ls-mxico POST /wp/v2/pages/12 --data @meta.json\n" +
			"  taw-fleet wp-remote ls-mxico POST /wp/v2/media --file hero.webp\n" +
			"  printf 'claude-bot:xxxx xxxx xxxx' | taw-fleet wp-remote key import ls-mxico",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, site, err := d.wpSite(cmd, g, args[0])
			if err != nil {
				return err
			}
			c, err := d.wpClient(cmd, slug, site)
			if err != nil {
				return err
			}
			r := wpremote.Request{Method: args[1], Route: args[2], File: file, Type: ctype}
			if data != "" && file != "" {
				return errors.New("--data and --file can't go together (set alt text in a second call)")
			}
			if data != "" {
				if r.JSON, err = readData(d, data); err != nil {
					return err
				}
			}
			out, err := c.Do(cmd.Context(), r)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return err
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "JSON body: inline, @file, or - for stdin")
	cmd.Flags().StringVar(&file, "file", "", "upload this file as the body (media)")
	cmd.Flags().StringVar(&ctype, "type", "", "the file's content type (default: from its extension)")
	cmd.AddCommand(newWPRemoteKeyCmd(d, g))
	return cmd
}

func newWPRemoteKeyCmd(d Deps, g *globals) *cobra.Command {
	key := &cobra.Command{Use: "key", Short: "The site's WordPress bot credentials (kept in the macOS Keychain)"}
	imp := &cobra.Command{
		Use:   "import <site>",
		Short: `Store "user:application password" (from stdin) for the site's bot`,
		Long: "Read the site's bot user and Application Password as \"user:password\" from stdin and store\n" +
			"them in the login Keychain. They're never written to a file or shown again.\n" +
			"Create the bot in wp-admin: Users → Add New (role Editor), then its Application Passwords.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, _, err := d.wpSite(cmd, g, args[0])
			if err != nil {
				return err
			}
			secret, err := readSecret(d, cmd.OutOrStdout(), "user:application password (not shown): ")
			if err != nil {
				return err
			}
			c, err := wpremote.ParseCreds(secret)
			if err != nil {
				return err
			}
			if err := (wpremote.Store{Exec: d.Runner}).Save(cmd.Context(), slug, c); err != nil {
				return err
			}
			p := d.palette()
			_, err = lipgloss.Fprintf(cmd.OutOrStdout(), "%s Stored %s's bot credentials (%s) in the Keychain. Check them: taw-fleet wp-remote key show %s\n",
				p.Fg(p.OK).Render("✓"), slug, c.User, slug)
			return err
		},
	}
	show := &cobra.Command{
		Use:   "show <site>",
		Short: "Say which bot is stored and whether the site accepts it (never the password)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, site, err := d.wpSite(cmd, g, args[0])
			if err != nil {
				return err
			}
			c, err := d.wpClient(cmd, slug, site)
			if err != nil {
				return err
			}
			p, out := d.palette(), cmd.OutOrStdout()
			if _, err := fmt.Fprintf(out, "site       %s\nbot        %s\n", site, c.Creds.User); err != nil {
				return err
			}
			me, err := c.WhoAmI(cmd.Context())
			if err != nil {
				return err
			}
			_, err = lipgloss.Fprintf(out, "%s %s accepts it: %s (%s)\n", p.Fg(p.OK).Render("✓"), site, me.Name, strings.Join(me.Roles, ", "))
			return err
		},
	}
	key.AddCommand(imp, show)
	return key
}

// wpSite resolves a site name to its Local folder and production URL.
func (d Deps) wpSite(cmd *cobra.Command, g *globals, name string) (slug, site string, err error) {
	offline := *g
	offline.offline = true
	rep, err := d.scanner(&offline).Run(cmd.Context())
	if err != nil {
		return "", "", err
	}
	s, err := scan.Resolve(rep.Sites, name)
	if err != nil {
		return "", "", err
	}
	cfg, err := config.Load(d.Paths)
	if err != nil {
		return "", "", err
	}
	u := cfg.Site(s.Slug).ProductionURL
	if u == "" {
		return "", "", fmt.Errorf("no production URL for %s: add [sites.%s] production_url to %s", s.Slug, s.Slug, config.File(d.Paths))
	}
	return s.Slug, u, nil
}

func (d Deps) wpClient(cmd *cobra.Command, slug, site string) (*wpremote.Client, error) {
	c, err := (wpremote.Store{Exec: d.Runner}).Load(cmd.Context(), slug)
	if errors.Is(err, wpremote.ErrNoCreds) {
		return nil, fmt.Errorf("no WordPress bot credentials for %s: create the bot (wp-admin → Users, role Editor, then an Application Password) and run\n  printf 'user:application password' | taw-fleet wp-remote key import %s", slug, slug)
	}
	if err != nil {
		return nil, err
	}
	return &wpremote.Client{Site: site, Creds: c, HTTP: d.WPHTTP, UserAgent: userAgent}, nil
}

// readData is --data: inline JSON, @file, or - (stdin).
func readData(d Deps, v string) ([]byte, error) {
	switch {
	case v == "-":
		if d.In == nil {
			return nil, errors.New("--data -: nothing on stdin")
		}
		return io.ReadAll(d.In)
	case strings.HasPrefix(v, "@"):
		return os.ReadFile(v[1:])
	}
	return []byte(v), nil
}
