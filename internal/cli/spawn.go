package cli

import (
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// newSpawnCmd builds `den spawn <nest> [repo...]`, an ordinary subcommand.
//
// It was not one until 2026-08-05: the root ITSELF carried the spawn's RunE,
// Args and six flags, so that `den api` spawned. That form is gone, and the
// spec 2026-08-05-spawn-command-design.md says why in full. The short version,
// because a reader will be tempted to bring it back for the two keystrokes it
// saved:
//
//   - the six flags lived on root.Flags(), so `den --help` showed them with no
//     owner, and `den --detach` alone fell through to cmd.Help() and swallowed
//     the flag in silence — the §2 "den refuses rather than normalizing in
//     silence" broken on den's most visible surface;
//   - every unknown first argument was a valid nest name by construction, so
//     no token could ever produce "this is not a command, here is what den
//     does". withSuggestion existed to apologize for that, from the wrong end;
//   - a nest named `ls` was unreachable for life. den knew and said so
//     (warnAboutShadowedNests), which is a warning, not a fix. `den spawn ls`
//     is the fix.
//
// deps is a PARAMETER rather than built here, like newDoctorCmd: that is what
// makes the flag-to-spawn.Options wiring checkable — an unwired flag is
// silent — without a test having to run the real `sbx`.
func newSpawnCmd(denHome *string, deps spawn.Deps) *cobra.Command {
	var o spawn.Options

	cmd := &cobra.Command{
		Use:   "spawn <nest> [repo...] [-- <cmd> [args...]]",
		Short: "Spawn or attach a nest's sandbox",
		// spawnArgs, not atLeastOneArg alone: the arguments past the first,
		// before `--`, are repos, and nothing caps how many a spawn may mount;
		// spawnArgs additionally refuses a command with no nest before it.
		Args: spawnArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// `--` splits repos from command. Before it: the nest, then the
			// repos. After it: the command, verbatim. Without it, everything is
			// positional as it always was — a spawn with no command is the
			// unchanged surface.
			//
			// `--` with nothing after it (`den spawn api --`) leaves command
			// empty too, deliberately, the same silent normalization `den
			// exec` allows itself (internal/cli/exec.go): an empty tail is
			// "no command", so the spawn attaches a shell rather than
			// refusing a separator the user did write.
			positional, command := args, []string(nil)
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				positional, command = args[:dash], args[dash:]
			}
			o.Nest = positional[0]
			// Raw: nest.Resolve expands the tilde and absolutizes against the
			// working directory, which internal/spawn reads. Doing it here
			// would put path resolution on the cobra side of the boundary,
			// where no test of the cascade could reach it.
			o.Repos = positional[1:]
			o.Command = command
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			// Local copy: Out, Err and In are decided here, at run time,
			// because they alone depend on the command (and hence on a test's
			// SetOut/SetErr/SetIn). The empty-agent warning goes to Err so it
			// never mixes into the stdout a caller might pipe; the `-i`
			// checklist reads In for the same reason. The terminal probe stays
			// in deps — it describes the machine, not the command.
			//
			// Out set here is not the last word on it: on a non-tty command,
			// spawn.Spawn aliases it to Err itself, so den's own log never
			// joins a file or pipe the command owns (spawn.Deps.Out).
			d := deps
			d.Out = cmd.OutOrStdout()
			d.Err = cmd.ErrOrStderr()
			d.In = cmd.InOrStdin()
			return spawn.Spawn(cmd.Context(), home, o, d)
		},
	}

	cmd.Flags().StringVarP(&o.Worktree, "worktree", "w", "", "worktree to propagate across all repos")
	cmd.Flags().StringVar(&o.Instance, "as", "",
		"name this instance, to run several sandboxes of one nest side by side")
	cmd.Flags().StringVar(&o.Agent, "agent", "", "agent to use (default: defaults.agent)")
	cmd.Flags().StringSliceVar(&o.Without, "without", nil, "exclude these optional repos")
	cmd.Flags().StringSliceVar(&o.Only, "only", nil, "keep only these optional repos")
	cmd.Flags().BoolVar(&o.Detach, "detach", false, "do not attach a shell after the spawn")
	cmd.Flags().BoolVarP(&o.Interactive, "interactive", "i", false,
		"pick the nest's optional repos from a checklist (contradicts --only/--without)")
	cmd.Flags().StringVar(&o.Workdir, "workdir", "",
		"working directory for the command (default: the first workspace the sandbox reports)")
	cmd.Flags().BoolVarP(&o.NoTTY, "no-tty", "T", false,
		"do not allocate a terminal (for pipes and CI)")
	return cmd
}
