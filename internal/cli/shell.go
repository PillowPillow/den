package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sshagent"
	"github.com/spf13/cobra"
)

// newShellCmd opens a login shell in an already live sandbox.
//
// It is `den exec <name>` with no command, as that form behaved until
// 2026-08-14 — and before that, `den sh`. The spec
// 2026-08-14-den-exec-shell-design.md says why it became a command of its own:
// `docker compose exec` REQUIRES a command, and den claimed to be modelled on
// it while keeping a shell as the default argument. Requiring the command on
// `den exec` leaves the login shell with nowhere to live; this is where it
// lives.
//
// It is NOT a second door. The body is enterSandbox, shared with `den exec`
// byte for byte, and `bash -l` itself is not spelled here at all — an empty
// spawn.Command.Argv means it (internal/spawn/enter.go:85), one layer down,
// where `den spawn` reads the same default.
//
// -T is REGISTERED and always refused, rather than left unknown. A named
// refusal beats cobra's `unknown flag: -T`, which is the argument #60 already
// made for this flag; do not "simplify" it away. Its message is identical, in
// the same words, to `den spawn`'s (internal/spawn/spawn.go:249) — one
// contradiction must not read as two rules (spec §2).
//
// The parameters are `den exec`'s, and pointers/injection follow the same
// reasons: denHome is a POINTER because --den-home is a persistent flag whose
// value only exists once cobra has parsed it; goos, freshness and isTTY are
// threaded from the wiring site so the suite never reads the real machine.
func newShellCmd(denHome *string, runner sbx.Runner, sshAgent func() sshagent.Result, goos string,
	freshness agent.GateOptions, isTTY func() bool) *cobra.Command {

	var workdir string
	var noTTY bool

	cmd := &cobra.Command{
		Use:   "shell <name>",
		Short: "Open a login shell in an existing sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if noTTY {
				return fmt.Errorf(
					"-T asks for no terminal and no command asks for a shell, which needs one — " +
						"give a command after `--`, or drop -T")
			}
			// Argv nil ⇒ `bash -l`. The tty is unconditional: the refusal above
			// is the only way to reach this line without one.
			return enterSandbox(cmd, args[0], nil, true, workdir, enterOptions{
				denHome: denHome, runner: runner, sshAgent: sshAgent,
				goos: goos, freshness: freshness, isTTY: isTTY,
			})
		},
	}

	cmd.Flags().StringVar(&workdir, "workdir", "",
		"working directory for the shell (default: the directory you ran den from, when the sandbox mounts it; otherwise the first workspace it reports)")
	cmd.Flags().BoolVarP(&noTTY, "no-tty", "T", false,
		"refused here — a login shell needs a terminal; use `den exec` for a command")
	// NO SetInterspersed(false) here, unlike `den exec`, and that is a decision
	// rather than an omission: `den shell` takes no command, so it has no second
	// side for a flag to belong to, and interspersing costs nothing. It buys one
	// thing — `den shell api -T` reaches the -T refusal, which names the flag and
	// the remedy. Under SetInterspersed(false) that same line would be refused by
	// ExactArgs as "accepts 1 arg(s), received 2", which names neither.
	return cmd
}
