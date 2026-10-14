package config

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/paths"
)

func TestUnknownKeysFromANewerBinary(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	write(t, p, "future_key = 1\n")
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "unknown keys: future_key") {
		t.Fatalf("a typo stays an error: %v", err)
	}
	if exeInfo == nil {
		t.Skip("no executable to stat")
	}
	statExe = func(string) (os.FileInfo, error) { return nil, errors.New("gone") }
	t.Cleanup(func() { statExe = os.Stat })
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "taw-fleet was updated since this one started") || !strings.Contains(err.Error(), "future_key") {
		t.Errorf("a replaced binary says so: %v", err)
	}
}
