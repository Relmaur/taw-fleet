// Package review is the publish review: a page served on this Mac where the
// person sees, field by field, what a publish changes on production (and
// what production changed since), picks a side for each conflict, ticks the
// records to publish, and presses Publish. It then stays as the record of
// what went out (umbrella docs/plans/content-publish.md).
package review

import (
	"strings"
	"unicode"
)

// Span is a piece of a diff: equal on both sides, only in the old (del) or
// only in the new (ins).
type Span struct {
	Op   string `json:"op"` // eq, del, ins
	Text string `json:"text"`
}

// maxCells bounds the LCS table; past it a value is shown as replaced whole.
const maxCells = 4_000_000

// Words diffs two short texts word by word (whitespace and punctuation are
// tokens of their own, so a changed comma shows as one).
func Words(before, after string) []Span {
	return merge(lcs(tokens(before), tokens(after)))
}

// Row is one line of a line diff: the same on both sides (eq), changed
// (old and new, with word spans), only old (del), only new (ins), or a run
// of unchanged lines folded away (skip, Count of them).
type Row struct {
	Op    string `json:"op"`
	Old   string `json:"old,omitempty"`
	New   string `json:"new,omitempty"`
	Spans []Span `json:"spans,omitempty"` // for a changed line: its word diff
	Count int    `json:"count,omitempty"`
}

// Lines diffs two long texts line by line, keeping context lines around each
// change and folding long unchanged runs; a deleted line followed by an
// inserted one is shown as one changed line with its word diff.
func Lines(before, after string, context int) []Row {
	ops := lcs(strings.Split(before, "\n"), strings.Split(after, "\n"))
	var rows []Row
	for i := 0; i < len(ops); {
		if ops[i].Op == "eq" {
			rows = append(rows, Row{Op: "eq", Old: ops[i].Text, New: ops[i].Text})
			i++
			continue
		}
		// A changed run: its deleted lines, then its inserted ones, paired
		// in order (the first old line with the first new one…).
		var dels, inses []string
		for ; i < len(ops) && ops[i].Op == "del"; i++ {
			dels = append(dels, ops[i].Text)
		}
		for ; i < len(ops) && ops[i].Op == "ins"; i++ {
			inses = append(inses, ops[i].Text)
		}
		for j := 0; j < max(len(dels), len(inses)); j++ {
			switch {
			case j < len(dels) && j < len(inses):
				rows = append(rows, Row{Op: "change", Old: dels[j], New: inses[j], Spans: Words(dels[j], inses[j])})
			case j < len(dels):
				rows = append(rows, Row{Op: "del", Old: dels[j]})
			default:
				rows = append(rows, Row{Op: "ins", New: inses[j]})
			}
		}
	}
	return fold(rows, context)
}

// fold keeps context unchanged lines around each change and folds the rest.
func fold(rows []Row, context int) []Row {
	keep := make([]bool, len(rows))
	for i, r := range rows {
		if r.Op == "eq" {
			continue
		}
		for j := max(0, i-context); j <= min(len(rows)-1, i+context); j++ {
			keep[j] = true
		}
	}
	var out []Row
	skipped := 0
	for i, r := range rows {
		if keep[i] {
			if skipped > 0 {
				out = append(out, Row{Op: "skip", Count: skipped})
				skipped = 0
			}
			out = append(out, r)
			continue
		}
		skipped++
	}
	if skipped > 0 {
		out = append(out, Row{Op: "skip", Count: skipped})
	}
	return out
}

// tokens splits text into words, runs of spaces, and single other characters.
func tokens(s string) []string {
	var out []string
	var cur strings.Builder
	kind := 0 // 1 word, 2 space
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		k := 3
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '\'' || r == '’':
			k = 1
		case unicode.IsSpace(r):
			k = 2
		}
		if k != kind || k == 3 {
			flush()
		}
		kind = k
		cur.WriteRune(r)
	}
	flush()
	return out
}

// lcs is the longest-common-subsequence diff of two token lists. Past
// maxCells it gives up and says "all replaced".
func lcs(a, b []string) []Span {
	// Common prefix and suffix first: most edits are small.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	head, tail := a[:pre], a[len(a)-suf:]
	a, b = a[pre:len(a)-suf], b[pre:len(b)-suf]

	var mid []Span
	switch {
	case len(a) == 0 && len(b) == 0:
	case len(a)*len(b) > maxCells:
		for _, t := range a {
			mid = append(mid, Span{"del", t})
		}
		for _, t := range b {
			mid = append(mid, Span{"ins", t})
		}
	default:
		n, m := len(a), len(b)
		table := make([][]int32, n+1)
		for i := range table {
			table[i] = make([]int32, m+1)
		}
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if a[i] == b[j] {
					table[i][j] = table[i+1][j+1] + 1
				} else {
					table[i][j] = max(table[i+1][j], table[i][j+1])
				}
			}
		}
		i, j := 0, 0
		for i < n && j < m {
			switch {
			case a[i] == b[j]:
				mid = append(mid, Span{"eq", a[i]})
				i++
				j++
			case table[i+1][j] >= table[i][j+1]:
				mid = append(mid, Span{"del", a[i]})
				i++
			default:
				mid = append(mid, Span{"ins", b[j]})
				j++
			}
		}
		for ; i < n; i++ {
			mid = append(mid, Span{"del", a[i]})
		}
		for ; j < m; j++ {
			mid = append(mid, Span{"ins", b[j]})
		}
	}
	out := make([]Span, 0, len(head)+len(mid)+len(tail))
	for _, t := range head {
		out = append(out, Span{"eq", t})
	}
	out = append(out, mid...)
	for _, t := range tail {
		out = append(out, Span{"eq", t})
	}
	return out
}

// merge joins neighbouring spans of the same op, and a run of spaces
// between two changes into the change, so "a b c" → "x y z" reads as one
// replacement, not six.
func merge(in []Span) []Span {
	var out []Span
	for i, s := range in {
		if s.Op == "eq" && strings.TrimSpace(s.Text) == "" && i > 0 && i < len(in)-1 && in[i-1].Op != "eq" && in[i+1].Op != "eq" {
			out = append(out, Span{"del", s.Text}, Span{"ins", s.Text})
			continue
		}
		out = append(out, s)
	}
	var merged []Span
	for _, s := range out {
		// Keep dels before inses within a changed run.
		if n := len(merged); n > 0 && merged[n-1].Op == s.Op {
			merged[n-1].Text += s.Text
			continue
		}
		if n := len(merged); s.Op == "del" && n > 0 && merged[n-1].Op == "ins" {
			if n > 1 && merged[n-2].Op == "del" {
				merged[n-2].Text += s.Text
				continue
			}
			merged = append(merged[:n-1], s, merged[n-1])
			continue
		}
		merged = append(merged, s)
	}
	return merged
}
