package sbx

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func completeCreate() Create {
	return Create{
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

func TestCreateArgvStructure(t *testing.T) {
	argv, err := CreateArgv(completeCreate())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if argv[0] != "create" {
		t.Errorf("argv[0] = %q, want create", argv[0])
	}
	// `sbx create [flags] AGENT PATH [PATH...]`: the positional agent is
	// MANDATORY. Omitting it produces "unknown agent".
	iAgent := slices.Index(argv, PositionalAgent)
	if iAgent < 0 {
		t.Fatalf("the positional agent %q is missing: %v", PositionalAgent, argv)
	}
	// Everything after the agent is a path, nothing else.
	for _, a := range argv[iAgent+1:] {
		if strings.HasPrefix(a, "-") {
			t.Errorf("a flag (%q) trails after the positional agent: %v", a, argv)
		}
	}
	if !slices.Equal(argv[iAgent+1:], completeCreate().Workspaces) {
		t.Errorf("positionals = %v, want the workspaces in order", argv[iAgent+1:])
	}
}

// The most expensive invariant of the plan: the mixin is fail-closed and the
// sbx dispatcher exits on the first failure, depriving LATER kits of their
// startup commands. It must therefore be the LAST --kit.
func TestCreateArgvMixinIsLastKit(t *testing.T) {
	argv, err := CreateArgv(completeCreate())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var kits []string
	for i, a := range argv {
		if a == "--kit" && i+1 < len(argv) {
			kits = append(kits, argv[i+1])
		}
	}
	if len(kits) != 3 {
		t.Fatalf("kits = %v, want 3", kits)
	}
	if kits[len(kits)-1] != "/den/cache/mixins/api.feat12" {
		t.Errorf("the mixin must be the LAST --kit; kits = %v", kits)
	}
	// And the order of stack kits is preserved: it's a layering order.
	if kits[0] != "/den/kits/ssh-known-hosts" || kits[1] != "/den/stacks/dgdevx/kit" {
		t.Errorf("order of stack kits not preserved: %v", kits)
	}
}

// Loading `Stack.Kits` keeps empty entries (a malformed YAML bullet gives an
// empty string in the list). Nothing upstream filters them: this is where the
// neutralization happens, and a `--kit ""` would make the kit dispatcher fail
// — so the whole boot, mixin included.
func TestCreateArgvIgnoresEmptyKits(t *testing.T) {
	c := completeCreate()
	c.StackKits = []string{"/den/kits/ssh-known-hosts", "", "/den/stacks/dgdevx/kit"}

	argv, err := CreateArgv(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var kits []string
	for i, a := range argv {
		if a == "--kit" && i+1 < len(argv) {
			kits = append(kits, argv[i+1])
		}
	}
	if slices.Contains(kits, "") {
		t.Errorf("an empty kit was emitted; kits = %v; argv = %v", kits, argv)
	}
	// And the empty entry actually dropped out, not merely shifted.
	want := []string{
		"/den/kits/ssh-known-hosts",
		"/den/stacks/dgdevx/kit",
		"/den/cache/mixins/api.feat12",
	}
	if !slices.Equal(kits, want) {
		t.Errorf("kits = %v, want %v", kits, want)
	}
}

func TestCreateArgvNameAndTemplate(t *testing.T) {
	argv, err := CreateArgv(completeCreate())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if i := slices.Index(argv, "--name"); i < 0 || argv[i+1] != "api.feat12" {
		t.Errorf("--name missing or wrong: %v", argv)
	}
	// The image travels VERBATIM: it's the user who decides whether it carries
	// a registry (docker.io/library/...) or not. den prefixes nothing.
	if i := slices.Index(argv, "--template"); i < 0 || argv[i+1] != "docker.io/library/dgdevx:v1" {
		t.Errorf("--template missing or wrong: %v", argv)
	}
}

// No --label: sbx has none (verified 2026-07-28). This test is the guard
// against reintroducing one from the original spec.
func TestCreateArgvNeverEmitsLabel(t *testing.T) {
	argv, err := CreateArgv(completeCreate())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slices.Contains(argv, "--label") {
		t.Errorf("sbx create has no --label; argv = %v", argv)
	}
}

func TestCreateArgvRejectsIncompleteEntries(t *testing.T) {
	cases := map[string]func(c *Create){
		"empty name":           func(c *Create) { c.Name = "" },
		"non-sandboxable name": func(c *Create) { c.Name = "my_api" },
		"empty image":          func(c *Create) { c.Image = "" },
		"missing mixin":        func(c *Create) { c.MixinKit = "" },
		"no workspace":         func(c *Create) { c.Workspaces = nil },
	}
	for name, mutate := range cases {
		c := completeCreate()
		mutate(&c)
		if _, err := CreateArgv(c); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
}

// The content of the workspaces is guarded HERE, not by the caller: it's
// `CreateArgv` that turns these values into a command line, and "the caller
// guarantees the paths are absolute" is a contract written nowhere.
// `config.ExpandPath` only expands a leading "~": a `worktree_root:
// my-worktrees` in config.yaml would arrive relative here.
func TestCreateArgvRejectsDodgyWorkspaces(t *testing.T) {
	cases := []struct {
		name       string
		workspaces []string
		position   string // the position must appear in the error
		culprit    string // and the offending path too
	}{
		{"empty entry", []string{"/dev/api", ""}, "#2", ""},
		{"relative path", []string{"/dev/api", "my-worktrees/api"}, "#2", "my-worktrees/api"},
		{"path read as a flag", []string{"/dev/api", "-api"}, "#2", "-api"},
		{"the repo itself is relative", []string{"my-worktrees/api"}, "#1", "my-worktrees/api"},
		{":ro suffix without a path", []string{"/dev/api", ":ro"}, "#2", ":ro"},
	}
	for _, cs := range cases {
		c := completeCreate()
		c.Workspaces = cs.workspaces
		_, err := CreateArgv(c)
		if err == nil {
			t.Errorf("%s: must be rejected", cs.name)
			continue
		}
		// A guard that says "a workspace is invalid" without saying which one
		// leaves the user searching a list they didn't write.
		if !strings.Contains(err.Error(), cs.position) {
			t.Errorf("%s: the error doesn't locate the workspace (%s): %v", cs.name, cs.position, err)
		}
		if cs.culprit != "" && !strings.Contains(err.Error(), cs.culprit) {
			t.Errorf("%s: the error doesn't name the offending path %q: %v", cs.name, cs.culprit, err)
		}
	}
}

// The ":ro" suffix is a mount option, not part of the path: the guard must
// strip it before judging absoluteness. Without this test, a tightening would
// break read-only mounts without anything flagging it.
func TestCreateArgvAcceptsROSuffix(t *testing.T) {
	c := completeCreate()
	c.Workspaces = []string{"/dev/api", "/home/me/.ssh_sbx:ro"}

	argv, err := CreateArgv(c)
	if err != nil {
		t.Fatalf("the :ro suffix must remain accepted: %v", err)
	}
	// And it travels VERBATIM: it's sbx that interprets it, not den.
	if !slices.Contains(argv, "/home/me/.ssh_sbx:ro") {
		t.Errorf("the :ro suffix must pass through intact: %v", argv)
	}
}

// A compound name carries a dot: validation must accept the separator while
// still rejecting the characters `sbx create --name` rejects.
func TestCreateArgvAcceptsCompoundName(t *testing.T) {
	for _, name := range []string{"api", "api.feat12", "my-api.feat-2"} {
		c := completeCreate()
		c.Name = name
		if _, err := CreateArgv(c); err != nil {
			t.Errorf("%q must be accepted: %v", name, err)
		}
	}
}

func TestCreateArgvGolden(t *testing.T) {
	cases := []struct {
		file string
		c    Create
	}{
		{"create-minimal.golden", Create{
			Name:       "api",
			Image:      "devx:v1",
			MixinKit:   "/den/cache/mixins/api",
			Workspaces: []string{"/dev/api", "/home/me/.den/agents/claude"},
		}},
		{"create-complete.golden", completeCreate()},
	}
	for _, c := range cases {
		argv, err := CreateArgv(c.c)
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		path := filepath.Join("testdata", c.file)
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		got := strings.Join(argv, "\n") + "\n"
		if got != string(want) {
			t.Errorf("%s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
		}
	}
}

// A name ending in the separator used to pass component-by-component
// validation: "api." splits into "api" + an empty worktree, two valid
// components. sbx accepts the dot, so the sandbox "api." would be REALLY
// created, then `sbx ls` would split it back into nest "api" —
// indistinguishable from the sandbox "api". This is the duplication risk
// recorded by commit 3b2b42d.
func TestCreateArgvRejectsNonCanonicalName(t *testing.T) {
	for _, name := range []string{"api.", "api.feat12.", ".feat12", "api..feat"} {
		c := completeCreate()
		c.Name = name
		if _, err := CreateArgv(c); err == nil {
			t.Errorf("%q must be rejected: it would designate a sandbox already nameable otherwise", name)
		}
	}
}
