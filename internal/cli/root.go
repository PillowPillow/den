// Package cli wires den's cobra commands. No business logic here: everything
// worth testing lives in internal/config, internal/nest, internal/doctor.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/ports"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/PillowPillow/den/internal/sshagent"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
)

// Version is injected at build time (-ldflags "-X .../internal/cli.Version=...").
var Version = "dev"

// Deps holds every system access the cobra root hands down to its subcommands.
// Sbx is UNIQUE: `den ls` and the spawn path both read it from THIS field, so
// there is structurally no second runner to keep in sync.
type Deps struct {
	Doctor doctor.Deps
	Sbx    sbx.Runner
	Git    worktree.Git
	Policy policy.Options
	// Freshness is the patience of the §9.1 agent-freshness gate, injected for
	// the reason Policy is: its clock is real (time.Sleep, time.Now), so a test
	// tree that inherited it would stand and wait for a dispatcher no fake will
	// ever answer for.
	Freshness agent.GateOptions
	// Scanner tells `den ports` whether a host port is free. Injected like
	// every other system access, and for the sharpest reason in this struct:
	// the real one (ports.ListenScanner) BINDS host sockets across 9000-17990,
	// so a test tree that inherited it would open real ports on the machine
	// running the suite and take its verdict from whatever else listens there.
	Scanner ports.Scanner
	// SSHAgent probes the forwarded SSH agent for the spawn's empty-agent
	// warning. Injected here (not hard-wired in NewRootCmdWith) so the wiring
	// tests, which build Deps by hand, leave it nil and skip the real ssh-add —
	// keeping them owing nothing to the machine, exactly as they do for Git.
	SSHAgent func() sshagent.Result
	// Open hands the URL of an `open: true` port to the host's browser, for
	// `den ports`. Injected for the same reason as Scanner, one notch sharper:
	// the real one (ports.OpenURL) SPAWNS A PROCESS, so a suite that inherited
	// it would pop a browser window per test run. Every test injects a recording
	// double or leaves this nil — and nil is a no-op, so the wiring tests that
	// build Deps by hand keep owing nothing to the machine.
	Open func(url string) error
	// IsTTY reports whether den's input is a terminal, for the `-i` checklist.
	// Injected here for the same reason as SSHAgent: the wiring tests build
	// Deps by hand, leave it nil, and `-i` then takes its clean refusal instead
	// of depending on whether the suite happens to run under a terminal.
	IsTTY func() bool
}

// SystemDeps wires the real system accesses: sbx from PATH, real git, the
// default patience of policy's settle loop, and a real SSH-agent probe.
func SystemDeps() Deps {
	return Deps{
		Doctor:    doctor.SystemDeps(),
		Sbx:       sbx.NewExec(""),
		Git:       worktree.NewGit(),
		Policy:    policy.DefaultOptions(),
		Freshness: agent.DefaultGateOptions(),
		Scanner:   ports.ListenScanner{},
		Open:      ports.OpenURL,
		SSHAgent:  sshagent.System(),
		IsTTY:     spawn.StdinIsTerminal,
	}
}

// NewRootCmd builds a fresh command tree with the real system accesses.
// Returning a new instance per call, rather than a singleton, is what makes
// the commands testable.
func NewRootCmd() *cobra.Command {
	return NewRootCmdWith(SystemDeps())
}

// NewRootCmdWith takes its world accesses as a parameter, so tests can exercise
// `den ls` and `den <nest>` without sbx (or git) being installed: EVERY system
// access comes from the caller, none is hard-wired here.
//
// denHome is declared HERE, not at package level, so two command trees built in
// the same process can carry two different --den-home values. Subcommands get
// its address; the value is only filled in when flags are parsed.
func NewRootCmdWith(deps Deps) *cobra.Command {
	var denHome string

	root := &cobra.Command{
		Use:           "den",
		Short:         "Simple, repeatable sbx sandboxes",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&denHome, "den-home", "",
		"den config directory (default: $DEN_HOME or ~/.den)")

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print den's version",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "den %s\n", displayVersion())
			return nil
		},
	})
	root.AddCommand(newInitCmd(&denHome))
	root.AddCommand(newNestCmd(&denHome))
	root.AddCommand(newDoctorCmd(&denHome, deps.Doctor))
	root.AddCommand(newLsCmd(&denHome, deps.Sbx))
	// `den sh` gets the SSH probe and the OS too: re-entering a sandbox whose
	// forwarded agent has been emptied fails `git push` exactly as a fresh
	// `den <nest>` would, and this is the surface that re-enters most often.
	// runtime.GOOS is named here, at the wiring site, like the spawn's below.
	root.AddCommand(newShCmd(&denHome, deps.Sbx, deps.SSHAgent, runtime.GOOS, deps.Freshness))
	root.AddCommand(newRmCmd(&denHome, deps.Sbx, deps.Git))
	// `den ports` reads the SAME sbx as `den ls` and `den sh` (deps.Sbx is the
	// single runner) and gets its port scanner AND its browser opener from Deps,
	// where a test can replace the real ones — which bind host sockets and spawn
	// a browser — with doubles.
	root.AddCommand(newPortsCmd(&denHome, deps.Sbx, deps.Scanner, deps.Open))
	// `den build` reads the same single sbx: to learn which images already
	// exist (SbxImages) AND to run the create/exec/stop/save/rm sequence
	// itself (build.Execute) — there is no second runner to inject for it.
	root.AddCommand(newBuildCmd(&denHome, deps.Sbx))
	// `den source` reads the SAME injected Git as `den rm` (deps.Git) — the
	// whole tree tests against file:// remotes, never the real network.
	root.AddCommand(newSourceCmd(&denHome, deps.Git))

	// spawn.Deps is ASSEMBLED here from the very fields newLsCmd just got:
	// deps.Sbx is the single source. Out is left unset, configureSpawn
	// overwrites it on every run with cmd.OutOrStdout() (the only way to follow
	// a test's SetOut).
	//
	// LAST: configureSpawn sets Args on the root, which only makes sense once
	// every subcommand is registered.
	configureSpawn(root, &denHome, spawn.Deps{
		Sbx:       deps.Sbx,
		Git:       deps.Git,
		Policy:    deps.Policy,
		Freshness: deps.Freshness,
		SSHAgent:  deps.SSHAgent,
		IsTTY:     deps.IsTTY,
		// The real OS, named at the wiring site like every other system access:
		// spawn has no SystemDeps constructor to hold it (see spawn.Deps), and a
		// field left implicit here is a dependency the reader has to hunt for.
		GOOS: runtime.GOOS,
	})
	return root
}

// Execute is the entry point called by main.
//
// signal.NotifyContext, not a bare context.Background(): without it a Ctrl-C
// (or a `kill`) hits den's default disposition and the process dies on the
// spot — every `defer` in flight, including buildOne's `sbx rm --force`
// teardown (internal/build/execute.go), never runs, and a long `den build`
// leaves its throwaway VM behind on every interruption. Wiring the signal
// here, once, at the entry point, turns that into an ordinary cancellation
// that propagates through cmd.Context() to every command.
//
// Safe for every command, not just build, for two reasons already documented
// where they live: sbx.ExecError (internal/sbx/runner.go) carries the whole
// cancellation chain — Run reads ctx.Err() itself because a killed process's
// own error otherwise hides it — so any command that shells out through
// Runner.Run already "recognizes a Ctrl-C" instead of just dying with it. And
// Runner.Attach (the interactive `exec -it` path used by `den <nest>`, `den
// sh`, spawn) deliberately sets cmd.Cancel = nil, so this context ending
// does nothing to an attached shell — the tty driver delivers a Ctrl-C typed
// inside it directly to the sandbox's foreground process, not through here.
// Checked, not assumed: in both callers (internal/cli/sh.go, spawn.Spawn at
// internal/spawn/spawn.go) the Attach call is the LAST use of ctx — nothing
// runs after it that could see this context canceled by an in-shell Ctrl-C
// the tty already delivered straight to den's own process group.
//
// No re-arm, deliberately: the textbook pattern — a goroutine that calls
// stop() again once ctx.Done() fires, so a second signal escalates — is
// rejected here. Attach's cmd.Cancel = nil exists precisely so that ctx
// canceling does nothing to an attached shell; re-arming would undo that on
// the very path it protects, making a second in-shell Ctrl-C during `den sh`
// kill den outright while sbx still holds the tty. Bounding the teardown
// instead (context.WithTimeout over the context.WithoutCancel(ctx) buildOne
// already uses) was considered and rejected at buildOne's decision site, not
// here. Without this paragraph the next reader "fixes" the swallowed second
// Ctrl-C and breaks `den sh`.
//
// The wiring itself is an untestable one-liner, in the shape spawn.
// StdinIsTerminal and ports.ListenScanner already are: sending a real signal
// to the test binary is not something a hermetic suite does. No test exists
// for this line; do not add one that sends signals.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return NewRootCmd().ExecuteContext(ctx)
}

// liveNames names the live sandboxes, for the "not found" messages of `den rm`
// and `den sh`.
//
// Already sorted: sbx.Ls returns its sandboxes by name (locked by
// TestLsSortsByName), so re-sorting here would duplicate that knowledge without
// guaranteeing anything more.
func liveNames(boxes []sbx.Sandbox) []string {
	names := make([]string, 0, len(boxes))
	for _, b := range boxes {
		names = append(names, b.Name)
	}
	return names
}

// Argument validators. cobra's own cobra.NoArgs / cobra.ExactArgs render
// "accepts 1 arg(s), received 0", which never says WHICH argument is missing;
// these always recall the usage line, the useful half of the diagnosis.
var (
	noArgs        = argsBetween(0, 0)
	exactlyOneArg = argsBetween(1, 1)
	atMostOneArg  = argsBetween(0, 1)
)

// argsBetween returns a validator accepting between min and max arguments.
//
// The wording follows the DIRECTION of the violation, not just the bounds:
// "one argument expected" when one is missing, "exactly one argument expected"
// when there are too many. Combinations den does not use fall back on an
// explicit generic phrasing rather than on a mechanical "%d argument(s)".
func argsBetween(min, max int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		n := len(args)
		if n >= min && n <= max {
			return nil
		}
		var expected, detail string
		if n < min {
			expected = fmt.Sprintf("at least %d arguments expected", min)
			if min == 1 {
				expected = "one argument expected"
			}
			detail = "none received"
			if n > 0 {
				detail = fmt.Sprintf("%d received", n)
			}
		} else {
			switch {
			case max == 0:
				expected = "no argument expected"
			case max == 1 && min == 0:
				expected = "at most one argument expected"
			case max == 1:
				expected = "exactly one argument expected"
			default:
				expected = fmt.Sprintf("at most %d arguments expected", max)
			}
			// The first extra argument is quoted: it is what den failed to
			// understand, often a misplaced flag or a subcommand that does not
			// exist.
			detail = fmt.Sprintf("%d received, starting with %q", n, args[max])
			if n == 1 {
				detail = fmt.Sprintf("%q received", args[0])
			}
		}
		return fmt.Errorf("%s: %s, %s — usage: %s",
			cmd.CommandPath(), expected, detail, cmd.UseLine())
	}
}
