package instance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/v2/internal/config"
	"github.com/PillowPillow/den/v2/internal/nest"
	"github.com/PillowPillow/den/v2/internal/registry"
	"github.com/PillowPillow/den/v2/internal/sbxenv"
	"github.com/PillowPillow/den/v2/internal/worktree"
)

// committedRepo is a git repository with one commit: a worktree needs a HEAD to branch from.
func (f *fixture) committedRepo(t *testing.T, name string) string {
	t.Helper()
	p := f.gitRepo(t, name)
	for _, args := range [][]string{
		{"config", "user.email", "test@example.test"},
		{"config", "user.name", "Test"},
		{"commit", "-q", "--allow-empty", "-m", "initial"},
	} {
		if _, err := f.git.Run(context.Background(), p, args...); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func mustCreate(t *testing.T, f *fixture, p *Plan) registry.Record {
	t.Helper()
	r, err := Create(context.Background(), f.deps(), p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCreateWritesTheS1TrioForAWorktree(t *testing.T) {
	f := newFixture(t)
	api := f.committedRepo(t, "api")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false))})
	p := mustPrepare(t, f, Options{Nest: "am", Branch: "feat"})
	rec := mustCreate(t, f, p)

	ctx := context.Background()
	wt := worktree.Path("central", filepath.Join(f.home, "wt"), "feat", api)
	common, err := worktree.CommonGitDir(ctx, f.git, api)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := worktree.AdminDir(ctx, f.git, wt)
	if err != nil {
		t.Fatal(err)
	}
	gitFile := filepath.Join(p.GenDir, "git", "api.git")
	want := "additionalWorkspaces:\n" +
		"  - path: " + wt + "\n    target: " + api + "\n" +
		"  - path: " + common + "\n    target: /den-git/api\n" +
		"  - path: " + gitFile + "\n    target: " + api + "/.git\n    readOnly: true\n"
	env := readFile(t, filepath.Join(p.RunDir, "sbxenv.yaml"))
	if !strings.Contains(env, want) {
		t.Errorf("sbxenv.yaml:\n%s\nlacks the mounts, in order:\n%s", env, want)
	}
	if got, want := readFile(t, gitFile), "gitdir: /den-git/api/worktrees/"+filepath.Base(admin)+"\n"; got != want {
		t.Errorf("%s = %q, want %q", gitFile, got, want)
	}
	list, err := f.git.Run(ctx, api, "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(list), "locked den") {
		t.Errorf("worktree not locked with reason den:\n%s", list)
	}
	wantRepo := registry.Repo{Name: "api", Repo: api, Mount: wt, Target: api, Worktree: true}
	if len(rec.Repos) != 1 || rec.Repos[0] != wantRepo {
		t.Errorf("recorded repos %+v, want [%+v]", rec.Repos, wantRepo)
	}
	wantWT := registry.Worktree{Branch: "feat", Dir: "feat", Layout: "central", Root: filepath.Join(f.home, "wt")}
	if rec.Worktree == nil || *rec.Worktree != wantWT {
		t.Errorf("recorded worktree %+v, want %+v", rec.Worktree, wantWT)
	}
}

func TestCreateComposesTheKitsAndWritesThem(t *testing.T) {
	f := newFixture(t)
	kit := writeFile(t, filepath.Join(f.src, "kits", "mine", "spec.yaml"), "x")
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) +
		"\nkits: [" + filepath.Dir(kit) + "]\nnetwork: {allow: [example.test]}\nports: [{name: web, sandbox: 3000, host: 8080}]"})
	p := mustPrepare(t, f, Options{Nest: "am"})
	mustCreate(t, f, p)

	claude := filepath.Join(p.GenDir, sbxenv.AgentName)
	want := "kits:\n  - " + claude + "\n  - " + filepath.Dir(kit) + "\n  - " + filepath.Join(p.GenDir, "network") + "\n"
	env := readFile(t, filepath.Join(p.RunDir, "sbxenv.yaml"))
	for _, w := range []string{want, "workspace: " + p.RunDir + "\n", "  - sandbox: 3000\n    host: 8080\n", "name: am\n"} {
		if !strings.Contains(env, w) {
			t.Errorf("sbxenv.yaml:\n%s\nlacks:\n%s", env, w)
		}
	}
	if want, _ := sbxenv.DenClaudeKit(templateFrom, true); readFile(t, filepath.Join(claude, "spec.yaml")) != string(want) {
		t.Errorf("den-claude kit is not the embedded one on the nest's image")
	}
	if got := readFile(t, filepath.Join(p.GenDir, "network", "spec.yaml")); got != string(sbxenv.NetworkKit([]string{"example.test"})) {
		t.Errorf("network kit = %q", got)
	}
}

func TestCreateTwiceOnALockedWorktreeSucceeds(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.committedRepo(t, "api"), false))})
	mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am", Branch: "feat"}))

	p := mustPrepare(t, f, Options{Nest: "am", Branch: "feat"})
	if p.State != Recorded {
		t.Fatalf("state %v, want Recorded", p.State)
	}
	rec := mustCreate(t, f, p)
	if !rec.Repos[0].Worktree {
		t.Errorf("the second create forgot that den created the worktree: %+v", rec.Repos[0])
	}
}

func TestCreateDoesNotClaimAWorktreeItFoundInPlace(t *testing.T) {
	f := newFixture(t)
	api := f.committedRepo(t, "api")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false))})
	wt := worktree.Path("central", filepath.Join(f.home, "wt"), "feat", api)
	if _, err := f.git.Run(context.Background(), api, "worktree", "add", "-q", "-b", "feat", wt); err != nil {
		t.Fatal(err)
	}
	rec := mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am", Branch: "feat"}))
	if rec.Repos[0].Worktree {
		t.Errorf("recorded a worktree den found in place as den-created: %+v", rec.Repos[0])
	}
}

func TestCreateLocksOnlyTheWorktreesItCreated(t *testing.T) {
	f := newFixture(t)
	api, web := f.committedRepo(t, "api"), f.committedRepo(t, "web")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, false))})
	found := worktree.Path("central", filepath.Join(f.home, "wt"), "feat", api)
	if _, err := f.git.Run(context.Background(), api, "worktree", "add", "-q", "-b", "feat", found); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am", Branch: "feat"}))

	for repo, wantLocked := range map[string]bool{api: false, web: true} {
		out, err := f.git.Run(context.Background(), repo, "worktree", "list", "--porcelain")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(out), "locked"); got != wantLocked {
			t.Errorf("%s: locked = %t, want %t:\n%s", repo, got, wantLocked, out)
		}
		if wantLocked && !strings.Contains(string(out), "locked "+worktree.LockReason) {
			t.Errorf("%s: not locked with reason %q:\n%s", repo, worktree.LockReason, out)
		}
	}
}

func TestCreateRecordsBeforeSbxCreate(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	p := mustPrepare(t, f, Options{Nest: "am"})
	f.fake.Default.Err = errors.New("boom")
	if _, err := Create(context.Background(), f.deps(), p); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error %v, want sbx's", err)
	}
	if _, err := registry.Read(f.home, "am"); err != nil {
		t.Errorf("no record after a failed env create: %v", err)
	}
}

func TestCreateFailureNamesTheFullUpCommand(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.committedRepo(t, "api"), false))})
	p := mustPrepare(t, f, Options{Nest: "am", Branch: "feature/12"})
	f.fake.Default.Err = errors.New("boom")
	_, err := Create(context.Background(), f.deps(), p)
	for _, w := range []string{"boom", "`den up am -w feature/12`", "`den rm am.feature-12`"} {
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Errorf("error %v lacks %q", err, w)
		}
	}
}

func TestCreatePassesNoInputForceAndSkipHostCommands(t *testing.T) {
	f := newFixture(t)
	layer := writeFile(t, filepath.Join(f.src, "layer.yaml"), "schemaVersion: \"1\"\n")
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) + "\nlayers: [" + layer + "]"})
	p := mustPrepare(t, f, Options{Nest: "am"})
	rec := mustCreate(t, f, p)

	files := []string{filepath.Join(p.RunDir, "sbxenv.yaml"), layer}
	if !reflect.DeepEqual(rec.EnvFiles, files) {
		t.Errorf("recorded env files %v, want %v", rec.EnvFiles, files)
	}
	var hashes []string
	for _, file := range files {
		sum := sha256.Sum256([]byte(readFile(t, file)))
		hashes = append(hashes, hex.EncodeToString(sum[:]))
	}
	if !reflect.DeepEqual(rec.EnvHashes, hashes) {
		t.Errorf("recorded env hashes %v, want %v", rec.EnvHashes, hashes)
	}
	if onDisk, err := registry.Read(f.home, "am"); err != nil || !reflect.DeepEqual(onDisk.EnvHashes, hashes) {
		t.Errorf("record on disk %+v (%v), want env hashes %v", onDisk.EnvHashes, err, hashes)
	}
	want := append([]string{"--no-input", "env", "create", "--force", "--skip-host-commands"}, files...)
	if !slices.ContainsFunc(f.fake.Calls, func(c []string) bool { return slices.Equal(c, want) }) {
		t.Errorf("calls %v, want %v", f.fake.Calls, want)
	}
	if !f.fake.HasCalled("--no-input", "port", "ls", "am") {
		t.Errorf("calls %v, want `--no-input port ls am`", f.fake.Calls)
	}
}

func TestCreateWritesGitignoreInAProjectFolder(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am"}))
	ignore := filepath.Join(f.proj, ".den", ".gitignore")
	if got := readFile(t, ignore); got != "*\n" {
		t.Errorf("%s = %q, want %q", ignore, got, "*\n")
	}

	writeFile(t, ignore, "mine\n")
	mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am"}))
	if got := readFile(t, ignore); got != "mine\n" {
		t.Errorf("rewrote an existing %s: %q", ignore, got)
	}
}

func TestCreateDoesNotWriteGitFilesIntoTheRunDir(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.committedRepo(t, "api"), false))})
	p := mustPrepare(t, f, Options{Nest: "am", Branch: "feat"})
	mustCreate(t, f, p)
	entries, err := os.ReadDir(p.RunDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !reflect.DeepEqual(names, []string{"sbxenv.yaml"}) {
		t.Errorf("run dir holds %v, want sbxenv.yaml only", names)
	}
}

func TestCreateMountsNoProfileDirectory(t *testing.T) {
	f := newFixture(t)
	f.global(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	writeFile(t, filepath.Join(f.home, "profiles", "p", "settings.json"), "{}")
	writeFile(t, filepath.Join(f.home, "profiles", "p", "CLAUDE.md"), "x")
	p := mustPrepare(t, f, Options{Nest: "am"})
	mustCreate(t, f, p)
	env := readFile(t, filepath.Join(p.RunDir, "sbxenv.yaml"))
	for _, absent := range []string{"/den/profile", "/home/agent/.claude", "profiles"} {
		if strings.Contains(env, absent) {
			t.Errorf("sbxenv.yaml mentions %q:\n%s", absent, env)
		}
	}
}

// strayAdmin answers `rev-parse --git-dir` with a directory outside every common git dir.
type strayAdmin struct {
	worktree.Git
	dir string
}

func (s strayAdmin) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if slices.Equal(args, []string{"rev-parse", "--git-dir"}) {
		return []byte(s.dir + "\n"), nil
	}
	return s.Git.Run(ctx, dir, args...)
}

func TestCreateRefusesAnAdminDirOutsideTheCommonGitDir(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.committedRepo(t, "api"), false))})
	p := mustPrepare(t, f, Options{Nest: "am", Branch: "feat"})
	stray := filepath.Join(f.src, "elsewhere", "feat")
	f.git = strayAdmin{Git: f.git, dir: stray}

	_, err := Create(context.Background(), f.deps(), p)
	if err == nil || !strings.Contains(err.Error(), stray) {
		t.Fatalf("error %v, want a refusal naming %s", err, stray)
	}
	for _, path := range []string{p.GenDir, p.RunDir, filepath.Join(f.home, "gen")} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s was written before the refusal", path)
		}
	}
	if _, err := registry.Read(f.home, "am.feat"); err == nil {
		t.Errorf("a record was written before the refusal")
	}
	if f.fake.HasCalled("--no-input", "env", "create") {
		t.Errorf("env create ran after the refusal: %v", f.fake.Calls)
	}
}

func TestCreateRollsBackTheWorktreesItMadeWhenALaterRepoFails(t *testing.T) {
	f := newFixture(t)
	api, web := f.committedRepo(t, "api"), f.committedRepo(t, "web")
	if _, err := f.git.Run(context.Background(), web, "worktree", "add", "-q", "-b", "feat", filepath.Join(f.src, "elsewhere")); err != nil {
		t.Fatal(err)
	}
	f.project(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, false))})
	p := mustPrepare(t, f, Options{Nest: "am", Branch: "feat"})

	if _, err := Create(context.Background(), f.deps(), p); err == nil {
		t.Fatal("want web's worktree to fail: feat is checked out elsewhere")
	}
	wt := worktree.Path("central", filepath.Join(f.home, "wt"), "feat", api)
	if _, err := os.Stat(wt); err == nil {
		t.Errorf("%s survived the rollback", wt)
	}
	if _, err := f.git.Run(context.Background(), api, "rev-parse", "--verify", "-q", "refs/heads/feat"); err != nil {
		t.Errorf("the rollback deleted branch feat of api: %v", err)
	}
	if _, err := registry.Read(f.home, "am.feat"); err == nil {
		t.Errorf("a record was written for a failed create")
	}
	if f.fake.HasCalled("--no-input", "env", "create") {
		t.Errorf("env create ran after a failed worktree")
	}
}

func TestCreateRollbackKeepsAWorktreeFoundInPlace(t *testing.T) {
	f := newFixture(t)
	api, web := f.committedRepo(t, "api"), f.committedRepo(t, "web")
	wt := worktree.Path("central", filepath.Join(f.home, "wt"), "feat", api)
	if _, err := f.git.Run(context.Background(), api, "worktree", "add", "-q", "-b", "feat", wt); err != nil {
		t.Fatal(err)
	}
	if _, err := f.git.Run(context.Background(), web, "worktree", "add", "-q", "-b", "feat", filepath.Join(f.src, "elsewhere")); err != nil {
		t.Fatal(err)
	}
	f.project(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, false))})
	p := mustPrepare(t, f, Options{Nest: "am", Branch: "feat"})

	if _, err := Create(context.Background(), f.deps(), p); err == nil {
		t.Fatal("want web's worktree to fail")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("the rollback removed %s, which den found in place: %v", wt, err)
	}
}

func TestPrepareRefusesARetryThatDropsADenWorktree(t *testing.T) {
	f := newFixture(t)
	api, web := f.committedRepo(t, "api"), f.committedRepo(t, "web")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, true))})
	mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am", Branch: "feat"}))

	prepareErr(t, f, Options{Nest: "am", Branch: "feat", Selection: nest.Selection{Without: []string{"web"}}},
		"web", worktree.Path("central", filepath.Join(f.home, "wt"), "feat", web), "`den rm am.feat`")
}

func TestPrepareRefusesARetryWhoseWorktreeMoved(t *testing.T) {
	f := newFixture(t)
	api := f.committedRepo(t, "api")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false))})
	mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am", Branch: "feat"}))

	moved := filepath.Join(f.src, "wt2")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false)) + "\nworktrees: {root: " + moved + "}"})
	prepareErr(t, f, Options{Nest: "am", Branch: "feat"},
		"api", worktree.Path("central", filepath.Join(f.home, "wt"), "feat", api),
		worktree.Path("central", moved, "feat", api), "`den rm am.feat`")
}

func TestOverlaysCoverTheDefinition(t *testing.T) {
	f := newFixture(t)
	api := f.repo(t, "api")
	loc := filepath.Join(api, "tpl")
	writeFile(t, filepath.Join(loc, "kits", "tk", "spec.yaml"), "kind: mixin\n")
	writeFile(t, filepath.Join(loc, "lib.sh"), "set -e\n")
	step := writeFile(t, filepath.Join(loc, "10-node.sh"), "true\n")
	f.dirTemplate(t, "legacy", loc, "kits: [./kits/tk]\nbuild: {includes: [./lib.sh], steps: [./10-node.sh]}")
	layer := writeFile(t, filepath.Join(api, "sbx", "extra.yaml"), "env: {}\n")
	writeFile(t, filepath.Join(f.proj, "kits", "k", "spec.yaml"), "kind: mixin\n")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false)) +
		fmt.Sprintf("\ntemplate: legacy\nkits: [./kits/k]\nlayers: [%q]\nstart: .", layer)})
	p := mustPrepare(t, f, Options{Nest: "am"})
	mustCreate(t, f, p)

	env := readFile(t, filepath.Join(p.RunDir, "sbxenv.yaml"))
	got := strings.NewReplacer(
		worktree.ResolvePath(f.home), "/denhome", worktree.ResolvePath(f.proj), "/proj", worktree.ResolvePath(f.src), "/src",
		f.home, "/denhome", f.proj, "/proj", f.src, "/src").Replace(env)
	want := readFile(t, filepath.Join("testdata", "overlays.golden"))
	if got != want {
		t.Errorf("sbxenv.yaml mismatch\n--- got\n%s\n--- want\n%s", got, want)
	}

	if err := os.Remove(step); err != nil {
		t.Fatal(err)
	}
	prepareErr(t, f, Options{Nest: "am"}, "of template \"legacy\" in "+filepath.Join(worktree.ResolvePath(loc), config.DefinitionFileName))
}

func TestCreateWritesAKitPerInstance(t *testing.T) {
	f := newFixture(t)
	f.dirTemplate(t, "legacy", filepath.Join(f.src, "tpl"), "")
	f.project(t, map[string]string{
		"am":  repos(repoEntry(f.repo(t, "api"), false)) + "\ntemplate: legacy",
		"web": repos(repoEntry(f.repo(t, "site"), false)),
	})
	for nest, image := range map[string]string{"am": templateImage, "web": templateFrom} {
		p := mustPrepare(t, f, Options{Nest: nest})
		mustCreate(t, f, p)
		kit := filepath.Join(p.GenDir, sbxenv.AgentName)
		if got := readFile(t, filepath.Join(kit, "spec.yaml")); !strings.Contains(got, "\n  image: "+image+"\n") {
			t.Errorf("%s: den-claude kit does not run %s:\n%s", nest, image, got)
		}
		if env := readFile(t, filepath.Join(p.RunDir, EnvFileName)); !strings.Contains(env, "kits:\n  - "+kit+"\n") {
			t.Errorf("%s: sbxenv.yaml does not start from its own kit %s:\n%s", nest, kit, env)
		}
	}
	if _, err := os.Stat(filepath.Join(f.home, "gen", ".kits")); err == nil {
		t.Errorf("gen/.kits was written: an instance kit shared by two templates would carry the image of the last create")
	}
}

func TestWriteDenClaudeKitWritesTheImage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "den-claude")
	image := "registry.test/claude@sha256:" + strings.Repeat("cd", 32)
	if err := WriteDenClaudeKit(dir, image, true); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "spec.yaml"))
	if !strings.Contains(got, "  image: "+image+"\n") || strings.Count(got, "image:") != 1 {
		t.Errorf("kit does not carry exactly the image:\n%s", got)
	}
}

func TestWriteDenClaudeKitRefusesNoImage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "den-claude")
	if err := WriteDenClaudeKit(dir, "", true); err == nil {
		t.Fatal("want a refusal: den has no image of its own")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("%s was written without an image", dir)
	}
}

func typoNest(t *testing.T, f *fixture) *Plan {
	t.Helper()
	f.project(t, map[string]string{"am": "template: claude-cod\n" + repos(repoEntry(f.repo(t, "api"), false))})
	return mustPrepare(t, f, Options{Nest: "am"})
}

func TestCreateOnAnImageNamesTheKeyOnFailure(t *testing.T) {
	f := newFixture(t)
	p := typoNest(t, f)
	f.fake.Default.Err = errors.New("pull access denied")
	_, err := Create(context.Background(), f.deps(), p)
	c := p.Resolved.Template.Choice
	for _, w := range []string{
		`: if the VM never started, no template is named "claude-cod" in ` + strings.Join(c.Searched, " or ") +
			", and sbx could not start the image docker.io/docker/sandbox-templates:claude-cod",
		"fix " + c.Origin,
	} {
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Errorf("error %v lacks %q", err, w)
		}
	}
}

func TestUpNamesTheKeyWhenTheAttachFailsRightAfterCreate(t *testing.T) {
	f := newFixture(t)
	p := typoNest(t, f)
	f.fake.AttachErr = errors.New("image not found")
	err := Up(context.Background(), f.deps(), Options{Nest: "am"}, false)
	if err == nil || !strings.Contains(err.Error(), "image not found") ||
		!strings.Contains(err.Error(), `: if the VM never started, no template is named "claude-cod"`) ||
		!strings.Contains(err.Error(), "fix "+p.Resolved.Template.Choice.Origin) {
		t.Errorf("error %v", err)
	}
}

func TestUpLeavesALiveAttachErrorAlone(t *testing.T) {
	f := newFixture(t)
	typoNest(t, f)
	f.live("am")
	f.fake.AttachErr = errors.New("image not found")
	err := Up(context.Background(), f.deps(), Options{Nest: "am"}, false)
	if err == nil || strings.Contains(err.Error(), "no template is named") {
		t.Errorf("error %v", err)
	}
}

func TestCreateOnATemplateAddsNoImageHint(t *testing.T) {
	f := newFixture(t)
	f.dirTemplate(t, "legacy", filepath.Join(f.src, "tpl"), "")
	f.project(t, map[string]string{"am": "template: legacy\n" + repos(repoEntry(f.repo(t, "api"), false))})
	p := mustPrepare(t, f, Options{Nest: "am"})
	f.fake.Default.Err = errors.New("boom")
	_, err := Create(context.Background(), f.deps(), p)
	if err == nil || strings.Contains(err.Error(), "no template is named") {
		t.Errorf("error %v", err)
	}
}

func trustList(t *testing.T, p *Plan) []string {
	t.Helper()
	var got []string
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(p.GenDir, sbxenv.AgentName, sbxenv.TrustJSONFile))), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCreateTrustsEveryMountedRepoAndTheStartFolder(t *testing.T) {
	f := newFixture(t)
	api, web := f.repo(t, "api"), f.repo(t, "web")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false), repoEntry(web, false)) + "\nstart: api/sub"})
	p := mustPrepare(t, f, Options{Nest: "am"})
	mustCreate(t, f, p)
	if got, want := trustList(t, p), []string{api, web, filepath.Join(api, "sub")}; !reflect.DeepEqual(got, want) {
		t.Errorf("trusted %v, want %v", got, want)
	}
}

func TestCreateTrustsTheProjectFolderWhenItIsTheStart(t *testing.T) {
	f := newFixture(t)
	api := f.repo(t, "api")
	f.project(t, map[string]string{"am": repos(repoEntry(api, false)) + "\nstart: ."})
	p := mustPrepare(t, f, Options{Nest: "am"})
	mustCreate(t, f, p)
	if got, want := trustList(t, p), []string{api, f.proj}; !reflect.DeepEqual(got, want) {
		t.Errorf("trusted %v, want %v", got, want)
	}
}

func TestCreateWithoutAutoTrustTrustsNothingAndDropsTheSandboxedSwitch(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) + "\nauto_trust: false"})
	p := mustPrepare(t, f, Options{Nest: "am"})
	mustCreate(t, f, p)
	if got := trustList(t, p); len(got) != 0 {
		t.Errorf("trusted %v, want none", got)
	}
	if want, _ := sbxenv.DenClaudeKit(templateFrom, false); readFile(t, filepath.Join(p.GenDir, sbxenv.AgentName, "spec.yaml")) != string(want) {
		t.Errorf("den-claude kit is not the kit without auto-trust")
	}
}

func TestCreateWritesTheNestResources(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) + "\nresources: {cpus: 4, memory: 8g}"})
	p := mustPrepare(t, f, Options{Nest: "am"})
	mustCreate(t, f, p)

	if env := readFile(t, filepath.Join(p.RunDir, "sbxenv.yaml")); !strings.Contains(env, "  cpus: 4\n  memory: 8g\n") {
		t.Errorf("sbxenv.yaml:\n%s\nlacks the nest's cpus and memory", env)
	}
}

func TestCreateWithoutResourcesWritesNoCPUsNorMemory(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	p := mustPrepare(t, f, Options{Nest: "am"})
	mustCreate(t, f, p)

	env := readFile(t, filepath.Join(p.RunDir, "sbxenv.yaml"))
	for _, k := range []string{"cpus:", "memory:"} {
		if strings.Contains(env, k) {
			t.Errorf("sbxenv.yaml writes %s with no resources declared:\n%s", k, env)
		}
	}
}

func TestCreateMountsTheSessionsFolderOfTheInstance(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.committedRepo(t, "api"), false))})
	p := mustPrepare(t, f, Options{Nest: "am", Branch: "feat"})
	mustCreate(t, f, p)

	dir := filepath.Join(f.home, "sessions", "am.feat")
	want := "  - path: " + dir + "\n    target: " + sbxenv.SessionsVMPath + "\n"
	if env := readFile(t, filepath.Join(p.RunDir, "sbxenv.yaml")); !strings.Contains(env, want) {
		t.Errorf("sbxenv.yaml:\n%s\nlacks:\n%s", env, want)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o700 {
		t.Errorf("%s has mode %o, want 700: a transcript holds what the user and the tools wrote", dir, got)
	}
}

func TestCreateKeepsTheSessionsAnEarlierInstanceLeft(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	old := writeFile(t, filepath.Join(f.home, "sessions", "am", "-src-api", "s.jsonl"), "{}\n")
	mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am"}))
	mustExist(t, old)
}

func TestCreateSetsMode700OnASessionsFolderFoundLooser(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
	root := filepath.Join(f.home, "sessions")
	dir := filepath.Join(root, "am")
	for _, d := range []string{root, dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustCreate(t, f, mustPrepare(t, f, Options{Nest: "am"}))

	for _, d := range []string{root, dir} {
		st, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != 0o700 {
			t.Errorf("%s has mode %o, want 700: the VM of an earlier instance can have changed it", d, got)
		}
	}
}

func TestCreateNamesTheSessionsFolderItLoadsOnlyWhenItHoldsSomething(t *testing.T) {
	for _, tc := range []struct {
		name string
		kept bool
	}{{"kept sessions", true}, {"new folder", false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false))})
			dir := filepath.Join(f.home, "sessions", "am")
			if tc.kept {
				writeFile(t, filepath.Join(dir, "-src-api", "s.jsonl"), "{}\n")
			}
			var stderr bytes.Buffer
			d := f.deps()
			d.Err = &stderr
			if _, err := Create(context.Background(), d, mustPrepare(t, f, Options{Nest: "am"})); err != nil {
				t.Fatal(err)
			}
			want := "am loads the sessions kept in " + dir
			if got := strings.Contains(stderr.String(), want); got != tc.kept {
				t.Errorf("stderr %q: notice %q present = %t, want %t", stderr.String(), want, got, tc.kept)
			}
		})
	}
}

func TestCreateWithoutSessionsMountsNoSessionsFolder(t *testing.T) {
	f := newFixture(t)
	f.project(t, map[string]string{"am": repos(repoEntry(f.repo(t, "api"), false)) + "\nsessions: false"})
	p := mustPrepare(t, f, Options{Nest: "am"})
	mustCreate(t, f, p)

	if env := readFile(t, filepath.Join(p.RunDir, "sbxenv.yaml")); strings.Contains(env, sbxenv.SessionsVMPath) {
		t.Errorf("sbxenv.yaml mounts a sessions folder with sessions: false:\n%s", env)
	}
	mustBeGone(t, filepath.Join(f.home, "sessions"))
}
