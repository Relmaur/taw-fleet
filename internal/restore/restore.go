// Package restore keeps restore points: before a risky step (an update, a
// merge, a content import) the Local site's database and the theme's commit,
// so the site can be put back the way it was.
//
// A restore point holds a full copy of a site's database. It must never be
// where git could commit it or a sync could upload it: points live only
// under taw-fleet's own folder (~/Library/Application Support/taw-fleet/
// restore/), which Guard checks is outside every site folder, theme and git
// repository before anything is written. The database never touches the
// disk anywhere else: wp db export writes to its stdout, straight into a
// private (0600) gzip file.
package restore

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Why a point was made.
const (
	BeforeUpdate  = "update"
	BeforeMerge   = "merge"
	BeforeImport  = "import"
	BeforeRestore = "restore"
	ByHand        = "manual"
)

// dbFile is the database copy's name inside a point's folder.
const dbFile = "database.sql.gz"

// Point is one restore point.
type Point struct {
	ID       string    `json:"id"` // its folder: "20261012-183000-update"
	Site     string    `json:"site"`
	Theme    string    `json:"theme"`
	Reason   string    `json:"reason"`
	Note     string    `json:"note,omitempty"` // "before taw/core 1.93.3", "before merging #12"
	Created  time.Time `json:"created_at"`
	Branch   string    `json:"branch,omitempty"` // "" = detached
	Commit   string    `json:"commit,omitempty"`
	Database bool      `json:"database"`
	DBBytes  int64     `json:"database_bytes,omitempty"`
	NoDB     string    `json:"no_database,omitempty"` // why there's no database copy
	Dir      string    `json:"-"`
}

// Label is the point in a few words: "Oct 12 18:30 · before update".
func (p Point) Label() string {
	why := map[string]string{BeforeUpdate: "before an update", BeforeMerge: "before a merge", BeforeImport: "before a content import",
		BeforeRestore: "before a restore", ByHand: "saved by hand"}[p.Reason]
	if p.Note != "" {
		why = p.Note
	}
	return p.Created.Local().Format("Jan 2 15:04") + " · " + why
}

// Store makes, lists and puts back restore points.
type Store struct {
	Paths  paths.Paths
	Runner exec.Runner
	Keep   int // points kept per site; 0 = none are made
	Now    func() time.Time
}

// Root is where every site's points live.
func (st Store) Root() string { return filepath.Join(st.Paths.DataDir, "restore") }

func (st Store) siteDir(slug string) string { return filepath.Join(st.Root(), slug) }

func (st Store) now() time.Time {
	if st.Now != nil {
		return st.Now()
	}
	return time.Now()
}

// ErrUnsafe means the restore folder is somewhere a database copy must never
// be: inside a site, a theme or a git repository.
var ErrUnsafe = errors.New("unsafe place for a database copy")

// Guard checks that dir is a safe home for database copies: absolute,
// outside the site's folder and every theme (followed through symlinks), and
// in no git repository. It creates dir (0700) to ask git about it.
func (st Store) Guard(ctx context.Context, dir string, s site.Site) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%w: %s isn't an absolute path", ErrUnsafe, dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	var roots []string
	for _, r := range []string{s.Path, s.WebRoot} {
		if r != "" {
			roots = append(roots, r)
		}
	}
	for _, t := range s.Themes {
		roots = append(roots, t.Path, t.RealPath)
	}
	for _, r := range roots {
		if r == "" {
			continue
		}
		if rr, err := filepath.EvalSymlinks(r); err == nil {
			r = rr
		}
		if within(resolved, r) {
			return fmt.Errorf("%w: %s is inside %s", ErrUnsafe, dir, r)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := st.Runner.Run(ctx, exec.Spec{Name: "git", Args: []string{"-C", resolved, "rev-parse", "--show-toplevel"}, Unset: gitEnv})
	if err != nil {
		return fmt.Errorf("%w: couldn't ask git about %s: %w", ErrUnsafe, dir, err)
	}
	if res.Code == 0 {
		return fmt.Errorf("%w: %s is inside the git repository %s, where a database copy could be committed", ErrUnsafe, dir, strings.TrimSpace(string(res.Stdout)))
	}
	return nil
}

// gitEnv would point git somewhere else than -C says.
var gitEnv = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_CEILING_DIRECTORIES"}

// within reports whether path is root or inside it.
func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// Save makes a restore point of the site's database (when it runs) and the
// theme's branch and commit, then keeps only the newest Keep. A stopped site
// gets a point without a database, and NoDB says so. Keep 0 saves nothing
// and returns a zero Point.
func (st Store) Save(ctx context.Context, s site.Site, t site.Theme, reason, note string) (Point, error) {
	if st.Keep <= 0 {
		return Point{}, nil
	}
	p, err := st.save(ctx, s, t, reason, note)
	if err != nil {
		return p, err
	}
	return p, st.prune(s.Slug, p.ID)
}

func (st Store) save(ctx context.Context, s site.Site, t site.Theme, reason, note string) (Point, error) {
	now := st.now()
	p := Point{Site: s.Slug, Theme: t.Dir, Reason: reason, Note: note, Created: now}
	p.ID = now.Format("20060102-150405") + "-" + reason
	p.Dir = filepath.Join(st.siteDir(s.Slug), p.ID)
	for i := 2; exists(p.Dir); i++ { // two in the same second
		p.ID = fmt.Sprintf("%s-%s-%d", now.Format("20060102-150405"), reason, i)
		p.Dir = filepath.Join(st.siteDir(s.Slug), p.ID)
	}
	if err := st.Guard(ctx, st.Root(), s); err != nil {
		return p, err
	}
	if err := os.WriteFile(filepath.Join(st.Root(), ".gitignore"), []byte("# Database copies: never commit anything here.\n*\n"), 0o600); err != nil {
		return p, err
	}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return p, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(p.Dir)
		}
	}()

	if dir := themeDir(t); dir != "" {
		p.Branch, p.Commit = st.gitHead(ctx, dir)
	}
	switch {
	case reason == BeforeMerge: // a merge changes production and the code, never this database
		p.NoDB = "a merge doesn't change the Local database"
	case !s.SockLive:
		p.NoDB = "the site was stopped"
	default:
		n, err := st.export(ctx, s, filepath.Join(p.Dir, dbFile))
		if err != nil {
			return p, fmt.Errorf("couldn't copy %s's database: %w", s.Slug, err)
		}
		p.Database, p.DBBytes = true, n
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return p, err
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "point.json"), append(data, '\n'), 0o600); err != nil {
		return p, err
	}
	ok = true
	return p, nil
}

// export streams wp db export's stdout into a private gzip file: written
// under a temporary name first, so a failed copy never looks like a point.
func (st Store) export(ctx context.Context, s site.Site, file string) (int64, error) {
	spec, err := local.WPSpec(st.Paths, s, []string{"db", "export", "-", "--single-transaction", "--quick", "--lock-tables=false"})
	if err != nil {
		return 0, err
	}
	st.mysqlPath(&spec, s)
	tmp, err := os.OpenFile(file+".partial", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	zw := gzip.NewWriter(tmp)
	var errb strings.Builder
	spec.Stdout, spec.Stderr = zw, &errb
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	res, err := st.Runner.Run(ctx, spec)
	if err == nil && res.Code != 0 {
		err = fmt.Errorf("wp db export exited %d: %s", res.Code, lastLine(errb.String()))
	}
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	fi, err := os.Stat(tmp.Name())
	if err != nil {
		return 0, err
	}
	if fi.Size() < 64 { // an empty gzip is ~20 bytes: nothing came out
		return 0, errors.New("wp db export wrote nothing")
	}
	return fi.Size(), os.Rename(tmp.Name(), file)
}

// mysqlPath puts the site's own MySQL client tools first on the PATH, so wp
// db uses Local's mysqldump and mysql for the site's server version, not
// another one installed on the Mac.
func (st Store) mysqlPath(spec *exec.Spec, s site.Site) {
	if bin, ok := local.MySQLBin(st.Paths, s.MySQLVersion); ok {
		path := "/usr/bin:/bin:/usr/sbin:/sbin"
		if st.Paths.Getenv != nil && st.Paths.Getenv("PATH") != "" {
			path = st.Paths.Getenv("PATH")
		}
		spec.Env = append(spec.Env, "PATH="+bin+":"+path)
	}
}

func (st Store) gitHead(ctx context.Context, dir string) (branch, commit string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if res, err := st.Runner.Run(ctx, exec.Spec{Name: "git", Args: []string{"-C", dir, "rev-parse", "HEAD"}}); err == nil && res.Code == 0 {
		commit = strings.TrimSpace(string(res.Stdout))
	}
	if res, err := st.Runner.Run(ctx, exec.Spec{Name: "git", Args: []string{"-C", dir, "symbolic-ref", "--quiet", "--short", "HEAD"}}); err == nil && res.Code == 0 {
		branch = strings.TrimSpace(string(res.Stdout))
	}
	return branch, commit
}

// List is a site's points, newest first. A folder without a readable
// point.json (a copy cut short) isn't a point.
func (st Store) List(slug string) ([]Point, error) {
	entries, err := os.ReadDir(st.siteDir(slug))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Point
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(st.siteDir(slug), e.Name())
		data, err := os.ReadFile(filepath.Join(dir, "point.json"))
		if err != nil {
			continue
		}
		var p Point
		if json.Unmarshal(data, &p) != nil {
			continue
		}
		p.Dir = dir
		if p.Database {
			if _, err := os.Stat(filepath.Join(dir, dbFile)); err != nil {
				p.Database, p.NoDB = false, "its database copy is missing"
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// Find is a site's point by id; "" or "latest" is the newest.
func (st Store) Find(slug, id string) (Point, error) {
	points, err := st.List(slug)
	if err != nil {
		return Point{}, err
	}
	if len(points) == 0 {
		return Point{}, fmt.Errorf("%s has no restore points", slug)
	}
	if id == "" || id == "latest" {
		return points[0], nil
	}
	for _, p := range points {
		if p.ID == id {
			return p, nil
		}
	}
	return Point{}, fmt.Errorf("%s has no restore point %s (taw-fleet restore %s lists them)", slug, id, slug)
}

// prune keeps the newest Keep points of a site, never removing keep.
func (st Store) prune(slug, keep string) error {
	points, err := st.List(slug)
	if err != nil {
		return err
	}
	n := 0
	for _, p := range points {
		if p.ID == keep {
			n++
			continue
		}
		if n < st.Keep {
			n++
			continue
		}
		if err := os.RemoveAll(p.Dir); err != nil {
			return err
		}
	}
	return nil
}

// Plan is what putting a point back would do, for the question.
type Plan struct {
	Point    Point
	Switch   string // the branch to switch the theme to ("" = it's on it already)
	Moved    bool   // that branch has commits since the point (not undone)
	Database bool   // the database is replaced
}

// Check says what Restore would do, or why it can't: a theme with
// uncommitted changes, or a database copy for a stopped site.
func (st Store) Check(ctx context.Context, s site.Site, t site.Theme, p Point) (Plan, error) {
	pl := Plan{Point: p, Database: p.Database}
	if p.Database && !s.SockLive {
		return pl, fmt.Errorf("%s is stopped: start it first (taw-fleet start %s), so its database can be put back", s.Slug, s.Slug)
	}
	dir := themeDir(t)
	if p.Commit == "" || dir == "" {
		return pl, nil
	}
	{
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		res, err := st.Runner.Run(ctx, exec.Spec{Name: "git", Args: []string{"-C", dir, "status", "--porcelain", "--untracked-files=no"}})
		cancel()
		if err != nil || res.Code != 0 {
			return pl, fmt.Errorf("git status in %s failed", t.Dir)
		}
		if strings.TrimSpace(string(res.Stdout)) != "" {
			return pl, fmt.Errorf("%s has uncommitted changes: commit or stash them first", t.Dir)
		}
	}
	branch, commit := st.gitHead(ctx, dir)
	if p.Branch != "" && branch != p.Branch {
		pl.Switch = p.Branch
	}
	if p.Branch != "" {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if res, err := st.Runner.Run(ctx, exec.Spec{Name: "git", Args: []string{"-C", dir, "rev-parse", p.Branch}}); err == nil && res.Code == 0 {
			commit = strings.TrimSpace(string(res.Stdout))
		}
	}
	pl.Moved = commit != "" && commit != p.Commit
	return pl, nil
}

// Restore puts a point back: first a new point of how things are now (so
// this can be undone too), then the theme onto the point's branch, then the
// database from its copy. A branch with commits since the point isn't
// reset: the summary names the point's commit instead.
func (st Store) Restore(ctx context.Context, s site.Site, t site.Theme, p Point, out io.Writer) (Point, error) {
	pl, err := st.Check(ctx, s, t, p)
	if err != nil {
		return Point{}, err
	}
	before, err := st.save(ctx, s, t, BeforeRestore, "")
	if err != nil {
		return Point{}, fmt.Errorf("couldn't save how things are now first, so nothing was put back: %w", err)
	}
	_, _ = fmt.Fprintf(out, "Saved how things are now: %s\n", before.ID)
	if pl.Switch != "" {
		res, err := st.Runner.Run(ctx, exec.Spec{Name: "git", Args: []string{"-C", themeDir(t), "switch", pl.Switch}})
		if err == nil && res.Code != 0 {
			err = errors.New(lastLine(string(res.Stderr)))
		}
		if err != nil {
			return before, fmt.Errorf("git switch %s: %w", pl.Switch, err)
		}
		_, _ = fmt.Fprintf(out, "%s is on %s again\n", t.Dir, pl.Switch)
	}
	if p.Database {
		if err := st.importDB(ctx, s, filepath.Join(p.Dir, dbFile)); err != nil {
			return before, fmt.Errorf("couldn't put the database back (%s still has the copy; %s has how it was just now): %w", p.ID, before.ID, err)
		}
		_, _ = fmt.Fprintf(out, "%s's database is back as it was at %s\n", s.Slug, p.Created.Local().Format("Jan 2 15:04"))
	}
	if err := st.prune(s.Slug, before.ID); err != nil {
		return before, err
	}
	return before, nil
}

// importDB streams the gzip copy into wp db import's stdin: the database
// is never written out unpacked.
func (st Store) importDB(ctx context.Context, s site.Site, file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	spec, err := local.WPSpec(st.Paths, s, []string{"db", "import", "-"})
	if err != nil {
		return err
	}
	st.mysqlPath(&spec, s)
	spec.Stdin = zr
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	res, err := st.Runner.Run(ctx, spec)
	if err == nil && res.Code != 0 {
		err = fmt.Errorf("wp db import exited %d: %s", res.Code, lastLine(string(res.Stderr)))
	}
	return err
}

func themeDir(t site.Theme) string {
	if t.Git == nil {
		return ""
	}
	if t.RealPath != "" {
		return t.RealPath
	}
	return t.Path
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Size is a byte count in words: "12 MB".
func Size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", (n+(1<<19))>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", (n+(1<<9))>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
