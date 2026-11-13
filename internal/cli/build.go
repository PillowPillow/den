package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/internal/base"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/instance"
	"github.com/PillowPillow/den/internal/nest"
)

func newBuildCmd(deps Deps, denHome *string) *cobra.Command {
	return &cobra.Command{
		Use:   "build <base>",
		Short: "Build a base's image: provision a throwaway VM and save it as a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			cwd, err := deps.Getwd()
			if err != nil {
				return fmt.Errorf("reading the current directory: %w", err)
			}
			b, _, err := nest.FindBase(home, cwd, args[0])
			if err != nil {
				return err
			}
			d := instance.Deps{Sbx: deps.Sbx, DenHome: home, Cwd: cwd, Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()}
			return base.Build(cmd.Context(), d, args[0], b)
		},
	}
}
