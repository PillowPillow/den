package worktree

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestMain neutralise la configuration git de la machine. Le module lit les
// règles d'ignorance pour décider d'une suppression, et un ~/.gitignore_global
// (celui de cette machine porte « .sbx ») rendrait les verdicts de saleté
// dépendants du poste qui lance la suite.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
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

func TestRetireSupprimeLeWorktree(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()
	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}

	if err := Retire(context.Background(), NewGit(), repo, chemin, false); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if _, err := os.Stat(chemin); !os.IsNotExist(err) {
		t.Errorf("le worktree doit avoir disparu de %s", chemin)
	}
}

// Spec §14 : refuser si dirty sans --force. Perdre du travail non commité
// serait le pire effet de bord possible pour un `den rm`.
func TestRetireRefuseUnWorktreeSale(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()
	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	if err := os.WriteFile(filepath.Join(chemin, "brouillon.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = Retire(context.Background(), NewGit(), repo, chemin, false)
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

	if err := Retire(context.Background(), NewGit(), repo, chemin, true); err != nil {
		t.Fatalf("avec force, la suppression doit passer : %v", err)
	}
}

// Idempotence de la suppression : `den rm` peut retomber sur un worktree déjà
// retiré à la main. Sans cette branche, git échouerait sur un chemin absent.
func TestRetireSurUnWorktreeDejaAbsentEstIdempotent(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()
	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	if err := Retire(context.Background(), NewGit(), repo, chemin, false); err != nil {
		t.Fatalf("première suppression : %v", err)
	}

	if err := Retire(context.Background(), NewGit(), repo, chemin, false); err != nil {
		t.Errorf("un worktree déjà absent doit rendre nil ; obtenu : %v", err)
	}
	// Un chemin qui n'a jamais existé est traité pareil.
	jamais := filepath.Join(root, "feat12", "jamais-cree")
	if err := Retire(context.Background(), NewGit(), repo, jamais, false); err != nil {
		t.Errorf("un worktree jamais créé doit rendre nil ; obtenu : %v", err)
	}
}

// La saleté ne se limite pas aux fichiers non suivis : un fichier COMMITÉ puis
// modifié est du travail tout aussi destructible. Une implémentation qui ne
// regarderait que les fichiers non suivis (`git ls-files --others`) passerait
// le test du brouillon tout en détruisant ce travail-ci.
func TestRetireRefuseUnFichierSuiviModifie(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()
	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
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

	err = Retire(context.Background(), NewGit(), repo, chemin, false)
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
	repo := depotTest(t, "api")
	chemin, err := Assure(context.Background(), NewGit(), "central", t.TempDir(), "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	ecris(t, filepath.Join(chemin, ".gitignore"), ".env\n")
	git(t, chemin, "add", ".gitignore")
	git(t, chemin, "commit", "-m", "ignore .env")
	ecris(t, filepath.Join(chemin, ".env"), "SECRET=hunter2\n")

	err = Retire(context.Background(), NewGit(), repo, chemin, false)
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
	repo := depotTest(t, "api")
	chemin, err := Assure(context.Background(), NewGit(), "central", t.TempDir(), "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	ecris(t, filepath.Join(chemin, ".gitignore"), "node_modules/\n")
	git(t, chemin, "add", ".gitignore")
	git(t, chemin, "commit", "-m", "ignore node_modules")
	if err := os.MkdirAll(filepath.Join(chemin, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	ecris(t, filepath.Join(chemin, "node_modules", "pkg", "index.js"), "module.exports={}\n")

	if err := Retire(context.Background(), NewGit(), repo, chemin, false); err != nil {
		t.Fatalf("un dossier ignoré en bloc ne doit pas bloquer la suppression : %v", err)
	}
}

// Dossier disparu : l'enregistrement git survit et bloque tout Assure ultérieur.
// Rendre nil sans rien faire, c'était mentir sur un nettoyage non effectué.
func TestRetireEffaceUnEnregistrementOrphelin(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()
	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	if err := os.RemoveAll(chemin); err != nil {
		t.Fatal(err)
	}
	if liste := git(t, repo, "worktree", "list"); !strings.Contains(liste, "prunable") {
		t.Fatalf("préparation : enregistrement orphelin attendu, obtenu :\n%s", liste)
	}

	if err := Retire(context.Background(), NewGit(), repo, chemin, false); err != nil {
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
	repo := depotTest(t, "api")
	root := t.TempDir()
	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	if err := os.RemoveAll(chemin); err != nil {
		t.Fatal(err)
	}

	_, err = Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
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

	chemin, err := Assure(context.Background(), NewGit(), "central", t.TempDir(), "feat12", repoA)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}

	err = Retire(context.Background(), NewGit(), repoB, chemin, true)
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
	g.appels = append(g.appels, append([]string(nil), args...))
	return g.reel.Run(ctx, dir, args...)
}

func (g *gitEspion) appel(prefixe ...string) []string {
	for _, a := range g.appels {
		if len(a) >= len(prefixe) && slices.Equal(a[:len(prefixe)], prefixe) {
			return a
		}
	}
	return nil
}

// Sans force, den délègue le dernier mot à git : ajouter --force en toutes
// circonstances désarmerait le second filet sans qu'aucun test ne le voie.
func TestRetireNArmePasForceSansDemande(t *testing.T) {
	for _, cas := range []struct {
		nom         string
		force       bool
		attendForce bool
	}{
		{"sans force", false, false},
		{"avec force", true, true},
	} {
		t.Run(cas.nom, func(t *testing.T) {
			repo := depotTest(t, "api")
			chemin, err := Assure(context.Background(), NewGit(), "central", t.TempDir(), "feat12", repo)
			if err != nil {
				t.Fatalf("préparation : %v", err)
			}
			espion := &gitEspion{reel: NewGit()}
			if err := Retire(context.Background(), espion, repo, chemin, cas.force); err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			argv := espion.appel("worktree", "remove")
			if argv == nil {
				t.Fatalf("aucun `git worktree remove` émis ; appels : %v", espion.appels)
			}
			if got := slices.Contains(argv, "--force"); got != cas.attendForce {
				t.Errorf("--force présent = %v, attendu %v ; argv = %v", got, cas.attendForce, argv)
			}
		})
	}
}

// Le suffixe « / » seul n'est pas un discriminant : git réduit à une entrée
// unique `!! conf/` un dossier que l'utilisateur n'a JAMAIS ignoré, dès lors que
// tout son contenu l'est. Or `*.env` est la façon la plus répandue d'ignorer des
// secrets — le dossier collapsé n'est pas du cache régénérable.
func TestRetireRefuseUnSecretDansUnDossierNonIgnore(t *testing.T) {
	repo := depotTest(t, "api")
	chemin, err := Assure(context.Background(), NewGit(), "central", t.TempDir(), "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
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

	err = Retire(context.Background(), NewGit(), repo, chemin, false)
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
	repo := depotTest(t, "api")
	chemin, err := Assure(context.Background(), NewGit(), "central", t.TempDir(), "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	ecris(t, filepath.Join(chemin, ".gitignore"), "données/\nmon cache/\n")
	git(t, chemin, "add", ".gitignore")
	git(t, chemin, "commit", "-m", "ignore les caches")
	for _, nom := range []string{"données", "mon cache"} {
		if err := os.MkdirAll(filepath.Join(chemin, nom), 0o755); err != nil {
			t.Fatal(err)
		}
		ecris(t, filepath.Join(chemin, nom, "x.bin"), "cache\n")
	}

	if err := Retire(context.Background(), NewGit(), repo, chemin, false); err != nil {
		t.Fatalf("un dossier ignoré en bloc au nom cité ne doit pas bloquer la suppression : %v", err)
	}
}

// `git worktree prune` saute silencieusement les worktrees verrouillés — et
// `lock` existe précisément pour les volumes amovibles, donc pour le cas où le
// dossier disparaît légitimement. Sans revérification, Retire rendrait nil en
// prétendant avoir nettoyé et Assure renverrait vers `den rm` : boucle fermée.
func TestRetireSignaleUnWorktreeVerrouille(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()
	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	git(t, repo, "worktree", "lock", chemin)
	if err := os.RemoveAll(chemin); err != nil {
		t.Fatal(err)
	}

	for _, force := range []bool{false, true} {
		err := Retire(context.Background(), NewGit(), repo, chemin, force)
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

// En -z, un renommage occupe deux enregistrements et le second, le chemin
// source, n'a pas de préfixe d'état. Non consommé, il est relu comme une entrée
// à part entière dès que son 3e caractère est une espace — et den nomme alors à
// l'utilisateur un fichier qui n'existe pas.
func TestRetireNInventePasDeFichierSurUnRenommage(t *testing.T) {
	repo := depotTest(t, "api")
	chemin, err := Assure(context.Background(), NewGit(), "central", t.TempDir(), "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	ecris(t, filepath.Join(chemin, "ab cd.txt"), "contenu\n")
	git(t, chemin, "add", "ab cd.txt")
	git(t, chemin, "commit", "-m", "ajoute ab cd.txt")
	git(t, chemin, "mv", "ab cd.txt", "xy.txt")

	err = Retire(context.Background(), NewGit(), repo, chemin, false)
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
	if err := Retire(context.Background(), NewGit(), repo, premier, false); err != nil {
		t.Errorf("la suppression doit tenir à travers le lien : %v", err)
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
