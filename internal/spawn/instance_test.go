package spawn

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/sbx"
)

// sandboxNameFrom spawns with the given options and returns the name `sbx
// create` received. Asserting on the ARGV rather than on an internal variable
// is what makes the test survive a refactor of the naming block: the name that
// matters is the one sbx is told, not the one den computed.
func sandboxNameFrom(t *testing.T, denHome string, o Options) string {
	t.Helper()
	f, d := fakeDeps()
	if err := Spawn(context.Background(), denHome, o, d); err != nil {
		t.Fatalf("spawn %+v: %v", o, err)
	}
	for _, call := range f.Calls {
		if len(call) == 0 || call[0] != "create" {
			continue
		}
		for i, arg := range call {
			if arg == "--name" && i+1 < len(call) {
				return call[i+1]
			}
		}
	}
	t.Fatalf("no `create --name` in calls: %v", f.Calls)
	return ""
}

// The naming table of the spec (§ "Le nom de sandbox"). One test, four rows:
// the rule is a single rule and reading it in one place is the point.
func TestInstanceNamesTheSandbox(t *testing.T) {
	for _, c := range []struct {
		name string
		o    Options
		want string
	}{
		{"bare", Options{Nest: "api"}, "api"},
		{"worktree only", Options{Nest: "api", Worktree: "feature/123"}, "api.feature-123"},
		{"instance only", Options{Nest: "api", Instance: "reco"}, "api.reco"},
		{"instance wins over worktree",
			Options{Nest: "api", Worktree: "feature/123", Instance: "reco"}, "api.reco"},
	} {
		t.Run(c.name, func(t *testing.T) {
			denHome, _ := denTest(t)
			if got := sandboxNameFrom(t, denHome, c.o); got != c.want {
				t.Errorf("sandbox name = %q, want %q", got, c.want)
			}
		})
	}
}

// --as goes through the SAME flattening as -w: one charset, one rewrite, no
// second path to keep in sync.
func TestInstanceIsFlattenedLikeAWorktree(t *testing.T) {
	denHome, _ := denTest(t)
	if got := sandboxNameFrom(t, denHome, Options{Nest: "api", Instance: "feat/x"}); got != "api.feat-x" {
		t.Errorf("sandbox name = %q, want %q", got, "api.feat-x")
	}
}

// An instance that cannot be named is refused BEFORE any side effect, like
// every other name den builds.
func TestSpawnRefusesAnUnnameableInstance(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()
	err := Spawn(context.Background(), denHome, Options{Nest: "api", Instance: "-x"}, d)
	if err == nil {
		t.Fatal("an instance starting with `-` must be refused: it is indistinguishable from a flag")
	}
	if !strings.Contains(err.Error(), "instance") {
		t.Errorf("the refusal must name what is wrong (instance), got: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("refused, yet something was created: %v", f.Calls)
	}
}

// Decision 4: --as renames the SANDBOX, never the worktree directory. The
// manifest is where that separation is observable, and it is also what `den
// rm` replays — a label recorded as Worktree.Name would send worktree.Remove
// at a directory nobody created.
func TestInstanceDoesNotRenameTheWorktreeDirectory(t *testing.T) {
	denHome, _ := denTest(t)
	_, d := fakeDeps()
	o := Options{Nest: "api", Worktree: "feature/123", Instance: "reco"}
	if err := Spawn(context.Background(), denHome, o, d); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	m, err := manifest.Read(denHome, "api.reco")
	if err != nil {
		t.Fatalf("reading the manifest of api.reco: %v", err)
	}
	if m.Worktree == nil {
		t.Fatal("a -w spawn must record a worktree block")
	}
	if m.Worktree.Name != "feature-123" {
		t.Errorf("Worktree.Name = %q, want %q (the flattened BRANCH, not the label)",
			m.Worktree.Name, "feature-123")
	}
	if m.Worktree.Branch != "feature/123" {
		t.Errorf("Worktree.Branch = %q, want %q", m.Worktree.Branch, "feature/123")
	}
}

// `-w` on a LIVE instance creates NOTHING.
//
// A live sandbox is attached to and nothing is reapplied to it (§6): its mounts
// are frozen at its creation. den used to run worktree.Ensure on this branch
// anyway, so `den spawn api --as reco -w brand/new` created a git worktree per
// repo plus the branch — for a sandbox that mounts none of it. The manifest is
// not rewritten on the attach branch either, so `den rm` reclaimed nothing: the
// directories and the branches were orphaned, with no den command able to
// remove them.
//
// Both halves are asserted, on disk and in git: the directory is what the user
// trips over, the branch is what `git branch` shows them forever after.
func TestWorktreeFlagOnALiveInstanceCreatesNothing(t *testing.T) {
	denHome, repo := denTest(t)

	// The sandbox is created WITHOUT -w: it mounts the repo as it is, and that
	// is what its record says.
	_, first := fakeDeps()
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Instance: "reco"}, first); err != nil {
		t.Fatalf("first spawn: %v", err)
	}

	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api.reco","status":"running","workspaces":["` +
			repo + `"]}]}`),
	}
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Instance: "reco", Worktree: "brand/new"}, d); err != nil {
		t.Fatalf("attaching a live instance must not refuse over a -w it cannot honour: %v", err)
	}

	if _, err := os.Stat(filepath.Join(denHome, "worktrees", "brand-new")); err == nil {
		t.Error("a worktree was created for a sandbox that mounts none of it, " +
			"and no den command can reclaim it")
	}
	if branchExists(t, repo, "brand/new") {
		t.Error("a branch was created for a sandbox that mounts none of it")
	}
	// The attach still happens, in the directory the VM really mounts.
	if !f.HasAttached("exec", "-it", "-w", repo, "api.reco", "bash", "-l") {
		t.Errorf("den must attach to the live instance; attaches: %v", f.Attaches)
	}
	// And the VM is not slandered: it mounts exactly what its record names.
	if strings.Contains(out.String(), "is not mounted") {
		t.Errorf("the VM mounts what it was created with; nothing may be reported:\n%s", out.String())
	}
	// The progress line goes with the creation it announces. Matched on the
	// CREATE shape (`worktree <repo>: `), never on the `worktree ` prefix: step
	// 1bis prints `worktree "brand/new": branch name kept, sandbox becomes
	// api.reco` on this very spawn, and a prefix match would fail on a line
	// that is correct and wanted.
	if strings.Contains(out.String(), "worktree api: ") {
		t.Errorf("a creation was announced on a branch that creates nothing:\n%s", out.String())
	}
}

// The other side of the same coin: an `--as` instance created WITH -w,
// re-attached WITHOUT repeating it.
//
// The sandbox name's second component is the LABEL since `--as`, so it says
// nothing about worktrees: den has to read the record. Deriving the expected
// mounts from the flags instead left `workspaces` holding the raw repo paths
// while the VM mounts the worktrees, and every repo of a healthy sandbox came
// back "is not mounted", with `den rm` — destruction — as the advice.
func TestAttachingAnInstanceWithoutRepeatingTheWorktreeReportsNothing(t *testing.T) {
	denHome, _ := denTest(t)

	_, first := fakeDeps()
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Instance: "reco", Worktree: "feature/123"}, first); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	// The mounts come from the RECORD, so the fixture and den agree on the
	// worktree path without this test recomputing the layout.
	recorded, err := manifest.Read(denHome, "api.reco")
	if err != nil {
		t.Fatalf("reading the record the first spawn wrote: %v", err)
	}
	mounts := []string{}
	for _, r := range recorded.Repos {
		mounts = append(mounts, r.Mount)
	}
	mounts = append(mounts, recorded.GitDirs...)

	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api.reco","status":"running","workspaces":` +
			jsonStrings(mounts) + `}]}`),
	}
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Instance: "reco"}, d); err != nil {
		t.Fatalf("attaching spawn: %v", err)
	}

	if strings.Contains(out.String(), "is not mounted") {
		t.Errorf("the VM mounts every repo its record names:\n%s", out.String())
	}
	if strings.Contains(out.String(), "den rm") {
		t.Errorf("destruction must not be advised over a healthy sandbox:\n%s", out.String())
	}
	// Guard on the guard: the silence above must come from a comparison that
	// happened, not from an empty one. A VM mounting something else must still
	// be reported.
	mismatched, md := fakeDeps()
	var mismatchedOut bytes.Buffer
	md.Out = &mismatchedOut
	mismatched.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"api.reco","status":"running","workspaces":["/w/elsewhere"]}]}`),
	}
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Instance: "reco"}, md); err != nil {
		t.Fatalf("attaching spawn (mismatched): %v", err)
	}
	if !strings.Contains(mismatchedOut.String(), mounts[0]+" is not mounted") {
		t.Errorf("a repo the VM does not mount must still be reported:\n%s", mismatchedOut.String())
	}
}

// --as without -w creates no worktree at all: the record must carry NO
// worktree block, or `den ls` would print a branch that does not exist and
// `den rm` would look for a directory nobody created.
func TestInstanceWithoutWorktreeRecordsNoWorktreeBlock(t *testing.T) {
	denHome, _ := denTest(t)
	_, d := fakeDeps()
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Instance: "reco"}, d); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	m, err := manifest.Read(denHome, "api.reco")
	if err != nil {
		t.Fatalf("reading the manifest of api.reco: %v", err)
	}
	if m.Worktree != nil {
		t.Errorf("no -w was given, yet a worktree block was recorded: %+v", m.Worktree)
	}
}
