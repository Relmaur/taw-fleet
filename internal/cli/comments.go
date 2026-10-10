package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/bugsmash"
	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/style"
)

// userAgent identifies taw-fleet to BugSmash (whose CDN refuses some
// generic agents).
const userAgent = "taw-fleet (+https://github.com/Relmaur/taw-fleet)"

func (d Deps) keyStore() bugsmash.KeyStore { return bugsmash.KeyStore{Exec: d.Runner} }

// feedbackProber builds the BugSmash prober from the stored API key.
// bugsmash.ErrNoKey when there is none.
func (d Deps) feedbackProber(ctx context.Context) (*bugsmash.Prober, error) {
	if d.Feedback != nil {
		return d.Feedback(ctx)
	}
	key, _, err := d.keyStore().Load(ctx, d.Paths)
	if err != nil {
		return nil, err
	}
	return &bugsmash.Prober{Client: &bugsmash.Client{Key: key, UserAgent: userAgent}, CacheDir: d.Paths.CacheDir}, nil
}

// feedbackTargets are the sites with a bugsmash_project in the config.
func feedbackTargets(cfg config.Config) []bugsmash.Target {
	var out []bugsmash.Target
	for slug, s := range cfg.Sites {
		if s.BugSmashProject != "" {
			out = append(out, bugsmash.Target{Slug: slug, Project: s.BugSmashProject})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// checkFeedback reads the targets' open comments (cached unless fresh).
// Without an API key every target says so, rather than nothing showing.
func (d Deps) checkFeedback(ctx context.Context, targets []bugsmash.Target, fresh bool) (map[string]site.Feedback, error) {
	pr, err := d.feedbackProber(ctx)
	if errors.Is(err, bugsmash.ErrNoKey) {
		return bugsmash.NoKey(targets, time.Now()), nil
	}
	if err != nil {
		return nil, err
	}
	return pr.CheckAll(ctx, targets, fresh), nil
}

// applyFeedback puts the open comments on the report, for doctor and show.
func (d Deps) applyFeedback(ctx context.Context, rep *scan.Report, fresh bool) {
	cfg, err := config.Load(d.Paths)
	if err != nil {
		return
	}
	targets := feedbackTargets(cfg)
	if len(targets) == 0 {
		return
	}
	res, err := d.checkFeedback(ctx, targets, fresh)
	if err != nil {
		return
	}
	bugsmash.Apply(rep.Sites, res)
}

// feedbackSummary is refresh's BugSmash line.
func feedbackSummary(p style.Palette, res map[string]site.Feedback) string {
	open, sites := 0, 0
	var bad []string
	for slug, f := range res {
		switch {
		case f.Error != "":
			bad = append(bad, slug)
		case f.Open > 0:
			open += f.Open
			sites++
		}
	}
	sort.Strings(bad)
	msg := fmt.Sprintf("%s BugSmash: %d open %s on %d %s", p.Fg(p.OK).Render("✓"), open, plural(open, "comment", "comments"), sites, plural(sites, "site", "sites"))
	if open == 0 {
		msg = p.Fg(p.OK).Render("✓") + " BugSmash: no open comments"
	}
	if len(bad) > 0 {
		msg += p.Fg(p.Warn).Render(" · check " + strings.Join(bad, ", "))
	}
	return msg
}

func newCommentsCmd(d Deps, g *globals) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "comments [site]",
		Short: "Open review comments in each site's BugSmash project",
		Long: "List the comments reviewers left in BugSmash that nobody resolved yet, per site (newest\n" +
			"first). Sites come from bugsmash_project in the config; the API key from the Keychain\n" +
			"(taw-fleet comments key import) or $BUGSMASH_API_KEY. It only reads: resolve them with\n" +
			"the theme's resolve-comments skill (X in the dashboard).",
		Example: "  taw-fleet comments\n  taw-fleet comments chcapital --json\n  taw-fleet comments projects",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(d.Paths)
			if err != nil {
				return err
			}
			targets := feedbackTargets(cfg)
			if len(targets) == 0 {
				return errors.New("no BugSmash projects: add bugsmash_project under [sites.<folder>] in the config (taw-fleet config path; ids from taw-fleet comments projects)")
			}
			if len(args) == 1 {
				t, err := pickFeedbackTarget(cmd.Context(), d, g, targets, args[0])
				if err != nil {
					return err
				}
				targets = []bugsmash.Target{t}
			}
			pr, err := d.feedbackProber(cmd.Context())
			if err != nil {
				return err
			}
			res := pr.CheckAll(cmd.Context(), targets, !g.offline)
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return renderComments(cmd.OutOrStdout(), d.palette(), targets, res, time.Now(), len(args) == 1)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.AddCommand(newCommentsKeyCmd(d), newCommentsProjectsCmd(d))
	return cmd
}

// pickFeedbackTarget finds a target by Local folder, site name or theme.
func pickFeedbackTarget(ctx context.Context, d Deps, g *globals, targets []bugsmash.Target, q string) (bugsmash.Target, error) {
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
			return bugsmash.Target{}, fmt.Errorf("%s has no bugsmash_project in the config", s.Slug)
		}
	}
	return bugsmash.Target{}, fmt.Errorf("no site with a BugSmash project matches %q", q)
}

func renderComments(w io.Writer, p style.Palette, targets []bugsmash.Target, res map[string]site.Feedback, now time.Time, all bool) error {
	muted, bold := p.Fg(p.Muted), lipgloss.NewStyle().Bold(true)
	var b strings.Builder
	for i, t := range targets {
		f := res[t.Slug]
		if i > 0 {
			b.WriteString("\n")
		}
		head := " " + p.Feedback(&f) + " " + bold.Render(t.Slug)
		if f.Project != "" {
			head += "  " + muted.Render(f.Project)
		}
		b.WriteString(head + "\n")
		if f.Error != "" {
			b.WriteString("   " + p.Fg(p.Warn).Render(f.Error) + "\n")
			continue
		}
		if f.Open == 0 {
			b.WriteString("   " + muted.Render("no open comments") + "\n")
			continue
		}
		b.WriteString("   " + fmt.Sprintf("%d open", f.Open) + muted.Render(", the oldest "+render.Ago(now, f.Oldest)) + "\n")
		shown := f.Comments
		if !all {
			shown = shown[:min(len(shown), 3)]
		}
		for _, c := range shown {
			b.WriteString("   " + render.CommentLine(p, c, now, 100) + "\n")
		}
		if more := f.Open - len(shown); more > 0 {
			b.WriteString("   " + muted.Render(fmt.Sprintf("… %d more in BugSmash", more)) + "\n")
		}
	}
	_, err := lipgloss.Fprint(w, b.String())
	return err
}

func newCommentsKeyCmd(d Deps) *cobra.Command {
	key := &cobra.Command{Use: "key", Short: "The BugSmash API key (kept in the macOS Keychain)"}
	imp := &cobra.Command{
		Use:   "import",
		Short: "Store the BugSmash API key (from stdin) in the Keychain",
		Long: "Read the BugSmash API key (BugSmash → Settings → API Key) from stdin and store it in the\n" +
			"login Keychain. It is never written to a file or shown again.",
		Example: "  pbpaste | taw-fleet comments key import\n  taw-fleet comments key import   # asks, without echo",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			secret, err := readSecret(d, cmd.OutOrStdout(), "BugSmash API key (not shown): ")
			if err != nil {
				return err
			}
			if err := d.keyStore().Save(cmd.Context(), secret); err != nil {
				return err
			}
			p := d.palette()
			_, err = lipgloss.Fprintln(cmd.OutOrStdout(), p.Fg(p.OK).Render("✓")+" Stored the BugSmash API key in the Keychain.")
			return err
		},
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "Say where the API key comes from and whether BugSmash accepts it (never the key)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			k, from, err := d.keyStore().Load(cmd.Context(), d.Paths)
			if err != nil {
				return err
			}
			p, out := d.palette(), cmd.OutOrStdout()
			c := &bugsmash.Client{Key: k, UserAgent: userAgent}
			if pr, err := d.feedbackProber(cmd.Context()); err == nil && d.Feedback != nil {
				c = pr.Client
			}
			ps, err := c.Projects(cmd.Context())
			if err != nil {
				_, _ = fmt.Fprintf(out, "key from   %s\n", from)
				return err
			}
			_, err = lipgloss.Fprintf(out, "key from   %s\n%s BugSmash accepts it: %d %s\n", from, p.Fg(p.OK).Render("✓"), len(ps), plural(len(ps), "project", "projects"))
			return err
		},
	}
	key.AddCommand(imp, show)
	return key
}

func newCommentsProjectsCmd(d Deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "List the BugSmash projects and their ids (for bugsmash_project)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pr, err := d.feedbackProber(cmd.Context())
			if err != nil {
				return err
			}
			ps, err := pr.Client.Projects(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), ps)
			}
			cfg, _ := config.Load(d.Paths)
			used := map[string]string{}
			for _, t := range feedbackTargets(cfg) {
				used[t.Project] = t.Slug
			}
			p := d.palette()
			var b strings.Builder
			for _, pj := range ps {
				line := " " + pj.ID + "  " + bold(pj.Name)
				if slug, ok := used[pj.ID]; ok {
					line += "  " + p.Fg(p.OK).Render("← "+slug)
				}
				if pj.ShortURL != "" {
					line += "  " + p.Fg(p.Muted).Render(pj.ShortURL)
				}
				b.WriteString(line + "\n")
			}
			_, err = lipgloss.Fprint(cmd.OutOrStdout(), b.String())
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}
