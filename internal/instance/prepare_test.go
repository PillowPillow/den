package instance

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/prompt"
	"github.com/PillowPillow/den/internal/registry"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sbxenv"
	"github.com/PillowPillow/den/internal/worktree"
)

func TestMain(m *testing.M) {
	worktree.NeutralizeGitEnvironment()
	os.Exit(m.Run())
}

const (
	okVersion   = "sbx version v0.53.0 abc\n"
	okTemplates = `[{"repository":"docker.io/library/b","tag":"v1"}]`
	bypass      = `{"permissions":{"defaultMode":"bypassPermissions"},"model":"x"}`
)

type fixture struct {
	home, proj, src string
	fake            *sbx.Fake
	prompt          *prompt.Fake
	git             worktree.Git
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{home: t.TempDir(), proj: t.TempDir(), src: t.TempDir(), prompt: &prompt.Fake{}}
	writeFile(t, filepath.Join(f.home, "profiles", "p", "settings.json"), bypass)
	f.fake = &sbx.Fake{Responses: map[string]sbx.Response{
		"version":            {Output: []byte(okVersion)},
		"ls --json":          {Output: []byte(`[]`)},
		"template ls --json": {Output: []byte(okTemplates)},
	}}
	return f
}

func (f *fixture) deps() Deps {
	return Deps{Sbx: f.fake, Git: f.git, Prompt: f.prompt, DenHome: f.home, Cwd: f.proj, Out: io.Discard, Err: io.Discard}
}

func (f *fixture) respond(key, out string) { f.fake.Responses[key] = sbx.Response{Output: []byte(out)} }

func (f *fixture) live(name string) {
	f.respond("ls --json", fmt.Sprintf(`[{"name":%q,"status":"running"}]`, name))
}

// repo creates a plain directory under src; gitRepo makes it a git repository.
func (f *fixture) repo(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(f.src, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *fixture) gitRepo(t *testing.T, name string) string {
	t.Helper()
	p := f.repo(t, name)
	f.git = worktree.NewGit()
	if _, err := f.git.Run(context.Background(), p, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func repoEntry(path string, optional bool) string {
	return fmt.Sprintf("{path: %q, optional: %t}", path, optional)
}

// denYAML declares the nests given as name → body, the body's lines indented under the name.
func denYAML(nests map[string]string) string {
	var b strings.Builder
	b.WriteString("schema: 1\nbases:\n  b: {image: \"b:v1\"}\nnests:\n")
	names := make([]string, 0, len(nests))
	for n := range nests {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "  %s:\n    base: b\n    profile: p\n", n)
		for _, line := range strings.Split(strings.TrimSpace(nests[n]), "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	return b.String()
}

func repos(entries ...string) string { return "repos:\n  - " + strings.Join(entries, "\n  - ") }

func (f *fixture) project(t *testing.T, nests map[string]string) {
	t.Helper()
	writeFile(t, filepath.Join(f.proj, "den.yaml"), denYAML(nests))
}

func (f *fixture) global(t *testing.T, nests map[string]string) {
	t.Helper()
	writeFile(t, filepath.Join(f.home, "den.yaml"), denYAML(nests))
}

func mustPrepare(t *testing.T, f *fixture, o Options) *Plan {
	t.Helper()
	p, err := Prepare(context.Background(), f.deps(), o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func prepareErr(t *testing.T, f *fixture, o Options, wants ...string) {
	t.Helper()
	_, err := Prepare(context.Background(), f.deps(), o)
	if err == nil {
		t.Fatalf("want an error containing %q", wants)
	}
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q lacks %q", err, w)
		}
	}
}

// record writes what Task 8's Create would record for p.
func record(t *testing.T, home string, p *Plan) {
	t.Helper()
	r := registry.Record{
		Instance: p.Instance, Nest: p.Resolved.Name, ConfigFile: p.Resolved.File.Path,
		RunDir: p.RunDir, GenDir: p.GenDir, EnvFiles: []string{filepath.Join(p.RunDir, "sbxenv.yaml")},
		ConfigHash: p.ConfigHash, Start: p.Start,
	}
	for _, repo := range p.Repos {
		r.Repos = append(r.Repos, registry.Repo{Name: repo.Name, Repo: repo.Path, Mount: repo.Path, Target: repo.Path})
	}
	if p.Worktree != nil {
		r.Worktree = &registry.Worktree{Branch: p.Worktree.Branch, Dir: p.Worktree.Dir, Layout: p.Resolved.Layout, Root: p.Resolved.Root}
	}
	if err := registry.Write(home, r); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRefusesInteractiveWithNoInput(t *testing.T) {
	f := newFixture(t)
	prepareErr(t, f, Options{Nest: "am", NoInput: true, Selection: nest.Selection{Interactive: true}},
		"-i cannot be used with den exec: it never asks")
	if len(f.fake.Calls) != 0 {
		t.Errorf("sbx calls %v, want none", f.fake.Calls)
	}
}

func TestPrepareRefusesAForeignSandbox(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	f.live("am")
	prepareErr(t, f, Options{Nest: "am"},
		`a sandbox named "am" exists but den V2 did not create it`, "`sbx rm am`", "`den rm am`")
	for _, c := range f.fake.Calls {
		if k := strings.Join(c, " "); k != "ls --json" && k != "version" {
			t.Errorf("unexpected sbx call %q", k)
		}
	}
}

func TestPrepareLiveSkipsSelection(t *testing.T) {
	f := newFixture(t)
	api, web := f.repo(t, "api"), f.repo(t, "web")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, true))})
	created := mustPrepare(t, f, Options{Nest: "am", Selection: nest.Selection{Without: []string{"web"}}})
	record(t, f.home, created)
	f.live("am")
	if err := os.RemoveAll(filepath.Join(f.home, "profiles")); err != nil {
		t.Fatal(err)
	}
	f.fake.Calls = nil

	p := mustPrepare(t, f, Options{Nest: "am", Selection: nest.Selection{Interactive: true}})
	if p.State != Live || p.Record == nil || p.Record.Instance != "am" {
		t.Fatalf("state %v record %+v, want Live with the record", p.State, p.Record)
	}
	if len(f.prompt.MultiSelects) != 0 {
		t.Errorf("prompted %d times on a live instance", len(f.prompt.MultiSelects))
	}
	if f.fake.HasCalled("template") {
		t.Errorf("checked templates on a live instance: %v", f.fake.Calls)
	}
	if want := []nest.Repo{{Name: "api", Path: api}}; !reflect.DeepEqual(p.Repos, want) {
		t.Errorf("repos %+v, want the recorded %+v", p.Repos, want)
	}
	if p.Start != created.Start {
		t.Errorf("start %q, want the recorded %q", p.Start, created.Start)
	}
}

func TestPrepareChecksSbxOnTheLivePath(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	record(t, f.home, mustPrepare(t, f, Options{Nest: "am"}))
	f.live("am")
	f.respond("version", "sbx version v0.52.0 abc\n")
	prepareErr(t, f, Options{Nest: "am"}, "v0.52.0", sbx.MinVersion)
}

func TestPrepareRefusesAMissingProfile(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	settings := filepath.Join(f.home, "profiles", "p", "settings.json")
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}
	prepareErr(t, f, Options{Nest: "am"}, settings, "profile")
}

func TestPrepareRefusesAProfileWithoutBypass(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	settings := writeFile(t, filepath.Join(f.home, "profiles", "p", "settings.json"), `{"permissions":{"defaultMode":"acceptEdits"}}`)
	prepareErr(t, f, Options{Nest: "am"}, settings, "permissions.defaultMode", "bypassPermissions")
}

func TestPrepareNamesDenBuildWhenTheImageIsMissing(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	f.respond("template ls --json", `[{"repository":"docker.io/library/other","tag":"v1"}]`)
	prepareErr(t, f, Options{Nest: "am"}, "b:v1", "run `den build b` from "+f.proj)
}

func TestPrepareNamesDenBuildAloneForAGlobalBase(t *testing.T) {
	f := newFixture(t)
	f.global(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	f.respond("template ls --json", `[{"repository":"docker.io/library/other","tag":"v1"}]`)
	_, err := Prepare(context.Background(), f.deps(), Options{Nest: "am"})
	if err == nil || !strings.Contains(err.Error(), "run `den build b`") || strings.Contains(err.Error(), " from ") {
		t.Errorf("error %v, want `den build b` with no folder: the global file is visible from anywhere", err)
	}
}

func TestPrepareRefusesAnOldSbx(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	f.respond("version", "sbx version v0.52.0 abc\n")
	prepareErr(t, f, Options{Nest: "am"}, "v0.52.0", sbx.MinVersion)
	if f.fake.HasCalled("ls") {
		t.Errorf("listed sandboxes under an sbx below the floor: %v", f.fake.Calls)
	}
}

func TestPrepareRefusesAnUnreadableSbxVersion(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	f.respond("version", "garbage\n")
	prepareErr(t, f, Options{Nest: "am"}, "sbx version", sbx.MinVersion)
}

func TestPrepareStartDefaultsToTheFirstRepo(t *testing.T) {
	f := newFixture(t)
	api, web := f.repo(t, "api"), f.repo(t, "web")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, false))})
	p := mustPrepare(t, f, Options{Nest: "am"})
	if p.Start != api {
		t.Errorf("start %q, want %q", p.Start, api)
	}
}

func TestPrepareStartDotIsTheProjectFolder(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) + "\nstart: ."})
	p := mustPrepare(t, f, Options{Nest: "am"})
	if p.Start != f.proj {
		t.Errorf("start %q, want the project folder %q", p.Start, f.proj)
	}
}

func TestPrepareWorkdirOverridesTheStart(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	base := mustPrepare(t, f, Options{Nest: "am"})
	p := mustPrepare(t, f, Options{Nest: "am", Workdir: "/somewhere/else"})
	if p.Start != base.Start || p.Workdir != "/somewhere/else" {
		t.Errorf("start %q workdir %q, want the mapped %q and the --workdir value", p.Start, p.Workdir, base.Start)
	}
	if p.ConfigHash != base.ConfigHash {
		t.Errorf("--workdir changed the config hash; a later `den up` without it would report drift")
	}
}

func TestPrepareStartRepoUnderWorktreeIsTheMainClonePath(t *testing.T) {
	f := newFixture(t)
	api := f.gitRepo(t, "api")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false)) + "\nstart: api/sub"})
	p := mustPrepare(t, f, Options{Nest: "am", Branch: "feat"})
	if want := filepath.Join(api, "sub"); p.Start != want {
		t.Errorf("start %q, want %q", p.Start, want)
	}
	if p.Worktree == nil || *p.Worktree != (worktree.Name{Dir: "feat", Branch: "feat"}) {
		t.Errorf("worktree %+v", p.Worktree)
	}
}

func TestPrepareRefusesAStartInARepoTheSelectionDropped(t *testing.T) {
	f := newFixture(t)
	api, web := f.repo(t, "api"), f.repo(t, "web")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, true)) + "\nstart: web/src"})
	prepareErr(t, f, Options{Nest: "am", Selection: nest.Selection{Without: []string{"web"}}}, `"web"`, "--without")
}

func TestPrepareRefusesAnUnmappableStartEvenWithWorkdir(t *testing.T) {
	f := newFixture(t)
	api, web := f.repo(t, "api"), f.repo(t, "web")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, true)) + "\nstart: web/src"})
	o := Options{Nest: "am", Workdir: "/elsewhere", Selection: nest.Selection{Without: []string{"web"}}}
	_, err := Prepare(context.Background(), f.deps(), o)
	if err == nil {
		t.Fatal("want a refusal: --workdir is one-off and the record needs a start")
	}
	for _, w := range []string{`"web"`, "--without", "fix `start:` in " + filepath.Join(f.proj, "den.yaml")} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q lacks %q", err, w)
		}
	}
	if strings.Contains(err.Error(), "--workdir") {
		t.Errorf("error %q offers --workdir, which no longer sets the start", err)
	}
}

func TestPrepareRefusesAStartNamingNoRepo(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) + "\nstart: nope"})
	prepareErr(t, f, Options{Nest: "am"}, `"nope"`, filepath.Join(f.proj, "den.yaml"))
}

func TestPrepareFlattensTheBranch(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.gitRepo(t, "api"), false))})
	p := mustPrepare(t, f, Options{Nest: "am", Branch: "feature/12"})
	if p.Instance != "am.feature-12" {
		t.Errorf("instance %q, want am.feature-12", p.Instance)
	}
	if p.Worktree == nil || *p.Worktree != (worktree.Name{Dir: "feature-12", Branch: "feature/12"}) {
		t.Errorf("worktree %+v", p.Worktree)
	}
}

func TestPrepareWithABranchRefusesARepoThatIsNotAGitRoot(t *testing.T) {
	for name, mk := range map[string]func(*testing.T, *fixture) string{
		"plain directory": func(t *testing.T, f *fixture) string { f.git = worktree.NewGit(); return f.repo(t, "api") },
		"subdirectory": func(t *testing.T, f *fixture) string {
			sub := filepath.Join(f.gitRepo(t, "mono"), "api")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			return sub
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			path := mk(t, f)
			f.project(t, map[string]string{"am": repos(repoEntry(path, false))})
			prepareErr(t, f, Options{Nest: "am", Branch: "feat"}, path, "-w")
		})
	}
}

func TestPrepareRefusesAMissingRepo(t *testing.T) {
	f := newFixture(t)
	missing := filepath.Join(f.src, "gone")
	f.project(t, map[string]string{"am": repos(repoEntry(missing, false))})
	prepareErr(t, f, Options{Nest: "am"}, missing, filepath.Join(f.proj, "den.yaml"))
}

func TestPrepareRefusesALocalKitWithoutASpec(t *testing.T) {
	f := newFixture(t)
	kit := filepath.Join(f.src, "kit")
	if err := os.MkdirAll(kit, 0o755); err != nil {
		t.Fatal(err)
	}
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) +
		fmt.Sprintf("\nkits: [%q, \"git+https://example.com/kit.git\"]", kit)})
	prepareErr(t, f, Options{Nest: "am"}, filepath.Join(kit, "spec.yaml"))

	writeFile(t, filepath.Join(kit, "spec.yaml"), "kind: mixin\n")
	mustPrepare(t, f, Options{Nest: "am"})
}

func TestPrepareNamesTheGlobalKeyOfAMissingRepo(t *testing.T) {
	f := newFixture(t)
	missing := filepath.Join(f.src, "gone")
	global := writeFile(t, filepath.Join(f.home, "den.yaml"), fmt.Sprintf("schema: 1\nrepos: {api: %q}\n", missing))
	writeFile(t, filepath.Join(f.proj, "den.yaml"),
		"schema: 1\nbases:\n  b: {image: \"b:v1\"}\nnests:\n  am:\n    base: b\n    profile: p\n    repos: [{key: api}]\n")
	prepareErr(t, f, Options{Nest: "am"}, missing, "fix `repos.api` in "+global)
}

func TestPrepareNamesTheFileDeclaringTheBaseOfABrokenKit(t *testing.T) {
	f := newFixture(t)
	kit := filepath.Join(f.src, "kit")
	if err := os.MkdirAll(kit, 0o755); err != nil {
		t.Fatal(err)
	}
	global := writeFile(t, filepath.Join(f.home, "den.yaml"),
		fmt.Sprintf("schema: 1\nbases:\n  g: {image: \"b:v1\", kits: [%q]}\n", kit))
	writeFile(t, filepath.Join(f.proj, "den.yaml"), fmt.Sprintf(
		"schema: 1\nnests:\n  am:\n    base: g\n    profile: p\n    repos: [{path: %q}]\n", f.repo(t, "api")))
	prepareErr(t, f, Options{Nest: "am"}, filepath.Join(kit, "spec.yaml"), "fix `kits:` of base \"g\" in "+global)
}

func kitBody(f *fixture, kit string) string {
	return repos(repoEntry(filepath.Join(f.src, "api"), false)) + fmt.Sprintf("\nkits: [%q]", kit)
}

func TestPrepareAcceptsAKitUnderTheProjectFolderWithoutStartDot(t *testing.T) {
	f := newFixture(t)
	f.repo(t, "api")
	writeFile(t, filepath.Join(f.proj, "kits", "mine", "spec.yaml"), "kind: mixin\n")
	f.project(t, map[string]string{"am": kitBody(f, "./kits/mine")})
	mustPrepare(t, f, Options{Nest: "am"})
}

func TestPrepareAcceptsAKitBesideARepo(t *testing.T) {
	f := newFixture(t)
	f.repo(t, "api")
	kit := filepath.Dir(writeFile(t, filepath.Join(f.src, "api-kits", "mine", "spec.yaml"), "kind: mixin\n"))
	f.global(t, map[string]string{"am": kitBody(f, kit)})
	mustPrepare(t, f, Options{Nest: "am"})
}

func TestPrepareGlobalNestWorkspaceIsTheRunDir(t *testing.T) {
	f := newFixture(t)
	f.global(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	p := mustPrepare(t, f, Options{Nest: "am"})
	if want := filepath.Join(f.home, "run", "am"); p.RunDir != want || p.Workspace != want {
		t.Errorf("run dir %q workspace %q, want both %q", p.RunDir, p.Workspace, want)
	}
	if want := filepath.Join(f.home, "gen", "am"); p.GenDir != want {
		t.Errorf("gen dir %q, want %q", p.GenDir, want)
	}
}

func TestPrepareProjectNestWorkspaceIsTheRunDirUnlessStartIsDot(t *testing.T) {
	runDir := func(f *fixture) string { return filepath.Join(f.proj, ".den", "run", "am") }
	for start, want := range map[string]func(*fixture) string{
		"":  runDir,
		".": func(f *fixture) string { return f.proj },
	} {
		t.Run("start="+start, func(t *testing.T) {
			f := newFixture(t)
			body := repos(repoEntry(f.repo(t, "api"), false))
			if start != "" {
				body += "\nstart: " + start
			}
			f.project(t, map[string]string{"am": body})
			p := mustPrepare(t, f, Options{Nest: "am"})
			if p.RunDir != runDir(f) {
				t.Errorf("run dir %q, want %q", p.RunDir, runDir(f))
			}
			if p.Workspace != want(f) {
				t.Errorf("workspace %q, want %q", p.Workspace, want(f))
			}
			if p.GenDir != filepath.Join(f.home, "gen", "am") {
				t.Errorf("gen dir %q", p.GenDir)
			}
		})
	}
}

func layerBody(f *fixture, layer string) string {
	return repos(repoEntry(filepath.Join(f.src, "api"), false)) + fmt.Sprintf("\nlayers: [%q]", layer)
}

func TestPrepareAcceptsALayerBesideARepo(t *testing.T) {
	f := newFixture(t)
	f.repo(t, "api")
	layer := writeFile(t, filepath.Join(f.src, "api-layers", "x.yaml"), "secrets: {}\n")
	f.global(t, map[string]string{"am": layerBody(f, layer)})
	p := mustPrepare(t, f, Options{Nest: "am"})
	if !reflect.DeepEqual(p.Resolved.Layers, []string{layer}) {
		t.Errorf("layers %v", p.Resolved.Layers)
	}
}

func TestPrepareRefusesAMissingLayer(t *testing.T) {
	f := newFixture(t)
	f.repo(t, "api")
	layer := filepath.Join(f.src, "nope.yaml")
	f.global(t, map[string]string{"am": layerBody(f, layer)})
	prepareErr(t, f, Options{Nest: "am"}, layer, "layers")
}

func ro(path, target string) sbxenv.Mount {
	return sbxenv.Mount{Path: path, Target: target, ReadOnly: true}
}

func wantOverlays(t *testing.T, p *Plan, want ...sbxenv.Mount) {
	t.Helper()
	if len(p.Overlays) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(p.Overlays, want) {
		t.Errorf("overlays\n%+v\nwant\n%+v", p.Overlays, want)
	}
}

func symlink(t *testing.T, target, link string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestPrepareOverlaysAKitUnderTheWorkspace(t *testing.T) {
	f := newFixture(t)
	f.repo(t, "api")
	kit := filepath.Dir(writeFile(t, filepath.Join(f.proj, "kits", "k", "spec.yaml"), "kind: mixin\n"))
	f.project(t, map[string]string{"am": kitBody(f, "./kits/k") + "\nstart: ."})
	file := filepath.Join(f.proj, "den.yaml")
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}), ro(file, file), ro(kit, kit))
}

func TestPrepareOverlaysALayerUnderARepo(t *testing.T) {
	f := newFixture(t)
	api := f.repo(t, "api")
	layer := writeFile(t, filepath.Join(api, "sbx", "extra.yaml"), "secrets: {}\n")
	f.global(t, map[string]string{"am": layerBody(f, layer)})
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}), ro(layer, layer))
}

func TestPrepareOverlaysTheProjectDenYamlWithStartDot(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) + "\nstart: ."})
	file := filepath.Join(f.proj, "den.yaml")
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}), ro(file, file))

	f.project(t, map[string]string{"am": repos(repoEntry(filepath.Join(f.src, "api"), false))})
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}))
}

func TestPrepareOverlaysTheProjectDenYamlAtTheRootOfARepo(t *testing.T) {
	f := newFixture(t)
	f.proj = f.repo(t, "api")
	f.project(t, map[string]string{"am": repos(repoEntry(f.proj, false))})
	file := filepath.Join(f.proj, "den.yaml")
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}), ro(file, file))
}

// buildBase writes a global file whose base b provisions with these includes and steps.
func buildBase(t *testing.T, f *fixture, includes, steps []string) string {
	t.Helper()
	flow := func(paths []string) string {
		quoted := make([]string, len(paths))
		for i, p := range paths {
			quoted[i] = fmt.Sprintf("%q", p)
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	}
	return writeFile(t, filepath.Join(f.home, "den.yaml"), fmt.Sprintf(
		"schema: 1\nbases:\n  b: {image: \"b:v1\", build: {includes: %s, steps: %s}}\nnests:\n  am:\n    base: b\n    profile: p\n    repos: [{path: %q}]\n",
		flow(includes), flow(steps), filepath.Join(f.src, "api")))
}

func TestPrepareOverlaysBuildStepsUnderAMountedRepo(t *testing.T) {
	f := newFixture(t)
	api := f.repo(t, "api")
	include := writeFile(t, filepath.Join(api, "build", "lib.sh"), "set -e\n")
	step := writeFile(t, filepath.Join(api, "build", "10-node.sh"), "true\n")
	outside := writeFile(t, filepath.Join(f.home, "steps", "20-go.sh"), "true\n")
	buildBase(t, f, []string{include}, []string{step, outside})
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}), ro(include, include), ro(step, step))
}

func TestPrepareRefusesAMissingBuildStepUnderAMountedRepo(t *testing.T) {
	f := newFixture(t)
	api := f.repo(t, "api")
	step := filepath.Join(api, "build", "10-node.sh")
	global := buildBase(t, f, nil, []string{step})
	prepareErr(t, f, Options{Nest: "am"}, step, "fix `build.steps` of base \"b\" in "+global)
}

func TestPrepareMapsTheOverlayTargetThroughAWorktreeMount(t *testing.T) {
	f := newFixture(t)
	api := f.gitRepo(t, "api")
	wt := worktree.Path("central", filepath.Join(f.home, "wt"), "feat", api)
	inTree := filepath.Dir(writeFile(t, filepath.Join(wt, "kit", "spec.yaml"), "kind: mixin\n"))
	inGit := filepath.Dir(writeFile(t, filepath.Join(api, ".git", "den-kit", "spec.yaml"), "kind: mixin\n"))
	f.global(t, map[string]string{"am": repos(repoEntry(api, false)) + fmt.Sprintf("\nkits: [%q, %q]", inTree, inGit)})
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am", Branch: "feat"}),
		ro(inTree, filepath.Join(api, "kit")),
		ro(worktree.ResolvePath(inGit), "/den-git/api/den-kit"))
}

func TestPrepareAddsNoOverlayOutsideMounts(t *testing.T) {
	f := newFixture(t)
	f.repo(t, "api")
	kit := filepath.Dir(writeFile(t, filepath.Join(f.home, "kits", "mine", "spec.yaml"), "kind: mixin\n"))
	f.global(t, map[string]string{"am": kitBody(f, kit)})
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}))
}

func TestPrepareOverlaysASymlinkedKitByItsResolvedForm(t *testing.T) {
	f := newFixture(t)
	api := f.repo(t, "api")
	realKit := filepath.Dir(writeFile(t, filepath.Join(api, "kits", "k", "spec.yaml"), "kind: mixin\n"))
	link := symlink(t, realKit, filepath.Join(f.home, "kits", "link"))
	f.global(t, map[string]string{"am": kitBody(f, link)})
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}), ro(worktree.ResolvePath(realKit), realKit))
}

func TestPrepareOverlaysASymlinkedLayerInsideARepoAtItsLexicalPath(t *testing.T) {
	for name, link := range map[string]func(t *testing.T, api, safe string) string{
		"leaf symlink": func(t *testing.T, api, safe string) string {
			return symlink(t, filepath.Join(safe, "x.yaml"), filepath.Join(api, "sbx", "extra.yaml"))
		},
		"symlinked directory": func(t *testing.T, api, safe string) string {
			return filepath.Join(symlink(t, safe, filepath.Join(api, "linkdir")), "x.yaml")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			api := f.repo(t, "api")
			safe := filepath.Join(f.src, "safe")
			writeFile(t, filepath.Join(safe, "x.yaml"), "secrets: {}\n")
			layer := link(t, api, safe)
			f.global(t, map[string]string{"am": layerBody(f, layer)})
			wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}), ro(layer, layer))
		})
	}
}

func TestPrepareOverlaysBothEndsOfALinkBetweenMountedRepos(t *testing.T) {
	f := newFixture(t)
	api, web := f.repo(t, "api"), f.repo(t, "web")
	realKit := filepath.Dir(writeFile(t, filepath.Join(web, "kits", "k", "spec.yaml"), "kind: mixin\n"))
	link := symlink(t, realKit, filepath.Join(api, "kitlink"))
	f.global(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, false)) + fmt.Sprintf("\nkits: [%q]", link)})
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}), ro(link, link), ro(worktree.ResolvePath(realKit), realKit))
}

func TestConfigHashIgnoresOtherNests(t *testing.T) {
	f := newFixture(t)
	am := repos(repoEntry(f.repo(t, "api"), false))
	other := repos(repoEntry(f.repo(t, "web"), false))
	f.project(t, map[string]string{"am": am, "other": other})
	before := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash

	f.project(t, map[string]string{"am": am, "other": other + "\nenv: {X: \"1\"}"})
	if got := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash; got != before {
		t.Errorf("editing another nest changed the hash of am")
	}

	f.project(t, map[string]string{"am": am + "\nenv: {X: \"1\"}", "other": other})
	if got := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash; got == before {
		t.Errorf("editing am did not change its hash")
	}
}

func TestConfigHashOfALiveInstanceUsesTheRecordedSelection(t *testing.T) {
	f := newFixture(t)
	api, web, db := f.repo(t, "api"), f.repo(t, "web"), f.repo(t, "db")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, true), repoEntry(db, true))})
	created := mustPrepare(t, f, Options{Nest: "am", Selection: nest.Selection{Only: []string{"web"}}})
	record(t, f.home, created)

	if recorded := mustPrepare(t, f, Options{Nest: "am"}); recorded.State != Recorded || recorded.ConfigHash == created.ConfigHash {
		t.Fatalf("state %v: without a VM the fresh selection must be hashed, and it differs from the subset", recorded.State)
	}

	f.live("am")
	p := mustPrepare(t, f, Options{Nest: "am"})
	if p.State != Live {
		t.Fatalf("state %v, want Live", p.State)
	}
	if p.ConfigHash != created.ConfigHash {
		t.Errorf("live hash %s differs from the creation hash %s: every `den up` would report drift", p.ConfigHash, created.ConfigHash)
	}
}

func TestPrepareWritesNothingButTheIndex(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	mustPrepare(t, f, Options{Nest: "am"})
	for root, want := range map[string][]string{
		f.home: {"profiles/p/settings.json", "state/v2/nests.yaml"},
		f.proj: {"den.yaml"},
	} {
		var got []string
		err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() {
				rel, _ := filepath.Rel(root, p)
				got = append(got, filepath.ToSlash(rel))
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s holds %v, want %v", root, got, want)
		}
	}
}
