// Package cli wires den's cobra commands. No business logic here: everything
// worth testing lives in internal/config, internal/nest, internal/doctor.
package cli

import (
	"fmt"
	"runtime"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/policy"
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
	// SSHAgent probes the forwarded SSH agent for the spawn's empty-agent
	// warning. Injected here (not hard-wired in NewRootCmdWith) so the wiring
	// tests, which build Deps by hand, leave it nil and skip the real ssh-add —
	// keeping them owing nothing to the machine, exactly as they do for Git.
	SSHAgent func() sshagent.Result
}

// SystemDeps wires the real system accesses: sbx from PATH, real git, the
// default patience of policy's settle loop, and a real SSH-agent probe.
func SystemDeps() Deps {
	return Deps{
		Doctor:   doctor.SystemDeps(),
		Sbx:      sbx.NewExec(""),
		Git:      worktree.NewGit(),
		Policy:   policy.DefaultOptions(),
		SSHAgent: sshagent.System(),
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
			fmt.Fprintf(cmd.OutOrStdout(), "den %s\n", Version)
			return nil
		},
	})
	root.AddCommand(newNestCmd(&denHome))
	root.AddCommand(newDoctorCmd(&denHome, deps.Doctor))
	root.AddCommand(newLsCmd(&denHome, deps.Sbx))
	root.AddCommand(newShCmd(deps.Sbx))
	root.AddCommand(newRmCmd(&denHome, deps.Sbx, deps.Git))

	// spawn.Deps is ASSEMBLED here from the very fields newLsCmd just got:
	// deps.Sbx is the single source. Out is left unset, configureSpawn
	// overwrites it on every run with cmd.OutOrStdout() (the only way to follow
	// a test's SetOut).
	//
	// LAST: configureSpawn sets Args on the root, which only makes sense once
	// every subcommand is registered.
	configureSpawn(root, &denHome, spawn.Deps{
		Sbx:      deps.Sbx,
		Git:      deps.Git,
		Policy:   deps.Policy,
		SSHAgent: deps.SSHAgent,
		// The real OS, named at the wiring site like every other system access:
		// spawn has no SystemDeps constructor to hold it (see spawn.Deps), and a
		// field left implicit here is a dependency the reader has to hunt for.
		GOOS: runtime.GOOS,
	})
	return root
}

// Execute is the entry point called by main.
func Execute() error {
	return NewRootCmd().Execute()
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
