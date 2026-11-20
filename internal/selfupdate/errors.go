package selfupdate

import (
	"fmt"
)

// The remedy sentences live in one place because they are the whole value of
// the refusal: den's errors name the fix, not just the fault (spec §2 of the
// CLI design). MethodArchive returns nil — it is the one method den updates.
func MethodRefusal(m Method, execPath string) error {
	switch m {
	case MethodHomebrew:
		return fmt.Errorf("den was installed by Homebrew (%s) — run `brew upgrade --cask den`; "+
			"den does not touch a binary another package manager owns", execPath)
	case MethodGoInstall:
		return fmt.Errorf("den was installed by the go toolchain (%s) — run "+
			"`go install github.com/PillowPillow/den/cmd/den@latest`; "+
			"den does not touch a binary another package manager owns", execPath)
	default:
		return nil
	}
}

// VersionError refuses a binary whose version is not a clean release — `dev`,
// or the `git describe` stamp `task build` produces. See IsUpdatableVersion for
// why a semver comparison alone cannot catch the second one.
type VersionError struct {
	Observed string
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("den version %q is not a released build — `den update` replaces a release "+
		"with a release; run `git pull && task build` for a checkout, or install a release with install.sh",
		e.Observed)
}

// MajorVersionError refuses to swap the binary across a major version. v2 is a
// rewrite that shares no config or state with v1, so a silent swap would leave
// the user with v1 sandboxes nothing can remove and worktrees still registered
// in git. The v1 binary is the only tool that can clean them up, which is why
// the remedy is spelled out here rather than behind a flag: there is no flag,
// and a reinstall through install.sh or Homebrew is the deliberate way across.
type MajorVersionError struct {
	Current string
	Latest  string
}

func (e *MajorVersionError) Error() string {
	return fmt.Sprintf("the latest release is den %s, a new major version, and `den update` does not "+
		"cross one: v2 is a rewrite that does not read v1's config (~/.den/config.yaml, stacks, nests) "+
		"and cannot list or remove v1 sandboxes. Before switching, commit and push your work, then run "+
		"`den rm <sandbox>` on each v1 sandbox with this binary (den %s) — once it is replaced, nothing "+
		"is left that can remove them or the git worktrees they registered. The steps are in "+
		"https://github.com/PillowPillow/den/blob/main/docs/migrating-from-v1.md; then install v2 with "+
		"`curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | sh` "+
		"(or `brew upgrade --cask den`)", e.Latest, e.Current)
}

// WriteError reports a destination den cannot write. It names the directory and
// a remedy but NOT a cause: a directory can be unwritable, full, or read-only,
// and asserting "permission denied" would send a full-disk user to chmod. The
// underlying error is appended verbatim and reachable through errors.Is.
type WriteError struct {
	Dir string
	Err error
}

func (e *WriteError) Error() string {
	return fmt.Sprintf("cannot write to %s — pick a writable destination and reinstall with "+
		"`curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | "+
		"DEN_INSTALL_DIR=~/.local/bin sh`: %v", e.Dir, e.Err)
}

func (e *WriteError) Unwrap() error { return e.Err }

// StagingError reports a staging path that already exists, which O_EXCL turns
// into a refusal rather than a followed symlink (see SwapBinary).
//
// It is NOT a WriteError: that message sends the user to reinstall elsewhere
// with DEN_INSTALL_DIR, which is nonsense advice here — the directory is
// writable, one specific file is in the way. den's errors name the remedy, and
// the remedy is that file.
type StagingError struct {
	Path string
	Err  error
}

func (e *StagingError) Error() string {
	return fmt.Sprintf("%s already exists — another `den update` is running, or an earlier one was "+
		"killed before it could clean up: remove the file and retry: %v", e.Path, e.Err)
}

func (e *StagingError) Unwrap() error { return e.Err }
