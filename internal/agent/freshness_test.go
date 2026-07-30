package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

func agentClaude() config.Agent {
	return config.Agent{
		ConfigDir: "/home/me/.den/agents/claude",
		Env:       map[string]string{"CLAUDE_CONFIG_DIR": "{config_dir}"},
		BinDirs:   []string{"$HOME/.local/bin", "$HOME/.claude/local"},
		Update:    "claude update",
	}
}

// bin_dirs' $HOME targets the VM's home: it must cross den INTACT. This is
// the invariant handoff §11.1 requires locking down first.
func TestFreshnessCommandDoesNotExpandHOME(t *testing.T) {
	argv, err := FreshnessCommand("claude", agentClaude())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(argv) != 3 || argv[0] != "bash" || argv[1] != "-c" {
		t.Fatalf("expected argv [bash -c <script>], got %q", argv)
	}
	script := argv[2]
	if !strings.Contains(script, `export PATH="$HOME/.local/bin:$HOME/.claude/local:$PATH"`) {
		t.Errorf("the script must set PATH with the LITERAL bin_dirs; got:\n%s", script)
	}
	if home, _ := os.UserHomeDir(); home != "" && strings.Contains(script, home) {
		t.Errorf("the HOST home %q leaked into the script meant for the VM:\n%s", home, script)
	}
}

// The sbx dispatcher exits on the first failure: the command must be
// fail-closed, with bounded attempts to absorb egress policy propagation.
func TestFreshnessCommandIsFailClosed(t *testing.T) {
	argv, err := FreshnessCommand("claude", agentClaude())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	script := argv[2]
	for _, want := range []string{"claude update", "exit 127", "exit 0", "sleep 10"} {
		if !strings.Contains(script, want) {
			t.Errorf("the script must contain %q; got:\n%s", want, script)
		}
	}

	// "exit 1" is a substring of "exit 127", already emitted above: a Contains
	// check wouldn't bite. What matters is the position — the fail-closed exit
	// is the LAST thing the script does.
	if !strings.HasSuffix(script, "exit 1\n") {
		t.Errorf("the script must end on the fail-closed \"exit 1\"; got:\n%s", script)
	}

	// The loop must be BOUNDED: without a bound and an increment, an agent
	// whose update keeps failing would block the boot instead of failing.
	bound := fmt.Sprintf("while [ \"$attempt\" -le %d ]; do", freshnessAttempts)
	if !strings.Contains(script, bound) {
		t.Errorf("the loop must be bounded by %q; got:\n%s", bound, script)
	}
	if !strings.Contains(script, "attempt=$((attempt + 1))") {
		t.Errorf("the loop must increment the counter, or the bound bounds nothing:\n%s", script)
	}
}

func TestFreshnessCommandWithoutBinDirs(t *testing.T) {
	a := agentClaude()
	a.BinDirs = nil
	argv, err := FreshnessCommand("claude", a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(argv[2], "export PATH") {
		t.Errorf("without bin_dirs, no export PATH line must be emitted:\n%s", argv[2])
	}
}

func TestFreshnessCommandRefusesEmptyUpdate(t *testing.T) {
	a := agentClaude()
	a.Update = ""
	if _, err := FreshnessCommand("claude", a); err == nil {
		t.Fatal("an agent without an update command must be refused (spec §9.1)")
	}
}

// Golden file: regression net on the exact rendering.
func TestFreshnessCommandGolden(t *testing.T) {
	argv, err := FreshnessCommand("claude", agentClaude())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	path := filepath.Join("testdata", "freshness-claude.golden")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	if got := argv[2]; got != string(want) {
		t.Errorf("rendered != %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}
