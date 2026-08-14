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

	if _, err := executeCmdWithSbx(t, f, "exec", "api"); err != nil {
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
	if !slices.Contains(attach, "bash") {
		t.Errorf("the attach must launch a shell; got: %v", attach)
	}
}

// The fixture's `:ro` suffix is not decorative: it separates b.Workdir()
// (which strips it) from b.Workspaces[0] (which would keep it). Without it,
// both implementations pass — measured by review, on this exact file.
//
// Necessary complement to the test above, which scans f.Calls: Calls CONFLATES
// Run and Attach (see sbx/fake.go), so a `Run("exec", ...)` — a mute shell,
// no tty — satisfies it just as much as a real attach. Only f.Attaches tells
// the two apart. This test also locks the `-it` flag and the FULL argv, in
// order: `sbx exec [flags] SANDBOX COMMAND` — a postponed `-w` would land as-is
// on `bash -l` instead of setting the working directory.
func TestExecAttachesWithATtyNotARun(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api:ro","/profile"]}]}`)},
	}}

	if _, err := executeCmdWithSbx(t, f, "exec", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Errorf("the attach must be an Attach, with the full ordered argv; attaches: %v", f.Attaches)
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

	if _, err := executeCmdWithSbx(t, f, "exec", "api"); err != nil {
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

	_, err := executeCmdWithSbx(t, f, "exec", "missing")
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
func TestExecResumesAStoppedSandbox(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w/api"]}]}`)},
	}}

	if _, err := executeCmdWithSbx(t, f, "exec", "api"); err != nil {
		t.Fatalf("a stopped sandbox must be resumed: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Errorf("resuming must attach in the VM's workdir; attaches: %v", f.Attaches)
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

			_, err := executeCmdWithSbx(t, f, "exec", "api")
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

	_, err := executeCmdWithSbx(t, f, "exec", "api")
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

	_, err := executeCmdWithSbx(t, f, "exec", "api")
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
// The clock is the injected one (sbxDeps), so the rounds happen instantly.
func TestExecPollsRatherThanReadsOnceWhenItStartsAStoppedSandbox(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w/api"]}]}`)},
		// A run that has begun and reported nothing: GatePending, forever.
		execGateRead("api"): {Output: []byte("=== dispatcher run 2026-08-03T10:00:00Z ===\n")},
	}}

	stdout, err := executeCmdWithSbx(t, f, "exec", "api")
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
	if !strings.Contains(stdout, "waiting for agent freshness") {
		t.Errorf("a wait den actually performs must be announced; got: %q", stdout)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Errorf("an exhausted budget must still open the shell; attaches: %v", f.Attaches)
	}
}

// The other half: a gate that PASSED costs the user nothing — no line, and the
// shell they asked for. Asserted together with the refusal above, because a
// `den exec` that refused everything would satisfy that test alone.
func TestExecAttachesAndStaysSilentWhenTheFreshnessGatePassed(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
		execGateRead("api"): {Output: execGateLog("api", "ok")},
	}}

	stdout, err := executeCmdWithSbx(t, f, "exec", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Errorf("a passing gate must not cost the shell; attaches: %v", f.Attaches)
	}
	if strings.Contains(stdout, "freshness") {
		t.Errorf("a passing gate is the ordinary outcome and says nothing; got: %q", stdout)
	}
}

// The gate is read BEFORE the attach, not alongside it. Read after, its refusal
// would arrive behind a shell that already owns the terminal — which is the
// exact defect the ordering of warnEmptyAgentOnReentry already avoids.
func TestExecReadsTheFreshnessGateBeforeAttaching(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
		execGateRead("api"): {Output: execGateLog("api", "ok")},
	}}

	if _, err := executeCmdWithSbx(t, f, "exec", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	read, attach := -1, -1
	for i, c := range f.Calls {
		if strings.Join(c, " ") == execGateRead("api") {
			read = i
		}
		if slices.Contains(c, "-it") {
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
		"exec", "api")
	if err != nil {
		t.Fatalf("an empty agent must warn, not block: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Fatalf("the shell must still open; attaches: %v", f.Attaches)
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
		"exec", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stderr, "warning") {
		t.Errorf("an agent with keys must be silent; stderr: %q", stderr)
	}
}

// THE constraint that makes the warning acceptable on this command at all:
// `den exec` reads the den home only to learn ssh.mode, and a den home it cannot
// read costs the user NOTHING — no error, no missing shell. The whole point of
// the command is that a broken ~/.den never stands between the user and a live
// sandbox; the warning is advisory, so it is what gives way, silently.
//
// The empty temp dir is exactly that state: no config.yaml at all.
func TestExecOpensTheShellWhenTheDenHomeCannotBeRead(t *testing.T) {
	execSocket(t)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	probed := false

	_, stderr, err := runExecWithAgent(t, t.TempDir(), f, func() sshagent.Result {
		probed = true
		return sshagent.Result{State: sshagent.StateEmpty}
	}, "exec", "api")
	if err != nil {
		t.Fatalf("an unreadable den home must not cost the user their shell: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Fatalf("the shell must open regardless; attaches: %v", f.Attaches)
	}
	if probed {
		t.Error("with no readable ssh.mode, den cannot know the agent is even forwarded: " +
			"probing it decides on a mode it never read")
	}
	if strings.Contains(stderr, "warning") {
		t.Errorf("stderr = %q, a den home den could not read must produce no verdict", stderr)
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
			}, "exec", "api")
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
			// gave up before reaching it: the attach is what makes the silence a
			// decision about the mode.
			if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
				t.Errorf("the shell must have opened; attaches: %v", f.Attaches)
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
	}, "exec", "api")
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

	_, err := executeCmdWithSbx(t, f, "exec", "missing")
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

	if _, err := executeCmdWithSbx(t, f, "exec", "corp:api"); err != nil {
		t.Fatalf("den exec corp:api: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w", "corp-api", "bash", "-l") {
		t.Errorf("the attach must target the flattened sandbox corp-api; attaches: %v", f.Attaches)
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

	if _, err := executeCmdWithSbx(t, f, "exec", "corp:api.feat12"); err != nil {
		t.Fatalf("den exec corp:api.feat12: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w", "corp-api.feat12", "bash", "-l") {
		t.Errorf("the attach must target corp-api.feat12; attaches: %v", f.Attaches)
	}
}

// The contract of #60: `den exec api -- go test ./...` runs the command, and
// the shell is what happens when no command is given — not the reverse.
//
// Deviation from the brief: executeCmdWithSbx leaves IsTTY as the REAL probe
// (spawn.LooksInteractive), so this test would flip verdict depending on what
// fd 0 and fd 1 happen to be wherever it runs. `go test` under a plain shell
// leaves both attached to the developer's terminal — true — and a CI runner
// redirects them — false. The injection below is what makes the verdict the
// test's own.
//
// The reason was sharper before #66, when the real probe answered true for ANY
// pair of char devices including /dev/null; the probe is exact now
// (spawn/isterminal_*.go) and the argument survives unchanged, because "exact"
// still means "reads the machine it runs on". Every other IsTTY-sensitive test
// in the repo (interactive_test.go, spawn_test.go:352) injects a fixed probe
// instead of trusting the real one; this test does the same, through the same
// Deps+NewRootCmdWith form the sibling tests below already use.
func TestExecRunsTheCommandAfterTheDoubleDash(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"exec", "api", "--", "go", "test", "./..."); err != nil {
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
				"exec", "api", "--", "true"); err != nil {
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
func TestExecMinusTSuppressesTheTtyEvenOnATerminal(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, IsTTY: func() bool { return true }}
	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"exec", "api", "-T", "--", "true"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.Attaches) != 0 {
		t.Errorf("-T must forbid the tty; attaches = %v", f.Attaches)
	}
}

// --workdir is spelled LONG on purpose: -w is den spawn's worktree, and giving
// it a second meaning on a sibling command is the collision den refuses
// elsewhere. The flag overrides the workspace the VM reported.
//
// Same deviation as TestExecRunsTheCommandAfterTheDoubleDash above: the probe
// is injected rather than left as the real stdin check, for the same reason.
func TestExecWorkdirOverridesTheReportedWorkspace(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return false }}

	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		"exec", "api", "--workdir", "/srv", "--", "true"); err != nil {
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
	if _, err := executeCmdWithSbx(t, f, "exec", "api", "-w", "/srv", "--", "true"); err == nil {
		t.Error("-w must not be a workdir on den exec")
	}
}

// A command not separated by `--` is refused rather than guessed: `den exec api
// ls` could be a sandbox named api running ls, or two sandbox names. den
// refuses and names the form (spec §2).
func TestExecRefusesACommandWithoutTheDoubleDash(t *testing.T) {
	f := &sbx.Fake{}
	_, err := executeCmdWithSbx(t, f, "exec", "api", "go", "test")
	if err == nil {
		t.Fatal("a command without `--` must be refused")
	}
	if !strings.Contains(err.Error(), "--") {
		t.Errorf("the refusal must name the form to use; got %q", err.Error())
	}
	if len(f.Calls) != 0 {
		t.Errorf("the refusal must land before anything is asked of sbx; calls = %v", f.Calls)
	}
}

// execArgs' `dash != 1` branch, uncovered until now: `den exec` takes exactly
// one sandbox name before `--`, and zero is as wrong as two. Exercised
// separately from TestExecRefusesACommandWithoutTheDoubleDash, which never
// reaches this branch (its args have no `--` at all, so ArgsLenAtDash is -1).
func TestExecRefusesZeroPositionalsBeforeTheDoubleDash(t *testing.T) {
	f := &sbx.Fake{}
	_, err := executeCmdWithSbx(t, f, "exec", "--", "a", "b")
	if err == nil {
		t.Fatal("`--` with no sandbox name before it must be refused")
	}
	if !strings.Contains(err.Error(), "sandbox name") {
		t.Errorf("the refusal must name what is missing; got %q", err.Error())
	}
	if len(f.Calls) != 0 {
		t.Errorf("the refusal must land before anything is asked of sbx; calls = %v", f.Calls)
	}
}

// The design spec requires this refusal to be identical, "dans les mêmes mots,
// octet pour octet", to den spawn's (TestNoTTYReachesSpawnOptions,
// spawn_test.go) — an identity that was pinned on one side only until now.
func TestExecRefusesNoTTYWithNoCommand(t *testing.T) {
	f := &sbx.Fake{}
	for _, name := range []string{"-T", "--no-tty"} {
		t.Run(name, func(t *testing.T) {
			_, err := executeCmdWithSbx(t, f, "exec", "api", name)
			if err == nil {
				t.Fatal("-T with no command must be refused")
			}
			if !strings.Contains(err.Error(), "-T") {
				t.Errorf("the refusal must name the flag in play: %v", err)
			}
			if len(f.Calls) != 0 {
				t.Errorf("the refusal must land before anything is asked of sbx; calls = %v", f.Calls)
			}
		})
	}
}

// den's own lines belong on stderr when the caller is a pipe: `den exec api -T
// -- go build | tee log` must carry the child's stdout and nothing else.
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
		"exec", "api", "--", "true")
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
// This case also covers the no-command + tty path: len(command) == 0 forces
// tty unconditionally in the RunE, so IsTTY: true here is confirming the
// login-shell branch, not exercising the -T refusal (noTTY is false).
func TestExecKeepsItsChatterOnStdoutWithATty(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w/api"]}]}`)},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions(), IsTTY: func() bool { return true }}
	stdout, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "exec", "api")
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
// Same deviation as TestExecRunsTheCommandAfterTheDoubleDash above: PipeErr is
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
	_, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "exec", "api", "--", "false")
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

	if _, err := executeCmdWithSbx(t, f, "exec", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", cwd, "api", "bash", "-l") {
		t.Errorf("-w must be the cwd, not the mount root; attaches: %v", f.Attaches)
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

	if _, err := executeCmdWithSbx(t, f, "exec", "api", "--workdir", "/custom"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/custom", "api", "bash", "-l") {
		t.Errorf("--workdir must win over the cwd; attaches: %v", f.Attaches)
	}
}
