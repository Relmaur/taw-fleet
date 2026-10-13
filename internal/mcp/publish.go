package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Decisions are what to publish and which side wins each conflict, as an
// agent passes them (taw-fleet publish --decisions).
type Decisions struct {
	Include []string                     `json:"include" jsonschema:"the record keys to publish (record_key in the review's JSON)"`
	Sides   map[string]map[string]string `json:"sides,omitempty" jsonschema:"record key → change key → keep (production's) or take (Local's)"`
}

// file writes the decisions where the command reads them. The name comes
// from what they say, so the question's call and the confirmed one run the
// same command (the token is bound to it).
func (d Decisions) file() (string, error) {
	if len(d.Include) == 0 {
		return "", errors.New("decisions.include names no record")
	}
	for _, sides := range d.Sides {
		for _, side := range sides {
			if side != "keep" && side != "take" {
				return "", errors.New(`decisions.sides: each value is "keep" (production's) or "take" (Local's)`)
			}
		}
	}
	data, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	path := filepath.Join(os.TempDir(), "taw-fleet-decisions-"+hex.EncodeToString(sum[:8])+".json")
	return path, os.WriteFile(path, data, 0o600)
}
