package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/v2/internal/registry"
)

// complete runs cobra's hidden completion command the way a shell does at <TAB>, and returns the
// candidates and the directive line that ends them.
func (f *upFixture) complete(t *testing.T, args ...string) (candidates []string, directive string) {
	t.Helper()
	if err := f.up2("__complete", args...); err != nil {
		t.Fatal(err)
	}
	// cobra prints one candidate per line, then the directive: the last line is never a candidate.
	lines := strings.Split(strings.TrimRight(f.out.String(), "\n"), "\n")
	return lines[:len(lines)-1], lines[len(lines)-1]
}

// noFileComp is ShellCompDirectiveNoFileComp as cobra prints it.
const noFileComp = ":4"

func (f *upFixture) instances(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		f.record(t, registry.Record{Instance: name, Nest: "am"})
	}
}

func TestCompletionOffersTheRecordedInstances(t *testing.T) {
	for _, command := range []string{"rm", "shell"} {
		t.Run(command, func(t *testing.T) {
			f := newUpFixture(t)
			f.instances(t, "dg.feature-1", "am")
			got, directive := f.complete(t, command, "")
			if want := []string{"am", "dg.feature-1"}; !slices.Equal(got, want) {
				t.Errorf("candidates %q, want %q", got, want)
			}
			if directive != noFileComp {
				t.Errorf("directive %q, want %q: the shell would offer file names", directive, noFileComp)
			}
		})
	}
}

func TestCompletionKeepsTheInstancesThatStartWithTheTypedText(t *testing.T) {
	f := newUpFixture(t)
	f.instances(t, "am", "dg", "dg.feature-1")
	got, _ := f.complete(t, "rm", "dg")
	if want := []string{"dg", "dg.feature-1"}; !slices.Equal(got, want) {
		t.Errorf("candidates %q, want %q", got, want)
	}
}

func TestCompletionOffersNothingAfterTheInstance(t *testing.T) {
	f := newUpFixture(t)
	f.instances(t, "am", "dg")
	got, directive := f.complete(t, "rm", "am", "")
	if len(got) != 0 || directive != noFileComp {
		t.Errorf("candidates %q, directive %q: want none and %q", got, directive, noFileComp)
	}
}

func TestCompletionOffersNothingWithoutAnInstance(t *testing.T) {
	f := newUpFixture(t)
	got, directive := f.complete(t, "rm", "")
	if len(got) != 0 || directive != noFileComp {
		t.Errorf("candidates %q, directive %q: want none and %q", got, directive, noFileComp)
	}
}

func TestCompletionSkipsAnUnreadableRecord(t *testing.T) {
	f := newUpFixture(t)
	f.instances(t, "am")
	writeTestFile(t, filepath.Join(registry.Dir(f.home), "broken.yaml"), "not: [a record")
	got, _ := f.complete(t, "rm", "")
	if want := []string{"am"}; !slices.Equal(got, want) {
		t.Errorf("candidates %q, want %q", got, want)
	}
	if strings.Contains(f.err.String(), "broken.yaml") {
		t.Errorf("a completion has nowhere to print a warning, got %q", f.err.String())
	}
}

// A completion runs at every <TAB>: it must not wait for sbx, nor fail when sbx is signed out.
func TestCompletionNeverCallsSbx(t *testing.T) {
	f := newUpFixture(t)
	f.instances(t, "am")
	f.complete(t, "shell", "")
	if len(f.fake.Calls) != 0 {
		t.Errorf("completion called sbx: %v", f.fake.Calls)
	}
}

func TestCompletionOffersTheNestsDenCanSee(t *testing.T) {
	for _, command := range []string{"up", "exec"} {
		t.Run(command, func(t *testing.T) {
			f := newUpFixture(t)
			f.project(t, [2]string{"am", "repos: [{path: /x}]"}, [2]string{"dg", "repos: [{path: /y}]"})
			writeTestFile(t, filepath.Join(f.home, "den.yaml"), "schema: 1\ndefault_template: claude-code-docker\nnests:\n  global:\n    repos: [{path: /g}]\n")
			got, directive := f.complete(t, command, "")
			if want := []string{"am", "dg", "global"}; !slices.Equal(got, want) {
				t.Errorf("candidates %q, want %q", got, want)
			}
			if directive != noFileComp {
				t.Errorf("directive %q, want %q: the shell would offer file names", directive, noFileComp)
			}
		})
	}
}

func TestCompletionKeepsTheNestsThatStartWithTheTypedText(t *testing.T) {
	f := newUpFixture(t)
	f.project(t, [2]string{"am", "repos: [{path: /x}]"}, [2]string{"dg", "repos: [{path: /y}]"}, [2]string{"dg2", "repos: [{path: /z}]"})
	got, _ := f.complete(t, "up", "dg")
	if want := []string{"dg", "dg2"}; !slices.Equal(got, want) {
		t.Errorf("candidates %q, want %q", got, want)
	}
}

func TestCompletionOffersNothingAfterTheNest(t *testing.T) {
	for _, command := range []string{"up", "exec"} {
		t.Run(command, func(t *testing.T) {
			f := newUpFixture(t)
			f.project(t, [2]string{"am", "repos: [{path: /x}]"})
			got, directive := f.complete(t, command, "am", "")
			if len(got) != 0 || directive != noFileComp {
				t.Errorf("candidates %q, directive %q: want none and %q", got, directive, noFileComp)
			}
		})
	}
}

func TestCompletionOfANestWritesNoIndex(t *testing.T) {
	f := newUpFixture(t)
	f.project(t, [2]string{"am", "repos: [{path: /x}]"})
	f.complete(t, "up", "")
	if _, err := os.Stat(filepath.Join(f.home, "state", "v2", "nests.yaml")); !os.IsNotExist(err) {
		t.Errorf("a <TAB> wrote nests.yaml: %v", err)
	}
	if len(f.fake.Calls) != 0 {
		t.Errorf("completion called sbx: %v", f.fake.Calls)
	}
}

func TestCompletionOfANestOffersNothingOnABrokenProjectFile(t *testing.T) {
	f := newUpFixture(t)
	writeTestFile(t, filepath.Join(f.proj, "den.yaml"), "schema: 1\nbogus: 1\n")
	got, directive := f.complete(t, "up", "")
	if len(got) != 0 || directive != noFileComp {
		t.Errorf("candidates %q, directive %q: want none and %q", got, directive, noFileComp)
	}
	if strings.Contains(f.err.String(), "bogus") {
		t.Errorf("a completion has nowhere to print a warning, got %q", f.err.String())
	}
}

func TestCompletionOffersNoFileForATemplate(t *testing.T) {
	f := newUpFixture(t)
	got, directive := f.complete(t, "build", "")
	if len(got) != 0 || directive != noFileComp {
		t.Errorf("candidates %q, directive %q: want none and %q", got, directive, noFileComp)
	}
}

func TestCompletionOffersNoFileForTheCommandOfDenExec(t *testing.T) {
	f := newUpFixture(t)
	_, directive := f.complete(t, "exec", "am", "--", "")
	if directive != noFileComp {
		t.Errorf("directive %q, want %q: the command runs in the VM", directive, noFileComp)
	}
}

func TestCompletionOffersNoFileForAVMWorkdir(t *testing.T) {
	for _, command := range []string{"up", "exec", "shell"} {
		t.Run(command, func(t *testing.T) {
			f := newUpFixture(t)
			_, directive := f.complete(t, command, "--workdir", "")
			if directive != noFileComp {
				t.Errorf("directive %q, want %q: --workdir is a VM path", directive, noFileComp)
			}
		})
	}
}

func TestCompletionKeepsTheHostFilesForRepo(t *testing.T) {
	for _, command := range []string{"up", "exec"} {
		t.Run(command, func(t *testing.T) {
			f := newUpFixture(t)
			_, directive := f.complete(t, command, "--repo", "")
			if directive != ":0" {
				t.Errorf("directive %q, want %q: --repo is a host folder", directive, ":0")
			}
		})
	}
}
