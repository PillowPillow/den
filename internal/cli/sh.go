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

// newShCmd opens a shell in an already live sandbox.
//
// What `den sh` DECIDES comes from the SANDBOX and nothing else: `sbx ls --json`
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
// that is not. `den sh` only consults it on the branch that starts a stopped
// sandbox — the branch that waits.
func newShCmd(denHome *string, runner sbx.Runner, sshAgent func() sshagent.Result, goos string,
	freshness agent.GateOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "sh <name>",
		Short: "Open a shell in an existing sandbox",
		Args:  exactlyOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The SANDBOX name is the flattened reference: ":" is not in
			// sbx's `--name` charset, so a nest loaded from a source never
			// spawns under its prefixed name (spawn.go) — the live VM this
			// command must find is already "corp-api", not "corp:api". `den
			// sh` reads no nest file at all, so nothing else here needs
			// source.Locate.
			name := args[0]
			if src, _ := config.SplitSourceRef(name); src != "" {
				var err error
				if name, err = config.FlattenSandboxComponent("nest", name); err != nil {
					return err
				}
			}
			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			if b := sbx.Find(boxes, name); b != nil {
				// Same guard as `den <nest>`, through the same helper: both
				// paths end in an `sbx exec`. A STOPPED VM passes, `sbx exec`
				// restarts it.
				if err := b.CheckAttachable(); err != nil {
					return err
				}
				stopped := b.IsStopped()
				if stopped {
					fmt.Fprintf(cmd.OutOrStdout(),
						"sandbox %s is stopped: it restarts on attach (its state is kept)\n", b.Name)
				}
				// The §9.1 gate, on the door spawn does not own. `den <nest>`
				// refuses a sandbox whose agent den knows to be stale; this
				// command reached the same sandbox and said nothing (issue #27).
				// It runs AFTER the stopped-sandbox line above, which is what
				// tells the user the gate below will restart the VM, and BEFORE
				// the attach, because a refusal behind a shell that owns the
				// terminal is a refusal nobody reads.
				//
				// stopped is passed through, not re-derived: on a stopped
				// sandbox this command STARTS one, and §9.2 makes that branch
				// wait rather than read once. The verdict handling — refuse,
				// warn, note — belongs to spawn: two doors answering the same
				// journal differently is exactly the defect being closed.
				if err := spawn.CheckFreshnessOnReentry(
					cmd.Context(), runner, cmd.OutOrStdout(), b.Name, stopped, freshness); err != nil {
					return err
				}
				// Before the attach, never after: once the shell has the terminal,
				// a warning printed behind it is a line the user scrolls past on
				// the way out.
				warnEmptyAgentOnReentry(cmd, denHome, sshAgent, goos)
				// The workdir comes from the first workspace REPORTED BY THE VM,
				// never from a path recomputed from the config: without it the
				// user lands in the VM's home, not in their code.
				return spawn.Attach(cmd.Context(), runner, b.Name, b.Workdir())
			}

			names := liveNames(boxes)
			if len(names) == 0 {
				return fmt.Errorf("sandbox %q not found — no sandbox is running", name)
			}
			return fmt.Errorf("sandbox %q not found (live: %v)", name, names)
		},
	}
}

// warnEmptyAgentOnReentry warns, on the command's stderr, when the sandbox the
// user is about to re-enter would inherit an SSH agent holding no key.
//
// Why `den sh` warns at all: `spawn.WarnEmptySSHAgentOnReentry` holds that
// argument, and the divergence from `den <nest>`'s preflight (an absent socket
// says nothing here) with it. This function is the part that belongs to the
// CLI: finding ssh.mode without letting the search cost anything.
//
// Hence errors are swallowed, all of them, and nothing here can fail the
// command. `den sh`'s contract is that a broken den home never stands between
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
// can override it and `den sh`, which knows no nest, loses nothing by not
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
