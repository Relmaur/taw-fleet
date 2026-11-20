package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
	fleetNotes "github.com/Relmaur/taw-fleet/internal/notes"
)

func TestMovedMarksShowsAndClears(t *testing.T) {
	p := fixture(t)
	if _, err := config.SetSite(p, "acme", "production_url", "https://acme.example"); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, p, "moved", "acme"); err != nil || !strings.Contains(out, "acme is a WordPress site in the fleet") {
		t.Fatalf("show unmoved: %v %s", err, out)
	}
	if out, err := run(t, p, "moved", "acme", "Laravel on Forge", "--yes"); err != nil || !strings.Contains(out, "acme moved to Laravel on Forge") {
		t.Fatalf("mark: %v %s", err, out)
	}
	c, _ := config.Load(p)
	if s := c.Site("acme"); !s.Moved() || s.ProductionURL != "" || s.MovedURL != "https://acme.example" {
		t.Errorf("stored %+v", s)
	}
	if out, _ := run(t, p, "moved", "acme"); !strings.Contains(out, "acme moved to Laravel on Forge (https://acme.example)") {
		t.Errorf("show: %q", out)
	}
	out, err := run(t, p, "list", "--json", "--offline")
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Sites []struct {
			Moved *struct {
				To  string `json:"to"`
				URL string `json:"url"`
			} `json:"moved"`
		} `json:"sites"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil || len(rep.Sites) != 1 || rep.Sites[0].Moved == nil || rep.Sites[0].Moved.To != "Laravel on Forge" {
		t.Fatalf("json: %v %s", err, out)
	}
	if out, err := run(t, p, "ready", "acme", "--offline"); err == nil || !strings.Contains(err.Error(), "moved to Laravel on Forge") {
		t.Errorf("ready: %v %s", err, out)
	}
	notes, _ := os.ReadFile(fleetNotes.File(p.FleetDir, "acme"))
	if !strings.Contains(string(notes), "Marked moved to Laravel on Forge") {
		t.Errorf("notes:\n%s", notes)
	}
	if _, err := run(t, p, "moved", "acme", " ", "--yes"); err == nil {
		t.Error("accepted an empty mark")
	}
	if _, err := run(t, p, "moved", "acme", "--clear", "--yes"); err != nil {
		t.Fatal(err)
	}
	if c, _ = config.Load(p); c.Site("acme").Moved() || c.Site("acme").ProductionURL != "https://acme.example" {
		t.Errorf("cleared %+v", c.Site("acme"))
	}
}
