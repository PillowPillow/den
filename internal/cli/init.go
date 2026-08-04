package cli

import (
	"fmt"
	"io/fs"

	den "github.com/PillowPillow/den"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/deninit"
	"github.com/spf13/cobra"
)

// newInitCmd takes denHome the same way newDoctorCmd does: a pointer filled
// in once cobra parses --den-home, so the two commands can be chained
// (`den init && den doctor`) against the same flag value.
func newInitCmd(denHome *string) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create a den home from the shipped example",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			// den.ExampleDenHome is embedded at examples/den-home — the only
			// path go:embed can express from the module root (see embed.go) —
			// so it has to be rooted here to the den-home layout deninit.Run
			// expects (config.yaml, nests/, stacks/ directly under it).
			//
			// Rooted in RunE, not at package init: fs.Sub only fails if
			// "examples/den-home" is missing from the embed, which would mean
			// the go:embed directive itself broke — caught for real by
			// embed_test.go at build time, in CI, long before any user runs
			// this binary. A package-level panic on that same fact would still
			// be true, but it would take down every den command and every
			// internal/cli test the moment this package is imported, not just
			// `den init`; returning the error here confines the blast radius
			// to the one command that actually touches the embed, and keeps
			// the error inside this package's normal "name the file, return
			// it" contract instead of a panic string nothing else in cli uses.
			src, err := fs.Sub(den.ExampleDenHome, "examples/den-home")
			if err != nil {
				return fmt.Errorf("embedded example den home: %w", err)
			}

			return deninit.Run(home, src, cmd.OutOrStdout())
		},
	}
}
