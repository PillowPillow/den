// Package converge owns the shared onboarding engine: transient answers,
// repository discovery, resource inspection, deterministic plans, application,
// readiness and status aggregation. Every command that installs or reconciles
// a manifested source (`den init --source`, `den source add|configure|update|
// status`, `den doctor`) goes through it, so the plan a user confirms and the
// mutations den performs can never come from two implementations.
//
// It imports internal/source and never the reverse: source owns the persisted
// contract, this package owns what happens to a machine because of it.
package converge

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/source"
)

// Redacted is the ONLY rendering of a credential value den ever produces —
// in a plan, a status, an error, a log or a receipt (spec §12.3). A constant,
// so no call site can invent a second spelling that a redaction test would
// not recognize.
const Redacted = "<redacted>"

// Answers are the inputs of ONE execution. They are never written to the den
// home: repository roots and credential references answer "what is true on
// this machine right now", not "how this source is configured" (spec §7.1).
//
// The same type serves the interactive wizard and the answer file, so a
// non-interactive run exercises the same planner and applier as a human one.
type Answers struct {
	// RepositoryRoots are the directories den scans for working repositories,
	// expanded and absolute. Deliberately absent from source-config and from
	// the receipt.
	RepositoryRoots []string
	// Credentials are the transient secrets, keyed by the input name the
	// manifest declares.
	Credentials map[string]CredentialAnswer
	// Repos maps a repository key to a directory chosen by hand — the
	// non-interactive spelling of confirming an ambiguous or non-standard
	// discovery match.
	Repos map[string]string
}

// CredentialAnswer is one transient secret and where it came from.
//
// Value carries no YAML tag and is filled only by the resolver: the wire
// struct below has no field that could carry a literal, so an answer file
// CANNOT persist a secret even by accident. That is a schema decision, not a
// convention — the recommended automation path must not be the one that
// leaks.
type CredentialAnswer struct {
	FromEnv string
	Value   string
}

// String renders the answer WITHOUT its value. On the type, so a `%v` on an
// Answers struct anywhere in den — a plan, a log line, a test failure — is
// redacted by construction rather than by the discipline of each call site.
func (c CredentialAnswer) String() string {
	if c.FromEnv != "" {
		return fmt.Sprintf("from_env %s: %s", c.FromEnv, Redacted)
	}
	return Redacted
}

// answerFile is the WIRE shape, private on purpose: it has no `value:` key at
// all, which is what makes a literal secret in an answer file a strict-decode
// refusal rather than a silently accepted one.
type answerFile struct {
	RepositoryRoots []string                  `yaml:"repository_roots"`
	Credentials     map[string]credentialWire `yaml:"credentials"`
	Repos           map[string]string         `yaml:"repos"`
}

type credentialWire struct {
	FromEnv string `yaml:"from_env"`
}

// LoadAnswers decodes an answer file and resolves its environment references.
//
// getenv is injected rather than read here: the tests must exercise the exact
// resolution the CLI performs, and a suite that read the developer's real
// environment would pass or fail on what happens to be exported.
//
// A named environment variable that is empty is a REFUSAL, not an empty
// answer: the user asked for a value from a place that has none, and applying
// an empty secret would configure sbx with a credential nobody can use.
func LoadAnswers(path string, getenv func(string) string) (Answers, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Answers{}, fmt.Errorf("reading the answer file %s: %w", path, &config.FileError{Err: err})
	}
	var wire answerFile
	if err := config.DecodeYAMLStrict(path, raw, &wire); err != nil {
		return Answers{}, err
	}

	out := Answers{}
	for i, root := range wire.RepositoryRoots {
		expanded, err := config.ExpandPath(root)
		if err != nil {
			return Answers{}, fmt.Errorf("%s: repository_roots[%d]: %w", path, i, err)
		}
		if !filepath.IsAbs(expanded) {
			return Answers{}, fmt.Errorf(
				"%s: repository_roots[%d]: %q is relative — den scans it from a working directory "+
					"it does not control; write an absolute path, or one starting with ~/",
				path, i, root)
		}
		out.RepositoryRoots = append(out.RepositoryRoots, expanded)
	}

	if len(wire.Credentials) > 0 {
		out.Credentials = make(map[string]CredentialAnswer, len(wire.Credentials))
	}
	// Sorted: a file declaring two faulty credentials must refuse the same one
	// on every run, or a CI log would change between identical pushes.
	for _, name := range slices.Sorted(maps.Keys(wire.Credentials)) {
		c := wire.Credentials[name]
		if strings.TrimSpace(c.FromEnv) == "" {
			return Answers{}, fmt.Errorf(
				"%s: credentials.%s: no `from_env:` — an answer file names the environment variable "+
					"holding the value, never the value itself, so that automating this flow cannot "+
					"commit a secret", path, name)
		}
		value := getenv(c.FromEnv)
		if value == "" {
			return Answers{}, fmt.Errorf(
				"%s: credentials.%s: the environment variable %s is empty or unset — export it before "+
					"running den, or drop the entry to be prompted", path, name, c.FromEnv)
		}
		out.Credentials[name] = CredentialAnswer{FromEnv: c.FromEnv, Value: value}
	}

	if len(wire.Repos) > 0 {
		out.Repos = make(map[string]string, len(wire.Repos))
	}
	for _, key := range slices.Sorted(maps.Keys(wire.Repos)) {
		expanded, err := config.ExpandPath(wire.Repos[key])
		if err != nil {
			return Answers{}, fmt.Errorf("%s: repos.%s: %w", path, key, err)
		}
		if !filepath.IsAbs(expanded) {
			return Answers{}, fmt.Errorf(
				"%s: repos.%s: %q is relative — it names a repository den must find from any working "+
					"directory; write an absolute path, or one starting with ~/",
				path, key, wire.Repos[key])
		}
		out.Repos[key] = expanded
	}
	return out, nil
}

// ValidateAnswers checks the answers AGAINST the manifest that will consume
// them: every credential named must be one the source declares.
//
// Separate from LoadAnswers because the wizard produces the same Answers
// without a file, and both paths must be judged identically — an undeclared
// name is a typo den has to name, or it would prompt for the real input while
// ignoring the one the user filled in.
func ValidateAnswers(m *source.Manifest, a Answers) error {
	for _, name := range slices.Sorted(maps.Keys(a.Credentials)) {
		if _, ok := m.Inputs.Credentials[name]; !ok {
			declared := slices.Sorted(maps.Keys(m.Inputs.Credentials))
			return fmt.Errorf(
				"credential %q is not declared by this source — it declares %v; "+
					"check the spelling, or remove the entry", name, declared)
		}
	}
	return nil
}

// MissingCredentials names the declared inputs the answers do not cover, in a
// stable order. It is what the wizard prompts for and what a non-interactive
// run refuses on, and it is computed from the manifest so a source that adds
// an input gets asked about it without a code change.
func MissingCredentials(m *source.Manifest, a Answers) []string {
	var missing []string
	for _, name := range slices.Sorted(maps.Keys(m.Inputs.Credentials)) {
		if a.Credentials[name].Value == "" {
			missing = append(missing, name)
		}
	}
	return missing
}
