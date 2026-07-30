package agent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"gopkg.in/yaml.v3"
)

func exampleMixin(t *testing.T) Mixin {
	t.Helper()
	freshness, err := FreshnessCommand("claude", agentClaude())
	if err != nil {
		t.Fatalf("FreshnessCommand: %v", err)
	}
	return Mixin{
		SandboxName: "api.feat12",
		Env: map[string]string{
			"CLAUDE_CONFIG_DIR": "/home/me/.den/agents/claude",
			"SOME_VAR":          "value",
		},
		Egress:    []string{"api.anthropic.com", "github.com"},
		Freshness: freshness,
	}
}

func TestRenderMixinCarriesAllThreePayloads(t *testing.T) {
	out, err := RenderMixin(exampleMixin(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rendered := string(out)

	// The REAL sbx schema: caps.network.allow and environment.variables. The
	// original spec wrote network.allow and env — that was wrong.
	for _, want := range []string{
		"schemaVersion: 2",
		"kind: mixin",
		"caps:",
		"network:",
		"allow:",
		"- api.anthropic.com",
		"environment:",
		"variables:",
		"CLAUDE_CONFIG_DIR: /home/me/.den/agents/claude",
		"SOME_VAR: value",
		"commands:",
		"startup:",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("mixin must contain %q; got:\n%s", want, rendered)
		}
	}
}

// Freshness is fail-closed and the sbx dispatcher exits on the first failure:
// it must be the LAST startup command of the LAST kit.
func TestRenderMixinPutsFreshnessLastInStartup(t *testing.T) {
	m := exampleMixin(t)
	out, err := RenderMixin(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rendered := string(out)

	iStartup := strings.Index(rendered, "startup:")
	iUpdate := strings.Index(rendered, "claude update")
	if iStartup < 0 || iUpdate < 0 || iUpdate < iStartup {
		t.Fatalf("the freshness command must appear under commands.startup; got:\n%s", rendered)
	}
	// And the bin_dirs' $HOME must survive intact into the YAML.
	if !strings.Contains(rendered, "$HOME/.local/bin") {
		t.Errorf("the bin_dirs' $HOME must survive YAML rendering; got:\n%s", rendered)
	}
}

// Determinism: two successive renders must be identical, or the golden file
// is a false-positive trap and the mixin changes on every spawn.
func TestRenderMixinIsDeterministic(t *testing.T) {
	m := exampleMixin(t)
	for i := 0; i < 20; i++ {
		a, err := RenderMixin(m)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		b, err := RenderMixin(m)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(a) != string(b) {
			t.Fatalf("non-deterministic render at iteration %d:\n%s\n---\n%s", i, a, b)
		}
	}
}

// A kit name cannot carry the sandbox name separator.
func TestRenderMixinNamesTheKitWithoutADot(t *testing.T) {
	out, err := RenderMixin(exampleMixin(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "name: den-api-feat12") {
		t.Errorf("kit name must be den-api-feat12; got:\n%s", out)
	}
}

// A nest with neither egress nor env must not produce empty sections: an
// empty `allow: []` means "nothing allowed", not "no constraint".
func TestRenderMixinOmitsEmptySections(t *testing.T) {
	freshness, err := FreshnessCommand("claude", agentClaude())
	if err != nil {
		t.Fatalf("FreshnessCommand: %v", err)
	}
	out, err := RenderMixin(Mixin{SandboxName: "api", Freshness: freshness})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rendered := string(out)
	if strings.Contains(rendered, "caps:") {
		t.Errorf("no egress ⇒ no caps section; got:\n%s", rendered)
	}
	if strings.Contains(rendered, "environment:") {
		t.Errorf("no variables ⇒ no environment section; got:\n%s", rendered)
	}
	// Freshness, on the other hand, is NOT optional: omitting empty sections
	// must never spill over onto commands.
	if !strings.Contains(rendered, "commands:") {
		t.Errorf("commands is never omitted, even without egress or env; got:\n%s", rendered)
	}
}

// An empty Freshness must not be omitted like an empty section: the rendered
// mixin would be valid and would start a sandbox with NO freshness check,
// exactly what FreshnessCommand refuses (spec §9.1). Fail-closed.
func TestRenderMixinRefusesEmptyFreshness(t *testing.T) {
	m := exampleMixin(t)
	m.Freshness = nil
	if _, err := RenderMixin(m); err == nil {
		t.Fatal("an empty Freshness must be refused, not silently omitted")
	}
}

// Corollary: any successfully rendered mixin carries its freshness command.
func TestRenderMixinAlwaysCarriesFreshness(t *testing.T) {
	for _, m := range []Mixin{
		exampleMixin(t),
		{SandboxName: "api", Freshness: exampleMixin(t).Freshness},
	} {
		out, err := RenderMixin(m)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, want := range []string{"commands:", "startup:", "claude update"} {
			if !strings.Contains(string(out), want) {
				t.Errorf("a rendered mixin must always contain %q; got:\n%s", want, out)
			}
		}
	}
}

func exampleResolved(t *testing.T) *nest.Resolved {
	t.Helper()
	g := &config.Global{
		Agents:         map[string]config.Agent{"claude": agentClaude()},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
		Egress:         []string{"github.com"},
	}
	stacks := config.Stacks{Healthy: map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}}
	n := &nest.Nest{Name: "api", Stack: "devx", Egress: []string{"10.22.11.54:27017"}}

	r, err := nest.Resolve("/home/me/.den", g, stacks, n, nest.Options{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return r
}

func TestMixinFromAssemblesTheResolvedNest(t *testing.T) {
	m, err := MixinFrom(exampleResolved(t), "api")
	if err != nil {
		t.Fatalf("MixinFrom: %v", err)
	}
	if m.SandboxName != "api" {
		t.Errorf("SandboxName = %q", m.SandboxName)
	}
	// Egress comes from the cascade, already unioned and sorted by nest.Resolve.
	if len(m.Egress) != 2 || m.Egress[0] != "10.22.11.54:27017" || m.Egress[1] != "github.com" {
		t.Errorf("Egress = %v, expected the unioned and sorted cascade", m.Egress)
	}
	if m.Env["CLAUDE_CONFIG_DIR"] != "/home/me/.den/agents/claude" {
		t.Errorf("Env = %v, {config_dir} must be substituted", m.Env)
	}
	if len(m.Freshness) != 3 {
		t.Errorf("Freshness = %v, expected an argv [bash -c script]", m.Freshness)
	}
}

// Env and Egress are exported on an exported struct: aliasing them would turn
// a write on the mixin into a silent mutation of the resolved nest, which the
// caller keeps using after the spawn.
func TestMixinFromDoesNotAliasTheResolved(t *testing.T) {
	r := exampleResolved(t)
	m, err := MixinFrom(r, "api")
	if err != nil {
		t.Fatalf("MixinFrom: %v", err)
	}

	m.Env["INJECTED"] = "x"
	m.Egress[0] = "replaced.example.test"

	if _, present := r.Env["INJECTED"]; present {
		t.Errorf("writing into Mixin.Env mutated the resolved nest: %v", r.Env)
	}
	if r.Egress[0] == "replaced.example.test" {
		t.Errorf("writing into Mixin.Egress mutated the resolved nest: %v", r.Egress)
	}
}

func TestWriteMixinWritesUnderCache(t *testing.T) {
	denHome := t.TempDir()
	dir, err := WriteMixin(denHome, "api.feat12", exampleMixin(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := filepath.Join(denHome, "cache", "mixins", "api.feat12")
	if dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	// This is the DIRECTORY passed to `--kit`, and sbx looks for spec.yaml there.
	if _, err := os.Stat(filepath.Join(dir, "spec.yaml")); err != nil {
		t.Errorf("spec.yaml must exist in %s: %v", dir, err)
	}
}

// spec.yaml carries environment.variables, i.e. free-form user env — exactly
// where an API key or a credentialed URI ends up. Nothing justifies making it
// readable by everyone.
func TestWriteMixinRestrictsPermissions(t *testing.T) {
	denHome := t.TempDir()
	dir, err := WriteMixin(denHome, "api", exampleMixin(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("dir permissions = %o, want 700", got)
	}

	fileInfo, err := os.Stat(filepath.Join(dir, "spec.yaml"))
	if err != nil {
		t.Fatalf("stat spec.yaml: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("spec.yaml permissions = %o, want 600 — this file carries the resolved env", got)
	}
}

// WriteMixin turns a name into a host path: this is WHERE the guard belongs.
// filepath.Join CLEANS a ".." into a real traversal instead of rejecting it,
// and sbx.SplitName is total and validates nothing.
func TestWriteMixinRefusesANameOutsideTheCharset(t *testing.T) {
	denHome := t.TempDir()
	for _, name := range []string{
		"", ".", "..", "../escape", "api/../../escape", "a/b", "-api", "api.feat.too.many",
	} {
		if _, err := WriteMixin(denHome, name, exampleMixin(t)); err == nil {
			t.Errorf("sandbox name %q must be refused", name)
		}
		// ReadMixin composes the SAME host path, via the same mixinPath: its
		// guard must refuse the same table. Without this, it's exercised by no
		// test (measured: removed, the other 9 packages stay `ok`) and a
		// refactor could drop it without anything turning red. Defense in
		// depth: Spawn already refuses these names upstream.
		//
		// The assertion checks the CAUSE, not just the presence of an error:
		// without the guard, ReadMixin would still return an error anyway —
		// the missing-file one — and a bare `err == nil` check would stay
		// green for a reason unrelated to what it claims to test.
		_, err := ReadMixin(denHome, name)
		if err == nil {
			t.Errorf("ReadMixin must refuse sandbox name %q", name)
		} else if errors.Is(err, os.ErrNotExist) {
			t.Errorf("ReadMixin(%q) hit the missing-file case, not the charset guard: %v", name, err)
		}
	}

	// Counter-proof: no write happened anywhere under denHome.
	var written []string
	if err := filepath.WalkDir(denHome, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			written = append(written, p)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("a refused name must write nothing; got %v", written)
	}
}

// A repeated spawn must overwrite, not stack: the mixin is reconstructible
// and always reflects the CURRENT config, never the previous spawn's.
func TestWriteMixinIsIdempotent(t *testing.T) {
	denHome := t.TempDir()
	m := exampleMixin(t)

	if _, err := WriteMixin(denHome, "api", m); err != nil {
		t.Fatalf("first write: %v", err)
	}
	m.Egress = []string{"new.example.test"}
	dir, err := WriteMixin(denHome, "api", m)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "spec.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(content), "new.example.test") {
		t.Errorf("the second write must replace the first; got:\n%s", content)
	}
	if strings.Contains(string(content), "api.anthropic.com") {
		t.Errorf("the first write's content must not survive; got:\n%s", content)
	}
}

func TestRenderMixinIsRereadableYAML(t *testing.T) {
	m := exampleMixin(t)
	out, err := RenderMixin(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var read struct {
		SchemaVersion int    `yaml:"schemaVersion"`
		Kind          string `yaml:"kind"`
		Caps          struct {
			Network struct {
				Allow []string `yaml:"allow"`
			} `yaml:"network"`
		} `yaml:"caps"`
		Commands struct {
			Startup []struct {
				Command []string `yaml:"command"`
			} `yaml:"startup"`
		} `yaml:"commands"`
	}
	if err := yaml.Unmarshal(out, &read); err != nil {
		t.Fatalf("the rendered mixin must be rereadable YAML: %v\n%s", err, out)
	}
	if read.SchemaVersion != 2 || read.Kind != "mixin" {
		t.Errorf("read header = %d/%q", read.SchemaVersion, read.Kind)
	}
	if len(read.Caps.Network.Allow) != 2 {
		t.Errorf("read allow = %v", read.Caps.Network.Allow)
	}
	if len(read.Commands.Startup) != 1 || len(read.Commands.Startup[0].Command) != 3 {
		t.Fatalf("read startup = %v", read.Commands.Startup)
	}
	// The script must round-trip through YAML BYTE FOR BYTE: it's what runs in
	// the VM. A substring check would say nothing about lost whitespace or a
	// chomping that eats the last line ("exit 1").
	for i, want := range m.Freshness {
		if read.Commands.Startup[0].Command[i] != want {
			t.Errorf("argv[%d] read ≠ rendered\n--- read ---\n%s\n--- want ---\n%s",
				i, read.Commands.Startup[0].Command[i], want)
		}
	}
}

func TestRenderMixinGolden(t *testing.T) {
	out, err := RenderMixin(exampleMixin(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	path := filepath.Join("testdata", "mixin-complete.golden")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	if string(out) != string(want) {
		t.Errorf("rendered != %s\n--- got ---\n%s\n--- want ---\n%s", path, out, want)
	}
}
