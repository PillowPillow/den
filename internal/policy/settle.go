// Package policy waits for a sandbox's network policy to actually be in
// place, before den attaches a shell to it.
package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PillowPillow/den/internal/sbx"
)

// Options parametrizes the loop. Sleep and Now are injected so tests don't
// actually wait.
//
// No field has an implicit default: incomplete Options are refused by
// Settle, not silently completed. See validate().
type Options struct {
	Timeout  time.Duration
	Interval time.Duration

	// Sleep must advance Now by roughly Interval: it's the PAIR, not either
	// field alone, that makes the loop progress. A no-op Sleep next to a real
	// clock is the classic trap — the loop never reaches its bound and exits
	// via the round-count guard instead.
	Sleep func(time.Duration)

	// Now is assumed to be monotonically non-decreasing. That's an
	// assumption, not a checked property: Settle doesn't defend against it. A
	// clock that goes backward or jumps between calls doesn't break the loop
	// (the round-count guard holds regardless), but it makes the diagnostic
	// it produces wrong — that diagnostic is built from the elapsed time
	// between the first and last call. time.Now and any increasing counter
	// work.
	Now func() time.Time
}

// DefaultOptions returns 60s of patience, polling every 2s. Observed
// propagation on spikes is measured in seconds, never minutes; 60s leaves a
// wide margin without turning a real blockage into an endless wait.
func DefaultOptions() Options {
	return Options{
		Timeout:  60 * time.Second,
		Interval: 2 * time.Second,
		Sleep:    time.Sleep,
		Now:      time.Now,
	}
}

// validate refuses incomplete or inconsistent Options instead of filling
// gaps.
//
// Filling gaps would be worse than refusing. A nil Sleep or Now panics, so
// it's visible; but a zero Timeout would leave the loop with no patience at
// all, and a zero Interval would make it hammer sbx nonstop. In both cases
// den's only network guard would keep saying yes without actually guarding
// anything — a silent "half-working" state, exactly what Settle exists to
// prevent. Silently filling in DefaultOptions() values would hide the
// caller's bug just as much.
//
// It also checks a RELATION, not just values: an Interval larger than the
// Timeout promises a second of patience and sleeps thirty. What it
// structurally CANNOT see is a clock that lies; that's what Settle's
// round-count guard is for.
func (o Options) validate() error {
	var invalid []string
	if o.Timeout <= 0 {
		invalid = append(invalid, fmt.Sprintf("Timeout (%s)", o.Timeout))
	}
	if o.Interval <= 0 {
		invalid = append(invalid, fmt.Sprintf("Interval (%s)", o.Interval))
	}
	if o.Sleep == nil {
		invalid = append(invalid, "Sleep (nil)")
	}
	if o.Now == nil {
		invalid = append(invalid, "Now (nil)")
	}
	if len(invalid) > 0 {
		return fmt.Errorf(
			"unusable settle options: %s — build them from policy.DefaultOptions() "+
				"and only override what needs to be",
			strings.Join(invalid, ", "))
	}
	if o.Interval > o.Timeout {
		return fmt.Errorf(
			"unusable settle options: Interval (%s) exceeds Timeout (%s) — "+
				"the loop would sleep longer than the patience it promises; "+
				"build them from policy.DefaultOptions()",
			o.Interval, o.Timeout)
	}
	return nil
}

// maxRounds bounds the number of loop rounds.
//
// This is an ARITHMETIC guard, not a temporal one, and that's the whole
// point: since the clock is injected, the loop's only temporal bound is the
// caller's good faith. A clock double that always returns the same time —
// perfectly accepted by validate(), which inspects values, not behavior —
// would make Settle loop forever, and a `go test ./...` would hang with
// nothing pointing at this package.
//
// The bound is set to EXACTLY what an honest clock produces:
// ceil(Timeout/Interval) sleeps, so one extra round. It therefore never
// triggers before the normal timeout; if it triggers, the clock is lying.
func (o Options) maxRounds() int {
	rounds := o.Timeout / o.Interval
	if o.Timeout%o.Interval != 0 {
		rounds++
	}
	return int(rounds) + 1
}

// Settle loops until ALL hosts are allowed in this sandbox's context, or
// until the timeout.
//
// Fail-closed (spec §7): if a host doesn't pass, den doesn't attach. A
// sandbox that starts half-way — an agent without access to
// api.anthropic.com, an install that fails partway through — costs more to
// diagnose than a clean refusal.
//
// The --sandbox scope is essential: the allowlist is set as
// caps.network.allow of a mixin scoped to this sandbox. Querying the GLOBAL
// policy would validate something else than what was just set — hence the
// name validation, without which an empty name would go straight into the
// argv unchanged.
func Settle(ctx context.Context, r sbx.Runner, sandbox string, hosts []string, o Options) error {
	// Before the empty-allowlist shortcut: broken Options stay broken on the
	// next call, with actual hosts this time. Better that the caller learns
	// on the first pass.
	if err := o.validate(); err != nil {
		return fmt.Errorf("sandbox %s: %w", sandbox, err)
	}
	// ValidateSandboxName is the single source of truth on a name; redefining
	// a "non-empty" check here would make a second copy, and in this repo two
	// copies of the same validation have already diverged.
	if err := sbx.ValidateSandboxName(sandbox); err != nil {
		return fmt.Errorf("waiting for network policy: %w", err)
	}
	remaining, err := cleanAllowlist(hosts)
	if err != nil {
		return fmt.Errorf("sandbox %s: allowlist: %w", sandbox, err)
	}
	if len(remaining) == 0 {
		return nil
	}

	start := o.Now()
	deadline := start.Add(o.Timeout)
	maxRounds := o.maxRounds()

	// hint: last invocation error observed ON A HOST STILL BLOCKED, reset on
	// every round. It can therefore never bring back into the message a host
	// that ended up passing.
	var hint error
	var hintHost string

	for round := 1; ; round++ {
		if err := ctx.Err(); err != nil {
			return canceled(sandbox, err)
		}
		if round > maxRounds {
			return inconsistentClock(sandbox, o, round-1, o.Now().Sub(start))
		}

		// Only hosts STILL blocked are re-probed: a long allowlist with a
		// single lagging host must not replay the whole list every round, and
		// a host already allowed has no business coming back in the message.
		var stillBlocked []string
		hint, hintHost = nil, ""
		for _, h := range remaining {
			ok, hnt, err := hostAllowed(ctx, r, sandbox, h)
			if err != nil {
				// A cancellation almost always arrives DURING a pass, not
				// between rounds. sbx then gets killed and cmd.Run returns
				// "signal: killed"; sbx.Exec.Run joins ctx.Err() itself, so
				// the reason IS detectable via errors.Is. The substitution
				// below stays, but for a reason that was never that one: a
				// Ctrl-C isn't the probed host's fault, and surfacing the
				// runner's error would embed its full argv in the message. So
				// the context's reason is returned, without the host.
				if errCtx := ctx.Err(); errCtx != nil {
					return canceled(sandbox, errCtx)
				}
				return err
			}
			if !ok {
				stillBlocked = append(stillBlocked, h)
				if hnt != nil {
					hint, hintHost = hnt, h
				}
			}
		}
		remaining = stillBlocked

		if len(remaining) == 0 {
			return nil
		}
		// The timeout message is built here, from only the remaining hosts: a
		// timeout must speak of EVERYTHING still blocked, not the last
		// failure encountered, and never hosts that already passed.
		if !o.Now().Before(deadline) {
			return stillBlockedTimeout(sandbox, o, remaining, hint, hintHost)
		}
		o.Sleep(o.Interval)
	}
}

// stillBlockedTimeout builds the fail-closed timeout error: the loop's
// normal exit when hosts remain blocked past the deadline.
func stillBlockedTimeout(sandbox string, o Options, remaining []string, hint error, hintHost string) error {
	slices.Sort(remaining) // deterministic message
	// When the verdict was kept DESPITE a failed invocation, this error is
	// the only thing den saw of the real cause: without it, a `sandbox "api"
	// not found` repeated thirty times ends up as "check your allowlist",
	// which is a false lead. It's joined as a HINT, never as the cause: the
	// cause stays the fail-closed timeout.
	detail := ""
	if hint != nil {
		detail = fmt.Sprintf(
			" Hint: `sbx` ALSO failed on the last check of %s (%v) — "+
				"the cause might be there, not in the allowlist.", hintHost, hint)
	}
	return fmt.Errorf(
		"sandbox %s: network policy still doesn't allow %d host(s) after %s — "+
			"den is not attaching (fail-closed). Blocked hosts: %s.%s "+
			"Check the nest's and stack's allowlist, then "+
			"`sbx policy check network --sandbox %s --verbose <host>`",
		sandbox, len(remaining), o.Timeout, strings.Join(remaining, ", "), detail, sandbox)
}

// inconsistentClock explains crossing the round-count bound.
//
// Two distinct causes lead here, and conflating them sends the fix to the
// wrong field: the clock can be FROZEN, or simply advancing by less than one
// Interval per round — typically a no-op Sleep next to a real clock. The
// message therefore reports the observed ADVANCE, not the current
// timestamp: saying "Now() always returns 00:01:00" of a clock that advanced
// by a minute is false, and contradicts itself further since the displayed
// time can be past the timeout deadline.
func inconsistentClock(sandbox string, o Options, rounds int, advanced time.Duration) error {
	header := fmt.Sprintf(
		"sandbox %s: waiting for the policy made %d rounds (Timeout %s, Interval %s) without "+
			"ever reaching its limit",
		sandbox, rounds, o.Timeout, o.Interval)
	// Now is assumed monotonic (see Options); if it isn't, "advanced by only
	// -1ms" makes no sense and "frozen" would be false. One branch to avoid
	// asserting something false, without otherwise guarding against an
	// adversarial clock.
	if advanced < 0 {
		return fmt.Errorf(
			"%s, and Now() went backward by %s — the clock supplied in policy.Options "+
				"isn't monotonic. That's a caller bug, not a network blockage",
			header, -advanced)
	}
	if advanced == 0 {
		return fmt.Errorf(
			"%s, without Now() advancing by a single nanosecond — the clock supplied "+
				"in policy.Options is frozen. That's a caller bug, not a "+
				"network blockage", header)
	}
	return fmt.Errorf(
		"%s: Now() only advanced by %s, where %d sleeps of %s promised %s — "+
			"the clock advances slower than Sleep claims (a no-op Sleep next to a "+
			"real clock produces exactly that). That's a caller bug, not a "+
			"network blockage",
		header, advanced, rounds, o.Interval, time.Duration(rounds)*o.Interval)
}

// canceled: the single cancellation message, wherever it's observed. It
// wraps the CONTEXT's reason, not the runner's error, so a caller can do
// errors.Is(err, context.Canceled). sbx.Exec.Run's error would now support
// that too, but the context's reason is kept anyway: it drags along NEITHER
// sbx's full argv NOR the name of a host that had nothing to do with it.
func canceled(sandbox string, err error) error {
	return fmt.Errorf("sandbox %s: waiting for network policy interrupted: %w", sandbox, err)
}

// cleanAllowlist validates and deduplicates the allowlist BEFORE the loop.
//
// An empty host (a `-` with no value in an allowlist YAML is enough to
// produce one) would go out as `--json ""` and den might conclude everything
// is fine. A duplicate (the same host in the nest and the stack) would be
// probed twice per round and listed twice in the failure message.
func cleanAllowlist(hosts []string) ([]string, error) {
	seen := make(map[string]bool, len(hosts))
	clean := make([]string, 0, len(hosts))
	for i, h := range hosts {
		if strings.TrimSpace(h) == "" {
			return nil, fmt.Errorf(
				"empty host at position %d of %d — a `-` with no value in an allowlist "+
					"YAML is enough to produce this", i+1, len(hosts))
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		clean = append(clean, h)
	}
	return clean, nil
}

// hostAllowed queries the policy for ONE host, in the sandbox's context.
//
// sbx's exit code is NOT the verdict. Nobody here has been able to confirm
// that `sbx policy check` exits 0 when a host is simply denied; if it exited
// 1, a naive read would fail den on the first round, blaming the first
// probed host, and the settle-loop would be pointless.
// The output is therefore read BEFORE drawing any conclusion from the error.
//
// The following asymmetry is deliberate: a "no" returned by a command that
// failed is trusted (we loop again — the safe behavior), a "yes" is not (den
// doesn't attach). Trusting a "yes" from a failed invocation — truncated
// stdout, unknown flag, missing sandbox — would be the only path by which
// this package could open a shell on a policy that was never actually
// checked.
//
// hint is non-nil only in the case where a verdict was kept DESPITE a failed
// invocation. It interrupts nothing, but the caller keeps it: if the loop
// ends in timeout, it's the only trace of what actually went wrong, and
// without it den sends the user to check an allowlist that has nothing to do
// with it.
func hostAllowed(ctx context.Context, r sbx.Runner, sandbox, host string) (allowed bool, hint, err error) {
	output, runErr := r.Run(ctx, "policy", "check", "network", "--sandbox", sandbox, "--json", host)
	verdict, readErr := readVerdict(output)

	if runErr != nil {
		if verdict != nil && !*verdict {
			return false, runErr, nil // explicit refusal: it's a verdict, loop again
		}
		return false, nil, fmt.Errorf("sandbox %s: checking %s: %w", sandbox, host, runErr)
	}
	if readErr != nil {
		return false, nil, fmt.Errorf("sandbox %s: checking %s: %w", sandbox, host, readErr)
	}
	return *verdict, nil, nil
}

// readVerdict extracts the `allowed` field from sbx's output.
//
// Allowed is a POINTER: a missing field must be distinguishable from a
// `false`. Conflating the two would run the loop to the timeout blaming the
// network, when the cause would be an sbx schema change.
//
// Reading uses a json.Decoder, not json.Unmarshal: Unmarshal refuses any
// content AFTER the value, so a banner, a log line, or NDJSON behind the
// verdict would permanently prevent den from attaching. The verdict is in
// the first value; what follows is ignored, deliberately. What PRECEDES it
// is still an error: we don't go looking for a verdict in the middle of a
// stream we don't understand.
//
// EXACT scope of the schema detection, measured empirically, because "the
// schema changed" is looser than it sounds: encoding/json field matching is
// CASE-INSENSITIVE. `{"ALLOWED": true}` therefore makes den attach, just
// like `{"Allowed": true}`. Refused, though, are fields only NEIGHBORING it
// — `allowedx`, `allow`, `"allowed "` with a trailing space. And when two
// keys match (`{"ALLOWED":false,"allowed":true}`), it's the LAST one in the
// document that wins, not the better-spelled one.
//
// Kept as is, rather than made strict via manual key reading:
//   - no safety consequence — a `true` under a different casing is still sbx
//     saying "allowed", from the same producer;
//   - the fail-closed property this code must hold is "ABSENCE of a verdict
//     ⇒ don't attach", and it holds across every neighboring form measured;
//   - conversely, strict casing would turn a mere recasing on sbx's side —
//     spec §14.1's axis A4, precisely the one nobody could verify — into an
//     outright refusal to attach.
//
// Three tests in settle_test.go hold these three properties; changing them
// changes this contract, not just clarifies it.
func readVerdict(output []byte) (*bool, error) {
	if len(bytes.TrimSpace(output)) == 0 {
		return nil, fmt.Errorf(
			"`sbx policy check network` wrote nothing to stdout (empty output) — " +
				"check that the --json flag exists and the verdict isn't going to stderr")
	}

	excerpt, suffix := outputExcerpt(output)

	var doc struct {
		Allowed *bool `json:"allowed"`
	}
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&doc); err != nil {
		// %q, not %s: output that isn't JSON may have no visible characters at
		// all, or be full of control characters. A message ending in ":" and
		// nothing leaves no lead.
		return nil, fmt.Errorf(
			"unreadable output from `sbx policy check network` (%w): %q%s", err, excerpt, suffix)
	}
	if doc.Allowed == nil {
		// The raw output is shown as-is — the brief requires it, and it's the
		// only way to see what sbx now renders. It's ANNOUNCED, because a
		// verbose sbx will happily cite hosts other than the one probed: the
		// property held here isn't "never names an already-passed host" —
		// showing the raw output makes that untenable — but "never ATTRIBUTES
		// anything to it". The caller has already prefixed "checking <host>",
		// and what follows "raw output:" belongs to sbx.
		return nil, fmt.Errorf(
			"the output of `sbx policy check network` carries no \"allowed\" field — "+
				"sbx's schema probably changed. Raw output: %s%s", excerpt, suffix)
	}
	return doc.Allowed, nil
}

// maxOutputSize bounds how much of sbx's output an error message repeats.
// Enough for a verdict JSON object, or the start of a usage message; not
// enough for a multi-kilobyte output to drown the diagnostic it's supposed
// to carry in the user's terminal.
const maxOutputSize = 512

// outputExcerpt returns the quotable portion of the output and, if
// applicable, the truncation note to append after it. The cut lands on a
// rune boundary: cutting mid-character would produce a U+FFFD replacement in
// the message, wrongly suggesting sbx emitted invalid bytes.
func outputExcerpt(output []byte) (excerpt, suffix string) {
	if len(output) <= maxOutputSize {
		return string(output), ""
	}
	cut := maxOutputSize
	for cut > 0 && !utf8.RuneStart(output[cut]) {
		cut--
	}
	return string(output[:cut]), fmt.Sprintf(" (truncated, %d bytes total)", len(output))
}
