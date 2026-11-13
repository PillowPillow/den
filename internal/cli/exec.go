package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/instance"
	"github.com/PillowPillow/den/internal/nest"
)

func newExecCmd(deps Deps, denHome *string) *cobra.Command {
	var o instance.Options
	cmd := &cobra.Command{
		Use:   "exec <nest> [-w <branch>] [--only a,b | --without a,b] [--workdir <vm path>] -- <cmd> [args…]",
		Short: "Create a nest's instance if needed, run one command in it, and return its exit code",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			cwd, err := deps.Getwd()
			if err != nil {
				return fmt.Errorf("reading the current directory: %w", err)
			}
			// ArgsLenAtDash, not SetInterspersed(false): the latter would stop flag parsing at the
			// nest, so `den exec am -w x -- cmd` would hand -w to the command.
			dash := cmd.ArgsLenAtDash()
			names, argv := args, []string(nil)
			if dash >= 0 {
				names, argv = args[:dash], args[dash:]
			}
			if len(names) == 0 {
				seen, err := nest.Visible(home, cwd)
				if err != nil {
					return err
				}
				return fmt.Errorf("den exec needs a nest; %s — run `den exec <nest> -- <cmd>`", seen)
			}
			if len(names) > 1 || len(argv) == 0 {
				return fmt.Errorf("den exec takes one nest, then `--` and the command — run `den exec %s -- <cmd> [args…]`", names[0])
			}
			o.Nest = names[0]
			// Out is stderr: only the command's own output may reach a piped stdout.
			d := instance.Deps{
				Sbx: deps.Sbx, Git: deps.Git, Prompt: deps.Prompt, DenHome: home, Cwd: cwd,
				Out: cmd.ErrOrStderr(), Err: cmd.ErrOrStderr(),
			}
			return instance.Exec(cmd.Context(), d, o, argv)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&o.Branch, "worktree", "w", "", "run on a git worktree of every repo, on this branch")
	f.StringSliceVar(&o.Selection.Only, "only", nil, "keep only these optional repos")
	f.StringSliceVar(&o.Selection.Without, "without", nil, "drop these optional repos")
	f.BoolVarP(&o.Selection.Interactive, "interactive", "i", false, "refused: den exec never asks")
	f.StringVar(&o.Workdir, "workdir", "", "folder the command runs in, as a VM path")
	return cmd
}
