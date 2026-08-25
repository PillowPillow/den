package spawn

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/sbx"
)

// nestWithResources rewrites the harness's `api` nest with a `resources:`
// block, keeping everything else denTest declared.
func nestWithResources(t *testing.T, denHome, repo, block string) {
	t.Helper()
	write(t, filepath.Join(denHome, "nests", "api.yaml"),
		"stack: devx\nrepos:\n  - { path: "+repo+" }\n"+block)
}

// flagValue returns the argument following flag in argv, or "" if the flag is
// absent — the shape every resource assertion below needs.
func flagValue(argv []string, flag string) string {
	i := slices.Index(argv, flag)
	if i < 0 || i+1 >= len(argv) {
		return ""
	}
	return argv[i+1]
}

func TestSpawnSendsTheResolvedResources(t *testing.T) {
	denHome, repo := denTest(t)
	nestWithResources(t, denHome, repo, "resources:\n  cpus: 4\n  memory: 8g\n")
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	create := callStartingWith(f, "create")
	if got := flagValue(create, "--cpus"); got != "4" {
		t.Errorf("--cpus = %q, expected 4; argv = %v", got, create)
	}
	if got := flagValue(create, "--memory"); got != "8g" {
		t.Errorf("--memory = %q, expected 8g; argv = %v", got, create)
	}
}

// A den declaring no `resources:` must send the argv it sent before the field
// existed: absent is ABSENT, and in particular `--cpus 0` is never invented.
func TestSpawnSendsNoResourceFlagsWhenNoneDeclared(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	create := callStartingWith(f, "create")
	for _, flag := range []string{"--cpus", "--memory"} {
		if slices.Contains(create, flag) {
			t.Errorf("%s emitted with nothing declared; argv = %v", flag, create)
		}
	}
}

// The refusal spec §6 places before the first side effect. sbx rejects a
// memory below 1 GiB SERVER-side, after `✓ image ready` (measured 2026-08-24):
// relayed, it would cost an image pull AND leave the worktree den had already
// created with nothing mounting it.
func TestSpawnRefusesTooSmallAMemoryBeforeCreatingAnything(t *testing.T) {
	denHome, repo := denTest(t)
	nestWithResources(t, denHome, repo, "resources:\n  memory: 512m\n")
	f, d := fakeDeps()

	err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat12"}, d)
	if err == nil {
		t.Fatal("expected a refusal on a memory below sbx's minimum")
	}
	if !strings.Contains(err.Error(), "1 GiB") {
		t.Errorf("error = %q, expected it to state sbx's minimum", err)
	}
	// The file to fix, by name: the nest is where the value was written.
	if !strings.Contains(err.Error(), filepath.Join(denHome, "nests", "api.yaml")) {
		t.Errorf("error = %q, expected it to name the nest file", err)
	}
	if !createdNothing(f) {
		t.Errorf("a refusal must create nothing; calls: %v", f.Calls)
	}
	wt := filepath.Join(denHome, "worktrees", "feat12", "api")
	if _, err := os.Stat(wt); err == nil {
		t.Errorf("%s was created before the refusal — an orphaned worktree", wt)
	}
}

// The creation record carries the size, because nothing else can: `sbx ls
// --json` has no resources field, so the manifest is the only reference a
// later attach can compare against.
func TestSpawnRecordsTheResourcesItAskedFor(t *testing.T) {
	denHome, repo := denTest(t)
	nestWithResources(t, denHome, repo, "resources:\n  cpus: 4\n  memory: 8g\n")
	_, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m, err := manifest.Read(denHome, "api")
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	if m.Resources == nil {
		t.Fatal("the record carries no resources block")
	}
	if m.Resources.CPUs == nil || *m.Resources.CPUs != 4 || m.Resources.Memory != "8g" {
		t.Errorf("recorded resources = %+v, expected 4 / 8g", m.Resources)
	}
}

// A spawn that declared nothing writes NO block, rather than one of zero
// values that would read like a size someone chose — and would then report
// drift the day a nest declares its first `resources:`... which is exactly
// what it should do, but by comparing against a real absence, not a fake zero.
func TestSpawnRecordsNoResourcesWhenNoneDeclared(t *testing.T) {
	denHome, _ := denTest(t)
	_, d := fakeDeps()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m, err := manifest.Read(denHome, "api")
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	if m.Resources != nil {
		t.Errorf("resources recorded with nothing declared: %+v", m.Resources)
	}
}

// liveAfterCreate makes the fake report the sandbox this den home has just
// created as running, so a second Spawn takes the ATTACH branch.
func liveAfterCreate(f *sbx.Fake, name, workspace string) {
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"` + name + `","status":"running","workspaces":["` +
			workspace + `"]}]}`),
	}
}

// The attach branch reapplies NOTHING to a live VM (spec §6): a `resources:`
// edited since creation is warned about, never re-sent and never a cause to
// recreate.
func TestSpawnWarnsWhenResourcesChangedSinceCreation(t *testing.T) {
	denHome, repo := denTest(t)
	nestWithResources(t, denHome, repo, "resources:\n  cpus: 4\n  memory: 8g\n")
	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("first spawn: %v", err)
	}

	// The nest grows: 8 CPUs and 16 GiB from now on.
	nestWithResources(t, denHome, repo, "resources:\n  cpus: 8\n  memory: 16g\n")
	liveAfterCreate(f, "api", repo)
	out.Reset()
	f.Calls = nil

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("second spawn: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "cpus") || !strings.Contains(got, "memory") {
		t.Errorf("both changed fields must be named; output:\n%s", got)
	}
	// The number it RUNS WITH and the number the configuration asks for: a
	// warning that gave only one would leave the reader to guess which.
	for _, want := range []string{"4", "8", "8g", "16g"} {
		if !strings.Contains(got, want) {
			t.Errorf("output must carry %q; output:\n%s", want, got)
		}
	}
	if callStartingWith(f, "create") != nil {
		t.Errorf("den recreated a live sandbox; calls: %v", f.Calls)
	}
}

// The counterpart that keeps the warning from becoming permanent noise: an
// unchanged `resources:` says nothing. A warning that fires on every attach
// stops being read, including the day it tells the truth.
func TestSpawnSaysNothingWhenResourcesAreUnchanged(t *testing.T) {
	denHome, repo := denTest(t)
	nestWithResources(t, denHome, repo, "resources:\n  cpus: 4\n  memory: 8g\n")
	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	liveAfterCreate(f, "api", repo)
	out.Reset()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("second spawn: %v", err)
	}
	if strings.Contains(out.String(), "cpus") || strings.Contains(out.String(), "memory") {
		t.Errorf("no size warning on an unchanged nest; output:\n%s", out.String())
	}
}

// A REFORMATTED size is not drift: `8g` and `8192m` are the same number of
// bytes, so the VM is the same VM and there is nothing to fix. Warning here
// would fire on every attach until the user destroyed a sandbox that was never
// wrong — the permanent warning that stops being read.
func TestSpawnSaysNothingWhenTheMemoryIsOnlyRespelled(t *testing.T) {
	denHome, repo := denTest(t)
	nestWithResources(t, denHome, repo, "resources:\n  memory: 8g\n")
	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	nestWithResources(t, denHome, repo, "resources:\n  memory: 8192m\n")
	liveAfterCreate(f, "api", repo)
	out.Reset()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("second spawn: %v", err)
	}
	if strings.Contains(out.String(), "memory") {
		t.Errorf("8g and 8192m are the same size; output:\n%s", out.String())
	}
}

// A sandbox created BEFORE `resources:` existed records none. Declaring one
// now is a real divergence — the VM keeps the size sbx chose for it — and
// saying so is the whole point of the warning.
func TestSpawnWarnsWhenResourcesAppearOnALiveSandbox(t *testing.T) {
	denHome, repo := denTest(t)
	f, d := fakeDeps()
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	nestWithResources(t, denHome, repo, "resources:\n  memory: 16g\n")
	liveAfterCreate(f, "api", repo)
	out.Reset()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("second spawn: %v", err)
	}
	if !strings.Contains(out.String(), "16g") {
		t.Errorf("the newly declared size must be reported; output:\n%s", out.String())
	}
}
