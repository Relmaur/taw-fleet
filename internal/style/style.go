// Package style is taw-fleet's look: one palette for light and dark terminals,
// status dots and badges. The CLI tables and the dashboard share it.
//
// Colors are written as truecolor; Lip Gloss downsamples them to what the
// terminal supports, and drops them entirely when output isn't a terminal or
// NO_COLOR is set.
package style

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/Relmaur/taw-fleet/internal/site"
)

// Palette holds the colors for one background.
type Palette struct {
	Text, Muted, Faint, Accent, Brand color.Color
	OK, Warn, Err, Info               color.Color
	BadgeText                         color.Color
	Selected                          color.Color // the selected row's background
}

// New returns the palette for a dark or light background.
func New(dark bool) Palette {
	ld := lipgloss.LightDark(dark)
	return Palette{
		Text:      ld(lipgloss.Color("#1F2328"), lipgloss.Color("#E6EDF3")),
		Muted:     ld(lipgloss.Color("#59636E"), lipgloss.Color("#9198A1")),
		Faint:     ld(lipgloss.Color("#B7BDC4"), lipgloss.Color("#3D444D")),
		Accent:    ld(lipgloss.Color("#6639BA"), lipgloss.Color("#B392F0")),
		Brand:     ld(lipgloss.Color("#0969DA"), lipgloss.Color("#58A6FF")),
		OK:        ld(lipgloss.Color("#1A7F37"), lipgloss.Color("#3FB950")),
		Warn:      ld(lipgloss.Color("#9A6700"), lipgloss.Color("#D29922")),
		Err:       ld(lipgloss.Color("#CF222E"), lipgloss.Color("#F85149")),
		Info:      ld(lipgloss.Color("#0969DA"), lipgloss.Color("#58A6FF")),
		BadgeText: ld(lipgloss.Color("#FFFFFF"), lipgloss.Color("#0D1117")),
		Selected:  ld(lipgloss.Color("#EFE9FB"), lipgloss.Color("#2E2845")),
	}
}

// Fg is a foreground-only style.
func (p Palette) Fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// Dot is the status marker: ● running, ○ halted, ◐ busy, · unknown.
func (p Palette) Dot(s site.Status) string {
	switch s {
	case site.StatusRunning:
		return p.Fg(p.OK).Render("●")
	case site.StatusHalted:
		return p.Fg(p.Muted).Render("○")
	case site.StatusBusy:
		return p.Fg(p.Warn).Render("◐")
	}
	return p.Fg(p.Faint).Render("·")
}

// StatusText is the status in words, colored like its dot.
func (p Palette) StatusText(s site.Status) string {
	switch s {
	case site.StatusRunning:
		return p.Fg(p.OK).Render("running")
	case site.StatusHalted:
		return p.Fg(p.Muted).Render("halted")
	case site.StatusBusy:
		return p.Fg(p.Warn).Render("busy")
	}
	return p.Fg(p.Faint).Render("unknown")
}

// Badge is a short label on a colored background, e.g. " CLASSIC ".
func (p Palette) Badge(text string, bg color.Color) string {
	return lipgloss.NewStyle().Background(bg).Foreground(p.BadgeText).Bold(true).Padding(0, 1).Render(text)
}

// Kind is a theme's kind as a quiet colored word: classic or block.
func (p Palette) Kind(k site.ThemeKind) string {
	switch k {
	case site.KindClassic:
		return p.Fg(p.Muted).Render("classic")
	case site.KindGutenberg:
		return p.Fg(p.Accent).Render("block")
	}
	return p.Fg(p.Muted).Render("—")
}

// Title is the bold accent heading used at the top of command output.
func (p Palette) Title(s string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render(s)
}
