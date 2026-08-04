package spawn

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sshagent"
	"github.com/PillowPillow/den/internal/worktree"
)

// denTest builds a complete temporary ~/.den home backed by a real git repo.
func denTest(t *testing.T) (denHome, repo string) {
	t.Helper()
	return denTestSSH(t, "  mode: agent-forward\n")
}

// oneHostEgress is the `egress:` block for every test that doesn't exercise
// configuration drift.
const oneHostEgress = "  - github.com\n"

// denTestSSH lets the `ssh:` block vary — the only lever that adds a THIRD
// workspace, and so the only one that makes their order observable.
func denTestSSH(t *testing.T, sshBlock string) (denHome, repo string) {
	t.Helper()
	denHome = t.TempDir()
	repo = filepath.Join(t.TempDir(), "api")

	createRepo(t, repo)

	writeConfig(t, denHome, sshBlock, oneHostEgress)
	// Two kits declared, not zero: without them the mixin would be the argv's
	// only `--kit`, and "the mixin layers last" would trivially hold on its
	// own.
	//
	// The directories are CREATED: a stack pointing at kit paths that don't
	// exist on disk is exactly the defect TestSpawnRefusesAMissingKit guards
	// against.
	write(t, filepath.Join(denHome, "stacks", "devx", "stack.yaml"),
		"image: devx:v1\nkits: [transverse]\nkit: devx-kit\n")
	for _, kit := range []string{"transverse", "devx-kit"} {
		if err := os.MkdirAll(filepath.Join(denHome, "stacks", "devx", kit), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(denHome, "nests", "api.yaml"), "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	return denHome, repo
}

// createRepo makes a REAL git repo with one commit, the only state
// `git worktree add` can branch a new worktree from.
func createRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@example.test"},
		{"config", "user.name", "T"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", c...)
		cmd.Dir = path
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", c, err, out)
		}
	}
}

// writeConfig (re)writes the den home's config.yaml. Split out of
// denTestSSH so drift tests can REWRITE the cascade between two spawns — the
// only way to reproduce a config that moved under a VM that didn't.
func writeConfig(t *testing.T, denHome, sshBlock, egressBlock string) {
	t.Helper()
	write(t, filepath.Join(denHome, "config.yaml"), `agents:
  claude:
    config_dir: `+filepath.Join(denHome, "agents", "claude")+`
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }
    bin_dirs: ["$HOME/.local/bin"]
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
`+sshBlock+`worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
egress:
`+egressBlock)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// instantPolicy returns the default settle-loop settings on a SIMULATED
// clock that Sleep advances.
//
// A no-op Sleep next to a real time.Now is a BROKEN double: the clock never
// advances by an interval per round, the loop never hits its deadline, and
// it exits on the round bound instead — a caller-fault error that isn't a
// fail-closed. Hence the shared Sleep/Now clock, and the assertion on the
// FAILURE CAUSE in TestSpawnDoesNotAttachWhenPolicyFails.
func instantPolicy() policy.Options {
	o := policy.DefaultOptions()
	clock := time.Now()
	o.Sleep = func(d time.Duration) { clock = clock.Add(d) }
	o.Now = func() time.Time { return clock }
	return o
}

// instantFreshness is instantPolicy's twin for the §9.1 gate: the real budget,
// a clock that only moves when the loop sleeps. The gate then exhausts its
// patience in no wall-clock time at all, which is what every test that is NOT
// about the gate wants — a fake sbx answers nothing a dispatcher journal could
// be read out of, so the verdict is "has not reported yet" and den warns.
func instantFreshness() agent.GateOptions {
	o := agent.DefaultGateOptions()
	clock := time.Now()
	o.Sleep = func(d time.Duration) { clock = clock.Add(d) }
	o.Now = func() time.Time { return clock }
	return o
}

// fakeDeps returns a fake sbx that answers "no sandbox" then "everything
// allowed".
func fakeDeps() (*sbx.Fake, Deps) {
	return fakeDepsWithVerdict(`{"allowed": true}`)
}

func fakeDepsWithVerdict(verdict string) (*sbx.Fake, Deps) {
	f := &sbx.Fake{
		Responses: map[string]sbx.Response{
			"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
		},
		Default: sbx.Response{Output: []byte(verdict)},
	}
	return f, Deps{
		Sbx:       f,
		Git:       worktree.NewGit(),
		Policy:    instantPolicy(),
		Freshness: instantFreshness(),
		Out:       io.Discard,
	}
}

func TestSpawnRunsTheNominalSequence(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !f.HasCalled("create", "--name", "api", "--template", "devx:v1") {
		t.Errorf("a create must have happened; calls: %v", f.Calls)
	}
	if !f.HasCalled("policy", "check", "network", "--sandbox", "api", "--json", "github.com") {
		t.Errorf("the settle-loop must have run on the cascade's egress; calls: %v", f.Calls)
	}
	// HasAttached, not HasCalled: Calls conflates Run and Attach, and a Run in
	// place of an Attach would hand the user a mute shell, with no tty.
	if !f.HasAttached("exec", "-it", "-w", repo, "api", "bash", "-l") {
		t.Errorf("the attach must have happened; attaches: %v", f.Attaches)
	}
}

// D1 (T10, hostile config #14): Global.Validate() used to be called only by
// `den doctor`, so `worktree_layout: centrl` passed spawn unseen and
// silently fell back to `central`, changing worktree layout on a typo. The
// refusal must land before any side effect, or the user cleans up by hand.
func TestSpawnRefusesInvalidConfiguration(t *testing.T) {
	denHome, _ := denTest(t)
	write(t, filepath.Join(denHome, "config.yaml"), `agents:
  claude:
    config_dir: `+filepath.Join(denHome, "agents", "claude")+`
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: centrl
`)
	f, d := fakeDeps()

	err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d)
	if err == nil {
		t.Fatal("expected a refusal of `worktree_layout: centrl`, got nil")
	}
	if !strings.Contains(err.Error(), "centrl") {
		t.Errorf("error = %q, expected the offending value named", err.Error())
	}
	if len(f.Calls) != 0 || len(f.Attaches) != 0 {
		t.Errorf("no sbx call should precede the refusal; calls: %v, attaches: %v", f.Calls, f.Attaches)
	}
	// No disk side effect either: the agent profile is created by a
	// MkdirAll midway through the sequence.
	if _, err := os.Stat(filepath.Join(denHome, "agents", "claude")); err == nil {
		t.Error("the agent profile must not have been created before the refusal")
	}
}

// Ordering is a safety property: attaching before the policy is in place is
// exactly the half-working state spec §7 forbids.
func TestSpawnAttachesAfterTheSettleLoop(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	iCreate, iPolicy, iExec := -1, -1, -1
	for i, a := range f.Calls {
		if len(a) > 0 && a[0] == "create" && iCreate < 0 {
			iCreate = i
		}
		if len(a) > 0 && a[0] == "policy" && iPolicy < 0 {
			iPolicy = i
		}
		if len(a) > 0 && a[0] == "exec" {
			iExec = i
		}
	}
	if iCreate < 0 || iPolicy < 0 || iExec < 0 {
		t.Fatalf("create (%d), policy (%d) and exec (%d) must all have happened; calls: %v",
			iCreate, iPolicy, iExec, f.Calls)
	}
	if !(iCreate < iPolicy && iPolicy < iExec) {
		t.Errorf("expected order create (%d) < policy (%d) < attach (%d); calls: %v",
			iCreate, iPolicy, iExec, f.Calls)
	}
}

// End-to-end fail-closed: policy blocked ⇒ no attach.
func TestSpawnDoesNotAttachWhenPolicyFails(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDepsWithVerdict(`{"allowed": false}`)

	err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d)
	if err == nil {
		t.Fatal("a policy that doesn't pass must fail the spawn")
	}
	// The spawn must fail for the RIGHT reason. Settle has two error exits:
	// fail-closed (what we're testing) and the round bound, reserved for a
	// lying clock. Without this distinction, a broken clock double would
	// keep this test green without ever exercising the fail-closed path.
	if !strings.Contains(err.Error(), "fail-closed") {
		t.Errorf("the failure must be the policy's fail-closed; got: %v", err)
	}
	if strings.Contains(err.Error(), "caller fault") {
		t.Errorf("Settle hit its round bound (broken test clock), not the fail-closed; got: %v", err)
	}
	if len(f.Attaches) != 0 {
		t.Errorf("no attach must happen; attaches: %v", f.Attaches)
	}
}

// Spawn-or-attach (spec §11): a name that's already live is not an error.
func TestSpawnAttachesWithoutRecreatingWhenSandboxExists(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("no create must happen on a live sandbox; calls: %v", f.Calls)
	}
	// The settle-loop must run on THIS branch too — it's the one every spawn
	// after the first takes. Without this assertion, a settle-loop folded
	// into the `create` branch only would stay green while `den api`
	// attaches a shell without ever checking the policy.
	if !f.HasCalled("policy", "check", "network", "--sandbox", "api", "--json", "github.com") {
		t.Errorf("the settle-loop must also run on a live sandbox; calls: %v", f.Calls)
	}
	if !f.HasAttached("exec", "-it", "-w", repo, "api", "bash", "-l") {
		t.Errorf("the attach must happen; attaches: %v", f.Attaches)
	}
}

// It's the FULL name, worktree included, that must be looked up among live
// sandboxes. Searching on `o.Nest` alone would conflate `api` with
// `api.feat12` — undetectable as long as the only live-sandbox test has no
// worktree, since the two values then coincide.
//
// The expected `-w` is `/w`, the workspace the VM reports, not the worktree
// path the cascade would recompute. That expectation belongs to
// TestSpawnAttachesInTheWorkdirReportedByTheVM below; here it's only a side
// effect.
func TestSpawnLooksUpTheWorktreeNameAmongLiveSandboxes(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api.feat12","status":"running","workspaces":["/w"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat12"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("no create must happen on a live api.feat12; calls: %v", f.Calls)
	}
	if !f.HasAttached("exec", "-it", "-w", "/w", "api.feat12", "bash", "-l") {
		t.Errorf("the attach must target api.feat12; attaches: %v", f.Attaches)
	}
}

// D2: a LIVE sandbox's `-w` comes from the workspace the VM actually
// MOUNTS, never a path recomputed from the current configuration.
//
// A VM mounts the workspaces of its original `sbx create`: if the nest's
// first repo has since moved (or `-w` was added), the recomputed path
// simply doesn't exist inside it, and `sbx exec -w` fails — or worse, lands
// elsewhere. sbx.Sandbox.Workdir exists exactly for this.
//
// The fixture's `:ro` isn't decorative: it distinguishes `b.Workdir()`
// (which strips it) from `b.Workspaces[0]` (which would keep it), the two
// possible implementations.
func TestSpawnAttachesInTheWorkdirReportedByTheVM(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"running",` +
			`"workspaces":["/mounted/by/the/vm:ro","/profile"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "-w", "/mounted/by/the/vm", "api", "bash", "-l") {
		t.Errorf("-w must come from the VM's workspace; attaches: %v", f.Attaches)
	}
	// And definitely NOT the path the cascade recomputes: that's the defect.
	for _, a := range f.Attaches {
		if slices.Contains(a, repo) {
			t.Errorf("-w must not be the recomputed path %s; attach: %v", repo, a)
		}
	}
}

// D2's corollary, the one case nothing covered: a VM that mounts NO
// workspace has no workdir, and the attach must OMIT -w — never fall back
// to the path recomputed from configuration. Falling back there was
// previously unlocked by no test: behavior was correct but unverified.
func TestSpawnDoesNotInventAWorkdirWhenTheVMMountsNothing(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"running"}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasAttached("exec", "-it", "api", "bash", "-l") {
		t.Errorf("without a workspace, the attach must omit -w; attaches: %v", f.Attaches)
	}
	for _, a := range f.Attaches {
		if slices.Contains(a, "-w") {
			t.Errorf("no -w must be set; attach: %v", a)
		}
		if slices.Contains(a, repo) {
			t.Errorf("the recomputed path %s must not resurface; attach: %v", repo, a)
		}
	}
}

// D1: a sandbox found but NOT RUNNING is not a live sandbox.
//
// Before this check, sbx.Exists returned only a bool and discarded Status:
// `den api` on an `exited` VM printed "already live: attaching" then ran
// `sbx exec` against a stopped VM.
//
// F2: a STOPPED sandbox is resumed, not destroyed — sbx parks idle
// sandboxes after a few minutes, so it's the NORMAL state on returning to a
// `--detach` VM, and `sbx exec` restarts it transparently. The old refusal
// pointed at `den rm`, which destroys state the stop had preserved.
func TestSpawnResumesAStoppedSandbox(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("a stopped sandbox must be resumed, not refused: %v", err)
	}

	// Resuming = attaching. -w comes from the VM's workspaces, as on a live
	// VM: it's carried by its `create`, not by today's config.
	if !f.HasAttached("exec", "-it", "-w", "/w", "api", "bash", "-l") {
		t.Errorf("resuming must attach in the VM's workdir; attaches: %v", f.Attaches)
	}
	// And definitely no create: the name is held by a VM carrying work.
	if f.HasCalled("create") {
		t.Errorf("no create on a name already taken; calls: %v", f.Calls)
	}
	// Resuming takes several seconds: silent, it looks like a hang.
	if !strings.Contains(out.String(), "stopped") {
		t.Errorf("the resume must be announced; output:\n%s", out.String())
	}
	// What the message must NOT suggest anymore: F2's old defect, a remedy
	// that destroys the state the stop had just preserved.
	if strings.Contains(out.String(), "den rm") {
		t.Errorf("a resume must not suggest destroying the VM; output:\n%s", out.String())
	}
}

// #17: `--detach` on a stopped sandbox may NOT call it "ready".
//
// The attach branch restarts nothing — no mixin is reapplied, no `sbx exec`
// runs, and the settle-loop answers on a stopped VM too (`sbx policy check`
// does not need it running, smoke #2 §6). den printed "ready (detached)" over a
// sandbox `sbx ls --json` still reported as `stopped`, and the scripted
// follow-up `den X --detach && den ports X` walked into an sbx 500.
//
// Two-sided: forbidding the word alone would go green on a message that says
// nothing at all. The positive half pins what the line must carry — that the
// sandbox stays stopped, that its state survives, and that the next attach
// starts it.
func TestSpawnDetachedDoesNotCallAStoppedSandboxReady(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("--detach on a stopped sandbox: %v", err)
	}

	if strings.Contains(out.String(), "ready") {
		t.Errorf("nothing restarted the VM, so it is not ready: den may not claim it; output:\n%s",
			out.String())
	}
	for _, want := range []string{"stays stopped", "state preserved", "den sh api", "den ports api"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the detached line must state %q; output:\n%s", want, out.String())
		}
	}
	// Still no exec: --detach's contract is not to enter the VM, and waking it
	// here would buy a truth sbx undoes in about 45 s of idleness.
	if f.HasCalled("exec") {
		t.Errorf("--detach must start nothing; calls: %v", f.Calls)
	}
}

// The other side of the same line: a sandbox den just CREATED is running, and
// "ready (detached)" is the truth there. The status check must not have turned
// the ordinary spawn into a hedge.
func TestSpawnDetachedStillCallsAFreshSandboxReady(t *testing.T) {
	denHome, _ := denTest(t)
	_, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("--detach on a fresh sandbox: %v", err)
	}
	if !strings.Contains(out.String(), "ready (detached)") {
		t.Errorf("a sandbox just created is running: the line must say ready; output:\n%s", out.String())
	}
}

// gateLog scripts the kit-journal read of a sandbox, leaving every other call
// on the Fake's Default. The key is the exact argv agent.WaitFreshness issues,
// which is what makes these tests fail loudly if that argv ever moves.
func gateLog(f *sbx.Fake, sandbox, log string) {
	f.Responses["exec "+sandbox+" cat "+agent.KitLogPath] = sbx.Response{Output: []byte(log)}
}

// gatePassed is a complete dispatcher run in which den's mixin passed.
func gatePassed(sandbox string) string {
	path := "/etc/durable-startup.d/002-startup-" + agent.MixinName(sandbox) + "/000-cmd.sh"
	return "=== dispatcher run 2026-07-31T15:34:24Z ===\n> " + path + "\nok " + path +
		"\n=== dispatcher complete ===\n"
}

// gateFailed is smoke #2's measured failure path (§D4): an agent whose update
// command exits non-zero, the fail-closed abort after three attempts.
func gateFailed(sandbox string) string {
	path := "/etc/durable-startup.d/002-startup-" + agent.MixinName(sandbox) + "/000-cmd.sh"
	return "=== dispatcher run 2026-07-31T15:34:24Z ===\n> " + path +
		"\nagent broken: FATAL update failed after 3 attempts (fail-closed)\nfail " + path +
		" exit=1\n"
}

// #18, the half that has no cost and no arbitration: a gate that has already
// FAILED refuses the spawn, on both paths.
//
// This was measured and is the sharpest half of the defect: with an agent whose
// freshness command fails by construction, the fail-closed gate closed, den had
// already returned "ready" and exit 0 — and it never caught up. A re-attach on
// the same sandbox printed "already live: attaching … ready (detached)", exit 0,
// with nothing on either stream about the failure, then or ever. §9.1 promises
// "a sandbox never starts with a stale agent"; the agent had never been updated
// at all.
func TestSpawnRefusesASandboxWhoseFreshnessGateFailed(t *testing.T) {
	for _, detach := range []bool{false, true} {
		name := "attach"
		if detach {
			name = "detach"
		}
		t.Run(name, func(t *testing.T) {
			denHome, _ := denTest(t)
			f, d := fakeDeps()
			gateLog(f, "api", gateFailed("api"))

			err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: detach}, d)
			if err == nil {
				t.Fatal("§9.1 is fail-closed: a sandbox whose agent was never updated must be refused")
			}
			// The log line travels with the refusal — §9.1 makes the journal the
			// diagnosis, and a message without it sends the user into the VM to
			// read what den already read.
			for _, want := range []string{"§9.1", "exit=1", agent.KitLogPath} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must carry %q; got: %v", want, err)
				}
			}
			// And no shell is opened into it.
			if f.HasAttached("exec", "-it") {
				t.Errorf("a refused gate must not attach; attaches: %v", f.Attaches)
			}
		})
	}
}

// A gate that PASSED is silent: the ordinary outcome, and announcing it on
// every spawn would bury the lines that mean something.
func TestSpawnSaysNothingWhenTheFreshnessGatePassed(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out
	gateLog(f, "api", gatePassed("api"))

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("a passing gate must not fail the spawn: %v", err)
	}
	for _, unwanted := range []string{"warning", "note:", "waiting for agent freshness"} {
		if strings.Contains(out.String(), unwanted) {
			t.Errorf("a passing gate is silent, %q leaked; output:\n%s", unwanted, out.String())
		}
	}
}

// THE ARBITRATION, in one test: den waits on the attach path and does not wait
// under `--detach`.
//
// Waiting costs the difference between a 7.6 s spawn and a ~42 s one (measured),
// so where it waits was decided rather than assumed: a user about to run the
// agent gets a fresh one, a script that will not touch it gets its seven
// seconds back and a note saying the verdict is not in yet.
//
// The number of journal READS is what proves it, not the message: a single read
// cannot be a wait, and a poll cannot be anything else.
func TestSpawnWaitsForTheFreshnessGateOnlyWhenItAttaches(t *testing.T) {
	pending := "=== dispatcher run 2026-07-31T15:34:24Z ===\n"

	detached, dd := fakeDeps()
	var detachedOut bytes.Buffer
	dd.Out = &detachedOut
	gateLog(detached, "api", pending)
	denHome, _ := denTest(t)
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, dd); err != nil {
		t.Fatalf("--detach must not fail on a gate that has not reported: %v", err)
	}
	if n := countCalls(detached, "exec", "api", "cat", agent.KitLogPath); n != 1 {
		t.Errorf("--detach must read the journal ONCE, not wait for it; reads: %d", n)
	}
	if !strings.Contains(detachedOut.String(), "note:") {
		t.Errorf("--detach must say the verdict is not in yet; output:\n%s", detachedOut.String())
	}
	if strings.Contains(detachedOut.String(), "waiting for agent freshness") {
		t.Errorf("--detach announces no wait, because it does not wait; output:\n%s",
			detachedOut.String())
	}

	attaching, ad := fakeDeps()
	var attachingOut bytes.Buffer
	ad.Out = &attachingOut
	gateLog(attaching, "api", pending)
	denHome2, _ := denTest(t)
	if err := Spawn(context.Background(), denHome2, Options{Nest: "api"}, ad); err != nil {
		t.Fatalf("a gate that never reports must not fail the spawn: %v", err)
	}
	if n := countCalls(attaching, "exec", "api", "cat", agent.KitLogPath); n < 2 {
		t.Errorf("the attach path must WAIT for the gate, polling the journal; reads: %d", n)
	}
	if !strings.Contains(attachingOut.String(), "waiting for agent freshness") {
		t.Errorf("a wait of tens of seconds must be announced; output:\n%s", attachingOut.String())
	}
	// A budget that runs out is a note, never a refusal: den waited what it
	// promised, and a dispatcher still working is no evidence of a stale agent.
	if !attaching.HasAttached("exec", "-it") {
		t.Errorf("a gate still silent at the budget must not block the attach; attaches: %v",
			attaching.Attaches)
	}
}

// The gate is SKIPPED on a sandbox den has decided to leave stopped: reading the
// journal is an `sbx exec`, which restarts the VM, and waking one to inspect it
// would contradict the line `--detach` prints about that very sandbox (#17).
// Nothing is lost — the dispatcher re-runs on the next restart (measured), so
// the gate is evaluated exactly when the sandbox comes back.
func TestSpawnDoesNotWakeAStoppedSandboxToReadTheFreshnessGate(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"stopped","workspaces":["/w"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("--detach on a stopped sandbox: %v", err)
	}
	if n := countCalls(f, "exec", "api", "cat", agent.KitLogPath); n != 0 {
		t.Errorf("reading the journal restarts the VM: den must not, on a sandbox it just said "+
			"stays stopped; reads: %d", n)
	}
	if strings.Contains(out.String(), "freshness") {
		t.Errorf("den checked nothing and must claim nothing; output:\n%s", out.String())
	}
}

// countCalls counts the invocations whose argv starts with this prefix — the
// counting sibling of Fake.HasCalled, which only answers "at least one" and so
// cannot tell a single read apart from a poll.
func countCalls(f *sbx.Fake, prefix ...string) int {
	n := 0
	for _, c := range f.Calls {
		if len(c) >= len(prefix) && slices.Equal(c[:len(prefix)], prefix) {
			n++
		}
	}
	return n
}

// The allowlist stays FAIL-CLOSED for everything else: sbx status values
// other than `running` aren't recognized. A denylist would attach on any
// status a later sbx version might introduce, including an error status.
// The accepted cost: a transient startup status makes a too-early
// `den api` fail, with a message naming the status read.
func TestSpawnRefusesASandboxThatIsNotRunning(t *testing.T) {
	for _, status := range []string{"exited", "paused", "Running", ""} {
		t.Run("status="+status, func(t *testing.T) {
			denHome, _ := denTest(t)
			f, d := fakeDeps()
			f.Responses["ls --json"] = sbx.Response{
				Output: []byte(`{"sandboxes":[{"name":"api","status":"` + status +
					`","workspaces":["/w"]}]}`),
			}

			err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d)
			if err == nil {
				t.Fatalf("a status %q must not be treated as live", status)
			}
			// The message must render the status READ: without it, the user
			// doesn't know what den is complaining about or what to type
			// next.
			//
			// strconv.Quote, not the bare status: on the status="" sub-case,
			// `strings.Contains(err, "")` is trivially true and asserts
			// nothing. The quoted form is what the message actually renders
			// (`%q`).
			if !strings.Contains(err.Error(), strconv.Quote(status)) ||
				!strings.Contains(err.Error(), strconv.Quote("running")) {
				t.Errorf("the message must render the status read and the one expected; got: %v", err)
			}
			if !strings.Contains(err.Error(), "api") {
				t.Errorf("the message must name the sandbox; got: %v", err)
			}
			if len(f.Attaches) != 0 {
				t.Errorf("no attach in a stopped VM; attaches: %v", f.Attaches)
			}
			// No create (the name is taken) and no settle-loop: den stops
			// dead.
			if f.HasCalled("create") {
				t.Errorf("no create must be attempted on a name already taken; calls: %v", f.Calls)
			}
		})
	}
}

// D3: nothing reapplies a mixin to a running VM.
//
// A NARROWED `egress:` passes the settle-loop silently: the wide policy the
// VM carries from its create obviously allows the narrow list submitted to
// it. The user believes their sandbox tightened when it stayed open. (The
// opposite, widening, fails cleanly on the settle-loop.)
//
// ORDERING TRAP, and the reason this test exists: Spawn REWRITES the mixin
// on every pass (step 5), BEFORE the spawn-or-attach branch. A comparison
// made after that write compares the mixin to itself and never detects
// anything, with a fully green suite. The killer mutation: moving ReadMixin
// after WriteMixin.
func TestSpawnWarnsWhenConfigHasDriftedUnderTheSandbox(t *testing.T) {
	denHome, repo := denTest(t)
	writeConfig(t, denHome, "  mode: agent-forward\n", "  - api.anthropic.com\n  - github.com\n")

	// First pass: the sandbox is created, and THIS is the mixin it carries.
	f, d := fakeDeps()
	log := &bytes.Buffer{}
	d.Out = log
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("first spawn: unexpected error: %v", err)
	}
	if !f.HasCalled("create", "--name", "api") {
		t.Fatalf("the first spawn must create the sandbox; calls: %v", f.Calls)
	}

	// The configuration narrows. The VM doesn't move.
	writeConfig(t, denHome, "  mode: agent-forward\n", "  - github.com\n")
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}
	log.Reset()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("second spawn: unexpected error: %v", err)
	}
	out := log.String()
	if !strings.Contains(out, "api.anthropic.com") {
		t.Errorf("the warning must NAME the egress that vanished from the config;\n%s", out)
	}
	if !strings.Contains(out, "warning") {
		t.Errorf("the drift must be announced as a warning;\n%s", out)
	}
	// We WARN, we don't refuse: refusing would break a `den api` that
	// worked yesterday over a harmless YAML change (decision settled, see
	// T12 §6).
	if len(f.Attaches) != 1 {
		t.Errorf("drift warns then attaches; attaches: %v", f.Attaches)
	}
}

// The essential counterpart: no drift, no warning. A warning printed on
// EVERY attach would stop being read at all, and would let the test above
// pass without proving anything.
func TestSpawnDoesNotWarnWithoutDrift(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := fakeDeps()
	log := &bytes.Buffer{}
	d.Out = log
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("first spawn: unexpected error: %v", err)
	}

	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}
	log.Reset()
	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("second spawn: unexpected error: %v", err)
	}
	if out := log.String(); strings.Contains(out, "warning") {
		t.Errorf("an unchanged configuration must report nothing;\n%s", out)
	}
}

// The CREATE branch must never warn: it's the create that LAYS DOWN the
// mixin, so it can't have drifted from itself.
//
// The tricky case is a cache/ surviving the sandbox: spec §3 declares
// cache/ reconstructible and den doesn't purge it, so `sbx rm` followed by
// `den api` finds the DEFUNCT sandbox's mixin still on disk. A warning
// hoisted outside the "live" branch would fire there, on a sandbox that
// actually gets the exact configuration.
func TestSpawnDoesNotWarnOnTheCreateBranch(t *testing.T) {
	denHome, _ := denTest(t)
	writeConfig(t, denHome, "  mode: agent-forward\n", "  - api.anthropic.com\n  - github.com\n")

	f, d := fakeDeps()
	log := &bytes.Buffer{}
	d.Out = log
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("first spawn: unexpected error: %v", err)
	}

	// The config changes, the sandbox is gone (`sbx rm`), the cache remains.
	writeConfig(t, denHome, "  mode: agent-forward\n", "  - github.com\n")
	log.Reset()
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("second spawn: unexpected error: %v", err)
	}
	if !f.HasCalled("create", "--name", "api") {
		t.Fatalf("the second spawn must create; calls: %v", f.Calls)
	}
	if out := log.String(); strings.Contains(out, "warning") {
		t.Errorf("a create lays down the mixin: nothing to report;\n%s", out)
	}
}

// The on-disk mixin is the create's REFERENCE: it must not be rewritten
// while the sandbox is already live.
//
// Otherwise drift erases itself: the first `den api` warns and overwrites
// the reference along the way, and the second stays silent — even though
// the VM still hasn't moved. The costliest defect of the lot: it makes
// detection MUTE exactly where it matters, without ever failing anything.
func TestSpawnDoesNotRewriteTheMixinOfALiveSandbox(t *testing.T) {
	denHome, repo := denTest(t)
	writeConfig(t, denHome, "  mode: agent-forward\n", "  - api.anthropic.com\n  - github.com\n")

	f, d := fakeDeps()
	log := &bytes.Buffer{}
	d.Out = log
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("first spawn: unexpected error: %v", err)
	}
	spec := filepath.Join(denHome, "cache", "mixins", "api", "spec.yaml")
	reference, err := os.ReadFile(spec)
	if err != nil {
		t.Fatalf("create must have written %s: %v", spec, err)
	}

	writeConfig(t, denHome, "  mode: agent-forward\n", "  - github.com\n")
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}

	// Two attaches in a row: the second is the one that would expose a
	// reference clobbered by the first.
	for round := 1; round <= 2; round++ {
		log.Reset()
		if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
			t.Fatalf("round %d: unexpected error: %v", round, err)
		}
		after, err := os.ReadFile(spec)
		if err != nil {
			t.Fatalf("round %d: reading %s: %v", round, spec, err)
		}
		if string(after) != string(reference) {
			t.Fatalf("round %d: the reference mixin was rewritten on a live sandbox;\n"+
				"before:\n%s\nafter:\n%s", round, reference, after)
		}
		if !strings.Contains(log.String(), "api.anthropic.com") {
			t.Errorf("round %d: the drift must stay reported;\n%s", round, log.String())
		}
	}
}

// A MISSING reference must be announced too.
//
// `rm -rf ~/.den/cache` — an operation spec §3 declares SAFE — permanently
// disabled drift detection for that sandbox: the attach branch never
// re-lays the reference, so the silence wasn't "once", it was "forever".
// The "first spawn" that would justify silence never takes this path: it
// goes through create. Hence the TWO rounds below — the second proves
// nothing closes the gap on its own.
//
// absenceMarkers: text that belongs ONLY to the missing-reference message.
// The two tests below use it as a mirror — one requires its presence, the
// other its absence — and that pair alone locks in that den renders two
// DIFFERENT messages for two different situations.
var absenceMarkers = []string{"no configuration reference", "purged cache"}

func TestSpawnReportsAMissingReferenceAfterCachePurge(t *testing.T) {
	denHome, repo := denTest(t)
	writeConfig(t, denHome, "  mode: agent-forward\n", "  - api.anthropic.com\n  - github.com\n")

	f, d := fakeDeps()
	log := &bytes.Buffer{}
	d.Out = log
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("first spawn: unexpected error: %v", err)
	}

	// `rm -rf ~/.den/cache`, and the config narrows behind the VM's back.
	if err := os.RemoveAll(filepath.Join(denHome, "cache")); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, denHome, "  mode: agent-forward\n", "  - github.com\n")
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}

	for round := 1; round <= 2; round++ {
		log.Reset()
		if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
			t.Fatalf("round %d: unexpected error: %v", round, err)
		}
		out := log.String()
		if !strings.Contains(out, "can't be checked") {
			t.Errorf("round %d: a missing reference must be reported;\n%s", round, out)
		}
		if !strings.Contains(out, "warning") {
			t.Errorf("round %d: this must be a warning;\n%s", round, out)
		}
		// And the message must be the ABSENCE one, not the corrupt-file
		// one. This distinction is the only consumer of the %w on
		// os.ErrNotExist: without it, a refactor could collapse the two
		// messages and leave
		// TestReadMixinAbsentIsDistinguishableFromABrokenRead green while
		// proving a distinction nobody exploits anymore.
		for _, marker := range absenceMarkers {
			if !strings.Contains(out, marker) {
				t.Errorf("round %d: the ABSENCE message must contain %q;\n%s", round, marker, out)
			}
		}
	}
	// And the attach happens every round: not knowing doesn't block.
	if len(f.Attaches) != 2 {
		t.Errorf("every round must attach; attaches: %v", f.Attaches)
	}
}

// An UNREADABLE reference must be announced too: den can't answer the
// question, and staying silent about it would read as "nothing changed".
func TestSpawnReportsUnverifiableDrift(t *testing.T) {
	denHome, repo := denTest(t)
	write(t, filepath.Join(denHome, "cache", "mixins", "api", "spec.yaml"), "\tnot yaml")

	f, d := fakeDeps()
	log := &bytes.Buffer{}
	d.Out = log
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := log.String()
	if !strings.Contains(out, "can't be checked") {
		t.Errorf("an unreadable reference must be reported as such;\n%s", out)
	}
	// The MIRROR of the absence test: a corrupt file is not a purged cache,
	// and sending the user to the wrong cause makes them look in the wrong
	// place.
	for _, marker := range absenceMarkers {
		if strings.Contains(out, marker) {
			t.Errorf("a CORRUPT file must not render the absence message (%q);\n%s", marker, out)
		}
	}
	// And the attach happens: unverifiable drift doesn't block.
	if len(f.Attaches) != 1 {
		t.Errorf("the attach must happen; attaches: %v", f.Attaches)
	}
}

func TestSpawnWithWorktree(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat12"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !f.HasCalled("create", "--name", "api.feat12") {
		t.Errorf("the name must carry the worktree; calls: %v", f.Calls)
	}
	worktreePath := filepath.Join(denHome, "worktrees", "feat12", "api")
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("the worktree must exist at %s: %v", worktreePath, err)
	}
	// And it's the worktree, not the repo, that's mounted — as the first
	// positional.
	ws := workspacesOf(callStartingWith(f, "create"))
	if len(ws) == 0 || ws[0] != worktreePath {
		t.Errorf("the worktree must be the first workspace; workspaces = %v", ws)
	}
	// The worktreed name must travel through the WHOLE sequence: a
	// settle-loop scoped on plain "api" would validate another sandbox's
	// policy.
	if !f.HasCalled("policy", "check", "network", "--sandbox", "api.feat12", "--json", "github.com") {
		t.Errorf("the settle-loop must be scoped on api.feat12; calls: %v", f.Calls)
	}
	if !f.HasAttached("exec", "-it", "-w", worktreePath, "api.feat12", "bash", "-l") {
		t.Errorf("the attach must open in the worktree; attaches: %v", f.Attaches)
	}
}

// F1: a worktree mounted ALONE is a worktree where git is dead.
//
// A linked worktree's `.git` is not a directory but a file reading
// `gitdir: <repo>/.git/worktrees/<name>`, pointing into the main repo —
// which den didn't mount. Every git command in the microVM failed with
// "fatal: not a git repository": no status, diff, commit or push, the
// central use case of `-w`.
//
// What's mounted is the COMMON GIT DIR (`<repo>/.git`), not the whole
// repo: it carries the admin dir, objects and refs — everything a commit
// needs — while keeping the main worktree invisible in the VM, exactly the
// isolation `-w` exists for. Mounting the whole repo would also fix git,
// but re-exposes the main worktree WRITABLE.
//
// One mount per repo, not a single one: `-w` propagates the worktree to
// EVERY repo of the nest (spec §13.4), each with its own common git dir.
func TestSpawnMountsEachRepoGitDirWithAWorktree(t *testing.T) {
	denHome, repoA := denTest(t)
	repoB := filepath.Join(t.TempDir(), "web")
	createRepo(t, repoB)
	write(t, filepath.Join(denHome, "nests", "api.yaml"),
		"stack: devx\nrepos:\n  - { path: "+repoA+" }\n  - { path: "+repoB+" }\n")
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat12"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ws := workspacesOf(callStartingWith(f, "create"))
	expected := []string{
		filepath.Join(denHome, "worktrees", "feat12", "api"),
		filepath.Join(denHome, "worktrees", "feat12", "web"),
		resolvedGitDir(t, repoA),
		resolvedGitDir(t, repoB),
		filepath.Join(denHome, "agents", "claude"),
	}
	if !slices.Equal(ws, expected) {
		t.Errorf("workspaces = %v,\nexpected      %v", ws, expected)
	}

	// The assertion that gives the resolved form above its REASON, which no
	// `filepath.Join(repo, ".git")` would satisfy: the mounted path must be
	// the one the worktree's `.git` file designates. The two can differ —
	// on macOS /var is a symlink to /private/var, and git writes the
	// resolved form — and inside the microVM there's no symlink left to
	// bridge the gap: mounting the unresolved form would leave `gitdir:`
	// pointing at nothing, exactly the failure this test guards against.
	for i, repo := range []string{repoA, repoB} {
		wt := ws[i]
		link, err := os.ReadFile(filepath.Join(wt, ".git"))
		if err != nil {
			t.Fatalf("%s: a linked worktree's .git must be a file: %v", wt, err)
		}
		target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(link)), "gitdir:"))
		mounted := ws[2+i]
		if !strings.HasPrefix(target, mounted+string(filepath.Separator)) {
			t.Errorf("worktree of %s: `gitdir:` points at %q, outside the mounted workspace %q — "+
				"inside the microVM this path won't resolve", repo, target, mounted)
		}
	}
}

// resolvedGitDir returns a repo's `.git` path the way git itself names it.
func resolvedGitDir(t *testing.T, repo string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(real, ".git")
}

// F1, the other half: a sandbox created BEFORE the fix is still running,
// and it doesn't mount the `.git` dirs. Nothing remounts a live VM, and
// reportDrift is blind to this case (the mixin hasn't changed): without
// this signal, the user reattaches to a VM with dead git and only finds
// out on their first git command — the silent failure F1 fixes.
func TestSpawnReportsAVMMissingGitDirs(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out
	wt := filepath.Join(denHome, "worktrees", "feat12", "api")
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api.feat12","status":"running","workspaces":["` +
			wt + `"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat12"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// WARN, don't refuse: the VM might carry work in progress, and the
	// user may have good reasons to return to it without git.
	if !f.HasAttached("exec", "-it", "-w", wt, "api.feat12", "bash", "-l") {
		t.Errorf("den must still attach; attaches: %v", f.Attaches)
	}
	if !strings.Contains(out.String(), "git") {
		t.Errorf("den must report that git will be inoperative; output:\n%s", out.String())
	}
	// The remedy is destruction: nothing remounts a live VM.
	if !strings.Contains(out.String(), "den rm api.feat12") {
		t.Errorf("the message must give the exact remedy; output:\n%s", out.String())
	}
}

// The counterpart, which keeps the warning from firing on correct VMs: a
// sandbox that does mount its git dirs must trigger nothing.
func TestSpawnReportsNothingWhenGitDirsAreMounted(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out
	wt := filepath.Join(denHome, "worktrees", "feat12", "api")
	// The `:ro` is a mount option, not part of the path: it must not break
	// the comparison.
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api.feat12","status":"running","workspaces":["` +
			wt + `","` + resolvedGitDir(t, repo) + `:ro"]}]}`),
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat12"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "den rm") {
		t.Errorf("no warning on a correct VM; output:\n%s", out.String())
	}
}

// The counterpart: WITHOUT a worktree, the whole repo is already mounted,
// `.git` included. An extra mount would be redundant, and worse, would
// mask the real reason for the first one — a `.git` mounted "for git" when
// git already worked would spread to cases where it has no business
// being.
func TestSpawnMountsNoGitDirWithoutAWorktree(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ws := workspacesOf(callStartingWith(f, "create"))
	expected := []string{repo, filepath.Join(denHome, "agents", "claude")}
	if !slices.Equal(ws, expected) {
		t.Errorf("workspaces = %v, expected %v", ws, expected)
	}
}

// LOCKED INVARIANT: the FIRST workspace must be the repo (or its
// worktree).
//
// sbx.Sandbox.Workdir takes `sbx ls`'s first workspace as the working
// directory, and nothing at its level can verify the caller kept it
// first — this is the one place that builds the list, so the contract
// must be locked here. The agent profile (always present) and the SSH dir
// (mount mode) are the two natural candidates to displace it.
func TestSpawnMountsTheRepoBeforeTheAgentProfileAndSSH(t *testing.T) {
	// The directory is CREATED: a version mounting a nonexistent path is
	// exactly the defect TestSpawnRefusesAMissingSSHDir guards against.
	sshDir := filepath.Join(t.TempDir(), "ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	denHome, repo := denTestSSH(t, "  mode: mount\n  dir: "+sshDir+"\n")
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ws := workspacesOf(callStartingWith(f, "create"))
	expected := []string{repo, filepath.Join(denHome, "agents", "claude"), sshDir}
	if !slices.Equal(ws, expected) {
		t.Errorf("workspaces = %v, expected %v", ws, expected)
	}
	// The same contract seen from the other end: this first workspace
	// becomes the attach's -w.
	if !f.HasAttached("exec", "-it", "-w", repo, "api", "bash", "-l") {
		t.Errorf("the attach must open in the repo; attaches: %v", f.Attaches)
	}
}

// D5: in `ssh.mode: mount`, ssh.dir becomes a workspace, so it goes
// VERBATIM into `sbx create`'s argv. Plan invariant #3: never pass sbx a
// path den hasn't guaranteed exists — a missing path becomes a mount of an
// empty directory that OVERWRITES the user's view of their keys.
// Validate() already covers "ssh.dir not declared"; this is "declared but
// absent from disk", visible only to a system probe.
func TestSpawnRefusesAMissingSSHDir(t *testing.T) {
	sshDir := filepath.Join(t.TempDir(), "ssh-never-created")
	denHome, _ := denTestSSH(t, "  mode: mount\n  dir: "+sshDir+"\n")
	f, d := fakeDeps()

	err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d)
	if err == nil {
		t.Fatal("a missing ssh.dir must be refused, got nil")
	}
	if !strings.Contains(err.Error(), sshDir) {
		t.Errorf("error = %q, expected the full path of the missing directory", err.Error())
	}
	// Refused BEFORE any side effect, like the missing repos.
	if len(f.Calls) != 0 || len(f.Attaches) != 0 {
		t.Errorf("no sbx call should precede the refusal; calls: %v, attaches: %v", f.Calls, f.Attaches)
	}
	if _, err := os.Stat(filepath.Join(denHome, "agents", "claude")); err == nil {
		t.Error("the agent profile must not have been created before the refusal")
	}
}

// The counterpart: modes that mount NOTHING must not start requiring an
// ssh.dir on disk. Without this case, a check hoisted outside
// `mode == "mount"` would break agent-forward and none unnoticed.
func TestSpawnDoesNotRequireSSHDirOutsideMountMode(t *testing.T) {
	for _, mode := range []string{"agent-forward", "none"} {
		t.Run(mode, func(t *testing.T) {
			denHome, _ := denTestSSH(t, "  mode: "+mode+"\n")
			_, d := fakeDeps()
			if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
				t.Fatalf("mode %q: unexpected error: %v", mode, err)
			}
		})
	}
}

// D3: `none` and `agent-forward` add NO workspace, by design, not by
// omission. The distinction isn't theoretical: an `if mode == "mount"`
// with no `else` leaves nobody able to tell "nothing to do" from
// "forgotten case" apart, and that ambiguity produced the (false)
// diagnosis that every default sandbox ships without SSH access —
// agent-forward actually relies on sbx inheriting SSH_AUTH_SOCK, proved by
// internal/sbx's TestExecRunTransmitsDenEnvironment.
//
// What this test locks in, matching spawn.go's comment: the three modes
// mount the same workspaces, except for ssh.dir, which belongs to mount
// alone.
func TestSpawnAddsNoWorkspaceOutsideMountMode(t *testing.T) {
	// ssh.dir is DECLARED and EXISTS in all three configurations. Without
	// that, "agent-forward doesn't mount ssh.dir" would be indistinguishable
	// from "there was nothing to mount", and a mutation hoisting the append
	// outside `mount` would stay invisible.
	sshDir := filepath.Join(t.TempDir(), "ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// The list is NORMALIZED before comparison: denTestSSH rebuilds a den
	// home and repo on every call, so raw paths differ across modes and
	// can't be compared directly. What's compared is the list's SHAPE —
	// which roles, in which order — exactly the property under test. An
	// unexpected path surfaces as-is rather than being swallowed.
	shapeOf := func(mode string) []string {
		t.Helper()
		denHome, repo := denTestSSH(t, "  mode: "+mode+"\n  dir: "+sshDir+"\n")
		f, d := fakeDeps()
		if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
			t.Fatalf("mode %q: unexpected error: %v", mode, err)
		}
		roles := map[string]string{
			repo: "<repo>",
			filepath.Join(denHome, "agents", "claude"): "<agent-profile>",
			sshDir: "<ssh.dir>",
		}
		var shape []string
		for _, w := range workspacesOf(callStartingWith(f, "create")) {
			role, known := roles[w]
			if !known {
				role = "<unknown:" + w + ">"
			}
			shape = append(shape, role)
		}
		return shape
	}

	none := shapeOf("none")
	agentForward := shapeOf("agent-forward")
	mount := shapeOf("mount")

	if !slices.Equal(agentForward, none) {
		t.Errorf("agent-forward mounts %v, none mounts %v: expected exactly the SAME list — "+
			"agent-forward adds no workspace, it relies on sbx inheriting SSH_AUTH_SOCK",
			agentForward, none)
	}
	// The comparison above would also pass for two empty lists. The next
	// two assertions say WHAT each mode mounts.
	if expected := []string{"<repo>", "<agent-profile>"}; !slices.Equal(agentForward, expected) {
		t.Errorf("agent-forward mounts %v, expected %v", agentForward, expected)
	}
	if expected := []string{"<repo>", "<agent-profile>", "<ssh.dir>"}; !slices.Equal(mount, expected) {
		t.Errorf("mount mounts %v, expected %v", mount, expected)
	}
	if len(mount) != len(agentForward)+1 {
		t.Errorf("mount mounts %d workspace(s) and agent-forward %d: expected exactly one more, "+
			"and that one is ssh.dir", len(mount), len(agentForward))
	}
}

// F3: kits go into `sbx create`'s `--kit` argv, exactly as ssh.dir goes in
// as a workspace — same plan invariant #3 (never pass sbx a path den
// hasn't guaranteed). The asymmetry was the defect: kit checks lived only
// in `doctor`, so `den api` exited 0 and sent sbx nonexistent paths for
// anyone who skipped `den doctor`.
func TestSpawnRefusesAMissingKit(t *testing.T) {
	for _, kit := range []string{"transverse", "devx-kit"} { // plural `kits:`, then singular `kit:`
		t.Run(kit, func(t *testing.T) {
			denHome, _ := denTest(t)
			missing := filepath.Join(denHome, "stacks", "devx", kit)
			if err := os.RemoveAll(missing); err != nil {
				t.Fatal(err)
			}
			f, d := fakeDeps()

			err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d)
			if err == nil {
				t.Fatal("a missing kit must be refused, got nil")
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("error = %q, expected the full path of the missing kit", err.Error())
			}
			// The stack that declares it: with several stacks, a bare path
			// doesn't say which stack.yaml to fix.
			if !strings.Contains(err.Error(), "devx") {
				t.Errorf("error = %q, expected the offending stack named", err.Error())
			}
			// Refused BEFORE any side effect, like the repos and ssh.dir.
			if len(f.Calls) != 0 || len(f.Attaches) != 0 {
				t.Errorf("no sbx call should precede the refusal; calls: %v, attaches: %v",
					f.Calls, f.Attaches)
			}
			if _, err := os.Stat(filepath.Join(denHome, "agents", "claude")); err == nil {
				t.Error("the agent profile must not have been created before the refusal")
			}
		})
	}
}

// An EMPTY entry in `kits:` (plural) must be ignored, as it already is by
// doctor and by sbx.CreateArgv. F3's first pass filtered only the
// SINGULAR `kit:` when empty: `kits: ["", "transverse"]` passed
// `den doctor` as "all clear" yet made `den <nest>` refuse with a
// "kit not found: " on an empty path — two judges of the same field
// disagreeing, the very defect T2-min-5 names.
//
// The doctor-side counterpart is TestRunIgnoresAnEmptyEntryInKits: both
// hold the SAME property from the two ends, which is what makes any
// future divergence between the two paths visible.
func TestSpawnIgnoresAnEmptyEntryInKits(t *testing.T) {
	denHome, _ := denTest(t)
	write(t, filepath.Join(denHome, "stacks", "devx", "stack.yaml"),
		"image: devx:v1\nkits: [\"\", transverse]\nkit: devx-kit\n")
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("an empty entry in kits: must be ignored, not refused: %v", err)
	}
	// And it must not slip into the argv either: a `--kit ""` would reach
	// sbx.
	kits := kitsOf(callStartingWith(f, "create"))
	for i, k := range kits {
		if k == "" {
			t.Errorf("--kit #%d is empty; kits = %v", i, kits)
		}
	}
	// The layering order stays the declaration order, empty entry removed.
	expected := []string{
		filepath.Join(denHome, "stacks", "devx", "transverse"),
		filepath.Join(denHome, "stacks", "devx", "devx-kit"),
		filepath.Join(denHome, "cache", "mixins", "api"),
	}
	if !slices.Equal(kits, expected) {
		t.Errorf("kits = %v, expected %v", kits, expected)
	}
}

// The counterpart: a stack declaring NO kit is perfectly valid (spec
// §4.2) and must require nothing. Without this case, a check refusing the
// empty string would break every kit-less stack.
func TestSpawnAcceptsAStackWithoutKit(t *testing.T) {
	denHome, _ := denTest(t)
	write(t, filepath.Join(denHome, "stacks", "devx", "stack.yaml"), "image: devx:v1\n")
	_, d := fakeDeps()
	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("a stack without a kit must stay valid: %v", err)
	}
}

// F4: `-w feature/123` used to be refused, even though it's a perfectly
// ordinary branch name — the first one anyone working on a forge types.
//
// What gets flattened is whatever becomes a NAME: the sandbox and the
// worktree directory. The branch keeps what the user typed — the name
// their `git log`, forge and PR all use.
//
// The assertions follow the round trip `den rm` will take: sandbox name →
// sbx.SplitName → worktree.Path → the directory Ensure actually created.
// Flattening applied to fewer than all THREE would break that chain, and
// `den rm` would clean up the wrong place.
func TestSpawnFlattensTheSandboxNameAndKeepsTheBranch(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Worktree: "feature/123"}, d); err != nil {
		t.Fatalf("\"feature/123\" is a legitimate branch name: %v", err)
	}

	if !f.HasCalled("create", "--name", "api.feature-123") {
		t.Errorf("the sandbox name must be flattened; calls: %v", f.Calls)
	}
	// The settle-loop and the attach must target the SAME name: flattening
	// applied only at `create` would leave the policy scoped on a name sbx
	// doesn't know.
	if !f.HasCalled("policy", "check", "network", "--sandbox", "api.feature-123") {
		t.Errorf("the settle-loop must be scoped on the flattened name; calls: %v", f.Calls)
	}

	worktreePath := filepath.Join(denHome, "worktrees", "feature-123", "api")
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("the worktree directory must be flattened too (%s): %v", worktreePath, err)
	}
	if !f.HasAttached("exec", "-it", "-w", worktreePath, "api.feature-123", "bash", "-l") {
		t.Errorf("the attach must open in the flattened worktree; attaches: %v", f.Attaches)
	}

	// The round trip, spelled out: it's what `den rm` uses to find the
	// directory to clean up, with nothing else to go on but the sandbox
	// name. Flattening applied to the name but not the directory would let
	// these two paths diverge, and `den rm` would clean up the wrong place.
	_, wt := sbx.SplitName("api.feature-123")
	if got := worktree.Path("central", filepath.Join(denHome, "worktrees"), wt, repo); got != worktreePath {
		t.Errorf("`den rm` would look for the worktree at %q, it's at %q", got, worktreePath)
	}

	// And the branch is NOT flattened.
	if got := branchOf(t, worktreePath); got != "feature/123" {
		t.Errorf("branch = %q, expected feature/123 — that's the user's branch", got)
	}

	// The flattening is announced: otherwise the user looks for
	// "feature/123" in `den ls` and never finds it.
	for _, expected := range []string{"feature/123", "api.feature-123"} {
		if !strings.Contains(out.String(), expected) {
			t.Errorf("the output must announce the rename (expected %q);\n%s", expected, out.String())
		}
	}
}

// The counterpart: what flattening can't fix stays refused, before any
// side effect. "-wip" has no forbidden character — "-" is legal elsewhere
// in a name — but its position is the problem: a name starting with a
// dash is indistinguishable from a flag. Prefixing it automatically would
// mean choosing a name on the user's behalf.
func TestSpawnRefusesAWorktreeFlatteningCannotFix(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()

	err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "-wip"}, d)
	if err == nil {
		t.Fatal("a worktree starting with \"-\" must be refused")
	}
	if !strings.Contains(err.Error(), "-wip") {
		t.Errorf("the message must render the name as TYPED, not its flattened form; got: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("no sbx call should have happened; calls: %v", f.Calls)
	}
	if _, err := os.Stat(filepath.Join(denHome, "worktrees")); err == nil {
		t.Error("no worktree must have been created")
	}
}

// Spec §11: "repo path not found → stop BEFORE any create".
func TestSpawnStopsBeforeCreateWhenARepoIsMissing(t *testing.T) {
	denHome, repo := denTest(t)
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err == nil {
		t.Fatal("a missing repo must fail the spawn")
	} else if !strings.Contains(err.Error(), repo) {
		t.Errorf("the message must name the missing repo; got: %v", err)
	}
	// No call at all, not just no create: the check must precede even the
	// spawn-or-attach's `sbx ls`.
	if len(f.Calls) != 0 {
		t.Errorf("no sbx call should have happened; calls: %v", f.Calls)
	}
	// And no disk side effect either. Spec §11 says "stop before any
	// create", but the intent is "before any side effect": without both
	// checks, moving the guard just before the spawn-or-attach block would
	// leave the agent profile and mixin behind while still keeping the
	// calls assertion true.
	for _, path := range []string{
		filepath.Join(denHome, "agents", "claude"),
		filepath.Join(denHome, "cache", "mixins"),
	} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("no side effect must have happened, yet %s exists", path)
		}
	}
}

// An ABSENT config_dir is no longer a fault: internal/config defaults it to
// <den home>/agents/<name> at load time (config.go). This locks down the
// consequence at the spawn boundary — the profile still ends up mounted from
// a real, existing directory, computed against THIS run's den home, not a
// hardcoded one.
func TestSpawnDefaultsConfigDirWhenAbsent(t *testing.T) {
	denHome, _ := denTest(t)
	write(t, filepath.Join(denHome, "config.yaml"), `agents:
  claude:
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`)
	_, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat12"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(denHome, "agents", "claude")); err != nil {
		t.Errorf("the defaulted config_dir must exist: %v", err)
	}
}

// The agent profile is mounted RW: a WRITTEN-but-blank config_dir would
// mount an empty path, and a bare MkdirAll("")'s error would name nothing.
// Unlike absence (above), this survives LoadGlobalUnvalidated's `== ""`
// default and is refused by Validate — checked before any side effect, and
// the message names the file to fix.
func TestSpawnRefusesAWhitespaceOnlyConfigDir(t *testing.T) {
	denHome, _ := denTest(t)
	write(t, filepath.Join(denHome, "config.yaml"), `agents:
  claude:
    config_dir: "   "
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`)
	f, d := fakeDeps()

	err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat12"}, d)
	if err == nil {
		t.Fatal("a blank config_dir must fail the spawn")
	}
	if !strings.Contains(err.Error(), filepath.Join(denHome, "config.yaml")) {
		t.Errorf("the message must name the offending file; got: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("no sbx call should have happened; calls: %v", f.Calls)
	}
	if _, err := os.Stat(filepath.Join(denHome, "worktrees")); err == nil {
		t.Error("no worktree must have been created")
	}
}

func TestSpawnDetachDoesNotAttach(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasCalled("create", "--name", "api") {
		t.Errorf("the create must happen; calls: %v", f.Calls)
	}
	if !f.HasCalled("policy", "check", "network", "--sandbox", "api") {
		t.Errorf("--detach doesn't skip the settle-loop; calls: %v", f.Calls)
	}
	if len(f.Attaches) != 0 {
		t.Errorf("--detach must not attach; attaches: %v", f.Attaches)
	}
}

// The agent profile is mounted RW and must exist: sbx would otherwise
// mount an empty directory, and the agent would start from scratch on
// every spawn.
func TestSpawnCreatesTheAgentProfile(t *testing.T) {
	denHome, _ := denTest(t)
	_, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(denHome, "agents", "claude")); err != nil {
		t.Errorf("the agent's config_dir must exist: %v", err)
	}
}

func TestSpawnWritesTheMixin(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := filepath.Join(denHome, "cache", "mixins", "api", "spec.yaml")
	content, err := os.ReadFile(spec)
	if err != nil {
		t.Fatalf("the mixin must be written to %s: %v", spec, err)
	}
	if !strings.Contains(string(content), "github.com") {
		t.Errorf("the mixin must carry the cascade's egress:\n%s", content)
	}
	// Full layering order: transverse kits, then the stack's kit, then the
	// mixin — ALWAYS last (sbx's dispatcher does `exit $rc` on the first
	// failure and would starve later kits of their startup commands).
	stackDir := filepath.Join(denHome, "stacks", "devx")
	expected := []string{
		filepath.Join(stackDir, "transverse"),
		filepath.Join(stackDir, "devx-kit"),
		filepath.Dir(spec),
	}
	if k := kitsOf(callStartingWith(f, "create")); !slices.Equal(k, expected) {
		t.Errorf("--kit = %v, expected %v", k, expected)
	}
}

// The three cascade options must reach nest.Resolve.
//
// Each is exercised with an INVALID value: the only way, without sbx, to
// get a message that depends on the VALUE passed — proof it made it
// through. A silently dropped option (`Only` or `Agent` not forwarded)
// would fall back to the default and succeed quietly: `--agent
// claude-next` would mount the default agent's profile and write ITS
// environment variables into the mixin, without a word.
func TestSpawnPropagatesCascadeOptions(t *testing.T) {
	cases := []struct {
		name     string
		options  Options
		expected string
	}{
		{"Without", Options{Nest: "api", Without: []string{"unknown"}}, `--without: repo "unknown"`},
		{"Only", Options{Nest: "api", Only: []string{"unknown"}}, `--only: repo "unknown"`},
		{"Agent", Options{Nest: "api", Agent: "unknown"}, `agent "unknown"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			denHome, _ := denTest(t)
			f, d := fakeDeps()

			err := Spawn(context.Background(), denHome, c.options, d)
			if err == nil {
				t.Fatalf("%s with an unknown value must fail the spawn", c.name)
			}
			if !strings.Contains(err.Error(), c.expected) {
				t.Errorf("%s doesn't reach the cascade (expected %q); got: %v", c.name, c.expected, err)
			}
			if len(f.Calls) != 0 {
				t.Errorf("no sbx call should have happened; calls: %v", f.Calls)
			}
		})
	}
}

// A failed `sbx create` must be recontextualized. Exec.Run's raw message
// is prefixed with the FULL argv — a giant line with every --kit and every
// workspace — in which the failed step becomes unreadable.
func TestSpawnNamesTheStepWhenCreateFails(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	f.Default = sbx.Response{Err: errors.New("boom")}

	err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d)
	if err == nil {
		t.Fatal("a failing create must fail the spawn")
	}
	if !strings.Contains(err.Error(), "creating sandbox api") {
		t.Errorf("the message must name the step and the sandbox; got: %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("the message must keep the cause; got: %v", err)
	}
	if len(f.Attaches) != 0 {
		t.Errorf("no attach must happen; attaches: %v", f.Attaches)
	}
}

// Spawn refuses a relative den home BEFORE any side effect.
//
// This invariant — guaranteed by nest.Resolve — is what makes `denHome`
// and `r.DenHome` interchangeable, so the choice of which one feeds
// WriteMixin is INDETECTABLE by construction: Resolve sets
// r.DenHome = denHome only when it's absolute, and refuses everything
// else. What's under test isn't that choice, it's the invariant that
// makes it inconsequential.
func TestSpawnRefusesARelativeDenHome(t *testing.T) {
	denHome, _ := denTest(t)
	t.Chdir(filepath.Dir(denHome))
	f, d := fakeDeps()

	err := Spawn(context.Background(), filepath.Base(denHome), Options{Nest: "api"}, d)
	if err == nil {
		t.Fatal("a relative den home must fail the spawn")
	}
	if !strings.Contains(err.Error(), "not an absolute path") {
		t.Errorf("the message must name the cause; got: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("no sbx call should have happened; calls: %v", f.Calls)
	}
}

// A nil Out must not panic mid-spawn: a caller who forgets to set it
// already has, by this point, a sandbox created and started.
func TestSpawnToleratesANilOut(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	d.Out = nil

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasCalled("create", "--name", "api") {
		t.Errorf("the spawn must have run; calls: %v", f.Calls)
	}
}

// The same contract for Err, and it needs its own test: every warning test
// below hands Spawn a real buffer, and fakeDeps leaves BOTH Err and SSHAgent
// nil — so warnEmptySSHAgent returns on its nil-probe guard before reaching a
// single Fprintf. A nil Err was therefore never once written to, and the
// default could be dropped with the suite still green.
//
// Hence the deliberate setup: a socket in the environment and an EMPTY agent,
// the one combination that makes the warning actually print. `probed` is what
// keeps the test from passing vacuously — a spawn that silently skipped the
// warning would satisfy "no panic" too.
func TestSpawnToleratesANilErr(t *testing.T) {
	forwardedSocket(t)
	denHome, _ := denTest(t) // denTest is agent-forward
	f, d := fakeDeps()
	d.Err = nil
	probed := false
	d.SSHAgent = func() sshagent.Result {
		probed = true
		return sshagent.Result{State: sshagent.StateEmpty}
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !probed {
		t.Fatal("the warning path was never reached; this test would prove nothing about a nil Err")
	}
	if !f.HasCalled("create", "--name", "api") {
		t.Errorf("the spawn must have run; calls: %v", f.Calls)
	}
}

// --- empty SSH agent warning (agent-forward) --------------------------
//
// sbx forwards the host agent's socket faithfully, but an empty agent
// forwards an empty agent: `git push` dies on publickey inside the VM, far
// from the cause. den warns at spawn — non-blocking, on stderr — the same
// probe `den doctor` runs.

// forwardedSocket puts a NON-EMPTY SSH_AUTH_SOCK in den's environment: the
// precondition of every test below that expects the agent to be PROBED, since
// the warning judges the variable before touching the probe.
//
// Set explicitly rather than inherited: left to the machine, these tests would
// exercise the probe branches on a developer's box (agent running) and the
// absent-socket branch on a bare CI runner — the same test asserting two
// different things depending on where it runs.
//
// The path is never opened. den stats no socket, and the probe here is a
// double, so the value only has to be non-empty and obviously fake.
func forwardedSocket(t *testing.T) {
	t.Helper()
	t.Setenv("SSH_AUTH_SOCK", "/tmp/den-test/agent.sock")
}

// The nominal defect: an empty forwarded agent warns on stderr, and the spawn
// runs to completion regardless — HTTPS and read-only work need no SSH.
func TestSpawnWarnsOnStderrWhenTheForwardedAgentIsEmpty(t *testing.T) {
	forwardedSocket(t)
	denHome, _ := denTest(t) // denTest is agent-forward
	f, d := fakeDeps()
	var errBuf bytes.Buffer
	d.Err = &errBuf
	d.SSHAgent = func() sshagent.Result { return sshagent.Result{State: sshagent.StateEmpty} }

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("an empty agent must warn, not block: %v", err)
	}
	// The spawn happened despite the warning.
	if !f.HasCalled("create", "--name", "api") {
		t.Errorf("the spawn must run despite the warning; calls: %v", f.Calls)
	}
	out := errBuf.String()
	if !strings.Contains(out, "warning") {
		t.Errorf("an empty agent must warn on stderr; got: %q", out)
	}
	// The message names the consequence and a fix that works WITHOUT respawn —
	// the whole point (the forwarded socket is a live proxy).
	for _, want := range []string{"publickey", "ssh-add", "without respawning"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr = %q, must contain %q", out, want)
		}
	}
}

// The fix the warning quotes is OS-specific: macOS needs `--apple-use-keychain`
// to reach the login keychain where it keeps passphrases. Reading runtime.GOOS
// inside the spawn made that branch unassertable — this suite runs on Linux, so
// the macOS message nobody could exercise was the one shipped to macOS users.
// Deps.GOOS puts the choice back under the injection contract.
func TestSpawnNamesTheMacOSRemedyWhenGOOSIsDarwin(t *testing.T) {
	forwardedSocket(t)
	denHome, _ := denTest(t) // denTest is agent-forward
	_, d := fakeDeps()
	var errBuf bytes.Buffer
	d.Err = &errBuf
	d.GOOS = "darwin"
	d.SSHAgent = func() sshagent.Result { return sshagent.Result{State: sshagent.StateEmpty} }

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("an empty agent must warn, not block: %v", err)
	}
	if want := sshagent.FixCommand("darwin"); !strings.Contains(errBuf.String(), want) {
		t.Errorf("stderr = %q, must name the darwin remedy %q in full", errBuf.String(), want)
	}
}

// The counterpart, without which a FixCommand returning the macOS form
// everywhere would pass the test above: on linux the keychain flag must not
// appear, since ssh-add there rejects it outright.
func TestSpawnOmitsTheMacOSFlagOnLinux(t *testing.T) {
	forwardedSocket(t)
	denHome, _ := denTest(t)
	_, d := fakeDeps()
	var errBuf bytes.Buffer
	d.Err = &errBuf
	d.GOOS = "linux"
	d.SSHAgent = func() sshagent.Result { return sshagent.Result{State: sshagent.StateEmpty} }

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("an empty agent must warn, not block: %v", err)
	}
	out := errBuf.String()
	if strings.Contains(out, "--apple-use-keychain") {
		t.Errorf("stderr = %q, must not hand a Linux user a macOS-only flag", out)
	}
	if !strings.Contains(out, "ssh-add") {
		t.Errorf("stderr = %q, must still name a concrete fix command", out)
	}
}

// An unreachable agent warns the same way, with the different cause named.
func TestSpawnWarnsOnStderrWhenTheForwardedAgentIsUnreachable(t *testing.T) {
	// A socket IS set here: "unreachable" is what a present variable pointing
	// at a dead agent means, and it is exactly what an absent one must not be
	// reported as (see TestSpawnWarnsWithoutProbingWhenTheSSHSocketIsAbsent).
	forwardedSocket(t)
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	var errBuf bytes.Buffer
	d.Err = &errBuf
	d.SSHAgent = func() sshagent.Result { return sshagent.Result{State: sshagent.StateUnreachable} }

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("an unreachable agent must warn, not block: %v", err)
	}
	if !f.HasCalled("create", "--name", "api") {
		t.Errorf("the spawn must run despite the warning; calls: %v", f.Calls)
	}
	if out := errBuf.String(); !strings.Contains(out, "warning") || !strings.Contains(out, "unreachable") {
		t.Errorf("an unreachable agent must warn and name the cause; got: %q", out)
	}
}

// The warning is documented as applying to the ATTACH branch too — the
// forwarded socket is a live proxy, so an empty agent is just as fatal to
// `git push` when returning to a sandbox that is already running. Every other
// test in this block asserts `create`, so the whole SSH warning could sit
// inside the create-only branch and the suite would stay green while the
// second `den api` of the day — the common case, since a sandbox is created
// once and re-entered daily — silently forwarded an empty agent.
//
// Hence the two assertions together: NO create happened (this really is the
// attach branch) and the warning still reached stderr.
func TestSpawnWarnsOnTheAttachBranchWhenTheForwardedAgentIsEmpty(t *testing.T) {
	forwardedSocket(t)
	denHome, repo := denTest(t) // denTest is agent-forward
	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`),
	}
	var outBuf, errBuf bytes.Buffer
	d.Out = &outBuf
	d.Err = &errBuf
	d.SSHAgent = func() sshagent.Result { return sshagent.Result{State: sshagent.StateEmpty} }

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("an empty agent must warn, not block: %v", err)
	}
	if f.HasCalled("create") {
		t.Fatalf("no create must happen on a live sandbox — this test would stop "+
			"exercising the attach branch; calls: %v", f.Calls)
	}
	// The attach itself, so "no create" can't be a spawn that gave up early.
	if !f.HasAttached("exec", "-it", "-w", repo, "api", "bash", "-l") {
		t.Fatalf("the attach must have happened; attaches: %v", f.Attaches)
	}
	out := errBuf.String()
	if !strings.Contains(out, "warning") {
		t.Errorf("an empty agent must warn on stderr when re-attaching too; got: %q", out)
	}
	// Same message as the create branch: the consequence, and a fix that acts
	// without respawning den — the very reason it is true on this branch.
	for _, want := range []string{"publickey", "ssh-add", "without respawning"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr = %q, must contain %q", out, want)
		}
	}
	// And the stream discipline, asserted HERE and nowhere else: the only other
	// test that inspects stdout is the has-keys one, where no warning exists to
	// leak — so "the warning never lands on stdout" was checked in the single
	// case where it could not fail. This is the case where it can.
	//
	// The attach's own log is asserted FIRST, and that order carries the
	// argument: `Out = io.Discard` or a spawn that gave up before writing
	// anything would satisfy "stdout holds no warning" vacuously. stdout must be
	// PROVEN in use, then proven clean.
	if !strings.Contains(outBuf.String(), "already live: attaching") {
		t.Fatalf("stdout must carry the attach's own log, otherwise the cleanliness "+
			"assertion below proves nothing; stdout: %q", outBuf.String())
	}
	// Markers of THIS warning, not the bare word "warning": stdout legitimately
	// carries other ones — reportDrift writes there by the rule in Deps.Err, and
	// this very run emits it — so asserting "no warning at all" would fail on a
	// correct spawn and say nothing about the SSH message.
	for _, leaked := range []string{"ssh-add", "publickey"} {
		if strings.Contains(outBuf.String(), leaked) {
			t.Errorf("the SSH warning must stay on stderr while stdout carries the spawn's "+
				"log — a caller piping stdout must not receive %q; stdout: %q",
				leaked, outBuf.String())
		}
	}
}

// An ABSENT SSH_AUTH_SOCK is not an unreachable agent, and den must not read
// it as one: there is no socket to point a probe at, so probing is a wasted
// `ssh-add` on the mainline spawn path, and "SSH_AUTH_SOCK points at an
// unreachable agent" describes a variable the user never set — sending them
// to look for a dead socket that does not exist. `den doctor` already decides
// the opposite for the SAME machine state
// (doctor.TestRunDoesNotQueryTheAgentWhenTheSocketIsAbsent): the two must not
// contradict each other on one `den <nest>`.
//
// The probe returns StateUnreachable deliberately — the state that used to
// produce the wrong sentence — so the message can only come from the socket
// check, never from a probe that never should have run.
func TestSpawnWarnsWithoutProbingWhenTheSSHSocketIsAbsent(t *testing.T) {
	// Set EMPTY rather than left alone: os.Getenv answers "" for both absent
	// and empty, and a test that relied on the machine having no agent would
	// pass on CI and quietly stop exercising this branch on a developer's box.
	t.Setenv("SSH_AUTH_SOCK", "")
	denHome, _ := denTest(t) // denTest is agent-forward
	f, d := fakeDeps()
	var errBuf bytes.Buffer
	d.Err = &errBuf
	probed := false
	d.SSHAgent = func() sshagent.Result {
		probed = true
		return sshagent.Result{State: sshagent.StateUnreachable}
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("an absent socket must warn, not block: %v", err)
	}
	if !f.HasCalled("create", "--name", "api") {
		t.Errorf("the spawn must run despite the warning; calls: %v", f.Calls)
	}
	if probed {
		t.Error("the SSH agent was probed with no socket to point it at")
	}
	out := errBuf.String()
	if !strings.Contains(out, "absent or empty") {
		t.Errorf("stderr = %q, must report SSH_AUTH_SOCK as absent or empty, "+
			"the same verdict `den doctor` reaches for this machine", out)
	}
	if strings.Contains(out, "points at") {
		t.Errorf("stderr = %q, must not claim an unset SSH_AUTH_SOCK points at anything", out)
	}
}

// The essential counterpart: an agent holding keys is the healthy case and
// must be SILENT. Without it, a warning wired unconditionally would pass the
// test above. The warning also stays off stdout: it belongs on stderr alone.
func TestSpawnDoesNotWarnWhenTheForwardedAgentHasKeys(t *testing.T) {
	forwardedSocket(t)
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	var outBuf, errBuf bytes.Buffer
	d.Out = &outBuf
	d.Err = &errBuf
	d.SSHAgent = func() sshagent.Result {
		return sshagent.Result{State: sshagent.StateKeys, Identities: 2}
	}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasCalled("create", "--name", "api") {
		t.Fatalf("the spawn must have run; calls: %v", f.Calls)
	}
	if strings.Contains(errBuf.String(), "warning") {
		t.Errorf("an agent with keys must be silent; stderr: %q", errBuf.String())
	}
	if strings.Contains(outBuf.String(), "ssh-add") {
		t.Errorf("the SSH warning must never land on stdout; stdout: %q", outBuf.String())
	}
}

// Modes `mount` and `none` don't forward the agent, so its state is
// irrelevant and no probe must fire — even when that agent is empty. The spy
// proves the probe is never called outside agent-forward.
func TestSpawnDoesNotProbeTheAgentOutsideAgentForward(t *testing.T) {
	for _, mode := range []string{"mount", "none"} {
		t.Run(mode, func(t *testing.T) {
			sshBlock := "  mode: " + mode + "\n"
			if mode == "mount" {
				sshDir := filepath.Join(t.TempDir(), "ssh")
				if err := os.MkdirAll(sshDir, 0o700); err != nil {
					t.Fatal(err)
				}
				sshBlock = "  mode: mount\n  dir: " + sshDir + "\n"
			}
			denHome, _ := denTestSSH(t, sshBlock)
			f, d := fakeDeps()
			var errBuf bytes.Buffer
			d.Err = &errBuf
			probed := false
			d.SSHAgent = func() sshagent.Result {
				probed = true
				return sshagent.Result{State: sshagent.StateEmpty}
			}

			if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
				t.Fatalf("mode %q: unexpected error: %v", mode, err)
			}
			if probed {
				t.Errorf("mode %q: the SSH agent must not be probed", mode)
			}
			if strings.Contains(errBuf.String(), "warning") {
				t.Errorf("mode %q: no SSH warning expected; stderr: %q", mode, errBuf.String())
			}
			// Without this, "no probe" would also be satisfied by a spawn that
			// gave up before reaching the probe at all — the silence would
			// prove nothing about the mode. The create is what makes the
			// absence of a probe a decision rather than an early exit.
			if !f.HasCalled("create", "--name", "api") {
				t.Errorf("mode %q: the spawn must have run; calls: %v", mode, f.Calls)
			}
		})
	}
}

// A nil probe must not panic: every test that doesn't exercise SSH, plus the
// wiring double, leaves SSHAgent unset, and the spawn must simply skip the
// warning rather than dereference nil.
func TestSpawnToleratesANilSSHAgentProbe(t *testing.T) {
	denHome, _ := denTest(t) // agent-forward, the mode that would probe
	f, d := fakeDeps()
	d.SSHAgent = nil

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("a nil probe must be tolerated: %v", err)
	}
	if !f.HasCalled("create", "--name", "api") {
		t.Errorf("the spawn must have run; calls: %v", f.Calls)
	}
}

// WarnEmptySSHAgentOnReentry is what `den sh` calls, and its first contract is
// that the message is the SAME one `den <nest>` prints: two surfaces describing
// one machine state must not word it two ways, and the fix that acts without a
// respawn is precisely what makes the warning worth printing on a re-entry.
func TestWarnEmptySSHAgentOnReentryWarnsOnAnEmptyAgent(t *testing.T) {
	var buf bytes.Buffer
	probe := func() sshagent.Result { return sshagent.Result{State: sshagent.StateEmpty} }

	WarnEmptySSHAgentOnReentry(&buf, "agent-forward", "/tmp/den-test/agent.sock", probe, "darwin")

	out := buf.String()
	for _, want := range []string{"warning", "publickey", "without respawning",
		sshagent.FixCommand("darwin")} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, must contain %q", out, want)
		}
	}
}

// The one place where re-entry DIVERGES from the preflight, and the reason it
// has its own function rather than reusing the call site of `den <nest>`.
//
// An absent SSH_AUTH_SOCK in the shell running `den sh` says nothing about the
// sandbox: a live VM forwards the socket it inherited at its `sbx create`, from
// an environment that may be long gone, and `den sh` creates nothing. So the
// preflight's message — "start an agent … and relaunch den, which forwards the
// socket at creation time" — would be advice for a step this command does not
// have, about a socket den cannot see. Silence, and no probe either: without a
// socket, `ssh-add -l` reports StateUnreachable, which would print "SSH_AUTH_SOCK
// points at an unreachable agent" about a variable the user never set.
func TestWarnEmptySSHAgentOnReentryStaysSilentWithoutASocket(t *testing.T) {
	var buf bytes.Buffer
	probed := false
	probe := func() sshagent.Result {
		probed = true
		return sshagent.Result{State: sshagent.StateUnreachable}
	}

	WarnEmptySSHAgentOnReentry(&buf, "agent-forward", "", probe, "linux")

	if probed {
		t.Error("the agent was probed with no socket to point it at")
	}
	if buf.String() != "" {
		t.Errorf("output = %q, want silence: nothing here describes the socket the "+
			"live sandbox actually forwards", buf.String())
	}
}

// The healthy case stays silent, and so do the modes that forward no agent —
// the properties that keep the two tests above from being satisfied by a
// function that warns unconditionally. Shared with `den <nest>` by
// construction (both go through warnEmptySSHAgent), asserted here because
// "shared by construction" is exactly what a refactor breaks.
func TestWarnEmptySSHAgentOnReentryStaysSilentWhenNothingIsWrong(t *testing.T) {
	for name, tc := range map[string]struct {
		sshMode string
		state   sshagent.State
	}{
		"agent has keys": {"agent-forward", sshagent.StateKeys},
		"mode mount":     {"mount", sshagent.StateEmpty},
		"mode none":      {"none", sshagent.StateEmpty},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			probe := func() sshagent.Result {
				return sshagent.Result{State: tc.state, Identities: 1}
			}

			WarnEmptySSHAgentOnReentry(&buf, tc.sshMode, "/tmp/den-test/agent.sock", probe, "linux")

			if buf.String() != "" {
				t.Errorf("output = %q, want silence", buf.String())
			}
		})
	}
}

// The spawn's side of the same property as doctor's
// TestRunNamesTheNonDefaultKeyCaveatWhereverItQuotesAFix: all three warning
// branches quote a load command, and `ssh-add` loads only default-named keys —
// so all three must say so, or the user runs a command that succeeds and keeps
// pushing into `Permission denied (publickey)`.
//
// The branches are driven straight through warnEmptySSHAgent rather than through
// a full Spawn: what is under test is the message, and three spawns would prove
// the same thing about it while also depending on a den home and a fake sbx.
func TestWarnEmptySSHAgentNamesTheNonDefaultKeyCaveatOnEveryBranch(t *testing.T) {
	for name, tc := range map[string]struct {
		socket string
		state  sshagent.State
	}{
		"socket absent": {"", sshagent.StateUnreachable},
		"empty":         {"/tmp/den-test/agent.sock", sshagent.StateEmpty},
		"unreachable":   {"/tmp/den-test/agent.sock", sshagent.StateUnreachable},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer

			warnEmptySSHAgent(&buf, "agent-forward", tc.socket,
				func() sshagent.Result { return sshagent.Result{State: tc.state} }, "darwin")

			if !strings.Contains(buf.String(), "warning") {
				t.Fatalf("output = %q, this branch must warn or it asserts nothing", buf.String())
			}
			if !strings.Contains(buf.String(), sshagent.KeyNameCaveat) {
				t.Errorf("output = %q, must carry the non-default-key caveat %q",
					buf.String(), sshagent.KeyNameCaveat)
			}
		})
	}
}

// A State this switch does not model must still say something, on BOTH entry
// points. Without a default arm the warning printed nothing at all: the spawn
// went quiet about the agent, which reads as "nothing to report" — the check
// disappearing is worse than any verdict it could give, and it is exactly the
// arm doctor.go gained (doctor.go's `default`, same reasoning). A state added to
// sshagent must surface here rather than deleting the diagnostic.
//
// StateKeys+1 rather than a literal: the point is "outside what this switch
// models", and a hardcoded number would stop meaning that the day a fourth
// state is declared.
func TestWarnEmptySSHAgentReportsAStateItDoesNotModel(t *testing.T) {
	unmodelled := sshagent.StateKeys + 1
	for name, warn := range map[string]func(io.Writer, string, string, func() sshagent.Result, string){
		"preflight": warnEmptySSHAgent,
		"reentry":   WarnEmptySSHAgentOnReentry,
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			warn(&buf, "agent-forward", "/tmp/den-test/agent.sock",
				func() sshagent.Result { return sshagent.Result{State: unmodelled} }, "linux")

			out := buf.String()
			if !strings.Contains(out, "warning") {
				t.Fatalf("output = %q, an unrecognized state must not silence the check", out)
			}
			// The value seen is named: a warning that doesn't say WHAT it read
			// leaves nobody able to tell which state escaped the model.
			if !strings.Contains(out, strconv.Itoa(int(unmodelled))) {
				t.Errorf("output = %q, must name the state it could not read (%d)", out, unmodelled)
			}
			// And a way out that does not depend on den understanding the agent.
			if !strings.Contains(out, "ssh-add -l") {
				t.Errorf("output = %q, must point at a check the user can run by hand", out)
			}
		})
	}
}

// A nil probe is tolerated on this path too: `den sh`'s own wiring tests build
// their accesses by hand and leave it unset, and they must skip the warning
// rather than dereference nil mid-command.
func TestWarnEmptySSHAgentOnReentryToleratesANilProbe(t *testing.T) {
	var buf bytes.Buffer

	WarnEmptySSHAgentOnReentry(&buf, "agent-forward", "/tmp/den-test/agent.sock", nil, "linux")

	if buf.String() != "" {
		t.Errorf("output = %q, want silence on a nil probe", buf.String())
	}
}

func callStartingWith(f *sbx.Fake, head string) []string {
	for _, a := range f.Calls {
		if len(a) > 0 && a[0] == head {
			return a
		}
	}
	return nil
}

// kitsOf extracts the `--kit` values of an argv, in order.
func kitsOf(argv []string) []string {
	var out []string
	for i, a := range argv {
		if a == "--kit" && i+1 < len(argv) {
			out = append(out, argv[i+1])
		}
	}
	return out
}

// workspacesOf extracts the positionals of a `sbx create`, i.e. everything
// after the positional agent.
func workspacesOf(argv []string) []string {
	i := slices.Index(argv, sbx.PositionalAgent)
	if i < 0 {
		return nil
	}
	return argv[i+1:]
}

// branchOf returns a worktree's checked-out branch. Goes through git
// rather than reading `.git/HEAD` directly: a LINKED worktree's `.git` is
// a redirect file, not a directory.
func branchOf(t *testing.T, path string) string {
	t.Helper()
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = path
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --show-current in %s: %v\n%s", path, err, out)
	}
	return strings.TrimSpace(string(out))
}

// --- The §11 stack-image check (issue #8) -------------------------------
//
// den refuses a create whose image was never built, so it can say what §11
// promises — "run `den build <stack>`" — instead of relaying sbx's own refusal.
// That refusal, measured on 2026-07-31, is a `403 Forbidden: pull failed for
// image "X"`: sbx treats an unknown template as a registry pull, so the user
// reads about authorization, never about a missing build. Pattern-matching it
// is impossible (a real unauthorized pull returns the same 403), hence the
// check BEFORE `sbx create`, against `sbx template ls --json`.

// withBuildableStack gives the test stack `provision.steps`, which is what
// makes den's `den build devx` advice truthful — and therefore what arms the
// check.
//
// Buildability is a property of the stack YAML, never of a file on disk: den
// no longer runs a `stacks/<n>/build.sh`, it plays each `provision.steps` entry
// inside the build VM. Nothing needs to exist next to the stack.yaml for this
// helper to arm the check — the spawn reads config.Stack.Buildable, which reads
// the declaration.
func withBuildableStack(t *testing.T, denHome string) {
	t.Helper()
	write(t, filepath.Join(denHome, "stacks", "devx", "stack.yaml"),
		"image: devx:v1\nbase: claude\nkits: [transverse]\nkit: devx-kit\n"+
			"provision:\n  steps: [./provision/setup.sh]\n")
}

// answerTemplates makes the fake sbx answer `template ls --json` with this
// inventory, leaving every other call on the default.
func answerTemplates(f *sbx.Fake, json string) {
	f.Responses["template ls --json"] = sbx.Response{Output: []byte(json)}
}

func TestSpawnRefusesAStackImageThatWasNeverBuilt(t *testing.T) {
	denHome, _ := denTest(t)
	withBuildableStack(t, denHome)
	f, d := fakeDeps()
	answerTemplates(f, `{"images":[]}`)

	err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d)
	if err == nil {
		t.Fatal("expected a refusal on an image no build ever produced")
	}
	msg := err.Error()
	for _, want := range []string{"devx:v1", "den build devx"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, want it to contain %q", msg, want)
		}
	}
	if f.HasCalled("create") {
		t.Errorf("no create may follow the refusal; calls: %v", f.Calls)
	}
}

// The refusal lands BEFORE the worktrees, which is the whole reason the
// spawn-or-attach reading moved up in the sequence: at its old position den
// would have created one git worktree per repo and left the user to clean them
// up by hand.
func TestSpawnRefusesAnUnbuiltImageBeforeCreatingAnyWorktree(t *testing.T) {
	denHome, _ := denTest(t)
	withBuildableStack(t, denHome)
	f, d := fakeDeps()
	answerTemplates(f, `{"images":[]}`)

	err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat"}, d)
	if err == nil {
		t.Fatal("expected a refusal on an image no build ever produced")
	}
	root := filepath.Join(denHome, "worktrees")
	entries, statErr := os.ReadDir(root)
	if statErr == nil && len(entries) != 0 {
		t.Errorf("%s = %v, want no worktree created before the refusal", root, entries)
	}
	// Same guard on the agent profile, created midway through the sequence.
	if _, err := os.Stat(filepath.Join(denHome, "agents", "claude")); err == nil {
		t.Error("the agent profile must not have been created before the refusal")
	}
}

// The bare form a stack writes must find the qualified image sbx reports —
// this is sbx.NormalizeImageRef doing its job, seen from the spawn.
func TestSpawnCreatesWhenTheImageIsBuilt(t *testing.T) {
	denHome, _ := denTest(t)
	withBuildableStack(t, denHome)
	f, d := fakeDeps()
	answerTemplates(f, `{"images":[{"repository":"docker.io/library/devx","tag":"v1"}]}`)

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("`image: devx:v1` must match the `docker.io/library/devx` + `v1` sbx reports: %v", err)
	}
	if !f.HasCalled("create") {
		t.Errorf("a create must have happened; calls: %v", f.Calls)
	}
}

// A stack with NO `provision.steps` is left alone, and not even asked about:
// `image:` may name a registry image sbx will happily pull, and `den build` on
// a stack den cannot build is not advice, it is a second error.
//
// The same silence as TestSpawnDoesNotCheckTheImageOfAPullableStack below,
// reached through the WHOLE spawn rather than through checkStackImage in
// isolation: this one proves no `template ls` process is spent on the way.
func TestSpawnDoesNotCheckTheImageOfANotBuildableStack(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	answerTemplates(f, `{"images":[]}`)

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("a stack den cannot build must not be refused over its image: %v", err)
	}
	if f.HasCalled("template", "ls", "--json") {
		t.Errorf("the inventory must not even be read; calls: %v", f.Calls)
	}
}

// An `image:` pinned by DIGEST is left alone, and not even asked about: `sbx
// template ls --json` reports a repository and a tag and no digest, so the
// inventory cannot confirm or deny the pin. Reading that silence as "absent"
// would refuse a `den <nest>` over an image that is present — the false refusal
// the whole normalization exists to prevent.
func TestSpawnDoesNotCheckADigestPinnedImage(t *testing.T) {
	denHome, _ := denTest(t)
	withBuildableStack(t, denHome)
	// The provision.steps above arm the check; only the digest pin disarms it.
	// Must include both provision: (to make the stack buildable) and the digest
	// image: (to exercise the second silence), so the digest path is actually tested.
	write(t, filepath.Join(denHome, "stacks", "devx", "stack.yaml"),
		"image: devx@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\n"+
			"base: claude\nkits: [transverse]\nkit: devx-kit\n"+
			"provision:\n  steps: [./provision/setup.sh]\n")
	f, d := fakeDeps()
	answerTemplates(f, `{"images":[]}`)

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("a digest pin den cannot arbitrate must not be refused: %v", err)
	}
	if f.HasCalled("template", "ls", "--json") {
		t.Errorf("an inventory that carries no digest cannot answer, so it must not be read; calls: %v", f.Calls)
	}
	if !f.HasCalled("create") {
		t.Errorf("a create must have happened; calls: %v", f.Calls)
	}
}

// A failing inventory is fail-open. The check improves a message; it guards
// nothing — sbx still refuses the create by itself if the image really is
// absent, so a diagnostic that failed must not forbid a spawn.
func TestSpawnCreatesAnywayWhenTheImageInventoryIsUnreadable(t *testing.T) {
	denHome, _ := denTest(t)
	withBuildableStack(t, denHome)
	f, d := fakeDeps()
	answerTemplates(f, `{"templates":[]}`) // the key sbx does NOT use

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("an unreadable inventory must not refuse the spawn: %v", err)
	}
	if !f.HasCalled("create") {
		t.Errorf("a create must have happened; calls: %v", f.Calls)
	}
}

// Attaching needs no image: the VM stopped needing it the moment it was
// created. Refusing here would refuse a `den <nest>` that works.
func TestSpawnDoesNotCheckTheImageWhenAttaching(t *testing.T) {
	denHome, repo := denTest(t)
	withBuildableStack(t, denHome)
	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{Output: []byte(
		`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`)}
	answerTemplates(f, `{"images":[]}`)

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("attaching to a live sandbox must not consult the image inventory: %v", err)
	}
	if f.HasCalled("template", "ls", "--json") {
		t.Errorf("the inventory must not be read on the attach branch; calls: %v", f.Calls)
	}
	if !f.HasAttached("exec") {
		t.Errorf("the attach must have happened; attaches: %v", f.Attaches)
	}
}

// The spawn's image check keys off BUILDABILITY, not off a file on disk. A
// stack whose image: is one sbx pulls is left alone — "run `den build`" on a
// stack den cannot build is not advice, it is a second error.
//
// This test also pins the import-graph consequence: the verdict comes from
// config, so internal/spawn no longer needs internal/build at all.
func TestSpawnDoesNotCheckTheImageOfAPullableStack(t *testing.T) {
	fake := &sbx.Fake{}
	// A stack with no provision.steps: not buildable.
	s := &config.Stack{Name: "pulled", Image: "ghcr.io/acme/base:v3"}
	if err := checkStackImage(context.Background(), Deps{Sbx: fake}, s); err != nil {
		t.Fatalf("checkStackImage refused a pullable stack: %v", err)
	}
	if fake.HasCalled("template", "ls") {
		t.Error("den read the inventory for a stack it cannot build — it has no remedy to offer")
	}
}

func TestSpawnMountsCommandLineRepos(t *testing.T) {
	denHome, repo := denTest(t)
	hotfix := filepath.Join(t.TempDir(), "hotfix")
	createRepo(t, hotfix)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Repos: []string{hotfix}}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := workspacesOf(callStartingWith(f, "create"))
	expected := []string{hotfix, repo, filepath.Join(denHome, "agents", "claude")}
	if !slices.Equal(got, expected) {
		t.Errorf("workspaces = %v, expected %v — the positional comes first, because "+
			"Workspaces[0] is where the attached shell starts", got, expected)
	}
}

func TestSpawnMountsSeveralCommandLineReposInOrder(t *testing.T) {
	// A nest with NO `repos:` at all: the headline case. Without the
	// positionals its only workspace would be the agent profile, which is a
	// useless place to land.
	denHome, _ := denTest(t)
	write(t, filepath.Join(denHome, "nests", "scratch.yaml"), "stack: devx\n")
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	createRepo(t, a)
	createRepo(t, b)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "scratch", Repos: []string{a, b}}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := workspacesOf(callStartingWith(f, "create"))
	expected := []string{a, b, filepath.Join(denHome, "agents", "claude")}
	if !slices.Equal(got, expected) {
		t.Errorf("workspaces = %v, expected %v", got, expected)
	}
}

func TestSpawnRefusesAMissingCommandLineRepoWithoutBlamingTheNestFile(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()

	err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Repos: []string{filepath.Join(t.TempDir(), "gone")}}, d)
	if err == nil {
		t.Fatal("expected a refusal on a path that does not exist")
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("error = %q, expected it to name the command line — sending the user to edit "+
			"nests/api.yaml over a path they typed by hand is the wrong remedy", err)
	}
	if strings.Contains(err.Error(), "repos:") {
		t.Errorf("error = %q, expected it NOT to quote `repos:`", err)
	}
	if callStartingWith(f, "create") != nil {
		t.Error("a sandbox was created despite the refusal: everything rejectable from config " +
			"alone must be rejected before the first side effect")
	}
}

func TestSpawnStillBlamesTheNestFileForADeclaredRepo(t *testing.T) {
	// The counterpart, so "branches on origin" cannot degrade into "always says
	// command line".
	denHome, _ := denTest(t)
	write(t, filepath.Join(denHome, "nests", "api.yaml"),
		"stack: devx\nrepos:\n  - { path: "+filepath.Join(t.TempDir(), "gone")+" }\n")
	_, d := fakeDeps()

	err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "repos:") {
		t.Errorf("error = %q, expected it to send the user to `repos:`", err)
	}
}

func TestSpawnRefusesANonGitRepoUnderWorktreeBeforeCreatingAnything(t *testing.T) {
	denHome, _ := denTest(t)
	// early is a REAL git repo, given BEFORE data on the command line — it is
	// what makes this test discriminate. nest.Resolve prepends ad-hoc repos
	// ahead of declared ones, so the nest's OWN repo (from denTest) lands at
	// r.Repos[2], after both of these: step 3's loop would never reach it
	// either way, before or after this fix, so asserting on IT would pass
	// whether the fix works or not. early, at r.Repos[0], is what step 3
	// would have given a worktree to, before ever reaching data — and that
	// is the orphan this ordering exists to prevent.
	early := filepath.Join(t.TempDir(), "early")
	createRepo(t, early)
	data := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	f, d := fakeDeps()

	err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Worktree: "feat", Repos: []string{early, data}}, d)
	if err == nil {
		t.Fatal("expected a refusal: -w propagates a worktree to every repo, and data is not one")
	}
	if !strings.Contains(err.Error(), data) {
		t.Errorf("error = %q, expected it to name the offending path", err)
	}
	if !strings.Contains(err.Error(), "-w") {
		t.Errorf("error = %q, expected it to name the flag that made this fatal", err)
	}

	// The assertion that actually proves "before the first side effect": no
	// worktree exists for early, the repo that WOULD have gotten one first —
	// step 3's loop reaches data (not a git repo) only after early, so a
	// refusal originating there, instead of at 2bis, would already have
	// created early's worktree.
	//
	// The message alone proves nothing — it would read identically after the
	// damage was done.
	wt := filepath.Join(denHome, "worktrees", "feat", "early")
	if _, statErr := os.Stat(wt); statErr == nil {
		t.Errorf("%s exists: a worktree was created before the refusal, which is the orphan "+
			"this ordering exists to prevent", wt)
	}
	if callStartingWith(f, "create") != nil {
		t.Error("a sandbox was created despite the refusal")
	}
}

func TestSpawnGivesACommandLineRepoAWorktreeAndItsGitDir(t *testing.T) {
	denHome, repo := denTest(t)
	hotfix := filepath.Join(t.TempDir(), "hotfix")
	createRepo(t, hotfix)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Worktree: "feat", Repos: []string{hotfix}}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := workspacesOf(callStartingWith(f, "create"))
	expected := []string{
		filepath.Join(denHome, "worktrees", "feat", "hotfix"),
		filepath.Join(denHome, "worktrees", "feat", filepath.Base(repo)),
		// resolvedGitDir, not filepath.Join(x, ".git"): on macOS /var is a
		// symlink to /private/var, and git reports the resolved form — same
		// reason TestSpawnMountsEachRepoGitDirWithAWorktree above uses it.
		resolvedGitDir(t, hotfix),
		resolvedGitDir(t, repo),
		filepath.Join(denHome, "agents", "claude"),
	}
	if !slices.Equal(got, expected) {
		t.Errorf("workspaces = %v, expected %v — a repo given on the command line gets the "+
			"SAME treatment as a declared one: worktree, then its common git dir", got, expected)
	}
}

// The counterpart of TestSpawnRefusesANonGitRepoUnderWorktreeBeforeCreatingAnything:
// a non-git repo reached from `repos:` (not the command line) must be told
// which FILE to fix, the same way step 2's existence check already
// distinguishes the two origins.
func TestSpawnRefusesADeclaredNonGitRepoUnderWorktreeNamingTheNestFile(t *testing.T) {
	denHome, _ := denTest(t)
	data := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(denHome, "nests", "api.yaml"),
		"stack: devx\nrepos:\n  - { path: "+data+" }\n")
	_, d := fakeDeps()

	err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat"}, d)
	if err == nil {
		t.Fatal("expected a refusal: -w propagates a worktree to every repo, and data is not one")
	}
	if !strings.Contains(err.Error(), nest.FilePath(denHome, "api")) {
		t.Errorf("error = %q, expected it to name the nest file — this repo came from `repos:`, "+
			"not the command line, so `drop that path` is not a remedy anyone can follow", err)
	}
}
