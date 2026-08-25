package nest

import (
	"fmt"
	"path/filepath"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/sbx"
)

// resourceLevel is one level of the cascade's `resources:`, next to the place
// a refusal about it has to send the user.
//
// remedy is a FUNCTION of the field name, not a stored path, because the last
// level has no file: a value typed as a flag is fixed by retyping the flag,
// and a message telling that user to edit `resources:` in a yaml file they
// never wrote it in is exactly the wrong remedy spec §2 exists to forbid.
type resourceLevel struct {
	res    config.Resources
	remedy func(field string) string
}

// fileRemedy is the remedy for a level that IS a file: every declaring level
// but the flags.
func fileRemedy(path string) func(string) string {
	return func(string) string { return fmt.Sprintf("fix `resources:` in %s", path) }
}

// flagRemedy is the remedy for the flags level, which names the flag itself.
func flagRemedy(field string) string { return fmt.Sprintf("fix the `--%s` you typed", field) }

// mergeResources applies the cascade to `resources:` and reports which level
// supplied each winner.
//
// FIELD by field, never block by block: a stack pinning its toolchain's memory
// floor and a nest asking for more CPUs are two independent statements, and a
// whole-block override would make the second silently discard the first. Same
// rule mergeEnv follows one key at a time.
//
// The provenance comes back as two locals rather than as fields on Resolved:
// its only reader is the refusal built right below, and a value carried on the
// resolved object would be a second thing every future consumer has to keep
// true for no one's benefit.
//
// The winning CPUs pointer is COPIED, never aliased: Resolved is handed to
// internal/spawn, and a consumer writing through an aliased pointer would edit
// the loaded nest itself — which compiles, and which no test asserting values
// alone would ever catch.
func mergeResources(levels ...resourceLevel) (out config.Resources, cpusFrom, memoryFrom func(string) string) {
	for _, l := range levels {
		if l.res.CPUs != nil {
			n := *l.res.CPUs
			out.CPUs = &n
			cpusFrom = l.remedy
		}
		if l.res.Memory != "" {
			out.Memory = l.res.Memory
			memoryFrom = l.remedy
		}
	}
	return out, cpusFrom, memoryFrom
}

// resolveResources runs the whole `resources:` cascade and refuses a value sbx
// would reject.
//
// Refused HERE, in the pure resolution, and therefore before the first side
// effect of a spawn (spec §6): a memory below sbx's minimum is rejected
// SERVER-side, after `✓ image ready` (measured 2026-08-24, spec §14 probe
// #90), so relaying it verbatim would cost an image pull and would fail long
// after den had created worktrees it then leaves orphaned.
//
// Only the WINNER is validated, and that is deliberate rather than lazy: a
// global `memory: 512m` that a nest overrides never reaches sbx, and refusing
// the spawn over a value nothing sends would be den refusing a configuration
// that works.
func resolveResources(denHome string, g *config.Global, s *config.Stack, n *Nest, o Options) (config.Resources, error) {
	// Declaration order IS the cascade order — the same one Resolve's doc
	// states, and the same `egress:` and `env:` follow.
	res, cpusFrom, memoryFrom := mergeResources(
		resourceLevel{g.Resources, fileRemedy(config.GlobalPath(denHome))},
		resourceLevel{s.Resources, fileRemedy(filepath.Join(s.Dir, "stack.yaml"))},
		resourceLevel{n.Resources, fileRemedy(FilePath(denHome, n.Name))},
		resourceLevel{o.Resources, flagRemedy},
	)
	if res.CPUs != nil {
		if err := sbx.ValidateCPUs(*res.CPUs); err != nil {
			// The fact and the grammar come from internal/sbx, which owns what
			// sbx accepts; the file to edit is appended here, where the winning
			// level is known. One sentence, two authors — the split
			// UnmappedRepoKeyError.Remedy makes, for the same reason.
			return config.Resources{}, fmt.Errorf("%w — %s", err, cpusFrom("cpus"))
		}
	}
	if err := sbx.ValidateMemory(res.Memory); err != nil {
		return config.Resources{}, fmt.Errorf("%w — %s", err, memoryFrom("memory"))
	}
	return res, nil
}
