package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// depsOK simule un système où sbx est installé et tous les chemins existent.
func depsOK() Deps {
	return Deps{
		LookPath: func(string) (string, error) { return "/usr/local/bin/sbx", nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
}

func denHomeValide(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ecris := func(rel, contenu string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(contenu), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ecris("config.yaml", `
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`)
	ecris("stacks/devx/stack.yaml", "image: devx:v1\n")
	ecris("nests/api.yaml", "stack: devx\nrepos:\n  - { path: /dev/api }\n")
	return dir
}

func trouve(checks []Check, fragment string) (Check, bool) {
	for _, c := range checks {
		if strings.Contains(c.Nom, fragment) || strings.Contains(c.Detail, fragment) {
			return c, true
		}
	}
	return Check{}, false
}

func tousOK(checks []Check) bool {
	for _, c := range checks {
		if !c.OK {
			return false
		}
	}
	return true
}

func TestRunConfigSaine(t *testing.T) {
	checks := Run(denHomeValide(t), depsOK())
	if len(checks) == 0 {
		t.Fatal("aucun check exécuté")
	}
	if !tousOK(checks) {
		t.Errorf("attendu tous les checks OK, obtenu %+v", checks)
	}
}

func TestRunSbxAbsent(t *testing.T) {
	d := depsOK()
	d.LookPath = func(string) (string, error) { return "", errors.New("introuvable") }
	checks := Run(denHomeValide(t), d)
	c, ok := trouve(checks, "sbx")
	if !ok {
		t.Fatal("aucun check ne concerne sbx")
	}
	if c.OK {
		t.Error("le check sbx devrait échouer quand le binaire est absent")
	}
	if tousOK(checks) {
		t.Error("Run ne doit pas rapporter tout-OK quand sbx manque")
	}
}

func TestRunConfigAbsente(t *testing.T) {
	checks := Run(t.TempDir(), depsOK())
	if tousOK(checks) {
		t.Error("attendu un échec quand config.yaml est absent")
	}
	if _, ok := trouve(checks, "config.yaml"); !ok {
		t.Error("le check en échec devrait nommer config.yaml")
	}
}

func TestRunStackParDefautInconnue(t *testing.T) {
	dir := denHomeValide(t)
	// on supprime la stack devx référencée par defaults.stack
	if err := os.RemoveAll(filepath.Join(dir, "stacks", "devx")); err != nil {
		t.Fatal(err)
	}
	checks := Run(dir, depsOK())
	if tousOK(checks) {
		t.Error("attendu un échec quand defaults.stack n'existe pas")
	}
	if _, ok := trouve(checks, "devx"); !ok {
		t.Error("le check en échec devrait nommer la stack manquante")
	}
}

func TestRunRepoDeNestIntrouvable(t *testing.T) {
	d := depsOK()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/dev/api" {
			return nil, errors.New("introuvable")
		}
		return nil, nil
	}
	checks := Run(denHomeValide(t), d)
	if tousOK(checks) {
		t.Error("attendu un échec quand un repo de nest n'existe pas")
	}
	if _, ok := trouve(checks, "/dev/api"); !ok {
		t.Error("le check en échec devrait nommer le repo manquant")
	}
}

func TestRunAgentSansCommandeUpdate(t *testing.T) {
	dir := t.TempDir()
	contenu := "agents:\n  claude:\n    config_dir: /tmp/c\ndefaults:\n  agent: claude\n  stack: devx\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
	checks := Run(dir, depsOK())
	if _, ok := trouve(checks, "update"); !ok {
		t.Error("un agent sans commande update doit être signalé (spec §9.1)")
	}
}
