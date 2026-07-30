package doctor

import "os"

// FakeSSHSocket is the SSH_AUTH_SOCK value FakeDeps reports. Named because
// tests in this package and in internal/cli both assert it appears inside a
// Detail.
const FakeSSHSocket = "/tmp/den-test/agent-ssh.sock"

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
		// doctor only looks at the error, never the FileInfo.
		Stat: func(string) (os.FileInfo, error) { return nil, nil },
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
	}
}
