package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/restore"
)

func TestRestoreListAndRefusedSave(t *testing.T) {
	p := fixture(t)
	out, err := run(t, p, "restore", "acme")
	if err != nil || !strings.Contains(out, "acme has no restore points yet") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	out, err = run(t, p, "restore", "acme", "--json")
	var got struct {
		Keep   int             `json:"keep"`
		Points []restore.Point `json:"points"`
	}
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || got.Keep != 5 || got.Points == nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	// The test runner answers every git command with success, so git says the
	// restore folder is in a repository: nothing may be saved there.
	if _, err := run(t, p, "restore", "acme", "--save", "--theme", "acme-theme"); !errors.Is(err, restore.ErrUnsafe) {
		t.Fatalf("save in a 'repository': %v", err)
	}
	if out, _ := run(t, p, "restore", "acme"); !strings.Contains(out, "no restore points yet") {
		t.Errorf("a refused save left a point:\n%s", out)
	}
	if _, err := run(t, p, "restore", "acme", "latest"); err == nil || !strings.Contains(err.Error(), "no restore points") {
		t.Errorf("restore with none: %v", err)
	}
}
