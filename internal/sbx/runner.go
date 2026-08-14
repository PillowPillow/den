package sbx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Runner is den's ONLY point of contact with the sbx CLI. Everything goes
// through it, which makes the rest of den testable without a microVM — sbx
// isn't even installed on the development machine.
//
// Four methods, because the four uses are irreconcilable:
//   - Run captures stdout to parse it (`ls --json`, `policy check --json`).
//   - Stream relays stdout AND stderr to a writer as they are produced. It is
//     neither of the other two, and adding it was cheaper than bending either:
//     against Run, there is nothing to PARSE — a provisioning step's log is
//     addressed to the user, so capturing it would delay four minutes of
//     apt-get to the end AND drop stderr entirely on success (Run only ever
//     surfaces stderr inside ExecError, i.e. on failure); against Attach, no
//     tty is handed over — nothing is typed into a build step — so Attach's
//     deliberate "context cancellation does nothing" contract is exactly wrong
//     here, where a Ctrl-C must kill the step.
//   - Attach wires the current process's ttys to give the user an
//     interactive shell (`exec -it ... bash -l`); there's nothing to capture,
//     and capturing would break interactivity.
//   - Pipe wires the current process's three descriptors through UNMERGED and
//     stays killable: it is `den exec -T -- <cmd>`, the non-interactive door.
//     Against Attach, whose `cmd.Cancel = nil` is right for a shell, a Ctrl-C
//     on a three-minute `go test` must actually kill it. Against Stream, which
//     hands the child ONE descriptor on purpose, sbx keeps stdout and stderr
//     apart without -it (measured 2026-08-10, spec §14.0) and a caller piping
//     stdout must not receive stderr in it. Against Run, nothing is parsed.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
	Stream(ctx context.Context, out io.Writer, args ...string) error
	Attach(ctx context.Context, args ...string) error
	Pipe(ctx context.Context, args ...string) error
}

// Exec is the real implementation, backed by the sbx binary from the PATH.
//
// ENVIRONMENT: cmd.Env is left nil in Run, Stream and Attach alike, and that's
// a decision, not an oversight. nil ⇒ the process inherits den's environment,
// which is the ONLY support for `ssh.mode: agent-forward` — the config
// default (internal/config/config.go), which adds neither an argv argument to
// `sbx create` nor a mixin entry, and relies entirely on SSH_AUTH_SOCK
// reaching the sbx process.
//
// Setting cmd.Env here, for any reason, would cut off SSH access for EVERY
// sandbox. TestExec{Run,Stream,Attach}TransmitsDenEnvironment are what forbid
// it.
//
// What's proven stops at the process boundary: whether sbx then propagates
// this socket INTO the microVM isn't verifiable without sbx, and remains a
// spec assumption.
type Exec struct {
	Bin string
	// DrainDelay bounds how long pipes are given to finish draining, once the
	// process has exited OR the context has been canceled. Zero ⇒
	// defaultDrainDelay. Adjustable so bound tests take milliseconds rather
	// than seconds.
	DrainDelay time.Duration
}

// NewExec builds a real Runner. Empty bin ⇒ "sbx" (resolved via the PATH).
func NewExec(bin string) Runner {
	if bin == "" {
		bin = "sbx"
	}
	return &Exec{Bin: bin}
}

// ExecError is the error Run returns on failure. Exported (not a plain
// formatted message) because policy and spawn need to inspect the result, not
// just display it. The three properties below are each held by a test in
// runner_test.go:
//   - errors.As to retrieve the underlying *exec.ExitError and its exit code
//     (create failed, we need to say why);
//   - errors.Is(err, exec.ErrNotFound) to distinguish "sbx missing from the
//     PATH" from any application failure (doctor.go already makes this
//     distinction via LookPath; the runner must be able to make it too);
//   - errors.Is(err, context.Canceled) — and likewise DeadlineExceeded — to
//     recognize a Ctrl-C, whether the context was canceled BEFORE the process
//     started or DURING its execution.
//
// This third property does NOT come for free, and the Cancellation field is
// what carries it. When the context is canceled during execution, os/exec
// kills the process and `Cmd.Wait` PREFERS the process's error to the
// context's — cmd.Run returns an *exec.ExitError ("signal: killed") that
// wraps neither Canceled nor DeadlineExceeded. Only a cancellation that
// happened before the process started comes through as-is (Cmd.Start returns
// ctx.Err() without launching the process). Run therefore reads ctx.Err()
// itself and joins it to the chain.
//
// Hence Unwrap: cmd.Run()'s error chain must survive intact — the first two
// properties depend on it — alongside the cancellation reason and the
// message, which folds in stderr to stay readable by a human.
type ExecError struct {
	Bin    string
	Args   []string
	Stderr string
	Err    error
	// Cancellation carries the ctx.Err() observed when cmd.Run returns, nil if
	// the context had nothing to do with it. It's the ONLY source of
	// Canceled/DeadlineExceeded in the chain when the process was killed.
	Cancellation error
}

// missingBinaryMessage renders, with a remedy, the one execution failure the
// user can fix themselves: the binary isn't installed.
//
// It's the FIRST-CONTACT failure — sbx not yet set up on the machine — and it
// hits all four commands that touch sbx (`den ls`, `den spawn`, `den exec`,
// `den rm`), all of which go through Ls before anything else. Without this
// handling, the first line a new user sees is os/exec's:
// `exec: "sbx": executable file not found in $PATH` — with no remedy.
//
// `den doctor` is named because it diagnoses EXACTLY this case (doctor.go,
// step 1, via exec.LookPath) and will say in the same breath whether the rest
// of the install holds up. The argv is NOT rendered: when the binary is
// missing, no argument would have changed anything, and citing it would drown
// the one fact that matters.
func missingBinaryMessage(bin string) string {
	return fmt.Sprintf(
		"%q not found in the PATH — install it, then check your setup with `den doctor`", bin)
}

// Detail is the CAUSE alone — everything Error renders except the argv.
//
// It exists because the argv is not always worth what it costs. `sbx exec
// <name> -- bash -lc "<the whole provisioning script>"` renders an entire shell
// script before reaching `: E: Unable to locate package ripgrep`, which is the
// only line the user can act on. Spec §14.1 names that exact shape as a defect
// of the pre-#8 experience — "il incruste l'argv complet de `sbx create` avant
// d'en venir à la cause" — so a caller that has already named the operation in
// its own words needs the cause without the argv (internal/build/execute.go).
//
// SPLIT OUT of Error rather than reimplemented at the call site: the fallback
// chain (stderr, then Err), the cancellation prefix and the missing-binary
// remedy are one rendering decision, and two copies of it would drift. Error
// calls it, so they cannot.
func (e *ExecError) Detail() string {
	// BEFORE the cancellation reason: a canceled context on a missing binary
	// is still, for the user, a missing binary — the cancellation would be the
	// consequence, never the cause.
	if errors.Is(e.Err, exec.ErrNotFound) {
		return missingBinaryMessage(e.Bin)
	}
	detail := e.Stderr
	if detail == "" && e.Err != nil {
		detail = e.Err.Error()
	}
	if e.Cancellation != nil {
		// The cancellation reason comes BEFORE the detail, and doesn't replace
		// it: "signal: killed" alone reads like sbx crashing, but dropping it
		// would lose whatever stderr sbx had time to write.
		return fmt.Sprintf("%s: %s", cancellationReason(e.Cancellation), detail)
	}
	return detail
}

func (e *ExecError) Error() string {
	// The missing-binary case short-circuits HERE too, and not only in Detail:
	// prefixing "sbx exec … :" onto "\"sbx\" not found in the PATH" would name
	// the binary twice and put an argv that never ran in front of the one fact
	// that matters. Same reason as in Detail, one rendering layer up.
	if errors.Is(e.Err, exec.ErrNotFound) {
		return missingBinaryMessage(e.Bin)
	}
	return fmt.Sprintf("%s %s: %s", e.Bin, strings.Join(e.Args, " "), e.Detail())
}

// cancellationReason renders the stdlib's two cancellation reasons in
// English. The default returns the error as-is: a context can carry an
// application cause (context.WithCancelCause), and inventing one would lose
// it.
func cancellationReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "interrupted (deadline exceeded)"
	case errors.Is(err, context.Canceled):
		return "interrupted (canceled)"
	default:
		return fmt.Sprintf("interrupted (%v)", err)
	}
}

// Unwrap returns a SLICE: errors.Is must find the cancellation reason AND
// errors.As the *exec.ExitError, two distinct errors. The slice never holds
// nil, which errors' contract forbids.
func (e *ExecError) Unwrap() []error {
	var chain []error
	if e.Err != nil {
		chain = append(chain, e.Err)
	}
	if e.Cancellation != nil {
		chain = append(chain, e.Cancellation)
	}
	return chain
}

// defaultDrainDelay bounds how long pipes are given to finish draining.
//
// WaitDelay's timer starts when the context is canceled OR as soon as Wait
// observes the process exit, whichever comes first — it does NOT trigger
// "only after a cancellation". What it bounds is waiting on a descendant that
// inherited the pipes and keeps them open after the process exits, not the
// process's own runtime: as long as sbx itself hasn't exited, nothing bounds
// it (a slow `create` is not cut short). See Run for how ErrWaitDelay is
// handled.
const defaultDrainDelay = 2 * time.Second

// effectiveDelay applies the default. A separate function so that "Exec's
// zero value is the SAFE value" is verifiable without sleeping through the
// suite: the bound itself is proven by running a process, the default's
// choice is not.
func effectiveDelay(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultDrainDelay
	}
	return d
}

// drainCutShortOnSuccess reports whether the error cmd.Run returned is ONLY
// the drain being cut short on a process that otherwise succeeded.
//
// Separate function because two of its three conditions cover states we can't
// provoke on demand from an end-to-end test — os/exec only returns
// ErrWaitDelay on a success status, and a cancellation concurrent with a
// success is a race. Exercising them here on fabricated values is the only
// way to leave them with any proof.
//
// Each condition, and what it protects:
//   - the reason IS the drain: a nonzero exit, an exec.ErrNotFound or a copy
//     error remain sbx failures and must propagate;
//   - the context had nothing to do with it: a cancellation must never be
//     silently turned into a success;
//   - the process exited with a SUCCESS status: that's what attests sbx did
//     its job, the rest being pipe plumbing.
func drainCutShortOnSuccess(err, ctxErr error, state *os.ProcessState) bool {
	return errors.Is(err, exec.ErrWaitDelay) &&
		ctxErr == nil &&
		state != nil && state.Success()
}

// Run executes sbx and returns stdout. On failure, stderr is FOLDED into the
// message: sbx puts the useful diagnostic there, and a bare "exit status 1"
// is useless to the user and the maintainer alike. The original error (exit
// code, exec.ErrNotFound) and any cancellation reason stay reachable via
// errors.As/errors.Is thanks to ExecError.Unwrap — see its comment.
func (e *Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Zero ⇒ default, never "no bound": Exec's zero value (the one tests
	// construct, `&Exec{Bin: "sh"}`) must be the SAFE value. That's the whole
	// point of effectiveDelay: passed through as-is to WaitDelay, 0 means
	// "wait forever".
	cmd.WaitDelay = effectiveDelay(e.DrainDelay)
	if err := cmd.Run(); err != nil {
		// A drain cut short on a process that SUCCEEDED is not an sbx failure,
		// and reporting it as one cost a regression: `sbx create` leaving
		// behind a supervisor that inherits the pipe made ErrWaitDelay surface
		// INSTEAD of nil — den announced "couldn't create the sandbox" while it
		// EXISTS, then abandoned the sequence. See drainCutShortOnSuccess for
		// the three conditions that guard against silencing a real failure.
		//
		// The collected output is complete, for a precise reason: os/exec's
		// copier drains the pipe CONTINUOUSLY, so whatever the direct child
		// wrote before exiting is already captured. Verified at 240 KiB, well
		// beyond a pipe's buffer. Whatever a DESCENDANT would write after the
		// close is lost — that's the intended behavior, not sbx's output.
		if drainCutShortOnSuccess(err, ctx.Err(), cmd.ProcessState) {
			return stdout.Bytes(), nil
		}
		return stdout.Bytes(), &ExecError{
			Bin:    e.Bin,
			Args:   slices.Clone(args),
			Stderr: strings.TrimSpace(stderr.String()),
			Err:    err,
			// ctx.Err() is read HERE, when cmd.Run returns: it's the only place
			// where we know the failure and the cancellation are concurrent.
			Cancellation: ctx.Err(),
		}
	}
	return stdout.Bytes(), nil
}

// streamSink forwards to the caller's writer behind a POINTER, and that
// indirection is the entire mechanism of Stream's interleaving guarantee.
//
// os/exec hands the child ONE descriptor for both streams only when
// `interfaceEqual(cmd.Stderr, cmd.Stdout)` holds — a recover-guarded `==` on
// the two interface values. One descriptor ⇒ the kernel orders the two streams
// in the pipe and a SINGLE copier goroutine drains it, which is what makes
// "interleaved as produced" true and what makes concurrent writes to the
// caller's writer impossible.
//
// REJECTED: assigning `out` to both fields directly. It works for every writer
// den passes today (all pointers: *os.File, *strings.Builder, *bytes.Buffer),
// and degrades silently for a writer whose dynamic type is not comparable —
// interfaceEqual returns false, os/exec opens TWO pipes and runs TWO copiers
// writing to the same io.Writer, and the guarantee becomes a data race that no
// test in this repo would observe. A pointer wrapper makes the identity
// structural instead of dependent on what the caller happens to pass.
//
// No mutex, deliberately: one descriptor means one copier, so a second writer
// never exists — a mutex here would assert the opposite of the comment above.
//
// The price is fd passthrough: an *os.File out would otherwise be handed to
// the child directly, with no pipe and no copier. Paid knowingly — the copier
// drains continuously, so real-time relay holds either way — and it buys back
// the ErrWaitDelay-on-success case being genuinely reachable here, which is
// what makes the drain guard below load-bearing rather than decorative.
type streamSink struct{ w io.Writer }

func (s *streamSink) Write(p []byte) (int, error) { return s.w.Write(p) }

// Stream executes sbx and RELAYS its output to out — stdout and stderr both,
// interleaved as produced — instead of collecting it. It exists for
// internal/build's provisioning steps: text den does not parse, addressed to
// the user, arriving over minutes.
//
// On failure the ExecError's Stderr is left EMPTY, which is not an omission but
// the consequence of the relay: every byte sbx wrote has ALREADY reached out,
// and folding it into the message a second time would print the same apt-get
// failure twice. ExecError.Detail then falls back to Err, so a step failure
// renders as `... failed: exit status 1` — the shape spec §6 promises, with the
// diagnostic sitting in the log right above it.
func (e *Exec) Stream(ctx context.Context, out io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	// ONE sink for both, through one pointer — see streamSink.
	sink := &streamSink{w: out}
	cmd.Stdout = sink
	cmd.Stderr = sink
	// cmd.Cancel is left at CommandContext's default (SIGKILL), unlike Attach
	// forty lines below, which sets it to nil: there is no tty to leave in raw
	// mode here, and a Ctrl-C during a four-minute provisioning step must
	// actually stop it. Read Attach's comment before copying its `cmd.Cancel =
	// nil` up here.
	cmd.WaitDelay = effectiveDelay(e.DrainDelay)
	if err := cmd.Run(); err != nil {
		// Same guard, same three conditions, same reason as Run — none of them
		// depends on where the output went. What changes is only the sentence
		// about completeness: os/exec's copier drains the pipe CONTINUOUSLY, so
		// whatever the direct child wrote before exiting has already been
		// WRITTEN TO out (rather than collected into a buffer) by the time
		// WaitDelay closes the pipe. Whatever a DESCENDANT writes after that
		// close is lost, and that is intended — it isn't the step's output.
		if drainCutShortOnSuccess(err, ctx.Err(), cmd.ProcessState) {
			return nil
		}
		return &ExecError{
			Bin:  e.Bin,
			Args: slices.Clone(args),
			// Stderr stays EMPTY on purpose — see the godoc. Not "there was
			// none": there was, and the user already read it.
			Err: err,
			// Read HERE, as in Run: the only place where the failure and the
			// cancellation are known to be concurrent.
			Cancellation: ctx.Err(),
		}
	}
	return nil
}

// Attach hands control to sbx over the current process's ttys.
func (e *Exec) Attach(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	// Attach is the ONLY method in this file where context cancellation must
	// do NOTHING. CommandContext's default behavior is to SIGKILL the process
	// (cmd.Cancel = Process.Kill): on an interactive shell, that leaves the
	// terminal in raw mode, unflushed, without a clean `exit`. A Ctrl-C typed
	// INSIDE the sandbox's shell is delivered by the tty driver to the
	// foreground process group anyway, not relayed through this context — den
	// has nothing to do when this context ends here.
	//
	// cmd.Cancel = func() error { return nil } is NOT ENOUGH: in os/exec's
	// watchCtx, a Cancel that returns nil without being os.ErrProcessDone still
	// triggers `err = ctx.Err()` afterward, even if the process then exits
	// successfully (verified empirically, not just read in the docs).
	// Returning os.ErrProcessDone from Cancel would get the same effect as
	// below; cmd.Cancel = nil is used because it says so more directly, not
	// because it's the only form that works.
	cmd.Cancel = nil
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// *ExecError, not a fmt.Errorf: the rendered message is the SAME
		// (empty Stderr ⇒ ExecError.Error falls back to e.Err, so
		// "sbx exec -it ... : exit status 1", word for word what the previous
		// fmt.Errorf produced), but the missing-binary handling is shared with
		// Run instead of duplicated — and the error chain gains ExecError's
		// Unwrap.
		//
		// Cancellation stays NIL, deliberately: Attach is the one method where
		// context cancellation must do nothing (see cmd.Cancel above), and
		// setting it here would show "interrupted" on a shell den deliberately
		// let finish.
		return &ExecError{Bin: e.Bin, Args: slices.Clone(args), Err: err}
	}
	return nil
}

// Pipe runs sbx with den's own three descriptors passed through, unmerged, and
// with the context's cancellation left able to kill it.
//
// Every line below is the difference from a neighbour, so read them against
// Attach and Stream rather than on their own:
//
//   - cmd.Cancel is NOT set to nil. Attach nils it because SIGKILLing an
//     interactive shell leaves the terminal in raw mode; here there is no tty
//     to protect and the child is a build or a test the user must be able to
//     interrupt.
//   - the three descriptors are assigned SEPARATELY, never through one shared
//     sink. Stream's streamSink deliberately makes stdout and stderr one
//     descriptor to interleave them; that would put stderr into the pipe of
//     `den exec -T -- go build | tee log`.
//   - Cancellation IS filled, unlike Attach, for the same reason as Run and
//     Stream: a killed process's own error hides the context's, so ctx.Err()
//     is read here, where the two are known to be concurrent.
//
// Stderr stays EMPTY in the ExecError, as in Stream and for its reason: every
// byte sbx wrote has already reached the user's terminal, and folding it into
// the message would print it twice.
func (e *Exec) Pipe(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.WaitDelay = effectiveDelay(e.DrainDelay)
	if err := cmd.Run(); err != nil {
		// Same guard, same three conditions, same reason as Run and Stream.
		if drainCutShortOnSuccess(err, ctx.Err(), cmd.ProcessState) {
			return nil
		}
		return &ExecError{
			Bin:          e.Bin,
			Args:         slices.Clone(args),
			Err:          err,
			Cancellation: ctx.Err(),
		}
	}
	return nil
}

// SecretRunner is the secret-carrying half of den's contact with sbx. It is a
// SEPARATE interface from Runner on purpose: only the source-onboarding
// convergence needs it, and adding two methods to Runner would force every
// test double in den — policy's, spawn's, agent's — to implement I/O they
// never exercise.
//
// Both methods exist because a secret must never reach a place a human or a
// log can read it:
//
//   - RunInput feeds the value on STDIN. It is the right shape whenever sbx
//     offers a `--*-stdin` flag, and it is what den uses for a registry
//     password: an argv is visible to every process on the machine through
//     `ps`.
//   - RunSensitive is the fallback for the commands that have no stdin form.
//     sbx v0.38.0's `secret set-custom` requires `--value <secret>` (probed
//     2026-08-14), so the value HAS to travel in argv. What den controls is
//     everything after: the named argument indexes are redacted from the
//     error message, so a failing command cannot print the secret into a
//     terminal, a CI log or a bug report.
type SecretRunner interface {
	RunInput(ctx context.Context, input []byte, args ...string) ([]byte, error)
	RunSensitive(ctx context.Context, redactedIndexes []int, args ...string) ([]byte, error)
}

// RedactedArg is what a redacted argument renders as, everywhere. Same string
// as converge.Redacted, and deliberately duplicated rather than imported:
// internal/sbx is a leaf package, and a redaction that depended on an upper
// layer would be one an upper layer could remove.
const RedactedArg = "<redacted>"

// RunInput runs sbx with input on its standard input, capturing stdout.
//
// The input is NEVER recorded in the error: it is the secret itself.
func (e *Exec) RunInput(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = effectiveDelay(e.DrainDelay)
	if err := cmd.Run(); err != nil {
		if drainCutShortOnSuccess(err, ctx.Err(), cmd.ProcessState) {
			return stdout.Bytes(), nil
		}
		return stdout.Bytes(), &ExecError{
			Bin:          e.Bin,
			Args:         slices.Clone(args),
			Stderr:       strings.TrimSpace(stderr.String()),
			Err:          err,
			Cancellation: ctx.Err(),
		}
	}
	return stdout.Bytes(), nil
}

// RunSensitive runs sbx with a secret in argv, and keeps that secret out of
// every error it can produce.
//
// The redaction happens on the COPY stored in ExecError, not on the argv
// handed to the process: the command must still work. redactedIndexes are
// positions in args; an index out of range is ignored rather than refused —
// this must never be the reason a credential fails to apply.
func (e *Exec) RunSensitive(ctx context.Context, redactedIndexes []int, args ...string) ([]byte, error) {
	out, err := e.Run(ctx, args...)
	var execErr *ExecError
	if errors.As(err, &execErr) {
		execErr.Args = RedactArgs(execErr.Args, redactedIndexes)
	}
	return out, err
}

// RedactArgs returns a copy of args with the named positions replaced. It is
// exported so a test double redacts exactly as production does — the fake and
// the real runner rendering a secret differently is how a suite proves a
// redaction that does not exist.
func RedactArgs(args []string, redactedIndexes []int) []string {
	out := slices.Clone(args)
	for _, i := range redactedIndexes {
		if i >= 0 && i < len(out) {
			out[i] = RedactedArg
		}
	}
	return out
}
