package sbx

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// MinMemoryBytes is the smallest `--memory` sbx accepts: 1 GiB.
//
// den holds sbx's own threshold because the refusal is SERVER-side and
// arrives AFTER the image is pulled (measured 2026-08-24, spec §14 probe #90):
//
//	$ sbx create --memory 512m --name den-probe-mem shell <dir>
//	   ✓ image ready
//	ERROR: request failed: 400 Bad Request: invalid memory "512m": memory 512m
//	       is below the minimum of 1 GiB
//
// Nothing is left behind — the sandbox is not created — but a relayed value
// costs a full image pull before failing, and it fails long after the point
// spawn could still have refused cleanly (spec §6: everything rejectable from
// configuration alone is rejected before the first side effect).
//
// "below the minimum" is the server's own word, so the comparison here is
// `>=`: 1g and 1024m are exactly this value and both pass.
const MinMemoryBytes int64 = 1 << 30

// memoryGrammar mirrors what `sbx create --memory` actually parses.
//
// It is docker's go-units `RAMInBytes` on the sbx side — identified from its
// verbatim diagnostic (`invalid size: 'abc'`) and confirmed by measurement:
// `1kib`, a bare `1024` and `0.5g` all reached the server's MINIMUM check,
// which is proof the grammar accepted them. So: a number (integer or
// decimal), an optional single space, an optional unit letter, and an
// optional `b`/`ib` suffix, any case.
//
// den re-implements it rather than taking the dependency: ~20 lines against a
// new module in go.mod, for a grammar that is measured and frozen in a test.
//
// The rule this regexp exists to serve is "no narrower than sbx". A den that
// accepted only `m` and `g` — the two spellings sbx's help gives as EXAMPLES —
// would refuse `2gb`, `4G` and `2048MiB`, all of which work. That trades a
// saved image pull for a refusal of working configuration, which is the worse
// half of the bargain.
//
// No narrower — and no WIDER either. `b` is deliberately absent from the unit
// class, exactly as it is from go-units': the trailing `[iI]?[bB]` group is
// what makes `1b` one byte, and admitting `b` as a unit as well would let
// `1bb` through here for sbx to refuse server-side, after the image pull —
// the very cost this parser exists to avoid.
var memoryGrammar = regexp.MustCompile(`^(\d+(?:\.\d+)?) ?([kmgtpKMGTP])?(?:[iI]?[bB])?$`)

// memoryUnits is binary throughout — sbx's help says "binary units", and
// `1024m` is exactly the 1 GiB minimum, which decides it.
var memoryUnits = map[byte]float64{
	'k': 1 << 10,
	'm': 1 << 20,
	'g': 1 << 30,
	't': 1 << 40,
	'p': 1 << 50,
}

// ParseMemory turns a `--memory` value into bytes, using sbx's own grammar.
//
// Exported because two places need the SAME verdict and must never disagree:
// nest.Resolve refuses before the first side effect, and CreateArgv guards its
// own input (the doctrine ValidateSandboxName and checkWorkspace state in
// argv.go — this function is exported and takes a struct anyone can fill).
func ParseMemory(v string) (int64, error) {
	m := memoryGrammar.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, fmt.Errorf(
			"memory %q is not a size — write a number with a binary unit, e.g. `1024m`, `8g`, `2gib`", v)
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		// Unreachable through the regexp above, which admits only digits and
		// one dot. Kept rather than discarded: ParseFloat is the authority on
		// what a float is, and a silently swallowed error here would turn a
		// future grammar widening into a value of 0 bytes.
		return 0, fmt.Errorf("memory %q is not a size: %w", v, err)
	}
	mul := float64(1) // no unit letter means BYTES, measured: `1024` is 1024 bytes
	if m[2] != "" {
		mul = memoryUnits[strings.ToLower(m[2])[0]]
	}
	bytes := n * mul
	// float64 carries 2^53 exactly, so the guard is on the CONVERSION, not on
	// the arithmetic: `9999999p` overflows int64, and Go's float→int
	// conversion is undefined there rather than saturating. A value this size
	// is a typo, and naming it as one is the honest answer.
	if bytes > math.MaxInt64 {
		return 0, fmt.Errorf("memory %q is larger than any machine — check the unit", v)
	}
	return int64(bytes), nil
}

// ValidateMemory refuses a `memory:` den would otherwise relay to sbx.
//
// The EMPTY string passes: an absent `memory:` is not a faulty one, it is a
// caller that will omit the flag. Making absence an error here would force
// every caller to guard before asking, and one of them would forget.
//
// The message states the grammar and the threshold — the facts that are true
// wherever this value was written. It never names a file: only the caller
// knows which level of the cascade won, and a second guess here would be a
// second dialect for one remedy (the split UnmappedRepoKeyError.Remedy makes,
// in internal/nest, for the same reason).
func ValidateMemory(v string) error {
	if v == "" {
		return nil
	}
	bytes, err := ParseMemory(v)
	if err != nil {
		return err
	}
	if bytes < MinMemoryBytes {
		return fmt.Errorf(
			"memory %q is below sbx's minimum of 1 GiB — raise it to `1g` or more", v)
	}
	return nil
}

// ValidateCPUs refuses a `cpus:` sbx could not honour.
//
// 0 is LEGITIMATE and deliberately not refused: `sbx create --help` documents
// `--cpus 0` as "auto: all host CPUs". It is the value a nest writes to send a
// stack's fixed count back to the host default — which is why the field
// carrying it is a pointer, so an absent `cpus:` stays distinguishable from a
// written zero.
//
// Negative is refused here rather than relayed. Unlike the memory minimum this
// one is not a measurement — no probe was spent on it — but the reasoning does
// not need one: a negative CPU count is not a request sbx has any reading of,
// and den refuses rather than normalizing in silence (spec §2).
func ValidateCPUs(n int) error {
	if n < 0 {
		return fmt.Errorf(
			"cpus %d is negative — write a count of 1 or more, or `0` for sbx's auto (all host CPUs)", n)
	}
	return nil
}
