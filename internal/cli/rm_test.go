package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
)

// writeUnder writes a den configuration file, relative to denHome, creating
// the necessary parent directories. writeConfig/writeStack/writeNest are named
// facades over it: one body to maintain rather than three near-identical
// copies.
func writeUnder(t *testing.T, denHome, rel, content string) {
	t.Helper()
	p := filepath.Join(denHome, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, denHome, content string) {
	t.Helper()
	writeUnder(t, denHome, "config.yaml", content)
}

func writeStack(t *testing.T, denHome, name, content string) {
	t.Helper()
	writeUnder(t, denHome, filepath.Join("stacks", name, "stack.yaml"), content)
}

func writeNest(t *testing.T, denHome, name, content string) {
	t.Helper()
	writeUnder(t, denHome, filepath.Join("nests", name+".yaml"), content)
}

// minimalConfig is enough for every test in this file that does not exercise
// agent resolution itself.
const minimalConfig = `agents:
  claude:
    config_dir: /profile/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`

// lsWith scripts `sbx ls --json` to render exactly these sandboxes, all
// "running".
func lsWith(names ...string) map[string]sbx.Response {
	var b strings.Builder
	b.WriteString(`{"sandboxes":[`)
	for i, n := range names {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"name":"` + n + `","status":"running","workspaces":["/w"]}`)
	}
	b.WriteString(`]}`)
	return map[string]sbx.Response{"ls --json": {Output: []byte(b.String())}}
}

// createTestRepo creates a real git repo, with an initial commit, at the
// given path. Git environment neutralization already ensured package-wide by
// TestMain (main_test.go).
func createTestRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@example.test"},
		{"config", "user.name", "T"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", c...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", c, err, out)
		}
	}
}

func TestRmDestroysTheSandbox(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	envPath := writeEnvRecord(t, denHome, "api")
	f := &sbx.Fake{Responses: lsWith("api")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("calls: %v", f.Calls)
	}
}

// writeEnvRecord puts a .sbxenv.yaml where den will look for it, describing
// exactly that sandbox — the ordinary case. A thin facade over
// writeEnvRecordAt (atSandbox == describesSandbox): one body to maintain
// rather than two copies that would drift the day sbx.EnvFile's required
// fields change.
func writeEnvRecord(t *testing.T, denHome, sandbox string) string {
	t.Helper()
	return writeEnvRecordAt(t, denHome, sandbox, sandbox)
}

// writeEnvRecordAt puts a well-formed, readable `.sbxenv.yaml` at the path
// `den rm atSandbox` will look for, but carrying `name: describesSandbox` —
// reproducing the one case CheckEnvFile's second argument exists to catch: a
// record den can decode perfectly, sitting under the right sandbox's
// directory, that nonetheless does NOT describe that sandbox. Written through
// sbx.EnvFile rather than by hand: a hand-written fixture would drift from
// what den really emits, and half of this file's tests are precisely about
// den reading back its own emission.
func writeEnvRecordAt(t *testing.T, denHome, atSandbox, describesSandbox string) string {
	t.Helper()
	out, err := sbx.EnvFile(sbx.Env{
		Name:       describesSandbox,
		Image:      "devx:v1",
		MixinKit:   filepath.Join(denHome, "cache", "mixins", describesSandbox),
		Workspaces: []string{"/dev/api", "/profile/claude"},
	})
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	path, err := manifest.SbxEnvPath(denHome, atSandbox)
	if err != nil {
		t.Fatalf("SbxEnvPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The name-mismatch protection (task 5, CheckEnvFile's second argument) is
// pinned by a unit test in internal/sbx alone (fix round 1, finding 2's own
// test suite) — nothing in internal/cli fails if that argument is dropped or
// mis-wired. This is the integration test that closes the hole: a record
// under state/sandboxes/api/ that reads fine but names "web" must still make
// `den rm api` refuse, on EITHER route (with or without --force is not the
// point here — plain `den rm api` must never reach a destroy call at all),
// because `sbx env rm` resolves the sandbox FROM the file, and handing it
// this file would destroy web while the user typed `den rm api`.
func TestRmRefusesARecordNamingAnotherSandbox(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	envPath := writeEnvRecordAt(t, denHome, "api", "web")

	f := &sbx.Fake{Responses: lsWith("api")}
	_, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api")

	if err == nil {
		t.Fatal("den destroyed a sandbox whose record names a different sandbox")
	}
	if !strings.Contains(err.Error(), "api") || !strings.Contains(err.Error(), "web") {
		t.Errorf("the refusal does not name the mismatch (api vs web): %v", err)
	}
	if !strings.Contains(err.Error(), envPath) {
		t.Errorf("the refusal does not name the file: %v", err)
	}
	// Nothing destroyed, on either route: web must not go, and neither must api.
	if f.HasCalled("env", "rm") || f.HasCalled("rm", "--force") {
		t.Errorf("den destroyed after refusing; calls: %v", f.Calls)
	}
	// The record survives: den never deletes a file it could not vouch for.
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("den deleted a record it could not vouch for: %v", err)
	}
}

// The normal path: den hands sbx the file it emitted, and `-f` is not optional
// — `sbx env rm` prompts for confirmation without it (measured 2026-08-25), and
// a prompt in a non-interactive `den rm` blocks forever.
func TestRmRemovesThroughSbxEnvRm(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	envPath := writeEnvRecord(t, denHome, "api")

	f := &sbx.Fake{Responses: lsWith("api")}
	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("den did not destroy through `sbx env rm`; calls: %v", f.Calls)
	}
	if f.HasCalled("rm", "--force", "api") {
		t.Errorf("den destroyed by name on the normal path; calls: %v", f.Calls)
	}
	// Post-probe leak fix (2026-08-25): a real `sbx env rm -f` was measured to
	// NOT delete the .sbxenv.yaml it is handed — `sbx env rm --help` never
	// claims to. This is the PRIMARY route: CheckEnvFile passed, so den could
	// read this file, and it has just successfully consumed it — the opposite
	// of the spec §11 case ("never delete a record den could NOT read"), so
	// den removes it itself. Without this, state/sandboxes/api/ — a directory
	// §11 promises is never purged — would keep a stale file after every
	// successful `den rm` forever, invisible to `den ls`/`den doctor` (task 2's
	// manifest.List already treats a directory with no manifest.yaml as the
	// ordinary shape a forced removal leaves).
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Errorf("den left the .sbxenv.yaml behind after successfully consuming it: stat = %v", err)
	}
	// The directory itself must go too: manifest.Remove already tried to clear
	// it (called from cleanWorktrees, upstream of the destroy call) and found
	// it non-empty because THIS file was still in it — removing the file and
	// retrying is what finishes that job, and a leftover empty directory is
	// still a leak in a never-purged tree.
	if dir, err := manifest.SandboxDir(denHome, "api"); err != nil {
		t.Fatal(err)
	} else if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("den left an empty sandbox directory behind: %s (stat = %v)", dir, statErr)
	}
}

// The refusal, and the whole point of it: `sbx env rm` resolves the sandbox FROM
// the file, so an unreadable file is not a detail den can route around (§5.7 —
// a limitation is documented, never worked around by a second permanent path).
// The message must name the file AND the flag that unblocks the same command.
func TestRmRefusesAnUnreadableEnvRecordAndNamesForce(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	envPath := writeEnvRecord(t, denHome, "api")
	// A NEWER den's file: good YAML, a schemaVersion this den does not emit.
	if err := os.WriteFile(envPath, []byte("schemaVersion: \"9\"\nagent: shell\nname: api\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api")}
	_, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api")

	if err == nil {
		t.Fatal("den destroyed a sandbox whose record it could not read")
	}
	if !strings.Contains(err.Error(), envPath) {
		t.Errorf("the refusal does not name the file: %v", err)
	}
	if !strings.Contains(err.Error(), "den rm --force api") {
		t.Errorf("the refusal does not name the remedy: %v", err)
	}
	// Nothing was destroyed, on either route.
	if f.HasCalled("env", "rm", "-f", envPath) || f.HasCalled("rm", "--force", "api") {
		t.Errorf("den destroyed after refusing; calls: %v", f.Calls)
	}
	// And the record it could not read survives (spec §11).
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("den deleted a record it could not read: %v", err)
	}

	// The composition fix round 1 (finding 5) found untested at the `den rm`
	// level: unreadable record + --force + the file is still there afterward.
	// --force is what a sandbox with no vouchable record has always needed
	// (§5.8) — it is not "trust this file anyway", it is "destroy by name
	// instead", and the file this den could not read is never the thing
	// --force authorizes touching.
	f2 := &sbx.Fake{Responses: lsWith("api")}
	if _, err := executeCmdWithSbx(t, f2, "--den-home", denHome, "rm", "api", "--force"); err != nil {
		t.Fatalf("with --force, the rm must succeed despite the unreadable record: %v", err)
	}
	if !f2.HasCalled("rm", "--force", "api") {
		t.Errorf("--force must destroy through the conceded fallback, by name; calls: %v", f2.Calls)
	}
	if f2.HasCalled("env", "rm") {
		t.Errorf("den must not hand sbx a record it could not vouch for, even under --force; "+
			"calls: %v", f2.Calls)
	}
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("den deleted a record it could not read, even under --force: %v", err)
	}
}

// The other cause, and it must read differently: no file at all means this
// sandbox predates the emitter (or was never created by den), which is a fact
// about WHEN the sandbox was made — not a corruption report about a file the
// user never knew existed. Both causes name the same remedy.
func TestRmRefusesAnAbsentEnvRecordAndNamesForce(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	// No writeEnvRecord at all: this sandbox has no .sbxenv.yaml, the ordinary
	// state of every sandbox created before the emitter shipped.
	envPath, err := manifest.SbxEnvPath(denHome, "api")
	if err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api")}
	_, err = executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api")

	if err == nil {
		t.Fatal("den destroyed a sandbox with no record to hand sbx")
	}
	if !strings.Contains(err.Error(), "predates the emitter") {
		t.Errorf("the absent cause must read as a fact about age, not a corruption report: %v", err)
	}
	if strings.Contains(err.Error(), "no such file or directory") {
		t.Errorf("the raw os.ReadFile diagnosis is CheckEnvFile's UNREADABLE-file sentence, not "+
			"the ABSENT one: %v", err)
	}
	if !strings.Contains(err.Error(), envPath) {
		t.Errorf("the refusal does not name the file den looked for: %v", err)
	}
	if !strings.Contains(err.Error(), "den rm --force api") {
		t.Errorf("the refusal does not name the remedy: %v", err)
	}
	if f.HasCalled("env", "rm") || f.HasCalled("rm", "--force", "api") {
		t.Errorf("den destroyed after refusing; calls: %v", f.Calls)
	}
}

// The absent-record cause, on the --force side this time — the commonest case
// in the whole plan (a pre-switchover sandbox, created before the emitter
// shipped) taking the announcement branch. It must read as the same fact
// TestRmRefusesAnAbsentEnvRecordAndNamesForce pins for the refusal: "predates
// the emitter", never a raw os.ReadFile "no such file or directory" and never
// wrapped in an "unreadable" label — the file is not unreadable, it does not
// exist.
func TestRmForceAnnouncesTheByNameDestructionForAnAbsentRecord(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	// No writeEnvRecord at all.
	envPath, err := manifest.SbxEnvPath(denHome, "api")
	if err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "--force", "api")
	if err != nil {
		t.Fatalf("--force must destroy, not refuse: %v", err)
	}
	if !f.HasCalled("rm", "--force", "api") {
		t.Errorf("--force did not destroy by name; calls: %v", f.Calls)
	}
	if !strings.Contains(out, "predates the emitter") {
		t.Errorf("the absent cause must read as a fact about age, not a corruption report:\n%s", out)
	}
	if strings.Contains(out, "no such file or directory") {
		t.Errorf("the raw os.ReadFile diagnosis is CheckEnvFile's UNREADABLE-file sentence, not "+
			"the ABSENT one:\n%s", out)
	}
	if !strings.Contains(out, envPath) {
		t.Errorf("the announcement does not name the file den looked for:\n%s", out)
	}
	if !strings.Contains(out, "by name") {
		t.Errorf("the announcement does not say the destruction is by name:\n%s", out)
	}
	if !strings.Contains(out, "sbx secret ls --sandbox api") {
		t.Errorf("the announcement does not name how to see the secrets left behind:\n%s", out)
	}
	if f.HasCalled("env", "rm") {
		t.Errorf("den handed sbx a record it could not vouch for; calls: %v", f.Calls)
	}
	// ORDER, proven the only way that has real teeth: by making the destroy
	// call ITSELF fail. A string-position check against the final success
	// line does NOT catch "the announce block moved to sit after
	// runner.Run(ctx, \"rm\", \"--force\", name)" (fix round 1, finding 1) —
	// both prints still land, in the same relative order to EACH OTHER, no
	// matter which side of that Run call they sit on; only their order
	// relative to the Run call itself is the property under test, and that is
	// not observable from string positions in a successful run. Scripting the
	// destroy call to fail makes it observable: if the announcement runs
	// AFTER runner.Run, the failing call returns before den ever reaches it,
	// and `out` (which keeps whatever was written before the early return)
	// would not carry it.
	f2 := &sbx.Fake{Responses: lsWith("api")}
	f2.Responses["rm --force api"] = sbx.Response{Err: errors.New("sbx: boom")}
	out2, err2 := executeCmdWithSbx(t, f2, "--den-home", denHome, "rm", "--force", "api")
	if err2 == nil {
		t.Fatal("expected the destroy call's failure to propagate")
	}
	// Same cause as phase one — the absent-record token, not a generic "by
	// name": confirms this second run took the same branch, not some other
	// path that also happens to print "by name".
	if !strings.Contains(out2, "predates the emitter") || !strings.Contains(out2, "by name") {
		t.Errorf("the announcement was not printed before the (failing) destroy call:\n%s", out2)
	}
	// The other half of the property: den must NOT have reached the success
	// line. Without this, a change that swallowed the Run error would leave
	// this test green while den printed "destroyed" for a sandbox it never
	// destroyed.
	if strings.Contains(out2, "destroyed (the agent profile is kept)") {
		t.Errorf("den announced success after a failed destroy call:\n%s", out2)
	}
}

// Second sense exercised: den says so BEFORE acting, names why, and names what
// is left behind. `sbx env rm` is what removes the sandbox-scoped secrets; a
// destruction by name does not, so the user has to be told and given the
// command.
func TestRmForceAnnouncesTheByNameDestruction(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	envPath := writeEnvRecord(t, denHome, "api")
	if err := os.WriteFile(envPath, []byte("schemaVersion: \"9\"\nagent: shell\nname: api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{Responses: lsWith("api")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "--force", "api")
	if err != nil {
		t.Fatalf("--force must destroy, not refuse: %v", err)
	}
	if !f.HasCalled("rm", "--force", "api") {
		t.Errorf("--force did not destroy by name; calls: %v", f.Calls)
	}
	if !strings.Contains(out, envPath) {
		t.Errorf("the announcement does not name the unreadable record:\n%s", out)
	}
	if !strings.Contains(out, "by name") {
		t.Errorf("the announcement does not say the destruction is by name:\n%s", out)
	}
	if !strings.Contains(out, "sbx secret ls --sandbox api") {
		t.Errorf("the announcement does not name how to see the secrets left behind:\n%s", out)
	}
	if f.HasCalled("env", "rm") {
		t.Errorf("den handed sbx a record it could not vouch for; calls: %v", f.Calls)
	}
	// ORDER, same technique as TestRmForceAnnouncesTheByNameDestructionForAnAbsentRecord
	// (see its comment for why a string-position check against the success
	// line does not actually catch the announce block moving to after
	// runner.Run): make the destroy call fail, and check the announcement
	// still reached `out` before den returned the propagated error.
	f2 := &sbx.Fake{Responses: lsWith("api")}
	f2.Responses["rm --force api"] = sbx.Response{Err: errors.New("sbx: boom")}
	out2, err2 := executeCmdWithSbx(t, f2, "--den-home", denHome, "rm", "--force", "api")
	if err2 == nil {
		t.Fatal("expected the destroy call's failure to propagate")
	}
	// Same cause as phase one — schemaVersion, the unreadable-file token, not
	// a generic "by name": confirms this second run took the same branch.
	if !strings.Contains(out2, "schemaVersion") || !strings.Contains(out2, "by name") {
		t.Errorf("the announcement was not printed before the (failing) destroy call:\n%s", out2)
	}
	if strings.Contains(out2, "destroyed (the agent profile is kept)") {
		t.Errorf("den announced success after a failed destroy call:\n%s", out2)
	}
	// The unreadable file SURVIVES: den never deletes what it could not read.
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("den deleted a record it could not read: %v", err)
	}
}

// The third cause on the --force side (fix round 1, finding 2): a record
// den can decode perfectly, sitting under state/sandboxes/api/, but whose
// `name:` says "web". The behaviour is right by construction — the guard is
// `envErr != nil && force`, and `cause` is computed once above both branches
// — but until this test, no announce-side assertion pinned it: only the
// refusal test (TestRmRefusesARecordNamingAnotherSandbox) covered the
// mismatch cause, and only on the !force route. This one matters more than
// the other two announce tests: the wording has to carry, correctly, that
// den is about to destroy "api" by name PRECISELY BECAUSE the file could not
// be proven to describe "api" — not because it is corrupt or absent.
func TestRmForceAnnouncesTheByNameDestructionForAMismatchedRecord(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	envPath := writeEnvRecordAt(t, denHome, "api", "web")

	f := &sbx.Fake{Responses: lsWith("api")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "--force", "api")
	if err != nil {
		t.Fatalf("--force must destroy, not refuse: %v", err)
	}
	// By NAME: "api", the sandbox the user typed — never "web", the name the
	// mismatched file claims. `sbx rm` resolves by the argument it is given,
	// unlike `sbx env rm`, which is exactly why this fallback exists.
	if !f.HasCalled("rm", "--force", "api") {
		t.Errorf("--force did not destroy \"api\" by name; calls: %v", f.Calls)
	}
	if f.HasCalled("rm", "--force", "web") {
		t.Errorf("den destroyed \"web\", the name the mismatched file claims, instead of \"api\", "+
			"the name the user typed; calls: %v", f.Calls)
	}
	// The cause-specific sentence: CheckEnvFile's own mismatch diagnosis,
	// naming both sandboxes, not a generic "unreadable" or "absent" wording.
	if !strings.Contains(out, "web") || !strings.Contains(out, "api") {
		t.Errorf("the announcement does not name the mismatch (api vs web):\n%s", out)
	}
	if !strings.Contains(out, "does not describe the sandbox") {
		t.Errorf("the announcement does not carry CheckEnvFile's mismatch diagnosis:\n%s", out)
	}
	if !strings.Contains(out, envPath) {
		t.Errorf("the announcement does not name the file:\n%s", out)
	}
	if !strings.Contains(out, "by name") {
		t.Errorf("the announcement does not say the destruction is by name:\n%s", out)
	}
	if !strings.Contains(out, "sbx secret ls --sandbox api") {
		t.Errorf("the announcement does not name how to see the secrets left behind:\n%s", out)
	}
	if f.HasCalled("env", "rm") {
		t.Errorf("den handed sbx a record it could not vouch for; calls: %v", f.Calls)
	}
	// ORDER, same technique as TestRmForceAnnouncesTheByNameDestructionForAnAbsentRecord.
	f2 := &sbx.Fake{Responses: lsWith("api")}
	f2.Responses["rm --force api"] = sbx.Response{Err: errors.New("sbx: boom")}
	out2, err2 := executeCmdWithSbx(t, f2, "--den-home", denHome, "rm", "--force", "api")
	if err2 == nil {
		t.Fatal("expected the destroy call's failure to propagate")
	}
	// Same cause as phase one — the mismatch diagnosis, not a generic "by
	// name": confirms this second run took the same branch.
	if !strings.Contains(out2, "does not describe the sandbox") || !strings.Contains(out2, "by name") {
		t.Errorf("the announcement was not printed before the (failing) destroy call:\n%s", out2)
	}
	if strings.Contains(out2, "destroyed (the agent profile is kept)") {
		t.Errorf("den announced success after a failed destroy call:\n%s", out2)
	}
	// The mismatched file SURVIVES: den never deletes what it could not vouch for.
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("den deleted a record it could not vouch for: %v", err)
	}
}

// First sense only: `--force` used to reclaim a dirty worktree on a sandbox
// whose record reads fine must say NOTHING about the second sense. A warning
// that fires on every forced removal stops being read.
func TestRmForceStaysSilentAboutTheSecondSense(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	writeEnvRecord(t, denHome, "api") // READABLE: --force serves the first sense alone
	f := &sbx.Fake{Responses: lsWith("api")}
	stdout, stderr, err := executeCmdWithSbxSeparateStreams(t, f, "--den-home", denHome, "rm", "--force", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout + stderr
	// "secrets scoped", not "scoped secrets": the message reads "the secrets
	// scoped to it are left in place" (rm.go), so a token that doesn't occur
	// verbatim in den's own output can never fail this assertion — it was
	// carried over from the brief without checking the exact wording; caught
	// by fix round 1, finding 3.
	for _, noise := range []string{"by name", "sbx secret", "secrets scoped"} {
		if strings.Contains(out, noise) {
			t.Errorf("den mentions the second sense of --force when it did not exercise it (%q):\n%s", noise, out)
		}
	}
}

// The real decision of this task, pinned: the order. A worktree that is
// CLEAN (not dirty — that refusal is TestRmDoesNotDestroyTheSandboxWhenAWorktreeIsDirty's
// subject) must survive an unreadable `.sbxenv.yaml` untouched. Validating the
// record happens BEFORE any worktree reclaim: the reverse would move the
// worktree to the trash and only then refuse, and a refusal that has already
// acted is not a refusal.
func TestRmRefusesBeforeReclaimingAnyWorktree(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, `agents:
  claude:
    config_dir: /profile/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
`)
	writeStack(t, denHome, "devx", "image: devx:v1\n")

	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	path, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"central", filepath.Join(denHome, "worktrees"), worktree.Name{Dir: "feat12", Branch: "feat12"}, repo)
	if err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	draft := filepath.Join(path, "draft.txt")
	if err := os.WriteFile(draft, []byte("clean, and committed"), 0o644); err != nil {
		t.Fatal(err)
	}
	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = path
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	commitCmd := exec.Command("git", "commit", "-m", "draft")
	commitCmd.Dir = path
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	envPath := writeEnvRecord(t, denHome, "api.feat12")
	// A NEWER den's file: good YAML, a schemaVersion this den does not emit.
	if err := os.WriteFile(envPath, []byte("schemaVersion: \"9\"\nagent: shell\nname: api.feat12\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	_, err = executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")

	if err == nil {
		t.Fatal("den destroyed a sandbox whose record it could not read")
	}
	if !strings.Contains(err.Error(), envPath) {
		t.Errorf("the refusal does not name the file: %v", err)
	}
	// THE property: the worktree is untouched. A refusal that had already
	// moved it to the trash would not be a refusal.
	if _, err := os.Stat(draft); err != nil {
		t.Errorf("a refusal that already reclaimed the worktree is not a refusal: %v", err)
	}
	if f.HasCalled("env", "rm") || f.HasCalled("rm", "--force") {
		t.Errorf("den destroyed after refusing; calls: %v", f.Calls)
	}
}

// The agent profile persists: that is the whole point of a config_dir mounted
// RW. A den rm that wiped it would force the user to /login again.
func TestRmNeverTouchesTheAgentProfile(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	writeEnvRecord(t, denHome, "api")
	profile := filepath.Join(denHome, "agents", "claude")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{Responses: lsWith("api")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(profile); err != nil {
		t.Errorf("the agent profile must survive rm: %v", err)
	}
}

func TestRmUnknownName(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	f := &sbx.Fake{Responses: lsWith("api", "web")}

	_, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "missing")
	if err == nil {
		t.Fatal("an unknown name must produce an error")
	}
	if !strings.Contains(err.Error(), "api") || !strings.Contains(err.Error(), "web") {
		t.Errorf("the message must list ALL live sandboxes; got: %v", err)
	}
	if f.HasCalled("rm") || f.HasCalled("env", "rm") {
		t.Errorf("no destruction must be attempted; calls: %v", f.Calls)
	}
}

// --keep-worktrees: the sandbox goes, the directories stay.
func TestRmKeepWorktreesLeavesDiskUntouched(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	// The repo is declared (basename "api", matching the worktree directory
	// created below): a nest with EMPTY repos would let an implementation that
	// calls cleanWorktrees even with --keep-worktrees slip through, since
	// there would then be nothing to iterate that could catch it.
	repo := filepath.Join(t.TempDir(), "api")
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	wt := filepath.Join(denHome, "worktrees", "feat12", "api")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome,
		"rm", "api.feat12", "--keep-worktrees"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("--keep-worktrees must preserve %s: %v", wt, err)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
}

// A sandbox with no worktree has nothing to clean up: cleanup must not invent
// a path.
func TestRmWithNoWorktreeCleansUpNothing(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")
	writeEnvRecord(t, denHome, "api")
	f := &sbx.Fake{Responses: lsWith("api")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "worktree") {
		t.Errorf("no worktree cleanup must be announced; got:\n%s", out)
	}
}

// A sandbox listed by `sbx ls` may have been created outside den, with a name
// sbx accepts but den would refuse as a path component: without validation,
// this name travels as-is to worktree.Path and sends Remove outside
// worktree_root — reproduced here exactly as measured in review: a real,
// declared nest "api" (LoadNest succeeds), so the guard is exercised at the
// end of the REAL resolution, not short-circuited by a missing nest that would
// fail for an unrelated reason anyway (best-effort — see
// TestRmUnreadableNestDoesNotPreventDestruction).
func TestRmRejectsANonCanonicalSandboxName(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	// SplitName cuts at the FIRST dot: nest "api" (valid, LoadNest succeeds),
	// worktree "../../escape" (invalid — a sandbox name component cannot start
	// with ".").
	foreignName := "api.../../escape"
	f := &sbx.Fake{Responses: lsWith(foreignName)}

	_, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", foreignName)
	if err == nil {
		t.Fatal("a non-canonical sandbox name must be refused")
	}
	if f.HasCalled("rm", "--force", foreignName) || f.HasCalled("env", "rm") {
		t.Errorf("no destruction must be attempted on a non-canonical name; calls: %v", f.Calls)
	}
	// No assertion on the escape path itself (worktree.Path(..., "../../escape",
	// repo)): in this minimal reproduction, no worktree really exists there
	// (nothing was ever created there through worktree.Ensure), so "the
	// directory does not exist" would stay true even WITHOUT the guard —
	// verified: without it, Remove just concludes "already gone" and returns
	// nil without moving anything, err and rm --force already show that above.
}

// Best-effort on RESOLUTION: a nest removed from ~/.den/nests since the spawn
// must not prevent destroying a genuinely live sandbox — and the warning must
// say where the abandoned worktree was left.
func TestRmUnreadableNestDoesNotPreventDestruction(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	// No writeNest("api", ...): nest "api" is absent from ~/.den/nests.
	//
	// A READABLE .sbxenv.yaml is written despite the broken nest: this test's
	// subject is the nest fallback, not the env-record refusal, and the two
	// must not be conflated — giving it no record would make den refuse for
	// the WRONG reason before ever reaching the nest resolution this test
	// means to exercise.
	envPath := writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "unreadable") {
		t.Errorf("the output must report the unreadable nest; got:\n%s", out)
	}
	// Default worktree_layout/worktree_root (minimalConfig declares neither):
	// central, under <denHome>/worktrees.
	expectedWhere := filepath.Join(denHome, "worktrees", "feat12")
	if !strings.Contains(out, expectedWhere) {
		t.Errorf("the output must say where the abandoned worktree was left (%s); got:\n%s",
			expectedWhere, out)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
}

// F1 — REGRESSION from task 17a, measured in review. Since LoadGlobal
// validates, a fault in a field UNRELATED to worktrees (agents.claude.update,
// ssh.mode, bin_dirs...) made cleanWorktrees fail BEFORE the `sbx rm --force`,
// and `den rm` could no longer destroy a genuinely live sandbox.
//
// This is the T13/T16 doctrine: a broken ~/.den must never block access to
// live VMs. And that is already what cleanWorktrees' godoc promises —
// "best-effort on RESOLUTION".
//
// A command validates what it USES: cleanWorktrees only reads worktree_layout
// and worktree_root.
func TestRmDestroysTheSandboxDespiteAnUnrelatedConfigFault(t *testing.T) {
	// Two faults, in two different families, NEITHER of which decides where
	// worktrees live. Both are rejected by LoadGlobal.
	const faultyConfigOutsideWorktrees = `agents:
  claude:
    config_dir: /profile/claude
    update: "   "
defaults:
  agent: claude
  stack: devx
ssh:
  mode: nfs
`
	denHome := t.TempDir()
	writeConfig(t, denHome, faultyConfigOutsideWorktrees)
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")
	envPath := writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("a config fault unrelated to worktrees must not prevent destroying a live sandbox: %v", err)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox must have been destroyed; calls: %v", f.Calls)
	}
}

// The counterpart, in the other direction: a fault on a field that
// cleanWorktrees USES must stay a HARD error. `centrl` is not caught by
// LoadGlobalUnvalidated — only an EMPTY worktree_layout gets the `central`
// default (config.go) — so without this refusal, den would compute a wrong
// worktree path and clean up somewhere else SILENTLY.
func TestRmRejectsAnUnknownWorktreeLayout(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig+"worktree_layout: centrl\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")
	// Readable, so the refusal this test checks for is the layout's, not the
	// env-record precheck's — the two run in that order and must not be
	// conflated.
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	_, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err == nil {
		t.Fatal("an unknown worktree_layout must fail den rm: we don't clean up without knowing where")
	}
	if !strings.Contains(err.Error(), "worktree_layout") {
		t.Errorf("error = %q, expected the faulty field named", err.Error())
	}
	if !strings.Contains(err.Error(), "centrl") {
		t.Errorf("error = %q, expected the faulty value named", err.Error())
	}
	// And nothing was destroyed, on EITHER route: we do not remove a sandbox
	// whose worktrees we cannot clean up.
	if f.HasCalled("rm") || f.HasCalled("env", "rm") {
		t.Errorf("no destruction must be attempted; calls: %v", f.Calls)
	}
}

// The "nest unreadable" warning goes on STDERR, never on stdout: a
// `den rm | grep` must see a clean success without the warning mixed in
// (I7 in review). executeCmdWithSbx deliberately merges both streams (see its
// comment) and therefore CANNOT check this separation — only
// executeCmdSeparateStreams, which gives two distinct buffers, can.
func TestRmUnreadableNestWritesToStderr(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	// No writeNest("api", ...): nest "api" is absent from ~/.den/nests.
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	deps := SystemDeps()
	deps.Sbx = f
	root := NewRootCmdWith(deps)

	stdout, stderr, err := executeCmdSeparateStreams(t, root, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "unreadable") {
		t.Errorf("the warning must go on stderr; got stderr:\n%s", stderr)
	}
	if strings.Contains(stdout, "unreadable") {
		t.Errorf("the warning must NOT appear on stdout; got stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "destroyed") {
		t.Errorf("the success message must appear on stdout; got stdout:\n%s", stdout)
	}
}

// The "worktrees first, sandbox second" order is a safety property: reversed,
// it would leave the user with no VM AND an error about a directory.
func TestRmDoesNotDestroyTheSandboxWhenAWorktreeIsDirty(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, `agents:
  claude:
    config_dir: /profile/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
`)
	writeStack(t, denHome, "devx", "image: devx:v1\n")

	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	// Readable: this test's subject is the dirty-worktree refusal, not the
	// env-record precheck, and the record check runs first.
	envPath := writeEnvRecord(t, denHome, "api.feat12")

	path, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"central", filepath.Join(denHome, "worktrees"), worktree.Name{Dir: "feat12", Branch: "feat12"}, repo)
	if err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "draft.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	_, err = executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")

	if err == nil {
		t.Fatal("a dirty worktree must fail the rm")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the message must name the offending worktree; got: %v", err)
	}
	// THE property: the sandbox is INTACT, and so is the worktree. Checked on
	// BOTH destruction routes: the primary one (env record present) and the
	// conceded fallback, since a bug could reach either.
	if f.HasCalled("rm", "--force", "api.feat12") || f.HasCalled("env", "rm") {
		t.Errorf("the sandbox must NOT have been destroyed; calls: %v", f.Calls)
	}
	if _, err := os.Stat(filepath.Join(path, "draft.txt")); err != nil {
		t.Errorf("the uncommitted work must be intact: %v", err)
	}

	// And with --force, everything goes: the worktree goes to trash, the
	// sandbox is destroyed, and the user learns where their work went — under
	// a name that carries the sandbox's FULL identity (nest AND worktree), not
	// just the nest (M12 in review: otherwise two worktrees of different
	// worktrees of the same nest would collide in the trash).
	//
	// The env record is still present and readable, so --force here overrides
	// only the DIRTY-WORKTREE refusal — the destruction itself still goes
	// through the normal `sbx env rm` route, not the conceded fallback.
	f2 := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f2, "--den-home", denHome,
		"rm", "api.feat12", "--force")
	if err != nil {
		t.Fatalf("with --force, the rm must succeed: %v", err)
	}
	if !f2.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("calls: %v", f2.Calls)
	}
	if !strings.Contains(out, filepath.Join(denHome, "trash")) {
		t.Errorf("the output must say where the worktree went (the trash); got:\n%s", out)
	}
	if !strings.Contains(out, "api.feat12-api") {
		t.Errorf("the trash entry must carry the full identity api.feat12, not just api; got:\n%s", out)
	}
	// F3: and nothing is left behind. `<worktree_root>/feat12` only existed to
	// carry this worktree; leaving it behind would turn worktree_root into a
	// list of empty directories as the user spawns and destroys.
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Errorf("%s should have disappeared with its last worktree (err = %v)", filepath.Dir(path), err)
	}
	// The root, though, stays: that is a user setting.
	if _, err := os.Stat(filepath.Join(denHome, "worktrees")); err != nil {
		t.Errorf("worktree_root must not be touched: %v", err)
	}
}

// worktree_layout: per-repo is a supported configuration (spec §13.5). A
// layout hardcoded in the code looks for the worktree in the wrong place,
// does not find it, lets Remove conclude "already gone", and ABANDONS the
// REAL worktree on disk without a word.
func TestRmRespectsThePerRepoLayout(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, `agents:
  claude:
    config_dir: /profile/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: per-repo
`)
	writeStack(t, denHome, "devx", "image: devx:v1\n")

	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	path, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"per-repo", "", worktree.Name{Dir: "feat12", Branch: "feat12"}, repo)
	if err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	expected := filepath.Join(repo, ".den", "feat12")
	if path != expected {
		t.Fatalf("worktree.Ensure returned %q, expected %q", path, expected)
	}

	envPath := writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the per-repo worktree must have been moved from %s; stat: %v", path, err)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("calls: %v", f.Calls)
	}
}

// The worktree's directory may have disappeared BEFORE `den rm` runs (a
// manual `rm -rf` by the user): Remove then returns an EMPTY trash path, and
// nothing must be announced — otherwise the command would print "worktree
// moved to trash: " followed by nothing, telling the user their work went
// nowhere.
func TestRmAnnouncesNothingWhenTheWorktreeHasAlreadyDisappeared(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, `agents:
  claude:
    config_dir: /profile/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
`)
	writeStack(t, denHome, "devx", "image: devx:v1\n")

	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	path, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"central", filepath.Join(denHome, "worktrees"), worktree.Name{Dir: "feat12", Branch: "feat12"}, repo)
	if err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}

	envPath := writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "moved to trash") {
		t.Errorf("no trash announcement must appear for a directory already gone; got:\n%s", out)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("calls: %v", f.Calls)
	}
}

// fakePruneFailingGit delegates everything to a real Git, except "worktree
// prune" which always fails: it isolates the one scenario where
// worktree.Remove returns a NON-EMPTY dest alongside an error (the move
// succeeded, but the registration could not be pruned).
type fakePruneFailingGit struct {
	real worktree.Git
}

func (g fakePruneFailingGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if len(args) == 2 && args[0] == "worktree" && args[1] == "prune" {
		return nil, fmt.Errorf("fake prune: simulated failure")
	}
	return g.real.Run(ctx, dir, args...)
}

func (g fakePruneFailingGit) RunWithInput(ctx context.Context, dir string, input []byte, args ...string) ([]byte, error) {
	if len(args) == 2 && args[0] == "worktree" && args[1] == "prune" {
		return nil, fmt.Errorf("fake prune: simulated failure")
	}
	return g.real.RunWithInput(ctx, dir, input, args...)
}

var _ worktree.Git = fakePruneFailingGit{}

// cleanWorktrees discards dest when Remove returns (dest, err) both non-empty
// (M11 in review) — this test checks that Remove's error still NAMES the
// trash, and that the sandbox is not destroyed despite everything.
func TestRmNamesTheTrashEvenWhenPruningFails(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, `agents:
  claude:
    config_dir: /profile/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
`)
	writeStack(t, denHome, "devx", "image: devx:v1\n")

	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	if _, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"central", filepath.Join(denHome, "worktrees"), worktree.Name{Dir: "feat12", Branch: "feat12"}, repo); err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	writeEnvRecord(t, denHome, "api.feat12")

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	deps := SystemDeps()
	deps.Sbx = f
	deps.Git = fakePruneFailingGit{real: worktree.NewGit()}
	root := NewRootCmdWith(deps)

	_, err := executeCmd(t, root, "--den-home", denHome, "rm", "api.feat12")
	if err == nil {
		t.Fatal("the pruning failure must surface as an error")
	}
	trash := filepath.Join(denHome, "trash")
	entries, readErr := os.ReadDir(trash)
	if readErr != nil || len(entries) == 0 {
		t.Fatalf("the worktree must have been moved to %s despite the pruning failure: %v (%v)",
			trash, readErr, entries)
	}
	if !strings.Contains(err.Error(), trash) {
		t.Errorf("the error must name the trash where the work landed; got: %v", err)
	}
	// Checked on both destruction routes: cleanWorktrees fails before either is
	// ever reached, so neither must have run.
	if f.HasCalled("rm", "--force", "api.feat12") || f.HasCalled("env", "rm") {
		t.Errorf("the sandbox must not be destroyed if cleanup fails; calls: %v", f.Calls)
	}
}

// A failure of `sbx env rm` (locked VM, sbx down...) must surface as-is, not
// be silently swallowed. Scripted on the PRIMARY route (env record present):
// without a record this would instead exercise the env-record refusal, which
// is a different failure with a different message — TestRmRefusesAnUnreadableEnvRecordAndNamesForce
// already covers that one.
func TestRmSbxFailureSurfaces(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	envPath := writeEnvRecord(t, denHome, "api")
	responses := lsWith("api")
	responses["env rm -f "+envPath] = sbx.Response{Err: fmt.Errorf("fake sbx env rm: simulated failure")}
	f := &sbx.Fake{Responses: responses}

	_, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api")
	if err == nil {
		t.Fatal("a failure of sbx env rm must surface")
	}
	// Fix round 1 (finding 3): `err == nil` alone would go green again on the
	// env-record REFUSAL if `writeEnvRecord` above were ever lost — that
	// refusal also returns a non-nil error, for an entirely different reason.
	// Pinned to the actual failure text AND to the call having been reached at
	// all, so the two causes cannot be confused with each other.
	if !strings.Contains(err.Error(), "simulated failure") {
		t.Errorf("the scripted sbx failure must surface verbatim, not some other error: %v", err)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the failure must come from the primary route actually being attempted; calls: %v",
			f.Calls)
	}
	// Post-probe leak fix (2026-08-25), third case: a destruction that did NOT
	// happen must not lose its record. den only deletes the .sbxenv.yaml after
	// `sbx env rm` returns success — here it returned an error instead, so the
	// file (den's only trace of what it would need to retry) must survive.
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("den deleted the record despite the destruction failing: %v", err)
	}
}

// worktree.Remove's git probes must be bounded: a repo on a dead network
// mount must not make `den rm` hang forever.
func TestRmBoundsGitProbesWithADeadline(t *testing.T) {
	original := gitProbeTimeout
	gitProbeTimeout = 5 * time.Second
	t.Cleanup(func() { gitProbeTimeout = original })

	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	writeEnvRecord(t, denHome, "api.feat12")

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	git := &fakeGit{}
	deps := SystemDeps()
	deps.Sbx = f
	deps.Git = git
	root := NewRootCmdWith(deps)

	_, err := executeCmd(t, root, "--den-home", denHome, "rm", "api.feat12")
	if err == nil {
		t.Fatal("the fake git refuses systematically: an error is expected")
	}
	if len(git.deadlines) == 0 {
		t.Fatal("the context passed to worktree.Remove carries no deadline: the probes are not bounded")
	}
	remaining := time.Until(git.deadlines[0])
	// Bounded from ABOVE and BELOW: a hardcoded delay totally disconnected from
	// gitProbeTimeout (a measured mutant: 400ms, under the documented floor of
	// 499ms) would only fail a "remaining <= gitProbeTimeout" check alone — it
	// must also be CLOSE to gitProbeTimeout.
	if remaining <= 0 || remaining > gitProbeTimeout || gitProbeTimeout-remaining > 500*time.Millisecond {
		t.Errorf("deadline out of bounds: %v remaining for a %v delay", remaining, gitProbeTimeout)
	}
}

// fakeGitTwoRepoDeadlines simulates, for SEVERAL repos, worktree.Remove's
// "already gone" outcome (rc=0, no registration) with no real disk access at
// all: it answers just enough for Remove to conclude "directory absent,
// nothing to do" for each repo (`worktree prune` then
// `worktree list --porcelain`, both empty). This isolates the context's
// DEADLINE BOUNDING from the rest of Remove's behavior.
//
// A simulated slowdown (sleepOnFirstCall) is inserted on the very first call:
// if the deadline is set ONCE for the whole loop, the second repo's remaining
// budget will already have been eaten by that slowdown; if it is set FOR EACH
// repo, the second repo starts from an almost-intact budget.
//
// Each repo now shows up in listDeadlines more than once — the preflight pass
// asks first (reclaimAll), then the move pass asks again through Remove — so
// the deadlines are read by INDEX, not counted.
type fakeGitTwoRepoDeadlines struct {
	sleepOnFirstCall time.Duration
	calls            int
	listDeadlines    []time.Time // one per repo, taken on "worktree list"
}

func (g *fakeGitTwoRepoDeadlines) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunWithInput(ctx, dir, nil, args...)
}

func (g *fakeGitTwoRepoDeadlines) RunWithInput(ctx context.Context, _ string, _ []byte, args ...string) ([]byte, error) {
	g.calls++
	if g.calls == 1 && g.sleepOnFirstCall > 0 {
		time.Sleep(g.sleepOnFirstCall)
	}
	if len(args) == 3 && args[0] == "worktree" && args[1] == "list" {
		if d, ok := ctx.Deadline(); ok {
			g.listDeadlines = append(g.listDeadlines, d)
		}
	}
	return nil, nil
}

var _ worktree.Git = (*fakeGitTwoRepoDeadlines)(nil)

func TestRmGivesAFreshDeadlineToEachRepo(t *testing.T) {
	original := gitProbeTimeout
	gitProbeTimeout = 1200 * time.Millisecond
	t.Cleanup(func() { gitProbeTimeout = original })

	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	alpha := filepath.Join(t.TempDir(), "alpha")
	beta := filepath.Join(t.TempDir(), "beta")
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+alpha+" }\n  - { path: "+beta+" }\n")
	writeEnvRecord(t, denHome, "api.feat12")

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	git := &fakeGitTwoRepoDeadlines{sleepOnFirstCall: 700 * time.Millisecond}
	deps := SystemDeps()
	deps.Sbx = f
	deps.Git = git
	root := NewRootCmdWith(deps)

	if _, err := executeCmd(t, root, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// One `worktree list` per repo in the PREFLIGHT pass, then more in the move
	// pass (reclaimAll judges the whole set before it moves anything, and
	// Remove re-asks for itself). The count is therefore not the property —
	// index 1 is, and it is the second repo's preflight: the call right after
	// the slowdown, hence the one a hoisted deadline would starve.
	if len(git.listDeadlines) < 2 {
		t.Fatalf("expected at least one deadline per repo (2 repos), got %d", len(git.listDeadlines))
	}
	secondRemaining := time.Until(git.listDeadlines[1])
	// A deadline HOISTED out of the loop would have already lost ~700ms by the
	// time the second repo runs; a deadline PER REPO starts from an
	// almost-intact budget.
	if secondRemaining < gitProbeTimeout-400*time.Millisecond {
		t.Errorf("the second repo inherits a spent budget (%v remaining out of %v): "+
			"the deadline is not set for each repo", secondRemaining, gitProbeTimeout)
	}
}

// A source reference names a sandbox that was never spawned under its
// prefixed name: ":" is not in sbx's charset, so spawn (spawn.go) named the
// live VM "corp-api", the FLATTENED reference. `den rm corp:api` must find
// and destroy THAT sandbox.
//
// No worktree fixture here on purpose: a colon reference names a sandbox
// spawned WITHOUT `-w` (flattening the whole argument would mangle a
// worktree suffix's own "." separator, so den does not offer that
// combination — see rm.go). cleanWorktrees is therefore exercised through
// its ordinary, unchanged path: `sbx.SplitName("corp-api")` reports no
// worktree, and it returns immediately.
func TestRmAcceptsASourceReference(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	envPath := writeEnvRecord(t, denHome, "corp-api")
	f := &sbx.Fake{Responses: lsWith("corp-api")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "corp:api"); err != nil {
		t.Fatalf("den rm corp:api: %v", err)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("expected the flattened sandbox corp-api to be destroyed; calls: %v", f.Calls)
	}
}

// README's second known limitation, closed: `den rm corp-api.feat12`
// destroyed the sandbox correctly but could not reverse-decode "corp-api"
// back into the source "corp", so the nest that declares the worktree's repos
// was never found and cleanup degraded to a warning — the worktree was left
// on disk. The decode now finds it, and the worktree really is moved to trash.
func TestRmCleansTheWorktreeOfAFlattenedSourceSandbox(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, `agents:
  claude:
    config_dir: /profile/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
`)
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeUnder(t, denHome, filepath.Join("sources", "corp", "stacks", "devx", "stack.yaml"),
		"image: devx:v1\n")
	writeUnder(t, denHome, filepath.Join("sources", "corp", "nests", "api.yaml"),
		"stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	if _, err := worktree.Ensure(context.Background(), worktree.NewGit(), "central",
		filepath.Join(denHome, "worktrees"), worktree.Name{Dir: "feat12", Branch: "feat12"}, repo); err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	envPath := writeEnvRecord(t, denHome, "corp-api.feat12")

	f := &sbx.Fake{Responses: lsWith("corp-api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "corp-api.feat12")
	if err != nil {
		t.Fatalf("den rm corp-api.feat12: %v", err)
	}
	if !strings.Contains(out, "moved to trash") {
		t.Errorf("the source nest's worktree must be cleaned up, not warned about; got:\n%s", out)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
}

// The prefixed spelling reaches the same worktree'd sandbox: flattening the
// whole argument would rewrite the "." and address "corp-api-feat12".
func TestRmAcceptsAWorktreedSourceReference(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	envPath := writeEnvRecord(t, denHome, "corp-api.feat12")
	f := &sbx.Fake{Responses: lsWith("corp-api.feat12")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome,
		"rm", "corp:api.feat12", "--keep-worktrees"); err != nil {
		t.Fatalf("den rm corp:api.feat12: %v", err)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("expected corp-api.feat12 to be destroyed; calls: %v", f.Calls)
	}
}

// A SOURCE nest declares its repos by `key:` — that is what makes it
// shareable — and LoadNest leaves Key entries with an EMPTY Path (only
// nest.Resolve fills it from the personal mapping). Now that the decode
// actually reaches such a nest, cleanup has to resolve the key the same way
// spawn did when it created the worktree.
func TestRmResolvesRepoKeysWhenCleaningWorktrees(t *testing.T) {
	denHome := t.TempDir()
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeConfig(t, denHome, `agents:
  claude:
    config_dir: /profile/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
repos:
  api: `+repo+`
`)
	writeUnder(t, denHome, filepath.Join("sources", "corp", "nests", "api.yaml"),
		"stack: devx\nrepos:\n  - { key: api }\n")

	if _, err := worktree.Ensure(context.Background(), worktree.NewGit(), "central",
		filepath.Join(denHome, "worktrees"), worktree.Name{Dir: "feat12", Branch: "feat12"}, repo); err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	writeEnvRecord(t, denHome, "corp-api.feat12")

	f := &sbx.Fake{Responses: lsWith("corp-api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "corp-api.feat12")
	if err != nil {
		t.Fatalf("den rm corp-api.feat12: %v", err)
	}
	if !strings.Contains(out, "moved to trash") {
		t.Errorf("a key-typed repo's worktree must be cleaned up; got:\n%s", out)
	}
}

// The same nest with the key NOT mapped. An unresolved key leaves Path empty,
// and worktree.Path("central", root, wt, "") joins to root/<wt> — the whole
// sandbox's worktree DIRECTORY rather than one repo's subdirectory. den must
// skip that repo and say so, never move a directory it cannot attribute.
func TestRmSkipsAnUnmappedRepoKeyRatherThanTrashingTheWholeWorktreeDir(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, `agents:
  claude:
    config_dir: /profile/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
`)
	writeUnder(t, denHome, filepath.Join("sources", "corp", "nests", "api.yaml"),
		"stack: devx\nrepos:\n  - { key: api }\n")

	dir := filepath.Join(denHome, "worktrees", "feat12", "api")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeEnvRecord(t, denHome, "corp-api.feat12")

	f := &sbx.Fake{Responses: lsWith("corp-api.feat12")}
	stdout, stderr, err := executeCmdWithSbxSeparateStreams(t, f,
		"--den-home", denHome, "rm", "corp-api.feat12")
	if err != nil {
		t.Fatalf("an unmapped key must not fail the removal: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(denHome, "worktrees", "feat12")); statErr != nil {
		t.Errorf("den moved the whole worktree directory for a repo it could not locate: %v", statErr)
	}
	if !strings.Contains(stderr, "api") {
		t.Errorf("the skipped repo must be named on stderr; got:\n%s", stderr)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
	_ = stdout
}

// The prefixed spelling WITH cleanup enabled — the path nothing exercised:
// TestRmAcceptsAWorktreedSourceReference passes --keep-worktrees (so
// cleanWorktrees never runs) and the two cleanup tests use the bare
// "corp-api.feat12". Here nestOfSandbox's explicit-reference branch decides
// which repos' directories get removed, so a wrong nest means removing the
// wrong ones.
func TestRmCleansTheWorktreeOfAPrefixedSourceReference(t *testing.T) {
	denHome := t.TempDir()
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeConfig(t, denHome, `agents:
  claude:
    config_dir: /profile/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
`)
	writeUnder(t, denHome, filepath.Join("sources", "corp", "nests", "api.yaml"),
		"stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	if _, err := worktree.Ensure(context.Background(), worktree.NewGit(), "central",
		filepath.Join(denHome, "worktrees"), worktree.Name{Dir: "feat12", Branch: "feat12"}, repo); err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	envPath := writeEnvRecord(t, denHome, "corp-api.feat12")

	f := &sbx.Fake{Responses: lsWith("corp-api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "corp:api.feat12")
	if err != nil {
		t.Fatalf("den rm corp:api.feat12: %v", err)
	}
	if !strings.Contains(out, "moved to trash") {
		t.Errorf("the source nest's worktree must be cleaned up; got:\n%s", out)
	}
	// The trash entry is named <timestamp>-<sandbox>-<repo>: asserting the
	// repo suffix is what proves the SOURCE nest's `repos:` were read, not
	// merely that something was moved.
	if !strings.Contains(out, "corp-api.feat12-api") {
		t.Errorf("the trash entry must name the source nest's declared repo; got:\n%s", out)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
}

// worktreeConfig is minimalConfig with the two worktree settings `den rm`
// validates. Written out because the manifest path must be assertable against
// a config.yaml that says something DIFFERENT from the record — that
// divergence is the whole point of the feature.
func worktreeConfig(root string) string {
	return minimalConfig + "worktree_layout: central\nworktree_root: " + root + "\n"
}

// createWorktree creates a REAL worktree of repo under root, the way a spawn
// would have. It takes the ROOT rather than the final path because that is
// what worktree.Ensure takes, and the final path is precisely what these tests
// must observe rather than assume.
func createWorktree(t *testing.T, repo, root, branch string) string {
	t.Helper()
	path, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"central", root, worktree.Name{Dir: branch, Branch: branch}, repo)
	if err != nil {
		t.Fatalf("preparing the worktree of %s: %v", repo, err)
	}
	return path
}

// writeManifest drops a creation record for a sandbox, the way spawn would
// have. Tests that need one build it here rather than running a spawn: the rm
// path must be assertable on a record whose configuration no longer exists.
func writeManifest(t *testing.T, denHome string, m manifest.Manifest) {
	t.Helper()
	if err := manifest.Write(denHome, m); err != nil {
		t.Fatal(err)
	}
}

// Proof 3 — the hole this feature exists to close. A repo mounted from the
// command line is declared in NO file: before the manifest, its worktree was
// never reclaimed and nothing said so.
func TestRmReclaimsACommandLineRepoWorktree(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")
	repo := filepath.Join(t.TempDir(), "hotfix")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, root, "feat12")

	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "hotfix", Origin: manifest.OriginCommandLine,
			Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the command-line worktree must be reclaimed: %v", err)
	}
	if _, err := manifest.Read(denHome, "api.feat12"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the manifest must be removed once everything it lists is reclaimed: %v", err)
	}
}

// Proof 8 — a repo mounted as-is is the user's own working directory. den does
// not dispose of it, and `worktree: false` is the bit that says so.
func TestRmNeverTouchesARepoMountedAsIs(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)

	writeManifest(t, denHome, manifest.Manifest{
		Sandbox: "api",
		Nest:    manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: repo, Worktree: false,
		}},
	})
	writeEnvRecord(t, denHome, "api")
	f := &sbx.Fake{Responses: lsWith("api")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Errorf("a repo mounted as-is must survive rm: %v", err)
	}
	if _, err := manifest.Read(denHome, "api"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the record of a destroyed sandbox must go with it: %v", err)
	}
}

// Proof 4 — the nest is gone, and it no longer matters: the record does not
// need it.
func TestRmCleansUpEvenWhenTheNestWasDeleted(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, root, "feat12")

	// No nests/api.yaml at all: the derivation this replaces would have given
	// up here with a warning, leaving the directory on disk.
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the worktree must be reclaimed without the nest: %v", err)
	}
	if strings.Contains(out, "unreadable") {
		t.Errorf("the nest is not consulted at all, so nothing about it must be reported; got:\n%s", out)
	}
}

// Proof 5 — worktree_root moved in config.yaml between the spawn and the rm.
// The recorded mount still names the real directory.
func TestRmReclaimsTheOriginalDirectoryAfterWorktreeRootMoved(t *testing.T) {
	denHome := t.TempDir()
	original := filepath.Join(t.TempDir(), "worktrees-then")
	writeConfig(t, denHome, worktreeConfig(filepath.Join(t.TempDir(), "worktrees-now")))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	wt := createWorktree(t, repo, original, "feat12")

	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: original},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the directory that really exists must be reclaimed, not the one today's "+
			"worktree_root implies: %v", err)
	}
}

// Proof 6 — a key unmapped since the spawn. rm.go used to abandon the
// directory with a warning; the record carries the path itself.
func TestRmReclaimsAWorktreeWhoseKeyIsNoLongerMapped(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	// `repos:` is EMPTY: the mapping that resolved this key at spawn time is
	// gone from this machine.
	writeConfig(t, denHome, worktreeConfig(root)+"repos: {}\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { key: api }\n")
	wt := createWorktree(t, repo, root, "feat12")

	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginKey, Key: "api",
			Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the record carries the path, so the key mapping is not needed: %v", err)
	}
	if strings.Contains(out, "not mapped") {
		t.Errorf("nothing must be abandoned over an unmapped key; got:\n%s", out)
	}
}

// Proof 7 — --without at spawn. The record holds the repos this spawn really
// mounted, so a repo it excluded is not reclaimed by association.
func TestRmDoesNotReclaimARepoTheSpawnExcluded(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	api := filepath.Join(t.TempDir(), "api")
	web := filepath.Join(t.TempDir(), "web")
	createTestRepo(t, api)
	createTestRepo(t, web)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+api+" }\n  - { path: "+web+" }\n")
	mounted := createWorktree(t, api, root, "feat12")
	// A worktree of the EXCLUDED repo, under the same root: it belongs to
	// another spawn, and the nest alone cannot tell the two apart.
	excluded := createWorktree(t, web, root, "feat12")

	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: api, Mount: mounted, Worktree: true,
		}},
	})
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(mounted); !os.IsNotExist(err) {
		t.Errorf("the recorded worktree must be reclaimed: %v", err)
	}
	if _, err := os.Stat(excluded); err != nil {
		t.Errorf("a repo this spawn never mounted must be left alone: %v", err)
	}
}

// --keep-worktrees keeps the RECORD too: the directories survive, and doctor
// must still be able to find them.
func TestRmKeepWorktreesKeepsTheManifest(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, root, "feat12")

	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome,
		"rm", "api.feat12", "--keep-worktrees")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("--keep-worktrees must preserve %s: %v", wt, err)
	}
	if _, err := manifest.Read(denHome, "api.feat12"); err != nil {
		t.Errorf("the record must survive with the directories it names: %v", err)
	}
	path, err := manifest.Path(denHome, "api.feat12")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, path) {
		t.Errorf("the kept record must be named, so the user can act on it; got:\n%s", out)
	}
}

// Proof 9 — a sandbox from before this feature. The old derivation still
// runs, and the user is told the answer is only as good as an unchanged
// configuration.
func TestRmFallsBackAndSaysSoWithoutAManifest(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	wt := createWorktree(t, repo, root, "feat12")
	// No manifest.Manifest: that absence is what sends this `den rm` down the
	// LEGACY derivation. The .sbxenv.yaml is a separate record and does not
	// touch that — this test's subject is unaffected by writing one.
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the legacy derivation must still reclaim the worktree: %v", err)
	}
	if !strings.Contains(out, "no creation record") {
		t.Errorf("the fallback must be announced; got: %s", out)
	}
}

// Proof 10 — a corrupt record must never block. The file is named, the
// derivation takes over, and above all the VM is destroyed: a `den rm` that
// refuses leaves a live sandbox nobody can remove (doctrine T13/T16).
func TestRmWarnsAndStillDestroysOnACorruptManifest(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	wt := createWorktree(t, repo, root, "feat12")

	path, err := manifest.Path(denHome, "api.feat12")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A corrupt MANIFEST, not a corrupt .sbxenv.yaml — the two records are
	// separate files, and this test's subject is the manifest fallback.
	envPath := writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("a corrupt record must never fail the rm: %v", err)
	}
	if !strings.Contains(out, path) {
		t.Errorf("the unreadable file must be named; got:\n%s", out)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the derivation must take over and reclaim the worktree: %v", err)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
	// den read nothing of that file, so it cannot know it is worthless: a
	// record written by a NEWER den lands in this same branch, and deleting it
	// would destroy that den's only trace of a live sandbox. The message
	// carries the remedy instead.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("den must leave a file it could not read alone: %v", err)
	}
	if !strings.Contains(out, "by hand") {
		t.Errorf("the message must say what to do with the file it leaves behind; got:\n%s", out)
	}
}

// Finding 1 — `--as` (PR #68) lets two sandboxes share one worktree: spawning
// `api -w feature/123` then `api -w feature/123 --as reco` reuses the
// directory the first spawn created (worktree.Ensure is idempotent on the
// same branch), so BOTH records end up naming it. `den rm api.reco` must not
// move that directory to the trash while `api.feature-123` is still running
// and mounting it — only the record of the sandbox actually being removed
// goes.
func TestRmLeavesAMountAnotherRecordStillNames(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, root, "feat12")

	// The live sibling still holding the mount.
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feature-123",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feature/123", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	// The `--as` sandbox being removed, recorded with the SAME mount.
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.reco",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feature/123", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	// Only for the sandbox being REMOVED: the sibling's own .sbxenv.yaml plays
	// no role in this test.
	envPath := writeEnvRecord(t, denHome, "api.reco")
	f := &sbx.Fake{Responses: lsWith("api.feature-123", "api.reco")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.reco")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("api.feature-123 still mounts this worktree; it must survive: %v", err)
	}
	if _, err := manifest.Read(denHome, "api.reco"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the record of the removed sandbox must still go: den is removing THIS " +
			"sandbox, not disowning the directory the sibling holds")
	}
	if _, err := manifest.Read(denHome, "api.feature-123"); err != nil {
		t.Errorf("the sibling's own record must be untouched: %v", err)
	}
	if !strings.Contains(out, "api.feature-123") {
		t.Errorf("the message must name the sandbox that still holds the worktree; got:\n%s", out)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox itself must still be destroyed; calls: %v", f.Calls)
	}
}

// Regression floor for Finding 1's fix: a single record with no sibling
// naming its Mount must reclaim exactly as it did before the guard existed.
func TestRmSingleRecordStillReclaimsItsWorktree(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, root, "feat12")

	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("with no sibling record, the worktree must still be reclaimed: %v", err)
	}
	if _, err := manifest.Read(denHome, "api.feat12"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the record must still be removed: %v", err)
	}
	if strings.Contains(out, "worktree kept") {
		t.Errorf("no sibling names this mount, so the guard must not fire; got:\n%s", out)
	}
}

// writeRawManifest drops a file in state/sandboxes/ that manifest.Write could
// never produce. That is the point: these tests are about the records den
// REFUSES to decode, and every one of them would be unreachable through the
// typed writer.
func writeRawManifest(t *testing.T, denHome, sandbox, content string) string {
	t.Helper()
	if err := os.MkdirAll(manifest.Dir(denHome), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(manifest.Dir(denHome), sandbox+".yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The hole the guard above left open: it read manifest.List's DECODABLE half
// only. A sibling written by a NEWER den is refused on its `schema` alone —
// otherwise perfectly good YAML, naming the very mount this `den rm` is about
// to trash while that sandbox is live. The lax mount scan makes it visible.
func TestRmLeavesAMountAnUndecodableRecordStillNames(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, root, "feat12")

	// The live sibling, recorded by a den this one does not understand.
	writeRawManifest(t, denHome, "api.feature-123",
		"schema: 9999\nsandbox: api.feature-123\ninvented_by_a_newer_den: yes\n"+
			"repos:\n  - name: api\n    mount: "+wt+"\n")
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.reco",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feature/123", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	envPath := writeEnvRecord(t, denHome, "api.reco")
	f := &sbx.Fake{Responses: lsWith("api.feature-123", "api.reco")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.reco")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("api.feature-123 still mounts this worktree; it must survive: %v", err)
	}
	if !strings.Contains(out, "worktree kept") || !strings.Contains(out, "api.feature-123") {
		t.Errorf("the message must name the sandbox that still holds the worktree; got:\n%s", out)
	}
	if _, err := manifest.Read(denHome, "api.reco"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the record of the removed sandbox must still go: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manifest.Dir(denHome), "api.feature-123.yaml")); err != nil {
		t.Errorf("den never deletes a record it could not read: %v", err)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox itself must still be destroyed; calls: %v", f.Calls)
	}
}

// A record that will not parse AT ALL names no mount anyone can enumerate, so
// it is an unknown sharer: den reclaims nothing for this run rather than
// guessing, names the file it could not read and the directories it left, and
// still destroys the sandbox it was asked to destroy (doctrine T13/T16).
//
// Its own record SURVIVES, like after any other skipped reclaim: it is the
// only trace of the directories still on disk, and it is what `den doctor
// --fix` needs to finish the job once the unreadable file is gone.
func TestRmReclaimsNothingWhenARecordCannotBeParsedAtAll(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, root, "feat12")

	badPath := writeRawManifest(t, denHome, "api.feature-123", "repos: [ {mount: "+wt+"\n")
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.reco",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feature/123", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	envPath := writeEnvRecord(t, denHome, "api.reco")
	f := &sbx.Fake{Responses: lsWith("api.feature-123", "api.reco")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.reco")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("an unknown sharer may be mounting it; the worktree must survive: %v", err)
	}
	if !strings.Contains(out, badPath) {
		t.Errorf("the message must name the file den could not read; got:\n%s", out)
	}
	if !strings.Contains(out, "worktree kept: "+wt) {
		t.Errorf("the kept directory must be named, not counted; got:\n%s", out)
	}
	if _, err := os.Stat(badPath); err != nil {
		t.Errorf("den never deletes a record it could not read: %v", err)
	}
	if _, err := manifest.Read(denHome, "api.reco"); err != nil {
		t.Errorf("nothing was reclaimed, so the record must survive as the only trace of "+
			"those directories — and as what `den doctor --fix` acts on: %v", err)
	}
	if !strings.Contains(out, "the record of api.reco is kept too") {
		t.Errorf("a surviving record after a `den rm` is surprising enough to be said, with "+
			"what will act on it; got:\n%s", out)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox itself must still be destroyed; calls: %v", f.Calls)
	}
}

// A mount a READABLE record still names is reported as held, even while an
// unreadable file elsewhere holds everything else back. The two messages say
// opposite things about who to ask before touching a directory, and "reclaim
// them by hand" aimed at a live sibling's workspace is exactly the deletion
// this whole guard exists to prevent.
func TestRmNeverTellsTheUserToReclaimAMountAnotherRecordNames(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, root, "feat12")

	// The junk file is about a THIRD sandbox: it holds nothing back that the
	// readable sibling does not already account for.
	badPath := writeRawManifest(t, denHome, "web.feat9", "repos: [ {mount: /elsewhere\n")
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feature-123",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feature/123", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.reco",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feature/123", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	writeEnvRecord(t, denHome, "api.reco")
	f := &sbx.Fake{Responses: lsWith("api.feature-123", "api.reco", "web.feat9")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.reco")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("api.feature-123 still mounts this worktree; it must survive: %v", err)
	}
	if !strings.Contains(out, "worktree kept: "+wt+" is also mounted by sandbox api.feature-123") {
		t.Errorf("a mount a readable record names must be reported as held; got:\n%s", out)
	}
	if strings.Contains(out, "an unreadable record may name it") {
		t.Errorf("this directory has a known holder, so it must be reported as held, not as "+
			"a directory nobody can account for; got:\n%s", out)
	}
	if _, err := manifest.Read(denHome, "api.reco"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("nothing was held back, so the record of the removed sandbox still goes: %v", err)
	}
	if strings.Contains(out, badPath) {
		t.Errorf("the unreadable file held nothing back here, so naming it would report a "+
			"problem that had no effect; got:\n%s", out)
	}
}

// Regression floor for the lax scan: it must not turn every broken file into a
// reason to keep everything. A record den cannot decode that names ANOTHER
// directory holds nothing here, and reclaim proceeds exactly as it did before
// this scan existed — the floor for "no broken record at all" is held by
// TestRmSingleRecordStillReclaimsItsWorktree.
func TestRmStillReclaimsDespiteABrokenRecordNamingAnotherMount(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, root, "feat12")

	writeRawManifest(t, denHome, "web.feat9",
		"schema: 9999\nsandbox: web.feat9\nrepos:\n  - name: web\n    mount: /elsewhere/feat9\n")
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12", "web.feat9")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("no record names this mount, so it must still be reclaimed: %v", err)
	}
	if strings.Contains(out, "worktree kept") || strings.Contains(out, "no worktree reclaimed") {
		t.Errorf("a broken record naming another directory holds nothing here; got:\n%s", out)
	}
	if _, err := manifest.Read(denHome, "api.feat12"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the record must still be removed: %v", err)
	}
}

// legacyNest lays out what the DERIVATION needs to find a worktree on its own:
// a config, a stack, a nest declaring the repo, and a real worktree where the
// layout says it goes. The sandbox it describes deliberately gets NO creation
// record — that absence is what sends `den rm` down cleanWorktreesLegacy.
//
// "record" here, and in every `…WithoutARecord…` test name below, means the
// MANIFEST (internal/manifest.Manifest) alone: since this task, the same
// sandbox directory can ALSO hold a `.sbxenv.yaml` (written separately, by
// `writeEnvRecord` where a test needs `den rm` to reach past the env-record
// precheck) without affecting which cleanup branch runs — that choice is
// still driven purely by the manifest's absence.
func legacyNest(t *testing.T, denHome string) (repo, root, wt string) {
	t.Helper()
	root = filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo = filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	return repo, root, createWorktree(t, repo, root, "feat12")
}

// registeredWorktrees returns the worktree directories git still knows about
// for this repo. It reads the registrations rather than the disk: a reclaim
// that skipped worktree.Remove leaves a stale one behind, and nothing on disk
// says so.
func registeredWorktrees(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v\n%s", err, out)
	}
	return string(out)
}

// Finding 1 again, one branch over. With no record for the sandbox being
// removed, `den rm` DERIVES the worktree — and moved it to the trash without
// asking whether anyone else still mounts it. `--as` is what made that
// reachable: `den spawn api -w feature/123` then `den spawn api -w feature/123
// --as reco` share one directory, and deleting the record of either one is
// enough to land here.
func TestRmWithoutARecordLeavesAMountAnotherRecordStillNames(t *testing.T) {
	denHome := t.TempDir()
	repo, root, wt := legacyNest(t, denHome)

	// The live sibling, with a record of its own. The sandbox being removed
	// has none.
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.reco",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	// The sandbox being removed has no manifest.Manifest (that is what sends it
	// down the legacy path), but it DOES need a .sbxenv.yaml — a separate file
	// — for den to hand `sbx env rm` a record it can vouch for.
	envPath := writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12", "api.reco")}

	stdout, stderr, err := executeCmdWithSbxSeparateStreams(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("api.reco still mounts this worktree; it must survive: %v", err)
	}
	if !strings.Contains(stdout, "worktree kept: "+wt+" is also mounted by sandbox api.reco") {
		t.Errorf("the message must name the sandbox that still holds the worktree; got:\n%s", stdout)
	}
	// The same event on both branches lands on the same stream: cleanFromManifest
	// prints this on stdout, and a script must not have to read stderr too
	// depending on which branch reclaimed.
	if strings.Contains(stderr, "worktree kept") {
		t.Errorf("a hold-back is an outcome, announced on stdout like on the record path; got:\n%s", stderr)
	}
	// Reserved for the directories NOBODY can account for. This one has a named
	// holder, and telling the user to dispose of a live sibling's workspace is
	// the deletion this whole guard exists to prevent.
	if strings.Contains(stdout, "remove them by hand") {
		t.Errorf("a mount with a known holder must not be offered for removal; got:\n%s", stdout)
	}
	if _, err := manifest.Read(denHome, "api.reco"); err != nil {
		t.Errorf("the sibling's own record must be untouched: %v", err)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox itself must still be destroyed; calls: %v", f.Calls)
	}
}

// Regression floor: with no other record naming the derived directory, the
// legacy path reclaims exactly as it did before the guard existed.
func TestRmWithoutARecordStillReclaimsItsWorktree(t *testing.T) {
	denHome := t.TempDir()
	_, _, wt := legacyNest(t, denHome)
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("no record names this mount, so it must still be reclaimed: %v", err)
	}
	if !strings.Contains(out, "worktree moved to trash") {
		t.Errorf("the trash entry must still be announced; got:\n%s", out)
	}
	if strings.Contains(out, "worktree kept") {
		t.Errorf("nothing names this mount, so the guard must not fire; got:\n%s", out)
	}
}

// This sandbox's OWN record, undecodable — the other way into the legacy path.
// The guard reads the broken records for their mounts, and this file sits among
// them under this very sandbox's name: counted, it would hold back the reclaim
// of the sandbox it belongs to, forever, on every such `den rm`.
func TestRmReclaimsDespiteItsOwnUnreadableRecord(t *testing.T) {
	denHome := t.TempDir()
	_, _, wt := legacyNest(t, denHome)
	badPath := writeRawManifest(t, denHome, "api.feat12", "repos: [ {mount: "+wt+"\n")
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("a sandbox's own record never holds back its own reclaim: %v", err)
	}
	if strings.Contains(out, "worktree kept") {
		t.Errorf("the only unreadable record is this sandbox's own; got:\n%s", out)
	}
	if _, err := os.Stat(badPath); err != nil {
		t.Errorf("den never deletes a record it could not read: %v", err)
	}
}

// A record den cannot enumerate may name anything, including the directory the
// derivation just aimed at. den leaves it alone, names the file it could not
// read, and still destroys the sandbox (doctrine T13/T16). It also says what
// the record path can promise and this one cannot: nothing will offer these
// directories again, because there is no record for `den doctor` to replay.
func TestRmWithoutARecordKeepsWhatAnUnreadableRecordMayName(t *testing.T) {
	denHome := t.TempDir()
	_, _, wt := legacyNest(t, denHome)
	badPath := writeRawManifest(t, denHome, "web.feat9", "repos: [ {mount: /elsewhere\n")
	envPath := writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12", "web.feat9")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("an unknown sharer may be mounting it; the worktree must survive: %v", err)
	}
	if !strings.Contains(out, "worktree kept: "+wt+" — an unreadable record may name it") {
		t.Errorf("the kept directory must be named, not counted; got:\n%s", out)
	}
	if !strings.Contains(out, badPath) {
		t.Errorf("the message must name the file den could not read; got:\n%s", out)
	}
	if !strings.Contains(out, "den has no record it can replay for api.feat12") {
		t.Errorf("`den doctor` replays records and there is none here: the user must be told "+
			"nothing will reclaim these directories for them; got:\n%s", out)
	}
	if _, err := os.Stat(badPath); err != nil {
		t.Errorf("den never deletes a record it could not read: %v", err)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox itself must still be destroyed; calls: %v", f.Calls)
	}
}

// The tolerance this path is built on: the derived directory does not exist,
// and the guard must not start charging for that miss. Nothing is announced as
// kept — there is nothing to keep — and worktree.Remove still runs, which is
// what prunes the registration a vanished directory leaves behind.
func TestRmWithoutARecordSaysNothingOfADerivedPathThatIsNotThere(t *testing.T) {
	denHome := t.TempDir()
	repo, _, wt := legacyNest(t, denHome)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	badPath := writeRawManifest(t, denHome, "web.feat9", "repos: [ {mount: /elsewhere\n")
	envPath := writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12", "web.feat9")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "worktree kept") || strings.Contains(out, badPath) {
		t.Errorf("a directory that is not on disk holds nobody's workspace, and naming it "+
			"would point the user at a path they cannot go look at; got:\n%s", out)
	}
	if reg := registeredWorktrees(t, repo); strings.Contains(reg, wt) {
		t.Errorf("worktree.Remove must still run and prune the stale registration; got:\n%s", reg)
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox itself must still be destroyed; calls: %v", f.Calls)
	}
}

// A file den never wrote, named so that the trim recovers no sandbox name at
// all: manifest.List admits any entry ending in ".yaml", ".yaml" included.
// Stored as a nameless holder it protected nothing AND masked the next
// record's real claim on the same mount. Counted as an unknown sharer, it
// holds the directory back like any other file den cannot account for.
func TestRmTreatsANamelessRecordAsAnUnknownSharer(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, root, "feat12")

	badPath := writeRawManifest(t, denHome, "", "schema: 9999\nrepos:\n  - name: api\n    mount: "+wt+"\n")
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("a record den cannot attribute to a sandbox must hold its mount back: %v", err)
	}
	if !strings.Contains(out, badPath) {
		t.Errorf("the message must name the file den could not account for; got:\n%s", out)
	}
	if !strings.Contains(out, "worktree kept: "+wt+" — an unreadable record may name it") {
		t.Errorf("the kept directory must be named; got:\n%s", out)
	}
	if _, err := os.Stat(badPath); err != nil {
		t.Errorf("den never deletes a record it could not read: %v", err)
	}
}

// The other half of the same finding, on the DECODABLE side: a record den can
// read but whose `sandbox:` names nobody. Stored, its nameless claim came first
// (records are sorted, "" sorts before every name) and silenced the live
// sibling's real claim on the very same directory — a mount with a known holder
// went to the trash because a hand-edited file got there first.
func TestRmANamelessRecordNeverMasksARealHolder(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	wt := createWorktree(t, repo, root, "feat12")

	// Decodable — schema 1, every field known — and yet it attributes the mount
	// to no sandbox. Only a hand-edited file says that: manifest.Path refuses
	// an empty sandbox name, so den never wrote one.
	writeRawManifest(t, denHome, "nameless", "schema: 1\nrepos:\n  - name: api\n    mount: "+wt+"\n")
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.reco",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{{
			Name: "api", Origin: manifest.OriginPath, Repo: repo, Mount: wt, Worktree: true,
		}},
	})
	writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12", "api.reco")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("api.reco still mounts this worktree; it must survive: %v", err)
	}
	if !strings.Contains(out, "worktree kept: "+wt+" is also mounted by sandbox api.reco") {
		t.Errorf("the holder that HAS a name must be the one announced; got:\n%s", out)
	}
}

// The two ways into the legacy path, together: this sandbox's own record is
// undecodable AND a third party's is unreadable. It is the only run where den
// says both things at once — "den leaves your file alone" on the way in, and
// "nothing will reclaim these worktrees" on the way out — and the two must read
// as one story. den keeps the file, reports it, and still cannot replay it: it
// is a record, not a map of these directories.
func TestRmWithoutAReadableRecordKeepsWhatAThirdPartyMayName(t *testing.T) {
	denHome := t.TempDir()
	_, _, wt := legacyNest(t, denHome)
	ownPath := writeRawManifest(t, denHome, "api.feat12", "schema: 9999\nsandbox: api.feat12\n")
	badPath := writeRawManifest(t, denHome, "web.feat9", "repos: [ {mount: /elsewhere\n")
	envPath := writeEnvRecord(t, denHome, "api.feat12")
	f := &sbx.Fake{Responses: lsWith("api.feat12", "web.feat9")}

	out, warn, err := executeCmdWithSbxSeparateStreams(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("an unknown sharer may be mounting it; the worktree must survive: %v", err)
	}
	// The own record is announced on the way IN, as the reason den fell back on
	// the derivation — and never again as a sharer: counted among them it would
	// name this sandbox's own worktrees as somebody else's, on every run of
	// this branch.
	if !strings.Contains(warn, ownPath) {
		t.Errorf("the record den could not decode is why this run derives anything; got:\n%s", warn)
	}
	if strings.Contains(out, "creation record "+ownPath) {
		t.Errorf("a sandbox's own record never speaks for a third party; got:\n%s", out)
	}
	if !strings.Contains(out, "creation record "+badPath) {
		t.Errorf("the third party's file is the one den could not account for; got:\n%s", out)
	}
	if !strings.Contains(out, "den has no record it can replay for api.feat12") {
		t.Errorf("a file survives under this name, and den still cannot reclaim from it: the "+
			"claim must be about replaying, not about having; got:\n%s", out)
	}
	for _, p := range []string{ownPath, badPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("den never deletes a record it could not read: %v", err)
		}
	}
	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("the sandbox itself must still be destroyed; calls: %v", f.Calls)
	}
}

// blockRecordsDir puts a FILE where the records directory belongs, so
// os.ReadDir answers ENOTDIR and manifest.List enumerates nothing at all.
//
// A file rather than a mode-000 directory: no test here may depend on a
// permission bit, since a suite running as root would sail straight through
// one, and this failure needs no privilege to stage.
func blockRecordsDir(t *testing.T, denHome string) string {
	t.Helper()
	dir := manifest.Dir(denHome)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The guard's OWN failure mode, at the source: not one record is enumerated,
// readable or broken. Swallowed, that error made holderOf answer "nobody holds
// this" for every mount, and `den rm` reclaimed everything on the very run
// where den knew the least. An unenumerable directory says strictly less than
// one unparseable record, and a single one of those already holds every
// directory back — so this earns the same verdict.
func TestMountGuardHoldsBackWhenTheRecordsDirectoryCannotBeEnumerated(t *testing.T) {
	denHome := t.TempDir()
	blockRecordsDir(t, denHome)

	holder, unknown := newMountGuard(denHome, "api.feat12").holderOf("/anywhere")
	if holder != "" {
		t.Errorf("no record was read, so no sandbox can be named a holder; got %q", holder)
	}
	if !unknown {
		t.Error("a directory den cannot enumerate may hold any mount: every one is unknown")
	}
}

// The same failure through the whole command. den keeps the worktree, names the
// directory it could not enumerate, and still destroys the sandbox (doctrine
// T13/T16). There is no record to keep here — the same ENOTDIR is why den fell
// back on the derivation — but what IS on disk under state/ survives untouched:
// den never deletes what it could not read.
// blockRecordsDir replaces state/sandboxes (denHome) with a FILE — the very
// directory a `.sbxenv.yaml` would need to live under. No record can be
// written there at all, so this run cannot reach the primary route; --force
// is what a sandbox with no vouchable record has always needed (§5.8), and it
// is what lets this test reach the legacy enumeration failure it means to
// exercise rather than stopping earlier on the env-record refusal.
func TestRmForceKeepsEveryWorktreeWhenTheRecordsDirectoryCannotBeEnumerated(t *testing.T) {
	denHome := t.TempDir()
	_, _, wt := legacyNest(t, denHome)
	dir := blockRecordsDir(t, denHome)
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, _, err := executeCmdWithSbxSeparateStreams(t, f, "--den-home", denHome, "rm", "api.feat12", "--force")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("den enumerated no record at all, so a live sibling may mount this "+
			"worktree; it must survive: %v", err)
	}
	if !strings.Contains(out, "creation records unreadable: ") || !strings.Contains(out, dir) {
		t.Errorf("the directory den could not enumerate must be named; got:\n%s", out)
	}
	if strings.Contains(out, "delete it by hand") {
		t.Errorf("state/ is never purged: den must not send the user to delete the directory "+
			"holding every record it has; got:\n%s", out)
	}
	if !strings.Contains(out, "worktree kept: "+wt+" — an unreadable record may name it") {
		t.Errorf("the kept directory must be named, not counted; got:\n%s", out)
	}
	// The tail the legacy path prints when it really found no record would be a
	// lie here: den never looked inside the directory, so a good record for this
	// sandbox may be in it, and `den doctor --fix` would reclaim these very
	// worktrees once den can read it. That is also what the guard promises when
	// it holds them back — the two must not contradict each other.
	//
	// This asserts the mode-000 chain too: a permission bit and a FILE reach the
	// same ENOTDIR/EACCES verdict from manifest.List, hence the same branch, and
	// only the file needs no privilege to stage.
	if strings.Contains(out, "remove them by hand") {
		t.Errorf("a record den has not looked at may still name these worktrees; got:\n%s", out)
	}
	if !strings.Contains(out, "den cannot tell whether it has a record for api.feat12") {
		t.Errorf("den must not claim an absence it did not verify; got:\n%s", out)
	}
	if !strings.Contains(out, "`den doctor --fix` reclaims the worktrees a record still names") {
		t.Errorf("the remedy must survive: these directories are reclaimable once den can read "+
			"the records; got:\n%s", out)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("den never deletes what it could not read: %v", err)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("the sandbox itself must still be destroyed; calls: %v", f.Calls)
	}
}

// sourceNestFixture installs a MANIFESTED source whose nest declares its repo
// by key, maps that key in the source's own personal configuration, and
// creates the worktree a spawn would have. The sandbox gets NO creation
// record: that is what sends `den rm` down cleanWorktreesLegacy, the branch
// that has to resolve the key itself.
func sourceNestFixture(t *testing.T, denHome string, mapKey bool) (repo, wt string) {
	t.Helper()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	repo = filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)

	src := filepath.Join("sources", "corp")
	writeUnder(t, denHome, filepath.Join(src, "den-source.yaml"), `schema_version: 1
kind: source
metadata: { name: corp, version: 1.0.0 }
exports:
  nests:
    - { name: api, path: nests/api.yaml }
  stacks:
    - { name: devx, path: stacks/devx/stack.yaml }
`)
	writeUnder(t, denHome, filepath.Join(src, "stacks", "devx", "stack.yaml"), "image: devx:v1\n")
	writeUnder(t, denHome, filepath.Join(src, "nests", "api.yaml"),
		"stack: devx\nrepos:\n  - { key: api, url: https://git.example.test/team/api.git }\n")
	if mapKey {
		writeUnder(t, denHome, filepath.Join("source-config", "corp.yaml"),
			"schema_version: 1\nversion: 1.0.0\nrepos:\n  api: "+repo+"\n")
	}
	return repo, createWorktree(t, repo, root, "feat12")
}

// A sandbox spawned from a manifested source resolves its repo keys through
// THAT source's personal configuration. Without a creation record, `den rm`
// has to do the same lookup — resolving it in config.yaml would find nothing
// here and abandon the worktree on disk.
func TestRmWithoutARecordResolvesKeysThroughTheSourceMapping(t *testing.T) {
	denHome := t.TempDir()
	_, wt := sourceNestFixture(t, denHome, true)
	writeEnvRecord(t, denHome, "corp-api.feat12")
	f := &sbx.Fake{Responses: lsWith("corp-api.feat12")}

	stdout, stderr, err := executeCmdWithSbxSeparateStreams(t, f,
		"--den-home", denHome, "rm", "corp-api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s\n%s", err, stdout, stderr)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the worktree was not reclaimed (%v): the key resolved in the wrong scope\n%s",
			err, stderr)
	}
}

// And when the source maps nothing, the remedy names the file that would fix
// it — the source's own configuration, never config.yaml, which den would
// refuse to read for a manifested source anyway.
func TestRmWithoutARecordNamesTheSourceConfigurationOfAnUnmappedKey(t *testing.T) {
	denHome := t.TempDir()
	_, wt := sourceNestFixture(t, denHome, false)
	writeEnvRecord(t, denHome, "corp-api.feat12")
	f := &sbx.Fake{Responses: lsWith("corp-api.feat12")}

	_, stderr, err := executeCmdWithSbxSeparateStreams(t, f,
		"--den-home", denHome, "rm", "corp-api.feat12")
	if err != nil {
		t.Fatalf("an unmapped key must not refuse the removal: %v\n%s", err, stderr)
	}
	want := filepath.Join(denHome, "source-config", "corp.yaml")
	if !strings.Contains(stderr, want) {
		t.Errorf("the warning must name %s; got:\n%s", want, stderr)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("den moved a directory it could not attribute to a repo: %v", err)
	}
}

// Reproduction (2026-08-19, real user report): a nest with two repos, the
// SECOND one dirty. The loop moved the first worktree to the trash, then
// refused on the second — leaving the user with one worktree in the trash, one
// on disk with a live git registration, a live sandbox and a surviving record.
// Nothing said which half had happened.
func TestRmMovesNothingWhenALaterWorktreeIsDirty(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	clean := filepath.Join(t.TempDir(), "back")
	dirty := filepath.Join(t.TempDir(), "front")
	createTestRepo(t, clean)
	createTestRepo(t, dirty)
	cleanWt := createWorktree(t, clean, root, "feat12")
	dirtyWt := createWorktree(t, dirty, root, "feat12")
	if err := os.WriteFile(filepath.Join(dirtyWt, "draft.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{
			{Name: "back", Origin: manifest.OriginPath, Repo: clean, Mount: cleanWt, Worktree: true},
			{Name: "front", Origin: manifest.OriginPath, Repo: dirty, Mount: dirtyWt, Worktree: true},
		},
	})
	// Readable: this test's subject is the dirty-worktree refusal, which runs
	// AFTER the env-record precheck.
	writeEnvRecord(t, denHome, "api.feat12")

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	_, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")

	if err == nil {
		t.Fatal("a dirty worktree must fail the rm")
	}
	// THE property: the refusal happens before the FIRST side effect. A repo
	// whose worktree den already moved cannot be brought back by retrying.
	if _, statErr := os.Stat(cleanWt); statErr != nil {
		t.Errorf("the clean worktree must still be in place: %v", statErr)
	}
	if _, statErr := os.Stat(dirtyWt); statErr != nil {
		t.Errorf("the dirty worktree must still be in place: %v", statErr)
	}
	// Checked on both destruction routes: cleanWorktrees fails before either is
	// ever reached, so neither must have run.
	if f.HasCalled("rm", "--force", "api.feat12") || f.HasCalled("env", "rm") {
		t.Errorf("the sandbox must NOT have been destroyed; calls: %v", f.Calls)
	}
	// And the message names the repo that blocks, not just a path the user
	// then has to map back to a repo by hand.
	if !strings.Contains(err.Error(), dirtyWt) {
		t.Errorf("the message must name the offending worktree; got: %v", err)
	}
}

// Companion of the reproduction above: the refusal names EVERY worktree that
// blocks, not just the first one den met. Reporting them one per run turns a
// two-repo nest into two `den rm`, each one telling the user about work they
// could have committed in the same pass.
func TestRmNamesEveryDirtyWorktreeAtOnce(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, worktreeConfig(root))
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	back := filepath.Join(t.TempDir(), "back")
	front := filepath.Join(t.TempDir(), "front")
	createTestRepo(t, back)
	createTestRepo(t, front)
	backWt := createWorktree(t, back, root, "feat12")
	frontWt := createWorktree(t, front, root, "feat12")
	for _, wt := range []string{backWt, frontWt} {
		if err := os.WriteFile(filepath.Join(wt, "draft.txt"), []byte("wip"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeManifest(t, denHome, manifest.Manifest{
		Sandbox:  "api.feat12",
		Nest:     manifest.Nest{Ref: "api", File: filepath.Join(denHome, "nests", "api.yaml")},
		Worktree: &manifest.Worktree{Name: "feat12", Branch: "feat12", Layout: "central", Root: root},
		Repos: []manifest.Repo{
			{Name: "back", Origin: manifest.OriginPath, Repo: back, Mount: backWt, Worktree: true},
			{Name: "front", Origin: manifest.OriginPath, Repo: front, Mount: frontWt, Worktree: true},
		},
	})
	writeEnvRecord(t, denHome, "api.feat12")

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	_, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err == nil {
		t.Fatal("two dirty worktrees must fail the rm")
	}
	for _, wt := range []string{backWt, frontWt} {
		if !strings.Contains(err.Error(), wt) {
			t.Errorf("the message must name %s; got: %v", wt, err)
		}
	}
	// And with --force both go, in one run.
	f2 := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f2, "--den-home", denHome, "rm", "api.feat12", "--force")
	if err != nil {
		t.Fatalf("with --force, the rm must succeed: %v", err)
	}
	if strings.Count(out, "worktree moved to trash:") != 2 {
		t.Errorf("both worktrees must be reclaimed; got:\n%s", out)
	}
}
