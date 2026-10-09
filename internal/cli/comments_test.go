package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/bugsmash"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

const testProject = "a15552c8-a3d9-4bce-9a06-7859fbb7f27c"

// fakeBugSmash answers like BugSmash's REST API: one project, two open
// comments (one of them old).
func fakeBugSmash(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project/" + testProject:
			_, _ = io.WriteString(w, `{"status":true,"data":{"id":"`+testProject+`","name":"acme.mx"}}`)
		case "/projects":
			_, _ = io.WriteString(w, `{"status":true,"current_page":1,"last_page":1,"data":[{"id":"`+testProject+`","name":"acme.mx","short_url":"https://x.bugsmash.io/review/a"},{"id":"a1617cba-34ff-49c1-9fc3-6a802471010d","name":"Sandbox"}]}`)
		case "/comments":
			_, _ = io.WriteString(w, `{"status":true,"data":[
				{"comment_id":"c1","comment_number":108,"comment":"Actualizar texto por: Buró","status":"Active","comment_by_name":"Paola","created_at":"2026-10-08T18:00:00.000000Z","location_metadata":{"page_url":"https://acme.mx/credito-pyme/"}},
				{"comment_id":"c2","comment_number":99,"comment":"Cambiar el nombre","status":"Active","comment_by_name":"Paola","created_at":"2020-01-01T00:00:00.000000Z","location_metadata":{"page_url":"https://acme.mx/multimedia/"}}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"status":false,"message":"no route"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runComments(t *testing.T, p paths.Paths, in string, r *exec.FakeRunner, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	d := Deps{Paths: p, Runner: r, In: strings.NewReader(in), Out: &out, Err: &out, Dark: true, GitHub: fakeGitHub(t)}
	if srv != nil {
		d.Feedback = func(context.Context) (*bugsmash.Prober, error) {
			return &bugsmash.Prober{Client: &bugsmash.Client{BaseURL: srv.URL, Key: "k"}}, nil
		}
	}
	root := NewRoot(BuildInfo{Version: "1.2.3", Commit: "abc123"}, d)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestComments(t *testing.T) {
	p := fixture(t)
	srv := fakeBugSmash(t)
	if _, err := runComments(t, p, "", keychainFake(), srv, "comments"); err == nil || !strings.Contains(err.Error(), "no BugSmash projects") {
		t.Errorf("no projects configured: %v", err)
	}
	write(t, filepath.Join(p.ConfigDir, "config.toml"), "[sites.acme]\nbugsmash_project = \""+testProject+"\"\n")

	out, err := runComments(t, p, "", keychainFake(), srv, "comments")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"acme", "acme.mx", "2 open", "#108", "/credito-pyme/", "Actualizar texto por: Buró"} {
		if !strings.Contains(out, want) {
			t.Errorf("comments output lacks %q:\n%s", want, out)
		}
	}

	out, err = runComments(t, p, "", keychainFake(), srv, "comments", "acme", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res map[string]site.Feedback
	if err := json.Unmarshal([]byte(out), &res); err != nil || res["acme"].Open != 2 || res["acme"].Project != "acme.mx" {
		t.Errorf("json: %v %+v", err, res)
	}

	out, err = runComments(t, p, "", keychainFake(), srv, "comments", "projects")
	if err != nil || !strings.Contains(out, testProject) || !strings.Contains(out, "← acme") || !strings.Contains(out, "Sandbox") {
		t.Errorf("projects: %v\n%s", err, out)
	}

	out, err = runComments(t, p, "", keychainFake(), srv, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"comments.open"`) || !strings.Contains(out, "2 open comments in BugSmash") {
		t.Errorf("doctor lacks comments.open:\n%s", out)
	}
}

func TestCommentsKey(t *testing.T) {
	p := fixture(t)
	kc := keychainFake()
	if _, err := runComments(t, p, "", kc, nil, "comments", "key", "show"); err == nil || !strings.Contains(err.Error(), "comments key import") {
		t.Errorf("no key yet: %v", err)
	}
	out, err := runComments(t, p, "the-key\n", kc, nil, "comments", "key", "import")
	if err != nil || !strings.Contains(out, "Stored the BugSmash API key") {
		t.Fatalf("import: %v\n%s", err, out)
	}
	for _, c := range kc.Calls() {
		if strings.Contains(strings.Join(c.Args, " "), "the-key") {
			t.Error("the key must never be on the command line")
		}
	}
	if strings.Contains(out, "the-key") {
		t.Error("the key must never be printed")
	}
	srv := fakeBugSmash(t)
	out, err = runComments(t, p, "", kc, srv, "comments", "key", "show")
	if err != nil || !strings.Contains(out, "key from   Keychain") || !strings.Contains(out, "BugSmash accepts it: 2 projects") || strings.Contains(out, "the-key") {
		t.Errorf("show: %v\n%s", err, out)
	}
}

func TestCommentsWithoutKeyMarkEverySite(t *testing.T) {
	p := fixture(t)
	write(t, filepath.Join(p.ConfigDir, "config.toml"), "[sites.acme]\nbugsmash_project = \""+testProject+"\"\n")
	out, err := runComments(t, p, "", keychainFake(), nil, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"comments.no-key"`) {
		t.Errorf("doctor without a key:\n%s", out)
	}
}
