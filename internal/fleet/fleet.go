// Package fleet keeps the fleet folder (~/TAW Fleet/): where Claude works
// on the whole fleet, from the dashboard's chat or a plain `claude` there.
// It holds a CLAUDE.md made from the settings, taw-fleet's MCP server in
// .mcp.json, and the fleet skills, which ship inside taw-fleet and are
// written again whenever they change (plan taw-fleet-product.md § C).
// Everything else in the folder (reports/, CLAUDE.local.md, the person's
// own skills) is left alone.
package fleet

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/agent"
)

//go:embed skills
var skillsFS embed.FS

// Owner marks the skills taw-fleet writes (frontmatter `owner:`): those it
// may replace or remove.
const Owner = "taw-fleet"

// Skill is a fleet skill.
type Skill struct {
	Name, Description string
}

// Skills are the fleet skills taw-fleet ships, by name.
func Skills() []Skill {
	var out []Skill
	entries, _ := skillsFS.ReadDir("skills")
	for _, e := range entries {
		b, err := skillsFS.ReadFile(path.Join("skills", e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		fm := frontmatter(b)
		out = append(out, Skill{Name: fm["name"], Description: fm["description"]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Site is what CLAUDE.md says about a site from the settings.
type Site struct {
	Slug, Production, BugSmash, Language, Notes string
}

// Folder is the fleet folder.
type Folder struct {
	Dir      string // ~/TAW Fleet
	Exe      string // taw-fleet, for .mcp.json
	Version  string
	Settings string // the settings file's path, named in CLAUDE.md
	Home     string // shortens paths under it to ~
}

// Result is what Write changed.
type Result struct {
	Wrote   []string // files written (new or changed), relative to the folder
	Removed []string // fleet skills taw-fleet no longer ships
}

// Write brings the folder up to date: CLAUDE.md, .mcp.json and the fleet
// skills. Files that already say the same are left untouched.
func (f Folder) Write(sites []Site) (Result, error) {
	var r Result
	if err := os.MkdirAll(f.Dir, 0o755); err != nil {
		return r, err
	}
	put := func(rel string, b []byte) error {
		p := filepath.Join(f.Dir, rel)
		if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, b) {
			return nil
		}
		if err := writeFile(p, b); err != nil {
			return err
		}
		r.Wrote = append(r.Wrote, rel)
		return nil
	}
	if err := put("CLAUDE.md", []byte(f.claudeMD(sites))); err != nil {
		return r, err
	}
	mcp, err := f.mcpJSON()
	if err != nil {
		return r, err
	}
	if err := put(".mcp.json", mcp); err != nil {
		return r, err
	}
	shipped := map[string]bool{}
	err = fs.WalkDir(skillsFS, "skills", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := skillsFS.ReadFile(p)
		if err != nil {
			return err
		}
		rel := filepath.Join(".claude", filepath.FromSlash(p))
		shipped[strings.Split(strings.TrimPrefix(p, "skills/"), "/")[0]] = true
		return put(rel, b)
	})
	if err != nil {
		return r, err
	}
	// A fleet skill taw-fleet no longer ships goes; anyone else's stays.
	root := filepath.Join(f.Dir, ".claude", "skills")
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() || shipped[e.Name()] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, e.Name(), "SKILL.md"))
		if err != nil || frontmatter(b)["owner"] != Owner {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			return r, err
		}
		r.Removed = append(r.Removed, filepath.Join(".claude", "skills", e.Name()))
	}
	return r, nil
}

// mcpJSON is .mcp.json with taw-fleet's server, keeping any other server
// already there.
func (f Folder) mcpJSON() ([]byte, error) {
	cfg := map[string]any{}
	if b, err := os.ReadFile(filepath.Join(f.Dir, ".mcp.json")); err == nil {
		if json.Unmarshal(b, &cfg) != nil {
			return nil, fmt.Errorf("%s isn't valid JSON: fix it or remove it, and taw-fleet writes it again", filepath.Join(f.Dir, ".mcp.json"))
		}
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[agent.Server] = map[string]any{"command": f.Exe, "args": []string{"mcp"}}
	cfg["mcpServers"] = servers
	b, err := json.MarshalIndent(cfg, "", "  ")
	return append(b, '\n'), err
}

func (f Folder) claudeMD(sites []Site) string {
	var b strings.Builder
	b.WriteString("# TAW Fleet\n\n")
	b.WriteString("<!-- Written by taw-fleet")
	if f.Version != "" {
		b.WriteString(" " + f.Version)
	}
	b.WriteString(": `taw-fleet init` and the dashboard write it again from the settings. Your own notes go in CLAUDE.local.md, which taw-fleet never touches. -->\n\n")
	b.WriteString(`This folder is where Claude works on the whole TAW fleet: every TAW WordPress site on this Mac
(Local by Flywheel) and their production copies. taw-fleet's dashboard chat (` + "`>`" + `) starts here;
so can ` + "`claude`" + ` in a terminal. For one site, work in its theme folder instead
(` + "`taw-fleet list --json`" + ` has each theme's path).

## How to work

`)
	b.WriteString(agent.Rules(false))
	b.WriteString("\n## Sites with a production copy\n\n")
	if len(sites) == 0 {
		b.WriteString("None in the settings yet. Add `production_url` under `[sites.<folder>]` (see below).\n")
	} else {
		b.WriteString("From the settings; `list_sites` (or `taw-fleet list --json`) has every site, running or not.\n\n")
		b.WriteString("| Site | Production | BugSmash project | Report language | Notes |\n|---|---|---|---|---|\n")
		for _, s := range sites {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", s.Slug, dash(s.Production), dash(s.BugSmash), dash(s.Language), dash(cell(s.Notes)))
		}
	}
	b.WriteString("\n## Fleet skills\n\n")
	for _, s := range Skills() {
		what, _, _ := strings.Cut(s.Description, " Triggers on")
		fmt.Fprintf(&b, "- **%s**: %s\n", s.Name, what)
	}
	b.WriteString(`
## Where things are

- **Settings:** ` + "`" + f.settings() + "`" + ` (` + "`taw-fleet config show`" + `). Unknown keys are errors.
- **Sites:** ` + "`~/Local Sites/<folder>/`" + `; each theme's path from ` + "`taw-fleet list --json`" + `.
- **Monthly reports:** ` + "`reports/<site>/<month>.{md,html}`" + ` in this folder.
- **New sites:** ` + "`sites/<slug>/`" + ` in this folder: the brief, the reference and build screenshots, the
  review page (the new-site skill). Never in the theme.
- **Restore points:** ` + "`~/Library/Application Support/taw-fleet/restore/`" + `. They hold database copies:
  never copy, move or read them into a site, a theme or anything git or a sync reaches. Use
  ` + "`restore_points`" + ` and ` + "`restore_site`" + `.
- **Keys:** in the macOS Keychain, through taw-fleet (` + "`live key`" + `, ` + "`comments key`" + `, ` + "`wp-remote key`" + `,
  ` + "`report key`" + `). Never print, copy or store a key anywhere else.
- **Docs:** ` + "`taw-fleet help`" + `, and the taw-fleet pages of the TAW docs.
`)
	return b.String()
}

func (f Folder) settings() string {
	if f.Settings == "" {
		return "~/.config/taw-fleet/config.toml"
	}
	if f.Home != "" && strings.HasPrefix(f.Settings, f.Home+"/") {
		return "~" + strings.TrimPrefix(f.Settings, f.Home)
	}
	return f.Settings
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func cell(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", "/")
}

func writeFile(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// frontmatter reads a SKILL.md's one-line "key: value" frontmatter.
func frontmatter(b []byte) map[string]string {
	out := map[string]string{}
	lines := strings.Split(string(b), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return out
	}
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "---" {
			break
		}
		if k, v, ok := strings.Cut(l, ":"); ok && !strings.HasPrefix(k, " ") {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}
