package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/spf13/cobra"
)

// executeCmd exécute un arbre de commandes DONNÉ et retourne sa sortie. Séparé
// de run() pour que les tests puissent construire plusieurs arbres avant d'en
// exécuter un seul.
func executeCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// executeCmdFluxSepares exécute l'arbre de commandes avec DEUX buffers
// distincts (stdout, stderr) plutôt que le buffer unique d'executeCmd.
// Nécessaire pour vérifier qu'un message part bien sur l'un des deux flux et
// PAS sur l'autre — une propriété qu'executeCmd, qui fusionne délibérément
// les deux dans le même buffer, ne peut pas distinguer.
//
// Fonction séparée plutôt que changement de signature d'executeCmd : ce
// dernier a douze appelants existants dans ce paquet, dont root_deps_test.go
// et spawn_test.go dont les assertions ne doivent pas changer — les toucher
// tous pour un besoin que seuls quelques tests ont (rm_test.go, ls_test.go)
// aurait été le mauvais compromis.
func executeCmdFluxSepares(t *testing.T, cmd *cobra.Command, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// exécute une commande racine neuve avec des arguments et retourne sa sortie standard.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return executeCmd(t, NewRootCmd(), args...)
}

// executeCmdAvecSbx exécute l'arbre de commandes avec un sbx.Runner injecté ;
// les accès Doctor et Spawn.Policy restent ceux de DepsSysteme() (réels).
// deps.Git, lui, EST exercé pour de vrai par les tests de `den rm` qui
// passent par ici (rm_test.go, ex. TestRmNeDetruitPasLaSandboxSiUnWorktreeEstSale) :
// ils touchent donc du git réel — c'est le TestMain de main_test.go qui rend
// ça hermétique au poste qui exécute la suite, pas ce helper. Aucun test ne
// passe encore par la RunE de configureSpawn (`den ls` ne l'appelle jamais).
// Si un futur test veut exercer `den <nest>` par ce chemin, il doit fournir
// sa PROPRE spawn.Deps isolée plutôt que réutiliser ce helper tel quel.
func executeCmdAvecSbx(t *testing.T, r sbx.Runner, args ...string) (string, error) {
	t.Helper()
	deps := DepsSysteme()
	deps.Sbx = r
	return executeCmd(t, NewRootCmdAvec(deps), args...)
}

// executeCmdAvecSbxFluxSepares combine executeCmdAvecSbx (Runner injecté) et
// executeCmdFluxSepares (stdout/stderr distincts) : nécessaire pour les tests
// qui vérifient qu'un message part sur l'un des deux flux et PAS sur l'autre,
// sur une commande qui a par ailleurs besoin d'un sbx.Runner (den ls).
func executeCmdAvecSbxFluxSepares(t *testing.T, r sbx.Runner, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	deps := DepsSysteme()
	deps.Sbx = r
	return executeCmdFluxSepares(t, NewRootCmdAvec(deps), args...)
}

func TestVersionAfficheLaVersion(t *testing.T) {
	// Version est une variable de paquet : la restaurer évite de contaminer les
	// tests suivants avec une valeur de test.
	original := Version
	t.Cleanup(func() { Version = original })
	Version = "1.2.3"
	out, err := run(t, "version")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(out, "1.2.3") {
		t.Errorf("sortie = %q, attendu contenant %q", out, "1.2.3")
	}
}

// denHomeAvecNest fabrique un den home minimal ne contenant qu'un nest.
// `den nest ls` n'a besoin de rien d'autre.
func denHomeAvecNest(t *testing.T, nom string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nests", nom+".yaml"), []byte("stack: devx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Le flag --den-home appartient à l'instance de commande, pas au paquet.
//
// L'entrelacement est ce qui compte : cmd2 est construit AVANT que cmd1 ne
// s'exécute. Avec une variable de paquet, la construction de cmd2 remet la
// variable à "" (StringVar écrit son défaut), puis l'exécution de cmd1 y écrit
// dirA, et cmd2 — qui ne porte pourtant aucun --den-home — hérite de dirA au
// lieu de retomber sur DEN_HOME. Construire les deux arbres dans l'ordre
// d'exécution masquerait complètement le défaut.
func TestDenHomeEstScopeAChaqueInstance(t *testing.T) {
	dirA := denHomeAvecNest(t, "alpha")
	dirB := denHomeAvecNest(t, "beta")
	t.Setenv("DEN_HOME", dirB)

	cmd1 := NewRootCmd()
	cmd2 := NewRootCmd() // construit avant l'exécution de cmd1

	out1, err := executeCmd(t, cmd1, "nest", "ls", "--den-home", dirA)
	if err != nil {
		t.Fatalf("cmd1 : erreur inattendue : %v", err)
	}
	if !strings.Contains(out1, "alpha") {
		t.Errorf("cmd1 = %q, attendu le nest de %s", out1, dirA)
	}

	out2, err := executeCmd(t, cmd2, "nest", "ls")
	if err != nil {
		t.Fatalf("cmd2 : erreur inattendue : %v", err)
	}
	if strings.Contains(out2, "alpha") {
		t.Errorf("cmd2 = %q : le --den-home de cmd1 a fuité dans une autre instance", out2)
	}
	if !strings.Contains(out2, "beta") {
		t.Errorf("cmd2 = %q, attendu le nest de DEN_HOME (%s)", out2, dirB)
	}
}

// La racine étant devenue la commande de spawn, un premier argument inconnu
// n'est plus « une commande inconnue » : c'est un NOM DE NEST, et l'échec doit
// nommer le fichier attendu plutôt que de parler de commande.
//
// DEN_HOME est épinglé : sans lui, ce test ne passe que sur les machines qui
// n'ont pas de ~/.den, et va lire le den home réel du développeur sur les autres.
func TestUnPremierArgumentInconnuEstUnNestIntrouvable(t *testing.T) {
	dir := denHomeAvecNest(t, "api")
	// config.yaml doit exister, sinon le spawn échoue une étape trop tôt et
	// l'erreur ne dit plus rien du nest demandé.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"),
		[]byte("defaults:\n  agent: claude\n  stack: devx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEN_HOME", dir)

	_, err := run(t, "nexistepas")
	if err == nil {
		t.Fatal("attendu une erreur pour un nest inconnu, obtenu nil")
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, "nests", "nexistepas.yaml")) {
		t.Errorf("l'erreur doit nommer le fichier de nest attendu ; obtenu : %v", err)
	}
}
