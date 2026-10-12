package watch

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

var t0 = time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC)

// fleet is one site with a TAW theme on GitHub, production and comments;
// change tweaks it.
func fleet(change func(s *site.Site)) []site.Site {
	s := site.Site{
		Slug: "ls-mxico", Name: "LS México",
		Themes: []site.Theme{{Dir: "ls-mexico", IsTAW: true, GitHub: &site.RepoState{
			Repo: "EmeLambda/ls-mexico", Default: "main", Head: "aaaaaaa1111", HeadCI: site.ChecksPassing,
			Deploy: &site.Deploy{Workflow: "Deploy"},
		}}},
		Production: &site.Production{URL: "https://lsmexico.mx", Reachable: true, Verified: true},
		Feedback:   &site.Feedback{URL: "https://bugsmash.io/p/1", Comments: []site.Comment{{ID: "c1", Number: 1, Text: "old"}}},
	}
	if change != nil {
		change(&s)
	}
	return []site.Site{s}
}

func kinds(events []Event) string {
	var k []string
	for _, e := range events {
		k = append(k, e.Kind)
	}
	return strings.Join(k, ",")
}

func TestFirstCheckOnlyNotes(t *testing.T) {
	bad := fleet(func(s *site.Site) {
		s.Themes[0].GitHub.Deploy.Failed = &site.Run{SHA: "bbb", URL: "https://run/1"}
		s.Themes[0].GitHub.HeadCI = site.ChecksFailing
	})
	st, events := Check(State{}, bad, t0)
	if len(events) != 0 {
		t.Fatalf("first check told %s", kinds(events))
	}
	// The same again: still nothing (it was noted).
	if _, events = Check(st, bad, t0.Add(5*time.Minute)); len(events) != 0 {
		t.Fatalf("second check told %s", kinds(events))
	}
}

func TestDeployCIAndUpdateTellOnce(t *testing.T) {
	st, _ := Check(State{}, fleet(nil), t0)
	bad := fleet(func(s *site.Site) {
		gh := s.Themes[0].GitHub
		gh.Deploy.Failed = &site.Run{SHA: "bbbbbbbbbb", URL: "https://run/2"}
		gh.HeadCI = site.ChecksFailing
		gh.PRs = []site.PullRequest{
			{Number: 12, Title: "taw/core 1.94.0", Branch: "taw/update-20261012-140000", HeadSHA: "c1", Checks: site.ChecksPassing, URL: "https://pr/12"},
			{Number: 13, Title: "a feature", Branch: "feat/x", HeadSHA: "c2", Checks: site.ChecksPassing},
			{Number: 14, Title: "taw/core 1.94.1", Branch: "taw/update-2", HeadSHA: "c3", Checks: site.ChecksPending},
		}
	})
	st, events := Check(st, bad, t0.Add(5*time.Minute))
	if got := kinds(events); got != "deploy.failed,ci.failed,pr.ready" {
		t.Fatalf("events = %s", got)
	}
	if e := events[0]; e.Site != "ls-mxico" || e.Theme != "ls-mexico" || e.URL != "https://run/2" || !strings.Contains(e.Body, "bbbbbbb") {
		t.Errorf("deploy event = %+v", e)
	}
	if !strings.Contains(events[2].Body, "#12") || events[2].URL != "https://pr/12" {
		t.Errorf("ready event = %+v", events[2])
	}
	if _, again := Check(st, bad, t0.Add(10*time.Minute)); len(again) != 0 {
		t.Errorf("told twice: %s", kinds(again))
	}
	// A new failed run, and a new commit on the ready PR, are news again.
	worse := fleet(func(s *site.Site) {
		gh := s.Themes[0].GitHub
		gh.Deploy.Failed = &site.Run{SHA: "ccc", URL: "https://run/3"}
		gh.HeadCI = site.ChecksFailing
		gh.PRs = []site.PullRequest{{Number: 12, Branch: "taw/update-20261012-140000", HeadSHA: "c9", Checks: site.ChecksPassing}}
	})
	if _, again := Check(st, worse, t0.Add(15*time.Minute)); kinds(again) != "deploy.failed,pr.ready" {
		t.Errorf("events = %s", kinds(again))
	}
}

func TestGitHubErrorKeepsWhatWasSeen(t *testing.T) {
	bad := fleet(func(s *site.Site) { s.Themes[0].GitHub.Deploy.Failed = &site.Run{URL: "https://run/2"} })
	st, _ := Check(State{}, fleet(nil), t0)
	st, _ = Check(st, bad, t0.Add(time.Minute))
	down := fleet(func(s *site.Site) { s.Themes[0].GitHub = &site.RepoState{Error: "github: 502"} })
	st, events := Check(st, down, t0.Add(2*time.Minute))
	if len(events) != 0 {
		t.Fatalf("outage told %s", kinds(events))
	}
	if _, events = Check(st, bad, t0.Add(3*time.Minute)); len(events) != 0 {
		t.Errorf("after the outage, told again: %s", kinds(events))
	}
}

func TestProductionDownAfterTwoChecksThenBack(t *testing.T) {
	st, _ := Check(State{}, fleet(nil), t0)
	off := fleet(func(s *site.Site) {
		s.Production = &site.Production{URL: "https://lsmexico.mx", Error: "dial tcp: timeout\nmore", ErrorKind: "unreachable", CheckedAt: t0.Add(5 * time.Minute)}
	})
	st, events := Check(st, off, t0.Add(5*time.Minute))
	if len(events) != 0 {
		t.Fatalf("one failed check told %s", kinds(events))
	}
	st, events = Check(st, off, t0.Add(10*time.Minute))
	if kinds(events) != "production.down" || events[0].Body != "lsmexico.mx hasn't answered for 5 minutes: dial tcp: timeout" {
		t.Fatalf("events = %+v", events)
	}
	st, events = Check(st, off, t0.Add(15*time.Minute))
	if len(events) != 0 {
		t.Fatalf("down told twice: %s", kinds(events))
	}
	_, events = Check(st, fleet(nil), t0.Add(40*time.Minute))
	if kinds(events) != "production.up" || !strings.Contains(events[0].Body, "after 35 minutes") {
		t.Fatalf("events = %+v", events)
	}
}

func TestProductionBlipIsQuiet(t *testing.T) {
	st, _ := Check(State{}, fleet(nil), t0)
	st, _ = Check(st, fleet(func(s *site.Site) { s.Production.Reachable = false }), t0.Add(5*time.Minute))
	if _, events := Check(st, fleet(nil), t0.Add(10*time.Minute)); len(events) != 0 {
		t.Errorf("a blip told %s", kinds(events))
	}
}

func TestUnverifiedAndNoKey(t *testing.T) {
	st, _ := Check(State{}, fleet(nil), t0)
	unv := fleet(func(s *site.Site) { s.Production.Verified, s.Production.Error = false, "signature mismatch" })
	st, events := Check(st, unv, t0.Add(time.Minute))
	if kinds(events) != "production.unverified" {
		t.Fatalf("events = %s", kinds(events))
	}
	// No signing key on this Mac: nothing is known; nothing changes.
	nokey := fleet(func(s *site.Site) { s.Production = &site.Production{ErrorKind: "no-key", Error: "no key"} })
	st, events = Check(st, nokey, t0.Add(2*time.Minute))
	if len(events) != 0 || !st.Sites["ls-mxico"].Unverified {
		t.Errorf("no-key: events=%s state=%+v", kinds(events), st.Sites["ls-mxico"])
	}
}

func TestNewComments(t *testing.T) {
	st, _ := Check(State{}, fleet(nil), t0)
	more := fleet(func(s *site.Site) {
		s.Feedback.Comments = []site.Comment{
			{ID: "c3", Number: 3, Author: "Ana", Text: "The logo is blurry\non mobile"},
			{ID: "c2", Number: 2, Text: "Typo"},
			{ID: "c1", Number: 1, Text: "old"},
		}
	})
	st, events := Check(st, more, t0.Add(time.Minute))
	if kinds(events) != "comments.new" {
		t.Fatalf("events = %s", kinds(events))
	}
	if e := events[0]; e.Title != "2 new comments" || e.Body != "LS México: Ana, #3: The logo is blurry (and 1 more)" || e.URL != "https://bugsmash.io/p/1" {
		t.Errorf("event = %+v", e)
	}
	// BugSmash failing doesn't forget them.
	broken := fleet(func(s *site.Site) { s.Feedback = &site.Feedback{Error: "502"} })
	st, _ = Check(st, broken, t0.Add(2*time.Minute))
	if _, events = Check(st, more, t0.Add(3*time.Minute)); len(events) != 0 {
		t.Errorf("told again: %s", kinds(events))
	}
}

func TestNewSiteOrThemeOnlyNotes(t *testing.T) {
	st, _ := Check(State{}, nil, t0)
	bad := fleet(func(s *site.Site) { s.Themes[0].GitHub.Deploy.Failed = &site.Run{URL: "https://run/9"} })
	if _, events := Check(st, bad, t0.Add(time.Minute)); len(events) != 0 {
		t.Errorf("a new site told %s", kinds(events))
	}
}

func TestUptimeAndRecent(t *testing.T) {
	var st State
	for i := range 4 {
		up := i != 2
		st, _ = Check(st, fleet(func(s *site.Site) { s.Production.Reachable = up }), t0.Add(time.Duration(i)*5*time.Minute))
	}
	share, checks := st.UptimeBetween("ls-mxico", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if checks != 4 || share != 0.75 {
		t.Errorf("uptime = %v over %d", share, checks)
	}
	if _, checks := st.UptimeBetween("ls-mxico", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)); checks != 0 {
		t.Errorf("november checks = %d", checks)
	}
	// A year and more later, the old days are gone.
	st, _ = Check(st, fleet(nil), t0.AddDate(1, 2, 0))
	if len(st.Uptime["ls-mxico"]) != 1 {
		t.Errorf("days kept = %v", st.Uptime["ls-mxico"])
	}
}

func TestSaveLoad(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	st, err := Load(p)
	if err != nil || !st.Checked.IsZero() {
		t.Fatalf("empty load = %+v, %v", st, err)
	}
	st, _ = Check(State{}, fleet(nil), t0)
	if err := Save(p, st); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || !got.Checked.Equal(t0) || len(got.Sites["ls-mxico"].Comments) != 1 {
		t.Fatalf("load = %+v, %v", got, err)
	}
	if err := os.WriteFile(File(p), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "state.json") {
		t.Errorf("broken file: %v", err)
	}
}

func TestNotifierTerminalNotifier(t *testing.T) {
	r := &exec.FakeRunner{}
	n := Notifier{Runner: r,
		LookPath: func(name string) (string, error) {
			if name == "terminal-notifier" {
				return "/opt/homebrew/bin/terminal-notifier", nil
			}
			return "", os.ErrNotExist
		},
		Open: func(slug string) []string { return []string{"/opt/homebrew/bin/taw-fleet", "--site", slug} },
	}
	if !n.Clickable() {
		t.Fatal("not clickable")
	}
	e := Event{Kind: DeployFailed, Site: "o'brien", Title: "Deploy failed", Body: "[x] broke", URL: "https://run/1"}
	if err := n.Send(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	c := r.Calls()[0]
	got := strings.Join(c.Args, "|")
	want := `-title|taw-fleet|-subtitle|Deploy failed|-message|\[x] broke|-group|taw-fleet.o'brien.deploy.failed|-sound|default|-execute|'/opt/homebrew/bin/taw-fleet' '--site' 'o'\''brien'`
	if c.Name != "/opt/homebrew/bin/terminal-notifier" || got != want {
		t.Errorf("%s %s\nwant %s", c.Name, got, want)
	}
}

func TestNotifierOsascript(t *testing.T) {
	r := &exec.FakeRunner{}
	n := Notifier{Runner: r, LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
	if n.Clickable() {
		t.Fatal("clickable without terminal-notifier")
	}
	e := Event{Kind: CommentsNew, Site: "x", Title: "1 new comment", Body: `say "hi" & $(rm -rf ~)`}
	if err := n.Send(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	c := r.Calls()[0]
	if c.Name != "/usr/bin/osascript" {
		t.Fatal(c.Name)
	}
	// The values are arguments, never part of the script.
	tail := c.Args[len(c.Args)-3:]
	if tail[0] != e.Body || tail[1] != "taw-fleet" || tail[2] != "1 new comment" {
		t.Errorf("args = %q", c.Args)
	}
	if strings.Contains(strings.Join(c.Args[:len(c.Args)-3], " "), "sound") {
		t.Error("a comment isn't loud")
	}
}

func TestAgent(t *testing.T) {
	p := paths.ForHome(t.TempDir(), nil)
	loaded := false
	r := &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		switch s.Args[0] {
		case "bootstrap":
			loaded = true
		case "bootout":
			if !loaded {
				return exec.Result{Code: 3, Stderr: []byte("Boot-out failed: 3: No such process")}, nil
			}
			loaded = false
		case "print":
			if !loaded {
				return exec.Result{Code: 113}, nil
			}
			return exec.Result{Stdout: []byte("\tstate = not running\n\tlast exit code = 0\n")}, nil
		}
		return exec.Result{}, nil
	}}
	a := Agent{Paths: p, Runner: r, UID: 501, Exe: "/opt/homebrew/bin/taw-fleet"}
	if st := a.Status(context.Background()); st.Installed || st.Loaded {
		t.Fatalf("before install: %+v", st)
	}
	if err := a.Install(context.Background(), 30*time.Second); err == nil {
		t.Error("30s allowed")
	}
	if err := a.Install(context.Background(), 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(a.Plist())
	for _, want := range []string{"<string>" + Label + "</string>", "<string>/opt/homebrew/bin/taw-fleet</string>\n\t\t<string>watch</string>\n\t\t<string>--once</string>",
		"<integer>600</integer>", "<key>RunAtLoad</key>", "/opt/homebrew/bin:", a.Log()} {
		if !strings.Contains(string(data), want) {
			t.Errorf("plist lacks %q:\n%s", want, data)
		}
	}
	calls := r.Calls()
	last := calls[len(calls)-1]
	if last.Name != "/bin/launchctl" || strings.Join(last.Args, " ") != "bootstrap gui/501 "+a.Plist() {
		t.Errorf("last call = %s %v", last.Name, last.Args)
	}
	st := a.Status(context.Background())
	if !st.Installed || !st.Loaded || st.Every != 10*time.Minute || st.Program != "/opt/homebrew/bin/taw-fleet" || st.LastExit != "0" {
		t.Errorf("status = %+v", st)
	}
	if err := a.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Plist()); !os.IsNotExist(err) {
		t.Error("plist still there")
	}
	if err := a.Uninstall(context.Background()); err != nil {
		t.Errorf("uninstall twice: %v", err)
	}
}
