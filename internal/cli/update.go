package cli

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/PillowPillow/den/internal/selfupdate"
	"github.com/spf13/cobra"
)

// newUpdateCmd is wiring and nothing else: the classification, the refusals,
// the download and the swap all live in internal/selfupdate, which is what
// keeps `net/http` out of this package (internal/ports/hermeticity_test.go).
//
// No flags, on purpose. `den update` moves this binary to the latest release or
// says why it will not; pinning a version and rolling back stay install.sh's
// job, where DEN_VERSION already does it.
func newUpdateCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update den to the latest release (not for Homebrew or go install)",
		Long: "`den update` replaces the running binary with the latest release: it resolves the tag, " +
			"verifies the published sha256 (refusing on a mismatch, like `install.sh`), and swaps the " +
			"binary through a single atomic rename, so an update while den is running cannot leave a " +
			"half-written file on your PATH.\n\n" +
			"It refuses, naming the right command, when another package manager owns the binary — " +
			"Homebrew or the go toolchain — because overwriting their file would leave them managing a " +
			"version they no longer manage. It also refuses a build from a checkout, which `git describe` " +
			"stamps with a commit count or `-dirty`. There " +
			"are no flags: pin a version or roll back with `DEN_VERSION=v1.0.1 sh install.sh`.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if d.Updater == nil {
				return fmt.Errorf("`den update` was wired without a fetcher — this is a den defect")
			}
			if d.Executable == nil {
				return fmt.Errorf("`den update` was wired without an executable probe — this is a den defect")
			}
			exe, err := d.Executable()
			if err != nil {
				return fmt.Errorf("cannot tell where den is installed (%v) — reinstall with install.sh, "+
					"or `brew upgrade --cask den` under Homebrew", err)
			}
			version := "dev"
			if d.DenVersion != nil {
				version = d.DenVersion()
			}
			req := selfupdate.Request{
				ExecPath: exe,
				Current:  version,
				GOOS:     runtime.GOOS,
				GOARCH:   runtime.GOARCH,
				Env:      selfupdate.EnvFromOS(d.Getenv),
				// The same resolution root.go applies to the executable, applied
				// to the candidate directories: a GOBIN that is itself a symlink
				// otherwise never matches the resolved executable path, and a
				// go-install binary classifies as an archive one.
				Resolve: filepath.EvalSymlinks,
			}
			return selfupdate.Run(cmd.Context(), d.Updater, req, cmd.OutOrStdout())
		},
	}
}
