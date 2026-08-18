package selfupdate

import "testing"

func TestClassify(t *testing.T) {
	// The macOS Caskroom path is the one MEASURED on 2026-08-18
	// (/opt/homebrew/bin/den -> /opt/homebrew/Caskroom/den/1.8.1/den); the
	// others are documented defaults, which is why HOMEBREW_PREFIX and
	// HOMEBREW_CELLAR are consulted first — see the spec §4.
	env := Env{Gopath: "/Users/dev/go", Home: "/Users/dev"}
	cases := []struct {
		name string
		path string
		env  Env
		want Method
	}{
		{"caskroom macos", "/opt/homebrew/Caskroom/den/1.8.1/den", env, MethodHomebrew},
		{"cellar intel", "/usr/local/Cellar/den/1.8.1/bin/den", env, MethodHomebrew},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/Caskroom/den/1.8.1/den", env, MethodHomebrew},
		{"custom prefix via env", "/srv/brew/Caskroom/den/1.8.1/den",
			Env{HomebrewPrefix: "/srv/brew"}, MethodHomebrew},
		{"custom cellar via env", "/srv/kegs/den/1.8.1/bin/den",
			Env{HomebrewCellar: "/srv/kegs"}, MethodHomebrew},
		{"false positive guarded", "/Users/dev/MyCellar/den", env, MethodArchive},
		{"gobin wins over gopath", "/opt/bin/den",
			Env{Gobin: "/opt/bin", Gopath: "/Users/dev/go"}, MethodGoInstall},
		{"gopath bin", "/Users/dev/go/bin/den", env, MethodGoInstall},
		{"default go bin", "/Users/dev/go/bin/den", Env{Home: "/Users/dev"}, MethodGoInstall},
		{"local bin", "/Users/dev/.local/bin/den", env, MethodArchive},
		{"exotic install dir", "/srv/tools/den", env, MethodArchive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.path, c.env); got != c.want {
				t.Fatalf("Classify(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

func TestIsUpdatableVersion(t *testing.T) {
	// The three refused strings are the ones MEASURED against x/mod/semver on
	// 2026-08-18: `v1.5.0-17-g0ec48d8-dirty` is VALID semver and compares BELOW
	// v1.8.1, so a plain semver comparison would overwrite a local build.
	cases := []struct {
		version string
		want    bool
	}{
		{"v1.8.1", true},
		{"v0.1.0", true},
		{"dev", false},
		{"v1.5.0-17-g0ec48d8-dirty", false},
		{"v1.8.1-dirty", false},
		{"v1.9.0-rc1", false},
		{"v1.8.1+build5", false},
		{"", false},
		{"1.8.1", false},
	}
	for _, c := range cases {
		if got := IsUpdatableVersion(c.version); got != c.want {
			t.Errorf("IsUpdatableVersion(%q) = %v, want %v", c.version, got, c.want)
		}
	}
}
