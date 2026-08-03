package cli

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sshagent"
)

func TestShAttachesInTheWorkdir(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api:ro","/profile"]}]}`)},
	}}

	if _, err := executeCmdWithSbx(t, f, "sh", "api"); err != nil {
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
func TestShAttachesWithATtyNotARun(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api:ro","/profile"]}]}`)},
	}}

	if _, err := executeCmdWithSbx(t, f, "sh", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Errorf("the attach must be an Attach, with the full ordered argv; attaches: %v", f.Attaches)
	}
}

// `sbx run` would launch the image's flavor (often claude): never.
//
// The fixture's `"status":"running"` is not decorative: `den sh` now refuses
// any sandbox whose status is not explicitly "running" (see
// TestShRefusesASandboxThatIsNotRunning), and a fixture without `status` would
// no longer even reach the attach.
func TestShNeverUsesSbxRun(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w"]}]}`)},
	}}

	if _, err := executeCmdWithSbx(t, f, "sh", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.HasCalled("run") {
		t.Errorf("den sh must never go through `sbx run`; calls: %v", f.Calls)
	}
}

// An unknown name must list what is running: "not found" alone would force
// the user to run another command just to know what to type.
func TestShUnknownName(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[{"name":"api"},{"name":"web"}]}`)},
	}}

	_, err := executeCmdWithSbx(t, f, "sh", "missing")
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

// F2, on the OTHER path: `den sh` must resume a stopped sandbox, like
// `den <nest>`. Proven HERE and not only in internal/spawn — nothing at the
// level of sbx.CheckAttachable guarantees newShCmd calls it, and a policy
// widened on only one side would reopen the defect on the other.
func TestShResumesAStoppedSandbox(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w/api"]}]}`)},
	}}

	if _, err := executeCmdWithSbx(t, f, "sh", "api"); err != nil {
		t.Fatalf("a stopped sandbox must be resumed: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Errorf("resuming must attach in the VM's workdir; attaches: %v", f.Attaches)
	}
}

// The same guard as on `den <nest>`, on the OTHER path: both end in an
// `sbx exec`, and both are wrong on a VM den knows nothing about. A `den sh`
// that opens a shell in an `exited` sandbox is no less wrong than a
// `den <nest>` that does — and it is the very same defect, not a cousin.
func TestShRefusesASandboxThatIsNotRunning(t *testing.T) {
	for _, status := range []string{"exited", "paused", "Running", ""} {
		t.Run("status="+status, func(t *testing.T) {
			f := &sbx.Fake{Responses: map[string]sbx.Response{
				"ls --json": {Output: []byte(
					`{"sandboxes":[{"name":"api","status":"` + status + `","workspaces":["/w/api"]}]}`)},
			}}

			_, err := executeCmdWithSbx(t, f, "sh", "api")
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

// shGateLog renders a dispatcher journal whose LAST run reports on den's own
// mixin for this sandbox. `verdict` is the whole verdict line's leading token —
// `ok` or `fail … exit=1` — because what agent.ParseKitLog reads is that token
// and the path that follows it.
//
// The path carries the `002-` prefix sbx assigns, which agent.MixinName
// deliberately does NOT match on: writing the realistic form here is what keeps
// this fixture a sample of the measured journal rather than a restatement of
// the parser.
func shGateLog(sandbox, verdict string) []byte {
	path := "/etc/durable-startup.d/002-startup-den-" + strings.ReplaceAll(sandbox, ".", "-") + "/000-cmd.sh"
	return []byte("=== dispatcher run 2026-08-03T10:00:00Z ===\n" +
		"> " + path + "\n" +
		verdict + " " + path + "\n")
}

// shGateRead is the key of the ONE `sbx exec` agent.ReadFreshness makes.
func shGateRead(sandbox string) string {
	return "exec " + sandbox + " cat /var/log/sbx-kit-startup.log"
}

// #18's hole, entered through the other door. `den <nest>` holds the §9.1 gate
// since PR #26 — it refuses a sandbox whose agent den KNOWS was not updated —
// but `den sh` does not go through spawn.Spawn at all, and on the bench the
// very same sandbox that `den <nest>` refused handed out a shell in silence.
//
// A guarantee held by one door out of two is more misleading than no guarantee:
// §9.1 says "a sandbox never starts with a stale agent", and `den sh` on a
// STOPPED sandbox starts one.
func TestShRefusesASandboxWhoseFreshnessGateFailed(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
		shGateRead("api"): {Output: shGateLog("api", "fail")},
	}}

	_, err := executeCmdWithSbx(t, f, "sh", "api")
	if err == nil {
		t.Fatal("a failed §9.1 gate must not lead to a shell")
	}
	for _, want := range []string{"api", "§9.1", "/var/log/sbx-kit-startup.log"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must contain %q; got: %v", want, err)
		}
	}
	if len(f.Attaches) != 0 {
		t.Errorf("a refused gate must attach nowhere; attaches: %v", f.Attaches)
	}
}

// The case issue #27 names as the real one: `den sh` on a STOPPED sandbox
// STARTS it, and the gate must hold there too rather than be skipped for a VM
// den is about to boot.
//
// The fixture is TWO blocks — a failed run, then the one the restart appended,
// failing again, which is what a deterministically broken freshness command
// does on every boot. It pins that the stopped branch REFUSES; it does not by
// itself prove den waited, since a single read of the last block would refuse
// this fixture too. TestShPollsRatherThanReadsOnceWhenItStartsAStoppedSandbox
// below is the one that discriminates, and it exists because the two are
// otherwise indistinguishable — which is exactly how the hole got in.
func TestShWaitsForTheGateWhenItStartsAStoppedSandbox(t *testing.T) {
	log := append(shGateLog("api", "fail"), shGateLog("api", "fail")...)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w/api"]}]}`)},
		shGateRead("api"): {Output: log},
	}}

	_, err := executeCmdWithSbx(t, f, "sh", "api")
	if err == nil {
		t.Fatal("starting a stopped sandbox whose gate failed must not lead to a shell")
	}
	if !strings.Contains(err.Error(), "§9.1") {
		t.Errorf("the refusal must be the §9.1 one; got: %v", err)
	}
	if len(f.Attaches) != 0 {
		t.Errorf("a refused gate must attach nowhere; attaches: %v", f.Attaches)
	}
}

// ...and the wait really is a WAIT, not a single read wearing its name.
//
// This is the property that closes the hole. A restart makes the dispatcher
// RE-RUN (measured, agent.KitLogPath) and ParseKitLog reads only the LAST
// block, so right after `den sh` wakes a stopped sandbox the fresh block has
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
func TestShPollsRatherThanReadsOnceWhenItStartsAStoppedSandbox(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w/api"]}]}`)},
		// A run that has begun and reported nothing: GatePending, forever.
		shGateRead("api"): {Output: []byte("=== dispatcher run 2026-08-03T10:00:00Z ===\n")},
	}}

	stdout, err := executeCmdWithSbx(t, f, "sh", "api")
	if err != nil {
		t.Fatalf("a budget that runs out is a note, never a refusal: %v", err)
	}
	reads := 0
	for _, c := range f.Calls {
		if strings.Join(c, " ") == shGateRead("api") {
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
// `den sh` that refused everything would satisfy that test alone.
func TestShAttachesAndStaysSilentWhenTheFreshnessGatePassed(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
		shGateRead("api"): {Output: shGateLog("api", "ok")},
	}}

	stdout, err := executeCmdWithSbx(t, f, "sh", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Errorf("a passing gate must not cost the shell; attaches: %v", f.Attaches)
	}
	if strings.Contains(stdout, "§9.1") {
		t.Errorf("a passing gate is the ordinary outcome and says nothing; got: %q", stdout)
	}
}

// The gate is read BEFORE the attach, not alongside it. Read after, its refusal
// would arrive behind a shell that already owns the terminal — which is the
// exact defect the ordering of warnEmptyAgentOnReentry already avoids.
func TestShReadsTheFreshnessGateBeforeAttaching(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
		shGateRead("api"): {Output: shGateLog("api", "ok")},
	}}

	if _, err := executeCmdWithSbx(t, f, "sh", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	read, attach := -1, -1
	for i, c := range f.Calls {
		if strings.Join(c, " ") == shGateRead("api") {
			read = i
		}
		if slices.Contains(c, "-it") {
			attach = i
		}
	}
	if read < 0 {
		t.Fatalf("den sh must read the §9.1 journal; calls: %v", f.Calls)
	}
	if attach < 0 || read > attach {
		t.Errorf("the journal must be read before the attach; read=%d attach=%d, calls: %v",
			read, attach, f.Calls)
	}
}

// runShWithAgent runs `den sh` through the REAL command tree — NewRootCmdWith,
// not a hand-built sh command — with an injected sbx.Runner and SSH probe, on a
// given den home.
//
// The full tree on purpose: everything the empty-agent warning needs on this
// path is wiring (the den home reaching sh.go, deps.SSHAgent reaching it too,
// the warning landing on the command's stderr), and a helper that called
// newShCmd directly would prove none of it.
//
// Separate streams, for the property the warning is judged on: a diagnostic
// must not land in the stdout a caller pipes.
func runShWithAgent(t *testing.T, denHome string, r sbx.Runner,
	probe func() sshagent.Result, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: r, SSHAgent: probe}
	return executeCmdSeparateStreams(t, NewRootCmdWith(deps),
		append([]string{"--den-home", denHome}, args...)...)
}

// shSocket puts a forwarded socket in den's environment, like spawn's
// forwardedSocket: without it every test below takes the absent-socket branch
// and never reaches the probe. Set explicitly rather than inherited — left to
// the machine, these tests would pass on a workstation running an agent and
// change verdict on a bare CI runner.
func shSocket(t *testing.T) {
	t.Helper()
	t.Setenv("SSH_AUTH_SOCK", "/tmp/den-test/agent.sock")
}

// shDenHome writes the smallest den home `den sh` can read a mode out of. The
// ssh block is the caller's, because the mode is what these tests vary; empty
// means the config's default, agent-forward.
func shDenHome(t *testing.T, sshBlock string) string {
	t.Helper()
	dir := t.TempDir()
	writeConfig(t, dir, minimalConfig+sshBlock)
	return dir
}

// A sandbox is created once and re-entered daily, most often with `den sh` —
// and `den sh` said nothing about an empty forwarded agent, on any OS, while
// `den <nest>` warned on both its branches. The forwarded socket being a live
// proxy, an agent emptied since the VM booted denies `git push` inside it just
// as silently on re-entry: same machine state, same consequence, same warning.
//
// The attach is asserted too: the warning must not have replaced the shell the
// user asked for.
func TestShWarnsWhenTheForwardedAgentIsEmpty(t *testing.T) {
	shSocket(t)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}

	stdout, stderr, err := runShWithAgent(t, shDenHome(t, ""), f,
		func() sshagent.Result { return sshagent.Result{State: sshagent.StateEmpty} },
		"sh", "api")
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
func TestShDoesNotWarnWhenTheForwardedAgentHasKeys(t *testing.T) {
	shSocket(t)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}

	_, stderr, err := runShWithAgent(t, shDenHome(t, ""), f,
		func() sshagent.Result { return sshagent.Result{State: sshagent.StateKeys, Identities: 2} },
		"sh", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stderr, "warning") {
		t.Errorf("an agent with keys must be silent; stderr: %q", stderr)
	}
}

// THE constraint that makes the warning acceptable on this command at all:
// `den sh` reads the den home only to learn ssh.mode, and a den home it cannot
// read costs the user NOTHING — no error, no missing shell. The whole point of
// the command is that a broken ~/.den never stands between the user and a live
// sandbox; the warning is advisory, so it is what gives way, silently.
//
// The empty temp dir is exactly that state: no config.yaml at all.
func TestShOpensTheShellWhenTheDenHomeCannotBeRead(t *testing.T) {
	shSocket(t)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	probed := false

	_, stderr, err := runShWithAgent(t, t.TempDir(), f, func() sshagent.Result {
		probed = true
		return sshagent.Result{State: sshagent.StateEmpty}
	}, "sh", "api")
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
func TestShDoesNotProbeTheAgentOutsideAgentForward(t *testing.T) {
	for _, sshBlock := range []string{"ssh:\n  mode: none\n", "ssh:\n  mode: mount\n  dir: /tmp/den-test/ssh\n"} {
		t.Run(sshBlock, func(t *testing.T) {
			shSocket(t)
			f := &sbx.Fake{Responses: map[string]sbx.Response{
				"ls --json": {Output: []byte(
					`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
			}}
			probed := false

			_, stderr, err := runShWithAgent(t, shDenHome(t, sshBlock), f, func() sshagent.Result {
				probed = true
				return sshagent.Result{State: sshagent.StateEmpty}
			}, "sh", "api")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if probed {
				t.Error("the agent must not be probed outside agent-forward")
			}
			if strings.Contains(stderr, "warning") {
				t.Errorf("stderr = %q, no SSH warning expected outside agent-forward", stderr)
			}
			// Without this, "no probe" would also be satisfied by a `den sh` that
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
// of one says nothing about the VM — and `den sh`, which creates nothing, has no
// "relaunch den to forward it" to offer. Silence, and no probe: `ssh-add -l`
// without a socket answers StateUnreachable, which would blame a variable the
// user never set.
func TestShDoesNotWarnWhenTheSSHSocketIsAbsent(t *testing.T) {
	// Set EMPTY rather than left alone: os.Getenv answers "" for both, and a test
	// relying on the machine having no agent would quietly stop exercising this.
	t.Setenv("SSH_AUTH_SOCK", "")
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	probed := false

	_, stderr, err := runShWithAgent(t, shDenHome(t, ""), f, func() sshagent.Result {
		probed = true
		return sshagent.Result{State: sshagent.StateUnreachable}
	}, "sh", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if probed {
		t.Error("the agent was probed with no socket to point it at")
	}
	if strings.Contains(stderr, "warning") {
		t.Errorf("stderr = %q, den sh must stay silent about a socket it cannot judge", stderr)
	}
}

// No live sandbox at all: the message cannot offer a list, it must SAY so.
// "(live: [])" would send the user looking for a typo in an empty list.
func TestShWithNoSandboxAtAll(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}

	_, err := executeCmdWithSbx(t, f, "sh", "missing")
	if err == nil {
		t.Fatal("an unknown sandbox name must produce an error")
	}
	if !strings.Contains(err.Error(), "no sandbox is running") {
		t.Errorf("the message must say no sandbox is running; got: %v", err)
	}
}
