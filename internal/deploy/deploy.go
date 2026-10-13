// Package deploy sets up a theme's deploy to its host, any host: the target
// (SSH + rsync, or git push), host profiles that only pre-fill it and say
// where each value is, a deploy key, and the connection test that pins the
// server's host key. The deploy itself is taw/core's shared theme-deploy.yml
// (plan taw-fleet-agent.md § 9; owner: "several options, not vendor lock-ins").
package deploy

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/Relmaur/taw-fleet/internal/keychain"
)

// Method is how the theme reaches the host.
type Method string

// The methods taw/core's theme-deploy.yml knows.
const (
	Rsync Method = "rsync" // SSH + rsync into the theme's folder on the server
	Git   Method = "git"   // a commit of the built theme pushed to the host's git remote
)

// Profile is a host's defaults and where to find each value. A profile never
// decides anything the test can't confirm; "other" profiles cover any host.
type Profile struct {
	ID, Name string
	Method   Method
	Port     string // the usual SSH port ("" = 22)
	Branch   string // git: the branch the host deploys
	Host     string // where the SSH host is, or its pattern
	User     string
	Path     string // the theme's folder on the server, as a pattern with <theme>
	Remote   string // git: the remote, as a pattern
	GitPath  string // git: where the theme sits in the host's repository
	Key      string // where the public key goes
}

// Profiles are the hosts taw-fleet knows, then any SSH host and any git remote.
var Profiles = []Profile{
	{ID: "wpmudev", Name: "WPMU DEV", Method: Rsync,
		Host: "The Hub → the site → Hosting → SSH/SFTP: the hostname", Port: "",
		User: "the same page: create an SSH user for the site",
		Path: "usually /htdocs/wp-content/themes/<theme> (SSH in and run pwd in the theme folder to be sure)",
		Key:  "The Hub → Hosting → SSH/SFTP → the SSH user → Public key"},
	{ID: "wpengine", Name: "WP Engine (SSH gateway)", Method: Rsync,
		Host: "<install>.ssh.wpengine.net", Port: "22", User: "<install> (the environment's name)",
		Path: "/home/wpe-user/sites/<install>/wp-content/themes/<theme>",
		Key:  "WP Engine User Portal → Profile → SSH keys"},
	{ID: "wpengine-git", Name: "WP Engine (git push)", Method: Git,
		Remote: "git@git.wpengine.com:<install>.git", Branch: "master", GitPath: "wp-content/themes/<theme>",
		Key: "WP Engine User Portal → the environment → Git push → add the key"},
	{ID: "kinsta", Name: "Kinsta", Method: Rsync,
		Host: "MyKinsta → the site → Info → SFTP/SSH: Host (an IP)", Port: "the same panel: Port (not 22)",
		User: "the same panel: Username", Path: "/www/<site folder>_<id>/public/wp-content/themes/<theme>",
		Key: "MyKinsta → User settings → SSH keys"},
	{ID: "siteground", Name: "SiteGround", Method: Rsync,
		Host: "Site Tools → Devs → SSH Keys Manager → the key's actions → SSH credentials: Hostname", Port: "18765",
		User: "the same: Username", Path: "/home/<user>/www/<domain>/public_html/wp-content/themes/<theme>",
		Key: "Site Tools → Devs → SSH Keys Manager → Import"},
	{ID: "cloudways", Name: "Cloudways", Method: Rsync,
		Host: "the server's Public IP (Server Management → Master Credentials)", Port: "22",
		User: "an application's SSH user (Application Management → Access Details)",
		Path: "/home/master/applications/<app>/public_html/wp-content/themes/<theme>",
		Key:  "Server Management → Master Credentials → SSH public keys"},
	{ID: "ssh", Name: "Other SSH host (VPS, cPanel with SSH…)", Method: Rsync,
		Host: "the server's hostname or IP", Port: "22", User: "the SSH user",
		Path: "the theme's folder: …/wp-content/themes/<theme>",
		Key:  "~/.ssh/authorized_keys of that user on the server (or the host's SSH keys page)"},
	{ID: "git", Name: "Other git remote", Method: Git,
		Remote: "the host's SSH git remote (user@host:repo.git)", Branch: "master", GitPath: "wp-content/themes/<theme>",
		Key: "the host's deploy keys page"},
}

// FindProfile is a profile by id.
func FindProfile(id string) (Profile, bool) {
	for _, p := range Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// Target is where one site deploys.
type Target struct {
	Method     Method `json:"method"`
	Host       string `json:"host,omitempty"`
	Port       string `json:"port,omitempty"`
	User       string `json:"user,omitempty"`
	Path       string `json:"path,omitempty"`
	Remote     string `json:"git_remote,omitempty"`
	Branch     string `json:"git_branch,omitempty"`
	GitPath    string `json:"git_path,omitempty"`
	KnownHosts string `json:"-"`
	URL        string `json:"production_url"`
}

var (
	themePath = regexp.MustCompile(`/themes/[^/]+/?$`)
	relPath   = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	hostRE    = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	userRE    = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	scpRemote = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9.-]+):(.+)$`)
)

// Check says what's missing or wrong, the same way taw/core's deploy.sh would.
func (t Target) Check() error {
	var bad []string
	if !strings.HasPrefix(t.URL, "https://") && !strings.HasPrefix(t.URL, "http://") {
		bad = append(bad, "the production URL (https://…)")
	}
	switch t.Method {
	case Rsync:
		if !hostRE.MatchString(t.Host) {
			bad = append(bad, "the SSH host")
		}
		if t.Port != "" {
			if n, err := strconv.Atoi(t.Port); err != nil || n < 1 || n > 65535 {
				bad = append(bad, "the SSH port (a number)")
			}
		}
		if !userRE.MatchString(t.User) {
			bad = append(bad, "the SSH user")
		}
		if !strings.HasPrefix(t.Path, "/") || !themePath.MatchString(t.Path) || strings.Contains(t.Path, "..") {
			bad = append(bad, "the theme's folder on the server (an absolute path ending in /themes/<name>)")
		}
	case Git:
		if _, _, err := t.gitHost(); err != nil {
			bad = append(bad, "the git remote (user@host:repo.git or ssh://user@host[:port]/repo.git)")
		}
		if t.GitPath == "" || !relPath.MatchString(t.GitPath) || strings.HasPrefix(t.GitPath, "/") || strings.Contains(t.GitPath, "..") {
			bad = append(bad, "where the theme sits in the host's repository (wp-content/themes/<name>)")
		}
	default:
		bad = append(bad, "the method (rsync or git)")
	}
	if len(bad) > 0 {
		return errors.New("missing or not right: " + strings.Join(bad, "; "))
	}
	return nil
}

// SSHHost is the server the test and the deploy connect to.
func (t Target) SSHHost() (host, port string, err error) {
	if t.Method == Git {
		return t.gitHost()
	}
	port = t.Port
	if port == "" {
		port = "22"
	}
	return t.Host, port, nil
}

func (t Target) gitHost() (host, port string, err error) {
	if m := scpRemote.FindStringSubmatch(t.Remote); m != nil {
		return m[2], "22", nil
	}
	if rest, ok := strings.CutPrefix(t.Remote, "ssh://"); ok {
		hostPart, _, _ := strings.Cut(rest, "/")
		if at := strings.LastIndex(hostPart, "@"); at >= 0 {
			hostPart = hostPart[at+1:]
		}
		host, port, _ = strings.Cut(hostPart, ":")
		if port == "" {
			port = "22"
		}
		if hostRE.MatchString(host) {
			return host, port, nil
		}
	}
	return "", "", errors.New("not an SSH git remote: " + t.Remote)
}

// Vars are the repository variables taw/core's deploy stub reads.
func (t Target) Vars() map[string]string {
	v := map[string]string{"DEPLOY_METHOD": string(t.Method), "DEPLOY_KNOWN_HOSTS": t.KnownHosts}
	switch t.Method {
	case Rsync:
		port := t.Port
		if port == "" {
			port = "22"
		}
		v["DEPLOY_HOST"], v["DEPLOY_PORT"], v["DEPLOY_USER"], v["DEPLOY_PATH"] = t.Host, port, t.User, strings.TrimSuffix(t.Path, "/")
	case Git:
		branch := t.Branch
		if branch == "" {
			branch = "master"
		}
		v["DEPLOY_GIT_REMOTE"], v["DEPLOY_GIT_BRANCH"], v["DEPLOY_GIT_PATH"] = t.Remote, branch, t.GitPath
	}
	return v
}

// VarNames are Vars' names in order.
func VarNames(v map[string]string) []string {
	names := make([]string, 0, len(v))
	for k := range v {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Where is the target in a few words: user@host:path, or the git remote.
func (t Target) Where() string {
	if t.Method == Git {
		return t.Remote + " (" + t.GitPath + ")"
	}
	return t.User + "@" + t.Host + ":" + t.Path
}

// Key is a deploy key: the private half for GitHub's secret, the public half
// for the host.
type Key struct {
	Private []byte // OpenSSH PEM
	Public  string // an authorized_keys line
}

// NewKey makes an ed25519 deploy key.
func NewKey(comment string) (Key, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Key{}, err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return Key{}, err
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return Key{}, err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))) + " " + comment
	return Key{Private: pem.EncodeToMemory(block), Public: line}, nil
}

// Fingerprint is the key's SHA256 fingerprint, as hosts show it.
func (k Key) Fingerprint() string {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.Public))
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(pk)
}

// Keys keeps a site's deploy key in the Keychain between the setup's steps.
type Keys struct{ Keychain keychain.Keychain }

func item(slug string) keychain.Item {
	return keychain.Item{Account: "deploy-" + slug, Label: "taw-fleet deploy key for " + slug + " (until the setup finishes)"}
}

// Save stores the key (base64: the Keychain item is one line).
func (k Keys) Save(ctx context.Context, slug string, key Key) error {
	return k.Keychain.Save(ctx, item(slug), base64.StdEncoding.EncodeToString(key.Private)+" "+base64.StdEncoding.EncodeToString([]byte(key.Public)))
}

// Load reads it; keychain.ErrNotFound when there's none.
func (k Keys) Load(ctx context.Context, slug string) (Key, error) {
	v, err := k.Keychain.Load(ctx, item(slug))
	if err != nil {
		return Key{}, err
	}
	privB64, pubB64, ok := strings.Cut(v, " ")
	priv, err1 := base64.StdEncoding.DecodeString(privB64)
	pub, err2 := base64.StdEncoding.DecodeString(pubB64)
	if !ok || err1 != nil || err2 != nil {
		return Key{}, fmt.Errorf("the stored deploy key for %s is damaged: make a new one", slug)
	}
	return Key{Private: priv, Public: string(pub)}, nil
}

// Delete removes it, once GitHub has it.
func (k Keys) Delete(ctx context.Context, slug string) error {
	return k.Keychain.Delete(ctx, item(slug))
}
