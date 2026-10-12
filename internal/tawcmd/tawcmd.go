// Package tawcmd is the `taw` command: it finds a TAW theme and runs that
// theme's own `vendor/bin/taw`, with the site's PHP and MySQL socket when the
// theme belongs to a Local site. It never carries taw/core's commands itself,
// so every site keeps exactly its own taw/core version (umbrella plan
// docs/plans/taw-command.md).
package tawcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Env is what `taw` reads outside the process. Tests build their own.
type Env struct {
	Paths paths.Paths
	Getwd func() (string, error)
	Sites func(context.Context) ([]site.Site, error) // the Local sites; nil = none
	Err   io.Writer                                  // hints, and the list when not in a theme
}

// Command is the resolved command: what `taw` hands over to.
type Command struct {
	Dir  string   // where it runs
	Path string   // the program (PHP)
	Args []string // argv, Args[0] included
	Env  []string // added to the environment
	Site string   // the Local site's slug, when the theme belongs to one
}

// ErrNotInTheme means there's no theme to run in; the sites were listed.
var ErrNotInTheme = errors.New("not in a TAW theme")

// Resolve works out the command for `taw <args…>`.
func Resolve(ctx context.Context, env Env, args []string) (Command, error) {
	var sites []site.Site
	loadSites := func() []site.Site {
		if sites == nil && env.Sites != nil {
			sites, _ = env.Sites(ctx)
		}
		return sites
	}

	var (
		root    string
		dir     string
		matched *site.Site
	)
	if len(args) > 0 && strings.HasPrefix(args[0], "@") {
		q, theme, _ := strings.Cut(strings.TrimPrefix(args[0], "@"), "/")
		args = args[1:]
		s, t, err := scan.ResolveTheme(loadSites(), q, theme)
		if err != nil {
			if strings.Contains(err.Error(), "add --theme") {
				err = errors.New(strings.Replace(err.Error(), "add --theme <name>", "use @"+q+"/<theme>", 1))
			}
			return Command{}, err
		}
		root, dir, matched = t.RealPath, t.RealPath, s
	} else {
		wd, err := env.Getwd()
		if err != nil {
			return Command{}, err
		}
		root = themeRoot(wd)
		if root == "" {
			listSites(env.Err, loadSites())
			return Command{}, ErrNotInTheme
		}
		dir = wd // relative paths in the arguments keep working
	}

	script, legacy := entry(root)
	switch {
	case script == "":
		return Command{}, fmt.Errorf("%s has no vendor/: run composer install there", root)
	case legacy:
		dir = root // bin/taw before taw/core 1.90 expects the theme folder
	}

	if matched == nil {
		matched = siteOf(loadSites(), root)
	}
	cmd := Command{Dir: dir}
	php := ""
	if matched != nil {
		cmd.Site = matched.Slug
		if bin, ok := local.PHPBinary(env.Paths, matched.PHPVersion); ok {
			php = bin
		}
	}
	if php == "" {
		lp, err := env.Paths.LookPath("php")
		if err != nil {
			return Command{}, errors.New("no php: neither the site's PHP from Local nor one on the PATH")
		}
		php = lp
	}
	cmd.Path = php
	cmd.Args = []string{"php"}
	if matched != nil && matched.Socket != "" {
		cmd.Args = append(cmd.Args, "-d", "mysqli.default_socket="+matched.Socket, "-d", "pdo_mysql.default_socket="+matched.Socket)
		cmd.Env = append(cmd.Env, "MYSQL_UNIX_PORT="+matched.Socket)
		if !matched.SockLive {
			_, _ = fmt.Fprintf(env.Err, "taw: %s is halted, so commands that need its database will fail: taw-fleet start %s\n", matched.Slug, matched.Slug)
		}
	}
	cmd.Args = append(append(cmd.Args, script), args...)
	return cmd, nil
}

// themeRoot walks up from dir to the nearest TAW theme: a folder with
// vendor/bin/taw, or, before taw/core 1.90, bin/taw next to composer.json.
// A folder with composer.json and vendor/taw/core but no vendor/bin/taw
// still counts (its vendor/ is broken, which entry reports).
func themeRoot(dir string) string {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if isTheme(d) {
			return d
		}
		if p := filepath.Dir(d); p == d {
			return ""
		}
	}
}

// isTheme: a project that requires taw/core (not taw/core's own package,
// which has a bin/taw too).
func isTheme(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		return false
	}
	var c struct {
		Name    string            `json:"name"`
		Require map[string]string `json:"require"`
	}
	if json.Unmarshal(data, &c) != nil || c.Name == "taw/core" {
		return false
	}
	_, requires := c.Require["taw/core"]
	return requires || isFile(filepath.Join(dir, "vendor", "bin", "taw")) || isFile(filepath.Join(dir, "bin", "taw"))
}

// entry is the theme's taw script: vendor/bin/taw, else (taw/core before
// 1.90) bin/taw, which legacy reports. "" = no vendor/ to run.
func entry(root string) (script string, legacy bool) {
	if s := filepath.Join(root, "vendor", "bin", "taw"); isFile(s) {
		return s, false
	}
	if !isDir(filepath.Join(root, "vendor")) {
		return "", false
	}
	if s := filepath.Join(root, "bin", "taw"); isFile(s) {
		return s, true
	}
	return "", false
}

// siteOf is the Local site whose themes include root, through symlinks too
// (the umbrella's taw-theme is linked into the taw site).
func siteOf(sites []site.Site, root string) *site.Site {
	want := realPath(root)
	for i := range sites {
		for _, t := range sites[i].Themes {
			if t.RealPath != "" && realPath(t.RealPath) == want {
				return &sites[i]
			}
		}
	}
	return nil
}

// listSites says where `taw` can run: every TAW theme, by the shortest name
// `taw @…` takes for it (the theme folder, unless two sites share it).
func listSites(w io.Writer, sites []site.Site) {
	_, _ = fmt.Fprintln(w, "taw: not in a TAW theme. Run it in a theme folder, or name one:")
	seen := map[string]int{}
	for _, s := range sites {
		for _, t := range s.TAWThemes() {
			seen[strings.ToLower(t.Dir)]++
		}
	}
	type row struct{ name, site string }
	var rows []row
	width := 0
	for _, s := range sites {
		for _, t := range s.TAWThemes() {
			name := "@" + t.Dir
			if seen[strings.ToLower(t.Dir)] > 1 {
				name = "@" + s.Slug + "/" + t.Dir
			}
			rows = append(rows, row{name, s.Slug})
			width = max(width, len(name))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(w, "  (no Local site with a TAW theme on this Mac)")
		return
	}
	_, _ = fmt.Fprintln(w)
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, "  taw %-*s  %s\n", width, r.name, r.site)
	}
	_, _ = fmt.Fprintln(w, "\nFor example: taw "+rows[0].name+" list")
}

// Line is the command as a shell line, for TAW_DEBUG: paste it to run the
// same thing by hand.
func (c Command) Line() string {
	var words []string
	for _, e := range c.Env {
		k, v, _ := strings.Cut(e, "=")
		words = append(words, k+"="+shellQuote(v)) // NAME='value': still an assignment
	}
	words = append(words, shellQuote(c.Path))
	for _, a := range c.Args[1:] {
		words = append(words, shellQuote(a))
	}
	return strings.Join(words, " ")
}

func shellQuote(s string) string {
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@+,%", r)
	}
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
