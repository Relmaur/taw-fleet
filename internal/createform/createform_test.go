package createform

import (
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/create"
	"github.com/Relmaur/taw-fleet/internal/paths"
)

func TestDefaultsAndRequest(t *testing.T) {
	f := Defaults(config.Create{Kind: "block", AdminUser: "marco", AdminEmail: "m@x.test", PHP: "8.2"})
	if f.Kind != "block" || f.AdminUser != "marco" || f.PHP != "8.2" {
		t.Errorf("f = %+v", f)
	}
	if Defaults(config.Create{}).Kind != "classic" {
		t.Error("classic by default")
	}
	f.Name, f.Domain = "  Acme  ", " acme.test "
	r := f.Request()
	if r.Name != "Acme" || r.Domain != "acme.test" || r.Kind != create.Block {
		t.Errorf("r = %+v", r)
	}
}

func TestSummary(t *testing.T) {
	s := Summary(Fields{Name: "Acme Shop", Kind: "classic", AdminUser: "marco", AdminEmail: "m@x.test"})
	for _, want := range []string{"http://acme-shop.local", "~/Local Sites/acme-shop", "Local's preferred", "taw-theme in wp-content/themes/acme-shop", "marco <m@x.test>"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %q", want, s)
		}
	}
	s = Summary(Fields{Name: "B", Kind: "block", Domain: "b.test", ThemeDir: "bee", PHP: "8.5.3", WebServer: "nginx-1.26.1"})
	for _, want := range []string{"http://b.test", "PHP 8.5.3, nginx 1.26.1", "taw-gutenberg in wp-content/themes/bee"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %q", want, s)
		}
	}
}

func TestFormBuilds(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	p.LocalApp = t.TempDir()
	f := Defaults(config.Create{})
	form := New(p, true, &f)
	form = form.WithWidth(80)
	_ = form.Init()
	if !strings.Contains(form.View(), "Site name") {
		t.Error("form view")
	}
}
