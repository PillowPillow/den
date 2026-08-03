package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
)

// Deps is what Execute needs from the machine. Injected for the reason every
// field of cli.Deps is: the real Runner spawns processes, and DenHome is what
// `--den-home` redirects to keep the suite hermetic.
type Deps struct {
	Sbx     sbx.Runner
	DenHome string
}

// buildPlan is what the pre-flight loop below derives from a stack's config,
// BEFORE the first `sbx create`: the provisioning text, the create argv, and
// the two names the rest of the sequence is built on. All of it comes from pure
// functions over data den already has in hand — nothing here touches sbx — so
// computing it up front, and caching it here rather than recomputing inside
// buildOne, is what lets a chain reject everything config alone can reject
// before its first side effect.
//
// sandbox and scratch are FIELDS rather than two more calls to SandboxName and
// ScratchDir at the point of use. They were derived twice, independently, and
// the two derivations agreed only because they call the same pure function:
// nothing tied the directory buildOne creates to the one CreateArgv tells sbx
// to mount, and nothing tied the name the leftover check looks for to the name
// the teardown removes. Deriving once and carrying it makes the agreement
// structural instead of coincidental.
type buildPlan struct {
	sandbox      string
	scratch      string
	provisioning Provisioning
	createArgv   []string
}

// Execute runs the plan, in order.
//
// EVERYTHING rejectable from config alone runs BEFORE the first create — the
// ordering internal/spawn states at length: anything rejectable up front is
// rejected before the first side effect. That is not just ReadProvisioning:
// CreateArgv's own two guards (ValidateSandboxName; "no origin" for a stack
// with neither `base:` nor a resolved parent image) are just as cheap to run
// here and just as expensive to discover after several stacks have already
// built — so this loop runs CreateArgv too and keeps the resulting argv
// rather than throwing it away and reassembling it later. And whether a
// `<stack>-build` already exists is checked ONCE for the WHOLE chain, with a
// SINGLE `sbx ls --json`, rather than once per stack: a leftover for stack 3
// of 5 must be caught before stack 1 is even created, not discovered after
// stacks 1 and 2 have already spent minutes building — and one process beats
// N regardless.
//
// errOut receives everything that is a DIAGNOSTIC rather than build progress —
// today, only buildOne's teardown warning. A parameter, not a field on Deps:
// out is already a parameter rather than living on Deps (Deps is the MACHINE
// this needs — sbx, DenHome — while out/errOut are the two streams a caller
// picks per invocation, cmd.OutOrStdout()/cmd.ErrOrStderr() in
// internal/cli/build.go), so errOut sits next to the stream it is a sibling
// of instead of a field two lines above.
func Execute(ctx context.Context, steps []Step, d Deps, out, errOut io.Writer) error {
	plans := make(map[string]buildPlan, len(steps))
	for _, s := range steps {
		if !s.Build {
			continue
		}
		// Plan already turns a stack with no provision.steps into a skip (or,
		// for the stack the user named, into a refusal), so no plan Plan
		// produces reaches this. The check stays as a GUARD, in the shape
		// agent.RenderMixin's freshness guard takes: Step is a bare exported
		// struct, and a hand-built plan must not reach half the chain before
		// discovering the hole.
		if !s.Stack.Buildable() {
			return notBuildableError(s.Stack)
		}
		p, err := ReadProvisioning(s.Stack)
		if err != nil {
			return err
		}
		// s.Stack.ParentImage, not a re-derived value, and read UNCONDITIONALLY:
		// build.Chain already resolved it, empty for a root stack — which is
		// exactly what makes CreateArgv fall back to `base:` — and Execute was
		// handed a flat chain precisely so it would not have to re-walk the
		// graph to get it back.
		// Guarded HERE, in the pre-flight, and not where the directory is
		// actually emptied: buildOne's RemoveAll is the one destructive
		// operation in the command, and ScratchDir collapses under an empty
		// input — an empty stack name makes it the SHARED `cache/build` root,
		// whose removal would wipe every stack's scratch, and an empty DenHome
		// makes it the RELATIVE `cache/build/<stack>` under whatever directory
		// den happens to run from. Neither is reachable through the CLI, but
		// Deps and Step are exported bare structs anyone can fill, which is
		// exactly the doctrine sbx.CreateArgv states for its own inputs — and
		// "unreachable" is what CreateArgv's own comment said while a hole was
		// open. Rejectable from config alone ⇒ rejected before the first side
		// effect, which is this loop's whole reason to exist; leaving it in
		// buildOne would have let the chain's `sbx ls` run first.
		if d.DenHome == "" || s.Stack.Name == "" {
			return fmt.Errorf(
				"refusing to derive a build scratch from an empty den home or stack name "+
					"(den home %q, stack %q) — the path would be the shared build cache root, "+
					"or relative to the current directory", d.DenHome, s.Stack.Name)
		}
		scratch := ScratchDir(d.DenHome, s.Stack.Name)
		argv, err := CreateArgv(s.Stack, s.Stack.ParentImage, scratch)
		if err != nil {
			return err
		}
		plans[s.Stack.Name] = buildPlan{
			sandbox:      SandboxName(s.Stack.Name),
			scratch:      scratch,
			provisioning: p,
			createArgv:   argv,
		}
	}

	// ONE `sbx ls --json` for the whole chain — see the doc above — skipped
	// entirely when plans is empty: an all-skipped chain builds no `<stack>
	// -build` sandbox at all, so there is no name that could collide, and the
	// check below would loop over zero candidates. Not an optimization; the
	// call would be for nothing.
	if len(plans) > 0 {
		boxes, err := sbx.Ls(ctx, d.Sbx)
		if err != nil {
			// Named like every other refusal in this file: what den was about to
			// do, and where to go next. This one can name no single stack — it
			// is the ONE call made for the whole chain — so the remedy carries
			// the weight instead, the way Plan's inventory refusal names
			// `--force`. `den doctor` is the right one here and not `--force`:
			// an `sbx ls` that will not answer is a broken install, not an
			// arbitration den could be told to skip.
			return fmt.Errorf(
				"den could not list the existing sandboxes, so it cannot tell whether a leftover "+
					"`<stack>-build` from an interrupted build is in the way: %w — "+
					"check your sbx setup with `den doctor`", err)
		}
		for _, s := range steps {
			if !s.Build {
				continue
			}
			// A pre-existing build sandbox is a REFUSAL, not a `rm --force`
			// first. `<stack>-build` is a legal nest name (the component
			// charset allows it), so a blind cleanup can destroy a real
			// sandbox of the user's. The teardown in buildOne being deferred
			// AND run on context.WithoutCancel(ctx) (see buildOne), a Ctrl-C
			// or `kill` now tears down same as a clean failure — cli.Execute
			// wires signal.NotifyContext so this ctx observes the interrupt
			// in the first place. A leftover now survives only a SIGKILL
			// (which no `defer` outruns) or den itself crashing: rare enough
			// to deserve a human look.
			name := plans[s.Stack.Name].sandbox
			if sbx.Find(boxes, name) != nil {
				return fmt.Errorf(
					"stack %q: a sandbox named %s already exists — den will not remove it for you, "+
						"because that name is also a legal nest name. Inspect it, then `sbx rm --force %s`",
					s.Stack.Name, name, name)
			}
		}
	}

	for i, s := range steps {
		if !s.Build {
			// Announced, never silent: a `den build dgdevx` that printed one
			// line would leave "devx was already built" indistinguishable from
			// "den forgot devx". The reason carries its own remedy.
			fmt.Fprintf(out, "[%d/%d] %s: skipped, %s\n", i+1, len(steps), s.Stack.Name, s.Skipped)
			continue
		}
		fmt.Fprintf(out, "[%d/%d] %s: building %s...\n", i+1, len(steps), s.Stack.Name, s.Stack.Image)
		if err := buildOne(ctx, d, s.Stack, plans[s.Stack.Name], out, errOut); err != nil {
			return err
		}
	}
	return nil
}

// buildOne is one stack's whole sequence — everything rejectable from config
// alone has already run in Execute's pre-flight loop, so the only thing left
// here that can fail before the first side effect is the scratch directory
// itself, which IS that side effect. A function of its own so the teardown
// can be a `defer` — in a loop it would pile up until Execute returned, which
// is exactly the leak it exists to prevent.
func buildOne(ctx context.Context, d Deps, s *config.Stack, plan buildPlan, out, errOut io.Writer) error {
	name, scratch := plan.sandbox, plan.scratch

	// EMPTIED, not merely created. Spec §6 calls the scratch "un dossier **vide**
	// monté dans la VM de build", and after one build it is no longer: the VM has
	// had it mounted read-write and may have written into it, so the next build
	// would mount the residue of the last one — a build whose result depends on
	// what a previous build happened to leave behind, which is the one property
	// a reproducible image must not have. Safe to clear because it lives under
	// `cache/`, which spec §3 declares reconstructible and never a source of
	// truth; nothing else in den reads it.
	if err := os.RemoveAll(scratch); err != nil {
		return fmt.Errorf("stack %q: could not empty the build scratch %s: %w", s.Name, scratch, &config.FileError{Err: err})
	}
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return fmt.Errorf("stack %q: could not create the build scratch %s: %w", s.Name, scratch, &config.FileError{Err: err})
	}

	if _, err := d.Sbx.Run(ctx, plan.createArgv...); err != nil {
		// causeOf, not %w: the create argv carries the scratch path and, for a
		// derived stack, the parent's `--template`. den has just named the stack
		// and the operation in its own words; repeating the argv in front of the
		// cause is the §14.1 defect on a smaller scale. The chain survives —
		// see failureError.
		return failureCause(fmt.Sprintf("stack %q: could not create the build sandbox", s.Name), err)
	}
	// From here on the VM exists: it must not outlive this function, whatever
	// happens. This is the `trap` every build.sh had to write by hand, and that
	// no test could verify.
	defer func() {
		// context.WithoutCancel(ctx), not ctx: whatever killed the step that
		// made this defer necessary — a Ctrl-C the user typed, a SIGTERM,
		// cli.Execute's signal.NotifyContext turning either into a canceled
		// ctx — must not ALSO kill this cleanup. Passing ctx as-is would have
		// os/exec refuse to even start the process (sbx.Exec.Run: Cmd.Start
		// returns ctx.Err() without launching anything for an already-canceled
		// context), leaving exactly the leftover this defer exists to
		// prevent. Detaching from cancellation is NOT the same as bounding this
		// call: runner.go's defaultDrainDelay comment is explicit that nothing
		// times out a process still running, only the pipe drain after it
		// exits — a genuinely hung `sbx rm` still hangs den on exit. That
		// tradeoff is accepted here, not solved: a leftover VM the user can
		// inspect is a smaller failure than a `rm --force` killed mid-flight
		// against a VM already in whatever state it was left in.
		if _, err := d.Sbx.Run(context.WithoutCancel(ctx), "rm", "--force", name); err != nil {
			fmt.Fprintf(errOut, "warning: build sandbox %s could not be removed: %v\n", name, err)
		}
	}()

	for i := range plan.provisioning.Steps {
		// `-- bash -lc <payload>`, and every token earns its place:
		//
		//   - `--` ends sbx's own flag parsing before den's text begins. The
		//     three sibling `sbx exec` call sites omit it and are right to:
		//     agent.ReadFreshness sends `exec <n> cat <path>`, cli/ports sends
		//     `exec <n> true`, spawn.Attach sends `exec -it <n> bash -l` — every
		//     token after the sandbox name a fixed, dash-free literal. This one
		//     passes a whole USER-AUTHORED script as the last argv element.
		//     `sbx exec [flags] SANDBOX COMMAND [ARG...]` already stops reading
		//     flags at SANDBOX (spawn.Attach documents the consequence for `-w`),
		//     so `--` is belt-and-braces rather than strictly required; kept
		//     because it costs one token and the failure it forecloses — an sbx
		//     that keeps scanning, over text den does not control — would be
		//     silent.
		//   - `bash`, not `sh`: `includes` routinely carries `set -o pipefail`,
		//     a bashism, and spec §6 measured exactly that in sbx-devbox's own
		//     lib/common.sh.
		//   - `-l`, a LOGIN shell. Required for the base image's PATH (go, node),
		//     which arrives through /etc/profile.d and which a non-login shell
		//     never reads. Spec §6 states it as the counterpart of den, rather
		//     than the script's shebang, choosing the shell.
		//   - `-c <payload>`: the text travels INSIDE the argv. Nothing is
		//     written into the VM — see Provisioning.Payload, including the size
		//     ceiling that carries.
		//
		// ATTESTED against a real sbx on 2026-08-03: `sbx exec <name> -- bash
		// -lc '<payload>'` runs, `--` included. This repo attests sbx behaviour
		// with its date rather than extrapolating it.
		stdout, err := d.Sbx.Run(ctx, "exec", name, "--", "bash", "-lc", plan.provisioning.Payload(i))
		// Relayed on SUCCESS AND ON FAILURE, and before the error is returned so
		// the log reads above the cause rather than after it. A build that
		// swallowed its own output left the user with a stack name and an exit
		// code for four minutes of apt-get. Only the STEPS are relayed — they are
		// the part the stack authored; `create`, `stop`, `save` and `rm` are
		// den's own plumbing.
		//
		// After the step, not during: real-time streaming would need a new
		// sbx.Runner method (Run captures to parse), and adding one is out of
		// this change's scope. The whole step's log arrives at once.
		out.Write(stdout)
		if err != nil {
			return failureCause(
				fmt.Sprintf("stack %q: step %d/%d %s failed",
					s.Name, i+1, len(plan.provisioning.Steps), plan.provisioning.Steps[i].Path),
				err)
		}
	}

	if _, err := d.Sbx.Run(ctx, "stop", name); err != nil {
		return fmt.Errorf("stack %q: could not stop the build sandbox before saving: %w", s.Name, err)
	}
	// den passes the image name. THE point of the whole sequence: `image:` and
	// what is actually saved cannot disagree, so `den build` succeeding and
	// `den <nest>` demanding `den build` can no longer both be true.
	if _, err := d.Sbx.Run(ctx, "template", "save", name, s.Image); err != nil {
		return fmt.Errorf("stack %q: could not save image %s: %w", s.Name, s.Image, err)
	}
	return nil
}

// failureError is "what den was doing" followed by the CAUSE ALONE — the sbx
// argv deliberately left out.
//
// Spec §6 promises this shape, verbatim:
//
//	stack "devx": step 2/3 ./provision/gh.sh failed: exit status 1
//
// A plain `%w` around the *sbx.ExecError cannot produce it. ExecError.Error
// renders `Bin + strings.Join(Args, " ")` first, and Args now holds the entire
// includes+step text: a real failure printed the whole shell script, a lone
// `: `, and only then `E: Unable to locate package ripgrep` on the last line.
// Spec §14.1 names that exact shape as a defect of the pre-#8 experience — "il
// incruste l'argv complet de `sbx create` avant d'en venir à la cause" — and
// the payload made it worse than it had been.
//
// Err is kept and Unwrap exposes it, so nothing downstream loses what the argv
// was hiding: errors.As still reaches *sbx.ExecError and, through ITS Unwrap
// slice, the *exec.ExitError and its code; errors.Is still recognizes
// context.Canceled and exec.ErrNotFound. Only the RENDERING drops the argv.
type failureError struct {
	// What den was attempting, already naming the stack. Rendered as-is.
	Doing string
	// Cause is sbx.ExecError.Detail() when the failure came from a runner, and
	// the error's own text otherwise.
	Cause string
	Err   error
}

func (e *failureError) Error() string { return e.Doing + ": " + e.Cause }

func (e *failureError) Unwrap() error { return e.Err }

// failureCause wraps a runner failure, taking the cause from
// sbx.ExecError.Detail — which is the stderr sbx wrote, falling back to the
// error itself when stderr was empty, and carrying the cancellation reason and
// the missing-binary remedy that Detail owns.
//
// errors.As and not a type assertion: the runner is an interface, and a future
// implementation wrapping its own failure must still be understood. An error
// that is NOT an *sbx.ExecError renders as itself — a fake runner's plain
// errors.New("boom") in the tests, and any non-sbx failure in production.
func failureCause(doing string, err error) error {
	var execErr *sbx.ExecError
	if errors.As(err, &execErr) {
		return &failureError{Doing: doing, Cause: execErr.Detail(), Err: err}
	}
	return &failureError{Doing: doing, Cause: err.Error(), Err: err}
}
