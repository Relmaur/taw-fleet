package cli

import (
	"encoding/json"
	"io"
)

// writeJSON prints v as indented JSON. JSON output is never colored.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
