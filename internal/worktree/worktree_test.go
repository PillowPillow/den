package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	return dir
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
	if !strings.Contains(err.Error(), "/n/existe/pas") {
		t.Errorf("le message doit nommer le chemin fautif ; obtenu : %v", err)
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

	if err := Retire(context.Background(), NewGit(), repo, chemin, false); err == nil {
		t.Fatal("un worktree avec des modifications non commitées doit être refusé sans force")
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
