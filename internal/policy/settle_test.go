package policy

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/sbx"
)

// testOptions: injected clock and sleep, so the suite stays instant.
func testOptions(t *testing.T) (Options, *int) {
	t.Helper()
	slept := 0
	fake := time.Unix(0, 0)
	return Options{
		Timeout:  60 * time.Second,
		Interval: 2 * time.Second,
		Sleep: func(d time.Duration) {
			slept++
			fake = fake.Add(d)
		},
		Now: func() time.Time { return fake },
	}, &slept
}

func allowed(sandbox string, hosts ...string) map[string]sbx.Response {
	m := make(map[string]sbx.Response, len(hosts))
	for _, h := range hosts {
		key := strings.Join([]string{"policy", "check", "network", "--sandbox", sandbox, "--json", h}, " ")
		m[key] = sbx.Response{Output: []byte(`{"allowed": true}`)}
	}
	return m
}

func TestSettlePassesWhenEverythingIsAllowed(t *testing.T) {
	o, slept := testOptions(t)
	f := &sbx.Fake{Responses: allowed("api", "github.com", "api.anthropic.com")}

	err := Settle(context.Background(), f, "api", []string{"github.com", "api.anthropic.com"}, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *slept != 0 {
		t.Errorf("no sleep should be needed when everything passes on the first try (%d)", *slept)
	}
}

// The exact argv matters: --sandbox scopes the evaluation to THIS sandbox's
// policy. Without it, we'd be validating the global policy — a green that
// proves nothing about what was just set.
func TestSettleQueriesTheScopedPolicy(t *testing.T) {
	o, _ := testOptions(t)
	f := &sbx.Fake{Responses: allowed("api", "github.com")}

	if err := Settle(context.Background(), f, "api", []string{"github.com"}, o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasCalled("policy", "check", "network", "--sandbox", "api", "--json", "github.com") {
		t.Errorf("expected argv with --sandbox; calls: %v", f.Calls)
	}
}

// Fail-closed: a host that never passes must exit in error, NAMING the
// blocked hosts — that's all the user gets to diagnose it.
func TestSettleFailsNamingTheBlockedHosts(t *testing.T) {
	o, _ := testOptions(t)
	responses := allowed("api", "github.com")
	f := &sbx.Fake{
		Responses: responses,
		Default:   sbx.Response{Output: []byte(`{"allowed": false}`)},
	}

	err := Settle(context.Background(), f, "api", []string{"github.com", "blocked.example.test"}, o)
	if err == nil {
		t.Fatal("a durably blocked host must produce an error (fail-closed)")
	}
	if !strings.Contains(err.Error(), "blocked.example.test") {
		t.Errorf("the message must name the blocked host; got: %v", err)
	}
	if strings.Contains(err.Error(), "github.com") {
		t.Errorf("the message must NOT list already-passed hosts; got: %v", err)
	}
}

// Since propagation isn't instant, a host first denied then allowed must
// eventually pass — that's the whole reason for the loop.
func TestSettleWaitsForPropagation(t *testing.T) {
	o, slept := testOptions(t)
	f := &fakeProgressive{allowedAfter: 3}

	if err := Settle(context.Background(), f, "api", []string{"slow.example.test"}, o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *slept == 0 {
		t.Error("the loop must have slept at least once")
	}
	if f.calls < 3 {
		t.Errorf("calls = %d, want at least 3", f.calls)
	}
}

// A missing `allowed` field means an sbx schema change. Reading it as false
// would run the loop to the timeout blaming the network.
func TestSettleRefusesOutputWithoutAnAllowedField(t *testing.T) {
	o, _ := testOptions(t)
	f := &sbx.Fake{Default: sbx.Response{Output: []byte(`{"other": "thing"}`)}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("output without an allowed field must fail immediately")
	}
	if !strings.Contains(err.Error(), "allowed") || !strings.Contains(err.Error(), `{"other": "thing"}`) {
		t.Errorf("the message must name the missing field and show the raw output; got: %v", err)
	}
}

func TestSettleWithoutHosts(t *testing.T) {
	o, _ := testOptions(t)
	f := &sbx.Fake{Default: sbx.Response{Err: errors.New("must not be called")}}

	if err := Settle(context.Background(), f, "api", nil, o); err != nil {
		t.Errorf("an empty allowlist is not an error: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("no call should be made; calls: %v", f.Calls)
	}
}

func TestSettleRespectsContextCancellation(t *testing.T) {
	o, _ := testOptions(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	f := &sbx.Fake{Default: sbx.Response{Output: []byte(`{"allowed": false}`)}}
	err := Settle(ctx, f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("a canceled context must interrupt the loop")
	}
	// "err != nil" alone would prove nothing: the injected clock eventually
	// reaches the timeout, and a loop that IGNORES the context would also
	// return an error — the fail-closed one. What's checked here is that the
	// interruption really comes from the cancellation, and that no call was
	// attempted on an already-dead context.
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the error must wrap context.Canceled; got: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("no sbx call on a canceled context; calls: %v", f.Calls)
	}
}

// An already-allowed host is not reprobed: each round only probes hosts still
// blocked. Without this, a twenty-host allowlist with one slow host would
// multiply calls to sbx by twenty each round, and the timeout message might
// start citing hosts that actually pass.
func TestSettleDoesNotReprobeHostsAlreadyPassed(t *testing.T) {
	o, _ := testOptions(t)
	f := &fakePerHost{allowedAfter: map[string]int{"slow.example.test": 3}}

	if err := Settle(context.Background(), f, "api", []string{"fast.example.test", "slow.example.test"}, o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := f.calls["fast.example.test"]; n != 1 {
		t.Errorf("the immediately-allowed host was queried %d times, want 1; calls: %v", n, f.calls)
	}
	if n := f.calls["slow.example.test"]; n != 3 {
		t.Errorf("the slow host was queried %d times, want 3; calls: %v", n, f.calls)
	}
}

// Nothing forces a caller through DefaultOptions(): Options can also be built
// by hand. Fields left at zero must produce an error naming the offenders —
// never a nil panic on Sleep or Now, and especially never a silently
// disarmed settle-loop (a zero Timeout leaves the loop with no patience, a
// zero Interval makes it hammer sbx nonstop). Guessing on the caller's behalf
// would turn den's only network guard into a guard that guards nothing,
// without saying so.
func TestSettleRefusesIncompleteOptions(t *testing.T) {
	complete := func() Options {
		return Options{
			Timeout:  30 * time.Second,
			Interval: time.Second,
			Sleep:    func(time.Duration) {},
			Now:      time.Now,
		}
	}
	noTimeout := complete()
	noTimeout.Timeout = 0
	noInterval := complete()
	noInterval.Interval = 0
	noSleep := complete()
	noSleep.Sleep = nil
	noNow := complete()
	noNow.Now = nil
	// An ABSURD relation, which field-by-field inspection doesn't see: 30s of
	// real sleep for a timeout announced at 1s.
	intervalTooBig := complete()
	intervalTooBig.Timeout = time.Second
	intervalTooBig.Interval = 30 * time.Second

	cases := []struct {
		name    string
		options Options
		field   string
		// output: what sbx returns. Chosen so that REMOVING the validation
		// makes the case fail fast (nil error or panic), instead of letting
		// it loop.
		output string
	}{
		{"everything zero", Options{}, "Timeout", `{"allowed": true}`},
		{"no Timeout", noTimeout, "Timeout", `{"allowed": true}`},
		{"no Interval", noInterval, "Interval", `{"allowed": true}`},
		{"no Sleep", noSleep, "Sleep", `{"allowed": false}`},
		{"no Now", noNow, "Now", `{"allowed": false}`},
		{"Interval bigger than Timeout", intervalTooBig, "Interval", `{"allowed": true}`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &sbx.Fake{Default: sbx.Response{Output: []byte(c.output)}}

			err := Settle(context.Background(), f, "api", []string{"github.com"}, c.options)
			if err == nil {
				t.Fatal("incomplete Options must be refused")
			}
			if !strings.Contains(err.Error(), c.field) {
				t.Errorf("the message must name field %s; got: %v", c.field, err)
			}
			if !strings.Contains(err.Error(), "DefaultOptions") {
				t.Errorf("the message must point to DefaultOptions(); got: %v", err)
			}
			if len(f.Calls) != 0 {
				t.Errorf("invalid Options must produce NO sbx call; calls: %v", f.Calls)
			}
		})
	}
}

// DefaultOptions must remain acceptable by Settle: it's the only documented
// constructor, and the test above would be worthless if the validation also
// refused the default options.
func TestDefaultOptionsIsAccepted(t *testing.T) {
	f := &sbx.Fake{Responses: allowed("api", "github.com")}

	if err := Settle(context.Background(), f, "api", []string{"github.com"}, DefaultOptions()); err != nil {
		t.Fatalf("DefaultOptions() must be usable as-is: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Correction round 1: paths the first suite didn't reach.
// ---------------------------------------------------------------------------

// clock is the injected clock, but one that remembers: it gives access to
// the ELAPSED time, not just the sleep count. Without it, neither the
// Timeout nor the Interval value is observable from a test.
type clock struct {
	slept   int
	start   time.Time
	current time.Time
}

func newClock() *clock {
	d := time.Unix(0, 0)
	return &clock{start: d, current: d}
}

func (c *clock) options(timeout, interval time.Duration) Options {
	return Options{
		Timeout:  timeout,
		Interval: interval,
		Sleep: func(d time.Duration) {
			c.slept++
			c.current = c.current.Add(d)
		},
		Now: func() time.Time { return c.current },
	}
}

func (c *clock) elapsed() time.Duration { return c.current.Sub(c.start) }

// C-1 — when sbx's invocation fails WITHOUT an exploitable verdict, den must
// fail. This is the branch that decides whether den attaches a shell when
// the policy could never be checked: a fail-open here would be exactly the
// "half-working" state §7 forbids.
func TestSettleFailsWhenSbxFailsWithoutAVerdict(t *testing.T) {
	o, _ := testOptions(t)
	f := &sbx.Fake{Default: sbx.Response{Err: errors.New("boom")}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("an sbx that fails without returning anything usable must fail Settle")
	}
	if !strings.Contains(err.Error(), "github.com") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("the message must name the probed host and the underlying failure; got: %v", err)
	}
	// A check that could not run at all has one common cause den can name
	// without guessing: sbx answers no policy command before its one-time
	// `sbx policy init`. On a fresh laptop (reported 2026-08-18) this message
	// was the wall the user hit at their first `den up`, and it sent them
	// looking at an allowlist that was not the problem.
	if !strings.Contains(err.Error(), "sbx policy init") {
		t.Errorf("the message names no cause the user can act on; got: %v", err)
	}

	// And a long allowlist does not multiply it: the per-host pass returns the
	// FIRST error in allowlist order, so the remedy is one sentence whatever the
	// nest declares. A message repeating it per host would be the shape this
	// change removed from the plan.
	many := &sbx.Fake{Default: sbx.Response{Err: errors.New("boom")}}
	err = Settle(context.Background(), many, "api",
		[]string{"github.com", "gitlab.com", "registry.example.test"}, o)
	if err == nil {
		t.Fatal("an sbx that fails without returning anything usable must fail Settle")
	}
	if n := strings.Count(err.Error(), "sbx policy init"); n != 1 {
		t.Errorf("the remedy appears %d times, want once; got: %v", n, err)
	}
}

// C-1 — sbx's exit code is NOT the verdict. Nobody here can confirm that
// `sbx policy check` exits 0 for a simply-denied host; den must not depend
// on that. If the output carries an exploitable `allowed`, IT decides, and
// the loop keeps looping.
func TestSettleUsesTheVerdictEvenWhenSbxExitsInError(t *testing.T) {
	t.Run("denial with a non-zero exit code: loop, then fail-closed", func(t *testing.T) {
		h := newClock()
		f := &sbx.Fake{Default: sbx.Response{
			Output: []byte(`{"allowed": false}`),
			Err:    errors.New("exit status 1"),
		}}

		err := Settle(context.Background(), f, "api", []string{"github.com"}, h.options(60*time.Second, 2*time.Second))
		if err == nil {
			t.Fatal("a durably denied host must produce an error")
		}
		if h.slept == 0 {
			t.Errorf("the denial must be treated as a VERDICT, hence retried: %d sleep(s)", h.slept)
		}
		if !strings.Contains(err.Error(), "fail-closed") {
			t.Errorf("the failure must be the fail-closed timeout, not a transport failure; got: %v", err)
		}
		// The exit code must not be presented as THE cause — but nothing bars
		// MENTIONING it as a hint. What's forbidden is attribution: it must
		// only appear after the "Hint" framing.
		msg := err.Error()
		if i, j := strings.Index(msg, "Hint"), strings.Index(msg, "exit status 1"); j >= 0 && (i < 0 || j < i) {
			t.Errorf("the command failure must only appear under the \"Hint\" framing; got: %v", err)
		}
	})

	// The asymmetry is deliberate: trusting a "yes" from an invocation that
	// failed (truncated stdout, unknown flag, missing sandbox) would be the
	// only path by which this package could open a shell on a policy that
	// was never checked. A "no" is safe to trust, a "yes" is not.
	t.Run("allowance with a non-zero exit code: refused anyway", func(t *testing.T) {
		o, _ := testOptions(t)
		f := &sbx.Fake{Default: sbx.Response{
			Output: []byte(`{"allowed": true}`),
			Err:    errors.New("exit status 1"),
		}}

		err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
		if err == nil {
			t.Fatal("a \"yes\" from a failed command must not be enough to attach")
		}
		if !strings.Contains(err.Error(), "exit status 1") {
			t.Errorf("the message must show the command failure; got: %v", err)
		}
	})
}

// C-1 / I-5 — output that isn't JSON at all (usage, help message, plain-text
// error) must fail, and the message must SHOW that output.
func TestSettleFailsOnUnreadableOutput(t *testing.T) {
	o, _ := testOptions(t)
	f := &sbx.Fake{Default: sbx.Response{Output: []byte("usage: sbx policy check network [flags]")}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("non-JSON output must fail Settle")
	}
	if !strings.Contains(err.Error(), "unreadable") || !strings.Contains(err.Error(), "usage: sbx policy check network") {
		t.Errorf("the message must call itself unreadable and show the output; got: %v", err)
	}
}

// I-5 — the case of an sbx that writes its verdict to stderr, or refuses
// --json: stdout is empty. The message meant to show the raw output must
// not end on a bare colon.
func TestSettleNamesAnEmptyOutput(t *testing.T) {
	o, _ := testOptions(t)
	f := &sbx.Fake{Default: sbx.Response{Output: []byte("   \n")}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("empty output must fail Settle")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("the message must explicitly say the output is empty; got: %v", err)
	}
	if strings.HasSuffix(strings.TrimSpace(err.Error()), ":") {
		t.Errorf("the message must not end on a bare colon; got: %v", err)
	}
}

// I-5 (continued) — non-JSON output can contain anything, including escape
// sequences and null bytes. Spitting them back verbatim into an error
// message garbles the reader's terminal; that's what %q prevents, and
// nothing tested for it since empty output got its own branch.
func TestSettleEscapesUnprintableOutput(t *testing.T) {
	o, _ := testOptions(t)
	f := &sbx.Fake{Default: sbx.Response{Output: []byte("\x1b[2Joops\x00")}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("non-JSON output must fail Settle")
	}
	if strings.ContainsAny(err.Error(), "\x00\x1b") {
		t.Errorf("the message must not contain raw control bytes; got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "oops") {
		t.Errorf("the message must still show the output; got: %v", err)
	}
}

// I-6 — on a schema mismatch, the raw output is shown (the brief requires
// it) and it may contain hosts that are neither blocked nor even probed. The
// message must therefore FRAME what it shows: name the host actually
// probed, and announce that what follows is sbx's output, not den's verdict.
func TestSettleFramesTheRawOutputOnASchemaMismatch(t *testing.T) {
	o, _ := testOptions(t)
	verbose := `{"policy":{"allow":["github.com","api.anthropic.com"]},"verdict":"deny"}`
	f := &sbx.Fake{Default: sbx.Response{Output: []byte(verbose)}}

	err := Settle(context.Background(), f, "api", []string{"blocked.example.test"}, o)
	if err == nil {
		t.Fatal("output without an allowed field must fail immediately")
	}
	if !strings.Contains(err.Error(), "blocked.example.test") {
		t.Errorf("the message must name the host actually probed; got: %v", err)
	}
	if !strings.Contains(err.Error(), "Raw output:") {
		t.Errorf("the message must announce the raw output before showing it; got: %v", err)
	}
	if !strings.Contains(err.Error(), verbose) {
		t.Errorf("the message must show the raw output; got: %v", err)
	}
}

// A3 — noise AFTER the JSON value (a log line, a banner, NDJSON) must not
// prevent den from attaching: the verdict is in the first value.
// json.Unmarshal, by contrast, refuses any content following the value.
func TestSettleToleratesNoiseAfterTheJSON(t *testing.T) {
	o, _ := testOptions(t)
	f := &sbx.Fake{Default: sbx.Response{Output: []byte("{\"allowed\": true}\n{\"warn\":\"cache miss\"}\n")}}

	if err := Settle(context.Background(), f, "api", []string{"github.com"}, o); err != nil {
		t.Fatalf("the first JSON value's verdict must be enough: %v", err)
	}
}

// D4 — encoding/json field matching is CASE-INSENSITIVE. `{"ALLOWED": true}`
// is therefore a valid verdict and den attaches. This is a deliberate
// choice, not an oversight (see readVerdict's comment): the test is here so
// that the day someone wants to make it strict, they do it ON PURPOSE.
func TestSettleAcceptsAVerdictUnderAnotherCasing(t *testing.T) {
	for _, form := range []string{`{"ALLOWED": true}`, `{"Allowed": true}`, `{"aLLoWeD": true}`} {
		t.Run(form, func(t *testing.T) {
			o, _ := testOptions(t)
			f := &sbx.Fake{Default: sbx.Response{Output: []byte(form)}}

			if err := Settle(context.Background(), f, "api", []string{"github.com"}, o); err != nil {
				t.Fatalf("%s must be read as a verdict; got: %v", form, err)
			}
		})
	}
}

// The counterpart of the previous test, and the one that really protects: the
// tolerance stops EXACTLY at casing. A neighboring field — prefix, suffix,
// stray space — remains a schema change, hence an immediate failure. Without
// this test, "case-insensitive" could drift toward fuzzy matching with
// nothing opposing it.
func TestSettleRefusesAFieldNeighboringAllowed(t *testing.T) {
	for _, form := range []string{`{"allowedx": true}`, `{"allow": true}`, `{"allowed ": true}`} {
		t.Run(form, func(t *testing.T) {
			o, _ := testOptions(t)
			f := &sbx.Fake{Default: sbx.Response{Output: []byte(form)}}

			err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
			if err == nil {
				t.Fatalf("%s is not an allowed field: fail-closed must trigger", form)
			}
			if !strings.Contains(err.Error(), "allowed") {
				t.Errorf("the message must name the missing field; got: %v", err)
			}
		})
	}
}

// Measured in 17b, undocumented until now: when TWO keys match the same
// field, the LAST one wins — the decoder assigns them in document order,
// and exact matching doesn't take precedence over case-insensitive matching.
// The verdict of `{"ALLOWED":false,"allowed":true}` therefore depends on
// sbx's key-writing order.
//
// No safety consequence (both keys come from the same producer), but it's
// the property to have in view before concluding "the exact field always
// wins". The point of this test is to make the day this becomes FALSE
// visible, not to bless the situation.
func TestSettleLastKeyWinsOnDuplicate(t *testing.T) {
	cases := []struct {
		name   string
		output string
		attach bool
	}{
		{"the last one is false", `{"allowed": true, "ALLOWED": false}`, false},
		{"the last one is true", `{"ALLOWED": false, "allowed": true}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, _ := testOptions(t)
			f := &sbx.Fake{Default: sbx.Response{Output: []byte(c.output)}}

			err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
			if c.attach && err != nil {
				t.Fatalf("%s: expected an allowing verdict; got: %v", c.output, err)
			}
			if !c.attach && err == nil {
				t.Fatalf("%s: expected a refusal (the last verdict is false)", c.output)
			}
		})
	}
}

// I-3 — in production, cancellation arrives DURING a pass, not between
// rounds: sbx gets killed, and the runner returns a transport error wrapping
// no context reason (measured: "signal: killed"). Settle must recognize the
// cancellation itself, and not blame a host for a Ctrl-C.
func TestSettleRecognizesCancellationDuringAPass(t *testing.T) {
	o, _ := testOptions(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fakeCanceling{cancel: cancel}

	err := Settle(ctx, f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("a cancellation during a pass must interrupt Settle")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the error must wrap context.Canceled; got: %v", err)
	}
	if !strings.Contains(err.Error(), "interrupted") {
		t.Errorf("the message must speak of interrupted waiting; got: %v", err)
	}
	if strings.Contains(err.Error(), "github.com") {
		t.Errorf("a Ctrl-C is not the probed host's fault; got: %v", err)
	}
}

// I-4 — the --sandbox scope is what gives the whole check its value. An
// empty name goes straight into the argv unchanged and proves nothing
// anymore.
func TestSettleRefusesAnInvalidSandboxName(t *testing.T) {
	cases := []struct {
		name    string
		sandbox string
	}{
		{"empty", ""},
		{"non-canonical", "api."},
		{"forbidden character", "api/feat"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, _ := testOptions(t)
			f := &sbx.Fake{Default: sbx.Response{Output: []byte(`{"allowed": true}`)}}

			err := Settle(context.Background(), f, c.sandbox, []string{"github.com"}, o)
			if err == nil {
				t.Fatalf("a %s sandbox name must be refused", c.name)
			}
			if len(f.Calls) != 0 {
				t.Errorf("no probe should go out on an invalid scope; calls: %v", f.Calls)
			}
		})
	}
}

// Minor — a stray `-` in the allowlist YAML produces an empty host. Probed
// as-is, it would send `--json ""` and Settle might return nil. A host made
// of blanks comes from the same kind of slip (`-   ` or an empty quoted
// value) and isn't any more probeable; without the case below, nothing
// would distinguish TrimSpace(h) == "" from h == "".
func TestSettleRefusesAnEmptyHost(t *testing.T) {
	cases := []struct {
		name string
		host string
	}{
		{"empty string", ""},
		{"spaces", "   "},
		{"tab and newline", "\t\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, _ := testOptions(t)
			f := &sbx.Fake{Default: sbx.Response{Output: []byte(`{"allowed": true}`)}}

			err := Settle(context.Background(), f, "api", []string{"github.com", c.host}, o)
			if err == nil {
				t.Fatal("an empty host in the allowlist must be refused")
			}
			if !strings.Contains(err.Error(), "empty") {
				t.Errorf("the message must say the host is empty; got: %v", err)
			}
			if len(f.Calls) != 0 {
				t.Errorf("no probe should go out with an invalid allowlist; calls: %v", f.Calls)
			}
		})
	}
}

// Minor — a host present twice (nest + stack) must only be probed once per
// round, and only appear once in the message.
func TestSettleDedupesTheAllowlist(t *testing.T) {
	h := newClock()
	f := &fakePerHost{allowedAfter: map[string]int{"a.test": 99}}

	err := Settle(context.Background(), f, "api", []string{"a.test", "a.test"}, h.options(5*time.Second, 2*time.Second))
	if err == nil {
		t.Fatal("the host never passes: error expected")
	}
	// 3 sleeps ⇒ 4 rounds; a single host to probe per round.
	if n := f.calls["a.test"]; n != 4 {
		t.Errorf("host probed %d times, want 4 (once per round); calls: %v", n, f.calls)
	}
	if n := strings.Count(err.Error(), "a.test"); n != 1 {
		t.Errorf("the host appears %d times in the message, want 1: %v", n, err)
	}
}

// I-2 — until now only the PRESENCE of a sleep was checked, never its
// duration, and the existence of a timeout, never its value. A den that
// waits ten times too long, or hammers sbx twice as fast, passed CI. The
// injected clock knows exactly how much time has "passed".
func TestSettleRespectsTimeoutAndInterval(t *testing.T) {
	cases := []struct {
		timeout  time.Duration
		interval time.Duration
		sleeps   int // = ceil(timeout / interval)
	}{
		{60 * time.Second, 2 * time.Second, 30},
		{5 * time.Second, 2 * time.Second, 3},
		{3 * time.Second, 2 * time.Second, 2},
		{2 * time.Second, 2 * time.Second, 1},
	}
	for _, c := range cases {
		t.Run(c.timeout.String()+"/"+c.interval.String(), func(t *testing.T) {
			h := newClock()
			f := &sbx.Fake{Default: sbx.Response{Output: []byte(`{"allowed": false}`)}}

			err := Settle(context.Background(), f, "api", []string{"blocked.test"}, h.options(c.timeout, c.interval))
			if err == nil {
				t.Fatal("expected a timeout error")
			}
			if !strings.Contains(err.Error(), "fail-closed") {
				t.Fatalf("the fail-closed timeout must be the one returned; got: %v", err)
			}
			if h.slept != c.sleeps {
				t.Errorf("slept %d times, want %d: sleep duration isn't Interval", h.slept, c.sleeps)
			}
			if want := time.Duration(c.sleeps) * c.interval; h.elapsed() != want {
				t.Errorf("elapsed time %s, want %s: the deadline isn't Now()+Timeout", h.elapsed(), want)
			}
			// TWO calls per round, and one round more than sleeps: the
			// `policy ls` fast path is attempted first, and this Fake's
			// `{"allowed": false}` is not a rule listing — so every round falls
			// back on the authoritative per-host probe. That fallback is the
			// point of the count: it proves an unrecognized listing costs one
			// wasted call and changes NOTHING else about the loop's cadence.
			if n := len(f.Calls); n != 2*(c.sleeps+1) {
				t.Errorf("%d calls, want %d (one listing + one probe per round)", n, 2*(c.sleeps+1))
			}
		})
	}
}

// I-2 — DefaultOptions()'s values are the real patience of the project's
// only network guard; nothing locked them down.
func TestDefaultOptionsValues(t *testing.T) {
	o := DefaultOptions()
	if o.Timeout != 60*time.Second {
		t.Errorf("Timeout = %s, want 1m0s", o.Timeout)
	}
	if o.Interval != 2*time.Second {
		t.Errorf("Interval = %s, want 2s", o.Interval)
	}
	if o.Sleep == nil || o.Now == nil {
		t.Error("DefaultOptions() must supply a full real clock")
	}
}

// I-1 — an injected clock that doesn't advance passes validate() (which
// inspects values, not a relation) and made Settle loop WITHOUT END. This is
// reachable with correct production code and legal Options: a naive clock
// double at task 12 would hang the whole project's `go test ./...` with
// nothing pointing at policy.
func TestSettleBoundsTheNumberOfRounds(t *testing.T) {
	f := func() *sbx.Fake { return &sbx.Fake{Default: sbx.Response{Output: []byte(`{"allowed": false}`)}} }

	// Two distinct causes fall into the same bound, and the message must
	// distinguish them: a frozen clock, and a clock that advances — but
	// slower than one Interval per round, because Sleep doesn't advance it.
	// The second profile is the most plausible caller double: a "no-op Sleep
	// so tests don't wait" + a real clock.
	t.Run("frozen clock", func(t *testing.T) {
		o := Options{
			Timeout:  60 * time.Second,
			Interval: 2 * time.Second,
			Sleep:    func(time.Duration) {},
			Now:      func() time.Time { return time.Unix(0, 0) },
		}

		err := boundedSettle(t, o, f())
		if err == nil {
			t.Fatal("a frozen clock must produce an error, not a success")
		}
		msg := err.Error()
		if !strings.Contains(msg, "clock") || !strings.Contains(msg, "frozen") {
			t.Errorf("the message must point at a frozen clock; got: %v", err)
		}
		if !strings.Contains(msg, "made 31 rounds") {
			t.Errorf("the message must announce the number of rounds actually run; got: %v", err)
		}
	})

	t.Run("too-slow clock", func(t *testing.T) {
		current := time.Unix(0, 0)
		o := Options{
			Timeout:  60 * time.Second,
			Interval: 2 * time.Second,
			Sleep:    func(time.Duration) {}, // does NOT advance the clock
			Now: func() time.Time { // ...which advances anyway, too little
				current = current.Add(time.Second)
				return current
			},
		}

		err := boundedSettle(t, o, f())
		if err == nil {
			t.Fatal("a too-slow clock must produce an error, not a success")
		}
		msg := err.Error()
		// The clock did advance (by more than a minute, here): calling it
		// frozen would be false, and would send the fix to the wrong field.
		if strings.Contains(msg, "frozen") {
			t.Errorf("the clock advances: the message must not call it frozen; got: %v", err)
		}
		if !strings.Contains(msg, "only advanced by") {
			t.Errorf("the message must report the clock's ADVANCE; got: %v", err)
		}
		if !strings.Contains(msg, "Sleep") {
			t.Errorf("the message must name Sleep, the field to fix; got: %v", err)
		}
		if !strings.Contains(msg, "made 31 rounds") {
			t.Errorf("the message must announce the number of rounds actually run; got: %v", err)
		}
	})

	// Degenerate case: Now() is assumed monotonic, and a clock going backward
	// is not the behavior of any plausible double. But if it does go
	// backward, saying it "only advanced by -1ms" makes no sense, and calling
	// it frozen would be false: the only true wording is the one naming the
	// regression.
	t.Run("clock going backward", func(t *testing.T) {
		current := time.Unix(0, 0)
		o := Options{
			Timeout:  60 * time.Second,
			Interval: 2 * time.Second,
			Sleep:    func(time.Duration) {},
			Now: func() time.Time {
				current = current.Add(-time.Millisecond)
				return current
			},
		}

		err := boundedSettle(t, o, f())
		if err == nil {
			t.Fatal("a clock going backward must produce an error, not a success")
		}
		msg := err.Error()
		if !strings.Contains(msg, "backward") {
			t.Errorf("the message must name the clock's regression; got: %v", err)
		}
		if strings.Contains(msg, "frozen") || strings.Contains(msg, "only advanced by") {
			t.Errorf("a clock going backward is neither frozen nor slow; got: %v", err)
		}
	})
}

// boundedSettle runs Settle under a safety net: without the round-count
// bound, these cases would NEVER return. The only place in the package
// where a real clock is used — and it costs nothing as long as the bound
// exists.
func boundedSettle(t *testing.T, o Options, r sbx.Runner) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- Settle(context.Background(), r, "api", []string{"github.com"}, o) }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Settle never returned: the loop is not bounded in number of rounds")
		return nil
	}
}

// The review's ruling (read the verdict before concluding from the error)
// has a price: when sbx fails FOR ANOTHER REASON while still returning a
// refusal, den loops to the timeout and returns a message pointing at the
// allowlist — when the real cause was in plain sight every round. The error
// must not become THE cause (that would roll back the ruling), but it must
// be joined as a hint.
func TestSettleJoinsTheLastRunnerErrorToTheTimeout(t *testing.T) {
	h := newClock()
	f := &sbx.Fake{Default: sbx.Response{
		Output: []byte(`{"allowed": false}`),
		Err:    errors.New(`sandbox "api" not found`),
	}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, h.options(60*time.Second, 2*time.Second))
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "fail-closed") {
		t.Errorf("the fail-closed timeout remains the main failure; got: %v", err)
	}
	i, j := strings.Index(msg, "Hint"), strings.Index(msg, `sandbox "api" not found`)
	if i < 0 || j < 0 {
		t.Fatalf("the error observed every round must be joined under the \"Hint\" framing; got: %v", err)
	}
	if j < i {
		t.Errorf("the error must not precede its framing: it would be presented as the cause; got: %v", err)
	}
}

// A hint naming the wrong host is worse than no hint: it sends the user to
// diagnose a host that did nothing wrong, in the very message that cost a
// whole round to get right. The (host, error) pairing only shows with AT
// LEAST TWO blocked hosts carrying distinct errors — with a single one, any
// pairing is correct.
//
// The choice, along the way: only ONE hint is joined, that of the last host
// that failed on the last round. Stacking them would bloat the message for
// dubious gain (transport failures are heavily correlated), and above all a
// hint is only worth showing if it's still true at the moment it's
// displayed.
func TestSettlePairsTheHintWithItsHost(t *testing.T) {
	h := newClock()
	key := func(host string) string { return "policy check network --sandbox api --json " + host }
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		key("a.test"): {Output: []byte(`{"allowed": false}`), Err: errors.New("first one down")},
		key("b.test"): {Output: []byte(`{"allowed": false}`), Err: errors.New("second one down")},
	}}

	err := Settle(context.Background(), f, "api", []string{"a.test", "b.test"}, h.options(60*time.Second, 2*time.Second))
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "check of b.test (second one down)") {
		t.Errorf("the hint must pair the error with the host it comes from; got: %v", err)
	}
	if strings.Contains(msg, "first one down") {
		t.Errorf("only one hint is joined, the last one; got: %v", err)
	}
	// Both remain blocked: both must be listed.
	if !strings.Contains(msg, "a.test") || !strings.Contains(msg, "b.test") {
		t.Errorf("both blocked hosts must be listed; got: %v", err)
	}
}

// Symmetric to the previous test: without an invocation failure, there's no
// hint to give, and inventing one would send a false lead.
func TestSettleHasNoHintWhenTheInvocationSucceeds(t *testing.T) {
	h := newClock()
	f := &sbx.Fake{Default: sbx.Response{Output: []byte(`{"allowed": false}`)}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, h.options(60*time.Second, 2*time.Second))
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if strings.Contains(err.Error(), "Hint") {
		t.Errorf("no invocation failed: no hint to join; got: %v", err)
	}
}

// The hint must not reopen the door the brief's property closes: an error
// observed on a host that DID end up passing must not come back in the
// timeout message, neither as a host nor as a diagnostic.
func TestSettleDoesNotJoinTheHintOfAnAlreadyPassedHost(t *testing.T) {
	h := newClock()
	f := &fakeFleetingHint{}

	err := Settle(context.Background(), f, "api",
		[]string{"passes.test", "blocked.test"}, h.options(60*time.Second, 2*time.Second))
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	msg := err.Error()
	if strings.Contains(msg, "passes.test") {
		t.Errorf("the eventually-allowed host must not come back in the message; got: %v", err)
	}
	if strings.Contains(msg, "fleeting glitch") {
		t.Errorf("the hint of a host that passed must not survive; got: %v", err)
	}
	if !strings.Contains(msg, "blocked.test") {
		t.Errorf("the actually blocked host must be named; got: %v", err)
	}
}

// sbx's output lands in an error message, which lands in a terminal. A
// multi-kilobyte output spat back verbatim drowns the diagnostic it's
// supposed to carry.
func TestSettleTruncatesAHugeOutput(t *testing.T) {
	big := strings.Repeat("x", 4096)
	cases := []struct {
		name   string
		output string
	}{
		{"unreadable output", big},
		{"output without an allowed field", `{"note":"` + big + `"}`},
		// The cut lands here in the MIDDLE of an "é" (the prefix is exactly
		// 511 bytes). Cutting there would produce a U+FFFD in the message,
		// wrongly suggesting sbx emitted invalid bytes. This case goes
		// through the "schema" branch, in %s: the unreadable branch, by
		// contrast, is in %q, which escapes the orphan byte and would mask
		// the defect.
		{"cut in the middle of a rune", `{"note":"` + strings.Repeat("x", 502) + strings.Repeat("é", 2000) + `"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, _ := testOptions(t)
			f := &sbx.Fake{Default: sbx.Response{Output: []byte(c.output)}}

			err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
			if err == nil {
				t.Fatal("expected an error")
			}
			if n := len(err.Error()); n > 1024 {
				t.Errorf("message of %d bytes: the raw output must be bounded", n)
			}
			if !strings.Contains(err.Error(), "truncated") {
				t.Errorf("truncated output must be announced as such; got (%d bytes): %.200v", len(err.Error()), err)
			}
			if strings.ContainsRune(err.Error(), '�') {
				t.Errorf("the cut must land on a rune boundary; got: %.200v", err)
			}
		})
	}
}

// listing builds a `sbx policy ls <sandbox> --json` response: one scoped rule
// carrying scopedHosts, plus one global rule carrying globalHosts.
func listing(sandbox string, scopedHosts, globalHosts []string) (string, sbx.Response) {
	quote := func(hs []string) string {
		if len(hs) == 0 {
			return ""
		}
		return `"` + strings.Join(hs, `","`) + `"`
	}
	doc := `{"rules":[
		{"id":"global-1","scope":"global","applies_to":"all","resource_type":"network",
		 "decision":"allow","status":"active","resources":[` + quote(globalHosts) + `]},
		{"id":"scoped-1","scope":"sandbox:` + sandbox + `","applies_to":"sandbox:` + sandbox + `",
		 "resource_type":"network","decision":"allow","status":"active","resources":[` + quote(scopedHosts) + `]}
	]}`
	return "policy ls " + sandbox + " --json", sbx.Response{Output: []byte(doc)}
}

// THE point of the fast path: one listing plus ONE authoritative check, whatever
// the allowlist's length. The sequential sweep cost one `sbx` process per host,
// and a process is ~390 ms of the ~486 ms a check costs — on a 26-host stack
// that was ~12 s of a ~36 s spawn.
func TestSettleAnswersALongAllowlistInTwoCalls(t *testing.T) {
	o, slept := testOptions(t)
	hosts := []string{"a.test:443", "b.test:443", "c.test:443", "d.test:443", "e.test:443"}
	key, resp := listing("api", hosts, []string{"unrelated.test:443"})
	responses := allowed("api", hosts...)
	responses[key] = resp
	f := &sbx.Fake{Responses: responses, Default: sbx.Response{Err: errors.New("unscripted call")}}

	if err := Settle(context.Background(), f, "api", hosts, o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *slept != 0 {
		t.Errorf("nothing had to propagate: %d sleep(s)", *slept)
	}
	if n := len(f.Calls); n != 2 {
		t.Errorf("%d calls for %d hosts, want 2 (one listing + one witness); calls: %v", n, len(hosts), f.Calls)
	}
}

// The listing alone must NEVER be enough. Measured on 2026-08-05: `policy ls`
// showed the rule 83–172 ms before `policy check` allowed the host it uniquely
// covers, on four consecutive spawns. Attaching on the listing alone is the
// half-start §7 forbids, only narrower.
func TestSettleDoesNotAttachOnTheListingAlone(t *testing.T) {
	h := newClock()
	hosts := []string{"scoped.test:443"}
	key, resp := listing("api", hosts, nil)
	f := &sbx.Fake{
		Responses: map[string]sbx.Response{key: resp},
		Default:   sbx.Response{Output: []byte(`{"allowed": false}`)},
	}

	err := Settle(context.Background(), f, "api", hosts, h.options(6*time.Second, 2*time.Second))
	if err == nil {
		t.Fatal("the rule is listed but the authorizer refuses: den must not attach")
	}
	if !strings.Contains(err.Error(), "fail-closed") {
		t.Errorf("the refusal must be the fail-closed timeout; got: %v", err)
	}
}

// The witness is the host NO other rule mentions: den's allowlist lands as a
// single scoped rule, so a host only that rule covers is allowed exactly when
// the rule is live. Picking a globally-covered host instead would return
// "allowed" from someone else's rule and prove nothing.
func TestSettlePicksAWitnessNoOtherRuleCovers(t *testing.T) {
	o, _ := testOptions(t)
	hosts := []string{"everywhere.test:443", "only-here.test:443"}
	key, resp := listing("api", hosts, []string{"everywhere.test:443"})
	responses := allowed("api", "only-here.test:443")
	responses[key] = resp
	f := &sbx.Fake{Responses: responses, Default: sbx.Response{Err: errors.New("unscripted call")}}

	if err := Settle(context.Background(), f, "api", hosts, o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasCalled("policy", "check", "network", "--sandbox", "api", "--json", "only-here.test:443") {
		t.Errorf("the witness must be the host no global rule covers; calls: %v", f.Calls)
	}
	if f.HasCalled("policy", "check", "network", "--sandbox", "api", "--json", "everywhere.test:443") {
		t.Errorf("the globally-covered host proves nothing and must not be the witness; calls: %v", f.Calls)
	}
}

// A host not in the listing yet is ordinary propagation — the very thing the
// loop exists for. It must SLEEP, not fall back on the whole per-host sweep:
// collapsing the two would make a round-1 miss cost the listing PLUS the sweep,
// i.e. slower than before precisely when the loop is doing its job.
func TestSettleSleepsRatherThanSweepingWhenTheRuleIsIncomplete(t *testing.T) {
	h := newClock()
	hosts := []string{"present.test:443", "absent.test:443"}
	key, resp := listing("api", []string{"present.test:443"}, nil)
	f := &sbx.Fake{
		Responses: map[string]sbx.Response{key: resp},
		Default:   sbx.Response{Output: []byte(`{"allowed": true}`)},
	}

	err := Settle(context.Background(), f, "api", hosts, h.options(4*time.Second, 2*time.Second))
	if err == nil {
		t.Fatal("a host never listed must end in the fail-closed timeout")
	}
	if !strings.Contains(err.Error(), "absent.test:443") {
		t.Errorf("the message must name the host still missing; got: %v", err)
	}
	if strings.Contains(err.Error(), "present.test:443") {
		t.Errorf("a host already covered must not be listed as blocked; got: %v", err)
	}
	// 3 rounds (2 sleeps + 1), one listing each, and NO per-host probe.
	if n := len(f.Calls); n != 3 {
		t.Errorf("%d calls, want 3 listings and no sweep; calls: %v", n, f.Calls)
	}
}

// A `deny` rule brings a precedence question den deliberately does not model.
// Modelling it would mean re-implementing the authorizer, which is how the two
// diverge; so the whole round goes back to the authoritative per-host pass.
func TestSettleFallsBackOnTheSweepWhenADenyRuleExists(t *testing.T) {
	o, _ := testOptions(t)
	hosts := []string{"a.test:443"}
	doc := `{"rules":[
		{"id":"scoped-1","scope":"sandbox:api","resource_type":"network","decision":"allow",
		 "status":"active","resources":["a.test:443"]},
		{"id":"deny-1","scope":"global","resource_type":"network","decision":"deny",
		 "status":"active","resources":["a.test:443"]}
	]}`
	responses := allowed("api", "a.test:443")
	responses["policy ls api --json"] = sbx.Response{Output: []byte(doc)}
	f := &sbx.Fake{Responses: responses}

	if err := Settle(context.Background(), f, "api", hosts, o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasCalled("policy", "check", "network", "--sandbox", "api", "--json", "a.test:443") {
		t.Errorf("a deny rule must send the round to the authorizer; calls: %v", f.Calls)
	}
}

// A listing den does not recognize is NOT a refusal — unlike an unreadable
// `policy check`, which is. The difference is deliberate: the fast path is an
// optimization, so an sbx release that reshapes `policy ls` must cost den a
// wasted call, never a sandbox it declines to attach.
func TestSettleFallsBackOnAnUnrecognizedListing(t *testing.T) {
	cases := map[string]string{
		"no rules field":  `{"policies": []}`,
		"not json at all": `usage: sbx policy ls [SANDBOX]`,
		"empty output":    ``,
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			o, _ := testOptions(t)
			responses := allowed("api", "a.test:443")
			responses["policy ls api --json"] = sbx.Response{Output: []byte(out)}
			f := &sbx.Fake{Responses: responses}

			if err := Settle(context.Background(), f, "api", []string{"a.test:443"}, o); err != nil {
				t.Fatalf("an unrecognized listing must not refuse the attach: %v", err)
			}
			if !f.HasCalled("policy", "check", "network", "--sandbox", "api", "--json", "a.test:443") {
				t.Errorf("the authoritative probe must have run; calls: %v", f.Calls)
			}
		})
	}
}

// The fast path must not swallow the hint. A witness check that FAILS while
// still returning an exploitable refusal is exactly the case the hint exists
// for: without this, the loop times out on "check the nest's and stack's
// allowlist" while sbx was broken every round — the false lead hostAllowed's
// comment names — and the remedy would be doubly wrong, since the allowlist is
// fine and the rule simply is not live.
func TestSettleKeepsTheHintWhenTheWitnessCheckFails(t *testing.T) {
	h := newClock()
	hosts := []string{"witness.test:443"}
	key, resp := listing("api", hosts, nil)
	f := &sbx.Fake{
		Responses: map[string]sbx.Response{
			key: resp,
			"policy check network --sandbox api --json witness.test:443": {
				Output: []byte(`{"allowed": false}`),
				Err:    errors.New(`sandbox "api" not found`),
			},
		},
	}

	err := Settle(context.Background(), f, "api", hosts, h.options(4*time.Second, 2*time.Second))
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	msg := err.Error()
	i, j := strings.Index(msg, "Hint"), strings.Index(msg, `sandbox "api" not found`)
	if i < 0 || j < 0 {
		t.Fatalf("the failure observed every round must survive the fast path, under the \"Hint\" framing; got: %v", err)
	}
	if j < i {
		t.Errorf("the error must not precede its framing: it would read as the cause; got: %v", err)
	}
}

// den does not model globs, and must not: a host covered only by someone
// else's `**.foo.test:443` is simply not recognized verbatim, and the round
// falls back on the authorizer. The asymmetry is always in the safe direction.
func TestSettleDoesNotResolveGlobsItself(t *testing.T) {
	h := newClock()
	key, resp := listing("api", nil, []string{"**.foo.test:443"})
	f := &sbx.Fake{
		Responses: map[string]sbx.Response{key: resp},
		Default:   sbx.Response{Output: []byte(`{"allowed": false}`)},
	}

	err := Settle(context.Background(), f, "api", []string{"api.foo.test:443"}, h.options(4*time.Second, 2*time.Second))
	if err == nil {
		t.Fatal("a host matched only by a glob must not be declared covered")
	}
	if !strings.Contains(err.Error(), "api.foo.test:443") {
		t.Errorf("the message must name the host; got: %v", err)
	}
}

// The authoritative sweep runs concurrently now (maxProbeConcurrency). Its
// messages were all defined by the sequential walk's ORDER, so the results are
// consumed in allowlist order whatever the completion order: the hint must
// still pair with the LAST blocked host of the list, not the last goroutine to
// finish.
func TestSettleKeepsAllowlistOrderUnderConcurrency(t *testing.T) {
	h := newClock()
	key := func(host string) string { return "policy check network --sandbox api --json " + host }
	responses := map[string]sbx.Response{}
	hosts := make([]string, 0, 12)
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		host := name + ".test"
		hosts = append(hosts, host)
		responses[key(host)] = sbx.Response{
			Output: []byte(`{"allowed": false}`),
			Err:    errors.New("down: " + name),
		}
	}
	f := &sbx.Fake{Responses: responses, Default: sbx.Response{Output: []byte(`{"allowed": false}`)}}

	err := Settle(context.Background(), f, "api", hosts, h.options(2*time.Second, 2*time.Second))
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	// "l" is last in the allowlist: the sequential loop kept ITS error, and the
	// concurrent one must keep the same, run after run.
	if !strings.Contains(err.Error(), "check of l.test (down: l)") {
		t.Errorf("the hint must be the last host in ALLOWLIST order; got: %v", err)
	}
	for _, h := range hosts {
		if !strings.Contains(err.Error(), h) {
			t.Errorf("every blocked host must be listed, %s is missing; got: %v", h, err)
		}
	}
}

// Same property on the error path: the sweep returns the error of the FIRST
// host in allowlist order that failed without a verdict, not of whichever
// goroutine happened to fail first.
func TestSettleReportsTheFirstErrorInAllowlistOrder(t *testing.T) {
	o, _ := testOptions(t)
	key := func(host string) string { return "policy check network --sandbox api --json " + host }
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		key("a.test"): {Output: []byte(`{"allowed": true}`)},
		key("b.test"): {Err: errors.New("boom on b")},
		key("c.test"): {Err: errors.New("boom on c")},
	}}

	err := Settle(context.Background(), f, "api", []string{"a.test", "b.test", "c.test"}, o)
	if err == nil {
		t.Fatal("an sbx failing without a verdict must fail Settle")
	}
	if !strings.Contains(err.Error(), "boom on b") {
		t.Errorf("the first failure in allowlist order must be the one returned; got: %v", err)
	}
	if strings.Contains(err.Error(), "boom on c") {
		t.Errorf("only one error is returned; got: %v", err)
	}
}

// fakeFleetingHint: "passes.test" fails at the transport level while
// returning a refusal, then passes; "blocked.test" stays refused, never
// erroring.
// The mutex here and in fakePerHost is not decoration: the authoritative sweep
// probes hosts concurrently (probeAll), so a counting double is called from
// several goroutines at once and `go test -race` reports it.
type fakeFleetingHint struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeFleetingHint) Run(_ context.Context, args ...string) ([]byte, error) {
	if args[len(args)-1] != "passes.test" {
		return []byte(`{"allowed": false}`), nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		return []byte(`{"allowed": false}`), errors.New("fleeting glitch on passes.test")
	}
	return []byte(`{"allowed": true}`), nil
}

func (f *fakeFleetingHint) Attach(_ context.Context, _ ...string) error { return nil }

func (f *fakeFleetingHint) Stream(_ context.Context, _ io.Writer, _ ...string) error { return nil }

func (f *fakeFleetingHint) Pipe(_ context.Context, _ ...string) error { return nil }

// fakeCanceling cancels the context DURING the pass, then fails as a killed
// sbx would: a transport error wrapping no context reason.
type fakeCanceling struct {
	cancel context.CancelFunc
	calls  int
}

func (f *fakeCanceling) Run(_ context.Context, _ ...string) ([]byte, error) {
	f.calls++
	f.cancel()
	return nil, errors.New("signal: killed")
}

func (f *fakeCanceling) Attach(_ context.Context, _ ...string) error { return nil }

func (f *fakeCanceling) Stream(_ context.Context, _ io.Writer, _ ...string) error { return nil }

func (f *fakeCanceling) Pipe(_ context.Context, _ ...string) error { return nil }

// fakeProgressive allows the host starting from the n-th call.
type fakeProgressive struct {
	mu           sync.Mutex
	calls        int
	allowedAfter int
}

func (f *fakeProgressive) Run(_ context.Context, _ ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls >= f.allowedAfter {
		return []byte(`{"allowed": true}`), nil
	}
	return []byte(`{"allowed": false}`), nil
}

func (f *fakeProgressive) Attach(_ context.Context, _ ...string) error { return nil }

func (f *fakeProgressive) Stream(_ context.Context, _ io.Writer, _ ...string) error { return nil }

func (f *fakeProgressive) Pipe(_ context.Context, _ ...string) error { return nil }

// fakePerHost counts checks per host (the host is the argv's last argument)
// and only allows each one starting from the n-th call CONCERNING IT. A host
// absent from allowedAfter passes on the first try.
type fakePerHost struct {
	mu           sync.Mutex
	calls        map[string]int
	allowedAfter map[string]int
}

func (f *fakePerHost) Run(_ context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("empty argv")
	}
	host := args[len(args)-1]
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = make(map[string]int)
	}
	f.calls[host]++
	if f.calls[host] >= f.allowedAfter[host] {
		return []byte(`{"allowed": true}`), nil
	}
	return []byte(`{"allowed": false}`), nil
}

func (f *fakePerHost) Attach(_ context.Context, _ ...string) error { return nil }

func (f *fakePerHost) Stream(_ context.Context, _ io.Writer, _ ...string) error { return nil }

func (f *fakePerHost) Pipe(_ context.Context, _ ...string) error { return nil }
