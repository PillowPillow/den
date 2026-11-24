package instance

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/nest"
	"github.com/PillowPillow/den/v2/internal/prompt"
	"github.com/PillowPillow/den/v2/internal/registry"
	scanrepos "github.com/PillowPillow/den/v2/internal/repos"
	"github.com/PillowPillow/den/v2/internal/sbx"
	"github.com/PillowPillow/den/v2/internal/sbxenv"
	"github.com/PillowPillow/den/v2/internal/template"
	"github.com/PillowPillow/den/v2/internal/worktree"
)

func TestMain(m *testing.M) {
	worktree.NeutralizeGitEnvironment()
	os.Exit(m.Run())
}

const (
	okVersion      = "sbx version v0.54.0 abc\n"
	templateDigest = "sha256:4a94d9144a70df744fd9247e0c365a14cfb97024afab8db7597c8ba3e0637642"
	templateFrom   = "docker.io/docker/sandbox-templates:claude-code-docker"
	templateImage  = "docker.io/docker/sandbox-templates@" + templateDigest
)

type fixture struct {
	home, proj, src string
	fake            *sbx.Fake
	prompt          *prompt.Fake
	git             worktree.Git
	templates       []string // `templates:` entries project and global write
	sbxTemplates    []string // rows `sbx template ls --json` answers
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{home: t.TempDir(), proj: t.TempDir(), src: t.TempDir(), prompt: &prompt.Fake{}}
	f.fake = &sbx.Fake{Responses: map[string]sbx.Response{
		"version":            {Output: []byte(okVersion)},
		"ls --json":          {Output: []byte(`[]`)},
		"template ls --json": {Output: []byte(`[]`)},
	}}
	return f
}

// definition writes <dir>/den-template.yaml declaring name from the official image, with extra lines under it.
func definition(t *testing.T, dir, name, extra string) string {
	t.Helper()
	body := "schema: 1\ntemplates:\n  " + name + ":\n    from: claude-code-docker\n"
	for _, line := range strings.Split(strings.TrimSpace(extra), "\n") {
		if line != "" {
			body += "    " + line + "\n"
		}
	}
	return writeFile(t, filepath.Join(dir, config.DefinitionFileName), body)
}

// built records a build of name from src and makes sbx list <name>:v2, as `den build` leaves them.
func (f *fixture) built(t *testing.T, name string, src template.Source, commit, hash, digest string) {
	t.Helper()
	err := template.WriteRecord(f.home, template.Record{Template: name, Tag: name + ":v2", Source: src, Commit: commit,
		DefinitionHash: hash, From: template.From{Ref: templateFrom, Digest: digest}})
	if err != nil {
		t.Fatal(err)
	}
	row := fmt.Sprintf(`{"repository":"docker.io/library/%s","tag":"v2"}`, name)
	if !slices.Contains(f.sbxTemplates, row) {
		f.sbxTemplates = append(f.sbxTemplates, row)
	}
	f.respond("template ls --json", "["+strings.Join(f.sbxTemplates, ",")+"]")
}

// dirTemplate declares name as a dir location at dir, writes its definition and records a build of it.
func (f *fixture) dirTemplate(t *testing.T, name, dir, extra string) {
	t.Helper()
	definition(t, dir, name, extra)
	f.templates = append(f.templates, fmt.Sprintf("%s: {dir: %q}", name, dir))
	f.built(t, name, template.Source{Kind: "dir", Dir: dir}, "", "h1", templateDigest)
}

// repoTemplate declares name as a repo location and leaves its copy as `den build` does: a clone
// at the commit the record names.
func (f *fixture) repoTemplate(t *testing.T, name, url string) (copyDir, commit string) {
	t.Helper()
	if f.git == nil {
		f.git = worktree.NewGit()
	}
	copyDir = template.CopyDir(f.home, name)
	definition(t, copyDir, name, "")
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@example.test", "-c", "user.name=T", "add", "."},
		{"-c", "user.email=t@example.test", "-c", "user.name=T", "commit", "-q", "-m", "definition"},
	} {
		if _, err := f.git.Run(ctx, copyDir, args...); err != nil {
			t.Fatal(err)
		}
	}
	commit, err := template.Head(ctx, f.git, copyDir)
	if err != nil {
		t.Fatal(err)
	}
	f.templates = append(f.templates, fmt.Sprintf("%s: {repo: %q}", name, url))
	f.built(t, name, template.Source{Kind: "repo", URL: url}, commit, "h1", templateDigest)
	return copyDir, commit
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

// denYAML declares the templates entries given and the nests given as name → body, the body's lines
// indented under the name; a nest without `template:` runs on the official claude-code-docker image.
func denYAML(templates []string, nests map[string]string) string {
	var b strings.Builder
	b.WriteString("schema: 1\ndefault_template: claude-code-docker\n")
	if len(templates) > 0 {
		b.WriteString("templates:\n")
		for _, e := range templates {
			b.WriteString("  " + e + "\n")
		}
	}
	b.WriteString("nests:\n")
	names := make([]string, 0, len(nests))
	for n := range nests {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "  %s:\n", n)
		for _, line := range strings.Split(strings.TrimSpace(nests[n]), "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	return b.String()
}

func repos(entries ...string) string { return "repos:\n  - " + strings.Join(entries, "\n  - ") }

func (f *fixture) project(t *testing.T, nests map[string]string) {
	t.Helper()
	writeFile(t, filepath.Join(f.proj, "den.yaml"), denYAML(f.templates, nests))
}

func (f *fixture) global(t *testing.T, nests map[string]string) {
	t.Helper()
	writeFile(t, filepath.Join(f.home, "den.yaml"), denYAML(f.templates, nests))
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
	f.dirTemplate(t, "legacy", filepath.Join(f.src, "tpl"), "")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, true)) + "\ntemplate: legacy"})
	created := mustPrepare(t, f, Options{Nest: "am", Selection: nest.Selection{Without: []string{"web"}}})
	record(t, f.home, created)
	f.live("am")
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
	f.respond("version", "sbx version v0.53.0 abc\n")
	prepareErr(t, f, Options{Nest: "am"}, "v0.53.0", sbx.MinVersion)
}

func TestPrepareRefusesAMissingSbxTemplate(t *testing.T) {
	f := newFixture(t)
	f.dirTemplate(t, "digitaleo", filepath.Join(f.src, "tpl"), "")
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) + "\ntemplate: digitaleo"})
	f.respond("template ls --json", `[{"repository":"docker.io/library/other","tag":"v2"}]`)
	prepareErr(t, f, Options{Nest: "am"}, `template "digitaleo"`, "digitaleo:v2", "run `den build digitaleo` from "+f.proj)
}

func TestPrepareNamesDenBuildAloneForAGlobalTemplate(t *testing.T) {
	f := newFixture(t)
	f.dirTemplate(t, "digitaleo", filepath.Join(f.src, "tpl"), "")
	f.global(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) + "\ntemplate: digitaleo"})
	f.respond("template ls --json", `[{"repository":"docker.io/library/other","tag":"v2"}]`)
	_, err := Prepare(context.Background(), f.deps(), Options{Nest: "am"})
	if err == nil || !strings.Contains(err.Error(), "run `den build digitaleo`") || strings.Contains(err.Error(), " from ") {
		t.Errorf("error %v, want `den build digitaleo` with no folder: the global file is visible from anywhere", err)
	}
}

func TestPrepareRefusesACopyAtAnotherCommit(t *testing.T) {
	f := newFixture(t)
	copyDir, built := f.repoTemplate(t, "digitaleo", "https://example.test/acme/den-env.git")
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) + "\ntemplate: digitaleo"})
	mustPrepare(t, f, Options{Nest: "am"})

	if _, err := f.git.Run(context.Background(), copyDir, "-c", "user.email=t@example.test", "-c", "user.name=T",
		"commit", "-q", "--allow-empty", "-m", "moved"); err != nil {
		t.Fatal(err)
	}
	prepareErr(t, f, Options{Nest: "am"}, `template "digitaleo"`, built, copyDir, "run `den build digitaleo` from "+f.proj)
}

func TestPrepareOnAnImageSkipsTheTemplateCheck(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) + "\ntemplate: claude-code-docker"})
	p := mustPrepare(t, f, Options{Nest: "am"})
	if f.fake.HasCalled("template") {
		t.Errorf("listed sbx templates for an image: %v", f.fake.Calls)
	}
	mustCreate(t, f, p)
	if env := readFile(t, filepath.Join(p.RunDir, EnvFileName)); strings.Contains(env, "sandboxOptions") {
		t.Errorf("sbxenv.yaml names an sbx template for an image:\n%s", env)
	}
	kit := readFile(t, filepath.Join(p.GenDir, sbxenv.AgentName, "spec.yaml"))
	if !strings.Contains(kit, "\n  image: docker.io/docker/sandbox-templates:claude-code-docker\n") {
		t.Errorf("den-claude kit does not run the nest's image:\n%s", kit)
	}
}

func TestPrepareRefusesAnOldSbx(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	f.respond("version", "sbx version v0.53.0 abc\n")
	prepareErr(t, f, Options{Nest: "am"}, "v0.53.0", sbx.MinVersion)
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

func TestPrepareNamesTheProfileOfAMissingProfileKit(t *testing.T) {
	f := newFixture(t)
	kit := filepath.Join(f.home, "kits", "perso")
	global := writeFile(t, filepath.Join(f.home, "den.yaml"), fmt.Sprintf("schema: 1\nprofiles:\n  perso: [%q]\n", kit))
	writeFile(t, filepath.Join(f.proj, "den.yaml"),
		fmt.Sprintf("schema: 1\nprofile: perso\ndefault_template: claude-code-docker\nnests:\n  am:\n    repos: [{path: %q}]\n", f.repo(t, "api")))
	prepareErr(t, f, Options{Nest: "am"}, filepath.Join(kit, "spec.yaml"), "`profiles.perso` in "+global)

	writeFile(t, filepath.Join(kit, "spec.yaml"), "kind: mixin\n")
	p := mustPrepare(t, f, Options{Nest: "am"})
	if got := p.Resolved.Kits; len(got) != 1 || got[0] != kit {
		t.Errorf("kits %v, want [%s]", got, kit)
	}
}

func TestPrepareNamesTheGlobalKeyOfAMissingRepo(t *testing.T) {
	f := newFixture(t)
	missing := filepath.Join(f.src, "gone")
	global := writeFile(t, filepath.Join(f.home, "den.yaml"), fmt.Sprintf("schema: 1\nrepos: {api: %q}\n", missing))
	writeFile(t, filepath.Join(f.proj, "den.yaml"),
		"schema: 1\ndefault_template: claude-code-docker\nnests:\n  am:\n    repos: [{key: api}]\n")
	prepareErr(t, f, Options{Nest: "am"}, missing, "fix `repos.api` in "+global)
}

func TestPrepareNamesTheDefinitionOfABrokenKit(t *testing.T) {
	f := newFixture(t)
	loc := filepath.Join(f.src, "tpl")
	if err := os.MkdirAll(filepath.Join(loc, "kits", "k"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.dirTemplate(t, "legacy", loc, "kits: [./kits/k]")
	f.global(t, nil)
	writeFile(t, filepath.Join(f.proj, "den.yaml"), fmt.Sprintf(
		"schema: 1\nnests:\n  am:\n    template: legacy\n    repos: [{path: %q}]\n", f.repo(t, "api")))
	resolved := worktree.ResolvePath(loc)
	prepareErr(t, f, Options{Nest: "am"}, filepath.Join(resolved, "kits", "k", "spec.yaml"),
		"fix `kits:` of template \"legacy\" in "+filepath.Join(resolved, config.DefinitionFileName))
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

// stepsTemplate declares legacy as a dir location inside the mounted repo api, whose build reads these includes and steps.
func stepsTemplate(t *testing.T, f *fixture, api string, includes, steps []string) string {
	t.Helper()
	flow := func(paths []string) string {
		quoted := make([]string, len(paths))
		for i, p := range paths {
			quoted[i] = fmt.Sprintf("%q", p)
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	}
	loc := filepath.Join(api, "tpl")
	f.dirTemplate(t, "legacy", loc, fmt.Sprintf("build: {includes: %s, steps: %s}", flow(includes), flow(steps)))
	f.global(t, map[string]string{"am": repos(repoEntry(api, false)) + "\ntemplate: legacy"})
	return loc
}

func TestPrepareOverlaysBuildStepsUnderAMountedRepo(t *testing.T) {
	f := newFixture(t)
	api := f.repo(t, "api")
	include := writeFile(t, filepath.Join(api, "tpl", "lib.sh"), "set -e\n")
	step := writeFile(t, filepath.Join(api, "tpl", "10-node.sh"), "true\n")
	loc := stepsTemplate(t, f, api, []string{"./lib.sh"}, []string{"./10-node.sh"})
	defs := filepath.Join(loc, config.DefinitionFileName)
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}), ro(defs, defs),
		ro(worktree.ResolvePath(include), include), ro(worktree.ResolvePath(step), step))
}

func TestPrepareRefusesAMissingBuildStepUnderAMountedRepo(t *testing.T) {
	f := newFixture(t)
	api := f.repo(t, "api")
	loc := stepsTemplate(t, f, api, nil, []string{"./10-node.sh"})
	resolved := worktree.ResolvePath(loc)
	prepareErr(t, f, Options{Nest: "am"}, filepath.Join(resolved, "10-node.sh"),
		"fix `build.steps` of template \"legacy\" in "+filepath.Join(resolved, config.DefinitionFileName))
}

func TestPrepareAddsNoOverlayForADefinitionOutsideMounts(t *testing.T) {
	f := newFixture(t)
	f.repo(t, "api")
	loc := filepath.Join(f.src, "tpl")
	writeFile(t, filepath.Join(loc, "kits", "k", "spec.yaml"), "kind: mixin\n")
	writeFile(t, filepath.Join(loc, "10-node.sh"), "true\n")
	f.dirTemplate(t, "legacy", loc, "kits: [./kits/k]\nbuild: {steps: [./10-node.sh]}")
	f.global(t, map[string]string{"am": repos(repoEntry(filepath.Join(f.src, "api"), false)) + "\ntemplate: legacy"})
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}))
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

func TestPrepareRefusesASymlinkedLayerInsideARepo(t *testing.T) {
	for name, link := range map[string]func(t *testing.T, api, safe string) (layer, crossed string){
		"leaf symlink": func(t *testing.T, api, safe string) (string, string) {
			l := symlink(t, filepath.Join(safe, "x.yaml"), filepath.Join(api, "sbx", "extra.yaml"))
			return l, l
		},
		"symlinked directory": func(t *testing.T, api, safe string) (string, string) {
			dir := symlink(t, safe, filepath.Join(api, "linkdir"))
			return filepath.Join(dir, "x.yaml"), dir
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			api := f.repo(t, "api")
			safe := filepath.Join(f.src, "safe")
			writeFile(t, filepath.Join(safe, "x.yaml"), "secrets: {}\n")
			layer, crossed := link(t, api, safe)
			f.global(t, map[string]string{"am": layerBody(f, layer)})
			prepareErr(t, f, Options{Nest: "am"}, layer+" crosses the symlink "+crossed+" inside the mounted folder "+api,
				"move it out of every mounted folder or reference its real path",
				"fix `layers:` of nest \"am\" in "+filepath.Join(f.home, "den.yaml"))
		})
	}
}

func TestPrepareRefusesAKitLinkedBetweenMountedRepos(t *testing.T) {
	f := newFixture(t)
	api, web := f.repo(t, "api"), f.repo(t, "web")
	realKit := filepath.Dir(writeFile(t, filepath.Join(web, "kits", "k", "spec.yaml"), "kind: mixin\n"))
	link := symlink(t, realKit, filepath.Join(api, "kitlink"))
	f.global(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, false)) + fmt.Sprintf("\nkits: [%q]", link)})
	prepareErr(t, f, Options{Nest: "am"}, link+" crosses the symlink "+link+" inside the mounted folder "+api,
		"fix `kits:` of nest \"am\" in "+filepath.Join(f.home, "den.yaml"))
}

func TestPrepareRefusesAProjectReachedThroughASymlinkInsideARepo(t *testing.T) {
	f := newFixture(t)
	api := f.repo(t, "api")
	realProj := f.proj
	f.proj = symlink(t, realProj, filepath.Join(api, "proj"))
	f.project(t, map[string]string{"am": repos(repoEntry(api, false))})
	file := filepath.Join(f.proj, "den.yaml")
	prepareErr(t, f, Options{Nest: "am"}, file+" crosses the symlink "+f.proj+" inside the mounted folder "+api,
		"run den from "+worktree.ResolvePath(realProj))
}

func TestPrepareOverlaysAFileUnderASymlinkedRepoRoot(t *testing.T) {
	f := newFixture(t)
	realAPI := f.repo(t, "real-api")
	api := symlink(t, realAPI, filepath.Join(f.src, "api"))
	layer := writeFile(t, filepath.Join(api, "x.yaml"), "secrets: {}\n")
	f.global(t, map[string]string{"am": layerBody(f, layer)})
	wantOverlays(t, mustPrepare(t, f, Options{Nest: "am"}), ro(layer, layer))
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
		f.home: {"state/v2/nests.yaml"},
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

func TestConfigHashMovesWithTheTemplateBuild(t *testing.T) {
	f := newFixture(t)
	loc := filepath.Join(f.src, "tpl")
	f.dirTemplate(t, "legacy", loc, "")
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) + "\ntemplate: legacy"})
	created := mustPrepare(t, f, Options{Nest: "am"})
	record(t, f.home, created)
	f.live("am")
	if p := mustPrepare(t, f, Options{Nest: "am"}); p.State != Live || p.ConfigHash != created.ConfigHash {
		t.Fatalf("state %v: the same build must hash the same", p.State)
	}

	src := template.Source{Kind: "dir", Dir: loc}
	f.built(t, "legacy", src, "", "h2", templateDigest)
	rebuilt := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash
	if rebuilt == created.ConfigHash {
		t.Errorf("another definition_hash left the hash unchanged")
	}
	f.built(t, "legacy", src, "", "h2", "sha256:"+strings.Repeat("0", 64))
	if got := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash; got == rebuilt {
		t.Errorf("another from.digest left the hash unchanged")
	}

	var stderr strings.Builder
	d := f.deps()
	d.Err = &stderr
	if err := Up(context.Background(), d, Options{Nest: "am"}, false); err != nil {
		t.Fatalf("a rebuilt template must never refuse a live instance: %v", err)
	}
	if want := filepath.Join(f.proj, "den.yaml") + " changed since am was created — run `den rm am` then `den up am`"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr %q lacks %q", stderr.String(), want)
	}
	if !f.fake.HasAttached("run", "--name", "am") {
		t.Errorf("no attach: %v", f.fake.Calls)
	}
}

func TestPrepareRefusesAKitUnderTheInstancesOwnDirs(t *testing.T) {
	for _, c := range []struct {
		name, kind, rel string
		project         bool
	}{
		{"global run dir", "run", "run", false},
		{"gen dir", "gen", "gen", false},
		{"project run dir", "run", filepath.Join(".den", "run"), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.repo(t, "api")
			base, den := f.home, filepath.Join(f.home, "den.yaml")
			if c.project {
				base, den = f.proj, filepath.Join(f.proj, "den.yaml")
			}
			dir := filepath.Join(base, c.rel, "am")
			kit := filepath.Join(dir, "k")
			writeFile(t, filepath.Join(kit, "spec.yaml"), "kind: mixin\n")
			if c.project {
				f.project(t, map[string]string{"am": kitBody(f, kit)})
			} else {
				f.global(t, map[string]string{"am": kitBody(f, kit)})
			}
			prepareErr(t, f, Options{Nest: "am"}, kit, "den's "+c.kind+" dir "+dir, "move it out of den's "+c.kind+" dir "+dir,
				"fix `kits:` of nest \"am\" in "+den)
		})
	}
}

func TestPrepareRefusesAKitThatIsAMountedFolder(t *testing.T) {
	f := newFixture(t)
	api := f.repo(t, "api")
	writeFile(t, filepath.Join(api, "spec.yaml"), "kind: mixin\n")
	f.global(t, map[string]string{"am": kitBody(f, api)})
	prepareErr(t, f, Options{Nest: "am"}, api+" is the mounted folder "+api+" itself", "put it in a sub folder, or outside every mounted folder",
		"fix `kits:` of nest \"am\" in "+filepath.Join(f.home, "den.yaml"))
}

// livePointedAtAnUnbuiltTemplate leaves am Live on legacy, then points its nest at other, declared but never built.
func livePointedAtAnUnbuiltTemplate(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	f.dirTemplate(t, "legacy", filepath.Join(f.src, "tpl"), "")
	api := f.repo(t, "api")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false)) + "\ntemplate: legacy"})
	record(t, f.home, mustPrepare(t, f, Options{Nest: "am"}))
	f.live("am")
	f.templates = append(f.templates, fmt.Sprintf("other: {dir: %q}", filepath.Join(f.src, "other")))
	f.project(t, map[string]string{"am": repos(repoEntry(api, false)) + "\ntemplate: other"})
	return f, "template \"other\" is not built — run `den build other` from " + f.proj
}

func TestUpOnALiveInstanceWarnsAboutAnUnbuiltTemplate(t *testing.T) {
	f, notBuilt := livePointedAtAnUnbuiltTemplate(t)
	var stderr strings.Builder
	d := f.deps()
	d.Err = &stderr
	if err := Up(context.Background(), d, Options{Nest: "am"}, false); err != nil {
		t.Fatalf("a live instance must attach whatever its config became: %v", err)
	}
	for _, want := range []string{"warning: " + notBuilt, "changed since am was created"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr %q lacks %q", stderr.String(), want)
		}
	}
	if !f.fake.HasAttached("run", "--name", "am") {
		t.Errorf("no attach: %v", f.fake.Calls)
	}
}

func TestPrepareRefusesAnUnbuiltTemplateWhenNotLive(t *testing.T) {
	f, notBuilt := livePointedAtAnUnbuiltTemplate(t)
	f.respond("ls --json", `[]`)
	prepareErr(t, f, Options{Nest: "am"}, notBuilt)
	if f.fake.HasCalled("--no-input", "env", "create") {
		t.Errorf("env create ran: %v", f.fake.Calls)
	}
}

// remoteGit answers ls-remote itself and leaves every other git call to the real one.
type remoteGit struct {
	worktree.Git
	answer func(ctx context.Context) ([]byte, error)
}

func (g remoteGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if slices.Contains(args, "ls-remote") {
		return g.answer(ctx)
	}
	return g.Git.Run(ctx, dir, args...)
}

func repoTemplateNest(t *testing.T, answer func(ctx context.Context) ([]byte, error)) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	_, commit := f.repoTemplate(t, "legacy", "https://example.test/legacy.git")
	api := f.repo(t, "api")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false)) + "\ntemplate: legacy"})
	f.git = remoteGit{Git: f.git, answer: answer}
	return f, commit
}

func TestUpPrintsNoticesAndExecDoesNot(t *testing.T) {
	newer := "feedfacefeedfacefeedfacefeedfacefeedface"
	f, _ := repoTemplateNest(t, func(context.Context) ([]byte, error) { return []byte(newer + "\tHEAD\n"), nil })
	var upErr, execErr, execOut strings.Builder
	d := f.deps()
	d.Err = &upErr
	if err := Up(context.Background(), d, Options{Nest: "am"}, true); err != nil {
		t.Fatal(err)
	}
	want := "template legacy: a newer definition is available"
	if !strings.Contains(upErr.String(), want) || !strings.Contains(upErr.String(), "den build legacy --pull") {
		t.Errorf("den up stderr %q lacks the notice", upErr.String())
	}
	f.live("am")
	d = f.deps()
	d.Err, d.Out = &execErr, &execOut
	if err := Exec(context.Background(), d, Options{Nest: "am"}, []string{"true"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(execErr.String(), "newer definition") || strings.Contains(execOut.String(), "newer definition") {
		t.Errorf("den exec printed a notice: stderr %q stdout %q", execErr.String(), execOut.String())
	}
}

func TestUpNoticeRespectsItsDeadline(t *testing.T) {
	f, _ := repoTemplateNest(t, func(ctx context.Context) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	var stderr strings.Builder
	d := f.deps()
	d.Err = &stderr
	start := time.Now()
	if err := Up(context.Background(), d, Options{Nest: "am"}, true); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took >= 4*time.Second {
		t.Errorf("den up took %v behind a silent remote", took)
	}
	if strings.Contains(stderr.String(), "newer definition") {
		t.Errorf("stderr %q has a notice", stderr.String())
	}
}

func (f *fixture) globalWithEnv(t *testing.T, env string, nests map[string]string) string {
	t.Helper()
	return writeFile(t, filepath.Join(f.home, "den.yaml"), strings.Replace(denYAML(f.templates, nests), "nests:\n", "env:\n  "+env+"\nnests:\n", 1))
}

func TestPrepareRefusesDenReposInEnv(t *testing.T) {
	f := newFixture(t)
	path := f.globalWithEnv(t, "DEN_REPOS: x", map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	prepareErr(t, f, Options{Nest: "am"}, "DEN_REPOS", path, "remove it")
}

func TestPrepareAttachesLiveDespiteDenReposInEnv(t *testing.T) {
	f := newFixture(t)
	nests := map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))}
	f.global(t, nests)
	record(t, f.home, mustPrepare(t, f, Options{Nest: "am"}))
	f.globalWithEnv(t, "DEN_REPOS: x", nests)
	f.live("am")
	if p := mustPrepare(t, f, Options{Nest: "am"}); p.State != Live {
		t.Fatalf("state %v, want Live", p.State)
	}
}

func TestRepoFixNamesTheDeclaringFile(t *testing.T) {
	f := newFixture(t)
	missing := filepath.Join(f.src, "gone")
	project := writeFile(t, filepath.Join(f.proj, "den.yaml"),
		fmt.Sprintf("schema: 1\ndefault_template: claude-code-docker\nrepos:\n  api: %q\nnests:\n  am:\n    repos: [{key: api}]\n", missing))
	prepareErr(t, f, Options{Nest: "am"}, missing, "fix `repos.api` in "+project)
}

func TestRepoFixNamesDenScanForATableClone(t *testing.T) {
	f := newFixture(t)
	f.git = worktree.NewGit()
	notARepo := writeFile(t, filepath.Join(f.src, "api", ".git", "config"), "[remote \"origin\"]\n\turl = git@gitlab.com:dig/api.git\n")
	dir := filepath.Dir(filepath.Dir(notARepo))
	writeFile(t, filepath.Join(f.proj, "den.yaml"),
		"schema: 1\ndefault_template: claude-code-docker\nrepos:\n  api: {origin: \"git@gitlab.com:dig/api.git\"}\nnests:\n  am:\n    repos: [{key: api}]\n")
	if err := scanrepos.WriteTable(f.home, scanrepos.Table{Repos: map[string]scanrepos.Entry{"gitlab.com/dig/api": {Path: dir}}}); err != nil {
		t.Fatal(err)
	}
	_, err := Prepare(context.Background(), f.deps(), Options{Nest: "am", Branch: "feat"})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, w := range []string{dir, "den scan", scanrepos.TablePath(f.home), "gitlab.com/dig/api"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q lacks %q", err, w)
		}
	}
	if strings.Contains(err.Error(), "git@gitlab.com") {
		t.Errorf("error %q prints the raw origin", err)
	}
}

// missingKey writes the project nest am: the folder web, and the key api, whose origin has no folder.
func (f *fixture) missingKey(t *testing.T) (web string) {
	t.Helper()
	web = f.repo(t, "web")
	writeFile(t, filepath.Join(f.proj, "den.yaml"), fmt.Sprintf("schema: 1\ndefault_template: claude-code-docker\n"+
		"repos:\n  api: {origin: \"git@gitlab.com:dig/api.git\"}\nnests:\n  am:\n    repos: [{path: %q}, {key: api}]\n", web))
	return web
}

func (f *fixture) ttyDeps(stderr io.Writer) Deps {
	d := f.deps()
	d.IsTTY = func() bool { return true }
	d.Err = stderr
	return d
}

func TestMissingKeyAsksThenStartsWithoutIt(t *testing.T) {
	f := newFixture(t)
	web := f.missingKey(t)
	f.prompt.ConfirmAnswers = []bool{true}
	var stderr bytes.Buffer
	p, err := Prepare(context.Background(), f.ttyDeps(&stderr), Options{Nest: "am"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Repos) != 1 || p.Repos[0].Path != web {
		t.Fatalf("repos %+v, want web alone", p.Repos)
	}
	if len(f.prompt.Confirms) != 1 || f.prompt.Confirms[0].Question != "start am without this repo?" {
		t.Fatalf("confirms %+v", f.prompt.Confirms)
	}
	if !strings.Contains(stderr.String(), "warning: nest \"am\": 1 repo has no folder on this machine:\n  - api: git clone git@gitlab.com:dig/api.git") {
		t.Fatalf("stderr %q does not list the missing key", stderr.String())
	}
}

func TestMissingKeyStopsWhenTheAnswerIsNo(t *testing.T) {
	f := newFixture(t)
	f.missingKey(t)
	f.prompt.ConfirmAnswers = []bool{false}
	_, err := Prepare(context.Background(), f.ttyDeps(io.Discard), Options{Nest: "am"})
	if err == nil || !strings.Contains(err.Error(), "am not started: clone this repo, then run `den up am` again") {
		t.Fatalf("err %v", err)
	}
}

func TestMissingKeyRefusesWithoutATerminal(t *testing.T) {
	f := newFixture(t)
	f.missingKey(t)
	prepareErr(t, f, Options{Nest: "am"}, "git clone git@gitlab.com:dig/api.git", "den up asks before it starts am without this repo")
	if len(f.prompt.Confirms) != 0 {
		t.Fatalf("asked %+v without a terminal", f.prompt.Confirms)
	}
}

func TestDenExecRefusesAMissingKey(t *testing.T) {
	f := newFixture(t)
	f.missingKey(t)
	_, err := Prepare(context.Background(), f.ttyDeps(io.Discard), Options{Nest: "am", NoInput: true})
	if err == nil || !strings.Contains(err.Error(), "den exec never asks") || len(f.prompt.Confirms) != 0 {
		t.Fatalf("err %v, confirms %+v", err, f.prompt.Confirms)
	}
}

func TestLiveInstanceAttachesWithAMissingKeyAsAWarning(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "web"), false))})
	record(t, f.home, mustPrepare(t, f, Options{Nest: "am"}))
	f.missingKey(t)
	f.live("am")
	p, err := Prepare(context.Background(), f.ttyDeps(io.Discard), Options{Nest: "am"})
	if err != nil {
		t.Fatal(err)
	}
	if p.State != Live || len(f.prompt.Confirms) != 0 {
		t.Fatalf("state %v, confirms %+v: want Live and no question", p.State, f.prompt.Confirms)
	}
	if !slices.ContainsFunc(p.Resolved.Warnings, func(w string) bool { return strings.Contains(w, "git clone git@gitlab.com:dig/api.git") }) {
		t.Fatalf("warnings %q lack the missing key", p.Resolved.Warnings)
	}
}

func TestMissingKeyIsNamedWhenItLeavesNoStartFolder(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.proj, "den.yaml"),
		"schema: 1\ndefault_template: claude-code-docker\nrepos:\n  api: {origin: \"git@gitlab.com:dig/api.git\"}\nnests:\n  am:\n    repos: [{key: api}]\n")
	prepareErr(t, f, Options{Nest: "am"}, "git clone git@gitlab.com:dig/api.git", "no repo is selected")
}

func TestWorktreeRefusesTwoReposWithTheSameFolderName(t *testing.T) {
	f := newFixture(t)
	a, b := f.repo(t, filepath.Join("a", "api")), f.repo(t, filepath.Join("b", "api"))
	writeFile(t, filepath.Join(f.home, "den.yaml"), fmt.Sprintf("schema: 1\nrepos:\n  front-api: %q\n  back-api: %q\n", a, b))
	f.project(t, map[string]string{"am": "repos: [{key: front-api}, {key: back-api}]"})
	prepareErr(t, f, Options{Nest: "am", Branch: "feat"},
		`repos front-api and back-api share the folder name "api", and -w puts both at `+filepath.Join(f.home, "wt", "feat", "api")+
			` — set worktrees.layout: per-repo, or drop one`)
	mustPrepare(t, f, Options{Nest: "am"})
}

func TestRecreateKeepsTheWorktreeOfARenamedKey(t *testing.T) {
	f := newFixture(t)
	dir := f.repo(t, "go.dgdev")
	res := &nest.Resolved{Layout: "central", Root: filepath.Join(f.home, "wt"), File: &config.Loaded{}}
	rec := &registry.Record{Instance: "am.feat", Repos: []registry.Repo{{
		Name: "go.dgdev", Repo: dir, Mount: filepath.Join(res.Root, "feat", "go.dgdev"), Worktree: true,
	}}}
	repos := []nest.Repo{{Name: "go-dgdev", Key: "go-dgdev", Path: dir}}
	if err := checkRecordedWorktrees(rec, res, repos, &worktree.Name{Dir: "feat", Branch: "feat"}); err != nil {
		t.Fatal(err)
	}
}

func TestCloningAMissingKeyLaterIsDriftOnTheLiveInstance(t *testing.T) {
	f := newFixture(t)
	f.missingKey(t)
	f.prompt.ConfirmAnswers = []bool{true}
	p, err := Prepare(context.Background(), f.ttyDeps(io.Discard), Options{Nest: "am"})
	if err != nil {
		t.Fatal(err)
	}
	record(t, f.home, p)
	f.live("am")
	if still := mustPrepare(t, f, Options{Nest: "am"}); still.ConfigHash != p.ConfigHash {
		t.Fatal("a key still missing is drift")
	}
	cfg := writeFile(t, filepath.Join(f.src, "api", ".git", "config"), "[remote \"origin\"]\n\turl = git@gitlab.com:dig/api.git\n")
	if err := scanrepos.WriteTable(f.home, scanrepos.Table{Repos: map[string]scanrepos.Entry{"gitlab.com/dig/api": {Path: filepath.Dir(filepath.Dir(cfg))}}}); err != nil {
		t.Fatal(err)
	}
	if cloned := mustPrepare(t, f, Options{Nest: "am"}); cloned.ConfigHash == p.ConfigHash {
		t.Fatal("the key now has a folder, and the hash of the running instance did not move")
	}
}

func TestConfigHashMovesOnlyWhenAutoTrustIsOff(t *testing.T) {
	f := newFixture(t)
	am := repos(repoEntry(f.repo(t, "api"), false))
	f.project(t, map[string]string{"am": am})
	before := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash

	f.project(t, map[string]string{"am": am + "\nauto_trust: true"})
	if got := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash; got != before {
		t.Errorf("an explicit auto_trust: true changed the hash: every instance made before the key would drift")
	}

	f.project(t, map[string]string{"am": am + "\nauto_trust: false"})
	if got := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash; got == before {
		t.Errorf("auto_trust: false did not change the hash")
	}
}

func TestConfigHashIgnoresAddDirs(t *testing.T) {
	f := newFixture(t)
	am := repos(repoEntry(f.repo(t, "api"), false))
	f.project(t, map[string]string{"am": am + "\nadd_dirs: false"})
	before := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash

	f.project(t, map[string]string{"am": am + "\nadd_dirs: true"})
	if got := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash; got != before {
		t.Errorf("add_dirs changed the hash: it applies per attach, and a live instance would warn about drift")
	}
}

func TestConfigHashMovesOnlyWhenResourcesAreSet(t *testing.T) {
	f := newFixture(t)
	am := repos(repoEntry(f.repo(t, "api"), false))
	f.project(t, map[string]string{"am": am})
	before := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash

	f.project(t, map[string]string{"am": am + "\nresources: {}"})
	if got := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash; got != before {
		t.Errorf("an empty resources: changed the hash: every instance made before the key would drift")
	}

	f.project(t, map[string]string{"am": am + "\nresources: {cpus: 4}"})
	cpus := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash
	if cpus == before {
		t.Errorf("cpus: 4 did not change the hash")
	}

	f.project(t, map[string]string{"am": am + "\nresources: {memory: 8g}"})
	if got := mustPrepare(t, f, Options{Nest: "am"}).ConfigHash; got == before || got == cpus {
		t.Errorf("memory: 8g did not give its own hash")
	}
}
