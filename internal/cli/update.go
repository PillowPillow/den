package cli

import (
	"fmt"
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
		Args:  noArgs,
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
			}
			return selfupdate.Run(cmd.Context(), d.Updater, req, cmd.OutOrStdout())
		},
	}
}
