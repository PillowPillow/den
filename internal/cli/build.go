package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/build"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/spf13/cobra"
)

// newBuildCmd wires `den build [<stack>] [--force]` (spec §6, issue #8).
//
// Wiring and display only, like every other command here: the graph, the
// deterministic order, the cycle refusal and the "is the image already there?"
// arbitration all live in internal/build, and the two system accesses — sbx,
// and running a build.sh — arrive as parameters.
func newBuildCmd(denHome *string, runner sbx.Runner, script build.Script) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "build [stack]",
		Short: "Build stack images, in dependency order",
		Long: "Build stack images in `parent` order.\n\n" +
			"Without an argument, every declared stack is built. With one, its ancestors " +
			"are built only if their image is missing, then the stack itself — --force " +
			"rebuilds the ancestors too. Each stack is built by its own stacks/<name>/build.sh, " +
			"which den runs unchanged.",
		Args: atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Nil is a clean refusal, not a panic — the doctrine every other
			// injected field of cli.Deps states for itself. The wiring tests
			// build Deps by hand and leave this one unset; without the guard the
			// first `den build` through such a tree would dereference it.
			if script == nil {
				return fmt.Errorf("den build: no build runner wired — this is a den bug, report it")
			}
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			stacks, err := config.LoadStacks(home)
			if err != nil {
				return err
			}

			target := ""
			if len(args) == 1 {
				target = args[0]
			}

			chain, err := build.Chain(stacks, target)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(chain) == 0 {
				// Only reachable without a target — a named one that does not
				// exist was refused by Chain. So the diagnosis is "nothing is
				// declared", and it names where to declare it.
				fmt.Fprintf(out, "no stack declared in %s\n", stacks.Root)
				return nil
			}

			// A stack present on disk but unreadable is reported and NOT built,
			// on config.LoadStacks' own doctrine: it must not hide the healthy
			// ones. Reported on stderr and by name — a `den build` that quietly
			// skipped a broken stack would look like it built everything.
			for _, b := range stacks.Broken {
				fmt.Fprintf(cmd.ErrOrStderr(), "stack %s unreadable, not built: %v\n", b.Name, b.Err)
			}

			// The inventory is passed even when the plan will not consult it:
			// SbxImages reads `sbx template ls --json` lazily, so `den build`
			// (all) and `--force` still spend no process on it.
			steps, err := build.Plan(cmd.Context(), chain, target, force, &build.SbxImages{Runner: runner})
			if err != nil {
				return err
			}
			return build.Execute(cmd.Context(), steps, script, out)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"rebuild the ancestors too, instead of skipping the ones whose image is already built")
	return cmd
}
