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
// made for this flag; do not "simplify" it away.
//
// Its message is NOT `den spawn`'s (internal/spawn/spawn.go), and the spec's
// claim that the byte-for-byte pair merely moved from `den exec` to `den shell`
// is what this corrects. The two refusals stopped being one contradiction the
// day `den exec` required a command:
//
//   - `den spawn -T` with no command: -T contradicts the DEFAULT argv, and the
//     way out is to give a command — after `--`, which `den spawn` still takes.
//   - `den shell -T`: -T contradicts the COMMAND ITSELF. `den shell` has no
//     command form to fill in, so "give a command after `--`" — the words this
//     message carried until 2026-08-14 — named a shape `den exec` now refuses
//     (`den exec api -- go test` answers "`--` is not needed"). Dead advice.
//
// §2 asks that one contradiction not read as two rules. It does not ask two
// different contradictions to share a remedy neither can honour: den refuses -T
// on both commands, and each names the way out that exists on IT.
//
// The parameters are `den exec`'s MINUS the terminal probe, and pointers /
// injection follow the same reasons: denHome is a POINTER because --den-home is
// a persistent flag whose value only exists once cobra has parsed it; goos and
// freshness are threaded from the wiring site so the suite never reads the real
// machine.
//
// No isTTY, and that is the shape of the decision rather than an oversight:
// this command passes tty=true unconditionally, so it has nothing to ask a
// probe. It took one until 2026-08-14, handed it to enterOptions, and nobody
// read it on either side — a probe carried but never consulted is the first
// half of a second verdict.
func newShellCmd(denHome *string, runner sbx.Runner, sshAgent func() sshagent.Result, goos string,
	freshness agent.GateOptions) *cobra.Command {

	var workdir string
	var noTTY bool

	cmd := &cobra.Command{
		Use:   "shell <name>",
		Short: "Open a login shell in an existing sandbox",
		// exactlyOneArg, not cobra.ExactArgs(1): root.go names cobra's own
		// validators as the ones den never uses, because "accepts 1 arg(s),
		// received 0" never says WHICH argument is missing. Every sibling taking
		// one sandbox name — `rm`, `ports`, `lint`, `source` — goes through this
		// helper, and `den shell` shipped with cobra's for one review cycle
		// because root_test.go's table had no `shell` row. It has one now.
		Args: exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			if noTTY {
				return fmt.Errorf(
					"-T asks for no terminal, and `den shell` opens a login shell, which needs one — "+
						"drop -T, or run your command with `den exec -T %s <cmd>`", args[0])
			}
			// Argv nil ⇒ `bash -l`. The tty is unconditional: the refusal above
			// is the only way to reach this line without one.
			return enterSandbox(cmd, args[0], nil, true, workdir, enterOptions{
				denHome: denHome, runner: runner, sshAgent: sshAgent,
				goos: goos, freshness: freshness,
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
	// the remedy. Under SetInterspersed(false) that same line would arrive as two
	// positionals and be refused by exactlyOneArg for its COUNT ("exactly one
	// argument expected: 2 received — usage: …"), which names neither the flag
	// nor what to do about it.
	return cmd
}
