package sbx

import (
	"slices"
	"strings"
	"testing"
)

// The feature is INVISIBLE when unused, and this is the assertion that says so:
// the two goldens that predate `resources:` must stay byte-identical, which
// TestCreateArgvGolden already checks — this one states the same property at
// the level a reader looks for it.
func TestCreateArgvOmitsResourcesWhenUnset(t *testing.T) {
	argv, err := CreateArgv(completeCreate())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, flag := range []string{"--cpus", "--memory"} {
		if slices.Contains(argv, flag) {
			t.Errorf("%s emitted with nothing declared; argv = %v", flag, argv)
		}
	}
}

// `--cpus 0` is sbx's documented "auto", so an absent cpus: must send NO flag
// rather than a zero. The two mean the same thing to sbx v0.39.0, but by
// coincidence — this test is what keeps den from depending on it.
func TestCreateArgvNilCPUsSendsNoZero(t *testing.T) {
	c := completeCreate()
	c.CPUs = nil
	argv, err := CreateArgv(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slices.Contains(argv, "0") {
		t.Errorf("a bare 0 reached the argv with no cpus declared; argv = %v", argv)
	}
}

func TestCreateArgvEmitsResources(t *testing.T) {
	c := completeCreate()
	n := 4
	c.CPUs = &n
	c.Memory = "8g"
	argv, err := CreateArgv(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if i := slices.Index(argv, "--cpus"); i < 0 || argv[i+1] != "4" {
		t.Errorf("--cpus missing or wrong: %v", argv)
	}
	if i := slices.Index(argv, "--memory"); i < 0 || argv[i+1] != "8g" {
		t.Errorf("--memory missing or wrong: %v", argv)
	}
	// The value travels VERBATIM, in the spelling the user wrote: sbx's grammar
	// is the authority, and a den that re-rendered `8g` as `8589934592` would
	// print a number back at a user who typed a word.
	if slices.Contains(argv, "8589934592") {
		t.Errorf("memory was re-rendered instead of relayed verbatim: %v", argv)
	}
}

// An explicitly written `cpus: 0` DOES reach sbx: it is the value that asks for
// "auto" over a stack's fixed count, and dropping it would silently give that
// nest the stack's number back.
func TestCreateArgvEmitsExplicitZeroCPUs(t *testing.T) {
	c := completeCreate()
	zero := 0
	c.CPUs = &zero
	argv, err := CreateArgv(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if i := slices.Index(argv, "--cpus"); i < 0 || argv[i+1] != "0" {
		t.Errorf("--cpus 0 missing: %v", argv)
	}
}

// The BOUNDARY guard, the doctrine checkWorkspace and the empty-kit filter
// already state: CreateArgv is exported and takes a struct anyone can fill, so
// it refuses what sbx would refuse — even though nest.Resolve has already
// refused it one layer up, where the message can name the file to fix.
func TestCreateArgvGuardsItsOwnResources(t *testing.T) {
	cases := map[string]func(c *Create){
		"memory below the minimum": func(c *Create) { c.Memory = "512m" },
		"memory not a size":        func(c *Create) { c.Memory = "plenty" },
		"negative cpus":            func(c *Create) { n := -1; c.CPUs = &n },
	}
	for name, mutate := range cases {
		c := completeCreate()
		mutate(&c)
		_, err := CreateArgv(c)
		if err == nil {
			t.Errorf("%s: accepted, expected a refusal", name)
			continue
		}
		// The sandbox is named, like every other refusal this function makes:
		// a spawn assembles several, and an unattributed one is a message the
		// reader has to guess the subject of.
		if !strings.Contains(err.Error(), "api.feat12") {
			t.Errorf("%s: error %q does not name the sandbox", name, err)
		}
	}
}
