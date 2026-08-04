// Package doctor diagnoses a den installation: consistent configuration,
// stacks and repos present, sbx available. No side effects, no network.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/sshagent"
)

// Level says how much a diagnostic weighs on `den doctor`'s exit code.
//
// Three states, not two: a healthy machine working locally without a remote
// repo must not fail, yet a config value the user should still be told about
// isn't a plain success either. A bool cannot hold that third case without
// lying on one side or the other.
type Level int

const (
	// LevelOK: nothing to report.
	LevelOK Level = iota
	// LevelWarning: worth reading, but den works and the exit code stays zero.
	LevelWarning
	// LevelFail: den will not work correctly as configured.
	LevelFail
)

// Check is the result of one diagnostic.
type Check struct {
	Name   string
	Level  Level
	Detail string
}

// Blocking reports whether this diagnostic should make `den doctor` exit
// non-zero. Only LevelFail is.
func (c Check) Blocking() bool { return c.Level == LevelFail }

// Deps injects system access, so tests run without sbx installed and without
// depending on the machine's real filesystem.
type Deps struct {
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
	// GitVersion returns the raw output of `git --version`.
	GitVersion func() (string, error)
	// Getenv reads den's environment, for the SSH_AUTH_SOCK check.
	Getenv func(string) string
	// SSHAgent reports the state of the SSH agent behind SSH_AUTH_SOCK. Injected
	// like the rest so the socket-present cases (empty, dead, has keys) are
	// reproducible without a real agent on the machine running the suite.
	SSHAgent func() sshagent.Result
	// GOOS names the operating system whose ssh-agent remedy the warnings should
	// quote; empty means runtime.GOOS. A parameter for the same reason
	// sshagent.FixCommand takes one: read directly, the darwin branch — the only
	// one that carries `--apple-use-keychain` — would be unassertable on the
	// Linux CI where this suite runs, so the message shipped to macOS users would
	// be the one no test ever exercises.
	GOOS string
}

// goos is Deps.GOOS with its documented default applied. Empty falls back to
// runtime.GOOS rather than to a hard-coded OS: a Deps built by hand must keep
// describing the machine it runs on, so only a test that OPTS IN gets another
// OS's remedy.
func (d Deps) goos() string {
	if d.GOOS == "" {
		return runtime.GOOS
	}
	return d.GOOS
}

// SystemDeps returns the real dependencies.
func SystemDeps() Deps {
	return Deps{
		LookPath:   exec.LookPath,
		Stat:       os.Stat,
		GitVersion: systemGitVersion,
		Getenv:     os.Getenv,
		SSHAgent:   sshagent.System(),
		// Named here rather than left to the fallback: SystemDeps is where every
		// real system access is spelled out, and a field silently defaulted is a
		// dependency the reader has to go looking for.
		GOOS: runtime.GOOS,
	}
}

// systemGitVersion runs `git --version`. The only doctor call that spawns a
// process: it reads no repository, writes nothing, and does not depend on cwd.
func systemGitVersion() (string, error) {
	out, err := exec.Command("git", "--version").Output()
	return string(out), err
}

// Minimum git version den requires. `git rev-parse --path-format=absolute` is
// the only den call that needs it (internal/worktree/worktree.go), and that
// option appeared in git 2.31; below it, git rejects the flag and the user
// sees git's own error at the first worktree instead of a den diagnostic.
const (
	minGitMajor = 2
	minGitMinor = 31
)

// parseGitVersion extracts the major.minor pair from `git --version` output.
// Distributions suffix freely ("2.39.5 (Apple Git-154)", "2.45.2.windows.1"),
// so only the first two numbers are read and the rest is ignored.
//
// An unreadable output is an error, not 0.0: Run fails the check either way,
// but naming the output actually received keeps the message honest instead of
// telling a user on a fine, differently-formatted git that "git 0.0" is too old.
func parseGitVersion(output string) (major, minor int, err error) {
	unreadable := func() (int, int, error) {
		return 0, 0, fmt.Errorf("unreadable `git --version` output: %q", strings.TrimSpace(output))
	}
	fields := strings.Fields(output)
	if len(fields) < 3 || fields[0] != "git" || fields[1] != "version" {
		return unreadable()
	}
	numbers := strings.Split(fields[2], ".")
	if len(numbers) < 2 {
		return unreadable()
	}
	if major, err = strconv.Atoi(numbers[0]); err != nil {
		return unreadable()
	}
	if minor, err = strconv.Atoi(numbers[1]); err != nil {
		return unreadable()
	}
	return major, minor, nil
}

// identities renders an identity count for display, singular for one so the
// line reads "1 identity" rather than the jarring "1 identities".
func identities(n int) string {
	if n == 1 {
		return "1 identity"
	}
	return fmt.Sprintf("%d identities", n)
}

// Run executes every diagnostic and returns the full list, failures included.
// It never stops at the first problem: the user must see everything at once.
func Run(denHome string, d Deps) []Check {
	var checks []Check
	add := func(name string, ok bool, format string, args ...any) {
		level := LevelFail
		if ok {
			level = LevelOK
		}
		checks = append(checks, Check{Name: name, Level: level, Detail: fmt.Sprintf(format, args...)})
	}
	// warn is kept separate from add: the existing diagnostics are genuinely
	// binary, and forcing an explicit level on all of them would only invite
	// mistakes.
	warn := func(name string, format string, args ...any) {
		checks = append(checks, Check{Name: name, Level: LevelWarning, Detail: fmt.Sprintf(format, args...)})
	}

	// 1. sbx present
	if path, err := d.LookPath("sbx"); err != nil {
		add("sbx", false, "sbx binary not found in PATH")
	} else {
		add("sbx", true, "%s", path)
	}

	// 2. git recent enough. Checked next to sbx: neither depends on
	// configuration, so an empty den home must not withhold either answer.
	if output, err := d.GitVersion(); err != nil {
		add("git", false, "could not determine git version: %v", err)
	} else if major, minor, err := parseGitVersion(output); err != nil {
		add("git", false, "%v", err)
	} else if major < minGitMajor || (major == minGitMajor && minor < minGitMinor) {
		// Name both the version read and the version required: the user
		// needs to know how far to upgrade, not just that they must.
		add("git", false,
			"%s is too old: den requires git %d.%d or later — `git rev-parse --path-format=absolute`, "+
				"which den uses to locate every worktree, does not exist before that",
			strings.TrimSpace(output), minGitMajor, minGitMinor)
	} else {
		add("git", true, "%s", strings.TrimSpace(output))
	}

	// 3. config.yaml loadable
	//
	// LoadGlobalUnvalidated, not LoadGlobal: the latter rejects an
	// inconsistent config, which would return right here and make stacks and
	// nests unreachable. doctor is the one place in the project where
	// "load" and "judge" must stay separate, because its job is to show
	// everything.
	g, err := config.LoadGlobalUnvalidated(denHome)
	if err != nil {
		add("config.yaml", false, "%v", err)
		return checks // without a config, nothing else is decidable
	}
	add("config.yaml", true, "%s", config.GlobalPath(denHome))

	// 4. internal config consistency
	configErrors := g.Validate()
	for _, e := range configErrors {
		add("config", false, "%v", e)
	}
	if len(configErrors) == 0 {
		add("config", true, "consistent")
	}

	// 5. stacks
	stacks, err := config.LoadStacks(denHome)
	if err != nil {
		// Structural failure only (unreadable stacks/ directory): a stack
		// that fails to decode lands in Broken below instead.
		add("stacks", false, "%v", err)
		stacks = config.Stacks{Healthy: map[string]*config.Stack{}}
	} else {
		// Same shape and verdict as the "nests" line right below it: two
		// neighboring totals that count differently read as a contradiction.
		add("stacks", len(stacks.Broken) == 0, "%d declared, %d unreadable",
			len(stacks.Healthy), len(stacks.Broken))
	}
	// Each broken stack is named individually, like broken nests further
	// down: a single bad stack must not sink the diagnosis of the others.
	for _, c := range stacks.Broken {
		add("stack "+c.Name, false, "unreadable: %v", c.Err)
	}
	if g.Defaults.Stack != "" {
		// Get, not a membership test: only Get distinguishes "unreadable"
		// from "not declared", the source of this verdict.
		if _, err := stacks.Get(g.Defaults.Stack); err != nil {
			add("defaults.stack", false, "%v", err)
		} else {
			add("defaults.stack", true, "%s", g.Defaults.Stack)
		}
	}

	// 6. stack kits: each path must exist before sbx receives it. A
	// missing kit doesn't fail at config load, it fails at microVM boot,
	// where the dispatcher does `exit $rc` — the user sees a dying VM, not a
	// den message. Names sorted for display, since a Go map has no order.
	// Names lists only healthy stacks: a broken stack has no kits to check
	// and was already named above.
	for _, stackName := range stacks.Names() {
		// DeclaredKits is the single source of "which kits, in which
		// order"; it already filters empty entries.
		for _, k := range stacks.Healthy[stackName].DeclaredKits() {
			if _, err := d.Stat(k); err != nil {
				add("stack "+stackName, false, "kit not found: %s", k)
			}
		}
	}

	// 7. ssh.dir, mount mode only: it's the only mode where it's mounted
	// as a workspace and ends up in `sbx create`'s argv. Validate() only
	// judges "declared or not"; "declared but missing on disk" needs a
	// filesystem probe, hence d.Stat.
	if g.SSH.Mode == "mount" && g.SSH.Dir != "" {
		if _, err := d.Stat(g.SSH.Dir); err != nil {
			add("ssh.dir", false,
				"%s not found — in \"mount\" mode this directory is mounted into the sandbox, "+
					"and a missing path would mount an empty directory instead of the keys", g.SSH.Dir)
		} else {
			add("ssh.dir", true, "%s", g.SSH.Dir)
		}
	}

	// 8. ssh.mode agent-forward — the config's DEFAULT
	// (config.LoadGlobalUnvalidated sets it when `ssh.mode` is absent).
	//
	// This mode adds no argument to `sbx create`'s argv and no mixin entry:
	// it relies entirely on the sbx process inheriting den's environment,
	// SSH_AUTH_SOCK included — proven, not assumed, by internal/sbx
	// TestExecRunTransmitsDenEnvironment — but there is nothing to inherit
	// if the variable is absent.
	//
	// WARNING, not failure, and that's the whole point of Level: working
	// locally without a remote repo is legitimate, and den has no way to
	// know whether the user needs SSH. Failing `den doctor` over this would
	// turn a healthy machine red.
	//
	// "absent OR EMPTY", not "absent": den reads the environment with
	// os.Getenv, which returns "" for both cases (os.LookupEnv would tell
	// them apart, den doesn't call it) — naming "absent" on a variable set
	// empty would describe a plausible cause instead of what was observed.
	if g.SSH.Mode == "agent-forward" {
		socket := d.Getenv("SSH_AUTH_SOCK")
		switch {
		case socket == "":
			// The load command comes from FixCommand, like the two branches below,
			// and is NOT spelled out here: hardcoded, this was the one warning of
			// the three that printed the bare `ssh-add` on darwin — the OS
			// inconsistency the rest of this check exists to remove, surviving in
			// the branch nobody had templated. `eval $(ssh-agent)` stays literal:
			// starting an agent is the same command everywhere, only loading keys
			// into it differs per OS.
			warn("ssh.mode",
				"agent-forward, but SSH_AUTH_SOCK is absent or empty in den's environment: "+
					"there is no SSH agent to forward, sandboxes will have no SSH access "+
					"and `git push` will fail from the VM, far from the cause — start an agent "+
					"(`eval $(ssh-agent)` then `%s`), or set `ssh.mode` to \"mount\" in %s — %s",
				sshagent.FixCommand(d.goos()), config.GlobalPath(denHome), sshagent.KeyNameCaveat)
		default:
			// Socket present: the old check stopped here and called it OK, blind
			// to a forwarded agent that is empty or dead. Interrogate it — the
			// socket alone proves a proxy exists, not that anything answers with
			// a key behind it.
			switch res := d.SSHAgent(); res.State {
			case sshagent.StateKeys:
				// The value AND the count are named: an "ok" that doesn't say
				// what it saw wouldn't let anyone spot a stale socket or an agent
				// that quietly lost its keys.
				add("ssh.mode", true, "agent-forward, SSH_AUTH_SOCK=%s (%s)",
					socket, identities(res.Identities))
			case sshagent.StateEmpty:
				warn("ssh.mode",
					"agent-forward, but the agent at SSH_AUTH_SOCK=%s holds no identity: sandboxes "+
						"inherit an empty agent and are denied SSH access (publickey), so `git push` "+
						"fails from the VM far from the cause — load a key with `%s` — %s",
					socket, sshagent.FixCommand(d.goos()), sshagent.KeyNameCaveat)
			case sshagent.StateUnreachable:
				warn("ssh.mode",
					"agent-forward, but SSH_AUTH_SOCK=%s points at an unreachable agent (dead socket, "+
						"no agent running, or ssh-add absent from PATH): sandboxes will have no SSH "+
						"access and `git push` fails from the VM — start an agent and load a key with "+
						"`%s`, or set `ssh.mode` to \"mount\" in %s — %s",
					socket, sshagent.FixCommand(d.goos()), config.GlobalPath(denHome),
					sshagent.KeyNameCaveat)
			default:
				// Without this arm, a State this switch doesn't model emitted NO
				// ssh.mode line at all: `den doctor` stayed silent about the agent,
				// which reads as "nothing to report" — the check disappearing is
				// worse than any verdict it could give. Warn instead, naming the
				// value seen, so a state added to sshagent surfaces here rather
				// than deleting the diagnostic.
				warn("ssh.mode",
					"agent-forward, SSH_AUTH_SOCK=%s, but the agent probe returned the unrecognized "+
						"state %d: den cannot tell whether a key will reach the sandbox — check the "+
						"agent by hand with `ssh-add -l`",
					socket, int(res.State))
			}
		}
	}

	// (a step re-judging agents.*.update was removed here: config.Global.Validate()
	// already covers it via TrimSpace, the stricter test — see validate.go.)

	// 9. nests: referenced stack exists, repos present on disk
	nests, brokenNests, err := nest.ListNests(denHome)
	if err != nil {
		add("nests", false, "%v", err)
		return checks
	}
	// A broken nest is named individually and doesn't stop the others from
	// being diagnosed: that's precisely doctor's job.
	for _, c := range brokenNests {
		add("nest "+c.Name, false, "unreadable: %v", c.Err)
	}
	for _, n := range nests {
		stackName := n.Stack
		if stackName == "" {
			stackName = g.Defaults.Stack
		}
		// Get: the nest must learn whether ITS stack is unreadable or
		// missing — two different fixes. A membership test would say
		// "not found" for both, which is what made doctor lie whenever
		// some other stack broke the whole load.
		if _, err := stacks.Get(stackName); err != nil {
			add("nest "+n.Name, false, "%v", err)
		}
		for _, r := range n.Repos {
			if r.Key == "" {
				// path: repo — r.Path is already the concrete machine path
				// nest.LoadNest expanded.
				if _, err := d.Stat(r.Path); err != nil {
					add("nest "+n.Name, false, "repo not found: %s", r.Path)
				}
				continue
			}
			// key: repo — nest.LoadNest leaves r.Path empty on purpose;
			// nest.Resolve is what fills it, by looking r.Key up in
			// g.Repos. doctor does not call Resolve (it stays a
			// non-resolving diagnostic, matching the rest of this
			// function), so it must do that same lookup itself here —
			// otherwise d.Stat("") "fails" against a blank path and the
			// report names nothing, the opposite of den's doctrine that a
			// refusal always names the thing to fix.
			path, ok := g.Repos[r.Key]
			if !ok {
				// Same wording family as resolveRepoKeys's own
				// unmapped-key refusal (internal/nest/resolve.go): the
				// remedy the user is sent to is the same file either way,
				// and a diverging message here would just be a second
				// dialect for the same fix.
				hint := ""
				if r.URL != "" {
					hint = fmt.Sprintf(" (clone: %s)", r.URL)
				}
				add("nest "+n.Name, false,
					"repo key %q is not mapped on this machine — add `%s: <local path>` under "+
						"`repos:` in %s%s", r.Key, r.Key, config.GlobalPath(denHome), hint)
				continue
			}
			if _, err := d.Stat(path); err != nil {
				// Same "repo not found: <path>" prefix the path: branch
				// above prints, with the key appended: both are named, so
				// the user knows which `repos:` entry in config.yaml
				// points at the wrong place, not just that some path is
				// missing.
				add("nest "+n.Name, false, "repo not found: %s (key %q)", path, r.Key)
			}
		}
	}
	if len(nests) > 0 || len(brokenNests) > 0 {
		add("nests", len(brokenNests) == 0, "%d declared, %d unreadable", len(nests), len(brokenNests))
	}

	return checks
}
