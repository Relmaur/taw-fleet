package main

import (
	"runtime/debug"
	"testing"
)

func TestStamp(t *testing.T) {
	read := func(mv, rev string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			bi := &debug.BuildInfo{Main: debug.Module{Version: mv}}
			if rev != "" {
				bi.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: rev}}
			}
			return bi, true
		}
	}
	cases := []struct {
		name, v, c   string
		read         func() (*debug.BuildInfo, bool)
		wantV, wantC string
	}{
		{"release build wins", "0.7.0", "abc1234", read("v0.6.0", "ffffffffff"), "0.7.0", "abc1234"},
		{"go install", "dev", "none", read("v0.7.0", ""), "v0.7.0", "none"},
		{"go build in a checkout", "dev", "none", read("(devel)", "0123456789abcdef"), "dev", "0123456"},
		{"no build info", "dev", "none", func() (*debug.BuildInfo, bool) { return nil, false }, "dev", "none"},
	}
	for _, tc := range cases {
		got := stamp(tc.v, tc.c, tc.read)
		if got.Version != tc.wantV || got.Commit != tc.wantC {
			t.Errorf("%s: got %+v", tc.name, got)
		}
	}
}
