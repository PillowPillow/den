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
// command's own output in between — stdout AND stderr, measured on both den
// commands' `FATAL` lines (2026-07-31 and 2026-08-07). The file ACCUMULATES: a
// restart appends a new `=== dispatcher run … ===` block (measured — the
// dispatcher really does re-run when a stopped sandbox is restarted, which
// smoke #2 left open), so only the LAST block describes the sandbox as it is
// now.
//
// ONE KIT DIRECTORY, SEVERAL SCRIPTS. Since `mounts:`, den's own directory
// holds two — the link phase then the freshness command — and the dispatcher
// numbers them within it (measured 2026-08-07, sbx v0.37.1):
//
//	> /etc/durable-startup.d/002-startup-den-smoke/000-cmd.sh
//	den mounts: /home/agent/.linkme -> /tmp/den-mount-smoke/linkme
//	ok /etc/durable-startup.d/002-startup-den-smoke/000-cmd.sh
//	> /etc/durable-startup.d/002-startup-den-smoke/001-cmd.sh
//	agent claude: up to date
//	ok /etc/durable-startup.d/002-startup-den-smoke/001-cmd.sh
//
// This is what ParseKitLog reads, and getting it wrong once already cost the
// return of defect #18 — see the rule stated there.
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
// it, and — for anything but a pass — why, plus what to fix.
//
// Line is carried because the diagnosis §9.1 promises is IN the log ("it is
// precisely the `fail … exit=127` of the journal that made the 2026-07-27 bug
// diagnosable"), and a message that says "the gate failed" without it sends the
// user back into the VM to find out what den already read.
//
// Remedy is separate from Reason because den's kit now runs TWO commands, and
// they are fixed in different files: a failed agent update is fixed in the
// agent registry, a refused link phase in `mounts:` / `ssh.dir`. The caller
// renders it verbatim, so a hardcoded "fix the agent's `update:`" in the
// refusal sentence would name the wrong file half the time — which is exactly
// what it did.
type GateVerdict struct {
	State  GateState
	Line   string
	Reason string
	Remedy string
}

// ParseKitLog reads the dispatcher journal and answers what its LAST run says
// about the kit named mixinName.
//
// The last run only: the file accumulates one block per boot, and a `fail` from
// three restarts ago describes a sandbox that no longer exists in that state.
// A log with no run marker at all is read whole — the block baked into the
// image carries no marker of its own in every sample observed, and refusing to
// read it would turn a known shape into an error.
//
// # WHY THIS READS THE WHOLE RUN INSTEAD OF RETURNING ON THE FIRST VERDICT
//
// den's kit DIRECTORY may hold several scripts. Since `mounts:` it holds two —
// the link phase `000-cmd.sh` and the freshness command `001-cmd.sh` — and
// ownsPath matches on the directory segment, so BOTH are "owned". Returning on
// the first owned verdict therefore let the link phase's `ok` decide the gate
// before freshness was ever read: a sandbox whose agent update FAILED was
// reported as passing and attached to, exit 0, in silence. That is defect #18,
// which this repo had already fixed once.
//
// The verdict is decided from the JOURNAL ALONE — never from how many entries
// den believes it emitted. Threading that count in was the other candidate and
// it is worse: `checkFreshness` runs on the create branch AND the attach
// branch, so the count would have to come from the current mixin on one and
// from the on-disk mixin on the other, and getting that wrong makes every
// attach to a sandbox created by an older den a hard 90-second timeout. The
// journal is the same shape on both branches and needs no such arbitration.
//
// The rule, over OWNED lines of the last run:
//
//   - any owned `fail` → GateFailed, naming the phase that failed;
//   - the run COMPLETED and every owned `> <path>` announcement has a matching
//     owned `ok` → GatePassed;
//   - no owned line at all in a completed run → GateAbsent;
//   - anything else → GatePending.
//
// completeMarker is required for the pass, and that is the non-obvious half.
// Without it, a journal caught in the instant between `ok …/000-cmd.sh` and the
// `> …/001-cmd.sh` that follows it satisfies "every announcement matched" while
// the freshness command has not started — the same silent pass, through a
// narrower window. The cost of requiring it is a GatePending that resolves on
// the next poll, which is what GatePending is for.
func ParseKitLog(log []byte, mixinName string) GateVerdict {
	lines := strings.Split(string(log), "\n")
	if start := lastRunStart(lines); start >= 0 {
		lines = lines[start:]
	}

	suffix := "startup-" + mixinName
	completed := false
	announced := map[string]bool{}
	passed := map[string]bool{}
	// lastOK is the line CARRIED by a pass: the last owned verdict read, which
	// is the freshness command's — the one that actually decided.
	lastOK := ""
	// inOwned is the owned entry whose output is being read, and linkLine the
	// last LinkPhaseMarker line printed inside it. Together they answer "was
	// this the link phase?" without asking the mixin anything.
	inOwned, linkLine := "", ""

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == completeMarker {
			completed = true
			continue
		}
		if rest, found := strings.CutPrefix(line, "> "); found {
			path := strings.TrimSpace(rest)
			inOwned, linkLine = "", ""
			if ownsPath(path, suffix) {
				announced[path] = true
				inOwned = path
			}
			continue
		}
		path, failed, ok := verdictLine(line)
		if !ok {
			// A command's own output. The only thing read out of it is den's
			// own link-phase marker, and only inside an owned entry.
			if inOwned != "" && strings.HasPrefix(line, LinkPhaseMarker) {
				linkLine = line
			}
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
					Remedy: "Fix the failing kit named in the line above",
				}
			}
			inOwned, linkLine = "", ""
			continue
		}
		if failed {
			return failedEntry(line, linkLine)
		}
		passed[path] = true
		lastOK = line
		inOwned, linkLine = "", ""
	}

	// GateAbsent is decided BEFORE the pass, and the guard is not decoration:
	// "every owned announcement has a matching ok" is vacuously true when there
	// are none, which would turn a sandbox carrying no den mixin at all into a
	// pass.
	if len(announced) == 0 && len(passed) == 0 {
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
	if completed && allAnnouncedPassed(announced, passed) {
		return GateVerdict{State: GatePassed, Line: lastOK}
	}
	return GateVerdict{State: GatePending}
}

// allAnnouncedPassed reports whether every owned `> <path>` announcement of the
// run has a matching owned `ok <path>`.
func allAnnouncedPassed(announced, passed map[string]bool) bool {
	for path := range announced {
		if !passed[path] {
			return false
		}
	}
	return true
}

// failedEntry builds the verdict for an owned entry that exited non-zero.
//
// linkLine is the last LinkPhaseMarker line the entry printed, or "" — and it
// is the whole discriminator. A refused link phase aborts the run before the
// freshness command is ever announced (measured 2026-08-07), so the failing
// script is the LAST owned entry in the journal either way, and position says
// nothing. What the link phase does say is its own name, on every branch it can
// exit through.
//
// Getting this wrong is not cosmetic: reporting a link refusal as a failed
// agent update sends the user to the agent registry when the fix is one line of
// `mounts:` — and the journal already told den which it was.
func failedEntry(line, linkLine string) GateVerdict {
	if linkLine != "" {
		return GateVerdict{
			State: GateFailed,
			Line:  line,
			Reason: "den's link phase refused, so the freshness command never ran and the agent was " +
				"NOT updated: " + strings.TrimSpace(linkLine),
			Remedy: "Fix the `mounts:` entry (or `ssh.dir`) named in that line, in den's global " +
				"config.yaml",
		}
	}
	return GateVerdict{
		State:  GateFailed,
		Line:   line,
		Reason: "den's freshness command exited non-zero: the agent was NOT updated",
		Remedy: "Fix the agent's `update:` command in the registry",
	}
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
// commands' own output, which is arbitrary user text — is not a verdict. The
// announcements are read by ParseKitLog itself, before this is called: they are
// what tells an entry that has not answered from one that was never started.
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
			"reading the agent-freshness journal of sandbox %s (%s): %w — the agent "+
				"update is fail-closed, and den cannot tell whether it passed without this file",
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
