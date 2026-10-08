package taw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/site"
)

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func driftFile(cacheDir, slug, dir string) string {
	name := strings.Trim(unsafe.ReplaceAllString(slug+"--"+dir, "-"), "-")
	return filepath.Join(cacheDir, "sync", name+".json")
}

// SaveDrift keeps a theme's last sync result (best effort).
func SaveDrift(cacheDir, slug, dir string, d site.Drift) {
	f := driftFile(cacheDir, slug, dir)
	if os.MkdirAll(filepath.Dir(f), 0o755) != nil {
		return
	}
	data, _ := json.Marshal(d)
	if os.WriteFile(f+".tmp", data, 0o644) == nil {
		_ = os.Rename(f+".tmp", f)
	}
}

// LoadDrift returns the last saved sync result, or nil.
func LoadDrift(cacheDir, slug, dir string) *site.Drift {
	data, err := os.ReadFile(driftFile(cacheDir, slug, dir))
	if err != nil {
		return nil
	}
	var d site.Drift
	if json.Unmarshal(data, &d) != nil || d.At.IsZero() {
		return nil
	}
	return &d
}
