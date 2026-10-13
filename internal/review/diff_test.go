package review

import (
	"strings"
	"testing"
)

func render(spans []Span) (before, after string) {
	var o, n strings.Builder
	for _, s := range spans {
		switch s.Op {
		case "eq":
			o.WriteString(s.Text)
			n.WriteString(s.Text)
		case "del":
			o.WriteString("[-" + s.Text + "-]")
		case "ins":
			n.WriteString("{+" + s.Text + "+}")
		}
	}
	return o.String(), n.String()
}

func TestWords(t *testing.T) {
	for _, c := range []struct{ old, new, wantOld, wantNew string }{
		{"Welcome to Acme", "Welcome to Acme Bakery", "Welcome to Acme", "Welcome to Acme{+ Bakery+}"},
		{"We bake every morning.", "We bake every morning, since 1998.", "We bake every morning.", "We bake every morning{+, since 1998+}."},
		{"a b c", "x y z", "[-a b c-]", "{+x y z+}"},
		{"same", "same", "same", "same"},
		{"", "new text", "", "{+new text+}"},
		{"Hola, ¿qué tal?", "Hola, ¿cómo estás?", "Hola, ¿[-qué tal-]?", "Hola, ¿{+cómo estás+}?"},
	} {
		o, n := render(Words(c.old, c.new))
		if o != c.wantOld || n != c.wantNew {
			t.Errorf("%q → %q:\n old %q want %q\n new %q want %q", c.old, c.new, o, c.wantOld, n, c.wantNew)
		}
	}
}

func TestWordsKeepsBothSidesWhole(t *testing.T) {
	before, after := "The quick brown fox jumps over the lazy dog", "The quick red fox leaps over the dog"
	var o, n strings.Builder
	for _, s := range Words(before, after) {
		if s.Op != "ins" {
			o.WriteString(s.Text)
		}
		if s.Op != "del" {
			n.WriteString(s.Text)
		}
	}
	if o.String() != before || n.String() != after {
		t.Errorf("sides don't add up: %q / %q", o.String(), n.String())
	}
}

func TestLinesFoldsAndPairs(t *testing.T) {
	var before, after []string
	for i := range 20 {
		before = append(before, "line "+string(rune('a'+i)))
	}
	after = append(after, before...)
	after[10] = "line k, edited"
	after = append(after[:15], append([]string{"inserted"}, after[15:]...)...)

	rows := Lines(strings.Join(before, "\n"), strings.Join(after, "\n"), 1)
	var ops []string
	for _, r := range rows {
		ops = append(ops, r.Op)
	}
	if got := strings.Join(ops, " "); got != "skip eq change eq skip eq ins eq skip" {
		t.Errorf("rows %s", got)
	}
	if rows[0].Count != 9 || rows[2].Old != "line k" || rows[2].New != "line k, edited" || len(rows[2].Spans) == 0 {
		t.Errorf("rows %+v", rows[:3])
	}
}

func TestLinesPairsAChangedRunInOrder(t *testing.T) {
	before := "<!-- wp:media-text {\"mediaId\":5003} -->\n<div><img class=\"wp-image-5003\"></div>\n<!-- /wp:media-text -->"
	after := "<!-- wp:media-text {\"mediaId\":10} -->\n<div><img class=\"wp-image-10\"></div>\n<p>new</p>\n<!-- /wp:media-text -->"
	rows := Lines(before, after, 3)
	var ops []string
	for _, r := range rows {
		ops = append(ops, r.Op)
	}
	if got := strings.Join(ops, " "); got != "change change ins eq" {
		t.Fatalf("rows %s", got)
	}
	if !strings.HasPrefix(rows[0].Old, "<!--") || !strings.HasPrefix(rows[0].New, "<!--") ||
		!strings.HasPrefix(rows[1].Old, "<div>") || !strings.HasPrefix(rows[1].New, "<div>") || rows[2].New != "<p>new</p>" {
		t.Errorf("each old line beside its own new one: %+v", rows)
	}
}

func TestHugeValuesAreReplacedWhole(t *testing.T) {
	a := strings.Repeat("a ", 3000)
	b := strings.Repeat("b ", 3000)
	spans := Words(a, b)
	if len(spans) > 3 || spans[0].Op != "del" || spans[1].Op != "ins" {
		t.Errorf("%d spans: %v…", len(spans), spans[:min(3, len(spans))])
	}
}
