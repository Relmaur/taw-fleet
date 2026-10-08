package style

import (
	"strconv"
	"strings"
)

// ThemeEnv forces the palette: "light" or "dark".
const ThemeEnv = "TAW_FLEET_THEME"

// IsDark guesses the terminal background without querying the terminal (a
// query blocks for seconds on terminals that don't answer, and needs raw
// mode). Order: $TAW_FLEET_THEME, then $COLORFGBG ("fg;bg", set by iTerm2,
// Konsole, rxvt…), then dark. The palette's colors stay readable on either
// background, so a wrong guess only costs some contrast. The dashboard asks
// the terminal asynchronously instead.
func IsDark(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(ThemeEnv))) {
	case "light":
		return false
	case "dark":
		return true
	}
	if v := getenv("COLORFGBG"); v != "" {
		parts := strings.Split(v, ";")
		if bg, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			// ANSI 0–6 and 8 are dark; 7 and 9–15 are light.
			return bg < 7 || bg == 8
		}
	}
	return true
}
