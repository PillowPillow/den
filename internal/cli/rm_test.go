package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// gitOutput runs git in dir and returns its combined output, for assertions on
// what the repository still knows.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestRmDestroysTheSandbox(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	f := &sbx.Fake{Responses: lsWith("api")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.HasCalled("rm", "--force", "api") {
		t.Errorf("calls: %v", f.Calls)
	}
}

// The agent profile persists: that is the whole point of a config_dir mounted
// RW. A den rm that wiped it would force the user to /login again.
func TestRmNeverTouchesTheAgentProfile(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
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
	if f.HasCalled("rm") {
		t.Errorf("no rm must be attempted; calls: %v", f.Calls)
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
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome,
		"rm", "api.feat12", "--keep-worktrees"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("--keep-worktrees must preserve %s: %v", wt, err)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
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
	if f.HasCalled("rm", "--force", foreignName) {
		t.Errorf("no rm must be attempted on a non-canonical name; calls: %v", f.Calls)
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
// say where den looked for the worktrees it could not name.
func TestRmUnreadableNestDoesNotPreventDestruction(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	// No writeNest("api", ...): nest "api" is absent from ~/.den/nests.
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "unreadable") {
		t.Errorf("the output must report the unreadable nest; got:\n%s", out)
	}
	// Default worktree_layout/worktree_root (minimalConfig declares neither):
	// central, under <denHome>/worktrees. Nothing was ever created there in
	// this test, so there is nothing to recover — the assertion is on den
	// naming where it looked, not on an abandoned directory.
	expectedWhere := filepath.Join(denHome, "worktrees", "feat12")
	if !strings.Contains(out, expectedWhere) {
		t.Errorf("the output must say where den looked for worktrees (%s); got:\n%s",
			expectedWhere, out)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
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
	f := &sbx.Fake{Responses: lsWith("api.feat12")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("a config fault unrelated to worktrees must not prevent destroying a live sandbox: %v", err)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
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
	// And nothing was destroyed: we do not remove a sandbox whose worktrees we
	// cannot clean up.
	if f.HasCalled("rm") {
		t.Errorf("no rm must be attempted; calls: %v", f.Calls)
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

// The nest yaml is gone, so den knows NO repo — and recovers the worktree
// anyway, from the directory itself. Same mechanism as issue #46's ad-hoc
// repos: the enumeration needs no declared list.
func TestRmUnreadableNestStillCleansUpUnderCentralLayout(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	// No writeNest("api", ...): the nest is absent from ~/.den/nests.

	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	p, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"central", root, worktree.Name{Dir: "feat12", Branch: "feat12"}, repo)
	if err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
		t.Errorf("%s must have been recovered and moved despite the unreadable nest; stat: %v", p, statErr)
	}
	// The failed resolution is still reported: the user must know den read no
	// nest, even though it cleaned up.
	if !strings.Contains(out, "unreadable") {
		t.Errorf("the unreadable nest must still be reported; got:\n%s", out)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
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
	// THE property: the sandbox is INTACT, and so is the worktree.
	if f.HasCalled("rm", "--force", "api.feat12") {
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
	f2 := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f2, "--den-home", denHome,
		"rm", "api.feat12", "--force")
	if err != nil {
		t.Fatalf("with --force, the rm must succeed: %v", err)
	}
	if !f2.HasCalled("rm", "--force", "api.feat12") {
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

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the per-repo worktree must have been moved from %s; stat: %v", path, err)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("calls: %v", f.Calls)
	}
}

// In per-repo, Path puts the worktree INSIDE the user's repository and den has
// no directory to enumerate: a repo passed on the command line leaves
// <repo>/.den/<wt> behind, inside a repository the user cares about, where
// nothing gitignores it. den cannot find it — so it says so, without ever
// claiming a leftover exists (it keeps no state and cannot know).
func TestRmWarnsAboutPossibleLeftoversUnderThePerRepoLayout(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig+"worktree_layout: per-repo\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")

	repo := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, repo)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	if _, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"per-repo", "", worktree.Name{Dir: "feat12", Branch: "feat12"}, repo); err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "<repo>/.den/feat12") {
		t.Errorf("the warning must name where to look; got:\n%s", out)
	}
	// Conditional, never an assertion: this teardown declared its repo and left
	// nothing behind.
	if strings.Contains(out, "was left behind") || strings.Contains(out, "survives at") {
		t.Errorf("the warning must not claim a leftover exists; got:\n%s", out)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
}

// The counterpart: nothing of the sort is printed under the central layout,
// where den enumerates and therefore knows.
func TestRmDoesNotWarnAboutLeftoversUnderTheCentralLayout(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, ".den/feat12") {
		t.Errorf("no per-repo warning must appear under the central layout; got:\n%s", out)
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

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "moved to trash") {
		t.Errorf("no trash announcement must appear for a directory already gone; got:\n%s", out)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
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
	if f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("the sandbox must not be destroyed if cleanup fails; calls: %v", f.Calls)
	}
}

// A failure of `sbx rm` (locked VM, sbx down...) must surface as-is, not be
// silently swallowed.
func TestRmSbxFailureSurfaces(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	responses := lsWith("api")
	responses["rm --force api"] = sbx.Response{Err: fmt.Errorf("fake sbx rm: simulated failure")}
	f := &sbx.Fake{Responses: responses}

	_, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api")
	if err == nil {
		t.Fatal("a failure of sbx rm must surface")
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

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	git := &fakeGitTwoRepoDeadlines{sleepOnFirstCall: 700 * time.Millisecond}
	deps := SystemDeps()
	deps.Sbx = f
	deps.Git = git
	root := NewRootCmdWith(deps)

	if _, err := executeCmd(t, root, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(git.listDeadlines) != 2 {
		t.Fatalf("expected one deadline per repo (2 repos), got %d", len(git.listDeadlines))
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

// Issue #46. `den api -w feat ~/dev/hotfix` gives hotfix a worktree, but a
// positional is deliberately NOT part of the sandbox identity, so `den rm`
// cannot find it in the nest. It is recovered from the directory instead.
//
// This is ALSO the ORDERING test, and the only one: it holds one declared repo
// AND one orphan, so an implementation that removed before enumerating would
// see removeParentDir empty <root>/<wt> and lose the orphan. Do not "simplify"
// it down to a single repo — that silently drops the coverage.
func TestRmCleansUpAWorktreeNoRepoDeclares(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")

	declared := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, declared)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+declared+" }\n")

	// The ad-hoc repo: mounted on the command line at spawn time, declared
	// nowhere.
	adhoc := filepath.Join(t.TempDir(), "hotfix")
	createTestRepo(t, adhoc)

	var paths []string
	for _, repo := range []string{declared, adhoc} {
		p, err := worktree.Ensure(context.Background(), worktree.NewGit(),
			"central", root, worktree.Name{Dir: "feat12", Branch: "feat12"}, repo)
		if err != nil {
			t.Fatalf("preparing the worktree of %s: %v", repo, err)
		}
		paths = append(paths, p)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s must have been moved to the trash; stat: %v", p, err)
		}
	}
	// executeCmdWithSbx MERGES stdout and stderr, so this count sees warnings
	// too: no later task may introduce a warning containing "moved to trash"
	// (Task 5's per-repo message deliberately does not).
	if strings.Count(out, "moved to trash") != 2 {
		t.Errorf("both worktrees must be announced; got:\n%s", out)
	}
	// The registration must be gone too, otherwise `git branch -d feat12` in
	// the user's own repository still refuses with "already checked out".
	if reg := gitOutput(t, adhoc, "worktree", "list", "--porcelain"); strings.Contains(reg, "feat12") {
		t.Errorf("the registration survives in %s:\n%s", adhoc, reg)
	}
}

// An orphan is not a licence to delete work: the uncommitted-changes refusal
// applies to a RECOVERED entry exactly as to a declared one — which is why
// orphans go through the same worktree.Remove and not a second removal path.
func TestRmRefusesADirtyOrphanWorktree(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	adhoc := filepath.Join(t.TempDir(), "hotfix")
	createTestRepo(t, adhoc)
	p, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"central", root, worktree.Name{Dir: "feat12", Branch: "feat12"}, adhoc)
	if err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(p, "wip.txt"), []byte("work in progress"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	_, err = executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err == nil {
		t.Fatal("a dirty recovered worktree must stop den rm, like a declared one")
	}
	if !strings.Contains(err.Error(), "wip.txt") {
		t.Errorf("error = %q, expected the uncommitted file to be named", err.Error())
	}
	if _, statErr := os.Stat(p); statErr != nil {
		t.Errorf("the worktree must still be there: %v", statErr)
	}
	if f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("the sandbox must NOT be destroyed while work is at stake; calls: %v", f.Calls)
	}
}

// ...and --force applies to a recovered entry too: the same flag, on the same
// code path.
func TestRmForceRemovesADirtyOrphanWorktree(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	adhoc := filepath.Join(t.TempDir(), "hotfix")
	createTestRepo(t, adhoc)
	p, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"central", root, worktree.Name{Dir: "feat12", Branch: "feat12"}, adhoc)
	if err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(p, "wip.txt"), []byte("work in progress"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12", "--force")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
		t.Errorf("--force must move the recovered worktree; stat: %v", statErr)
	}
	// den never deletes: the user must be told where their work went.
	if !strings.Contains(out, "moved to trash") {
		t.Errorf("the trash path must be announced; got:\n%s", out)
	}
}

// Best-effort on RESOLUTION (doctrine T13/T16): an orphan whose repository has
// been deleted since the spawn cannot be recovered. den says so and carries on
// — refusing here would leave the user with a live VM they can no longer
// destroy, over a directory.
func TestRmWarnsAboutAnUnrecoverableOrphanAndStillDestroys(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	// A directory that is not a git worktree at all: recovery is impossible.
	stray := filepath.Join(root, "feat12", "hotfix")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("an unrecoverable orphan must not fail den rm: %v", err)
	}
	if !strings.Contains(out, stray) {
		t.Errorf("the warning must name the directory left behind; got:\n%s", out)
	}
	if _, statErr := os.Stat(stray); statErr != nil {
		t.Errorf("den must not have touched %s: %v", stray, statErr)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
}

// THE CROSS-NEST INVARIANT. worktree.Path has no nest component, so
// <worktree_root>/<wt> is a namespace SHARED by every nest spawned with the
// same -w: `den api -w feat12` and `den web -w feat12` both land under
// <root>/feat12. The enumeration therefore SEES nest web's worktree, and all
// six recovery guards say "yes, den placed this" — because den did. Only the
// repos web DECLARES tell the two apart.
//
// Two nests, each declaring its own repo, is the whole point of this setup: do
// NOT simplify it down to one nest. That is exactly the shape a future
// "accountedFor already de-duplicates" simplification would drop, and the
// regression it hides is den trashing another nest's work under a trash entry
// named after this one.
func TestRmLeavesTheWorktreeOfAnotherNestAlone(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")

	mine := filepath.Join(t.TempDir(), "api")
	createTestRepo(t, mine)
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+mine+" }\n")

	// Another nest, another repo, the SAME worktree name: nothing in den
	// forbids it, and the two worktrees sit side by side under <root>/feat12.
	theirs := filepath.Join(t.TempDir(), "web")
	createTestRepo(t, theirs)
	writeNest(t, denHome, "web", "stack: devx\nrepos:\n  - { path: "+theirs+" }\n")

	var paths []string
	for _, repo := range []string{mine, theirs} {
		p, err := worktree.Ensure(context.Background(), worktree.NewGit(),
			"central", root, worktree.Name{Dir: "feat12", Branch: "feat12"}, repo)
		if err != nil {
			t.Fatalf("preparing the worktree of %s: %v", repo, err)
		}
		paths = append(paths, p)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12", "web.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(paths[0]); !os.IsNotExist(statErr) {
		t.Errorf("this nest's own worktree %s must still be cleaned up; stat: %v", paths[0], statErr)
	}
	if _, statErr := os.Stat(paths[1]); statErr != nil {
		t.Errorf("nest web's worktree %s must survive `den rm api.feat12`: %v", paths[1], statErr)
	}
	// The DIRECTORY surviving is not enough: a web worktree stripped of its
	// registration is no longer usable, and `den web -w feat12` would then
	// refuse to reuse it.
	if reg := gitOutput(t, theirs, "worktree", "list", "--porcelain"); !strings.Contains(reg, "feat12") {
		t.Errorf("nest web's registration must survive in %s:\n%s", theirs, reg)
	}
	// Skip-and-warn, never silent exclusion: a user who sees a directory
	// survive must learn why, and the reason names the nest that owns it.
	if !strings.Contains(out, paths[1]) || !strings.Contains(out, `"web"`) {
		t.Errorf("the warning must name the surviving directory and the nest owning it; got:\n%s", out)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
}

// The BLOCKING mode of the same defect, and the worse one: recovered as an
// orphan, another nest's dirty worktree meets the uncommitted-changes refusal
// and `den rm api.feat12` FAILS — naming a file the user never touched in this
// nest, and leaving them with a live VM they cannot destroy until they clean up
// unrelated work. The guard drops the entry from the work list entirely, so the
// refusal never sees it.
func TestRmSucceedsWhenAnotherNestsWorktreeIsDirty(t *testing.T) {
	denHome := t.TempDir()
	root := filepath.Join(denHome, "worktrees")
	writeConfig(t, denHome, minimalConfig+"worktree_layout: central\nworktree_root: "+root+"\n")
	writeStack(t, denHome, "devx", "image: devx:v1\n")
	writeNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	theirs := filepath.Join(t.TempDir(), "web")
	createTestRepo(t, theirs)
	writeNest(t, denHome, "web", "stack: devx\nrepos:\n  - { path: "+theirs+" }\n")

	p, err := worktree.Ensure(context.Background(), worktree.NewGit(),
		"central", root, worktree.Name{Dir: "feat12", Branch: "feat12"}, theirs)
	if err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(p, "wip.txt"), []byte("work in progress"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api.feat12", "web.feat12")}
	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("another nest's uncommitted work must not block this teardown: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(p, "wip.txt")); statErr != nil {
		t.Errorf("nest web's uncommitted work must be untouched: %v", statErr)
	}
	if !f.HasCalled("rm", "--force", "api.feat12") {
		t.Errorf("the sandbox must be destroyed; calls: %v", f.Calls)
	}
}
