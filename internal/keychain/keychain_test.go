package keychain

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/exec"
)

func TestSaveLoad(t *testing.T) {
	var stdin string
	f := &exec.FakeRunner{Script: func(s exec.Spec) (exec.Result, error) {
		switch s.Args[0] {
		case "-i":
			b, _ := io.ReadAll(s.Stdin)
			stdin = string(b)
			return exec.Result{}, nil
		case "find-generic-password":
			if stdin == "" {
				return exec.Result{Code: 44}, nil
			}
			return exec.Result{Stdout: []byte("s3cr\"et\n")}, nil
		}
		return exec.Result{Code: 1}, nil
	}}
	k, it := Keychain{Exec: f}, Item{Account: "bugsmash-api-key", Label: "taw-fleet BugSmash API key"}
	if _, err := k.Load(context.Background(), it); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty: %v", err)
	}
	if err := k.Save(context.Background(), it, `s3cr"et`); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		if strings.Contains(strings.Join(c.Args, " "), "s3cr") {
			t.Error("the secret must never be on the command line")
		}
	}
	want := `add-generic-password -U -s taw-fleet -a bugsmash-api-key -l "taw-fleet BugSmash API key" -w "s3cr\"et"` + "\n"
	if stdin != want {
		t.Errorf("stdin = %q, want %q", stdin, want)
	}
	got, err := k.Load(context.Background(), it)
	if err != nil || got != `s3cr"et` {
		t.Errorf("Load = %q, %v", got, err)
	}
	if err := k.Save(context.Background(), it, "two\nlines"); err == nil {
		t.Error("a multi-line secret must be refused")
	}
}

func TestSaveError(t *testing.T) {
	f := &exec.FakeRunner{Script: func(exec.Spec) (exec.Result, error) {
		return exec.Result{Stderr: []byte("security: SecKeychainItemCreate: Error")}, nil
	}}
	if err := (Keychain{Exec: f}).Save(context.Background(), Item{Account: "a"}, "x"); err == nil {
		t.Error("an error on stderr must fail the save")
	}
}
