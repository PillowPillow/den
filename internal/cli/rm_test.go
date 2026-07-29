package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
)

// ecrisDans écrit un fichier de configuration den, relatif à denHome, créant
// les dossiers parents nécessaires. ecrisConfig/ecrisStack/ecrisNest en sont
// des façades nommées : un seul corps à maintenir plutôt que trois copies
// presque identiques.
func ecrisDans(t *testing.T, denHome, rel, contenu string) {
	t.Helper()
	p := filepath.Join(denHome, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ecrisConfig(t *testing.T, denHome, contenu string) {
	t.Helper()
	ecrisDans(t, denHome, "config.yaml", contenu)
}

func ecrisStack(t *testing.T, denHome, nom, contenu string) {
	t.Helper()
	ecrisDans(t, denHome, filepath.Join("stacks", nom, "stack.yaml"), contenu)
}

func ecrisNest(t *testing.T, denHome, nom, contenu string) {
	t.Helper()
	ecrisDans(t, denHome, filepath.Join("nests", nom+".yaml"), contenu)
}

// configMinimale suffit à tous les tests de ce fichier qui n'exercent pas la
// résolution d'agent elle-même.
const configMinimale = `agents:
  claude:
    config_dir: /profil/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`

// lsAvec scripte `sbx ls --json` pour qu'il rende exactement ces sandboxes,
// toutes "running".
func lsAvec(noms ...string) map[string]sbx.Reponse {
	var b strings.Builder
	b.WriteString(`{"sandboxes":[`)
	for i, n := range noms {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"name":"` + n + `","status":"running","workspaces":["/w"]}`)
	}
	b.WriteString(`]}`)
	return map[string]sbx.Reponse{"ls --json": {Sortie: []byte(b.String())}}
}

// creeDepotGit crée un dépôt git réel, avec un commit initial, au chemin
// donné. Neutralisation de l'environnement git déjà assurée package-wide par
// TestMain (main_test.go).
func creeDepotGit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@exemple.test"},
		{"config", "user.name", "T"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", c...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v : %v\n%s", c, err, out)
		}
	}
}

func TestRmSupprimeLaSandbox(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	f := &sbx.Fake{Reponses: lsAvec("api")}

	if _, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !f.AAppele("rm", "--force", "api") {
		t.Errorf("appels : %v", f.Appels)
	}
}

// Le profil agent persiste : c'est toute la raison d'être d'un config_dir
// monté RW. Un den rm qui l'effacerait obligerait à refaire /login.
func TestRmNeToucheJamaisAuProfilAgent(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	profil := filepath.Join(denHome, "agents", "claude")
	if err := os.MkdirAll(profil, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{Reponses: lsAvec("api")}

	if _, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if _, err := os.Stat(profil); err != nil {
		t.Errorf("le profil agent doit survivre au rm : %v", err)
	}
}

func TestRmNomInconnu(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	f := &sbx.Fake{Reponses: lsAvec("api", "web")}

	_, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "absente")
	if err == nil {
		t.Fatal("un nom inconnu doit produire une erreur")
	}
	if !strings.Contains(err.Error(), "api") || !strings.Contains(err.Error(), "web") {
		t.Errorf("le message doit lister TOUTES les sandboxes vivantes ; obtenu : %v", err)
	}
	if f.AAppele("rm") {
		t.Errorf("aucun rm ne doit être tenté ; appels : %v", f.Appels)
	}
}

// --keep-worktrees : la sandbox part, les dossiers restent.
func TestRmKeepWorktreesNeTouchePasAuDisque(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	// Le repo est déclaré (basename "api", assorti au dossier de worktree créé
	// ci-dessous) : un nest à repos VIDES laisserait passer une implémentation
	// qui appellerait nettoieWorktrees même avec --keep-worktrees, puisqu'il
	// n'y aurait alors rien à itérer pour le trahir.
	repo := filepath.Join(t.TempDir(), "api")
	ecrisNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	wt := filepath.Join(denHome, "worktrees", "feat12", "api")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}

	if _, err := executeCmdAvecSbx(t, f, "--den-home", denHome,
		"rm", "api.feat12", "--keep-worktrees"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("--keep-worktrees doit préserver %s : %v", wt, err)
	}
	if !f.AAppele("rm", "--force", "api.feat12") {
		t.Errorf("la sandbox doit tout de même être supprimée ; appels : %v", f.Appels)
	}
}

// Une sandbox sans worktree n'a rien à nettoyer : le nettoyage ne doit pas
// s'inventer un chemin.
func TestRmSansWorktreeNeNettoieRien(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisNest(t, denHome, "api", "stack: devx\nrepos: []\n")
	f := &sbx.Fake{Reponses: lsAvec("api")}

	sortie, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if strings.Contains(sortie, "worktree") {
		t.Errorf("aucun nettoyage de worktree ne doit être annoncé ; obtenu :\n%s", sortie)
	}
}

// Une sandbox listée par `sbx ls` peut avoir été créée hors de den, avec un
// nom que sbx accepte mais que den refuserait comme composant de chemin :
// sans validation, ce nom traverse tel quel jusqu'à worktree.Chemin et
// envoie Retire hors de worktree_root — reproduit ici exactement comme
// mesuré en revue : un nest "api" bien réel et déclaré (LoadNest réussit),
// pour que la garde soit exercée au bout de la VRAIE résolution, pas
// court-circuitée par un nest absent qui échouerait de toute façon pour une
// autre raison (best-effort — voir TestRmNestIllisibleNEmpechePasLaDestruction).
func TestRmRejetteUnNomDeSandboxNonCanonique(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	creeDepotGit(t, repo)
	ecrisNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	// DecomposeNom coupe au PREMIER point : nest "api" (valide, LoadNest
	// réussit), worktree "../../evade" (invalide — un composant de nom de
	// sandbox ne peut pas commencer par ".").
	nomEtranger := "api.../../evade"
	f := &sbx.Fake{Reponses: lsAvec(nomEtranger)}

	_, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", nomEtranger)
	if err == nil {
		t.Fatal("un nom de sandbox non canonique doit être refusé")
	}
	if f.AAppele("rm", "--force", nomEtranger) {
		t.Errorf("aucun rm ne doit être tenté sur un nom non canonique ; appels : %v", f.Appels)
	}
	// Pas d'assertion sur le chemin d'évasion lui-même (worktree.Chemin(…,
	// "../../evade", repo)) : dans cette reproduction minimale, aucun worktree
	// n'existe réellement là (rien n'a jamais été créé à cet endroit via
	// worktree.Assure), donc « le dossier n'existe pas » resterait vrai même
	// SANS la garde — vérifié : sans elle, Retire conclut juste « déjà
	// disparu » et rend nil sans rien déplacer, err et rm --force en
	// témoignent déjà ci-dessus.
}

// Best-effort sur la RÉSOLUTION : un nest supprimé de ~/.den/nests depuis le
// spawn ne doit pas empêcher de détruire une sandbox bel et bien vivante — et
// l'avertissement doit dire où le worktree abandonné a été laissé.
func TestRmNestIllisibleNEmpechePasLaDestruction(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	// Pas de ecrisNest("api", ...) : le nest "api" est absent de ~/.den/nests.
	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}

	sortie, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(sortie, "illisible") {
		t.Errorf("la sortie doit signaler le nest illisible ; obtenu :\n%s", sortie)
	}
	// worktree_layout/worktree_root par défaut (configMinimale n'en déclare
	// aucun) : central, sous <denHome>/worktrees.
	attenduOu := filepath.Join(denHome, "worktrees", "feat12")
	if !strings.Contains(sortie, attenduOu) {
		t.Errorf("la sortie doit dire où le worktree abandonné a été laissé (%s) ; obtenu :\n%s",
			attenduOu, sortie)
	}
	if !f.AAppele("rm", "--force", "api.feat12") {
		t.Errorf("la sandbox doit tout de même être détruite ; appels : %v", f.Appels)
	}
}

// F1 — RÉGRESSION de la tâche 17a, mesurée en revue. Depuis que LoadGlobal
// valide, une faute dans un champ SANS RAPPORT avec les worktrees
// (agents.claude.update, ssh.mode, bin_dirs…) faisait sortir nettoieWorktrees
// en erreur AVANT le `sbx rm --force`, et `den rm` n'arrivait plus à détruire
// une sandbox bel et bien vivante.
//
// C'est la doctrine T13/T16 : un ~/.den cassé ne doit jamais bloquer l'accès à
// des VM vivantes. Et c'est déjà ce que promet la godoc de nettoieWorktrees —
// « best-effort sur la RÉSOLUTION ».
//
// Une commande valide ce qu'elle UTILISE : nettoieWorktrees ne lit que
// worktree_layout et worktree_root.
func TestRmDetruitLaSandboxMalgreUneFauteDeConfigSansRapport(t *testing.T) {
	// Deux fautes, dans deux familles différentes, dont AUCUNE ne décide où
	// vivent les worktrees. Les deux sont refusées par LoadGlobal.
	const configFautiveHorsWorktrees = `agents:
  claude:
    config_dir: /profil/claude
    update: "   "
defaults:
  agent: claude
  stack: devx
ssh:
  mode: nfs
`
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configFautiveHorsWorktrees)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisNest(t, denHome, "api", "stack: devx\nrepos: []\n")
	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}

	if _, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("une faute de config hors worktrees ne doit pas empêcher de détruire une sandbox vivante : %v", err)
	}
	if !f.AAppele("rm", "--force", "api.feat12") {
		t.Errorf("la sandbox doit avoir été détruite ; appels : %v", f.Appels)
	}
}

// La contrepartie, dans l'autre sens : une faute sur un champ que
// nettoieWorktrees UTILISE doit rester une erreur DURE. `centrl` n'est pas
// rattrapé par LoadGlobalSansValider — seul un worktree_layout VIDE reçoit le
// défaut `central` (config.go) — donc sans ce refus, den calculerait un chemin
// de worktree faux et nettoierait à côté, silencieusement.
func TestRmRefuseUnWorktreeLayoutInconnu(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale+"worktree_layout: centrl\n")
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisNest(t, denHome, "api", "stack: devx\nrepos: []\n")
	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}

	_, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err == nil {
		t.Fatal("un worktree_layout inconnu doit faire échouer den rm : on ne nettoie pas sans savoir où")
	}
	if !strings.Contains(err.Error(), "worktree_layout") {
		t.Errorf("erreur = %q, attendu le champ fautif nommé", err.Error())
	}
	if !strings.Contains(err.Error(), "centrl") {
		t.Errorf("erreur = %q, attendu la valeur fautive nommée", err.Error())
	}
	// Et rien n'a été détruit : on ne supprime pas une sandbox dont on ne sait
	// pas nettoyer les worktrees.
	if f.AAppele("rm") {
		t.Errorf("aucun rm ne doit être tenté ; appels : %v", f.Appels)
	}
}

// L'avertissement « nest illisible » part sur STDERR, jamais sur stdout : un
// `den rm | grep` doit voir un succès propre sans l'avertissement mélangé
// dedans (I7 en revue). executeCmdAvecSbx fusionne délibérément les deux
// flux (voir son commentaire) et ne peut donc PAS vérifier cette séparation —
// seul executeCmdFluxSepares, qui donne deux buffers distincts, le peut.
func TestRmNestIllisibleEcritSurStderr(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	// Pas de ecrisNest("api", ...) : le nest "api" est absent de ~/.den/nests.
	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}
	deps := DepsSysteme()
	deps.Sbx = f
	root := NewRootCmdAvec(deps)

	stdout, stderr, err := executeCmdFluxSepares(t, root, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(stderr, "illisible") {
		t.Errorf("l'avertissement doit sortir sur stderr ; stderr obtenu :\n%s", stderr)
	}
	if strings.Contains(stdout, "illisible") {
		t.Errorf("l'avertissement ne doit PAS apparaître sur stdout ; stdout obtenu :\n%s", stdout)
	}
	if !strings.Contains(stdout, "détruite") {
		t.Errorf("le message de succès doit apparaître sur stdout ; stdout obtenu :\n%s", stdout)
	}
}

// L'ordre « worktrees d'abord, sandbox ensuite » est une propriété de sûreté :
// l'inverse laisserait l'utilisateur sans VM ET avec un message d'erreur sur
// un dossier.
func TestRmNeDetruitPasLaSandboxSiUnWorktreeEstSale(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, `agents:
  claude:
    config_dir: /profil/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
`)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")

	repo := filepath.Join(t.TempDir(), "api")
	creeDepotGit(t, repo)
	ecrisNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	chemin, err := worktree.Assure(context.Background(), worktree.NewGit(),
		"central", filepath.Join(denHome, "worktrees"), worktree.Nom{Dossier: "feat12", Branche: "feat12"}, repo)
	if err != nil {
		t.Fatalf("préparation du worktree : %v", err)
	}
	if err := os.WriteFile(filepath.Join(chemin, "brouillon.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}
	_, err = executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api.feat12")

	if err == nil {
		t.Fatal("un worktree sale doit faire échouer le rm")
	}
	if !strings.Contains(err.Error(), chemin) {
		t.Errorf("le message doit nommer le worktree fautif ; obtenu : %v", err)
	}
	// LA propriété : la sandbox est INTACTE, et le worktree aussi.
	if f.AAppele("rm", "--force", "api.feat12") {
		t.Errorf("la sandbox ne doit PAS avoir été détruite ; appels : %v", f.Appels)
	}
	if _, err := os.Stat(filepath.Join(chemin, "brouillon.txt")); err != nil {
		t.Errorf("le travail non commité doit être intact : %v", err)
	}

	// Et avec --force, tout part : le worktree va à la corbeille, la sandbox
	// est détruite, et l'utilisateur apprend où son travail est parti — sous
	// un nom qui porte l'identité COMPLÈTE de la sandbox (nest ET worktree),
	// pas seulement le nest (M12 en revue : sans quoi deux worktrees de
	// worktrees différents du même nest se confondraient dans la corbeille).
	f2 := &sbx.Fake{Reponses: lsAvec("api.feat12")}
	sortie, err := executeCmdAvecSbx(t, f2, "--den-home", denHome,
		"rm", "api.feat12", "--force")
	if err != nil {
		t.Fatalf("avec --force, le rm doit passer : %v", err)
	}
	if !f2.AAppele("rm", "--force", "api.feat12") {
		t.Errorf("appels : %v", f2.Appels)
	}
	if !strings.Contains(sortie, filepath.Join(denHome, "trash")) {
		t.Errorf("la sortie doit dire où le worktree est parti (la corbeille) ; obtenu :\n%s", sortie)
	}
	if !strings.Contains(sortie, "api.feat12-api") {
		t.Errorf("l'entrée de corbeille doit porter l'identité complète api.feat12, pas juste api ; obtenu :\n%s", sortie)
	}
	// F3 : et rien ne reste. Le dossier `<worktree_root>/feat12` n'existait que
	// pour porter ce worktree ; le laisser derrière transforme worktree_root en
	// liste de dossiers vides à mesure que l'utilisateur spawne et détruit.
	if _, err := os.Stat(filepath.Dir(chemin)); !os.IsNotExist(err) {
		t.Errorf("%s devait disparaître avec son dernier worktree (err = %v)", filepath.Dir(chemin), err)
	}
	// La racine, elle, reste : c'est un réglage de l'utilisateur.
	if _, err := os.Stat(filepath.Join(denHome, "worktrees")); err != nil {
		t.Errorf("worktree_root ne doit pas être touché : %v", err)
	}
}

// worktree_layout: per-repo est une configuration supportée (spec §13.5). Un
// layout figé dans le code cherche le worktree au mauvais endroit, ne le
// trouve pas, laisse Retire conclure « déjà disparu », et ABANDONNE le
// worktree RÉEL sur le disque sans un mot.
func TestRmRespecteLeLayoutPerRepo(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, `agents:
  claude:
    config_dir: /profil/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: per-repo
`)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")

	repo := filepath.Join(t.TempDir(), "api")
	creeDepotGit(t, repo)
	ecrisNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	chemin, err := worktree.Assure(context.Background(), worktree.NewGit(),
		"per-repo", "", worktree.Nom{Dossier: "feat12", Branche: "feat12"}, repo)
	if err != nil {
		t.Fatalf("préparation du worktree : %v", err)
	}
	attendu := filepath.Join(repo, ".den", "feat12")
	if chemin != attendu {
		t.Fatalf("worktree.Assure a rendu %q, attendu %q", chemin, attendu)
	}

	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}
	if _, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if _, err := os.Stat(chemin); !os.IsNotExist(err) {
		t.Errorf("le worktree per-repo doit avoir été déplacé de %s ; stat : %v", chemin, err)
	}
	if !f.AAppele("rm", "--force", "api.feat12") {
		t.Errorf("appels : %v", f.Appels)
	}
}

// Le dossier du worktree peut avoir disparu AVANT que `den rm` soit lancé (un
// `rm -rf` manuel de l'utilisateur) : Retire rend alors un chemin de corbeille
// VIDE, et rien ne doit être annoncé — sans quoi la commande affiche
// « worktree envoyé à la corbeille : » suivi de rien, ce qui dit à
// l'utilisateur que son travail est parti nulle part.
func TestRmNAnnonceRienQuandLeWorktreeADejaDisparu(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, `agents:
  claude:
    config_dir: /profil/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
`)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")

	repo := filepath.Join(t.TempDir(), "api")
	creeDepotGit(t, repo)
	ecrisNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	chemin, err := worktree.Assure(context.Background(), worktree.NewGit(),
		"central", filepath.Join(denHome, "worktrees"), worktree.Nom{Dossier: "feat12", Branche: "feat12"}, repo)
	if err != nil {
		t.Fatalf("préparation du worktree : %v", err)
	}
	if err := os.RemoveAll(chemin); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}
	sortie, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if strings.Contains(sortie, "envoyé à la corbeille") {
		t.Errorf("aucune annonce de corbeille ne doit apparaître pour un dossier déjà disparu ; obtenu :\n%s", sortie)
	}
	if !f.AAppele("rm", "--force", "api.feat12") {
		t.Errorf("appels : %v", f.Appels)
	}
}

// gitPruneEchoueFactice délègue tout à un Git réel, sauf « worktree prune »
// qui échoue systématiquement : ça isole le seul scénario où
// worktree.Retire rend un dest NON VIDE en même temps qu'une erreur (le
// déplacement a réussi, mais l'enregistrement n'a pas pu être élagué).
type gitPruneEchoueFactice struct {
	reel worktree.Git
}

func (g gitPruneEchoueFactice) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if len(args) == 2 && args[0] == "worktree" && args[1] == "prune" {
		return nil, fmt.Errorf("prune factice : échec simulé")
	}
	return g.reel.Run(ctx, dir, args...)
}

func (g gitPruneEchoueFactice) RunAvecEntree(ctx context.Context, dir string, entree []byte, args ...string) ([]byte, error) {
	if len(args) == 2 && args[0] == "worktree" && args[1] == "prune" {
		return nil, fmt.Errorf("prune factice : échec simulé")
	}
	return g.reel.RunAvecEntree(ctx, dir, entree, args...)
}

var _ worktree.Git = gitPruneEchoueFactice{}

// nettoieWorktrees jette dest quand Retire rend (dest, err) tous deux non
// vides (M11 en revue) — ce test vérifie que l'erreur de Retire, elle, NOMME
// quand même la corbeille, et que la sandbox n'est pas détruite malgré tout.
func TestRmNommeLaCorbeilleMemeQuandLElagageEchoue(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, `agents:
  claude:
    config_dir: /profil/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
`)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")

	repo := filepath.Join(t.TempDir(), "api")
	creeDepotGit(t, repo)
	ecrisNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	if _, err := worktree.Assure(context.Background(), worktree.NewGit(),
		"central", filepath.Join(denHome, "worktrees"), worktree.Nom{Dossier: "feat12", Branche: "feat12"}, repo); err != nil {
		t.Fatalf("préparation du worktree : %v", err)
	}

	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}
	deps := DepsSysteme()
	deps.Sbx = f
	deps.Git = gitPruneEchoueFactice{reel: worktree.NewGit()}
	root := NewRootCmdAvec(deps)

	_, err := executeCmd(t, root, "--den-home", denHome, "rm", "api.feat12")
	if err == nil {
		t.Fatal("l'échec de l'élagage doit remonter comme une erreur")
	}
	trash := filepath.Join(denHome, "trash")
	entrees, errLecture := os.ReadDir(trash)
	if errLecture != nil || len(entrees) == 0 {
		t.Fatalf("le worktree doit avoir été déplacé vers %s malgré l'échec de l'élagage : %v (%v)",
			trash, errLecture, entrees)
	}
	if !strings.Contains(err.Error(), trash) {
		t.Errorf("l'erreur doit nommer la corbeille où le travail a atterri ; obtenu : %v", err)
	}
	if f.AAppele("rm", "--force", "api.feat12") {
		t.Errorf("la sandbox ne doit pas être détruite si le nettoyage échoue ; appels : %v", f.Appels)
	}
}

// Un échec de `sbx rm` (VM verrouillée, sbx en panne…) doit remonter tel
// quel, pas être avalé en silence.
func TestRmEchecDeSbxRemonte(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	reponses := lsAvec("api")
	reponses["rm --force api"] = sbx.Reponse{Err: fmt.Errorf("sbx rm factice : échec simulé")}
	f := &sbx.Fake{Reponses: reponses}

	_, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api")
	if err == nil {
		t.Fatal("un échec de sbx rm doit remonter")
	}
}

// Les sondes git de worktree.Retire doivent être bornées : un dépôt sur un
// montage réseau mort ne doit pas faire pendre `den rm` indéfiniment.
func TestRmBorneLesSondesGitParUneEcheance(t *testing.T) {
	original := delaiSondesGit
	delaiSondesGit = 5 * time.Second
	t.Cleanup(func() { delaiSondesGit = original })

	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	repo := filepath.Join(t.TempDir(), "api")
	ecrisNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}
	git := &gitFactice{}
	deps := DepsSysteme()
	deps.Sbx = f
	deps.Git = git
	root := NewRootCmdAvec(deps)

	_, err := executeCmd(t, root, "--den-home", denHome, "rm", "api.feat12")
	if err == nil {
		t.Fatal("le git factice refuse systématiquement : une erreur est attendue")
	}
	if len(git.echeances) == 0 {
		t.Fatal("le contexte transmis à worktree.Retire ne porte aucune échéance : les sondes ne sont pas bornées")
	}
	restant := time.Until(git.echeances[0])
	// Bornée par le HAUT et par le BAS : un délai en dur totalement débranché
	// de delaiSondesGit (un mutant mesuré : 400 ms, sous le plancher documenté
	// de 499 ms) ne laisse passer qu'un contrôle « restant <= delaiSondesGit »
	// seul — il doit aussi être PROCHE de delaiSondesGit.
	if restant <= 0 || restant > delaiSondesGit || delaiSondesGit-restant > 500*time.Millisecond {
		t.Errorf("échéance hors bornes : il reste %v pour un délai de %v", restant, delaiSondesGit)
	}
}

// gitDeuxReposEcheanceFactice simule, pour PLUSIEURS repos, l'issue « déjà
// disparu » de worktree.Retire (rc=0, aucun enregistrement) sans aucun accès
// disque réel : il répond juste assez pour que Retire conclue « dossier
// absent, rien à faire » à chaque repo (`worktree prune` puis
// `worktree list --porcelain`, tous deux vides). Ça isole le BORNAGE du
// contexte du reste du comportement de Retire.
//
// Un ralentissement simulé (sleepPremierAppel) est inséré au tout premier
// appel : si l'échéance est posée UNE SEULE FOIS pour toute la boucle, le
// budget restant au second repo se sera déjà entamé de ce ralentissement ;
// si elle est posée À CHAQUE repo, le second repo repart d'un budget quasi
// intact.
type gitDeuxReposEcheanceFactice struct {
	sleepPremierAppel time.Duration
	appels            int
	echeancesListe    []time.Time // une par repo, prise sur « worktree list »
}

func (g *gitDeuxReposEcheanceFactice) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunAvecEntree(ctx, dir, nil, args...)
}

func (g *gitDeuxReposEcheanceFactice) RunAvecEntree(ctx context.Context, _ string, _ []byte, args ...string) ([]byte, error) {
	g.appels++
	if g.appels == 1 && g.sleepPremierAppel > 0 {
		time.Sleep(g.sleepPremierAppel)
	}
	if len(args) == 3 && args[0] == "worktree" && args[1] == "list" {
		if d, ok := ctx.Deadline(); ok {
			g.echeancesListe = append(g.echeancesListe, d)
		}
	}
	return nil, nil
}

var _ worktree.Git = (*gitDeuxReposEcheanceFactice)(nil)

func TestRmDonneUneEcheanceFraicheAChaqueRepo(t *testing.T) {
	original := delaiSondesGit
	delaiSondesGit = 1200 * time.Millisecond
	t.Cleanup(func() { delaiSondesGit = original })

	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	alpha := filepath.Join(t.TempDir(), "alpha")
	beta := filepath.Join(t.TempDir(), "beta")
	ecrisNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+alpha+" }\n  - { path: "+beta+" }\n")

	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}
	git := &gitDeuxReposEcheanceFactice{sleepPremierAppel: 700 * time.Millisecond}
	deps := DepsSysteme()
	deps.Sbx = f
	deps.Git = git
	root := NewRootCmdAvec(deps)

	if _, err := executeCmd(t, root, "--den-home", denHome, "rm", "api.feat12"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(git.echeancesListe) != 2 {
		t.Fatalf("attendu une échéance par repo (2 repos), obtenu %d", len(git.echeancesListe))
	}
	restantDeuxieme := time.Until(git.echeancesListe[1])
	// Une échéance HISSÉE hors de la boucle aurait déjà perdu ~700 ms au
	// moment du second repo ; une échéance PAR REPO repart d'un budget quasi
	// intact.
	if restantDeuxieme < delaiSondesGit-400*time.Millisecond {
		t.Errorf("le second repo hérite d'un budget entamé (reste %v sur %v) : "+
			"l'échéance n'est pas posée à chaque repo", restantDeuxieme, delaiSondesGit)
	}
}
