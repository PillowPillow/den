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

// ecrisConfig écrit un config.yaml dans denHome.
func ecrisConfig(t *testing.T, denHome, contenu string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(denHome, "config.yaml"), []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ecrisStack écrit stacks/<nom>/stack.yaml dans denHome.
func ecrisStack(t *testing.T, denHome, nom, contenu string) {
	t.Helper()
	p := filepath.Join(denHome, "stacks", nom, "stack.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ecrisNest écrit nests/<nom>.yaml dans denHome.
func ecrisNest(t *testing.T, denHome, nom, contenu string) {
	t.Helper()
	p := filepath.Join(denHome, "nests", nom+".yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
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
	f := &sbx.Fake{Reponses: lsAvec("api")}

	_, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "absente")
	if err == nil {
		t.Fatal("un nom inconnu doit produire une erreur")
	}
	if !strings.Contains(err.Error(), "api") {
		t.Errorf("le message doit lister les sandboxes vivantes ; obtenu : %v", err)
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

// Best-effort sur la RÉSOLUTION : un nest supprimé de ~/.den/nests depuis le
// spawn ne doit pas empêcher de détruire une sandbox bel et bien vivante.
func TestRmNestIllisibleNEmpechePasLaDestruction(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	// Pas de ecrisNest("api", ...) : le nest "api" est absent de ~/.den/nests.
	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}

	sortie, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api.feat12")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(sortie, "api") {
		t.Errorf("la sortie doit mentionner le nest illisible ; obtenu :\n%s", sortie)
	}
	if !f.AAppele("rm", "--force", "api.feat12") {
		t.Errorf("la sandbox doit tout de même être détruite ; appels : %v", f.Appels)
	}
}

// L'ordre « worktrees d'abord, sandbox ensuite » est une propriété de sûreté :
// l'inverse laisserait l'utilisateur sans VM ET avec un message d'erreur sur
// un dossier.
func TestRmNeDetruitPasLaSandboxSiUnWorktreeEstSale(t *testing.T) {
	// Le core.excludesfile de ce poste est corrompu (des octets NUL avant
	// ".sbx" dans le fichier global) : neutralisé pour que ce test ne dépende
	// pas de la machine qui l'exécute.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)

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

	// Dépôt git réel + worktree réel, sale.
	repo := filepath.Join(t.TempDir(), "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@exemple.test"},
		{"config", "user.name", "T"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", c...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v : %v\n%s", c, err, out)
		}
	}
	ecrisNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	chemin, err := worktree.Assure(context.Background(), worktree.NewGit(),
		"central", filepath.Join(denHome, "worktrees"), "feat12", repo)
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
	// est détruite, et l'utilisateur apprend où son travail est parti.
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
}

// gitDeadlineFactice enregistre, pour chaque appel, si le contexte reçu porte
// une échéance et laquelle. Contrairement à un double qui bloquerait pour de
// vrai, il permet de vérifier le BORNAGE sans jamais attendre le délai réel.
type gitDeadlineFactice struct {
	aUneEcheance []bool
	echeances    []time.Time
}

func (g *gitDeadlineFactice) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.RunAvecEntree(ctx, dir, nil, args...)
}

func (g *gitDeadlineFactice) RunAvecEntree(ctx context.Context, _ string, _ []byte, args ...string) ([]byte, error) {
	d, ok := ctx.Deadline()
	g.aUneEcheance = append(g.aUneEcheance, ok)
	g.echeances = append(g.echeances, d)
	return nil, fmt.Errorf("git factice : appel refusé pour %v", args)
}

var _ worktree.Git = (*gitDeadlineFactice)(nil)

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
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	ecrisNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}
	git := &gitDeadlineFactice{}
	deps := DepsSysteme()
	deps.Sbx = f
	deps.Git = git
	root := NewRootCmdAvec(deps)

	_, err := executeCmd(t, root, "--den-home", denHome, "rm", "api.feat12")
	if err == nil {
		t.Fatal("le git factice refuse systématiquement : une erreur est attendue")
	}
	if len(git.aUneEcheance) == 0 {
		t.Fatal("le git factice n'a reçu aucun appel")
	}
	if !git.aUneEcheance[0] {
		t.Fatal("le contexte transmis à worktree.Retire ne porte aucune échéance : les sondes ne sont pas bornées")
	}
	restant := time.Until(git.echeances[0])
	if restant <= 0 || restant > delaiSondesGit {
		t.Errorf("échéance hors bornes : il reste %v pour un délai de %v", restant, delaiSondesGit)
	}
}
