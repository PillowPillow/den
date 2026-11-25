package instance

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/v2/internal/registry"
	"github.com/PillowPillow/den/v2/internal/sbx"
	"github.com/PillowPillow/den/v2/internal/worktree"
)

// commitFile commits name with body at the tip of repo's current branch.
func (f *fixture) commitFile(t *testing.T, repo, name, body string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, name), body)
	for _, args := range [][]string{{"add", name}, {"commit", "-q", "-m", "add " + name}} {
		if _, err := f.git.Run(context.Background(), repo, args...); err != nil {
			t.Fatal(err)
		}
	}
}

// created runs a real Create of nest am on branch feat, then lists its VM and forgets the calls Create made.
func (f *fixture) created(t *testing.T, nestBody string) registry.Record {
	t.Helper()
	f.project(t, map[string]string{"am": nestBody})
	rec := mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am", Branch: "feat"}))
	f.live(rec.Instance)
	f.fake.Calls = nil
	return rec
}

func (f *fixture) remove(instance string, force bool) error {
	return Remove(context.Background(), f.deps(), instance, force)
}

func (f *fixture) removedVM() bool {
	return f.fake.HasCalled("--no-input", "rm") || f.fake.HasCalled("--no-input", "env", "rm")
}

func mustExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s is gone: %v", p, err)
		}
	}
}

func mustBeGone(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s survived: %v", p, err)
		}
	}
}

func recordPath(t *testing.T, home, instance string) string {
	t.Helper()
	p, err := registry.Path(home, instance)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func wantErr(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want an error containing %q", wants)
	}
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q lacks %q", err, w)
		}
	}
}

func TestRmRefusesADirtyWorktreeAndTouchesNothing(t *testing.T) {
	f := newFixture(t)
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false)))
	notes := writeFile(t, filepath.Join(rec.Repos[0].Mount, "notes.txt"), "work\n")

	wantErr(t, f.remove(rec.Instance, false), "notes.txt", "--force")
	if f.removedVM() {
		t.Errorf("the VM went before the refusal: %v", f.fake.Calls)
	}
	mustExist(t, recordPath(t, f.home, rec.Instance), rec.Repos[0].Mount, notes, rec.RunDir, rec.GenDir)
}

func TestRmRefusesAnIgnoredFile(t *testing.T) {
	f := newFixture(t)
	api := f.committedRepo(t, "api")
	f.commitFile(t, api, ".gitignore", ".env\n")
	rec := f.created(t, repos(repoEntry(api, false)))
	env := writeFile(t, filepath.Join(rec.Repos[0].Mount, ".env"), "TOKEN=x\n")

	wantErr(t, f.remove(rec.Instance, false), ".env", "--force")
	if f.removedVM() {
		t.Errorf("the VM went before the refusal: %v", f.fake.Calls)
	}
	mustExist(t, recordPath(t, f.home, rec.Instance), env)
}

func TestRmRefusesAModifiedTrackedFile(t *testing.T) {
	f := newFixture(t)
	api := f.committedRepo(t, "api")
	f.commitFile(t, api, "main.go", "package main\n")
	rec := f.created(t, repos(repoEntry(api, false)))
	writeFile(t, filepath.Join(rec.Repos[0].Mount, "main.go"), "package main // edited\n")

	wantErr(t, f.remove(rec.Instance, false), "main.go", "--force")
	if f.removedVM() {
		t.Errorf("the VM went before the refusal: %v", f.fake.Calls)
	}
	mustExist(t, recordPath(t, f.home, rec.Instance), rec.Repos[0].Mount)
}

func TestRmKeepsACleanWorktreeWhenASiblingIsDirty(t *testing.T) {
	f := newFixture(t)
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false), repoEntry(f.committedRepo(t, "web"), false)))
	writeFile(t, filepath.Join(rec.Repos[1].Mount, "b.txt"), "b\n")

	wantErr(t, f.remove(rec.Instance, false), "b.txt", "--force")
	if f.removedVM() {
		t.Errorf("the VM went before the refusal: %v", f.fake.Calls)
	}
	mustExist(t, recordPath(t, f.home, rec.Instance), rec.Repos[0].Mount, rec.Repos[1].Mount)
}

func TestRmForceRemovesAll(t *testing.T) {
	f := newFixture(t)
	api := f.committedRepo(t, "api")
	rec := f.created(t, repos(repoEntry(api, false)))
	writeFile(t, filepath.Join(rec.Repos[0].Mount, "notes.txt"), "work\n")
	shared := writeFile(t, filepath.Join(f.home, "gen", ".templates", "legacy", "den-template.yaml"), "schema: 1\n")

	if err := f.remove(rec.Instance, true); err != nil {
		t.Fatal(err)
	}
	if !f.removedVM() {
		t.Errorf("no VM removal: %v", f.fake.Calls)
	}
	mustBeGone(t, rec.Repos[0].Mount, rec.RunDir, rec.GenDir, recordPath(t, f.home, rec.Instance))
	if _, err := f.git.Run(context.Background(), api, "rev-parse", "--verify", "-q", "refs/heads/feat"); err != nil {
		t.Errorf("rm deleted branch feat: %v", err)
	}
	mustExist(t, shared)
}

func TestRmPassesSkipHostCommands(t *testing.T) {
	f := newFixture(t)
	layer := writeFile(t, filepath.Join(f.src, "layer.yaml"), "schemaVersion: \"1\"\n")
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false))+"\nlayers: ["+layer+"]")

	if err := f.remove(rec.Instance, false); err != nil {
		t.Fatal(err)
	}
	want := append([]string{"--no-input", "env", "rm", "--force", "--skip-host-commands"}, rec.EnvFiles...)
	if len(rec.EnvFiles) != 2 || !slices.ContainsFunc(f.fake.Calls, func(c []string) bool { return slices.Equal(c, want) }) {
		t.Errorf("calls %v, want %v", f.fake.Calls, want)
	}
	if f.fake.HasCalled("--no-input", "rm") {
		t.Errorf("sbx rm ran beside env rm: %v", f.fake.Calls)
	}
}

func TestRmUsesSbxRmWhenAnEnvFileIsGone(t *testing.T) {
	f := newFixture(t)
	layer := writeFile(t, filepath.Join(f.src, "layer.yaml"), "schemaVersion: \"1\"\n")
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false))+"\nlayers: ["+layer+"]")
	if err := os.Remove(layer); err != nil {
		t.Fatal(err)
	}

	if err := f.remove(rec.Instance, false); err != nil {
		t.Fatal(err)
	}
	want := []string{"--no-input", "rm", "--force", rec.Instance}
	if !slices.ContainsFunc(f.fake.Calls, func(c []string) bool { return slices.Equal(c, want) }) {
		t.Errorf("calls %v, want %v", f.fake.Calls, want)
	}
	if f.fake.HasCalled("--no-input", "env", "rm") {
		t.Errorf("env rm ran with a missing env file: %v", f.fake.Calls)
	}
}

func TestRmUsesSbxRmWhenAnEnvFileChanged(t *testing.T) {
	for name, file := range map[string]func(rec registry.Record) string{
		"generated sbxenv.yaml": func(rec registry.Record) string { return rec.EnvFiles[0] },
		"layer":                 func(rec registry.Record) string { return rec.EnvFiles[1] },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			layer := writeFile(t, filepath.Join(f.src, "layer.yaml"), "schemaVersion: \"1\"\n")
			rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false))+"\nlayers: ["+layer+"]")
			writeFile(t, file(rec), "schemaVersion: \"1\"\nlifecycle: {preRemove: [\"touch /tmp/pwned\"]}\n")

			if err := f.remove(rec.Instance, false); err != nil {
				t.Fatal(err)
			}
			want := []string{"--no-input", "rm", "--force", rec.Instance}
			if !slices.ContainsFunc(f.fake.Calls, func(c []string) bool { return slices.Equal(c, want) }) {
				t.Errorf("calls %v, want %v", f.fake.Calls, want)
			}
			if f.fake.HasCalled("--no-input", "env", "rm") {
				t.Errorf("env rm ran on a file changed since create: %v", f.fake.Calls)
			}
		})
	}
}

func TestRmUsesSbxRmWithoutRecordedHashes(t *testing.T) {
	f := newFixture(t)
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false)))
	rec.EnvHashes = nil
	if err := registry.Write(f.home, rec); err != nil {
		t.Fatal(err)
	}

	if err := f.remove(rec.Instance, false); err != nil {
		t.Fatal(err)
	}
	if want := []string{"--no-input", "rm", "--force", rec.Instance}; !slices.ContainsFunc(f.fake.Calls, func(c []string) bool { return slices.Equal(c, want) }) {
		t.Errorf("calls %v, want %v", f.fake.Calls, want)
	}
}

func TestRmSkipsTheVMWhenSbxDoesNotListIt(t *testing.T) {
	f := newFixture(t)
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false)))
	f.respond("ls --json", `[]`)

	if err := f.remove(rec.Instance, false); err != nil {
		t.Fatal(err)
	}
	if f.removedVM() {
		t.Errorf("removed a VM sbx does not list: %v", f.fake.Calls)
	}
	mustBeGone(t, rec.Repos[0].Mount, recordPath(t, f.home, rec.Instance))
}

func TestRmRefusesWhenSbxLsFails(t *testing.T) {
	f := newFixture(t)
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false)))
	f.fake.Responses["ls --json"] = sbx.Response{Err: errors.New("daemon down")}

	wantErr(t, f.remove(rec.Instance, false), "daemon down")
	mustExist(t, rec.Repos[0].Mount, recordPath(t, f.home, rec.Instance))
}

func TestRmRefusesWithoutARecord(t *testing.T) {
	f := newFixture(t)
	err := f.remove("am", false)
	want := "no den V2 record for \"am\" — den only removes what it created; use `sbx rm am`"
	if err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
	if len(f.fake.Calls) != 0 {
		t.Errorf("sbx calls %v, want none", f.fake.Calls)
	}
}

func TestRmRefusesARecordOfAnotherSchema(t *testing.T) {
	f := newFixture(t)
	path := writeFile(t, recordPath(t, f.home, "am"), "schema: 3\ninstance: am\n")

	wantErr(t, f.remove("am", false), path, "schema 3")
	if len(f.fake.Calls) != 0 {
		t.Errorf("sbx calls %v, want none", f.fake.Calls)
	}
	mustExist(t, path)
}

// failingRemove is real git, except that `git worktree remove` fails in one repo.
type failingRemove struct {
	worktree.Git
	repo string
}

func (g failingRemove) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if dir == g.repo && len(args) >= 2 && args[0] == "worktree" && args[1] == "remove" {
		return nil, errors.New("disk on fire")
	}
	return g.Git.Run(ctx, dir, args...)
}

// recordingGit is real git that also records the argv of every Run.
type recordingGit struct {
	worktree.Git
	calls *[][]string
}

func (g recordingGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	*g.calls = append(*g.calls, args)
	return g.Git.Run(ctx, dir, args...)
}

func TestRmKeepsTheRecordWhenAWorktreeRemovalFails(t *testing.T) {
	f := newFixture(t)
	api, web := f.committedRepo(t, "api"), f.committedRepo(t, "web")
	rec := f.created(t, repos(repoEntry(api, false), repoEntry(web, false)))
	f.git = failingRemove{Git: f.git, repo: api}

	wantErr(t, f.remove(rec.Instance, false), "disk on fire", rec.Repos[0].Mount, "den rm "+rec.Instance)
	if !f.removedVM() {
		t.Errorf("no VM removal: %v", f.fake.Calls)
	}
	mustExist(t, recordPath(t, f.home, rec.Instance), rec.RunDir, rec.GenDir, rec.Repos[0].Mount)
	mustBeGone(t, rec.Repos[1].Mount)
}

func TestRmStopsWhenTheVMRemovalFails(t *testing.T) {
	f := newFixture(t)
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false)))
	f.fake.Responses[strings.Join(append([]string{"--no-input", "env", "rm", "--force", "--skip-host-commands"}, rec.EnvFiles...), " ")] =
		sbx.Response{Err: errors.New("vm stuck")}

	wantErr(t, f.remove(rec.Instance, false), "vm stuck")
	mustExist(t, recordPath(t, f.home, rec.Instance), rec.RunDir, rec.GenDir, rec.Repos[0].Mount)
}

func TestRmNeverTouchesAMountedRepo(t *testing.T) {
	f := newFixture(t)
	api := f.repo(t, "api")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false))})
	rec := mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am"}))
	f.live(rec.Instance)
	writeFile(t, filepath.Join(api, "notes.txt"), "work\n")

	if err := f.remove(rec.Instance, false); err != nil {
		t.Fatal(err)
	}
	mustExist(t, api, filepath.Join(api, "notes.txt"))
	mustBeGone(t, recordPath(t, f.home, rec.Instance))
}

func TestRmNeverTouchesAWorktreeFoundInPlace(t *testing.T) {
	f := newFixture(t)
	api := f.committedRepo(t, "api")
	wt := worktree.Path("central", filepath.Join(f.home, "wt"), "feat", api)
	if _, err := f.git.Run(context.Background(), api, "worktree", "add", "-q", "-b", "feat", wt); err != nil {
		t.Fatal(err)
	}
	rec := f.created(t, repos(repoEntry(api, false)))
	if rec.Repos[0].Worktree {
		t.Fatalf("setup: the worktree was recorded as den's: %+v", rec.Repos[0])
	}
	notes := writeFile(t, filepath.Join(wt, "notes.txt"), "work\n")
	calls := &[][]string{}
	f.git = recordingGit{Git: f.git, calls: calls}

	if err := f.remove(rec.Instance, false); err != nil {
		t.Fatal(err)
	}
	mustExist(t, wt, notes)
	out, err := f.git.Run(context.Background(), api, "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "worktree "+worktree.ResolvePath(wt)+"\n") {
		t.Errorf("rm unregistered a worktree it found in place:\n%s", out)
	}
	if strings.Contains(string(out), "locked") {
		t.Errorf("a worktree found in place got locked:\n%s", out)
	}
	for _, c := range *calls {
		if len(c) > 1 && c[0] == "worktree" && c[1] != "list" {
			t.Errorf("rm ran `git %s` on a worktree it found in place", strings.Join(c, " "))
		}
	}
}

func TestRmRefusesAWorktreeASiblingInstanceMounts(t *testing.T) {
	f := newFixture(t)
	api := f.committedRepo(t, "api")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false)), "bm": repos(repoEntry(api, false))})
	first := mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am", Branch: "feat"}))
	second := mustCreate(t, f, mustPrepare(t, f, Options{Nest: "bm", Branch: "feat"}))
	if !first.Repos[0].Worktree || second.Repos[0].Worktree || first.Repos[0].Mount != second.Repos[0].Mount {
		t.Fatalf("setup: want one worktree, den-created for am only: %+v / %+v", first.Repos[0], second.Repos[0])
	}
	f.respond("ls --json", `[{"name":"am.feat","status":"running"},{"name":"bm.feat","status":"running"}]`)
	f.fake.Calls = nil

	wantErr(t, f.remove(first.Instance, false), "bm.feat", first.Repos[0].Mount, "`den rm bm.feat`")
	if f.removedVM() {
		t.Errorf("removed the VM despite the refusal: %v", f.fake.Calls)
	}
	mustExist(t, first.Repos[0].Mount, first.RunDir, recordPath(t, f.home, first.Instance))

	if err := f.remove(second.Instance, false); err != nil {
		t.Fatal(err)
	}
	if err := f.remove(first.Instance, false); err != nil {
		t.Fatal(err)
	}
	mustBeGone(t, first.Repos[0].Mount)
}

func TestRmRefusesAWorktreeWhileARecordIsUnreadable(t *testing.T) {
	f := newFixture(t)
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false)))
	broken := writeFile(t, recordPath(t, f.home, "zz"), "schema: 99\ninstance: zz\n")

	wantErr(t, f.remove(rec.Instance, false), broken, "fix or remove "+broken)
	if f.removedVM() {
		t.Errorf("removed the VM despite the refusal: %v", f.fake.Calls)
	}
	mustExist(t, rec.Repos[0].Mount, recordPath(t, f.home, rec.Instance), broken)
}

func TestRemoveRefusesBesideASchemaOneRecord(t *testing.T) {
	f := newFixture(t)
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false)))
	old := writeFile(t, recordPath(t, f.home, "am"), "schema: 1\ninstance: am\n")

	wantErr(t, f.remove(rec.Instance, false), "remove the VM with `sbx rm am`, then delete "+old)
	if f.removedVM() {
		t.Errorf("removed the VM despite the refusal: %v", f.fake.Calls)
	}
	mustExist(t, rec.Repos[0].Mount, recordPath(t, f.home, rec.Instance), old)
}

func TestRmWithoutAWorktreeIgnoresAnUnreadableRecord(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	rec := mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am"}))
	f.live(rec.Instance)
	broken := writeFile(t, recordPath(t, f.home, "zz"), "schema: 99\ninstance: zz\n")

	if err := f.remove(rec.Instance, false); err != nil {
		t.Fatal(err)
	}
	mustBeGone(t, recordPath(t, f.home, rec.Instance))
	mustExist(t, broken)
}

func TestRmRemovesAGlobalNestRunDir(t *testing.T) {
	f := newFixture(t)
	f.global(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	rec := mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am"}))
	f.live(rec.Instance)
	if rec.RunDir != filepath.Join(f.home, "run", "am") {
		t.Fatalf("setup: run dir %s", rec.RunDir)
	}

	if err := f.remove(rec.Instance, false); err != nil {
		t.Fatal(err)
	}
	mustBeGone(t, rec.RunDir, rec.GenDir, recordPath(t, f.home, rec.Instance))
}

func TestRmRefusesARecordedDirDenDidNotPlace(t *testing.T) {
	for name, tamper := range map[string]func(r *registry.Record, elsewhere string){
		"run dir": func(r *registry.Record, elsewhere string) { r.RunDir = elsewhere },
		"gen dir": func(r *registry.Record, elsewhere string) { r.GenDir = elsewhere },
		"empty":   func(r *registry.Record, _ string) { r.GenDir = "" },
		"project": func(r *registry.Record, elsewhere string) { r.Project = filepath.Dir(elsewhere) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false)))
			elsewhere := filepath.Join(f.src, "keep")
			writeFile(t, filepath.Join(elsewhere, "precious"), "x\n")
			tamper(&rec, elsewhere)
			if err := registry.Write(f.home, rec); err != nil {
				t.Fatal(err)
			}

			wantErr(t, f.remove(rec.Instance, true), recordPath(t, f.home, rec.Instance))
			if f.removedVM() {
				t.Errorf("the VM went before the refusal: %v", f.fake.Calls)
			}
			mustExist(t, filepath.Join(elsewhere, "precious"), recordPath(t, f.home, rec.Instance), rec.Repos[0].Mount)
		})
	}
}

func TestRmRefusesADetachedHeadNoRefContainsAndTouchesNothing(t *testing.T) {
	f := newFixture(t)
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false)))
	wt := rec.Repos[0].Mount
	for _, args := range [][]string{{"checkout", "-q", "--detach"}, {"commit", "-q", "--allow-empty", "-m", "detached work"}} {
		if _, err := f.git.Run(context.Background(), wt, args...); err != nil {
			t.Fatal(err)
		}
	}
	head, err := f.git.Run(context.Background(), wt, "rev-parse", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	wantErr(t, f.remove(rec.Instance, false), strings.TrimSpace(string(head)), "--force")
	if f.removedVM() {
		t.Errorf("the VM went before the refusal: %v", f.fake.Calls)
	}
	mustExist(t, recordPath(t, f.home, rec.Instance), wt, rec.RunDir, rec.GenDir)
}

func TestRmKeepsTheSessionsAndNamesTheirFolder(t *testing.T) {
	f := newFixture(t)
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false)))
	transcript := writeFile(t, filepath.Join(f.home, "sessions", rec.Instance, "-src-api", "s.jsonl"), "{}\n")

	var stderr bytes.Buffer
	d := f.deps()
	d.Err = &stderr
	if err := Remove(context.Background(), d, rec.Instance, false); err != nil {
		t.Fatal(err)
	}
	mustExist(t, transcript)
	mustBeGone(t, rec.GenDir, recordPath(t, f.home, rec.Instance))
	if want := filepath.Join(f.home, "sessions", rec.Instance); !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr %q does not name the sessions folder %s", stderr.String(), want)
	}
}

func TestRmKeepsASessionsFolderThatIsASymlink(t *testing.T) {
	f := newFixture(t)
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false)))
	link := filepath.Join(f.home, "sessions", rec.Instance)
	elsewhere := t.TempDir()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	d := f.deps()
	d.Err = &stderr
	if err := Remove(context.Background(), d, rec.Instance, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("den rm unlinked %s, the user's link to the folder that keeps the sessions: %v", link, err)
	}
	if !strings.Contains(stderr.String(), link) {
		t.Errorf("stderr %q does not name the sessions folder %s", stderr.String(), link)
	}
}

func TestRmRemovesASessionsFolderThatHoldsNothing(t *testing.T) {
	f := newFixture(t)
	rec := f.created(t, repos(repoEntry(f.committedRepo(t, "api"), false)))
	dir := filepath.Join(f.home, "sessions", rec.Instance)
	mustExist(t, dir)

	var stderr bytes.Buffer
	d := f.deps()
	d.Err = &stderr
	if err := Remove(context.Background(), d, rec.Instance, false); err != nil {
		t.Fatal(err)
	}
	mustBeGone(t, dir)
	if strings.Contains(stderr.String(), "sessions") {
		t.Errorf("stderr names sessions that do not exist: %q", stderr.String())
	}
}
