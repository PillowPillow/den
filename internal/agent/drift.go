package agent

import (
	"fmt"
	"maps"
	"os"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
)

// parsedSpec is the subset of the kit schema that den GENERATES, and therefore
// the only one it knows how to reread. Decoding is TOLERANT of unknown keys
// (unlike configuration YAML, which is strict): this file may have been
// written by another den version, and a section added since must not make
// drift undetectable.
//
// The price of that tolerance: a key RENAMED on the RenderMixin side would
// produce no error here, just an empty section, i.e. phantom drift reported
// on every attach. TestReadMixinDecodesTheGolden guards against this by
// decoding the golden — hand-written and never regenerated — rather than a
// fresh RenderMixin output that would drift along with it.
//
// TWO spellings of the same two sections are decoded, on purpose. sbx renamed
// `caps:` → `permissions:` and `commands:` → `setup:` between v0.35 and v0.38
// (measured 2026-08-10), and RenderMixin now writes only the new ones — but the
// mixins ALREADY cached on a machine carry the old ones, and each is the drift
// reference of a sandbox that is still running. Reading only the new spelling
// would decode those files to empty sections and report, on every attach, an
// egress emptied and a freshness command gone: a permanent false warning, which
// teaches the user to stop reading the drift report. Expand–contract — the old
// keys leave when the last mixin written before 2026-08-10 is gone, which
// nothing here can date.
type parsedSpec struct {
	Permissions struct {
		Network struct {
			Allow []string `yaml:"allow"`
		} `yaml:"network"`
	} `yaml:"permissions"`
	// Pre-v0.38 spelling of Permissions.
	Caps struct {
		Network struct {
			Allow []string `yaml:"allow"`
		} `yaml:"network"`
	} `yaml:"caps"`
	Environment struct {
		Variables map[string]string `yaml:"variables"`
	} `yaml:"environment"`
	Setup struct {
		Startup []startupEntry `yaml:"startup"`
	} `yaml:"setup"`
	// Pre-v0.38 spelling of Setup.
	Commands struct {
		Startup []startupEntry `yaml:"startup"`
	} `yaml:"commands"`
}

type startupEntry struct {
	Command []string `yaml:"command"`
}

// ReadMixin rereads the mixin left by a previous spawn — i.e. the one the
// sandbox actually received at its `sbx create`.
//
// A missing file is wrapped with %w: the caller must be able to distinguish
// "no reference" (cache/ purged — spec §3 declares it reconstructible — or a
// sandbox created outside this den) from a broken read. Both are "den doesn't
// know", and both get reported; the distinction only tells the user which of
// the two they're looking at, because they don't respond to it the same way.
func ReadMixin(denHome, sandboxName string) (Mixin, error) {
	// Same guard as WriteMixin, for the same reason: this function is exported
	// and composes a host path from the name. Defense in depth — Spawn already
	// refuses these names via sbx.SandboxName.
	if err := sbx.ValidateSandboxName(sandboxName); err != nil {
		return Mixin{}, fmt.Errorf("reading mixin: %w", err)
	}
	path := mixinPath(denHome, sandboxName)
	content, err := os.ReadFile(path)
	if err != nil {
		// config.FileError, not the raw error: the *fs.PathError repeats the
		// path already named on this line, and this message reaches the
		// terminal on every attach whose cache was purged (spawn.reportDrift).
		// %w is mandatory - reportDrift does errors.Is(err, os.ErrNotExist) on
		// it (spawn.go:389).
		return Mixin{}, fmt.Errorf("reading mixin %s: %w", path, &config.FileError{Err: err})
	}
	var spec parsedSpec
	if err := yaml.Unmarshal(content, &spec); err != nil {
		return Mixin{}, fmt.Errorf("reading mixin %s: %w", path, err)
	}

	// New spelling first, old one as the fallback. A file carries one or the
	// other, never both — den generated it — so the order only decides which
	// wins on a hand-edited hybrid, and the section den writes today must be the
	// one that counts.
	egress := spec.Permissions.Network.Allow
	if egress == nil {
		egress = spec.Caps.Network.Allow
	}
	startup := spec.Setup.Startup
	if startup == nil {
		startup = spec.Commands.Startup
	}

	m := Mixin{
		SandboxName: sandboxName,
		Env:         spec.Environment.Variables,
		Egress:      egress,
	}
	// The sequence carries at most two entries, in a FIXED order: the optional
	// link phase, then freshness. Read back positionally, and defensively:
	//
	//   - Freshness is Startup[n-1] — the LAST entry, which it is by
	//     construction (spec §9.1). Unchanged by the arrival of the link phase.
	//   - Links is Startup[0] ONLY when there are two entries. A mixin written
	//     by an older den has exactly one, and it is freshness; mistaking it for
	//     a link phase would report drift on every attach to a sandbox created
	//     before this change.
	if n := len(startup); n > 0 {
		m.Freshness = startup[n-1].Command
		if n > 1 {
			m.Links = startup[0].Command
		}
	}
	return m, nil
}

// Differences lists, in plain terms and in deterministic order, what changed
// between the mixin of an `sbx create` and the one the configuration would
// produce now.
//
// It exists because NOTHING reapplies a mixin to a running VM: a narrowed
// `egress:` passes the settle-loop silently (the VM's broader policy
// obviously allows the narrower list too), and the user believes their
// sandbox is tightened while it stayed open. The reverse — a widened egress —
// fails cleanly, on the settle-loop.
//
// What it does NOT see: the stack image, the kits, and the workspace list.
// None of the three is carried by the mixin, so drift on those axes stays
// invisible here.
//
// Env VALUES are never rendered, only the keys: environment.variables carries
// free-form user env — the very reason for WriteMixin's 0600 — and these
// lines go to the terminal.
func Differences(previous, current Mixin) []string {
	var lines []string

	for _, h := range missingFrom(previous.Egress, current.Egress) {
		lines = append(lines, fmt.Sprintf(
			"egress removed from config: %s — the sandbox still lets it through", h))
	}
	for _, h := range missingFrom(current.Egress, previous.Egress) {
		lines = append(lines, fmt.Sprintf(
			"egress added to config: %s — the sandbox doesn't know it", h))
	}

	for _, k := range slices.Sorted(maps.Keys(previous.Env)) {
		value, present := current.Env[k]
		switch {
		case !present:
			lines = append(lines, fmt.Sprintf(
				"env removed from config: %s — the sandbox still carries it", k))
		case value != previous.Env[k]:
			lines = append(lines, fmt.Sprintf(
				"env changed in config: %s — the sandbox keeps the value from its create", k))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(current.Env)) {
		if _, present := previous.Env[k]; !present {
			lines = append(lines, fmt.Sprintf(
				"env added to config: %s — the sandbox doesn't have it", k))
		}
	}

	// The script isn't rendered: it runs dozens of lines and would drown out
	// everything else.
	if !slices.Equal(previous.Freshness, current.Freshness) {
		lines = append(lines,
			"freshness command changed — the sandbox replays the old one on every boot")
	}

	if !slices.Equal(previous.Links, current.Links) {
		// Same shape as the Freshness line above. Without it, editing `mounts:`
		// and re-attaching reports nothing at all — and mounts are create-time,
		// so the edit genuinely has no effect until a respawn. That silence is
		// the failure mode this whole feature exists to remove.
		lines = append(lines,
			"link phase changed — the sandbox still links what it was created with")
	}
	return lines
}

// missingFrom returns, sorted, the elements of a that are not in b. Sorted
// because these lines go to a terminal: an order that shifts from one spawn
// to the next reads as a change.
func missingFrom(a, b []string) []string {
	var out []string
	for _, v := range a {
		if !slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}
