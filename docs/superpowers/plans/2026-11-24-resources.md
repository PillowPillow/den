# `resources:` (nest CPU and memory) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A nest sets the CPU count and memory of its VM through `resources: {cpus, memory}` in `den.yaml`, written to `sandboxOptions` in the instance's `sbxenv.yaml`.

**Architecture:** `internal/config` decodes and checks the format at load; `nest.Resolve` merges global then nest, key by key, into `*nest.Resolved`; `internal/instance` passes the values to `sbxenv.Render` and into `configHash` (omitempty), so a Live instance reports drift through the existing warning.

**Tech Stack:** Go, `gopkg.in/yaml.v3` (strict decoding), `task check`.

**Spec:** `docs/superpowers/specs/2026-11-24-resources-design.md`, validated by `docs/superpowers/specs/2026-11-24-resources-validation.md` (the validation wins).

## Global Constraints

- `resources:` is allowed at the top level of the global `den.yaml` and in any nest; the top level of a project `den.yaml` refuses it with the `move` message used for `auto_trust` (`is global-only — move it to the global den.yaml`).
- `den-template.yaml` gets no `resources:` key.
- `cpus`: an integer ≥ 1. A fraction fails at decode; 0 or a negative fails in `validate`.
- `memory`: `^[1-9][0-9]*[mg]$` (`m` = MiB, `g` = GiB, lowercase). `8G`, `8gb`, `8`, `0g` are refused.
- den checks the format only, never the bounds (sbx v0.54.0: 512 MiB minimum, host memory maximum).
- An absent key writes nothing to `sbxenv.yaml`; den never writes `cpus: 0`.
- Merge per key: global, then nest; the nest wins for each key it sets.
- `configHash` gains the two values with `omitempty`: an instance made before this change keeps its hash.
- `den ls` is unchanged; the instance record gets no new field.
- No test calls `t.Parallel()`, opens a socket or spawns a process other than git. Goldens are edited by hand; there is no `-update` flag.
- Code, comments and messages in English; comment density matches the surrounding file.

## Review Focus

1. `cpus: 2.5` — refused at load, error names the file (decode error). Test in Task 1.
2. `memory: 8` (a YAML integer, no unit) — refused with den's own memory message, never passed to sbx. Test in Task 1.
3. `resources: {}` or `resources: {cpus: 2}` alone — no empty or half `sandboxOptions:` block, no `memory:` line. Tests in Tasks 3 and 4.
4. Upgrading den with Live instances that never had `resources:` — no drift warning. Test in Task 4.
5. A format error in one nest of a project file — the whole file fails to load, naming the file and `nests.<n>.resources.<key>`. Test in Task 1.

---

### Task 1: Schema and format checks in `internal/config`

**Files:**
- Modify: `internal/config/schema.go` (`File`, `Nest`, new `Resources`)
- Modify: `internal/config/load.go` (`validate`)
- Test: `internal/config/load_test.go`

**Interfaces:**
- Produces: `type Resources struct { CPUs *int \`yaml:"cpus"\`; Memory string \`yaml:"memory"\` }`; `File.Resources *Resources \`yaml:"resources"\``; `Nest.Resources *Resources \`yaml:"resources"\``.

- [ ] **Step 1: Write the failing tests** (use the existing `mustFail`, `writeFile`, `minimalNest`)

```go
func TestLoadRefusesResourcesAtTheTopOfAProjectFile(t *testing.T) {
	mustFail(t, minimalNest+"resources: {cpus: 2}\n", false, "resources", "global-only", "global den.yaml")
}

func TestLoadReadsResourcesOnANestAndAtTheTopOfTheGlobalFile(t *testing.T) {
	// global=true, body = minimalNest with "resources: {memory: 8g}\n" at the top
	// and "    resources: {cpus: 4}\n" under nests.am
	// want: l.File.Resources.Memory == "8g", *l.File.Nests["am"].Resources.CPUs == 4
}

func TestLoadRefusesABadCPUCount(t *testing.T) {
	// table over nest values "0", "-1": mustFail(..., "nests.am.resources.cpus", "remove cpus")
	// "2.5": mustFail(..., "2.5")
}

func TestLoadRefusesABadMemorySize(t *testing.T) {
	// table over "8G", "8gb", "8", "0g", "1.5g", "8 g":
	// mustFail(..., "nests.am.resources.memory", "e.g. 8g")
}

func TestLoadRefusesAnUnknownResourcesKey(t *testing.T) {
	mustFail(t, /* minimalNest with "    resources: {memroy: 8g}\n" */, false, "memroy")
}

func TestLoadAcceptsAWellFormedMemorySize(t *testing.T) {
	// "512m", "8g", "100000g" all load (den never checks the bounds)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/config/ -run 'Resources|CPUCount|MemorySize' -count=1`
Expected: FAIL (`field resources not found in type config.Nest`, and the top-level test passes the load).

- [ ] **Step 3: Implement**

In `schema.go` add `Resources` and the two fields; add `Resources` to the `File` doc comment's global-only list. In `validate`: in the `!global` block, `if f.Resources != nil { add("resources: %s", move) }`; call a new `validateResources(key string, r *Resources, add func(string, ...any))` for `"resources"` (top level) and for `key+".resources"` in the nest loop. Exact messages:

- `%s.cpus: %d is not a CPU count — set 1 or more, or remove cpus to use every host CPU`
- `%s.memory: %q is not a memory size — write an integer then m (MiB) or g (GiB), e.g. 8g`

`memory` is checked against `regexp.MustCompile(`^[1-9][0-9]*[mg]$`)`, compiled once at package level. If `memory: 8` decodes as a string `"8"`, the regexp refuses it; if yaml.v3 refuses the int-to-string decode instead, keep that error and change the test to assert `"8"` only.

- [ ] **Step 4: Run them to verify they pass**

Run: `go test ./internal/config/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: resources: in den.yaml, format checked at load (#90)"
```

### Task 2: Per-key merge in `nest.Resolve`

**Files:**
- Modify: `internal/nest/resolve.go` (`Resolved`, merge next to `AutoTrust`/`AddDirs`)
- Test: `internal/nest/nest_test.go`

**Interfaces:**
- Consumes: `config.Resources`, `File.Resources`, `Nest.Resources` (Task 1).
- Produces: `Resolved.CPUs int` (0 = not set) and `Resolved.Memory string` ("" = not set).

- [ ] **Step 1: Write the failing test** `TestResolveResources`, modeled on `TestResolveAutoTrust` (same `profileGlobal`, `nestLine`, `resolveRich`):

| case | global | nest | want CPUs | want Memory |
|---|---|---|---|---|
| none | — | — | 0 | "" |
| global only | `resources: {cpus: 2, memory: 4g}` | — | 2 | "4g" |
| nest wins per key | `resources: {cpus: 2, memory: 4g}` | `    resources: {cpus: 6}` | 6 | "4g" |
| nest only | — | `    resources: {memory: 16g}` | 0 | "16g" |

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/nest/ -run TestResolveResources -count=1`
Expected: FAIL (`r.CPUs undefined`).

- [ ] **Step 3: Implement** — after the `AddDirs` loop, iterate `[]*config.Resources{global.File.Resources, n.Resources}`; a non-nil `CPUs` sets `r.CPUs`, a non-empty `Memory` sets `r.Memory`. Add the two fields to `Resolved` with a one-line comment in the style of `AutoTrust`'s.

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/nest/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nest/
git commit -m "feat: nest.Resolve merges resources key by key (#90)"
```

### Task 3: `sandboxOptions` in `sbxenv.Render`

**Files:**
- Modify: `internal/sbxenv/render.go` (`Env`, end of `Render`)
- Test: `internal/sbxenv/render_test.go`

**Interfaces:**
- Produces: `Env.CPUs int`, `Env.Memory string`; `sandboxOptions:` is written when any of `Template`, `CPUs > 0`, `Memory != ""` is set, keys in the order `template`, `cpus`, `memory`.

- [ ] **Step 1: Write the failing tests**

```go
func TestRenderWritesResourcesUnderSandboxOptions(t *testing.T) {
	// Env{Name: "a", Workspace: "/w", Template: "t:v2", CPUs: 4, Memory: "8g"}
	// contains "sandboxOptions:\n  template: t:v2\n  cpus: 4\n  memory: 8g\n"
}

func TestRenderWritesResourcesWithoutATemplate(t *testing.T) {
	// Env{Name: "a", Workspace: "/w", CPUs: 2}
	// contains "sandboxOptions:\n  cpus: 2\n"; does not contain "memory:" nor "template:"
}
```

`TestRenderOmitsEmptyBlocks` already asserts no `sandboxOptions:` for an empty `Env`; keep it as is. The goldens do not change.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/sbxenv/ -run Resources -count=1`
Expected: FAIL (`unknown field CPUs`).

- [ ] **Step 3: Implement** — replace the `if e.Template != ""` block with one `sandboxOptions:` header guarded by the three conditions, then one line per set value (`template` through `scalar`, `cpus` as `%d`, `memory` through `scalar`).

- [ ] **Step 4: Run them to verify they pass**

Run: `go test ./internal/sbxenv/ -count=1`
Expected: PASS, goldens unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/sbxenv/
git commit -m "feat: sbxenv renders cpus and memory under sandboxOptions (#90)"
```

### Task 4: Wire into the instance, hash, and docs

**Files:**
- Modify: `internal/instance/create.go:273` (`sbxenv.Env{…}` literal)
- Modify: `internal/instance/prepare.go` (`configHash`)
- Modify: `docs/den-yaml.md` (example file, merge-order table), then `task plugin-sync` (updates `plugins/den/reference/den-yaml.md`)
- Test: `internal/instance/create_test.go`, `internal/instance/prepare_test.go`

**Interfaces:**
- Consumes: `Resolved.CPUs`, `Resolved.Memory` (Task 2); `Env.CPUs`, `Env.Memory` (Task 3).

- [ ] **Step 1: Write the failing tests** (fixture helpers as in `TestCreateComposesTheKitsAndWritesThem` and `TestConfigHashMovesOnlyWhenAutoTrustIsOff`)

```go
func TestCreateWritesTheNestResources(t *testing.T) {
	// nest am: repos(...) + "\nresources: {cpus: 4, memory: 8g}"
	// sbxenv.yaml contains "  cpus: 4\n  memory: 8g\n"
}

func TestCreateWithoutResourcesWritesNoCPUsNorMemory(t *testing.T) {
	// nest am with no resources: sbxenv.yaml contains neither "cpus:" nor "memory:"
}

func TestConfigHashMovesOnlyWhenResourcesAreSet(t *testing.T) {
	// before := hash with no resources key
	// "\nresources: {}"            -> hash == before (an instance made before the key must not drift)
	// "\nresources: {cpus: 4}"     -> hash != before
	// "\nresources: {memory: 8g}"  -> hash != before and != the cpus one
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/instance/ -run 'Resources' -count=1`
Expected: FAIL (no `cpus:` in sbxenv.yaml; hash unchanged when resources are set).

- [ ] **Step 3: Implement** — set `CPUs: res.CPUs, Memory: res.Memory` in the `sbxenv.Env` literal; add to the `configHash` struct, after `NoAutoTrust`:

```go
CPUs   int    `yaml:"cpus,omitempty"`
Memory string `yaml:"memory,omitempty"`
```

with one comment line matching `NoAutoTrust`'s reason (omitted when unset, so older instances keep their hash), and fill them from `res`.

Docs, in `docs/den-yaml.md`: in the global block after `add_dirs`, add `resources: {cpus: 4, memory: 8g}   # optional; the VM size of every instance, see "Merge order"`; in the nest example after `add_dirs: true`, add `resources: {memory: 16g}   # optional; wins over the global resources, key by key`; in the merge-order table add the row `| \`resources\` | sbx's default, then global file, then nest (per key: \`cpus\`, \`memory\`) |`. One sentence under the table: den checks only the format (`cpus` ≥ 1, `memory` as `512m` or `8g`); sbx checks the bounds. Then run `task plugin-sync`.

- [ ] **Step 4: Run the full gate**

Run: `task check`
Expected: lint, typecheck and tests all pass; `internal/plugin` passes after the sync.

- [ ] **Step 5: Commit**

```bash
git add internal/instance/ docs/den-yaml.md plugins/den/reference/den-yaml.md
git commit -m "feat: an instance gets its nest's cpus and memory (#90)"
```

### Task 5: One real create (manual, needs sbx and the user's go)

Not a code task: the format check passed sbx's parser (measured), but no real VM was created with these values on v0.54.0.

- [ ] **Step 1:** `task build`, then `./den up <throwaway nest> ` with `resources: {cpus: 2, memory: 2g}` in a scratch project `den.yaml`.
- [ ] **Step 2:** `./den exec <nest> -- nproc` prints `2`; `./den exec <nest> -- free -g` shows about 2 GiB total.
- [ ] **Step 3:** `./den rm <nest>`, then delete the scratch `den.yaml` (a leftover declaration refuses a same-named nest later).
