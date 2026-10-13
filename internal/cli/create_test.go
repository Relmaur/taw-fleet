package cli

import (
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/actions"
	"github.com/Relmaur/taw-fleet/internal/create"
)

func TestHidePassword(t *testing.T) {
	sum := actions.Summary{Lines: []string{"http://acme.local   http://acme.local/wp-admin", "Admin marco · password s3cr3t-Pass   (shown once: save it now)"},
		Secret: "s3cr3t-Pass", Report: create.Result{Slug: "acme", AdminUser: "marco"}}
	hidePassword(&sum)
	all := strings.Join(sum.Lines, "\n")
	if strings.Contains(all, "s3cr3t") || sum.Secret != "" || !strings.Contains(all, "Admin marco · password not shown; set one with: taw-fleet wp acme user update marco --prompt=user_pass") {
		t.Errorf("%q %q", sum.Lines, sum.Secret)
	}
}
