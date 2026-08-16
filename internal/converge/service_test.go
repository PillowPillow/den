package converge

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/source"
	"github.com/PillowPillow/den/internal/worktree"
)

func testClock() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC) }

// sourceRemote turns a source tree into a git remote den can clone from.
func sourceRemote(t *testing.T, tree string) string {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.email=t@example.test", "-c", "user.name=t", "commit", "-q", "-m", "source"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = tree
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return tree
}

// serviceFixture prepares a den home, a source remote whose nests need one
// repository, and that repository present on the machine.
func serviceFixture(t *testing.T) (denHome, remote, repo string) {
	t.Helper()
	denHome = t.TempDir()
	tree := validSourceTree(t)
	// The exported nest declares a repo key so discovery and readiness have
	// something real to answer.
	writeFile(t, filepath.Join(tree, "nests", "leo.yaml"),
		"stack: base\nrepos:\n  - { key: api, url: https://gitlab.example.test/team/api.git }\n")
	remote = sourceRemote(t, tree)

	root := t.TempDir()
	repo = initRepoWithRemote(t, filepath.Join(root, "api"), "git@gitlab.example.test:team/api.git")
	return denHome, remote, root
}

func planFor(t *testing.T, s Service, req Request) *Plan {
	t.Helper()
	p, err := s.Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return p
}

func requestFor(t *testing.T, denHome, remote, root string, f *sbx.Machine) (Service, Request, func()) {
	t.Helper()
	s := Service{Git: worktree.NewGit(), Sbx: f, Secrets: f, Now: testClock}
	c, err := source.AcquireCandidate(context.Background(), s.Git, denHome, remote)
	if err != nil {
		t.Fatalf("AcquireCandidate: %v", err)
	}
	req := Request{
		Mode: ModeInit, DenHome: denHome, Name: "dg", Candidate: c,
		DenVersion: "1.7.0",
		Answers: Answers{
			RepositoryRoots: []string{root},
			Credentials:     map[string]CredentialAnswer{"gitlab_token": {Value: "sentinel-secret"}},
		},
	}
	return s, req, func() { c.Close() }
}

// snapshot lists every path under dir, so a "nothing was written" assertion
// catches a CREATED DIRECTORY too — WriteReceipt and WritePersonal both
// MkdirAll on first use, and a planner that touched them would slip past a
// file-content comparison.
func snapshot(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Planning is READ-ONLY. It is what a user confirms, and a plan that had
// already changed the machine would make the confirmation a formality.
func TestPlanMutatesNothing(t *testing.T) {
	denHome, remote, root := serviceFixture(t)
	f := sbx.NewMachine()
	s, req, cleanup := requestFor(t, denHome, remote, root, f)
	defer cleanup()

	before := snapshot(t, denHome)
	plan := planFor(t, s, req)
	after := snapshot(t, denHome)

	// The candidate's own staging directory is under cache/ and existed before
	// the snapshot: nothing NEW may appear.
	if len(before) != len(after) {
		t.Fatalf("planning wrote to the den home:\nbefore %v\nafter  %v", before, after)
	}
	if _, err := os.Stat(source.Dir(denHome, "dg")); err == nil {
		t.Error("planning installed the source")
	}
	if _, err := os.Stat(source.ReceiptPath(denHome, "dg")); err == nil {
		t.Error("planning wrote a receipt")
	}
	for _, call := range f.Calls {
		switch call[0] {
		case "secret", "policy":
			if len(call) > 1 && call[1] != "ls" {
				t.Errorf("planning ran a mutating sbx command: %v", call)
			}
		case "create", "exec", "template":
			if call[0] == "template" && len(call) > 1 && call[1] == "ls" {
				continue
			}
			t.Errorf("planning ran a mutating sbx command: %v", call)
		}
	}

	// And the plan really covers the source: every credential, the policy
	// group, the build, the repo key and every exported nest.
	if len(plan.Resources) != 5 {
		t.Errorf("resources = %+v, want three credentials, the policy group and the build", plan.Resources)
	}
	if len(plan.RepoMatches) != 1 || plan.RepoMatches[0].Requirement.Key != "api" {
		t.Errorf("repo matches = %+v", plan.RepoMatches)
	}
	if len(plan.Nests) != 1 || plan.Nests[0].Name != "leo" {
		t.Errorf("nests = %+v", plan.Nests)
	}
	if plan.Status != source.StatusReady {
		t.Errorf("status = %q, want ready: the repository is on this machine", plan.Status)
	}
	if !strings.Contains(plan.TrustBoundary, "provision scripts") {
		t.Errorf("trust boundary = %q", plan.TrustBoundary)
	}
}

// An sbx that does not answer is `unknown`, and the plan still lists
// everything the source declares: a plan that shrank would let a user confirm
// an installation whose scope they cannot see.
func TestPlanReportsUnknownWhenTheMachineCannotBeObserved(t *testing.T) {
	denHome, remote, root := serviceFixture(t)
	f := sbx.NewMachine()
	f.Fail["secret ls -g"] = errors.New("keychain error -50")
	s, req, cleanup := requestFor(t, denHome, remote, root, f)
	defer cleanup()

	plan := planFor(t, s, req)
	if plan.Status != source.StatusUnknown {
		t.Fatalf("status = %q, want unknown", plan.Status)
	}
	if Succeeds(plan.Status) {
		t.Error("an unknown status must not let a command exit 0")
	}
	if len(plan.Resources) != 5 {
		t.Errorf("resources = %+v, want every declared resource listed", plan.Resources)
	}
	for _, r := range plan.Resources {
		if r.Known {
			t.Errorf("resource %s claims to be known on an unobservable machine", r.ID)
		}
	}
}

// The application order of spec §9.2, and the final commit marker last.
func TestApplyOrdersMutationsAndCommitsLast(t *testing.T) {
	denHome, remote, root := serviceFixture(t)
	f := sbx.NewMachine()
	s, req, cleanup := requestFor(t, denHome, remote, root, f)
	defer cleanup()
	req.FreshGlobalConfig = []byte("agents: {}\n")

	plan := planFor(t, s, req)
	var out strings.Builder
	result, err := s.Apply(context.Background(), req, plan, &out, &out)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Credentials, then the policy, then the build.
	order := []string{}
	for _, c := range f.Calls {
		switch {
		case c[0] == "secret" && c[1] != "ls":
			order = append(order, "secret")
		case c[0] == "policy" && c[1] == "allow":
			order = append(order, "policy")
		case c[0] == "create":
			order = append(order, "build")
		}
	}
	if len(order) < 3 || order[0] != "secret" {
		t.Fatalf("mutation order = %v, want credentials first", order)
	}
	lastSecret, firstPolicy := -1, -1
	for i, kind := range order {
		if kind == "secret" {
			lastSecret = i
		}
		if kind == "policy" && firstPolicy < 0 {
			firstPolicy = i
		}
	}
	if firstPolicy < lastSecret {
		t.Errorf("mutation order = %v, want every credential before the network policy", order)
	}

	// The source is installed, the personal configuration carries the
	// discovered repository, and the receipt is final.
	if _, err := os.Stat(filepath.Join(source.Dir(denHome, "dg"), source.ManifestFile)); err != nil {
		t.Fatalf("the source was not installed: %v", err)
	}
	personal, err := source.LoadPersonal(denHome, "dg")
	if err != nil {
		t.Fatalf("LoadPersonal: %v", err)
	}
	if personal.Version != "1.0.0" || personal.Repos["api"] == "" {
		t.Errorf("personal = %+v", personal)
	}
	receipt, err := source.LoadReceipt(denHome, "dg")
	if err != nil {
		t.Fatalf("LoadReceipt: %v", err)
	}
	if receipt.Status != source.StatusReady || receipt.Version != "1.0.0" {
		t.Errorf("receipt = %+v", receipt)
	}
	if receipt.Commit == "" || !strings.HasPrefix(receipt.ManifestDigest, "sha256:") {
		t.Errorf("receipt = %+v, want the commit and the manifest digest", receipt)
	}
	if !receipt.AppliedAt.Equal(testClock()) {
		t.Errorf("applied_at = %v", receipt.AppliedAt)
	}
	if result.Status != source.StatusReady {
		t.Errorf("result = %+v", result)
	}
	// The source is usable straight away: the three files agree.
	if _, err := source.RequireUsable(denHome, "dg"); err != nil {
		t.Errorf("the installed source is not usable: %v", err)
	}
	// Nothing printed carries the credential value.
	if strings.Contains(out.String(), "sentinel-secret") {
		t.Errorf("the applied value was printed:\n%s", out.String())
	}
}

// A managed resource that fails stops the run, keeps the previous version
// active, and leaves an `applying` receipt recording what did converge — which
// is what makes `den source configure` a resume rather than a restart.
func TestApplyLeavesAResumableStateOnFailure(t *testing.T) {
	denHome, remote, root := serviceFixture(t)
	f := sbx.NewMachine()
	f.Fail["policy allow network api.example.test"] = errors.New("sbx refused")
	s, req, cleanup := requestFor(t, denHome, remote, root, f)
	defer cleanup()

	plan := planFor(t, s, req)
	var out strings.Builder
	if _, err := s.Apply(context.Background(), req, plan, &out, &out); err == nil {
		t.Fatal("expected the policy failure to stop the run")
	}

	if _, err := source.LoadPersonal(denHome, "dg"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a failed installation must not configure a version: %v", err)
	}
	receipt, err := source.LoadReceipt(denHome, "dg")
	if err != nil {
		t.Fatalf("LoadReceipt: %v", err)
	}
	if receipt.Status != source.StatusApplying || receipt.TargetVersion != "1.0.0" {
		t.Fatalf("receipt = %+v, want an applying marker naming the target", receipt)
	}
	if receipt.Resources.Credentials != source.ResourceReady {
		t.Errorf("receipt = %+v, want the credentials that DID converge recorded", receipt.Resources)
	}
	if _, err := os.Stat(source.Dir(denHome, "dg")); err == nil {
		t.Error("a source whose resources failed must not be installed")
	}
}

// The resume: the same convergence run again skips what verifies and finishes
// the rest. It also proves the skip is not blind — the run VERIFIES the
// resources it does not reapply.
func TestConfigureResumesWithoutReapplyingVerifiedResources(t *testing.T) {
	denHome, remote, root := serviceFixture(t)
	f := sbx.NewMachine()
	f.Fail["policy allow network api.example.test"] = errors.New("sbx refused")
	s, req, cleanup := requestFor(t, denHome, remote, root, f)
	defer cleanup()

	plan := planFor(t, s, req)
	var out strings.Builder
	if _, err := s.Apply(context.Background(), req, plan, &out, &out); err == nil {
		t.Fatal("expected the first run to fail")
	}

	// The credentials applied by the failed run are really on the machine
	// (the double recorded them), and the policy command now works.
	delete(f.Fail, "policy allow network api.example.test")

	before := len(f.Calls)
	second := planFor(t, s, req)
	for _, r := range second.Resources {
		if r.Kind == KindCredential && r.Action != ActionUnchanged {
			t.Errorf("credential %s = %q, want unchanged on a resume", r.ID, r.Action)
		}
	}
	if _, err := s.Apply(context.Background(), req, second, &out, &out); err != nil {
		t.Fatalf("the resume must complete: %v", err)
	}
	for _, c := range f.Calls[before:] {
		if c[0] == "secret" && c[1] != "ls" {
			t.Errorf("a verified credential was reapplied: %v", c)
		}
	}
	receipt, err := source.LoadReceipt(denHome, "dg")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != source.StatusReady {
		t.Errorf("receipt = %+v, want the resume to finish the convergence", receipt)
	}
}

// A repository this machine does not have is `partially_ready`: the
// installation SUCCEEDS, the nest needing it is not_ready, and den never
// clones (spec §7.2).
func TestApplySucceedsPartiallyWhenARepositoryIsMissing(t *testing.T) {
	denHome, remote, _ := serviceFixture(t)
	f := sbx.NewMachine()
	s, req, cleanup := requestFor(t, denHome, remote, t.TempDir(), f) // an empty root
	defer cleanup()

	plan := planFor(t, s, req)
	if plan.Status != source.StatusPartiallyReady {
		t.Fatalf("status = %q, want partially_ready", plan.Status)
	}
	var out strings.Builder
	result, err := s.Apply(context.Background(), req, plan, &out, &out)
	if err != nil {
		t.Fatalf("a missing working repository must not fail the installation: %v", err)
	}
	if result.Status != source.StatusPartiallyReady || !Succeeds(result.Status) {
		t.Errorf("result = %+v", result)
	}
	receipt, err := source.LoadReceipt(denHome, "dg")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Nests["leo"].Status != source.NestNotReady {
		t.Errorf("receipt nests = %+v", receipt.Nests)
	}
	if len(receipt.Nests["leo"].MissingRepos) != 1 || receipt.Nests["leo"].MissingRepos[0] != "api" {
		t.Errorf("receipt leo = %+v, want the missing key named", receipt.Nests["leo"])
	}
}

// An existing global configuration is preserved byte for byte: `den init
// --source` on a working den configures a source, it does not rewrite the
// user's settings (spec §8).
func TestApplyNeverRewritesAnExistingGlobalConfig(t *testing.T) {
	denHome, remote, root := serviceFixture(t)
	existing := "agents:\n  claude:\n    update: \"claude update\"\n# a comment the user wrote\n"
	writeFile(t, filepath.Join(denHome, "config.yaml"), existing)

	f := sbx.NewMachine()
	s, req, cleanup := requestFor(t, denHome, remote, root, f)
	defer cleanup()
	req.FreshGlobalConfig = []byte("agents: {}\n")

	plan := planFor(t, s, req)
	var out strings.Builder
	if _, err := s.Apply(context.Background(), req, plan, &out, &out); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(denHome, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != existing {
		t.Errorf("the global configuration was rewritten:\n%s", got)
	}
}

// A repository the user already mapped by hand keeps the path they WROTE: a
// configure that expanded "~/Development/api" into an absolute path would
// rewrite a file the user owns.
func TestApplyPreservesHandWrittenRepoPaths(t *testing.T) {
	denHome, remote, root := serviceFixture(t)
	if err := source.WritePersonal(denHome, "dg", source.Personal{
		Version: "1.0.0",
		Repos:   map[string]string{"other": "~/Development/other"},
	}); err != nil {
		t.Fatal(err)
	}
	f := sbx.NewMachine()
	s, req, cleanup := requestFor(t, denHome, remote, root, f)
	defer cleanup()

	plan := planFor(t, s, req)
	var out strings.Builder
	if _, err := s.Apply(context.Background(), req, plan, &out, &out); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	personal, err := source.LoadPersonal(denHome, "dg")
	if err != nil {
		t.Fatal(err)
	}
	if personal.Repos["other"] != "~/Development/other" {
		t.Errorf("repos = %#v, expected the hand-written entry untouched", personal.Repos)
	}
	if personal.Repos["api"] == "" {
		t.Errorf("repos = %#v, expected the discovered mapping added", personal.Repos)
	}
}

// publishVersion rewrites the remote's manifest at a new version, adding one
// host to the egress the source's builds need — so an update really has a
// resource to converge, the way a real version bump does.
func publishVersion(t *testing.T, remote, version, extraHost string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(remote, source.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(raw), "version: 1.0.0", "version: "+version, 1)
	updated = strings.Replace(updated, "      - cdn.example.test",
		"      - cdn.example.test\n      - "+extraHost, 1)
	writeFile(t, filepath.Join(remote, source.ManifestFile), updated)
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.email=t@example.test", "-c", "user.name=t", "commit", "-q", "-m", "publish " + version},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = remote
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// An update moves the checkout to the CONFIRMED commit before applying, and
// makes the new version active only once every resource verifies.
//
// The two halves of that sentence are the whole safety property of spec §11.3:
// between them, the machine holds a checkout at the new version whose resources
// are half applied — and RequireUsable must refuse there, or a spawn would mix
// the new catalogue with the old infrastructure.
func TestUpdateActivatesTheNewVersionOnlyAfterEveryResourceVerifies(t *testing.T) {
	denHome, remote, root := serviceFixture(t)
	f := sbx.NewMachine()
	s, req, cleanup := requestFor(t, denHome, remote, root, f)
	if _, err := s.Apply(context.Background(), req, planFor(t, s, req),
		&strings.Builder{}, &strings.Builder{}); err != nil {
		t.Fatalf("installing 1.0.0: %v", err)
	}
	cleanup()
	installed := source.Dir(denHome, "dg")
	before := gitOutput(t, installed, "rev-parse", "HEAD")

	publishVersion(t, remote, "2.0.0", "new.example.test")
	f.Fail["policy allow network new.example.test"] = errors.New("sbx refused")

	c, err := source.FetchCandidate(context.Background(), s.Git, denHome, "dg")
	if err != nil {
		t.Fatalf("FetchCandidate: %v", err)
	}
	defer c.Close()
	update := req
	update.Mode = ModeUpdate
	update.Candidate = c

	plan := planFor(t, s, update)
	if plan.Version != "2.0.0" {
		t.Fatalf("the plan is not computed from the fetched content: %+v", plan)
	}
	var out strings.Builder
	if _, err := s.Apply(context.Background(), update, plan, &out, &out); err == nil {
		t.Fatal("expected the failing policy to stop the update")
	}

	// The checkout IS at the new version — the builds of 2.0.0 run from it.
	if got := gitOutput(t, installed, "rev-parse", "HEAD"); got == before {
		t.Errorf("the checkout was never fast-forwarded (still %s)", before)
	}
	if got := gitOutput(t, installed, "rev-parse", "HEAD"); got != c.Commit {
		t.Errorf("HEAD = %s, want the confirmed commit %s", got, c.Commit)
	}
	// But the ACTIVE version is still the one whose resources are applied.
	personal, err := source.LoadPersonal(denHome, "dg")
	if err != nil {
		t.Fatal(err)
	}
	if personal.Version != "1.0.0" {
		t.Errorf("personal version = %q, want the previous one until the new one converges", personal.Version)
	}
	receipt, err := source.LoadReceipt(denHome, "dg")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != source.StatusApplying || receipt.TargetVersion != "2.0.0" {
		t.Errorf("receipt = %+v, want an applying marker naming 2.0.0", receipt)
	}
	if _, err := source.RequireUsable(denHome, "dg"); err == nil {
		t.Error("a half-converged source must not be usable")
	}

	// And `den source configure` finishes it, from the INSTALLED checkout — no
	// fetch, no reapplication of what already verified.
	delete(f.Fail, "policy allow network new.example.test")
	resume, err := source.InstalledCandidate(context.Background(), s.Git, denHome, "dg")
	if err != nil {
		t.Fatal(err)
	}
	defer resume.Close()
	finish := update
	finish.Mode, finish.Candidate = ModeConfigure, resume
	if _, err := s.Apply(context.Background(), finish, planFor(t, s, finish), &out, &out); err != nil {
		t.Fatalf("the resume must complete: %v", err)
	}
	if personal, err = source.LoadPersonal(denHome, "dg"); err != nil || personal.Version != "2.0.0" {
		t.Errorf("personal = %+v (%v), want the new version active after the resume", personal, err)
	}
	if _, err := source.RequireUsable(denHome, "dg"); err != nil {
		t.Errorf("the converged source is not usable: %v", err)
	}
}

// gitOutput runs git for ASSERTIONS only.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Status is the read-only view of an installed source (spec §12.1). It reads
// the machine and the den home, and NOTHING else — the remote most of all: the
// test deletes it before asking, so any fetch would fail the call.
func TestStatusObservesWithoutContactingTheRemote(t *testing.T) {
	denHome, remote, root := serviceFixture(t)
	f := sbx.NewMachine()
	s, req, cleanup := requestFor(t, denHome, remote, root, f)
	if _, err := s.Apply(context.Background(), req, planFor(t, s, req),
		&strings.Builder{}, &strings.Builder{}); err != nil {
		t.Fatalf("installing: %v", err)
	}
	cleanup()
	if err := os.RemoveAll(remote); err != nil {
		t.Fatal(err)
	}

	before := len(f.Calls)
	status, err := s.Status(context.Background(), denHome, "dg")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Status != source.StatusReady {
		t.Errorf("status = %q, want ready:\n%+v", status.Status, status)
	}
	if len(status.Resources) != len(planFor(t, s, req).Resources) {
		t.Errorf("resources = %+v, want every declared resource reported", status.Resources)
	}
	for _, r := range status.Resources {
		if r.Action != ActionUnchanged {
			t.Errorf("resource %s = %q: a converged machine has nothing to do", r.ID, r.Action)
		}
	}
	for _, c := range f.Calls[before:] {
		if len(c) > 1 && c[1] != "ls" {
			t.Errorf("status ran a mutating sbx command: %v", c)
		}
	}
}

// A source this machine holds in a half-converged state is BLOCKED, and the
// status carries the same sentence RequireUsable refuses spawns with.
func TestStatusReportsADivergenceAsBlocked(t *testing.T) {
	denHome, remote, root := serviceFixture(t)
	f := sbx.NewMachine()
	s, req, cleanup := requestFor(t, denHome, remote, root, f)
	if _, err := s.Apply(context.Background(), req, planFor(t, s, req),
		&strings.Builder{}, &strings.Builder{}); err != nil {
		t.Fatalf("installing: %v", err)
	}
	cleanup()
	// The state an interrupted update leaves: the checkout is at 1.0.0 while
	// this machine is configured for something else.
	if err := source.WritePersonal(denHome, "dg", source.Personal{Version: "0.9.0"}); err != nil {
		t.Fatal(err)
	}

	status, err := s.Status(context.Background(), denHome, "dg")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Status != source.StatusBlocked {
		t.Errorf("status = %q, want blocked", status.Status)
	}
	if len(status.Warnings) == 0 || !strings.Contains(status.Warnings[0], "0.9.0") {
		t.Errorf("warnings = %v, want the divergence named", status.Warnings)
	}
}

// A machine den cannot observe is `unknown`, never "nothing is configured":
// the two have different remedies and a user acts on the word (spec §12.2).
func TestStatusReportsUnknownWhenTheMachineCannotBeObserved(t *testing.T) {
	denHome, remote, root := serviceFixture(t)
	f := sbx.NewMachine()
	s, req, cleanup := requestFor(t, denHome, remote, root, f)
	if _, err := s.Apply(context.Background(), req, planFor(t, s, req),
		&strings.Builder{}, &strings.Builder{}); err != nil {
		t.Fatalf("installing: %v", err)
	}
	cleanup()
	f.Fail["secret ls -g"] = errors.New("keychain access denied")

	status, err := s.Status(context.Background(), denHome, "dg")
	if err != nil {
		t.Fatalf("an unobservable machine is a state, not an error: %v", err)
	}
	if status.Status != source.StatusUnknown {
		t.Errorf("status = %q, want unknown", status.Status)
	}
	var rendered strings.Builder
	RenderStatus(&rendered, status)
	if strings.Contains(rendered.String(), "absent") {
		t.Errorf("an unobserved resource was rendered as absent:\n%s", rendered.String())
	}
}

// A legacy source has no contract to report on, and the refusal says which
// command does apply to it.
func TestStatusRefusesALegacySource(t *testing.T) {
	denHome := t.TempDir()
	tree := t.TempDir()
	writeFile(t, filepath.Join(tree, "stacks", "devx", "stack.yaml"), "image: devx:v1\n")
	remote := sourceRemote(t, tree)
	s := Service{Git: worktree.NewGit(), Now: testClock}
	if _, err := source.Add(context.Background(), s.Git, denHome, remote, "corp"); err != nil {
		t.Fatal(err)
	}

	_, err := s.Status(context.Background(), denHome, "corp")
	if err == nil || !strings.Contains(err.Error(), source.ManifestFile) {
		t.Fatalf("error = %v, want a refusal naming the missing contract", err)
	}
}
