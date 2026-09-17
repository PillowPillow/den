package sbx

import (
	"context"
	"errors"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := map[string]string{
		"sbx version: v0.43.0 79805a6e3c6667520dc2da4f6bdeddae9b700969\n": "v0.43.0",
		"sbx version: v0.38.0\n":           "v0.38.0",
		"noise\nsbx version: v1.2.3 abc\n": "v1.2.3",
		"something else entirely\n":        "",
	}
	for output, want := range cases {
		if got := ParseVersion(output); got != want {
			t.Errorf("ParseVersion(%q) = %q, want %q", output, got, want)
		}
	}
}

// Plain `sbx version`, never `--json`: the JSON form asks the daemon for its
// state, the plain one answers from embedded data (v0.43.0 release note), and
// `den doctor` must not start a daemon to read a number.
func TestVersionRunsThePlainCommand(t *testing.T) {
	f := &Fake{Responses: map[string]Response{
		"version": {Output: []byte("sbx version: v0.43.0 abc\n")},
	}}
	got, err := Version(context.Background(), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v0.43.0" {
		t.Errorf("Version = %q, want v0.43.0", got)
	}
	if !f.HasCalled("version") || len(f.Calls) != 1 || len(f.Calls[0]) != 1 {
		t.Errorf("calls = %v, want exactly [version]", f.Calls)
	}
}

func TestVersionReturnsTheRunnerError(t *testing.T) {
	f := &Fake{Default: Response{Err: errors.New("exit status 1")}}
	if _, err := Version(context.Background(), f); err == nil {
		t.Fatal("a failing `sbx version` must be an error, not an empty version")
	}
}

func TestReleaseVersion(t *testing.T) {
	cases := []struct {
		observed string
		want     string
		wantOK   bool
	}{
		{"v0.43.0-dev", "v0.43.0", true},
		{"0.43.0", "v0.43.0", true},
		{"v1.7.0-3-gabc+meta-x", "v1.7.0", true},
		{"v1.7.0+meta", "v1.7.0", true},
		{"dev", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := ReleaseVersion(c.observed)
		if got != c.want || ok != c.wantOK {
			t.Errorf("ReleaseVersion(%q) = (%q, %v), want (%q, %v)",
				c.observed, got, ok, c.want, c.wantOK)
		}
	}
}
