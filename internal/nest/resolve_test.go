package nest

import (
	"reflect"
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

// A key-typed repo's Path is filled from the personal mapping before repo
// selection runs.
func TestResolveRepoKeys(t *testing.T) {
	g := globalTest()
	g.Repos = map[string]string{"api": "/home/u/dev/api"}
	n := &Nest{Name: "n", Stack: "devx", Repos: []Repo{{Key: "api"}}}
	r, err := Resolve("/d", g, stacksTest(), n, Options{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.Repos[0].Path != "/home/u/dev/api" {
		t.Errorf("Path = %q", r.Repos[0].Path)
	}
}

// A key with no local mapping is a refusal BEFORE any side effect, naming
// the file to fix and — when the nest declared one — the clone command.
func TestResolveRepoKeyMissing(t *testing.T) {
	g := globalTest()
	n := &Nest{Name: "n", Stack: "devx",
		Repos: []Repo{{Key: "api", URL: "git@gitlab.corp:a/api.git"}}}
	_, err := Resolve("/d", g, stacksTest(), n, Options{})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"api", "repos:", "config.yaml", "git@gitlab.corp:a/api.git"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
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

// The spec's own §2.4 nest: one required key, one OPTIONAL key, and only the
// required one mapped. Before this, key resolution ran before selection, so
// `optional:` meant something for a `path:` entry (--without could drop a
// missing one) and nothing at all for a `key:` entry — the very axis the
// marker exists for.
func deselectableKeyNest() (*config.Global, *Nest) {
	g := globalTest()
	g.Repos = map[string]string{"review-mgmt": "/dev/review-mgmt"}
	n := &Nest{Name: "backend", Stack: "devx", Repos: []Repo{
		{Key: "review-mgmt"},
		{Key: "front-app", Optional: true, URL: "git@gitlab.corp:front/app.git"},
	}}
	return g, n
}

func TestResolveWithoutEscapesAnUnmappedOptionalKey(t *testing.T) {
	g, n := deselectableKeyNest()
	r, err := Resolve("/d", g, stacksTest(), n, Options{Without: []string{"front-app"}})
	if err != nil {
		t.Fatalf("--without must escape an unmapped OPTIONAL key: %v", err)
	}
	if got := names(r.Repos); len(got) != 1 || got[0] != "review-mgmt" {
		t.Errorf("Repos = %v, expected [review-mgmt]", got)
	}
	if r.Repos[0].Path != "/dev/review-mgmt" {
		t.Errorf("Repos[0].Path = %q, expected the mapped path", r.Repos[0].Path)
	}
}

func TestResolveOnlyEscapesAnUnmappedOptionalKey(t *testing.T) {
	g, n := deselectableKeyNest()
	r, err := Resolve("/d", g, stacksTest(), n, Options{Only: []string{"review-mgmt"}})
	if err != nil {
		t.Fatalf("--only must escape an unmapped OPTIONAL key: %v", err)
	}
	if got := names(r.Repos); len(got) != 1 || got[0] != "review-mgmt" {
		t.Errorf("Repos = %v, expected [review-mgmt]", got)
	}
}

// Fail-loud stays: a key that is still SELECTED refuses, and because this one
// is optional the refusal must hand over the escape rather than leave the
// teammate who simply does not have that repo to guess it.
func TestResolveUnmappedSelectedOptionalKeyNamesWithout(t *testing.T) {
	g, n := deselectableKeyNest()
	_, err := Resolve("/d", g, stacksTest(), n, Options{})
	if err == nil {
		t.Fatal("expected a refusal: front-app is selected and unmapped")
	}
	for _, want := range []string{"front-app", "--without front-app", "git@gitlab.corp:front/app.git"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// A REQUIRED unmapped key is not escapable and must not be told it is:
// --without refuses required repos outright.
func TestResolveUnmappedRequiredKeyDoesNotOfferWithout(t *testing.T) {
	g, n := deselectableKeyNest()
	g.Repos = nil
	_, err := Resolve("/d", g, stacksTest(), n, Options{})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "review-mgmt") {
		t.Errorf("error %q must name the required key", err)
	}
	if strings.Contains(err.Error(), "--without review-mgmt") {
		t.Errorf("error %q offers --without on a REQUIRED repo, which selectRepos refuses", err)
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
	// exactly the one the finding names, `den spawn api ~/dev/api` colliding with an
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

// Two DISTINCT keys are a legal team nest, and the collision is created by
// THIS machine's mapping: both directories are named `api`. Neither existing
// pass sees it — the paths differ, so checkNoDuplicatePaths is silent, and the
// keys differ, so checkUniqueNames is too — yet worktree.Path names the
// worktree directory after that basename, so under -w the two would land on the
// same directory. Left to worktree.Ensure, the refusal comes only after the
// FIRST worktree has been created: exactly the orphan the pre-flight ordering
// exists to prevent.
func TestResolveRefusesTwoKeysMountingTheSameBasename(t *testing.T) {
	g := globalTest()
	g.Repos = map[string]string{"api-v1": "/dev/a/api", "api-v2": "/dev/b/api"}
	n := &Nest{Name: "backend", Stack: "devx", Repos: []Repo{{Key: "api-v1"}, {Key: "api-v2"}}}
	_, err := Resolve("/d", g, stacksTest(), n, Options{})
	if err == nil {
		t.Fatal("expected a refusal: both keys map to a directory named \"api\"")
	}
	// The keys are what the user can act on: the nest names them, and the
	// mapping to fix is `repos:` in config.yaml. The paths alone would leave
	// them to reverse the mapping by hand.
	for _, want := range []string{"api-v1", "api-v2", "/dev/a/api", "/dev/b/api", `"api"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, expected it to name %s", err, want)
		}
	}
	if !strings.Contains(err.Error(), "rename one of the directories") {
		t.Errorf("error = %q, expected it to name the remedy — den refuses rather than "+
			"normalizing, so the refusal owes the user a way out", err)
	}
	// The diagnosis must rest on what den itself controls and can verify —
	// worktree.Path. An earlier draft also blamed the `sbx create` workspaces:
	// den passes HOST paths there, so what becomes of two distinct ones inside
	// the VM is a claim about sbx's mount behavior that nothing has measured,
	// and under §14.1's open hypothesis A11 they would not collide at all.
	for _, forbidden := range []string{"sbx", "workspace"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("error = %q, must not assert %q in a user-facing refusal: sbx behavior "+
				"is a hypothesis to document, never an assertion", err, forbidden)
		}
	}
}

// The mixed pair: a key whose mapped directory collides with a basename
// declared as a plain path in the same nest.
func TestResolveRefusesAKeyMountingTheBasenameOfADeclaredPath(t *testing.T) {
	g := globalTest()
	g.Repos = map[string]string{"front": "/dev/x/api"}
	n := &Nest{Name: "backend", Stack: "devx", Repos: []Repo{{Key: "front"}, {Path: "/dev/y/api"}}}
	_, err := Resolve("/d", g, stacksTest(), n, Options{})
	if err == nil {
		t.Fatal("expected a refusal: `front` maps to /dev/x/api, colliding with /dev/y/api")
	}
	for _, want := range []string{"front", "/dev/x/api", "/dev/y/api", `"api"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, expected it to name %s", err, want)
		}
	}
}

// The same hole reached from the command line: the positional is merged into
// the list before the pass runs, so it is judged like any other repo.
func TestResolveRefusesAKeyMountingTheBasenameOfACommandLineRepo(t *testing.T) {
	g := globalTest()
	g.Repos = map[string]string{"front": "/dev/x/api"}
	n := &Nest{Name: "backend", Stack: "devx", Repos: []Repo{{Key: "front"}}}
	_, err := Resolve("/d", g, stacksTest(), n, Options{
		Repos: []string{"/dev/y/api"},
		Cwd:   "/work",
	})
	if err == nil {
		t.Fatal("expected a refusal: the positional's basename collides with `front`'s mapped directory")
	}
	for _, want := range []string{"front", "/dev/x/api", "/dev/y/api", "command line", `"api"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, expected it to name %s", err, want)
		}
	}
}

// The refusal is on the BASENAME, not on having two keys: the ordinary team
// nest must still resolve. A pass that refused any two key-typed repos would
// pass the tests above and break every real nest.
func TestResolveAcceptsTwoKeysMountingDistinctBasenames(t *testing.T) {
	g := globalTest()
	g.Repos = map[string]string{"api": "/dev/a/api", "web": "/dev/b/web"}
	n := &Nest{Name: "backend", Stack: "devx", Repos: []Repo{{Key: "api"}, {Key: "web"}}}
	r, err := Resolve("/d", g, stacksTest(), n, Options{})
	if err != nil {
		t.Fatalf("two keys mounting distinct directories must resolve: %v", err)
	}
	if got := paths(r.Repos); !slices.Equal(got, []string{"/dev/a/api", "/dev/b/web"}) {
		t.Errorf("Repos = %v, expected both mapped paths", got)
	}
}

// Precedence: two keys pointing at the SAME directory are a basename collision
// too, but "declared twice" is the sharper diagnosis — the basename message
// would send the reader hunting for a second, distinct directory that does not
// exist. The new pass must therefore run after checkNoDuplicatePaths, not
// instead of it.
func TestResolveKeysOnTheSamePathKeepTheDuplicatePathMessage(t *testing.T) {
	g := globalTest()
	g.Repos = map[string]string{"api-v1": "/dev/a/api", "api-v2": "/dev/a/api"}
	n := &Nest{Name: "backend", Stack: "devx", Repos: []Repo{{Key: "api-v1"}, {Key: "api-v2"}}}
	_, err := Resolve("/d", g, stacksTest(), n, Options{})
	if err == nil {
		t.Fatal("expected a refusal: both keys map to the same directory")
	}
	if !strings.Contains(err.Error(), "declared twice") {
		t.Errorf("error = %q, expected the SAME-PATH message", err)
	}
	if strings.Contains(err.Error(), "both mount a directory named") {
		t.Errorf("error = %q, expected the duplicate-path diagnosis to win over the basename one", err)
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

// resolveWithSSH resolves globalTest/stacksTest/nestTest with only ssh.mode
// and ssh.dir overridden — the minimal fixture the sugar-desugaring tests need.
func resolveWithSSH(t *testing.T, mode, dir string) *Resolved {
	t.Helper()
	g := globalTest()
	g.SSH = config.SSH{Mode: mode, Dir: dir}
	r, err := Resolve("/d", g, stacksTest(), nestTest(), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return r
}

// resolveWithMounts resolves the same fixture with only `mounts:` overridden.
func resolveWithMounts(t *testing.T, mounts []config.Mount) *Resolved {
	t.Helper()
	g := globalTest()
	g.Mounts = mounts
	r, err := Resolve("/d", g, stacksTest(), nestTest(), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return r
}

// resolveWithMountsAndSSH combines both overrides, to assert their order.
func resolveWithMountsAndSSH(t *testing.T, mounts []config.Mount, mode, dir string) *Resolved {
	t.Helper()
	g := globalTest()
	g.Mounts = mounts
	g.SSH = config.SSH{Mode: mode, Dir: dir}
	r, err := Resolve("/d", g, stacksTest(), nestTest(), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return r
}

func TestResolveDesugarsSSHMountIntoAMount(t *testing.T) {
	// `ssh.mode: mount` must produce a mount INDISTINGUISHABLE from a
	// hand-written one except for Key. That equivalence is the whole
	// justification for deleting mount mode's private code path: if the two
	// diverge, there are two mechanisms again and the drift has somewhere to
	// hide.
	r := resolveWithSSH(t, "mount", "/host/ssh_sbx")
	want := []Mount{{
		Host: "/host/ssh_sbx",
		Link: "$HOME/.ssh",
		RO:   false, // ssh writes known_hosts; a read-only mount breaks it obscurely
		Key:  "ssh.dir",
	}}
	if !reflect.DeepEqual(r.Mounts, want) {
		t.Fatalf("got %+v, want %+v", r.Mounts, want)
	}
}

func TestResolveAddsNoMountOutsideSSHMountMode(t *testing.T) {
	for _, mode := range []string{"agent-forward", "none"} {
		t.Run(mode, func(t *testing.T) {
			// ssh.dir is DECLARED in both, so "this mode adds nothing" stays
			// distinguishable from "there was nothing to add".
			r := resolveWithSSH(t, mode, "/host/ssh_sbx")
			if len(r.Mounts) != 0 {
				t.Fatalf("mode %q: got %+v, want no mounts", mode, r.Mounts)
			}
		})
	}
}

func TestResolveKeepsConfigMountsAndKeysThemByIndex(t *testing.T) {
	// The index in Key is what lets a refusal name the line the user wrote.
	r := resolveWithMounts(t, []config.Mount{
		{Host: "/host/a", Link: "$HOME/a"},
		{Host: "/host/b", RO: true},
	})
	want := []Mount{
		{Host: "/host/a", Link: "$HOME/a", Key: "mounts[0]"},
		{Host: "/host/b", RO: true, Key: "mounts[1]"},
	}
	if !reflect.DeepEqual(r.Mounts, want) {
		t.Fatalf("got %+v, want %+v", r.Mounts, want)
	}
}

func TestResolveOrdersConfigMountsBeforeTheSSHSugar(t *testing.T) {
	// Deterministic order, asserted rather than assumed: it decides the
	// workspace argv order in Task 6 and the link order in Task 4, and a map
	// iteration creeping in later would make both flap.
	r := resolveWithMountsAndSSH(t,
		[]config.Mount{{Host: "/host/a", Link: "$HOME/a"}},
		"mount", "/host/ssh_sbx")
	if len(r.Mounts) != 2 || r.Mounts[0].Key != "mounts[0]" || r.Mounts[1].Key != "ssh.dir" {
		t.Fatalf("got %+v, want mounts[0] then ssh.dir", r.Mounts)
	}
}
