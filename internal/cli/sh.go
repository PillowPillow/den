package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// newShCmd opens a shell in an already live sandbox.
//
// No den home is read: `den sh` works ONLY on what `sbx ls --json` reports, so
// a broken ~/.den never costs the user their shell.
func newShCmd(runner sbx.Runner) *cobra.Command {
	return &cobra.Command{
		Use:   "sh <name>",
		Short: "Open a shell in an existing sandbox",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			if b := sbx.Find(boxes, name); b != nil {
				// Same guard as `den <nest>`, through the same helper: both
				// paths end in an `sbx exec`. A STOPPED VM passes, `sbx exec`
				// restarts it.
				if err := b.CheckAttachable(); err != nil {
					return err
				}
				if b.IsStopped() {
					fmt.Fprintf(cmd.OutOrStdout(),
						"sandbox %s is stopped: it restarts on attach (its state is kept)\n", b.Name)
				}
				// The workdir comes from the first workspace REPORTED BY THE VM,
				// never from a path recomputed from the config: without it the
				// user lands in the VM's home, not in their code.
				return spawn.Attach(cmd.Context(), runner, b.Name, b.Workdir())
			}

			names := liveNames(boxes)
			if len(names) == 0 {
				return fmt.Errorf("sandbox %q not found — no sandbox is running", name)
			}
			return fmt.Errorf("sandbox %q not found (live: %v)", name, names)
		},
	}
}
