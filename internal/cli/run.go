package cli

import (
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// newRunCmd builds `den run <nest> <cmd> [args...]`: create-or-attach, then the
// command. It is `den spawn <nest> -- <cmd>` of 2026-08-15, without the
// separator.
//
// It is NOT compose's ephemeral `run`. `docker compose run` builds a throwaway
// container beside the project that --rm deletes on exit; den has no such
// object. `den run` enters THE nest's sandbox, creates it if absent, and leaves
// it alive. Named here so a compose reader does not discover it by use.
//
// SetInterspersed(false), unlike `den up`: everything after the nest name is
// the command, verbatim, its own flags included. Without it, `den run api go
// test -v` dies on "unknown shorthand flag: 'v'". The consequence is the
// contract's break — den's own flags sit LEFT of the nest name — and enterArgs
// refuses the wrong order by name rather than letting `-T` reach the VM as
// `bash: -T: command not found`.
func newRunCmd(denHome *string, deps spawn.Deps) *cobra.Command {
	var o spawn.Options

	cmd := &cobra.Command{
		Use:   "run <nest> <cmd> [args...]",
		Short: "Spawn or attach a nest's sandbox, then run a command",
		Args: func(cmd *cobra.Command, args []string) error {
			return enterArgs(cmd, args, "nest", "den up")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// enterArgs has refused every other shape, so args[1:] is a real
			// command. No ArgsLenAtDash: under SetInterspersed(false) it is
			// always -1 past the nest name, and the one shape where it is not —
			// a leading `--` — enterArgs already refused.
			o.Nest = args[0]
			o.Command = args[1:]
			return spawnNest(cmd, denHome, o, deps)
		},
	}

	registerSpawnFlags(cmd, &o)
	// REGISTERED and always refused; see newUpCmd for why the refusal is not
	// spelled here but in spawn.go's step 0.
	cmd.Flags().BoolVar(&o.Detach, "detach", false,
		"refused here — `den run` runs a command inside the sandbox; use `den up --detach <nest>`")
	cmd.Flags().BoolVarP(&o.NoTTY, "no-tty", "T", false,
		"do not allocate a terminal (for pipes and CI)")
	cmd.Flags().SetInterspersed(false)
	return cmd
}
