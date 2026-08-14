package spawn

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStartDir(t *testing.T) {
	cases := []struct {
		name     string
		override string
		cwd      string
		mounts   []string
		want     string
	}{{
		name:     "override wins over a cwd that matches a mount",
		override: "/dev/api",
		cwd:      "/dev/api/internal/spawn",
		mounts:   []string{"/dev/api"},
		want:     "/dev/api",
	}, {
		name:     "override is passed through verbatim, matching nothing",
		override: "/somewhere/else",
		cwd:      "/dev/api",
		mounts:   []string{"/dev/api"},
		want:     "/somewhere/else",
	}, {
		name:   "a cwd under a mount is the start directory",
		cwd:    "/dev/api/internal/spawn",
		mounts: []string{"/dev/api"},
		want:   "/dev/api/internal/spawn",
	}, {
		name:   "the deepest mount wins, not the first that matches",
		cwd:    "/dev/mono/packages/api/src",
		mounts: []string{"/dev/mono", "/dev/mono/packages/api"},
		want:   "/dev/mono/packages/api/src",
	}, {
		name:   "a cwd equal to a mount that is not the first still wins",
		cwd:    "/dev/b",
		mounts: []string{"/dev/a", "/dev/b"},
		want:   "/dev/b",
	}, {
		name:   "the prefix must end on a component boundary",
		cwd:    "/dev/api-v2/internal",
		mounts: []string{"/dev/api"},
		want:   "/dev/api",
	}, {
		name:   "a mount's :ro suffix is a mount option, not part of the path",
		cwd:    "/dev/docs/chapters",
		mounts: []string{"/dev/api", "/dev/docs:ro"},
		want:   "/dev/docs/chapters",
	}, {
		name:   "a trailing slash on a declared mount does not cost a match",
		cwd:    "/dev/api/internal",
		mounts: []string{"/dev/api/"},
		want:   "/dev/api/internal",
	}, {
		name:   "a cwd outside every mount falls back to the first one",
		cwd:    "/home/me",
		mounts: []string{"/dev/api", "/dev/docs"},
		want:   "/dev/api",
	}, {
		name:   "an unreadable cwd falls back to the first mount",
		cwd:    "",
		mounts: []string{"/dev/api"},
		want:   "/dev/api",
	}, {
		name:   "the fallback strips :ro and cleans, like the workspace it replaces",
		cwd:    "/home/me",
		mounts: []string{"/dev/api/:ro"},
		want:   "/dev/api",
	}, {
		name:   "a VM that mounts nothing gets no -w at all",
		cwd:    "/dev/api",
		mounts: nil,
		want:   "",
	}, {
		name:   "a root mount does not build a doubled separator",
		cwd:    "/dev/api",
		mounts: []string{"/"},
		want:   "/dev/api",
	}, {
		name:   "an empty mount entry matches nothing",
		cwd:    "/dev/api",
		mounts: []string{"", "/dev/api"},
		want:   "/dev/api",
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StartDir(c.override, c.cwd, c.mounts); got != c.want {
				t.Errorf("StartDir(%q, %q, %v) = %q, want %q",
					c.override, c.cwd, c.mounts, got, c.want)
			}
		})
	}
}

// TestStartDirSymlinkedCwdReturnsAPathUnderTheDeclaredMount is the darwin case
// that breaks the suite before it breaks a user: /tmp is a symlink to
// /private/tmp, so a cwd read with os.Getwd() is resolved while the mount den
// handed sbx is the declared one. The match must survive the symlink, and the
// ANSWER must stay under the declared mount — that is the only path the VM is
// known to carry.
func TestStartDirSymlinkedCwdReturnsAPathUnderTheDeclaredMount(t *testing.T) {
	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, "internal", "spawn"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "api")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// The mount is declared through the SYMLINK, the cwd arrives resolved.
	if got, want := StartDir("", filepath.Join(real, "internal", "spawn"), []string{link}),
		filepath.Join(link, "internal", "spawn"); got != want {
		t.Errorf("resolved cwd under a symlinked mount = %q, want %q", got, want)
	}
	// And the mirror image: the mount is the real path, the cwd arrives
	// through the symlink.
	if got, want := StartDir("", filepath.Join(link, "internal", "spawn"), []string{real}),
		filepath.Join(real, "internal", "spawn"); got != want {
		t.Errorf("symlinked cwd under a real mount = %q, want %q", got, want)
	}
}
