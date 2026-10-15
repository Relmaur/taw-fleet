package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/notes"
	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/scan"
)

// notesFile is a site's notes file.
func (d Deps) notesFile(slug string) string { return notes.File(d.Paths.FleetDir, slug) }

// newNotesCmd shows or adds a site's notes (N in the dashboard).
func newNotesCmd(d Deps, g *globals) *cobra.Command {
	var asJSON, path bool
	var by string
	var last int
	cmd := &cobra.Command{
		Use:     "notes <site> [text…]",
		Aliases: []string{"note"},
		Short:   "Show a site's notes, or add one (N in the dashboard)",
		Long: "A site's notes say what happened on it, for whoever looks at it next: you, or Claude.\n" +
			"You write them (here, or N in the dashboard), Claude writes them (the add_note tool), and\n" +
			"taw-fleet adds its own after an update, a merge, a publish, an undo, a content pull, a\n" +
			"restore, a scaffold apply, a deploy setup or a new repository. They're kept, newest first,\n" +
			"in ~/TAW Fleet/sites/<site>/notes.md, which you can edit by hand: never in the theme or\n" +
			"its repository. With only the site, prints them; with text, adds it (- reads it from\n" +
			"stdin). Adding doesn't ask: a note only goes into that file.",
		Example: "  taw-fleet notes acme-shop\n  taw-fleet notes acme-shop \"Client asked to hold the redesign until November\"\n" +
			"  taw-fleet notes acme-shop --last 3 --json\n  echo \"DNS moved to Cloudflare\" | taw-fleet notes acme-shop -",
		Args: cobra.MinimumNArgs(1),
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
			file := d.notesFile(s.Slug)
			out := cmd.OutOrStdout()
			if path {
				_, err := fmt.Fprintln(out, file)
				return err
			}
			if len(args) > 1 {
				return addNote(cmd, d, s.Slug, file, args[1:], by, asJSON)
			}
			es, err := notes.Read(file)
			if err != nil {
				return err
			}
			if last > 0 && len(es) > last {
				es = es[:last]
			}
			if asJSON {
				if es == nil {
					es = []notes.Entry{}
				}
				return writeJSON(out, struct {
					Site  string        `json:"site"`
					File  string        `json:"file"`
					Notes []notes.Entry `json:"notes"`
				}{s.Slug, file, es})
			}
			if len(es) == 0 {
				_, err := fmt.Fprintln(out, s.Slug+" has no notes yet. taw-fleet notes "+s.Slug+" \"…\" adds one.")
				return err
			}
			_, err = lipgloss.Fprint(out, renderNotes(d, es, time.Now()))
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().BoolVar(&path, "path", false, "print the notes file's path")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "only the newest N")
	cmd.Flags().StringVar(&by, "by", notes.You, "who writes it: you or claude")
	return cmd
}

func addNote(cmd *cobra.Command, d Deps, slug, file string, words []string, by string, asJSON bool) error {
	text := strings.Join(words, " ")
	if text == "-" {
		data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), notes.MaxText+1))
		if err != nil {
			return err
		}
		text = string(data)
	}
	switch strings.ToLower(strings.TrimSpace(by)) {
	case "you", "":
		by = notes.You
	case "claude":
		by = notes.Claude
	default:
		return errors.New("--by is you or claude")
	}
	e := notes.Entry{At: time.Now(), By: by, Text: text}
	if err := notes.Add(file, slug, e); err != nil {
		return err
	}
	e.Text = strings.TrimSpace(e.Text)
	if asJSON {
		return writeJSON(cmd.OutOrStdout(), struct {
			Site string      `json:"site"`
			File string      `json:"file"`
			Note notes.Entry `json:"note"`
		}{slug, file, e})
	}
	p := d.palette()
	_, err := lipgloss.Fprintln(cmd.OutOrStdout(), p.Fg(p.OK).Render("✓")+" Note added to "+slug+"  "+p.Fg(p.Muted).Render(render.Tilde(d.Paths, file)))
	return err
}

// renderNotes is the notes, newest first: when and who, then the text.
func renderNotes(d Deps, es []notes.Entry, now time.Time) string {
	p := d.palette()
	var b strings.Builder
	for _, e := range es {
		b.WriteString(p.Fg(p.Accent).Render(e.At.Format("2006-01-02 15:04")) + p.Fg(p.Muted).Render(" · "+e.By+" · "+render.Ago(now, e.At)) + "\n")
		for _, l := range strings.Split(e.Text, "\n") {
			b.WriteString("  " + l + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
