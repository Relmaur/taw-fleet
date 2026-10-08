package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// LoadStatuses reads site-statuses.json ({id: "running"|"halted"|…}). A missing
// file means Local hasn't recorded any state yet: every site is unknown.
func LoadStatuses(p paths.Paths) (map[string]site.Status, error) {
	data, err := os.ReadFile(filepath.Join(p.LocalSupport, "site-statuses.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]site.Status{}, nil
	}
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("site-statuses.json: %w", err)
	}
	out := make(map[string]site.Status, len(raw))
	for id, v := range raw {
		out[id] = site.ParseStatus(asString(v))
	}
	return out, nil
}

// SocketPath is where Local puts a site's MySQL socket while it runs.
func SocketPath(p paths.Paths, id string) string {
	return filepath.Join(p.LocalSupport, "run", id, "mysql", "mysqld.sock")
}

// SocketLive reports whether the socket exists. It's a socket, not a regular
// file, so this checks existence only.
func SocketLive(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
