package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/nest"
)

func TestLinkCommandIsNilWithoutLinks(t *testing.T) {
	// nil, not an empty script: RenderMixin uses this to decide whether to emit
	// a startup entry at all, and an entry running `true` would appear in every
	// golden and every drift comparison for nothing.
	if got := LinkCommand(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	// A mount with no Link is legitimate (env-var consumers) and still produces
	// no link phase.
	if got := LinkCommand([]nest.Mount{{Host: "/host/a", Key: "mounts[0]"}}); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestLinkCommandQuotesHostAndLinkDIFFERENTLY(t *testing.T) {
	// The asymmetry IS the feature, and it is the single most dangerous line to
	// get wrong, so it gets a dedicated test rather than only a golden:
	//   - Host is SINGLE-quoted: den already expanded it, and the VM must not
	//     touch it. A host directory literally named `$HOME` must survive.
	//   - Link is DOUBLE-quoted: $HOME must expand IN THE VM, to /home/agent.
	got := strings.Join(LinkCommand([]nest.Mount{
		{Host: "/host/$HOME dir", Link: "$HOME/.ssh", Key: "ssh.dir"},
	}), "\n")
	if !strings.Contains(got, `'/host/$HOME dir'`) {
		t.Errorf("host must be single-quoted verbatim:\n%s", got)
	}
	if !strings.Contains(got, `"$HOME/.ssh"`) {
		t.Errorf("link must be double-quoted so the VM expands it:\n%s", got)
	}
}

func TestLinkCommandGolden(t *testing.T) {
	got := LinkCommand([]nest.Mount{
		{Host: "/home/me/.digitaleo", Link: "$HOME/.digitaleo", Key: "mounts[0]"},
		{Host: "/home/me/.ssh_sbx", Link: "$HOME/.ssh", Key: "ssh.dir"},
	})
	if len(got) != 3 || got[0] != "bash" || got[1] != "-c" {
		t.Fatalf("argv shape: got %v, want [bash -c <script>]", got)
	}
	path := filepath.Join("testdata", "links-ssh.golden")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	if got[2] != string(want) {
		t.Errorf("rendered != %s\n--- got ---\n%s\n--- want ---\n%s", path, got[2], want)
	}
}

// LinkPhaseMarker is what ParseKitLog reads back out of the dispatcher journal
// to tell a refused LINK phase from a failed freshness command — a distinction
// position cannot make, since a refused link phase aborts the run before the
// freshness command is ever announced. If den_link ever stopped printing it on
// one of its branches, the gate would silently start sending users to the agent
// registry for a bad `mounts:` entry, and no other test would notice.
//
// So: every line den_link can print must carry the marker, and the rendered
// script must too.
func TestEveryLinkPhaseOutputCarriesLinkPhaseMarker(t *testing.T) {
	for _, line := range strings.Split(linkFunc, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "echo ") {
			continue
		}
		if !strings.Contains(trimmed, LinkPhaseMarker) {
			t.Errorf("den_link prints a line the gate cannot attribute to the link phase:\n  %s", trimmed)
		}
	}
	got := strings.Join(LinkCommand([]nest.Mount{
		{Host: "/host/a", Link: "$HOME/.aws", Key: "mounts[0]"},
	}), "\n")
	if !strings.Contains(got, LinkPhaseMarker) {
		t.Errorf("the rendered link phase must carry %q:\n%s", LinkPhaseMarker, got)
	}
}

func TestLinkCommandRewritesTildeToHOME(t *testing.T) {
	// bash does not expand `~` inside double quotes, and the link must stay
	// double-quoted for `$HOME` to expand. Emitting a bare `~` would create a
	// directory literally named `~` in the VM.
	got := strings.Join(LinkCommand([]nest.Mount{
		{Host: "/host/a", Link: "~/.aws", Key: "mounts[0]"},
	}), "\n")
	if !strings.Contains(got, `"$HOME/.aws"`) {
		t.Errorf("a `~/` link must be emitted as $HOME/:\n%s", got)
	}
	if strings.Contains(got, "~") {
		t.Errorf("no literal tilde may reach the VM:\n%s", got)
	}

	// config.LoadGlobal already trims at load, but a `nest.Mount` built
	// directly (as here) bypasses that — the filter loop and the emission must
	// still agree on one, trimmed value.
	got = strings.Join(LinkCommand([]nest.Mount{
		{Host: "/host/a", Link: "  ~/.aws", Key: "mounts[0]"},
	}), "\n")
	if !strings.Contains(got, `"$HOME/.aws"`) {
		t.Errorf("a padded `~/` link must still be rewritten to $HOME/:\n%s", got)
	}
}
