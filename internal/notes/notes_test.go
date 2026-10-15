package notes

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation(layout, s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func TestAddNewestFirstAndRead(t *testing.T) {
	f := File(t.TempDir(), "acme")
	if es, err := Read(f); err != nil || es != nil {
		t.Fatalf("no file: %v %v", es, err)
	}
	if err := Add(f, "acme", Entry{At: at("2026-10-12 09:10"), By: Claude, Text: "Updated taw/core.\nHero needed a fix."}); err != nil {
		t.Fatal(err)
	}
	if err := Add(f, "acme", Entry{At: at("2026-10-15 14:32"), By: You, Text: "  Client asked to hold the redesign.  "}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(f)
	if !strings.HasPrefix(string(data), "# acme: notes\n") || strings.Index(string(data), "14:32") > strings.Index(string(data), "09:10") {
		t.Errorf("file:\n%s", data)
	}
	es, err := Read(f)
	if err != nil || len(es) != 2 {
		t.Fatalf("%v %v", es, err)
	}
	if es[0].By != You || es[0].Text != "Client asked to hold the redesign." || !es[0].At.Equal(at("2026-10-15 14:32")) {
		t.Errorf("first: %+v", es[0])
	}
	if es[1].Text != "Updated taw/core.\nHero needed a fix." {
		t.Errorf("second: %+v", es[1])
	}
}

func TestHandEditsAndHeadingsInText(t *testing.T) {
	f := File(t.TempDir(), "acme")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	hand := "# Acme, my own title\n\nSome words of mine.\n\n## 2026-10-01 08:00 · you\nby hand\n## not a date · x\nstill by hand\n"
	if err := os.WriteFile(f, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Add(f, "acme", Entry{At: at("2026-10-02 10:00"), By: TawFleet, Text: "## 2026-01-01 00:00 · fake\nreal"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(f)
	if !strings.HasPrefix(string(data), "# Acme, my own title\n\nSome words of mine.\n\n## 2026-10-02 10:00 · taw-fleet\n") {
		t.Errorf("the title part is kept, the entry goes under it:\n%s", data)
	}
	es, _ := Read(f)
	if len(es) != 2 || es[0].Text != "## 2026-01-01 00:00 · fake\nreal" || es[1].Text != "by hand\n## not a date · x\nstill by hand" {
		t.Errorf("entries: %+v", es)
	}
}

func TestAddRefuses(t *testing.T) {
	f := File(t.TempDir(), "acme")
	for _, e := range []Entry{{By: You, Text: "  "}, {By: "", Text: "x"}, {By: "a · b", Text: "x"}, {By: You, Text: strings.Repeat("x", MaxText+1)}} {
		if err := Add(f, "acme", e); err == nil {
			t.Errorf("%+v must be refused", e)
		}
	}
}

func TestAddWaitsForOtherWriters(t *testing.T) {
	f := File(t.TempDir(), "acme")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Add(f, "acme", Entry{By: TawFleet, Text: strings.Repeat("n", i+1)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if es, _ := Read(f); len(es) != 20 {
		t.Errorf("%d entries, want 20", len(es))
	}
}

func TestLine(t *testing.T) {
	if l := Line(Entry{Text: "one\ntwo"}, 40); l != "one …" {
		t.Errorf("%q", l)
	}
	if l := Line(Entry{Text: "abcdefghij"}, 5); l != "abcd…" {
		t.Errorf("%q", l)
	}
}

func TestFileStaysInTheFleetFolder(t *testing.T) {
	for slug, want := range map[string]string{"acme": "acme", "../../etc": "etc", "a b/c": "a-b-c", "..": "site"} {
		if got := File("/f", slug); got != filepath.Join("/f", "sites", want, "notes.md") {
			t.Errorf("%q → %s", slug, got)
		}
	}
}
