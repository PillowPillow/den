package doctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/manifest"
	"github.com/PillowPillow/den/internal/sshagent"
)

// okDeps is FakeDeps under its short local name: 19 call sites in this file
// read easier without the package prefix on a receiver already inside
// package doctor.
func okDeps() Deps {
	return FakeDeps()
}

func validDenHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.yaml", `
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`)
	write("stacks/devx/stack.yaml", "image: devx:v1\n")
	write("nests/api.yaml", "stack: devx\nrepos:\n  - { path: /dev/api }\n")
	return dir
}

// find returns the first check whose name or detail contains fragment.
func find(checks []Check, fragment string) (Check, bool) {
	for _, c := range checks {
		if strings.Contains(c.Name, fragment) || strings.Contains(c.Detail, fragment) {
			return c, true
		}
	}
	return Check{}, false
}

// findName reports whether a check with this EXACT name exists: unlike find,
// a substring is not enough ("config" must not be satisfied by "config.yaml").
func findName(checks []Check, name string) bool {
	for _, c := range checks {
		if c.Name == name {
			return true
		}
	}
	return false
}

// findExactName returns the Check with exactly this name. Unlike find, no
// substring match counts: "git" must not be served by some other check whose
// name merely contains the word.
func findExactName(checks []Check, name string) (Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func allOK(checks []Check) bool {
	for _, c := range checks {
		if c.Level != LevelOK {
			return false
		}
	}
	return true
}

func TestRunHealthyConfig(t *testing.T) {
	checks := Run(validDenHome(t), okDeps())
	if len(checks) == 0 {
		t.Fatal("no check ran")
	}
	if !allOK(checks) {
		t.Errorf("expected every check OK, got %+v", checks)
	}
	// allOK alone would pass with a single trivial check: verify, by exact
	// name (find would false-positive: "config" is a substring of
	// "config.yaml"), that every expected diagnostic actually ran.
	for _, name := range []string{"sbx", "config.yaml", "config", "stacks", "defaults.stack", "nests"} {
		if !findName(checks, name) {
			t.Errorf("no check named %q, got %+v", name, checks)
		}
	}
}

func TestRunSbxMissing(t *testing.T) {
	d := okDeps()
	d.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	checks := Run(validDenHome(t), d)
	c, ok := find(checks, "sbx")
	if !ok {
		t.Fatal("no check concerns sbx")
	}
	if !c.Blocking() {
		t.Error("the sbx check should fail when the binary is missing")
	}
	if allOK(checks) {
		t.Error("Run must not report all-OK when sbx is missing")
	}
}

func TestRunMissingConfig(t *testing.T) {
	checks := Run(t.TempDir(), okDeps())
	if allOK(checks) {
		t.Error("expected a failure when config.yaml is missing")
	}
	c, ok := findExactName(checks, "config.yaml")
	if !ok {
		t.Fatal("the failing check should name config.yaml")
	}
	// doctor.go's config.yaml check is `add("config.yaml", false, "%v", err)` —
	// a bare pass-through of LoadGlobalUnvalidated's error. This is the
	// acceptance case: an empty --den-home is exactly what a fresh
	// brew/go-install user's FIRST `den doctor` sees, so the remedy must
	// reach this line, not just LoadGlobal's own error in config_test.go.
	if !strings.Contains(c.Detail, "den init") {
		t.Errorf("detail = %q, expected the `den init` remedy", c.Detail)
	}
}

func TestRunUnknownDefaultStack(t *testing.T) {
	dir := validDenHome(t)
	// remove the devx stack referenced by defaults.stack
	if err := os.RemoveAll(filepath.Join(dir, "stacks", "devx")); err != nil {
		t.Fatal(err)
	}
	checks := Run(dir, okDeps())
	if allOK(checks) {
		t.Error("expected a failure when defaults.stack does not exist")
	}
	if _, ok := find(checks, "devx"); !ok {
		t.Error("the failing check should name the missing stack")
	}
}

func TestRunNestRepoNotFound(t *testing.T) {
	d := okDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/dev/api" {
			return nil, errors.New("not found")
		}
		return FakeDirInfo(), nil
	}
	checks := Run(validDenHome(t), d)
	if allOK(checks) {
		t.Error("expected a failure when a nest's repo does not exist")
	}
	if _, ok := find(checks, "/dev/api"); !ok {
		t.Error("the failing check should name the missing repo")
	}
}

// --- key-typed repos (repos: entries resolved via config.yaml's `repos:` map) ---
//
// nest.LoadNest leaves Repo.Path empty for a `key:` entry — only nest.Resolve
// fills it, by looking the key up in Global.Repos (internal/nest/resolve.go).
// doctor does not call Resolve; it must do the same lookup itself against g,
// already in scope, or a key-typed repo gets Stat-ed on its blank Path and
// reported as "repo not found: " — a message that names nothing.

// keyRepoDenHome builds a den home whose only nest ("api") declares a single
// key-typed repo. An empty mappedPath omits the `repos:` mapping entirely,
// to exercise the unmapped-key case.
func keyRepoDenHome(t *testing.T, key, mappedPath string) string {
	t.Helper()
	dir := validDenHome(t)
	reposBlock := ""
	if mappedPath != "" {
		reposBlock = fmt.Sprintf("repos:\n  %s: %s\n", key, mappedPath)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`+reposBlock), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nests", "api.yaml"),
		[]byte(fmt.Sprintf("stack: devx\nrepos:\n  - { key: %s }\n", key)), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A key mapped to a path that exists must report nothing. The Stat double
// fails on the EMPTY path and on it alone (same technique as
// TestRunIgnoresAnEmptyEntryInKits): with an always-OK okDeps a check that
// never resolved the key and left Path blank would pass this test unnoticed,
// since Stat("") would also return nil.
func TestRunKeyRepoMappedAndPresent(t *testing.T) {
	dir := keyRepoDenHome(t, "myrepo", "/dev/myrepo")
	d := okDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "" {
			return nil, errors.New("empty path")
		}
		return FakeDirInfo(), nil
	}
	checks := Run(dir, d)
	if !allOK(checks) {
		t.Errorf("a mapped, present key repo must report nothing; checks: %+v", checks)
	}
}

// A key mapped to a path that does NOT exist: the message must name BOTH the
// key and the mapped path, so the user knows which mapping (config.yaml's
// `repos:` entry) points at the wrong place.
func TestRunKeyRepoMappedButMissing(t *testing.T) {
	dir := keyRepoDenHome(t, "myrepo", "/dev/myrepo")
	d := okDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/dev/myrepo" {
			return nil, errors.New("not found")
		}
		return FakeDirInfo(), nil
	}
	checks := Run(dir, d)
	if allOK(checks) {
		t.Error("expected a failure when a mapped key's path does not exist")
	}
	c, ok := find(checks, "myrepo")
	if !ok {
		t.Fatal("the failing check should name the key")
	}
	if !c.Blocking() {
		t.Errorf("a missing mapped repo must be a failure; got %+v", c)
	}
	if !strings.Contains(c.Detail, "/dev/myrepo") {
		t.Errorf("detail = %q, must also name the mapped path — otherwise the user "+
			"can't tell which mapping is wrong", c.Detail)
	}
}

// A key with no mapping at all: the message must name the key and the
// remedy — add it under `repos:` in the global config — in the same wording
// family as nest.Resolve's own unmapped-key refusal (resolveRepoKeys in
// internal/nest/resolve.go).
func TestRunKeyRepoUnmapped(t *testing.T) {
	dir := keyRepoDenHome(t, "myrepo", "") // no `repos:` mapping at all
	checks := Run(dir, okDeps())
	if allOK(checks) {
		t.Error("expected a failure when a key repo has no mapping")
	}
	c, ok := find(checks, "myrepo")
	if !ok {
		t.Fatal("the failing check should name the unmapped key")
	}
	if !c.Blocking() {
		t.Errorf("an unmapped key repo must be a failure; got %+v", c)
	}
	if !strings.Contains(c.Detail, "repos:") {
		t.Errorf("detail = %q, must name the remedy (`repos:` in config.yaml)", c.Detail)
	}
	if !strings.Contains(c.Detail, config.GlobalPath(dir)) {
		t.Errorf("detail = %q, must name the file to fix, like nest.Resolve's own "+
			"unmapped-key refusal does", c.Detail)
	}
}

// An unmapped key whose repo entry also declares `url:` must surface the
// clone hint, exactly like resolveRepoKeys's own refusal does — the entry
// exists precisely to point at the clone command an unmapped key most likely
// needs next.
func TestRunKeyRepoUnmappedNamesTheCloneURL(t *testing.T) {
	dir := keyRepoDenHome(t, "myrepo", "")
	if err := os.WriteFile(filepath.Join(dir, "nests", "api.yaml"),
		[]byte("stack: devx\nrepos:\n  - { key: myrepo, url: git@example.com:acme/myrepo.git }\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	checks := Run(dir, okDeps())
	c, ok := find(checks, "myrepo")
	if !ok {
		t.Fatal("the failing check should name the unmapped key")
	}
	if !strings.Contains(c.Detail, "git@example.com:acme/myrepo.git") {
		t.Errorf("detail = %q, must carry the clone hint from `url:`", c.Detail)
	}
}

// writeNestFile replaces the den home's "api" nest, so a test states the nest
// it is about instead of the fixture's default one.
func writeNestFile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "nests", "api.yaml"),
		[]byte("stack: devx\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The nest the mode exists for: `select: prompt`, and two optional keys this
// machine maps nowhere. That is a CORRECTLY configured machine — the nest
// declares a team's catalogue, this session works on neither repo — and
// `den doctor` used to exit 1 on it, one FAIL per key, drowning the real
// problems in a nest that is not broken.
//
// allOK is the assertion the exit code follows: no check may be blocking, and
// none may be a warning either, or `den doctor` ends on "review the [warn]
// lines" for a machine with nothing to review.
func TestRunAnUnmappedOptionalKeyIsNormalOnAPromptingNest(t *testing.T) {
	dir := keyRepoDenHome(t, "crm", "") // no `repos:` mapping at all
	writeNestFile(t, dir, "select: prompt\nrepos:\n"+
		"  - { key: crm, optional: true, url: git@example.com:acme/crm.git }\n"+
		"  - { key: dwh, optional: true }\n")

	checks := Run(dir, okDeps())
	if !allOK(checks) {
		t.Errorf("a prompting nest whose optional keys are unmapped is healthy; checks: %+v", checks)
	}
	c, ok := findExactName(checks, "nest api")
	if !ok {
		t.Fatalf("the state must still be REPORTED, not merely tolerated; checks: %+v", checks)
	}
	// Every key by name, and the file that would map them: a count alone
	// ("2 keys unmapped") tells a reader nothing they can act on.
	for _, want := range []string{"crm", "dwh", "select: prompt", config.GlobalPath(dir)} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, must name %q", c.Detail, want)
		}
	}
	// ONE line for the nest, not one per key: at thirty repos, twenty-six
	// diagnostics are the noise this fix removes, whatever their level.
	count := 0
	for _, ch := range checks {
		if ch.Name == "nest api" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected one aggregated line for the nest, got %d: %+v", count, checks)
	}
}

// The regression floor, both halves of it. `select: prompt` governs the
// OPTIONAL repos of a nest and nothing else: everywhere else an unmapped key is
// the fault it always was, and doctor must still fail on it.
func TestRunUnmappedKeyStaysAFailureOutsideTheOptionalPromptingCase(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
	}{
		// The historical shape: no `select:` at all, so every optional repo IS
		// meant to be mounted and an unmapped key breaks that spawn.
		{"an optional key on a select: all nest", "repos:\n  - { key: crm, optional: true }\n"},
		// A required repo is mounted by every spawn of the nest, prompting or
		// not: no checklist can decline it.
		{"a required key on a prompting nest", "select: prompt\nrepos:\n  - { key: crm }\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := keyRepoDenHome(t, "crm", "")
			writeNestFile(t, dir, c.body)

			checks := Run(dir, okDeps())
			found, ok := find(checks, "crm")
			if !ok {
				t.Fatalf("the unmapped key must be reported; checks: %+v", checks)
			}
			if !found.Blocking() {
				t.Errorf("this unmapped key is a real fault and must fail; got %+v", found)
			}
		})
	}
}

// TestRunReportsABrokenNestWithoutHidingOthers locks down that a broken nest
// must neither hide doctor's nests section nor prevent the REAL diagnosis of
// the other nests. The "healthy" nest points at a missing repo, with a rigged
// Stat that fails on that one path only: with an always-OK Deps, a nest with
// no anomaly produces no individual check, so its mere presence in the list
// would prove nothing — a genuine detected anomaly is needed to prove it was
// actually diagnosed, not just listed.
func TestRunReportsABrokenNestWithoutHidingOthers(t *testing.T) {
	dir := validDenHome(t) // valid config, devx stack, "api" nest
	if err := os.WriteFile(filepath.Join(dir, "nests", "healthy.yaml"),
		[]byte("stack: devx\nrepos:\n  - { path: /dev/healthy-missing }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nests", "broken.yaml"), []byte("egres: [x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := okDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/dev/healthy-missing" {
			return nil, errors.New("not found")
		}
		return FakeDirInfo(), nil
	}

	checks := Run(dir, d)

	var sawBroken, sawHealthyDiagnosed, sawNestsFailing bool
	for _, c := range checks {
		if c.Name == "nest broken" && c.Blocking() {
			sawBroken = true
		}
		if c.Name == "nest healthy" && c.Blocking() && strings.Contains(c.Detail, "/dev/healthy-missing") {
			sawHealthyDiagnosed = true
		}
		if c.Name == "nests" && c.Blocking() {
			sawNestsFailing = true
		}
	}
	if !sawBroken {
		t.Errorf("the broken nest must be reported as a failure; checks: %+v", checks)
	}
	if !sawHealthyDiagnosed {
		t.Errorf("the healthy nest must still be genuinely diagnosed (not just listed); checks: %+v", checks)
	}
	if !sawNestsFailing {
		t.Errorf("the summary 'nests' check must fail when there are broken nests; checks: %+v", checks)
	}
}

// config.LoadGlobal REJECTS an inconsistent configuration. If doctor went
// through it, it would stop at load (doctor.go returns `checks` as soon as
// loading fails) and never reach its own validation — the user would see one
// error line instead of the full list, and nothing about stacks or nests.
// This test locks down the opposite: doctor must load WITHOUT validating,
// accumulate every fault, and continue.
func TestRunAccumulatesConfigErrorsAndContinues(t *testing.T) {
	dir := validDenHome(t)
	// Two independent faults in an otherwise complete den home (devx stack
	// and api nest present): diagnosing both proves Run got past the config.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: nfs
worktree_layout: centrl
`), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := Run(dir, okDeps())

	for _, want := range []string{"nfs", "centrl"} {
		if _, ok := find(checks, want); !ok {
			t.Errorf("no check mentions %q: doctor must show ALL faults at once; checks: %+v",
				want, checks)
		}
	}
	// And it must have continued past the config: without that, a
	// validating load would have truncated the diagnosis without any
	// assertion above catching it.
	for _, name := range []string{"stacks", "defaults.stack", "nests"} {
		if !findName(checks, name) {
			t.Errorf("no check named %q: a faulty config must not interrupt the diagnosis; checks: %+v",
				name, checks)
		}
	}
}

// A missing kit path was never checked, on either `kit:` or `kits:`. The sbx
// dispatcher fails LATE — `exit $rc` at microVM boot — so the user saw a VM
// dying at startup, never a den message. doctor already checked nest repos;
// kits follow the same pattern, with the same injected d.Stat.
func TestRunStackKitNotFound(t *testing.T) {
	dir := validDenHome(t)
	// both `kits:` (plural) and `kit:` (singular) must be checked, not just
	// whichever one already had a test.
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nkits: [transverse]\nkit: devx-kit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// LoadStack returns kit paths made absolute, relative to the stack's directory.
	singularKit := filepath.Join(dir, "stacks", "devx", "devx-kit")
	pluralKit := filepath.Join(dir, "stacks", "devx", "transverse")

	d := okDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == singularKit || p == pluralKit {
			return nil, errors.New("not found")
		}
		return FakeDirInfo(), nil
	}

	checks := Run(dir, d)

	// The FULL path, so the message is actionable without guessing the root
	// the kit was relative to.
	for _, path := range []string{singularKit, pluralKit} {
		c, ok := find(checks, path)
		if !ok {
			t.Errorf("no check names the missing kit %s; checks: %+v", path, checks)
			continue
		}
		if !c.Blocking() {
			t.Errorf("the check naming %s must be a failure; got %+v", path, c)
		}
	}
	// And the stack declaring them must be named: with several stacks, a
	// bare path doesn't say which stack.yaml to fix.
	if !findName(checks, "stack devx") {
		t.Errorf("no check named %q: the message must name the offending stack; checks: %+v",
			"stack devx", checks)
	}
}

// A present kit must produce NO failure: without this case, a check that
// rejected every kit would pass the test above unnoticed.
func TestRunPresentKitsReportNothing(t *testing.T) {
	dir := validDenHome(t)
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nkits: [transverse]\nkit: devx-kit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	checks := Run(dir, okDeps()) // okDeps: every path exists
	if !allOK(checks) {
		t.Errorf("expected every check OK when the kits exist, got %+v", checks)
	}
}

// --- mounts[].host ----------------------------------------------------
//
// Same probe as ssh.dir, same reason: Validate() only judges "declared or
// not"; "declared but missing on disk" needs a filesystem probe, and that's
// the very path `sbx create` mounts into the sandbox.

// mountDenHome builds a den home whose config.yaml declares one `mounts:`
// entry per host, in order — so a test can put more than one entry under
// `mounts:` and check that doctor's index tracks POSITION, not the count of
// entries it actually reports on. Modeled on keyRepoDenHome above: same
// technique (rewrite config.yaml wholesale over validDenHome's base), applied
// to `mounts:` instead of a key-typed repo.
func mountDenHome(t *testing.T, hosts ...string) string {
	t.Helper()
	dir := validDenHome(t)
	var entries strings.Builder
	for _, h := range hosts {
		fmt.Fprintf(&entries, "  - host: %s\n    link: $HOME/x\n", h)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(fmt.Sprintf(`
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
mounts:
%s`, entries.String())), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDoctorReportsMissingMountHost(t *testing.T) {
	dir := mountDenHome(t, "/nope/missing")
	d := okDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/nope/missing" {
			return nil, errors.New("not found")
		}
		return FakeDirInfo(), nil
	}
	checks := Run(dir, d)
	line, ok := findExactName(checks, "mounts[0]")
	if !ok {
		t.Fatalf("no check named \"mounts[0]\"; checks: %+v", checks)
	}
	if line.Level == LevelOK {
		t.Fatal("a missing mount host must be reported as not-ok")
	}
	if !strings.Contains(line.Detail, "/nope/missing") {
		t.Fatalf("the detail must name the path, got %q", line.Detail)
	}
}

// A mount host that EXISTS but is a regular FILE. den mounts directories, and
// `host: ~/.digitaleo/config.yaml` — the file instead of the directory holding
// it — is a plausible typo that a bare Stat accepts. The line must say what is
// actually wrong: "not found" is a false statement about a file that exists.
func TestDoctorReportsAMountHostThatIsAFile(t *testing.T) {
	dir := mountDenHome(t, "/dev/mounted/config.yaml")
	d := okDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/dev/mounted/config.yaml" {
			return FakeFileInfo(), nil
		}
		return FakeDirInfo(), nil
	}
	checks := Run(dir, d)
	line, ok := findExactName(checks, "mounts[0]")
	if !ok {
		t.Fatalf("no check named \"mounts[0]\"; checks: %+v", checks)
	}
	if line.Level == LevelOK {
		t.Fatal("den mounts directories: a file host must not report OK")
	}
	if !strings.Contains(line.Detail, "not a directory") {
		t.Errorf("the detail must say what is wrong; got %q", line.Detail)
	}
	if strings.Contains(line.Detail, "not found") {
		t.Errorf("\"not found\" is false of a file that exists; got %q", line.Detail)
	}
}

// The same probe on ssh.dir, whose text says "this directory" too. Fixed
// alongside its twin rather than after it: leaving the identical hole beside a
// fixed one is how the two drift.
func TestDoctorReportsAnSSHDirThatIsAFile(t *testing.T) {
	dir := validDenHome(t)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: mount
  dir: /dev/keys/id_ed25519
`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := okDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/dev/keys/id_ed25519" {
			return FakeFileInfo(), nil
		}
		return FakeDirInfo(), nil
	}
	checks := Run(dir, d)
	line, ok := findExactName(checks, "ssh.dir")
	if !ok {
		t.Fatalf("no check named \"ssh.dir\"; checks: %+v", checks)
	}
	if line.Level == LevelOK {
		t.Fatal("in \"mount\" mode den mounts a DIRECTORY: a file ssh.dir must not report OK")
	}
	if !strings.Contains(line.Detail, "not a directory") {
		t.Errorf("the detail must say what is wrong; got %q", line.Detail)
	}
}

// The counterpart: a mount host that exists reports OK, by index. Without
// this case, a check that failed every mount unconditionally would pass the
// test above unnoticed.
func TestDoctorReportsPresentMountHost(t *testing.T) {
	dir := mountDenHome(t, "/dev/mounted")
	checks := Run(dir, okDeps()) // okDeps: every path exists
	line, ok := findExactName(checks, "mounts[0]")
	if !ok {
		t.Fatalf("no check named \"mounts[0]\"; checks: %+v", checks)
	}
	if line.Level != LevelOK {
		t.Errorf("a present mount host must report OK; got %+v", line)
	}
	if !strings.Contains(line.Detail, "/dev/mounted") {
		t.Errorf("detail = %q, want the host path", line.Detail)
	}
}

// A blank host: Validate() already refuses it (config errors are surfaced by
// the "config" check above). doctor must not produce a SECOND diagnostic for
// the same fault under "mounts[0]", or the user sees one problem reported
// twice under two different names.
//
// "   " and "" take the same path here: YAML strips trailing whitespace off
// an unquoted plain scalar on its own, and LoadGlobalUnvalidated's own
// TrimSpace runs before doctor ever sees the value — so g.Mounts[0].Host is
// already "" by the time this test's assertion matters. The literal is kept
// non-empty only so the YAML line reads as a real (if blank) entry, not as a
// key with an outright missing value.
func TestDoctorDoesNotDoubleReportABlankMountHost(t *testing.T) {
	dir := mountDenHome(t, "   ")
	checks := Run(dir, okDeps())
	if _, ok := findExactName(checks, "mounts[0]"); ok {
		t.Errorf("a blank host must not get its own mounts[0] line: "+
			"Validate() already reports it; checks: %+v", checks)
	}
}

// The blank entry's `continue` must not shift the index of the entry that
// follows it: the index is the entry's POSITION in `mounts:`, not a count of
// how many entries doctor actually reports on. Without this test, an
// implementation that appended to `checks` and derived the label from
// `len(checks)` instead of the loop's `i` would pass every test above (all
// single-entry) while mislabeling any config with more than one mount.
func TestDoctorIndexesMountsByPositionNotByReportedCount(t *testing.T) {
	dir := mountDenHome(t, "   ", "/nope/missing") // index 0 blank, index 1 missing
	d := okDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/nope/missing" {
			return nil, errors.New("not found")
		}
		return FakeDirInfo(), nil
	}
	checks := Run(dir, d)
	if _, ok := findExactName(checks, "mounts[0]"); ok {
		t.Errorf("the blank entry at index 0 must not get its own line; checks: %+v", checks)
	}
	line, ok := findExactName(checks, "mounts[1]")
	if !ok {
		t.Fatalf("no check named \"mounts[1]\": the second entry must keep its own "+
			"index, not be renumbered to fill the gap; checks: %+v", checks)
	}
	if line.Level == LevelOK {
		t.Error("a missing mount host must be reported as not-ok")
	}
	if !strings.Contains(line.Detail, "/nope/missing") {
		t.Errorf("the detail must name the path, got %q", line.Detail)
	}
}

// --- git version floor -------------------------------------------------
//
// den calls `git rev-parse --path-format=absolute` (internal/worktree/
// worktree.go), an option that appeared in git 2.31. Nothing declared or
// checked it: on a machine with git 2.25, the user got an obscure git error
// at the first worktree, never a den diagnostic.

func TestParseGitVersion(t *testing.T) {
	cases := []struct {
		output       string
		major, minor int
		wantErr      bool
	}{
		// Forms actually seen in the wild: plain git, Apple's git, git for
		// Windows — each suffixes the version differently.
		{"git version 2.43.0\n", 2, 43, false},
		{"git version 2.39.5 (Apple Git-154)\n", 2, 39, false},
		{"git version 2.45.2.windows.1\n", 2, 45, false},
		{"git version 2.25.1", 2, 25, false},
		{"git version 3.0.0", 3, 0, false},
		// An output we can't parse is an error, never a guess: treating it
		// as 0.0 would reject a perfectly good git.
		{"", 0, 0, true},
		{"some other command", 0, 0, true},
		{"git version two", 0, 0, true},
	}
	for _, c := range cases {
		t.Run(strconv.Quote(c.output), func(t *testing.T) {
			major, minor, err := parseGitVersion(c.output)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got %d.%d", c.output, major, minor)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", c.output, err)
			}
			if major != c.major || minor != c.minor {
				t.Errorf("parseGitVersion(%q) = %d.%d, want %d.%d", c.output, major, minor, c.major, c.minor)
			}
		})
	}
}

// The BOUNDARY, not just "old vs new": 2.30 rejected, 2.31 accepted. Without
// the equality case, a floor set at 2.32 would pass this test unnoticed.
func TestRunGitVersionFloor(t *testing.T) {
	cases := []struct {
		version  string
		accepted bool
	}{
		{"git version 2.25.1", false},
		{"git version 2.30.9", false},
		{"git version 2.31.0", true}, // exactly the floor: accepted
		{"git version 2.43.0", true},
		{"git version 3.0.0", true}, // higher major: minor no longer matters
	}
	for _, c := range cases {
		t.Run(c.version, func(t *testing.T) {
			d := okDeps()
			d.GitVersion = func() (string, error) { return c.version, nil }
			checks := Run(validDenHome(t), d)

			g, ok := findExactName(checks, "git")
			if !ok {
				t.Fatalf("no check named \"git\"; checks: %+v", checks)
			}
			green := g.Level == LevelOK
			if green != c.accepted {
				t.Fatalf("version %q: git check green = %v, want %v (detail: %s)",
					c.version, green, c.accepted, g.Detail)
			}
			if c.accepted {
				return
			}
			// A too-old git BLOCKS: `den doctor` must exit non-zero over it.
			// Without this line, downgrading it to a warning would leave the
			// test green.
			if !g.Blocking() {
				t.Fatalf("version %q: a too-old git must be blocking; got %+v", c.version, g)
			}
			// A rejection must name BOTH versions: the one read and the one
			// required. Without both, the user doesn't know what to install.
			if !strings.Contains(g.Detail, "2.31") {
				t.Errorf("detail = %q, want the required version (2.31)", g.Detail)
			}
			if !strings.Contains(g.Detail, strings.TrimPrefix(c.version, "git version ")) {
				t.Errorf("detail = %q, want the version read (%q)", g.Detail, c.version)
			}
		})
	}
}

// git missing, or unresponsive: den cannot create a single worktree.
func TestRunGitUnreachable(t *testing.T) {
	d := okDeps()
	d.GitVersion = func() (string, error) { return "", errors.New("exec: \"git\": executable file not found in $PATH") }
	checks := Run(validDenHome(t), d)

	g, ok := findExactName(checks, "git")
	if !ok {
		t.Fatalf("no check named \"git\"; checks: %+v", checks)
	}
	if !g.Blocking() {
		t.Errorf("an unreachable git must be a failure; got %+v", g)
	}
	// And the diagnosis must continue: a missing git doesn't stop den from
	// reporting what else is wrong.
	if !findName(checks, "nests") {
		t.Errorf("the diagnosis must continue despite an unreachable git; checks: %+v", checks)
	}
}

// A declared ssh.dir missing on disk. Validate() only sees "declared or
// not"; only a filesystem probe sees "declared and not found", and that's
// the very path `sbx create` receives as a workspace.
func TestRunSSHDirNotFound(t *testing.T) {
	dir := validDenHome(t)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: mount
  dir: /dev/ssh-missing
`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := okDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/dev/ssh-missing" {
			return nil, errors.New("not found")
		}
		return FakeDirInfo(), nil
	}

	checks := Run(dir, d)
	c, ok := findExactName(checks, "ssh.dir")
	if !ok {
		t.Fatalf("no check named \"ssh.dir\"; checks: %+v", checks)
	}
	if !c.Blocking() {
		t.Errorf("a missing ssh.dir must be a failure; got %+v", c)
	}
	if !strings.Contains(c.Detail, "/dev/ssh-missing") {
		t.Errorf("detail = %q, want the full path", c.Detail)
	}
}

// Outside mount mode, ssh.dir isn't mounted anywhere: nothing to check, and
// above all nothing to report. Without this case, an unconditional check
// would fail doctor on every agent-forward configuration.
func TestRunDoesNotCheckSSHDirOutsideMountMode(t *testing.T) {
	dir := validDenHome(t)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: agent-forward
  dir: /dev/ssh-missing
`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := okDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/dev/ssh-missing" {
			return nil, errors.New("not found")
		}
		return FakeDirInfo(), nil
	}
	if checks := Run(dir, d); !allOK(checks) {
		t.Errorf("in agent-forward, a missing ssh.dir must report nothing; checks: %+v", checks)
	}
}

// `ssh.mode: agent-forward` is the config's DEFAULT, and it adds NEITHER an
// argument to `sbx create`'s argv NOR a mixin entry: it relies entirely on
// the sbx process inheriting SSH_AUTH_SOCK from den's environment. That
// inheritance is proven, not assumed — see internal/sbx
// TestExecRunTransmitsDenEnvironment. If the variable is absent, there is
// simply nothing to inherit, and the user only learns it inside the VM, at
// the first `git push` — far from the cause.
func TestRunWarnsWhenAgentForwardHasNoSSHSocket(t *testing.T) {
	d := okDeps()
	d.Getenv = func(string) string { return "" }

	checks := Run(validDenHome(t), d)
	c, ok := findExactName(checks, "ssh.mode")
	if !ok {
		t.Fatalf("no check named \"ssh.mode\"; checks: %+v", checks)
	}
	if c.Level != LevelWarning {
		t.Errorf("level = %v, want LevelWarning (%v); got %+v",
			c.Level, LevelWarning, c)
	}
	// The property that matters to the user, kept SEPARATE from the level:
	// it's the one deciding `den doctor`'s exit code.
	if c.Blocking() {
		t.Error("a missing SSH agent must NOT fail den doctor: working locally " +
			"without a remote repo is legitimate, and den has no way to know whether the user needs SSH")
	}
	// The message must name the variable (what to look for), the mode (why
	// it's the one demanding it), and the concrete CONSEQUENCE — without it,
	// the user can't tell whether the line concerns them.
	// "absent or empty", not "absent": os.Getenv returns "" in both cases,
	// and den doesn't call os.LookupEnv — the message must describe what was
	// observed, not one of two plausible causes.
	for _, want := range []string{"SSH_AUTH_SOCK", "agent-forward", "git push", "absent or empty"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, must contain %q", c.Detail, want)
		}
	}
}

// The real WIRING of Getenv, which every test above bypasses by injecting a
// double. Two properties no other test touches:
//
//   - SystemDeps really reads the PROCESS environment (otherwise the
//     ssh.mode check would be green in tests and wrong in production);
//   - a variable set EMPTY takes exactly the same path as a missing one.
//     `SSH_AUTH_SOCK=""` gives os.Getenv -> "" and os.LookupEnv -> ("", true);
//     missing, os.Getenv -> "" and os.LookupEnv -> ("", false). den doesn't
//     call LookupEnv, so it CANNOT tell the two apart — which is exactly why
//     "absent or empty" is accurate and "absent" would be wrong.
//
// The value is SET with t.Setenv, never assumed: this machine may have a
// real SSH_AUTH_SOCK, and a test relying on that would be accidentally true
// here and red in CI. t.Setenv rules out t.Parallel in this test.
func TestSystemDepsReadsEnvironmentAndTreatsEmptyAsAbsent(t *testing.T) {
	read := SystemDeps().Getenv
	if read == nil {
		t.Fatal("SystemDeps().Getenv is nil: the ssh.mode check would panic at runtime")
	}

	const set = "/tmp/den-test/socket-set.sock"
	t.Setenv("SSH_AUTH_SOCK", set)
	if got := read("SSH_AUTH_SOCK"); got != set {
		t.Errorf("Getenv(SSH_AUTH_SOCK) = %q, want %q: SystemDeps must read the process environment",
			got, set)
	}

	t.Setenv("SSH_AUTH_SOCK", "")
	if got := read("SSH_AUTH_SOCK"); got != "" {
		t.Errorf("Getenv(SSH_AUTH_SOCK) = %q for a variable set EMPTY, want \"\": "+
			"an empty variable must take the same path as a missing one", got)
	}
}

// The real WIRING of SSHAgent, the sibling of the Getenv check right above and
// the one field of Deps no test could otherwise miss being dropped.
//
// Every test in this file injects its own probe (FakeDeps, fake.go), so
// removing `SSHAgent: sshagent.System()` from SystemDeps leaves the whole
// suite green — and leaves `den doctor` with a nil probe on the DEFAULT
// configuration path: ssh.mode is "agent-forward" unless the user says
// otherwise, and with SSH_AUTH_SOCK set, doctor.Run reaches
// `switch res := d.SSHAgent(); res.State` (doctor.go) with no nil check, which
// panics the command outright rather than degrading.
//
// The absence of that nil check is deliberate and stays: Deps' convention is
// that ALL its fields are required — LookPath, Stat, GitVersion and Getenv are
// each dereferenced unguarded too — so the guard belongs here, in a test on
// the single production wiring, not in a lone `if` at the call site. (spawn's
// warnEmptySSHAgent is nil-TOLERANT for the opposite reason: its probe is
// genuinely optional, skipping the warning for the modes and tests that never
// forward an agent.)
//
// The probe is NOT called: doing so would spawn a real `ssh-add -l` and make
// this package's verdict depend on the machine running the suite. What is
// asserted is what the wiring can lose.
func TestSystemDepsProvidesAnSSHAgentProbe(t *testing.T) {
	if SystemDeps().SSHAgent == nil {
		t.Fatal("SystemDeps().SSHAgent is nil: with ssh.mode agent-forward (the default) " +
			"and SSH_AUTH_SOCK set, `den doctor` panics on the nil probe")
	}
}

// The counterpart: a running SSH agent produces no warning. Without it, a
// `warn` wired unconditionally would pass the previous test.
func TestRunDoesNotWarnWhenSSHAgentIsRunning(t *testing.T) {
	checks := Run(validDenHome(t), okDeps())
	c, ok := findExactName(checks, "ssh.mode")
	if !ok {
		t.Fatalf("no check named \"ssh.mode\"; checks: %+v", checks)
	}
	if c.Level != LevelOK {
		t.Errorf("a running SSH agent must report nothing; got %+v", c)
	}
	// The socket is named: an "ok" that doesn't say what it saw wouldn't let
	// anyone spot a stale SSH_AUTH_SOCK.
	if !strings.Contains(c.Detail, FakeSSHSocket) {
		t.Errorf("detail = %q, must name the socket seen (%q)", c.Detail, FakeSSHSocket)
	}
	// And the COUNT the fake's agent reports, expected through identities()
	// rather than hardcoded so the two renderings can never drift. Without this
	// line nothing in the tree read FakeSSHIdentities: FakeDeps could return any
	// number of keys — or none while claiming StateKeys — and the whole suite
	// stayed green, which is exactly what the constant is supposed to prevent.
	if !strings.Contains(c.Detail, identities(FakeSSHIdentities)) {
		t.Errorf("detail = %q, must surface the count the fake agent reports (%q)",
			c.Detail, identities(FakeSSHIdentities))
	}
}

// A present socket is no longer enough for the "ok" line: the agent behind it
// is interrogated, and the identity COUNT it reports is surfaced. A socket
// present with a keyless agent used to pass as OK — the very blind spot this
// check closes — so the count is what proves the agent was actually queried.
func TestRunReportsTheIdentityCountWhenTheAgentHasKeys(t *testing.T) {
	d := okDeps()
	d.SSHAgent = func() sshagent.Result {
		return sshagent.Result{State: sshagent.StateKeys, Identities: 3}
	}

	checks := Run(validDenHome(t), d)
	c, ok := findExactName(checks, "ssh.mode")
	if !ok {
		t.Fatalf("no check named \"ssh.mode\"; checks: %+v", checks)
	}
	if c.Level != LevelOK {
		t.Errorf("an agent holding keys must report OK; got %+v", c)
	}
	if !strings.Contains(c.Detail, "3 identities") {
		t.Errorf("detail = %q, must surface the identity count the agent reported", c.Detail)
	}
	if !strings.Contains(c.Detail, FakeSSHSocket) {
		t.Errorf("detail = %q, must still name the socket", c.Detail)
	}
}

// One key: the singular reads "1 identity", never "1 identities". A cosmetic
// detail, but the "ok" line is meant to be read, and the plural jars.
func TestRunReportsASingleIdentityInTheSingular(t *testing.T) {
	d := okDeps()
	d.SSHAgent = func() sshagent.Result {
		return sshagent.Result{State: sshagent.StateKeys, Identities: 1}
	}
	c, ok := findExactName(Run(validDenHome(t), d), "ssh.mode")
	if !ok {
		t.Fatal("no ssh.mode check")
	}
	if !strings.Contains(c.Detail, "1 identity") || strings.Contains(c.Detail, "1 identities") {
		t.Errorf("detail = %q, want the singular \"1 identity\"", c.Detail)
	}
}

// THE case this whole change exists for: the socket is present, so the old
// check called it OK, but the agent behind it holds no key. A sandbox then
// inherits a live-but-empty agent and every push dies on publickey — a warning
// here, non-blocking (working locally without a remote is legitimate).
func TestRunWarnsWhenTheForwardedAgentIsEmpty(t *testing.T) {
	d := okDeps()
	d.SSHAgent = func() sshagent.Result { return sshagent.Result{State: sshagent.StateEmpty} }

	checks := Run(validDenHome(t), d)
	c, ok := findExactName(checks, "ssh.mode")
	if !ok {
		t.Fatalf("no check named \"ssh.mode\"; checks: %+v", checks)
	}
	if c.Level != LevelWarning {
		t.Errorf("an empty forwarded agent must WARN; got %+v", c)
	}
	if c.Blocking() {
		t.Error("an empty agent must not fail den doctor: working locally without a remote is legitimate")
	}
	// The message must name the observed cause, the consequence, and a fix the
	// user can run — the socket, publickey, and a concrete ssh-add command.
	for _, want := range []string{FakeSSHSocket, "no identity", "publickey", "ssh-add"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, must contain %q", c.Detail, want)
		}
	}
}

// The remedy is OS-specific — macOS needs `--apple-use-keychain` to reach the
// login keychain — and sshagent.FixCommand takes goos as a PARAMETER precisely
// so each branch is assertable anywhere. Reading runtime.GOOS inside Run threw
// that away: the darwin remedy became unassertable on a Linux CI, which is the
// only place this suite ever runs. Deps.GOOS puts the choice back under the
// injection contract, so the exact command a macOS user is told to run is
// checked here rather than trusted.
// ALL THREE agent-forward warnings quote a fix command, so all three are
// checked, the absent-socket branch included: a remedy that only got the OS
// right on some branches would still mislead the macOS users who land on the
// others. That branch is not hypothetical — it hardcoded the bare form while
// its two siblings templated theirs through FixCommand, so on darwin one
// warning out of three printed a command without `--apple-use-keychain`, in
// the one place nobody had templated.
func TestRunNamesTheMacOSRemedyWhenGOOSIsDarwin(t *testing.T) {
	for name, branch := range agentForwardWarningBranches {
		t.Run(name, func(t *testing.T) {
			d := okDeps()
			d.GOOS = "darwin"
			branch(&d)

			c, ok := findExactName(Run(validDenHome(t), d), "ssh.mode")
			if !ok {
				t.Fatal("no ssh.mode check")
			}
			if c.Level != LevelWarning {
				t.Fatalf("this branch must warn, otherwise the remedy assertion below "+
					"stops exercising anything; got %+v", c)
			}
			if want := sshagent.FixCommand("darwin"); !strings.Contains(c.Detail, want) {
				t.Errorf("detail = %q, must name the darwin remedy %q in full", c.Detail, want)
			}
		})
	}
}

// Every warning that hands the user a load command must also name what that
// command does NOT load: `ssh-add` reads the default `~/.ssh/id_*` names only,
// so on a host whose real keys are named otherwise it exits 0, `ssh-add -l`
// reports an identity, this check turns green — and `git push` from the sandbox
// stays denied. Measured on the verification machine, where the only
// default-named key is an `id_rsa` nothing uses.
//
// Asserted against the CONSTANT, never a copy of its wording: a caveat the test
// spells out itself would keep passing after the message stopped saying it.
func TestRunNamesTheNonDefaultKeyCaveatWhereverItQuotesAFix(t *testing.T) {
	for name, branch := range agentForwardWarningBranches {
		t.Run(name, func(t *testing.T) {
			d := okDeps()
			branch(&d)

			c, ok := findExactName(Run(validDenHome(t), d), "ssh.mode")
			if !ok {
				t.Fatal("no ssh.mode check")
			}
			if !strings.Contains(c.Detail, sshagent.KeyNameCaveat) {
				t.Errorf("detail = %q, must carry the non-default-key caveat %q: the fix it "+
					"quotes can succeed and still leave `git push` broken",
					c.Detail, sshagent.KeyNameCaveat)
			}
		})
	}
}

// The three machine states in which `den doctor` warns about agent-forward AND
// quotes a command to run — the population the OS-remedy tests must cover in
// full. Declared once: the darwin assertion and its Linux counterpart have to
// range over the SAME set, or a branch added to one is left untested by the
// other.
//
// The absent-socket branch is set up by injecting Getenv, not by unsetting the
// variable: Run reads the socket through Deps, and this suite owes nothing to
// the machine it runs on.
var agentForwardWarningBranches = map[string]func(*Deps){
	"empty": func(d *Deps) {
		d.SSHAgent = func() sshagent.Result { return sshagent.Result{State: sshagent.StateEmpty} }
	},
	"unreachable": func(d *Deps) {
		d.SSHAgent = func() sshagent.Result { return sshagent.Result{State: sshagent.StateUnreachable} }
	},
	"socket absent": func(d *Deps) {
		d.Getenv = func(string) string { return "" }
	},
}

// The counterpart, and what makes the test above more than a tautology: on a
// non-darwin GOOS the keychain flag must NOT appear. Without it, a FixCommand
// that returned the macOS form everywhere would satisfy the darwin assertion
// while telling every Linux user to pass a flag their ssh-add rejects.
//
// Over the same three branches, for the reason given on
// agentForwardWarningBranches: the pair only holds if both sides range over the
// whole population.
func TestRunOmitsTheMacOSFlagOnLinux(t *testing.T) {
	for name, branch := range agentForwardWarningBranches {
		t.Run(name, func(t *testing.T) {
			d := okDeps()
			d.GOOS = "linux"
			branch(&d)

			c, ok := findExactName(Run(validDenHome(t), d), "ssh.mode")
			if !ok {
				t.Fatal("no ssh.mode check")
			}
			if strings.Contains(c.Detail, "--apple-use-keychain") {
				t.Errorf("detail = %q, must not hand a Linux user a macOS-only flag", c.Detail)
			}
			if !strings.Contains(c.Detail, "ssh-add") {
				t.Errorf("detail = %q, must still name a concrete fix command", c.Detail)
			}
		})
	}
}

// The socket points at an agent nothing answers behind — a dead socket, no
// agent, or ssh-add off PATH. Same verdict family as the empty case (warn, not
// fail), with a message that names the different cause.
func TestRunWarnsWhenTheForwardedAgentIsUnreachable(t *testing.T) {
	d := okDeps()
	d.SSHAgent = func() sshagent.Result { return sshagent.Result{State: sshagent.StateUnreachable} }

	checks := Run(validDenHome(t), d)
	c, ok := findExactName(checks, "ssh.mode")
	if !ok {
		t.Fatalf("no check named \"ssh.mode\"; checks: %+v", checks)
	}
	if c.Level != LevelWarning {
		t.Errorf("an unreachable agent must WARN; got %+v", c)
	}
	if c.Blocking() {
		t.Error("an unreachable agent must not fail den doctor")
	}
	for _, want := range []string{FakeSSHSocket, "unreachable", "ssh-add"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, must contain %q", c.Detail, want)
		}
	}
}

// A state the switch doesn't model must not make the diagnostic VANISH. With
// no default arm, a State added to sshagent (or any value doctor wasn't taught)
// produced NO ssh.mode line at all: `den doctor` then printed nothing about the
// agent, which reads as "nothing to report" — silence where the check is meant
// to speak. The line must still appear, as a warning naming what was seen.
func TestRunStillReportsSSHModeOnAnUnrecognizedAgentState(t *testing.T) {
	d := okDeps()
	d.SSHAgent = func() sshagent.Result { return sshagent.Result{State: sshagent.State(99)} }

	checks := Run(validDenHome(t), d)
	c, ok := findExactName(checks, "ssh.mode")
	if !ok {
		t.Fatalf("no check named \"ssh.mode\" for an unmodelled agent state: the diagnostic "+
			"disappeared instead of warning; checks: %+v", checks)
	}
	if c.Level != LevelWarning {
		t.Errorf("an unrecognized agent state must WARN, not pass for healthy; got %+v", c)
	}
	if c.Blocking() {
		t.Error("an unrecognized agent state must not fail den doctor")
	}
	// The socket and the unrecognized value are both named: a warning that says
	// neither what it looked at nor what it got is unactionable.
	for _, want := range []string{FakeSSHSocket, "99"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, must contain %q", c.Detail, want)
		}
	}
}

// When SSH_AUTH_SOCK is absent, there is no agent to query: the old "absent or
// empty" warning stands and SSHAgent must NOT be called — probing a socket
// that isn't there would be nonsense (and, with the real Exec, a wasted
// ssh-add).
func TestRunDoesNotQueryTheAgentWhenTheSocketIsAbsent(t *testing.T) {
	called := false
	d := okDeps()
	d.Getenv = func(string) string { return "" }
	d.SSHAgent = func() sshagent.Result {
		called = true
		return sshagent.Result{}
	}

	checks := Run(validDenHome(t), d)
	if called {
		t.Error("SSHAgent was queried with no socket to point it at")
	}
	c, ok := findExactName(checks, "ssh.mode")
	if !ok {
		t.Fatalf("no check named \"ssh.mode\"; checks: %+v", checks)
	}
	if c.Level != LevelWarning || !strings.Contains(c.Detail, "absent or empty") {
		t.Errorf("an absent socket must keep the original absent-or-empty warning; got %+v", c)
	}
}

// Outside agent-forward, the agent is queried by nothing: `none` and `mount`
// don't forward it, so probing it would be a false positive AND a needless
// ssh-add. The spy proves no query happens, from the same angle as
// TestRunDoesNotWarnOutsideAgentForward.
func TestRunDoesNotQueryTheAgentOutsideAgentForward(t *testing.T) {
	for _, c := range []struct{ name, ssh string }{
		{"none", "ssh:\n  mode: none\n"},
		{"mount", "ssh:\n  mode: mount\n  dir: /dev/ssh\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := validDenHome(t)
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`+c.ssh), 0o644); err != nil {
				t.Fatal(err)
			}
			called := false
			d := okDeps()
			d.SSHAgent = func() sshagent.Result {
				called = true
				return sshagent.Result{}
			}

			Run(dir, d)
			if called {
				t.Errorf("mode %q: the SSH agent must not be queried", c.name)
			}
		})
	}
}

// Outside agent-forward, SSH_AUTH_SOCK is used by nothing: neither `none`
// nor `mount` depend on it, and a warning there would be a false positive on
// every `den doctor` run. Same family as
// TestRunDoesNotCheckSSHDirOutsideMountMode, from the other end.
func TestRunDoesNotWarnOutsideAgentForward(t *testing.T) {
	for _, c := range []struct{ name, ssh string }{
		{"none", "ssh:\n  mode: none\n"},
		{"mount", "ssh:\n  mode: mount\n  dir: /dev/ssh\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := validDenHome(t)
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`+c.ssh), 0o644); err != nil {
				t.Fatal(err)
			}
			d := okDeps()
			// No SSH agent: precisely the situation that triggers the
			// warning in agent-forward.
			d.Getenv = func(string) string { return "" }

			checks := Run(dir, d)
			if got, ok := findExactName(checks, "ssh.mode"); ok {
				t.Errorf("mode %q: no ssh.mode check expected; got %+v", c.name, got)
			}
			if !allOK(checks) {
				t.Errorf("mode %q: no report expected; checks: %+v", c.name, checks)
			}
		})
	}
}

// Exact counterpart of the spawn-side test for an empty entry in `kits:`:
// side doctor, an empty entry in `kits:` is not a missing kit, it's a line
// with no content. Both tests hold the SAME property from both ends, since
// `kits:` and `kit:` are composed by a single source
// (config.Stack.DeclaredKits): neutralizing the filter there must turn both
// red, which is what proves there is now only one such source.
func TestRunIgnoresAnEmptyEntryInKits(t *testing.T) {
	dir := validDenHome(t)
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nkits: [\"\", transverse]\nkit: devx-kit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stat fails on the EMPTY path and on it alone: with an always-OK
	// okDeps, a check that probed the empty entry wouldn't give itself away.
	d := okDeps()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "" {
			return nil, errors.New("empty path")
		}
		return FakeDirInfo(), nil
	}

	checks := Run(dir, d)
	if !allOK(checks) {
		t.Errorf("an empty entry in kits: must produce no failure; checks: %+v", checks)
	}
}

func TestRunAgentWithoutUpdateCommand(t *testing.T) {
	dir := t.TempDir()
	content := "agents:\n  claude:\n    config_dir: /tmp/c\ndefaults:\n  agent: claude\n  stack: devx\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	checks := Run(dir, okDeps())
	if _, ok := find(checks, "update"); !ok {
		t.Error("an agent without an update command must be reported (spec §9.1)")
	}
}

// ONE fault, ONE diagnostic — and the same count regardless of which
// invisible whitespace is written into the field.
//
// Two judges used to rule on `update`: Validate() (step 3, on TrimSpace) and
// a loop specific to doctor (step 5, on `== ""`), so a whitespace-only value
// produced two failures where a genuinely empty one produced one — and the
// doctor-only line named just "agent claude" instead of the actual key.
// Validate() alone now covers it, naming the key to fix.
func TestDoctorCountsOnlyOneFailurePerBadUpdate(t *testing.T) {
	for _, update := range []string{"", "   ", "\t"} {
		t.Run(strconv.Quote(update), func(t *testing.T) {
			dir := t.TempDir()
			content := "agents:\n  claude:\n    config_dir: /tmp/c\n    update: " +
				strconv.Quote(update) + "\ndefaults:\n  agent: claude\n  stack: devx\n"
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}

			var failing []Check
			for _, c := range Run(dir, okDeps()) {
				if c.Blocking() && strings.Contains(c.Detail, "update") {
					failing = append(failing, c)
				}
			}
			if len(failing) != 1 {
				t.Fatalf("%d failing diagnostics for `update` for ONE fault, want 1; got: %+v",
					len(failing), failing)
			}
			// The exact key, not just the agent: "agent claude" doesn't say
			// which YAML line to fix.
			if !strings.Contains(failing[0].Detail, "agents.claude.update") {
				t.Errorf("the diagnostic must name the key to fix; got: %+v", failing[0])
			}
		})
	}
}

// A broken stack must produce only ONE diagnostic — its own — and above all
// no FALSE diagnostic on the healthy objects. Before the fix, LoadStacks
// failed in bulk on a single bad stack, so doctor fell back to an empty map
// and declared everything it no longer contained "not found" — sending the
// user to fix the wrong file. It's the lie, more than the failure, that this
// test locks down.
func TestABrokenStackProducesNoFalseDiagnostics(t *testing.T) {
	dir := validDenHome(t)
	// devx stays healthy; add a second stack that nobody uses.
	if err := os.MkdirAll(filepath.Join(dir, "stacks", "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stacks", "other", "stack.yaml"),
		[]byte("image: other:v1\nimag: typo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := Run(dir, okDeps())

	// 1. The broken stack is named.
	c, ok := findExactName(checks, "stack other")
	if !ok || !c.Blocking() {
		t.Fatalf("the broken stack must produce a failing diagnostic naming it; checks = %+v", checks)
	}
	if !strings.Contains(c.Detail, "imag") {
		t.Errorf("the diagnostic must name the offending key; got: %s", c.Detail)
	}

	// 2. NO diagnostic must claim devx is not found.
	for _, ch := range checks {
		if strings.Contains(ch.Detail, "devx") && strings.Contains(ch.Detail, "not found") {
			t.Errorf("FALSE diagnostic: devx is healthy, yet %q says: %s", ch.Name, ch.Detail)
		}
	}

	// 3. defaults.stack (= devx) stays green.
	if d, ok := findExactName(checks, "defaults.stack"); !ok || d.Level != LevelOK {
		t.Errorf("defaults.stack points at devx, which is healthy: expected green; got %+v", d)
	}

	// 4. The api nest, which uses devx, must produce no stack failure.
	if n, ok := findExactName(checks, "nest api"); ok && n.Level != LevelOK {
		t.Errorf("the api nest uses devx, which is healthy: expected no failure; got: %s", n.Detail)
	}
}

// The counterpart: when it's the nest's OWN stack that's broken, the nest
// must learn it — and read "unreadable", not "not found". Without this test,
// a fix that simply ignored broken stacks would pass the previous test while
// making the real problem invisible.
func TestANestWhoseStackIsBrokenLearnsIt(t *testing.T) {
	dir := validDenHome(t)
	// devx, the api nest's stack, becomes unreadable.
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nimag: typo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := Run(dir, okDeps())

	n, ok := findExactName(checks, "nest api")
	if !ok || !n.Blocking() {
		t.Fatalf("the nest whose stack is broken must produce a failure; checks = %+v", checks)
	}
	if !strings.Contains(n.Detail, "unreadable") {
		t.Errorf("the nest must read \"unreadable\", not \"not found\": its stack exists, "+
			"it just doesn't load; got: %s", n.Detail)
	}
}

// The "stacks" line must count like the "nests" line, its immediate neighbor
// on screen: declared AND unreadable, so two adjacent totals never appear to
// contradict each other.
func TestTheStacksLineCountsLikeTheNestsLine(t *testing.T) {
	dir := validDenHome(t) // devx healthy, one api nest
	if err := os.MkdirAll(filepath.Join(dir, "stacks", "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stacks", "other", "stack.yaml"),
		[]byte("image: other:v1\nimag: typo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := Run(dir, okDeps())

	c, ok := findExactName(checks, "stacks")
	if !ok {
		t.Fatalf("no \"stacks\" line; checks = %+v", checks)
	}
	// The count of unreadable stacks, just as the nests line does.
	if !strings.Contains(c.Detail, "unreadable") {
		t.Errorf("the stacks line must count the unreadable ones, like the nests line does; got: %q",
			c.Detail)
	}
	// 1 healthy (devx), 1 unreadable (other).
	if !strings.Contains(c.Detail, "1 declared") || !strings.Contains(c.Detail, "1 unreadable") {
		t.Errorf("expected \"1 declared, 1 unreadable\"; got: %q", c.Detail)
	}
	// And the verdict follows the count: an unreadable stack is not OK.
	if !c.Blocking() {
		t.Errorf("the stacks line is OK while a stack is unreadable; got %+v", c)
	}
}

// The counterpart: with no broken stack, the line stays green and reports
// zero unreadable. Without it, a verdict hardcoded to false would pass the
// previous test.
func TestTheStacksLineStaysGreenWithoutABrokenStack(t *testing.T) {
	checks := Run(validDenHome(t), okDeps())

	c, ok := findExactName(checks, "stacks")
	if !ok {
		t.Fatalf("no \"stacks\" line; checks = %+v", checks)
	}
	if c.Level != LevelOK {
		t.Errorf("no stack is broken: the stacks line must be green; got: %+v", c)
	}
	if !strings.Contains(c.Detail, "0 unreadable") {
		t.Errorf("expected \"0 unreadable\"; got: %q", c.Detail)
	}
}

// A record with no live sandbox is an orphan: `sbx rm` run outside den, a
// failed boot, or a `den rm --keep-worktrees`. Only the directories den
// created are named — a repo mounted as-is belongs to the user.
func TestOrphansNamesRecordsWithoutALiveSandbox(t *testing.T) {
	live := LiveSandboxes{Known: true, Names: []string{"web"}}
	ms := []manifest.Manifest{
		{Sandbox: "api.feat12", Repos: []manifest.Repo{
			{Name: "api", Mount: "/wt/feat12/api", Worktree: true},
			{Name: "hotfix", Mount: "/tmp/hotfix", Worktree: false},
		}},
		{Sandbox: "web", Repos: []manifest.Repo{{Name: "web", Mount: "/wt/web", Worktree: true}}},
	}
	got := Orphans(live, ms)
	if len(got) != 1 || got[0].Sandbox != "api.feat12" {
		t.Fatalf("only the sandbox with no live VM is an orphan: %#v", got)
	}
	if !reflect.DeepEqual(got[0].Worktrees, []string{"/wt/feat12/api"}) {
		t.Errorf("only den-created directories are named: %#v", got[0].Worktrees)
	}
}

// Proof 14 — with sbx absent the live list is UNKNOWN, and every healthy
// sandbox would look like an orphan. The check is skipped and says so; the sbx
// line above it already carries the real problem.
func TestOrphanCheckIsSkippedWhenTheLiveListIsUnknown(t *testing.T) {
	if got := Orphans(LiveSandboxes{}, []manifest.Manifest{{Sandbox: "api"}}); len(got) != 0 {
		t.Errorf("an unknown live list makes nobody an orphan: %#v", got)
	}
	c := OrphanCheck(LiveSandboxes{}, []manifest.Manifest{{Sandbox: "api"}})
	if c.Blocking() || c.Level != LevelOK {
		t.Errorf("an unknown live list must not accuse anyone: %#v", c)
	}
	if !strings.Contains(c.Detail, "skipped") {
		t.Errorf("the skip must be visible: %q", c.Detail)
	}
}

// An orphan is a warning, never a failure: leftover directories are not a
// broken installation, and turning `den doctor` red over them would train the
// user to ignore it.
func TestOrphanCheckWarnsAndNamesTheRemedy(t *testing.T) {
	c := OrphanCheck(
		LiveSandboxes{Known: true},
		[]manifest.Manifest{{Sandbox: "api.feat12", Repos: []manifest.Repo{
			{Mount: "/wt/feat12/api", Worktree: true}}}})
	if c.Level != LevelWarning {
		t.Errorf("orphans warn, they do not fail: %#v", c)
	}
	if !strings.Contains(c.Detail, "api.feat12") {
		t.Errorf("the orphan must be named: %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "den doctor --fix") {
		t.Errorf("the remedy must be named: %q", c.Detail)
	}
}

// Nothing recorded, nothing to say — but the check still reports, so a healthy
// den home does not leave a hole where the line usually is.
func TestOrphanCheckIsOKWithNoRecordAtAll(t *testing.T) {
	c := OrphanCheck(LiveSandboxes{Known: true}, nil)
	if c.Level != LevelOK || c.Detail != "none" {
		t.Errorf("no record means nothing to reclaim: %#v", c)
	}
}
