package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// configureSpawn turns the root itself into the spawn command: `den <nest>` is
// not a subcommand, it is the default argument. cobra falls back on the root's
// RunE when args[0] matches no subcommand.
//
// Call it AFTER the root.AddCommand calls: setting Args on the root disables
// cobra's legacyArgs ("unknown command"), and that switch is what makes a nest
// name acceptable in first position.
//
// deps is a parameter rather than built here, like newDoctorCmd: that is what
// makes the flag-to-spawn.Options wiring checkable — an unwired flag is silent
// — without a test having to run the real `sbx`.
func configureSpawn(root *cobra.Command, denHome *string, deps spawn.Deps) {
	var o spawn.Options

	root.Use = "den <nest>"
	root.Args = atMostOneArg
	// Explicit, because cobra does NOT apply it on this path: its default of 2
	// is set in findSuggestions(), which serves the "unknown command" branch den
	// never takes (the root has a RunE). Called directly, SuggestionsFor reads
	// this field as-is, and at 0 `den doctr` would suggest nothing.
	root.SuggestionsMinimumDistance = 2
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		o.Nest = args[0]
		home, err := config.Home(*denHome)
		if err != nil {
			return err
		}
		// Local copy: Out and Err are decided here, at run time, because they
		// alone depend on the command (and hence on a test's SetOut/SetErr).
		// The empty-agent warning goes to Err so it never mixes into the stdout
		// a caller might pipe.
		d := deps
		d.Out = cmd.OutOrStdout()
		d.Err = cmd.ErrOrStderr()
		// In follows the same rule as Out and Err, and for the same reason: the
		// `-i` checklist must read what cobra hands this command (a test's
		// SetIn), never os.Stdin directly. The terminal probe stays in deps —
		// it describes the machine, not the command.
		d.In = cmd.InOrStdin()
		return withSuggestion(root, o.Nest, spawn.Spawn(cmd.Context(), home, o, d))
	}

	root.Flags().StringVarP(&o.Worktree, "worktree", "w", "", "worktree to propagate across all repos")
	root.Flags().StringVar(&o.Agent, "agent", "", "agent to use (default: defaults.agent)")
	root.Flags().StringSliceVar(&o.Without, "without", nil, "exclude these optional repos")
	root.Flags().StringSliceVar(&o.Only, "only", nil, "keep only these optional repos")
	root.Flags().BoolVar(&o.Detach, "detach", false, "do not attach a shell after the spawn")
	root.Flags().BoolVarP(&o.Interactive, "interactive", "i", false,
		"pick the nest's optional repos from a checklist (contradicts --only/--without)")
}

// withSuggestion appends "did you mean ...?" to a failed spawn when the
// requested name is not a nest AND looks like a subcommand. `den doctr` spawns
// a nest named "doctr": that is the price of "the root IS the spawn command"
// (spec §11), and the bare error says nothing about the typo.
//
// The suggestion is attached to the RESOLUTION FAILURE, and only there:
// rejecting up front any argument close to a subcommand would break a nest
// legitimately named "doctr". A nest that EXISTS never reaches this code.
//
// nest.NestNotFoundError rather than errors.Is(err, fs.ErrNotExist): in an
// empty den home a missing config.yaml is also fs.ErrNotExist, and we would
// pin a suggestion on an error that says nothing about the nest.
//
// The candidate names come from root.Commands() through cobra's SuggestionsFor,
// never from a hardcoded list that would drift at the next root.AddCommand.
func withSuggestion(root *cobra.Command, name string, err error) error {
	var notFound *nest.NestNotFoundError
	if !errors.As(err, &notFound) {
		return err
	}
	// The reported name must be the one the user typed: if the spawn sequence
	// ever loaded ANOTHER nest, its absence would say nothing about a typo on
	// the command line.
	if notFound.Name != name {
		return err
	}
	candidates := root.SuggestionsFor(name)
	if len(candidates) == 0 {
		return err
	}
	quoted := make([]string, len(candidates))
	for i, c := range candidates {
		quoted[i] = fmt.Sprintf("`den %s`", c)
	}
	// On its own line: the nest failure stays readable as-is, and the
	// suggestion does not pass itself off as the cause of the error.
	return fmt.Errorf("%w\ndid you mean %s? (an unknown first argument is read "+
		"as a nest name, never as a command)", err, strings.Join(quoted, " or "))
}
