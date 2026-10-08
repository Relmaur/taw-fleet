// Package scan finds sites (through Sources), discovers their themes and fills
// in the details (through Enrichers). A failure in one place becomes an error
// on that site; the scan always returns what it could read.
package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/composer"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Source lists sites. Local is the only one today; a production source (the
// TAW Hub) can be added later without changing the model.
type Source interface {
	Name() string
	Sites(ctx context.Context) ([]site.Site, error)
}

// LocalSource reads Local by Flywheel's registry.
type LocalSource struct {
	Paths paths.Paths
}

// NewLocalSource returns the Local source for these paths.
func NewLocalSource(p paths.Paths) *LocalSource { return &LocalSource{Paths: p} }

// Name implements Source.
func (*LocalSource) Name() string { return "local" }

// Sites implements Source.
func (l *LocalSource) Sites(ctx context.Context) ([]site.Site, error) {
	raws, err := local.LoadRegistry(l.Paths)
	if err != nil {
		return nil, err
	}
	statuses, statusErr := local.LoadStatuses(l.Paths)

	sites := make([]site.Site, 0, len(raws))
	for _, r := range raws {
		if err := ctx.Err(); err != nil {
			return sites, err
		}
		s := site.Site{
			ID:           r.ID,
			Name:         r.Name,
			Path:         r.Path,
			Domain:       r.Domain,
			Source:       "local",
			Status:       site.StatusUnknown,
			PHPVersion:   r.PHPVersion,
			MySQLVersion: r.MySQLVersion,
			HTTPPort:     r.HTTPPort,
			MySQLPort:    r.MySQLPort,
			WebServer:    r.WebServer,
			MultiSite:    r.MultiSite,
			Xdebug:       r.Xdebug,
			Themes:       []site.Theme{},
		}
		if s.Name == "" {
			s.Name = r.ID
		}
		if r.Path != "" {
			s.Slug = filepath.Base(r.Path)
			s.WebRoot = filepath.Join(r.Path, "app", "public")
		}
		if r.Domain != "" {
			s.URL = "http://" + r.Domain
		}
		if st, ok := statuses[r.ID]; ok {
			s.Status = st
		}
		s.Socket = local.SocketPath(l.Paths, r.ID)
		s.SockLive = local.SocketLive(s.Socket)
		for _, h := range r.Hosts {
			s.Hosts = append(s.Hosts, site.HostConnection{HostID: h.HostID, Env: h.Env})
		}
		s.AddError("registry", r.ParseErr)
		s.AddError("statuses", statusErr)

		if s.WebRoot != "" {
			themes, err := DiscoverThemes(s.WebRoot)
			s.Themes = themes
			s.AddError("themes", err)
		}
		sites = append(sites, s)
	}
	return sites, nil
}

// DiscoverThemes lists wp-content/themes under a WordPress root. Symlinked
// themes (the umbrella's taw-theme and taw-gutenberg) are followed; a broken
// link is kept and marked Broken.
func DiscoverThemes(webRoot string) ([]site.Theme, error) {
	dir := filepath.Join(webRoot, "wp-content", "themes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []site.Theme{}, fmt.Errorf("read themes: %w", err)
	}
	themes := []site.Theme{}
	var problems []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		t := site.Theme{Dir: name, Path: path, RealPath: path, Kind: site.KindOther}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Symlink = true
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				t.Broken = true
				themes = append(themes, t)
				continue
			}
			t.RealPath = target
			if info, err = os.Stat(target); err != nil {
				t.Broken = true
				themes = append(themes, t)
				continue
			}
		}
		if !info.IsDir() {
			continue // index.php and other files
		}
		d, err := composer.Detect(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
		}
		t.Package, t.Kind, t.IsTAW, t.HasBinTaw = d.Package, d.Kind, d.IsTAW, d.HasBinTaw
		themes = append(themes, t)
	}
	sort.Slice(themes, func(i, j int) bool { return themes[i].Dir < themes[j].Dir })
	if len(problems) > 0 {
		return themes, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return themes, nil
}
