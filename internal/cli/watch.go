package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/watch"
)

// exePath is the taw-fleet that the agent and a notification's click run:
// Homebrew's link rather than the versioned folder it points into, so they
// keep working after an upgrade.
func (d Deps) exePath() (string, error) {
	exe := d.Executable
	if exe == nil {
		exe = os.Executable
	}
	path, err := exe()
	if err != nil {
		return "", err
	}
	if strings.Contains(path, "/Caskroom/") || strings.Contains(path, "/Cellar/") {
		if link, err := d.Paths.LookPath("taw-fleet"); err == nil {
			return link, nil
		}
	}
	return path, nil
}

func (d Deps) agent() (watch.Agent, error) {
	exe, err := d.exePath()
	return watch.Agent{Paths: d.Paths, Runner: d.Runner, UID: os.Getuid(), Exe: exe}, err
}

func (d Deps) notifier() watch.Notifier {
	n := watch.Notifier{Runner: d.Runner, LookPath: d.Paths.LookPath}
	if exe, err := d.exePath(); err == nil {
		n.Open = func(slug string) []string {
			if slug == "" {
				return []string{exe}
			}
			return []string{exe, "--site", slug}
		}
	}
	return n
}

// observe is the fleet with everything watch compares: each theme's pull
// requests, CI and deploys, each production site, each site's open comments;
// all asked now, past the caches. What couldn't be asked comes back as
// problems (one line each, no repeats).
func (d Deps) observe(cmd *cobra.Command, g *globals) ([]site.Site, []string, error) {
	offline := *g
	offline.offline = true
	rep, err := d.scanner(&offline).Run(cmd.Context())
	if err != nil {
		return nil, nil, err
	}
	d.applyRepos(cmd, g, &rep)
	d.applyLive(cmd.Context(), &rep, true)
	d.applyFeedback(cmd.Context(), &rep, true)
	seen := map[string]bool{}
	var problems []string
	note := func(p string) {
		if !seen[p] {
			seen[p] = true
			problems = append(problems, p)
		}
	}
	for _, s := range rep.Sites {
		for _, t := range s.TAWThemes() {
			if t.GitHub != nil && t.GitHub.Error != "" {
				note(s.Slug + ": GitHub " + t.GitHub.Repo + ": " + t.GitHub.Error)
			}
		}
		if f := s.Feedback; f != nil && f.Error != "" && f.ErrorKind != "no-key" {
			note(s.Slug + " comments: " + f.Error)
		}
	}
	return rep.Sites, problems, nil
}

// check is one watch: compare, save, notify. It returns what changed,
// whether this was the first check (which only notes what it sees), what it
// couldn't ask, and whether that's the same as last time.
func (d Deps) check(cmd *cobra.Command, g *globals, notify bool) ([]watch.Event, bool, []string, bool, error) {
	prev, err := watch.Load(d.Paths)
	if err != nil {
		return nil, false, nil, false, fmt.Errorf("%w (delete it to start over: the next check notes the fleet again)", err)
	}
	sites, problems, err := d.observe(cmd, g)
	if err != nil {
		return nil, false, problems, false, err
	}
	next, events := watch.Check(prev, sites, time.Now())
	next.Problems = problems
	same := slices.Equal(prev.Problems, problems)
	if err := watch.Save(d.Paths, next); err != nil {
		return events, false, problems, same, err
	}
	if notify {
		n := d.notifier()
		for _, e := range events {
			if err := n.Send(cmd.Context(), e); err != nil {
				problems = append(problems, "notification: "+err.Error())
				if errors.Is(err, watch.ErrNotAllowed) {
					break // said once; the rest go out the same way
				}
			}
		}
	}
	return events, prev.Checked.IsZero(), problems, same, nil
}

func newWatchCmd(d Deps, g *globals) *cobra.Command {
	var once, quiet, asJSON bool
	var every time.Duration
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "A Mac notification when something in the fleet needs you",
		Long: "Checks the fleet and tells you, once, what changed since the last check:\n" +
			"  - a deploy failed, or CI failed on a theme's default branch;\n" +
			"  - a production site stopped answering (two checks in a row), or came back;\n" +
			"  - a production site answered without its pinned key's signature;\n" +
			"  - new client comments in BugSmash;\n" +
			"  - an update's pull request passed its checks and is ready to merge.\n" +
			"Each is a macOS notification; clicking it opens the dashboard on that site\n" +
			"(with terminal-notifier, which the Homebrew cask installs). The first check only notes\n" +
			"what it sees.\n\n" +
			"taw-fleet watch install runs it in the background every 5 minutes, with this Mac's\n" +
			"login (a launchd agent). taw-fleet watch alone checks every 5 minutes in this\n" +
			"terminal; --once checks once.",
		Example: "  taw-fleet watch install\n  taw-fleet watch status\n  taw-fleet watch --once\n  taw-fleet watch test",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if every < time.Minute {
				return errors.New("--every: at least 1m")
			}
			run := func() error {
				events, first, problems, same, err := d.check(cmd, g, !quiet)
				if asJSON {
					if events == nil {
						events = []watch.Event{}
					}
					if problems == nil {
						problems = []string{}
					}
					if jerr := writeJSON(cmd.OutOrStdout(), map[string]any{"first": first, "events": events, "problems": problems}); jerr != nil {
						return jerr
					}
					return err
				}
				chatty := !once || d.Interactive
				if same && !chatty {
					problems = nil // the log has them already
				}
				printWatch(cmd, d, events, first, problems, chatty)
				return err
			}
			if once {
				return run()
			}
			if err := run(); err != nil {
				return err
			}
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-cmd.Context().Done():
					return nil
				case <-t.C:
					if err := run(); err != nil {
						_, _ = fmt.Fprintln(d.Err, "taw-fleet watch:", err)
					}
				}
			}
		},
	}
	cmd.Flags().BoolVar(&once, "once", false, "check once and stop (what the background agent runs)")
	cmd.Flags().BoolVar(&quiet, "no-notify", false, "print what changed, without notifications")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON (with --once)")
	cmd.Flags().DurationVar(&every, "every", watch.Every, "how often to check")
	cmd.AddCommand(newWatchInstallCmd(d), newWatchUninstallCmd(d), newWatchStatusCmd(d), newWatchTestCmd(d, g))
	return cmd
}

// printWatch prints a check. Under launchd (chatty false) a check with
// nothing to say prints nothing, so the log only holds news.
func printWatch(cmd *cobra.Command, d Deps, events []watch.Event, first bool, problems []string, chatty bool) {
	p, now := d.palette(), time.Now()
	stamp := p.Fg(p.Muted).Render(now.Format("2006-01-02 15:04"))
	var b strings.Builder
	switch {
	case first:
		b.WriteString(stamp + "  First check: noted the fleet as it is. From now on, each change is told once.\n")
	case len(events) == 0 && chatty:
		b.WriteString(stamp + "  Nothing new.\n")
	}
	for _, e := range events {
		mark := p.Fg(p.Accent).Render("●")
		if e.Loud() {
			mark = p.Fg(p.Err).Render("●")
		}
		b.WriteString(stamp + "  " + mark + " " + lipgloss.NewStyle().Bold(true).Render(e.Title) + "  " + e.Body + "\n")
		if e.URL != "" {
			b.WriteString(strings.Repeat(" ", 20) + p.Fg(p.Muted).Render(e.URL) + "\n")
		}
	}
	for _, pr := range problems {
		b.WriteString(stamp + "  " + p.Fg(p.Warn).Render("couldn't check: ") + pr + "\n")
	}
	_, _ = lipgloss.Fprint(cmd.OutOrStdout(), b.String())
}

func newWatchInstallCmd(d Deps) *cobra.Command {
	var every time.Duration
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Check in the background every 5 minutes, with this Mac's login (a launchd agent)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := d.agent()
			if err != nil {
				return err
			}
			if err := a.Install(cmd.Context(), every); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Watching the fleet every %s, in the background (also after a restart).\n", words(every))
			_, _ = fmt.Fprintf(out, "The first check runs now and only notes the fleet as it is.\n")
			if !d.notifier().Clickable() {
				_, _ = fmt.Fprintln(out, "For notifications that open the dashboard when clicked: brew install terminal-notifier")
			}
			_, _ = fmt.Fprintln(out, "taw-fleet watch status shows what it's seen; taw-fleet watch uninstall stops it.")
			return nil
		},
	}
	cmd.Flags().DurationVar(&every, "every", watch.Every, "how often to check")
	return cmd
}

func newWatchUninstallCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop checking in the background",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := d.agent()
			if err != nil {
				return err
			}
			was := a.Status(cmd.Context()).Installed
			if err := a.Uninstall(cmd.Context()); err != nil {
				return err
			}
			msg := "Stopped: nothing checks the fleet in the background now."
			if !was {
				msg = "Nothing was checking in the background."
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), msg)
			return err
		},
	}
}

func newWatchStatusCmd(d Deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Whether watch runs in the background, its last check, and what it told",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := d.agent()
			if err != nil {
				return err
			}
			ag := a.Status(cmd.Context())
			st, err := watch.Load(d.Paths)
			if err != nil {
				return err
			}
			clickable := d.notifier().Clickable()
			if asJSON {
				recent := st.Recent
				if recent == nil {
					recent = []watch.Event{}
				}
				return writeJSON(cmd.OutOrStdout(), map[string]any{
					"agent": ag, "every_seconds": int(ag.Every.Seconds()), "checked_at": st.Checked,
					"clickable": clickable, "log": a.Log(), "recent": recent,
				})
			}
			p, now := d.palette(), time.Now()
			key := func(k string) string { return p.Fg(p.Muted).Render(fmt.Sprintf("%-15s", k)) }
			var b strings.Builder
			switch {
			case ag.Loaded:
				b.WriteString(key("Background") + p.Fg(p.OK).Render("on") + fmt.Sprintf(", every %s\n", words(ag.Every)))
			case ag.Installed:
				b.WriteString(key("Background") + p.Fg(p.Warn).Render("installed but not running") + " (taw-fleet watch install starts it again)\n")
			default:
				b.WriteString(key("Background") + "off (taw-fleet watch install)\n")
			}
			if st.Checked.IsZero() {
				b.WriteString(key("Last check") + "never\n")
			} else {
				b.WriteString(key("Last check") + words(now.Sub(st.Checked)) + " ago" + p.Fg(p.Muted).Render(" ("+st.Checked.Format("Jan 2 15:04")+")") + "\n")
			}
			if clickable {
				b.WriteString(key("Notifications") + "clicking one opens the dashboard on its site\n")
			} else {
				b.WriteString(key("Notifications") + "plain (brew install terminal-notifier: clicking one then opens the dashboard)\n")
			}
			b.WriteString(key("Log") + a.Log() + "\n")
			for _, pr := range st.Problems {
				b.WriteString(key("Can't check") + pr + "\n")
			}
			if len(st.Recent) > 0 {
				b.WriteString("\nTold lately:\n")
				for i, e := range st.Recent {
					if i == 10 {
						break
					}
					b.WriteString("  " + p.Fg(p.Muted).Render(e.At.Format("Jan 2 15:04")) + "  " + lipgloss.NewStyle().Bold(true).Render(e.Title) + "  " + e.Body + "\n")
				}
			}
			_, err = lipgloss.Fprint(cmd.OutOrStdout(), b.String())
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func newWatchTestCmd(d Deps, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "test [site]",
		Short: "Send a notification, to see that they arrive (and where a click goes)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e := watch.Event{Kind: "test", Title: "Notifications work", Body: "This is how taw-fleet watch tells you something changed. Clicking it opens the dashboard."}
			if len(args) == 1 {
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
				e.Site, e.Name = s.Slug, s.Name
				e.Body = "This is how taw-fleet watch tells you something changed on " + s.Name + ". Clicking it opens the dashboard on it."
			}
			n := d.notifier()
			err := n.Send(cmd.Context(), e)
			var msg string
			switch {
			case errors.Is(err, watch.ErrNotAllowed):
				msg = "Sent as a plain notification: macOS has notifications off for terminal-notifier, so a click\n" +
					"can't open the dashboard. Turn them on in System Settings → Notifications → terminal-notifier\n" +
					"(Allow notifications, Banners), then run this again."
			case err != nil:
				return err
			case n.Clickable():
				msg = "Sent. Nothing showed up? Allow notifications in System Settings → Notifications → terminal-notifier."
			default:
				msg = "Sent. Nothing showed up? Allow notifications in System Settings → Notifications → Script Editor.\n" +
					"For notifications that open the dashboard when clicked: brew install terminal-notifier"
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), msg)
			return err
		},
	}
}

// words is a duration in words: "5 minutes", "2 hours".
func words(dur time.Duration) string {
	switch m := int(dur.Round(time.Minute).Minutes()); {
	case m < 1:
		return "less than a minute"
	case m == 1:
		return "1 minute"
	case m < 120:
		return fmt.Sprintf("%d minutes", m)
	case m < 48*60:
		return fmt.Sprintf("%d hours", m/60)
	default:
		return fmt.Sprintf("%d days", m/(24*60))
	}
}
