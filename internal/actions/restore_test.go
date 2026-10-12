package actions

import (
	"context"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/exec"
)

func TestUpdateSavesARestorePointFirst(t *testing.T) {
	keep := 5
	for _, inRepo := range []bool{false, true} {
		a, f := setup(t, config.Config{RestoreKeep: &keep})
		s, th := fixture()
		th.RealPath = t.TempDir()
		mustWriteBody(t, th.RealPath+"/vendor/composer/installed.json", `{"packages":[{"name":"taw/core","version":"v1.93.1"}]}`)
		var order []string
		f.Script = func(sp exec.Spec) (exec.Result, error) {
			args := strings.Join(sp.Args, " ")
			switch {
			case strings.Contains(args, "--show-toplevel"):
				order = append(order, "guard")
				if inRepo {
					return exec.Result{Stdout: []byte("/Users/me\n")}, nil
				}
				return exec.Result{Code: 128}, nil
			case strings.HasSuffix(args, "rev-parse HEAD"):
				return exec.Result{Stdout: []byte("abc1234def\n")}, nil
			case strings.Contains(args, "symbolic-ref"):
				return exec.Result{Stdout: []byte("main\n")}, nil
			case strings.HasPrefix(args, "vendor/bin/taw update"):
				order = append(order, "update")
				return exec.Result{Stdout: []byte(`{"status":"up-to-date"}`)}, nil
			}
			return exec.Result{}, nil
		}
		task, err := a.UpdateTask(s, th)
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		_, err = task.Run(context.Background(), &out)
		if inRepo {
			if err == nil || !strings.Contains(err.Error(), "nothing was changed") || strings.Join(order, ",") != "guard" {
				t.Errorf("in a repo: err=%v order=%v", err, order)
			}
			continue
		}
		if err != nil || strings.Join(order, ",") != "guard,update" {
			t.Fatalf("err=%v order=%v", err, order)
		}
		if !strings.Contains(out.String(), "Restore point 20261008-153000-update: no database (the site was stopped), ls-mexico on main at abc1234") {
			t.Errorf("out:\n%s", out.String())
		}
		if points, _ := a.Restores().List(s.Slug); len(points) != 1 {
			t.Errorf("points = %v", points)
		}
	}
}
