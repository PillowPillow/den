package sbxenv

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s mismatch\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestRenderPlainGolden(t *testing.T) {
	got, err := Render(Env{
		Name:      "am",
		Kits:      []string{"/h/.den/gen/am/den-claude", "/h/.den/gen/am/network"},
		Workspace: "/h/.den/run/am",
		Mounts:    []Mount{{Path: "/h/dev/am-backend"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "plain.golden", got)
}

func TestRenderWorktreeGolden(t *testing.T) {
	got, err := Render(Env{
		Name:      "am.feat",
		Kits:      []string{"/h/.den/gen/am.feat/den-claude", "/h/.den/kits/pillow-env", "/h/.den/gen/am.feat/network"},
		Workspace: "/h/.den/run/am.feat",
		Mounts: []Mount{
			{Path: "/h/.den/wt/feat/am-backend", Target: "/h/dev/am-backend"},
			{Path: "/h/dev/am-backend/.git", Target: GitMountRoot + "/am-backend"},
			{Path: "/h/.den/gen/am.feat/git/am-backend.git", Target: "/h/dev/am-backend/.git", ReadOnly: true},
		},
		Env:      map[string]string{"PAGER": "cat"},
		Ports:    []Port{{Sandbox: 5657}},
		Template: "pillow:v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "worktree.golden", got)
}

func TestRenderOmitsEmptyBlocks(t *testing.T) {
	got, err := Render(Env{Name: "a", Workspace: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"kits:", "additionalWorkspaces:", "env:", "ports:", "sandboxOptions:"} {
		if strings.Contains(string(got), k) {
			t.Errorf("empty %s rendered:\n%s", k, got)
		}
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	e := Env{Name: "a", Workspace: "/w", Env: map[string]string{"Z": "1", "A": "2", "M": "3", "B": "4"}}
	first, _ := Render(e)
	for i := 0; i < 20; i++ {
		again, _ := Render(e)
		if !bytes.Equal(first, again) {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, again)
		}
	}
	if !strings.Contains(string(first), "  A: \"2\"\n  B: \"4\"\n  M: \"3\"\n  Z: \"1\"\n") {
		t.Errorf("env keys not sorted or numeric strings unquoted:\n%s", first)
	}
}

func TestNetworkKitGolden(t *testing.T) {
	golden(t, "network.golden", NetworkKit([]string{"api.github.com", "registry.npmjs.org"}))
}

const kitImage = "docker.io/docker/sandbox-templates:claude-code-docker"

func kit(t *testing.T, image string) []byte {
	t.Helper()
	spec, err := DenClaudeKit(image)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestDenClaudeKitHasNoCredentialsBlock(t *testing.T) {
	var m map[string]any
	if err := yaml.Unmarshal(kit(t, kitImage), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["credentials"]; ok {
		t.Error("den-claude must not declare credentials")
	}
}

func TestDenClaudeKitAllowsOnlyHTTPSToAnthropic(t *testing.T) {
	var m struct {
		Permissions struct {
			Network struct {
				Allow []string `yaml:"allow"`
			} `yaml:"network"`
		} `yaml:"permissions"`
	}
	if err := yaml.Unmarshal(kit(t, kitImage), &m); err != nil {
		t.Fatal(err)
	}
	if want := []string{"api.anthropic.com:443"}; !reflect.DeepEqual(m.Permissions.Network.Allow, want) {
		t.Errorf("network allow %v, want %v", m.Permissions.Network.Allow, want)
	}
}

func TestDenClaudeKitHasNoSettingsFlag(t *testing.T) {
	var m struct {
		Sandbox struct {
			Entrypoint []string `yaml:"entrypoint"`
		} `yaml:"sandbox"`
		Setup any `yaml:"setup"`
	}
	if err := yaml.Unmarshal(kit(t, kitImage), &m); err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude", "--dangerously-skip-permissions"}; !reflect.DeepEqual(m.Sandbox.Entrypoint, want) {
		t.Errorf("entrypoint %v, want %v", m.Sandbox.Entrypoint, want)
	}
	if m.Setup != nil {
		t.Errorf("den-claude declares setup %v, want none", m.Setup)
	}
}

func TestGitFile(t *testing.T) {
	if got := string(GitFile("/x/.git/worktrees/w")); got != "gitdir: /x/.git/worktrees/w\n" {
		t.Errorf("got %q", got)
	}
}

func TestDenClaudeKitHoldsExactlyOneImageLine(t *testing.T) {
	if n := strings.Count(string(denClaudeSpec), "\n  image: "); n != 1 {
		t.Fatalf("embedded spec has %d `  image:` lines, want 1: DenClaudeKit replaces that line", n)
	}
}

func TestDenClaudeKitReplacesTheImage(t *testing.T) {
	image := "r.test/c@sha256:" + strings.Repeat("0", 64)
	got := string(kit(t, image))
	if !strings.Contains(got, "\n  image: "+image+"\n") || strings.Count(got, "image:") != 1 {
		t.Errorf("image not replaced:\n%s", got)
	}
}

func TestDenClaudeKitQuotesYAMLSpecials(t *testing.T) {
	for _, img := range []string{"*x@sha256:" + strings.Repeat("0", 64), "!x@sha256:" + strings.Repeat("1", 64)} {
		var spec struct {
			Sandbox struct {
				Image string `yaml:"image"`
			} `yaml:"sandbox"`
		}
		if err := yaml.Unmarshal(kit(t, img), &spec); err != nil {
			t.Fatalf("%q: spec does not parse: %v", img, err)
		}
		if spec.Sandbox.Image != img {
			t.Errorf("image = %q, want %q", spec.Sandbox.Image, img)
		}
	}
}

func TestDenClaudeKitNeedsAnImage(t *testing.T) {
	if _, err := DenClaudeKit(""); err == nil {
		t.Error("an empty image must be refused: den writes no image of its own (A9)")
	}
	for _, l := range strings.Split(string(denClaudeSpec), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "image:") && strings.Contains(l, "sha256:") {
			t.Errorf("embedded spec pins an image: %q", l)
		}
	}
}
