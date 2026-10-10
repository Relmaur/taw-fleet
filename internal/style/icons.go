package style

// Icons are the glyphs the interface marks things with. Two sets: plain
// Unicode symbols, which every Mac's fonts have (the default), and Nerd Font
// icons (config `icons = "nerd"`), for a terminal set to a Nerd Font. Every
// icon is one cell wide in both, so the layout doesn't move between them.
type Icons struct {
	Logo, Sites, Themes, Running, Behind    string
	Local, Live, Feedback, Findings, Update string
	Waiting, Done, Failed, Branch, PR       string
	Site, Theme, Code, Production, Claude   string
	App, Editor, Terminal, Deploy, Report   string
}

// SymbolIcons are plain Unicode symbols: they render with any font.
var SymbolIcons = Icons{
	Logo: "◆", Sites: "⌂", Themes: "◧", Running: "●", Behind: "↑",
	Local: "⌂", Live: "◎", Feedback: "✉", Findings: "⚑", Update: "⟳",
	Waiting: "◷", Done: "✓", Failed: "✗", Branch: "⎇", PR: "⇡",
	Site: "⌂", Theme: "◧", Code: "⎇", Production: "◎", Claude: "✦",
	App: "⚙", Editor: "✎", Terminal: "❯", Deploy: "⇪", Report: "≡",
}

// NerdIcons are Nerd Font glyphs (Font Awesome and Devicons, in the Basic
// Multilingual Plane, so they're one cell wide).
var NerdIcons = Icons{
	Logo: "", Sites: "", Themes: "", Running: "", Behind: "",
	Local: "", Live: "", Feedback: "", Findings: "", Update: "",
	Waiting: "", Done: "", Failed: "", Branch: "", PR: "",
	Site: "", Theme: "", Code: "", Production: "", Claude: "",
	App: "", Editor: "", Terminal: "", Deploy: "", Report: "",
}

// IconSets are the config's "icons" values.
var IconSets = map[string]Icons{"symbols": SymbolIcons, "nerd": NerdIcons}

// WithIcons is the palette with an icon set by name; an unknown name keeps
// the symbols.
func (p Palette) WithIcons(set string) Palette {
	if i, ok := IconSets[set]; ok {
		p.I = i
	}
	return p
}
