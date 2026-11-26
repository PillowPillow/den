package nest

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNamesReadsProjectIndexAndGlobal(t *testing.T) {
	home, proj, other := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, home, "den.yaml", nestFile("g"))
	write(t, proj, "den.yaml", nestFile("api"))
	o := write(t, other, "den.yaml", nestFile("lib"))
	write(t, home, "state/v2/nests.yaml", "nests:\n  lib: "+o+"\n")
	got, err := Names(home, proj)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"api", "g", "lib"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
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
	home, proj, other := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, proj, "den.yaml", nestFile("api"))
	broken := write(t, other, "den.yaml", nestFile("bad")+"bogus: 1\n")
	stale := write(t, t.TempDir(), "den.yaml", nestFile("renamed"))
	missing := filepath.Join(other, "gone", "den.yaml")
	write(t, home, "state/v2/nests.yaml", "nests:\n  bad: "+broken+"\n  old: "+stale+"\n  gone: "+missing+"\n")
	got, err := Names(home, proj)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"api"}; !reflect.DeepEqual(got, want) {
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
