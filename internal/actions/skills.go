package actions

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Skill is one Claude Code skill in a theme's .claude/skills/: the
// framework's (owner taw, from taw/core and the scaffold) or the site's own.
type Skill struct {
	Name        string
	Description string
	Owner       string // taw, site, or "" (unmarked)
	Dir         string
}

// Skills lists the theme's skills by name. Claude Code finds them there when
// it starts in the theme folder.
func Skills(t site.Theme) []Skill {
	root := filepath.Join(t.RealPath, ".claude", "skills")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil || !e.IsDir() {
			continue
		}
		fm := frontmatter(b)
		name := fm["name"]
		if name == "" {
			name = e.Name()
		}
		out = append(out, Skill{Name: name, Description: fm["description"], Owner: fm["owner"], Dir: dir})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// HasSkill reports whether the theme has the named skill.
func HasSkill(t site.Theme, name string) bool {
	return fileExists(filepath.Join(t.RealPath, ".claude", "skills", name, "SKILL.md"))
}

// frontmatter reads a SKILL.md's YAML frontmatter keys, enough for name,
// description and owner: "key: value" (quotes dropped) and folded or literal
// blocks ("key: >" / "key: |" followed by indented lines, joined by spaces).
func frontmatter(b []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return out
	}
	var block string // the key whose indented lines follow
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if block != "" && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			out[block] = strings.TrimSpace(out[block] + " " + strings.TrimSpace(line))
			continue
		}
		block = ""
		key, val, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(key, " ") {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch {
		case val == ">" || val == "|" || val == ">-" || val == "|-":
			block, out[key] = key, ""
		case len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0]:
			out[key] = val[1 : len(val)-1]
		default:
			out[key] = val
		}
	}
	return out
}

// SkillPrompt is the first message when taw-fleet starts Claude Code in the
// theme folder on a skill: use it, with what taw-fleet knows about the site.
func (a *Actions) SkillPrompt(s site.Site, t site.Theme, sk Skill, findings []site.Finding) handoff.Prompt {
	var b strings.Builder
	fmt.Fprintf(&b, "Use the %s skill on this site.\n\n", sk.Name)
	b.WriteString(a.siteContext(s, t, findings))
	return handoff.Prompt{Title: sk.Name + " on " + s.Slug, Text: b.String()}
}

// siteContext is what taw-fleet knows about the site, for a first message.
func (a *Actions) siteContext(s site.Site, t site.Theme, findings []site.Finding) string {
	var b strings.Builder
	b.WriteString("What taw-fleet knows (more: `taw-fleet show " + s.Slug + " --json`):\n")
	fmt.Fprintf(&b, "- Local site: %s (%s, %s)\n", s.Slug, s.URL, s.Status)
	fmt.Fprintf(&b, "- Theme: %s, this folder (%s, taw/core %s)\n", t.Dir, t.Kind, strings.TrimPrefix(t.Core.Installed, "v"))
	if g := t.Git; g != nil && g.Repo != nil {
		branch := g.DefaultBranch
		if branch == "" {
			branch = g.Branch
		}
		fmt.Fprintf(&b, "- Repo: %s, deploy branch %s (on %s now)\n", g.Repo.WebURL(), branch, g.Branch)
	}
	if u := a.Config.Site(s.Slug).ProductionURL; u != "" {
		fmt.Fprintf(&b, "- Production: %s\n", u)
	}
	if f := s.Feedback; f != nil && f.Error == "" {
		fmt.Fprintf(&b, "- BugSmash: %s, project %s, %d open %s", orDash(f.Project), f.ProjectID, f.Open, plural(f.Open, "comment", "comments"))
		if f.URL != "" {
			fmt.Fprintf(&b, ", review page %s", f.URL)
		}
		b.WriteString("\n")
	}
	if len(findings) > 0 {
		b.WriteString("- What `taw-fleet doctor` says:\n")
		for _, f := range findings[:min(len(findings), 6)] {
			fmt.Fprintf(&b, "  - %s (%s)\n", f.Message, f.Code)
		}
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// LaunchSkill opens Claude Code in the theme folder, beside the dashboard
// (like Launch), with the prompt as its first message. kind names the files
// ("comments" lets the dashboard ask BugSmash again when it exits).
func (a *Actions) LaunchSkill(ctx context.Context, s site.Site, t site.Theme, kind string, p handoff.Prompt, beside string) (Launched, error) {
	dir := filepath.Join(a.Paths.CacheDir, "handoff")
	base := fmt.Sprintf("%s-%s-%s", safeName(s.Slug), safeName(kind), a.now().Format("20060102-150405"))
	ls := LaunchScript{Title: p.Title, Dir: t.RealPath,
		Prompt: filepath.Join(dir, base+".md"), Done: filepath.Join(dir, base+".done")}
	used, placed, err := a.launch(ctx, ls, p.Text, filepath.Join(dir, base+".command"), beside)
	if err != nil {
		return Launched{}, err
	}
	msg := fmt.Sprintf("Started Claude Code in %s on %s (%s)", used, t.Dir, p.Title)
	if placed {
		msg = fmt.Sprintf("Claude Code is on %s in the window on the right (%s)", t.Dir, p.Title)
	}
	return Launched{Message: msg, Done: ls.Done}, nil
}
