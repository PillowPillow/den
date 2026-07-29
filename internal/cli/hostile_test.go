package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
)

// Ces tests exercent la CLI complète sur des configurations HOSTILES — celles
// de la table de la tâche 17c, plus celles trouvées en manipulant le binaire
// assemblé.
//
// Ils vivent au niveau de la CLI, et pas seulement dans les paquets qui portent
// les gardes, pour une raison précise : ce qui compte n'est pas qu'une fonction
// refuse, c'est que l'UTILISATEUR reçoive un refus avant que den n'ait rien
// abîmé. Cette propriété-là traverse la moitié du projet et ne se vérifie
// nulle part ailleurs.
//
// Aucun n'invoque le vrai `sbx` : le Runner est un sbx.Fake, et c'est sur ses
// Appels que porte l'assertion « aucun effet de bord ».

// denHomeHostile fabrique un den home VALIDE et rend son chemin, ainsi que
// celui de l'unique repo du nest. Chaque test n'en dégrade ensuite qu'un seul
// aspect : sans base saine, un refus ne prouverait pas ce qui l'a causé.
//
// Le repo est un DÉPÔT GIT RÉEL, et ce n'est pas un détail de confort : les
// tests qui vérifient qu'aucun worktree n'a été créé seraient VIDES sans lui.
// worktree.Assure ouvre par un verifieRepo qui échoue immédiatement sur un
// dossier ordinaire, sans rien créer — l'assertion « aucun worktree » serait
// alors vraie quoi qu'il arrive, y compris si den tentait vraiment d'en créer un.
// Mesuré : avec un dossier nu, inverser l'ordre des gardes dans spawn.Spawn
// laissait le test passer.
//
// Aucun `egress:` : policy.Settle rend nil sur une allowlist vide, ce qui
// épargne à la suite les 60 s du settle-loop (même précaution que
// denHomeSpawnable, spawn_test.go). L'environnement git est neutralisé
// package-wide par TestMain (main_test.go).
func denHomeHostile(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(t.TempDir(), "monrepo")
	creeDepotGit(t, repo)
	ecris := func(rel, contenu string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(contenu), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ecris("config.yaml", `agents:
  claude:
    config_dir: `+filepath.Join(dir, "agents", "claude")+`
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`)
	ecris("stacks/devx/stack.yaml", "image: devx:v1\n")
	ecris("nests/api.yaml", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	return dir, repo
}

// runHostile exécute l'arbre de commandes RÉEL sur un den home, avec un
// sbx.Fake, et rend le Fake pour que l'appelant puisse asserter l'ABSENCE
// d'appel autant que sa présence.
func runHostile(t *testing.T, home string, args ...string) (*sbx.Fake, string, error) {
	t.Helper()
	f := &sbx.Fake{
		Reponses: map[string]sbx.Reponse{"ls --json": {Sortie: []byte(`{"sandboxes":[]}`)}},
		Defaut:   sbx.Reponse{Sortie: []byte(`{"allowed": true}`)},
	}
	deps := DepsSysteme()
	deps.Sbx = f
	sortie, err := executeCmd(t, NewRootCmdAvec(deps), append(args, "--den-home", home)...)
	return f, sortie, err
}

// Cas 5 de la table — LE plus risqué, parce que son coût est un effet de bord :
// un worktree créé pour une sandbox qui ne verra jamais le jour, que
// l'utilisateur devra retirer à la main.
//
// `sbx.NomSandbox` est appelé AVANT la boucle de création des worktrees dans
// spawn.Spawn, et c'est cet ORDRE que ce test verrouille — pas le message. Le
// « / » de « feature/123 » est refusé parce qu'un nom de sandbox n'en contient
// pas ; inverser les deux étapes laisserait le refus intact et le worktree
// créé, sans qu'aucun test du paquet spawn ne s'en aperçoive.
func TestWorktreeNonSandboxableEstRefuseSansRienCreer(t *testing.T) {
	home, _ := denHomeHostile(t)

	f, _, err := runHostile(t, home, "api", "-w", "feature/123")
	if err == nil {
		t.Fatal("un worktree nommé « feature/123 » doit être refusé")
	}
	// Le caractère fautif, pas seulement « nom invalide » : c'est lui que
	// l'utilisateur doit retirer.
	if !strings.Contains(err.Error(), `"/"`) {
		t.Errorf("le message doit nommer le caractère refusé ; obtenu : %v", err)
	}
	if !strings.Contains(err.Error(), "feature/123") {
		t.Errorf("le message doit citer la valeur reçue ; obtenu : %v", err)
	}
	if len(f.Appels) != 0 {
		t.Errorf("aucun appel sbx ne doit avoir lieu ; appels : %v", f.Appels)
	}
	// L'effet de bord que ce test existe pour interdire : la racine des
	// worktrees ne doit pas même avoir été créée.
	if _, err := os.Stat(filepath.Join(home, "worktrees")); err == nil {
		t.Error("un worktree a été créé alors que le nom de sandbox est invalide")
	}
}

// Cas 7 de la table — un nest sans aucun repo.
//
// La question que posait le brief : `sbx create` a-t-il encore au moins un
// workspace ? Réponse mesurée : oui, le profil de l'agent. Ce test la fige,
// parce qu'ArgvCreate REFUSE un create sans workspace (« sbx create exige au
// moins un chemin ») : si le profil cessait un jour d'être monté, un nest sans
// repo deviendrait insandboxable, et le message parlerait de workspaces
// manquants sans dire que c'est le profil qui a disparu.
func TestUnNestSansRepoMonteQuandMemeLeProfilDeLAgent(t *testing.T) {
	home, _ := denHomeHostile(t)
	if err := os.WriteFile(filepath.Join(home, "nests", "api.yaml"),
		[]byte("stack: devx\nrepos: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := runHostile(t, home, "api")
	if err != nil {
		t.Fatalf("un nest sans repo doit rester spawnable : %v", err)
	}
	if !f.AAppele("create") {
		t.Fatalf("aucun `sbx create` ; appels : %v", f.Appels)
	}
	var create []string
	for _, a := range f.Appels {
		if len(a) > 0 && a[0] == "create" {
			create = a
		}
	}
	// Le profil de l'agent est le workspace de repli, et le seul ici.
	profil := filepath.Join(home, "agents", "claude")
	if !slicesContient(create, profil) {
		t.Errorf("le profil de l'agent (%s) doit être monté ; argv create = %v", profil, create)
	}
	// Et il est bien le DERNIER argument positionnel, donc un workspace : un
	// create sans aucun workspace serait refusé par ArgvCreate.
	if create[len(create)-1] != profil {
		t.Errorf("dernier workspace = %q, attendu le profil %q", create[len(create)-1], profil)
	}
}

// Cas 8 de la table, tel qu'il se manifeste pour l'UTILISATEUR — la 15ᵉ
// configuration hostile, trouvée en manipulant le binaire.
//
// Le refus lui-même est verrouillé par TestValideWorktreeRefuseUneRacineRelative
// (internal/config). Ce test-ci verrouille autre chose, que le paquet config ne
// peut pas voir : que le refus arrive AVANT le moindre effet de bord, et qu'il
// nomme le champ à corriger. Avant correctif, mesuré sur le binaire, den créait
// pour de bon un worktree et une branche DANS le dépôt de l'utilisateur, puis
// échouait sur un message parlant de « workspace n°1 » — un mot qui n'apparaît
// nulle part dans sa configuration.
func TestWorktreeRootRelatifEstRefuseEnNommantLeChamp(t *testing.T) {
	home, repo := denHomeHostile(t)
	config, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.yaml"),
		append(config, []byte("worktree_root: wt-relatif\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := runHostile(t, home, "api", "-w", "feat1")
	if err == nil {
		t.Fatal("un worktree_root relatif doit être refusé")
	}
	if !strings.Contains(err.Error(), "worktree_root") {
		t.Errorf("le message doit nommer le champ à corriger ; obtenu : %v", err)
	}
	if !strings.Contains(err.Error(), "wt-relatif") {
		t.Errorf("le message doit citer la valeur reçue ; obtenu : %v", err)
	}
	if len(f.Appels) != 0 {
		t.Errorf("aucun appel sbx ne doit avoir lieu ; appels : %v", f.Appels)
	}

	// LE DÉFAUT EST L'EFFET DE BORD, et c'est donc lui qu'il faut mesurer : le
	// message et l'absence d'appel sbx ne disent rien de ce que den a laissé
	// dans le dépôt. Mesuré avant correctif, `git worktree list` rendait
	// « <repo>/wt-relatif/feat1/monrepo … [feat1] » — un worktree ET une branche
	// que l'utilisateur devait retirer à la main.
	//
	// Le chemin exact vient de la résolution relative de git : `worktree add`
	// tourne AVEC LE REPO POUR CWD, donc « wt-relatif/… » atterrit dedans.
	if _, err := os.Stat(filepath.Join(repo, "wt-relatif")); err == nil {
		t.Errorf("den a créé %s dans le dépôt de l'utilisateur alors que la configuration "+
			"est refusée", filepath.Join(repo, "wt-relatif"))
	}
	// La branche aussi : `git worktree add -b` la crée, et elle survit même si
	// le dossier est nettoyé.
	if branches := gitDansTest(t, repo, "branch", "--list", "feat1"); strings.TrimSpace(branches) != "" {
		t.Errorf("den a créé la branche feat1 dans le dépôt de l'utilisateur : %q", branches)
	}
	// Et le dépôt ne connaît qu'un seul worktree, le principal.
	if liste := gitDansTest(t, repo, "worktree", "list"); strings.Count(strings.TrimSpace(liste), "\n") != 0 {
		t.Errorf("le dépôt porte plus d'un worktree :\n%s", liste)
	}
}

// gitDansTest lance git dans un dépôt et rend sa sortie.
//
// Elle passe par worktree.NewGit() plutôt que par le paquet exec de la
// bibliothèque standard, et ce n'est pas une coquetterie : la garde
// d'hermétisme de la tâche 17c interdit ce paquet dans les tests hors d'une
// liste blanche dont ce fichier ne fait pas partie. Le besoin — lancer un vrai
// git — est ici le même que celui qui a valu à internal/worktree son entrée
// dans cette liste ; emprunter son accès exporté satisfait les deux.
//
// L'environnement git de la machine est neutralisé package-wide par TestMain
// (main_test.go). Ces appels sont en LECTURE seule : aucune configuration
// n'est écrite, aucun `git config` n'est lancé.
func gitDansTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := worktree.NewGit().Run(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %v dans %s : %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// 16ᵉ configuration hostile : une stack cassée que PERSONNE n'utilise ne doit
// pas empêcher de spawner un nest dont la stack est saine.
//
// Mesuré sur le binaire avant correctif : `den api` échouait en affichant
// l'erreur YAML d'une stack sans rapport, alors que le nest api référence devx,
// parfaitement valide. C'est la doctrine que T16 a établie pour les nests, et
// qui n'avait jamais été appliquée aux stacks.
func TestUneStackCasseeNEmpechePasDeSpawnerUnNestSain(t *testing.T) {
	home, _ := denHomeHostile(t)
	if err := os.MkdirAll(filepath.Join(home, "stacks", "autre"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "stacks", "autre", "stack.yaml"),
		[]byte("image: autre:v1\nimag: faute-de-frappe\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := runHostile(t, home, "api")
	if err != nil {
		t.Fatalf("le nest api utilise devx, saine : le spawn doit aboutir ; obtenu : %v", err)
	}
	if !f.AAppele("create") {
		t.Errorf("aucun `sbx create` ; appels : %v", f.Appels)
	}
}

// Le pendant du test précédent, et la moitié qui compte vraiment : quand c'est
// SA PROPRE stack qui est cassée, le nest doit échouer — et le message doit
// porter la faute YAML, pas un « introuvable » qui enverrait créer un fichier
// déjà présent.
//
// Sans ce test, ignorer purement et simplement les stacks cassées passerait le
// test précédent en rendant le vrai problème muet.
func TestUnNestDontLaStackEstCasseeEchoueEnNommantLaFaute(t *testing.T) {
	home, _ := denHomeHostile(t)
	if err := os.WriteFile(filepath.Join(home, "stacks", "devx", "stack.yaml"),
		[]byte("image: devx:v1\nimag: faute-de-frappe\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := runHostile(t, home, "api")
	if err == nil {
		t.Fatal("un nest dont la stack est illisible ne doit pas spawner")
	}
	if !strings.Contains(err.Error(), "imag") {
		t.Errorf("le message doit porter la faute YAML de la stack ; obtenu : %v", err)
	}
	if strings.Contains(err.Error(), "introuvable") {
		t.Errorf("la stack existe : « introuvable » enverrait créer un fichier déjà présent ; obtenu : %v", err)
	}
	if f.AAppele("create") {
		t.Errorf("aucune sandbox ne doit être créée ; appels : %v", f.Appels)
	}
}

// slicesContient : slices.Contains sans importer slices pour un seul usage.
func slicesContient(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
