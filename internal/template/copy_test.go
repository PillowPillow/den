package template

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/worktree"
)

type spyGit struct {
	inner worktree.Git
	calls [][]string
	dirs  []string
}

func (s *spyGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	s.calls = append(s.calls, args)
	s.dirs = append(s.dirs, dir)
	return s.inner.Run(ctx, dir, args...)
}

func (s *spyGit) RunWithInput(ctx context.Context, dir string, in []byte, args ...string) ([]byte, error) {
	s.calls = append(s.calls, args)
	return s.inner.RunWithInput(ctx, dir, in, args...)
}

type fakeGit func(ctx context.Context, dir string, args []string) ([]byte, error)

func (f fakeGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return f(ctx, dir, args)
}

func (f fakeGit) RunWithInput(ctx context.Context, dir string, _ []byte, args ...string) ([]byte, error) {
	return f(ctx, dir, args)
}

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

func TestCloneDisablesHooksAndSubmodules(t *testing.T) {
	src := t.TempDir()
	git(t, src, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644)
	git(t, src, "add", ".")
	git(t, src, "commit", "-q", "-m", "c")
	head := git(t, src, "rev-parse", "HEAD")
	git(t, src, "tag", "v1")

	for _, ref := range []string{"", "v1"} {
		spy := &spyGit{inner: worktree.NewGit()}
		dest := filepath.Join(t.TempDir(), "d")
		commit, err := Clone(context.Background(), spy, src, ref, dest)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"-c", "core.hooksPath=/dev/null", "-c", "protocol.ext.allow=never", "clone", "--no-recurse-submodules", "--single-branch"}
		if ref != "" {
			want = append(want, "--branch", ref)
		}
		want = append(want, "--", src, dest)
		if !slices.Equal(spy.calls[0], want) {
			t.Fatalf("ref %q: argv %v, want %v", ref, spy.calls[0], want)
		}
		if spy.dirs[0] != "/" {
			t.Fatalf("ref %q: ran in %q, want /", ref, spy.dirs[0])
		}
		if commit != head {
			t.Fatalf("ref %q: commit %s, want %s", ref, commit, head)
		}
	}
}

func TestRemoteHeadIsBatchModeAndBounded(t *testing.T) {
	var argv []string
	var dir string
	spy := fakeGit(func(_ context.Context, d string, args []string) ([]byte, error) {
		argv, dir = args, d
		return []byte("aaaa\tHEAD\naaaa\trefs/heads/v1\n"), nil
	})
	if _, err := RemoteHead(context.Background(), spy, "u", ""); err != nil {
		t.Fatal(err)
	}
	want := []string{"-c", "core.sshCommand=ssh -o BatchMode=yes -o ConnectTimeout=3", "-c", "protocol.ext.allow=never", "ls-remote", "--", "u", "HEAD"}
	if !slices.Equal(argv, want) || dir != "/" {
		t.Fatalf("argv %v in %q, want %v in /", argv, dir, want)
	}
	if _, err := RemoteHead(context.Background(), spy, "u", "v1"); err != nil {
		t.Fatal(err)
	}
	if got := argv[len(argv)-2:]; !slices.Equal(got, []string{"v1", "v1^{}"}) {
		t.Fatalf("patterns %v", got)
	}

	blocked := fakeGit(func(ctx context.Context, _ string, _ []string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	start := time.Now()
	if _, err := RemoteHead(context.Background(), blocked, "u", "main"); err == nil {
		t.Fatal("blocked git did not fail")
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestRemoteHeadPeelsAnnotatedTags(t *testing.T) {
	a, b, c, d, e := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40), strings.Repeat("e", 40)
	out := func(s string) fakeGit {
		return func(context.Context, string, []string) ([]byte, error) { return []byte(s), nil }
	}
	got, err := RemoteHead(context.Background(), out(a+"\trefs/tags/v1\n"+b+"\trefs/tags/v1^{}\n"+c+"\trefs/heads/x/v1\n"), "u", "v1")
	if err != nil || got != b {
		t.Fatalf("annotated tag: %q, %v", got, err)
	}
	got, err = RemoteHead(context.Background(), out(c+"\trefs/heads/x/main\n"+d+"\trefs/heads/main\n"), "u", "main")
	if err != nil || got != d {
		t.Fatalf("branch: %q, %v", got, err)
	}
	got, err = RemoteHead(context.Background(), out(a+"\trefs/tags/dup\n"+b+"\trefs/tags/dup^{}\n"+d+"\trefs/heads/dup\n"), "u", "dup")
	if err != nil || got != d {
		t.Fatalf("branch beats tag: %q, %v", got, err)
	}
	got, err = RemoteHead(context.Background(), out(a+"\trefs/tags/v2\n"), "u", "v2")
	if err != nil || got != a {
		t.Fatalf("lightweight tag: %q, %v", got, err)
	}
	got, err = RemoteHead(context.Background(), out(e+"\trefs/remotes/origin/HEAD\n"+d+"\tHEAD\n"), "u", "")
	if err != nil || got != d {
		t.Fatalf("HEAD decoy: %q, %v", got, err)
	}
	if _, err := RemoteHead(context.Background(), out(c+"\trefs/heads/x/main\n"), "u", "main"); err == nil {
		t.Fatal("decoy alone must not match")
	}
}

func TestDefinitionHashMovesWithAnyCitedFile(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("den-template.yaml", `schema: 1
templates:
  web:
    from: base
    kits: [kits/k, ghcr.io/acme/kit:1]
    build:
      includes: [inc/a.txt]
      steps: [steps/s.sh]
`)
	write("kits/k/spec.yaml", "one")
	write("kits/k/sub/f", "one")
	write("inc/a.txt", "one")
	write("steps/s.sh", "one")
	write("other/o", "one")
	hash := func() string {
		d, err := config.LoadDefinitions(root)
		if err != nil {
			t.Fatal(err)
		}
		h, err := DefinitionHash(d, "web")
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := hash()
	if hash() != base {
		t.Fatal("hash is not deterministic")
	}
	write("other/o", "two")
	if hash() != base {
		t.Fatal("an uncited file moved the hash")
	}
	for _, rel := range []string{"kits/k/spec.yaml", "kits/k/sub/f", "inc/a.txt", "steps/s.sh"} {
		write(rel, "two")
		if got := hash(); got == base {
			t.Fatalf("%s did not move the hash", rel)
		}
		write(rel, "one")
		if hash() != base {
			t.Fatalf("restoring %s did not restore the hash", rel)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(root, "den-template.yaml"))
	write("den-template.yaml", string(raw)+"  extra:\n    from: base\n")
	if hash() == base {
		t.Fatal("den-template.yaml did not move the hash")
	}
}

func commitIn(t *testing.T, dir, file string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, file), []byte(file), 0o644)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", file)
}

func TestRemoteHeadMatchesCloneForRealRefs(t *testing.T) {
	src := t.TempDir()
	git(t, src, "init", "-q", "-b", "main")
	commitIn(t, src, "a")
	git(t, src, "tag", "-a", "-m", "v1", "v1")
	git(t, src, "branch", "x/v1")
	commitIn(t, src, "b")
	git(t, src, "tag", "-a", "-m", "dup", "dup")
	git(t, src, "branch", "dup")
	commitIn(t, src, "c")
	g := worktree.NewGit()
	for _, ref := range []string{"", "main", "v1", "dup"} {
		remote, err := RemoteHead(context.Background(), g, src, ref)
		if err != nil {
			t.Fatalf("%q: %v", ref, err)
		}
		cloned, err := Clone(context.Background(), g, src, ref, filepath.Join(t.TempDir(), "d"))
		if err != nil {
			t.Fatalf("%q: %v", ref, err)
		}
		if remote != cloned {
			t.Fatalf("ref %q: RemoteHead %s, Clone %s", ref, remote, cloned)
		}
	}
}

func TestGitIgnoresTheWorkingDirectoryConfig(t *testing.T) {
	src := t.TempDir()
	git(t, src, "init", "-q", "-b", "main")
	commitIn(t, src, "a")
	head := git(t, src, "rev-parse", "HEAD")
	cwd := t.TempDir()
	git(t, cwd, "init", "-q")
	git(t, cwd, "config", "url./nonexistent/.insteadOf", src)
	old, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	g := worktree.NewGit()
	got, err := RemoteHead(context.Background(), g, src, "")
	if err != nil || got != head {
		t.Fatalf("RemoteHead: %q, %v", got, err)
	}
	if _, err := Clone(context.Background(), g, src, "", filepath.Join(t.TempDir(), "d")); err != nil {
		t.Fatalf("Clone: %v", err)
	}
}

func TestDefinitionHashFollowsAnInRootKitSymlink(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "real"), 0o755)
	os.WriteFile(filepath.Join(root, "real", "spec.yaml"), []byte("one"), 0o644)
	os.Symlink("real", filepath.Join(root, "kit"))
	os.Symlink("spec.yaml", filepath.Join(root, "real", "alias"))
	os.WriteFile(filepath.Join(root, "den-template.yaml"), []byte("schema: 1\ntemplates:\n  web:\n    from: base\n    kits: [kit]\n"), 0o644)
	hash := func() string {
		d, err := config.LoadDefinitions(root)
		if err != nil {
			t.Fatal(err)
		}
		h, err := DefinitionHash(d, "web")
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := hash()
	os.WriteFile(filepath.Join(root, "real", "spec.yaml"), []byte("two"), 0o644)
	if hash() == base {
		t.Fatal("a file under the symlinked kit did not move the hash")
	}
}

func TestDefinitionHashCoversTheFromChain(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("den-template.yaml", `schema: 1
templates:
  root:
    from: claude-code-docker
    kits: [kits/r]
    build:
      includes: [inc/r.sh]
      steps: [steps/r.sh]
  digitaleo:
    from: root
    build:
      steps: [steps/d.sh]
  kampn:
    from: digitaleo
    build:
      steps: [steps/k.sh]
  other:
    from: claude-code-docker
    build:
      steps: [steps/o.sh]
`)
	for _, rel := range []string{"kits/r/spec.yaml", "inc/r.sh", "steps/r.sh", "steps/d.sh", "steps/k.sh", "steps/o.sh"} {
		write(rel, "one")
	}
	hash := func() string {
		d, err := config.LoadDefinitions(root)
		if err != nil {
			t.Fatal(err)
		}
		h, err := DefinitionHash(d, "kampn")
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := hash()
	for _, rel := range []string{"steps/d.sh", "kits/r/spec.yaml", "inc/r.sh", "steps/r.sh"} {
		write(rel, "two")
		if hash() == base {
			t.Fatalf("%s, cited by a template kampn starts from, did not move kampn's hash", rel)
		}
		write(rel, "one")
	}
	write("steps/o.sh", "two")
	if hash() != base {
		t.Fatal("a template outside the chain moved the hash")
	}
}

func TestDefinitionHashOfATemplateWithoutSiblingIgnoresItsChildren(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "steps"), 0o755)
	os.WriteFile(filepath.Join(root, "den-template.yaml"), []byte("schema: 1\ntemplates:\n"+
		"  digitaleo:\n    from: claude-code-docker\n    build:\n      steps: [steps/d.sh]\n"+
		"  kampn:\n    from: digitaleo\n    build:\n      steps: [steps/k.sh]\n"), 0o644)
	os.WriteFile(filepath.Join(root, "steps", "d.sh"), []byte("one"), 0o644)
	os.WriteFile(filepath.Join(root, "steps", "k.sh"), []byte("one"), 0o644)
	hash := func() string {
		d, err := config.LoadDefinitions(root)
		if err != nil {
			t.Fatal(err)
		}
		h, err := DefinitionHash(d, "digitaleo")
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := hash()
	os.WriteFile(filepath.Join(root, "steps", "k.sh"), []byte("two"), 0o644)
	if hash() != base {
		t.Fatal("a child's step moved its parent's hash")
	}
}
