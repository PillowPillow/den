package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FileError has ONE job: render the reason for a read failure without
// repeating the path the caller just named.
//
// The defect measured on the assembled binary, on the most banal error there
// is (`den unknown`):
//
//	den: nest "unknown": reading <path>: open <path>: no such file or directory
//
// The absolute path written TWICE — that's the OS's *fs.PathError, which
// already carries the path.
func TestFileErrorDoesNotRepeatThePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absent.yaml")

	_, err := os.ReadFile(path)
	if err == nil {
		t.Fatal("precondition: reading a missing file must fail")
	}

	// Exactly the shape callers use: they name the path, FileError only says
	// the reason.
	wrapped := fmt.Errorf("reading %s: %w", path, &FileError{Err: err})
	message := wrapped.Error()

	if n := strings.Count(message, path); n != 1 {
		t.Errorf("path appears %d times, want 1; message: %s", n, message)
	}
	// The error chain must survive: several callers do
	// errors.Is(err, fs.ErrNotExist) on these errors (internal/nest,
	// internal/spawn), which is what makes the type necessary rather than a
	// plain %v on the raw error.
	if !errors.Is(wrapped, fs.ErrNotExist) {
		t.Errorf("fs.ErrNotExist must remain detectable in the chain; err = %v", wrapped)
	}
}

// The translated reasons, and the fallback: an exact original message beats a
// mistranslated one.
func TestFileErrorTranslatesCommonReasonsAndFallsBackOnTheRest(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"missing", fs.ErrNotExist, "does not exist"},
		{"permission", fs.ErrPermission, "permission denied"},
		// Unrecognized: the original text passes through as-is rather than an
		// invented translation.
		{"unknown", errors.New("exotic OS reason"), "exotic OS reason"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Wrapped in a *fs.PathError, the way the OS really renders it:
			// it's this layer that duplicates the path.
			err := &FileError{Err: &fs.PathError{Op: "open", Path: "/a/path", Err: c.err}}
			if got := err.Error(); !strings.Contains(got, c.want) {
				t.Errorf("Error() = %q, want it to contain %q", got, c.want)
			}
			if strings.Contains(err.Error(), "/a/path") {
				t.Errorf("path must not be repeated; got: %s", err.Error())
			}
		})
	}
}

// An error that is NOT a *fs.PathError must pass through unchanged: the type
// is a reason translator, not a filter that swallows what it doesn't understand.
func TestFileErrorLeavesAPathlessErrorIntact(t *testing.T) {
	raw := errors.New("something else")
	if got := (&FileError{Err: raw}).Error(); got != "something else" {
		t.Errorf("Error() = %q, want the original message", got)
	}
}
