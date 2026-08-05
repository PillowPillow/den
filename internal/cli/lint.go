package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/lint"
	"github.com/spf13/cobra"
)

// newLintCmd validates a checkout — a team source repo being developed, in
// its CI or on a laptop. Deliberately den-home-agnostic: the argument is a
// path, and lint never reads the personal configuration, so a CI runner
// needs no den home at all (spec 2026-08-04 §5).
func newLintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lint <path>",
		Short: "Validate a source checkout (stacks, nests, references, confinement)",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			errs := lint.Run(args[0])
			if len(errs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "ok")
				return nil
			}
			// lint.Run resolves its root through EvalSymlinks, so error messages
			// cite the resolved path. We display the user's original arg in our
			// frame for context, but the errors' paths come from lint — the most
			// truthful representation for a checkout that may sit behind symlinks.
			var b strings.Builder
			fmt.Fprintf(&b, "%s is not a valid source:", args[0])
			for _, e := range errs {
				fmt.Fprintf(&b, "\n  - %v", e)
			}
			return errors.New(b.String())
		},
	}
}
