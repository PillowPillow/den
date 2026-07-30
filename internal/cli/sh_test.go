package cli

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
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
