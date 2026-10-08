package exec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// These run /bin/echo and /bin/sh, which every Mac and CI runner has. They
// test the runner itself, not a TAW command.

func TestOSRunnerCapturesOutput(t *testing.T) {
	res, err := OSRunner{}.Run(context.Background(), Spec{Name: "/bin/echo", Args: []string{"a b", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(res.Stdout); got != "a b c\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestOSRunnerNonZeroExitIsNotAnError(t *testing.T) {
	res, err := OSRunner{}.Run(context.Background(), Spec{Name: "/bin/sh", Args: []string{"-c", "echo oops >&2; exit 3"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 3 || !strings.Contains(string(res.Stderr), "oops") {
		t.Errorf("res = %+v", res)
	}
}

func TestOSRunnerTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := OSRunner{}.Run(ctx, Spec{Name: "/bin/sleep", Args: []string{"5"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
}

func TestOSRunnerAddsEnv(t *testing.T) {
	res, err := OSRunner{}.Run(context.Background(), Spec{Name: "/bin/sh", Args: []string{"-c", "printf %s \"$TAW_X\""}, Env: []string{"TAW_X=yes"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "yes" {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

func TestOSRunnerUnsets(t *testing.T) {
	t.Setenv("ELECTRON_RUN_AS_NODE", "1")
	t.Setenv("TAW_KEEP", "kept")
	res, err := OSRunner{}.Run(context.Background(), Spec{Name: "/bin/sh", Args: []string{"-c", "printf %s \"${ELECTRON_RUN_AS_NODE-unset} $TAW_KEEP\""}, Unset: []string{"ELECTRON_RUN_AS_NODE"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "unset kept" {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

func TestOSRunnerStreams(t *testing.T) {
	var out, errOut strings.Builder
	res, err := OSRunner{}.Run(context.Background(), Spec{Name: "/bin/sh", Args: []string{"-c", "echo hi; echo oops >&2; exit 2"}, Stdout: &out, Stderr: &errOut})
	if err != nil || res.Code != 2 || out.String() != "hi\n" || errOut.String() != "oops\n" || len(res.Stdout) != 0 {
		t.Errorf("res=%+v out=%q err=%q %v", res, out.String(), errOut.String(), err)
	}
}

func TestFakeRunnerRecords(t *testing.T) {
	f := &FakeRunner{Script: func(s Spec) (Result, error) {
		return Result{Stdout: []byte(s.Name)}, nil
	}}
	res, err := f.Run(context.Background(), Spec{Name: "git", Args: []string{"status"}})
	if err != nil || string(res.Stdout) != "git" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calls := f.Calls(); len(calls) != 1 || calls[0].Args[0] != "status" {
		t.Errorf("calls = %+v", calls)
	}
}
