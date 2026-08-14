package cli

import (
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sshagent"
)

// `den shell` is the login shell `den exec` opened until 2026-08-14, moved
// verbatim: `bash -l`, a tty unconditionally, and the workdir taken from the
// workspace the VM REPORTS. The `:ro` suffix in the fixture is not decorative
// — it separates b.Workdir() (which strips it) from b.Workspaces[0] (which
// would keep it); without it both implementations pass.
func TestShellAttachesALoginShellInTheWorkdir(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api:ro","/profile"]}]}`)},
	}}

	if _, err := executeCmdWithSbx(t, f, "shell", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Errorf("attaches = %v", f.Attaches)
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
func TestShellAttachesWithATtyNotARun(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api:ro","/profile"]}]}`)},
	}}

	if _, err := executeCmdWithSbx(t, f, "shell", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Errorf("the attach must be an Attach, with the full ordered argv; attaches: %v", f.Attaches)
	}
}

// noTTYRefusal is the message BOTH commands that can be asked for a terminless
// login shell must produce, byte for byte: `den shell` (shell.go) and
// `den spawn` with no command (internal/spawn/spawn.go). One contradiction must
// not read as two rules (spec §2).
//
// Spelled out in full here and in TestSpawnRefusesNoTTYWithNoCommand rather
// than asserted with strings.Contains, which is what both sides did until
// 2026-08-14: `Contains(err, "-T")` passes on any message naming the flag, so
// the identity the two production comments promise each other was held by
// nothing. Reword one side now and its own test fails, which is what sends the
// author to the other.
const noTTYRefusal = "-T asks for no terminal and no command asks for a shell, which needs one — " +
	"give a command after `--`, or drop -T"

// The refusal the spec moves here from `den exec`. -T and --no-tty are one flag
// with two spellings, so both reach it — and on EITHER side of the sandbox
// name, because `den shell` does not set SetInterspersed(false) (see
// newShellCmd's comment). That is the whole observable difference from
// `den exec`, so it is pinned here.
func TestShellRefusesNoTTY(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"-T before the name", []string{"shell", "-T", "api"}},
		{"-T after the name", []string{"shell", "api", "-T"}},
		{"--no-tty before the name", []string{"shell", "--no-tty", "api"}},
		{"--no-tty after the name", []string{"shell", "api", "--no-tty"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &sbx.Fake{}
			_, err := executeCmdWithSbx(t, f, tc.argv...)
			if err == nil {
				t.Fatal("-T on a login shell must be refused")
			}
			if err.Error() != noTTYRefusal {
				t.Errorf("the refusal must be den spawn's, byte for byte:\n got  %q\n want %q",
					err.Error(), noTTYRefusal)
			}
			if len(f.Calls) != 0 {
				t.Errorf("the refusal must land before anything is asked of sbx; calls = %v", f.Calls)
			}
		})
	}
}

// --workdir overrides the workspace the VM reported, on the shell exactly as on
// the command.
func TestShellWorkdirOverridesTheReportedWorkspace(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	if _, err := executeCmdWithSbx(t, f, "shell", "--workdir", "/srv", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/srv", "api", "bash", "-l") {
		t.Errorf("attaches = %v", f.Attaches)
	}
}

// `-w` must NOT be accepted: it is den spawn's worktree, and one letter meaning
// two things across sibling commands is the collision den refuses elsewhere.
func TestShellRefusesTheShortWorkdirFlag(t *testing.T) {
	f := &sbx.Fake{}
	if _, err := executeCmdWithSbx(t, f, "shell", "-w", "/srv", "api"); err == nil {
		t.Error("-w must not be a workdir on den shell")
	}
}

// A shell takes exactly one sandbox name. A second positional is not a command
// here — `den exec` is where commands go — so it is refused rather than
// silently ignored.
func TestShellRefusesASecondPositional(t *testing.T) {
	f := &sbx.Fake{}
	if _, err := executeCmdWithSbx(t, f, "shell", "api", "bash"); err == nil {
		t.Error("den shell takes one sandbox name")
	}
}

// THE constraint that makes the ssh-agent warning acceptable on this door at
// all: it reads the den home only to learn ssh.mode, and a den home it cannot
// read costs the user NOTHING — no error, no missing shell. The whole point of
// the command is that a broken ~/.den never stands between the user and a live
// sandbox; the warning is advisory, so it is what gives way, silently.
//
// The empty temp dir is exactly that state: no config.yaml at all.
func TestShellOpensWhenTheDenHomeCannotBeRead(t *testing.T) {
	execSocket(t)
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
	}}
	probed := false

	_, stderr, err := runExecWithAgent(t, t.TempDir(), f, func() sshagent.Result {
		probed = true
		return sshagent.Result{State: sshagent.StateEmpty}
	}, "shell", "api")
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

// The §9.1 freshness gate governs `den shell` exactly as it governed the shell
// `den exec` used to open: the user is about to run that agent.
func TestShellRefusesASandboxWhoseFreshnessGateFailed(t *testing.T) {
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api"]}]}`)},
		execGateRead("api"): {Output: execGateLog("api", "fail")},
	}}
	deps := Deps{Doctor: doctor.FakeDeps(), Sbx: f, Freshness: fakeGateOptions()}
	if _, _, err := executeCmdSeparateStreams(t, NewRootCmdWith(deps), "shell", "api"); err == nil {
		t.Fatal("a failed freshness gate must refuse the shell")
	}
}
