package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/v2/internal/registry"
)

func (f *upFixture) shellRecord(t *testing.T, instance, repo, start string) {
	t.Helper()
	f.record(t, registry.Record{
		Instance: instance, Nest: "am", Start: start,
		Repos: []registry.Repo{{Name: "r", Repo: repo, Mount: repo, Target: repo}},
	})
}

func (f *upFixture) shell(args ...string) error { return f.up2("shell", args...) }

func TestShellPicksTheInstanceFromCwd(t *testing.T) {
	f := newUpFixture(t)
	a, b := f.dir(t, "a"), f.dir(t, "ab")
	f.shellRecord(t, "one", a, a)
	f.shellRecord(t, "two", b, b)
	f.live("one", "two")
	f.cwd = filepath.Join(b, "sub")
	if err := f.shell(); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasAttached("exec", "-it", "-w", b, "two", "bash", "-l") {
		t.Errorf("attaches %v", f.fake.Attaches)
	}
}

func TestShellPrefersTheDeepestMatch(t *testing.T) {
	f := newUpFixture(t)
	outer := f.dir(t, "outer")
	inner := f.dir(t, "outer/inner")
	f.shellRecord(t, "one", outer, outer)
	f.shellRecord(t, "two", inner, inner)
	f.cwd = inner
	if err := f.shell(); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasAttached("exec", "-it", "-w", inner, "two") {
		t.Errorf("attaches %v", f.fake.Attaches)
	}
}

func TestShellFallsBackToTheOnlyRunningInstance(t *testing.T) {
	f := newUpFixture(t)
	a, b := f.dir(t, "a"), f.dir(t, "b")
	f.shellRecord(t, "one", a, a)
	f.shellRecord(t, "two", b, b)
	f.live("two")
	if err := f.shell(); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasAttached("exec", "-it", "-w", b, "two", "bash", "-l") {
		t.Errorf("attaches %v", f.fake.Attaches)
	}
}

func TestShellRefusesWhenAmbiguous(t *testing.T) {
	f := newUpFixture(t)
	a, b := f.dir(t, "a"), f.dir(t, "b")
	f.shellRecord(t, "one", a, a)
	f.shellRecord(t, "two", b, b)
	f.live("one", "two")
	err := f.shell()
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, w := range []string{"one", "two", "den shell <instance>"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q lacks %q", err, w)
		}
	}
	if len(f.fake.Attaches) != 0 {
		t.Errorf("attached %v", f.fake.Attaches)
	}
}

func TestShellUsesTheRecordedStart(t *testing.T) {
	f := newUpFixture(t)
	a := f.dir(t, "a")
	f.shellRecord(t, "one", a, "/vm/start")
	if err := f.shell("one"); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasAttached("exec", "-it", "-w", "/vm/start", "one", "bash", "-l") {
		t.Errorf("attaches %v", f.fake.Attaches)
	}
	if err := f.shell("one", "--workdir", "/elsewhere"); err != nil {
		t.Fatal(err)
	}
	if !f.fake.HasAttached("exec", "-it", "-w", "/elsewhere", "one") {
		t.Errorf("attaches %v", f.fake.Attaches)
	}
}

func TestShellRefusesAnInstanceWithoutARecord(t *testing.T) {
	f := newUpFixture(t)
	err := f.shell("ghost")
	if err == nil || !strings.Contains(err.Error(), "den ls") {
		t.Fatalf("err = %v, want a refusal naming `den ls`", err)
	}
	if len(f.fake.Attaches) != 0 {
		t.Errorf("attached %v", f.fake.Attaches)
	}
}
