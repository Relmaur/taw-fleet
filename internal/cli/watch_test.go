package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/watch"
)

func TestWatchOnceThenStatus(t *testing.T) {
	p := fixture(t)
	var got struct {
		First    bool          `json:"first"`
		Events   []watch.Event `json:"events"`
		Problems []string      `json:"problems"`
	}
	for i, wantFirst := range []bool{true, false} {
		out, err := run(t, p, "watch", "--once", "--json", "--no-notify")
		if err != nil {
			t.Fatalf("check %d: %v\n%s", i, err, out)
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("check %d: %v\n%s", i, err, out)
		}
		if got.First != wantFirst || got.Events == nil || len(got.Events) != 0 {
			t.Errorf("check %d: %+v", i, got)
		}
	}
	if _, err := os.Stat(watch.File(p)); err != nil {
		t.Errorf("no state saved: %v", err)
	}

	out, err := run(t, p, "watch", "status")
	if err != nil || !strings.Contains(out, "off (taw-fleet watch install)") || strings.Contains(out, "Last check     never") {
		t.Errorf("status: %v\n%s", err, out)
	}
}

func TestWatchInstallUninstall(t *testing.T) {
	p := fixture(t)
	out, err := run(t, p, "watch", "install", "--every", "10m")
	if err != nil || !strings.Contains(out, "every 10 minutes") {
		t.Fatalf("install: %v\n%s", err, out)
	}
	a := watch.Agent{Paths: p}
	if _, err := os.Stat(a.Plist()); err != nil {
		t.Fatalf("no agent file: %v", err)
	}
	if out, _ := run(t, p, "watch", "status"); !strings.Contains(out, "on, every 10 minutes") {
		t.Errorf("status:\n%s", out)
	}
	if out, err := run(t, p, "watch", "uninstall"); err != nil || !strings.Contains(out, "Stopped") {
		t.Errorf("uninstall: %v\n%s", err, out)
	}
	if out, _ := run(t, p, "watch", "uninstall"); !strings.Contains(out, "Nothing was checking") {
		t.Errorf("uninstall again:\n%s", out)
	}
}
