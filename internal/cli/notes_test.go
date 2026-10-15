package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/notes"
)

func TestNotesCommand(t *testing.T) {
	p := fixture(t)
	if out, err := run(t, p, "notes", "acme"); err != nil || !strings.Contains(out, "acme has no notes yet") {
		t.Errorf("none: %v\n%s", err, out)
	}
	if out, err := run(t, p, "notes", "acme", "Client", "asked", "to", "hold", "the", "redesign"); err != nil || !strings.Contains(out, "Note added to acme") {
		t.Fatalf("add: %v\n%s", err, out)
	}
	out, err := run(t, p, "note", "acme", "--by", "claude", "--json", "--", "-n is fine after --")
	if err != nil {
		t.Fatalf("add as Claude: %v\n%s", err, out)
	}
	var added struct {
		Site string      `json:"site"`
		Note notes.Entry `json:"note"`
	}
	if err := json.Unmarshal([]byte(out), &added); err != nil || added.Note.By != notes.Claude || added.Note.Text != "-n is fine after --" {
		t.Errorf("json: %v %+v", err, added)
	}
	if _, err := run(t, p, "notes", "acme", "--by", "taw-fleet", "pretending"); err == nil {
		t.Error("only you or claude")
	}

	// stdin
	var buf bytes.Buffer
	d := Deps{Paths: p, Runner: &exec.FakeRunner{}, Out: &buf, Err: &buf, Dark: true, GitHub: fakeGitHub(t)}
	root := NewRoot(BuildInfo{Version: "1.2.3"}, d)
	root.SetIn(strings.NewReader("DNS moved to Cloudflare.\nTTL 300.\n"))
	root.SetArgs([]string{"notes", "acme", "-"})
	if err := root.Execute(); err != nil {
		t.Fatalf("stdin: %v\n%s", err, buf.String())
	}

	out, err = run(t, p, "notes", "acme", "--json", "--last", "2")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		File  string        `json:"file"`
		Notes []notes.Entry `json:"notes"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Notes) != 2 || list.Notes[0].Text != "DNS moved to Cloudflare.\nTTL 300." || !strings.HasSuffix(list.File, "/TAW Fleet/sites/acme/notes.md") {
		t.Errorf("list: %v %+v", err, list)
	}
	if out, _ := run(t, p, "notes", "acme"); !strings.Contains(out, "· you ·") || !strings.Contains(out, "  Client asked to hold the redesign") {
		t.Errorf("text:\n%s", out)
	}

	// show carries the newest notes, as text and JSON.
	if out, _ := run(t, p, "show", "acme", "--offline"); !strings.Contains(out, "NOTES") || !strings.Contains(out, "TTL 300.") {
		t.Errorf("show:\n%s", out)
	}
	out, _ = run(t, p, "show", "acme", "--offline", "--json")
	var shown struct {
		Notes []notes.Entry `json:"notes"`
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil || len(shown.Notes) != 3 {
		t.Errorf("show --json: %v %d", err, len(shown.Notes))
	}
}
