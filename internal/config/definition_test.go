package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDefinitions(t *testing.T, body string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, DefinitionFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadDefinitionsResolvesPathsUnderTheRoot(t *testing.T) {
	root := writeDefinitions(t, `schema: 1
templates:
  t:
    kits: [./kits/a, docker.io/acme/kit:1, localhost:5000/k]
    build:
      includes: [lib/c.sh]
      steps: [s/1.sh]
`)
	l, err := LoadDefinitions(root)
	if err != nil {
		t.Fatal(err)
	}
	d := l.File.Templates["t"]
	wantKits := []string{filepath.Join(root, "kits/a"), "docker.io/acme/kit:1", "localhost:5000/k"}
	for i, w := range wantKits {
		if d.Kits[i] != w {
			t.Errorf("kits[%d] = %q, want %q", i, d.Kits[i], w)
		}
	}
	if d.Build.Includes[0] != filepath.Join(root, "lib/c.sh") || d.Build.Steps[0] != filepath.Join(root, "s/1.sh") {
		t.Errorf("includes/steps = %v %v", d.Build.Includes, d.Build.Steps)
	}
}

func TestLoadDefinitionsReadsTheDesignExample(t *testing.T) {
	root := writeDefinitions(t, `schema: 1
templates:
  am:
    from: docker-claude
    kits:
      - kits/ssh-known-hosts
      - kits/shell-zsh
      - stacks/base/kit
`)
	l, err := LoadDefinitions(root)
	if err != nil {
		t.Fatal(err)
	}
	for i, k := range l.File.Templates["am"].Kits {
		if !strings.HasPrefix(k, root+string(filepath.Separator)) {
			t.Errorf("kits[%d] = %q, not under %s", i, k, root)
		}
	}
	if l.IsSibling("docker-claude") || !l.IsSibling("am") {
		t.Error("IsSibling wrong")
	}
}

func TestLoadDefinitionsRefusesAnEscape(t *testing.T) {
	cases := map[string]string{
		"kits ..":     "kits: [../x]",
		"kits abs":    "kits: [/etc]",
		"kits home":   "kits: [~/k]",
		"steps up":    "build: {steps: [../../s.sh]}",
		"includes":    "build: {includes: [/tmp/i.sh]}",
		"kits symlnk": "kits: [./link]",
		"dangling":    "build: {steps: [dangling]}",
		"dir link":    "build: {steps: [link/missing.sh]}",
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			root := writeDefinitions(t, "schema: 1\ntemplates:\n  t:\n    "+field+"\n")
			if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(t.TempDir(), "missing.sh"), filepath.Join(root, "dangling")); err != nil {
				t.Fatal(err)
			}
			_, err := LoadDefinitions(root)
			if err == nil {
				t.Fatal("want an error")
			}
			for _, w := range []string{DefinitionFileName, "templates.t.", "leaves"} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestLoadDefinitionsRefusesAFromCycle(t *testing.T) {
	root := writeDefinitions(t, "schema: 1\ntemplates:\n  a: {from: b}\n  b: {from: a}\n")
	_, err := LoadDefinitions(root)
	if err == nil || !strings.Contains(err.Error(), "templates.a.from: cycle a → b → a") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadDefinitionsIsStrict(t *testing.T) {
	root := writeDefinitions(t, "schema: 1\ntemplates:\n  t:\n    image: x\n")
	_, err := LoadDefinitions(root)
	if err == nil || !strings.Contains(err.Error(), DefinitionFileName) || !strings.Contains(err.Error(), `unknown key "image"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadDefinitionsNamesTheMissingFile(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	_, err := LoadDefinitions(root)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, DefinitionFileName)) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadDefinitionsKitReferenceOrPath(t *testing.T) {
	cases := []struct {
		kit    string
		isPath bool
	}{
		{"kit.d", true},
		{"my:kit", true},
		{"localhost", true},
		{"./kits/a", true},
		{"kits/a", true},
		{"localhost:5000/k", false},
		{"docker.io/acme/kit:1", false},
	}
	for _, c := range cases {
		t.Run(c.kit, func(t *testing.T) {
			root := writeDefinitions(t, "schema: 1\ntemplates:\n  t:\n    kits: ["+c.kit+"]\n")
			l, err := LoadDefinitions(root)
			if err != nil {
				t.Fatal(err)
			}
			want := c.kit
			if c.isPath {
				want = filepath.Join(root, c.kit)
			}
			if got := l.File.Templates["t"].Kits[0]; got != want {
				t.Errorf("kit = %q, want %q", got, want)
			}
		})
	}
}

func TestLoadDefinitionsRequiresTheSchema(t *testing.T) {
	root := writeDefinitions(t, "templates:\n  t: {}\n")
	_, err := LoadDefinitions(root)
	if err == nil || !strings.Contains(err.Error(), DefinitionFileName) || !strings.Contains(err.Error(), "schema: must be 1, got 0") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadDefinitionsAcceptsAMissingTailUnderTheRoot(t *testing.T) {
	root := writeDefinitions(t, "schema: 1\ntemplates:\n  t:\n    build: {steps: [nodir/missing.sh]}\n")
	if _, err := LoadDefinitions(root); err != nil {
		t.Fatal(err)
	}
}
