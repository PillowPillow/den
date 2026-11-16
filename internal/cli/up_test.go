package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/hub"
	"github.com/PillowPillow/den/internal/prompt"
	"github.com/PillowPillow/den/internal/registry"
	"github.com/PillowPillow/den/internal/sbx"
)

type upFixture struct {
	home, proj, src string
	fake            *sbx.Fake
	prompt          *prompt.Fake
	hub             hub.Fake
	out, err        bytes.Buffer
	cwd             string
}

func newUpFixture(t *testing.T) *upFixture {
	t.Helper()
	f := &upFixture{home: t.TempDir(), proj: t.TempDir(), src: t.TempDir(), prompt: &prompt.Fake{}}
	f.fake = &sbx.Fake{Responses: map[string]sbx.Response{
		"version":            {Output: []byte("sbx version v0.53.0 abc\n")},
		"ls --json":          {Output: []byte(`[]`)},
		"template ls --json": {Output: []byte(`[{"repository":"docker.io/library/b","tag":"v1"}]`)},
	}}
	return f
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// project writes den.yaml with each nest name → its YAML body, indented under the name.
func (f *upFixture) project(t *testing.T, nests ...[2]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("schema: 1\nbases:\n  b: {image: \"b:v1\"}\nnests:\n")
	for _, n := range nests {
		fmt.Fprintf(&b, "  %s:\n    base: b\n", n[0])
		for _, line := range strings.Split(strings.TrimSpace(n[1]), "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	writeTestFile(t, filepath.Join(f.proj, "den.yaml"), b.String())
}

func (f *upFixture) dir(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(f.src, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *upFixture) live(names ...string) {
	var entries []string
	for _, n := range names {
		entries = append(entries, fmt.Sprintf(`{"name":%q,"status":"running"}`, n))
	}
	f.fake.Responses["ls --json"] = sbx.Response{Output: []byte("[" + strings.Join(entries, ",") + "]")}
}

func (f *upFixture) up(args ...string) error { return f.up2("up", args...) }

func (f *upFixture) up2(cmd string, args ...string) error {
	f.out.Reset()
	f.err.Reset()
	cwd := f.proj
	if f.cwd != "" {
		cwd = f.cwd
	}
	root := NewRoot(Deps{
		Sbx: f.fake, Prompt: f.prompt, Hub: f.hub, IsTTY: func() bool { return false },
		Getwd: func() (string, error) { return cwd, nil }, Out: &f.out, Err: &f.err,
	})
	root.SetArgs(append([]string{"--den-home", f.home, cmd}, args...))
	return root.Execute()
}

func (f *upFixture) record(t *testing.T, r registry.Record) {
	t.Helper()
	if err := registry.Write(f.home, r); err != nil {
		t.Fatal(err)
	}
}

func (f *upFixture) hasEnvCreate() bool { return f.fake.HasCalled("--no-input", "env", "create") }

func TestUpRefusesWithoutANest(t *testing.T) {
	f := newUpFixture(t)
	f.project(t, [2]string{"am", "repos: [{path: /x}]"}, [2]string{"dg", "repos: [{path: /y}]"})
	err := f.up()
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, w := range []string{"am", "dg", filepath.Join(f.proj, "den.yaml"), "den up <nest>"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q lacks %q", err, w)
		}
	}
	if len(f.fake.Calls) != 0 {
		t.Errorf("sbx calls %v, want none", f.fake.Calls)
	}
}

// liveAm records am as created from /h/dev/am-backend, which the attach path never stats.
func (f *upFixture) liveAm(t *testing.T, hash string) {
	t.Helper()
	f.project(t, [2]string{"am", "repos: [{path: /h/dev/am-backend}]"})
	f.record(t, registry.Record{
		Instance: "am", Nest: "am", ConfigFile: filepath.Join(f.proj, "den.yaml"), Project: f.proj,
		RunDir: filepath.Join(f.proj, ".den", "run", "am"), GenDir: filepath.Join(f.home, "gen", "am"),
		EnvFiles: []string{filepath.Join(f.proj, ".den", "run", "am", "sbxenv.yaml")}, ConfigHash: hash,
		Start: "/h/dev/am-backend",
		Repos: []registry.Repo{{Name: "am-backend", Repo: "/h/dev/am-backend", Mount: "/h/dev/am-backend", Target: "/h/dev/am-backend"}},
	})
	f.live("am")
}

func TestUpAttachesClaudeWithTheStartFolder(t *testing.T) {
	f := newUpFixture(t)
	f.liveAm(t, "stale")
	if err := f.up("am"); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasAttached("run", "--name", "am", "--workdir", "/h/dev/am-backend") {
		t.Errorf("attaches %v", f.fake.Attaches)
	}
	if f.hasEnvCreate() {
		t.Errorf("created a live instance again: %v", f.fake.Calls)
	}
}

func TestUpAttachesAtTheWorkdirOnALiveInstance(t *testing.T) {
	f := newUpFixture(t)
	f.liveAm(t, "stale")
	if err := f.up("am", "--workdir", "/h/dev/am-backend/sub"); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasAttached("run", "--name", "am", "--workdir", "/h/dev/am-backend/sub") {
		t.Errorf("attaches %v", f.fake.Attaches)
	}
}

func TestUpWorkdirAtCreationIsOneOff(t *testing.T) {
	f := newUpFixture(t)
	api := f.dir(t, "api")
	f.project(t, [2]string{"am", "repos: [{path: " + api + "}]"})
	if err := f.up("am", "--workdir", "/once"); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasAttached("run", "--name", "am", "--workdir", "/once") {
		t.Errorf("attaches %v, want the --workdir", f.fake.Attaches)
	}
	if rec, err := registry.Read(f.home, "am"); err != nil || rec.Start != api {
		t.Fatalf("recorded start %q (%v), want the mapped %q", rec.Start, err, api)
	}

	f.live("am")
	f.fake.Attaches = nil
	if err := f.up("am"); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasAttached("run", "--name", "am", "--workdir", api) {
		t.Errorf("attaches %v, want the mapped start", f.fake.Attaches)
	}
	if err := f.up2("shell", "am"); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasAttached("exec", "-it", "-w", api, "am", "bash", "-l") {
		t.Errorf("attaches %v, want the shell at the mapped start", f.fake.Attaches)
	}
}

func TestUpRefusesInteractiveWithoutATerminal(t *testing.T) {
	f := newUpFixture(t)
	f.project(t, [2]string{"am", "repos: [{path: " + f.dir(t, "api") + ", optional: true}]"})
	err := f.up("am", "-i")
	if err == nil || !strings.Contains(err.Error(), "-i needs a terminal; use --only/--without") {
		t.Fatalf("error %v, want the terminal refusal", err)
	}
	if len(f.fake.Calls) != 0 || len(f.prompt.MultiSelects) != 0 {
		t.Errorf("sbx calls %v, prompts %d after a refusal", f.fake.Calls, len(f.prompt.MultiSelects))
	}
	if _, err := os.Stat(filepath.Join(f.home, "state")); err == nil {
		t.Errorf("den wrote under %s before refusing", f.home)
	}
}

func TestUpDetachDoesNotAttach(t *testing.T) {
	f := newUpFixture(t)
	f.project(t, [2]string{"am", "repos: [{path: " + f.dir(t, "api") + "}]"})
	if err := f.up("am", "--detach"); err != nil {
		t.Fatal(err)
	}
	if !f.hasEnvCreate() {
		t.Errorf("no env create: %v", f.fake.Calls)
	}
	if len(f.fake.Attaches) != 0 {
		t.Errorf("attached with --detach: %v", f.fake.Attaches)
	}
	if !f.fake.HasCalled("--no-input", "run", "--detach", "--name", "am") {
		t.Errorf("no detached run after the create: %v", f.fake.Calls)
	}
}

func TestUpDetachStartsAStoppedLiveInstance(t *testing.T) {
	f := newUpFixture(t)
	f.liveAm(t, "stale")
	f.fake.Responses["ls --json"] = sbx.Response{Output: []byte(`[{"name":"am","status":"stopped"}]`)}
	if err := f.up("am", "--detach"); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasCalled("--no-input", "run", "--detach", "--name", "am") {
		t.Errorf("stopped instance not started: %v", f.fake.Calls)
	}
	if f.hasEnvCreate() || len(f.fake.Attaches) != 0 {
		t.Errorf("calls %v, attaches %v: want only the detached run", f.fake.Calls, f.fake.Attaches)
	}
}

func TestUpDetachReportsAFailedStart(t *testing.T) {
	f := newUpFixture(t)
	f.liveAm(t, "stale")
	f.fake.Responses["--no-input run --detach --name am"] = sbx.Response{Err: fmt.Errorf("boom")}
	err := f.up("am", "--detach")
	if err == nil || !strings.Contains(err.Error(), "am") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error %v, want the failed start naming am", err)
	}
}

func TestUpCreatesThenAttaches(t *testing.T) {
	f := newUpFixture(t)
	api := f.dir(t, "api")
	f.project(t, [2]string{"am", "repos: [{path: " + api + "}]"})
	if err := f.up("am"); err != nil {
		t.Fatal(err)
	}
	if !f.hasEnvCreate() || !f.fake.HasAttached("run", "--name", "am", "--workdir", api) {
		t.Errorf("calls %v", f.fake.Calls)
	}
}

func TestUpWarnsOnDrift(t *testing.T) {
	f := newUpFixture(t)
	f.liveAm(t, "stale")
	if err := f.up("am"); err != nil {
		t.Fatal(err)
	}
	want := "warning: " + filepath.Join(f.proj, "den.yaml") + " changed since am was created — run `den rm am` then `den up am` to apply it\n"
	if !strings.Contains(f.err.String(), want) {
		t.Errorf("stderr %q lacks %q", f.err.String(), want)
	}
}

func TestUpDriftAndSelectionWarningsNameTheBranch(t *testing.T) {
	f := newUpFixture(t)
	f.project(t, [2]string{"am", "repos: [{path: /h/dev/api, optional: true}]"})
	f.record(t, registry.Record{
		Instance: "am.feature-x", Nest: "am", ConfigFile: filepath.Join(f.proj, "den.yaml"), Start: "/h/dev/api", ConfigHash: "stale",
		RunDir: filepath.Join(f.proj, ".den", "run", "am.feature-x"), GenDir: filepath.Join(f.home, "gen", "am.feature-x"),
		Worktree: &registry.Worktree{Branch: "feature/x", Dir: "feature-x", Layout: "central", Root: filepath.Join(f.home, "wt")},
		Repos:    []registry.Repo{{Name: "api", Repo: "/h/dev/api", Mount: filepath.Join(f.home, "wt", "feature-x", "api"), Target: "/h/dev/api", Worktree: true}},
	})
	f.live("am.feature-x")
	if err := f.up("am", "-w", "feature/x", "--without", "api"); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"changed since am.feature-x was created — run `den rm am.feature-x` then `den up am -w feature/x` to apply it",
		"--without is ignored", "run `den rm am.feature-x` then `den up am -w feature/x` to change them",
	} {
		if !strings.Contains(f.err.String(), w) {
			t.Errorf("stderr %q lacks %q", f.err.String(), w)
		}
	}
}

func TestUpDoesNotWarnWhenAnotherNestChanges(t *testing.T) {
	f := newUpFixture(t)
	am := [2]string{"am", "repos: [{path: " + f.dir(t, "api") + "}]"}
	f.project(t, am, [2]string{"other", "repos: [{path: " + f.dir(t, "web") + "}]"})
	if err := f.up("am", "--detach"); err != nil {
		t.Fatal(err)
	}
	f.live("am")
	f.project(t, am, [2]string{"other", "repos: [{path: " + f.dir(t, "web") + "}]\nenv: {X: \"1\"}"})
	if err := f.up("am"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.err.String(), "warning:") {
		t.Errorf("stderr %q, want no warning", f.err.String())
	}
}

func TestUpDoesNotWarnOnTheNextUpOfASubset(t *testing.T) {
	f := newUpFixture(t)
	f.project(t, [2]string{"am", fmt.Sprintf("repos: [{path: %s}, {path: %s, optional: true}]", f.dir(t, "api"), f.dir(t, "web"))})
	if err := f.up("am", "--without", "web", "--detach"); err != nil {
		t.Fatal(err)
	}
	f.live("am")
	if err := f.up("am"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.err.String(), "warning:") {
		t.Errorf("stderr %q, want no warning: den recorded what the next up does not hash", f.err.String())
	}
}

func TestUpWarnsThatSelectionFlagsAreIgnoredOnALiveInstance(t *testing.T) {
	f := newUpFixture(t)
	f.liveAm(t, "stale")
	if err := f.up("am", "--only", "web"); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"warning: ", "--only", "am"} {
		if !strings.Contains(f.err.String(), w) {
			t.Errorf("stderr %q lacks %q", f.err.String(), w)
		}
	}
	if !f.fake.HasAttached("run", "--name", "am") {
		t.Errorf("attaches %v", f.fake.Attaches)
	}
}

func TestUpPrintsTheNestWarnings(t *testing.T) {
	f := newUpFixture(t)
	f.project(t, [2]string{"am", "repos: [{path: " + f.dir(t, "api") + "}, {key: gone, optional: true}]"})
	if err := f.up("am", "--detach"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.err.String(), `warning: optional repo key "gone"`) {
		t.Errorf("stderr %q lacks the nest warning", f.err.String())
	}
}

func TestUpRefusesABranchThatCollidesWithTheRecordedOne(t *testing.T) {
	f := newUpFixture(t)
	f.project(t, [2]string{"am", "repos: [{path: /h/dev/api}]"})
	f.record(t, registry.Record{
		Instance: "am.feature-x", Nest: "am", ConfigFile: filepath.Join(f.proj, "den.yaml"), Start: "/h/dev/api",
		RunDir: filepath.Join(f.proj, ".den", "run", "am.feature-x"), GenDir: filepath.Join(f.home, "gen", "am.feature-x"),
		Worktree: &registry.Worktree{Branch: "feature-x", Dir: "feature-x", Layout: "central", Root: filepath.Join(f.home, "wt")},
		Repos:    []registry.Repo{{Name: "api", Repo: "/h/dev/api", Mount: filepath.Join(f.home, "wt", "feature-x", "api"), Target: "/h/dev/api", Worktree: true}},
	})
	f.live("am.feature-x")
	err := f.up("am", "-w", "feature/x")
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, w := range []string{`"feature-x"`, `"feature/x"`, "am.feature-x"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q lacks %q", err, w)
		}
	}
	if len(f.fake.Attaches) != 0 {
		t.Errorf("attached: %v", f.fake.Attaches)
	}
}

func TestUpRefusesAForeignSandbox(t *testing.T) {
	f := newUpFixture(t)
	f.project(t, [2]string{"am", "repos: [{path: " + f.dir(t, "api") + "}]"})
	f.live("am.feat")
	err := f.up("am", "-w", "feat")
	if err == nil || !strings.Contains(err.Error(), "`sbx rm am.feat`") || !strings.Contains(err.Error(), "`den rm am.feat`") {
		t.Fatalf("error %v, want the foreign-sandbox refusal", err)
	}
	if f.hasEnvCreate() || len(f.fake.Attaches) != 0 {
		t.Errorf("calls %v after a refusal", f.fake.Calls)
	}
	for _, path := range []string{filepath.Join(f.home, "wt"), filepath.Join(f.home, "gen"), filepath.Join(f.proj, ".den")} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s exists after a refusal", path)
		}
	}
}

func respJSON(s string) sbx.Response { return sbx.Response{Output: []byte(s)} }

func TestUpRepoMountsAnExtraFolderAndKeepsItWithoutDrift(t *testing.T) {
	f := newUpFixture(t)
	api, extra := f.dir(t, "api"), f.dir(t, "extra")
	f.project(t, [2]string{"am", "repos: [{path: " + api + "}]"})
	if err := f.up("am", "--detach", "--repo", extra); err != nil {
		t.Fatal(err)
	}
	rec, err := registry.Read(f.home, "am")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rec.Repos {
		got = append(got, r.Repo)
	}
	if strings.Join(got, ",") != api+","+extra {
		t.Fatalf("recorded repos %v, want the nest's then the --repo one", got)
	}
	f.live("am")
	if err := f.up("am"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.err.String(), "changed since") {
		t.Errorf("drift reported on the next up: %q", f.err.String())
	}
}

func TestUpWarnsThatRepoIsIgnoredOnALiveInstance(t *testing.T) {
	f := newUpFixture(t)
	f.liveAm(t, "stale")
	if err := f.up("am", "--repo", f.dir(t, "extra")); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"warning: ", "--repo is ignored", "am"} {
		if !strings.Contains(f.err.String(), w) {
			t.Errorf("stderr %q lacks %q", f.err.String(), w)
		}
	}
}

func TestUpRefusesAMissingRepoFolderBeforeAnySideEffect(t *testing.T) {
	f := newUpFixture(t)
	f.project(t, [2]string{"am", "repos: [{path: " + f.dir(t, "api") + "}]"})
	missing := filepath.Join(f.src, "nope")
	err := f.up("am", "--repo", missing)
	if err == nil || !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "--repo") {
		t.Fatalf("error %v, want a refusal naming the folder and --repo", err)
	}
	if f.hasEnvCreate() {
		t.Errorf("created despite the refusal: %v", f.fake.Calls)
	}
}
