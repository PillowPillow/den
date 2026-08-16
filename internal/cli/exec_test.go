package cli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sshagent"
)

func TestExecAttachesInTheWorkdir(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api:ro","/profile"]}]}`)},
	}}

	if _, err := executeCmdWithSbx(t, f, "exec", "api", "true"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var attach []string
	for _, a := range f.Calls {
		if len(a) > 0 && a[0] == "exec" {
			attach = a
		}
	}
	if attach == nil {
		t.Fatalf("no attach; calls: %v", f.Calls)
	}
	if !slices.Contains(attach, "-w") || !slices.Contains(attach, "/w/api") {
		t.Errorf("the attach must set the workdir to the first workspace; got: %v", attach)
	}
	if !slices.Contains(attach, "true") {
		t.Errorf("the attach must carry the command; got: %v", attach)
	}
}

// `sbx run` would launch the image's flavor (often claude): never.
//
// The fixture's `"status":"running"` is not decorative: `den exec` now refuses
// any sandbox whose status is not explicitly "running" (see
// TestExecRefusesASandboxThatIsNotRunning), and a fixture without `status` would
// no longer even reach the attach.
func TestExecNeverUsesSbxRun(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w"]}]}`)},
	}}

	if _, err := executeCmdWithSbx(t, f, "exec", "api", "true"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.HasCalled("run") {
		t.Errorf("den exec must never go through `sbx run`; calls: %v", f.Calls)
	}
}

// An unknown name must list what is running: "not found" alone would force
// the user to run another command just to know what to type.
func TestExecUnknownName(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[{"name":"api"},{"name":"web"}]}`)},
	}}

	_, err := executeCmdWithSbx(t, f, "exec", "missing", "true")
	if err == nil {
		t.Fatal("an unknown sandbox name must produce an error")
	}
	for _, expected := range []string{"missing", "api", "web"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message must contain %q; got: %v", expected, err)
		}
	}
	if len(f.Attaches) != 0 {
		t.Errorf("an unknown name must attach nowhere; attaches: %v", f.Attaches)
	}
}

// F2, on the OTHER path: `den exec` must resume a stopped sandbox, like
// `den spawn`. Proven HERE and not only in internal/spawn — nothing at the
// level of sbx.CheckAttachable guarantees newExecCmd calls it, and a policy
// widened on only one side would reopen the defect on the other.
//
// The probe is INJECTED, as on every test below that asserts an argv: with a
// command present the tty is the probe's verdict, so which method carries the
// call — Attach or Pipe — would otherwise be a property of the harness the
// suite runs under. False, hence Pipe.
func TestExecResumesAStoppedSandbox(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "exec", "api", "true"); err != nil {
		t.Fatalf("a stopped sandbox must be resumed: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/w/api", "api", "true") {
		t.Errorf("resuming must run the command in the VM's workdir; pipes: %v", f.Pipes)
	}
}

// The same guard as on `den spawn`, on the OTHER path: both end in an
// `sbx exec`, and both are wrong on a VM den knows nothing about. A `den exec`
// that opens a shell in an `exited` sandbox is no less wrong than a
// `den spawn` that does — and it is the very same defect, not a cousin.
func TestExecRefusesASandboxThatIsNotRunning(t *testing.T) {
	for _, status := range []string{"exited", "paused", "Running", ""} {
		t.Run("status="+status, func(t *testing.T) {
			f := &sbx.Fake{Responses: map[string]sbx.Response{
				"ls --json": {Output: []byte(
					`{"sandboxes":[{"name":"api","status":"` + status + `","workspaces":["/w/api"]}]}`)},
			}}

			_, err := executeCmdWithSbx(t, f, "exec", "api", "true")
			if err == nil {
				t.Fatalf("status %q must not lead to an attach", status)
			}
			// strconv.Quote, not the bare status: on the status="" subcase,
			// `strings.Contains(err, "")` is trivially true and asserts
			// nothing. The quoted form is what the message renders (`%q`).
			if !strings.Contains(err.Error(), strconv.Quote(status)) ||
				!strings.Contains(err.Error(), strconv.Quote("running")) {
				t.Errorf("the message must render both the read status and the expected one; got: %v", err)
			}
			if len(f.Attaches) != 0 {
				t.Errorf("no attach in a stopped VM; attaches: %v", f.Attaches)
			}
		})
	}
}

// execGateLog renders a dispatcher journal whose LAST run reports on den's own
// mixin for this sandbox. `verdict` is the whole verdict line's leading token —
// `ok` or `fail … exit=1` — because what agent.ParseKitLog reads is that token
// and the path that follows it.
//
// The path carries the `002-` prefix sbx assigns, which agent.MixinName
// deliberately does NOT match on: writing the realistic form here is what keeps
// this fixture a sample of the measured journal rather than a restatement of
// the parser.
//
// A passing run ends with `=== dispatcher complete ===` and a failing one does
// not — the dispatcher does `exit $rc` at the first non-zero command (§14.0), so
// the marker is never written. The fixture used to omit it in both cases, which
// was a divergence from the measured journal that only became visible when
// agent.ParseKitLog started requiring it to declare a pass.
func execGateLog(sandbox, verdict string) []byte {
	path := "/etc/durable-startup.d/002-startup-den-" + strings.ReplaceAll(sandbox, ".", "-") + "/000-cmd.sh"
	log := "=== dispatcher run 2026-08-03T10:00:00Z ===\n" +
		"> " + path + "\n" +
		verdict + " " + path + "\n"
	if verdict == "ok" {
		log += "=== dispatcher complete ===\n"
	}
	return []byte(log)
}

// execGateRead is the key of the ONE `sbx exec` agent.ReadFreshness makes.
func execGateRead(sandbox string) string {
	return "exec " + sandbox + " cat /var/log/sbx-kit-startup.log"
}

// #18's hole, entered through the other door. `den spawn` holds the §9.1 gate
// since PR #26 — it refuses a sandbox whose agent den KNOWS was not updated —
// but `den exec` does not go through spawn.Spawn at all, and on the bench the
// very same sandbox that `den spawn` refused handed out a shell in silence.
//
// A guarantee held by one door out of two is more misleading than no guarantee:
// §9.1 says "a sandbox never starts with a stale agent", and `den exec` on a
// STOPPED sandbox starts one.
func TestExecRefusesASandboxWhoseFreshnessGateFailed(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
		execGateRead("api"): {Output: execGateLog("api", "fail")},
	}}

	_, err := executeCmdWithSbx(t, f, "exec", "api", "true")
	if err == nil {
		t.Fatal("a failed §9.1 gate must not lead to a shell")
	}
	for _, want := range []string{"api", "agent-freshness gate", "/var/log/sbx-kit-startup.log"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must contain %q; got: %v", want, err)
		}
	}
	if len(f.Attaches) != 0 {
		t.Errorf("a refused gate must attach nowhere; attaches: %v", f.Attaches)
	}
}

// The case issue #27 names as the real one: `den exec` on a STOPPED sandbox
// STARTS it, and the gate must hold there too rather than be skipped for a VM
// den is about to boot.
//
// The fixture is TWO blocks — a failed run, then the one the restart appended,
// failing again, which is what a deterministically broken freshness command
// does on every boot. It pins that the stopped branch REFUSES; it does not by
// itself prove den waited, since a single read of the last block would refuse
// this fixture too. TestExecPollsRatherThanReadsOnceWhenItStartsAStoppedSandbox
// below is the one that discriminates, and it exists because the two are
// otherwise indistinguishable — which is exactly how the hole got in.
func TestExecWaitsForTheGateWhenItStartsAStoppedSandbox(t *testing.T) {
	log := append(execGateLog("api", "fail"), execGateLog("api", "fail")...)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w/api"]}]}`)},
		execGateRead("api"): {Output: log},
	}}

	_, err := executeCmdWithSbx(t, f, "exec", "api", "true")
	if err == nil {
		t.Fatal("starting a stopped sandbox whose gate failed must not lead to a shell")
	}
	if !strings.Contains(err.Error(), "agent-freshness gate") {
		t.Errorf("the refusal must be the freshness-gate one; got: %v", err)
	}
	if len(f.Attaches) != 0 {
		t.Errorf("a refused gate must attach nowhere; attaches: %v", f.Attaches)
	}
}

// ...and the wait really is a WAIT, not a single read wearing its name.
//
// This is the property that closes the hole. A restart makes the dispatcher
// RE-RUN (measured, agent.KitLogPath) and ParseKitLog reads only the LAST
// block, so right after `den exec` wakes a stopped sandbox the fresh block has
// begun and reported nothing: a lone read answers GatePending, prints a note,
// and opens a shell while the agent is mid-update — #18's silence rebuilt
// inside the fix for #27. Polling is what lets the verdict arrive.
//
// The fixture stays pending forever because sbx.Fake answers every call with
// the same bytes; a journal that fills in mid-wait is what the real dispatcher
// does and what smoke #3 measures. What is assertable here is the SHAPE of the
// wait — more than one read, announced, and a budget that runs out is a note
// rather than a refusal.
//
// The clock is the injected one (fakeGateOptions), so the rounds happen
// instantly. The announcement is read on STDERR: den's own chatter follows the
// tty (exec.go), and this test injects a probe answering false, so the line
// lands where a piping caller can still see it.
func TestExecPollsRatherThanReadsOnceWhenItStartsAStoppedSandbox(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w/api"]}]}`)},
		// A run that has begun and reported nothing: GatePending, forever.
		execGateRead("api"): {Output: []byte("=== dispatcher run 2026-08-03T10:00:00Z ===\n")},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	_, stderr, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "exec", "api", "true")
	if err != nil {
		t.Fatalf("a budget that runs out is a note, never a refusal: %v", err)
	}
	reads := 0
	for _, c := range f.Calls {
		if strings.Join(c, " ") == execGateRead("api") {
			reads++
		}
	}
	if reads < 2 {
		t.Errorf("the stopped branch must POLL the journal; %d read(s), calls: %v", reads, f.Calls)
	}
	if !strings.Contains(stderr, "waiting for agent freshness") {
		t.Errorf("a wait den actually performs must be announced; got: %q", stderr)
	}
	if !f.HasPiped("exec", "-w", "/w/api", "api", "true") {
		t.Errorf("an exhausted budget must still run the command; pipes: %v", f.Pipes)
	}
}

// The other half: a gate that PASSED costs the user nothing — no line, and the
// command they asked for. Asserted together with the refusal above, because a
// `den exec` that refused everything would satisfy that test alone.
//
// Both streams are checked for silence, not stdout alone: den's chatter follows
// the tty, so a line the injected no-tty probe moves to stderr would slip past
// an assertion that only reads stdout.
func TestExecRunsAndStaysSilentWhenTheFreshnessGatePassed(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
		execGateRead("api"): {Output: execGateLog("api", "ok")},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	stdout, stderr, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "exec", "api", "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/w/api", "api", "true") {
		t.Errorf("a passing gate must not cost the command; pipes: %v", f.Pipes)
	}
	if strings.Contains(stdout+stderr, "freshness") {
		t.Errorf("a passing gate is the ordinary outcome and says nothing; got: %q / %q", stdout, stderr)
	}
}

// The gate is read BEFORE the attach, not alongside it. Read after, its refusal
// would arrive behind a shell that already owns the terminal — which is the
// exact defect the ordering of warnEmptyAgentOnReentry already avoids.
//
// The attach is found by the COMMAND it carries, not by `-it`: with a command
// and an injected no-tty probe there is no `-it` in any argv, and the old
// discriminator would report "no attach at all" rather than a wrong order.
func TestExecReadsTheFreshnessGateBeforeAttaching(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
		execGateRead("api"): {Output: execGateLog("api", "ok")},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "exec", "api", "true"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	read, attach := -1, -1
	for i, c := range f.Calls {
		if strings.Join(c, " ") == execGateRead("api") {
			read = i
		}
		if slices.Contains(c, "true") {
			attach = i
		}
	}
	if read < 0 {
		t.Fatalf("den exec must read the §9.1 journal; calls: %v", f.Calls)
	}
	if attach < 0 || read > attach {
		t.Errorf("the journal must be read before the attach; read=%d attach=%d, calls: %v",
			read, attach, f.Calls)
	}
}

// runExecWithAgent runs `den exec` through the REAL command tree — NewRootCmdWith,
// not a hand-built exec command — with an injected sbx.Runner and SSH probe, on a
// given den home.
//
// The full tree on purpose: everything the empty-agent warning needs on this
// path is wiring (the den home reaching exec.go, deps.SSHAgent reaching it too,
// the warning landing on the command's stderr), and a helper that called
// newExecCmd directly would prove none of it.
//
// Separate streams, for the property the warning is judged on: a diagnostic
// must not land in the stdout a caller pipes.
func runExecWithAgent(t *testing.T, denHome string, r sbx.Runner,
	probe func() sshagent.Result, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: r, SSHAgent: probe}
	return executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		append([]string{"--den-home", denHome}, args...)...)
}

// execSocket puts a forwarded socket in den's environment, like spawn's
// forwardedSocket: without it every test below takes the absent-socket branch
// and never reaches the probe. Set explicitly rather than inherited — left to
// the machine, these tests would pass on a workstation running an agent and
// change verdict on a bare CI runner.
func execSocket(t *testing.T) {
	t.Helper()
	t.Setenv("SSH_AUTH_SOCK", "/tmp/den-test/agent.sock")
}

// execDenHome writes the smallest den home `den exec` can read a mode out of. The
// ssh block is the caller's, because the mode is what these tests vary; empty
// means the config's default, agent-forward.
func execDenHome(t *testing.T, sshBlock string) string {
	t.Helper()
	dir := t.TempDir()
	writeConfig(t, dir, minimalConfig+sshBlock)
	return dir
}

// A sandbox is created once and re-entered daily, most often with `den exec` —
// and `den exec` said nothing about an empty forwarded agent, on any OS, while
// `den spawn` warned on both its branches. The forwarded socket being a live
// proxy, an agent emptied since the VM booted denies `git push` inside it just
// as silently on re-entry: same machine state, same consequence, same warning.
//
// The attach is asserted too: the warning must not have replaced the shell the
// user asked for.
func TestExecWarnsWhenTheForwardedAgentIsEmpty(t *testing.T) {
	execSocket(t)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}

	stdout, stderr, err := runExecWithAgent(t, execDenHome(t, ""), f,
		func() sshagent.Result { return sshagent.Result{State: sshagent.StateEmpty} },
		"exec", "api", "true")
	if err != nil {
		t.Fatalf("an empty agent must warn, not block: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/w/api", "api", "true") {
		t.Fatalf("the command must still run; pipes: %v", f.Pipes)
	}
	for _, want := range []string{"warning", "no identity", "publickey", "ssh-add"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, must contain %q", stderr, want)
		}
	}
	if strings.Contains(stdout, "ssh-add") {
		t.Errorf("stdout = %q, the warning belongs on stderr only", stdout)
	}
}

// The counterpart without which a warning wired unconditionally would pass the
// test above: an agent holding keys is the healthy case and says nothing.
func TestExecDoesNotWarnWhenTheForwardedAgentHasKeys(t *testing.T) {
	execSocket(t)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}

	_, stderr, err := runExecWithAgent(t, execDenHome(t, ""), f,
		func() sshagent.Result { return sshagent.Result{State: sshagent.StateKeys, Identities: 2} },
		"exec", "api", "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stderr, "warning") {
		t.Errorf("an agent with keys must be silent; stderr: %q", stderr)
	}
}

// Modes `mount` and `none` forward no agent, so its state is irrelevant and no
// probe must fire — the same rule as the spawn path, on the command that had to
// read the mode to obey it.
func TestExecDoesNotProbeTheAgentOutsideAgentForward(t *testing.T) {
	for _, sshBlock := range []string{"ssh:\n  mode: none\n", "ssh:\n  mode: mount\n  dir: /tmp/den-test/ssh\n"} {
		t.Run(sshBlock, func(t *testing.T) {
			execSocket(t)
			f := &sbx.Fake{Responses: map[string]sbx.Response{
				"ls --json": {Output: []byte(
					`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
			}}
			probed := false

			_, stderr, err := runExecWithAgent(t, execDenHome(t, sshBlock), f, func() sshagent.Result {
				probed = true
				return sshagent.Result{State: sshagent.StateEmpty}
			}, "exec", "api", "true")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if probed {
				t.Error("the agent must not be probed outside agent-forward")
			}
			if strings.Contains(stderr, "warning") {
				t.Errorf("stderr = %q, no SSH warning expected outside agent-forward", stderr)
			}
			// Without this, "no probe" would also be satisfied by a `den exec` that
			// gave up before reaching it: the run is what makes the silence a
			// decision about the mode.
			if !f.HasPiped("exec", "-w", "/w/api", "api", "true") {
				t.Errorf("the command must have run; pipes: %v", f.Pipes)
			}
		})
	}
}

// An absent SSH_AUTH_SOCK is where re-entry deliberately parts from the
// preflight: the socket a LIVE sandbox forwards was inherited at its
// `sbx create`, from an environment that may be long gone, so this shell's lack
// of one says nothing about the VM — and `den exec`, which creates nothing, has no
// "relaunch den to forward it" to offer. Silence, and no probe: `ssh-add -l`
// without a socket answers StateUnreachable, which would blame a variable the
// user never set.
func TestExecDoesNotWarnWhenTheSSHSocketIsAbsent(t *testing.T) {
	// Set EMPTY rather than left alone: os.Getenv answers "" for both, and a test
	// relying on the machine having no agent would quietly stop exercising this.
	t.Setenv("SSH_AUTH_SOCK", "")
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	probed := false

	_, stderr, err := runExecWithAgent(t, execDenHome(t, ""), f, func() sshagent.Result {
		probed = true
		return sshagent.Result{State: sshagent.StateUnreachable}
	}, "exec", "api", "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if probed {
		t.Error("the agent was probed with no socket to point it at")
	}
	if strings.Contains(stderr, "warning") {
		t.Errorf("stderr = %q, den exec must stay silent about a socket it cannot judge", stderr)
	}
}

// No live sandbox at all: the message cannot offer a list, it must SAY so.
// "(live: [])" would send the user looking for a typo in an empty list.
func TestExecWithNoSandboxAtAll(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}

	_, err := executeCmdWithSbx(t, f, "exec", "missing", "true")
	if err == nil {
		t.Fatal("an unknown sandbox name must produce an error")
	}
	if !strings.Contains(err.Error(), "no sandbox is running") {
		t.Errorf("the message must say no sandbox is running; got: %v", err)
	}
}

// A source reference names a sandbox that was never spawned under its
// prefixed name: ":" is not in sbx's charset, so spawn (spawn.go) names the
// live VM with the FLATTENED reference, "corp-api". `den exec corp:api` must
// reach that same sandbox — `den exec` never reads a nest file at all, so
// nothing here needs source.Locate; it only needs to look for the name spawn
// actually used.
func TestExecAcceptsASourceReference(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"corp-api","status":"running","workspaces":["/w"]}]}`)},
	}}

	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}
	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "exec", "corp:api", "true"); err != nil {
		t.Fatalf("den exec corp:api true: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/w", "corp-api", "true") {
		t.Errorf("the call must target the flattened sandbox corp-api; pipes: %v", f.Pipes)
	}
}

// The worktree'd form of TestExecAcceptsASourceReference. Flattening the WHOLE
// argument rewrote the "." too, so `den exec corp:api.feat12` looked for
// "corp-api-feat12" and matched nothing. The "." separates the worktree from
// the nest and only the NEST component carries a source prefix, so the split
// comes first and the flattening second.
func TestExecAcceptsAWorktreedSourceReference(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"corp-api.feat12","status":"running","workspaces":["/w"]}]}`)},
	}}

	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}
	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "exec", "corp:api.feat12", "true"); err != nil {
		t.Fatalf("den exec corp:api.feat12 true: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/w", "corp-api.feat12", "true") {
		t.Errorf("the call must target corp-api.feat12; pipes: %v", f.Pipes)
	}
}

// A command needs no separator. `den exec api go test` had two readings in the
// old comment here — a sandbox `api` running `go test`, or three sandbox names
// — but the second was never reachable: execArgs refused anything but exactly
// one name before `--`. The real ambiguity was FLAGS, and
// Flags().SetInterspersed(false) closes it the way docker compose does.
//
// Deviation from the brief: executeCmdWithSbx leaves IsTTY as the REAL probe
// (spawn.LooksInteractive), so this test's verdict would follow whatever fd 0
// and fd 1 happen to be wherever it runs — a developer's terminal, a CI runner's
// redirections, the pipes `go test` puts between itself and the test binary, a
// `go test -c` binary exec'd by hand. Which of those applies is not the point
// and must not become the point: it is a property of the harness, it differs by
// invocation and by toolchain, and pinning a claim about it here is how this
// comment went stale the last time. The rule is the one that holds regardless —
// this test must depend on NO real descriptor of the process. The injection
// below is what makes the verdict the test's own.
//
// The reason was sharper before #66, when the real probe answered true for ANY
// pair of char devices including /dev/null; the probe is exact now
// (spawn/isterminal_*.go) and the argument survives unchanged, because "exact"
// still means "reads the machine it runs on". Every other IsTTY-sensitive test
// in the repo (interactive_test.go, spawn_test.go:352) injects a fixed probe
// instead of trusting the real one; this test does the same, through the same
// Deps+NewRootCmdWith form the sibling tests below already use.
func TestExecRunsACommandWithoutTheDoubleDash(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"exec", "api", "go", "test", "./..."); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/w/api", "api", "go", "test", "./...") {
		t.Errorf("pipes = %v", f.Pipes)
	}
	if len(f.Attaches) != 0 {
		t.Errorf("a command must not open a shell; attaches = %v", f.Attaches)
	}
}

// Without a terminal there is no tty, and the argv says so. The verdict comes
// from the INJECTED probe, never from the terminal the suite happens to run
// under — that is why Deps.IsTTY exists.
func TestExecAllocatesATtyOnlyWhenDenHasOne(t *testing.T) {
	for _, tc := range []struct {
		name    string
		isTTY   func() bool
		wantTTY bool
	}{
		{"a terminal", func() bool { return true }, true},
		{"a pipe", func() bool { return false }, false},
		{"an unwired probe", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &sbx.Fake{Responses: map[string]sbx.Response{
				"ls --json": {Output: []byte(
					`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
			}}
			deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, IsTTY: tc.isTTY}
			if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
				"exec", "api", "true"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotTTY := f.HasAttached("exec", "-it")
			if gotTTY != tc.wantTTY {
				t.Errorf("tty = %v, want %v; calls = %v", gotTTY, tc.wantTTY, f.Calls)
			}
		})
	}
}

// -T is the docker compose spelling, and it wins over a real terminal: it
// exists so a caller in a terminal can still pipe cleanly.
//
// Both spellings are exercised. They are one flag, but only the long one is
// wired through cobra's `--name` path, and `den exec` has no other test left
// that reaches --no-tty at all: the deleted TestExecRefusesNoTTYWithNoCommand
// carried it as a subtest, and TestExecRefusesItsOwnFlagsAfterTheSandboxName
// only proves the long form is refused on the WRONG side of the name.
func TestExecMinusTSuppressesTheTtyEvenOnATerminal(t *testing.T) {
	for _, flag := range []string{"-T", "--no-tty"} {
		t.Run(flag, func(t *testing.T) {
			f := &sbx.Fake{Responses: map[string]sbx.Response{
				"ls --json": {Output: []byte(
					`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
			}}
			deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, IsTTY: func() bool { return true }}
			if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
				"exec", flag, "api", "true"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(f.Attaches) != 0 {
				t.Errorf("%s must forbid the tty; attaches = %v", flag, f.Attaches)
			}
			if !f.HasPiped("exec", "-w", "/w/api", "api", "true") {
				t.Errorf("the command must still run, without a terminal; pipes = %v", f.Pipes)
			}
		})
	}
}

// --workdir is spelled LONG on purpose: -w is den spawn's worktree, and giving
// it a second meaning on a sibling command is the collision den refuses
// elsewhere. The flag overrides the workspace the VM reported.
//
// It sits LEFT of the sandbox name since 2026-08-14: den's own flags all do,
// because SetInterspersed(false) hands everything past that name to the VM.
//
// Same deviation as TestExecRunsACommandWithoutTheDoubleDash above: the probe
// is injected rather than left as the real stdin check, for the same reason.
func TestExecWorkdirOverridesTheReportedWorkspace(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"exec", "--workdir", "/srv", "api", "true"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/srv", "api", "true") {
		t.Errorf("pipes = %v", f.Pipes)
	}
}

// `-w` must NOT be accepted here: silently taking den spawn's worktree
// shorthand as a workdir is exactly the kind of collision that makes two
// sibling commands mean different things by one letter.
func TestExecRefusesTheShortWorkdirFlag(t *testing.T) {
	f := &sbx.Fake{}
	if _, err := executeCmdWithSbx(t, f, "exec", "-w", "/srv", "api", "true"); err == nil {
		t.Error("-w must not be a workdir on den exec")
	}
}

// Zero positionals after the sandbox name has no reading left: the login shell
// moved to `den shell` on 2026-08-14. The message is the only place a user
// learns that, short of mistyping a command name, so the test pins it.
func TestExecRefusesASandboxWithNoCommand(t *testing.T) {
	f := &sbx.Fake{}
	_, err := executeCmdWithSbx(t, f, "exec", "api")
	if err == nil {
		t.Fatal("den exec with no command must be refused")
	}
	if !strings.Contains(err.Error(), "den shell api") {
		t.Errorf("the refusal must name `den shell api`; got %q", err.Error())
	}
	if len(f.Calls) != 0 {
		t.Errorf("the refusal must land before anything is asked of sbx; calls = %v", f.Calls)
	}
}

// With no sandbox name at all there is nothing to name in a remedy, so the
// refusal names the usage line instead.
func TestExecRefusesWithNoArgumentAtAll(t *testing.T) {
	f := &sbx.Fake{}
	_, err := executeCmdWithSbx(t, f, "exec")
	if err == nil {
		t.Fatal("den exec with no argument must be refused")
	}
	if len(f.Calls) != 0 {
		t.Errorf("the refusal must land before anything is asked of sbx; calls = %v", f.Calls)
	}
}

// A LEADING `--` is the one shape SetInterspersed(false) does not neutralize,
// and it is why execArgs consults ArgsLenAtDash at all. Measured 2026-08-14 on
// cobra v1.10.2, in den's real command tree: pflag handles the `--` terminator
// BEFORE the interspersed check, so `den exec -- a b` reaches Args with
// ArgsLenAtDash()==0 and args==["a","b"] — the separator already eaten. Left
// unrefused, that runs `b` in a sandbox named `a`, which is precisely the
// silent normalization spec §2 forbids.
//
// The `--`-AFTER-the-name shape is a different branch (an ordinary argument,
// dash==-1) and lives in TestExecRefusesItsOwnFlagsAfterTheSandboxName.
func TestExecRefusesALeadingDoubleDash(t *testing.T) {
	f := &sbx.Fake{}
	_, err := executeCmdWithSbx(t, f, "exec", "--", "api", "go", "test")
	if err == nil {
		t.Fatal("`--` before the sandbox name must be refused")
	}
	if !strings.Contains(err.Error(), "is not needed") {
		t.Errorf("the refusal must say the separator is not needed; got %q", err.Error())
	}
	if len(f.Calls) != 0 {
		t.Errorf("the refusal must land before anything is asked of sbx; calls = %v", f.Calls)
	}
}

// den's own flags belong LEFT of the sandbox name — SetInterspersed(false)
// stops parsing at the first positional, so `-T` after it would reach the VM
// as a program named `-T` and fail with `bash: -T: command not found`, an error
// that names nothing the user can fix. `--` is in the same closed set: cobra no
// longer consumes it (measured 2026-08-14), so it too would reach the VM.
//
// The set is CLOSED on purpose — `-T`, `--no-tty`, `--workdir`, `--workdir=…`,
// `--den-home`, `--den-home=…`, `--` — so the refusal cannot swallow a
// legitimate command. `--help` is NOT in it: it passes through to the sandbox,
// like compose (TestExecPassesHelpToTheSandbox).
//
// `--den-home` joined the set on 2026-08-14, and it is the case that proves the
// rule is about ORDER, not about ownership: it belongs to the root, but
// SetInterspersed(false) stops reading it past the first positional exactly as
// it stops reading `den exec`'s own flags. Left out, `den exec api --den-home
// /tmp true` ran a program named `--den-home` inside the sandbox.
func TestExecRefusesItsOwnFlagsAfterTheSandboxName(t *testing.T) {
	for _, tc := range []struct{ name, arg, want string }{
		{"-T", "-T", "before the sandbox name"},
		{"--no-tty", "--no-tty", "before the sandbox name"},
		{"--workdir", "--workdir", "before the sandbox name"},
		{"--workdir=", "--workdir=/srv", "before the sandbox name"},
		{"--den-home", "--den-home", "before the sandbox name"},
		{"--den-home=", "--den-home=/tmp", "before the sandbox name"},
		{"double dash", "--", "is not needed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &sbx.Fake{}
			_, err := executeCmdWithSbx(t, f, "exec", "api", tc.arg, "go", "build")
			if err == nil {
				t.Fatalf("%q in first command position must be refused", tc.arg)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal must say %q; got %q", tc.want, err.Error())
			}
			if len(f.Calls) != 0 {
				t.Errorf("the refusal must land before anything is asked of sbx; calls = %v", f.Calls)
			}
		})
	}
}

// validateArgs runs argv through the REAL command tree's argument validation,
// and through nothing else: root.Find picks the command, ParseFlags fills the
// flags, ValidateArgs is the very call cobra's Execute makes next. RunE never
// runs, so no sbx and no den home are touched — which is what lets the test
// below replay a remedy naming `--den-home /tmp` without reading /tmp.
//
// The real tree rather than a hand-built command: what is under test includes
// SetInterspersed(false) and the root's persistent `--den-home`, and both are
// properties of the assembled tree.
func validateArgs(t *testing.T, argv ...string) error {
	t.Helper()
	cmd, flags, err := NewRootCmd().Find(argv)
	if err != nil {
		t.Fatalf("no command for %v: %v", argv, err)
	}
	if err := cmd.ParseFlags(flags); err != nil {
		return err
	}
	return cmd.ValidateArgs(cmd.Flags().Args())
}

// remedyOf returns the command line a refusal proposes: the backticked span
// right after "write ". Anchored on that word rather than on the first backtick
// of the message, because a refusal quotes the token it objects to first
// (“den exec: `--` is not needed — write `…` “). The "no command given"
// wording carries a second remedy after this one — `den shell …` — and it is
// not a `den exec` line, so it is not what this file replays.
func remedyOf(t *testing.T, msg string) string {
	t.Helper()
	_, after, ok := strings.Cut(msg, "write `")
	if !ok {
		t.Fatalf("no remedy in %q", msg)
	}
	line, _, ok := strings.Cut(after, "`")
	if !ok {
		t.Fatalf("unterminated remedy in %q", msg)
	}
	return line
}

// Every refusal ends in "write `…`", and the line it writes must be one den
// ACCEPTS. That is one property, not five, so it is tested as one: each case
// asserts the remedy, then feeds the remedy back through the same validator and
// requires nil.
//
// The 2026-08-14 review found the class, not an instance. Each message used to
// re-join the raw tail on its own, so a line carrying two defects lost only one
// of them per round trip: `den exec api -- -T go build` proposed `den exec api
// -T go build`, refused in turn for the flag order — two refusals to reach a
// legal line. Degenerate tails were worse: `den exec api --` proposed
// `den exec api`, itself refused for having no command, and `den exec --`
// proposed `den exec ` with a trailing space and no sandbox at all.
//
// The last three cases are the ones a reviewer of the PR named on 2026-08-14:
// a flag's VALUE must travel with it (`--workdir /srv` lifted as a pair, or the
// proposal reads `--workdir api /srv true` and makes `api` the workdir), and
// `--den-home` must be in the closed set at all.
func TestExecRemediesAreThemselvesLegal(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"a separator and a flag after the name",
			[]string{"exec", "api", "--", "-T", "go", "build"}, "den exec -T api go build"},
		{"a separator and nothing after it",
			[]string{"exec", "api", "--"}, "den exec api go test"},
		{"a leading separator",
			[]string{"exec", "--", "api", "go", "build"}, "den exec api go build"},
		// This row was inverted on 2026-08-16. It used to expect
		// `den exec api go build` and carried a comment calling the dropped `-T`
		// an accepted omission. The remedy is a line the user RETYPES, so a
		// dropped flag is a silently different run — and with --repo it would be
		// a dropped mount.
		//
		// `--no-tty=true`, not `-T`: readBackFlags spells a read-back boolean
		// canonically with its value, and it has no shorthand path — it emits
		// "--" + f.Name. The bare form stays what the USER types.
		//
		// The replay holds: pflag parses `--no-tty=true` left of the first
		// positional, so s.flags is empty and enterArgs returns nil.
		{"a flag before a leading separator",
			[]string{"exec", "-T", "--", "api", "go", "build"}, "den exec --no-tty=true api go build"},
		{"a flag and no command",
			[]string{"exec", "api", "-T"}, "den exec -T api go test"},
		{"a workdir after the name",
			[]string{"exec", "api", "--workdir", "/srv", "true"}, "den exec --workdir /srv api true"},
		{"a workdir spelled with =",
			[]string{"exec", "api", "--workdir=/srv", "true"}, "den exec --workdir=/srv api true"},
		{"the root's own den home after the name",
			[]string{"exec", "api", "--den-home", "/tmp", "true"}, "den exec --den-home /tmp api true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArgs(t, tc.argv...)
			if err == nil {
				t.Fatalf("%v must be refused", tc.argv)
			}
			got := remedyOf(t, err.Error())
			if got != tc.want {
				t.Errorf("remedy = %q, want %q (full message: %q)", got, tc.want, err.Error())
			}
			// The property, and the reason this test exists: replay it.
			replay := strings.Fields(got)[1:] // drop "den"
			if err := validateArgs(t, replay...); err != nil {
				t.Errorf("the remedy %q is refused in turn: %v", got, err)
			}
		})
	}
}

// The remedy must carry a flag pflag consumed before the validator ran.
// Measured on the 2026-08-16 binary, the proposal dropped `--workdir /srv`
// entirely: a line the user retypes, silently landing them in another
// directory. exec.go called the omission a decision on the grounds that "cobra
// has honoured the flag" — it honoured it on an invocation that is REFUSED and
// never runs.
func TestExecRemediesCarryFlagsTypedBeforeTheSeparator(t *testing.T) {
	err := validateArgs(t, "exec", "--workdir", "/srv", "--", "api", "go", "build")
	if err == nil {
		t.Fatal("a leading separator must be refused")
	}
	const want = "den exec: `--` is not needed, and a sandbox name must come first — " +
		"write `den exec --workdir /srv api go build`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// With no sandbox name there is no line to propose, so these two refuse on the
// usage instead of writing a remedy. `den exec --` is the shape that used to
// produce “write `den exec ` “ — a trailing space, no name, nothing to run.
// The third case is a sandbox named by an unset shell variable — `den exec
// "$SANDBOX" -T go build` in a CI script. den must not go looking further down
// the line for something that looks like a name: `go` would fit, and the remedy
// would then propose running `build` in a sandbox the user never mentioned.
func TestExecRefusesWithNoSandboxNameByNamingTheUsage(t *testing.T) {
	for _, argv := range [][]string{{"exec"}, {"exec", "--"}, {"exec", "", "-T", "go", "build"}} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			err := validateArgs(t, argv...)
			if err == nil {
				t.Fatalf("%v must be refused", argv)
			}
			if strings.Contains(err.Error(), "write `") {
				t.Errorf("a refusal naming no sandbox must not write a remedy; got %q", err.Error())
			}
			if !strings.Contains(err.Error(), "usage: den exec <name> <cmd> [args...]") {
				t.Errorf("the refusal must name the usage line; got %q", err.Error())
			}
		})
	}
}

// The command's OWN flags pass through untouched. This is what
// SetInterspersed(false) buys, and it is the reason `--` could be dropped at
// all — measured on cobra in den's real command tree, 2026-08-14.
func TestExecPassesTheCommandsOwnFlagsThrough(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"exec", "api", "go", "test", "-v", "-run", "TestX"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/w/api", "api", "go", "test", "-v", "-run", "TestX") {
		t.Errorf("pipes = %v", f.Pipes)
	}
}

// `den exec api --help` runs `--help` IN the sandbox: cobra does NOT intercept
// it past the first positional under SetInterspersed(false) (measured
// 2026-08-14), and compose behaves the same. The easiest behaviour here to
// lose by accident, hence a test of its own.
func TestExecPassesHelpToTheSandbox(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			f := &sbx.Fake{Responses: map[string]sbx.Response{
				"ls --json": {Output: []byte(
					`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
			}}
			deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

			if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
				"exec", "api", "mytool", flag); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !f.HasPiped("exec", "-w", "/w/api", "api", "mytool", flag) {
				t.Errorf("pipes = %v", f.Pipes)
			}
		})
	}
}

// SetInterspersed(false) is set on the command's own FlagSet, which cobra
// merges the root's persistent flags INTO before parsing. The merge must not
// re-arm interspersing: --den-home has to keep parsing from the left.
//
// The assertion is POSITIVE — that the sbx lookup was reached — and not merely
// "the error is not about an unknown flag". A negative assertion here passes
// whatever happens: the fake answers nothing, so the command fails on the
// lookup either way, and a re-armed FlagSet would go unnoticed.
func TestExecStillReadsDenHomeBeforeTheSubcommand(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"--den-home", t.TempDir(), "exec", "api", "true"); err != nil {
		t.Fatalf("--den-home must still parse before the subcommand: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/w/api", "api", "true") {
		t.Errorf("the command must have run, which proves parsing got past --den-home; pipes = %v", f.Pipes)
	}
}

// den's own lines belong on stderr when the caller is a pipe: `den exec -T api
// go build | tee log` must carry the child's stdout and nothing else.
//
// Deviation from the brief: Freshness is added to Deps. The fixture is
// "stopped", so this reaches spawn.CheckFreshnessOnReentry on the STARTING
// branch, which spawn.go:1016-1018 documents as refusing a zero GateOptions
// by design (Sleep/Now/Timeout/Interval must be real) rather than silently
// completing — a Deps literal missing Freshness fails with "unusable
// agent-freshness gate options", not the assertion below. fakeGateOptions()
// is the same fixture sbxDeps() already uses for this reason.
func TestExecPutsItsOwnChatterOnStderrWithoutATty(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}
	stdout, stderr, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"exec", "api", "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout must belong to the command alone; got %q", stdout)
	}
	if !strings.Contains(stderr, "stopped") {
		t.Errorf("the stopped-sandbox line must still be said, on stderr; got %q", stderr)
	}
}

// The interactive path keeps saying it on stdout, as it always has: nothing is
// piped there, and moving it would change a surface #60 does not touch.
//
// Same deviation as TestExecPutsItsOwnChatterOnStderrWithoutATty above:
// Freshness added, for the same "stopped" fixture / starting-branch reason.
// The tty comes from the INJECTED probe and nothing else since 2026-08-14: a
// command is mandatory now, so there is no branch left that forces one. -T
// would flip this verdict, which is why the argv carries none.
func TestExecKeepsItsChatterOnStdoutWithATty(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return true }}
	stdout, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "exec", "api", "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "stopped") {
		t.Errorf("stdout = %q, want the stopped-sandbox line", stdout)
	}
}

// The status of the command becomes the status of den — the reason #60 calls
// exit-code propagation part of the issue rather than a follow-up.
//
// Same deviation as TestExecRunsACommandWithoutTheDoubleDash above: PipeErr is
// only consulted on the non-tty branch, so a real IsTTY probe reporting true
// (as this sandbox's does) would route this through Attach instead and never
// see the error at all — the injected probe is what makes this test exercise
// Pipe, deterministically.
func TestExecPropagatesTheCommandStatus(t *testing.T) {
	f := &sbx.Fake{
		Responses: map[string]sbx.Response{
			"ls --json": {Output: []byte(
				`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
		},
		PipeErr: &sbx.ExecError{Bin: "sbx", Err: fakeExitError{code: 42}},
	}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}
	_, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "exec", "api", "false")
	var child *sbx.ChildExit
	if !errors.As(err, &child) || child.Code != 42 {
		t.Fatalf("err = %v, want a *sbx.ChildExit carrying 42", err)
	}
}

type fakeExitError struct{ code int }

func (fakeExitError) Error() string   { return "exit status 42" }
func (e fakeExitError) ExitCode() int { return e.code }

// #69, the `den exec` door: the command runs where the user typed it, not in
// the first workspace the VM reports. Same judge as `den spawn`
// (spawn.StartDir) — two doors, one rule.
//
// The fixture mounts the PARENT of the test process's own working directory:
// the only directory a hermetic test can be sure both exists and contains its
// cwd, with no chdir and no process.
func TestExecStartsInTheDirectoryTheUserTypedFrom(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["` +
				filepath.Dir(cwd) + `"]}]}`)},
	}}

	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}
	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "exec", "api", "true"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasPiped("exec", "-w", cwd, "api", "true") {
		t.Errorf("-w must be the cwd, not the mount root; pipes: %v", f.Pipes)
	}
}

// …and --workdir still wins over it, unchanged: rule 1 of the judge is "I know
// better", and a cwd that matches a mount must not quietly overtake a path the
// caller named.
func TestExecWorkdirOverridesTheCwd(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["` +
				filepath.Dir(cwd) + `"]}]}`)},
	}}

	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}
	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"exec", "--workdir", "/custom", "api", "true"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasPiped("exec", "-w", "/custom", "api", "true") {
		t.Errorf("--workdir must win over the cwd; pipes: %v", f.Pipes)
	}
}
