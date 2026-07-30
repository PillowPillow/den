package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig creates a temporary DEN_HOME containing the given config.yaml content.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const fullConfig = `
agents:
  claude:
    config_dir: ~/.den/agents/claude
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }
    bin_dirs: ["$HOME/.local/bin", "$HOME/.claude/local"]
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: mount
  dir: ~/.ssh_sbx
worktree_layout: per-repo
worktree_root: ~/perso/wt
egress:
  - api.anthropic.com
  - github.com
`

// validConfig is the smallest config.yaml that Validate accepts. Rejection
// tests concatenate it with the single fault they're testing, so a failure
// points at that fault and not at some unrelated gap.
const validConfig = `agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`

func TestLoadGlobalFullFields(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	g, err := LoadGlobal(writeConfig(t, fullConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a, ok := g.Agents["claude"]
	if !ok {
		t.Fatal("agent claude missing from registry")
	}
	if want := filepath.Join(home, ".den/agents/claude"); a.ConfigDir != want {
		t.Errorf("ConfigDir = %q, want %q (tilde expanded)", a.ConfigDir, want)
	}
	if a.Update != "claude update" {
		t.Errorf("Update = %q, want %q", a.Update, "claude update")
	}
	// $HOME must cross unchanged: it's resolved inside the VM.
	if len(a.BinDirs) != 2 || a.BinDirs[0] != "$HOME/.local/bin" {
		t.Errorf("BinDirs = %v, want $HOME preserved", a.BinDirs)
	}
	if a.Env["CLAUDE_CONFIG_DIR"] != "{config_dir}" {
		t.Errorf("Env = %v, want the {config_dir} placeholder intact", a.Env)
	}
	if g.Defaults.Agent != "claude" || g.Defaults.Stack != "devx" {
		t.Errorf("Defaults = %+v", g.Defaults)
	}
	if g.SSH.Mode != "mount" {
		t.Errorf("SSH.Mode = %q, want mount", g.SSH.Mode)
	}
	if want := filepath.Join(home, ".ssh_sbx"); g.SSH.Dir != want {
		t.Errorf("SSH.Dir = %q, want %q", g.SSH.Dir, want)
	}
	if g.WorktreeLayout != "per-repo" {
		t.Errorf("WorktreeLayout = %q", g.WorktreeLayout)
	}
	if want := filepath.Join(home, "perso/wt"); g.WorktreeRoot != want {
		t.Errorf("WorktreeRoot = %q, want %q", g.WorktreeRoot, want)
	}
	if len(g.Egress) != 2 {
		t.Errorf("Egress = %v, want 2 entries", g.Egress)
	}
}

// Defaults apply BEFORE the consistency check: a config.yaml without
// `worktree_layout:` or `ssh:` is perfectly valid, and LoadGlobal — which now
// validates — must not reject it on the grounds that these fields are empty.
func TestLoadGlobalDefaultsApplied(t *testing.T) {
	denHome := writeConfig(t, validConfig)
	g, err := LoadGlobal(denHome)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.SSH.Mode != "agent-forward" {
		t.Errorf("SSH.Mode = %q, want default agent-forward", g.SSH.Mode)
	}
	if g.WorktreeLayout != "central" {
		t.Errorf("WorktreeLayout = %q, want default central", g.WorktreeLayout)
	}
	// The default is relative to the CURRENT den home, not literally
	// ~/.den/worktrees: on a temporary DEN_HOME, worktrees must stay under
	// that home.
	if want := filepath.Join(denHome, "worktrees"); g.WorktreeRoot != want {
		t.Errorf("WorktreeRoot = %q, want default %q", g.WorktreeRoot, want)
	}
}

func TestLoadGlobalMissingFile(t *testing.T) {
	denHome := t.TempDir()
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("expected an error when config.yaml is missing")
	}
	// The message must be actionable: it names the path that was searched.
	path := filepath.Join(denHome, "config.yaml")
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, expected the full path of the missing file", err.Error())
	}
	// Exactly once: the OS's *fs.PathError already carries the path this
	// wrapper just named. This is the first line a user with no ~/.den yet sees.
	if n := strings.Count(err.Error(), path); n != 1 {
		t.Errorf("path appears %d times, want 1; message: %s", n, err.Error())
	}
	// And in den's own wording: FileError replaces the OS's raw reason, it
	// does not let it leak through.
	if strings.Contains(err.Error(), "no such file or directory") {
		t.Errorf("the raw OS reason must not leak: %s", err.Error())
	}
}

func TestLoadGlobalInvalidYaml(t *testing.T) {
	if _, err := LoadGlobal(writeConfig(t, "agents: [this is not a map")); err == nil {
		t.Fatal("expected an error on invalid YAML")
	}
}

// A misspelled key must be an error, never a silence: `egres:` leaves the
// allowlist empty, and the sandbox stops reaching api.anthropic.com without
// anything reporting it. That's exactly what `den doctor` must catch.
func TestLoadGlobalRejectsAnUnknownKey(t *testing.T) {
	denHome := writeConfig(t, "defaults:\n  agent: claude\n  stack: devx\negres:\n  - api.anthropic.com\n")
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("expected an error on the unknown key `egres`")
	}
	if !strings.Contains(err.Error(), "egres") {
		t.Errorf("error = %q, expected a mention of the offending key", err.Error())
	}
	if !strings.Contains(err.Error(), filepath.Join(denHome, "config.yaml")) {
		t.Errorf("error = %q, expected the path of the offending file", err.Error())
	}
}

// An empty config.yaml is a config that declares nothing, not a corrupt file:
// Validate() will say plainly what's missing. yaml.v3 returns io.EOF on an
// empty file, which must never surface as-is to the user.
//
// The subject is LoadGlobalUnvalidated: it carries the "reading is not
// judging" contract that `den doctor` relies on to show everything at once.
// LoadGlobal, on the other hand, must refuse — both halves are checked here
// because they only make sense together.
func TestLoadGlobalUnvalidatedEmptyFile(t *testing.T) {
	denHome := writeConfig(t, "")
	g, err := LoadGlobalUnvalidated(denHome)
	if err != nil {
		t.Fatalf("an empty config.yaml must not be a load error: %v", err)
	}
	if g.SSH.Mode != "agent-forward" {
		t.Errorf("SSH.Mode = %q, expected the default applied even on an empty file", g.SSH.Mode)
	}
	if errs := g.Validate(); len(errs) == 0 {
		t.Error("expected Validate to flag an empty config")
	}
	if _, err := LoadGlobal(denHome); err == nil {
		t.Error("LoadGlobal must refuse an empty config.yaml, or validation stays optional")
	}
}

// --- D1: Validate() had only ONE caller, doctor.go:59 -----------------------
//
// Consequence measured before the fix: `den <nest>`, `den ls`, `den sh` and
// `den rm` loaded without ever validating. The three tests below lock down
// the three regressions that allowed.

// 14th hostile configuration (T10): `centrl` silently fell back to `central`
// — LoadGlobal only defaults the empty string, and nobody called Validate on
// this path. A typo would therefore change the worktree layout without a word.
func TestLoadGlobalRejectsAnUnknownWorktreeLayout(t *testing.T) {
	denHome := writeConfig(t, validConfig+"worktree_layout: centrl\n")
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("expected a rejection of `worktree_layout: centrl`, got nil")
	}
	if !strings.Contains(err.Error(), "centrl") {
		t.Errorf("error = %q, expected the offending value named", err.Error())
	}
	if !strings.Contains(err.Error(), filepath.Join(denHome, "config.yaml")) {
		t.Errorf("error = %q, expected the full path of the file to fix", err.Error())
	}
}

// T4-min-4: an empty `config_dir` becomes the empty string in `{config_dir}`
// and REACHES the microVM. Validate already forbade it; the spawn path didn't call it.
func TestLoadGlobalRejectsAnEmptyConfigDir(t *testing.T) {
	denHome := writeConfig(t, "agents:\n  claude:\n    config_dir: \"\"\n    update: \"claude update\"\n"+
		"defaults:\n  agent: claude\n  stack: devx\n")
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("expected a rejection of an empty config_dir, got nil")
	}
	if !strings.Contains(err.Error(), "agents.claude.config_dir") {
		t.Errorf("error = %q, expected the offending key named", err.Error())
	}
}

// Refusing at load time must not degrade the diagnostic to "first fault
// found": the user must see everything to fix at once, exactly like
// `den doctor`. Two independent faults, both named.
func TestLoadGlobalAccumulatesAllErrors(t *testing.T) {
	denHome := writeConfig(t, validConfig+"ssh:\n  mode: nfs\nworktree_layout: centrl\n")
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("expected a rejection, got nil")
	}
	for _, want := range []string{"nfs", "centrl"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, expected a mention of %q: LoadGlobal must accumulate, not stop at the first fault",
				err.Error(), want)
		}
	}
}
