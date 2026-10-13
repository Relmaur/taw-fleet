package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/companion"
	"github.com/Relmaur/taw-fleet/internal/review"
)

func TestPublishCommands(t *testing.T) {
	p := fixture(t)
	if _, err := run(t, p, "publish", "acme-theme"); err == nil || !strings.Contains(err.Error(), "no production URL") {
		t.Errorf("no production: %v", err)
	}
	out, err := run(t, p, "publish", "history", "acme")
	if err != nil || !strings.Contains(out, "No publishes yet: taw-fleet publish acme") {
		t.Errorf("history: %v %q", err, out)
	}
	out, err = run(t, p, "publish", "history", "acme", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Errorf("history --json: %v %q", err, out)
	}
}

func TestChoose(t *testing.T) {
	data, err := os.ReadFile("../review/testdata/plan.json")
	if err != nil {
		t.Fatal(err)
	}
	var plan companion.PublishPlan
	_ = json.Unmarshal(data, &plan)
	page := review.Build("b", "B", "https://b.test", "now", "base", plan)

	if _, missing, _ := choose(page, ""); len(missing) != 1 {
		t.Errorf("as reviewed: the value both sides changed is undecided: %v", missing)
	}
	file := filepath.Join(t.TempDir(), "choices.json")
	_ = os.WriteFile(file, []byte(`{"include":["post:page:about"],"sides":{"post:page:about":{"fields.f_textarea":"take"}}}`), 0o600)
	d, missing, err := choose(page, file)
	if err != nil || len(missing) != 0 || len(d.Approved) != 1 || d.Take["post:page:about"][0] != "fields.f_textarea" {
		t.Errorf("choices: %v %v %+v", err, missing, d)
	}
	_ = os.WriteFile(file, []byte(`{"sides":{}}`), 0o600)
	if _, _, err := choose(page, file); err == nil {
		t.Error("choices with nothing included")
	}
}
