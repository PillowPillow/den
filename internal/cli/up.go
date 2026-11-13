package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/instance"
	"github.com/PillowPillow/den/internal/nest"
)

func newUpCmd(deps Deps, denHome *string) *cobra.Command {
	var (
		o      instance.Options
		detach bool
	)
	cmd := &cobra.Command{
		Use:   "up <nest> [-w <branch>] [--only a,b | --without a,b | -i] [--workdir <vm path>] [--detach]",
		Short: "Create a nest's instance if needed, then run Claude in it",
		// MaximumNArgs, not ExactArgs: a missing nest gets the list of nests, not cobra's usage line.
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			cwd, err := deps.Getwd()
			if err != nil {
				return fmt.Errorf("reading the current directory: %w", err)
			}
			if len(args) == 0 {
				seen, err := nest.Visible(home, cwd)
				if err != nil {
					return err
				}
				return fmt.Errorf("den up needs a nest; %s — run `den up <nest>`", seen)
			}
			o.Nest = args[0]
			d := instance.Deps{
				Sbx: deps.Sbx, Git: deps.Git, Prompt: deps.Prompt, DenHome: home, Cwd: cwd,
				Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr(),
			}
			return instance.Up(cmd.Context(), d, o, detach)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&o.Branch, "worktree", "w", "", "run on a git worktree of every repo, on this branch")
	f.StringSliceVar(&o.Selection.Only, "only", nil, "keep only these optional repos")
	f.StringSliceVar(&o.Selection.Without, "without", nil, "drop these optional repos")
	f.BoolVarP(&o.Selection.Interactive, "interactive", "i", false, "pick the optional repos from a checklist")
	f.StringVar(&o.Workdir, "workdir", "", "folder Claude starts in, as a VM path")
	f.BoolVar(&detach, "detach", false, "create the instance without attaching")
	return cmd
}
