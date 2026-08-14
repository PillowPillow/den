package cli

import (
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/sbx"
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

// The refusal the spec moves here from `den exec`: it must stay identical, in
// the same words, to den spawn's (TestNoTTYReachesSpawnOptions,
// spawn_test.go). -T and --no-tty are one flag with two spellings, so both
// reach it — and on EITHER side of the sandbox name, because `den shell` does
// not set SetInterspersed(false) (see newShellCmd's comment). That is the whole
// observable difference from `den exec`, so it is pinned here.
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
			if !strings.Contains(err.Error(), "-T") {
				t.Errorf("the refusal must name the flag in play: %v", err)
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
