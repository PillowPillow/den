package cli

import (
	"fmt"
	"io/fs"
	"os"

	den "github.com/PillowPillow/den"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/converge"
	"github.com/PillowPillow/den/internal/deninit"
	"github.com/PillowPillow/den/internal/source"
	"github.com/spf13/cobra"
)

// newInitCmd takes denHome the same way newDoctorCmd does: a pointer filled
// in once cobra parses --den-home, so the two commands can be chained
// (`den init && den doctor`) against the same flag value.
func newInitCmd(denHome *string, d Deps) *cobra.Command {
	var flags convergenceFlags
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a den home, from the shipped example or from a team source",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			if flags.Source != "" {
				return initFromSource(cmd, d, home, flags)
			}
			// Rejected from the flags alone, before any side effect (spec §6):
			// these three only mean something for a source, and silently
			// ignoring them would create a home the user did not ask for. A
			// SLICE, not a map: two flags set must always name the same one
			// first, or the message changes between two identical runs.
			for _, f := range []struct {
				name string
				set  bool
			}{
				{"--name", flags.Name != ""},
				{"--answers", flags.Answers != ""},
				{"--yes", flags.Yes},
			} {
				if f.set {
					return fmt.Errorf(
						"%s configures a team source and `den init` was given no --source — "+
							"pass `--source <url>`, or drop %s to create the example home", f.name, f.name)
				}
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
	flags.bind(cmd, true, true)
	return cmd
}

// initFromSource is `den init --source <url>`: one command that installs a
// team source and configures the machine for it (spec §8).
//
// Everything rejectable is rejected before the first side effect, in the order
// spec §6 fixes for spawn and this path repeats: the clone is read, its content
// linted, the install name arbitrated — and only then does a plan get computed
// and shown. The clone itself lands in the den home's cache, never in
// sources/: nothing a refused confirmation leaves behind is a source.
func initFromSource(cmd *cobra.Command, d Deps, home string, flags convergenceFlags) error {
	c, err := source.AcquireCandidate(cmd.Context(), d.Git, home, flags.Source)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := requireManifest(c, flags.Source, fmt.Sprintf(
		"`den init --source` installs a source that declares its own contract; for a legacy source, "+
			"run `den init` then `den source add %s`", flags.Source)); err != nil {
		return err
	}
	name, err := source.ResolveNamespace(cmd.Context(), d.Git, home, flags.Source, flags.Name, c.Manifest)
	if err != nil {
		return err
	}
	// The SAME judge `den source add` and `den source update` use: lint can
	// never accept what a spawn would later refuse.
	if errs := source.Lint(c.Root); len(errs) > 0 {
		return source.LintRefusal(name, flags.Source, errs)
	}

	fresh, err := freshGlobalConfig(home)
	if err != nil {
		return err
	}
	return runConvergence(cmd, d, converge.ModeInit, home, name, c, flags, fresh)
}

// freshGlobalConfig returns the source-aware config.yaml to write, or nil when
// the home already has one.
//
// nil is what preserves an initialized home byte for byte (spec §8): `den init
// --source` on a working den adds a source, it does not reset the settings.
// The file is only READ here — Service.Apply writes it, after the confirmation,
// in the order that makes an interrupted run resumable.
func freshGlobalConfig(home string) ([]byte, error) {
	if _, err := os.Stat(config.GlobalPath(home)); err == nil {
		return nil, nil
	}
	// Rooted here for the same reason ExampleDenHome is above: go:embed cannot
	// climb out of the module root, so the path is spelled at the use site.
	raw, err := den.SourceAwareDenHome.ReadFile("examples/den-home-source/config.yaml")
	if err != nil {
		return nil, fmt.Errorf("embedded source-aware den home: %w", err)
	}
	return raw, nil
}
