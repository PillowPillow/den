package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// socketDeTest est la valeur d'SSH_AUTH_SOCK que rend depsOK. Constante nommée
// parce que deux tests l'attendent dans un Detail.
const socketDeTest = "/tmp/den-test/agent-ssh.sock"

// depsOK simule un système où sbx est installé, tous les chemins existent, git
// est assez récent et un agent SSH tourne. Tout est INJECTÉ : sans cela, le
// diagnostic de version dépendrait du git du poste et celui d'SSH_AUTH_SOCK de
// la session qui lance la suite — vert ici, rouge en CI.
//
// Getenv ne répond QUE sur SSH_AUTH_SOCK : un contrôle qui lirait une autre
// variable ne se trahirait pas si l'on rendait la même valeur pour tout.
func depsOK() Deps {
	return Deps{
		LookPath:   func(string) (string, error) { return "/usr/local/bin/sbx", nil },
		Stat:       func(string) (os.FileInfo, error) { return nil, nil },
		VersionGit: func() (string, error) { return "git version 2.43.0\n", nil },
		Getenv: func(nom string) string {
			if nom == "SSH_AUTH_SOCK" {
				return socketDeTest
			}
			return ""
		},
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
		if c.Niveau != NiveauOK {
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
	if !c.Bloquant() {
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
		if c.Nom == "nest casse" && c.Bloquant() {
			vuCasse = true
		}
		if c.Nom == "nest sain" && c.Bloquant() && strings.Contains(c.Detail, "/dev/sain-manquant") {
			vuSainDiagnostique = true
		}
		if c.Nom == "nests" && c.Bloquant() {
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
		if !c.Bloquant() {
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
			vert := g.Niveau == NiveauOK
			if vert != c.accepte {
				t.Fatalf("version %q : check git vert = %v, attendu %v (détail : %s)",
					c.version, vert, c.accepte, g.Detail)
			}
			if c.accepte {
				return
			}
			// Un git trop ancien BLOQUE : `den doctor` doit en sortir
			// non-zéro. Sans cette ligne, le rétrograder en avertissement
			// laisserait le test vert.
			if !g.Bloquant() {
				t.Fatalf("version %q : un git trop ancien doit être bloquant ; obtenu %+v", c.version, g)
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
	if !g.Bloquant() {
		t.Errorf("git injoignable doit être un échec ; obtenu %+v", g)
	}
	// Et le diagnostic doit continuer : git manquant n'empêche pas de dire ce
	// qui ne va pas ailleurs.
	if !trouveNom(checks, "nests") {
		t.Errorf("le diagnostic doit se poursuivre malgré git injoignable ; checks : %+v", checks)
	}
}

// D5 — un ssh.dir déclaré mais absent du disque. Validate() ne voit que « non
// déclaré » ; seule une sonde du système voit « déclaré et introuvable », et
// c'est ce chemin-là que `sbx create` reçoit en workspace.
func TestRunSSHDirIntrouvable(t *testing.T) {
	dir := denHomeValide(t)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: mount
  dir: /dev/ssh-absent
`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := depsOK()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/dev/ssh-absent" {
			return nil, errors.New("introuvable")
		}
		return nil, nil
	}

	checks := Run(dir, d)
	c, ok := trouveNomExact(checks, "ssh.dir")
	if !ok {
		t.Fatalf("aucun check nommé \"ssh.dir\" ; checks : %+v", checks)
	}
	if !c.Bloquant() {
		t.Errorf("un ssh.dir introuvable doit être un échec ; obtenu %+v", c)
	}
	if !strings.Contains(c.Detail, "/dev/ssh-absent") {
		t.Errorf("détail = %q, attendu le chemin complet", c.Detail)
	}
}

// Hors du mode mount, ssh.dir n'est monté nulle part : rien à contrôler, et
// surtout rien à signaler. Sans ce cas, un contrôle inconditionnel ferait
// échouer doctor sur toutes les configurations en agent-forward.
func TestRunNeControlePasSSHDirHorsDuModeMount(t *testing.T) {
	dir := denHomeValide(t)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: agent-forward
  dir: /dev/ssh-absent
`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := depsOK()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/dev/ssh-absent" {
			return nil, errors.New("introuvable")
		}
		return nil, nil
	}
	if checks := Run(dir, d); !tousOK(checks) {
		t.Errorf("en agent-forward, un ssh.dir absent ne doit rien signaler ; checks : %+v", checks)
	}
}

// D2 — `ssh.mode: agent-forward` est le DÉFAUT de la configuration, et il
// n'ajoute NI argument à l'argv de `sbx create` NI entrée au mixin : il repose
// entièrement sur le fait que le process sbx hérite de SSH_AUTH_SOCK depuis
// l'environnement de den. Cet héritage est prouvé, pas supposé — voir
// internal/sbx TestExecRunTransmetLEnvironnementDeDen. Si la variable est
// absente, il n'y a simplement rien à hériter, et l'utilisateur ne l'apprend
// que dans la VM, au premier `git push` : très loin de la cause.
func TestRunAvertitQuandAgentForwardSansSocketSSH(t *testing.T) {
	d := depsOK()
	d.Getenv = func(string) string { return "" }

	checks := Run(denHomeValide(t), d)
	c, ok := trouveNomExact(checks, "ssh.mode")
	if !ok {
		t.Fatalf("aucun check nommé \"ssh.mode\" ; checks : %+v", checks)
	}
	if c.Niveau != NiveauAvertissement {
		t.Errorf("niveau = %v, attendu NiveauAvertissement (%v) ; obtenu %+v",
			c.Niveau, NiveauAvertissement, c)
	}
	// La propriété qui compte pour l'utilisateur, et elle est SÉPARÉE du niveau
	// : c'est elle qui décide du code de sortie de `den doctor`.
	if c.Bloquant() {
		t.Error("un agent SSH absent ne doit PAS faire échouer den doctor : travailler en local " +
			"sans dépôt distant est légitime, et den n'a aucun moyen de savoir si l'utilisateur a besoin de SSH")
	}
	// Le message doit nommer la variable (ce qu'on cherche), le mode (pourquoi
	// c'est lui qui la réclame) et la CONSÉQUENCE concrète — sans elle,
	// l'utilisateur ne sait pas si la ligne le concerne.
	// « absent ou vide » et non « absent » : os.Getenv rend "" dans les deux
	// cas, et den n'appelle pas os.LookupEnv — le message doit décrire ce qui a
	// été vu, pas une cause plausible parmi deux.
	for _, attendu := range []string{"SSH_AUTH_SOCK", "agent-forward", "git push", "absent ou vide"} {
		if !strings.Contains(c.Detail, attendu) {
			t.Errorf("détail = %q, doit contenir %q", c.Detail, attendu)
		}
	}
}

// M2 — le CÂBLAGE réel de Getenv, que tous les tests ci-dessus contournent en
// injectant un double. Deux propriétés, qu'aucun autre test ne touche :
//
//   - DepsSysteme lit bien l'environnement du PROCESSUS (sans quoi le contrôle
//     de ssh.mode serait vert en test et faux en production) ;
//   - une variable posée VIDE y prend exactement le même chemin qu'une variable
//     absente. Mesuré : `SSH_AUTH_SOCK=""` donne os.Getenv → "" et
//     os.LookupEnv → ("", true) ; absente, os.Getenv → "" et os.LookupEnv →
//     ("", false). den n'appelle pas LookupEnv, donc il ne PEUT pas distinguer
//     les deux — c'est ce qui rend « absent ou vide » exact et « absent » faux.
//
// La valeur est POSÉE par t.Setenv, jamais présupposée : ce poste a un
// SSH_AUTH_SOCK réel, et un test qui s'appuierait dessus serait vrai par
// accident ici et rouge en CI. t.Setenv interdit t.Parallel dans ce test.
func TestDepsSystemeLitLEnvironnementEtTraiteVideCommeAbsent(t *testing.T) {
	lis := DepsSysteme().Getenv
	if lis == nil {
		t.Fatal("DepsSysteme().Getenv est nil : le contrôle de ssh.mode paniquerait en exécution réelle")
	}

	const pose = "/tmp/den-test/socket-pose.sock"
	t.Setenv("SSH_AUTH_SOCK", pose)
	if vu := lis("SSH_AUTH_SOCK"); vu != pose {
		t.Errorf("Getenv(SSH_AUTH_SOCK) = %q, attendu %q : DepsSysteme doit lire l'environnement du processus",
			vu, pose)
	}

	t.Setenv("SSH_AUTH_SOCK", "")
	if vu := lis("SSH_AUTH_SOCK"); vu != "" {
		t.Errorf("Getenv(SSH_AUTH_SOCK) = %q sur une variable posée VIDE, attendu \"\" : "+
			"une variable vide doit emprunter le même chemin qu'une variable absente", vu)
	}
}

// Le pendant : un agent SSH en marche ne produit aucun avertissement. Sans lui,
// un `avertit` câblé sans condition passerait le test précédent.
func TestRunNAvertitPasQuandLAgentSSHTourne(t *testing.T) {
	checks := Run(denHomeValide(t), depsOK())
	c, ok := trouveNomExact(checks, "ssh.mode")
	if !ok {
		t.Fatalf("aucun check nommé \"ssh.mode\" ; checks : %+v", checks)
	}
	if c.Niveau != NiveauOK {
		t.Errorf("un agent SSH en marche ne doit rien signaler ; obtenu %+v", c)
	}
	// Le socket est nommé : un diagnostic qui dit « ok » sans dire ce qu'il a vu
	// ne permet pas de repérer un SSH_AUTH_SOCK périmé.
	if !strings.Contains(c.Detail, socketDeTest) {
		t.Errorf("détail = %q, doit nommer le socket vu (%q)", c.Detail, socketDeTest)
	}
}

// Hors d'agent-forward, SSH_AUTH_SOCK n'est utilisé par rien : ni `none` ni
// `mount` n'en dépendent, et un avertissement y serait un faux positif servi à
// chaque `den doctor`. Même famille que
// TestRunNeControlePasSSHDirHorsDuModeMount, par l'autre bout.
func TestRunNAvertitPasHorsDAgentForward(t *testing.T) {
	for _, cas := range []struct{ nom, ssh string }{
		{"none", "ssh:\n  mode: none\n"},
		{"mount", "ssh:\n  mode: mount\n  dir: /dev/ssh\n"},
	} {
		t.Run(cas.nom, func(t *testing.T) {
			dir := denHomeValide(t)
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`+cas.ssh), 0o644); err != nil {
				t.Fatal(err)
			}
			d := depsOK()
			// Aucun agent SSH : c'est précisément la situation qui déclenche
			// l'avertissement en agent-forward.
			d.Getenv = func(string) string { return "" }

			checks := Run(dir, d)
			if c, ok := trouveNomExact(checks, "ssh.mode"); ok {
				t.Errorf("mode %q : aucun check ssh.mode attendu ; obtenu %+v", cas.nom, c)
			}
			if !tousOK(checks) {
				t.Errorf("mode %q : aucun signalement attendu ; checks : %+v", cas.nom, checks)
			}
		})
	}
}

// Pendant exact de TestSpawnIgnoreUneEntreeVideDansKits, côté doctor : une
// entrée vide dans `kits:` n'est pas un kit manquant, c'est une ligne sans
// contenu. Les deux tests tiennent la MÊME propriété par les deux bouts, depuis
// que `kits:` et `kit:` sont composés par une source unique
// (config.Stack.KitsDeclares) : neutraliser le filtre dans cette source doit
// faire rougir les deux, et c'est ce qui prouve qu'il n'y en a plus qu'une.
func TestRunIgnoreUneEntreeVideDansKits(t *testing.T) {
	dir := denHomeValide(t)
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nkits: [\"\", transverse]\nkit: devx-kit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stat échoue sur le chemin VIDE et sur lui seul : avec un depsOK
	// toujours-OK, un contrôle qui sonderait l'entrée vide ne se trahirait pas.
	d := depsOK()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "" {
			return nil, errors.New("chemin vide")
		}
		return nil, nil
	}

	checks := Run(dir, d)
	if !tousOK(checks) {
		t.Errorf("une entrée vide dans kits: ne doit produire aucun échec ; checks : %+v", checks)
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

// UNE faute, UN diagnostic — et le même compte quel que soit l'espace invisible
// écrit dans le champ.
//
// Deux juges portaient sur `update` : Validate() (étape 3, sur TrimSpace) et
// une boucle propre à doctor (étape 5, sur `== ""`). Mesuré sur le binaire
// AVANT correctif, den home identique à l'espace près :
//
//	update: ""     → 2 [FAIL], « 2 diagnostic(s) en échec »
//	update: "   "  → 1 [FAIL], « 1 diagnostic(s) en échec »
//
// La seconde ligne ne nommait de surcroît que « agent claude », là où
// Validate() nomme la CLÉ à corriger. C'était le dernier survivant de la classe
// « deux juges d'un même champ », que la branche a fermée trois fois ailleurs —
// et l'étape 5 n'était couverte par aucun test (mesuré : `== "" && false`
// laissait `go test ./... -count=1` entièrement vert).
//
// Le test porte sur le NOMBRE et sur la CLÉ, pas sur l'existence : c'est le
// nombre qui bougeait, et l'existence était déjà tenue par le test ci-dessus.
func TestDoctorNeCompteQuUnEchecParUpdateFautif(t *testing.T) {
	for _, update := range []string{"", "   ", "\t"} {
		t.Run(strconv.Quote(update), func(t *testing.T) {
			dir := t.TempDir()
			contenu := "agents:\n  claude:\n    config_dir: /tmp/c\n    update: " +
				strconv.Quote(update) + "\ndefaults:\n  agent: claude\n  stack: devx\n"
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(contenu), 0o644); err != nil {
				t.Fatal(err)
			}

			var fautifs []Check
			for _, c := range Run(dir, depsOK()) {
				if c.Bloquant() && strings.Contains(c.Detail, "update") {
					fautifs = append(fautifs, c)
				}
			}
			if len(fautifs) != 1 {
				t.Fatalf("%d diagnostics en échec sur `update` pour UNE faute, attendu 1 ; obtenu : %+v",
					len(fautifs), fautifs)
			}
			// La clé exacte, pas seulement l'agent : « agent claude » n'indique
			// pas quelle ligne du YAML corriger.
			if !strings.Contains(fautifs[0].Detail, "agents.claude.update") {
				t.Errorf("le diagnostic doit nommer la clé à corriger ; obtenu : %+v", fautifs[0])
			}
		})
	}
}

// Une stack cassée ne doit produire qu'UN SEUL diagnostic — le sien — et
// surtout aucun diagnostic FAUX sur les objets sains. 16ᵉ configuration
// hostile (tâche 17c).
//
// Sortie RÉELLE du binaire avant correctif, avec devx saine et `autre` portant
// une clé inconnue :
//
//	[FAIL] stacks         …/stacks/autre/stack.yaml : YAML invalide : clé inconnue "imag"
//	[FAIL] defaults.stack stack "devx" introuvable dans …/stacks
//	[FAIL] nest api       stack "devx" introuvable
//
// Les deux dernières lignes sont FAUSSES : devx existe et est parfaitement
// valide. LoadStacks ayant échoué en bloc, doctor retombait sur une map vide et
// déclarait introuvable tout ce qu'elle ne contenait plus — envoyant réparer le
// mauvais fichier. C'est le mensonge, plus que le blocage, que ce test verrouille.
func TestUneStackCasseeNeProduitAucunDiagnosticFaux(t *testing.T) {
	dir := denHomeValide(t)
	// devx reste saine ; on ajoute une seconde stack, que personne n'utilise.
	if err := os.MkdirAll(filepath.Join(dir, "stacks", "autre"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stacks", "autre", "stack.yaml"),
		[]byte("image: autre:v1\nimag: faute-de-frappe\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := Run(dir, depsOK())

	// 1. La stack cassée est nommée, elle.
	c, ok := trouveNomExact(checks, "stack autre")
	if !ok || !c.Bloquant() {
		t.Fatalf("la stack cassée doit produire un diagnostic en échec qui la nomme ; checks = %+v", checks)
	}
	if !strings.Contains(c.Detail, "imag") {
		t.Errorf("le diagnostic doit nommer la clé fautive ; obtenu : %s", c.Detail)
	}

	// 2. AUCUN diagnostic ne doit prétendre que devx est introuvable.
	for _, ch := range checks {
		if strings.Contains(ch.Detail, "devx") && strings.Contains(ch.Detail, "introuvable") {
			t.Errorf("diagnostic FAUX : devx est saine, or %q dit : %s", ch.Nom, ch.Detail)
		}
	}

	// 3. defaults.stack (= devx) reste vert.
	if d, ok := trouveNomExact(checks, "defaults.stack"); !ok || d.Niveau != NiveauOK {
		t.Errorf("defaults.stack pointe sur devx, qui est saine : attendu vert ; obtenu %+v", d)
	}

	// 4. Le nest api, qui utilise devx, ne doit produire aucun échec de stack.
	if n, ok := trouveNomExact(checks, "nest api"); ok && n.Niveau != NiveauOK {
		t.Errorf("le nest api utilise devx, saine : aucun échec attendu ; obtenu : %s", n.Detail)
	}
}

// Le pendant : quand c'est SA PROPRE stack qui est cassée, le nest doit
// l'apprendre — et lire « illisible », pas « introuvable ». Sans ce test, un
// correctif qui se contenterait d'ignorer les stacks cassées passerait le test
// précédent tout en rendant le vrai problème invisible.
func TestUnNestDontLaStackEstCasseeLApprend(t *testing.T) {
	dir := denHomeValide(t)
	// devx, la stack du nest api, devient illisible.
	if err := os.WriteFile(filepath.Join(dir, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nimag: faute-de-frappe\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := Run(dir, depsOK())

	n, ok := trouveNomExact(checks, "nest api")
	if !ok || !n.Bloquant() {
		t.Fatalf("le nest dont la stack est cassée doit produire un échec ; checks = %+v", checks)
	}
	if !strings.Contains(n.Detail, "illisible") {
		t.Errorf("le nest doit lire « illisible » et non « introuvable » : sa stack existe, "+
			"elle ne se charge pas ; obtenu : %s", n.Detail)
	}
}

// La ligne « stacks » doit compter comme la ligne « nests », sa voisine
// immédiate à l'écran : déclarées ET illisibles.
//
// Avant, elle rendait « [ok  ] stacks 1 déclarée(s) » là où DEUX dossiers de
// stacks existaient, dont un cassé — avec un [ok] par-dessus le marché. Le
// [FAIL] stack <nom> rattrapait l'information, mais deux lignes voisines qui
// comptent différemment se lisent comme une contradiction, et c'est le total
// qu'on parcourt en diagonale.
func TestLaLigneStacksCompteCommeLaLigneNests(t *testing.T) {
	dir := denHomeValide(t) // devx saine, un nest api
	if err := os.MkdirAll(filepath.Join(dir, "stacks", "autre"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stacks", "autre", "stack.yaml"),
		[]byte("image: autre:v1\nimag: faute-de-frappe\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := Run(dir, depsOK())

	c, ok := trouveNomExact(checks, "stacks")
	if !ok {
		t.Fatalf("aucune ligne « stacks » ; checks = %+v", checks)
	}
	// Le compte des illisibles, comme la ligne des nests le fait.
	if !strings.Contains(c.Detail, "illisible") {
		t.Errorf("la ligne stacks doit compter les illisibles, comme la ligne nests ; obtenu : %q",
			c.Detail)
	}
	// 1 saine (devx), 1 illisible (autre).
	if !strings.Contains(c.Detail, "1 déclarée(s)") || !strings.Contains(c.Detail, "1 illisible(s)") {
		t.Errorf("attendu « 1 déclarée(s), 1 illisible(s) » ; obtenu : %q", c.Detail)
	}
	// Et le verdict suit le compte : une stack illisible n'est pas un [ok].
	if !c.Bloquant() {
		t.Errorf("la ligne stacks est [ok] alors qu'une stack est illisible ; obtenu : %+v", c)
	}
}

// Le pendant : sans aucune stack cassée, la ligne reste verte et annonce zéro
// illisible. Sans lui, un verdict câblé à false passerait le test précédent.
func TestLaLigneStacksResteVerteSansStackCassee(t *testing.T) {
	checks := Run(denHomeValide(t), depsOK())

	c, ok := trouveNomExact(checks, "stacks")
	if !ok {
		t.Fatalf("aucune ligne « stacks » ; checks = %+v", checks)
	}
	if c.Niveau != NiveauOK {
		t.Errorf("aucune stack n'est cassée : la ligne stacks doit être verte ; obtenu : %+v", c)
	}
	if !strings.Contains(c.Detail, "0 illisible(s)") {
		t.Errorf("attendu « 0 illisible(s) » ; obtenu : %q", c.Detail)
	}
}
