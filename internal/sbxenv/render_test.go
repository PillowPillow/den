package sbxenv

import (
	"bytes"
	"os"
	"path/filepath"
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
		Kits:      []string{"/h/.den/gen/.kits/den-claude", "/h/.den/gen/am/network"},
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
		Kits:      []string{"/h/.den/gen/.kits/den-claude", "/h/.den/kits/pillow-env", "/h/.den/gen/am.feat/network"},
		Workspace: "/h/.den/run/am.feat",
		Mounts: []Mount{
			{Path: "/h/.den/profiles/perso", Target: ProfileTarget, ReadOnly: true},
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

func TestDenClaudeKitHasNoCredentialsBlock(t *testing.T) {
	var m map[string]any
	if err := yaml.Unmarshal(DenClaudeKit(), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["credentials"]; ok {
		t.Error("den-claude must not declare credentials")
	}
}

func TestDenClaudeKitEntrypointUsesProfileTarget(t *testing.T) {
	var m struct {
		Sandbox struct {
			Entrypoint []string `yaml:"entrypoint"`
		} `yaml:"sandbox"`
	}
	if err := yaml.Unmarshal(DenClaudeKit(), &m); err != nil {
		t.Fatal(err)
	}
	for _, a := range m.Sandbox.Entrypoint {
		if a == ProfileTarget+"/settings.json" {
			return
		}
	}
	t.Errorf("entrypoint %v lacks %s/settings.json", m.Sandbox.Entrypoint, ProfileTarget)
}

func TestGitFile(t *testing.T) {
	if got := string(GitFile("/x/.git/worktrees/w")); got != "gitdir: /x/.git/worktrees/w\n" {
		t.Errorf("got %q", got)
	}
}
