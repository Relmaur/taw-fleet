package taw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/composer"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// MinOneStep is the first taw/core whose `bin/taw update` taw-fleet runs:
// it reads versions from composer.lock (so vendor/ may run ahead of it) and
// takes Local's paths, which have spaces, through --composer.
const MinOneStep = "v1.91.2"

// OneStepTimeout bounds a whole update: Composer, the framework files, the
// migrations and the checks (phpstan, tests, a front-end build).
const OneStepTimeout = 20 * time.Minute

// ReportFile is where `bin/taw update` writes its report, in the theme.
const ReportFile = ".taw/update-report.md"

// UpdateReport is `bin/taw update --json`.
type UpdateReport struct {
	Status     string   `json:"status"` // updated | up-to-date | failed | refused
	Site       string   `json:"site"`
	Base       string   `json:"base"`
	Branch     string   `json:"branch"`
	Changed    []string `json:"changed"`
	Migrations []string `json:"migrations"`
	Manual     []string `json:"manual"`
	Held       []string `json:"held"`
	Core       struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"core"`
	Checks []struct {
		Name   string `json:"name"`
		Status string `json:"status"` // pass | fail | skip
		Reason string `json:"reason"`
	} `json:"checks"`
	Failure *struct {
		Step    string `json:"step"`
		Command string `json:"command"`
		Out     string `json:"out"`
		Reason  string `json:"reason"`
	} `json:"failure"`
	Delivered *struct {
		How  string `json:"how"`
		URL  string `json:"url"`
		Note string `json:"note"`
	} `json:"delivered"`

	// Bridged: the theme's taw/core had no one-step update, so vendor/
	// took a newer one first (taw/core's version before that).
	Bridged string `json:"-"`
	// ReportPath is the report's absolute path, when it was written.
	ReportPath string `json:"-"`
}

// Policy is what taw-fleet reads of the theme's taw.json, to ask the one
// question before an update (taw/core's `policy` command says all of it).
type Policy struct {
	File    bool   // the theme has a taw.json
	Core    string // minor | patch | pinned:<version>
	Deliver string // pr | pr+merge | branch
}

// ReadPolicy reads taw.json's "update" settings that the question names;
// the rest is taw/core's. A missing file means the defaults.
func ReadPolicy(themeDir string) (Policy, error) {
	p := Policy{Core: "minor", Deliver: "pr"}
	data, err := os.ReadFile(filepath.Join(themeDir, "taw.json"))
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	p.File = true
	var doc struct {
		Update struct {
			Core    string `json:"core"`
			Deliver string `json:"deliver"`
		} `json:"update"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return p, fmt.Errorf("taw.json: %w", err)
	}
	if doc.Update.Core != "" {
		p.Core = doc.Update.Core
	}
	if doc.Update.Deliver != "" {
		p.Deliver = doc.Update.Deliver
	}
	return p, nil
}

// Update runs the theme's whole update, as its taw.json says, with
// `vendor/bin/taw update --json`: a branch, taw/core, the framework files,
// the migrations, the checks, a commit, then a pull request (or what the
// policy says). Its progress lines go to out as they're written.
//
// A theme whose taw/core predates the one-step update gets one first, in
// vendor/ only: Composer moves taw/core, composer.lock goes back to what
// git has, and the new `update` then does the real update on its branch
// (it reads versions from the lock). Never on a dirty tree: the caller
// checks; `update` refuses too.
func (r Runner) Update(ctx context.Context, t site.Theme, out io.Writer) (UpdateReport, error) {
	if err := Guard(t, false); err != nil {
		return UpdateReport{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, OneStepTimeout)
	defer cancel()

	var rep UpdateReport
	installed, _ := composer.InstalledVersion(t.RealPath, composer.CorePackage)
	if composer.Older(installed, MinOneStep) || installed == "" {
		pol, err := ReadPolicy(t.RealPath)
		if err != nil {
			return rep, err
		}
		if pol.Core != "minor" {
			return rep, fmt.Errorf("taw.json keeps taw/core at %q, below the one-step update (taw/core %s): update it by hand (composer update taw/core, then vendor/bin/taw upgrade --apply)", pol.Core, strings.TrimPrefix(MinOneStep, "v"))
		}
		_, _ = fmt.Fprintf(out, "taw/core %s has no one-step update: getting the newest into vendor/ first (composer.lock goes back; the update itself commits it)\n", orNone(installed))
		if err := r.composer(ctx, t, out, "update", "taw/core", "--with-dependencies", "--no-interaction", "--no-progress"); err != nil {
			return rep, err
		}
		now, err := composer.InstalledVersion(t.RealPath, composer.CorePackage)
		if err != nil {
			return rep, fmt.Errorf("after Composer: %w", err)
		}
		if composer.Older(now, MinOneStep) {
			return rep, fmt.Errorf("taw/core only went to %s, which has no one-step update (%s+): `composer why-not taw/core %s` says what holds it back", strings.TrimPrefix(now, "v"), strings.TrimPrefix(MinOneStep, "v"), strings.TrimPrefix(MinOneStep, "v"))
		}
		if res, err := r.Exec.Run(ctx, exec.Spec{Dir: t.RealPath, Name: "git", Args: []string{"checkout", "--", "composer.lock"}}); err != nil || res.Code != 0 {
			return rep, fmt.Errorf("couldn't put composer.lock back (git checkout -- composer.lock): %s", errOr(err, res))
		}
		rep.Bridged = installed
	}

	composerCmd := "composer"
	if r.Composer != "" {
		composerCmd = fmt.Sprintf("%q %q", r.php(), r.Composer)
	}
	res, err := r.Exec.Run(ctx, exec.Spec{
		Dir: t.RealPath, Name: r.php(),
		Args:   []string{"vendor/bin/taw", "update", "--json", "--composer=" + composerCmd},
		Env:    r.env(),
		Stderr: out,
	})
	if err != nil {
		return rep, err
	}
	bridged := rep.Bridged
	s := string(res.Stdout)
	i := strings.Index(s, "{")
	if i < 0 || json.Unmarshal([]byte(s[i:]), &rep) != nil {
		return UpdateReport{Bridged: bridged}, fmt.Errorf("vendor/bin/taw update exited %d without its report: %s", res.Code, tail(strings.TrimSpace(s), 400))
	}
	rep.Bridged = bridged
	if p := filepath.Join(t.RealPath, ReportFile); fileExists(p) {
		rep.ReportPath = p
	}
	return rep, nil
}

// composer runs Local's Composer (or `composer`) in the theme, its output
// to out, with the theme's own post-update hook held back.
func (r Runner) composer(ctx context.Context, t site.Theme, out io.Writer, args ...string) error {
	name := "composer"
	if r.Composer != "" {
		name, args = r.php(), append([]string{r.Composer}, args...)
	}
	res, err := r.Exec.Run(ctx, exec.Spec{Dir: t.RealPath, Name: name, Args: args, Stdout: out, Stderr: out,
		Env: append(r.env(), "TAW_NO_UPGRADE=1")})
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("composer exited %d (output above)", res.Code)
	}
	return nil
}

// env puts the site's PHP first on PATH, so the scripts Composer runs
// (phpstan, the tests) use it too.
func (r Runner) env() []string {
	env := []string{"COMPOSER_NO_INTERACTION=1"}
	if r.PHP != "" {
		env = append(env, "PATH="+filepath.Dir(r.PHP)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return env
}

func orNone(v string) string {
	if v == "" {
		return "(none installed)"
	}
	return strings.TrimPrefix(v, "v")
}

func errOr(err error, res exec.Result) string {
	if err != nil {
		return err.Error()
	}
	return tail(strings.TrimSpace(string(res.Stdout)+string(res.Stderr)), 300)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
