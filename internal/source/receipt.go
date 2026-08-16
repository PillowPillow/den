package source

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/PillowPillow/den/internal/config"
)

// ReceiptSchema versions state/sources/<name>.yaml.
const ReceiptSchema = 1

// SourceStatus is the CLOSED vocabulary of a source's aggregate state (spec
// §10.1). Defined here, in the package that persists it, and consumed by
// internal/converge — which computes it but must never become an import of
// this package's writers.
//
//   - ready: every managed resource and every exported nest is usable;
//   - partially_ready: infrastructure is ready, a nest is not, because a
//     required working repository is absent — NOT a failure, den never clones;
//   - blocked: a managed dependency prevents the source from operating;
//   - unknown: den could not observe something it needs to judge (the sbx
//     daemon did not answer). Never inferred as "absent": an unobservable
//     credential is not a missing one (prototype 2026-08-14).
//   - applying: not an observation but a MARKER — a convergence started and
//     did not finish. Every consumer refuses while it is set, and `den source
//     configure` resumes (spec §11.3).
type SourceStatus string

const (
	StatusReady          SourceStatus = "ready"
	StatusPartiallyReady SourceStatus = "partially_ready"
	StatusBlocked        SourceStatus = "blocked"
	StatusUnknown        SourceStatus = "unknown"
	StatusApplying       SourceStatus = "applying"
)

// ResourceStatus is the per-resource vocabulary. Deliberately narrower than
// SourceStatus: a single credential is never "partially" anything, and
// "applying" belongs to the receipt as a whole.
type ResourceStatus string

const (
	ResourceReady   ResourceStatus = "ready"
	ResourceBlocked ResourceStatus = "blocked"
	ResourceUnknown ResourceStatus = "unknown"
)

// NestReady / NestNotReady is the per-nest vocabulary (spec §2): a nest is
// usable, or it is not because a required repository is absent from this
// machine. There is no third value — a nest whose stack or manifest is broken
// makes the SOURCE blocked, not the nest not-ready.
const (
	NestReady    = "ready"
	NestNotReady = "not_ready"
)

// Receipt is <denHome>/state/sources/<name>.yaml: what den APPLIED, written
// by den and not by the user.
//
// It is the final commit marker of a convergence — written last, after every
// managed resource verifies — and the resume state of a partial one. state/
// is not cache/: den never purges it (CLAUDE.md).
//
// What it must never contain: a credential value, the name of an input or of
// the environment variable a value came from, or the repository roots scanned
// during that run. All three are transient answers to one execution (spec
// §10.2), and this file outlives every execution.
type Receipt struct {
	SchemaVersion int          `yaml:"schema_version"`
	Status        SourceStatus `yaml:"status"`
	// Version is the exact functional version this receipt attests. Empty
	// while applying: a half-applied version is never the active one.
	Version         string    `yaml:"version,omitempty"`
	PreviousVersion string    `yaml:"previous_version,omitempty"`
	TargetVersion   string    `yaml:"target_version,omitempty"`
	Commit          string    `yaml:"commit,omitempty"`
	TargetCommit    string    `yaml:"target_commit,omitempty"`
	ManifestDigest  string    `yaml:"manifest_digest,omitempty"`
	AppliedAt       time.Time `yaml:"applied_at,omitempty"`

	Resources ReceiptResources       `yaml:"resources,omitempty"`
	Nests     map[string]ReceiptNest `yaml:"nests,omitempty"`
}

// ReceiptResources mirrors the manifest's resource groups. Stacks is keyed by
// stack name because a source may build several, and a resume must know which
// of them already verified.
type ReceiptResources struct {
	Credentials  ResourceStatus            `yaml:"credentials,omitempty"`
	BuildNetwork ResourceStatus            `yaml:"build_network,omitempty"`
	Stacks       map[string]ResourceStatus `yaml:"stacks,omitempty"`
}

// ReceiptNest is one exported nest's readiness. MissingRepos names the KEYS
// that are unmapped — never a path, which would be personal, and never a URL,
// which the manifest already carries.
type ReceiptNest struct {
	Status       string   `yaml:"status"`
	MissingRepos []string `yaml:"missing_repos,omitempty"`
}

// ReceiptDir is the SOLE definition of where receipts live. Under state/,
// beside the sandbox manifests, and never under cache/.
func ReceiptDir(denHome string) string { return filepath.Join(denHome, "state", "sources") }

// ReceiptPath is the SOLE definition of one source's receipt file. Like
// PersonalPath, it composes without validating; the reader and writer below
// validate the name, since only they open what they compose.
func ReceiptPath(denHome, name string) string {
	return filepath.Join(ReceiptDir(denHome), name+".yaml")
}

// LoadReceipt reads and strictly decodes one source's receipt. An absent
// receipt wraps os.ErrNotExist: it means no convergence has ever completed
// for that source on this machine, which is a state `den source configure`
// resolves — not a corruption.
func LoadReceipt(denHome, name string) (*Receipt, error) {
	if err := config.ValidateSourceName(name); err != nil {
		return nil, err
	}
	p := ReceiptPath(denHome, name)
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", p, &config.FileError{Err: err})
	}
	var out Receipt
	if err := config.DecodeYAMLStrict(p, raw, &out); err != nil {
		return nil, err
	}
	if out.SchemaVersion != ReceiptSchema {
		return nil, fmt.Errorf(
			"%s: schema_version: %d is unsupported — this den writes and reads %d; "+
				"the receipt may have been written by a newer den", p, out.SchemaVersion, ReceiptSchema)
	}
	// The closed vocabularies are checked at LOAD, not at use: a status den
	// does not know cannot be compared, and a caller silently treating it as
	// "not ready" would reapply resources over a converged machine.
	if !validSourceStatus(out.Status) {
		return nil, fmt.Errorf("%s: status: %q is not a status den writes (%s)",
			p, out.Status, sourceStatusList())
	}
	for _, key := range slices.Sorted(maps.Keys(out.Nests)) {
		if s := out.Nests[key].Status; s != NestReady && s != NestNotReady {
			return nil, fmt.Errorf("%s: nests.%s.status: %q is not a nest status den writes (%s, %s)",
				p, key, s, NestReady, NestNotReady)
		}
	}
	for _, s := range []struct {
		key   string
		value ResourceStatus
	}{
		{"resources.credentials", out.Resources.Credentials},
		{"resources.build_network", out.Resources.BuildNetwork},
	} {
		if s.value != "" && !validResourceStatus(s.value) {
			return nil, fmt.Errorf("%s: %s: %q is not a resource status den writes (%s, %s, %s)",
				p, s.key, s.value, ResourceReady, ResourceBlocked, ResourceUnknown)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(out.Resources.Stacks)) {
		if v := out.Resources.Stacks[key]; !validResourceStatus(v) {
			return nil, fmt.Errorf("%s: resources.stacks.%s: %q is not a resource status den writes (%s, %s, %s)",
				p, key, v, ResourceReady, ResourceBlocked, ResourceUnknown)
		}
	}
	return &out, nil
}

// WriteReceipt replaces the receipt atomically and privately. Atomicity is
// load-bearing here beyond durability: the receipt is the LAST write of a
// convergence, so a torn one would claim a version whose resources may never
// have been applied.
func WriteReceipt(denHome, name string, r Receipt) error {
	if err := config.ValidateSourceName(name); err != nil {
		return err
	}
	r.SchemaVersion = ReceiptSchema
	content, err := yaml.Marshal(r)
	if err != nil {
		return fmt.Errorf("rendering the receipt of source %q: %w", name, err)
	}
	return replacePrivateFile(ReceiptPath(denHome, name), content)
}

func validSourceStatus(s SourceStatus) bool {
	switch s {
	case StatusReady, StatusPartiallyReady, StatusBlocked, StatusUnknown, StatusApplying:
		return true
	}
	return false
}

func validResourceStatus(s ResourceStatus) bool {
	switch s {
	case ResourceReady, ResourceBlocked, ResourceUnknown:
		return true
	}
	return false
}

func sourceStatusList() string {
	return fmt.Sprintf("%s, %s, %s, %s, %s",
		StatusReady, StatusPartiallyReady, StatusBlocked, StatusUnknown, StatusApplying)
}
