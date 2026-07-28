package worktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain neutralise la configuration git de la machine ET les variables de
// redirection, via NeutraliseEnvironnementGit (voir worktree.go) : le module
// lit les règles d'ignorance pour décider d'une suppression (un
// ~/.gitignore_global — celui de cette machine porte « .sbx » — rendrait les
// verdicts de saleté dépendants du poste qui lance la suite), et les HELPERS
// de ce fichier lancent `git` brut, que GIT_DIR + GIT_WORK_TREE détournerait
// sinon vers le dépôt désigné par l'environnement au lieu du dossier
// temporaire du test — reproduit une fois : la suite lancée ainsi avait créé
// des branches et des commits DANS le dépôt de den lui-même, et déplacé son
// HEAD.
func TestMain(m *testing.M) {
	NeutraliseEnvironnementGit()
	os.Exit(m.Run())
}

func TestChemin(t *testing.T) {
	cas := []struct {
		layout, root, wt, repo, attendu string
	}{
		{"central", "/den/worktrees", "feat12", "/dev/api", "/den/worktrees/feat12/api"},
		{"per-repo", "/den/worktrees", "feat12", "/dev/api", "/dev/api/.den/feat12"},
	}
	for _, c := range cas {
		if got := Chemin(c.layout, c.root, c.wt, c.repo); got != c.attendu {
			t.Errorf("Chemin(%s,…) = %q, attendu %q", c.layout, got, c.attendu)
		}
	}
}

// depotTest crée un dépôt git réel avec un commit, dans t.TempDir().
func depotTest(t *testing.T, nom string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), nom)
	creeDepot(t, dir)
	return dir
}

// creeDepot crée un dépôt git réel avec un commit au chemin exact demandé.
// Extrait de depotTest pour les cas qui imposent l'emplacement des dépôts
// (deux repos de même nom sous des parents différents, par exemple).
func creeDepot(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmds := [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@exemple.test"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "initial"},
	}
	for _, c := range cmds {
		cmd := exec.Command("git", c...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v : %v\n%s", c, err, out)
		}
	}
}

// depotTestAvecOrigine crée un dépôt *cloné*, donc pourvu d'un
// refs/remotes/origin/HEAD — ce dont dépend la découverte de la branche par
// défaut. Les dépôts purement locaux de depotTest n'en ont pas, et exercent
// donc le repli.
func depotTestAvecOrigine(t *testing.T, nom string) string {
	t.Helper()
	base := t.TempDir()
	distant := filepath.Join(base, nom+".git")
	git(t, base, "init", "-q", "--bare", "-b", "main", distant)

	source := filepath.Join(base, nom+"-source")
	creeDepot(t, source)
	git(t, source, "remote", "add", "origin", distant)
	git(t, source, "push", "-q", "origin", "main")

	clone := filepath.Join(base, nom)
	git(t, base, "clone", "-q", distant, clone)
	git(t, clone, "config", "user.email", "test@exemple.test")
	git(t, clone, "config", "user.name", "Test")
	return clone
}

func TestAssureCreeLeWorktreeEtLaBranche(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()

	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if chemin != filepath.Join(root, "feat12", "api") {
		t.Errorf("chemin = %q", chemin)
	}
	if _, err := os.Stat(filepath.Join(chemin, ".git")); err != nil {
		t.Errorf("le worktree doit exister : %v", err)
	}
	if got := brancheDe(t, chemin); got != "feat12" {
		t.Errorf("branche = %q, attendu feat12", got)
	}
}

// Idempotence : re-spawner le même nest avec le même -w ne doit rien casser.
func TestAssureEstIdempotent(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()

	premier, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("premier appel : %v", err)
	}
	second, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("second appel : %v", err)
	}
	if premier != second {
		t.Errorf("chemins divergents : %q puis %q", premier, second)
	}
}

// Le worktree existe mais sur une AUTRE branche : arrêt actionnable (spec §11),
// jamais un checkout silencieux qui déplacerait le travail de l'utilisateur.
func TestAssureRefuseUnWorktreeSurUneAutreBranche(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()

	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	basculeSur(t, chemin, "autre")

	_, err = Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err == nil {
		t.Fatal("un worktree sur une autre branche doit produire une erreur")
	}
	for _, attendu := range []string{chemin, "feat12", "autre"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit contenir %q pour être actionnable ; obtenu : %v", attendu, err)
		}
	}
}

// La branche existe déjà côté repo : on la checkout, on ne tente pas de la
// recréer (git refuserait).
func TestAssureReutiliseUneBrancheExistante(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()
	git(t, repo, "branch", "feat12")

	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if got := brancheDe(t, chemin); got != "feat12" {
		t.Errorf("branche = %q, attendu feat12", got)
	}
}

func TestAssureRepoInexistant(t *testing.T) {
	root := t.TempDir()
	_, err := Assure(context.Background(), NewGit(), "central", root, "feat12", "/n/existe/pas")
	if err == nil {
		t.Fatal("un repo inexistant doit produire une erreur")
	}
	// Le chemin seul ne prouve rien : sans la garde de den, exec.Cmd échoue au
	// chdir avec un message qui contient déjà le chemin. Seul le marqueur
	// français distingue la garde de den de ce rattrapage de os/exec.
	for _, attendu := range []string{"/n/existe/pas", "repo introuvable"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit contenir %q ; obtenu : %v", attendu, err)
		}
	}
}

// Un EACCES sur le parent n'est pas une absence : diagnostiquer « introuvable »
// enverrait l'utilisateur chercher le mauvais problème.
func TestAssureDistingueUnRepoInaccessible(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root traverse les dossiers sans permission")
	}
	base := t.TempDir()
	parent := filepath.Join(base, "verrouille")
	repo := filepath.Join(parent, "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	_, err := Assure(context.Background(), NewGit(), "central", t.TempDir(), "feat12", repo)
	if err == nil {
		t.Fatal("un repo inaccessible doit produire une erreur")
	}
	if strings.Contains(err.Error(), "introuvable") {
		t.Errorf("un EACCES ne doit pas être diagnostiqué comme une absence ; obtenu : %v", err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("la cause réelle doit rester déroulable par errors.Is ; obtenu : %v", err)
	}
}

// prepareWorktree monte le décor commun à presque tous les tests de Retire : un
// dépôt « api », son worktree « feat12 » en layout central, et la Cible qui le
// désigne — den_home compris, puisque c'est là que vit la corbeille.
func prepareWorktree(t *testing.T) (repo, chemin string, c Cible) {
	t.Helper()
	repo = depotTest(t, "api")
	c = Cible{
		DenHome:    t.TempDir(),
		Layout:     "central",
		Root:       t.TempDir(),
		Nest:       "api.feat12",
		Worktree:   "feat12",
		CheminRepo: repo,
	}
	chemin, err := Assure(context.Background(), NewGit(), c.Layout, c.Root, c.Worktree, c.CheminRepo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	return repo, chemin, c
}

// avecForce rend une copie de la Cible dont Force vaut ce qu'on demande.
func avecForce(c Cible, force bool) Cible { c.Force = force; return c }

// Spec §14 : refuser si dirty sans --force. Perdre du travail non commité
// serait le pire effet de bord possible pour un `den rm`.
func TestRetireRefuseUnWorktreeSale(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	if err := os.WriteFile(filepath.Join(chemin, "brouillon.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("un worktree avec des modifications non commitées doit être refusé sans force")
	}
	// « une erreur + dossier intact » est fourni par git seul : supprimer toute
	// la garde de den laisse ces deux assertions vertes. Les marqueurs du
	// message français sont ce qui distingue la garde de den du filet de git.
	for _, attendu := range []string{chemin, "brouillon.txt", "non commitées", "--keep-worktrees"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit contenir %q ; obtenu : %v", attendu, err)
		}
	}
	if _, err := os.Stat(chemin); err != nil {
		t.Errorf("le worktree refusé doit être INTACT : %v", err)
	}

	if _, err := Retire(context.Background(), NewGit(), avecForce(cible, true)); err != nil {
		t.Fatalf("avec force, la suppression doit passer : %v", err)
	}
}

// Idempotence de la suppression : `den rm` peut retomber sur un worktree déjà
// retiré à la main. Sans cette branche, git échouerait sur un chemin absent.
func TestRetireSurUnWorktreeDejaAbsentEstIdempotent(t *testing.T) {
	_, _, cible := prepareWorktree(t)
	if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
		t.Fatalf("première suppression : %v", err)
	}

	if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
		t.Errorf("un worktree déjà absent doit rendre nil ; obtenu : %v", err)
	}
	// Un worktree que den n'a jamais créé est traité pareil.
	jamais := cible
	jamais.Worktree = "jamais-cree"
	if _, err := Retire(context.Background(), NewGit(), jamais); err != nil {
		t.Errorf("un worktree jamais créé doit rendre nil ; obtenu : %v", err)
	}
}

// La saleté ne se limite pas aux fichiers non suivis : un fichier COMMITÉ puis
// modifié est du travail tout aussi destructible. Une implémentation qui ne
// regarderait que les fichiers non suivis (`git ls-files --others`) passerait
// le test du brouillon tout en détruisant ce travail-ci.
func TestRetireRefuseUnFichierSuiviModifie(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	fichier := filepath.Join(chemin, "suivi.txt")
	if err := os.WriteFile(fichier, []byte("commité\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, chemin, "add", "suivi.txt")
	git(t, chemin, "commit", "-m", "ajoute suivi.txt")

	// Modification non commitée d'un fichier suivi : rien d'« autre », tout de suivi.
	if err := os.WriteFile(fichier, []byte("travail en cours\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("une modification non commitée d'un fichier suivi doit être refusée sans force")
	}
	// git refuse aussi de sa propre initiative : n'accepter QUE le message de den
	// est ce qui distingue notre garde du filet de sécurité de git — sans quoi
	// l'utilisateur reçoit un « fatal: » anglais au lieu d'une consigne actionnable.
	for _, attendu := range []string{chemin, "non commitées", "--keep-worktrees"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit contenir %q pour être actionnable ; obtenu : %v", attendu, err)
		}
	}
	if _, err := os.Stat(chemin); err != nil {
		t.Fatalf("le worktree refusé doit être INTACT : %v", err)
	}
	contenu, err := os.ReadFile(fichier)
	if err != nil {
		t.Fatalf("le fichier modifié doit être INTACT : %v", err)
	}
	if string(contenu) != "travail en cours\n" {
		t.Errorf("le travail non commité a été altéré : %q", contenu)
	}
}

// Spec §13.4-3 : la branche part de la branche par défaut du repo, pas du HEAD
// qui traînait. Sur un nest multi-repo, le HEAD courant fait partir chaque repo
// d'une base différente sans un mot.
func TestAssurePartDeLaBrancheParDefaut(t *testing.T) {
	repo := depotTestAvecOrigine(t, "api")
	root := t.TempDir()
	// On éloigne le HEAD courant de la branche par défaut.
	git(t, repo, "checkout", "-q", "-b", "vieille")
	git(t, repo, "commit", "--allow-empty", "-m", "ne doit PAS servir de base")

	defaut := git(t, repo, "rev-parse", "main")
	horsSujet := git(t, repo, "rev-parse", "vieille")

	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	base := git(t, chemin, "rev-parse", "HEAD")
	if base == horsSujet {
		t.Errorf("la branche est partie du HEAD courant (%s) au lieu de la branche par défaut (%s)", horsSujet, defaut)
	}
	if base != defaut {
		t.Errorf("base = %s, attendu %s (la branche par défaut)", base, defaut)
	}
}

// Le point de départ est une ref de suivi : sans --no-track, feat12 suivrait
// origin/main et `git push` échouerait en proposant de pousser sur main.
func TestAssureNeFaitPasSuivreLaBrancheParDefaut(t *testing.T) {
	repo := depotTestAvecOrigine(t, "api")
	chemin, err := Assure(context.Background(), NewGit(), "central", t.TempDir(), "feat12", repo)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if amont, err := gitTolerant(t, chemin, "config", "--get", "branch.feat12.merge"); err == nil {
		t.Errorf("feat12 ne doit pas suivre de branche amont ; obtenu : %q", amont)
	}
}

// Repli : un dépôt purement local, sans origin, reste parfaitement légitime.
func TestAssureRepliQuandLeRepoNaPasDOrigine(t *testing.T) {
	repo := depotTest(t, "api")
	if _, err := gitTolerant(t, repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		t.Fatal("préparation : ce dépôt ne devrait pas avoir d'origin/HEAD")
	}
	chemin, err := Assure(context.Background(), NewGit(), "central", t.TempDir(), "feat12", repo)
	if err != nil {
		t.Fatalf("un dépôt sans origin doit passer par le repli : %v", err)
	}
	if got := brancheDe(t, chemin); got != "feat12" {
		t.Errorf("branche = %q, attendu feat12", got)
	}
}

// Un fichier ignoré est exactement ce qu'on ne commite pas ET qu'on ne retrouve
// pas : ni git status --porcelain ni le filet de git worktree remove ne le
// voient. C'est le seul cas où Retire détruisait sans que rien ne l'ait dit.
func TestRetireRefuseUnFichierIgnore(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, ".gitignore"), ".env\n")
	git(t, chemin, "add", ".gitignore")
	git(t, chemin, "commit", "-m", "ignore .env")
	ecris(t, filepath.Join(chemin, ".env"), "SECRET=hunter2\n")

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("un fichier ignoré non retrouvable doit être refusé sans force")
	}
	for _, attendu := range []string{chemin, ".env"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit nommer %q ; obtenu : %v", attendu, err)
		}
	}
	contenu, err := os.ReadFile(filepath.Join(chemin, ".env"))
	if err != nil || string(contenu) != "SECRET=hunter2\n" {
		t.Errorf("le secret doit être INTACT ; lu %q, err %v", contenu, err)
	}
}

// Contrepartie : un dossier ignoré en bloc est du cache régénérable. Refuser
// dessus rendrait den rm inutilisable sur tout projet JS ou Python.
func TestRetireAccepteUnDossierIgnoreEnBloc(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, ".gitignore"), "node_modules/\n")
	git(t, chemin, "add", ".gitignore")
	git(t, chemin, "commit", "-m", "ignore node_modules")
	if err := os.MkdirAll(filepath.Join(chemin, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	ecris(t, filepath.Join(chemin, "node_modules", "pkg", "index.js"), "module.exports={}\n")

	if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
		t.Fatalf("un dossier ignoré en bloc ne doit pas bloquer la suppression : %v", err)
	}
}

// Dossier disparu : l'enregistrement git survit et bloque tout Assure ultérieur.
// Rendre nil sans rien faire, c'était mentir sur un nettoyage non effectué.
func TestRetireEffaceUnEnregistrementOrphelin(t *testing.T) {
	repo, chemin, cible := prepareWorktree(t)
	root := cible.Root
	if err := os.RemoveAll(chemin); err != nil {
		t.Fatal(err)
	}
	if liste := git(t, repo, "worktree", "list"); !strings.Contains(liste, "prunable") {
		t.Fatalf("préparation : enregistrement orphelin attendu, obtenu :\n%s", liste)
	}

	if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if liste := git(t, repo, "worktree", "list"); strings.Contains(liste, "prunable") {
		t.Errorf("l'enregistrement orphelin doit être effacé, obtenu :\n%s", liste)
	}
	// La preuve qui compte : re-spawner le même nest redevient possible.
	if _, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo); err != nil {
		t.Errorf("le re-spawn doit repartir après nettoyage : %v", err)
	}
}

// Tant que l'enregistrement orphelin est là, Assure doit dire quoi faire en
// français, pas relayer le « fatal: … missing but already registered » de git.
func TestAssureSignaleUnEnregistrementOrphelin(t *testing.T) {
	repo, chemin, cible := prepareWorktree(t)
	root := cible.Root
	if err := os.RemoveAll(chemin); err != nil {
		t.Fatal(err)
	}

	_, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err == nil {
		t.Fatal("un enregistrement orphelin doit produire une erreur actionnable")
	}
	for _, attendu := range []string{chemin, "den rm"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit contenir %q ; obtenu : %v", attendu, err)
		}
	}
	if strings.Contains(err.Error(), "fatal:") {
		t.Errorf("le message ne doit pas relayer le fatal anglais de git ; obtenu : %v", err)
	}
}

// worktree_root est global et Chemin ne retient que le basename du repo : deux
// nests pointant sur des repos homonymes visent le même dossier. Sans garde, le
// second repo repart avec le worktree du premier et l'agent commite au mauvais
// endroit.
func TestAssureRefuseUnWorktreeAppartenantAUnAutreRepo(t *testing.T) {
	base := t.TempDir()
	repoA := filepath.Join(base, "acme", "api")
	repoB := filepath.Join(base, "beta", "api")
	creeDepot(t, repoA)
	creeDepot(t, repoB)
	root := t.TempDir()

	cheminA, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repoA)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	_, err = Assure(context.Background(), NewGit(), "central", root, "feat12", repoB)
	if err == nil {
		t.Fatalf("le worktree de %s ne doit pas être servi à %s", repoA, repoB)
	}
	for _, attendu := range []string{repoA, repoB} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit nommer les deux dépôts, il manque %q ; obtenu : %v", attendu, err)
		}
	}
	// Le worktree de A reste celui de A.
	if got := git(t, cheminA, "rev-parse", "--path-format=absolute", "--git-common-dir"); !strings.HasPrefix(got, repoA) {
		t.Errorf("le worktree de A a changé de dépôt : %s", got)
	}
}

// En layout per-repo la cible est TOUJOURS sous un dépôt : sans vérifier que le
// chemin est une racine de worktree, git répond pour le dépôt englobant et den
// valide un dossier vide, qu'il montera tel quel dans la VM.
func TestAssureRefuseUnDossierQuiNestPasUnWorktree(t *testing.T) {
	repo := depotTest(t, "api")
	// Le dépôt est sur feat12 : la branche coïncide, donc la garde §11 ne
	// verrait rien à redire.
	git(t, repo, "checkout", "-q", "-b", "feat12")
	cible := filepath.Join(repo, ".den", "feat12")
	if err := os.MkdirAll(cible, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Assure(context.Background(), NewGit(), "per-repo", t.TempDir(), "feat12", repo)
	if err == nil {
		t.Fatal("un dossier vide n'est pas un worktree : Assure doit refuser")
	}
	if !strings.Contains(err.Error(), cible) {
		t.Errorf("le message doit nommer %q ; obtenu : %v", cible, err)
	}
}

// cmd.Dir n'est pas une isolation : GIT_DIR est prioritaire. den est fait pour
// tourner sous des agents et des hooks git, où ces variables sont posées — et
// estSale déciderait alors d'une destruction d'après un autre dépôt.
func TestGitNeutraliseLesVariablesGitDeLEnvironnement(t *testing.T) {
	victime := depotTest(t, "victime")
	autre := depotTest(t, "autre")
	git(t, autre, "checkout", "-q", "-b", "branche-de-lautre")

	t.Setenv("GIT_DIR", filepath.Join(autre, ".git"))
	t.Setenv("GIT_WORK_TREE", autre)

	out, err := NewGit().Run(context.Background(), victime, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "main" {
		t.Errorf("GIT_DIR de l'environnement a détourné la commande : branche = %q, attendu main", got)
	}
}

// Layout per-repo : sans exclusion, le dépôt de l'utilisateur reste « ?? .den/ »
// à demeure — bruit permanent, risque de git add -A, et faux positif garanti
// pour tout contrôle « propre » à venir.
func TestAssurePerRepoNeSalitPasLeDepot(t *testing.T) {
	repo := depotTest(t, "api")

	if _, err := Assure(context.Background(), NewGit(), "per-repo", t.TempDir(), "feat12", repo); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if etat := git(t, repo, "status", "--porcelain"); etat != "" {
		t.Errorf("le dépôt parent doit rester propre ; git status : %q", etat)
	}

	// Idempotence : un second worktree ne doit pas ré-écrire la ligne.
	if _, err := Assure(context.Background(), NewGit(), "per-repo", t.TempDir(), "feat13", repo); err != nil {
		t.Fatalf("second worktree : %v", err)
	}
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	contenu, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatalf("lecture de %s : %v", exclude, err)
	}
	if n := strings.Count(string(contenu), ".den/"); n != 1 {
		t.Errorf(".den/ doit être exclu une seule fois, trouvé %d fois dans %s", n, exclude)
	}
}

// Retire ne doit pas supprimer un worktree qui n'appartient pas au repo qu'on
// lui donne : rien ne garantit que l'appelant (tâche 15) apparie correctement.
func TestRetireRefuseUnWorktreeDunAutreRepo(t *testing.T) {
	base := t.TempDir()
	repoA := filepath.Join(base, "acme", "api")
	repoB := filepath.Join(base, "beta", "api")
	creeDepot(t, repoA)
	creeDepot(t, repoB)

	root := t.TempDir()
	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repoA)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}

	// Les deux dépôts s'appellent « api » : Chemin, qui ne retient que le
	// basename, désigne pour repoB le worktree que repoA vient d'obtenir.
	cibleB := Cible{
		DenHome: t.TempDir(), Layout: "central", Root: root,
		Nest: "beta.feat12", Worktree: "feat12", CheminRepo: repoB, Force: true,
	}
	if got := Chemin(cibleB.Layout, cibleB.Root, cibleB.Worktree, cibleB.CheminRepo); got != chemin {
		t.Fatalf("préparation : les deux cibles doivent viser le même dossier ; %q contre %q", got, chemin)
	}

	_, err = Retire(context.Background(), NewGit(), cibleB)
	if err == nil {
		t.Fatal("Retire doit refuser un worktree étranger au repo donné")
	}
	// git refuse déjà de lui-même ; seuls les marqueurs du message de den
	// distinguent notre garde de ce rattrapage.
	for _, attendu := range []string{repoA, repoB} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit nommer les deux dépôts, il manque %q ; obtenu : %v", attendu, err)
		}
	}
	if _, err := os.Stat(chemin); err != nil {
		t.Errorf("le worktree de A doit être INTACT : %v", err)
	}
}

// gitEspion enregistre l'argv passé à git tout en déléguant au vrai git : c'est
// la seule façon d'observer que Retire laisse le filet de git armé.
type gitEspion struct {
	reel   Git
	appels [][]string
}

func (g *gitEspion) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunAvecEntree(ctx, dir, nil, args...)
}

func (g *gitEspion) RunAvecEntree(ctx context.Context, dir string, entree []byte, args ...string) ([]byte, error) {
	g.appels = append(g.appels, append([]string(nil), args...))
	return g.reel.RunAvecEntree(ctx, dir, entree, args...)
}

// compte dit combien d'appels débutent par ce préfixe. Le nombre est ce qui
// distingue « une question posée pour tout le lot » de « une question par
// entrée » — le motif que ce module a déjà dû retirer deux fois.
func (g *gitEspion) compte(prefixe ...string) int {
	n := 0
	for _, a := range g.appels {
		if commence(a, prefixe) {
			n++
		}
	}
	return n
}

func (g *gitEspion) appel(prefixe ...string) []string {
	for _, a := range g.appels {
		if len(a) >= len(prefixe) && slices.Equal(a[:len(prefixe)], prefixe) {
			return a
		}
	}
	return nil
}

// den ne supprime PLUS : il déplace. `git worktree remove` porte son propre
// filet de sécurité, mais ce filet est le MÊME code que `git status` — mesuré
// sur cinq tours de relecture : quand status ne voit rien, remove ne voit rien
// non plus. Un appel à `remove` réintroduirait donc l'irréversible sans rien
// ajouter à la protection.
func TestRetireNAppelleJamaisWorktreeRemove(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%v", force), func(t *testing.T) {
			_, _, cible := prepareWorktree(t)
			espion := &gitEspion{reel: NewGit()}
			if _, err := Retire(context.Background(), espion, avecForce(cible, force)); err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			if argv := espion.appel("worktree", "remove"); argv != nil {
				t.Errorf("`git worktree remove` a été émis : %v", argv)
			}
			if argv := espion.appel("worktree", "prune"); argv == nil {
				t.Errorf("l'enregistrement doit être élagué ; appels : %v", espion.appels)
			}
		})
	}
}

// Le suffixe « / » seul n'est pas un discriminant : git réduit à une entrée
// unique `!! conf/` un dossier que l'utilisateur n'a JAMAIS ignoré, dès lors que
// tout son contenu l'est. Or `*.env` est la façon la plus répandue d'ignorer des
// secrets — le dossier collapsé n'est pas du cache régénérable.
func TestRetireRefuseUnSecretDansUnDossierNonIgnore(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, ".gitignore"), "*.env\n")
	git(t, chemin, "add", ".gitignore")
	git(t, chemin, "commit", "-m", "ignore les .env")
	if err := os.MkdirAll(filepath.Join(chemin, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(chemin, "conf", "prod.env")
	ecris(t, secret, "SECRET=hunter2\n")

	// Le dossier lui-même n'est pas ignoré : seul son contenu l'est.
	if _, err := gitTolerant(t, chemin, "check-ignore", "-q", "--", "conf/"); err == nil {
		t.Fatal("préparation : conf/ ne devrait pas être ignoré lui-même")
	}

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("un dossier non ignoré dont le contenu l'est doit être refusé sans force")
	}
	if !strings.Contains(err.Error(), "conf/") {
		t.Errorf("le message doit nommer conf/ ; obtenu : %v", err)
	}
	contenu, err := os.ReadFile(secret)
	if err != nil || string(contenu) != "SECRET=hunter2\n" {
		t.Errorf("le secret doit être INTACT ; lu %q, err %v", contenu, err)
	}
}

// Faux positif symétrique : git cite et échappe les chemins « spéciaux », si
// bien qu'un dossier ignoré en bloc au nom non-ASCII ou avec une espace ne finit
// plus par « / » et ferait refuser den rm sur du simple cache — en affichant un
// chemin en octal que l'utilisateur ne reconnaîtrait pas.
func TestRetireAccepteUnDossierIgnoreAuNomCite(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, ".gitignore"), "données/\nmon cache/\n")
	git(t, chemin, "add", ".gitignore")
	git(t, chemin, "commit", "-m", "ignore les caches")
	for _, nom := range []string{"données", "mon cache"} {
		if err := os.MkdirAll(filepath.Join(chemin, nom), 0o755); err != nil {
			t.Fatal(err)
		}
		ecris(t, filepath.Join(chemin, nom, "x.bin"), "cache\n")
	}

	if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
		t.Fatalf("un dossier ignoré en bloc au nom cité ne doit pas bloquer la suppression : %v", err)
	}
}

// `git worktree prune` saute silencieusement les worktrees verrouillés — et
// `lock` existe précisément pour les volumes amovibles, donc pour le cas où le
// dossier disparaît légitimement. Sans revérification, Retire rendrait nil en
// prétendant avoir nettoyé et Assure renverrait vers `den rm` : boucle fermée.
func TestRetireSignaleUnWorktreeVerrouille(t *testing.T) {
	repo, chemin, cible := prepareWorktree(t)
	git(t, repo, "worktree", "lock", chemin)
	if err := os.RemoveAll(chemin); err != nil {
		t.Fatal(err)
	}

	for _, force := range []bool{false, true} {
		_, err := Retire(context.Background(), NewGit(), avecForce(cible, force))
		if err == nil {
			t.Fatalf("force=%v : Retire ne doit pas prétendre avoir nettoyé un enregistrement verrouillé", force)
		}
		for _, attendu := range []string{chemin, "git worktree unlock"} {
			if !strings.Contains(err.Error(), attendu) {
				t.Errorf("force=%v : le message doit contenir %q ; obtenu : %v", force, attendu, err)
			}
		}
	}
}

// Le « / » final change la réponse de check-ignore : dans le wildmatch de git,
// `*` et `**` matchent la chaîne vide, et git applique le .gitignore du dossier
// lui-même au composant vide qui suit le « / ». Interroger git avec le « / »,
// c'est donc demander « ce dossier OU son enfant de nom vide est-il ignoré ? » —
// et `logs/*` comme un .gitignore imbriqué à `*` sont des idiomes courants.
func TestRetireRefuseUnSecretSousUneRegleDeContenu(t *testing.T) {
	cas := []struct {
		nom      string
		racine   string // .gitignore à la racine du worktree
		imbrique string // .gitignore dans conf/
	}{
		{"regle sur le contenu", "conf/*\n", ""},
		{"regle recursive", "conf/**\n", ""},
		{"gitignore imbrique", "sans-rapport\n", "*\n"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			_, chemin, cible := prepareWorktree(t)
			ecris(t, filepath.Join(chemin, ".gitignore"), c.racine)
			git(t, chemin, "add", ".gitignore")
			git(t, chemin, "commit", "-m", "regles")
			if err := os.MkdirAll(filepath.Join(chemin, "conf"), 0o755); err != nil {
				t.Fatal(err)
			}
			if c.imbrique != "" {
				ecris(t, filepath.Join(chemin, "conf", ".gitignore"), c.imbrique)
			}
			secret := filepath.Join(chemin, "conf", "prod.env")
			ecris(t, secret, "SECRET=hunter2\n")

			// Le dossier lui-même n'est pas ignoré ; seul son contenu l'est.
			if _, err := gitTolerant(t, chemin, "check-ignore", "-q", "--", "conf"); err == nil {
				t.Fatal("préparation : conf (sans /) ne devrait pas être ignoré")
			}

			_, err := Retire(context.Background(), NewGit(), cible)
			if err == nil {
				t.Fatal("une règle qui ne couvre que le CONTENU ne rend pas le dossier jetable")
			}
			if !strings.Contains(err.Error(), "conf/") {
				t.Errorf("le message doit nommer conf/ ; obtenu : %v", err)
			}
			if contenu, err := os.ReadFile(secret); err != nil || string(contenu) != "SECRET=hunter2\n" {
				t.Errorf("le secret doit être INTACT ; lu %q, err %v", contenu, err)
			}
		})
	}
}

// `status.showUntrackedFiles = no` est un réglage de performance répandu sur les
// gros dépôts. den lit git sous la config de l'utilisateur : sans demander
// explicitement les fichiers non suivis, toute la garde du spec §14 se désarme.
func TestRetireVoitLeTravailNonSuiviMalgreLaConfigDuDepot(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	git(t, chemin, "config", "status.showUntrackedFiles", "no")
	ecris(t, filepath.Join(chemin, "brouillon.txt"), "wip\n")

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("le brouillon non suivi doit être refusé quelle que soit la config du dépôt")
	}
	// Marqueurs du message de den : git refuse peut-être aussi de son côté.
	for _, attendu := range []string{"brouillon.txt", "non commitées"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit contenir %q ; obtenu : %v", attendu, err)
		}
	}
	if _, err := os.Stat(filepath.Join(chemin, "brouillon.txt")); err != nil {
		t.Errorf("le brouillon doit être INTACT : %v", err)
	}
}

// skip-worktree et assume-unchanged existent PRÉCISÉMENT pour porter des
// modifications locales qu'on ne veut pas commiter — et git les cache alors
// totalement de `status`. Mesuré : le filet de `git worktree remove` ne les voit
// pas non plus, donc rien ne protège ce travail.
func TestRetireRefuseUnFichierMarqueLocalement(t *testing.T) {
	for _, drapeau := range []string{"--skip-worktree", "--assume-unchanged"} {
		t.Run(drapeau, func(t *testing.T) {
			_, chemin, cible := prepareWorktree(t)
			ecris(t, filepath.Join(chemin, "local.conf"), "v1\n")
			git(t, chemin, "add", "local.conf")
			git(t, chemin, "commit", "-m", "ajoute local.conf")
			git(t, chemin, "update-index", drapeau, "local.conf")
			ecris(t, filepath.Join(chemin, "local.conf"), "TRAVAIL IRREMPLACABLE\n")

			// git ne rapporte rien : c'est tout le problème.
			if etat := git(t, chemin, "status", "--porcelain"); etat != "" {
				t.Fatalf("préparation : git ne devrait rien rapporter, obtenu %q", etat)
			}

			_, err := Retire(context.Background(), NewGit(), cible)
			if err == nil {
				t.Fatal("un fichier marqué localement modifié ne doit pas être détruit sans force")
			}
			if !strings.Contains(err.Error(), "local.conf") {
				t.Errorf("le message doit nommer local.conf ; obtenu : %v", err)
			}
			if contenu, err := os.ReadFile(filepath.Join(chemin, "local.conf")); err != nil ||
				string(contenu) != "TRAVAIL IRREMPLACABLE\n" {
				t.Errorf("le travail doit être INTACT ; lu %q, err %v", contenu, err)
			}
		})
	}
}

// Le sparse-checkout pose LES MÊMES bits skip-worktree, mais sur des fichiers
// volontairement absents du disque. Les compter comme sales rendrait `den rm`
// impossible sans --force sur tout dépôt en sparse-checkout. Le discriminant est
// la présence : un fichier marqué ET présent porte un contenu local que git
// refuse de regarder ; marqué et absent, il est simplement hors du cône.
func TestRetireAccepteUnSparseCheckout(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	for _, d := range []string{"garde", "exclu"} {
		if err := os.MkdirAll(filepath.Join(chemin, d), 0o755); err != nil {
			t.Fatal(err)
		}
		ecris(t, filepath.Join(chemin, d, "f.txt"), "contenu\n")
	}
	git(t, chemin, "add", ".")
	git(t, chemin, "commit", "-m", "deux dossiers")
	git(t, chemin, "sparse-checkout", "set", "--no-cone", "garde")

	// Préparation : git marque bien les fichiers hors du cône. Le drapeau exact
	// dépend de la config du dépôt (`S`, ou `s` si assume-unchanged s'y ajoute) ;
	// seule compte la présence d'un marquage, pas sa lettre.
	drapeaux := git(t, chemin, "ls-files", "-v")
	if !strings.Contains(drapeaux, " exclu/f.txt") ||
		strings.Contains(drapeaux, "H exclu/f.txt") {
		t.Fatalf("préparation : marquage attendu sur exclu/f.txt, obtenu :\n%s", drapeaux)
	}
	if _, err := os.Stat(filepath.Join(chemin, "exclu", "f.txt")); !os.IsNotExist(err) {
		t.Fatalf("préparation : le fichier hors du cône devrait être absent du disque")
	}

	if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
		t.Fatalf("un sparse-checkout ne doit pas bloquer la suppression : %v", err)
	}
}

// core.ignoreStat pose le bit assume-unchanged sur TOUT le dépôt. Compter ces
// entrées sans les regarder rendait `den rm` impossible sans --force sur un
// worktree parfaitement propre — et les bits SURVIVENT au réglage, donc le
// blocage était définitif.
func TestRetireAccepteUnDepotSousIgnoreStat(t *testing.T) {
	prepare := func(t *testing.T) (chemin string, cible Cible) {
		t.Helper()
		_, chemin, cible = prepareWorktree(t)
		git(t, chemin, "config", "core.ignoreStat", "true")
		// Contenus DISTINCTS : avec trois fichiers identiques, toute permutation
		// des empreintes serait un no-op et l'assertion « les fichiers intacts ne
		// doivent pas être nommés » ne prouverait rien.
		for _, f := range []string{"f1.txt", "f2.txt", "f3.txt"} {
			ecris(t, filepath.Join(chemin, f), "contenu de "+f+"\n")
		}
		git(t, chemin, "add", ".")
		git(t, chemin, "commit", "-m", "fichiers")
		if drapeaux := git(t, chemin, "ls-files", "-v"); !strings.Contains(drapeaux, "h f1.txt") {
			t.Fatalf("préparation : bit assume-unchanged attendu, obtenu :\n%s", drapeaux)
		}
		return chemin, cible
	}

	t.Run("reglage actif", func(t *testing.T) {
		_, cible := prepare(t)
		if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
			t.Fatalf("un worktree propre doit rester supprimable : %v", err)
		}
	})

	// Le cas qui rend le blocage définitif : le réglage est retiré, les bits
	// restent. Lire core.ignoreStat ne suffirait donc pas à débloquer.
	t.Run("reglage retire mais bits persistants", func(t *testing.T) {
		chemin, cible := prepare(t)
		git(t, chemin, "config", "--unset", "core.ignoreStat")
		if drapeaux := git(t, chemin, "ls-files", "-v"); !strings.Contains(drapeaux, "h f1.txt") {
			t.Fatalf("préparation : les bits doivent survivre au --unset, obtenu :\n%s", drapeaux)
		}
		if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
			t.Fatalf("un worktree propre doit rester supprimable : %v", err)
		}
	})

	// Contrepartie : sous le même réglage, une VRAIE modification reste refusée.
	t.Run("modification reelle sous le meme reglage", func(t *testing.T) {
		chemin, cible := prepare(t)
		ecris(t, filepath.Join(chemin, "f2.txt"), "TRAVAIL IRREMPLACABLE\n")
		if etat := git(t, chemin, "status", "--porcelain"); etat != "" {
			t.Fatalf("préparation : git ne devrait rien voir, obtenu %q", etat)
		}

		_, err := Retire(context.Background(), NewGit(), cible)
		if err == nil {
			t.Fatal("une modification réelle sous assume-unchanged doit être refusée")
		}
		if !strings.Contains(err.Error(), "f2.txt") {
			t.Errorf("le message doit nommer f2.txt ; obtenu : %v", err)
		}
		if strings.Contains(err.Error(), "f1.txt") || strings.Contains(err.Error(), "f3.txt") {
			t.Errorf("les fichiers intacts ne doivent pas être nommés ; obtenu : %v", err)
		}
	})
}

// `git hash-object` n'est PAS sûr sur un chemin non régulier : sur un fifo il ne
// rend jamais la main (mesuré : rc=124 au timeout, pas d'échec), sur un dossier
// il sort en `fatal:` anglais. Un `den rm` qui ne revient jamais n'a même plus
// de résultat à emballer.
func TestRetireRefuseUnCheminQueGitNePeutPasHacher(t *testing.T) {
	cas := []struct {
		nom  string
		pose func(t *testing.T, chemin string)
	}{
		{"fifo", func(t *testing.T, chemin string) {
			if err := syscall.Mkfifo(chemin, 0o644); err != nil {
				t.Skipf("fifo indisponible : %v", err)
			}
		}},
		{"dossier", func(t *testing.T, chemin string) {
			if err := os.Mkdir(chemin, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			_, chemin, cible := prepareWorktree(t)
			suivi := filepath.Join(chemin, "suivi.txt")
			ecris(t, suivi, "v1\n")
			git(t, chemin, "add", "suivi.txt")
			git(t, chemin, "commit", "-m", "ajoute suivi.txt")
			git(t, chemin, "update-index", "--assume-unchanged", "suivi.txt")
			if err := os.Remove(suivi); err != nil {
				t.Fatal(err)
			}
			c.pose(t, suivi)

			// Borné : avant correctif le fifo bloque, et seule cette limite sort.
			ctx, annule := context.WithTimeout(context.Background(), 15*time.Second)
			defer annule()
			debut := time.Now()
			_, err := Retire(ctx, NewGit(), cible)
			ecoule := time.Since(debut)

			if ecoule > 5*time.Second {
				t.Errorf("Retire a mis %s : la sonde s'est bloquée sur le chemin non régulier", ecoule)
			}
			if err == nil {
				t.Fatal("un chemin que git ne peut pas hacher ne doit pas être détruit sans force")
			}
			for _, attendu := range []string{"suivi.txt", "non commitées"} {
				if !strings.Contains(err.Error(), attendu) {
					t.Errorf("le message doit contenir %q ; obtenu : %v", attendu, err)
				}
			}
			if strings.Contains(err.Error(), "fatal:") {
				t.Errorf("le message ne doit pas relayer le fatal anglais de git ; obtenu : %v", err)
			}
		})
	}
}

// git hache le TEXTE d'un lien symbolique (mode 120000) ; hash-object, lui, SUIT
// le lien et hacherait le contenu de la cible. Les deux ne coïncident jamais, si
// bien qu'un dépôt propre portant un seul lien suivi était refusé à perpétuité
// dès qu'un bit d'index le marquait.
func TestRetireAccepteUnLienSymboliqueIntact(t *testing.T) {
	cas := []struct{ nom, cible string }{
		{"lien valide", "cible.txt"},
		{"lien casse", "/n/existe/pas"},
		// Deux textes qu'un TrimSpace ou une comparaison ligne à ligne
		// abîmerait : sans eux, la comparaison de texte tolérait un rognage
		// sans qu'aucune assertion ne s'en aperçoive.
		{"cible a espace finale", "cible.txt "},
		{"cible a retour a la ligne interne", "a\nb"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			_, chemin, cible := prepareWorktree(t)
			ecris(t, filepath.Join(chemin, "cible.txt"), "CONTENU DE LA CIBLE\n")
			if err := os.Symlink(c.cible, filepath.Join(chemin, "lien.txt")); err != nil {
				t.Skipf("liens symboliques indisponibles : %v", err)
			}
			git(t, chemin, "add", ".")
			git(t, chemin, "commit", "-m", "cible et lien")
			git(t, chemin, "update-index", "--skip-worktree", "lien.txt")
			if etat := git(t, chemin, "status", "--porcelain"); etat != "" {
				t.Fatalf("préparation : le worktree doit être propre, obtenu %q", etat)
			}

			if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
				t.Fatalf("un lien symbolique intact ne doit pas bloquer la suppression : %v", err)
			}
		})
	}
}

// La propriété qui porte tout le correctif du tour 4 : hash-object applique les
// filtres de .gitattributes. Sans elle, un dépôt en autocrlf dont le disque est
// en CRLF — donc parfaitement propre — serait refusé.
func TestRetireAppliqueLesFiltresGitattributes(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	git(t, chemin, "config", "core.autocrlf", "true")
	ecris(t, filepath.Join(chemin, ".gitattributes"), "* text=auto\n")
	ecris(t, filepath.Join(chemin, "f.txt"), "une ligne\nune autre\n")
	git(t, chemin, "add", ".")
	git(t, chemin, "commit", "-m", "texte")
	// Ce que produirait un checkout sous autocrlf : le disque passe en CRLF.
	ecris(t, filepath.Join(chemin, "f.txt"), "une ligne\r\nune autre\r\n")
	git(t, chemin, "update-index", "--skip-worktree", "f.txt")
	if etat := git(t, chemin, "status", "--porcelain"); etat != "" {
		t.Fatalf("préparation : le worktree doit être propre, obtenu %q", etat)
	}

	if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
		t.Fatalf("les fins de ligne converties ne sont pas une modification : %v", err)
	}
}

// Un fichier suivi remplacé sur le disque par un lien symbolique cassé porte un
// contenu que l'object store ne rend pas. os.Stat suit le lien et l'écartait.
func TestRetireRefuseUnFichierMarqueRemplaceParUnLien(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	suivi := filepath.Join(chemin, "suivi.txt")
	ecris(t, suivi, "v1\n")
	git(t, chemin, "add", "suivi.txt")
	git(t, chemin, "commit", "-m", "ajoute suivi.txt")
	git(t, chemin, "update-index", "--assume-unchanged", "suivi.txt")
	if err := os.Remove(suivi); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/n/existe/pas", suivi); err != nil {
		t.Skipf("liens symboliques indisponibles : %v", err)
	}

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("un fichier suivi remplacé par un lien cassé ne doit pas être détruit sans force")
	}
	if !strings.Contains(err.Error(), "suivi.txt") {
		t.Errorf("le message doit nommer suivi.txt ; obtenu : %v", err)
	}
}

// Cas limite qui décide du contrôle de mode : un fichier suivi dont le contenu
// vaut EXACTEMENT le texte du lien qui le remplace. Comparer les seuls octets
// conclurait « identique » et détruirait le fichier ; c'est le mode de l'entrée
// d'index (100644 contre 120000) qui tranche.
func TestRetireRefuseUnFichierRemplaceParUnLienDeMemeTexte(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, "cible.txt"), "CONTENU\n")
	// Contenu sans retour à la ligne, identique au texte du futur lien.
	if err := os.WriteFile(filepath.Join(chemin, "suivi.txt"), []byte("cible.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, chemin, "add", ".")
	git(t, chemin, "commit", "-m", "fichier et cible")
	git(t, chemin, "update-index", "--skip-worktree", "suivi.txt")
	if err := os.Remove(filepath.Join(chemin, "suivi.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("cible.txt", filepath.Join(chemin, "suivi.txt")); err != nil {
		t.Skipf("liens symboliques indisponibles : %v", err)
	}

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("un fichier suivi devenu lien symbolique est une modification, même à texte égal")
	}
	if !strings.Contains(err.Error(), "suivi.txt") {
		t.Errorf("le message doit nommer suivi.txt ; obtenu : %v", err)
	}
}

// core.fsmonitor délègue à un démon la question « qu'est-ce qui a changé ». Un
// démon redémarré, un Watchman qui a perdu son état ou un montage où inotify
// rate des événements répondent « rien », et git les croit. Le drapeau reste
// MAJUSCULE dans ls-files -v : la sonde des bits d'index ne peut pas voir ce
// cas-là, par construction.
func TestRetireVoitUneModificationMalgreFsmonitor(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, "suivi.txt"), "v1\n")
	git(t, chemin, "add", "suivi.txt")
	git(t, chemin, "commit", "-m", "ajoute suivi.txt")

	// Hook v2 qui répond « rien n'a changé » : un jeton, aucun chemin.
	hook := filepath.Join(t.TempDir(), "fsmonitor.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nprintf 'jeton\\0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, chemin, "config", "core.fsmonitor", hook)
	git(t, chemin, "config", "core.fsmonitorHookVersion", "2")
	git(t, chemin, "status", "--porcelain") // amorce le cache empoisonné

	ecris(t, filepath.Join(chemin, "suivi.txt"), "TRAVAIL IRREMPLACABLE\n")
	if etat := git(t, chemin, "status", "--porcelain"); strings.Contains(etat, "suivi.txt") {
		t.Skipf("le hook fsmonitor n'a pas pris : git rapporte %q", etat)
	}
	if drapeaux := git(t, chemin, "ls-files", "-v"); !strings.Contains(drapeaux, "H suivi.txt") {
		t.Fatalf("préparation : drapeau majuscule attendu, obtenu :\n%s", drapeaux)
	}

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("une modification cachée par fsmonitor ne doit pas être détruite sans force")
	}
	if !strings.Contains(err.Error(), "suivi.txt") {
		t.Errorf("le message doit nommer suivi.txt ; obtenu : %v", err)
	}
}

// gitDefaillant fait échouer une sous-commande précise, pour vérifier le sens du
// doute : une sonde muette ne doit jamais autoriser une destruction.
type gitDefaillant struct {
	reel Git
	sur  string
}

func (g gitDefaillant) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunAvecEntree(ctx, dir, nil, args...)
}

func (g gitDefaillant) RunAvecEntree(ctx context.Context, dir string, entree []byte, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == g.sur {
		return nil, errors.New("panne simulée de git " + g.sur)
	}
	return g.reel.RunAvecEntree(ctx, dir, entree, args...)
}

func TestRetireRefuseQuandUneSondeEstMuette(t *testing.T) {
	for _, sonde := range []string{"ls-files", "hash-object"} {
		t.Run(sonde, func(t *testing.T) {
			_, chemin, cible := prepareWorktree(t)
			ecris(t, filepath.Join(chemin, "local.conf"), "v1\n")
			git(t, chemin, "add", "local.conf")
			git(t, chemin, "commit", "-m", "ajoute local.conf")
			git(t, chemin, "update-index", "--skip-worktree", "local.conf")

			g := gitDefaillant{reel: NewGit(), sur: sonde}
			_, err := Retire(context.Background(), g, cible)
			if err == nil {
				t.Fatalf("une panne de git %s ne doit pas autoriser la suppression", sonde)
			}
			// La cause réelle doit remonter : avaler l'erreur et ne refuser que
			// par une garde en aval marcherait ici par accident.
			if !strings.Contains(err.Error(), "panne simulée") {
				t.Errorf("la panne doit remonter telle quelle ; obtenu : %v", err)
			}
			if _, err := os.Stat(chemin); err != nil {
				t.Errorf("le worktree doit être INTACT : %v", err)
			}
		})
	}
}

// Les gardes de C-2 ne s'exécutent QUE lorsque le dossier a disparu — c'est-à-dire
// quand EvalSymlinks échoue des deux côtés de memeChemin et le fait retomber sur
// deux chaînes brutes différentes. Sur macOS, $TMPDIR vit sous /var → private/var :
// la résolution est structurellement absente là où on en a besoin.
func TestGardesDeCul2DeSacTiennentSousUnLienSymbolique(t *testing.T) {
	prepare := func(t *testing.T) (repo, chemin string, c Cible) {
		t.Helper()
		repo = depotTest(t, "api")
		base := t.TempDir()
		reel := filepath.Join(base, "reel")
		if err := os.MkdirAll(reel, 0o755); err != nil {
			t.Fatal(err)
		}
		lien := filepath.Join(base, "lien")
		if err := os.Symlink(reel, lien); err != nil {
			t.Skipf("liens symboliques indisponibles : %v", err)
		}
		c = Cible{
			DenHome: t.TempDir(), Layout: "central", Root: lien,
			Nest: "api.feat12", Worktree: "feat12", CheminRepo: repo,
		}
		chemin, err := Assure(context.Background(), NewGit(), c.Layout, c.Root, c.Worktree, c.CheminRepo)
		if err != nil {
			t.Fatalf("préparation : %v", err)
		}
		return repo, chemin, c
	}

	t.Run("Retire sur un enregistrement verrouille", func(t *testing.T) {
		repo, chemin, cible := prepare(t)
		git(t, repo, "worktree", "lock", chemin)
		if err := os.RemoveAll(chemin); err != nil {
			t.Fatal(err)
		}
		_, err := Retire(context.Background(), NewGit(), cible)
		if err == nil {
			t.Fatal("Retire ne doit pas prétendre avoir nettoyé un enregistrement verrouillé")
		}
		if !strings.Contains(err.Error(), "git worktree unlock") {
			t.Errorf("le message doit citer git worktree unlock ; obtenu : %v", err)
		}
	})

	t.Run("Assure sur un enregistrement orphelin", func(t *testing.T) {
		repo, chemin, _ := prepare(t)
		git(t, repo, "worktree", "lock", chemin)
		if err := os.RemoveAll(chemin); err != nil {
			t.Fatal(err)
		}
		_, err := Assure(context.Background(), NewGit(), "central", filepath.Dir(filepath.Dir(chemin)), "feat12", repo)
		if err == nil {
			t.Fatal("un enregistrement survivant doit produire une erreur")
		}
		if strings.Contains(err.Error(), "fatal:") {
			t.Errorf("le message ne doit pas relayer le fatal anglais de git ; obtenu : %v", err)
		}
		if !strings.Contains(err.Error(), "den rm") {
			t.Errorf("le message doit orienter vers den rm ; obtenu : %v", err)
		}
	})
}

// En -z, un renommage occupe deux enregistrements et le second, le chemin
// source, n'a pas de préfixe d'état. Non consommé, il est relu comme une entrée
// à part entière dès que son 3e caractère est une espace — et den nomme alors à
// l'utilisateur un fichier qui n'existe pas.
func TestRetireNInventePasDeFichierSurUnRenommage(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, "ab cd.txt"), "contenu\n")
	git(t, chemin, "add", "ab cd.txt")
	git(t, chemin, "commit", "-m", "ajoute ab cd.txt")
	git(t, chemin, "mv", "ab cd.txt", "xy.txt")

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("un renommage non commité est une modification : refus attendu")
	}
	if !strings.Contains(err.Error(), "xy.txt") {
		t.Errorf("le message doit nommer le fichier renommé ; obtenu : %v", err)
	}
	// « cd.txt » est ce que produit la relecture du chemin source « ab cd.txt ».
	if strings.Contains(err.Error(), "cd.txt") {
		t.Errorf("le message invente un fichier issu du chemin source ; obtenu : %v", err)
	}
}

// Sur macOS, /var et $TMPDIR sont des liens symboliques : worktree_root y est
// atteint par un lien en pratique. git répond avec le chemin RÉSOLU là où den
// manipule celui qu'on lui a donné — sans résolution, l'idempotence et la
// suppression cassent toutes les deux. Linux ne peut pas voir la régression
// tout seul, /tmp n'y étant pas un lien.
func TestAssureSuitUnWorktreeRootParLienSymbolique(t *testing.T) {
	repo := depotTest(t, "api")
	base := t.TempDir()
	reel := filepath.Join(base, "reel")
	if err := os.MkdirAll(reel, 0o755); err != nil {
		t.Fatal(err)
	}
	lien := filepath.Join(base, "lien")
	if err := os.Symlink(reel, lien); err != nil {
		t.Skipf("liens symboliques indisponibles : %v", err)
	}

	premier, err := Assure(context.Background(), NewGit(), "central", lien, "feat12", repo)
	if err != nil {
		t.Fatalf("premier appel : %v", err)
	}
	second, err := Assure(context.Background(), NewGit(), "central", lien, "feat12", repo)
	if err != nil {
		t.Fatalf("l'idempotence doit tenir à travers le lien : %v", err)
	}
	if premier != second {
		t.Errorf("chemins divergents : %q puis %q", premier, second)
	}
	cible := Cible{
		DenHome: t.TempDir(), Layout: "central", Root: lien,
		Nest: "api.feat12", Worktree: "feat12", CheminRepo: repo,
	}
	if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
		t.Errorf("la suppression doit tenir à travers le lien : %v", err)
	}
}

// ── La corbeille ─────────────────────────────────────────────────────────────
//
// Cinq tours de relecture de la tâche 10 ont prouvé que l'énumération des
// façons dont git cache du travail ne converge pas, et que le filet de
// `git worktree remove` tombe AVEC celui de `git status` puisque c'est le même
// code. La réponse n'est pas un verdict de plus : c'est de ne jamais supprimer.

// lisEntrees rend les noms des entrées d'un dossier, ou nil s'il n'existe pas.
func lisEntrees(t *testing.T, dir string) []string {
	t.Helper()
	entrees, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("lecture de %s : %v", dir, err)
	}
	var noms []string
	for _, e := range entrees {
		noms = append(noms, e.Name())
	}
	slices.Sort(noms)
	return noms
}

// La propriété structurelle : le dossier n'est pas détruit, il est déplacé.
func TestRetireMetLeWorktreeALaCorbeille(t *testing.T) {
	repo, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, "suivi.txt"), "commité\n")
	git(t, chemin, "add", "suivi.txt")
	git(t, chemin, "commit", "-m", "ajoute suivi.txt")

	dest, err := Retire(context.Background(), NewGit(), cible)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if _, err := os.Stat(chemin); !os.IsNotExist(err) {
		t.Errorf("le worktree doit avoir quitté %s ; obtenu : %v", chemin, err)
	}
	corbeille := filepath.Join(cible.DenHome, "trash")
	if dest == "" {
		t.Fatal("Retire doit rendre le chemin de l'entrée de corbeille")
	}
	if filepath.Dir(dest) != corbeille {
		t.Errorf("l'entrée doit vivre sous %s ; obtenu %s", corbeille, dest)
	}
	// Le nom doit permettre à l'utilisateur de retrouver SON dossier parmi
	// d'autres : sans le nest ni le repo, la corbeille est un tas anonyme.
	for _, attendu := range []string{cible.Nest, filepath.Base(repo)} {
		if !strings.Contains(filepath.Base(dest), attendu) {
			t.Errorf("le nom de l'entrée doit contenir %q ; obtenu %q", attendu, filepath.Base(dest))
		}
	}
	if contenu, err := os.ReadFile(filepath.Join(dest, "suivi.txt")); err != nil || string(contenu) != "commité\n" {
		t.Errorf("le contenu doit avoir suivi le déplacement ; lu %q, err %v", contenu, err)
	}

	// L'enregistrement git doit être élagué, sans quoi Assure refuserait pour
	// toujours de recréer ce worktree.
	if liste := git(t, repo, "worktree", "list"); strings.Contains(liste, chemin) {
		t.Errorf("l'enregistrement doit être élagué, obtenu :\n%s", liste)
	}
	if _, err := Assure(context.Background(), NewGit(), cible.Layout, cible.Root, cible.Worktree, cible.CheminRepo); err != nil {
		t.Errorf("le re-spawn doit repartir après la mise à la corbeille : %v", err)
	}
	// La branche survit : les commits de l'utilisateur ne sont pas en jeu.
	if _, err := gitTolerant(t, repo, "show-ref", "--verify", "refs/heads/"+cible.Worktree); err != nil {
		t.Errorf("la branche %s doit survivre : %v", cible.Worktree, err)
	}
}

// LE cas que la corbeille existe pour rendre réversible : --force ne perd plus
// rien. Un fichier IGNORÉ est le pire des trois — invisible à `git status`, au
// filet de `git worktree remove`, et absent de l'object store.
func TestRetireAvecForceConserveLeTravailDansLaCorbeille(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, ".gitignore"), ".env\n")
	git(t, chemin, "add", ".gitignore")
	git(t, chemin, "commit", "-m", "ignore .env")
	ecris(t, filepath.Join(chemin, ".env"), "SECRET=hunter2\n")
	ecris(t, filepath.Join(chemin, "brouillon.txt"), "TRAVAIL IRREMPLACABLE\n")

	dest, err := Retire(context.Background(), NewGit(), avecForce(cible, true))
	if err != nil {
		t.Fatalf("avec force, la mise à la corbeille doit passer : %v", err)
	}
	for _, cas := range []struct{ nom, contenu string }{
		{".env", "SECRET=hunter2\n"},
		{"brouillon.txt", "TRAVAIL IRREMPLACABLE\n"},
	} {
		lu, err := os.ReadFile(filepath.Join(dest, cas.nom))
		if err != nil || string(lu) != cas.contenu {
			t.Errorf("%s doit être récupérable dans %s ; lu %q, err %v", cas.nom, dest, lu, err)
		}
	}
}

// den_home et worktree_root peuvent vivre sur des systèmes de fichiers
// différents : os.Rename échoue alors en EXDEV. Recopier à la main un gros
// worktree serait long et interruptible — la corbeille bascule sous
// worktree_root, qui porte le worktree donc partage forcément son système.
func TestRetireRepliQuandLaCorbeilleEstSurUnAutreSysteme(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ailleurs, err := os.MkdirTemp("/dev/shm", "den-corbeille-")
	if err != nil {
		t.Skipf("pas de second système de fichiers accessible : %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ailleurs) })
	if peripherique(t, ailleurs) == peripherique(t, chemin) {
		t.Skip("/dev/shm est sur le même système de fichiers que le worktree")
	}
	cible.DenHome = ailleurs
	ecris(t, filepath.Join(chemin, "brouillon.txt"), "TRAVAIL IRREMPLACABLE\n")

	dest, err := Retire(context.Background(), NewGit(), avecForce(cible, true))
	if err != nil {
		t.Fatalf("un den_home sur un autre système ne doit pas empêcher la corbeille : %v", err)
	}
	if filepath.Dir(dest) != filepath.Join(cible.Root, ".trash") {
		t.Errorf("le repli doit vivre sous %s ; obtenu %s", filepath.Join(cible.Root, ".trash"), dest)
	}
	if lu, err := os.ReadFile(filepath.Join(dest, "brouillon.txt")); err != nil ||
		string(lu) != "TRAVAIL IRREMPLACABLE\n" {
		t.Errorf("le travail doit être récupérable ; lu %q, err %v", lu, err)
	}
	if _, err := os.Stat(chemin); !os.IsNotExist(err) {
		t.Errorf("le worktree doit avoir quitté %s ; obtenu : %v", chemin, err)
	}
}

func peripherique(t *testing.T, chemin string) uint64 {
	t.Helper()
	infos, err := os.Stat(chemin)
	if err != nil {
		t.Fatalf("stat de %s : %v", chemin, err)
	}
	st, ok := infos.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skipf("numéro de périphérique indisponible pour %s", chemin)
	}
	return uint64(st.Dev)
}

// Une corbeille sans rétention est une fuite de disque. La purge se fait sur
// l'HORODATAGE DU NOM — celui que den a posé — et jamais sur un nom qu'elle ne
// sait pas lire : un dossier que l'utilisateur a rangé là ne doit pas partir.
func TestRetirePurgeLesEntreesTropAnciennes(t *testing.T) {
	_, _, cible := prepareWorktree(t)
	corbeille := filepath.Join(cible.DenHome, "trash")
	if err := os.MkdirAll(corbeille, 0o755); err != nil {
		t.Fatal(err)
	}
	vieux := time.Now().Add(-retentionCorbeille - 24*time.Hour).Format(formatHorodatage)
	recent := time.Now().Add(-24 * time.Hour).Format(formatHorodatage)

	aPurger := vieux + "-api.feat12-api"
	aGarder := []string{
		recent + "-api.feat12-api",
		// Arrêté par la garde de FORME : son octet 15 n'est pas un « - ».
		"rangé-à-la-main",
		// FRANCHIT la garde de forme (octet 15 = « - ») mais son préfixe n'est
		// pas une date. C'est le seul témoin qui atteigne réellement le parsing,
		// et donc le seul qui tienne son fail-closed : sans lui, ignorer
		// l'erreur de ParseInLocation laisse « quand » au temps zéro, l'écart
		// dépasse la rétention, et le dossier rangé par l'utilisateur est effacé.
		"sauvegarde-2026-07-28",
	}
	for _, nom := range append([]string{aPurger}, aGarder...) {
		if err := os.MkdirAll(filepath.Join(corbeille, nom), 0o755); err != nil {
			t.Fatal(err)
		}
		ecris(t, filepath.Join(corbeille, nom, "marqueur"), "x\n")
	}
	// Un FICHIER au nom parfaitement horodaté : ce n'est pas une entrée de
	// corbeille, et la purge ne doit pas s'en saisir.
	fichierTemoin := vieux + "-fichier-range"
	ecris(t, filepath.Join(corbeille, fichierTemoin), "note de l'utilisateur\n")
	aGarder = append(aGarder, fichierTemoin)

	// Préparation : le témoin de parsing franchit bien la garde de forme, sans
	// quoi il ne prouverait rien de plus que « rangé-à-la-main ».
	if n := "sauvegarde-2026-07-28"; n[len(formatHorodatage)] != '-' {
		t.Fatalf("préparation : %q doit porter un « - » en position %d, il porte %q",
			n, len(formatHorodatage), n[len(formatHorodatage)])
	}

	if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	restant := lisEntrees(t, corbeille)
	if slices.Contains(restant, aPurger) {
		t.Errorf("l'entrée expirée %q doit être purgée ; reste : %v", aPurger, restant)
	}
	for _, garde := range aGarder {
		if !slices.Contains(restant, garde) {
			t.Errorf("l'entrée %q ne doit PAS être purgée ; reste : %v", garde, restant)
		}
	}
}

// `git worktree lock` existe pour les volumes amovibles et les montages réseau.
// git refuse d'y toucher ; den doit refuser AVANT de déplacer, sans quoi il
// laisse un dossier en corbeille ET un enregistrement que `prune` saute
// silencieusement.
func TestRetireRefuseUnWorktreeVerrouilleEncorePresent(t *testing.T) {
	repo, chemin, cible := prepareWorktree(t)
	git(t, repo, "worktree", "lock", chemin)

	_, err := Retire(context.Background(), NewGit(), avecForce(cible, true))
	if err == nil {
		t.Fatal("un worktree verrouillé ne doit pas être mis à la corbeille")
	}
	if !strings.Contains(err.Error(), "git worktree unlock") {
		t.Errorf("le message doit citer git worktree unlock ; obtenu : %v", err)
	}
	if _, err := os.Stat(chemin); err != nil {
		t.Errorf("le worktree verrouillé doit être INTACT à sa place : %v", err)
	}
	if noms := lisEntrees(t, filepath.Join(cible.DenHome, "trash")); len(noms) != 0 {
		t.Errorf("rien ne doit avoir été déplacé ; corbeille : %v", noms)
	}
}

// Sans den_home, il n'y a pas de corbeille — et sans corbeille, den ne touche à
// rien. C'est un appelant fautif, pas un cas utilisateur : le refus doit le
// nommer plutôt que d'inventer un emplacement.
func TestRetireRefuseSansDenHome(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	cible.DenHome = ""
	// SALE à dessein : tant que la garde arrivait après le contrôle de saleté,
	// l'utilisateur recevait le message « … l'envoyer à la corbeille trash »,
	// qui nomme un chemin relatif ne désignant rien.
	ecris(t, filepath.Join(chemin, "brouillon.txt"), "wip\n")

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("une Cible sans DenHome doit être refusée")
	}
	if !strings.Contains(err.Error(), "den_home") {
		t.Errorf("le refus doit nommer la cause réelle ; obtenu : %v", err)
	}
	if strings.Contains(err.Error(), "corbeille trash") {
		t.Errorf("le message nomme une corbeille qui n'existe pas ; obtenu : %v", err)
	}
	if _, err := os.Stat(chemin); err != nil {
		t.Errorf("le worktree doit être INTACT : %v", err)
	}
}

// Deux mises à la corbeille du même nest et du même repo dans la même seconde
// portent le même nom calculé. Testé sur la fonction plutôt que sur deux appels
// de Retire : la collision ne se produirait sinon que si l'horloge veut bien.
func TestEntreeLibreEviteLesCollisions(t *testing.T) {
	corbeille := t.TempDir()
	occupe := filepath.Join(corbeille, "20260728-184530-api.feat12-api")
	if err := os.MkdirAll(occupe, 0o755); err != nil {
		t.Fatal(err)
	}
	ecris(t, filepath.Join(occupe, "deja-la"), "x\n")

	libre, err := entreeLibre(corbeille, "20260728-184530-api.feat12-api")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if libre == occupe {
		t.Fatalf("entreeLibre rend un chemin déjà occupé : %s", libre)
	}
	if _, err := os.Stat(libre); !os.IsNotExist(err) {
		t.Errorf("%s doit être libre ; obtenu : %v", libre, err)
	}
	// Le nom d'origine doit rester reconnaissable : un suffixe, pas un autre nom.
	if !strings.HasPrefix(filepath.Base(libre), "20260728-184530-api.feat12-api") {
		t.Errorf("le nom doit rester reconnaissable ; obtenu %q", filepath.Base(libre))
	}
}

// ── Correctness : les gardes que la tâche 10 a laissées non pinnées ──────────

// commence dit si l'argv d'un appel git débute par ce préfixe.
func commence(args, prefixe []string) bool {
	return len(args) >= len(prefixe) && slices.Equal(args[:len(prefixe)], prefixe)
}

// gitDefaillantSur échoue sur un PRÉFIXE d'arguments, là où gitDefaillant ne
// sait pas distinguer `ls-files -v` de `ls-files -s` — si bien que la sonde
// d'index tombait toujours la première et que l'erreur d'empreintesIndex
// restait inatteignable.
type gitDefaillantSur struct {
	reel    Git
	prefixe []string
}

func (g gitDefaillantSur) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunAvecEntree(ctx, dir, nil, args...)
}

func (g gitDefaillantSur) RunAvecEntree(ctx context.Context, dir string, entree []byte, args ...string) ([]byte, error) {
	if commence(args, g.prefixe) {
		return nil, errors.New("panne simulée de git " + strings.Join(g.prefixe, " "))
	}
	return g.reel.RunAvecEntree(ctx, dir, entree, args...)
}

// gitForge répond une sortie choisie à une sous-commande et délègue le reste.
// Il ne sert QU'À décrire des sorties que le vrai git produit mais qu'aucune
// fixture ne provoque de façon fiable — jamais à remplacer une observation.
type gitForge struct {
	reel    Git
	prefixe []string
	sortie  []byte
}

func (g gitForge) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunAvecEntree(ctx, dir, nil, args...)
}

func (g gitForge) RunAvecEntree(ctx context.Context, dir string, entree []byte, args ...string) ([]byte, error) {
	if commence(args, g.prefixe) {
		return g.sortie, nil
	}
	return g.reel.RunAvecEntree(ctx, dir, entree, args...)
}

// Symétrique de …RefuseUnFichierRemplaceParUnLienDeMemeTexte : l'index attend un
// LIEN (120000), le disque porte un fichier RÉGULIER dont le contenu vaut
// exactement le texte du lien. Les empreintes coïncident alors — git hache le
// texte du lien comme contenu du blob — et le fichier était détruit sans un mot.
// liensModifies contrôlait le mode ; cheminsModifies l'ignorait.
func TestRetireRefuseUnLienRemplaceParUnFichierDeMemeTexte(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, "cible.txt"), "CONTENU\n")
	if err := os.Symlink("cible.txt", filepath.Join(chemin, "lien.txt")); err != nil {
		t.Skipf("liens symboliques indisponibles : %v", err)
	}
	git(t, chemin, "add", ".")
	git(t, chemin, "commit", "-m", "cible et lien")
	git(t, chemin, "update-index", "--skip-worktree", "lien.txt")

	// Le lien devient un fichier régulier portant EXACTEMENT le texte du lien.
	if err := os.Remove(filepath.Join(chemin, "lien.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chemin, "lien.txt"), []byte("cible.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	// git ne rapporte rien : c'est tout le problème, et c'est ce qui empêche un
	// tiers de rendre ce test vert à la place de den.
	if etat := git(t, chemin, "status", "--porcelain"); etat != "" {
		t.Fatalf("préparation : git ne devrait rien rapporter, obtenu %q", etat)
	}

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("un lien suivi devenu fichier régulier est une modification, même à contenu égal")
	}
	if !strings.Contains(err.Error(), "lien.txt") {
		t.Errorf("le message doit nommer lien.txt ; obtenu : %v", err)
	}
}

// Renommage détecté dans l'ARBRE DE TRAVAIL (colonne Y) : `mv` puis `git add -N`
// le produit avec du vrai git. La consommation du chemin source ne regardait que
// la colonne X, si bien que « ab cd.txt » était relu comme une entrée à part
// entière — den nommait alors « cd.txt », un fichier qui n'existe pas.
func TestRetireNInventePasDeFichierSurUnRenommageDansLArbre(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, "ab cd.txt"), "contenu assez long pour la detection de renommage\n")
	git(t, chemin, "add", "ab cd.txt")
	git(t, chemin, "commit", "-m", "ajoute ab cd.txt")
	if err := os.Rename(filepath.Join(chemin, "ab cd.txt"), filepath.Join(chemin, "xy.txt")); err != nil {
		t.Fatal(err)
	}
	git(t, chemin, "add", "-N", "xy.txt")

	// Préparation : c'est bien la colonne Y que git remplit ici, pas la X.
	// gitBrut et non git : ce dernier TrimSpace, ce qui effacerait justement
	// l'espace de tête qui distingue «  R » de « R ».
	if etat := gitBrut(t, chemin, "status", "--porcelain"); !strings.HasPrefix(etat, " R ") {
		t.Skipf("git ne produit pas de renommage en colonne Y ici : %q", etat)
	}

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("un renommage non commité est une modification : refus attendu")
	}
	if !strings.Contains(err.Error(), "xy.txt") {
		t.Errorf("le message doit nommer le fichier renommé ; obtenu : %v", err)
	}
	if strings.Contains(err.Error(), "cd.txt") {
		t.Errorf("le message invente un fichier issu du chemin source ; obtenu : %v", err)
	}
}

// empreinteBidon a la forme d'une empreinte sans désigner aucun objet : ce qui
// compte dans ces tests est la DÉCISION de den, pas ce que git en ferait.
var empreinteBidon = strings.Repeat("0", 40)

// Le sens du doute sur le chemin des liens. Ces branches sont fail-closed dans
// le vrai code, mais aucune ne rougissait : rien n'empêchait qu'elles dérivent.
func TestLiensModifiesEstFailClosed(t *testing.T) {
	_, chemin, _ := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, "pas-un-lien.txt"), "contenu\n")
	if err := os.Symlink("cible.txt", filepath.Join(chemin, "lien.txt")); err != nil {
		t.Skipf("liens symboliques indisponibles : %v", err)
	}

	// Readlink échoue (EINVAL sur un fichier régulier) : rendre « rien de
	// modifié » laisserait passer un chemin que den n'a pas pu comparer.
	t.Run("chemin illisible comme lien", func(t *testing.T) {
		index := map[string]entreeIndex{"pas-un-lien.txt": {mode: modeLien, empreinte: empreinteBidon}}
		modifies, err := liensModifies(context.Background(), NewGit(), chemin, index, []string{"pas-un-lien.txt"})
		if err == nil {
			t.Fatalf("un lien illisible doit produire une erreur ; obtenu modifies=%v", modifies)
		}
		if !strings.Contains(err.Error(), "pas-un-lien.txt") {
			t.Errorf("le message doit nommer le chemin ; obtenu : %v", err)
		}
	})

	// L'index ne connaît pas ce chemin : den n'a rien à quoi comparer, donc il
	// le compte pour sale. La branche existe, mais rien ne la tenait.
	t.Run("absent de l'index", func(t *testing.T) {
		modifies, err := liensModifies(context.Background(), NewGit(), chemin, map[string]entreeIndex{}, []string{"lien.txt"})
		if err != nil {
			t.Fatalf("erreur inattendue : %v", err)
		}
		if !slices.Contains(modifies, "lien.txt") {
			t.Errorf("un lien absent de l'index doit compter pour modifié ; obtenu %v", modifies)
		}
	})

	// L'index attend un fichier régulier là où le disque porte un lien : c'est
	// une modification, quel que soit le contenu.
	t.Run("mode d'index inattendu", func(t *testing.T) {
		index := map[string]entreeIndex{"lien.txt": {mode: "100644", empreinte: empreinteBidon}}
		modifies, err := liensModifies(context.Background(), NewGit(), chemin, index, []string{"lien.txt"})
		if err != nil {
			t.Fatalf("erreur inattendue : %v", err)
		}
		if !slices.Contains(modifies, "lien.txt") {
			t.Errorf("un mode d'index inattendu doit compter pour modifié ; obtenu %v", modifies)
		}
	})
}

// Mêmes gardes du côté des fichiers réguliers.
func TestCheminsModifiesEstFailClosed(t *testing.T) {
	_, chemin, _ := prepareWorktree(t)
	for _, f := range []string{"a.txt", "b.txt"} {
		ecris(t, filepath.Join(chemin, f), "contenu de "+f+"\n")
	}

	// Ce que ce cas pin est le COMPORTEMENT (« inconnu ⇒ modifié »), pas une
	// branche : deux conditions le couvrent, le mode vide et l'empreinte vide.
	// La dérive qu'il attrape est celle qui compte — écarter un chemin que
	// l'index ne connaît pas au lieu de le compter pour sale.
	t.Run("absent de l'index", func(t *testing.T) {
		modifies, err := cheminsModifies(context.Background(), NewGit(), chemin, map[string]entreeIndex{}, []string{"a.txt"})
		if err != nil {
			t.Fatalf("erreur inattendue : %v", err)
		}
		if !slices.Contains(modifies, "a.txt") {
			t.Errorf("un chemin absent de l'index doit compter pour modifié ; obtenu %v", modifies)
		}
	})

	// Sans la garde de longueur, l'alignement argv↔sortie est rompu et den
	// indexe hors des bornes du lot : une panique, pas un refus.
	t.Run("sonde qui rend trop peu d'empreintes", func(t *testing.T) {
		g := gitForge{reel: NewGit(), prefixe: []string{"hash-object"}, sortie: []byte(empreinteBidon + "\n")}
		index := map[string]entreeIndex{
			"a.txt": {mode: "100644", empreinte: empreinteBidon},
			"b.txt": {mode: "100644", empreinte: empreinteBidon},
		}
		modifies, err := cheminsModifies(context.Background(), g, chemin, index, []string{"a.txt", "b.txt"})
		if err == nil {
			t.Fatalf("une sonde désalignée doit produire une erreur ; obtenu modifies=%v", modifies)
		}
		for _, attendu := range []string{"1", "2"} {
			if !strings.Contains(err.Error(), attendu) {
				t.Errorf("le message doit chiffrer le désalignement (%q) ; obtenu : %v", attendu, err)
			}
		}
	})
}

// `ls-files -s` et `ls-files -v` sont deux sondes distinctes que gitDefaillant
// ne savait pas séparer : la seconde tombait toujours la première, et l'erreur
// d'empreintesIndex n'était atteignable par aucun test.
func TestRetireRefuseQuandLIndexEstMuet(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, "local.conf"), "v1\n")
	git(t, chemin, "add", "local.conf")
	git(t, chemin, "commit", "-m", "ajoute local.conf")
	git(t, chemin, "update-index", "--skip-worktree", "local.conf")

	g := gitDefaillantSur{reel: NewGit(), prefixe: []string{"ls-files", "-s"}}
	_, err := Retire(context.Background(), g, cible)
	if err == nil {
		t.Fatal("une panne de `git ls-files -s` ne doit pas autoriser la mise à la corbeille")
	}
	if !strings.Contains(err.Error(), "panne simulée") {
		t.Errorf("la panne doit remonter telle quelle ; obtenu : %v", err)
	}
	if _, err := os.Stat(chemin); err != nil {
		t.Errorf("le worktree doit être INTACT : %v", err)
	}
}

// Les gardes de parsing SAUTAIENT l'enregistrement illisible. Sauter, c'est
// perdre une entrée sale sans le dire — la direction fail-open exacte que tout
// le module refuse ailleurs.
func TestSondesRefusentUneSortieIllisible(t *testing.T) {
	_, chemin, _ := prepareWorktree(t)
	prefixeStatus := []string{"-c", "core.fsmonitor=", "status"}

	t.Run("status tronqué", func(t *testing.T) {
		for _, sortie := range []string{"XY", "XYZfichier.txt\x00", " M\x00"} {
			g := gitForge{reel: NewGit(), prefixe: prefixeStatus, sortie: []byte(sortie)}
			sales, err := fichiersSales(context.Background(), g, chemin)
			if err == nil {
				t.Errorf("sortie %q : un enregistrement illisible doit produire une erreur ; obtenu %v", sortie, sales)
			}
		}
	})

	t.Run("ls-files tronqué", func(t *testing.T) {
		for _, sortie := range []string{"H", "Hchemin.txt\x00"} {
			g := gitForge{reel: NewGit(), prefixe: []string{"ls-files", "-v"}, sortie: []byte(sortie)}
			marques, err := fichiersMarques(context.Background(), g, chemin)
			if err == nil {
				t.Errorf("sortie %q : un enregistrement illisible doit produire une erreur ; obtenu %v", sortie, marques)
			}
		}
	})

	// Contrepartie indispensable : la sortie d'un worktree PROPRE est vide, et
	// une sortie bien formée se termine par un NUL — donc par un enregistrement
	// vide. Refuser dessus rendrait `den rm` inutilisable.
	// Ce sous-cas doit ASSERTER LE CONTENU, pas seulement l'absence d'erreur :
	// si le préfixe de la forge cessait de correspondre aux arguments réels, le
	// vrai git tournerait sur un worktree propre, ne rendrait aucune erreur, et
	// le test passerait en prouvant zéro.
	t.Run("sorties légitimes", func(t *testing.T) {
		cas := []struct {
			sortie  string
			attendu []string
		}{
			{"", nil},
			{"?? brouillon.txt\x00", []string{"brouillon.txt"}},
			{"R  neuf.txt\x00ancien.txt\x00", []string{"neuf.txt"}},
		}
		for _, c := range cas {
			g := gitForge{reel: NewGit(), prefixe: prefixeStatus, sortie: []byte(c.sortie)}
			sales, err := fichiersSales(context.Background(), g, chemin)
			if err != nil {
				t.Errorf("sortie %q : erreur inattendue : %v", c.sortie, err)
				continue
			}
			if !slices.Equal(sales, c.attendu) {
				t.Errorf("sortie %q : sales = %v, attendu %v", c.sortie, sales, c.attendu)
			}
		}
	})
}

// ── Performance : un appel pour tout le lot, pas un fork par entrée ─────────

// empreinteBlob remplace un fork `cat-file blob` par lien marqué. Le seul juge
// recevable de sa justesse est git lui-même : on compare à `git hash-object`,
// pas à une valeur recopiée dans le test.
func TestEmpreinteBlobCoincideAvecGit(t *testing.T) {
	_, chemin, _ := prepareWorktree(t)
	textes := []string{
		"cible.txt",
		"",
		"cible.txt ",      // espace FINALE : un TrimSpace la mangerait
		"a\nb",            // retour à la ligne INTERNE
		" \t bord \t ",    // espaces des deux côtés
		"données/été.txt", // non-ASCII
		strings.Repeat("x", 300),
	}
	for _, texte := range textes {
		cmd := exec.Command("git", "hash-object", "-t", "blob", "--no-filters", "--stdin")
		cmd.Dir = chemin
		cmd.Stdin = strings.NewReader(texte)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git hash-object %q : %v\n%s", texte, err, out)
		}
		attendu := strings.TrimSpace(string(out))

		got, err := empreinteBlob(texte, len(attendu))
		if err != nil {
			t.Fatalf("empreinteBlob(%q) : %v", texte, err)
		}
		if got != attendu {
			t.Errorf("empreinteBlob(%q) = %s, git dit %s", texte, got, attendu)
		}
	}
}

// Un dépôt en SHA-256 porte des empreintes de 64 caractères. Parier sur SHA-1
// rendrait « modifié » pour toujours ; une longueur inconnue doit donc être une
// erreur, pas un choix par défaut.
func TestEmpreinteBlobRefuseUnFormatInconnu(t *testing.T) {
	for _, longueur := range []int{0, 32, 41, 128} {
		if _, err := empreinteBlob("cible.txt", longueur); err == nil {
			t.Errorf("une empreinte de %d caractères doit être refusée", longueur)
		}
	}
	// SHA-256 doit passer, et donner autre chose que le SHA-1.
	sha1, err := empreinteBlob("cible.txt", 40)
	if err != nil {
		t.Fatal(err)
	}
	sha256, err := empreinteBlob("cible.txt", 64)
	if err != nil {
		t.Fatalf("un dépôt SHA-256 doit être supporté : %v", err)
	}
	if len(sha1) != 40 || len(sha256) != 64 || sha1 == sha256 {
		t.Errorf("empreintes incohérentes : %q et %q", sha1, sha256)
	}
}

// La contrepartie du test précédent : un lien MARQUÉ dont la cible a changé
// doit être refusé. Sans lui, « toujours propre » passerait les fixtures de
// liens intacts sans qu'aucune assertion ne bronche.
func TestRetireRefuseUnLienMarqueRepointe(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, "a.txt"), "A\n")
	ecris(t, filepath.Join(chemin, "b.txt"), "B\n")
	if err := os.Symlink("a.txt", filepath.Join(chemin, "lien.txt")); err != nil {
		t.Skipf("liens symboliques indisponibles : %v", err)
	}
	git(t, chemin, "add", ".")
	git(t, chemin, "commit", "-m", "cibles et lien")
	git(t, chemin, "update-index", "--skip-worktree", "lien.txt")

	if err := os.Remove(filepath.Join(chemin, "lien.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b.txt", filepath.Join(chemin, "lien.txt")); err != nil {
		t.Fatal(err)
	}
	if etat := git(t, chemin, "status", "--porcelain"); etat != "" {
		t.Fatalf("préparation : git ne devrait rien rapporter, obtenu %q", etat)
	}

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("un lien marqué repointé ailleurs est une modification : refus attendu")
	}
	if !strings.Contains(err.Error(), "lien.txt") {
		t.Errorf("le message doit nommer lien.txt ; obtenu : %v", err)
	}
}

// La question « ce dossier est-il lui-même ignoré ? » se pose à git UNE fois
// pour tout le lot. Un fork par entrée coûtait 183 ms pour 300 dossiers et
// 545 ms pour 1 000 — croissance linéaire sans borne.
func TestRetireInterrogeGitUneSeuleFoisSurLesDossiersIgnores(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	var regles strings.Builder
	for _, d := range []string{"cache1", "cache2", "cache3"} {
		regles.WriteString(d + "/\n")
		if err := os.MkdirAll(filepath.Join(chemin, d), 0o755); err != nil {
			t.Fatal(err)
		}
		ecris(t, filepath.Join(chemin, d, "x.bin"), "cache\n")
	}
	ecris(t, filepath.Join(chemin, ".gitignore"), regles.String())
	git(t, chemin, "add", ".gitignore")
	git(t, chemin, "commit", "-m", "ignore les caches")

	espion := &gitEspion{reel: NewGit()}
	if _, err := Retire(context.Background(), espion, cible); err != nil {
		t.Fatalf("trois dossiers ignorés en bloc ne doivent pas bloquer : %v", err)
	}
	if n := espion.compte("check-ignore"); n != 1 {
		t.Errorf("check-ignore doit être appelé une seule fois pour les 3 dossiers ; obtenu %d ; appels : %v",
			n, espion.appels)
	}
}

// ── Fix round 1 : les gardes justes que rien ne tenait ──────────────────────

// I-1. `check-ignore` répond par un SOUS-ENSEMBLE du lot, jamais par un
// alignement positionnel. Aucun test ne mélangeait les deux réponses : deux
// n'avaient qu'un candidat non ignoré (rc=1, table vide), le troisième que des
// candidats ignorés. « rc=0 ⇒ tout le lot est jetable » passait donc la suite
// entière — et envoyait le secret à la corbeille SANS refus.
func TestRetireDistingueDeuxDossiersIgnoresDuMemeLot(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	ecris(t, filepath.Join(chemin, ".gitignore"), "node_modules/\n*.env\n")
	git(t, chemin, "add", ".gitignore")
	git(t, chemin, "commit", "-m", "regles")
	for _, d := range []string{"node_modules", "conf"} {
		if err := os.MkdirAll(filepath.Join(chemin, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ecris(t, filepath.Join(chemin, "node_modules", "x.js"), "module.exports={}\n")
	secret := filepath.Join(chemin, "conf", "prod.env")
	ecris(t, secret, "SECRET=hunter2\n")

	// Préparation : les DEUX sortent collapsés en « !! », donc dans le même lot.
	// Sans cela, le test ne mélangerait rien et retomberait sur ce que les
	// anciens couvraient déjà.
	etat := gitBrut(t, chemin, "status", "--porcelain", "--ignored=traditional")
	for _, attendu := range []string{"!! conf/", "!! node_modules/"} {
		if !strings.Contains(etat, attendu) {
			t.Fatalf("préparation : %q attendu dans le status, obtenu :\n%s", attendu, etat)
		}
	}

	_, err := Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("un secret dans un dossier non ignoré doit être refusé, même à côté d'un cache jetable")
	}
	if !strings.Contains(err.Error(), "conf/") {
		t.Errorf("le message doit nommer conf/ ; obtenu : %v", err)
	}
	// L'autre moitié du discriminant : le cache régénérable ne doit PAS être
	// nommé, sans quoi « tout le lot est sale » passerait aussi bien.
	if strings.Contains(err.Error(), "node_modules") {
		t.Errorf("node_modules/ est du cache jetable et ne doit pas être nommé ; obtenu : %v", err)
	}
	if lu, err := os.ReadFile(secret); err != nil || string(lu) != "SECRET=hunter2\n" {
		t.Errorf("le secret doit être INTACT ; lu %q, err %v", lu, err)
	}
}

// I-3. La godoc d'estVerrouille affirme que l'abstention est le seul sens sûr
// avant un déplacement. Rien ne le tenait : sous « erreur ⇒ pas verrouillé »,
// Retire déplace un worktree dont il ignore l'état et laisse un enregistrement
// vivant que `prune` saute en silence.
//
// Force est armé À DESSEIN : il court-circuite fichiersSales, si bien que seule
// la garde de verrou peut encore produire le refus.
func TestRetireRefuseQuandLeVerrouEstIndecidable(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)

	g := gitDefaillantSur{reel: NewGit(), prefixe: []string{"worktree", "list"}}
	_, err := Retire(context.Background(), g, avecForce(cible, true))
	if err == nil {
		t.Fatal("un verrou indécidable ne doit pas autoriser la mise à la corbeille")
	}
	if !strings.Contains(err.Error(), "panne simulée") {
		t.Errorf("la panne doit remonter telle quelle ; obtenu : %v", err)
	}
	if _, err := os.Stat(chemin); err != nil {
		t.Errorf("le worktree doit être INTACT à sa place : %v", err)
	}
	if noms := lisEntrees(t, filepath.Join(cible.DenHome, "trash")); len(noms) != 0 {
		t.Errorf("rien ne doit avoir été déplacé ; corbeille : %v", noms)
	}
}

// I-4. Symétrique du précédent, sur la branche « dossier déjà disparu ».
// estEnregistre avalait l'erreur : Retire rendait alors (« », nil) et
// prétendait avoir nettoyé sans avoir rien pu vérifier — exactement ce que la
// revérification voisine existe pour empêcher.
func TestRetireNePretendPasAvoirNettoyeSansAvoirVerifie(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	if err := os.RemoveAll(chemin); err != nil {
		t.Fatal(err)
	}

	g := gitDefaillantSur{reel: NewGit(), prefixe: []string{"worktree", "list"}}
	_, err := Retire(context.Background(), g, cible)
	if err == nil {
		t.Fatal("Retire ne doit pas rendre nil quand il n'a pas pu vérifier l'enregistrement")
	}
	if !strings.Contains(err.Error(), "panne simulée") {
		t.Errorf("la panne doit remonter telle quelle ; obtenu : %v", err)
	}

	// Même discipline côté Assure, qui lit le même inventaire pour décider.
	if _, err := Assure(context.Background(), g, cible.Layout, cible.Root, cible.Worktree, cible.CheminRepo); err == nil {
		t.Error("Assure ne doit pas non plus repartir sans avoir pu lire l'inventaire")
	}
}

// M-1. Le nom du nest et le basename du repo composent le nom de l'entrée. Le
// commentaire de composantSur affirme qu'un « .. » ne peut pas désigner le
// parent de la corbeille ; rien ne le vérifiait.
func TestRetireNeSortPasDeLaCorbeille(t *testing.T) {
	_, _, cible := prepareWorktree(t)
	cible.Nest = "a/../../../evade"

	dest, err := Retire(context.Background(), NewGit(), cible)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	corbeille := filepath.Join(cible.DenHome, "trash")
	if filepath.Dir(dest) != corbeille {
		t.Errorf("l'entrée doit rester DANS %s ; obtenue en %s", corbeille, dest)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("l'entrée doit exister là où Retire l'annonce : %v", err)
	}
}

// M-2. Les deux gardes d'entreeLibre, dont le commentaire « Lstat et non Stat »
// n'était vérifié par rien.
func TestEntreeLibre(t *testing.T) {
	// Un lien symbolique CASSÉ occupe le nom tout autant : os.Stat dirait
	// « n'existe pas », et le rename suivant échouerait en ENOTDIR.
	t.Run("un lien pendant occupe le nom", func(t *testing.T) {
		dir := t.TempDir()
		occupe := filepath.Join(dir, "base")
		if err := os.Symlink("/n/existe/pas", occupe); err != nil {
			t.Skipf("liens symboliques indisponibles : %v", err)
		}
		libre, err := entreeLibre(dir, "base")
		if err != nil {
			t.Fatalf("erreur inattendue : %v", err)
		}
		if libre == occupe {
			t.Errorf("entreeLibre rend un nom occupé par un lien cassé : %s", libre)
		}
	})

	// Ne pas pouvoir inspecter n'est pas « c'est libre ».
	t.Run("inspection impossible", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root traverse les dossiers sans permission")
		}
		dir := filepath.Join(t.TempDir(), "verrouille")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

		if libre, err := entreeLibre(dir, "base"); err == nil {
			t.Errorf("une inspection impossible doit produire une erreur ; obtenu %q", libre)
		}
	})
}

// M-4. Le repli est réservé à EXDEV. Le sens positif était testé, pas le
// négatif : basculer sur le repli à la moindre erreur transformerait une panne
// en déplacement silencieux vers un emplacement que l'utilisateur n'attend pas.
func TestRetireNeReplieQueSurUnAutreSystemeDeFichiers(t *testing.T) {
	_, chemin, cible := prepareWorktree(t)
	// La corbeille principale est un FICHIER : MkdirAll échoue en ENOTDIR, pas
	// en EXDEV, et sur le MÊME système de fichiers que le repli.
	if err := os.MkdirAll(cible.DenHome, 0o755); err != nil {
		t.Fatal(err)
	}
	ecris(t, filepath.Join(cible.DenHome, "trash"), "pas un dossier\n")

	_, err := Retire(context.Background(), NewGit(), avecForce(cible, true))
	if err == nil {
		t.Fatal("une corbeille inutilisable doit produire une erreur")
	}
	if _, err := os.Stat(chemin); err != nil {
		t.Errorf("le worktree doit être INTACT : %v", err)
	}
	if noms := lisEntrees(t, filepath.Join(cible.Root, ".trash")); len(noms) != 0 {
		t.Errorf("le repli ne doit pas être tenté hors EXDEV ; obtenu : %v", noms)
	}
}

// M-6. empreinteBlob déduit l'algorithme de la longueur de l'empreinte d'index.
// La fonction était testée, le bout en bout non : rien ne prouvait qu'un dépôt
// réellement en SHA-256 traverse Retire.
func TestRetireAccepteUnLienIntactDansUnDepotSha256(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitTolerant(t, repo, "init", "-q", "-b", "main", "--object-format=sha256", "."); err != nil {
		t.Skipf("git ne sait pas créer de dépôt SHA-256 : %v", err)
	}
	git(t, repo, "config", "user.email", "test@exemple.test")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "commit", "--allow-empty", "-m", "initial")

	cible := Cible{
		DenHome: t.TempDir(), Layout: "central", Root: t.TempDir(),
		Nest: "api.feat12", Worktree: "feat12", CheminRepo: repo,
	}
	chemin, err := Assure(context.Background(), NewGit(), cible.Layout, cible.Root, cible.Worktree, cible.CheminRepo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	ecris(t, filepath.Join(chemin, "cible.txt"), "CONTENU\n")
	if err := os.Symlink("cible.txt", filepath.Join(chemin, "lien.txt")); err != nil {
		t.Skipf("liens symboliques indisponibles : %v", err)
	}
	git(t, chemin, "add", ".")
	git(t, chemin, "commit", "-m", "cible et lien")
	git(t, chemin, "update-index", "--skip-worktree", "lien.txt")

	// Préparation : c'est bien un dépôt SHA-256, donc des empreintes de 64
	// caractères. Sans ce contrôle, le test passerait sur un dépôt SHA-1 sans
	// rien exercer de neuf.
	index, err := empreintesIndex(context.Background(), NewGit(), chemin)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := index["lien.txt"]; !ok || len(e.empreinte) != 64 {
		t.Fatalf("préparation : empreinte de 64 caractères attendue, obtenu %+v", index["lien.txt"])
	}

	if _, err := Retire(context.Background(), NewGit(), cible); err != nil {
		t.Fatalf("un lien intact d'un dépôt SHA-256 ne doit pas bloquer la suppression : %v", err)
	}
}

// M-10. Le commentaire de fichiersMarques affirme qu'un gitlink marqué est
// refusé alors que `git status` ne rapporte rien. Mesuré à la main jusqu'ici,
// tenu par rien.
func TestRetireRefuseUnSousModuleMarque(t *testing.T) {
	base := t.TempDir()
	sous := filepath.Join(base, "sous")
	creeDepot(t, sous)
	ecris(t, filepath.Join(sous, "f.txt"), "contenu du sous-module\n")
	git(t, sous, "add", "f.txt")
	git(t, sous, "commit", "-m", "contenu")

	repo := filepath.Join(base, "api")
	creeDepot(t, repo)
	// protocol.file.allow : git refuse par défaut les sous-modules sur file://
	// (CVE-2022-39253). Le dépôt reste local, aucun accès réseau.
	if _, err := gitTolerant(t, repo, "-c", "protocol.file.allow=always",
		"submodule", "add", "-q", sous, "sm"); err != nil {
		t.Skipf("sous-modules indisponibles ici : %v", err)
	}
	git(t, repo, "commit", "-m", "ajoute le sous-module")

	cible := Cible{
		DenHome: t.TempDir(), Layout: "central", Root: t.TempDir(),
		Nest: "api.feat12", Worktree: "feat12", CheminRepo: repo,
	}
	chemin, err := Assure(context.Background(), NewGit(), cible.Layout, cible.Root, cible.Worktree, cible.CheminRepo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	git(t, chemin, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "-q")
	git(t, chemin, "update-index", "--skip-worktree", "sm")

	// git ne rapporte rien : c'est ce qui rend le refus de den observable.
	if etat := git(t, chemin, "status", "--porcelain"); etat != "" {
		t.Fatalf("préparation : git ne devrait rien rapporter, obtenu %q", etat)
	}

	_, err = Retire(context.Background(), NewGit(), cible)
	if err == nil {
		t.Fatal("un gitlink marqué doit être refusé : den ne sait pas ce qu'il porte")
	}
	if !strings.Contains(err.Error(), "sm") {
		t.Errorf("le message doit nommer sm ; obtenu : %v", err)
	}
}

func ecris(t *testing.T, chemin, contenu string) {
	t.Helper()
	if err := os.WriteFile(chemin, []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitTolerant rend l'erreur au lieu d'avorter : pour les commandes dont l'échec
// est une information (absence d'origin/HEAD, absence d'amont).
func gitTolerant(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// gitBrut ne rogne PAS la sortie : la colonne X de `git status --porcelain` est
// une espace pour tout ce qui n'a lieu que dans l'arbre de travail, et la rogner
// rend « \u00a0R » indiscernable de « R\u00a0».
func gitBrut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v dans %s : %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v dans %s : %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func brancheDe(t *testing.T, dir string) string {
	t.Helper()
	return git(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
}

func basculeSur(t *testing.T, dir, branche string) {
	t.Helper()
	git(t, dir, "checkout", "-b", branche)
}
