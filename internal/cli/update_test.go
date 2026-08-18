package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubFetcher answers nothing: these tests only reach the refusal paths, which
// happen before any request. The happy path is covered in internal/selfupdate,
// against its own fake.
type stubFetcher struct{ called bool }

func (s *stubFetcher) ResolveLatest(context.Context) (string, error) {
	s.called = true
	return "", errors.New("the command must not reach the network here")
}

func (s *stubFetcher) Get(context.Context, string) ([]byte, error) {
	s.called = true
	return nil, errors.New("the command must not reach the network here")
}

func TestUpdateRefusesAHomebrewInstall(t *testing.T) {
	f := &stubFetcher{}
	deps := Deps{
		Updater:    f,
		DenVersion: func() string { return "v1.8.0" },
		Executable: func() (string, error) { return "/opt/homebrew/Caskroom/den/1.8.0/den", nil },
	}
	cmd := NewRootCmdWith(deps)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"update"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(err.Error(), "brew upgrade --cask den") {
		t.Fatalf("the refusal must name the brew command, got %q", err)
	}
	if f.called {
		t.Fatal("the command reached the network before refusing")
	}
}

func TestUpdateRefusesALocalBuild(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "den")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	deps := Deps{
		Updater:    &stubFetcher{},
		DenVersion: func() string { return "v1.5.0-17-g0ec48d8-dirty" },
		Executable: func() (string, error) { return target, nil },
	}
	cmd := NewRootCmdWith(deps)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"update"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not a released build") {
		t.Fatalf("want the non-release refusal, got %v", err)
	}
}

func TestUpdateTakesNoArguments(t *testing.T) {
	cmd := NewRootCmdWith(Deps{Updater: &stubFetcher{}})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"update", "v1.9.0"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("`den update` takes no arguments — pinning is install.sh's job")
	}
}
