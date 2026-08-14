package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/PillowPillow/den/internal/sshagent"
	"github.com/spf13/cobra"
)

// execArgs accepts one sandbox name, and a command only behind an explicit
// `--`.
//
// The separator is REQUIRED rather than inferred, and that is a refusal den
// makes on purpose (spec §2). `den exec api go test` has two readings — a
// sandbox `api` running `go test`, or three sandbox names — and cobra hands
// both to the command as the same slice. Guessing would make the surface
// depend on whether a word happens to name a live sandbox.
//
// cmd.ArgsLenAtDash is the only thing that survives cobra's flag parsing to
// say where `--` was: it returns the number of positionals seen BEFORE it, or
// -1 when there was none.
func execArgs(cmd *cobra.Command, args []string) error {
	dash := cmd.ArgsLenAtDash()
	if dash < 0 {
		if len(args) == 1 {
			return nil
		}
		if len(args) == 0 {
			return fmt.Errorf("%s: one argument expected, none received — usage: %s",
				cmd.CommandPath(), cmd.UseLine())
		}
		return fmt.Errorf(
			"%s: a command must be separated by `--` — write `%s %s -- %s`",
			cmd.CommandPath(), cmd.CommandPath(), args[0], strings.Join(args[1:], " "))
	}
	if dash != 1 {
		return fmt.Errorf("%s: exactly one sandbox name expected before `--`, %d received — usage: %s",
			cmd.CommandPath(), dash, cmd.UseLine())
	}
	return nil
}

// spawnArgs is atLeastOneArg, aware of `--`: the nest must still be there when
// every positional sits after the separator (`den spawn -- go test` names no
// nest).
func spawnArgs(cmd *cobra.Command, args []string) error {
	dash := cmd.ArgsLenAtDash()
	if dash == 0 {
		return fmt.Errorf("%s: a nest must be named before `--` — usage: %s",
			cmd.CommandPath(), cmd.UseLine())
	}
	return atLeastOneArg(cmd, args)
}

// newExecCmd opens a shell in an already live sandbox, or runs one command in
// it.
//
// It was `den sh` until 2026-08-10, and the rename is the whole point of #60,
// not a matter of taste: den had exactly one door into a live sandbox and it
// was interactive, so every non-interactive caller — a CI step, a task target,
// a script — had to drive a shell or bypass den entirely, losing the sandbox
// name resolution, the §9.1 freshness gate and the ssh-agent warning that den
// owns. `exec` is the name that carries both modes, after `docker compose
// exec`, where a tty is allocated by default and -T turns it off.
//
// `sh` was NOT kept as an alias, though the issue recommended it: two spellings
// of one door means two rows in the command table for one behaviour. The cost
// is measured and accepted — `den sh api` now prints den's unknown-command
// listing, which suggests `den ls` or `den rm` (SuggestionsFor("sh"), distance
// 2) and never `den exec`, four edits away. The listing that follows names
// every command, exec included; that is where the user finds it.
//
// What `den exec` DECIDES comes from the SANDBOX and nothing else: `sbx ls --json`
// for its status, and the kit dispatcher's own journal for the §9.1 freshness
// gate. The den home is read for exactly one advisory purpose — ssh.mode, for
// the empty-agent warning below — and every failure to read it is swallowed, so
// a broken ~/.den still never costs the user their shell. A refused gate does
// cost it, deliberately: that one is a fact about the sandbox being entered,
// not about den's configuration.
//
// denHome is a POINTER for the reason newRmCmd's is: --den-home is a persistent
// flag, and its value only exists once cobra has parsed it, after this
// constructor has returned.
//
// goos names the OS whose ssh-agent remedy the warning quotes. Threaded from
// the wiring site like spawn.Deps.GOOS rather than read here from runtime.GOOS:
// den's convention is that system access is named where the tree is assembled.
//
// freshness is the §9.1 gate's patience, the SAME value spawn.Deps carries, and
// it is threaded for the reason cli.Deps.Freshness is injected at all: its clock
// is real, so a command tree built by a test must be able to hand it a clock
// that is not. `den exec` only consults it on the branch that starts a stopped
// sandbox — the branch that waits.
//
// isTTY is den's own terminal probe, threaded from cli.Deps.IsTTY the same way
// the argument above it is: a nil probe means "no terminal", never "assume
// one", so a wiring test that leaves it unset gets the non-interactive path
// rather than a verdict borrowed from whatever terminal the suite runs under.
func newExecCmd(denHome *string, runner sbx.Runner, sshAgent func() sshagent.Result, goos string,
	freshness agent.GateOptions, isTTY func() bool) *cobra.Command {

	var workdir string
	var noTTY bool

	cmd := &cobra.Command{
		Use:   "exec <name> [-- <cmd> [args...]]",
		Short: "Run a command in an existing sandbox, or open a shell",
		Args:  execArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Everything after `--`. execArgs has already refused every other
			// shape, so this slice is either empty or a real command.
			//
			// `--` with nothing after it (`den exec api --`) leaves command
			// empty too, deliberately: that is the one silent normalization
			// this file allows itself, on purpose — an empty tail is treated
			// as "no command", so it opens a shell exactly as bare `den exec
			// api` does, rather than refusing a separator the user did write.
			var command []string
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				command = args[dash:]
			}
			// A nil probe is "no terminal", never "assume one" — the same rule
			// spawn.interactive applies to `-i`, and the reason the wiring
			// tests that leave IsTTY nil get the non-interactive path instead
			// of a verdict from whatever terminal the suite runs under.
			//
			// With NO command the tty is unconditional: a login shell without
			// one is worth nothing, and that is the behaviour `den sh` had.
			// -T with no command is therefore a contradiction, refused below.
			//
			// With a command the probe DECIDES, and that is not a preference:
			// `sbx exec -t` with no terminal behind it silently DISCARDS the
			// command's output while still returning its status (measured
			// 2026-08-10 on v0.38.0 — `echo hello` into a redirected stdout
			// lands empty, spec §14.0). Passing -it in CI would lose every byte
			// the command wrote and report success.
			tty := len(command) == 0 || (!noTTY && isTTY != nil && isTTY())
			if noTTY && len(command) == 0 {
				return fmt.Errorf(
					"-T asks for no terminal and no command asks for a shell, which needs one — " +
						"give a command after `--`, or drop -T")
			}

			// The SANDBOX name is the flattened reference: ":" is not in
			// sbx's `--name` charset, so a nest loaded from a source never
			// spawns under its prefixed name (spawn.go) — the live VM this
			// command must find is already "corp-api", not "corp:api".
			//
			// This is the ONLY thing `den exec` needs from the reference pair:
			// it reads no nest file, so nestOfSandbox — the reverse decode
			// `den ports` and `den rm` share — has nothing to do here.
			name, err := sandboxNameOf(args[0])
			if err != nil {
				return err
			}
			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			b := sbx.Find(boxes, name)
			if b == nil {
				names := liveNames(boxes)
				if len(names) == 0 {
					return fmt.Errorf("sandbox %q not found — no sandbox is running", name)
				}
				return fmt.Errorf("sandbox %q not found (live: %v)", name, names)
			}
			// Same guard as `den spawn`, through the same helper: both
			// paths end in an `sbx exec`. A STOPPED VM passes, `sbx exec`
			// restarts it.
			if err := b.CheckAttachable(); err != nil {
				return err
			}
			// den's OWN lines go to stderr as soon as a caller might be piping
			// stdout: `den exec api -T -- go build | tee log` must carry the
			// command's output and nothing else. On the interactive path they
			// stay on stdout, where `den sh` always put them — no pipe is
			// listening there, and moving them would change a surface #60 does
			// not touch.
			chatter := cmd.ErrOrStderr()
			if tty {
				chatter = cmd.OutOrStdout()
			}
			stopped := b.IsStopped()
			if stopped {
				fmt.Fprintf(chatter,
					"sandbox %s is stopped: it restarts on attach (its state is kept)\n", b.Name)
			}
			// The §9.1 gate WAITS here even with no terminal, and that is the
			// decision #60 asked for. Under `--detach` den reads once because
			// nobody is at a prompt; `den exec -- <cmd>` fits neither case —
			// nobody is at a prompt AND the command being run is very often the
			// agent itself. The gate's reason ("you are about to run that
			// agent") is more true here, not less.
			if err := spawn.CheckFreshnessOnReentry(
				cmd.Context(), runner, chatter, b.Name, stopped, freshness); err != nil {
				return err
			}
			// Before the attach, never after: once the shell has the terminal,
			// a warning printed behind it is a line the user scrolls past on
			// the way out.
			warnEmptyAgentOnReentry(cmd, denHome, sshAgent, goos)
			// The workdir comes from the workspaces REPORTED BY THE VM, never
			// from a path recomputed from the config: without them the user
			// lands in the VM's home, not in their code. Which of them, and
			// how --workdir and the cwd rank, is spawn.StartDir's verdict —
			// `den spawn` calls the same judge, so the two sibling commands
			// cannot open a shell in two different places (#69).
			//
			// The cwd is read HERE, at the caller's edge, and handed in: the
			// judge stays pure, and an unreadable working directory is not an
			// error — `den exec` then behaves exactly as it did before, on the
			// first workspace.
			cwd, err := os.Getwd()
			if err != nil {
				cwd = "" // StartDir skips its cwd rule on "" and falls back.
			}
			return spawn.Enter(cmd.Context(), runner, b.Name, spawn.Command{
				Argv:    command,
				Workdir: spawn.StartDir(workdir, cwd, b.Workspaces),
				TTY:     tty,
			})
		},
	}

	cmd.Flags().StringVar(&workdir, "workdir", "",
		"working directory for the command (default: the directory you ran den from, when the sandbox mounts it; otherwise the first workspace it reports)")
	// Long name `--no-tty` because cobra requires one; -T is the spelling that
	// matters, and it is docker compose's. `-w` is NOT taken here: it is `den
	// spawn`'s worktree, and one letter meaning two things across sibling
	// commands is the collision den refuses elsewhere.
	cmd.Flags().BoolVarP(&noTTY, "no-tty", "T", false,
		"do not allocate a terminal (for pipes and CI)")
	return cmd
}

// warnEmptyAgentOnReentry warns, on the command's stderr, when the sandbox the
// user is about to re-enter would inherit an SSH agent holding no key.
//
// Why `den exec` warns at all: `spawn.WarnEmptySSHAgentOnReentry` holds that
// argument, and the divergence from `den spawn`'s preflight (an absent socket
// says nothing here) with it. This function is the part that belongs to the
// CLI: finding ssh.mode without letting the search cost anything.
//
// Hence errors are swallowed, all of them, and nothing here can fail the
// command. `den exec`'s contract is that a broken den home never stands between
// the user and a live sandbox, and a warning is advisory — so between the two,
// the warning is what gives way. Silently: a "den could not read your config"
// line on a command that reads it only to decide whether to say something else
// would report den's plumbing as if it were the user's problem.
//
// LoadGlobal, not LoadGlobalUnvalidated: the latter is reserved for `den doctor`
// (config.go), which needs to accumulate faults rather than stop at the first.
// The consequence is accepted — an inconsistency elsewhere in config.yaml
// silences this warning — because `den doctor` is the surface that names it, and
// it names the agent too.
//
// The mode comes from the GLOBAL config, which is where the only ssh.mode den
// has lives: nest.Resolve copies g.SSH.Mode verbatim (resolve.go), so no nest
// can override it and `den exec`, which knows no nest, loses nothing by not
// asking one.
func warnEmptyAgentOnReentry(cmd *cobra.Command, denHome *string, sshAgent func() sshagent.Result, goos string) {
	// A nil probe is the one case that needs no config at all: the probe is the
	// only thing that can produce a verdict here, so with none there is nothing
	// for a den home to be read FOR. (The tolerance itself lives in
	// spawn.warnEmptySSHAgent — this is only about not working for nothing, and
	// it is what keeps cli's own wiring tests off the machine's ~/.den.)
	if sshAgent == nil {
		return
	}
	home, err := config.Home(*denHome)
	if err != nil {
		return
	}
	g, err := config.LoadGlobal(home)
	if err != nil {
		return
	}
	spawn.WarnEmptySSHAgentOnReentry(cmd.ErrOrStderr(), g.SSH.Mode,
		os.Getenv("SSH_AUTH_SOCK"), sshAgent, goos)
}
