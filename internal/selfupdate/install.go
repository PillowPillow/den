package selfupdate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// stagingName is the name of the file the new binary lands on before the
// rename. The pid keeps two concurrent updates from fighting, and every path
// out of SwapBinary removes it — the same reason install.sh carries a trap for
// its own .den.new.$$.
func stagingName(pid int) string {
	return fmt.Sprintf(".den.new.%d", pid)
}

// ProbeWritable creates and removes the staging file BEFORE anything is
// downloaded. It answers two questions at once — is the directory writable, and
// will the staging file share a filesystem with the target — while a refusal
// still costs nothing. Without it, "cannot write here" is discovered after
// several megabytes have been fetched.
//
// Run calls it AFTER the up-to-date check and before the first download; the
// comment there says why an already-current den must not be refused for a
// directory it was never going to write.
func ProbeWritable(dir string) error {
	probe := filepath.Join(dir, stagingName(os.Getpid()))
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return &WriteError{Dir: dir, Err: err}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(probe)
		return &WriteError{Dir: dir, Err: err}
	}
	if err := os.Remove(probe); err != nil {
		return &WriteError{Dir: dir, Err: err}
	}
	return nil
}

// SwapBinary writes the new binary beside the target and renames it over.
//
// Two steps, not one, and for the reason install.sh spells out: writing
// straight onto the live file fails with ETXTBSY on Linux while den is running,
// and an interrupt mid-copy leaves a truncated binary on PATH. The rename is a
// single atomic operation — a reader gets the old den or the new one, never
// half of either — which is only true because the staging file sits in the
// SAME directory, hence on the same filesystem.
func SwapBinary(target string, body []byte) error {
	dir := filepath.Dir(target)
	staging := filepath.Join(dir, stagingName(os.Getpid()))

	// O_EXCL, not os.WriteFile. WriteFile is O_CREATE|O_TRUNC, which FOLLOWS a
	// symlink sitting at the staging path — and the path is predictable, since
	// ProbeWritable created and removed this exact name before the download.
	// Anyone able to write in the install directory could plant a symlink in
	// that window and have den write the verified bytes, and then chmod 0755,
	// somewhere else entirely. O_EXCL turns that race into an EEXIST refusal,
	// which is the fail-closed answer.
	f, err := os.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return &StagingError{Path: staging, Err: err}
		}
		return &WriteError{Dir: dir, Err: err}
	}
	// Armed only AFTER the exclusive create succeeds, never before: on the
	// EEXIST path the file is somebody else's — a live sibling update, or the
	// planted symlink O_EXCL is there to refuse — and deleting it would both
	// destroy a file den does not own and contradict the message, which tells
	// the user to remove it. From here on the file IS den's, and a signal
	// landing between the write and the rename must not leave a stray
	// .den.new.<pid> in the user's bin directory forever.
	defer func() { _ = os.Remove(staging) }()
	if err := writeStaging(f, body); err != nil {
		return &WriteError{Dir: dir, Err: err}
	}
	if err := os.Rename(staging, target); err != nil {
		return &WriteError{Dir: dir, Err: err}
	}
	syncDir(dir)
	return nil
}

// writeStaging fills the staging file and leaves it closed, durable and 0755.
// It always closes f, including on the error paths — a leaked descriptor here
// would be held for the lifetime of the process.
func writeStaging(f *os.File, body []byte) error {
	defer func() { _ = f.Close() }()
	if _, err := f.Write(body); err != nil {
		return err
	}
	// Chmod through the DESCRIPTOR, not the path. A second os.Chmod(staging,
	// …) would look the name up again and reopen the window O_EXCL was taken
	// to close. The chmod itself is not optional: O_EXCL's 0755 is a REQUEST
	// that the process umask subtracts from, so under a umask of 0111 the file
	// lands 0644 and the rename puts a non-executable den on PATH with no
	// error reported anywhere. install.sh does not have this bug because
	// `install -m 755` is not masked. Spec §5.6 says 0755 flatly.
	if err := f.Chmod(0o755); err != nil {
		return err
	}
	// Without this the rename can reach the disk before the bytes do: the
	// directory entry is journalled, the data blocks are not, and a power loss
	// seconds after `den update` reported success leaves a zero-length den as
	// the only one on PATH — the exact outcome the two-step swap exists to
	// prevent. install.sh has the same gap; this closes it on den's side.
	return f.Sync()
}

// syncDir persists the rename itself. Best effort by design: fsync on a
// directory is legal on darwin and linux, which is all den ships, but some
// filesystems answer EINVAL, and a durable binary that den merely could not
// confirm is not a reason to fail an update that already succeeded.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
