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
