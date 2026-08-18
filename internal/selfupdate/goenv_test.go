package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeEnv answers from a map, so no test here reads the developer's own shell.
func fakeEnv(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestGoEnvPath(t *testing.T) {
	cases := []struct {
		name string
		vars map[string]string
		goos string
		want string
	}{
		// $GOENV wins over every default, and the literal `off` means the
		// toolchain reads no file at all — den must not read one either, or it
		// would classify on settings the toolchain is ignoring.
		{"goenv wins", map[string]string{"GOENV": "/etc/go/env", "HOME": "/Users/dev"}, "darwin", "/etc/go/env"},
		{"goenv off reads nothing", map[string]string{"GOENV": "off", "HOME": "/Users/dev"}, "darwin", ""},
		{"darwin default", map[string]string{"HOME": "/Users/dev"}, "darwin",
			"/Users/dev/Library/Application Support/go/env"},
		{"linux default", map[string]string{"HOME": "/home/dev"}, "linux", "/home/dev/.config/go/env"},
		{"linux honours XDG_CONFIG_HOME", map[string]string{"HOME": "/home/dev", "XDG_CONFIG_HOME": "/cfg"},
			"linux", "/cfg/go/env"},
		{"windows default", map[string]string{"AppData": `C:\Users\dev\AppData\Roaming`}, "windows",
			filepath.Join(`C:\Users\dev\AppData\Roaming`, "go", "env")},
		// No HOME is not a failure: it means den cannot name the file, so it
		// reads none and classifies on the process environment alone.
		{"no home, no path", map[string]string{}, "linux", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := GoEnvPath(fakeEnv(c.vars), c.goos); got != c.want {
				t.Fatalf("GoEnvPath = %q, want %q", got, c.want)
			}
		})
	}
	if got := GoEnvPath(nil, "linux"); got != "" {
		t.Fatalf("a nil getenv must name no file, got %q", got)
	}
}

func TestParseGoEnv(t *testing.T) {
	// The shape `go env -w` actually writes, plus the two traps: a value
	// containing `=` must survive whole (only the FIRST separator splits), and
	// a key den does not care about must not disturb the two it does.
	content := "GOTOOLCHAIN=auto\n" +
		"GOBIN=/Users/dev/bin\n" +
		"# a comment\n" +
		"\n" +
		"GOPATH=/Users/dev/go\n" +
		"GOFLAGS=-ldflags=-s\n"
	gobin, gopath := ParseGoEnv(content)
	if gobin != "/Users/dev/bin" {
		t.Fatalf("GOBIN = %q", gobin)
	}
	if gopath != "/Users/dev/go" {
		t.Fatalf("GOPATH = %q", gopath)
	}
	if g, p := ParseGoEnv(""); g != "" || p != "" {
		t.Fatalf("an empty file must answer empty strings, got %q/%q", g, p)
	}
	if g, _ := ParseGoEnv("GOBIN=/a=b/bin\n"); g != "/a=b/bin" {
		t.Fatalf("a value containing = was truncated: %q", g)
	}
}

func TestEnvFromOSReadsTheGoEnvFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "env")
	if err := os.WriteFile(file, []byte("GOBIN=/Users/dev/bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := EnvFromOS(fakeEnv(map[string]string{"GOENV": file, "HOME": "/Users/dev"}))
	if env.GoenvGobin != "/Users/dev/bin" {
		t.Fatalf("GoenvGobin = %q, want /Users/dev/bin", env.GoenvGobin)
	}
	// The whole point: this binary now classifies MethodGoInstall, where before
	// goenv.go it classified MethodArchive and den overwrote it.
	if got := Classify("/Users/dev/bin/den", env, nil); got != MethodGoInstall {
		t.Fatalf("Classify = %v, want MethodGoInstall", got)
	}
}

func TestEnvFromOSSurvivesAnUnreadableGoEnvFile(t *testing.T) {
	// An unreadable file is a return to the pre-goenv.go behaviour, never a
	// refusal: this file only ever widens the set of refused directories, so
	// failing to read it must not fail an update the user asked for.
	env := EnvFromOS(fakeEnv(map[string]string{
		"GOENV": filepath.Join(t.TempDir(), "absent"),
		"HOME":  "/Users/dev",
		"GOBIN": "/opt/bin",
	}))
	if env.GoenvGobin != "" || env.GoenvGopath != "" {
		t.Fatalf("a missing file must answer empty strings, got %q/%q", env.GoenvGobin, env.GoenvGopath)
	}
	if env.Gobin != "/opt/bin" {
		t.Fatalf("the process environment must still be read, Gobin = %q", env.Gobin)
	}
}

func TestEnvFromOSWithoutAGetenvReadsNothing(t *testing.T) {
	if env := EnvFromOS(nil); env != (Env{}) {
		t.Fatalf("a nil getenv must answer an empty Env, got %+v", env)
	}
}
