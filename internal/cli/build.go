package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/internal/build"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/instance"
)

func newBuildCmd(deps Deps, denHome *string) *cobra.Command {
	var o build.Options
	cmd := &cobra.Command{
		Use:   "build <template>",
		Short: "Build a template: provision a throwaway VM, save it as an sbx template and record it",
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
			d := build.Deps{
				Deps: instance.Deps{
					Sbx: deps.Sbx, Git: deps.Git, Prompt: deps.Prompt, DenHome: home, Cwd: cwd,
					Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr(),
				},
				IsTTY: deps.IsTTY,
			}
			return explainNoTemplate(cmd.Context(), deps, build.Build(cmd.Context(), d, args[0], o))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.Pull, "pull", false, "fetch the template's repo again instead of building the local copy")
	f.BoolVar(&o.Yes, "yes", false, "trust a repo declared in a project den.yaml without asking")
	f.BoolVar(&o.Force, "force", false, "replace a template built from another source")
	return cmd
}
