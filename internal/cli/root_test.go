package cli

import (
	"bytes"
	"testing"
)

func TestVersionPrintsBuildInfo(t *testing.T) {
	var out bytes.Buffer
	root := NewRoot(BuildInfo{Version: "1.2.3", Commit: "abc123"}, &out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if got, want := out.String(), "taw-fleet 1.2.3 (abc123)\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestVersionRejectsArguments(t *testing.T) {
	var out bytes.Buffer
	root := NewRoot(BuildInfo{Version: "dev", Commit: "none"}, &out)
	root.SetArgs([]string{"version", "extra"})

	if err := root.Execute(); err == nil {
		t.Fatal("expected an error for an extra argument")
	}
}
