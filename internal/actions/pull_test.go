package actions

import (
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/site"
)

const planJSON = `PHP Notice: something
{"registry_drift":["block hero isn't registered here"],"records":[
 {"kind":"post","type":"page","slug":"about","op":"update","changes":{"title":{"status":"changed"},"content":{"status":"changed"}}},
 {"kind":"post","type":"page","slug":"home","op":"update","changes":[]},
 {"kind":"option","key":"_taw_phone","op":"create","changes":{"value":{"status":"new"}}}],"warnings":[]}`

func TestPullPreviewThenApply(t *testing.T) {
	a, f := setup(t, config.Config{Sites: map[string]config.Site{"ls-mxico": {ProductionURL: "https://lsmexico.mx"}}})
	s, th := fixture()
	s.Socket = "/run/x/mysqld.sock"
	th.HasBinTaw = true
	a.Content = func(_ context.Context, slug, url string) ([]byte, error) {
		if slug != "ls-mxico" || url != "https://lsmexico.mx" {
			t.Errorf("asked %s %s", slug, url)
		}
		return []byte(`{"meta":{"schema":"1.2"},"posts":[{},{}],"media":[{}]}`), nil
	}
	f.Script = func(sp exec.Spec) (exec.Result, error) {
		if slices.Contains(sp.Args, "--yes") {
			return exec.Result{Stdout: []byte(`{"created":[{}],"updated":[{}],"skipped":[],"deleted":[],"media_sideloaded":1,"rollback_path":"/r/rollback.json"}`)}, nil
		}
		return exec.Result{Stdout: []byte(planJSON)}, nil
	}
	var ops []local.Op
	op := func(_ context.Context, o local.Op, _ site.Site) (time.Duration, error) {
		ops = append(ops, o)
		return 0, nil
	}

	task, err := a.PullTask(s, th, op)
	if err != nil {
		t.Fatal(err)
	}
	if task.Writes || !strings.Contains(task.Ask, "Start ls-mxico and preview") {
		t.Errorf("preview: writes=%v ask=%q", task.Writes, task.Ask)
	}
	sum, err := task.Run(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0] != local.Start {
		t.Errorf("a stopped site is started: %v", ops)
	}
	pv, _ := sum.Report.(PullPreview)
	if sum.Headline != "lsmexico.mx → ls-mxico: 1 new, 1 changed, 1 unchanged" || pv.Media != 1 {
		t.Errorf("headline %q, %+v", sum.Headline, pv)
	}
	want := []string{"update  post:page:about  (content: changed, title: changed)", "create  option:_taw_phone  (value: new)", "note: block hero isn't registered here"}
	if strings.Join(sum.Lines, "|") != strings.Join(want, "|") {
		t.Errorf("lines:\n%s", strings.Join(sum.Lines, "\n"))
	}
	if b, err := os.ReadFile(pv.File); err != nil || !strings.Contains(string(b), `"schema":"1.2"`) {
		t.Errorf("snapshot saved: %v", err)
	}
	dry := f.Calls()[0]
	if dry.Dir != th.RealPath || !slices.Contains(dry.Args, "mysqli.default_socket=/run/x/mysqld.sock") || slices.Contains(dry.Args, "--yes") {
		t.Errorf("dry run: %s %v", dry.Dir, dry.Args)
	}

	apply := pv.Apply
	if !apply.Writes || apply.Ask != "Import lsmexico.mx's content into ls-mxico? 1 new, 1 changed; production wins. A rollback snapshot is saved first." {
		t.Errorf("apply ask = %q", apply.Ask)
	}
	sum, err = apply.Run(context.Background(), io.Discard)
	if err != nil || sum.Headline != "Pulled lsmexico.mx into ls-mxico: 1 created, 1 updated, 1 media downloaded" || sum.Lines[0] != "rollback snapshot: /r/rollback.json" {
		t.Errorf("applied: %q %v %v", sum.Headline, sum.Lines, err)
	}
	if last := f.Calls()[len(f.Calls())-1]; !slices.Contains(last.Args, "--policy=update") {
		t.Errorf("apply args %v", last.Args)
	}
}

func TestPullRefusals(t *testing.T) {
	a, _ := setup(t, config.Config{})
	s, th := fixture()
	th.HasBinTaw = true
	if _, err := a.PullTask(s, th, nil); err == nil || !strings.Contains(err.Error(), "production_url") {
		t.Errorf("no production URL: %v", err)
	}
	a.Config.Sites = map[string]config.Site{"ls-mxico": {ProductionURL: "https://lsmexico.mx"}}
	if _, err := a.PullTask(s, th, nil); err == nil || !strings.Contains(err.Error(), "live key import") {
		t.Errorf("no production access: %v", err)
	}
}
