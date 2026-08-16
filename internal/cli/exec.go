package cli

import (
	"fmt"
	"os"

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/PillowPillow/den/internal/sshagent"
	"github.com/spf13/cobra"
)

// execArgs accepts a sandbox name followed by a command. The command needs no
// `--`, and that separator is now refused rather than tolerated.
//
// This reverses the 2026-08-10 contract, on purpose, and the old comment here
// argued for it with a reading that never existed: "`den exec api go test` has
// two readings — a sandbox `api` running `go test`, or three sandbox names".
// The previous execArgs refused anything but exactly one name before `--`
// (`dash != 1`), so three names was never reachable. The genuine ambiguity was
// which side owned a FLAG, and Flags().SetInterspersed(false) — set in
// newExecCmd, the same mechanism docker compose uses — closes it: cobra stops
// parsing flags at the first positional.
//
// Consequence, and it is the real break of 2026-08-14: den's own flags must sit
// LEFT of the sandbox name. `den exec -T api go build`, not `den exec api -T`.
// Refusing the wrong order by name is cheaper than letting `-T` reach the VM,
// where it fails as `bash: -T: command not found` — an error that names nothing
// the user can act on.
//
// cmd.ArgsLenAtDash() is consulted for ONE shape only, and it is the shape
// SetInterspersed(false) does not neutralize. Measured 2026-08-14 on cobra
// v1.10.2: pflag handles the `--` terminator BEFORE its interspersed check, so
// a LEADING `--` still eats the separator and answers 0 — `den exec -- a b`
// arrives here as args == ["a","b"], which would run `b` in a sandbox named
// `a`. Past the first positional the flag parser has already stopped, so `--`
// arrives as an ordinary argument and dash is -1; execRewrite sees it there by
// exact string comparison, not by a heuristic.
//
// The refusals are phrased from execRewrite's shape rather than from the raw
// tail, and that is the 2026-08-14 review's finding: a remedy re-joined from
// what the user typed can propose a line the NEXT check refuses. Read execShape
// for the cases; the property is pinned by TestExecRemediesAreThemselvesLegal,
// which feeds every remedy back through this function.
func execArgs(cmd *cobra.Command, args []string) error {
	s := execRewrite(cmd, args)
	path := cmd.CommandPath()
	// FIRST-DEFECT-WINS, and the order is the order the user can act on. What is
	// MISSING outranks what is misplaced: `den exec api --` has both a stray
	// separator and no command, and telling it to drop the `--` would only earn
	// it the second refusal. The remedy quoted is the same rewritten shape in
	// every branch, so whichever message fires, the line proposed is legal.
	switch {
	case s.name == "":
		// Nothing to name in a remedy: with no sandbox there is no line to
		// propose, so the usage is the useful half of the diagnosis. Covers
		// `den exec`, `den exec --`, and the sandbox named by an unset shell
		// variable — see execShape.haveName for why that last one lands here
		// rather than borrowing a name from the command.
		return fmt.Errorf("%s: a sandbox name and a command expected, none received — usage: %s",
			path, cmd.UseLine())
	case len(s.command) == 0:
		// The example command is den's, not the user's — they gave none. It
		// carries their flags anyway: someone who typed `den exec api -T` gets
		// `den exec -T api go test`, which is their intent, spelled legally.
		return fmt.Errorf("%s: no command given — write `%s`, or `den shell %s` for a shell",
			path, remedyLine(cmd, path, s, []string{"go", "test"}), s.name)
	case cmd.ArgsLenAtDash() == 0:
		// The one shape SetInterspersed(false) does not neutralize, and the only
		// reason this validator consults ArgsLenAtDash at all: pflag ate the
		// separator, so `--` is not in args and s.sawDash cannot see it.
		//
		// A den flag typed before that leading `--` was ALSO eaten, by the flag
		// parser this time. It used to be absent from the line proposed here,
		// and the omission was written down as a decision — "the flag is one
		// cobra has honoured". False in its own terms: cobra honoured it on an
		// invocation that is REFUSED and never runs, so the user retypes a line
		// missing their --workdir and lands somewhere else in silence. Since
		// 2026-08-16 remedyLine reads those flags back off the FlagSet.
		return fmt.Errorf("%s: `--` is not needed, and a sandbox name must come first — write `%s`",
			path, remedyLine(cmd, path, s, s.command))
	case s.sawDash:
		return fmt.Errorf("%s: `--` is not needed — write `%s`",
			path, remedyLine(cmd, path, s, s.command))
	case len(s.flags) > 0:
		return fmt.Errorf("%s: den's flags go before the sandbox name — write `%s`",
			path, remedyLine(cmd, path, s, s.command))
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

// enterOptions carries what every door into a live sandbox needs. It exists so
// `den exec` and `den shell` cannot drift: #60's own comment (below) says two
// spellings of one door is the failure mode, and after 2026-08-14 there really
// ARE two commands — so the shared body is what keeps that from being true of
// the behaviour as well as the name.
// The terminal PROBE is not in here, and its absence is load-bearing: the tty
// is a PARAMETER of enterSandbox (see below), and a probe sitting beside it in
// the options would be a second source for one verdict. It was a field until
// 2026-08-14, written by both callers and read by nobody — dead state whose
// only possible future is the divergence this struct exists to prevent.
type enterOptions struct {
	denHome   *string
	runner    sbx.Runner
	sshAgent  func() sshagent.Result
	goos      string
	freshness agent.GateOptions
}

// enterSandbox is the body `den exec` and `den shell` share: find the sandbox,
// check it is attachable, hold the §9.1 gate, warn about an empty ssh agent,
// then hand `spawn.Enter` a command and a workdir.
//
// tty is a PARAMETER rather than computed here, and that is the one thing the
// two callers genuinely disagree about: `den exec` lets the probe and -T decide,
// because `sbx exec -t` with no terminal behind it silently DISCARDS the
// command's output while still returning its status (measured 2026-08-10 on
// v0.38.0, spec §14.0). `den shell` passes true unconditionally — a login shell
// without a terminal is worth nothing, which is why it refuses -T instead.
//
// command EMPTY means the login shell, and the default is NOT applied here: it
// lives in spawn.Command.Argv (internal/spawn/enter.go:85), one layer down,
// where `den spawn` reads it too.
func enterSandbox(cmd *cobra.Command, ref string, command []string, tty bool,
	workdir string, o enterOptions) error {

	// The SANDBOX name is the flattened reference: ":" is not in sbx's
	// `--name` charset, so a nest loaded from a source never spawns under its
	// prefixed name (spawn.go) — the live VM this command must find is already
	// "corp-api", not "corp:api".
	//
	// This is the ONLY thing this door needs from the reference pair: it reads
	// no nest file, so nestOfSandbox — the reverse decode `den ports` and
	// `den rm` share — has nothing to do here.
	name, err := sandboxNameOf(ref)
	if err != nil {
		return err
	}
	boxes, err := sbx.Ls(cmd.Context(), o.runner)
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
	// Same guard as `den spawn`, through the same helper: both paths end in an
	// `sbx exec`. A STOPPED VM passes, `sbx exec` restarts it.
	if err := b.CheckAttachable(); err != nil {
		return err
	}
	// den's OWN lines go to stderr as soon as a caller might be piping stdout:
	// `den exec -T api go build | tee log` must carry the command's output and
	// nothing else. On the interactive path they stay on stdout, where `den sh`
	// always put them — no pipe is listening there, and moving them would change
	// a surface #60 does not touch.
	chatter := cmd.ErrOrStderr()
	if tty {
		chatter = cmd.OutOrStdout()
	}
	stopped := b.IsStopped()
	if stopped {
		fmt.Fprintf(chatter,
			"sandbox %s is stopped: it restarts on attach (its state is kept)\n", b.Name)
	}
	// The §9.1 gate WAITS here even with no terminal, and that is the decision
	// #60 asked for. Under `--detach` den reads once because nobody is at a
	// prompt; `den exec <sb> <cmd>` fits neither case — nobody is at a prompt
	// AND the command being run is very often the agent itself. The gate's
	// reason ("you are about to run that agent") is more true here, not less.
	if err := spawn.CheckFreshnessOnReentry(
		cmd.Context(), o.runner, chatter, b.Name, stopped, o.freshness); err != nil {
		return err
	}
	// Before the attach, never after: once the shell has the terminal, a
	// warning printed behind it is a line the user scrolls past on the way out.
	warnEmptyAgentOnReentry(cmd, o.denHome, o.sshAgent, o.goos)
	// The workdir comes from the workspaces REPORTED BY THE VM, never from a
	// path recomputed from the config: without them the user lands in the VM's
	// home, not in their code. Which of them, and how --workdir and the cwd
	// rank, is spawn.StartDir's verdict — `den spawn` calls the same judge, so
	// the sibling commands cannot open a shell in two different places (#69).
	//
	// The cwd is read HERE, at the caller's edge, and handed in: the judge stays
	// pure, and an unreadable working directory is not an error.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "" // StartDir skips its cwd rule on "" and falls back.
	}
	return spawn.Enter(cmd.Context(), o.runner, b.Name, spawn.Command{
		Argv:    command,
		Workdir: spawn.StartDir(workdir, cwd, b.Workspaces),
		TTY:     tty,
	})
}

// newExecCmd runs one command in an already live sandbox.
//
// It opened a login shell too, when given no command, until 2026-08-14: that
// form is `den shell` now (shell.go), and a command is mandatory here — the
// contract `docker compose exec` has, which this command claimed to follow.
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
		Use:   "exec <name> <cmd> [args...]",
		Short: "Run a command in an existing sandbox",
		Args:  execArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// execArgs has refused every other shape, so args[1:] is a real
			// command. No ArgsLenAtDash: under SetInterspersed(false) it is
			// always -1 past the sandbox name, and a leading `--` — the one
			// shape where it is not — execArgs already refused.
			command := args[1:]
			// A nil probe is "no terminal", never "assume one" — the same rule
			// spawn.interactive applies to `-i`, and the reason the wiring
			// tests that leave IsTTY nil get the non-interactive path instead
			// of a verdict from whatever terminal the suite runs under.
			//
			// The probe DECIDES, and that is not a preference: `sbx exec -t`
			// with no terminal behind it silently DISCARDS the command's output
			// while still returning its status (measured 2026-08-10 on v0.38.0
			// — `echo hello` into a redirected stdout lands empty, spec §14.0).
			// Passing -it in CI would lose every byte the command wrote and
			// report success.
			//
			// The "-T asks for no terminal and no command asks for a shell"
			// refusal used to live here. It moved to `den shell` on 2026-08-14:
			// `den exec` now requires a command, so -T contradicts nothing on
			// this command.
			//
			// The spec 2026-08-10 pinned that message as identical byte for byte
			// between two commands, and the 2026-08-14 spec said the pair simply
			// became `den shell` ↔ `den spawn`. It did not: the two remedies
			// diverged with the contract — `den spawn` still points at a command
			// after `--`, `den shell` points at `den exec`, which refuses `--`.
			// newShellCmd's comment (shell.go) holds the argument.
			tty := !noTTY && isTTY != nil && isTTY()
			return enterSandbox(cmd, args[0], command, tty, workdir, enterOptions{
				denHome: denHome, runner: runner, sshAgent: sshAgent,
				goos: goos, freshness: freshness,
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
	// The mechanism that removes `--`, and the same one docker compose uses:
	// cobra stops parsing flags at the first positional, so everything after the
	// sandbox name is the command, verbatim, its own flags included. Without
	// this, `den exec api go test -v` fails on "unknown shorthand flag: 'v'".
	cmd.Flags().SetInterspersed(false)
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
