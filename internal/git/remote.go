package git

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/site"
)

// ParseRemote reads a remote URL into host/owner/name. It understands
//
//	https://github.com/owner/repo(.git)
//	git@github.com:owner/repo.git
//	git@github.com-work:owner/repo.git   (an SSH host alias → github.com)
//	ssh://git@github.com[:22]/owner/repo.git
func ParseRemote(raw string) (*site.Repo, error) {
	raw = strings.TrimSpace(raw)
	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		host, path = u.Hostname(), u.Path
	case strings.Contains(raw, ":"):
		// scp-like: [user@]host:path
		userHost, p, _ := strings.Cut(raw, ":")
		if at := strings.LastIndex(userHost, "@"); at >= 0 {
			userHost = userHost[at+1:]
		}
		host, path = userHost, p
	default:
		return nil, fmt.Errorf("not a remote URL: %q", raw)
	}

	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if host == "" || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("can't read owner/repo from %q", raw)
	}
	r := &site.Repo{Host: canonicalHost(host), Owner: parts[0], Name: parts[1]}
	if !strings.EqualFold(host, r.Host) {
		r.Alias = strings.ToLower(host)
	}
	return r, nil
}

// canonicalHost maps SSH config aliases such as "github.com-parallel" (a
// common way to use a second GitHub account) back to the real host.
func canonicalHost(h string) string {
	h = strings.ToLower(h)
	for _, known := range []string{"github.com", "gitlab.com", "bitbucket.org"} {
		if h == known || strings.HasPrefix(h, known+"-") || strings.HasPrefix(h, known+"_") {
			return known
		}
	}
	return h
}
