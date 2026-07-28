package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// depsOK simule un système où sbx est installé, tous les chemins existent et
// git est assez récent. La version est INJECTÉE : sans elle, le diagnostic de
// version dépendrait du git du poste et changerait de verdict d'une machine à
// l'autre.
func depsOK() Deps {
	return Deps{
		LookPath:   func(string) (string, error) { return "/usr/local/bin/sbx", nil },
		Stat:       func(string) (os.FileInfo, error) { return nil, nil },
		VersionGit: func() (string, error) { return "git version 2.43.0\n", nil },
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

// trouveNomExact rend le Check portant exactement ce nom. Contrairement à
// trouve, aucune sous-chaîne ne satisfait : "git" ne doit pas être servi par
// un check nommé autrement qui contiendrait le mot.
func trouveNomExact(checks []Check, nom string) (Check, bool) {
	for _, c := range checks {
		if c.Nom == nom {
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

// D2 — 13ᵉ configuration hostile (T5) : aucun chemin de kit n'était contrôlé, ni
// `kit:` ni `kits:`. Le dispatcher sbx échoue TARD — `exit $rc` au boot de la
// microVM — donc l'utilisateur voyait une VM qui meurt au démarrage, jamais un
// message de den. doctor contrôlait déjà les repos de nests ; les kits suivent
// le même patron, avec le même d.Stat injecté.
func TestRunKitDeStackIntrouvable(t *testing.T) {
	dir := denHomeValide(t)
	// `kits:` (pluriel, layerés d'abord) ET `kit:` (singulier) : les deux
	// familles doivent être contrôlées, pas seulement celle qui a un test.
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nkits: [transverse]\nkit: devx-kit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// LoadStack rend les chemins de kits absolus, relatifs au dossier de la stack.
	kitSingulier := filepath.Join(dir, "stacks", "devx", "devx-kit")
	kitPluriel := filepath.Join(dir, "stacks", "devx", "transverse")

	d := depsOK()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == kitSingulier || p == kitPluriel {
			return nil, errors.New("introuvable")
		}
		return nil, nil
	}

	checks := Run(dir, d)

	// Le chemin COMPLET, pour que le message soit actionnable sans deviner la
	// racine à laquelle le kit était relatif.
	for _, chemin := range []string{kitSingulier, kitPluriel} {
		c, ok := trouve(checks, chemin)
		if !ok {
			t.Errorf("aucun check ne nomme le kit manquant %s ; checks : %+v", chemin, checks)
			continue
		}
		if c.OK {
			t.Errorf("le check nommant %s doit être en échec ; obtenu %+v", chemin, c)
		}
	}
	// Et la stack qui les déclare doit être nommée : avec plusieurs stacks, un
	// chemin seul ne dit pas quel fichier stack.yaml corriger.
	if !trouveNom(checks, "stack devx") {
		t.Errorf("aucun check nommé %q : le message doit désigner la stack fautive ; checks : %+v",
			"stack devx", checks)
	}
}

// Un kit présent ne doit produire AUCUN échec : sans ce cas, un contrôle qui
// refuserait tous les kits passerait le test ci-dessus sans qu'on le voie.
func TestRunKitsPresentsNeSignalentRien(t *testing.T) {
	dir := denHomeValide(t)
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nkits: [transverse]\nkit: devx-kit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	checks := Run(dir, depsOK()) // depsOK : tout chemin existe
	if !tousOK(checks) {
		t.Errorf("attendu tous les checks OK quand les kits existent, obtenu %+v", checks)
	}
}

// --- D3 : plancher de version git ------------------------------------------
//
// den appelle `git rev-parse --path-format=absolute` (internal/worktree/
// worktree.go:599 et :611), option apparue dans git 2.31. Rien ne le déclarait
// ni ne le vérifiait : sur une machine à git 2.25, l'utilisateur récoltait un
// message git obscur au premier worktree, pas un diagnostic de den.

func TestAnalyseVersionGit(t *testing.T) {
	cas := []struct {
		sortie         string
		majeur, mineur int
		erreurAttendue bool
	}{
		// Les formes réellement rencontrées : git tout court, git d'Apple,
		// git for Windows — toutes suffixent la version différemment.
		{"git version 2.43.0\n", 2, 43, false},
		{"git version 2.39.5 (Apple Git-154)\n", 2, 39, false},
		{"git version 2.45.2.windows.1\n", 2, 45, false},
		{"git version 2.25.1", 2, 25, false},
		{"git version 3.0.0", 3, 0, false},
		// Une sortie qu'on ne sait pas lire est une erreur, jamais un pari : la
		// prendre pour 0.0 refuserait un git parfaitement bon.
		{"", 0, 0, true},
		{"une autre commande", 0, 0, true},
		{"git version deux", 0, 0, true},
	}
	for _, c := range cas {
		t.Run(strconv.Quote(c.sortie), func(t *testing.T) {
			majeur, mineur, err := analyseVersionGit(c.sortie)
			if c.erreurAttendue {
				if err == nil {
					t.Fatalf("attendu une erreur pour %q, obtenu %d.%d", c.sortie, majeur, mineur)
				}
				return
			}
			if err != nil {
				t.Fatalf("erreur inattendue pour %q : %v", c.sortie, err)
			}
			if majeur != c.majeur || mineur != c.mineur {
				t.Errorf("analyseVersionGit(%q) = %d.%d, attendu %d.%d", c.sortie, majeur, mineur, c.majeur, c.mineur)
			}
		})
	}
}

// La BORNE, pas seulement « vieux vs neuf » : 2.30 refusé, 2.31 accepté. Sans
// le cas d'égalité, un plancher posé à 2.32 passerait ce test sans qu'on le voie.
func TestRunPlancherDeVersionGit(t *testing.T) {
	cas := []struct {
		version string
		accepte bool
	}{
		{"git version 2.25.1", false},
		{"git version 2.30.9", false},
		{"git version 2.31.0", true}, // exactement le plancher : accepté
		{"git version 2.43.0", true},
		{"git version 3.0.0", true}, // majeur supérieur : le mineur ne compte plus
	}
	for _, c := range cas {
		t.Run(c.version, func(t *testing.T) {
			d := depsOK()
			d.VersionGit = func() (string, error) { return c.version, nil }
			checks := Run(denHomeValide(t), d)

			g, ok := trouveNomExact(checks, "git")
			if !ok {
				t.Fatalf("aucun check nommé \"git\" ; checks : %+v", checks)
			}
			if g.OK != c.accepte {
				t.Fatalf("version %q : check git OK = %v, attendu %v (détail : %s)",
					c.version, g.OK, c.accepte, g.Detail)
			}
			if c.accepte {
				return
			}
			// Un refus doit nommer les DEUX versions : celle qu'on a lue et
			// celle qu'on exige. Sans les deux, l'utilisateur ne sait pas quoi
			// installer.
			if !strings.Contains(g.Detail, "2.31") {
				t.Errorf("détail = %q, attendu la version exigée (2.31)", g.Detail)
			}
			if !strings.Contains(g.Detail, strings.TrimPrefix(c.version, "git version ")) {
				t.Errorf("détail = %q, attendu la version lue (%q)", g.Detail, c.version)
			}
		})
	}
}

// git absent, ou qui ne répond pas : den ne peut pas créer un seul worktree.
func TestRunGitInjoignable(t *testing.T) {
	d := depsOK()
	d.VersionGit = func() (string, error) { return "", errors.New("exec: \"git\": executable file not found in $PATH") }
	checks := Run(denHomeValide(t), d)

	g, ok := trouveNomExact(checks, "git")
	if !ok {
		t.Fatalf("aucun check nommé \"git\" ; checks : %+v", checks)
	}
	if g.OK {
		t.Errorf("git injoignable doit être un échec ; obtenu %+v", g)
	}
	// Et le diagnostic doit continuer : git manquant n'empêche pas de dire ce
	// qui ne va pas ailleurs.
	if !trouveNom(checks, "nests") {
		t.Errorf("le diagnostic doit se poursuivre malgré git injoignable ; checks : %+v", checks)
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
