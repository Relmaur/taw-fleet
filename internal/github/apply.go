package github

import (
	"sort"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/site"
)

// RepoNames lists the GitHub repositories ("owner/name") of the TAW themes,
// once each, sorted.
func RepoNames(sites []site.Site) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sites {
		for _, t := range s.TAWThemes() {
			if r := repoOf(t); r != "" && !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	sort.Strings(out)
	return out
}

// ApplyRepos puts each theme's repository state on it.
func ApplyRepos(sites []site.Site, states map[string]site.RepoState) {
	for i := range sites {
		for j := range sites[i].Themes {
			t := &sites[i].Themes[j]
			if st, ok := states[repoOf(*t)]; ok {
				t.GitHub = &st
			}
		}
	}
}

func repoOf(t site.Theme) string {
	if !t.IsTAW || t.Git == nil || t.Git.Repo == nil || !strings.EqualFold(t.Git.Repo.Host, "github.com") {
		return ""
	}
	return t.Git.Repo.FullName()
}
