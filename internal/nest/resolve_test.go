package nest

import (
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

func globalTest() *config.Global {
	return &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/home/me/.den/agents/claude",
				Env:       map[string]string{"CLAUDE_CONFIG_DIR": "{config_dir}"},
				BinDirs:   []string{"$HOME/.local/bin"},
				Update:    "claude update",
			},
			"codex": {ConfigDir: "/home/me/.den/agents/codex", Update: "codex --upgrade"},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
		WorktreeRoot:   "/home/me/.den/worktrees",
		Egress:         []string{"api.anthropic.com", "github.com"},
	}
}

func stacksTest() config.Stacks {
	return config.Stacks{Healthy: map[string]*config.Stack{
		"devx":   {Name: "devx", Image: "devx:v1", Kit: "/den/stacks/devx/kit"},
		"dgdevx": {Name: "dgdevx", Image: "dgdevx:v1", Parent: "devx", Kit: "/den/stacks/dgdevx/kit", Egress: []string{"gitlab.digitaleo.com"}},
	}}
}

func nestTest() *Nest {
	return &Nest{
		Name:   "fullstack",
		Stack:  "dgdevx",
		Egress: []string{"10.22.11.54:27017"},
		Repos:  []Repo{{Path: "/dev/api"}, {Path: "/dev/front", Optional: true}},
	}
}

func TestResolveAgentDefault(t *testing.T) {
	name, a, dir, err := resolveAgent(globalTest(), nestTest(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "claude" {
		t.Errorf("name = %q, expected claude (defaults.agent)", name)
	}
	if a.Update != "claude update" {
		t.Errorf("Update = %q", a.Update)
	}
	if dir != "/home/me/.den/agents/claude" {
		t.Errorf("configDir = %q, expected the global registry's", dir)
	}
}

func TestResolveAgentFlagOverride(t *testing.T) {
	name, _, dir, err := resolveAgent(globalTest(), nestTest(), "codex")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "codex" || dir != "/home/me/.den/agents/codex" {
		t.Errorf("name = %q, dir = %q", name, dir)
	}
}

func TestResolveAgentOverrideByNest(t *testing.T) {
	n := nestTest()
	n.Agents = map[string]string{"claude": "/personal/claude-fullstack"}
	_, _, dir, err := resolveAgent(globalTest(), n, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/personal/claude-fullstack" {
		t.Errorf("configDir = %q, expected the nest's override", dir)
	}
}

func TestResolveAgentOverrideNestTargetsTheRightAgent(t *testing.T) {
	// The nest overrides codex; the active agent is claude => the override does not apply.
	n := nestTest()
	n.Agents = map[string]string{"codex": "/personal/codex"}
	_, _, dir, err := resolveAgent(globalTest(), n, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/home/me/.den/agents/claude" {
		t.Errorf("configDir = %q, the codex override should not have applied to claude", dir)
	}
}

// ResolveAgent must accept a nil nest: the `if n != nil` guard exists for
// callers resolving an agent outside a nest context (the future
// `den doctor`/`den build`), and defensive code that is never exercised is not proven.
func TestResolveAgentWithoutNest(t *testing.T) {
	name, a, dir, err := resolveAgent(globalTest(), nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "claude" {
		t.Errorf("name = %q, expected claude (defaults.agent)", name)
	}
	if a.Update != "claude update" {
		t.Errorf("Update = %q", a.Update)
	}
	if dir != "/home/me/.den/agents/claude" {
		t.Errorf("configDir = %q, expected the global registry's", dir)
	}
}

func TestResolveAgentUnknown(t *testing.T) {
	_, _, _, err := resolveAgent(globalTest(), nestTest(), "gemini")
	if err == nil {
		t.Fatal("expected an error for an unknown agent")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Errorf("error = %q, expected the list of available agents", err.Error())
	}
}

func TestResolveFullCascade(t *testing.T) {
	r, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Stack.Image != "dgdevx:v1" {
		t.Errorf("Stack.Image = %q", r.Stack.Image)
	}
	if r.AgentName != "claude" {
		t.Errorf("AgentName = %q", r.AgentName)
	}
	expected := []string{"10.22.11.54:27017", "api.anthropic.com", "github.com", "gitlab.digitaleo.com"}
	if len(r.Egress) != len(expected) {
		t.Fatalf("Egress = %v, expected %v", r.Egress, expected)
	}
	for i := range expected {
		if r.Egress[i] != expected[i] {
			t.Fatalf("Egress = %v, expected %v", r.Egress, expected)
		}
	}
	if len(r.Repos) != 2 {
		t.Errorf("Repos = %v, expected both repos", names(r.Repos))
	}
	if r.SSHMode != "agent-forward" || r.WorktreeLayout != "central" {
		t.Errorf("SSH/worktree not inherited from global: %+v", r)
	}
}

func TestResolveAppliesRepoSelection(t *testing.T) {
	r, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{Without: []string{"front"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := names(r.Repos); len(got) != 1 || got[0] != "api" {
		t.Errorf("Repos = %v, expected [api]", got)
	}
}

func TestResolveStackDefaultsFromNestWhenAbsent(t *testing.T) {
	n := nestTest()
	n.Stack = "" // the nest does not decide => defaults.stack
	r, err := Resolve("/d", globalTest(), stacksTest(), n, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Stack.Name != "devx" {
		t.Errorf("Stack.Name = %q, expected the default devx", r.Stack.Name)
	}
}

func TestResolveUnknownStack(t *testing.T) {
	n := nestTest()
	n.Stack = "ghost"
	_, err := Resolve("/d", globalTest(), stacksTest(), n, Options{})
	if err == nil {
		t.Fatal("expected an error for an unknown stack")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %q, expected a mention of the missing stack", err.Error())
	}
}

func TestResolveMergesAndSubstitutesEnv(t *testing.T) {
	g := &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/home/me/.den/agents/claude",
				Env:       map[string]string{"CLAUDE_CONFIG_DIR": "{config_dir}"},
				Update:    "claude update",
			},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
		WorktreeRoot:   "/home/me/.den/worktrees",
	}
	stacks := config.Stacks{Healthy: map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1", Dir: "/d/stacks/devx"}}}
	n := &Nest{Name: "api", Stack: "devx", Env: map[string]string{"SOME_VAR": "value"}}

	r, err := Resolve("/home/me/.den", g, stacks, n, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if r.DenHome != "/home/me/.den" {
		t.Errorf("DenHome = %q, expected /home/me/.den", r.DenHome)
	}
	// {config_dir} must be resolved: the mixin cannot do it, and the target
	// path is a HOST path (sbx mounts at the same path in the VM).
	if got := r.Env["CLAUDE_CONFIG_DIR"]; got != "/home/me/.den/agents/claude" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, expected the resolved config_dir", got)
	}
	if got := r.Env["SOME_VAR"]; got != "value" {
		t.Errorf("SOME_VAR = %q, expected value", got)
	}
}

// Cascade: global ← stack ← nest ← flags. The nest wins over the agent.
func TestResolveNestEnvWinsOverAgentEnv(t *testing.T) {
	g := &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/profile",
				Env:       map[string]string{"SHARED": "agent", "OWN": "agent"},
				Update:    "claude update",
			},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
	}
	stacks := config.Stacks{Healthy: map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}}
	n := &Nest{Name: "api", Stack: "devx", Env: map[string]string{"SHARED": "nest"}}

	r, err := Resolve("/d", g, stacks, n, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Env["SHARED"] != "nest" {
		t.Errorf("SHARED = %q, expected nest (the nest is lower in the cascade)", r.Env["SHARED"])
	}
	if r.Env["OWN"] != "agent" {
		t.Errorf("OWN = %q, expected agent", r.Env["OWN"])
	}
}

// The nest's config_dir override must propagate INTO the substituted env,
// otherwise the VM would point at the shared profile even though the nest
// asked for isolation.
func TestResolveSubstitutesNestConfigDirOverride(t *testing.T) {
	g := &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/profile/shared",
				Env:       map[string]string{"CLAUDE_CONFIG_DIR": "{config_dir}"},
				Update:    "claude update",
			},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
	}
	stacks := config.Stacks{Healthy: map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}}
	n := &Nest{Name: "api", Stack: "devx", Agents: map[string]string{"claude": "/profile/isolated"}}

	r, err := Resolve("/d", g, stacks, n, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Env["CLAUDE_CONFIG_DIR"] != "/profile/isolated" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, expected /profile/isolated", r.Env["CLAUDE_CONFIG_DIR"])
	}
}

// The nest may also reference {config_dir} in its own env (e.g. to reassert
// the agent's default): the token must be substituted there too, otherwise a
// nest that believes it is reasserting CLAUDE_CONFIG_DIR overwrites the
// agent's substituted value with the literal token.
func TestResolveSubstitutesConfigDirInNestEnv(t *testing.T) {
	g := &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/profile/claude",
				Update:    "claude update",
			},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
	}
	stacks := config.Stacks{Healthy: map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}}
	n := &Nest{Name: "api", Stack: "devx", Env: map[string]string{"SOMETHING": "{config_dir}"}}

	r, err := Resolve("/d", g, stacks, n, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.Env["SOMETHING"]; got != "/profile/claude" {
		t.Errorf("SOMETHING = %q, expected the resolved config_dir (/profile/claude)", got)
	}
}

func TestResolveRefusesRelativeDenHome(t *testing.T) {
	g := &config.Global{
		Agents:         map[string]config.Agent{"claude": {ConfigDir: "/p", Update: "u"}},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
	}
	stacks := config.Stacks{Healthy: map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}}
	_, err := Resolve("relative/den", g, stacks, &Nest{Name: "api", Stack: "devx"}, Options{})
	if err == nil {
		t.Fatal("expected an error for a relative denHome")
	}
	if !strings.Contains(err.Error(), "relative/den") {
		t.Errorf("error = %q, expected the offending path", err.Error())
	}
}

func TestResolveEnvNeverNil(t *testing.T) {
	g := &config.Global{
		Agents:         map[string]config.Agent{"claude": {ConfigDir: "/p", Update: "u"}},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
	}
	stacks := config.Stacks{Healthy: map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}}
	r, err := Resolve("/d", g, stacks, &Nest{Name: "api", Stack: "devx"}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Env == nil {
		t.Error("Env must be an empty map, never nil: the mixin iterates over it without a guard")
	}
}

func TestResolvePutsCommandLineReposFirst(t *testing.T) {
	// Workspaces[0] decides where the attached shell starts
	// (sbx.Sandbox.Workdir). "I am mounting X on the fly" means "I have come to
	// work in X", so a positional wins over the nest's own first repo.
	r, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{
		Repos: []string{"/tmp/hotfix"},
		Cwd:   "/work",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"/tmp/hotfix", "/dev/api", "/dev/front"}
	if got := paths(r.Repos); !slices.Equal(got, expected) {
		t.Errorf("Repos = %v, expected %v — the positional comes first", got, expected)
	}
	if !r.Repos[0].AdHoc {
		t.Error("Repos[0].AdHoc = false: the origin must survive the merge")
	}
	if r.Repos[1].AdHoc {
		t.Error("Repos[1].AdHoc = true: a declared repo must not be reported as ad-hoc")
	}
}

func TestResolveWithoutStillFiltersDeclaredRepos(t *testing.T) {
	// --without/--only keep addressing the declared list ONLY. A repo given on
	// the command line is removed by not typing it.
	r, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{
		Repos:   []string{"/tmp/hotfix"},
		Cwd:     "/work",
		Without: []string{"front"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"/tmp/hotfix", "/dev/api"}
	if got := paths(r.Repos); !slices.Equal(got, expected) {
		t.Errorf("Repos = %v, expected %v", got, expected)
	}
}

func TestResolveRefusesWithoutNamingACommandLineRepo(t *testing.T) {
	_, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{
		Repos:   []string{"/tmp/hotfix"},
		Cwd:     "/work",
		Without: []string{"hotfix"},
	})
	if err == nil {
		t.Fatal("expected a refusal: --without does not address a repo given on the command line")
	}
	if !strings.Contains(err.Error(), "hotfix") {
		t.Errorf("error = %q, expected it to name the repo", err)
	}
}

func TestResolveRefusesABasenameCollisionWithTheCommandLine(t *testing.T) {
	// nestTest declares /dev/api. A positional whose basename is also "api"
	// makes --without, the worktree path and the sbx positional ambiguous at
	// once: a hard error, not a last-one-wins.
	_, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{
		Repos: []string{"/tmp/scratch/api"},
		Cwd:   "/work",
	})
	if err == nil {
		t.Fatal("expected a refusal on the duplicated short name \"api\"")
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("error = %q, expected it to point at the fixable half", err)
	}
}

func TestResolveRefusesTheSamePathTwiceOnTheCommandLine(t *testing.T) {
	// Two positionals naming the same directory: same basename, so absent the
	// duplicate-path pre-pass this falls through to the basename message,
	// which sends the reader hunting for a second, distinct path that does
	// not exist.
	_, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{
		Repos: []string{"/tmp/scratch/hotfix", "/tmp/scratch/hotfix"},
		Cwd:   "/work",
	})
	if err == nil {
		t.Fatal("expected a refusal: the same path was given twice on the command line")
	}
	if strings.Contains(err.Error(), "short name") {
		t.Errorf("error = %q, expected the SAME-PATH message, not the basename collision", err)
	}
	if !strings.Contains(err.Error(), "twice") {
		t.Errorf("error = %q, expected it to say the path was given twice", err)
	}
}

func TestResolveRefusesACommandLinePathEqualToADeclaredOne(t *testing.T) {
	// The declared entry carries a trailing slash — legal YAML, and LoadNest
	// only ever tilde-expands a declared path (nest.go:129), it never Cleans
	// it. A positional's path IS Cleaned, in parseRepoArg. If the duplicate
	// check compared raw strings instead of Clean(a) == Clean(b), this case —
	// exactly the one the finding names, `den api ~/dev/api` colliding with an
	// already-declared repo — would be missed and fall through to the
	// basename message instead.
	n := &Nest{
		Name:  "fullstack",
		Stack: "devx",
		Repos: []Repo{{Path: "/dev/api/"}},
	}
	_, err := Resolve("/d", globalTest(), stacksTest(), n, Options{
		Repos: []string{"/dev/api"},
		Cwd:   "/work",
	})
	if err == nil {
		t.Fatal("expected a refusal: the command line repeats the declared path")
	}
	if strings.Contains(err.Error(), "short name") {
		t.Errorf("error = %q, expected the SAME-PATH message, not the basename collision", err)
	}
	if !strings.Contains(err.Error(), "already declared") {
		t.Errorf("error = %q, expected it to say the path is already declared", err)
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("error = %q, expected it to point at the fixable half", err)
	}
}

func TestResolveWithoutCommandLineReposIsUnchanged(t *testing.T) {
	// The nominal path: no positional, no Cwd, and the declared list is exactly
	// what it was before this feature existed.
	r, err := Resolve("/d", globalTest(), stacksTest(), nestTest(), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := paths(r.Repos); !slices.Equal(got, []string{"/dev/api", "/dev/front"}) {
		t.Errorf("Repos = %v", got)
	}
}

// paths is the Path projection, so a failure prints what a reader recognizes
// rather than a wall of struct literals.
func paths(rs []Repo) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Path)
	}
	return out
}
