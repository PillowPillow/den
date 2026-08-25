package sbx

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func completeEnv() Env {
	return Env{
		Name:      "api.feat12",
		Image:     "docker.io/library/dgdevx:v1",
		StackKits: []string{"/den/kits/ssh-known-hosts", "/den/stacks/dgdevx/kit"},
		MixinKit:  "/den/cache/mixins/api.feat12",
		Workspaces: []string{
			"/den/worktrees/feat12/api",
			"/den/worktrees/feat12/front",
			"/home/me/.den/agents/claude",
			"/home/me/.ssh_sbx",
		},
	}
}

// The measured §14.4 key set, and NOTHING else may appear. This is a NEGATIVE
// test on purpose: it is what makes the seam claim of §5.5 point 4 real rather
// than aspirational — a future field added to the doc struct fails here before
// it ever reaches a real sbx.
func TestEnvFileWritesNoUnmeasuredKey(t *testing.T) {
	out, err := EnvFile(completeEnv())
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("the emitted file does not parse: %v", err)
	}
	allowed := map[string]bool{
		"schemaVersion": true, "agent": true, "name": true, "workspace": true,
		"additionalWorkspaces": true, "kits": true, "sandboxOptions": true,
	}
	for k := range doc {
		if !allowed[k] {
			t.Errorf("emitted key %q is outside the measured §14.4 set", k)
		}
	}
	// ports:, secrets:, registries: and bindings: are decisions 9 and 10 —
	// never emitted while their effect is unmeasured.
	for _, forbidden := range []string{"ports", "secrets", "registries", "bindings", "env", "mcp"} {
		if _, ok := doc[forbidden]; ok {
			t.Errorf("%q is emitted, and the spec forbids it", forbidden)
		}
	}
	opts, _ := doc["sandboxOptions"].(map[string]any)
	if _, ok := opts["profile"]; ok {
		t.Error("sandboxOptions.profile is emitted — decision 13 forbids it: den has nothing to point it at")
	}
}

// schemaVersion is a STRING, and the test is worth its line: written as the int
// 1 it round-trips through YAML as `schemaVersion: 1`, which sbx refuses with
// "unsupported schemaVersion" — a refusal that would only appear against a real
// binary.
func TestEnvFilePinsSchemaVersionAsAString(t *testing.T) {
	out, err := EnvFile(completeEnv())
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	if !strings.Contains(string(out), `schemaVersion: "1"`) {
		t.Errorf("schemaVersion is not the quoted string \"1\":\n%s", out)
	}
}

// The most expensive invariant of the design, unchanged from the argv it
// replaces: the mixin is fail-closed and sbx's dispatcher exits on the first
// failure, depriving LATER kits of their startup commands. Measured §14.4:
// kits: preserves declaration order.
func TestEnvFileMixinIsTheLastKit(t *testing.T) {
	out, err := EnvFile(completeEnv())
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	var doc struct {
		Kits []string `yaml:"kits"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"/den/kits/ssh-known-hosts", "/den/stacks/dgdevx/kit", "/den/cache/mixins/api.feat12"}
	if !slices.Equal(doc.Kits, want) {
		t.Errorf("kits = %v, want %v", doc.Kits, want)
	}
}

// The workspace is ALWAYS written, never left to the default — and the default
// is why: sbx resolves an omitted workspace to the directory of the environment
// file (§14.4), and den's file lives under <denHome>/state/sandboxes/<name>/.
// An omission would silently mount den's own state directory into the VM.
func TestEnvFileAlwaysWritesTheWorkspace(t *testing.T) {
	out, err := EnvFile(completeEnv())
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	var doc struct {
		Workspace struct {
			Path string `yaml:"path"`
		} `yaml:"workspace"`
		Additional []struct {
			Path string `yaml:"path"`
		} `yaml:"additionalWorkspaces"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The FIRST workspace is the repo: Sandbox.Workdir depends on it for the attach.
	if doc.Workspace.Path != "/den/worktrees/feat12/api" {
		t.Errorf("workspace.path = %q, want the first workspace", doc.Workspace.Path)
	}
	if len(doc.Additional) != 3 {
		t.Fatalf("additionalWorkspaces = %v, want the other three", doc.Additional)
	}
	if doc.Additional[0].Path != "/den/worktrees/feat12/front" {
		t.Errorf("additionalWorkspaces[0] = %q, want the second workspace", doc.Additional[0].Path)
	}
}

// No ${VAR} ever leaves the emitter (§5.5 point 2): interpolation is a
// convenience for a human writing by hand, and a hazard in a generated file —
// den resolved everything already, and a ${VAR} would re-open the resolution to
// whatever shell runs sbx.
func TestEnvFileEmitsNoInterpolation(t *testing.T) {
	e := completeEnv()
	e.Workspaces = append(e.Workspaces, "/tmp/${HOME}/x")
	if _, err := EnvFile(e); err == nil {
		t.Error("a workspace carrying ${VAR} must be refused: den resolves before emitting")
	}
}

func TestEnvFileRejectsIncompleteEntries(t *testing.T) {
	for name, mutate := range map[string]func(*Env){
		"no name":       func(e *Env) { e.Name = "" },
		"bad name":      func(e *Env) { e.Name = "api." },
		"no image":      func(e *Env) { e.Image = "" },
		"no mixin":      func(e *Env) { e.MixinKit = "" },
		"no workspace":  func(e *Env) { e.Workspaces = nil },
		"relative path": func(e *Env) { e.Workspaces = []string{"relative/path"} },
	} {
		e := completeEnv()
		mutate(&e)
		if _, err := EnvFile(e); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func TestEnvFileGolden(t *testing.T) {
	cases := []struct {
		file string
		e    Env
	}{
		{"env-minimal.golden", Env{
			Name:       "api",
			Image:      "devx:v1",
			MixinKit:   "/den/cache/mixins/api",
			Workspaces: []string{"/dev/api", "/home/me/.den/agents/claude"},
		}},
		{"env-complete.golden", completeEnv()},
		// A THIRD file rather than resources folded into completeEnv(): the two
		// above are what proves sandboxOptions carries no cpus/memory when
		// nothing declares them, and folding would spend that proof to save a file.
		{"env-resources.golden", func() Env {
			e := completeEnv()
			n := 4
			e.CPUs = &n
			e.Memory = "8g"
			return e
		}()},
	}
	for _, c := range cases {
		got, err := EnvFile(c.e)
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		path := filepath.Join("testdata", c.file)
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
		}
	}
}
