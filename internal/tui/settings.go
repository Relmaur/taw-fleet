package tui

// The : menu's settings: entries with no key of their own, toggled with
// enter and saved in the settings file at once (like = saves group_by).

// settingOpenAtLogin is the open_at_login entry.
const settingOpenAtLogin = "open_at_login"

// settingItems are the : menu's settings entries.
func (m Model) settingItems() []paletteItem {
	state, next := "off", "on"
	if m.openAtLogin {
		state, next = "on", "off"
	}
	it := paletteItem{
		group: "Settings", title: "Open at login: " + state, badge: "setting", setting: settingOpenAtLogin,
		alias:  "login startup",
		detail: "Open the dashboard in its own window each time you log in to this Mac. enter turns it " + next + ": saved in the settings (open_at_login), with a login agent in ~/Library/LaunchAgents. Nothing opens now; from your next login on. Also taw-fleet config open-at-login on|off.",
	}
	if m.deps.SaveOpenAtLogin == nil {
		it.disabled = "the settings file doesn't load: fix it first (taw-fleet config show)"
	}
	return []paletteItem{it}
}

// runSetting toggles a settings entry and saves it.
func (m Model) runSetting(name string) Model {
	if name != settingOpenAtLogin {
		return m
	}
	on := !m.openAtLogin
	if err := m.deps.SaveOpenAtLogin(on); err != nil {
		m.setFlash("open at login: "+err.Error(), true)
		return m
	}
	m.openAtLogin = on
	if on {
		m.setTip("Open at login is on: the dashboard opens in its own window at your next login.")
	} else {
		m.setTip("Open at login is off: the login agent is removed.")
	}
	return m
}
