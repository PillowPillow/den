package base

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/instance"
	"github.com/PillowPillow/den/internal/sbx"
)

type buildFixture struct {
	home, src string
	fake      *sbx.Fake
	out, err  bytes.Buffer
}

func newBuildFixture(t *testing.T) *buildFixture {
	t.Helper()
	f := &buildFixture{home: t.TempDir(), src: t.TempDir()}
	f.fake = &sbx.Fake{Responses: map[string]sbx.Response{
		"version":   {Output: []byte("sbx version v0.53.0 abc\n")},
		"ls --json": {Output: []byte(`[]`)},
	}}
	return f
}

func (f *buildFixture) step(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(f.src, name)
	writeFile(t, p, body)
	return p
}

func (f *buildFixture) build(b config.Base) error {
	return f.buildWith(b, "")
}

func (f *buildFixture) buildWith(b config.Base, agentImage string) error {
	d := instance.Deps{Sbx: f.fake, DenHome: f.home, Cwd: f.src, Out: &f.out, Err: &f.err}
	return Build(context.Background(), d, "devx", b, agentImage)
}

func (f *buildFixture) calls() []string {
	var out []string
	for _, c := range f.fake.Calls {
		if k := strings.Join(c, " "); k != "version" && k != "ls --json" {
			out = append(out, k)
		}
	}
	return out
}

func (f *buildFixture) envFile() string {
	return filepath.Join(f.home, "gen", ".build", "devx", "sbxenv.yaml")
}

func TestBuildRefusesAnUnreadableStepBeforeCreate(t *testing.T) {
	f := newBuildFixture(t)
	missing := filepath.Join(f.src, "gone.sh")
	err := f.build(config.Base{Image: "devx:v1", Build: config.Build{Steps: []string{missing}}})
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("err = %v, want a refusal naming %s", err, missing)
	}
	if got := f.calls(); len(got) != 0 {
		t.Errorf("sbx calls %v, want none", got)
	}
	if _, statErr := os.Stat(filepath.Join(f.home, "build")); statErr == nil {
		t.Error("a file was written before the refusal")
	}
}

func TestBuildRefusesAnExistingBuildVM(t *testing.T) {
	f := newBuildFixture(t)
	f.fake.Responses["ls --json"] = sbx.Response{Output: []byte(`[{"name":"devx-build","status":"running"}]`)}
	one := f.step(t, "one.sh", "echo one\n")
	err := f.build(config.Base{Image: "devx:v1", Build: config.Build{Steps: []string{one}}})
	if err == nil || !strings.Contains(err.Error(), "sbx rm devx-build") {
		t.Fatalf("err = %v, want a refusal naming `sbx rm devx-build`", err)
	}
	if got := f.calls(); len(got) != 0 {
		t.Errorf("sbx calls %v, want none: den must not env rm a VM it did not create", got)
	}
	if _, statErr := os.Stat(f.envFile()); statErr == nil {
		t.Error("sbxenv.yaml written before the refusal")
	}
}

func TestBuildCallSequence(t *testing.T) {
	f := newBuildFixture(t)
	one := f.step(t, "one.sh", "echo one\n")
	two := f.step(t, "two.sh", "echo two\n")
	if err := f.build(config.Base{Image: "devx:v1", Build: config.Build{Steps: []string{one, two}}}); err != nil {
		t.Fatal(err)
	}
	env := f.envFile()
	want := []string{
		"--no-input env create --force --skip-host-commands " + env,
		"exec devx-build -- bash -lc echo one\n",
		"exec devx-build -- bash -lc echo two\n",
		"stop devx-build",
		"template save devx-build devx:v1",
		"--no-input env rm --force --skip-host-commands " + env,
	}
	got := f.calls()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls:\n%q\nwant:\n%q", got, want)
	}
	scratch := filepath.Join(f.home, "build", "devx")
	if st, err := os.Stat(scratch); err != nil || !st.IsDir() {
		t.Errorf("scratch workspace %s missing: %v", scratch, err)
	}
	raw, err := os.ReadFile(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{scratch, filepath.Join(f.home, "gen", ".kits", "den-claude"), filepath.Join(f.home, "gen", ".build", "devx", "network")} {
		if !strings.Contains(string(raw), w) {
			t.Errorf("sbxenv.yaml lacks %s:\n%s", w, raw)
		}
	}
	if strings.Contains(string(raw), "profiles") {
		t.Errorf("build env mounts a profile:\n%s", raw)
	}
}

func TestBuildEmptiesTheScratchWorkspace(t *testing.T) {
	f := newBuildFixture(t)
	stale := filepath.Join(f.home, "build", "devx", "stale")
	writeFile(t, stale, "x")
	one := f.step(t, "one.sh", "echo one\n")
	if err := f.build(config.Base{Image: "devx:v1", Build: config.Build{Steps: []string{one}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("stale scratch file survived")
	}
}

func TestBuildRemovesTheVMWhenAStepFails(t *testing.T) {
	f := newBuildFixture(t)
	one := f.step(t, "one.sh", "echo one\n")
	two := f.step(t, "two.sh", "echo two\n")
	boom := errors.New("exit status 3")
	f.fake.Responses["exec devx-build -- bash -lc echo two\n"] = sbx.Response{Err: boom}
	err := f.build(config.Base{Image: "devx:v1", Build: config.Build{Steps: []string{one, two}}})
	if err == nil || !errors.Is(err, boom) || !strings.Contains(err.Error(), "step 2/2 "+two) {
		t.Fatalf("err = %v, want it to name step 2/2 %s", err, two)
	}
	got := f.calls()
	if last := got[len(got)-1]; !strings.HasPrefix(last, "--no-input env rm --force --skip-host-commands") {
		t.Errorf("last call = %q, want env rm", last)
	}
	for _, c := range got {
		if strings.HasPrefix(c, "stop") || strings.HasPrefix(c, "template save") {
			t.Errorf("call %q after a failed step", c)
		}
	}
}

func TestBuildWarnsWhenEnvRmFailsAndKeepsTheStepError(t *testing.T) {
	f := newBuildFixture(t)
	one := f.step(t, "one.sh", "echo one\n")
	boom := errors.New("exit status 3")
	f.fake.Responses["exec devx-build -- bash -lc echo one\n"] = sbx.Response{Err: boom}
	f.fake.Responses["--no-input env rm --force --skip-host-commands "+f.envFile()] = sbx.Response{Err: errors.New("rm broke")}
	err := f.build(config.Base{Image: "devx:v1", Build: config.Build{Steps: []string{one}}})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the step error", err)
	}
	if !strings.Contains(f.err.String(), "warning:") || !strings.Contains(f.err.String(), "devx-build") {
		t.Errorf("stderr = %q, want a warning naming the VM", f.err.String())
	}
}

func TestBuildTemplateSaveFailureIsAnError(t *testing.T) {
	f := newBuildFixture(t)
	one := f.step(t, "one.sh", "echo one\n")
	boom := errors.New("save broke")
	f.fake.Responses["template save devx-build devx:v1"] = sbx.Response{Err: boom}
	err := f.build(config.Base{Image: "devx:v1", Build: config.Build{Steps: []string{one}}})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the save error", err)
	}
	got := f.calls()
	if !strings.HasPrefix(got[len(got)-1], "--no-input env rm") {
		t.Errorf("calls %q: env rm must still run", got)
	}
}

func TestBuildUsesFromAsTemplate(t *testing.T) {
	f := newBuildFixture(t)
	one := f.step(t, "one.sh", "echo one\n")
	if err := f.build(config.Base{Image: "devx:v1", From: "docker/sandbox-templates:claude-code", Build: config.Build{Steps: []string{one}}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(f.envFile())
	if !strings.Contains(string(raw), "docker/sandbox-templates:claude-code") {
		t.Errorf("template missing:\n%s", raw)
	}
}

func TestBuildOmitsTemplateWithoutFrom(t *testing.T) {
	f := newBuildFixture(t)
	one := f.step(t, "one.sh", "echo one\n")
	if err := f.build(config.Base{Image: "devx:v1", Build: config.Build{Steps: []string{one}}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(f.envFile())
	if strings.Contains(string(raw), "template") {
		t.Errorf("template present:\n%s", raw)
	}
}

func TestBuildGivesTheBuildNetworkOnly(t *testing.T) {
	f := newBuildFixture(t)
	one := f.step(t, "one.sh", "echo one\n")
	b := config.Base{
		Image:   "devx:v1",
		Network: config.Network{Allow: []string{"runtime.example.test"}},
		Build:   config.Build{Steps: []string{one}, Network: config.Network{Allow: []string{"build.example.test"}}},
	}
	if err := f.build(b); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(f.home, "gen", ".build", "devx", "network", "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "build.example.test") || strings.Contains(string(raw), "runtime.example.test") {
		t.Errorf("network kit:\n%s", raw)
	}
}

func TestBuildRefusesAnOldSbx(t *testing.T) {
	f := newBuildFixture(t)
	f.fake.Responses["version"] = sbx.Response{Output: []byte("sbx version v0.52.0 abc\n")}
	one := f.step(t, "one.sh", "echo one\n")
	if err := f.build(config.Base{Image: "devx:v1", Build: config.Build{Steps: []string{one}}}); err == nil {
		t.Fatal("want a refusal")
	}
	if got := f.calls(); len(got) != 0 {
		t.Errorf("calls %v", got)
	}
}

func TestBuildUsesTheConfiguredAgentImage(t *testing.T) {
	f := newBuildFixture(t)
	one := f.step(t, "one.sh", "echo one\n")
	image := "registry.test/claude@sha256:" + strings.Repeat("ab", 32)
	if err := f.buildWith(config.Base{Image: "devx:v1", Build: config.Build{Steps: []string{one}}}, image); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(f.home, "gen", ".kits", "den-claude", "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "  image: "+image+"\n") {
		t.Errorf("den-claude kit does not carry %s:\n%s", image, raw)
	}
}
