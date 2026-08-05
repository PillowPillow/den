package nest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func repos() []Repo {
	return []Repo{
		{Path: "/dev/api"},                    // required
		{Path: "/dev/front", Optional: true},  // optional
		{Path: "/dev/worker", Optional: true}, // optional
	}
}

func names(rs []Repo) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Name())
	}
	return out
}

func TestSelectReposNominalCases(t *testing.T) {
	cases := []struct {
		name     string
		without  []string
		only     []string
		expected []string
	}{
		{"no filter: everything", nil, nil, []string{"api", "front", "worker"}},
		{"without one optional", []string{"front"}, nil, []string{"api", "worker"}},
		{"without several", []string{"front", "worker"}, nil, []string{"api"}},
		{"only one optional: required ones stay", nil, []string{"front"}, []string{"api", "front"}},
		{"only one required: optional ones drop", nil, []string{"api"}, []string{"api"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := selectRepos(repos(), c.without, c.only)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(names(got), c.expected) {
				t.Errorf("SelectRepos = %v, expected %v", names(got), c.expected)
			}
		})
	}
}

func TestSelectReposErrors(t *testing.T) {
	cases := []struct {
		name     string
		without  []string
		only     []string
		expected string
	}{
		{"without and only together", []string{"front"}, []string{"worker"}, "mutually exclusive"},
		{"without a required repo", []string{"api"}, nil, "required"},
		{"without an unknown repo", []string{"ghost"}, nil, "ghost"},
		{"only an unknown repo", nil, []string{"ghost"}, "ghost"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := selectRepos(repos(), c.without, c.only)
			if err == nil {
				t.Fatalf("expected an error mentioning %q", c.expected)
			}
			if !strings.Contains(err.Error(), c.expected) {
				t.Errorf("error = %q, expected a mention of %q", err.Error(), c.expected)
			}
		})
	}
}

func TestSelectReposDoesNotMutateInput(t *testing.T) {
	in := repos()
	if _, err := selectRepos(in, []string{"front"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(in) != 3 {
		t.Errorf("the input was mutated: %d repos instead of 3", len(in))
	}
}

func TestParseRepoArgNormalizes(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory: the tilde cases cannot be asserted")
	}
	cases := []struct {
		name     string
		cwd, raw string
		expected string
	}{
		{"absolute path travels as-is", "/work", "/dev/api", "/dev/api"},
		{"tilde is expanded", "/work", "~/dev/api", filepath.Join(home, "dev", "api")},
		{"dot is the working directory", "/work/api", ".", "/work/api"},
		{"relative path resolves against cwd", "/work", "sub/api", "/work/sub/api"},
		{"parent traversal resolves too", "/work/api", "../front", "/work/front"},
		{"redundant separators are cleaned", "/work", "/dev//api/", "/dev/api"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseRepoArg(c.cwd, c.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Path != c.expected {
				t.Errorf("Path = %q, expected %q", got.Path, c.expected)
			}
			if !got.AdHoc {
				t.Error("AdHoc = false: a repo built from the command line must carry its origin, " +
					"it is what decides which place the \"not found\" error tells the user to fix")
			}
			if got.Optional {
				t.Error("Optional = true: a repo typed on the command line was asked for explicitly, " +
					"--without/--only never address it")
			}
		})
	}
}

func TestParseRepoArgRefusesReadOnlySuffix(t *testing.T) {
	// The path EXISTS as far as this function is concerned: the point is that
	// the refusal must talk about `:ro`, not about a missing directory. sbx
	// accepts the suffix, so a user who writes it is asking for something den
	// deliberately does not do.
	cases := []struct {
		name string
		raw  string
	}{
		{"bare suffix", "/dev/api:ro"},
		// A shell-quoted argument can carry leading/trailing whitespace
		// (`den spawn scratch " /dev/api:ro "`). The refusal must still trigger: it
		// is judged on a trimmed copy of raw, not on raw itself, precisely so
		// this case is not missed.
		{"suffix padded with whitespace", " /dev/api:ro "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseRepoArg("/work", c.raw)
			if err == nil {
				t.Fatal("expected a refusal for the `:ro` suffix")
			}
			if !strings.Contains(err.Error(), ":ro") {
				t.Errorf("error = %q, expected it to name `:ro` — otherwise the user reads "+
					"\"no such path\" about a directory that exists", err)
			}
		})
	}
}

func TestParseRepoArgRefusesEmpty(t *testing.T) {
	_, err := parseRepoArg("/work", "   ")
	if err == nil {
		t.Fatal("expected a refusal for an empty path")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %q, expected it to say the path is empty", err)
	}
}

func TestParseRepoArgsRefusesAMissingWorkingDirectory(t *testing.T) {
	// A wiring defect, not a user error: nest.Options.Cwd unset while Repos is
	// not. Falling back on the process's cwd would be a silent retreat to
	// exactly the system access the parameter exists to remove, and it would
	// only show up at runtime, on the wrong path.
	_, err := parseRepoArgs("", []string{"/dev/api"})
	if err == nil {
		t.Fatal("expected a refusal when Cwd is unset")
	}
	if !strings.Contains(err.Error(), "Cwd") {
		t.Errorf("error = %q, expected it to name the unset field", err)
	}
}

func TestParseRepoArgsEmptyInputYieldsNothing(t *testing.T) {
	// The nominal case for every nest spawned without positionals: no Cwd is
	// required, and nothing is added.
	got, err := parseRepoArgs("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, expected nothing", got)
	}
}

func TestCheckUniqueNamesNamesTheCommandLineOrigin(t *testing.T) {
	err := checkUniqueNames([]Repo{
		{Path: "/tmp/scratch/api", AdHoc: true},
		{Path: "/dev/api"},
	}, "spawn")
	if err == nil {
		t.Fatal("expected a refusal: both repos have the short name \"api\"")
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("error = %q, expected it to say which of the two came from the command line — "+
			"only that one is fixable by retyping", err)
	}
	if !strings.Contains(err.Error(), "/dev/api") {
		t.Errorf("error = %q, expected it to name the declared path too", err)
	}
}

// The same path typed twice on the command line shares a basename with
// itself, so before the pre-pass existed this fell through to the "share the
// short name" message — which sends the reader hunting for a SECOND path
// that shares nothing with the one on screen, since there isn't one.
func TestCheckUniqueNamesRefusesTheSamePathTwiceOnTheCommandLine(t *testing.T) {
	err := checkUniqueNames([]Repo{
		{Path: "/dev/api", AdHoc: true},
		{Path: "/dev/api", AdHoc: true},
	}, "spawn")
	if err == nil {
		t.Fatal("expected a refusal: the same path was given twice")
	}
	if strings.Contains(err.Error(), "short name") {
		t.Errorf("error = %q, expected the SAME-PATH message, not the basename collision "+
			"it would otherwise fall through to", err)
	}
	if !strings.Contains(err.Error(), "twice") {
		t.Errorf("error = %q, expected it to say the path was given twice", err)
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("error = %q, expected it to say both occurrences came from the command line", err)
	}
}

// A command-line path can also repeat a DECLARED one — `den spawn api ~/dev/api`
// when `api` is already in repos:. Unlike the all-command-line case, this one
// has an unambiguous remedy: keep the declared entry, drop the positional.
func TestCheckUniqueNamesRefusesACommandLinePathEqualToADeclaredOne(t *testing.T) {
	err := checkUniqueNames([]Repo{
		{Path: "/dev/api", AdHoc: true},
		{Path: "/dev/api"},
	}, "spawn")
	if err == nil {
		t.Fatal("expected a refusal: the command line repeats the declared path")
	}
	if strings.Contains(err.Error(), "short name") {
		t.Errorf("error = %q, expected the SAME-PATH message, not the basename collision "+
			"it would otherwise fall through to", err)
	}
	if !strings.Contains(err.Error(), "already declared") {
		t.Errorf("error = %q, expected it to say the path is already declared", err)
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("error = %q, expected it to point at the fixable half", err)
	}
}
