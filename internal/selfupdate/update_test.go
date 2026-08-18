package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFetcher answers from memory. No test in this repo opens a socket, so the
// whole sequence is exercised through this double — the real HTTPFetcher stays
// the smallest untested surface, exactly like ports.ListenScanner.
type fakeFetcher struct {
	tag            string
	tagErr         error
	bodies         map[string][]byte
	requests       []string
	resolveLatestN int
}

// ResolveLatest counts its own calls separately from Get's: HTTPFetcher's
// ResolveLatest is a real network call too (it follows the /releases/latest
// redirect), so a reordering bug that moved it ahead of the three early
// refusals must be visible to a test — but the up-to-date path legitimately
// calls ResolveLatest and must NOT call Get, so the two cannot share one
// counter without breaking that distinction.
func (f *fakeFetcher) ResolveLatest(context.Context) (string, error) {
	f.resolveLatestN++
	if f.tagErr != nil {
		return "", f.tagErr
	}
	return f.tag, nil
}

func (f *fakeFetcher) Get(_ context.Context, url string) ([]byte, error) {
	f.requests = append(f.requests, url)
	body, ok := f.bodies[url]
	if !ok {
		return nil, errors.New("404")
	}
	return body, nil
}

// releaseFixture builds a fake release: the archive, and the checksums.txt that
// matches it.
func releaseFixture(t *testing.T, tag, goos, goarch, binary string) map[string][]byte {
	t.Helper()
	archive := tarGz(t, map[string]string{"den": binary})
	name := ArchiveName(tag, goos, goarch)
	sum := sha256Hex(archive)
	return map[string][]byte{
		DownloadURL(tag, name):            archive,
		DownloadURL(tag, "checksums.txt"): []byte(sum + "  " + name + "\n"),
	}
}

func TestRunReplacesTheBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "den")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fakeFetcher{tag: "v1.8.1", bodies: releaseFixture(t, "v1.8.1", "linux", "amd64", "NEW")}
	var out bytes.Buffer
	req := Request{ExecPath: target, Current: "v1.8.0", GOOS: "linux", GOARCH: "amd64"}
	if err := Run(context.Background(), f, req, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW" {
		t.Fatalf("binary holds %q, want NEW", got)
	}
	assertGolden(t, "update_success.golden", strings.ReplaceAll(out.String(), target, "<path>"))
}

func TestRunSaysNothingToDoWhenCurrent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "den")
	if err := os.WriteFile(target, []byte("same"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fakeFetcher{tag: "v1.8.1"}
	var out bytes.Buffer
	req := Request{ExecPath: target, Current: "v1.8.1", GOOS: "linux", GOARCH: "amd64"}
	if err := Run(context.Background(), f, req, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(f.requests) != 0 {
		t.Fatalf("an up-to-date den downloaded %v", f.requests)
	}
	assertGolden(t, "update_uptodate.golden", out.String())
}

func TestRunRefusesBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name string
		req  Request
	}{
		{"homebrew", Request{ExecPath: "/opt/homebrew/Caskroom/den/1.8.1/den", Current: "v1.8.0"}},
		{"go install", Request{ExecPath: "/Users/dev/go/bin/den", Current: "v1.8.0",
			Env: Env{Gopath: "/Users/dev/go"}}},
		{"local build", Request{ExecPath: "/Users/dev/.local/bin/den", Current: "v1.5.0-17-g0ec48d8-dirty"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeFetcher{tag: "v1.8.1"}
			err := Run(context.Background(), f, c.req, &bytes.Buffer{})
			if err == nil {
				t.Fatal("want a refusal")
			}
			if f.resolveLatestN != 0 {
				t.Fatalf("the refusal came AFTER ResolveLatest was reached (%d call(s))", f.resolveLatestN)
			}
			if len(f.requests) != 0 {
				t.Fatalf("the refusal came AFTER a download: %v", f.requests)
			}
		})
	}
}

func TestRunRefusesAMismatchedChecksum(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "den")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	bodies := releaseFixture(t, "v1.8.1", "linux", "amd64", "NEW")
	name := ArchiveName("v1.8.1", "linux", "amd64")
	bodies[DownloadURL("v1.8.1", "checksums.txt")] =
		[]byte("0000000000000000000000000000000000000000000000000000000000000000  " + name + "\n")
	f := &fakeFetcher{tag: "v1.8.1", bodies: bodies}
	req := Request{ExecPath: target, Current: "v1.8.0", GOOS: "linux", GOARCH: "amd64"}
	if err := Run(context.Background(), f, req, &bytes.Buffer{}); err == nil {
		t.Fatal("a checksum mismatch must refuse")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatal("the binary was replaced despite a failed verification")
	}
}
