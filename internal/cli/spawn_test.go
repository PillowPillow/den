package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// La racine devient la commande de spawn : les sous-commandes existantes ne
// doivent surtout pas être avalées comme des noms de nest.
//
// DEN_HOME est épinglé sur un dossier vide dans TOUS les tests qui passent par
// run() : si la racine capturait un jeton qu'elle ne devrait pas, le spawn
// partirait sur le ~/.den RÉEL de la machine — et sur le vrai `sbx`.
func TestLesSousCommandesRestentPrioritaires(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	sortie, err := run(t, "version")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.HasPrefix(sortie, "den ") {
		t.Errorf("`den version` doit rester la commande version ; obtenu : %q", sortie)
	}
}

func TestDenSansArgumentAfficheLAide(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	sortie, err := run(t)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(sortie, "nest") {
		t.Errorf("`den` seul doit afficher l'aide ; obtenu : %q", sortie)
	}
}

// Le câblage de bout en bout : args[0] devient le nest, et --den-home est bien
// celui que le spawn consulte. Un den home vide fait échouer la toute première
// étape (lecture de config.yaml), ce qui suffit à nommer le dossier consulté
// sans que `sbx` — absent de cette machine — soit jamais sollicité.
func TestDenNestRouteVersLeSpawn(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())
	dir := t.TempDir()

	if _, err := run(t, "api", "--den-home", dir); err == nil {
		t.Fatal("un den home vide doit faire échouer le spawn")
	} else if !strings.Contains(err.Error(), filepath.Join(dir, "config.yaml")) {
		t.Errorf("le spawn doit consulter le --den-home donné ; obtenu : %v", err)
	}
}

// Sans flag, la résolution du den home doit passer par config.Home (donc par
// DEN_HOME, puis ~/.den). Ce cas est celui qui distingue « on appelle
// config.Home » de « on passe la valeur brute du flag » : brute, elle vaut ""
// et le spawn irait lire un « config.yaml » relatif au cwd.
func TestDenNestSansFlagPasseParDenHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEN_HOME", dir)

	if _, err := run(t, "api"); err == nil {
		t.Fatal("un den home vide doit faire échouer le spawn")
	} else if !strings.Contains(err.Error(), filepath.Join(dir, "config.yaml")) {
		t.Errorf("le spawn doit résoudre le den home via DEN_HOME ; obtenu : %v", err)
	}
}

// runSpawn exécute la commande de spawn sur un den home donné, avec des accès
// injectés. Même raison que runDoctor : sans injection, le branchement des
// flags sur spawn.Options n'est vérifiable nulle part, et tout test qui
// atteindrait `sbx create` tenterait d'exécuter le vrai binaire.
func runSpawn(t *testing.T, home string, deps spawn.Deps, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	configureSpawn(root, &home, deps)
	return executeCmd(t, root, args...)
}

// denHomeSpawnable : un den home minimal sur lequel un spawn complet aboutit.
//
// Aucun dépôt git : aucun test de ce fichier ne crée de worktree. Aucun
// `egress:` non plus, ce qui court-circuite le settle-loop (policy.Settle rend
// nil sur une allowlist vide) — ces tests portent sur le câblage des flags, pas
// sur la boucle, déjà verrouillée dans internal/spawn. Sans ça, un sondage qui
// ne passerait pas ferait dormir la suite 60 s pour de vrai.
func denHomeSpawnable(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(t.TempDir(), "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

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
	return dir
}

func depsSpawnFactices() (*sbx.Fake, spawn.Deps) {
	f := &sbx.Fake{
		Reponses: map[string]sbx.Reponse{
			"ls --json": {Sortie: []byte(`{"sandboxes":[]}`)},
		},
		Defaut: sbx.Reponse{Sortie: []byte(`{"allowed": true}`)},
	}
	d := spawn.DepsSysteme()
	d.Sbx = f
	return f, d
}

// Chaque flag de `den <nest>` doit atteindre spawn.Options.
//
// Le câblage est précisément ce que personne ne teste, et un flag débranché est
// SILENCIEUX : `den api -w feat` créerait une sandbox « api » sur le checkout
// principal du repo, sans worktree, et l'utilisateur ne le découvrirait qu'en
// regardant sa branche depuis l'intérieur de la VM.
//
// Chaque flag est exercé par une valeur INVALIDE : c'est ce qui produit, sans
// sbx, un message qui dépend de la valeur passée — donc la preuve qu'elle a
// traversé le câblage. Débranché, le flag retombe sur son zéro, le spawn
// réussit, et il n'y a plus d'erreur du tout.
func TestLesFlagsAtteignentSpawnOptions(t *testing.T) {
	cas := []struct {
		nom     string
		args    []string
		attendu string
	}{
		{"-w", []string{"api", "-w", "feature/123"}, `worktree "feature/123"`},
		{"--worktree", []string{"api", "--worktree", "feature/123"}, `worktree "feature/123"`},
		{"--agent", []string{"api", "--agent", "inconnu"}, `agent "inconnu"`},
		{"--without", []string{"api", "--without", "inconnu"}, `--without : repo "inconnu"`},
		{"--only", []string{"api", "--only", "inconnu"}, `--only : repo "inconnu"`},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			f, d := depsSpawnFactices()

			_, err := runSpawn(t, denHomeSpawnable(t), d, c.args...)
			if err == nil {
				t.Fatalf("%s avec une valeur invalide doit faire échouer le spawn", c.nom)
			}
			if !strings.Contains(err.Error(), c.attendu) {
				t.Errorf("%s n'atteint pas spawn.Options (attendu %q) ; obtenu : %v", c.nom, c.attendu, err)
			}
			if len(f.Appels) != 0 {
				t.Errorf("aucun appel sbx ne doit avoir eu lieu ; appels : %v", f.Appels)
			}
		})
	}
}

// --detach est le seul flag dont la valeur n'a aucun effet observable avant la
// toute fin de la séquence : il se prouve par la DIFFÉRENCE avec le même spawn
// sans le flag. Asserter la seule absence d'attache ne prouverait rien — un
// spawn cassé la produirait tout aussi bien.
func TestDetachAtteintSpawnOptions(t *testing.T) {
	home := denHomeSpawnable(t)

	fAvec, dAvec := depsSpawnFactices()
	if _, err := runSpawn(t, home, dAvec, "api", "--detach"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(fAvec.Attaches) != 0 {
		t.Errorf("--detach ne doit pas attacher ; attaches : %v", fAvec.Attaches)
	}

	fSans, dSans := depsSpawnFactices()
	if _, err := runSpawn(t, home, dSans, "api"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(fSans.Attaches) != 1 {
		t.Errorf("sans --detach, une attache doit avoir lieu ; attaches : %v", fSans.Attaches)
	}
}
