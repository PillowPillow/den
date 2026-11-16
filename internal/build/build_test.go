package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/instance"
	"github.com/PillowPillow/den/internal/prompt"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/template"
	"github.com/PillowPillow/den/internal/worktree"
)

func TestMain(m *testing.M) {
	worktree.NeutralizeGitEnvironment()
	os.Exit(m.Run())
}

const (
	digest           = "sha256:4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a"
	claudeCodeDocker = "docker.io/docker/sandbox-templates:claude-code-docker"
	pinnedClaude     = "docker.io/docker/sandbox-templates@" + digest
)

type fixture struct {
	home, cwd string
	fake      *sbx.Fake
	prompt    *prompt.Fake
	tty       bool
	out, err  bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{home: t.TempDir(), cwd: t.TempDir(), prompt: &prompt.Fake{}}
	f.fake = &sbx.Fake{Responses: map[string]sbx.Response{
		"version":   {Output: []byte("sbx version v0.53.0 abc\n")},
		"ls --json": {Output: []byte(`[]`)},
	}}
	f.templates(row("docker.io/docker/sandbox-templates", "claude-code-docker", digest))
	return f
}

func row(repository, tag, digest string) string {
	return fmt.Sprintf(`{"repository":%q,"tag":%q,"digest":%q}`, repository, tag, digest)
}

func (f *fixture) templates(rows ...string) {
	f.fake.Responses["template ls --json"] = sbx.Response{Output: []byte("[" + strings.Join(rows, ",") + "]")}
}

func (f *fixture) global(t *testing.T, body string) {
	t.Helper()
	writeFile(t, filepath.Join(f.home, "den.yaml"), "schema: 1\n"+body)
}

func (f *fixture) project(t *testing.T, body string) {
	t.Helper()
	writeFile(t, filepath.Join(f.cwd, "den.yaml"), "schema: 1\n"+body)
}

func (f *fixture) build(value string, o Options) error {
	f.out.Reset()
	f.err.Reset()
	d := Deps{
		Deps: instance.Deps{Sbx: f.fake, Git: worktree.NewGit(), Prompt: f.prompt, DenHome: f.home, Cwd: f.cwd,
			Out: &f.out, Err: &f.err},
		IsTTY: func() bool { return f.tty },
	}
	return Build(context.Background(), d, value, o)
}

func (f *fixture) calls() []string {
	var out []string
	for _, c := range f.fake.Calls {
		if k := strings.Join(c, " "); k != "version" && k != "ls --json" {
			out = append(out, k)
		}
	}
	return out
}

func (f *fixture) saves() []string {
	var out []string
	for _, c := range f.calls() {
		if strings.HasPrefix(c, "template save ") {
			out = append(out, c)
		}
	}
	return out
}

func (f *fixture) envFile(name string) string {
	return filepath.Join(f.home, "gen", ".build", name, "sbxenv.yaml")
}

func (f *fixture) record(t *testing.T, name string) template.Record {
	t.Helper()
	r, err := template.ReadRecord(f.home, name)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// dirTemplate writes a location declaring defs, with one.sh and two.sh beside it, and declares
// it in the global den.yaml under name.
func (f *fixture) dirTemplate(t *testing.T, name, defs string) string {
	t.Helper()
	loc := t.TempDir()
	writeLocation(t, loc, defs)
	f.global(t, "templates:\n  "+name+":\n    dir: "+loc+"\n")
	return loc
}

func writeLocation(t *testing.T, dir, defs string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "den-template.yaml"), defs)
	writeFile(t, filepath.Join(dir, "one.sh"), "echo one\n")
	writeFile(t, filepath.Join(dir, "two.sh"), "echo two\n")
}

func defs(name, body string) string {
	return "schema: 1\ntemplates:\n  " + name + ":\n" + body
}

var oneStep = "    from: claude-code-docker\n    build:\n      steps: [one.sh]\n"

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo is a source repository holding a location; it returns its path, which is also its URL.
func newRepo(t *testing.T, defs string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	writeLocation(t, dir, defs)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func commitFile(t *testing.T, repo, name, body string) string {
	t.Helper()
	writeFile(t, filepath.Join(repo, name), body)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "change "+name)
	return git(t, repo, "rev-parse", "HEAD")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestBuildFromADirWritesTheRecord(t *testing.T) {
	f := newFixture(t)
	loc := f.dirTemplate(t, "legacy", defs("legacy", oneStep))
	if err := f.build("legacy", Options{}); err != nil {
		t.Fatal(err)
	}
	r := f.record(t, "legacy")
	loaded, err := config.LoadDefinitions(loc)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := template.DefinitionHash(loaded, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if r.Tag != "legacy:v2" || r.Source != (template.Source{Kind: "dir", Dir: loc}) || r.Commit != "" {
		t.Errorf("record %+v", r)
	}
	if r.From != (template.From{Ref: claudeCodeDocker, Digest: digest}) {
		t.Errorf("from %+v, want %s at %s", r.From, claudeCodeDocker, digest)
	}
	if r.DefinitionHash != hash {
		t.Errorf("definition_hash %s, want %s", r.DefinitionHash, hash)
	}
	if !f.fake.HasCalled("template", "save", "legacy-build", "legacy:v2") {
		t.Errorf("calls %v", f.calls())
	}
	if want := "den: building template legacy (legacy:v2)\n"; !strings.Contains(f.err.String(), want) {
		t.Errorf("stderr %q lacks %q", f.err.String(), want)
	}
}

func TestBuildClonesARepoIntoItsCopy(t *testing.T) {
	f := newFixture(t)
	repo := newRepo(t, defs("digitaleo", oneStep))
	head := git(t, repo, "rev-parse", "HEAD")
	f.global(t, "templates:\n  digitaleo:\n    repo: "+repo+"\n")
	if err := f.build("digitaleo", Options{}); err != nil {
		t.Fatal(err)
	}
	copyDir := filepath.Join(f.home, "gen", ".templates", "digitaleo")
	if got := git(t, copyDir, "rev-parse", "HEAD"); got != head {
		t.Errorf("copy at %s, want %s", got, head)
	}
	r := f.record(t, "digitaleo")
	if r.Commit != head || r.Source != (template.Source{Kind: "repo", URL: repo}) {
		t.Errorf("record %+v, want commit %s from %s", r, head, repo)
	}
	if exists(template.StagingDir(f.home, "digitaleo")) {
		t.Error("staging dir left behind")
	}
	loaded, err := config.LoadDefinitions(copyDir)
	if err != nil {
		t.Fatal(err)
	}
	if hash, err := template.DefinitionHash(loaded, "digitaleo"); err != nil || hash != r.DefinitionHash {
		t.Errorf("hash of the copy %s (%v), record says %s", hash, err, r.DefinitionHash)
	}
	if len(f.prompt.Confirms) != 0 {
		t.Errorf("asked %v for a global repo", f.prompt.Confirms)
	}
}

func TestBuildWithoutPullKeepsTheCopy(t *testing.T) {
	f := newFixture(t)
	repo := newRepo(t, defs("digitaleo", oneStep))
	old := git(t, repo, "rev-parse", "HEAD")
	f.global(t, "templates:\n  digitaleo:\n    repo: "+repo+"\n")
	if err := f.build("digitaleo", Options{}); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, "README", "new\n")
	if err := f.build("digitaleo", Options{}); err != nil {
		t.Fatal(err)
	}
	if r := f.record(t, "digitaleo"); r.Commit != old {
		t.Errorf("record commit %s, want the old %s", r.Commit, old)
	}
	if got := git(t, template.CopyDir(f.home, "digitaleo"), "rev-parse", "HEAD"); got != old {
		t.Errorf("copy moved to %s", got)
	}
}

func TestBuildWithPullAdvancesIt(t *testing.T) {
	f := newFixture(t)
	repo := newRepo(t, defs("digitaleo", oneStep))
	f.global(t, "templates:\n  digitaleo:\n    repo: "+repo+"\n")
	if err := f.build("digitaleo", Options{}); err != nil {
		t.Fatal(err)
	}
	head := commitFile(t, repo, "README", "new\n")
	if err := f.build("digitaleo", Options{Pull: true}); err != nil {
		t.Fatal(err)
	}
	if r := f.record(t, "digitaleo"); r.Commit != head {
		t.Errorf("record commit %s, want %s", r.Commit, head)
	}
	if got := git(t, template.CopyDir(f.home, "digitaleo"), "rev-parse", "HEAD"); got != head {
		t.Errorf("copy at %s, want %s", got, head)
	}
}

func TestFailedBuildLeavesThePreviousVersion(t *testing.T) {
	f := newFixture(t)
	repo := newRepo(t, defs("digitaleo", oneStep))
	old := git(t, repo, "rev-parse", "HEAD")
	f.global(t, "templates:\n  digitaleo:\n    repo: "+repo+"\n")
	if err := f.build("digitaleo", Options{}); err != nil {
		t.Fatal(err)
	}
	before := f.record(t, "digitaleo")
	commitFile(t, repo, "README", "new\n")
	boom := errors.New("exit status 3")
	f.fake.Responses["exec digitaleo-build -- bash -lc echo one\n"] = sbx.Response{Err: boom}
	f.fake.Calls = nil

	if err := f.build("digitaleo", Options{Pull: true}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the step error", err)
	}
	if after := f.record(t, "digitaleo"); after != before {
		t.Errorf("record changed: %+v, was %+v", after, before)
	}
	if got := git(t, template.CopyDir(f.home, "digitaleo"), "rev-parse", "HEAD"); got != old {
		t.Errorf("copy moved to %s, want %s", got, old)
	}
	if s := f.saves(); len(s) != 0 {
		t.Errorf("template saved: %v", s)
	}
	if exists(template.StagingDir(f.home, "digitaleo")) {
		t.Error("staging dir left behind")
	}
	if !f.fake.HasCalled("--no-input", "env", "rm", "--force", "--skip-host-commands", f.envFile("digitaleo")) {
		t.Errorf("no env rm in %v", f.calls())
	}
}

func TestBuildStopsWhenTheDigestIsUnreadable(t *testing.T) {
	for name, rows := range map[string][]string{
		"no row":       nil,
		"empty digest": {row("docker.io/docker/sandbox-templates", "claude-code-docker", "")},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.dirTemplate(t, "legacy", defs("legacy", oneStep))
			f.templates(rows...)
			err := f.build("legacy", Options{})
			if err == nil || !strings.Contains(err.Error(), claudeCodeDocker) {
				t.Fatalf("err = %v, want it to name %s", err, claudeCodeDocker)
			}
			for _, c := range f.calls() {
				if strings.HasPrefix(c, "exec ") || strings.HasPrefix(c, "template save") {
					t.Errorf("call %q after an unreadable digest", c)
				}
			}
			if !f.fake.HasCalled("--no-input", "env", "rm", "--force", "--skip-host-commands", f.envFile("legacy")) {
				t.Errorf("no env rm in %v", f.calls())
			}
			if exists(filepath.Join(f.home, "state", "v2", "templates", "legacy.yaml")) {
				t.Error("record written")
			}
		})
	}
}

func TestBuildAsksBeforeAnUnknownProjectURL(t *testing.T) {
	f := newFixture(t)
	repo := newRepo(t, defs("digitaleo", oneStep))
	f.project(t, "templates:\n  digitaleo:\n    repo: "+repo+"\n")

	err := f.build("digitaleo", Options{})
	if err == nil || !strings.Contains(err.Error(), "--yes") || !strings.Contains(err.Error(), repo) {
		t.Fatalf("err = %v, want a refusal naming --yes and %s", err, repo)
	}
	if exists(template.StagingDir(f.home, "digitaleo")) || exists(template.CopyDir(f.home, "digitaleo")) {
		t.Error("cloned before the user trusted the URL")
	}
	if ok, _ := template.Trusted(f.home, repo); ok {
		t.Error("trusted without a yes")
	}

	f.tty = true
	f.prompt.ConfirmAnswers = []bool{false}
	if err := f.build("digitaleo", Options{}); err == nil {
		t.Fatal("a no answer built")
	}
	if len(f.prompt.Confirms) != 1 || f.prompt.Confirms[0].Question != "fetch template definitions from "+repo+"?" {
		t.Errorf("confirms %v", f.prompt.Confirms)
	}
	if exists(template.CopyDir(f.home, "digitaleo")) {
		t.Error("cloned after a no")
	}

	f.tty = false
	if err := f.build("digitaleo", Options{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if ok, err := template.Trusted(f.home, repo); !ok || err != nil {
		t.Errorf("Trusted = %v, %v after --yes", ok, err)
	}
	if !exists(template.CopyDir(f.home, "digitaleo")) {
		t.Error("no copy after --yes")
	}
}

func TestBuildAsksOnceAndRemembers(t *testing.T) {
	f := newFixture(t)
	repo := newRepo(t, defs("digitaleo", oneStep))
	f.project(t, "templates:\n  digitaleo:\n    repo: "+repo+"\n")
	f.tty = true
	f.prompt.ConfirmAnswers = []bool{true}
	if err := f.build("digitaleo", Options{}); err != nil {
		t.Fatal(err)
	}
	if err := f.build("digitaleo", Options{Pull: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.prompt.Confirms) != 1 {
		t.Errorf("asked %d times", len(f.prompt.Confirms))
	}
}

func TestBuildNeverAsksForAGlobalURL(t *testing.T) {
	f := newFixture(t)
	repo := newRepo(t, defs("digitaleo", oneStep))
	f.global(t, "templates:\n  digitaleo:\n    repo: "+repo+"\n")
	f.tty = true
	if err := f.build("digitaleo", Options{}); err != nil {
		t.Fatal(err)
	}
	if len(f.prompt.Confirms) != 0 {
		t.Errorf("asked %v", f.prompt.Confirms)
	}
}

func TestBuildRefusesAnotherSourceWithoutForce(t *testing.T) {
	f := newFixture(t)
	a := newRepo(t, defs("digitaleo", oneStep))
	b := newRepo(t, defs("digitaleo", oneStep))
	bHead := commitFile(t, b, "only-in-b", "b\n")
	f.global(t, "templates:\n  digitaleo:\n    repo: "+a+"\n")
	if err := f.build("digitaleo", Options{}); err != nil {
		t.Fatal(err)
	}
	f.global(t, "templates:\n  digitaleo:\n    repo: "+b+"\n")
	f.fake.Calls = nil

	err := f.build("digitaleo", Options{})
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, w := range []string{a, b, "--force"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q lacks %q", err, w)
		}
	}
	if got := f.calls(); len(got) != 0 {
		t.Errorf("sbx calls %v before the refusal", got)
	}
	if r := f.record(t, "digitaleo"); r.Source.URL != a {
		t.Errorf("record moved to %s", r.Source.URL)
	}

	if err := f.build("digitaleo", Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	r := f.record(t, "digitaleo")
	if r.Source != (template.Source{Kind: "repo", URL: b}) || r.Commit != bHead {
		t.Errorf("record %+v, want %s at %s", r, b, bHead)
	}
	if !exists(filepath.Join(template.CopyDir(f.home, "digitaleo"), "only-in-b")) {
		t.Error("the copy still holds the old source")
	}
}

const siblings = "schema: 1\ntemplates:\n" +
	"  digitaleo:\n    from: claude-code-docker\n    build:\n      steps: [one.sh]\n" +
	"  kampn:\n    from: digitaleo\n    build:\n      steps: [two.sh]\n"

func TestBuildBuildsTheSiblingFirst(t *testing.T) {
	f := newFixture(t)
	loc := t.TempDir()
	writeLocation(t, loc, siblings)
	f.global(t, "templates:\n  kampn:\n    dir: "+loc+"\n")
	if err := f.build("kampn", Options{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"template save digitaleo-build digitaleo:v2", "template save kampn-build kampn:v2"}
	if got := f.saves(); !slices.Equal(got, want) {
		t.Errorf("saves %q, want %q", got, want)
	}
	env, err := os.ReadFile(f.envFile("kampn"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "  template: digitaleo:v2\n") {
		t.Errorf("kampn's build env:\n%s", env)
	}
	kit, err := os.ReadFile(filepath.Join(f.home, "gen", ".build", "kampn", "den-claude", "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kit), "  image: "+pinnedClaude+"\n") {
		t.Errorf("kampn's den-claude kit does not carry %s:\n%s", pinnedClaude, kit)
	}
	d, k := f.record(t, "digitaleo"), f.record(t, "kampn")
	if d.From != (template.From{Ref: claudeCodeDocker, Digest: digest}) || k.From != d.From {
		t.Errorf("from: digitaleo %+v, kampn %+v", d.From, k.From)
	}
	for _, w := range []string{
		"den: building template digitaleo first (template kampn starts from it)\n",
		"den: building template kampn (kampn:v2)\n",
	} {
		if !strings.Contains(f.err.String(), w) {
			t.Errorf("stderr %q lacks %q", f.err.String(), w)
		}
	}
}

func TestBuildSkipsABuiltSibling(t *testing.T) {
	f := newFixture(t)
	loc := t.TempDir()
	writeLocation(t, loc, siblings)
	f.global(t, "templates:\n  kampn:\n    dir: "+loc+"\n")
	if err := f.build("kampn", Options{}); err != nil {
		t.Fatal(err)
	}
	f.templates(row("docker.io/docker/sandbox-templates", "claude-code-docker", digest), row("docker.io/library/digitaleo", "v2", "sha256:"+strings.Repeat("b", 64)))
	f.fake.Calls = nil
	if err := f.build("kampn", Options{}); err != nil {
		t.Fatal(err)
	}
	if got, want := f.saves(), []string{"template save kampn-build kampn:v2"}; !slices.Equal(got, want) {
		t.Errorf("saves %q, want %q", got, want)
	}
}

func (f *fixture) siblingBuilt() {
	f.templates(row("docker.io/docker/sandbox-templates", "claude-code-docker", digest), row("docker.io/library/digitaleo", "v2", "sha256:"+strings.Repeat("b", 64)))
	f.fake.Calls = nil
}

func TestBuildRebuildsADriftedDirSibling(t *testing.T) {
	f := newFixture(t)
	loc := t.TempDir()
	writeLocation(t, loc, siblings)
	f.global(t, "templates:\n  kampn:\n    dir: "+loc+"\n")
	if err := f.build("kampn", Options{}); err != nil {
		t.Fatal(err)
	}
	before := f.record(t, "kampn").DefinitionHash
	writeFile(t, filepath.Join(loc, "one.sh"), "echo changed\n")
	f.siblingBuilt()
	if err := f.build("kampn", Options{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"template save digitaleo-build digitaleo:v2", "template save kampn-build kampn:v2"}
	if got := f.saves(); !slices.Equal(got, want) {
		t.Errorf("saves %q, want %q", got, want)
	}
	if !f.fake.HasCalled("exec", "digitaleo-build", "--", "bash", "-lc", "echo changed\n") {
		t.Errorf("digitaleo not rebuilt from its changed step: %v", f.calls())
	}
	if f.record(t, "kampn").DefinitionHash == before {
		t.Error("kampn's definition hash did not move with its sibling's step")
	}
}

func TestBuildWithPullRebuildsTheSiblingAtTheNewCommit(t *testing.T) {
	f := newFixture(t)
	repo := newRepo(t, siblings)
	f.global(t, "templates:\n  kampn:\n    repo: "+repo+"\n")
	if err := f.build("kampn", Options{}); err != nil {
		t.Fatal(err)
	}
	head := commitFile(t, repo, "README", "new\n")
	f.siblingBuilt()
	if err := f.build("kampn", Options{Pull: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{"template save digitaleo-build digitaleo:v2", "template save kampn-build kampn:v2"}
	if got := f.saves(); !slices.Equal(got, want) {
		t.Errorf("saves %q, want %q", got, want)
	}
	for _, n := range []string{"digitaleo", "kampn"} {
		if r := f.record(t, n); r.Commit != head {
			t.Errorf("%s recorded at %s, want %s", n, r.Commit, head)
		}
	}
}

func TestBuildRefusesASiblingBuiltFromAnotherCommit(t *testing.T) {
	f := newFixture(t)
	repo := newRepo(t, siblings)
	f.global(t, "templates:\n  kampn:\n    repo: "+repo+"\n")
	if err := f.build("kampn", Options{}); err != nil {
		t.Fatal(err)
	}
	r := f.record(t, "digitaleo")
	r.Commit = strings.Repeat("c", 40)
	if err := template.WriteRecord(f.home, r); err != nil {
		t.Fatal(err)
	}
	f.siblingBuilt()
	err := f.build("kampn", Options{})
	if err == nil || !strings.Contains(err.Error(), "den build kampn --pull") || !strings.Contains(err.Error(), r.Commit) {
		t.Fatalf("err = %v, want a refusal naming `den build kampn --pull`", err)
	}
	if s := f.saves(); len(s) != 0 {
		t.Errorf("saved %v before the refusal", s)
	}
}

func TestBuildForceDoesNotReachTheSibling(t *testing.T) {
	f := newFixture(t)
	loc := t.TempDir()
	writeLocation(t, loc, siblings)
	f.global(t, "templates:\n  kampn:\n    dir: "+loc+"\n")
	other := template.Record{Template: "digitaleo", Tag: "digitaleo:v2", Source: template.Source{Kind: "dir", Dir: "/elsewhere"},
		DefinitionHash: "x", From: template.From{Ref: claudeCodeDocker, Digest: digest}}
	if err := template.WriteRecord(f.home, other); err != nil {
		t.Fatal(err)
	}
	err := f.build("kampn", Options{Force: true})
	if err == nil || !strings.Contains(err.Error(), "den build digitaleo --force") || !strings.Contains(err.Error(), "/elsewhere") {
		t.Fatalf("err = %v, want the sibling's refusal naming `den build digitaleo --force`", err)
	}
	if r := f.record(t, "digitaleo"); r.Source != other.Source {
		t.Errorf("digitaleo's record moved to %+v", r.Source)
	}
	if s := f.saves(); len(s) != 0 {
		t.Errorf("saved %v", s)
	}
}

func TestBuildWithoutFromNeedsAnImageDefault(t *testing.T) {
	noFrom := defs("kampn", "    build:\n      steps: [one.sh]\n")
	for name, global := range map[string]string{
		"no default":          "templates:\n",
		"template as default": "default_template: digitaleo\ntemplates:\n  digitaleo:\n    dir: /elsewhere\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			loc := t.TempDir()
			writeLocation(t, loc, noFrom)
			f.global(t, global+"  kampn:\n    dir: "+loc+"\n")
			err := f.build("kampn", Options{})
			if err == nil {
				t.Fatal("want a refusal")
			}
			for _, w := range []string{"from:", filepath.Join(f.home, "den.yaml"), "den-template.yaml", "default_template"} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
			var nt *template.NoTemplateError
			if !errors.As(err, &nt) {
				t.Errorf("error %T is no *template.NoTemplateError: den build would not list the official images", err)
			}
			if f.fake.HasCalled("--no-input", "env", "create") {
				t.Error("env create after the refusal")
			}
		})
	}

	f := newFixture(t)
	loc := t.TempDir()
	writeLocation(t, loc, noFrom)
	f.global(t, "default_template: claude-code-docker\ntemplates:\n  kampn:\n    dir: "+loc+"\n")
	if err := f.build("kampn", Options{}); err != nil {
		t.Fatal(err)
	}
	kit, err := os.ReadFile(filepath.Join(f.home, "gen", ".build", "kampn", "den-claude", "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kit), "  image: "+claudeCodeDocker+"\n") {
		t.Errorf("den-claude kit does not carry %s:\n%s", claudeCodeDocker, kit)
	}
	if r := f.record(t, "kampn"); r.From != (template.From{Ref: claudeCodeDocker, Digest: digest}) {
		t.Errorf("from %+v", r.From)
	}
}

func TestBuildRefusesAnImage(t *testing.T) {
	f := newFixture(t)
	f.project(t, "templates: {}\n")
	err := f.build("claude-code-docker", Options{})
	want := `no template is named "claude-code-docker" in ` + filepath.Join(f.cwd, "den.yaml") + " or " + filepath.Join(f.home, "den.yaml") +
		`, so it is the image ` + claudeCodeDocker + `, which has no build — declare it under templates: there, ` +
		`or use the image directly as template: or default_template`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if got := f.calls(); len(got) != 0 {
		t.Errorf("calls %v", got)
	}
}

func TestBuildRefusesAnIllegalNameBeforeTrustAndClone(t *testing.T) {
	for name, entry := range map[string]string{
		"uppercase":   "Kampn",
		"leading dot": ".kampn",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			repo := newRepo(t, defs(entry, oneStep))
			f.project(t, "templates:\n  bad:\n    repo: "+repo+"\n    name: "+entry+"\n")
			err := f.build("bad", Options{Yes: true})
			if err == nil || !strings.Contains(err.Error(), entry) || !strings.Contains(err.Error(), filepath.Join(f.cwd, "den.yaml")) {
				t.Fatalf("err = %v, want a refusal naming %q and the project file", err, entry)
			}
			if ok, _ := template.Trusted(f.home, repo); ok {
				t.Error("trusted before the name was checked")
			}
			if exists(template.StagingDir(f.home, entry)) || exists(template.CopyDir(f.home, entry)) {
				t.Error("cloned before the name was checked")
			}
			if got := f.calls(); len(got) != 0 {
				t.Errorf("sbx calls %v", got)
			}
		})
	}
}

func TestBuildRefusesAnIllegalSiblingNameBeforeItsBuild(t *testing.T) {
	f := newFixture(t)
	loc := t.TempDir()
	writeLocation(t, loc, strings.ReplaceAll(siblings, "digitaleo", "Digitaleo"))
	f.global(t, "templates:\n  kampn:\n    dir: "+loc+"\n")
	err := f.build("kampn", Options{})
	if err == nil || !strings.Contains(err.Error(), `"Digitaleo"`) || !strings.Contains(err.Error(), `from: of template "kampn"`) {
		t.Fatalf("err = %v, want a refusal naming the sibling and its child", err)
	}
	if got := f.calls(); len(got) != 0 {
		t.Errorf("sbx calls %v", got)
	}
}

func TestBuildRefusesAnUndeclaredTemplate(t *testing.T) {
	f := newFixture(t)
	loc := f.dirTemplate(t, "legacy", defs("other", oneStep))
	err := f.build("legacy", Options{})
	if err == nil || !strings.Contains(err.Error(), "den-template.yaml") || !strings.Contains(err.Error(), `"legacy"`) {
		t.Fatalf("err = %v, want it to name %s/den-template.yaml", err, loc)
	}
	if got := f.calls(); len(got) != 0 {
		t.Errorf("calls %v", got)
	}
}

func TestBuildRefusesAnUnreadableStepBeforeCreate(t *testing.T) {
	f := newFixture(t)
	loc := f.dirTemplate(t, "devx", defs("devx", "    from: claude-code-docker\n    build:\n      steps: [gone.sh]\n"))
	err := f.build("devx", Options{})
	if err == nil || !strings.Contains(err.Error(), "gone.sh") {
		t.Fatalf("err = %v, want a refusal naming gone.sh in %s", err, loc)
	}
	if got := f.calls(); len(got) != 0 {
		t.Errorf("sbx calls %v, want none", got)
	}
	if exists(filepath.Join(f.home, "build")) {
		t.Error("a file was written before the refusal")
	}
}

func TestBuildRefusesAnExistingBuildVM(t *testing.T) {
	f := newFixture(t)
	f.fake.Responses["ls --json"] = sbx.Response{Output: []byte(`[{"name":"devx-build","status":"running"}]`)}
	f.dirTemplate(t, "devx", defs("devx", oneStep))
	err := f.build("devx", Options{})
	if err == nil || !strings.Contains(err.Error(), "sbx rm devx-build") {
		t.Fatalf("err = %v, want a refusal naming `sbx rm devx-build`", err)
	}
	if got := f.calls(); len(got) != 0 {
		t.Errorf("sbx calls %v, want none: den must not env rm a VM it did not create", got)
	}
	if exists(f.envFile("devx")) {
		t.Error("sbxenv.yaml written before the refusal")
	}
}

func TestBuildCallSequence(t *testing.T) {
	f := newFixture(t)
	f.dirTemplate(t, "devx", defs("devx", "    from: claude-code-docker\n    build:\n      steps: [one.sh, two.sh]\n"))
	if err := f.build("devx", Options{}); err != nil {
		t.Fatal(err)
	}
	env := f.envFile("devx")
	want := []string{
		"--no-input env create --force --skip-host-commands " + env,
		"template ls --json",
		"exec devx-build -- bash -lc echo one\n",
		"exec devx-build -- bash -lc echo two\n",
		"stop devx-build",
		"template save devx-build devx:v2",
		"--no-input env rm --force --skip-host-commands " + env,
	}
	if got := f.calls(); !slices.Equal(got, want) {
		t.Errorf("calls:\n%q\nwant:\n%q", got, want)
	}
	scratch := filepath.Join(f.home, "build", "devx")
	if st, err := os.Stat(scratch); err != nil || !st.IsDir() {
		t.Errorf("scratch workspace %s missing: %v", scratch, err)
	}
	raw, err := os.ReadFile(env)
	if err != nil {
		t.Fatal(err)
	}
	gen := filepath.Join(f.home, "gen", ".build", "devx")
	for _, w := range []string{scratch, filepath.Join(gen, "den-claude"), filepath.Join(gen, "network")} {
		if !strings.Contains(string(raw), w) {
			t.Errorf("sbxenv.yaml lacks %s:\n%s", w, raw)
		}
	}
	if strings.Contains(string(raw), "template") || strings.Contains(string(raw), ".kits") {
		t.Errorf("build env from an image names a template or the shared kit:\n%s", raw)
	}
	kit, err := os.ReadFile(filepath.Join(gen, "den-claude", "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kit), "  image: "+claudeCodeDocker+"\n") {
		t.Errorf("den-claude kit does not carry %s:\n%s", claudeCodeDocker, kit)
	}
}

func TestBuildEmptiesTheScratchWorkspace(t *testing.T) {
	f := newFixture(t)
	stale := filepath.Join(f.home, "build", "devx", "stale")
	writeFile(t, stale, "x")
	f.dirTemplate(t, "devx", defs("devx", oneStep))
	if err := f.build("devx", Options{}); err != nil {
		t.Fatal(err)
	}
	if exists(stale) {
		t.Error("stale scratch file survived")
	}
}

func TestBuildRemovesTheVMWhenAStepFails(t *testing.T) {
	f := newFixture(t)
	loc := f.dirTemplate(t, "devx", defs("devx", "    from: claude-code-docker\n    build:\n      steps: [one.sh, two.sh]\n"))
	boom := errors.New("exit status 3")
	f.fake.Responses["exec devx-build -- bash -lc echo two\n"] = sbx.Response{Err: boom}
	err := f.build("devx", Options{})
	if err == nil || !errors.Is(err, boom) || !strings.Contains(err.Error(), "step 2/2 ") || !strings.Contains(err.Error(), "two.sh") {
		t.Fatalf("err = %v, want it to name step 2/2 two.sh of %s", err, loc)
	}
	got := f.calls()
	if last := got[len(got)-1]; !strings.HasPrefix(last, "--no-input env rm --force --skip-host-commands") {
		t.Errorf("last call = %q, want env rm", last)
	}
	for _, c := range got {
		if strings.HasPrefix(c, "stop") || strings.HasPrefix(c, "template save") {
			t.Errorf("call %q after a failed step", c)
		}
	}
	if exists(filepath.Join(f.home, "state", "v2", "templates", "devx.yaml")) {
		t.Error("record written after a failed step")
	}
}

func TestBuildWarnsWhenEnvRmFailsAndKeepsTheStepError(t *testing.T) {
	f := newFixture(t)
	f.dirTemplate(t, "devx", defs("devx", oneStep))
	boom := errors.New("exit status 3")
	f.fake.Responses["exec devx-build -- bash -lc echo one\n"] = sbx.Response{Err: boom}
	f.fake.Responses["--no-input env rm --force --skip-host-commands "+f.envFile("devx")] = sbx.Response{Err: errors.New("rm broke")}
	err := f.build("devx", Options{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the step error", err)
	}
	if !strings.Contains(f.err.String(), "warning:") || !strings.Contains(f.err.String(), "devx-build") {
		t.Errorf("stderr = %q, want a warning naming the VM", f.err.String())
	}
}

func TestBuildTemplateSaveFailureIsAnError(t *testing.T) {
	f := newFixture(t)
	f.dirTemplate(t, "devx", defs("devx", oneStep))
	boom := errors.New("save broke")
	f.fake.Responses["template save devx-build devx:v2"] = sbx.Response{Err: boom}
	err := f.build("devx", Options{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the save error", err)
	}
	got := f.calls()
	if !strings.HasPrefix(got[len(got)-1], "--no-input env rm") {
		t.Errorf("calls %q: env rm must still run", got)
	}
	if exists(filepath.Join(f.home, "state", "v2", "templates", "devx.yaml")) {
		t.Error("record written after a failed save")
	}
}

func TestBuildGivesTheBuildNetworkOnly(t *testing.T) {
	f := newFixture(t)
	f.dirTemplate(t, "devx", defs("devx", "    from: claude-code-docker\n"+
		"    network:\n      allow: [runtime.example.test]\n"+
		"    build:\n      steps: [one.sh]\n      network:\n        allow: [build.example.test]\n"))
	if err := f.build("devx", Options{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(f.home, "gen", ".build", "devx", "network", "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "build.example.test") || strings.Contains(string(raw), "runtime.example.test") {
		t.Errorf("network kit:\n%s", raw)
	}
}

func TestBuildRefusesAnOldSbx(t *testing.T) {
	f := newFixture(t)
	f.fake.Responses["version"] = sbx.Response{Output: []byte("sbx version v0.52.0 abc\n")}
	f.dirTemplate(t, "devx", defs("devx", oneStep))
	if err := f.build("devx", Options{}); err == nil {
		t.Fatal("want a refusal")
	}
	if got := f.calls(); len(got) != 0 {
		t.Errorf("calls %v", got)
	}
}
