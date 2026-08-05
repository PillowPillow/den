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
	"sync"
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

		// The one-call path FIRST, because the per-host pass below is priced in
		// PROCESSES, not in policy work (see maxProbeConcurrency).
		switch verdict, missing := fastSettle(ctx, r, sandbox, remaining); verdict {
		case fastSettled:
			return nil
		case fastPending:
			// The listing was readable and den's rule simply is not fully there
			// yet. That is what the loop EXISTS for, so it sleeps — it does not
			// fall back on the authoritative pass. Collapsing the two would make
			// a round-1 miss cost one listing PLUS the whole per-host sweep,
			// i.e. slower than before precisely when the loop is doing its job.
			if !o.Now().Before(deadline) {
				return stillBlockedTimeout(sandbox, o, missing, nil, "")
			}
			o.Sleep(o.Interval)
			continue
		case fastUnusable:
			// An unreadable listing, an sbx schema den does not recognize, or a
			// `deny` rule whose precedence den deliberately does not model: the
			// authorizer gets the last word, host by host, exactly as before.
		}

		// Only hosts STILL blocked are re-probed: a long allowlist with a
		// single lagging host must not replay the whole list every round, and
		// a host already allowed has no business coming back in the message.
		results := probeAll(ctx, r, sandbox, remaining)

		var stillBlocked []string
		hint, hintHost = nil, ""
		// The results are consumed in ALLOWLIST ORDER, never in completion
		// order: the first error returned, the hint kept and the list of
		// blocked hosts must not depend on which goroutine finished first. This
		// walk is what makes the concurrent pass indistinguishable from the
		// sequential one it replaced, message for message.
		for i, h := range remaining {
			res := results[i]
			if res.err != nil {
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
				return res.err
			}
			if !res.allowed {
				stillBlocked = append(stillBlocked, h)
				if res.hint != nil {
					hint, hintHost = res.hint, h
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

// maxProbeConcurrency bounds how many `sbx policy check` run at once in the
// authoritative pass.
//
// The value is small because the measurement says the ceiling is low, and
// saying so here is the point. Benched on 2026-08-05 (10 cores, sbx v0.35.0):
// `sbx --version`, which makes NO daemon call at all, costs 390 ms per exec,
// against 486 ms for a full `policy check` — the price is the process, not the
// policy work. And that price barely parallelizes: twenty concurrent execs took
// 6,4 s against 7,8 s sequentially, where twenty `/bin/echo` take 13 ms. On the
// real 24-host allowlist the whole sweep went from 11,4 s sequential to 9,1 s at
// eight in flight and 7,9 s with all twenty-four — a 30 % ceiling, not a factor
// of N.
//
// Eight is therefore chosen as the point where the curve has already flattened,
// not as a throughput bet: past it another 1,2 s buys twenty-four concurrent
// sbx processes on a machine that is also booting a VM. The real win is not
// here at all, it is in fastSettle spending ONE process instead of twenty-six —
// this pass is the cold path.
const maxProbeConcurrency = 8

// probeResult is one host's outcome, kept in allowlist order (see probeAll).
type probeResult struct {
	allowed bool
	hint    error
	err     error
}

// probeAll runs the authoritative per-host check concurrently and returns the
// results INDEXED BY hosts, so the caller can consume them in allowlist order.
//
// Returning an indexed slice rather than, say, a channel of outcomes is the
// whole safety property: every message this package produces — the first error,
// the hint's host, the sorted list of blocked hosts — was defined by the
// sequential walk's order, and a result set that carries its index lets that
// walk stay exactly what it was.
//
// The bound is a semaphore rather than a worker pool: the list is short (an
// allowlist, not a work queue) and the goroutines do nothing but wait on a
// process.
func probeAll(ctx context.Context, r sbx.Runner, sandbox string, hosts []string) []probeResult {
	results := make([]probeResult, len(hosts))
	sem := make(chan struct{}, maxProbeConcurrency)
	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			allowed, hint, err := hostAllowed(ctx, r, sandbox, h)
			results[i] = probeResult{allowed: allowed, hint: hint, err: err}
		}()
	}
	wg.Wait()
	return results
}

// fastVerdict is what one `sbx policy ls` round could conclude.
type fastVerdict int

const (
	// fastUnusable: the listing answered nothing den is willing to act on. The
	// authoritative per-host pass decides. Never a reason to attach.
	fastUnusable fastVerdict = iota
	// fastPending: the listing is readable and den's hosts are not all in it
	// yet. Sleep and look again — this is ordinary propagation.
	fastPending
	// fastSettled: every host is covered AND the authorizer confirmed the rule
	// is live. The only verdict that attaches.
	fastSettled
)

// fastSettle answers the round in ONE listing plus ONE authoritative check,
// instead of one `sbx policy check` per host.
//
// WHY IT EXISTS. den's allowlist reached 26 hosts on a real stack, and the
// sequential sweep cost ~12 s of a ~36 s spawn — for ~100 ms of actual policy
// work, the rest being 26 × the cost of starting the `sbx` binary
// (maxProbeConcurrency has the numbers).
//
// WHY IT IS NOT JUST THE LISTING. Measured on 2026-08-05, with two independent
// pollers so neither sampling order biases the other: on four consecutive
// spawns `policy ls` showed the rule 83–172 ms BEFORE `policy check` allowed the
// host it uniquely covers — the same sign every time. Small, but the wrong
// direction: a listing-only verdict would attach into a policy the authorizer
// has not applied yet, which is the half-start §7 forbids, just narrower. So the
// listing is used for what only it can say cheaply — WHAT the rule contains —
// and the authorizer is still asked whether that rule is LIVE.
//
// WHY ONE CHECK IS ENOUGH. den's whole allowlist lands as a SINGLE scoped rule
// (`caps.network.allow` of the mixin, one rule id, verified against sbx v0.35.0),
// so its propagation is atomic: a host that only that rule covers is allowed if
// and only if the rule is live. Hence the witness below is chosen among hosts no
// OTHER active rule mentions verbatim. When no such host exists, every host is
// already covered elsewhere and the scoped rule's liveness cannot change any
// verdict — hosts[0] is then a witness like any other.
//
// WHY VERBATIM MATCHING IS SOUND. den does not model sbx's glob semantics
// (`**.foo.com:443`) and must not: re-implementing an authorizer is how the two
// diverge. It does not need to, either — it looks up the exact strings it wrote
// into the mixin itself, so this is set membership, not pattern matching. The
// asymmetry is deliberate and always in the safe direction: a host covered ONLY
// by someone else's glob is simply not recognized, and falls back to the
// authoritative pass.
//
// A sandbox-scoped rule does not survive `den rm` (verified: zero scoped rules
// remain afterwards, and the id rotates on every creation), so a stale rule from
// a previous sandbox of the same name cannot answer for a fresh one.
func fastSettle(ctx context.Context, r sbx.Runner, sandbox string, hosts []string) (fastVerdict, []string) {
	output, err := r.Run(ctx, "policy", "ls", sandbox, "--json")
	if err != nil {
		return fastUnusable, nil
	}
	rules, ok := readRules(output)
	if !ok {
		return fastUnusable, nil
	}

	// Two sets, because WHICH rule carries a host is what makes it a witness.
	fromScoped, fromOther := map[string]bool{}, map[string]bool{}
	for _, rule := range rules {
		if rule.ResourceType != "network" || rule.Status != "active" {
			continue
		}
		if rule.Decision != "allow" {
			// A `deny` — or any decision den has never seen — brings a
			// precedence question this function deliberately does not answer.
			// The authorizer already knows it: hand the whole round over.
			return fastUnusable, nil
		}
		target := fromOther
		if strings.HasPrefix(rule.Scope, "sandbox") {
			target = fromScoped
		}
		for _, res := range rule.Resources {
			target[res] = true
		}
	}

	var missing []string
	for _, h := range hosts {
		if !fromScoped[h] && !fromOther[h] {
			missing = append(missing, h)
		}
	}
	if len(missing) > 0 {
		return fastPending, missing
	}

	witness := hosts[0]
	for _, h := range hosts {
		if fromScoped[h] && !fromOther[h] {
			witness = h
			break
		}
	}
	allowed, hint, err := hostAllowed(ctx, r, sandbox, witness)
	if err != nil || hint != nil {
		// hint != nil means the check itself FAILED while still returning an
		// exploitable refusal — and that is precisely the case stillBlockedTimeout's
		// hint was built for. Keeping the refusal here and dropping the hint would
		// loop to the timeout on "check the nest's and stack's allowlist" while sbx
		// was broken every round: the exact false lead hostAllowed's comment names.
		// Rather than carry a second hint channel through the fast path, the round
		// is handed to the authoritative pass, which already produces that
		// diagnostic — and it is the honest move anyway, since a witness whose
		// verdict cost an error proves nothing about the rule being live.
		return fastUnusable, nil
	}
	if !allowed {
		// The rule is listed but not yet applied — the 83–172 ms window above.
		// Reported as the one host den can honestly name: the witness really is
		// blocked, and it is the one whose blockage proves the rule is not live.
		return fastPending, []string{witness}
	}
	return fastSettled, nil
}

// policyRule is the subset of `sbx policy ls --json`'s rule shape den reads.
// Everything else in that document is ignored on purpose: the fewer fields den
// binds, the fewer ways an sbx release can turn the fast path into a refusal.
type policyRule struct {
	Scope        string   `json:"scope"`
	ResourceType string   `json:"resource_type"`
	Decision     string   `json:"decision"`
	Status       string   `json:"status"`
	Resources    []string `json:"resources"`
}

// readRules extracts the rules from the listing, and reports whether the
// document was one den recognizes at all.
//
// The bool is not an error: an unrecognized listing is NOT a failure here, it
// is a reason to go ask the authorizer host by host — the path that worked
// before this function existed. That is why nothing in this file turns a
// schema change in `policy ls` into a refusal to attach, where readVerdict,
// which reads the authoritative answer, does exactly that.
//
// `rules` is a POINTER so that a document simply lacking the field — some other
// sbx command's output, a usage message that happens to be JSON — is
// distinguishable from a genuine empty rule set.
func readRules(output []byte) ([]policyRule, bool) {
	if len(bytes.TrimSpace(output)) == 0 {
		return nil, false
	}
	var doc struct {
		Rules *[]policyRule `json:"rules"`
	}
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&doc); err != nil {
		return nil, false
	}
	if doc.Rules == nil {
		return nil, false
	}
	return *doc.Rules, true
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
