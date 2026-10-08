package scan

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/github"
	"github.com/Relmaur/taw-fleet/internal/site"
)

func TestCoreEnricher(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "vendor", "composer", "installed.json"), `{"packages":[{"name":"taw/core","version":"v1.59.2"}]}`)
	write(t, filepath.Join(dir, "composer.lock"), `{"packages":[{"name":"taw/core","version":"v1.60.0"}]}`)
	th := site.Theme{RealPath: dir, IsTAW: true, Kind: site.KindClassic}
	if err := (CoreEnricher{}).Enrich(context.Background(), &site.Site{}, &th); err != nil {
		t.Fatal(err)
	}
	if th.Core.Installed != "v1.59.2" || th.Core.Locked != "v1.60.0" || !th.Core.LockMismatch || th.Scaffold.Name != "taw-theme" {
		t.Errorf("core = %+v scaffold = %+v", th.Core, th.Scaffold)
	}

	empty := site.Theme{RealPath: t.TempDir(), IsTAW: true, Kind: site.KindGutenberg}
	if err := (CoreEnricher{}).Enrich(context.Background(), &site.Site{}, &empty); err != nil {
		t.Fatalf("a theme without vendor/ is not an enrich error: %v", err)
	}
	if empty.Core.Installed != "" || empty.Core.LockMismatch || empty.Scaffold.Name != "taw-gutenberg" {
		t.Errorf("empty = %+v", empty)
	}

	bad := t.TempDir()
	write(t, filepath.Join(bad, "vendor", "composer", "installed.json"), `{"packages":`)
	broken := site.Theme{RealPath: bad, IsTAW: true}
	if err := (CoreEnricher{}).Enrich(context.Background(), &site.Site{}, &broken); err == nil || broken.Core.Err == "" {
		t.Errorf("invalid installed.json: err=%v core=%+v", err, broken.Core)
	}
}

func TestGitEnricherNotARepo(t *testing.T) {
	r := &exec.FakeRunner{Script: func(exec.Spec) (exec.Result, error) { return exec.Result{Code: 128}, nil }}
	th := site.Theme{RealPath: "/x", IsTAW: true}
	if err := (GitEnricher{Runner: r}).Enrich(context.Background(), &site.Site{}, &th); err != nil || th.Git != nil {
		t.Errorf("err=%v git=%+v", err, th.Git)
	}
}

type fakeLookup struct {
	latest map[string]string
	errs   []error
}

func (fakeLookup) Name() string { return "github" }
func (f fakeLookup) Lookup(context.Context) (map[string]string, []error) {
	return f.latest, f.errs
}

func TestScannerAppliesLatest(t *testing.T) {
	p := fleet(t)
	write(t, filepath.Join(p.Home, "Local Sites", "client", "app", "public", "wp-content", "themes", "clienttheme",
		"vendor", "composer", "installed.json"), `{"packages":[{"name":"taw/core","version":"v1.59.2"}]}`)

	sc := Scanner{
		Sources:   []Source{NewLocalSource(p)},
		Enrichers: []Enricher{CoreEnricher{}},
		Lookups: []Lookup{fakeLookup{
			latest: map[string]string{LatestCore: "v1.76.1", LatestTheme: "v1.12.43"},
			errs:   []error{errors.New("taw-gutenberg: rate limited")},
		}},
	}
	rep, err := sc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Latest[LatestCore] != "v1.76.1" {
		t.Errorf("Latest = %v", rep.Latest)
	}
	if len(rep.Errors) != 1 || rep.Errors[0].Stage != "github" {
		t.Errorf("lookup errors = %+v", rep.Errors)
	}
	c := find(t, rep.Sites, "id1").TAWThemes()[0]
	if !c.Core.Behind || c.Core.Latest != "v1.76.1" || c.Scaffold.Latest != "v1.12.43" {
		t.Errorf("client theme = %+v / %+v", c.Core, c.Scaffold)
	}
	// The umbrella theme has no vendor/: never "behind", the doctor says "missing".
	tw := find(t, rep.Sites, "id2").TAWThemes()[0]
	if tw.Core.Behind {
		t.Errorf("a theme without taw/core installed can't be behind: %+v", tw.Core)
	}
}

func TestGitHubLookupCombinesErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/taw-core/tags") {
			_, _ = w.Write([]byte(`[{"name":"v1.76.1"}]`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	defer srv.Close()
	c := github.New("", nil)
	c.BaseURL = srv.URL

	latest, errs := GitHubLookup{Client: c}.Lookup(context.Background())
	if latest[LatestCore] != "v1.76.1" || len(latest) != 1 {
		t.Errorf("latest = %v", latest)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "taw-gutenberg, taw-theme") ||
		!strings.Contains(errs[0].Error(), "rate limit") {
		t.Errorf("errs = %v", errs)
	}

	c.Offline = true
	if _, errs := (GitHubLookup{Client: c}).Lookup(context.Background()); len(errs) != 1 || !errors.Is(errs[0], github.ErrOffline) {
		t.Errorf("offline errs = %v", errs)
	}
}
