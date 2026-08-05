package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/worktree"
)

// These tests exercise the FULL CLI on HOSTILE configurations — those of task
// 17c's table, plus a few found by poking at the built binary.
//
// They live at the CLI level, not only in the packages that carry the guards,
// for a precise reason: what matters is not that a function refuses, it is
// that the USER gets a refusal before den has damaged anything. That property
// crosses half the project and is checked nowhere else.
//
// None of them invokes the real `sbx`: the Runner is an sbx.Fake, and the "no
// side effect" assertion is made on its Calls.

// denHomeHostile builds a VALID den home and returns its path, along with the
// path of the nest's single repo. Each test then degrades only one aspect: a
// refusal would prove nothing about its cause without a healthy baseline.
//
// The repo is a REAL GIT REPOSITORY, and that is not a comfort detail: the
// tests checking no worktree was created would be EMPTY without it.
// worktree.Ensure opens with a checkRepo that fails immediately on a plain
// directory, without creating anything — the "no worktree" assertion would
// then be true no matter what, including if den actually tried to create one.
// Measured: with a bare directory, swapping the guard order in spawn.Spawn
// still let the test pass.
//
// No `egress:`: policy.Settle returns nil on an empty allowlist, sparing the
// suite the 60s settle-loop (same precaution as denHomeSpawnable,
// spawn_test.go). The git environment is neutralized package-wide by TestMain
// (main_test.go).
func denHomeHostile(t *testing.T) (string, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "myrepo")
	createTestRepo(t, repo)
	return denHomeFor(t, repo), repo
}

// Case 5 of the table — THE riskiest, because its cost is a side effect: a
// worktree created for a sandbox that will never exist, which the user would
// have to remove by hand.
//
// The naming is computed BEFORE the worktree-creation loop in spawn.Spawn,
// and this test locks that ORDER, not the message: swapping the two steps
// would leave the refusal intact and the worktree created, without any test
// in the spawn package noticing.
//
// The rejected value is "+wip" since F4, no longer "feature/123": a "/" is
// now FLATTENED (the branch keeps its name, the sandbox takes
// "feature-123") and therefore no longer produces any error. What flattening
// does not fix is a first character that is not alphanumeric — such a name is
// indistinguishable from a flag, and den will not pick another one on the
// user's behalf. "+" rather than "-": pflag would take "-wip" for a flag
// before den ever sees anything.
func TestNonSandboxableWorktreeIsRefusedWithoutCreatingAnything(t *testing.T) {
	home, _ := denHomeHostile(t)

	f, _, err := runFullRoot(t, home, "spawn", "api", "-w", "+wip")
	if err == nil {
		t.Fatal("a worktree named \"+wip\" must be refused")
	}
	// The offending character, not just "invalid name": that is what the user
	// must fix.
	if !strings.Contains(err.Error(), `"+"`) {
		t.Errorf("the message must name the refused character; got: %v", err)
	}
	if !strings.Contains(err.Error(), "+wip") {
		t.Errorf("the message must quote the received value; got: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("no sbx call must happen; calls: %v", f.Calls)
	}
	// The side effect this test exists to forbid: the worktree root must not
	// even have been created.
	if _, err := os.Stat(filepath.Join(home, "worktrees")); err == nil {
		t.Error("a worktree was created although the sandbox name is invalid")
	}
}

// Case 7 of the table — a nest with no repo at all.
//
// The question the brief asked: does `sbx create` still get at least one
// workspace? Measured answer: yes, the agent's profile. This test pins it,
// because CreateArgv REJECTS a create with no workspace ("sbx create requires
// at least one path"): if the profile ever stopped being mounted, a
// repo-less nest would become unsandboxable, and the message would talk about
// missing workspaces without saying the profile is what vanished.
func TestANestWithNoRepoStillMountsTheAgentProfile(t *testing.T) {
	home, _ := denHomeHostile(t)
	if err := os.WriteFile(filepath.Join(home, "nests", "api.yaml"),
		[]byte("stack: devx\nrepos: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := runFullRoot(t, home, "spawn", "api")
	if err != nil {
		t.Fatalf("a nest with no repo must stay spawnable: %v", err)
	}
	if !f.HasCalled("create") {
		t.Fatalf("no `sbx create`; calls: %v", f.Calls)
	}
	var create []string
	for _, a := range f.Calls {
		if len(a) > 0 && a[0] == "create" {
			create = a
		}
	}
	// The agent's profile is the fallback workspace, and the only one here.
	profile := filepath.Join(home, "agents", "claude")
	if !slices.Contains(create, profile) {
		t.Errorf("the agent's profile (%s) must be mounted; create argv = %v", profile, create)
	}
	// And it is the LAST positional argument, hence a workspace: a create with
	// no workspace at all would be rejected by CreateArgv.
	if create[len(create)-1] != profile {
		t.Errorf("last workspace = %q, expected profile %q", create[len(create)-1], profile)
	}
}

// Case 8 of the table, as it shows up to the USER — the 15th hostile
// configuration, found by poking at the binary.
//
// The refusal itself is locked by TestValidateWorktreeRefusesARelativeRoot
// (internal/config). THIS test locks something the config package cannot
// see: that the refusal arrives BEFORE any side effect, and names the field
// to fix. Before the fix, measured on the binary, den actually created a
// worktree and a branch INSIDE the user's repo, then failed with a message
// talking about "workspace #1" — a word that appears nowhere in the user's
// configuration.
func TestRelativeWorktreeRootIsRefusedNamingTheField(t *testing.T) {
	home, repo := denHomeHostile(t)
	config, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.yaml"),
		append(config, []byte("worktree_root: wt-relative\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := runFullRoot(t, home, "spawn", "api", "-w", "feat1")
	if err == nil {
		t.Fatal("a relative worktree_root must be refused")
	}
	if !strings.Contains(err.Error(), "worktree_root") {
		t.Errorf("the message must name the field to fix; got: %v", err)
	}
	if !strings.Contains(err.Error(), "wt-relative") {
		t.Errorf("the message must quote the received value; got: %v", err)
	}
	if len(f.Calls) != 0 {
		t.Errorf("no sbx call must happen; calls: %v", f.Calls)
	}

	// THE DEFECT IS THE SIDE EFFECT, and that is therefore what must be
	// measured: the message and the absence of an sbx call say nothing about
	// what den left in the repo. Measured before the fix, `git worktree list`
	// rendered "<repo>/wt-relative/feat1/myrepo ... [feat1]" — a worktree AND a
	// branch the user had to remove by hand.
	//
	// The exact path comes from git's relative resolution: `worktree add` runs
	// WITH THE REPO AS CWD, so "wt-relative/..." lands inside it.
	if _, err := os.Stat(filepath.Join(repo, "wt-relative")); err == nil {
		t.Errorf("den created %s inside the user's repo although the configuration "+
			"is refused", filepath.Join(repo, "wt-relative"))
	}
	// The branch too: `git worktree add -b` creates it, and it survives even
	// if the directory is cleaned up.
	if branches := gitInTest(t, repo, "branch", "--list", "feat1"); strings.TrimSpace(branches) != "" {
		t.Errorf("den created branch feat1 in the user's repo: %q", branches)
	}
	// And the repo knows only one worktree, the main one.
	if list := gitInTest(t, repo, "worktree", "list"); strings.Count(strings.TrimSpace(list), "\n") != 0 {
		t.Errorf("the repo carries more than one worktree:\n%s", list)
	}
}

// gitInTest runs git in a repo and returns its output.
//
// It goes through worktree.NewGit() rather than the standard library's exec
// package, and that is not a stylistic choice: task 17c's hermeticity guard
// forbids that package in tests outside an allowlist this file is not part
// of. The need here — running a real git — is the same one that earned
// internal/worktree its place on that list; borrowing its exported access
// satisfies both.
//
// The machine's git environment is neutralized package-wide by TestMain
// (main_test.go). These calls are READ-ONLY: no configuration is written, no
// `git config` is run.
func gitInTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := worktree.NewGit().Run(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// 16th hostile configuration: a broken stack that NOBODY uses must not
// prevent spawning a nest whose stack is healthy.
//
// Measured on the binary before the fix: `den spawn api` failed printing the YAML
// error of an unrelated stack, although nest api references devx, perfectly
// valid. This is the doctrine T16 established for nests, never applied to
// stacks until now.
func TestABrokenStackDoesNotPreventSpawningAHealthyNest(t *testing.T) {
	home, _ := denHomeHostile(t)
	if err := os.MkdirAll(filepath.Join(home, "stacks", "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "stacks", "other", "stack.yaml"),
		[]byte("image: other:v1\nimag: typo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := runFullRoot(t, home, "spawn", "api")
	if err != nil {
		t.Fatalf("nest api uses devx, healthy: the spawn must succeed; got: %v", err)
	}
	if !f.HasCalled("create") {
		t.Errorf("no `sbx create`; calls: %v", f.Calls)
	}
}

// The counterpart of the previous test, and the half that really matters:
// when it is the nest's OWN stack that is broken, the nest must fail — and the
// message must carry the YAML fault, not a "not found" that would send the
// user to create a file that already exists.
//
// Without this test, plainly ignoring broken stacks would pass the previous
// test while making the real problem mute.
func TestANestWhoseStackIsBrokenFailsNamingTheFault(t *testing.T) {
	home, _ := denHomeHostile(t)
	if err := os.WriteFile(filepath.Join(home, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nimag: typo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := runFullRoot(t, home, "spawn", "api")
	if err == nil {
		t.Fatal("a nest whose stack is unreadable must not spawn")
	}
	if !strings.Contains(err.Error(), "imag") {
		t.Errorf("the message must carry the stack's YAML fault; got: %v", err)
	}
	if strings.Contains(err.Error(), "not found") {
		t.Errorf("the stack exists: \"not found\" would send the user to create a file "+
			"that is already present; got: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("no sandbox must be created; calls: %v", f.Calls)
	}
}
