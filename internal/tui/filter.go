package tui

import (
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/Relmaur/taw-fleet/internal/composer"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// The filter (/): words that must all match, "-" in front to leave out, and
// conditions: is:behind, has:comments, kind:block, core:<1.95. A word matches
// the card's text (site, domain, theme, branch, version) as before.

type filterTerm struct {
	neg   bool
	field string // "" = a word
	value string
}

// filterFacts is what a condition reads about one theme of one site.
type filterFacts struct {
	text []string        // what a word matches, lower case
	is   map[string]bool // is: and has: conditions that hold
	kind string          // classic | block
	core string          // the installed taw/core
}

// conditions are the is: and has: names, with what each means (the help line).
var conditions = map[string][]string{
	"is":  {"behind", "dirty", "unpushed", "running", "stopped", "live", "down", "failing", "deploying", "undeployed", "vite", "other"},
	"has": {"comments", "prs", "findings", "errors"},
}

// parseFilter splits q into terms; problems are what it didn't understand.
func parseFilter(q string) (terms []filterTerm, problems []string) {
	for _, w := range strings.Fields(strings.ToLower(q)) {
		t := filterTerm{}
		if len(w) > 1 && w[0] == '-' {
			t.neg, w = true, w[1:]
		}
		field, value, ok := strings.Cut(w, ":")
		if !ok {
			t.value = w
			terms = append(terms, t)
			continue
		}
		if value == "" { // still being typed
			continue
		}
		switch field {
		case "is", "has":
			if len(matchingConditions(field, value)) == 0 {
				problems = append(problems, w)
				continue
			}
		case "kind":
			if !kindNamed(value) {
				problems = append(problems, w)
				continue
			}
		case "core":
			if _, _, ok := coreCompare(value); !ok {
				problems = append(problems, w)
				continue
			}
		default:
			problems = append(problems, w)
			continue
		}
		t.field, t.value = field, value
		terms = append(terms, t)
	}
	return terms, problems
}

// kindNamed reports whether value starts a kind's name (classic, block).
func kindNamed(value string) bool {
	for _, k := range []string{"classic", "block"} {
		if strings.HasPrefix(k, value) {
			return true
		}
	}
	return false
}

// matchingConditions are the is:/has: names value starts: "is:beh" is behind,
// "is:d" any of dirty, down, deploying, while it's typed.
func matchingConditions(field, value string) []string {
	var out []string
	for _, c := range conditions[field] {
		if strings.HasPrefix(c, value) {
			out = append(out, c)
		}
	}
	return out
}

// coreCompare reads core:<1.95, core:>=1.90, core:=1.97.1 or core:1.97 (a
// prefix) into an operator and a version.
func coreCompare(v string) (op, version string, ok bool) {
	for _, o := range []string{"<=", ">=", "<", ">", "="} {
		if rest, found := strings.CutPrefix(v, o); found {
			op, v = o, rest
			break
		}
	}
	if composer.Canonical(v) == "" && composer.Canonical(v+".0") == "" {
		return "", "", false
	}
	return op, v, true
}

func (t filterTerm) matches(f filterFacts) bool {
	hit := false
	switch t.field {
	case "":
		for _, h := range f.text {
			if strings.Contains(h, t.value) {
				hit = true
				break
			}
		}
	case "is", "has":
		for _, c := range matchingConditions(t.field, t.value) {
			hit = hit || f.is[c]
		}
	case "kind":
		hit = f.kind != "" && strings.HasPrefix(f.kind, t.value)
	case "core":
		hit = coreMatches(f.core, t.value)
	}
	return hit != t.neg
}

func coreMatches(installed, cond string) bool {
	op, v, _ := coreCompare(cond)
	have := composer.Canonical(installed)
	if have == "" {
		return false
	}
	if op == "" { // a prefix: core:1.97 is every 1.97.x
		return strings.HasPrefix(strings.TrimPrefix(installed, "v")+".", v+".")
	}
	want := composer.Canonical(v)
	if want == "" {
		want = composer.Canonical(v + ".0")
	}
	c := semver.Compare(have, want)
	switch op {
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	case ">=":
		return c >= 0
	}
	return c == 0
}

// filterFactsOf is what the dashboard knows about a theme of a site.
func (m Model) filterFactsOf(s site.Site, t site.Theme) filterFacts {
	f := filterFacts{is: map[string]bool{}, core: t.Core.Installed, kind: string(t.Kind)}
	if t.Kind == site.KindGutenberg {
		f.kind = "block" // the cards say "block"
	}
	for _, h := range []string{s.Slug, s.Name, s.Domain, t.Dir, string(t.Kind), f.kind, string(s.Status), t.Core.Installed} {
		f.text = append(f.text, strings.ToLower(h))
	}
	set := func(c string, ok bool) {
		if ok {
			f.is[c] = true
			f.text = append(f.text, c) // the old words still work: "behind", "dirty"…
		}
	}
	set("behind", t.Core.Behind)
	set("running", s.Status == site.StatusRunning)
	set("stopped", s.Status == site.StatusHalted)
	set("vite", t.Dev != "")
	if t.Account != "" {
		set("other", true)
		f.text = append(f.text, "@"+strings.ToLower(t.Account))
	}
	if g := t.Git; g != nil {
		f.text = append(f.text, strings.ToLower(g.Branch))
		set("dirty", g.Dirty > 0)
		if g.Dirty > 0 {
			f.text = append(f.text, "uncommitted")
		}
		set("unpushed", !g.HasUpstream() || g.Ahead > 0)
	}
	set("live", m.deps.Production[s.Slug])
	if p, ok := m.live[s.Slug]; ok {
		set("down", !p.Reachable || !p.Verified)
	}
	if gh := t.GitHub; gh != nil {
		set("prs", len(gh.PRs) > 0)
		failing := gh.HeadCI == site.ChecksFailing
		if d := gh.Deploy; d != nil {
			failing = failing || d.Failed != nil
			set("deploying", d.Running != nil)
			set("undeployed", d.Behind > 0)
		}
		set("failing", failing)
	}
	if fb, ok := m.feedback[s.Slug]; ok {
		set("comments", fb.Open > 0)
	}
	for _, fd := range m.findings[s.ID] {
		set("findings", true)
		if fd.Severity == site.Error {
			set("errors", true)
		}
	}
	return f
}

// filterHint is the footer's word on a filter it couldn't read.
func filterHint(problems []string) string {
	if len(problems) == 0 {
		return ""
	}
	sort.Strings(problems)
	return "not understood: " + strings.Join(problems, ", ")
}
