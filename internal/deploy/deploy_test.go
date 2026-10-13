package deploy

import (
	"context"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/keychain"
)

func TestProfiles(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Profiles {
		if p.ID == "" || p.Name == "" || p.Key == "" || seen[p.ID] {
			t.Errorf("profile %+v", p)
		}
		seen[p.ID] = true
		if p.Method != Rsync && p.Method != Git {
			t.Errorf("%s: method %q", p.ID, p.Method)
		}
	}
	// Any host: an SSH host and a git remote that aren't a vendor.
	for _, id := range []string{"ssh", "git"} {
		if !seen[id] {
			t.Errorf("no %q profile", id)
		}
	}
}

func TestCheckAndVars(t *testing.T) {
	ok := Target{Method: Rsync, Host: "1.2.3.4", Port: "40123", User: "acme", Path: "/www/acme/public/wp-content/themes/acme/", URL: "https://acme.mx"}
	if err := ok.Check(); err != nil {
		t.Fatal(err)
	}
	v := ok.Vars()
	if v["DEPLOY_METHOD"] != "rsync" || v["DEPLOY_PATH"] != "/www/acme/public/wp-content/themes/acme" || v["DEPLOY_PORT"] != "40123" || v["DEPLOY_GIT_REMOTE"] != "" {
		t.Errorf("vars %v", v)
	}
	for _, bad := range []Target{
		{Method: Rsync, Host: "h", User: "u", Path: "/home/u/public_html", URL: "https://x"},              // not a theme folder: --delete
		{Method: Rsync, Host: "h; rm -rf /", User: "u", Path: "/x/themes/a", URL: "https://x"},            // not a host
		{Method: Rsync, Host: "h", User: "u", Path: "/x/themes/a", Port: "ssh", URL: "https://x"},         // port
		{Method: Rsync, Host: "h", User: "u", Path: "/x/themes/a", URL: "acme.mx"},                        // URL
		{Method: Git, Remote: "https://github.com/x/y", GitPath: "wp-content/themes/a", URL: "https://x"}, // not SSH
		{Method: Git, Remote: "git@git.wpengine.com:acme.git", GitPath: "../../etc", URL: "https://x"},    // path
		{Method: "ftp", URL: "https://x"},
	} {
		if bad.Check() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	g := Target{Method: Git, Remote: "git@git.wpengine.com:acme.git", GitPath: "wp-content/themes/acme", URL: "https://acme.mx"}
	if err := g.Check(); err != nil {
		t.Fatal(err)
	}
	if h, p, _ := g.SSHHost(); h != "git.wpengine.com" || p != "22" {
		t.Errorf("git host %s:%s", h, p)
	}
	if h, p, _ := (Target{Method: Git, Remote: "ssh://deploy@git.example.com:2222/srv/site.git"}).SSHHost(); h != "git.example.com" || p != "2222" {
		t.Errorf("ssh:// host %s:%s", h, p)
	}
	if gv := g.Vars(); gv["DEPLOY_GIT_BRANCH"] != "master" || gv["DEPLOY_GIT_PATH"] != "wp-content/themes/acme" || gv["DEPLOY_HOST"] != "" {
		t.Errorf("git vars %v", gv)
	}
}

func TestKey(t *testing.T) {
	k, err := NewKey("taw-fleet deploy acme")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.Public, "ssh-ed25519 ") || !strings.HasSuffix(k.Public, " taw-fleet deploy acme") {
		t.Errorf("public %q", k.Public)
	}
	signer, err := ssh.ParsePrivateKey(k.Private)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))); !strings.HasPrefix(k.Public, got) {
		t.Errorf("private and public halves differ")
	}
	if !strings.HasPrefix(k.Fingerprint(), "SHA256:") {
		t.Errorf("fingerprint %q", k.Fingerprint())
	}

	// The Keychain item is one line; it comes back the same.
	var stored string
	f := &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		if len(s.Args) > 0 && s.Args[0] == "-i" {
			b := make([]byte, 64<<10)
			n, _ := s.Stdin.Read(b)
			stored = string(b[:n])
			return exec.Result{}, nil
		}
		// find-generic-password -w: the value between -w "…" in what was stored.
		_, after, _ := strings.Cut(stored, ` -w "`)
		return exec.Result{Stdout: []byte(strings.TrimSuffix(strings.TrimSpace(after), `"`))}, nil
	}}
	keys := Keys{Keychain: keychain.Keychain{Exec: f}}
	if err := keys.Save(context.Background(), "acme", k); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "PRIVATE KEY") {
		t.Error("the PEM went in unencoded")
	}
	back, err := keys.Load(context.Background(), "acme")
	if err != nil || string(back.Private) != string(k.Private) || back.Public != k.Public {
		t.Errorf("round trip: %v", err)
	}
}

func TestTester(t *testing.T) {
	k, _ := NewKey("taw-fleet deploy acme")
	var keyMode os.FileMode
	var calls []string
	f := &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		calls = append(calls, s.Name+" "+strings.Join(s.Args, " "))
		switch s.Name {
		case "ssh-keyscan":
			return exec.Result{Stdout: []byte("# 1.2.3.4:40123 SSH-2.0\n[1.2.3.4]:40123 ssh-ed25519 AAAAC3Nza\n")}, nil
		case "ssh":
			for i, a := range s.Args {
				if a == "-i" {
					if st, err := os.Stat(s.Args[i+1]); err == nil {
						keyMode = st.Mode().Perm()
					}
				}
			}
			return exec.Result{Stdout: []byte("/usr/bin/rsync\n")}, nil
		}
		return exec.Result{}, nil
	}}
	dir := t.TempDir()
	tr := Tester{Exec: f, Dir: dir}
	target := Target{Method: Rsync, Host: "1.2.3.4", Port: "40123", User: "acme", Path: "/www/a b/public/wp-content/themes/acme", URL: "https://acme.mx"}
	res, err := tr.Test(context.Background(), target, k)
	if err != nil {
		t.Fatal(err)
	}
	if res.KnownHosts != "[1.2.3.4]:40123 ssh-ed25519 AAAAC3Nza" || !strings.Contains(res.Said, "rsync at /usr/bin/rsync") {
		t.Errorf("result %+v", res)
	}
	if keyMode != 0o600 {
		t.Errorf("key file mode %v", keyMode)
	}
	if last := calls[len(calls)-1]; !strings.HasSuffix(last, `acme@1.2.3.4 test -d '/www/a b/public/wp-content/themes' && command -v rsync`) ||
		!strings.Contains(last, "StrictHostKeyChecking=yes") || !strings.Contains(last, "BatchMode=yes") {
		t.Errorf("ssh call %q", last)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}

	// The key refused: SSH's 255.
	f.Script = func(s exec.Spec) (exec.Result, error) {
		if s.Name == "ssh-keyscan" {
			return exec.Result{Stdout: []byte("h ssh-ed25519 AAAA\n")}, nil
		}
		return exec.Result{Code: 255, Stderr: []byte("acme@1.2.3.4: Permission denied (publickey).")}, nil
	}
	if _, err := tr.Test(context.Background(), target, k); err == nil || !strings.Contains(err.Error(), "is the public key added at the host") {
		t.Errorf("refused: %v", err)
	}
	// Nobody home.
	f.Script = func(exec.Spec) (exec.Result, error) { return exec.Result{}, nil }
	if _, err := tr.Test(context.Background(), target, k); err == nil || !strings.Contains(err.Error(), "didn't answer on port 40123") {
		t.Errorf("no answer: %v", err)
	}
}
