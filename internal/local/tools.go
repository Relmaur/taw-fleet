package local

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/Relmaur/taw-fleet/internal/paths"
)

// lightningDirs are where Local keeps its PHP, MySQL… builds: the per-user
// folder first (Local downloads newer versions there), then the app bundle.
func lightningDirs(p paths.Paths) []string {
	return []string{
		filepath.Join(p.LocalSupport, "lightning-services"),
		filepath.Join(p.LocalApp, "Contents", "Resources", "extraResources", "lightning-services"),
	}
}

// platformDirs are Local's names for this machine's build, best match first.
func platformDirs() []string {
	if runtime.GOARCH == "arm64" {
		return []string{"darwin-arm64", "darwin"}
	}
	return []string{"darwin", "darwin-x64"}
}

// PHPBinary finds Local's PHP for a version such as "8.2.30". Folders are
// named "php-8.2.30+1"; the highest build wins.
func PHPBinary(p paths.Paths, version string) (string, bool) {
	if version == "" {
		return "", false
	}
	for _, root := range lightningDirs(p) {
		matches, _ := filepath.Glob(filepath.Join(root, "php-"+version+"+*"))
		if exact := filepath.Join(root, "php-"+version); isDir(exact) {
			matches = append(matches, exact)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(matches)))
		for _, dir := range matches {
			for _, plat := range platformDirs() {
				bin := filepath.Join(dir, "bin", plat, "bin", "php")
				if isFile(bin) {
					return bin, true
				}
			}
		}
	}
	return "", false
}

// ComposerPhar is the Composer that ships with Local.
func ComposerPhar(p paths.Paths) (string, bool) {
	f := filepath.Join(p.LocalApp, "Contents", "Resources", "extraResources", "bin", "composer", "composer.phar")
	return f, isFile(f)
}

// WPCliPhar is the wp-cli that ships with Local.
func WPCliPhar(p paths.Paths) (string, bool) {
	f := filepath.Join(p.LocalApp, "Contents", "Resources", "extraResources", "bin", "wp-cli", "wp-cli.phar")
	return f, isFile(f)
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
