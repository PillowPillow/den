package cli

import (
	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/instance"
)

func newRmCmd(deps Deps, denHome *string) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm <instance> [--force]",
		Short: "Remove an instance den created: its VM, the worktrees den made, its files",
		Args:  cobra.ExactArgs(1),
		// The instances den has a record of, which are the only ones rm acts on.
		ValidArgsFunction: completeInstance(denHome),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			d := instance.Deps{Sbx: deps.Sbx, Git: deps.Git, DenHome: home, Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()}
			return instance.Remove(cmd.Context(), d, args[0], force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "discard uncommitted and ignored files in the worktrees den created")
	return cmd
}
