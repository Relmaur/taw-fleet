package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Relmaur/taw-fleet/internal/config"
)

// Popups over the list: = picks how the list is grouped, T (or #) picks the
// selected site's tags from the fleet's, with new ones typed below.

type popKind int

const (
	popGroup popKind = iota
	popTags
	popNote // N: a note for the site (notes.go)
)

type popup struct {
	kind   popKind
	site   string // popTags: whose tags
	items  []popItem
	cursor int
	err    string
}

type popItem struct {
	key, label, hint string
	on               bool
}

// openGroupBy opens the grouping popup, on the grouping in use.
func (m Model) openGroupBy() (tea.Model, tea.Cmd) {
	pp := &popup{kind: popGroup}
	for i, g := range m.groupModes() {
		it := popItem{key: g, on: groupWords(g) == groupWords(m.groupBy)}
		switch g {
		case "":
			it.label, it.hint = "None", "the list as it is"
		case "tag":
			it.label, it.hint = "Tag", "under each of its tags"
		case "kind":
			it.label, it.hint = "Kind", "classic or block theme"
		case "production":
			it.label, it.hint = "Production", "live, or local only"
		default:
			p := strings.TrimPrefix(g, "prefix:")
			it.label, it.hint = strings.ToUpper(p[:1])+p[1:], "by its "+p+": tags"
		}
		if it.on {
			pp.cursor = i
		}
		pp.items = append(pp.items, it)
	}
	m.popup = pp
	return m, nil
}

// openTags opens the selected site's tags: the fleet's tags to tick, and a
// line for new ones.
func (m Model) openTags() (tea.Model, tea.Cmd) {
	s, _, ok := m.selectedTheme()
	if !ok {
		return m, nil
	}
	if m.deps.SaveTags == nil {
		m.setFlash("tags can't be saved: the settings didn't load (taw-fleet config show)", true)
		return m, nil
	}
	count := map[string]int{}
	for _, site := range m.rep.Sites {
		for _, t := range site.Tags {
			count[t]++
		}
	}
	mine := map[string]bool{}
	for _, t := range s.Tags {
		mine[t] = true
	}
	pp := &popup{kind: popTags, site: s.Slug}
	for t, n := range count {
		pp.items = append(pp.items, popItem{key: t, label: "#" + t, hint: fmt.Sprintf("%d %s", n, plural(n, "site", "sites")), on: mine[t]})
	}
	sort.Slice(pp.items, func(i, j int) bool { return pp.items[i].key < pp.items[j].key })
	m.popup = pp
	m.tagInput.SetValue("")
	m.tagInput.Prompt = "new: "
	m.tagInput.SetWidth(40)
	return m, m.tagInput.Focus()
}

// onPopupKey moves, picks, ticks, types or closes.
func (m Model) onPopupKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	pp := m.popup
	if pp.kind == popNote {
		return m.onNoteKey(msg)
	}
	k := msg.String()
	switch k {
	case "esc":
		m.popup = nil
		m.tagInput.Blur()
		return m, nil
	case "up", "ctrl+p":
		pp.cursor = max(pp.cursor-1, 0)
		return m, nil
	case "down", "ctrl+n":
		pp.cursor = min(pp.cursor+1, max(len(pp.items)-1, 0))
		return m, nil
	}
	if pp.kind == popGroup {
		switch k {
		case "enter", "space":
			return m.setGroup(pp.items[pp.cursor].key), nil
		case "j":
			pp.cursor = min(pp.cursor+1, len(pp.items)-1)
		case "k":
			pp.cursor = max(pp.cursor-1, 0)
		default:
			if n, err := strconv.Atoi(k); err == nil && n >= 1 && n <= len(pp.items) {
				return m.setGroup(pp.items[n-1].key), nil
			}
		}
		return m, nil
	}

	// Tags: space ticks while nothing is typed; enter adds what's typed, or saves.
	typed := strings.TrimSpace(m.tagInput.Value())
	switch {
	case k == "space" && typed == "" && len(pp.items) > 0:
		pp.items[pp.cursor].on = !pp.items[pp.cursor].on
		pp.err = ""
		return m, nil
	case k == "enter" && typed != "":
		tags, err := config.CleanTags(strings.Fields(strings.ReplaceAll(typed, ",", " ")))
		if err != nil {
			pp.err = err.Error()
			return m, nil
		}
		for _, t := range tags {
			found := false
			for i := range pp.items {
				if pp.items[i].key == t {
					pp.items[i].on, found = true, true
				}
			}
			if !found {
				pp.items = append(pp.items, popItem{key: t, label: "#" + t, hint: "new", on: true})
			}
		}
		sort.SliceStable(pp.items, func(i, j int) bool { return pp.items[i].key < pp.items[j].key })
		m.tagInput.SetValue("")
		pp.err = ""
		return m, nil
	case k == "enter":
		return m.saveTags()
	}
	var cmd tea.Cmd
	m.tagInput, cmd = m.tagInput.Update(msg)
	pp.err = ""
	return m, cmd
}

// saveTags keeps the ticked tags as the site's.
func (m Model) saveTags() (tea.Model, tea.Cmd) {
	pp := m.popup
	var picked []string
	for _, it := range pp.items {
		if it.on {
			picked = append(picked, it.key)
		}
	}
	tags, err := config.CleanTags(picked)
	if err == nil {
		err = m.deps.SaveTags(pp.site, tags)
	}
	if err != nil {
		pp.err = err.Error()
		return m, nil
	}
	for i := range m.rep.Sites {
		if m.rep.Sites[i].Slug == pp.site {
			m.rep.Sites[i].Tags = tags
		}
	}
	m.popup = nil
	m.tagInput.Blur()
	m.applyFilter()
	if len(tags) == 0 {
		m.setFlash(pp.site+" has no tags now.", false)
	} else {
		m.setFlash("Tags saved for "+pp.site+": #"+strings.Join(tags, " #"), false)
	}
	return m, nil
}

// overlay draws the open popup over the body, centred.
func (m Model) overlay(body string) string {
	box := m.popupBox()
	x := max((m.width-lipgloss.Width(box))/2, 0)
	y := max((m.bodyHeight()-lipgloss.Height(box))/3, 0)
	return lipgloss.NewCompositor(lipgloss.NewLayer(body), lipgloss.NewLayer(box).X(x).Y(y).Z(1)).Render()
}

func (m Model) popupBox() string {
	p := m.pal
	muted := p.Fg(p.Muted)
	pp := m.popup
	w := min(60, m.width-6)
	inner := w - 4
	var lines []string
	title := "Group the list by"
	switch pp.kind {
	case popTags:
		title = "Tags · " + pp.site
	case popNote:
		title = noteTitle(pp.site)
	}
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render(title), "")
	if pp.kind == popNote {
		lines = append(lines, m.notePopup(inner)...)
		return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Accent).Padding(0, 1).Width(w).
			Render(strings.Join(lines, "\n"))
	}
	if pp.kind == popTags && len(pp.items) == 0 {
		lines = append(lines, muted.Render("No tags in the fleet yet: type some below."))
	}
	labelW := 0
	for _, it := range pp.items {
		labelW = max(labelW, ansi.StringWidth(it.label))
	}
	for i, it := range pp.items {
		mark := "  "
		if i == pp.cursor {
			mark = p.Fg(p.Accent).Render("▸") + " "
		}
		var box string
		switch {
		case pp.kind == popGroup && it.on:
			box = p.Fg(p.Accent).Render("●") + " "
		case pp.kind == popGroup:
			box = muted.Render("○") + " "
		case it.on:
			box = p.Fg(p.OK).Render("[x]") + " "
		default:
			box = "[ ] "
		}
		num := ""
		if pp.kind == popGroup && i < 9 {
			num = muted.Render(strconv.Itoa(i+1)) + "  "
		}
		label := it.label + strings.Repeat(" ", labelW-ansi.StringWidth(it.label))
		if i == pp.cursor {
			label = lipgloss.NewStyle().Bold(true).Render(label)
		}
		lines = append(lines, ansi.Truncate(mark+num+box+label+"   "+muted.Render(it.hint), inner, "…"))
	}
	if pp.kind == popTags {
		lines = append(lines, "", m.tagInput.View())
		if pp.err != "" {
			lines = append(lines, p.Fg(p.Err).Render(ansi.Truncate("✗ "+pp.err, inner, "…")))
		}
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Accent).Padding(0, 1).Width(w).
		Render(strings.Join(lines, "\n"))
}

// popupKeys are the footer's keys while a popup is open.
type popupKeys struct {
	kind  popKind
	n     int
	typed bool
}

func (k popupKeys) ShortHelp() []key.Binding {
	if k.kind == popNote {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save the note")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		}
	}
	if k.kind == popGroup {
		return []key.Binding{
			key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑↓", "move")),
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "group")),
			key.NewBinding(key.WithKeys("1"), key.WithHelp(fmt.Sprintf("1–%d", k.n), "pick by number")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),
		}
	}
	if k.typed {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "add them")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		}
	}
	return []key.Binding{
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑↓", "move")),
		key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "tick")),
		key.NewBinding(key.WithKeys("a"), key.WithHelp("type", "new tags")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
	}
}

func (k popupKeys) FullHelp() [][]key.Binding { return [][]key.Binding{k.ShortHelp()} }
