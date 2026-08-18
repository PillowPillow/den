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
