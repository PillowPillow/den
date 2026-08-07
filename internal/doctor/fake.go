package doctor

import (
	"os"
	"time"

	"github.com/PillowPillow/den/internal/sshagent"
)

// FakeSSHSocket is the SSH_AUTH_SOCK value FakeDeps reports. Named because
// tests in this package and in internal/cli both assert it appears inside a
// Detail.
const FakeSSHSocket = "/tmp/den-test/agent-ssh.sock"

// FakeSSHIdentities is the identity count FakeDeps' agent reports. Named
// because the doctor test asserts it surfaces in the ssh.mode "ok" detail.
const FakeSSHIdentities = 1

// FakeGOOS is the operating system FakeDeps claims to run on, which decides
// which ssh-agent remedy the warnings quote. A value of its own rather than
// runtime.GOOS: see the field in FakeDeps.
const FakeGOOS = "linux"

// FakeDeps simulates a system where sbx is installed, every path exists, git
// is recent enough, and an SSH agent is running.
//
// It lives in this production package rather than in a `_test.go`: doctor and
// cli both need it, and the test must owe nothing to the machine running it
// — without injection, `den doctor`'s exit contract ("non-zero if a check
// fails") would turn green or red depending on the machine running the suite.
func FakeDeps() Deps {
	return Deps{
		LookPath: func(string) (string, error) { return "/usr/local/bin/sbx", nil },
		Stat:     func(string) (os.FileInfo, error) { return FakeDirInfo(), nil },
		// Injected like the other two: without it, `den doctor` would render
		// the verdict of the MACHINE's own git here, and the command's exit
		// contract would turn green or red depending on which machine runs
		// the suite.
		GitVersion: func() (string, error) { return "git version 2.43.0\n", nil },
		// Same reason, and sharper still: without injection, `den doctor`
		// would warn or not depending on whether the session running the
		// suite has an SSH agent running.
		Getenv: func(name string) string {
			if name == "SSH_AUTH_SOCK" {
				return FakeSSHSocket
			}
			return ""
		},
		// A healthy agent with one key: same reasoning as Getenv — without
		// injection the socket-present branch would query the machine's real
		// agent and turn the ssh.mode check green or amber depending on who runs
		// the suite. FakeSSHIdentities is named because tests assert the count
		// surfaces in the "ok" detail.
		SSHAgent: func() sshagent.Result {
			return sshagent.Result{State: sshagent.StateKeys, Identities: FakeSSHIdentities}
		},
		// FIXED, not runtime.GOOS: the ssh-agent warnings quote an OS-specific
		// remedy, so a fake that read the real OS would render one message on a
		// macOS laptop and another on the Linux CI — the machine dependency this
		// whole struct exists to remove. A test that wants the darwin branch sets
		// GOOS itself, which is the point of the field.
		GOOS: FakeGOOS,
	}
}

// FakeDirInfo is the os.FileInfo every FakeDeps.Stat success returns: an
// existing DIRECTORY.
//
// It exists because doctor stopped only looking at the error. `ssh.dir` and
// `mounts[].host` become `sbx create` workspaces, and den mounts directories:
// a `host:` pointing at a FILE is a plausible typo (`~/.digitaleo/config.yaml`
// instead of its directory) that must be refused with its own sentence, since
// "not found" is false for a file that exists. A double returning a nil
// FileInfo therefore panics rather than passing, which is the honest outcome —
// the alternative, tolerating nil, would make the suite blind to the very check
// it was added for.
//
// Exported: internal/cli builds Deps from this package too, and a second
// definition there would be a second thing to keep in step.
func FakeDirInfo() os.FileInfo { return fakeStatInfo{dir: true} }

// FakeFileInfo is its counterpart: a path that EXISTS and is a regular file.
// The shape a `host:` typo produces (`~/.digitaleo/config.yaml` instead of its
// directory), and the only way to exercise the refusal that tells it apart from
// a missing path.
func FakeFileInfo() os.FileInfo { return fakeStatInfo{} }

// fakeStatInfo answers "directory" or "regular file" and nothing else. Only
// IsDir is read; the rest satisfies the interface.
type fakeStatInfo struct{ dir bool }

func (i fakeStatInfo) Name() string { return "" }
func (i fakeStatInfo) Size() int64  { return 0 }
func (i fakeStatInfo) Mode() os.FileMode {
	if i.dir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func (i fakeStatInfo) ModTime() time.Time { return time.Time{} }
func (i fakeStatInfo) IsDir() bool        { return i.dir }
func (i fakeStatInfo) Sys() any           { return nil }
