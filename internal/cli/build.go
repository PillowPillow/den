package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/build"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
	"github.com/spf13/cobra"
)

// newBuildCmd wires `den build [<stack>] [--force]` (spec §6, issue #8).
//
// Wiring and display only, like every other command here: the graph, the
// deterministic order, the cycle refusal, the "is the image already there?"
// arbitration and the create/exec/stop/save/rm sequence itself all live in
// internal/build. The one system access — sbx — arrives as a parameter, same
// as `den ls` and `den sh` share the very Runner this one gets.
func newBuildCmd(denHome *string, runner sbx.Runner) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "build [stack]",
		Short: "Build stack images, in dependency order",
		Long: "Build stack images in `parent` order.\n\n" +
			"Without an argument, every declared stack is built. With one, its ancestors " +
			"are built only if their image is missing, then the stack itself — --force " +
			"rebuilds the ancestors too. den builds each stack in a throwaway sandbox: it " +
			"runs the stack's `provision.steps` inside it, then saves the result as `image:`.",
		Args: atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			target := ""
			if len(args) == 1 {
				target = args[0]
			}

			// A target may be a source reference ("corp:teamstack"):
			// source.Locate is the SOLE place that turns it into a root to
			// load stacks from, exactly as spawn.go resolves a nest or stack
			// reference. A BARE target (or no target at all) leaves
			// stacksRoot at the personal den home unchanged — which is also
			// what keeps a bare `den build` building the LOCAL stacks only:
			// it never touches source.Locate, so an installed source never
			// enters a graph nobody asked it to join. A source's images are
			// built by whoever maintains it.
			stacksRoot := home
			// The REFERENCE the user typed is kept alongside the bare name
			// the graph is keyed by, because every `den build ...` remedy
			// build.Plan prints must name a command addressing THIS stack:
			// `den build devx` and `den build corp:devx` are two different
			// stacks in two different roots, and on a den owning both, the
			// bare one builds successfully and fixes nothing.
			planTarget := build.LocalTarget(target)
			if target != "" {
				var bareTarget string
				if stacksRoot, _, bareTarget, err = source.Locate(home, target); err != nil {
					return err
				}
				planTarget = build.Target{Name: bareTarget, Ref: target}
				target = bareTarget
			}
			stacks, err := config.LoadStacks(stacksRoot)
			if err != nil {
				return err
			}

			chain, excluded, err := build.Chain(stacks, target)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			// A stack present on disk but unreadable is reported and NOT built,
			// on config.LoadStacks' own doctrine: it must not hide the healthy
			// ones. Reported on stderr and by name — a `den build` that quietly
			// skipped a broken stack would look like it built everything.
			//
			// BEFORE the empty-chain branch below, not after: a den whose only
			// stacks are broken produces an empty chain, and returning early
			// would have swallowed the only two lines that say why.
			for _, b := range stacks.Broken {
				fmt.Fprintf(cmd.ErrOrStderr(), "stack %s unreadable, not built: %v\n", b.Name, b.Err)
			}
			// And a stack whose `parent:` chain reaches a stack den cannot
			// resolve is not built either — same doctrine, one level removed.
			// The reason comes composed from internal/build, which is where the
			// verdict is known: an unreadable ancestor and a `parent:` naming
			// nothing send the user to two different files. Printed after the
			// loop above, which carries the unreadable ancestor's own full
			// diagnostic and its path.
			for _, x := range excluded {
				fmt.Fprintf(cmd.ErrOrStderr(), "stack %s not built: %s\n", x.Stack, x.Reason)
			}

			if len(chain) == 0 {
				// Only reachable without a target — a named one that does not
				// exist was refused by Chain. "declared" and "left to build" are
				// two different diagnoses: an empty den needs to be told where
				// stacks go, while a den whose stacks are all broken or excluded
				// has just been told what to fix, and would read the absence
				// message as den forgetting them.
				if len(stacks.Broken) == 0 && len(excluded) == 0 {
					fmt.Fprintf(out, "no stack declared in %s\n", stacks.Root)
				} else {
					fmt.Fprintf(out, "no stack left to build in %s\n", stacks.Root)
				}
				return nil
			}

			// The inventory is passed even when the plan will not consult it:
			// SbxImages reads `sbx template ls --json` lazily, so `den build`
			// (all) and `--force` still spend no process on it.
			steps, err := build.Plan(cmd.Context(), chain, planTarget, force, &build.SbxImages{Runner: runner})
			if err != nil {
				return err
			}
			// errOut is cmd.ErrOrStderr(), not out: the teardown warning a
			// failed `sbx rm --force` can print is a diagnostic, same kind as
			// the two unreadable/excluded loops above it in this function —
			// both already go to stderr, while out stays the build's own
			// progress log.
			return build.Execute(cmd.Context(), steps, build.Deps{Sbx: runner, DenHome: home}, out, cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"rebuild the ancestors too, instead of skipping the ones whose image is already built")
	return cmd
}
