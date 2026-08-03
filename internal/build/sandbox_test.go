package build

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

// A DERIVED stack starts from its parent's image, and the positional is
// `shell`: with a --template it is the image's flavor that decides the
// attached command, so `shell` promises nothing it does not keep. Same
// doctrine as sbx.PositionalAgent.
func TestCreateArgvForADerivedStack(t *testing.T) {
	s := &config.Stack{Name: "dgdevx", Image: "dgdevx:v1", Parent: "devx",
		Provision: config.Provision{Steps: []string{"/x/go.sh"}}}
	got, err := CreateArgv(s, "docker.io/library/devx:v1", "/scratch/dgdevx")
	if err != nil {
		t.Fatalf("CreateArgv: %v", err)
	}
	want := []string{"create", "--name", "dgdevx-build",
		"--template", "docker.io/library/devx:v1", "shell", "/scratch/dgdevx"}
	if !slices.Equal(got, want) {
		t.Errorf("argv =\n  %v\nwant\n  %v", got, want)
	}
}

// A ROOT stack has no --template, and THERE the positional is load-bearing:
// it selects the starting image. That is the entire reason `base:` exists.
func TestCreateArgvForARootStack(t *testing.T) {
	s := &config.Stack{Name: "devx", Image: "devx:v1", Base: "claude",
		Provision: config.Provision{Steps: []string{"/x/go.sh"}}}
	got, err := CreateArgv(s, "", "/scratch/devx")
	if err != nil {
		t.Fatalf("CreateArgv: %v", err)
	}
	want := []string{"create", "--name", "devx-build", "claude", "/scratch/devx"}
	if !slices.Equal(got, want) {
		t.Errorf("argv =\n  %v\nwant\n  %v", got, want)
	}
}

// A build sandbox gets NO mixin, NO stack kits and NO repo workspaces. It is
// thrown away at the end of the sequence, and every one of those exists to
// serve a spawn the user attaches to.
func TestCreateArgvCarriesNoSpawnMachinery(t *testing.T) {
	s := &config.Stack{Name: "devx", Image: "devx:v1", Base: "claude",
		Kit: "/k/kit", Kits: []string{"/k/known-hosts"},
		Provision: config.Provision{Steps: []string{"/x/go.sh"}}}
	got, err := CreateArgv(s, "", "/scratch/devx")
	if err != nil {
		t.Fatalf("CreateArgv: %v", err)
	}
	want := []string{"create", "--name", "devx-build", "claude", "/scratch/devx"}
	if !slices.Equal(got, want) {
		t.Errorf("argv =\n  %v\nwant\n  %v", got, want)
	}
}

// The name must survive sbx's own validation, or den would build an argv sbx
// refuses. Guarded here so a stack name legal for den but illegal as a
// sandbox component is caught before any process runs.
func TestCreateArgvRefusesAStackNameThatIsNotANameableSandbox(t *testing.T) {
	stackDir := "/home/u/.den/stacks/-weird"
	s := &config.Stack{Name: "-weird", Image: "x:v1", Base: "claude",
		Dir:       stackDir,
		Provision: config.Provision{Steps: []string{"/x/go.sh"}}}
	_, err := CreateArgv(s, "", "/scratch/x")
	if err == nil {
		t.Fatal("CreateArgv accepted a stack name that cannot be a sandbox name")
	}
	if !strings.Contains(err.Error(), stackDir) {
		t.Errorf("error does not name the stack directory: got %v, expected to contain %q", err, stackDir)
	}
}

func TestScratchDirIsUnderTheReconstructibleCache(t *testing.T) {
	got := ScratchDir("/home/u/.den", "devx")
	want := filepath.Join("/home/u/.den", "cache", "build", "devx")
	if got != want {
		t.Errorf("ScratchDir = %q, want %q", got, want)
	}
}
