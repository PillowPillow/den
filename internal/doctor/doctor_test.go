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

// trouveNom cherche un check par nom exact : contrairement à trouve, une
// sous-chaîne ne suffit pas ("config" ne doit pas être satisfait par
// "config.yaml").
func trouveNom(checks []Check, nom string) bool {
	for _, c := range checks {
		if c.Nom == nom {
			return true
		}
	}
	return false
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
	// tousOK seul passerait avec un unique check trivial : on vérifie, par nom
	// exact (trouve ferait un faux positif : "config" est une sous-chaîne de
	// "config.yaml"), que chaque diagnostic attendu est bien produit.
	for _, nom := range []string{"sbx", "config.yaml", "config", "stacks", "defaults.stack", "nests"} {
		if !trouveNom(checks, nom) {
			t.Errorf("aucun check nommé %q, obtenu %+v", nom, checks)
		}
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

// TestRunSignaleUnNestCasseSansMasquerLesAutres verrouille la dette de la
// tâche 16 : un nest illisible ne doit ni masquer la section nests de doctor,
// ni empêcher le diagnostic RÉEL des autres nests. Le nest "sain" pointe vers
// un repo absent avec un Stat truqué qui ne rate QUE sur ce chemin précis :
// avec un Deps toujours-OK (depsOK), un nest sans anomalie ne produit aucun
// check individuel, et sa seule présence dans la liste ne prouverait rien —
// il faut une vraie anomalie détectée pour prouver qu'il a été diagnostiqué,
// pas seulement listé.
func TestRunSignaleUnNestCasseSansMasquerLesAutres(t *testing.T) {
	dir := denHomeValide(t) // config valide, stack devx, nest "api"
	if err := os.WriteFile(filepath.Join(dir, "nests", "sain.yaml"),
		[]byte("stack: devx\nrepos:\n  - { path: /dev/sain-manquant }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nests", "casse.yaml"), []byte("egres: [x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := depsOK()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/dev/sain-manquant" {
			return nil, errors.New("introuvable")
		}
		return nil, nil
	}

	checks := Run(dir, d)

	var vuCasse, vuSainDiagnostique, vuNestsEnEchec bool
	for _, c := range checks {
		if c.Nom == "nest casse" && !c.OK {
			vuCasse = true
		}
		if c.Nom == "nest sain" && !c.OK && strings.Contains(c.Detail, "/dev/sain-manquant") {
			vuSainDiagnostique = true
		}
		if c.Nom == "nests" && !c.OK {
			vuNestsEnEchec = true
		}
	}
	if !vuCasse {
		t.Errorf("le nest cassé doit être signalé en échec ; checks : %+v", checks)
	}
	if !vuSainDiagnostique {
		t.Errorf("le nest sain doit rester réellement diagnostiqué (pas seulement listé) ; checks : %+v", checks)
	}
	if !vuNestsEnEchec {
		t.Errorf("le check récapitulatif 'nests' doit être en échec quand il y a des cassés ; checks : %+v", checks)
	}
}

// Depuis D1, config.LoadGlobal REFUSE une configuration incohérente. Si doctor
// passait par lui, il s'arrêterait au chargement (doctor.go rend `checks` dès
// que le chargement échoue) et n'atteindrait plus jamais sa propre validation :
// l'utilisateur ne verrait qu'une ligne d'erreur au lieu de la liste complète,
// et plus rien des stacks ni des nests. Ce test verrouille le contraire —
// doctor doit charger SANS valider, cumuler toutes les fautes, et continuer.
func TestRunCumuleLesErreursDeConfigEtContinue(t *testing.T) {
	dir := denHomeValide(t)
	// Deux fautes indépendantes dans un den home par ailleurs complet (stack
	// devx et nest api présents) : leur diagnostic à tous deux prouve que Run a
	// dépassé la config.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: nfs
worktree_layout: centrl
`), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := Run(dir, depsOK())

	for _, attendu := range []string{"nfs", "centrl"} {
		if _, ok := trouve(checks, attendu); !ok {
			t.Errorf("aucun check ne mentionne %q : doctor doit montrer TOUTES les fautes d'un coup ; checks : %+v",
				attendu, checks)
		}
	}
	// Et il doit avoir continué au-delà de la config : sans ça, un chargement
	// validant aurait tronqué le diagnostic sans qu'aucune assertion ne bouge.
	for _, nom := range []string{"stacks", "defaults.stack", "nests"} {
		if !trouveNom(checks, nom) {
			t.Errorf("aucun check nommé %q : une config fautive ne doit pas interrompre le diagnostic ; checks : %+v",
				nom, checks)
		}
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
