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
// say where the abandoned worktree was left.
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
	// central, under <denHome>/worktrees.
	expectedWhere := filepath.Join(denHome, "worktrees", "feat12")
	if !strings.Contains(out, expectedWhere) {
		t.Errorf("the output must say where the abandoned worktree was left (%s); got:\n%s",
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
	f := &sbx.Fake{Responses: lsWith("corp-api")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "corp:api"); err != nil {
		t.Fatalf("den rm corp:api: %v", err)
	}
	if !f.HasCalled("rm", "--force", "corp-api") {
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

	f := &sbx.Fake{Responses: lsWith("corp-api.feat12")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "corp-api.feat12")
	if err != nil {
		t.Fatalf("den rm corp-api.feat12: %v", err)
	}
	if !strings.Contains(out, "moved to trash") {
		t.Errorf("the source nest's worktree must be cleaned up, not warned about; got:\n%s", out)
	}
	if !f.HasCalled("rm", "--force", "corp-api.feat12") {
		t.Errorf("the sandbox must still be destroyed; calls: %v", f.Calls)
	}
}

// The prefixed spelling reaches the same worktree'd sandbox: flattening the
// whole argument would rewrite the "." and address "corp-api-feat12".
func TestRmAcceptsAWorktreedSourceReference(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	f := &sbx.Fake{Responses: lsWith("corp-api.feat12")}

	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome,
		"rm", "corp:api.feat12", "--keep-worktrees"); err != nil {
		t.Fatalf("den rm corp:api.feat12: %v", err)
	}
	if !f.HasCalled("rm", "--force", "corp-api.feat12") {
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
	if !f.HasCalled("rm", "--force", "corp-api.feat12") {
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
	if !f.HasCalled("rm", "--force", "corp-api.feat12") {
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
	if !f.HasCalled("rm", "--force", "api.feat12") {
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
	if !f.HasCalled("rm", "--force", "api.reco") {
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
	if !f.HasCalled("rm", "--force", "api.reco") {
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
	if !f.HasCalled("rm", "--force", "api.reco") {
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
