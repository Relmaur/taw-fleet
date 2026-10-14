package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Tags (T or #): the selected site's tags, picked in a popup (popup.go) and
// saved in the settings. They show on the card, the filter finds them
// (#retainer, tag:client:) and = groups by them.

// tagChips are a card's tags after its second line, cut to fit in width.
func (m Model) tagChips(line string, tags []string, width int) string {
	if len(tags) == 0 {
		return line
	}
	room := width - ansi.StringWidth(line) - 2
	if room < 4 {
		return line
	}
	chips := "#" + strings.Join(tags, " #")
	return line + "  " + m.pal.Fg(m.pal.Muted).Render(ansi.Truncate(chips, room, "…"))
}
