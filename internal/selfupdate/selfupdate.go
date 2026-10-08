// Package selfupdate replaces the running taw-fleet with the newest GitHub
// release: the archive for this Mac's architecture, checked against the
// release's checksums.txt before anything is written.
package selfupdate

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Repo is where releases come from.
const Repo = "Relmaur/taw-fleet"

// Binary is the executable's name inside the archive.
const Binary = "taw-fleet"

// maxDownload bounds an archive (the binary is ~10 MB).
const maxDownload = 64 << 20

// ErrHomebrew refuses to replace a binary Homebrew manages.
var ErrHomebrew = errors.New("installed with Homebrew: run `brew upgrade taw-fleet`")

// ErrChecksum means the download doesn't match checksums.txt.
var ErrChecksum = errors.New("the download doesn't match the release's checksums.txt")

// Release is a published GitHub release.
type Release struct {
	Tag    string            // v0.7.0
	URL    string            // the release page
	Assets map[string]string // file name → download URL
}

// Version is the tag without the v.
func (r Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

// Updater talks to GitHub. The zero value isn't usable; use New.
type Updater struct {
	BaseURL string       // https://api.github.com
	Repo    string       // owner/name
	HTTP    *http.Client // with a timeout
	Token   func() string
	GOOS    string
	GOARCH  string
}

// New returns an updater for this Mac and the taw-fleet repository.
func New(token func() string) *Updater {
	return &Updater{
		BaseURL: "https://api.github.com",
		Repo:    Repo,
		HTTP:    &http.Client{Timeout: 2 * time.Minute},
		Token:   token,
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
	}
}

// Latest returns the newest published (non-draft, non-prerelease) release.
func (u *Updater) Latest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(u.BaseURL, "/")+"/repos/"+u.Repo+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "taw-fleet")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if u.Token != nil {
		if tok := u.Token(); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	body, err := u.get(req, 2<<20)
	if err != nil {
		return Release{}, fmt.Errorf("newest release: %w", err)
	}
	var raw struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Release{}, fmt.Errorf("newest release: %w", err)
	}
	if !semver.IsValid(raw.TagName) {
		return Release{}, fmt.Errorf("newest release: %q isn't a version tag", raw.TagName)
	}
	rel := Release{Tag: raw.TagName, URL: raw.HTMLURL, Assets: map[string]string{}}
	for _, a := range raw.Assets {
		rel.Assets[a.Name] = a.URL
	}
	return rel, nil
}

// Newer reports whether release tag is newer than the running version
// ("0.6.0", "v0.6.0"). A dev build is never up to date.
func Newer(running, tag string) bool {
	cur := "v" + strings.TrimPrefix(running, "v")
	if !semver.IsValid(cur) {
		return true
	}
	return semver.Compare(tag, cur) > 0
}

// ArchiveName is GoReleaser's archive for a version and platform.
func ArchiveName(version, goos, goarch string) string {
	return fmt.Sprintf("%s_%s_%s_%s.tar.gz", Binary, strings.TrimPrefix(version, "v"), goos, goarch)
}

// Homebrew reports whether path is inside a Homebrew prefix (formula or cask).
func Homebrew(path string) bool {
	return strings.Contains(path, "/Cellar/") || strings.Contains(path, "/Caskroom/")
}

// Target is the file to replace: the running executable, symlinks resolved.
func Target(exe string) (string, error) {
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	if Homebrew(resolved) {
		return "", ErrHomebrew
	}
	return resolved, nil
}

// Apply downloads rel for this platform, checks it against checksums.txt and
// replaces target (from Target) with the new binary in one rename.
func (u *Updater) Apply(ctx context.Context, rel Release, target string) error {
	name := ArchiveName(rel.Tag, u.GOOS, u.GOARCH)
	archiveURL, sumsURL := rel.Assets[name], rel.Assets["checksums.txt"]
	if archiveURL == "" {
		return fmt.Errorf("release %s has no %s", rel.Tag, name)
	}
	if sumsURL == "" {
		return fmt.Errorf("release %s has no checksums.txt", rel.Tag)
	}
	sums, err := u.download(ctx, sumsURL, 1<<20)
	if err != nil {
		return err
	}
	want, err := checksum(sums, name)
	if err != nil {
		return err
	}
	archive, err := u.download(ctx, archiveURL, maxDownload)
	if err != nil {
		return err
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s: %w", name, ErrChecksum)
	}
	bin, err := extract(archive, Binary)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return replace(target, bin)
}

func (u *Updater) download(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "taw-fleet")
	body, err := u.get(req, limit)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", filepath.Base(url), err)
	}
	return body, nil
}

func (u *Updater) get(req *http.Request, limit int64) ([]byte, error) {
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("too large")
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &e) == nil && e.Message != "" {
			return nil, fmt.Errorf("%s: %s", resp.Status, e.Message)
		}
		return nil, errors.New(resp.Status)
	}
	return body, nil
}

// checksum finds name in a checksums.txt ("<sha256>  <name>" lines).
func checksum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no line for %s", name)
}

// extract returns the file called name from a .tar.gz.
func extract(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no %s in the archive", name)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == name {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}

// replace writes bin next to target and renames it over target, so a failure
// leaves the old binary in place.
func replace(target string, bin []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".taw-fleet-update-*")
	if err != nil {
		return fmt.Errorf("can't write next to %s: %w", target, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(bin); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}
