package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// exitCode ends the process with a code and no message (wp's own exit code).
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// siteOpTimeout is how long start/stop/restart may take.
const siteOpTimeout = 3 * time.Minute

// runSiteOp asks Local to start, stop or restart a site and waits.
//
// Local's leftover processes (see local.Leftover) make it hang or report a
// port conflict, so they're looked for first, and again when the operation
// fails or a stop ends: then the answer is a *local.LeftoversError naming
// them, not another try against them.
func (d Deps) runSiteOp(parent context.Context, op local.Op, s site.Site) (time.Duration, error) {
	if l := d.leftovers(parent, s, op == local.Stop); len(l) > 0 {
		return 0, &local.LeftoversError{Slug: s.Slug, Op: op, List: l}
	}
	g, err := local.NewGraphQL(d.Paths)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(parent, siteOpTimeout)
	defer cancel()
	took, err := g.Run(ctx, op, s.ID)
	if d.active != nil {
		d.active.Forget(s.ID)
	}
	if err != nil || op == local.Stop {
		if l := d.leftovers(parent, s, false); len(l) > 0 {
			return took, &local.LeftoversError{Slug: s.Slug, Op: op, Done: err == nil, List: l}
		}
	}
	return took, err
}

// leftovers are the leftover processes in s's way; siteOnly leaves out the
// router's (they don't keep a site from stopping). A failed look finds none:
// it must never block an operation by itself.
func (d Deps) leftovers(parent context.Context, s site.Site, siteOnly bool) local.Leftovers {
	if d.Runner == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 15*time.Second)
	defer cancel()
	l, err := local.FindLeftovers(ctx, d.Runner, d.Paths, s)
	if err != nil || !siteOnly {
		return l
	}
	var mine local.Leftovers
	for _, p := range l {
		if !p.Router() {
			mine = append(mine, p)
		}
	}
	return mine
}

var opWords = map[local.Op][3]string{ // verb, -ing, question
	local.Start:   {"start", "Starting", "Start"},
	local.Stop:    {"stop", "Stopping", "Stop"},
	local.Restart: {"restart", "Restarting", "Restart"},
}

func newSiteOpCmd(d Deps, g *globals, op local.Op) *cobra.Command {
	w := opWords[op]
	var yes bool
	cmd := &cobra.Command{
		Use:   w[0] + " <site>",
		Short: w[2] + " a site in Local by Flywheel",
		Long: w[2] + " a site through the Local app (it must be open), and wait until it's done.\n" +
			"Asks first on a terminal; --yes skips the question (needed in scripts).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			offline := *g
			offline.offline = true
			rep, err := d.scanner(&offline).Run(cmd.Context())
			if err != nil {
				return err
			}
			s, err := scan.Resolve(rep.Sites, args[0])
			if err != nil {
				return err
			}
			p, out := d.palette(), cmd.OutOrStdout()
			switch {
			case op == local.Start && s.Status == site.StatusRunning:
				_, err := lipgloss.Fprintln(out, p.Fg(p.OK).Render("✓")+" "+s.Slug+" is already running")
				return err
			case op == local.Stop && s.Status == site.StatusHalted:
				_, err := lipgloss.Fprintln(out, p.Fg(p.OK).Render("✓")+" "+s.Slug+" is already stopped")
				return err
			}
			if !yes {
				ok, err := d.confirm(out, fmt.Sprintf("%s %s?", w[2], s.Slug))
				if err != nil {
					return err
				}
				if !ok {
					_, err := fmt.Fprintln(out, "Nothing changed.")
					return err
				}
			}
			if _, err := lipgloss.Fprintln(out, p.Fg(p.Muted).Render(w[1]+" "+s.Slug+"…")); err != nil {
				return err
			}
			before := s.Status
			took, err := d.runSiteOp(cmd.Context(), op, *s)
			if err != nil {
				return fmt.Errorf("%s %s: %w", w[0], s.Slug, err)
			}
			_, err = lipgloss.Fprintf(out, "%s %s: %s → %s %s\n", p.Fg(p.OK).Render("✓"), s.Slug, before, op.Target(),
				p.Fg(p.Muted).Render(fmt.Sprintf("(%s)", took.Round(time.Second))))
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}

func newUnstickCmd(d Deps, g *globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "unstick <site>",
		Short: "End Local's leftover processes that keep a site from starting or stopping",
		Long: "After a lot of starting and stopping, Local can leave nginx or php-fpm workers behind\n" +
			"whose master is gone. They keep holding the site's PHP socket, its port, or the router's\n" +
			"ports 80 and 443, so the site hangs on start or stop, or Local reports a port conflict,\n" +
			"and restarting Local doesn't help. unstick lists them and ends them (TERM) after asking.\n" +
			"Only Local's own nginx and php-fpm whose parent is gone are touched.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			offline := *g
			offline.offline = true
			rep, err := d.scanner(&offline).Run(cmd.Context())
			if err != nil {
				return err
			}
			s, err := scan.Resolve(rep.Sites, args[0])
			if err != nil {
				return err
			}
			p, out := d.palette(), cmd.OutOrStdout()
			l, err := local.FindLeftovers(cmd.Context(), d.Runner, d.Paths, *s)
			if err != nil {
				return err
			}
			if len(l) == 0 {
				_, err := lipgloss.Fprintln(out, p.Fg(p.OK).Render("✓")+" nothing of Local's is left over in "+s.Slug+"'s way")
				return err
			}
			for _, x := range l {
				if _, err := lipgloss.Fprintf(out, "  %s %d  %s\n", x.Name, x.PID, p.Fg(p.Muted).Render("holds "+x.Holds)); err != nil {
					return err
				}
			}
			if !yes {
				ok, err := d.confirm(out, fmt.Sprintf("End %d leftover %s?", len(l), plural(len(l), "process", "processes")))
				if err != nil {
					return err
				}
				if !ok {
					_, err := fmt.Fprintln(out, "Nothing changed.")
					return err
				}
			}
			left, err := local.EndLeftovers(cmd.Context(), d.Runner, l)
			if err != nil {
				return err
			}
			if len(left) > 0 {
				return fmt.Errorf("still there after 5 s: %s (kill -KILL %s ends them for good)", left, joinInts(left.PIDs()))
			}
			_, err = lipgloss.Fprintf(out, "%s ended %d leftover %s; now %s\n", p.Fg(p.OK).Render("✓"), len(l), plural(len(l), "process", "processes"),
				p.Fg(p.Muted).Render("taw-fleet start "+s.Slug))
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}

func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = fmt.Sprint(n)
	}
	return strings.Join(parts, " ")
}

// confirm asks a y/N question on a terminal. Without one it refuses, so a
// script never changes a site by accident.
func (d Deps) confirm(w io.Writer, question string) (bool, error) {
	if !d.Interactive || d.In == nil {
		return false, errors.New("not a terminal: add --yes to confirm")
	}
	if _, err := fmt.Fprintf(w, "%s [y/N] ", question); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(d.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes", nil
}

func newWPCmd(d Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wp <site> [wp-cli arguments]",
		Short: "Run wp-cli on a site, with its own PHP and database socket",
		Long: "Run wp-cli for a Local site: the site's PHP, Local's wp-cli and the site's MySQL\n" +
			"socket are chosen for you. Everything after the site goes to wp-cli unchanged.",
		Example: "  taw-fleet wp chcapital option get stylesheet\n  taw-fleet wp taw plugin list --format=json\n  taw-fleet wp fsspx shell",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			offline := *g
			offline.offline = true
			rep, err := d.scanner(&offline).Run(cmd.Context())
			if err != nil {
				return err
			}
			s, err := scan.Resolve(rep.Sites, args[0])
			if err != nil {
				return err
			}
			spec, err := local.WPSpec(d.Paths, *s, args[1:])
			if err != nil {
				return err
			}
			spec.Stdin, spec.Stdout, spec.Stderr = d.In, cmd.OutOrStdout(), cmd.ErrOrStderr()
			res, err := d.Runner.Run(cmd.Context(), spec)
			if err != nil {
				return err
			}
			if res.Code != 0 {
				return exitCode(res.Code)
			}
			return nil
		},
	}
	// Flags after the site belong to wp-cli (taw-fleet wp acme post list --format=ids).
	cmd.Flags().SetInterspersed(false)
	return cmd
}
