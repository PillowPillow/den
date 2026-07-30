package config

import (
	"errors"
	"io/fs"
	"syscall"
)

// FileError renders the REASON for a filesystem failure without repeating the path.
//
// The OS's *fs.PathError already carries the absolute path, and every den
// caller names that path themselves in their own context, so wrapping the raw
// error would repeat it. A bare %v on the raw error would drop the repetition
// but break the chain: errors.Is(err, fs.ErrNotExist) is genuinely used on
// these errors (internal/nest/nest.go to tell an absent nest apart,
// internal/spawn/spawn.go to distinguish a purged cache from a corrupt mixin).
// Hence the type: Error() renders only the reason, Unwrap keeps the chain intact.
//
// The translator below is deliberately short: whatever it doesn't recognize
// passes through unchanged — an exact message beats a wrong one.
type FileError struct{ Err error }

func (e *FileError) Error() string {
	// The reason alone: it's *fs.PathError that duplicates the path, and its
	// Err field says what actually happened.
	reason := e.Err
	var pe *fs.PathError
	if errors.As(e.Err, &pe) {
		reason = pe.Err
	}
	switch {
	case errors.Is(reason, fs.ErrNotExist):
		return "file does not exist"
	case errors.Is(reason, fs.ErrPermission):
		return "permission denied"
	case errors.Is(reason, syscall.EISDIR):
		return "is a directory, not a file"
	case errors.Is(reason, syscall.ENOTDIR):
		return "a path element is not a directory"
	default:
		return reason.Error()
	}
}

// Unwrap returns the ORIGINAL error, *fs.PathError included: that's what
// errors.Is/errors.As must find, not the isolated reason Error() displays.
func (e *FileError) Unwrap() error { return e.Err }
