package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/notes"
	"github.com/Relmaur/taw-fleet/internal/render"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// A site's notes (~/TAW Fleet/sites/<site>/notes.md): what happened on it,
// written by the owner (N), by Claude and by taw-fleet's own tasks. The
// details show the newest in a NOTES section.

// notesMsg is every site's notes, newest first, read after a scan and after
// a task (a task writes its own).
type notesMsg map[string][]notes.Entry

// notesShown is how many notes the NOTES section and the popup show.
const notesShown = 3

func (m Model) loadNotes() tea.Cmd {
	read := m.deps.Notes
	if read == nil {
		return nil
	}
	var slugs []string
	for _, s := range m.rep.Sites {
		slugs = append(slugs, s.Slug)
	}
	return func() tea.Msg {
		out := notesMsg{}
		for _, slug := range slugs {
			if es, err := read(slug); err == nil && len(es) > 0 {
				out[slug] = es
			}
		}
		return out
	}
}

// openNote is N: the selected site's newest notes and a line for a new one.
func (m Model) openNote() (tea.Model, tea.Cmd) {
	s, _, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	if m.deps.AddNote == nil {
		m.setFlash("notes aren't available here", true)
		return m, nil
	}
	m.popup = &popup{kind: popNote, site: s.Slug}
	m.noteInput.SetValue("")
	m.noteInput.SetWidth(min(60, m.width-6) - 6)
	return m, m.noteInput.Focus()
}

// onNoteKey types the note; enter saves it, esc closes.
func (m Model) onNoteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	pp := m.popup
	switch msg.String() {
	case "esc":
		m.popup = nil
		m.noteInput.Blur()
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.noteInput.Value())
		if text == "" {
			pp.err = "type the note first (esc closes)"
			return m, nil
		}
		if err := m.deps.AddNote(pp.site, text); err != nil {
			pp.err = err.Error()
			return m, nil
		}
		m.popup = nil
		m.noteInput.Blur()
		m.setFlash("Note added to "+pp.site+".", false)
		return m, m.loadNotes()
	}
	var cmd tea.Cmd
	m.noteInput, cmd = m.noteInput.Update(msg)
	pp.err = ""
	return m, cmd
}

// notePopup is the popup's lines under its title.
func (m Model) notePopup(inner int) []string {
	p := m.pal
	muted := p.Fg(p.Muted)
	pp := m.popup
	var lines []string
	es := m.notes[pp.site]
	if len(es) == 0 {
		lines = append(lines, muted.Render("No notes yet. What happened, what was decided, what to"), muted.Render("watch out for: you, Claude and taw-fleet read them."))
	}
	for i, e := range es {
		if i == notesShown {
			lines = append(lines, muted.Render(fmt.Sprintf("and %d older", len(es)-notesShown)))
			break
		}
		lines = append(lines, m.noteLine(e, inner))
	}
	lines = append(lines, "", m.noteInput.View())
	if pp.err != "" {
		lines = append(lines, p.Fg(p.Err).Render(ansi.Truncate("✗ "+pp.err, inner, "…")))
	}
	lines = append(lines, muted.Render(ansi.Truncate("All of them: "+render.Tilde(m.deps.Paths, notes.File(m.deps.Paths.FleetDir, pp.site)), inner, "…")))
	return lines
}

// noteLine is one note: when, who, its first line.
func (m Model) noteLine(e notes.Entry, width int) string {
	p := m.pal
	head := shortAge(m.now.Sub(e.At)) + " ago · " + e.By + "  "
	return p.Fg(p.Muted).Render(head) + notes.Line(e, max(width-ansi.StringWidth(head), 10))
}

// notesSection is the detail pane's NOTES section: the site's newest notes.
func (m Model) notesSection(s site.Site, width int) string {
	es := m.notes[s.Slug]
	if len(es) == 0 && m.deps.AddNote == nil {
		return ""
	}
	p := m.pal
	var lines []string
	if len(es) == 0 {
		lines = append(lines, "   "+p.Fg(p.Muted).Render("none yet: N writes one; taw-fleet notes its updates, merges and publishes"))
	}
	for i, e := range es {
		if i == notesShown {
			lines = append(lines, "   "+p.Fg(p.Muted).Render(fmt.Sprintf("and %d older: taw-fleet notes %s", len(es)-notesShown, s.Slug)))
			break
		}
		lines = append(lines, " "+p.Fg(p.Accent).Render(p.I.Notes)+" "+m.noteLine(e, max(width-3, 20)))
	}
	return "\n" + render.Section(p, p.I.Notes+" NOTES", p.Accent, "N · add one", width) + strings.Join(lines, "\n") + "\n"
}

// noteTitle is the popup's title.
func noteTitle(slug string) string { return "Note · " + slug }
