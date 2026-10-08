// Package config reads the optional ~/.config/taw-fleet/config.toml. Nothing
// in it is required: taw-fleet works with no file at all.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Relmaur/taw-fleet/internal/paths"
)

// Config is the user's preferences.
type Config struct {
	Editor   string          `toml:"editor"`   // e.g. "Cursor", "code", "PhpStorm"; "" = first installed
	Terminal string          `toml:"terminal"` // e.g. "Ghostty", "iTerm2"; "" = first installed
	Create   Create          `toml:"create"`   // defaults for taw-fleet create
	Sites    map[string]Site `toml:"sites"`    // keyed by site folder name
}

// Create holds the defaults for new sites. Empty = ask, or Local's choice.
type Create struct {
	Kind       string `toml:"kind"`        // "classic" or "block"
	AdminUser  string `toml:"admin_user"`  // WordPress admin username
	AdminEmail string `toml:"admin_email"` // WordPress admin email
	PHP        string `toml:"php"`         // e.g. "8.2.30"; "" = Local's preferred
	WebServer  string `toml:"web_server"`  // e.g. "nginx"; "" = Local's preferred
}

// Site holds what taw-fleet can't learn from Local.
type Site struct {
	ProductionURL string `toml:"production_url"`
	Notes         string `toml:"notes"`
}

// File is the config file's path.
func File(p paths.Paths) string { return filepath.Join(p.ConfigDir, "config.toml") }

// Load reads the config. A missing file is an empty Config, not an error.
func Load(p paths.Paths) (Config, error) {
	var c Config
	data, err := os.ReadFile(File(p))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	meta, err := toml.Decode(string(data), &c)
	if err != nil {
		return c, fmt.Errorf("%s: %w", File(p), err)
	}
	if undec := meta.Undecoded(); len(undec) > 0 {
		var keys []string
		for _, k := range undec {
			keys = append(keys, k.String())
		}
		return c, fmt.Errorf("%s: unknown keys: %s", File(p), strings.Join(keys, ", "))
	}
	switch c.Create.Kind {
	case "", "classic", "block":
	default:
		return c, fmt.Errorf("%s: create.kind must be \"classic\" or \"block\"", File(p))
	}
	for slug, s := range c.Sites {
		if s.ProductionURL != "" && !strings.HasPrefix(s.ProductionURL, "https://") && !strings.HasPrefix(s.ProductionURL, "http://") {
			return c, fmt.Errorf("%s: sites.%s.production_url must start with https://", File(p), slug)
		}
	}
	return c, nil
}

// Site returns the settings for a site folder (zero value when none).
func (c Config) Site(slug string) Site { return c.Sites[slug] }

// Template is what `config init` writes: every key, commented, with examples.
const Template = `# taw-fleet settings. Everything here is optional.
# https://github.com/Relmaur/taw-fleet

# The editor for "open in editor" (e, or taw-fleet open --editor).
# Empty: the first one installed of Cursor, Visual Studio Code, PhpStorm, Zed,
# Sublime Text, Nova.
# editor = "Cursor"

# The terminal for "open a terminal here" (t) and the agent handoff (h, l).
# Empty: the first one installed of Ghostty, iTerm2, Warp, kitty, Terminal.
# terminal = "Ghostty"

# Defaults for taw-fleet create (n in the dashboard). Empty: asked, or
# Local's preferred PHP and web server. The admin password is always
# generated and shown once; it's never stored here.
# [create]
# kind = "classic"            # or "block"
# admin_user = "marco"
# admin_email = "me@example.com"
# php = "8.2.30"
# web_server = "nginx"

# What Local doesn't know about a site, keyed by its folder in ~/Local Sites.
# [sites.ls-mxico]
# production_url = "https://lsmexico.mx"
# notes = "Client: LS México. Deploys from main."
`

// Init writes the template. It refuses to overwrite an existing file.
func Init(p paths.Paths) (string, error) {
	f := File(p)
	if _, err := os.Stat(f); err == nil {
		return f, fmt.Errorf("%s already exists", f)
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return f, err
	}
	return f, os.WriteFile(f, []byte(Template), 0o644)
}
