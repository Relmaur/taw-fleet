package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/companion"
	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/live"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
)

func (d Deps) keychain() live.Keychain { return live.Keychain{Exec: d.Runner} }

// prober builds the live prober from the Keychain key and the pinned site
// keys. ErrNoKey when no key is stored.
func (d Deps) prober(ctx context.Context) (*live.Prober, error) {
	if d.Live != nil {
		return d.Live(ctx)
	}
	key, err := d.keychain().Load(ctx)
	if err != nil {
		return nil, err
	}
	pins, err := live.LoadPins(d.Paths)
	if err != nil {
		return nil, err
	}
	return &live.Prober{Client: companion.NewClient(key), Pins: pins, CacheDir: d.Paths.CacheDir}, nil
}

// liveTargets are the sites with a production_url in the config.
func liveTargets(cfg config.Config) []live.Target {
	var out []live.Target
	for slug, s := range cfg.Sites {
		if s.ProductionURL != "" {
			out = append(out, live.Target{Slug: slug, URL: s.ProductionURL})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// applyLive probes the production sites (cached) onto the report, for
// doctor and show. Without a key or targets it does nothing.
func (d Deps) applyLive(ctx context.Context, rep *scan.Report, fresh bool) {
	cfg, err := config.Load(d.Paths)
	if err != nil {
		return
	}
	targets := liveTargets(cfg)
	if len(targets) == 0 {
		return
	}
	pr, err := d.prober(ctx)
	if err != nil {
		return
	}
	live.Apply(rep.Sites, pr.ProbeAll(ctx, targets, fresh))
}

func newLiveCmd(d Deps, g *globals) *cobra.Command {
	var asJSON bool
	var logs int
	cmd := &cobra.Command{
		Use:   "live [site]",
		Short: "Check the production sites through their TAW companion",
		Long: "Ask each production site's companion plugin (signed, read-only) for its health:\n" +
			"WordPress, PHP, taw/core and companion versions, plugin updates, known vulnerabilities\n" +
			"and recent taw/core log lines. Sites come from production_url in the config; answers\n" +
			"are checked against each site's pinned key (taw-fleet live trust).",
		Example: "  taw-fleet live\n  taw-fleet live chcapital --logs 20\n  taw-fleet live key import < key.txt",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(d.Paths)
			if err != nil {
				return err
			}
			targets := liveTargets(cfg)
			if len(targets) == 0 {
				return errors.New("no production sites: add production_url under [sites.<folder>] in the config (taw-fleet config path)")
			}
			if len(args) == 1 {
				t, err := pickTarget(cmd.Context(), d, g, targets, args[0])
				if err != nil {
					return err
				}
				targets = []live.Target{t}
			}
			pr, err := d.prober(cmd.Context())
			if err != nil {
				return err
			}
			pr.Logs = logs
			results := pr.ProbeAll(cmd.Context(), targets, true)
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), results)
			}
			return renderLive(cmd.OutOrStdout(), d.palette(), targets, results, len(args) == 1)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().IntVar(&logs, "logs", 5, "log lines to fetch per site")
	cmd.AddCommand(newLiveKeyCmd(d), newLiveTrustCmd(d, g))
	return cmd
}

// pickTarget finds a production target by Local folder, site name or theme.
func pickTarget(ctx context.Context, d Deps, g *globals, targets []live.Target, q string) (live.Target, error) {
	for _, t := range targets {
		if t.Slug == q {
			return t, nil
		}
	}
	offline := *g
	offline.offline = true
	rep, err := d.scanner(&offline).Run(ctx)
	if err == nil {
		if s, err := scan.Resolve(rep.Sites, q); err == nil {
			for _, t := range targets {
				if t.Slug == s.Slug {
					return t, nil
				}
			}
			return live.Target{}, fmt.Errorf("%s has no production_url in the config", s.Slug)
		}
	}
	return live.Target{}, fmt.Errorf("no production site matches %q", q)
}

func renderLive(w io.Writer, p style.Palette, targets []live.Target, results map[string]site.Production, detail bool) error {
	muted, bold := p.Fg(p.Muted), lipgloss.NewStyle().Bold(true)
	var b strings.Builder
	for i, t := range targets {
		r := results[t.Slug]
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(" " + p.Live(&r) + " " + bold.Render(t.Slug) + "  " + muted.Render(t.URL) + muted.Render(fmt.Sprintf("  %d ms", r.TookMS)) + "\n")
		if !r.Reachable {
			b.WriteString("   " + p.Fg(p.Err).Render(r.Error) + "\n")
			continue
		}
		trust := p.Fg(p.OK).Render("✓ verified " + r.KeyID)
		if !r.Verified {
			trust = p.Fg(p.Warn).Render("▲ not verified")
		}
		fmt.Fprintf(&b, "   WordPress %s · PHP %s · taw/core %s · companion %s   %s\n",
			r.WP, r.PHP, strings.TrimPrefix(r.TawCore, "v"), r.Companion, trust)
		updates := muted.Render("no plugin updates")
		if n := len(r.PluginUpdates); n > 0 {
			updates = p.Fg(p.Warn).Render(fmt.Sprintf("%d plugin %s waiting", n, plural(n, "update", "updates")))
		}
		vulns := muted.Render("no known vulnerabilities")
		switch {
		case r.Scanner == "" && len(r.Vulns) == 0:
			vulns = muted.Render("no vulnerability scanner")
		case len(r.Vulns) > 0:
			c := p.Warn
			if r.WorstSeverity == "high" || r.WorstSeverity == "critical" {
				c = p.Err
			}
			vulns = p.Fg(c).Render(fmt.Sprintf("%d %s (worst %s)", len(r.Vulns), plural(len(r.Vulns), "vulnerability", "vulnerabilities"), r.WorstSeverity))
		}
		scanner := ""
		if r.Scanner != "" {
			scanner = muted.Render(" per " + r.Scanner)
		}
		plugins := fmt.Sprintf("%d plugins", r.Plugins)
		if !r.HasInventory {
			plugins, updates = muted.Render("plugins unknown"), ""
		}
		if !r.HasVulns {
			vulns, scanner = muted.Render("vulnerabilities unknown"), ""
		}
		b.WriteString("   " + strings.Join(nonEmpty(plugins, updates, vulns+scanner), " · ") + "\n")
		if detail {
			for _, u := range r.PluginUpdates {
				b.WriteString("     ↑ " + u + "\n")
			}
			for _, v := range r.Vulns {
				b.WriteString("     " + p.Fg(p.Err).Render("! ") + v.Component + "  " + v.Severity + "  " + v.Title + "\n")
			}
		}
		if r.Error != "" {
			b.WriteString("   " + p.Fg(p.Warn).Render("▲ "+r.Error) + "\n")
		}
		if len(r.Logs) > 0 {
			b.WriteString("   " + muted.Render("recent log") + "\n")
			for _, l := range r.Logs {
				b.WriteString("     " + muted.Render(l.TS+"  ") + levelStyle(p, l.Level).Render(l.Level) + "  " + l.Code + "  " + muted.Render(l.Message) + "\n")
			}
		}
	}
	_, err := lipgloss.Fprint(w, b.String())
	return err
}

func levelStyle(p style.Palette, level string) lipgloss.Style {
	switch level {
	case "error", "critical", "alert", "emergency":
		return p.Fg(p.Err)
	case "warning":
		return p.Fg(p.Warn)
	}
	return p.Fg(p.Muted)
}

func newLiveKeyCmd(d Deps) *cobra.Command {
	key := &cobra.Command{Use: "key", Short: "The signing key the sites trust (kept in the macOS Keychain)"}
	var keyID string
	imp := &cobra.Command{
		Use:   "import",
		Short: "Store the signing key (from stdin) in the Keychain",
		Long: "Read the fleet's Ed25519 signing key from stdin, base64 (libsodium's 64-byte secret key,\n" +
			"or a 32-byte seed), and store it in the login Keychain. The sites trust its public half\n" +
			"(TAW_HUB_PUBLIC_KEY in wp-config.php). It is never written to a file.",
		Example: "  grep '^HUB_SIGNING_SECRET_KEY=' ~/Herd/taw-hub/.env | cut -d= -f2- | taw-fleet live key import",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			secret, err := readSecret(d, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			k, err := companion.ParseKey(keyID, secret)
			if err != nil {
				return err
			}
			if err := d.keychain().Save(cmd.Context(), k); err != nil {
				return err
			}
			p := d.palette()
			_, err = lipgloss.Fprintln(cmd.OutOrStdout(), p.Fg(p.OK).Render("✓")+" Stored the signing key "+k.ID+" in the Keychain. Public key: "+k.Public())
			return err
		},
	}
	imp.Flags().StringVar(&keyID, "key-id", "hub-local", "the key id the sites expect (TAW_HUB_KEY_ID)")
	show := &cobra.Command{
		Use:   "show",
		Short: "Print the key id and public key (never the secret)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			k, err := d.keychain().Load(cmd.Context())
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "key id      %s\npublic key  %s\n", k.ID, k.Public())
			return err
		},
	}
	var newID string
	var force bool
	gen := &cobra.Command{
		Use:   "new",
		Short: "Generate a new signing key in the Keychain",
		Long: "Generate a new Ed25519 signing key and store it in the login Keychain. The sites trust\n" +
			"it once its public half reaches them (the companion's fleet key, or TAW_HUB_PUBLIC_KEY in\n" +
			"wp-config.php). Replacing a key stops every site trusting taw-fleet until they get the new one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if old, err := d.keychain().Load(cmd.Context()); err == nil && !force {
				return fmt.Errorf("a signing key (%s) is already stored; --force replaces it, and the sites stop trusting taw-fleet until they get the new one", old.ID)
			}
			k, err := companion.GenerateKey(newID)
			if err != nil {
				return err
			}
			if err := d.keychain().Save(cmd.Context(), k); err != nil {
				return err
			}
			p := d.palette()
			_, err = lipgloss.Fprintln(cmd.OutOrStdout(), p.Fg(p.OK).Render("✓")+" New signing key "+k.ID+" stored in the Keychain.\n  public key  "+k.Public())
			return err
		},
	}
	gen.Flags().StringVar(&newID, "key-id", "taw-fleet", "the key id")
	gen.Flags().BoolVar(&force, "force", false, "replace the stored key")
	key.AddCommand(imp, show, gen)
	return key
}

// readSecret reads one line from stdin, without echo when it's a terminal.
func readSecret(d Deps, out io.Writer) (string, error) {
	if f, ok := d.In.(*os.File); ok && term.IsTerminal(f.Fd()) {
		if _, err := fmt.Fprint(out, "Signing key (base64, not shown): "); err != nil {
			return "", err
		}
		b, err := term.ReadPassword(f.Fd())
		_, _ = fmt.Fprintln(out)
		return strings.TrimSpace(string(b)), err
	}
	if d.In == nil {
		return "", errors.New("no input: pipe the key in")
	}
	line, err := bufio.NewReader(d.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if strings.TrimSpace(line) == "" {
		return "", errors.New("no key on stdin")
	}
	return strings.TrimSpace(line), nil
}

func newLiveTrustCmd(d Deps, g *globals) *cobra.Command {
	var keyID, pub string
	var yes bool
	cmd := &cobra.Command{
		Use:   "trust <site>",
		Short: "Pin a production site's key, so its answers are verified",
		Long: "Pin the key a production site signs its answers with. With --key (and --key-id) that key\n" +
			"is pinned as given, e.g. from the old taw-hub database. Without, the site is asked\n" +
			"and the key it presents is shown for you to confirm (trust on first use).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(d.Paths)
			if err != nil {
				return err
			}
			t, err := pickTarget(cmd.Context(), d, g, liveTargets(cfg), args[0])
			if err != nil {
				return err
			}
			pins, err := live.LoadPins(d.Paths)
			if err != nil {
				return err
			}
			p, out := d.palette(), cmd.OutOrStdout()
			sk := companion.SiteKey{ID: keyID, Public: pub}
			if pub == "" {
				pr, err := d.prober(cmd.Context())
				if err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
				defer cancel()
				resp, err := pr.Client.Get(ctx, companion.Site{URL: t.URL}, "health", nil)
				if err != nil {
					return err
				}
				h, err := companion.Decode[companion.Health](resp)
				if err != nil {
					return err
				}
				sk = companion.SiteKey{ID: h.SiteKeyID, Public: h.SitePublicKey}
				// The answer must be signed by the key it presents.
				if _, err := pr.Client.Get(ctx, companion.Site{URL: t.URL, Pinned: &sk}, "health", nil); err != nil {
					return fmt.Errorf("the site's answer isn't signed by the key it presents: %w", err)
				}
				if _, err := lipgloss.Fprintln(out, " "+t.Slug+" ("+t.URL+") presents key "+bold(sk.ID)+"\n "+p.Fg(p.Muted).Render(sk.Public)); err != nil {
					return err
				}
			} else if sk.ID == "" {
				return errors.New("--key needs --key-id (the site's key id, site-…)")
			}
			if old, ok := pins[t.Slug]; ok && old == sk {
				_, err := lipgloss.Fprintln(out, p.Fg(p.OK).Render("✓")+" already pinned")
				return err
			} else if ok {
				if _, err := lipgloss.Fprintln(out, p.Fg(p.Warn).Render("▲")+" this replaces the pinned key "+old.ID); err != nil {
					return err
				}
			}
			if !yes {
				ok, err := d.confirm(out, "Pin this key for "+t.Slug+"?")
				if err != nil {
					return err
				}
				if !ok {
					_, err := fmt.Fprintln(out, "Nothing changed.")
					return err
				}
			}
			pins[t.Slug] = sk
			if err := pins.Save(d.Paths); err != nil {
				return err
			}
			_, err = lipgloss.Fprintln(out, p.Fg(p.OK).Render("✓")+" Pinned "+sk.ID+" for "+t.Slug+" in "+live.PinsFile(d.Paths))
			return err
		},
	}
	cmd.Flags().StringVar(&pub, "key", "", "the site's public key (base64), instead of asking the site")
	cmd.Flags().StringVar(&keyID, "key-id", "", "the site's key id (with --key)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func bold(s string) string { return lipgloss.NewStyle().Bold(true).Render(s) }
