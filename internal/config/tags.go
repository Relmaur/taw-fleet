package config

import (
	"fmt"
	"slices"
	"strings"
)

// CleanTags lowers, de-duplicates and checks tags, keeping their order. A
// leading # is dropped (#retainer is how the dashboard shows it).
func CleanTags(in []string) ([]string, error) {
	var out []string
	for _, t := range in {
		t = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t), "#"))
		if t == "" || slices.Contains(out, t) {
			continue
		}
		if err := CheckTag(t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if len(out) > MaxTags {
		return nil, fmt.Errorf("%d tags at most", MaxTags)
	}
	return out, nil
}

// TagMatches reports whether a site's tags have want: the tag itself, or,
// when want ends with ":", any tag that starts with it (client: = any client).
func TagMatches(tags []string, want string) bool {
	want = strings.ToLower(strings.TrimPrefix(want, "#"))
	for _, t := range tags {
		if t == want || (strings.HasSuffix(want, ":") && strings.HasPrefix(t, want)) {
			return true
		}
	}
	return false
}
