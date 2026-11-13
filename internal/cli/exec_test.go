package cli

import (
	"strings"
	"testing"
)

type exitErr int

func (e exitErr) Error() string { return "exit" }
func (e exitErr) ExitCode() int { return int(e) }

func (f *upFixture) exec(args ...string) error {
	return f.up2("exec", args...)
}

func TestExecRefusesInteractive(t *testing.T) {
	f := newUpFixture(t)
	api := f.dir(t, "api")
	f.project(t, [2]string{"am", "repos: [{path: " + api + "}]"})
	err := f.exec("am", "-i", "--", "true")
	if err == nil || !strings.Contains(err.Error(), "-i cannot be used with den exec") {
		t.Fatalf("err = %v", err)
	}
	if len(f.fake.Calls) != 0 || len(f.fake.Pipes) != 0 {
		t.Errorf("sbx touched: %v %v", f.fake.Calls, f.fake.Pipes)
	}
}

func TestExecCreatesThenPipes(t *testing.T) {
	f := newUpFixture(t)
	api := f.dir(t, "api")
	f.project(t, [2]string{"am", "repos: [{path: " + api + "}]"})
	if err := f.exec("am", "--", "go", "test", "-v"); err != nil {
		t.Fatal(err)
	}
	if !f.hasEnvCreate() || !f.fake.HasPiped("--no-input", "exec", "-w", api, "am", "go", "test", "-v") {
		t.Fatalf("calls %v pipes %v", f.fake.Calls, f.fake.Pipes)
	}
	create, pipe := -1, -1
	for i, c := range f.fake.Calls {
		if strings.Contains(strings.Join(c, " "), "env create") && create < 0 {
			create = i
		}
		if strings.Contains(strings.Join(c, " "), " exec ") || (len(c) > 1 && c[1] == "exec") {
			pipe = i
		}
	}
	if create < 0 || pipe < 0 || create > pipe {
		t.Errorf("order: create %d, exec %d in %v", create, pipe, f.fake.Calls)
	}
	if strings.Contains(f.out.String(), "den") {
		t.Errorf("den wrote to stdout: %q", f.out.String())
	}
}

func TestExecOnALiveInstanceDoesNotCreate(t *testing.T) {
	f := newUpFixture(t)
	f.liveAm(t, "stale")
	if err := f.exec("am", "--workdir", "/w", "--", "ls"); err != nil {
		t.Fatal(err)
	}
	if f.hasEnvCreate() {
		t.Errorf("created a live instance: %v", f.fake.Calls)
	}
	if !f.fake.HasPiped("--no-input", "exec", "-w", "/w", "am", "ls") {
		t.Errorf("pipes %v", f.fake.Pipes)
	}
}

func TestExecPropagatesTheExitCode(t *testing.T) {
	f := newUpFixture(t)
	f.liveAm(t, "stale")
	f.fake.PipeErr = exitErr(3)
	err := f.exec("am", "--", "false")
	if code, den := ExitStatus(err); code != 3 || den {
		t.Errorf("ExitStatus = (%d, %v), want (3, false); err %v", code, den, err)
	}
	f.fake.PipeErr = exitErr(125)
	if code, den := ExitStatus(f.exec("am", "--", "false")); code != 125 || den {
		t.Errorf("sbx failure = (%d, %v)", code, den)
	}
}

func TestExecNeverPrompts(t *testing.T) {
	f := newUpFixture(t)
	api, web := f.dir(t, "api"), f.dir(t, "web")
	f.project(t, [2]string{"am", "repos: [{path: " + api + "}, {path: " + web + ", optional: true}]"})
	if err := f.exec("am", "--", "true"); err != nil {
		t.Fatal(err)
	}
	p := f.prompt
	if len(p.MultiSelects)+len(p.Confirms)+len(p.Lines) != 0 {
		t.Errorf("prompted: %+v", p)
	}
}

func TestExecRequiresACommand(t *testing.T) {
	f := newUpFixture(t)
	f.liveAm(t, "stale")
	err := f.exec("am")
	if err == nil || !strings.Contains(err.Error(), "`--` and the command") {
		t.Fatalf("err = %v", err)
	}
	if len(f.fake.Pipes) != 0 || f.hasEnvCreate() {
		t.Errorf("ran: %v %v", f.fake.Calls, f.fake.Pipes)
	}
}
