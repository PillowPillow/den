package sbx

import (
	"strings"
	"testing"
)

// Absent means ABSENT: a den declaring no `resources:` emits a sandboxOptions
// carrying the template alone (§5.5 point 7).
func TestEnvFileOmitsResourcesWhenUnset(t *testing.T) {
	out, err := EnvFile(completeEnv())
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	for _, key := range []string{"cpus:", "memory:"} {
		if strings.Contains(string(out), key) {
			t.Errorf("%q is emitted while nothing declared it:\n%s", key, out)
		}
	}
}

// A WRITTEN zero is a value someone can mean: `sbx create --help` documents
// `--cpus 0` as "auto: all host CPUs". That is the entire reason CPUs is a
// pointer, and emitting `cpus: 0` for an ABSENCE would say something the user
// could have chosen to say.
func TestEnvFileEmitsExplicitZeroCPUs(t *testing.T) {
	e := completeEnv()
	zero := 0
	e.CPUs = &zero
	out, err := EnvFile(e)
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	if !strings.Contains(string(out), "cpus: 0") {
		t.Errorf("an explicitly written 0 must be emitted:\n%s", out)
	}
}

// BOUNDARY guard, the doctrine CreateArgv stated for its own inputs and this
// emitter inherits: nest.Resolve refuses these values one layer up, where the
// message names the yaml file to fix — but EnvFile is exported and takes a
// struct anyone can fill, and the values it does not guard are the ones sbx
// rejects SERVER-side, after pulling the image (§14.5).
func TestEnvFileGuardsItsOwnResources(t *testing.T) {
	for name, mutate := range map[string]func(*Env){
		"negative cpus": func(e *Env) { n := -1; e.CPUs = &n },
		"bogus memory":  func(e *Env) { e.Memory = "1bb" },
	} {
		e := completeEnv()
		mutate(&e)
		if _, err := EnvFile(e); err == nil {
			t.Errorf("%s: must be refused before the image pull", name)
		}
	}
}
