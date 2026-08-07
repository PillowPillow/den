package config

import (
	"errors"
	"io/fs"
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
	// The remedy: this is the ONE case a fresh brew/go-install user actually
	// hits (no ~/.den yet), and CLAUDE.md's doctrine is that an error names
	// both the file to fix AND the remedy.
	if !strings.Contains(err.Error(), "den init") {
		t.Errorf("error = %q, expected the `den init` remedy for a missing config.yaml", err.Error())
	}
	// Non-regression: internal/nest and internal/spawn both discriminate
	// their own FileError-wrapped errors on fs.ErrNotExist (see FileError's
	// godoc in file.go); adding the remedy text must not have broken the
	// chain that lets errors.Is see through fmt.Errorf's %w down to the
	// original *fs.PathError.
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false, want true; err = %v", err)
	}
}

// A config.yaml that EXISTS but can't be read must NOT advertise `den init`:
// deninit.Run refuses outright whenever config.yaml is already there (task
// 3), so pointing an unreadable-file error at a command guaranteed to refuse
// would send the user in a circle instead of telling them what's actually
// wrong.
//
// Built as a DIRECTORY named config.yaml inside t.TempDir(), not a
// chmod-000 file: the permission case only fails as an unprivileged user —
// running the suite as root (a real CI/container scenario) would make
// os.ReadFile succeed anyway and silently stop testing anything. EISDIR is
// hermetic under both.
func TestLoadGlobalUnreadableConfigOmitsRemedy(t *testing.T) {
	denHome := t.TempDir()
	if err := os.Mkdir(filepath.Join(denHome, "config.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("expected an error when config.yaml is a directory")
	}
	if strings.Contains(err.Error(), "den init") {
		t.Errorf("error = %q, must NOT suggest `den init` for an unreadable (not absent) config.yaml", err.Error())
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = true, want false: config.yaml exists, it's just unreadable; err = %v", err)
	}
}

// Invalid YAML fails inside DecodeYAMLStrict, past the os.ReadFile wrap this
// task touches — confirms the gate didn't leak the remedy onto a completely
// different failure mode.
func TestLoadGlobalInvalidYamlOmitsRemedy(t *testing.T) {
	_, err := LoadGlobal(writeConfig(t, "agents: [this is not a map"))
	if err == nil {
		t.Fatal("expected an error on invalid YAML")
	}
	if strings.Contains(err.Error(), "den init") {
		t.Errorf("error = %q, must NOT suggest `den init` for invalid YAML — the file exists, it's malformed", err.Error())
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

// Repos maps a personal repo KEY (spec 2026-08-04 §2.4) to a path on THIS
// machine, tilde-expanded at load like every other host path.
func TestLoadGlobalRepoKeys(t *testing.T) {
	denHome := writeConfig(t, validConfig+`repos:
  review-mgmt: ~/dev/review-mgmt
  front-app: /abs/front
`)
	g, err := LoadGlobal(denHome)
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if got := g.Repos["front-app"]; got != "/abs/front" {
		t.Errorf("front-app = %q", got)
	}
	if got := g.Repos["review-mgmt"]; strings.HasPrefix(got, "~") {
		t.Errorf("review-mgmt not expanded: %q", got)
	}
}

// --- D1: Validate() had only ONE caller, doctor.go:59 -----------------------
//
// Consequence measured before the fix: `den spawn`, `den ls`, `den sh` and
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

// config_dir now behaves exactly like worktree_root (TestLoadGlobalDefaultsApplied):
// an ABSENT value — and an explicit "" decodes to the same zero value — is
// defaulted at load time against the CURRENT den home, not literally
// ~/.den/agents/claude. T4-min-4's original concern (an empty config_dir
// reaching the microVM as the empty string in `{config_dir}`) no longer
// applies to this case: the default fills it before the value ever reaches
// FreshnessCommand.
func TestLoadGlobalConfigDirDefaultsToDenHome(t *testing.T) {
	denHome := writeConfig(t, "agents:\n  claude:\n    update: \"claude update\"\n"+
		"defaults:\n  agent: claude\n  stack: devx\n")
	g, err := LoadGlobal(denHome)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(denHome, "agents", "claude"); g.Agents["claude"].ConfigDir != want {
		t.Errorf("ConfigDir = %q, want default %q", g.Agents["claude"].ConfigDir, want)
	}
}

// What T4-min-4 still guards against: a config_dir that's blank but was
// WRITTEN (not merely absent) must still be refused, or it reaches the
// microVM as the empty string in `{config_dir}`. `== ""` in
// LoadGlobalUnvalidated only catches the absent/empty-string case (defaulted
// above); TrimSpace-only whitespace survives that check and must be caught
// here instead.
func TestLoadGlobalRejectsAWhitespaceOnlyConfigDir(t *testing.T) {
	denHome := writeConfig(t, "agents:\n  claude:\n    config_dir: \"   \"\n    update: \"claude update\"\n"+
		"defaults:\n  agent: claude\n  stack: devx\n")
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("expected a rejection of a whitespace-only config_dir, got nil")
	}
	if !strings.Contains(err.Error(), "agents.claude.config_dir: blank") {
		t.Errorf("error = %q, expected the offending key named and the blank wording", err.Error())
	}
	if strings.Contains(err.Error(), "required") {
		t.Errorf("error = %q, must not say \"required\": the field has a default now", err.Error())
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

func TestLoadGlobalExpandsMountHostButNeverLink(t *testing.T) {
	// The ONE property this whole feature rests on: `host` is a host path and
	// den expands it; `link` is a VM path and den must leave it ALONE. A `~`
	// expanded here would become /Users/<me>/... and point nowhere in the VM,
	// which is the exact bug the mounts design exists to fix.
	denHome := writeConfig(t, `
agents:
  claude:
    update: claude update
defaults:
  agent: claude
  stack: devx
mounts:
  - host: ~/.digitaleo
    link: $HOME/.digitaleo
  - host: ~/.aws
    link: ~/.aws
    ro: true
`)
	g, err := LoadGlobalUnvalidated(denHome)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(g.Mounts) != 2 {
		t.Fatalf("got %d mounts, want 2", len(g.Mounts))
	}
	if strings.HasPrefix(g.Mounts[0].Host, "~") {
		t.Errorf("host not expanded: %q", g.Mounts[0].Host)
	}
	if g.Mounts[0].Link != "$HOME/.digitaleo" {
		t.Errorf("link must stay verbatim, got %q", g.Mounts[0].Link)
	}
	// `~` in a link is a VM tilde. Expanding it host-side is the same bug as
	// expanding $HOME, and less obvious, so it gets its own assertion.
	if g.Mounts[1].Link != "~/.aws" {
		t.Errorf("link tilde must stay verbatim, got %q", g.Mounts[1].Link)
	}
	if !g.Mounts[1].RO {
		t.Errorf("ro not decoded")
	}
}

func TestLoadGlobalRejectsUnknownKeyInsideMount(t *testing.T) {
	// Strict decoding is not decorative here: a silent `lnik:` typo would
	// produce a mount that is never linked, and the tool inside the VM would
	// read the wrong path with no error at all (spec §12).
	denHome := writeConfig(t, `
agents:
  claude:
    update: claude update
defaults:
  agent: claude
  stack: devx
mounts:
  - host: /tmp/x
    lnik: /home/agent/x
`)
	if _, err := LoadGlobalUnvalidated(denHome); err == nil {
		t.Fatal("want a load error naming the unknown key, got nil")
	}
}

// Mounts with surrounding whitespace must be trimmed at load time, so that the
// same value is used everywhere: validation and every downstream consumer read
// the trimmed string, and no copy can diverge. A leading space in a link would
// break the `$HOME/` / `~/` prefix that later tasks rely on.
func TestLoadGlobalTrimsMountsHostAndLink(t *testing.T) {
	denHome := writeConfig(t, `
agents:
  claude:
    update: claude update
defaults:
  agent: claude
  stack: devx
mounts:
  - host: "  /tmp/host-with-spaces  "
    link: "  $HOME/.link-with-spaces  "
  - host: "  ~/.tilde-with-spaces  "
    link: "  ~/.tilde-link  "
`)
	g, err := LoadGlobalUnvalidated(denHome)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(g.Mounts) != 2 {
		t.Fatalf("got %d mounts, want 2", len(g.Mounts))
	}
	// First mount: host trimmed and expanded, link trimmed but not expanded.
	if strings.HasPrefix(g.Mounts[0].Host, " ") || strings.HasSuffix(g.Mounts[0].Host, " ") {
		t.Errorf("host not trimmed: %q", g.Mounts[0].Host)
	}
	if g.Mounts[0].Link != "$HOME/.link-with-spaces" {
		t.Errorf("link not trimmed: got %q, want %q", g.Mounts[0].Link, "$HOME/.link-with-spaces")
	}
	// Second mount: host trimmed and expanded, link trimmed.
	if strings.HasPrefix(g.Mounts[1].Host, " ") || strings.HasSuffix(g.Mounts[1].Host, " ") {
		t.Errorf("tilde host not trimmed: %q", g.Mounts[1].Host)
	}
	if g.Mounts[1].Link != "~/.tilde-link" {
		t.Errorf("tilde link not trimmed: got %q, want %q", g.Mounts[1].Link, "~/.tilde-link")
	}
}

// A trailing slash on `link` makes `ln` resolve THROUGH an already-correct
// symlink instead of replacing it: `$HOME/.ssh/` and `$HOME/.ssh` denote the
// same VM path, but only the second one round-trips through `ln -sfn`. Left
// unnormalized, the link phase would refuse on every boot after the first.
func TestLoadGlobalStripsTrailingSlashFromMountLink(t *testing.T) {
	denHome := writeConfig(t, `
agents:
  claude:
    update: claude update
defaults:
  agent: claude
  stack: devx
mounts:
  - host: /tmp/host
    link: $HOME/.ssh/
`)
	g, err := LoadGlobalUnvalidated(denHome)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(g.Mounts) != 1 {
		t.Fatalf("got %d mounts, want 1", len(g.Mounts))
	}
	if g.Mounts[0].Link != "$HOME/.ssh" {
		t.Errorf("trailing slash not stripped: got %q, want %q", g.Mounts[0].Link, "$HOME/.ssh")
	}
}

// An ALL-slashes link must never collapse to "". An empty link is legitimate
// (the env-var consumers), so the emptied value passes validation, the link
// phase filters the mount out, and den reports success for a mount it never
// linked — the silent wrong-path failure the link phase exists to remove. The
// stripping guards its RESULT, not the input's length.
func TestLoadGlobalNeverEmptiesAnAllSlashesMountLink(t *testing.T) {
	for _, link := range []string{"/", "//", "///"} {
		t.Run(link, func(t *testing.T) {
			denHome := writeConfig(t, `
agents:
  claude:
    update: claude update
defaults:
  agent: claude
  stack: devx
mounts:
  - host: /tmp/host
    link: "`+link+`"
`)
			g, err := LoadGlobalUnvalidated(denHome)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if g.Mounts[0].Link != "/" {
				t.Errorf("link %q became %q, want %q", link, g.Mounts[0].Link, "/")
			}
		})
	}
}
