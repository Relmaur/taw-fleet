package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/selfupdate"
)

func runSelfUpdate(t *testing.T, version, exe string, args ...string) (string, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.7.1","html_url":"https://example.test/v0.7.1","assets":[]}`))
	}))
	t.Cleanup(srv.Close)
	u := selfupdate.New(nil)
	u.BaseURL = srv.URL
	var out bytes.Buffer
	d := Deps{Paths: paths.ForHome(t.TempDir(), nil), Runner: &exec.FakeRunner{}, Out: &out, Err: &out, Dark: true,
		Updater: u, Executable: func() (string, error) { return exe, nil }}
	root := NewRoot(BuildInfo{Version: version, Commit: "abc123"}, d)
	root.SetArgs(append([]string{"self-update"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestSelfUpdate(t *testing.T) {
	out, err := runSelfUpdate(t, "0.7.1", "/nowhere")
	if err != nil || !strings.Contains(out, "0.7.1 is the newest release") {
		t.Errorf("up to date: %q %v", out, err)
	}
	out, err = runSelfUpdate(t, "0.7.0", "/nowhere", "--check")
	if err != nil || !strings.Contains(out, "0.7.1 is out (this is 0.7.0)") {
		t.Errorf("--check: %q %v", out, err)
	}

	cask := filepath.Join(t.TempDir(), "Caskroom", "taw-fleet", "0.7.0", "taw-fleet")
	_ = os.MkdirAll(filepath.Dir(cask), 0o755)
	_ = os.WriteFile(cask, nil, 0o755)
	if _, err := runSelfUpdate(t, "0.7.0", cask, "--yes"); err == nil || !strings.Contains(err.Error(), "brew upgrade taw-fleet") {
		t.Errorf("homebrew: %v", err)
	}

	plain := filepath.Join(t.TempDir(), "taw-fleet")
	_ = os.WriteFile(plain, []byte("OLD"), 0o755)
	if _, err := runSelfUpdate(t, "0.7.0", plain); err == nil || !strings.Contains(err.Error(), "add --yes") {
		t.Errorf("no terminal: %v", err)
	}
	if _, err := runSelfUpdate(t, "0.7.0", plain, "--yes"); err == nil || !strings.Contains(err.Error(), "has no taw-fleet_0.7.1_darwin_") {
		t.Errorf("no assets: %v", err)
	}
	if b, _ := os.ReadFile(plain); string(b) != "OLD" {
		t.Error("binary changed")
	}
}
