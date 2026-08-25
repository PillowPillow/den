package sbx

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// PositionalAgent is the agent passed to `sbx create`.
//
// "shell" and not "claude": `sbx create [flags] AGENT PATH [PATH...]` requires
// a positional agent (omitting it gives "unknown agent"), but the command
// actually attached is decided by the image's FLAVOR, not by this argument —
// an image snapshotted from the claude base launches `claude` regardless of
// what's written here. den attaches via `sbx exec ... bash -l` anyway, so
// "shell" is the honest choice: it promises nothing it doesn't keep.
const PositionalAgent = "shell"

// EnvSchemaVersion is the ONLY schemaVersion den emits, and it is a STRING:
// sbx answers `unsupported schemaVersion "2" (supported: 1)`, and the int 1
// round-trips as an unquoted scalar sbx refuses.
//
// Pinning it is the mechanism that makes the seam argument true rather than
// aspirational (spec 2026-08-24 §5.5 point 1): a schema evolution becomes a
// visible refusal, never a silently wrong emission. `sbx env` is EXPERIMENTAL
// on all four subcommands — this constant is where that bet is localized.
const EnvSchemaVersion = "1"

// Env is what den compiles a nest into: the input of EnvFile.
//
// It carries EXACTLY the fields Create carried, and that is the design: the
// spec announces a zero-sum exchange with argv.go (§5.4), so an emitter taking
// more inputs than the argv it replaces would be a widened scope in disguise.
//
// MixinKit is a field SEPARATE from StackKits, not the last element of one
// list: the mixin is fail-closed and sbx's dispatcher does `exit $rc` on the
// first failure, which deprives later kits of their startup commands. Two
// fields make the inversion impossible; a single list would only make it
// unlikely — and §14.4 measured that `kits:` preserves declaration order, so
// the position is expressible and therefore load-bearing.
type Env struct {
	Name      string   // sandbox name → `name:`, which wins over the file's directory
	Image     string   // → sandboxOptions.template, which overrides the agent's image
	StackKits []string // cross-cutting kits then the stack kit, layering order
	MixinKit  string   // generated mixin directory — ALWAYS the last kit
	// Workspaces: host paths mounted, in order. The FIRST one becomes
	// `workspace:` and must be the repo (or its worktree): Sandbox.Workdir
	// depends on it for the attach. The rest become additionalWorkspaces.
	// The ":ro" suffix is accepted, exactly like argv.go's Workspaces: measured
	// 2026-08-25 (spec §14.4, "Sonde du 2026-08-25") that a `.sbxenv.yaml`
	// workspace path carrying it DOES mount read-only.
	Workspaces []string
	// CPUs is sandboxOptions.cpus, and NIL means "write no key at all" — the
	// same contract the `--cpus` flag had, for the same measured reason: `sbx
	// create --help` documents `--cpus 0` as "auto: all host CPUs", so a
	// written 0 is a value someone can mean and must stay distinguishable from
	// silence.
	CPUs *int
	// Memory is sandboxOptions.memory, VERBATIM in the spelling the user wrote
	// — sbx's grammar is the authority (ParseMemory mirrors it). Empty writes
	// no key.
	Memory string
}

// envDoc is the ON-DISK shape, and it is unexported on purpose: it is the one
// place the §14.4 key set is spelled out, and nothing outside this file gets to
// grow a field on it. TestEnvFileWritesNoUnmeasuredKey is what holds the line.
//
// Absent from it, deliberately: `ports:` (decision 9 — den publishes on
// demand, and the create-time behaviour of `ports:` is DEDUCED, not measured),
// `secrets:` / `registries:` / `bindings:` (decision 10 — their real lifecycle
// is unmeasured, and den does not relay a field whose effect it does not know),
// `env:` (den's mixin carries it), `mcp:`, and sandboxOptions.profile
// (decision 13 — probed: `sbx policy profile ls` answers "No policy profiles
// found" and no subcommand creates one, so den has nothing to point it at).
type envDoc struct {
	SchemaVersion        string         `yaml:"schemaVersion"`
	Agent                string         `yaml:"agent"`
	Name                 string         `yaml:"name"`
	Workspace            envWorkspace   `yaml:"workspace"`
	AdditionalWorkspaces []envWorkspace `yaml:"additionalWorkspaces,omitempty"`
	Kits                 []string       `yaml:"kits,omitempty"`
	SandboxOptions       envOptions     `yaml:"sandboxOptions"`
}

// envWorkspace carries `path` ALONE, because §14.4 measured WorkspaceMount as
// carrying path alone — no `ro`, no `target`, no `clone`.
type envWorkspace struct {
	Path string `yaml:"path"`
}

type envOptions struct {
	// A pointer with omitempty: yaml.v3's isZero reports a non-nil pointer as
	// NON-empty even when it addresses 0, which is exactly the distinction
	// Env.CPUs exists to preserve.
	CPUs     *int   `yaml:"cpus,omitempty"`
	Memory   string `yaml:"memory,omitempty"`
	Template string `yaml:"template"`
}

// EnvFile renders the .sbxenv.yaml den hands to `sbx env create`.
//
// It replaces the now-deleted CreateArgv (formerly argv.go) one for one (spec
// 2026-08-24 §5.4) and inherits its doctrine: it is exported, it takes a
// struct anyone can fill, so it guards its own input even where nest.Resolve
// already refused the same values one layer up — the ones it does not guard
// are the ones sbx rejects SERVER-side, after pulling the image (§14.5), and
// §6 of the mother spec wants the refusal before the first side effect.
func EnvFile(e Env) ([]byte, error) {
	// Single source of truth, shared with internal/agent: validating
	// component-by-component let "api." through, which sbx would really
	// create and `sbx ls` would split back into "api".
	if err := ValidateSandboxName(e.Name); err != nil {
		return nil, err
	}
	if strings.TrimSpace(e.Image) == "" {
		return nil, fmt.Errorf(
			"sandbox %q: no image — the stack must declare `image:` in stack.yaml", e.Name)
	}
	if strings.TrimSpace(e.MixinKit) == "" {
		return nil, fmt.Errorf(
			"sandbox %q: missing generated mixin — it carries the egress, env and freshness "+
				"command, an emission without it would produce a mute VM", e.Name)
	}
	if len(e.Workspaces) == 0 {
		return nil, fmt.Errorf(
			"sandbox %q: no workspace to mount — `sbx env create` requires at least one path", e.Name)
	}
	for i, w := range e.Workspaces {
		if err := checkEnvWorkspace(e.Name, i, w); err != nil {
			return nil, err
		}
	}
	if e.CPUs != nil {
		if err := ValidateCPUs(*e.CPUs); err != nil {
			return nil, fmt.Errorf("sandbox %q: %w", e.Name, err)
		}
	}
	if err := ValidateMemory(e.Memory); err != nil {
		return nil, fmt.Errorf("sandbox %q: %w", e.Name, err)
	}

	doc := envDoc{
		SchemaVersion: EnvSchemaVersion,
		// The image's FLAVOR decides what actually runs, not this field — an
		// image snapshotted from the claude base launches `claude` whatever is
		// written here, and den attaches via `sbx exec ... bash -l` anyway. The
		// same honest choice PositionalAgent documents for the argv.
		Agent:     PositionalAgent,
		Name:      e.Name,
		Workspace: envWorkspace{Path: e.Workspaces[0]},
		SandboxOptions: envOptions{
			CPUs:     e.CPUs,
			Memory:   e.Memory,
			Template: e.Image,
		},
	}
	for _, w := range e.Workspaces[1:] {
		doc.AdditionalWorkspaces = append(doc.AdditionalWorkspaces, envWorkspace{Path: w})
	}
	// BOUNDARY guard, not a duplicate of config.Stack.DeclaredKits — which
	// already filters empty entries at production's one caller. An empty kit
	// reference would reach sbx, which has no reason to reject it cleanly.
	for _, k := range e.StackKits {
		if k == "" {
			continue
		}
		doc.Kits = append(doc.Kits, k)
	}
	doc.Kits = append(doc.Kits, e.MixinKit) // always last, see the type's doc

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	// Two spaces, fixed here rather than left to the default: the goldens are
	// compared byte for byte and there is no -update flag, so the indent is part
	// of the contract this file owns.
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("rendering the environment file of %s: %w", e.Name, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("rendering the environment file of %s: %w", e.Name, err)
	}
	return buf.Bytes(), nil
}

// checkEnvWorkspace guards one entry of Workspaces, identified by its position
// (index 0) in the list.
//
// Two refusals, each from a measurement (§14.4): a RELATIVE path resolved
// neither against the file's directory nor against the process cwd, in two
// attempts — so it names nothing at all; and a `${VAR}` is sbx's interpolation,
// useful to a human writing by hand and dangerous in a generated file, where it
// would re-open a resolution den has already made (§5.5 point 2).
func checkEnvWorkspace(sandboxName string, i int, w string) error {
	if strings.TrimSpace(w) == "" {
		return fmt.Errorf(
			"sandbox %q: workspace #%d empty — it would mount nothing", sandboxName, i+1)
	}
	// ":ro" is an sbx mount option, not part of the path: strip it to JUDGE
	// (the "$" and absolute-path checks below apply to the real path), and it
	// travels back to EnvFile verbatim — sbx honours it in a `.sbxenv.yaml`
	// workspace path exactly as it does in the `sbx create` positional, measured
	// 2026-08-25 against real sbx v0.39.0 (spec §14.4, "Sonde du 2026-08-25":
	// `RESOLVE SETUP` prints `(ro)`, the guest mount shows `virtiofs (ro,…)`, and
	// a write into the `:ro` workspace fails with "Read-only file system" while
	// the control workspace in the same sandbox accepts it). Dropping the
	// suffix here — as opposed to in the emitted doc — would silently turn a
	// read-only mount into a read-write one; see EnvFile, which writes w, not
	// path.
	path := strings.TrimSuffix(w, ":ro")
	// A literal "$" is refused OUTRIGHT, not just "${": sbx interpolates both
	// `${VAR}` and `$VAR` (§14.4), so a path holding a bare dollar is already
	// ambiguous to the consumer. den resolves every path before emitting, and an
	// emitted variable would re-open that resolution to whatever environment runs
	// sbx. A real path carrying a dollar therefore cannot be mounted through the
	// emitter, and the message says so rather than implying only ${VAR} is at stake.
	if strings.Contains(path, "$") {
		return fmt.Errorf(
			"sandbox %q: workspace #%d (%q) contains \"$\", which sbx reads as a variable "+
				"reference — den resolves every path before emitting, so a path holding a dollar "+
				"cannot be emitted", sandboxName, i+1, w)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf(
			"sandbox %q: workspace #%d (%q) is not an absolute path — `.sbxenv.yaml` resolves a "+
				"relative path against nothing at all (measured, spec §14.4)",
			sandboxName, i+1, w)
	}
	return nil
}
