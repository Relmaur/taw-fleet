package taw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// ImportTimeout bounds `bin/taw content:import` (applying downloads media).
const ImportTimeout = 10 * time.Minute

// ImportRecord is one record of content:import's plan.
type ImportRecord struct {
	Kind    string          `json:"kind"` // post, term, option, media…
	Type    string          `json:"type"`
	Key     string          `json:"key"`
	Slug    string          `json:"slug"`
	Op      string          `json:"op"` // create, update, would-delete…
	Changes json.RawMessage `json:"changes"`
}

// Fields are the record's changed fields ("title: changed"); none = the
// record already matches.
func (r ImportRecord) Fields() []string {
	var m map[string]struct {
		Status string `json:"status"`
	}
	if len(r.Changes) == 0 || r.Changes[0] != '{' || json.Unmarshal(r.Changes, &m) != nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for f, d := range m {
		out = append(out, strings.TrimSpace(f+": "+d.Status))
	}
	sort.Strings(out)
	return out
}

// Name is the record as "post:page:about".
func (r ImportRecord) Name() string {
	mid := r.Type
	if mid == "" {
		mid = r.Key
	}
	return strings.Trim(r.Kind+":"+mid+":"+r.Slug, ":")
}

// ImportPlan is content:import's dry run.
type ImportPlan struct {
	RegistryDrift []string       `json:"registry_drift"`
	Records       []ImportRecord `json:"records"`
	Warnings      []string       `json:"warnings"`
}

// Counts are how many records the plan creates, changes, leaves as they
// are, and would delete.
func (p ImportPlan) Counts() (create, change, same, del int) {
	for _, r := range p.Records {
		switch {
		case r.Op == "create":
			create++
		case strings.Contains(r.Op, "delete"):
			del++
		case len(r.Fields()) > 0:
			change++
		default:
			same++
		}
	}
	return
}

// ImportReport is what an applied content:import did.
type ImportReport struct {
	Created         []json.RawMessage `json:"created"`
	Updated         []json.RawMessage `json:"updated"`
	Skipped         []json.RawMessage `json:"skipped"`
	Deleted         []json.RawMessage `json:"deleted"`
	MediaSideloaded int               `json:"media_sideloaded"`
	RollbackPath    string            `json:"rollback_path"`
	RegistryDrift   []string          `json:"registry_drift"`
	Warnings        []string          `json:"warnings"`
}

// ContentPlan dry-runs `bin/taw content:import <file>`: what importing the
// snapshot would change in the site, writing nothing. The site must run.
func (r Runner) ContentPlan(ctx context.Context, s site.Site, t site.Theme, file string) (ImportPlan, error) {
	var p ImportPlan
	err := r.contentImport(ctx, s, t, file, false, nil, &p)
	return p, err
}

// ContentApply imports the snapshot (`--yes --policy=update`). taw/core
// saves a rollback snapshot first. Progress goes to out.
func (r Runner) ContentApply(ctx context.Context, s site.Site, t site.Theme, file string, out io.Writer) (ImportReport, error) {
	var rep ImportReport
	err := r.contentImport(ctx, s, t, file, true, out, &rep)
	return rep, err
}

func (r Runner) contentImport(ctx context.Context, s site.Site, t site.Theme, file string, apply bool, out io.Writer, v any) error {
	if err := Guard(t, false); err != nil {
		return err
	}
	if !t.HasBinTaw {
		return errors.New("this theme has no bin/taw")
	}
	args := []string{"-d", "mysqli.default_socket=" + s.Socket, "-d", "pdo_mysql.default_socket=" + s.Socket, "-d", "display_errors=stderr",
		"bin/taw", "content:import", file, "--json"}
	if apply {
		args = append(args, "--yes", "--policy=update")
	}
	ctx, cancel := context.WithTimeout(ctx, ImportTimeout)
	defer cancel()
	res, err := r.Exec.Run(ctx, exec.Spec{Dir: t.RealPath, Name: r.php(), Args: args, Env: []string{"MYSQL_UNIX_PORT=" + s.Socket}, Stderr: out})
	if err != nil {
		return err
	}
	text := string(res.Stdout)
	i := strings.Index(text, "{")
	if res.Code != 0 || i < 0 {
		msg := strings.TrimSpace(string(res.Stderr) + "\n" + text)
		if strings.Contains(msg, "content:import") && strings.Contains(msg, "not defined") {
			return errors.New("this theme's bin/taw has no content:import (needs taw/core 1.25+ and the command registered in bin/taw)")
		}
		return fmt.Errorf("bin/taw content:import exited %d: %s", res.Code, tail(msg, 400))
	}
	if err := json.Unmarshal([]byte(text[i:]), v); err != nil {
		return fmt.Errorf("content:import: unreadable JSON: %w (%q)", err, tail(text, 300))
	}
	return nil
}
