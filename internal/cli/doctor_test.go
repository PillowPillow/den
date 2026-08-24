package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/doctor"
	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sshagent"
	"github.com/PillowPillow/den/internal/worktree"
)

// runDoctor runs `den doctor` on a given den home with injected system
// access. The test must owe nothing to the machine running it: without this
// injection, the command's exit contract ("non-zero if a check fails") is
// unverifiable anywhere.
// The sbx it scripts answers "no live sandbox": these tests carry no creation
// record, so the orphan check has nothing to compare and stays silent either
// way — runDoctorWithSbx is the helper for the tests that do exercise it.
func runDoctor(t *testing.T, home string, deps doctor.Deps) (string, error) {
	t.Helper()
	return runDoctorWithSbx(t, home, deps, &sbx.Fake{Responses: lsWith()})
}

func TestDoctorSucceedsWhenEverythingIsFine(t *testing.T) {
	home := testDenHome(t)
	out, err := runDoctor(t, home, doctor.FakeDeps())
	if err != nil {
		t.Fatalf("expected a nil exit on a healthy config, got: %v\n%s", err, out)
	}
	if !strings.Contains(out, "all good") {
		t.Errorf("output = %q, expected the final success message", out)
	}
	if strings.Contains(out, "[FAIL]") {
		t.Errorf("output = %q, expected no failure", out)
	}
}

// LA property of D2, and the reason the third level exists: a warning does
// NOT change `den doctor`'s exit code. Without it, the SSH_AUTH_SOCK check
// would turn `den doctor` red on any machine where one works locally without
// an SSH agent — a perfectly healthy configuration den has no way to
// distinguish from a faulty one.
//
// The test exercises the WHOLE command (Execute), not doctor.Run: it is cobra
// that turns the error returned by RunE into an exit code, so that is where
// the property is checked. `err == nil` is exactly what `den doctor` will
// render as an rc of 0.
func TestDoctorDoesNotFailOnAWarning(t *testing.T) {
	home := testDenHome(t)
	deps := doctor.FakeDeps()
	// No SSH agent. The fixture's config does not declare `ssh:`, so the mode
	// is the default, agent-forward — the nominal case, not a corner one.
	deps.Getenv = func(string) string { return "" }

	out, err := runDoctor(t, home, deps)
	if err != nil {
		t.Fatalf("a warning must not fail den doctor, got: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[warn]") {
		t.Errorf("output = %q, expected a [warn] line", out)
	}
	if strings.Contains(out, "[FAIL]") {
		t.Errorf("output = %q, expected no failure: a missing SSH agent is not one", out)
	}
	// The warning must stay visible in the final summary: "all good" under a
	// [warn] line would read as leftover output.
	if strings.Contains(out, "all good") {
		t.Errorf("output = %q, \"all good\" contradicts the [warn] line", out)
	}
	if !strings.Contains(out, "warning") {
		t.Errorf("output = %q, the final summary must report the warning", out)
	}
	if !strings.Contains(out, "SSH_AUTH_SOCK") {
		t.Errorf("output = %q, expected the diagnostic naming SSH_AUTH_SOCK", out)
	}
}

// The socket-present blind spot, end to end: SSH_AUTH_SOCK is set but the
// agent behind it holds no key. The old check called that OK; now it is a
// [warn] line — non-blocking (rc 0), naming a fix — that `den doctor` renders
// through the whole command, not just doctor.Run.
func TestDoctorWarnsWhenTheForwardedAgentIsEmpty(t *testing.T) {
	home := testDenHome(t)
	deps := doctor.FakeDeps() // socket present (FakeSSHSocket), config default agent-forward
	deps.SSHAgent = func() sshagent.Result { return sshagent.Result{State: sshagent.StateEmpty} }

	out, err := runDoctor(t, home, deps)
	if err != nil {
		t.Fatalf("an empty agent is a warning, not a failure: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[warn]") {
		t.Errorf("output = %q, expected a [warn] line for the empty agent", out)
	}
	if strings.Contains(out, "[FAIL]") {
		t.Errorf("output = %q, an empty agent must not fail den doctor", out)
	}
	if !strings.Contains(out, "ssh-add") {
		t.Errorf("output = %q, the warning must offer a concrete fix", out)
	}
}

// I1 — the COMBINATION nothing exercised: a warning AND a failure in the same
// diagnostic. The two neighboring tests only ever produce one at a time —
// TestDoctorDoesNotFailOnAWarning sets `Getenv → ""` on an otherwise healthy
// config, TestDoctorFailsWhenSbxIsMissing keeps the socket — and neither
// reaches the one place where the question actually arises.
//
// What this test locks, and nothing did before: the ORDER of newDoctorCmd's
// two output blocks. The "failure" block must precede the "warning" block, or
// the second returns nil before the first is ever reached. Measured before
// writing this test, with the two blocks swapped: the WHOLE suite stayed
// green (rc=0), and the binary returned 0 on a broken install while printing
// "no failure" under a [FAIL] line.
//
// A warning must therefore be exactly what it claims: without effect on the
// exit code, in BOTH directions — it does not create one (neighboring test),
// and it does not erase one (this one).
func TestDoctorFailsEvenWithAWarning(t *testing.T) {
	home := testDenHome(t)
	deps := doctor.FakeDeps()
	// The warning: no SSH agent, on a config whose SSH mode is the default
	// (agent-forward — the fixture declares no `ssh:` block).
	deps.Getenv = func(string) string { return "" }
	// The failure, INDEPENDENT of the first: sbx missing from PATH. Two
	// unrelated causes, so neither can be mistaken for an effect of the other.
	deps.LookPath = func(string) (string, error) { return "", errors.New("not found in PATH") }

	out, err := runDoctor(t, home, deps)
	if err == nil {
		t.Fatalf("a failure must STAY a failure in the presence of a warning: "+
			"`den doctor` would return 0 on a broken install; output:\n%s", out)
	}
	// The error counts ONLY failures: a warning counted as a failure would be
	// the other half of the defect.
	if !strings.Contains(err.Error(), "1 failing check(s)") {
		t.Errorf("error = %q, expected a count of failures only (1); "+
			"the warning must not be counted in it", err.Error())
	}
	// Both lines coexist: the warning does not hide the failure, and the
	// failure does not silence the warning.
	if !strings.Contains(out, "[FAIL]") {
		t.Errorf("output = %q, expected a [FAIL] line", out)
	}
	if !strings.Contains(out, "[warn]") {
		t.Errorf("output = %q, expected a [warn] line: a failure must not silence the warning", out)
	}
	// The epilogue must assert NOTHING reassuring. "no failure" under a [FAIL]
	// line is self-contradictory, and that is what the output showed with the
	// two blocks swapped.
	for _, forbidden := range []string{"no failure", "all good"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("output = %q, must not contain %q while a diagnostic is failing",
				out, forbidden)
		}
	}
}

func TestDoctorFailsWhenSbxIsMissing(t *testing.T) {
	home := testDenHome(t)
	deps := doctor.FakeDeps()
	deps.LookPath = func(string) (string, error) { return "", errors.New("not found in PATH") }

	out, err := runDoctor(t, home, deps)
	if err == nil {
		t.Fatal("expected an error: a missing sbx is a diagnostic failure")
	}
	if !strings.Contains(out, "[FAIL]") {
		t.Errorf("output = %q, expected a [FAIL] line", out)
	}
	if !strings.Contains(out, "sbx") {
		t.Errorf("output = %q, expected the sbx diagnostic", out)
	}
	// The other diagnostics must still be printed: den never stops at the
	// first problem.
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("output = %q, expected the config.yaml diagnostic despite sbx failing", out)
	}
}

// The tests above build the command directly to inject their Deps, which
// leaves root.go's wiring (newDoctorCmd plugged into the root tree with
// doctor.SystemDeps()) uncovered. This one goes through NewRootCmd: it only
// asserts reachability and output, never the exit code tied to sbx.
func TestDoctorIsWiredIntoTheRootTree(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())
	out, err := run(t, "doctor")
	// config.yaml is missing: at least one diagnostic fails, whether sbx is
	// installed on the machine or not.
	if err == nil {
		t.Error("expected an error: config.yaml is missing from the den home")
	}
	if !strings.Contains(out, "den home:") {
		t.Errorf("output = %q, expected doctor's header", out)
	}
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("output = %q, expected the config.yaml diagnostic", out)
	}
}

func TestDoctorFailsOnMissingConfig(t *testing.T) {
	out, err := runDoctor(t, t.TempDir(), doctor.FakeDeps())
	if err == nil {
		t.Error("expected an error when the config is missing")
	}
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("output = %q, expected a mention of config.yaml", out)
	}
}

// TestDoctorForceWithoutFixRefuses pins the fix round 3 MINOR item: --force
// has no meaning except as a modifier of --fix, and a user who types it
// alone must be told so rather than getting a plain report with no sign the
// flag did anything.
func TestDoctorForceWithoutFixRefuses(t *testing.T) {
	home := testDenHome(t)
	f := &sbx.Fake{Responses: lsWith()}

	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), f, "--force")
	if err == nil {
		t.Fatal("expected --force without --fix to be refused")
	}
	if !strings.Contains(err.Error(), "--fix") {
		t.Errorf("refusal does not name --fix: %v", err)
	}
	// The refusal happens before the report: nothing about the checks
	// leaks into an error the user gave a nonsensical flag combination for.
	if strings.Contains(out, "den home:") {
		t.Errorf("output = %q, expected no report printed before the refusal", out)
	}
}

// TestDoctorFixForceStillBehavesAsBefore pins that 6c's new refusal changes
// NOTHING about the combination `--fix --force` already had: the dirty-
// worktree reclaim in TestDoctorFixRefusesADirtyWorktreeUnlessForced above
// covers the substance of that behavior; this asserts the narrower fact that
// `--fix --force` on a CLEAN state still succeeds plainly, same as before
// this fix existed.
func TestDoctorFixForceStillBehavesAsBefore(t *testing.T) {
	home := testDenHome(t)
	wt := orphanFixture(t, home, "api.feat12")
	f := &sbx.Fake{Responses: lsWith()}

	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), f, "--fix", "--force")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the orphaned worktree must be reclaimed: %v", err)
	}
}

// runDoctorWithSbx is runDoctor with a scripted sbx and real git: `den doctor`
// now asks which sandboxes are live (the orphan check) and, under --fix, moves
// worktrees. Both accesses are injected for the reason the whole file exists —
// the suite must owe nothing to the machine running it.
func runDoctorWithSbx(t *testing.T, home string, deps doctor.Deps, runner sbx.Runner, args ...string) (string, error) {
	t.Helper()
	cmd := newDoctorCmd(&home, deps, runner, worktree.NewGit())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// orphanFixture records a sandbox with one REAL worktree and returns the
// worktree's path. The record is what `den doctor` reads; the sandbox itself
// never exists, which is exactly the state under test.
func orphanFixture(t *testing.T, home, sandbox string) string {
	t.Helper()
	root := filepath.Join(home, "worktrees")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, root, "feat12")
	writeManifest(t, home, manifest.Manifest{
		Sandbox:  sandbox,
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(home, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	return wt
}

// Proof 12 — a `den rm --keep-worktrees` leaves a record on purpose, and
// doctor is what makes it addressable.
func TestDoctorReportsAnOrphanRecord(t *testing.T) {
	home := testDenHome(t)
	orphanFixture(t, home, "api.feat12")
	f := &sbx.Fake{Responses: lsWith()}

	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), f)
	if err != nil {
		t.Fatalf("leftover directories are not a broken installation: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[warn]") || !strings.Contains(out, "api.feat12") {
		t.Errorf("the orphaned record must be reported and named; got:\n%s", out)
	}
	if !strings.Contains(out, "den doctor --fix") {
		t.Errorf("the remedy must be named; got:\n%s", out)
	}
}

// --fix sends the orphaned worktrees to the trash and only then drops the
// record. Same strictness as rm: den never deletes, it moves.
func TestDoctorFixReclaimsOrphanedWorktrees(t *testing.T) {
	home := testDenHome(t)
	wt := orphanFixture(t, home, "api.feat12")
	f := &sbx.Fake{Responses: lsWith()}

	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), f, "--fix")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the orphaned worktree must be reclaimed: %v", err)
	}
	if !strings.Contains(out, filepath.Join(home, "trash")) {
		t.Errorf("the output must say where the work went; got:\n%s", out)
	}
	if _, err := manifest.Read(home, "api.feat12"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the record goes once what it lists is reclaimed: %v", err)
	}
}

// Proof 13 — a dirty worktree stops --fix, exactly as it stops rm. --force is
// the same consent, with the same effect: the trash, never deletion.
func TestDoctorFixRefusesADirtyWorktreeUnlessForced(t *testing.T) {
	home := testDenHome(t)
	wt := orphanFixture(t, home, "api.feat12")
	if err := os.WriteFile(filepath.Join(wt, "draft.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{Responses: lsWith()}

	if _, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), f, "--fix"); err == nil {
		t.Fatal("uncommitted work must stop --fix")
	}
	if _, err := os.Stat(filepath.Join(wt, "draft.txt")); err != nil {
		t.Errorf("the uncommitted work must be intact: %v", err)
	}
	if _, err := manifest.Read(home, "api.feat12"); err != nil {
		t.Errorf("the record must survive a refused reclaim — it is the only trace "+
			"of the directory still on disk: %v", err)
	}

	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), f, "--fix", "--force")
	if err != nil {
		t.Fatalf("with --force the reclaim must go through: %v\n%s", err, out)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("%s must have left its place: %v", wt, err)
	}
	if !strings.Contains(out, filepath.Join(home, "trash")) {
		t.Errorf("--force moves to the trash, it never deletes; got:\n%s", out)
	}
}

// Proof 14, end to end: sbx unreachable means the live list is unknown, and
// den must not accuse a healthy sandbox.
func TestDoctorSkipsTheOrphanCheckWhenSbxCannotAnswer(t *testing.T) {
	home := testDenHome(t)
	orphanFixture(t, home, "api.feat12")
	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Err: errors.New("sbx: command not found")},
	}}

	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), f)
	if err != nil {
		t.Fatalf("an unanswerable question is not a failing check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skipped") {
		t.Errorf("the skip must be visible; got:\n%s", out)
	}
	if strings.Contains(out, "api.feat12") {
		t.Errorf("den must accuse nobody when it cannot tell orphans from healthy "+
			"sandboxes; got:\n%s", out)
	}
}

// A machine whose sbx network policy den cannot READ fails `den doctor`, and
// the line carries sbx's own remedy.
//
// This is the check the 2026-08-18 report had nothing to offer: sbx demands a
// one-time `sbx policy init <profile>`, and on a laptop that never ran it every
// den command touching policy died — `den source add` while converging, `den
// up` in the settle loop — while `den doctor` reported a healthy install. A
// diagnostic that stays green on a machine den cannot use is worse than none:
// it sends the user looking somewhere else.
func TestDoctorFailsWhenTheNetworkPolicyCannotBeRead(t *testing.T) {
	home := testDenHome(t)
	f := &sbx.Fake{Responses: lsWith()}
	f.Responses["policy ls --type network --source local --decision allow --json"] = sbx.Response{
		Err: errors.New("ERROR: global network policy has not been initialized\n\n" +
			"Initialize it with:\n  sbx policy init <allow-all|balanced|deny-all>"),
	}

	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), f)
	if err == nil {
		t.Fatalf("den doctor reported a machine den cannot converge as healthy:\n%s", out)
	}
	if !strings.Contains(out, "[FAIL] sbx policy") {
		t.Errorf("the failing read has no line of its own:\n%s", out)
	}
	// One line, whatever sbx's stderr looks like: the report is a column of
	// checks, and a four-line paragraph in the middle of it breaks the reading.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "[FAIL] sbx policy") && !strings.Contains(line, "sbx policy init") {
			t.Errorf("the check does not carry sbx's own remedy on its line:\n%s", out)
		}
	}
}

// The check is a READ, not an assumption: a machine that answers is not
// reported as broken, and `den doctor` stays green.
func TestDoctorPassesWhenTheNetworkPolicyAnswers(t *testing.T) {
	home := testDenHome(t)
	f := &sbx.Fake{Responses: lsWith()}
	f.Responses["policy ls --type network --source local --decision allow --json"] = sbx.Response{
		Output: []byte(`{"rules":[]}`),
	}

	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), f)
	if err != nil {
		t.Fatalf("a readable policy must not fail den doctor: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[ok  ] sbx policy") {
		t.Errorf("the check is missing from a healthy report:\n%s", out)
	}
}

// Issue #88's own property, end to end: `den doctor` NAMES the machine state
// no source declares, and stays out of the exit code while doing it.
//
// The home holds no source at all, which is the case the report must handle
// rather than skip: den declares nothing, sbx wrote something, and every entry
// present is therefore undeclared. A doctor silent here would describe a
// machine reachable by three other authors as a machine den fully describes —
// the blind spot spec §2 forbids.
func TestDoctorNamesTheSbxStateNoSourceDeclares(t *testing.T) {
	home := testDenHome(t)
	m := sbx.NewMachine()
	m.Services["github"] = true
	m.Registries["ghcr.io:443"] = true
	m.MCPServers["notion"] = true

	out, err := runDoctorWithSbx(t, home, doctor.FakeDeps(), m)
	if err != nil {
		t.Fatalf("undeclared state must not fail the report: %v\n%s", err, out)
	}
	if strings.Contains(out, "[warn] sbx secrets") || strings.Contains(out, "[FAIL] sbx secrets") {
		t.Errorf("the undeclared-state report weighs on the exit contract:\n%s", out)
	}
	secrets := reportLine(t, out, "sbx secrets")
	for _, want := range []string{"present, undeclared", "service github", "registry ghcr.io:443"} {
		if !strings.Contains(secrets, want) {
			t.Errorf("the secrets line %q is missing %q", secrets, want)
		}
	}
	if mcp := reportLine(t, out, "sbx mcp servers"); !strings.Contains(mcp, "present, undeclared") {
		t.Errorf("a registered MCP server is not reported: %q", mcp)
	}
	// The skills store is empty on this machine, and "empty" must read as
	// empty rather than as one more thing to go and look at.
	if skills := reportLine(t, out, "sbx skills"); strings.Contains(skills, "undeclared") {
		t.Errorf("an empty skills store is reported as undeclared: %q", skills)
	}
	// The report is a diagnosis, never a cleanup: den removes nothing it did
	// not create, here or anywhere.
	if m.HasCalled("mcp", "rm") || m.HasCalled("secret", "rm") {
		t.Errorf("den removed sbx state it did not create: %v", m.Calls)
	}
}
