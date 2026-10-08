// Package composer reads a theme's Composer files: whether it's a TAW theme,
// which kind, and (later) which taw/core it has.
package composer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Relmaur/taw-fleet/internal/site"
)

// CorePackage is the TAW framework every TAW theme requires.
const CorePackage = "taw/core"

// Detection is what a theme folder says about itself.
type Detection struct {
	Package   string // composer.json "name"
	Kind      site.ThemeKind
	IsTAW     bool
	HasBinTaw bool
}

type manifest struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Require map[string]string `json:"require"`
}

// Detect reads <themeDir>/composer.json. A folder without one is simply not a
// TAW theme (no error); an unreadable or invalid one is an error.
//
// Rules:
//   - requires taw/core → TAW theme;
//   - name taw/gutenberg, or a wordpress-theme with theme.json and templates/ → gutenberg;
//   - any other TAW theme → classic (taw/theme and its client forks).
func Detect(themeDir string) (Detection, error) {
	d := Detection{Kind: site.KindOther, HasBinTaw: isFile(filepath.Join(themeDir, "bin", "taw"))}
	data, err := os.ReadFile(filepath.Join(themeDir, "composer.json"))
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return d, fmt.Errorf("composer.json: %w", err)
	}
	d.Package = m.Name
	if _, ok := m.Require[CorePackage]; !ok {
		return d, nil
	}
	d.IsTAW = true
	d.Kind = site.KindClassic
	block := m.Type == "wordpress-theme" &&
		isFile(filepath.Join(themeDir, "theme.json")) &&
		isDir(filepath.Join(themeDir, "templates"))
	if m.Name == "taw/gutenberg" || block {
		d.Kind = site.KindGutenberg
	}
	return d, nil
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
