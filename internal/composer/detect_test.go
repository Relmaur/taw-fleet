package composer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/site"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetect(t *testing.T) {
	root := t.TempDir()

	classic := filepath.Join(root, "chcapital")
	write(t, filepath.Join(classic, "composer.json"), `{"name":"taw/theme","type":"project","require":{"php":">=8.2","taw/core":"^1.22"}}`)
	write(t, filepath.Join(classic, "bin", "taw"), "#!/usr/bin/env php\n")

	gutenberg := filepath.Join(root, "taw-gutenberg")
	write(t, filepath.Join(gutenberg, "composer.json"), `{"name":"taw/gutenberg","type":"wordpress-theme","require":{"taw/core":"^1.50"}}`)

	childBlock := filepath.Join(root, "client-blocks")
	write(t, filepath.Join(childBlock, "composer.json"), `{"name":"acme/blocks","type":"wordpress-theme","require":{"taw/core":"^1.70"}}`)
	write(t, filepath.Join(childBlock, "theme.json"), `{}`)
	write(t, filepath.Join(childBlock, "templates", "index.html"), ``)

	other := filepath.Join(root, "rigid")
	write(t, filepath.Join(other, "composer.json"), `{"name":"mlizardo/rigid-hybrid","type":"wordpress-theme","require":{}}`)

	none := filepath.Join(root, "twentytwentyfive")
	write(t, filepath.Join(none, "style.css"), "/* Theme Name: TT5 */")

	cases := []struct {
		dir  string
		want Detection
	}{
		{classic, Detection{Package: "taw/theme", Kind: site.KindClassic, IsTAW: true, HasBinTaw: true}},
		{gutenberg, Detection{Package: "taw/gutenberg", Kind: site.KindGutenberg, IsTAW: true}},
		{childBlock, Detection{Package: "acme/blocks", Kind: site.KindGutenberg, IsTAW: true}},
		{other, Detection{Package: "mlizardo/rigid-hybrid", Kind: site.KindOther}},
		{none, Detection{Kind: site.KindOther}},
	}
	for _, c := range cases {
		got, err := Detect(c.dir)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(c.dir), err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", filepath.Base(c.dir), got, c.want)
		}
	}
}

func TestDetectInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "composer.json"), `{"name":`)
	if _, err := Detect(dir); err == nil {
		t.Error("want an error for invalid composer.json")
	}
}

func TestDetectThroughSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "umbrella", "taw-theme")
	write(t, filepath.Join(target, "composer.json"), `{"name":"taw/theme","require":{"taw/core":"^1.0"}}`)
	link := filepath.Join(root, "themes", "taw-theme")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got, err := Detect(link)
	if err != nil || !got.IsTAW || got.Kind != site.KindClassic {
		t.Errorf("got %+v err=%v", got, err)
	}
}
