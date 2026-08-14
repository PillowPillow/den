package source

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"slices"

	"golang.org/x/mod/semver"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/lint"
)

// ManifestFile is the SOLE definition of the onboarding contract's filename.
// Its PRESENCE is what separates a manifested source from a legacy one, and
// three commands read that verdict (`den source add`, `den source update`,
// `den init --source`) — so the name may exist in exactly one place.
const ManifestFile = "den-source.yaml"

// ManifestSchema is the only den-source.yaml contract this den understands.
// It versions the YAML SHAPE, and is independent of metadata.version, which
// versions the source's operational CONTENT (spec §5.1): a source publishing
// 1.0.0 and 4.2.0 of itself keeps writing schema_version: 1 until den changes
// the schema.
const ManifestSchema = 1

// The closed credential vocabulary. A manifest can only instantiate what den
// compiles in: it names no shell command, no hook and no plugin (spec §4.3).
// The list is exhaustive on purpose — an unknown type is refused, never
// ignored, because ignoring it would install a source whose credentials
// silently never get configured.
const (
	CredentialGitHub           = "sbx_github"
	CredentialRegistry         = "sbx_registry"
	CredentialHTTPSubstitution = "sbx_http_substitution"
)

// ScopeGlobal is the only scope this delivery supports. sbx also has
// per-sandbox secrets, but an onboarding contract configures a MACHINE: a
// secret scoped to one sandbox would have to be reapplied by every spawn, and
// nothing in the receipt could say whether it still exists.
const ScopeGlobal = "global"

// Manifest is den-source.yaml, decoded strictly. Every field is snake_case:
// with KnownFields(true), a camelCase spelling is a load error rather than an
// empty half of the contract (spec §4.2).
type Manifest struct {
	SchemaVersion int       `yaml:"schema_version"`
	Kind          string    `yaml:"kind"`
	Metadata      Metadata  `yaml:"metadata"`
	Requires      Requires  `yaml:"requires"`
	Exports       Exports   `yaml:"exports"`
	Inputs        Inputs    `yaml:"inputs"`
	Resources     Resources `yaml:"resources"`
}

// Metadata carries the source's identity. Name is a RECOMMENDATION: the
// install name is chosen per machine and `--name` overrides it (spec §5.1).
type Metadata struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// Requires are floors, written ">=<semver>". A range is deliberately not
// supported: den never installs or downgrades a binary, so the only question
// it can answer is "is what is installed new enough".
type Requires struct {
	Den string `yaml:"den"`
	Sbx string `yaml:"sbx"`
}

// Exports is the source's published catalogue. den never infers it by
// scanning the tree: a file that exists but is not exported stays an
// implementation detail of the source (spec §5.2).
type Exports struct {
	Nests  []Export `yaml:"nests"`
	Stacks []Export `yaml:"stacks"`
}

// Export is one published object. Path is REDUNDANT with Name — den loads
// both nests and stacks by name — and that redundancy is the point: it is
// checked against the canonical location (ValidateManifest), so the manifest
// cannot claim to publish a file den would never read.
type Export struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// Inputs declares what the source needs a human (or an answer file) to
// supply. Values are transient: den reads one only when a resource needs
// applying, and never writes it anywhere (spec §5.3).
type Inputs struct {
	Credentials map[string]CredentialInput `yaml:"credentials"`
}

// CredentialInput is the prompt shown for a transient secret. It carries no
// default and no value on purpose — a default secret in a versioned team file
// is a leaked secret.
type CredentialInput struct {
	Prompt string `yaml:"prompt"`
}

// Resources is the closed set of things den converges on the machine.
type Resources struct {
	Credentials  []CredentialResource `yaml:"credentials"`
	BuildNetwork BuildNetwork         `yaml:"build_network"`
	Builds       []Build              `yaml:"builds"`
}

// CredentialResource is one sbx credential. The fields a given Type uses are
// fixed by that type and checked at load: an `environment:` on an sbx_github
// entry is a misunderstanding den refuses rather than ignores.
type CredentialResource struct {
	ID          string    `yaml:"id"`
	Type        string    `yaml:"type"`
	Scope       string    `yaml:"scope"`
	Host        string    `yaml:"host"`
	Environment string    `yaml:"environment"`
	ValueFrom   ValueFrom `yaml:"value_from"`
}

// ValueFrom names a declared input. It cannot name a file, a command or an
// environment variable: where a value comes from on THIS machine is a
// personal question, answered by the wizard or the answer file, never by the
// team's versioned manifest.
type ValueFrom struct {
	Credential string `yaml:"credential"`
}

// BuildNetwork is the machine-level egress the source's builds need.
type BuildNetwork struct {
	Allow []string `yaml:"allow"`
}

// Build names an exported stack to build at install time.
type Build struct {
	Stack string `yaml:"stack"`
}

// ManifestPath is the SOLE definition of where the contract lives, like
// nest.FilePath and config.GlobalPath: every message naming the file to fix
// goes through it.
func ManifestPath(root string) string { return filepath.Join(root, ManifestFile) }

// HasManifest reports whether root is a manifested source. It answers the
// legacy/manifested dispatch, and only that: a manifest that exists but does
// not load is manifested and BROKEN — the caller must hear the load error,
// not silently fall back on the legacy path where a broken contract would go
// unread.
func HasManifest(root string) bool {
	fi, err := os.Stat(ManifestPath(root))
	return err == nil && fi.Mode().IsRegular()
}

// LoadManifest reads and decodes den-source.yaml, and judges everything the
// DOCUMENT alone can settle: schema, identity, requirement shape, export
// uniqueness, and the closed resource vocabulary.
//
// Split from ValidateManifest, which needs the filesystem, for two reasons.
// The first is honesty about failure: a document that does not decode has no
// exports to check, so accumulating tree errors on top of it would print
// consequences of the one fault that matters. The second is the return type —
// one error here (the document is a whole, and the first fault stops it),
// versus every finding there (a checkout is a set of objects, and a CI log
// must fix them all in one push, like lint.Run).
//
// An absent file wraps os.ErrNotExist so a caller can tell a LEGACY source
// (no contract, current behavior preserved) from a broken one.
func LoadManifest(root string) (*Manifest, error) {
	p := ManifestPath(root)
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}
	var m Manifest
	if err := config.DecodeYAMLStrict(p, raw, &m); err != nil {
		return nil, err
	}
	if err := checkDocument(p, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// checkDocument is LoadManifest's judgment, isolated so the rules read as a
// list. Order is chosen so the first refusal is the most explanatory one: a
// schema den does not know makes every later rule meaningless, and a source
// that is not a source cannot have exports.
func checkDocument(p string, m *Manifest) error {
	if m.SchemaVersion != ManifestSchema {
		return fmt.Errorf(
			"%s: schema_version: %d is unsupported — this den understands %d; "+
				"a newer contract needs a newer den", p, m.SchemaVersion, ManifestSchema)
	}
	if m.Kind != "source" {
		return fmt.Errorf(`%s: kind: %q is unsupported — expected "source"`, p, m.Kind)
	}
	if err := config.ValidateSourceName(m.Metadata.Name); err != nil {
		return fmt.Errorf("%s: metadata.name: %w", p, err)
	}
	if !isExactVersion(m.Metadata.Version) {
		return fmt.Errorf(
			"%s: metadata.version: %q is not an exact SemVer version — write it as "+
				"<major>.<minor>.<patch>, without a leading \"v\": den stores this exact string "+
				"as the installed version and compares updates against it",
			p, m.Metadata.Version)
	}
	for _, r := range []struct{ key, value string }{
		{"requires.den", m.Requires.Den},
		{"requires.sbx", m.Requires.Sbx},
	} {
		if r.value == "" {
			continue // no floor declared: that binary is unconstrained
		}
		if _, ok := parseFloor(r.value); !ok {
			return fmt.Errorf(
				"%s: %s: %q is not a supported requirement — write \">=<major>.<minor>.<patch>\": "+
					"den never installs or downgrades a binary, so the only question it can answer "+
					"is whether the installed one is new enough",
				p, r.key, r.value)
		}
	}
	if err := checkExportNames(p, "exports.stacks", m.Exports.Stacks, false); err != nil {
		return err
	}
	if err := checkExportNames(p, "exports.nests", m.Exports.Nests, true); err != nil {
		return err
	}
	return checkResources(p, m)
}

// checkExportNames enforces uniqueness and name legality within one category.
// Uniqueness is per category, not global: a stack and a nest may share a name
// — they live in different namespaces on disk and in every reference.
func checkExportNames(p, key string, exports []Export, sandboxComponent bool) error {
	seen := map[string]bool{}
	for _, e := range exports {
		kind := strings.TrimPrefix(key, "exports.")
		kind = strings.TrimSuffix(kind, "s")
		if err := config.ValidateName(kind, e.Name); err != nil {
			return fmt.Errorf("%s: %s: %w", p, key, err)
		}
		// A nest name becomes a sandbox name component (sbx has no --label),
		// so an exported nest den could never spawn is refused at the source,
		// not on the teammate's machine.
		if sandboxComponent {
			if err := config.ValidateSandboxComponent(kind, e.Name); err != nil {
				return fmt.Errorf("%s: %s: %w", p, key, err)
			}
		}
		if seen[e.Name] {
			return fmt.Errorf(
				"%s: %s: %q is declared twice — an export name is how a teammate addresses "+
					"the object, and den cannot arbitrate between two files claiming it", p, key, e.Name)
		}
		seen[e.Name] = true
	}
	return nil
}

// checkResources judges the closed vocabulary: supported types, the fields
// each type uses, unique ids, declared inputs, and builds naming exported
// stacks.
func checkResources(p string, m *Manifest) error {
	seen := map[string]bool{}
	for _, c := range m.Resources.Credentials {
		if c.ID == "" {
			return fmt.Errorf("%s: resources.credentials: an entry has no `id:` — "+
				"it is the name every plan, error and receipt uses to designate it", p)
		}
		if seen[c.ID] {
			return fmt.Errorf(
				"%s: resources.credentials: %q is declared twice — two entries under one id "+
					"cannot both be planned, applied and verified", p, c.ID)
		}
		seen[c.ID] = true

		where := fmt.Sprintf("%s: credential resource %q", p, c.ID)
		if c.Scope != ScopeGlobal {
			return fmt.Errorf(
				`%s: scope %q is unsupported — expected %q: an onboarding contract configures a `+
					`machine, and a sandbox-scoped secret would have to be reapplied by every spawn`,
				where, c.Scope, ScopeGlobal)
		}
		switch c.Type {
		case CredentialGitHub:
			// The github service credential is interactive on sbx's side
			// (`sbx secret set -g github` opens its own flow), so it takes no
			// host, no environment and no value: den only decides WHETHER to
			// run it.
			if err := refuseUnusedFields(where, c.Type, map[string]string{
				"host":        c.Host,
				"environment": c.Environment,
			}); err != nil {
				return err
			}
			if c.ValueFrom.Credential != "" {
				return fmt.Errorf(`%s: type %q declares no "value_from": sbx collects it interactively`, where, c.Type)
			}
		case CredentialRegistry:
			if c.Host == "" {
				return fmt.Errorf(`%s: type %q requires "host" — the registry it authenticates against`, where, c.Type)
			}
			if err := refuseUnusedFields(where, c.Type, map[string]string{"environment": c.Environment}); err != nil {
				return err
			}
			if c.ValueFrom.Credential == "" {
				return fmt.Errorf(`%s: type %q requires "value_from.credential"`, where, c.Type)
			}
		case CredentialHTTPSubstitution:
			if c.Host == "" {
				return fmt.Errorf(`%s: type %q requires "host" — the host whose requests carry the value`, where, c.Type)
			}
			if c.Environment == "" {
				return fmt.Errorf(`%s: type %q requires "environment" — the variable sbx substitutes`, where, c.Type)
			}
			if c.ValueFrom.Credential == "" {
				return fmt.Errorf(`%s: type %q requires "value_from.credential"`, where, c.Type)
			}
		default:
			return fmt.Errorf(
				"%s: type %q is unsupported — den instantiates only what it compiles in (%s, %s, %s); "+
					"a manifest names no command, hook or plugin",
				where, c.Type, CredentialGitHub, CredentialRegistry, CredentialHTTPSubstitution)
		}
		if ref := c.ValueFrom.Credential; ref != "" {
			if _, ok := m.Inputs.Credentials[ref]; !ok {
				return fmt.Errorf(
					"%s: value_from.credential %q is not declared under inputs.credentials — "+
						"den would have nothing to prompt for, and no name for an answer file to answer",
					where, ref)
			}
		}
	}

	exported := map[string]bool{}
	for _, e := range m.Exports.Stacks {
		exported[e.Name] = true
	}
	for _, b := range m.Resources.Builds {
		if !exported[b.Stack] {
			return fmt.Errorf(
				"%s: resources.builds: stack %q is not exported — den builds only what the source "+
					"publishes under exports.stacks", p, b.Stack)
		}
	}
	return nil
}

// refuseUnusedFields names the fields a type does not use. Refusing rather
// than ignoring: a `host:` written under an sbx_github entry means its author
// expected it to do something, and silence would leave them believing it did.
func refuseUnusedFields(where, typ string, fields map[string]string) error {
	for _, key := range []string{"host", "environment"} { // fixed order: messages must be reproducible
		if v, ok := fields[key]; ok && v != "" {
			return fmt.Errorf("%s: type %q declares no %q, and den would ignore %q", where, typ, key, v)
		}
	}
	return nil
}

// ValidateManifest judges the manifest AGAINST the checkout: that every export
// names a readable file, at the exact place den loads that object from, inside
// the source.
//
// Returns EVERY finding, like lint.Run and for the same reason — a source's CI
// must fix its whole contract in one push. A nil (or empty) result means valid.
func ValidateManifest(root string, m *Manifest) []error {
	// Resolve the root once, so the confinement comparison below is
	// like-for-like: on macOS a t.TempDir() already sits behind /var ->
	// /private/var, and comparing a resolved path against an unresolved root
	// would judge every checkout behind a symlink as escaping itself
	// (internal/lint carries the same note and the same fix).
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return []error{fmt.Errorf("resolving %s: %w", root, err)}
	}
	var errs []error
	for _, e := range m.Exports.Stacks {
		errs = append(errs, checkExportPath(resolvedRoot, "exports.stacks", "stack", e,
			path.Join("stacks", e.Name, "stack.yaml"))...)
	}
	for _, e := range m.Exports.Nests {
		errs = append(errs, checkExportPath(resolvedRoot, "exports.nests", "nest", e,
			path.Join("nests", e.Name+".yaml"))...)
	}
	return errs
}

// checkExportPath judges one export's `path:`.
//
// The canonical rule — the path must be EXACTLY where den loads that named
// object from — is stricter than "relative, confined and readable", and
// deliberately so: den loads a nest through nest.FilePath and a stack through
// config.LoadStack, both by NAME. A file exported from anywhere else is one no
// spawn could ever read, so accepting it would publish a catalogue whose
// entries fail on the teammate's machine rather than in the source's own CI.
// It is also the only implementable reading of "the export name agrees with
// the file": a stack.yaml carries no name (the directory names it) and a nest
// takes its name from its filename.
//
// Confinement is still checked after it, and is not redundant: a canonical
// nests/leo.yaml can itself be a SYMLINK out of the checkout — lexically
// perfect, readable on the author's machine, dangling on every clone.
func checkExportPath(root, key, kind string, e Export, canonical string) []error {
	if e.Path == "" {
		return []error{fmt.Errorf(
			"%s: %s %q: no `path:` — an export names the file it publishes, expected %s",
			ManifestFile, key, e.Name, canonical)}
	}
	if filepath.IsAbs(e.Path) || strings.HasPrefix(e.Path, "~") {
		return []error{fmt.Errorf(
			"%s: %s %q: path %q is absolute — a source is cloned onto machines with different "+
				"layouts; declare it relative to the source root, as %s",
			ManifestFile, key, e.Name, e.Path, canonical)}
	}
	if path.Clean(filepath.ToSlash(e.Path)) != canonical {
		return []error{fmt.Errorf(
			"%s: %s %q: path %q is not where den loads a %s named %q — den loads by name, so a "+
				"file published from anywhere else could never be spawned; write %s, or export it "+
				"under the name its location gives it",
			ManifestFile, key, e.Name, e.Path, kind, e.Name, canonical)}
	}

	full := filepath.Join(root, filepath.FromSlash(canonical))
	if _, err := os.Stat(full); err != nil {
		return []error{fmt.Errorf("%s: %s %q: %s: %w", ManifestFile, key, e.Name, canonical,
			&config.FileError{Err: err})}
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return []error{fmt.Errorf("%s: %s %q: %s: %w", ManifestFile, key, e.Name, canonical, err)}
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []error{fmt.Errorf(
			"%s: %s %q: %s escapes the checkout — a source is self-contained: a path outside its "+
				"tree depends on the receiving machine and cannot be shared",
			ManifestFile, key, e.Name, canonical)}
	}
	return nil
}

// UnknownVersionError says den could not READ a version, which is not the same
// verdict as "too old" and does not have the same remedy: upgrading fixes an
// old binary, while a version den cannot parse is either an unstamped build
// (`go build` answers "dev" — see internal/cli/version.go) or an sbx whose
// output den failed to observe. A type, not a message, so the CLI decides the
// policy without matching text (spec §12.3).
type UnknownVersionError struct {
	Tool     string
	Observed string
	Required string
}

func (e *UnknownVersionError) Error() string {
	return fmt.Sprintf(
		"%s %q is not a version den can compare against the source's requirement %q — "+
			"install a released build, or a build stamped by `task build`",
		e.Tool, e.Observed, e.Required)
}

// CheckCompatibility verifies the installed binaries against the source's
// floors. It NEVER installs or upgrades anything (spec §3): den's answer to an
// unmet floor is a refusal naming both versions.
//
// denVersion and sbxVersion are taken as observed — "1.7.0", "v1.7.0",
// "v1.7.0-3-gabc1234", "v1.7.0-dirty" — because that is what the two sources
// really produce: `git describe` for den (internal/cli/version.go) and
// `sbx version:` output for sbx.
func CheckCompatibility(m *Manifest, denVersion, sbxVersion string) error {
	for _, c := range []struct{ tool, requirement, observed string }{
		{"den", m.Requires.Den, denVersion},
		{"sbx", m.Requires.Sbx, sbxVersion},
	} {
		if c.requirement == "" {
			continue
		}
		floor, ok := parseFloor(c.requirement)
		if !ok {
			// Unreachable through LoadManifest, which refuses the shape. Kept
			// so a hand-built Manifest in a test cannot silently skip a floor.
			return fmt.Errorf("%s: requirement %q is not supported", c.tool, c.requirement)
		}
		installed, ok := releaseVersion(c.observed)
		if !ok {
			return &UnknownVersionError{Tool: c.tool, Observed: c.observed, Required: c.requirement}
		}
		if semver.Compare(installed, floor) < 0 {
			return fmt.Errorf(
				"%s %s is older than the %s required by this source — upgrade %s, den installs "+
					"neither binary", c.tool, strings.TrimPrefix(c.observed, "v"),
				strings.TrimPrefix(floor, "v"), c.tool)
		}
	}
	return nil
}

// parseFloor reads ">=<exact semver>" and returns it in x/mod's v-prefixed
// form. Only that one shape: see checkDocument for why a range is not a
// question den can answer.
func parseFloor(requirement string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(requirement), ">=")
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	if !isExactVersion(rest) {
		return "", false
	}
	return "v" + rest, true
}

// isExactVersion is the manifest's version spelling: <major>.<minor>.<patch>,
// no leading "v", no prerelease, no build metadata. ONE spelling on purpose —
// the exact string is stored in the personal configuration and compared to the
// checkout's on every command, and two spellings of one version would read as
// a divergence and refuse a source that is perfectly converged.
func isExactVersion(v string) bool {
	if v == "" || strings.HasPrefix(v, "v") {
		return false
	}
	return semver.Canonical("v"+v) == "v"+v
}

// releaseVersion normalizes an INSTALLED binary's version for comparison
// against a floor, and keeps only major.minor.patch.
//
// Dropping the prerelease is the whole point, and it is not laxity: `task
// build` stamps `git describe --tags --always --dirty`, so a binary built one
// commit past the v1.7.0 tag answers "v1.7.0-3-gabc1234" and a modified
// checkout answers "v1.7.0-dirty". Both are valid SemVer PRERELEASES, which
// semver.Compare ranks BELOW v1.7.0 — so comparing them whole would refuse a
// den newer than the floor, in the one build path this repo documents.
//
// ok=false for anything that is not a version at all ("dev", a bare commit
// sha): that is UnknownVersionError's case, not a comparison.
func releaseVersion(observed string) (string, bool) {
	v := strings.TrimSpace(observed)
	if v == "" {
		return "", false
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return "", false
	}
	// Cut build metadata first, then the prerelease: x/mod has no
	// "major.minor.patch" accessor, and Canonical KEEPS the prerelease it is
	// this function's job to drop. Cutting in this order is what the grammar
	// requires — a "-" may appear inside build metadata, never before it.
	core := v
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core = core[:i]
	}
	if i := strings.IndexByte(core, '-'); i >= 0 {
		core = core[:i]
	}
	// Canonical fills in an omitted minor or patch ("v1" -> "v1.0.0"), so a
	// floor and an installed version always compare on the same shape.
	core = semver.Canonical(core)
	if core == "" {
		return "", false
	}
	return core, true
}

// ExportedNestNames and ExportedStackNames are the catalogue as plain sorted
// names — what lint, readiness and status iterate over. In manifest order
// would be wrong for a display and right for nothing: the manifest's order
// carries no meaning outside resources.builds, which keeps its own.
func (m *Manifest) ExportedNestNames() []string  { return exportNames(m.Exports.Nests) }
func (m *Manifest) ExportedStackNames() []string { return exportNames(m.Exports.Stacks) }

func exportNames(exports []Export) []string {
	out := make([]string, 0, len(exports))
	for _, e := range exports {
		out = append(out, e.Name)
	}
	slices.Sort(out)
	return out
}

// Lint is the SOLE entry that validates a source checkout, whether it carries
// a den-source.yaml or not.
//
// It exists here rather than in internal/lint because THIS package owns the
// manifest, and internal/lint may not import it: source already imports lint
// (Add's post-clone gate, Update's fail-closed gate, List's per-source
// findings), so the reverse edge would be a cycle. Layering, not doctrine:
// internal/lint still owns every CHECK, this function only decides which
// catalogue they run over.
//
// One entry matters more than where it lives. `den source add` refuses AND
// DELETES the clone on a finding, `den source update` refuses before
// fast-forwarding, and `den lint` is what a team's CI runs — a manifest judged
// in only some of them would let a source pass CI and be refused on install,
// or the reverse.
//
// A source WITHOUT a manifest keeps the legacy directory scan, unchanged
// (spec §9.3): legacy sources predate this contract and must keep working.
func Lint(root string) []error {
	if !HasManifest(root) {
		return lint.Run(root)
	}
	m, err := LoadManifest(root)
	if err != nil {
		// One error, and nothing else attempted: a contract that does not
		// decode publishes no catalogue, so every later finding would be a
		// consequence of this one.
		return []error{err}
	}
	errs := ValidateManifest(root, m)
	return append(errs, lint.RunCatalogue(root, lint.Catalogue{
		Stacks: m.ExportedStackNames(),
		Nests:  m.ExportedNestNames(),
	})...)
}
