package selfupdate

import (
	"fmt"
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
// will the staging file share a filesystem with the target — on the correct
// side of the first side effect. Without it, "cannot write here" is discovered
// after several megabytes have been fetched.
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
	// Cleanup on every path out: a signal landing between the write and the
	// rename would otherwise leave a stray .den.new.<pid> in the user's bin
	// directory forever.
	defer func() { _ = os.Remove(staging) }()

	if err := os.WriteFile(staging, body, 0o755); err != nil {
		return &WriteError{Dir: dir, Err: err}
	}
	if err := os.Rename(staging, target); err != nil {
		return &WriteError{Dir: dir, Err: err}
	}
	return nil
}
