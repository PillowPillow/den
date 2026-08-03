# `den build` owns the sequence — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the build sequence into den — `sbx create` → N × `sbx exec` → `sbx stop` → `sbx template save` → `sbx rm` — so the image name comes from `image:` by construction, and delete the per-stack `build.sh`.

**Architecture:** `internal/build` stops spawning processes. The graph (`graph.go`) and the skip arbitration (`plan.go`) survive; `execute.go` is rewritten against `sbx.Runner`, and `script.go` is deleted. A stack declares `provision.includes` (text concatenated ahead of *every* step) and `provision.steps` (one `sbx exec` each), plus `base:` for root stacks. `internal/spawn` stops importing `internal/build`.

**Tech Stack:** Go · cobra · `yaml.v3` strict (`KnownFields(true)`) · plain `testing`, no assertion library · `sbx.Fake` as the sole test double.

## Global Constraints

- Spec is `docs/superpowers/specs/2026-07-27-den-cli-design.md` §6 (amended 2026-08-03, commit `b78c9e0`). It wins on intent.
- Code, comments and user-facing messages are **English**. The spec and handoffs are French.
- Comment density: a long "why" comment at each decision site — what was rejected, what regression the choice prevents. Terse code visibly does not match the surroundings.
- Errors name the file to fix and the remedy. den refuses rather than normalizing in silence (spec §2).
- **No test calls `t.Parallel()`, opens a socket, or spawns a process.** This is load-bearing, and this plan's whole point is that `internal/build` finally satisfies it.
- Strict YAML everywhere: an unknown key is a load error.
- Goldens live in `internal/*/testdata/*.golden`, compared by hand. **There is no `-update` flag** — edit them manually.
- `make test` is `go test -count=1 ./...`. Plain `go test` can pass stale.
- `make lint` runs `go vet ./...` and enforces `gofmt`. Run it before every commit.
- Never work on `main`. This plan runs on a feature branch.

---

### Task 1: `config.Stack` learns `base:` and `provision:`

**Files:**
- Modify: `internal/config/stack.go:14-28` (the `Stack` struct), `:74-84` (post-decode resolution)
- Test: `internal/config/stack_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `config.Provision{Includes, Steps []string}`, field `Stack.Base string`, field `Stack.Provision Provision`, method `(*Stack) Buildable() bool`. Every later task depends on these exact names.

- [ ] **Step 1: Write the failing tests**

In `internal/config/stack_test.go`:

```go
// Buildability is read off `provision.steps` and nothing else. It is the SOLE
// source of the verdict, consumed by `den build` and by the spawn's image
// check, which spec §6 requires to agree.
func TestStackBuildableFollowsProvisionSteps(t *testing.T) {
	var pullable Stack
	if pullable.Buildable() {
		t.Error("a stack with no provision.steps is not buildable — its image: is one sbx pulls")
	}
	buildable := Stack{Provision: Provision{Steps: []string{"./provision/go.sh"}}}
	if !buildable.Buildable() {
		t.Error("a stack declaring provision.steps is buildable")
	}
}

// The paths follow the rule `kit`/`kits` already follow: relative in YAML,
// absolute after loading, resolved against the STACK directory — not against
// the directory the user happened to run den from.
func TestLoadStackResolvesProvisionPathsAgainstTheStackDirectory(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "stacks", "devx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "stack.yaml"), `
image: devx:v1
base: claude
provision:
  includes:
    - ../../lib/common.sh
  steps:
    - ./provision/go-tools.sh
`)
	s, err := LoadStack(home, "devx")
	if err != nil {
		t.Fatalf("LoadStack: %v", err)
	}
	wantInclude := filepath.Join(home, "lib", "common.sh")
	if got := s.Provision.Includes[0]; got != wantInclude {
		t.Errorf("includes[0] = %q, want %q", got, wantInclude)
	}
	wantStep := filepath.Join(dir, "provision", "go-tools.sh")
	if got := s.Provision.Steps[0]; got != wantStep {
		t.Errorf("steps[0] = %q, want %q", got, wantStep)
	}
}

// Two origins for one image is a contradiction, not a precedence to arbitrate:
// den refuses rather than silently preferring one.
func TestLoadStackRefusesBaseAndParentTogether(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "stacks", "devx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "stack.yaml"), `
image: devx:v1
base: claude
parent: other
provision:
  steps: [./provision/go.sh]
`)
	_, err := LoadStack(home, "devx")
	if err == nil {
		t.Fatal("LoadStack accepted both base: and parent: — want a refusal")
	}
	for _, want := range []string{"base:", "parent:", "stack.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q — it must name the fault and the file", err, want)
		}
	}
}

// A buildable stack with NO origin cannot be built: den does not know what to
// start from. The message offers both remedies, because which one is right
// depends on whether the stack is a root.
func TestLoadStackRefusesBuildableStackWithNoOrigin(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "stacks", "devx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "stack.yaml"), `
image: devx:v1
provision:
  steps: [./provision/go.sh]
`)
	_, err := LoadStack(home, "devx")
	if err == nil {
		t.Fatal("LoadStack accepted provision.steps with neither base: nor parent:")
	}
	for _, want := range []string{"base:", "parent:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not offer the remedy %q", err, want)
		}
	}
}

// The mirror case, and the one a naive validation breaks: a stack with no
// provision.steps declares no origin BY DESIGN — its image: is one sbx pulls.
// Demanding an origin of it would refuse a configuration §6 calls legitimate.
func TestLoadStackAcceptsAPullableStackWithNoOrigin(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "stacks", "pulled")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "stack.yaml"), "image: ghcr.io/acme/base:v3\n")
	s, err := LoadStack(home, "pulled")
	if err != nil {
		t.Fatalf("LoadStack refused a pullable stack: %v", err)
	}
	if s.Buildable() {
		t.Error("a stack with no provision.steps must not be buildable")
	}
}
```

If `write` does not already exist in this package's tests, add it:

```go
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestStackBuildable|TestLoadStackResolvesProvision|TestLoadStackRefuses|TestLoadStackAcceptsAPullable' -count=1`
Expected: FAIL — `undefined: Provision`, `s.Buildable undefined`.

- [ ] **Step 3: Add the schema, the verdict and the validation**

In `internal/config/stack.go`, add above `type Stack`:

```go
// Provision is what den plays INSIDE the build VM (spec §6). den owns the
// sequence around it; a stack only says what to run.
type Provision struct {
	// Includes is concatenated ahead of EVERY step, and is never a step of its
	// own. Order is significant. Relative to the stack directory in YAML,
	// absolute after loading.
	//
	// The contract, which den cannot verify and the spec therefore states: it
	// DEFINES, it does not act. Each `sbx exec` opens a fresh shell, so this
	// text is re-emitted once per step — a side effect here runs N times, in
	// silence.
	Includes []string `yaml:"includes"`
	// Steps is one `sbx exec` per entry, in order. Relative to the stack
	// directory in YAML, absolute after loading.
	//
	// One exec per entry and not one big payload: it is what lets a failure
	// name the step that produced it. The price is that nothing but the VM
	// filesystem survives from one step to the next — hence Includes.
	Steps []string `yaml:"steps"`
}
```

Add the two fields to `Stack`, right after `Parent`:

```go
	// Base is the sbx AGENT positional, and applies to ROOT stacks only — the
	// ones with no `parent:`, which therefore get no `--template`. There the
	// positional is load-bearing: it selects the starting image
	// (`claude` → docker/sandbox-templates:claude-code-docker). With a
	// `--template`, it is the image's flavor that decides and den passes
	// `sbx.PositionalAgent` instead.
	Base      string    `yaml:"base"`
	Provision Provision `yaml:"provision"`
```

Add the verdict, after `DeclaredKits`:

```go
// Buildable reports whether den knows how to build this stack's image.
//
// SOLE SOURCE of that verdict, and it is read on BOTH sides: `den build` skips
// a stack that is not buildable, and the spawn's image check stays silent on
// it (internal/spawn, checkStackImage). Spec §6 requires the two to agree —
// measured on 2026-08-03, they had already drifted once, and a `den build` on
// a den holding a single pullable stack built NOTHING while demanding a script
// for the stack den had already decided not to build.
func (s *Stack) Buildable() bool { return len(s.Provision.Steps) > 0 }
```

In `LoadStack`, replace the `Kits` resolution loop with a shared helper and add the two new lists plus the validation:

```go
	s.Name = name // the directory is authoritative, unconditionally
	s.Dir = dir
	if s.Kit != "" && !filepath.IsAbs(s.Kit) {
		s.Kit = filepath.Join(dir, s.Kit)
	}
	resolveAgainst(dir, s.Kits)
	resolveAgainst(dir, s.Provision.Includes)
	resolveAgainst(dir, s.Provision.Steps)

	// Checked only for a stack den would actually BUILD. A pullable stack
	// declares no origin by design, and demanding one of it would refuse the
	// configuration spec §6 calls legitimate.
	if s.Buildable() {
		switch {
		case s.Base != "" && s.Parent != "":
			return nil, fmt.Errorf(
				"stack %q: `base:` and `parent:` are both set in %s — a stack has ONE origin: "+
					"`parent:` builds FROM another stack's image, `base:` starts from an sbx agent. "+
					"Two origins for one image is a contradiction, not a precedence den can arbitrate",
				name, path)
		case s.Base == "" && s.Parent == "":
			return nil, fmt.Errorf(
				"stack %q: `provision.steps` is declared but neither `base:` nor `parent:` is, in %s — "+
					"den does not know what to build FROM. Add `base: claude` for a root stack, "+
					"or `parent: <stack>` to derive from another",
				name, path)
		}
	}
	return &s, nil
}

// resolveAgainst rewrites the relative entries of a path list against dir, IN
// PLACE. Sole definition of the rule stated once in §4.2 and applying to
// `kits`, `provision.includes` and `provision.steps` alike: a blank entry is
// left alone rather than turned into the stack directory itself.
func resolveAgainst(dir string, paths []string) {
	for i, p := range paths {
		if p != "" && !filepath.IsAbs(p) {
			paths[i] = filepath.Join(dir, p)
		}
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/ -count=1`
Expected: PASS, including the pre-existing `kits` resolution tests — `resolveAgainst` must not have changed their behaviour.

- [ ] **Step 5: Commit**

```bash
make lint && make test
git add internal/config/stack.go internal/config/stack_test.go
git commit -m "feat(config): a stack declares base: and provision:"
```

---

### Task 2: `internal/spawn` stops importing `internal/build`

**Files:**
- Modify: `internal/spawn/spawn.go:813-817` (`checkStackImage`) and its import block
- Test: `internal/spawn/spawn_test.go`

**Interfaces:**
- Consumes: `(*config.Stack).Buildable()` from Task 1.
- Produces: nothing new. Removes the `internal/spawn` → `internal/build` edge, which is what lets Task 7 delete `script.go`.

- [ ] **Step 1: Write the failing test**

In `internal/spawn/spawn_test.go`:

```go
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
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/spawn/ -run TestSpawnDoesNotCheckTheImageOfAPullableStack -count=1`
Expected: FAIL — the current code stats `build.ScriptPath(s)`, which does not exist for this stack, so it returns nil early *by accident* and the assertion on `template ls` may pass while the code still imports `internal/build`. **If it passes, it is passing for the wrong reason** — proceed to Step 3 anyway and let Step 4 prove the import is gone.

- [ ] **Step 3: Switch the verdict and drop the import**

In `internal/spawn/spawn.go`, replace the opening of `checkStackImage`:

```go
func checkStackImage(ctx context.Context, d Deps, s *config.Stack) error {
	// Buildability comes from config, which is the SOLE source of the verdict
	// (config.Stack.Buildable). It used to be a `os.Stat` on the stack's
	// build.sh, from internal/build — an edge that existed only to answer this
	// one question, and that made the spawn depend on the build package for a
	// file test. Spec §6 requires this silence and `den build`'s skip to agree;
	// reading the same method is what makes that structural.
	if !s.Buildable() {
		return nil
	}
```

Update the doc comment above it — the first of the three silences now reads:

```go
//   - A stack that is NOT BUILDABLE (no `provision.steps`) is left alone.
//     `image:` may name a registry image sbx will happily pull, and den has no
//     remedy to offer for it — `den build` on a stack den cannot build is not
//     advice, it is a second error. Refusing there would turn a working
//     `den <nest>` into a stop.
```

Remove `"github.com/PillowPillow/den/internal/build"` from the import block. Remove `"os"` too **only if** nothing else in the file uses it — check with `grep -n 'os\.' internal/spawn/spawn.go` before deleting.

- [ ] **Step 4: Run the tests and prove the edge is gone**

```bash
go test ./internal/spawn/ -count=1
go list -deps ./internal/spawn | grep 'den/internal/build' && echo "STILL IMPORTED — fix it" || echo "edge removed"
```
Expected: PASS, then `edge removed`.

- [ ] **Step 5: Commit**

```bash
make lint && make test
git add internal/spawn/spawn.go internal/spawn/spawn_test.go
git commit -m "refactor(spawn): buildability comes from config, not from a file test"
```

---

### Task 3: `build.ReadProvisioning` — read host-side, compose per step

**Files:**
- Create: `internal/build/provision.go`
- Test: `internal/build/provision_test.go`

**Interfaces:**
- Consumes: `config.Stack.Provision` from Task 1.
- Produces: `type StepScript struct{ Path, Body string }`, `type Provisioning struct{ Includes string; Steps []StepScript }`, `func ReadProvisioning(s *config.Stack) (Provisioning, error)`, `func (Provisioning) Payload(i int) string`. Task 6 calls both.

- [ ] **Step 1: Write the failing tests**

In `internal/build/provision_test.go`:

```go
// The whole semantics of `includes` in one assertion: its text comes back
// ahead of EVERY step, not just the first. A payload built once and reused
// would look identical on step 1 and be wrong on step 2.
func TestPayloadRepeatsTheIncludesForEveryStep(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "common.sh"), "common::gh() { :; }\n")
	writeFile(t, filepath.Join(dir, "one.sh"), "common::gh\n")
	writeFile(t, filepath.Join(dir, "two.sh"), "echo two\n")

	p, err := ReadProvisioning(&config.Stack{
		Name: "devx", Dir: dir,
		Provision: config.Provision{
			Includes: []string{filepath.Join(dir, "common.sh")},
			Steps:    []string{filepath.Join(dir, "one.sh"), filepath.Join(dir, "two.sh")},
		},
	})
	if err != nil {
		t.Fatalf("ReadProvisioning: %v", err)
	}
	for i, wantTail := range []string{"common::gh\n", "echo two\n"} {
		got := p.Payload(i)
		if !strings.HasPrefix(got, "common::gh() { :; }\n") {
			t.Errorf("payload %d does not start with the includes:\n%s", i, got)
		}
		if !strings.HasSuffix(got, wantTail) {
			t.Errorf("payload %d does not end with its own step:\n%s", i, got)
		}
	}
}

// No includes is the normal case for a stack whose steps are self-contained.
// The payload is then the step, verbatim — not a step with a stray leading
// newline, which would shift every line number a shell error reports.
func TestPayloadWithoutIncludesIsTheStepVerbatim(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "one.sh"), "echo one\n")

	p, err := ReadProvisioning(&config.Stack{
		Name: "devx", Dir: dir,
		Provision: config.Provision{Steps: []string{filepath.Join(dir, "one.sh")}},
	})
	if err != nil {
		t.Fatalf("ReadProvisioning: %v", err)
	}
	if got := p.Payload(0); got != "echo one\n" {
		t.Errorf("Payload(0) = %q, want the step verbatim", got)
	}
}

// A file that is not there is named with its full path — the reason the read
// happens before the first `sbx create` at all (Task 6).
func TestReadProvisioningNamesTheMissingFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "provision", "gone.sh")
	_, err := ReadProvisioning(&config.Stack{
		Name: "devx", Dir: dir,
		Provision: config.Provision{Steps: []string{missing}},
	})
	if err == nil {
		t.Fatal("ReadProvisioning accepted a missing step")
	}
	for _, want := range []string{"devx", missing} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// Order is significant on BOTH lists, and a Go map would not preserve it.
func TestReadProvisioningKeepsDeclarationOrder(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.sh", "b.sh", "c.sh"} {
		writeFile(t, filepath.Join(dir, n), "echo "+n+"\n")
	}
	p, err := ReadProvisioning(&config.Stack{
		Name: "devx", Dir: dir,
		Provision: config.Provision{Steps: []string{
			filepath.Join(dir, "c.sh"), filepath.Join(dir, "a.sh"), filepath.Join(dir, "b.sh"),
		}},
	})
	if err != nil {
		t.Fatalf("ReadProvisioning: %v", err)
	}
	for i, want := range []string{"c.sh", "a.sh", "b.sh"} {
		if filepath.Base(p.Steps[i].Path) != want {
			t.Errorf("step %d = %s, want %s — declaration order is the execution order",
				i, filepath.Base(p.Steps[i].Path), want)
		}
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/build/ -run 'TestPayload|TestReadProvisioning' -count=1`
Expected: FAIL — `undefined: ReadProvisioning`.

- [ ] **Step 3: Write the implementation**

Create `internal/build/provision.go`:

```go
package build

import (
	"fmt"
	"os"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// StepScript is one entry of `provision.steps`: its path, kept for the failure
// message, and its text.
type StepScript struct {
	Path string
	Body string
}

// Provisioning is the TEXT of one stack's provision files, read on the HOST.
//
// Read as a whole, before anything is created: a build that discovers a
// missing step after four minutes of base image has spent that time to reach a
// refusal den could have made instantly. Nothing is read twice — den needs the
// text anyway to compose the payloads.
type Provisioning struct {
	// Includes is every `provision.includes` file concatenated in order, or
	// empty when none is declared.
	Includes string
	Steps    []StepScript
}

// Payload is what step i is sent as, and it is the whole of `includes`'
// semantics: the includes text, then the step.
//
// CONCATENATION, not a `source`: nothing is written into the VM, the text
// travels inside the exec argv. Which is also why it is re-emitted for EVERY
// step — each `sbx exec` opens a fresh shell, so a function or a variable an
// include defines does not survive the step that saw it (spec §6).
//
// With no includes the step travels VERBATIM, with no separator prepended: a
// stray leading newline would shift every line number a shell error reports,
// which is the one thing a build log must get right.
func (p Provisioning) Payload(i int) string {
	if p.Includes == "" {
		return p.Steps[i].Body
	}
	return p.Includes + "\n" + p.Steps[i].Body
}

// ReadProvisioning reads a stack's includes and steps, in declaration order.
func ReadProvisioning(s *config.Stack) (Provisioning, error) {
	var includes strings.Builder
	for _, path := range s.Provision.Includes {
		body, err := os.ReadFile(path)
		if err != nil {
			return Provisioning{}, fmt.Errorf(
				"stack %q: unreadable `provision.includes` entry %s: %w", s.Name, path, err)
		}
		includes.Write(body)
		// A file that does not end in a newline would weld its last line onto
		// the next include's first. The separator is added here rather than
		// demanded of the user.
		if len(body) > 0 && body[len(body)-1] != '\n' {
			includes.WriteByte('\n')
		}
	}

	p := Provisioning{Includes: includes.String(), Steps: make([]StepScript, 0, len(s.Provision.Steps))}
	for _, path := range s.Provision.Steps {
		body, err := os.ReadFile(path)
		if err != nil {
			return Provisioning{}, fmt.Errorf(
				"stack %q: unreadable `provision.steps` entry %s: %w", s.Name, path, err)
		}
		p.Steps = append(p.Steps, StepScript{Path: path, Body: string(body)})
	}
	return p, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/build/ -run 'TestPayload|TestReadProvisioning' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make lint && make test
git add internal/build/provision.go internal/build/provision_test.go
git commit -m "feat(build): read provision host-side, repeat includes per step"
```

---

### Task 4: `build.Plan` arbitrates on buildability, not on a script file

**Files:**
- Modify: `internal/build/plan.go:96-106` (the `os.Stat(script)` branch), `:144-156` (`missingScriptError`)
- Test: `internal/build/plan_test.go`

**Interfaces:**
- Consumes: `(*config.Stack).Buildable()` from Task 1.
- Produces: unchanged `Plan(ctx, chain, target, force, images) ([]Step, error)` and `Step{Stack, Build, Skipped}`. `missingScriptError` is replaced by `notBuildableError(s *config.Stack) error`.

- [ ] **Step 1: Write the failing tests**

In `internal/build/plan_test.go`:

```go
// A stack den cannot build is skipped and NAMED, never a refusal — the answer
// must match the spawn's silence (Task 2). The skip line carries the reason,
// and for this cause there is no --force that would help.
func TestPlanSkipsANotBuildableStack(t *testing.T) {
	pullable := &config.Stack{Name: "pulled", Image: "ghcr.io/acme/base:v3"}
	steps, err := Plan(context.Background(), []*config.Stack{pullable}, "", false, nil)
	if err != nil {
		t.Fatalf("Plan refused a pullable stack: %v", err)
	}
	if len(steps) != 1 || steps[0].Build {
		t.Fatalf("steps = %+v, want one skipped step", steps)
	}
	if !strings.Contains(steps[0].Skipped, "provision.steps") {
		t.Errorf("skip reason %q does not name what is missing", steps[0].Skipped)
	}
}

// The one exception: the stack the user NAMED. A "skipped" line there would
// read as success for a build they asked for specifically.
func TestPlanRefusesANamedStackItCannotBuild(t *testing.T) {
	pullable := &config.Stack{Name: "pulled", Image: "ghcr.io/acme/base:v3"}
	_, err := Plan(context.Background(), []*config.Stack{pullable}, "pulled", false, nil)
	if err == nil {
		t.Fatal("Plan accepted a named stack with no provision.steps")
	}
	for _, want := range []string{"pulled", "provision.steps"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
```

Update any existing test in this file that builds a stack expected to be *buildable*: it must now carry `Provision: config.Provision{Steps: []string{...}}` and an origin, instead of a `build.sh` on disk. Search with `grep -n 'build.sh\|ScriptPath' internal/build/*_test.go`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/build/ -run TestPlan -count=1`
Expected: FAIL — the current code stats `ScriptPath(s)` and skips with a message naming `build.sh`.

- [ ] **Step 3: Swap the verdict**

In `internal/build/plan.go`, replace the `script := ScriptPath(s)` block with:

```go
		// A stack with NO `provision.steps` is not buildable, and that is a
		// DECLARED configuration rather than a mistake: its `image:` may name a
		// registry image sbx will pull, which is exactly why the spawn's own
		// image check leaves such a stack alone. Reading the same
		// config.Stack.Buildable on both sides is what keeps the two answers
		// the same — measured on 2026-08-03, they had drifted, and a `den build`
		// on a den holding one pullable base and three buildable stacks built
		// NOTHING.
		//
		// The one exception is the stack the user NAMED. That is a request den
		// must refuse rather than answer with a skip line: the user asked for
		// that build specifically, and silently doing nothing reads as success.
		if !s.Buildable() {
			if s.Name == target {
				return nil, notBuildableError(s)
			}
			steps = append(steps, Step{
				Stack:   s,
				Skipped: "no `provision.steps`, nothing for den to build",
			})
			continue
		}
```

Replace `missingScriptError` with:

```go
// notBuildableError is the refusal for a stack den is asked to build and that
// declares nothing to run.
//
// ONE definition for the two sites that produce it — Plan, on the stack the
// user NAMED, and Execute's pre-flight guard — because they answer the same
// fault, and the two copies of the previous version had already started to
// drift apart on a verb.
func notBuildableError(s *config.Stack) error {
	return fmt.Errorf(
		"stack %q: nothing to build — declare `provision.steps` in %s "+
			"(den runs each entry in the build VM, in order, and saves the result as %s)",
		s.Name, filepath.Join(s.Dir, "stack.yaml"), s.Image)
}
```

Add `"path/filepath"` to the imports; drop `"os"` if nothing else in the file uses it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/build/ -count=1`
Expected: PASS for `plan_test.go` and `graph_test.go`. `execute_test.go` will still fail — Task 6 rewrites it.

- [ ] **Step 5: Commit**

```bash
make lint
go test ./internal/build/ -run 'TestPlan|TestChain' -count=1
git add internal/build/plan.go internal/build/plan_test.go
git commit -m "feat(build): plan arbitrates on provision.steps, not on a script file"
```

---

### Task 5: the build sandbox — name, scratch, `create` argv

**Files:**
- Create: `internal/build/sandbox.go`
- Test: `internal/build/sandbox_test.go`

**Interfaces:**
- Consumes: `config.Stack.Base`, `config.Stack.Parent` from Task 1.
- Produces: `func SandboxName(stack string) string`, `func ScratchDir(denHome, stack string) string`, `func CreateArgv(s *config.Stack, parentImage, scratch string) ([]string, error)`. Task 6 calls all three.

- [ ] **Step 1: Write the failing tests**

In `internal/build/sandbox_test.go`:

```go
// A DERIVED stack starts from its parent's image, and the positional is
// `shell`: with a --template it is the image's flavor that decides the
// attached command, so `shell` promises nothing it does not keep. Same
// doctrine as sbx.PositionalAgent.
func TestCreateArgvForADerivedStack(t *testing.T) {
	s := &config.Stack{Name: "dgdevx", Image: "dgdevx:v1", Parent: "devx",
		Provision: config.Provision{Steps: []string{"/x/go.sh"}}}
	got, err := CreateArgv(s, "docker.io/library/devx:v1", "/scratch/dgdevx")
	if err != nil {
		t.Fatalf("CreateArgv: %v", err)
	}
	want := []string{"create", "--name", "dgdevx-build",
		"--template", "docker.io/library/devx:v1", "shell", "/scratch/dgdevx"}
	if !slices.Equal(got, want) {
		t.Errorf("argv =\n  %v\nwant\n  %v", got, want)
	}
}

// A ROOT stack has no --template, and THERE the positional is load-bearing:
// it selects the starting image. That is the entire reason `base:` exists.
func TestCreateArgvForARootStack(t *testing.T) {
	s := &config.Stack{Name: "devx", Image: "devx:v1", Base: "claude",
		Provision: config.Provision{Steps: []string{"/x/go.sh"}}}
	got, err := CreateArgv(s, "", "/scratch/devx")
	if err != nil {
		t.Fatalf("CreateArgv: %v", err)
	}
	want := []string{"create", "--name", "devx-build", "claude", "/scratch/devx"}
	if !slices.Equal(got, want) {
		t.Errorf("argv =\n  %v\nwant\n  %v", got, want)
	}
}

// A build sandbox gets NO mixin, NO stack kits and NO repo workspaces. It is
// thrown away at the end of the sequence, and every one of those exists to
// serve a spawn the user attaches to.
func TestCreateArgvCarriesNoSpawnMachinery(t *testing.T) {
	s := &config.Stack{Name: "devx", Image: "devx:v1", Base: "claude",
		Kit: "/k/kit", Kits: []string{"/k/known-hosts"},
		Provision: config.Provision{Steps: []string{"/x/go.sh"}}}
	got, err := CreateArgv(s, "", "/scratch/devx")
	if err != nil {
		t.Fatalf("CreateArgv: %v", err)
	}
	if slices.Contains(got, "--kit") {
		t.Errorf("argv carries a --kit: %v", got)
	}
}

// The name must survive sbx's own validation, or den would build an argv sbx
// refuses. Guarded here so a stack name legal for den but illegal as a
// sandbox component is caught before any process runs.
func TestCreateArgvRefusesAStackNameThatIsNotANameableSandbox(t *testing.T) {
	s := &config.Stack{Name: "-weird", Image: "x:v1", Base: "claude",
		Provision: config.Provision{Steps: []string{"/x/go.sh"}}}
	if _, err := CreateArgv(s, "", "/scratch/x"); err == nil {
		t.Fatal("CreateArgv accepted a stack name that cannot be a sandbox name")
	}
}

func TestScratchDirIsUnderTheReconstructibleCache(t *testing.T) {
	got := ScratchDir("/home/u/.den", "devx")
	want := filepath.Join("/home/u/.den", "cache", "build", "devx")
	if got != want {
		t.Errorf("ScratchDir = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/build/ -run 'TestCreateArgv|TestScratchDir' -count=1`
Expected: FAIL — `undefined: CreateArgv`.

- [ ] **Step 3: Write the implementation**

Create `internal/build/sandbox.go`:

```go
package build

import (
	"fmt"
	"path/filepath"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
)

// buildSuffix distinguishes the throwaway VM from a real sandbox.
//
// A `-` and not a `.`: the period is the `<nest>.<worktree>` separator, and
// `devx.build` would decompose into a worktree named "build" — a name
// `den <nest> -w build` really produces. The hyphen keeps the build sandbox a
// single component, which is what `sbx.SplitName` then reads it as.
const buildSuffix = "-build"

// SandboxName is the throwaway VM `den build` works in.
//
// NOT collision-proof, and it cannot be: the component charset (§2) makes
// `devx-build` a perfectly legal nest name. That is precisely why Execute
// REFUSES a pre-existing sandbox by this name instead of removing it — see
// there.
func SandboxName(stack string) string { return stack + buildSuffix }

// ScratchDir is the empty directory mounted into the build VM.
//
// `sbx create` requires at least one path, and mounting the host's /tmp into a
// build VM has no justification. Under `cache/`, which spec §3 already
// declares reconstructible and never a source of truth.
func ScratchDir(denHome, stack string) string {
	return filepath.Join(denHome, "cache", "build", stack)
}

// CreateArgv is the `sbx create` of a BUILD sandbox.
//
// Deliberately NOT sbx.CreateArgv, which assembles a spawn: no generated
// mixin, no stack kits, no repo workspaces. Every one of those serves a VM the
// user attaches to and keeps; this one is destroyed at step 5 of the sequence.
// Sharing the builder would have meant weakening its guards — it refuses a
// create with no mixin, and rightly so for a spawn.
//
// parentImage empty ⇒ ROOT stack: no `--template`, and the positional is
// `s.Base`, which is what selects the starting image. Non-empty ⇒ DERIVED: the
// image decides, and the positional is `sbx.PositionalAgent` for the reason
// stated there.
func CreateArgv(s *config.Stack, parentImage, scratch string) ([]string, error) {
	name := SandboxName(s.Name)
	// Guarded here rather than trusted from config.ValidateName: that one
	// accepts names sbx would reject (it only forbids separators and the two
	// reserved dots), and a build must not reach a process to learn it.
	if err := sbx.ValidateSandboxName(name); err != nil {
		return nil, fmt.Errorf("stack %q: cannot name its build sandbox: %w", s.Name, err)
	}

	argv := []string{"create", "--name", name}
	positional := s.Base
	if parentImage != "" {
		argv = append(argv, "--template", parentImage)
		positional = sbx.PositionalAgent
	}
	if positional == "" {
		// Unreachable through LoadStack, which refuses a buildable stack with
		// no origin. Kept as a BOUNDARY guard: CreateArgv is exported and takes
		// a struct anyone can fill, the doctrine sbx.CreateArgv states for its
		// own inputs.
		return nil, notBuildableOriginError(s)
	}
	return append(argv, positional, scratch), nil
}

func notBuildableOriginError(s *config.Stack) error {
	return fmt.Errorf(
		"stack %q: no origin — declare `base:` (root stack) or `parent:` in %s",
		s.Name, filepath.Join(s.Dir, "stack.yaml"))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/build/ -run 'TestCreateArgv|TestScratchDir' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make lint
go test ./internal/build/ -run 'TestCreateArgv|TestScratchDir|TestPlan|TestChain|TestPayload|TestReadProvisioning' -count=1
git add internal/build/sandbox.go internal/build/sandbox_test.go
git commit -m "feat(build): the build sandbox — name, scratch dir, create argv"
```

---

### Task 6: `build.Execute` — the sequence

**Files:**
- Rewrite: `internal/build/execute.go`
- Rewrite: `internal/build/execute_test.go`

**Interfaces:**
- Consumes: `Step` (Task 4), `ReadProvisioning`/`Payload` (Task 3), `SandboxName`/`ScratchDir`/`CreateArgv` (Task 5), `sbx.Runner`, `sbx.Ls`, `sbx.Find`, `sbx.Fake`.
- Produces: `type Deps struct{ Sbx sbx.Runner; DenHome string }` and `func Execute(ctx context.Context, steps []Step, d Deps, out io.Writer) error`. Task 7 wires both.

- [ ] **Step 1: Write the failing tests**

Replace `internal/build/execute_test.go` wholesale:

```go
package build

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
)

// buildableStack writes a stack whose provision files really exist, since
// Execute reads them. Returns the stack and the den home it lives under.
func buildableStack(t *testing.T, name, image, base string, stepNames ...string) (*config.Stack, string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "stacks", name)
	steps := make([]string, 0, len(stepNames))
	for _, n := range stepNames {
		p := filepath.Join(dir, n)
		writeFile(t, p, "echo "+n+"\n")
		steps = append(steps, p)
	}
	return &config.Stack{
		Name: name, Image: image, Base: base, Dir: dir,
		Provision: config.Provision{Steps: steps},
	}, home
}

// The sequence, in order, with the image name coming from den. This is the
// assertion the whole change exists for: `template save` carries `image:`, so
// it cannot disagree with what the spawn later looks for.
func TestExecuteRunsTheWholeSequenceInOrder(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh", "two.sh")
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}
	if err := Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	scratch := ScratchDir(home, "devx")
	want := [][]string{
		{"ls", "--json"},
		{"create", "--name", "devx-build", "claude", scratch},
		{"exec", "devx-build", "--", "bash", "-lc", "echo one.sh\n"},
		{"exec", "devx-build", "--", "bash", "-lc", "echo two.sh\n"},
		{"stop", "devx-build"},
		{"template", "save", "devx-build", "devx:v1"},
		{"rm", "--force", "devx-build"},
	}
	if len(fake.Calls) != len(want) {
		t.Fatalf("calls =\n  %v\nwant %d calls", fake.Calls, len(want))
	}
	for i := range want {
		if !slices.Equal(fake.Calls[i], want[i]) {
			t.Errorf("call %d =\n  %v\nwant\n  %v", i, fake.Calls[i], want[i])
		}
	}
}

// The failing step is NAMED. Without it the user sees a wall of build log and
// an exit code, and has to count the stages to learn which script died.
func TestExecuteNamesTheFailingStep(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh", "two.sh")
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json":                                  {Output: []byte(`{"sandboxes":[]}`)},
		"exec devx-build -- bash -lc echo two.sh\n":  {Err: errors.New("exit status 1")},
	}}
	err := Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{})
	if err == nil {
		t.Fatal("Execute succeeded over a failing step")
	}
	for _, want := range []string{"devx", "step 2/2", "two.sh"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// Teardown is an INVARIANT, not a `trap` each script had to remember. A failed
// build must not leave the VM behind, and it must not save an image either.
func TestExecuteTearsDownAfterAFailedStep(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh")
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
		"exec devx-build -- bash -lc echo one.sh\n": {Err: errors.New("boom")},
	}}
	_ = Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{})
	if !fake.HasCalled("rm", "--force", "devx-build") {
		t.Error("no teardown after a failed step — the build VM leaks")
	}
	if fake.HasCalled("template", "save") {
		t.Error("den saved an image over a failed build")
	}
}

// A pre-existing `<stack>-build` is a REFUSAL, never a blind `rm --force`:
// that name is a legal nest name, so cleaning it up could destroy a real
// sandbox of the user's. The message names the remedy.
func TestExecuteRefusesAPreexistingBuildSandbox(t *testing.T) {
	s, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh")
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[{"name":"devx-build","status":"running"}]}`)},
	}}
	err := Execute(context.Background(), []Step{{Stack: s, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{})
	if err == nil {
		t.Fatal("Execute reused a pre-existing build sandbox")
	}
	if !strings.Contains(err.Error(), "sbx rm --force devx-build") {
		t.Errorf("error %q does not name the remedy", err)
	}
	if fake.HasCalled("create") {
		t.Error("den created over a pre-existing sandbox")
	}
}

// Every provision file of the WHOLE chain is read before the first create.
// Building four minutes of base image to then discover a missing script spends
// that time to reach a refusal den could make instantly.
func TestExecuteReadsEveryProvisionFileBeforeTheFirstCreate(t *testing.T) {
	good, home := buildableStack(t, "devx", "devx:v1", "claude", "one.sh")
	broken := &config.Stack{
		Name: "dgdevx", Image: "dgdevx:v1", Base: "claude",
		Dir:       filepath.Join(home, "stacks", "dgdevx"),
		Provision: config.Provision{Steps: []string{filepath.Join(home, "stacks", "dgdevx", "gone.sh")}},
	}
	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}
	err := Execute(context.Background(),
		[]Step{{Stack: good, Build: true}, {Stack: broken, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{})
	if err == nil {
		t.Fatal("Execute started a chain with an unreadable step")
	}
	if fake.HasCalled("create") {
		t.Error("den created a VM before discovering the unreadable step")
	}
}

// A skipped step is ANNOUNCED, never silent: "already built" and "den forgot
// it" must not look the same from the outside.
func TestExecuteAnnouncesSkippedSteps(t *testing.T) {
	s := &config.Stack{Name: "devx", Image: "devx:v1"}
	var out strings.Builder
	fake := &sbx.Fake{}
	if err := Execute(context.Background(),
		[]Step{{Stack: s, Skipped: "image devx:v1 already built (--force rebuilds it)"}},
		Deps{Sbx: fake, DenHome: t.TempDir()}, &out); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "already built") {
		t.Errorf("output %q does not carry the skip reason", out.String())
	}
	if len(fake.Calls) != 0 {
		t.Errorf("a skipped step touched sbx: %v", fake.Calls)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/build/ -run TestExecute -count=1`
Expected: FAIL — `Execute` still takes a `Script`.

- [ ] **Step 3: Rewrite `execute.go`**

```go
package build

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
)

// Deps is what Execute needs from the machine. Injected for the reason every
// field of cli.Deps is: the real Runner spawns processes, and DenHome is what
// `--den-home` redirects to keep the suite hermetic.
type Deps struct {
	Sbx     sbx.Runner
	DenHome string
}

// Execute runs the plan, in order.
//
// EVERY provision file of the whole chain is read BEFORE the first create —
// the ordering internal/spawn states at length: anything rejectable up front is
// rejected before the first side effect. Here the side effect is expensive
// rather than messy: a chain that built four minutes of base image and only
// then discovered a missing step would have spent that time to reach a refusal
// den could make instantly. Nothing is read twice; the text is what the
// payloads are composed from.
func Execute(ctx context.Context, steps []Step, d Deps, out io.Writer) error {
	provisioned := make(map[string]Provisioning, len(steps))
	for _, s := range steps {
		if !s.Build {
			continue
		}
		// Plan already turns a stack with no provision.steps into a skip (or,
		// for the stack the user named, into a refusal), so no plan Plan
		// produces reaches this. The check stays as a GUARD, in the shape
		// agent.RenderMixin's freshness guard takes: Step is a bare exported
		// struct, and a hand-built plan must not reach half the chain before
		// discovering the hole.
		if !s.Stack.Buildable() {
			return notBuildableError(s.Stack)
		}
		p, err := ReadProvisioning(s.Stack)
		if err != nil {
			return err
		}
		provisioned[s.Stack.Name] = p
	}

	for i, s := range steps {
		if !s.Build {
			// Announced, never silent: a `den build dgdevx` that printed one
			// line would leave "devx was already built" indistinguishable from
			// "den forgot devx". The reason carries its own remedy.
			fmt.Fprintf(out, "[%d/%d] %s: skipped, %s\n", i+1, len(steps), s.Stack.Name, s.Skipped)
			continue
		}
		fmt.Fprintf(out, "[%d/%d] %s: building %s...\n", i+1, len(steps), s.Stack.Name, s.Stack.Image)
		if err := buildOne(ctx, d, s.Stack, provisioned[s.Stack.Name], out); err != nil {
			return err
		}
	}
	return nil
}

// buildOne is one stack's whole sequence. A function of its own so the
// teardown can be a `defer` — in a loop it would pile up until Execute
// returned, which is exactly the leak it exists to prevent.
func buildOne(ctx context.Context, d Deps, s *config.Stack, p Provisioning, out io.Writer) error {
	name := SandboxName(s.Name)

	// A pre-existing build sandbox is a REFUSAL, not a `rm --force` first.
	// `<stack>-build` is a legal nest name (the component charset allows it),
	// so a blind cleanup can destroy a real sandbox of the user's. The teardown
	// below being deferred, a leftover only survives a den killed by SIGKILL:
	// rare enough to deserve a human look.
	boxes, err := sbx.Ls(ctx, d.Sbx)
	if err != nil {
		return fmt.Errorf("stack %q: could not check whether %s already exists: %w", s.Name, name, err)
	}
	if sbx.Find(boxes, name) != nil {
		return fmt.Errorf(
			"stack %q: a sandbox named %s already exists — den will not remove it for you, "+
				"because that name is also a legal nest name. Inspect it, then `sbx rm --force %s`",
			s.Name, name, name)
	}

	scratch := ScratchDir(d.DenHome, s.Name)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return fmt.Errorf("stack %q: could not create the build scratch %s: %w", s.Name, scratch, err)
	}

	// The parent's IMAGE, not its name: `--template` takes a reference. Empty
	// for a root stack, which is what makes CreateArgv use `base:` instead.
	parentImage := ""
	if s.Parent != "" {
		parentImage = s.ParentImage
	}
	argv, err := CreateArgv(s, parentImage, scratch)
	if err != nil {
		return err
	}
	if _, err := d.Sbx.Run(ctx, argv...); err != nil {
		return fmt.Errorf("stack %q: could not create the build sandbox: %w", s.Name, err)
	}
	// From here on the VM exists: it must not outlive this function, whatever
	// happens. This is the `trap` every build.sh had to write by hand, and that
	// no test could verify.
	defer func() {
		if _, err := d.Sbx.Run(ctx, "rm", "--force", name); err != nil {
			fmt.Fprintf(out, "warning: build sandbox %s could not be removed: %v\n", name, err)
		}
	}()

	for i := range p.Steps {
		if _, err := d.Sbx.Run(ctx, "exec", name, "--", "bash", "-lc", p.Payload(i)); err != nil {
			return fmt.Errorf("stack %q: step %d/%d %s failed: %w",
				s.Name, i+1, len(p.Steps), p.Steps[i].Path, err)
		}
	}

	if _, err := d.Sbx.Run(ctx, "stop", name); err != nil {
		return fmt.Errorf("stack %q: could not stop the build sandbox before saving: %w", s.Name, err)
	}
	// den passes the image name. THE point of the whole sequence: `image:` and
	// what is actually saved cannot disagree, so `den build` succeeding and
	// `den <nest>` demanding `den build` can no longer both be true.
	if _, err := d.Sbx.Run(ctx, "template", "save", name, s.Image); err != nil {
		return fmt.Errorf("stack %q: could not save image %s: %w", s.Name, s.Image, err)
	}
	return nil
}
```

**`s.ParentImage` does not exist yet.** Add it to `config.Stack` as a `yaml:"-"` field and populate it in `build.Chain` — the walk already resolves the parent through `stacks.Get`, so it holds the parent `*config.Stack` at that moment. In `internal/build/graph.go`, inside `visit`, after the parent resolves successfully:

```go
			parent, err := stacks.Get(s.Parent)
			// ... existing error handling on err ...
			// The parent's image is carried on the child so Execute does not
			// have to re-resolve the graph it was handed a linear chain of.
			s.ParentImage = parent.Image
```

Adjust the existing `if _, err := stacks.Get(s.Parent); err != nil {` to bind `parent` instead of `_`, keeping every error branch exactly as it is.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/build/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make lint && make test
git add internal/build/execute.go internal/build/execute_test.go internal/build/graph.go internal/config/stack.go
git commit -m "feat(build): den owns create/exec/stop/save/rm, teardown guaranteed"
```

---

### Task 7: delete `script.go`, rewire the CLI, golden the sequence

**Files:**
- Delete: `internal/build/script.go`
- Modify: `internal/cli/build.go`, `internal/cli/root.go:33-37,76`
- Create: `internal/build/testdata/sequence-two-stacks.golden`
- Test: `internal/build/execute_test.go` (add the golden test), `internal/cli/build_test.go`

**Interfaces:**
- Consumes: `build.Deps`, `build.Execute` (Task 6).
- Produces: `newBuildCmd(denHome *string, runner sbx.Runner)` — the `build.Script` parameter is gone. `cli.Deps.Build` is removed.

- [ ] **Step 1: Write the failing golden test**

Append to `internal/build/execute_test.go`:

```go
// The whole argv sequence of a two-stack chain, in one artefact. It is what
// the previous model could not have: running a real build.sh is not testable,
// so the ordering was only ever asserted piecemeal.
//
// Paths are rewritten to <scratch> so the golden does not carry a t.TempDir().
func TestExecuteSequenceGolden(t *testing.T) {
	home := t.TempDir()
	devxDir := filepath.Join(home, "stacks", "devx")
	writeFile(t, filepath.Join(devxDir, "go.sh"), "common::go_tools\n")
	writeFile(t, filepath.Join(home, "lib", "common.sh"), "common::go_tools() { :; }\n")
	devx := &config.Stack{
		Name: "devx", Image: "devx:v1", Base: "claude", Dir: devxDir,
		Provision: config.Provision{
			Includes: []string{filepath.Join(home, "lib", "common.sh")},
			Steps:    []string{filepath.Join(devxDir, "go.sh")},
		},
	}
	dgdevxDir := filepath.Join(home, "stacks", "dgdevx")
	writeFile(t, filepath.Join(dgdevxDir, "glab.sh"), "echo glab\n")
	dgdevx := &config.Stack{
		Name: "dgdevx", Image: "dgdevx:v1", Parent: "devx", ParentImage: "devx:v1", Dir: dgdevxDir,
		Provision: config.Provision{Steps: []string{filepath.Join(dgdevxDir, "glab.sh")}},
	}

	fake := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(`{"sandboxes":[]}`)},
	}}
	if err := Execute(context.Background(),
		[]Step{{Stack: devx, Build: true}, {Stack: dgdevx, Build: true}},
		Deps{Sbx: fake, DenHome: home}, &strings.Builder{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var got strings.Builder
	for _, call := range fake.Calls {
		line := strings.Join(call, " ")
		line = strings.ReplaceAll(line, ScratchDir(home, "devx"), "<scratch:devx>")
		line = strings.ReplaceAll(line, ScratchDir(home, "dgdevx"), "<scratch:dgdevx>")
		got.WriteString(line + "\n")
	}
	golden := filepath.Join("testdata", "sequence-two-stacks.golden")
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s: %v — there is no -update flag, write it by hand", golden, err)
	}
	if got.String() != string(want) {
		t.Errorf("sequence mismatch\n--- got ---\n%s\n--- want (%s) ---\n%s", got.String(), golden, want)
	}
}
```

Write `internal/build/testdata/sequence-two-stacks.golden` **by hand**:

```
ls --json
create --name devx-build claude <scratch:devx>
exec devx-build -- bash -lc common::go_tools() { :; }
common::go_tools

stop devx-build
template save devx-build devx:v1
rm --force devx-build
ls --json
create --name dgdevx-build --template devx:v1 shell <scratch:dgdevx>
exec dgdevx-build -- bash -lc echo glab

stop dgdevx-build
template save dgdevx-build dgdevx:v1
rm --force dgdevx-build
```

Note the embedded newlines: the payload is multi-line, so an `exec` line spans several golden lines and is followed by a blank one (the script's trailing newline). That is faithful, not a formatting slip — leave it.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/build/ -run TestExecuteSequenceGolden -count=1`
Expected: FAIL on the golden diff if the hand-written file is off by a line. Correct the **golden** to match reality only after reading the diff and confirming the actual sequence is right.

- [ ] **Step 3: Delete `script.go` and rewire the CLI**

```bash
git rm internal/build/script.go
```

In `internal/cli/root.go`, delete the `Build build.Script` field and its comment, and the `Build: build.ExecScript{}` line from the constructor. Drop the `internal/build` import if nothing else in the file uses it.

In `internal/cli/build.go`:

```go
func newBuildCmd(denHome *string, runner sbx.Runner) *cobra.Command {
```

Delete the `if script == nil` guard entirely — there is no injected script left to be nil, and `runner` is `cli.Deps.Sbx`, which the wiring already guarantees. Update the `Long` text:

```go
		Long: "Build stack images in `parent` order.\n\n" +
			"Without an argument, every declared stack is built. With one, its ancestors " +
			"are built only if their image is missing, then the stack itself — --force " +
			"rebuilds the ancestors too. den builds each stack in a throwaway sandbox: it " +
			"runs the stack's `provision.steps` inside it, then saves the result as `image:`.",
```

And the call:

```go
			return build.Execute(cmd.Context(), steps, build.Deps{Sbx: runner, DenHome: home}, out)
```

Update the caller of `newBuildCmd` in `root.go` to drop the third argument.

- [ ] **Step 4: Run everything**

```bash
make lint && make typecheck && make test
grep -rn 'build.Script\|ExecScript\|ScriptPath\|ScriptName' --include='*.go' . | grep -v '.claude/worktrees' || echo "no dangling references"
```
Expected: all green, then `no dangling references`.

- [ ] **Step 5: Commit**

```bash
git add -A internal/build internal/cli
git commit -m "refactor(build): drop the Script interface, wire Execute to sbx.Runner"
```

---

## Self-Review

**Spec coverage.** §4.2 `base`/`provision`/exclusion → Task 1. §6 "why den owns the sequence" → Tasks 5–7. §6 sequence table → Task 6. §6 pre-existing sandbox refusal → Task 6. §6 scratch under `cache/` → Task 5. §6 `includes` repeated per step → Task 3. §6 step failure naming → Task 6. §6 pre-flight read → Task 6. §6 "not buildable is skipped and named" → Task 4. §11/§14.1 spawn silence → Task 2. §12 golden on the argv sequence → Task 7.

**Two spec statements this plan does NOT implement, deliberately:**
- *"A step has no access to the host filesystem"* — this is a property of the design, not code to write. Nothing grants such access, so there is nothing to add. Named here so a reviewer does not go looking for the guard.
- *`versions.lock`* — removed from the model; no code ever referenced it.

**Known open item for the implementer.** `sbx template save` was surveyed as existing (`sbx template {ls,save,rm,load}`, 2026-07-31) but den has never invoked it, and its exact argument order (`save <sandbox> <tag>`) is taken from `sbx-devbox`'s scripts rather than from `--help`. Confirm with `sbx template save --help` before Task 6, and fix the argv plus the golden if it differs. Everything else in the sequence uses forms den already runs.
