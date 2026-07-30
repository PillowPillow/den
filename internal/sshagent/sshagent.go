// Package sshagent reports the state of the SSH agent reachable through
// SSH_AUTH_SOCK, so den can warn — at spawn and in `den doctor` — when
// `ssh.mode: agent-forward` would forward an agent that holds no key.
//
// The blind spot it closes: doctor already sees an ABSENT SSH_AUTH_SOCK, but
// not a socket that is present while the agent behind it is empty or dead. A
// sandbox then inherits a live-but-keyless agent and every `git push` dies on
// `Permission denied (publickey)`, with no ~/.ssh on disk to fall back to
// (that would be `ssh.mode: mount`).
package sshagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// State is what the agent behind SSH_AUTH_SOCK looks like right now. Three
// states, not two: "reachable with keys" and "unreachable" don't cover the
// case this package exists for — a reachable agent holding NO identity, which
// forwards silently and fails only inside the VM.
type State int

// The order is NOT cosmetic: StateUnreachable takes the iota zero so the zero
// value is the SAFE one — same rule as sbx.Exec's DrainDelay, where 0 must mean
// "the default bound", never "no bound". A Result nobody filled in (a caller
// that forgot a field, a probe returning early) then reads as "no usable
// agent", which merely warns. Had StateKeys kept the zero, that same empty
// Result would have certified a healthy agent and this check would fail OPEN —
// silent exactly when nothing answers.
const (
	// StateUnreachable: nothing to talk to — a dead socket, no agent running,
	// or `ssh-add` itself absent from PATH. Deliberately the zero value.
	StateUnreachable State = iota
	// StateEmpty: the agent answered but holds no identity. Forwards a live,
	// keyless agent — the exact silent failure this package warns about.
	StateEmpty
	// StateKeys: the agent answered and holds at least one identity. The only
	// healthy case; Result.Identities carries the count.
	StateKeys
)

// Result is a State plus, for StateKeys only, the number of identities the
// agent reported. Identities is 0 for the other two states.
//
// Its zero value is a StateUnreachable with no identity: the safe reading, see
// the const block above.
type Result struct {
	State      State
	Identities int
}

// Exec runs `ssh-add -l` (inheriting den's environment, so SSH_AUTH_SOCK is
// carried through) and returns its stdout and exit code. err is non-nil ONLY
// when the process could not be run to completion — `ssh-add` missing from
// PATH, most concretely; a plain non-zero exit is reported through code, not
// err, because it is exactly how OpenSSH tells the three states apart.
//
// Injectable for the same reason as doctor.Deps: the three states must be
// reproducible in a test without a real agent on the machine running it.
type Exec func() (stdout string, exitCode int, err error)

// Detect maps `ssh-add -l`'s outcome onto a State, using the exit codes
// OpenSSH standardizes identically across macOS, Linux and WSL:
//
//	0            → identities listed      (StateKeys, or StateEmpty if the
//	                                       listing is in fact empty)
//	1            → agent reachable, empty (StateEmpty)
//	2, or an     → no usable agent        (StateUnreachable)
//	exec failure
func Detect(run Exec) Result {
	stdout, code, err := run()
	if err != nil {
		// Couldn't even run ssh-add (not on PATH, say): there is no agent
		// answer to interpret, so it reads as unreachable.
		return Result{State: StateUnreachable}
	}
	switch code {
	case 0:
		// The COUNT decides, not the code alone: exit 0 on an empty listing
		// would otherwise announce a healthy agent holding zero identity — a
		// contradiction den would print as "ok (0 identities)" while every
		// `git push` in the sandbox dies on publickey. When the code and the
		// listing disagree, the listing is what the sandbox will actually get.
		n := countIdentities(stdout)
		if n == 0 {
			return Result{State: StateEmpty}
		}
		return Result{State: StateKeys, Identities: n}
	case 1:
		return Result{State: StateEmpty}
	default:
		// 2 is "could not connect"; anything else non-zero is no better an
		// agent. Both fold into unreachable rather than being invented into a
		// fourth state nobody could act on differently.
		return Result{State: StateUnreachable}
	}
}

// countIdentities counts the non-empty lines of `ssh-add -l`, which prints one
// per identity. Blank lines are skipped so a trailing newline never inflates
// the count by one.
func countIdentities(stdout string) int {
	n := 0
	for _, line := range strings.Split(stdout, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// probeTimeout bounds ONE `ssh-add -l`. The probe runs on the mainline
// `den <nest>` path and in `den doctor`, and it talks to a socket whose far
// end den does not own: a stalled agent proxy — a forwarded SSH_AUTH_SOCK
// whose other end went away, a 1Password/gpg-agent replacement wedged on a
// prompt — ACCEPTS the connection and then never answers. Unbounded, that
// suspends den forever on a check which is only advisory: the warning would
// cost more than the risk it reports.
//
// 2 s, and not a tenth of that: a local agent answers in milliseconds, but one
// backed by a hardware key or an unlock prompt can take a moment, and a probe
// that gives up too early prints "no usable agent" at someone whose agent is
// fine — the false alarm this package must not manufacture. The bound lives
// here rather than being threaded from the caller because doctor.Run takes no
// context (doctor.go) and neither does cli's call site: threading one would
// change that signature and every caller, for a probe whose only sane deadline
// is this one.
const probeTimeout = 2 * time.Second

// SystemExec is the real Exec: it runs `ssh-add -l` with the inherited
// environment (cmd.Env left nil), so SSH_AUTH_SOCK reaches the child exactly
// as the sbx process would inherit it, and bounded by probeTimeout.
//
// A one-line delegation on purpose: the timeout is a PARAMETER of systemExec
// so the bound can be proven by a test in 50 ms instead of 2 s (same split as
// sbx's effectiveDelay/defaultDrainDelay — the bound is proven by running a
// process, the value's choice by reading the constant).
func SystemExec() Exec {
	return systemExec(probeTimeout)
}

// systemExec is SystemExec with the deadline made injectable.
func systemExec(timeout time.Duration) Exec {
	return func() (string, int, error) {
		// The deadline starts when the PROBE RUNS, not when the Exec is built:
		// doctor assembles its Deps up front and calls them later, so a context
		// created out here would already be spent — or worse, expired, turning
		// every probe into a StateUnreachable.
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var out bytes.Buffer
		cmd := exec.CommandContext(ctx, "ssh-add", "-l")
		cmd.Stdout = &out
		// Killing ssh-add is not enough to come back: a descendant that
		// inherited stdout holds the pipe open and os/exec keeps draining it.
		// WaitDelay bounds that SECOND wait, exactly as sbx.Exec's DrainDelay
		// does — without it the deadline above buys nothing in that case.
		cmd.WaitDelay = timeout
		// stderr is discarded: on an empty or dead agent ssh-add writes its
		// human message there, but the exit code already carries the verdict.
		err := cmd.Run()
		if err == nil {
			return out.String(), 0, nil
		}
		// THE DEADLINE IS READ BEFORE THE EXIT CODE, and that order is the
		// contract. A ctx-killed process comes back from cmd.Run as an
		// *exec.ExitError ("signal: killed") whose ExitCode() is -1, and
		// os/exec prefers it over the context's own error. Handed to the
		// errors.As branch below, that -1 would be reported as an agent ANSWER
		// with err == nil — a fabricated exit code for a conversation that
		// never happened. It must surface as a run failure, which Detect reads
		// as StateUnreachable.
		//
		// A real answer racing the deadline (ssh-add exiting 1 in the same
		// instant) folds into unreachable too: both are a warning, and the
		// alternative — trusting a code produced while den was killing the
		// process — is the one that could fail silent.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", 0, fmt.Errorf("ssh-add -l did not answer within %s: %w", timeout, ctxErr)
		}
		// A non-zero exit is not a run failure: it is the signal. Report its
		// code with err=nil so Detect reads the state, and reserve the real
		// err (ssh-add missing from PATH) for the unreachable path.
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out.String(), exit.ExitCode(), nil
		}
		return "", 0, err
	}
}

// System is THE construction of the real probe — Detect reading a real
// `ssh-add -l` — and the only one den's wirings are meant to call:
// cli.SystemDeps fills Deps.SSHAgent with it for the spawn warning, and
// doctor.SystemDeps for `den doctor`.
//
// It exists because both used to spell `func() Result { return Detect(SystemExec()) }`
// out themselves, byte for byte. That gave the probe TWO construction sites to
// keep in sync, which none of den's other system accesses has (sbx.NewExec,
// worktree.NewGit, policy.DefaultOptions each own theirs): the day the real
// probe needs anything more than this composition, updating one site and not
// the other would leave `den doctor` and the spawn warning judging the SAME
// agent by two different rules — the contradiction being invisible precisely
// because both lines still compile.
//
// It returns a FUNC, not a Result, for the reason systemExec builds its context
// lazily: doctor and cli assemble their Deps up front and call them later, so a
// Result computed here would be a verdict about the agent as it was at startup,
// frozen — an agent unlocked a second afterwards would keep reading as dead.
func System() func() Result {
	return func() Result { return Detect(SystemExec()) }
}

// FixCommand returns the command the user should run, on this OS, to load
// identities into the running agent.
//
// Both branches rely on the same thing — a bare `ssh-add`, with no argument,
// loads the default ~/.ssh keys — and differ only by the flag macOS needs to
// reach the login keychain where it keeps passphrases. No path is passed: the
// positional argument of ssh-add is a private KEY FILE, so handing it `~/.ssh/`
// makes it read a directory as a key and exit 1 having loaded nothing.
//
// goos is a parameter, not a direct runtime.GOOS read, so each branch is
// assertable in a test regardless of where it runs.
//
// TWO LIMITS OF THIS COMMAND, both decided rather than overlooked, because a
// remedy den prints must not be read as a promise it does not make:
//
// macOS BEFORE 12 (Monterey) spells the flag `-K`; `--apple-use-keychain` is
// the name Apple introduced with 12 and there is deliberately NO version
// detection. den would have to shell out to sw_vers on the way to printing a
// HINT — a string the user runs, not one den executes — and on an older macOS
// the wrong spelling costs an "unknown option" plus ssh-add's own usage line,
// which names the flag that machine does want. Paying a probe on every warning
// to spare that, on releases Apple itself stopped updating, is the worse trade.
// Verified on macOS 26.5.2 / OpenSSH 10.2p1; older macOS was not measurable
// there, which is why this stays a decision and not a verification.
//
// KEYS THAT ARE NOT DEFAULT-NAMED are not loaded by either branch, and that
// limit does not stay in this comment: it travels to the user, as
// KeyNameCaveat, in every warning that quotes this command.
func FixCommand(goos string) string {
	if goos == "darwin" {
		return "ssh-add --apple-use-keychain"
	}
	return "ssh-add"
}

// KeyNameCaveat is the sentence that must travel with every FixCommand den
// prints, and it is what keeps the remedy from being a promise it cannot keep.
//
// Bare `ssh-add` — with or without the keychain flag — loads the DEFAULT names
// only: ~/.ssh/id_rsa, id_ecdsa, id_ed25519 and their siblings. On a host whose
// real keys are named anything else — a per-forge `id_ed25519_work`, the common
// shape as soon as someone has more than one remote — the command den suggested
// exits 0, `ssh-add -l` reports an identity, `den doctor` turns green, and
// `git push` from the sandbox is still denied on publickey. Measured on the
// verification machine, where the only default-named key was an `id_rsa` nothing
// used and every key carrying real traffic was named otherwise: there, this was
// not the tail case but the dominant one.
//
// den cannot name the right file for the user — it does not read
// ~/.ssh/config's IdentityFile entries, and on that same machine the key in the
// live agent had no `.pub` to be found by globbing either. So the message says
// what den does know: which names the command covers, and that anything else has
// to be passed explicitly.
//
// A CONSTANT, not a sentence retyped at each of the six warnings that need it
// (three in spawn, three in doctor): a caveat pasted six times is a caveat that
// ends up worded six ways, and the tests assert THIS symbol so a message that
// drops it fails rather than quietly reverting to the old promise. It carries no
// `%` verb, so callers concatenate it into a format string as-is.
//
// A STANDALONE SENTENCE, appended last, and that shape is the readable one:
// spliced mid-message it landed inside the existing parenthesis of the
// absent-socket warnings — a parenthetical holding its own parenthetical, ahead
// of the clause about `ssh.mode: mount` it had nothing to do with. Rendered by
// hand through the real binary, which is how that was seen.
const KeyNameCaveat = "note: only default-named keys (~/.ssh/id_*) are loaded, " +
	"so a key named otherwise has to be passed explicitly (`ssh-add ~/.ssh/<key>`)"
