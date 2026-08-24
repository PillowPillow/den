package sbx

import (
	"strings"
	"testing"
)

// Every value this table says PARSES was measured against the real
// `sbx create` on 2026-08-24 (spec §14, probe #90): each one reached the
// server's own minimum check, which is proof the grammar accepted it. den's
// parser must be no narrower — a den that refused `2gb` or `4G` would trade a
// saved image pull for a refusal of configuration that works.
func TestParseMemoryGrammar(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		// Measured: sbx answered "below the minimum of 1 GiB", i.e. it parsed.
		{"1kib", 1024},
		{"1024", 1024}, // a bare number is BYTES
		{"0.5g", 536870912},
		// The two spellings sbx's own help offers.
		{"1024m", 1 << 30},
		{"8g", 8 << 30},
		// The rest of the go-units grammar the measured cases imply: an
		// optional `b`/`ib` suffix, any case, an optional space.
		{"2gb", 2 << 30},
		{"4G", 4 << 30},
		{"2048MiB", 2 << 30},
		{"1 g", 1 << 30},
		{"1b", 1},
		{"1t", 1 << 40},
		{"1p", 1 << 50},
	}
	for _, tc := range tests {
		got, err := ParseMemory(tc.in)
		if err != nil {
			t.Errorf("ParseMemory(%q) = error %v, expected %d", tc.in, err, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseMemory(%q) = %d, expected %d", tc.in, got, tc.want)
		}
	}
}

func TestParseMemoryRefusesNonSizes(t *testing.T) {
	// "abc" is measured: sbx answered `invalid size: 'abc'`.
	for _, in := range []string{"abc", "", "   ", "g", "1x", "-1g", "1.2.3g", "1 000m",
		// `b` is not a unit letter, only a suffix: sbx refuses `1bb` too, and
		// den must not be the wider of the two.
		"1bb"} {
		if _, err := ParseMemory(in); err == nil {
			t.Errorf("ParseMemory(%q) accepted it, expected a refusal", in)
		}
	}
}

// The minimum is sbx's, and it is a SERVER-side check that arrives after
// `✓ image ready` (measured 2026-08-24): relaying the value verbatim costs an
// image pull before failing, which is why den holds the same threshold.
func TestValidateMemoryMinimum(t *testing.T) {
	// Exactly 1 GiB passes: sbx refuses what is BELOW the minimum.
	for _, ok := range []string{"1g", "1024m", "1gib", "1073741824"} {
		if err := ValidateMemory(ok); err != nil {
			t.Errorf("ValidateMemory(%q) = %v, expected it to pass (exactly at or above 1 GiB)", ok, err)
		}
	}
	for _, low := range []string{"512m", "1kib", "1024", "0.5g", "1073741823"} {
		err := ValidateMemory(low)
		if err == nil {
			t.Fatalf("ValidateMemory(%q) accepted it, expected a refusal", low)
		}
		if !strings.Contains(err.Error(), "1 GiB") {
			t.Errorf("ValidateMemory(%q) = %q, expected it to state the minimum", low, err)
		}
	}
}

func TestValidateMemoryEmptyIsNotAValue(t *testing.T) {
	// An absent `memory:` is not a faulty one: the caller omits the flag, and
	// nothing here has an opinion about it.
	if err := ValidateMemory(""); err != nil {
		t.Errorf("ValidateMemory(\"\") = %v, expected it to pass — absent means absent", err)
	}
}

func TestValidateCPUsRefusesNegative(t *testing.T) {
	if err := ValidateCPUs(-1); err == nil {
		t.Error("ValidateCPUs(-1) accepted it, expected a refusal")
	}
	// 0 is sbx's documented "auto: all host CPUs", a legitimate value to WRITE.
	for _, n := range []int{0, 1, 64} {
		if err := ValidateCPUs(n); err != nil {
			t.Errorf("ValidateCPUs(%d) = %v, expected it to pass", n, err)
		}
	}
}
