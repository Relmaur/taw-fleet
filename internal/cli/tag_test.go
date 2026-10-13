package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
)

func TestTagSetsShowsAndClears(t *testing.T) {
	p := fixture(t)
	if out, err := run(t, p, "tag", "acme"); err != nil || !strings.Contains(out, "acme has no tags") {
		t.Fatalf("show none: %v %s", err, out)
	}
	if out, err := run(t, p, "tag", "acme", "Retainer", "#es", "es", "client:acme", "--yes"); err != nil || !strings.Contains(out, "acme: retainer es client:acme") {
		t.Fatalf("set: %v %s", err, out)
	}
	c, _ := config.Load(p)
	if got := strings.Join(c.Site("acme").Tags, " "); got != "retainer es client:acme" {
		t.Errorf("stored %q", got)
	}
	if out, _ := run(t, p, "tag", "acme"); strings.TrimSpace(out) != "retainer es client:acme" {
		t.Errorf("show: %q", out)
	}
	if _, err := run(t, p, "tag", "acme", "--add", "on-hold", "--remove", "retainer", "--yes"); err != nil {
		t.Fatal(err)
	}
	c, _ = config.Load(p)
	if got := strings.Join(c.Site("acme").Tags, " "); got != "es client:acme on-hold" {
		t.Errorf("add/remove %q", got)
	}
	if _, err := run(t, p, "tag", "acme", "Not OK", "--yes"); err == nil {
		t.Error("accepted a bad tag")
	}
	if _, err := run(t, p, "tag", "acme", "--clear", "--yes"); err != nil {
		t.Fatal(err)
	}
	if c, _ = config.Load(p); len(c.Site("acme").Tags) != 0 {
		t.Errorf("not cleared: %v", c.Site("acme").Tags)
	}
}

func TestListByTag(t *testing.T) {
	p := fixture(t)
	if _, err := run(t, p, "tag", "acme", "retainer", "client:acme", "--yes"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, p, "list", "--json", "--offline")
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Sites []struct {
			Slug string   `json:"slug"`
			Tags []string `json:"tags"`
		} `json:"sites"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil || len(rep.Sites) != 1 || strings.Join(rep.Sites[0].Tags, ",") != "retainer,client:acme" {
		t.Fatalf("json tags: %v %s", err, out)
	}
	for q, want := range map[string]int{"retainer": 1, "client:": 1, "es": 0, "client": 0} {
		out, err := run(t, p, "list", "--json", "--offline", "--tag", q)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal([]byte(out), &rep)
		if len(rep.Sites) != want {
			t.Errorf("--tag %s: %d sites", q, len(rep.Sites))
		}
	}
	if out, _ := run(t, p, "list", "--offline"); !strings.Contains(out, "#retainer #client:acme") {
		t.Errorf("text list:\n%s", out)
	}
}
