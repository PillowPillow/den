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
	spec, err := DenClaudeKit(image, true)
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

type denClaudeSpecShape struct {
	Sandbox struct {
		Entrypoint []string `yaml:"entrypoint"`
	} `yaml:"sandbox"`
	Environment struct {
		Variables map[string]string `yaml:"variables"`
	} `yaml:"environment"`
	Setup struct {
		Install []any `yaml:"install"`
		Files   []any `yaml:"files"`
		Startup []struct {
			Command []string `yaml:"command"`
		} `yaml:"startup"`
	} `yaml:"setup"`
}

func decodeDenClaude(t *testing.T) denClaudeSpecShape {
	t.Helper()
	var m denClaudeSpecShape
	if err := yaml.Unmarshal(kit(t, kitImage), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDenClaudeKitHasNoSettingsFlag(t *testing.T) {
	if got, want := decodeDenClaude(t).Sandbox.Entrypoint, []string{"claude", "--dangerously-skip-permissions"}; !reflect.DeepEqual(got, want) {
		t.Errorf("entrypoint %q, want %q", got, want)
	}
}

func TestDenClaudeKitMarksClaudeSandboxedSoNoFolderAsksForTrust(t *testing.T) {
	if got := decodeDenClaude(t).Environment.Variables; !reflect.DeepEqual(got, map[string]string{"CLAUDE_CODE_SANDBOXED": "1"}) {
		t.Errorf("environment variables %v, want only CLAUDE_CODE_SANDBOXED=1", got)
	}
}

func TestDenClaudeKitStartupMergesOnboardingAndBypassConsent(t *testing.T) {
	m := decodeDenClaude(t)
	if len(m.Setup.Install) != 0 || len(m.Setup.Files) != 0 {
		t.Errorf("den-claude declares install %v or files %v, want only a startup step", m.Setup.Install, m.Setup.Files)
	}
	if len(m.Setup.Startup) != 1 {
		t.Fatalf("startup has %d steps, want 1", len(m.Setup.Startup))
	}
	cmd := m.Setup.Startup[0].Command
	if len(cmd) != 3 || cmd[0] != "sh" || cmd[1] != "-c" {
		t.Fatalf("startup command %q, want sh -c <script>", cmd)
	}
	script := cmd[2]
	for _, frag := range []string{
		"command -v jq",
		"umask 077",
		`merge "$h/.claude.json" '.hasCompletedOnboarding = true'`,
		`merge "$h/.claude/settings.json" '.skipDangerousModePermissionPrompt = true'`,
	} {
		if !strings.Contains(script, frag) {
			t.Errorf("startup script lacks %q:\n%s", frag, script)
		}
	}
}

func TestDenClaudeKitStartupTrustsOnlyTheFoldersDenLists(t *testing.T) {
	script := decodeDenClaude(t).Setup.Startup[0].Command[2]
	for _, frag := range []string{
		`trusted=$(jq -ce 'if type == "array" then . else error("not an array") end' "$h/.den/trust.json")`,
		`merge "$h/.claude.json" --argjson trusted "$trusted" 'reduce $trusted[] as $p (.; .projects[$p].hasTrustDialogAccepted = true)'`,
	} {
		if !strings.Contains(script, frag) {
			t.Errorf("startup script lacks %q:\n%s", frag, script)
		}
	}
	if strings.Contains(script, `projects["/"]`) || strings.Contains(script, `projects."/"`) {
		t.Errorf("startup script trusts the root folder:\n%s", script)
	}
}

func TestDenClaudeKitStartupLinksClaudeProjectsToTheSessionsMount(t *testing.T) {
	script := decodeDenClaude(t).Setup.Startup[0].Command[2]
	for _, frag := range []string{
		`if [ -d ` + SessionsVMPath + ` ] && [ ! -L "$h/.claude/projects" ]; then`,
		`if [ ! -e "` + SessionsVMPath + `/$f" ] && [ ! -L "` + SessionsVMPath + `/$f" ]; then`,
		`cp -a "$f" "` + SessionsVMPath + `/$f"` + "\n",
		"find . ! -type d -exec sh -c 'set -e\n",
		`cd "$h/.claude/projects" &&` + "\n",
		`) || { echo "den-claude: cannot copy the image's ~/.claude/projects into ` + SessionsVMPath + `: `,
		`ln -s ` + SessionsVMPath + ` "$h/.claude/projects"`,
	} {
		if !strings.Contains(script, frag) {
			t.Errorf("startup script lacks %q:\n%s", frag, script)
		}
	}
	if strings.Contains(script, `cp -a "$h/.claude/projects/." `) {
		t.Errorf("a copy of the whole folder puts the image's files back over the ones an earlier VM wrote:\n%s", script)
	}
	if strings.Contains(script, `sh {} + &&`) || strings.Contains(script, ") &&\n") {
		t.Errorf("a failed copy must stop the script under set -e, and an && list would not:\n%s", script)
	}
}

func TestDenClaudeKitWithoutAutoTrustIsTheKitWithoutItsEnvironment(t *testing.T) {
	off, err := DenClaudeKit(kitImage, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(off), "CLAUDE_CODE_SANDBOXED") {
		t.Errorf("kit without auto-trust still names CLAUDE_CODE_SANDBOXED:\n%s", off)
	}
	var got, want map[string]any
	if err := yaml.Unmarshal(off, &got); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(kit(t, kitImage), &want); err != nil {
		t.Fatal(err)
	}
	if _, ok := want["environment"]; !ok {
		t.Fatal("the kit has no environment block to drop")
	}
	delete(want, "environment")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kit without auto-trust %v, want the kit without environment %v", got, want)
	}
}

func TestRenderTrustJSON(t *testing.T) {
	if got := string(RenderTrustJSON(nil)); got != "[]\n" {
		t.Errorf("no folder renders %q, want []", got)
	}
	if got, want := string(RenderTrustJSON([]string{"/a b", `/c"d`})), "[\"/a b\",\"/c\\\"d\"]\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDenClaudeKitStartupMergesSettingsDefaultsUnderSettings(t *testing.T) {
	m := decodeDenClaude(t)
	if len(m.Setup.Startup) != 1 || len(m.Setup.Startup[0].Command) != 3 {
		t.Fatalf("startup %v, want one sh -c <script> step", m.Setup.Startup)
	}
	script := m.Setup.Startup[0].Command[2]
	for _, frag := range []string{
		`set -- "$h/.den/claude-settings.d"/*.json`,
		`jq -e 'type == "object"' "$f"`,
		"is not a JSON object: fix the kit that ships it",
		`jq -s 'reduce .[] as $x ({}; . * $x)' "$@"`,
		`merge "$h/.claude/settings.json" --argjson defaults "$defaults" '$defaults * .'`,
	} {
		if !strings.Contains(script, frag) {
			t.Errorf("startup script lacks %q:\n%s", frag, script)
		}
	}
}

func TestDenClaudeBuildKitIsTheInstanceKitWithoutSetup(t *testing.T) {
	build, err := DenClaudeBuildKit(kitImage)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	if err := yaml.Unmarshal(build, &got); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(kit(t, kitImage), &want); err != nil {
		t.Fatal(err)
	}
	if _, ok := want["setup"]; !ok {
		t.Fatal("the instance kit has no setup block to drop")
	}
	delete(want, "setup")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("build kit %v, want the instance kit without setup %v", got, want)
	}
	if !strings.HasSuffix(string(build), "\"1\"\n") || strings.HasSuffix(string(build), "\n\n") {
		t.Errorf("build kit must end with its last line and one newline:\n%q", build[len(build)-20:])
	}
}

func TestDenClaudeBuildKitNeedsAnImage(t *testing.T) {
	if _, err := DenClaudeBuildKit(""); err == nil {
		t.Error("an empty image must be refused")
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
	if _, err := DenClaudeKit("", true); err == nil {
		t.Error("an empty image must be refused: den writes no image of its own (A9)")
	}
	for _, l := range strings.Split(string(denClaudeSpec), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "image:") && strings.Contains(l, "sha256:") {
			t.Errorf("embedded spec pins an image: %q", l)
		}
	}
}

func TestRenderWritesResourcesUnderSandboxOptions(t *testing.T) {
	got, err := Render(Env{Name: "a", Workspace: "/w", Template: "t:v2", CPUs: 4, Memory: "8g"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "sandboxOptions:\n  template: t:v2\n  cpus: 4\n  memory: 8g\n"; !strings.Contains(string(got), want) {
		t.Errorf("rendered:\n%s\nlacks:\n%s", got, want)
	}
}

func TestRenderWritesResourcesWithoutATemplate(t *testing.T) {
	got, err := Render(Env{Name: "a", Workspace: "/w", CPUs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := "sandboxOptions:\n  cpus: 2\n"; !strings.Contains(string(got), want) {
		t.Errorf("rendered:\n%s\nlacks:\n%s", got, want)
	}
	for _, k := range []string{"memory:", "template:"} {
		if strings.Contains(string(got), k) {
			t.Errorf("unset %s rendered:\n%s", k, got)
		}
	}
}
