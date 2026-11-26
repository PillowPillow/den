package nest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNamesFromAProjectListsTheProjectAndGlobalNestsOnly(t *testing.T) {
	home, proj, other := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", nestFile("g"))
	write(t, proj, "den.yaml", nestFile("api"))
	o := write(t, other, "den.yaml", nestFile("lib"))
	write(t, home, "state/v2/nests.yaml", "nests:\n  lib: "+o+"\n")
	got, err := Names(home, proj)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"api", "g"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNamesWithoutAProjectAddsTheIndexedNests(t *testing.T) {
	home, other := t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", nestFile("g"))
	o := write(t, other, "den.yaml", nestFile("lib"))
	write(t, home, "state/v2/nests.yaml", "nests:\n  lib: "+o+"\n")
	got, err := Names(home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"g", "lib"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNamesLeavesOutANestTheIndexedFileDeclaresButTheIndexDoesNot(t *testing.T) {
	home, other := t.TempDir(), t.TempDir()
	o := write(t, other, "den.yaml", nestFile("lib")+"  added:\n    template: claude-code-docker\n    repos: [{path: ./r}]\n")
	write(t, home, "state/v2/nests.yaml", "nests:\n  lib: "+o+"\n")
	got, err := Names(home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"lib"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	v, err := Visible(home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v, "nests den can see: lib (from ") {
		t.Errorf("Visible lists a nest Find refuses: %q", v)
	}
}

func TestEveryNameIsANestFindAccepts(t *testing.T) {
	home, proj, other := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", nestFile("g"))
	write(t, proj, "den.yaml", nestFile("api"))
	o := write(t, other, "den.yaml", nestFile("lib")+"  added:\n    template: claude-code-docker\n    repos: [{path: ./r}]\n")
	write(t, home, "state/v2/nests.yaml", "nests:\n  lib: "+o+"\n")
	for _, cwd := range []string{proj, t.TempDir()} {
		names, err := Names(home, cwd)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range names {
			if _, _, err := Find(home, cwd, n); err != nil {
				t.Errorf("from %s, Names offers %q but Find refuses it: %v", cwd, n, err)
			}
		}
	}
}

func TestNamesDoesNotClaim(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", nestFile("g"))
	write(t, proj, "den.yaml", nestFile("api"))
	if _, err := Names(home, proj); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(indexPath(home)); !os.IsNotExist(err) {
		t.Errorf("nests.yaml was written: %v", err)
	}
}

func TestNamesSkipsAnUnreadableOrStaleIndexedFile(t *testing.T) {
	home, other := t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", nestFile("g"))
	broken := write(t, other, "den.yaml", nestFile("bad")+"bogus: 1\n")
	stale := write(t, t.TempDir(), "den.yaml", nestFile("renamed"))
	missing := filepath.Join(other, "gone", "den.yaml")
	write(t, home, "state/v2/nests.yaml", "nests:\n  bad: "+broken+"\n  old: "+stale+"\n  gone: "+missing+"\n")
	got, err := Names(home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"g"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNamesListsANestOnceWhenTheIndexPointsAtTheProjectFile(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	p := write(t, proj, "den.yaml", nestFile("api"))
	write(t, home, "state/v2/nests.yaml", "nests:\n  api: "+p+"\n")
	got, err := Names(home, proj)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"api"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestVisibleNamesABrokenIndexedFileOnce(t *testing.T) {
	home, proj, other := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, proj, "den.yaml", nestFile("api"))
	broken := write(t, other, "den.yaml", "schema: 1\nbogus: 1\nnests:\n  a: {}\n  b: {}\n")
	write(t, home, "state/v2/nests.yaml", "nests:\n  a: "+broken+"\n  b: "+broken+"\n")
	got, err := Visible(home, proj)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, broken); n != 1 {
		t.Errorf("%s named %d times in %q", broken, n, got)
	}
}
