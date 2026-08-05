package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/PillowPillow/den/internal/sbx"
)

// KitLogPath is the sbx kit dispatcher's journal INSIDE the sandbox.
//
// Surveyed 2026-07-31 against sbx v0.35.0, and machine-readable — which is the
// only reason the §9.1 gate can be observed at all. One run writes:
//
//	=== dispatcher run 2026-07-31T15:34:24Z ===
//	> /etc/durable-startup.d/001-startup-claude/000-cmd.sh
//	ok /etc/durable-startup.d/001-startup-claude/000-cmd.sh
//	> /etc/durable-startup.d/002-startup-den-alpha/000-cmd.sh
//	agent claude: up to date
//	ok /etc/durable-startup.d/002-startup-den-alpha/000-cmd.sh
//	=== dispatcher complete ===
//
// A `>` line per command, then `ok <path>` or `fail <path> exit=<n>`, with the
// command's own output in between. The file ACCUMULATES: a restart appends a
// new `=== dispatcher run … ===` block (measured — the dispatcher really does
// re-run when a stopped sandbox is restarted, which smoke #2 left open), so
// only the LAST block describes the sandbox as it is now.
const KitLogPath = "/var/log/sbx-kit-startup.log"

// runMarker and completeMarker delimit one dispatcher run in KitLogPath.
const (
	runMarker      = "=== dispatcher run "
	completeMarker = "=== dispatcher complete ==="
)

// MixinName is the kit `name:` den generates for a sandbox — and, because the
// dispatcher names each kit's directory `<NNN>-startup-<kit name>`, the string
// that identifies den's own lines in KitLogPath.
//
// A kit name cannot carry the sandbox name separator, hence the `.` → `-`
// flattening: sandbox `api.feat12` becomes kit `den-api-feat12`, directory
// `002-startup-den-api-feat12`.
//
// The numeric prefix is NOT part of it, deliberately. `002-` is what sbx
// happened to assign on every observation, and it is nowhere documented:
// matching on it would tie den to a layering position it does not choose.
func MixinName(sandboxName string) string {
	return "den-" + strings.ReplaceAll(sandboxName, ".", "-")
}

// GateState is what KitLogPath says about den's freshness command in the last
// dispatcher run.
type GateState int

const (
	// GatePending: the run has started and has not reported on den's mixin
	// yet. The ordinary state for the first ~35 s after a spawn.
	GatePending GateState = iota
	// GatePassed: `ok <path>` — the agent was updated, §9.1 is satisfied.
	GatePassed
	// GateFailed: `fail <path> exit=<n>` on den's own command, or an EARLIER
	// kit failing and aborting the run before den's ever ran. The two are one
	// state on purpose: the dispatcher does `exit $rc` at the first failure
	// (§14.0), so an earlier kit's failure means the freshness command did not
	// run — and an agent that was never updated is exactly what §9.1 forbids
	// starting with. Reason distinguishes them for the reader.
	GateFailed
	// GateAbsent: the run COMPLETED without ever naming den's mixin. Not a
	// stale agent — a sandbox that carries no den mixin at all, which is what a
	// VM created by an older den looks like. Worth saying, never worth
	// refusing over: nothing about it is fixable by waiting.
	GateAbsent
)

// GateVerdict is a reading of KitLogPath: the state, the log line that decided
// it, and — for anything but a pass — why.
//
// Line is carried because the diagnosis §9.1 promises is IN the log ("it is
// precisely the `fail … exit=127` of the journal that made the 2026-07-27 bug
// diagnosable"), and a message that says "the gate failed" without it sends the
// user back into the VM to find out what den already read.
type GateVerdict struct {
	State  GateState
	Line   string
	Reason string
}

// ParseKitLog reads the dispatcher journal and answers what its LAST run says
// about the kit named mixinName.
//
// The last run only: the file accumulates one block per boot, and a `fail` from
// three restarts ago describes a sandbox that no longer exists in that state.
// A log with no run marker at all is read whole — the block baked into the
// image carries no marker of its own in every sample observed, and refusing to
// read it would turn a known shape into an error.
func ParseKitLog(log []byte, mixinName string) GateVerdict {
	lines := strings.Split(string(log), "\n")
	if start := lastRunStart(lines); start >= 0 {
		lines = lines[start:]
	}

	suffix := "startup-" + mixinName
	completed := false
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == completeMarker {
			completed = true
			continue
		}
		path, failed, ok := verdictLine(line)
		if !ok {
			continue
		}
		if !ownsPath(path, suffix) {
			// SOMEONE ELSE's failure, and it is den's problem all the same: the
			// dispatcher aborts the whole run at the first non-zero command, so
			// den's mixin — layered last precisely to be the only one that may
			// fail — never ran at all.
			if failed {
				return GateVerdict{
					State: GateFailed,
					Line:  line,
					Reason: fmt.Sprintf(
						"an earlier kit failed and the dispatcher aborted the run, so den's freshness "+
							"command (%s) never ran — the agent was NOT updated", mixinName),
				}
			}
			continue
		}
		if failed {
			return GateVerdict{
				State:  GateFailed,
				Line:   line,
				Reason: "den's freshness command exited non-zero: the agent was NOT updated",
			}
		}
		return GateVerdict{State: GatePassed, Line: line}
	}

	if completed {
		return GateVerdict{
			State: GateAbsent,
			Reason: fmt.Sprintf(
				"the dispatcher run completed without ever running %s — this sandbox carries no den "+
					"mixin, which is what a VM created by an older den looks like", mixinName),
		}
	}
	return GateVerdict{State: GatePending}
}

// lastRunStart returns the index of the last `=== dispatcher run … ===` line,
// or -1 when the log carries none.
func lastRunStart(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], runMarker) {
			return i
		}
	}
	return -1
}

// verdictLine reads one line as a dispatcher verdict: `ok <path>` or
// `fail <path> exit=<n>`. Anything else — the `> <path>` announcements, and the
// commands' own output, which is arbitrary user text — is not a verdict.
//
// The exit code is deliberately NOT parsed out: it is already in the line, the
// line travels whole to the user, and a second rendering of it would be one
// more thing to keep in step with sbx.
func verdictLine(line string) (path string, failed, ok bool) {
	if rest, found := strings.CutPrefix(line, "ok "); found {
		return strings.TrimSpace(rest), false, true
	}
	if rest, found := strings.CutPrefix(line, "fail "); found {
		path, _, _ = strings.Cut(strings.TrimSpace(rest), " ")
		return path, true, true
	}
	return "", false, false
}

// ownsPath reports whether a dispatcher path belongs to the kit directory whose
// name ends in suffix (`startup-den-<sandbox>`).
//
// The comparison is on a WHOLE path segment, never on containment: a kit named
// `den-api` must not claim the lines of `002-startup-den-api-feat12`, and a
// substring test would.
func ownsPath(path, suffix string) bool {
	for _, segment := range strings.Split(path, "/") {
		if strings.HasSuffix(segment, suffix) {
			return true
		}
	}
	return false
}

// GateOptions parametrizes the wait, on the model of policy.Options: Sleep and
// Now are injected so the suite never actually waits, and nothing has an
// implicit default — incomplete options are refused rather than quietly
// completed.
type GateOptions struct {
	Timeout  time.Duration
	Interval time.Duration
	Sleep    func(time.Duration)
	Now      func() time.Time
}

// DefaultGateOptions returns 90 s of patience, polling every 3 s.
//
// The budget is sized on the MEASURED shape of the thing waited for, not on a
// round number: the freshness command completed about 35 s after `sbx create`
// returned on the success path, and its failure path is bounded by §9.1's own
// three attempts spaced 10 s. 90 s clears both with margin. The interval is 3 s
// because each poll is an `sbx exec … cat`, a process spawn: polling faster
// would cost more than it saves on a wait measured in tens of seconds.
func DefaultGateOptions() GateOptions {
	return GateOptions{
		Timeout:  90 * time.Second,
		Interval: 3 * time.Second,
		Sleep:    time.Sleep,
		Now:      time.Now,
	}
}

// validate refuses incomplete options instead of filling the gaps — the reason
// policy.Options.validate gives, and it applies identically here: a zero
// Timeout leaves the gate with no patience at all and turns every spawn into
// "has not reported yet", which reads as den checking when it is not.
func (o GateOptions) validate() error {
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
			"unusable agent-freshness gate options: %s — build them from agent.DefaultGateOptions() "+
				"and only override what needs to be", strings.Join(invalid, ", "))
	}
	if o.Interval > o.Timeout {
		return fmt.Errorf(
			"unusable agent-freshness gate options: Interval (%s) exceeds Timeout (%s) — the gate "+
				"would sleep longer than the patience it promises; build them from "+
				"agent.DefaultGateOptions()", o.Interval, o.Timeout)
	}
	return nil
}

// maxRounds is policy.Options.maxRounds' twin, and exists for its reason: the
// clock is injected, so the loop's only temporal bound is the caller's good
// faith, and a clock double that never advances would hang `go test ./...` with
// nothing pointing here. Set to exactly what an honest clock produces, so it
// never fires before the timeout does.
func (o GateOptions) maxRounds() int {
	rounds := o.Timeout / o.Interval
	if o.Timeout%o.Interval != 0 {
		rounds++
	}
	return int(rounds) + 1
}

// ReadFreshness reads KitLogPath ONCE and answers what it says about this
// sandbox. It is the whole of the gate for a caller that will not stand and
// wait — `den spawn --detach`, where nobody is at a prompt — and a caller that
// will still needs a verdict already in the journal: a re-attach onto a sandbox
// whose gate failed an hour ago is exactly a log that carries one.
//
// A single read is written as a function of its own rather than as a wait with
// its budget collapsed to nothing. A one-nanosecond timeout reads as "check
// with no patience", loops one extra round against an injected clock, and makes
// the difference between "did not wait" and "waited badly" impossible to assert.
func ReadFreshness(ctx context.Context, r sbx.Runner, sandbox string) (GateVerdict, error) {
	output, err := r.Run(ctx, "exec", sandbox, "cat", KitLogPath)
	if err != nil {
		return GateVerdict{}, fmt.Errorf(
			"reading the agent-freshness journal of sandbox %s (%s): %w — §9.1 makes the agent "+
				"update fail-closed, and den cannot tell whether it passed without this file",
			sandbox, KitLogPath, err)
	}
	return ParseKitLog(output, MixinName(sandbox)), nil
}

// WaitFreshness polls KitLogPath until the §9.1 gate has reported on this
// sandbox, or the budget runs out.
//
// It returns the last verdict READ, and an error only when the log could not be
// read at all. A budget that runs out with the gate still silent is NOT an
// error: den waited what it promised to wait, and a dispatcher that is still
// working says nothing about the agent being stale. The caller decides what to
// do with GatePending — spawn warns, because refusing there would turn a slow
// machine into a broken one.
//
// The read is `sbx exec <sandbox> cat <KitLogPath>`, which RESTARTS a stopped
// sandbox. That is why the caller must not reach here for a sandbox it has
// decided to leave stopped: waking a VM to inspect it contradicts the decision
// `den spawn --detach` makes about the very same VM (internal/cli/ports.go,
// wakeForPorts, states the rule both obey).
func WaitFreshness(ctx context.Context, r sbx.Runner, sandbox string, o GateOptions,
	onWait func()) (GateVerdict, error) {
	if err := o.validate(); err != nil {
		return GateVerdict{}, err
	}
	deadline := o.Now().Add(o.Timeout)
	announced := false

	for round := 0; round < o.maxRounds(); round++ {
		verdict, err := ReadFreshness(ctx, r, sandbox)
		if err != nil {
			return GateVerdict{}, err
		}
		if verdict.State != GatePending {
			return verdict, nil
		}
		if !o.Now().Before(deadline) {
			return verdict, nil
		}
		// Announced ONCE, and only when den is actually going to wait: the
		// ordinary case is a gate that has already reported (a re-attach, a
		// sandbox spawned a minute ago), and a "waiting for…" line printed
		// before every read would appear on spawns that wait for nothing.
		if !announced && onWait != nil {
			onWait()
			announced = true
		}
		o.Sleep(o.Interval)
	}
	// The round guard fired: the clock is not advancing. Reported as pending
	// rather than as an error, for the reason above — den has no evidence about
	// the agent, and inventing one in either direction would be worse.
	return GateVerdict{State: GatePending}, nil
}
