package actions

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/notes"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/taw"
)

// NotesFile is a site's notes (~/TAW Fleet/sites/<site>/notes.md).
func (a *Actions) NotesFile(slug string) string {
	return notes.File(a.Paths.FleetDir, slug)
}

// AddNote writes an entry to a site's notes.
func (a *Actions) AddNote(slug, by, text string) error {
	return notes.Add(a.NotesFile(slug), slug, notes.Entry{At: a.now(), By: by, Text: text})
}

// noted is t, with what it did written to the site's notes when it's done:
// its Note, else its Headline, unless it says Unnoted; when it stops, why. A
// task the person cancelled (its context) isn't noted.
func (a *Actions) noted(slug string, t Task) Task {
	run := t.Run
	if run == nil {
		return t
	}
	t.Run = func(ctx context.Context, out io.Writer) (Summary, error) {
		sum, err := run(ctx, out)
		var text string
		switch {
		case err != nil && (ctx.Err() != nil || errors.Is(err, context.Canceled)):
		case err != nil:
			text = t.Title + " stopped: " + firstLine(err.Error())
		case sum.Unnoted:
		case sum.Note != "":
			text = sum.Note
		default:
			text = sum.Headline
		}
		if strings.TrimSpace(text) != "" {
			if nerr := a.AddNote(slug, notes.TawFleet, text); nerr != nil {
				sum.Lines = append(sum.Lines, "note: the site's notes weren't written: "+nerr.Error())
			}
		}
		return sum, err
	}
	return t
}

// noteUpdate writes one theme's update to its site's notes (update all
// notes each theme on its own site, as u does).
func (a *Actions) noteUpdate(ctx context.Context, s site.Site, t site.Theme, rep taw.UpdateReport, err error) {
	var text string
	switch {
	case err != nil && ctx.Err() != nil:
	case err != nil:
		text = "Update " + t.Dir + " stopped: " + firstLine(err.Error())
	case rep.Status != "up-to-date":
		text = summarizeUpdate(t, rep).Headline
	}
	if text != "" {
		_ = a.AddNote(s.Slug, notes.TawFleet, text)
	}
}

// notesShown is how many of the newest notes a first message carries.
const notesShown = 5

// notesContext is the site's newest notes for Claude's first message, and
// how to add one: what happened before, and a way to tell the next session.
func (a *Actions) notesContext(slug string) string {
	es, _ := notes.Read(a.NotesFile(slug))
	var b strings.Builder
	if len(es) > 0 {
		b.WriteString("- Notes on the site, newest first (all: `taw-fleet notes " + slug + "`):\n")
		for _, e := range es[:min(len(es), notesShown)] {
			b.WriteString("  - " + e.At.Format("2006-01-02") + ", " + e.By + ": " + notes.Line(e, 200) + "\n")
		}
	}
	b.WriteString("- When you finish, or find or decide something the next person should know, add a short note: `taw-fleet notes " + slug + " --by claude \"…\"` (or the add_note tool). taw-fleet notes its own updates, merges and publishes.\n")
	return b.String()
}
