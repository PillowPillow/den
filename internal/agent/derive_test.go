package agent

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// poseGolden matérialise le GOLDEN du mixin là où LisMixin ira le chercher.
//
// Le golden et non une sortie fraîche de RendMixin, à dessein : un aller-retour
// RendMixin → LisMixin resterait vert si les DEUX côtés se trompaient du même
// chemin de clés (« cap » pour « caps », « env » pour « environment » — c'est
// exactement la faute que le spec portait avant la tâche 1). Le golden est écrit
// à la main et jamais régénéré : c'est la seule référence indépendante du
// décodeur qu'on teste.
func poseGolden(t *testing.T, nomSandbox string) string {
	t.Helper()
	denHome := t.TempDir()
	dir := filepath.Join(denHome, "cache", "mixins", nomSandbox)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "mixin-complet.golden"))
	if err != nil {
		t.Fatalf("lecture du golden : %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.yaml"), golden, 0o600); err != nil {
		t.Fatal(err)
	}
	return denHome
}

func TestLisMixinDecodeLeGolden(t *testing.T) {
	denHome := poseGolden(t, "api.feat12")

	m, err := LisMixin(denHome, "api.feat12")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if m.NomSandbox != "api.feat12" {
		t.Errorf("NomSandbox = %q, attendu %q", m.NomSandbox, "api.feat12")
	}
	if attendu := []string{"api.anthropic.com", "github.com"}; !slices.Equal(m.Egress, attendu) {
		t.Errorf("Egress = %v, attendu %v", m.Egress, attendu)
	}
	attenduEnv := map[string]string{
		"CLAUDE_CONFIG_DIR": "/home/moi/.den/agents/claude",
		"SOME_VAR":          "value",
	}
	if len(m.Env) != len(attenduEnv) {
		t.Errorf("Env = %v, attendu %v", m.Env, attenduEnv)
	}
	for k, v := range attenduEnv {
		if m.Env[k] != v {
			t.Errorf("Env[%q] = %q, attendu %q", k, m.Env[k], v)
		}
	}
	// La fraîcheur est un argv, dont le dernier élément est le script bash.
	if len(m.Fraicheur) != 3 || m.Fraicheur[0] != "bash" || m.Fraicheur[1] != "-c" {
		t.Fatalf("Fraicheur = %v, attendu [bash -c <script>]", m.Fraicheur)
	}
	if !strings.Contains(m.Fraicheur[2], "claude update") {
		t.Errorf("le script de fraîcheur doit porter la commande d'update ; obtenu :\n%s", m.Fraicheur[2])
	}
}

// Spawn doit distinguer « pas de référence » (premier spawn, ou cache purgé —
// cache/ est déclaré reconstructible par le spec §3) d'une lecture cassée : le
// premier cas est normal et silencieux, le second doit s'annoncer. Sans %w, les
// deux se confondent et Spawn ne peut que se taire sur les deux.
func TestLisMixinAbsentEstDistinguableDUneLectureCassee(t *testing.T) {
	_, err := LisMixin(t.TempDir(), "api")
	if err == nil {
		t.Fatal("un mixin absent doit produire une erreur")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("l'erreur doit envelopper os.ErrNotExist ; obtenu : %v", err)
	}
}

// Le message nomme le CHEMIN complet : une dérive non vérifiable envoie
// l'utilisateur sur ce fichier, et « lecture du mixin » seul ne désigne rien.
func TestLisMixinNommeLeChemin(t *testing.T) {
	denHome := t.TempDir()
	_, err := LisMixin(denHome, "api")
	if err == nil {
		t.Fatal("un mixin absent doit produire une erreur")
	}
	chemin := filepath.Join(denHome, "cache", "mixins", "api", "spec.yaml")
	if !strings.Contains(err.Error(), chemin) {
		t.Errorf("le message doit nommer %s ; obtenu : %v", chemin, err)
	}
}

func TestLisMixinRefuseUnYAMLIllisible(t *testing.T) {
	denHome := t.TempDir()
	dir := filepath.Join(denHome, "cache", "mixins", "api")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.yaml"), []byte("\tpas du yaml"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LisMixin(denHome, "api")
	if err == nil {
		t.Fatal("un spec.yaml illisible doit produire une erreur")
	}
	// Et surtout PAS os.ErrNotExist : Spawn se tairait sur un mixin corrompu.
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("un YAML cassé ne doit pas se confondre avec un fichier absent ; obtenu : %v", err)
	}
}

// LisMixin doit relire ce qu'EcrisMixin vient d'écrire : les deux se partagent le
// chemin, et une divergence rendrait la dérive INDÉTECTABLE en silence (LisMixin
// rendrait toujours os.ErrNotExist, que Spawn traite comme un premier spawn).
// Le golden ci-dessus prouve les clés, celui-ci prouve le chemin.
func TestLisMixinRelitCeQuEcrisMixinEcrit(t *testing.T) {
	denHome := t.TempDir()
	m := mixinExemple(t)
	if _, err := EcrisMixin(denHome, m.NomSandbox, m); err != nil {
		t.Fatalf("EcrisMixin : %v", err)
	}

	relu, err := LisMixin(denHome, m.NomSandbox)
	if err != nil {
		t.Fatalf("LisMixin après EcrisMixin : %v", err)
	}
	if d := Differences(m, relu); len(d) != 0 {
		t.Errorf("un aller-retour ne doit produire aucune différence ; obtenu : %v", d)
	}
}

func TestDifferencesIdentiquesNeRendRien(t *testing.T) {
	m := mixinExemple(t)
	if d := Differences(m, m); len(d) != 0 {
		t.Errorf("deux mixins identiques ne doivent produire aucune différence ; obtenu : %v", d)
	}
}

// Le cas qui motive tout le dispositif : un egress RÉTRÉCI passe le settle-loop
// en silence (la policy large de la VM autorise évidemment la liste étroite), et
// l'utilisateur croit sa sandbox resserrée alors qu'elle est restée ouverte.
func TestDifferencesNommeLEgressRetreci(t *testing.T) {
	ancien := mixinExemple(t)
	ancien.Egress = []string{"api.anthropic.com", "github.com"}
	nouveau := ancien
	nouveau.Egress = []string{"github.com"}

	d := Differences(ancien, nouveau)
	if len(d) != 1 {
		t.Fatalf("une seule différence attendue ; obtenu : %v", d)
	}
	if !strings.Contains(d[0], "api.anthropic.com") {
		t.Errorf("la différence doit NOMMER l'hôte retiré ; obtenu : %q", d[0])
	}
	if strings.Contains(d[0], "github.com") {
		t.Errorf("un hôte inchangé ne doit pas être signalé ; obtenu : %q", d[0])
	}
}

func TestDifferencesNommeLEgressAjoute(t *testing.T) {
	ancien := mixinExemple(t)
	ancien.Egress = []string{"github.com"}
	nouveau := ancien
	nouveau.Egress = []string{"github.com", "pypi.org"}

	d := Differences(ancien, nouveau)
	if len(d) != 1 {
		t.Fatalf("une seule différence attendue ; obtenu : %v", d)
	}
	if !strings.Contains(d[0], "pypi.org") {
		t.Errorf("la différence doit nommer l'hôte ajouté ; obtenu : %q", d[0])
	}
}

func TestDifferencesNommeLesClesDEnv(t *testing.T) {
	ancien := mixinExemple(t)
	ancien.Env = map[string]string{"GARDE": "1", "RETIRE": "1", "CHANGE": "avant"}
	nouveau := ancien
	nouveau.Env = map[string]string{"GARDE": "1", "AJOUTE": "1", "CHANGE": "apres"}

	d := Differences(ancien, nouveau)
	joint := strings.Join(d, "\n")
	for _, cle := range []string{"RETIRE", "AJOUTE", "CHANGE"} {
		if !strings.Contains(joint, cle) {
			t.Errorf("la clé %q doit être signalée ; obtenu :\n%s", cle, joint)
		}
	}
	if strings.Contains(joint, "GARDE") {
		t.Errorf("une clé inchangée ne doit pas être signalée ; obtenu :\n%s", joint)
	}
}

// Les VALEURS d'environnement ne sortent JAMAIS sur le terminal, seulement les
// clés. EcrisMixin pose spec.yaml en 0600 précisément parce
// qu'environment.variables porte l'env utilisateur libre, où atterrissent
// naturellement une clé d'API ou une URI à credentials. Un avertissement de
// dérive qui les imprime les recopie dans le scrollback du terminal et dans les
// logs CI — annulant la précaution d'un cran plus bas.
func TestDifferencesNImprimePasLesValeursDEnv(t *testing.T) {
	ancien := mixinExemple(t)
	ancien.Env = map[string]string{"API_KEY": "sk-secret-avant", "PARTI": "secret-parti"}
	nouveau := ancien
	nouveau.Env = map[string]string{"API_KEY": "sk-secret-apres", "NEUF": "secret-neuf"}

	joint := strings.Join(Differences(ancien, nouveau), "\n")
	for _, secret := range []string{"sk-secret-avant", "sk-secret-apres", "secret-parti", "secret-neuf"} {
		if strings.Contains(joint, secret) {
			t.Errorf("la valeur %q ne doit jamais être rendue ; obtenu :\n%s", secret, joint)
		}
	}
}

// La commande de fraîcheur est la garde fail-closed du spec §9.1 : elle change
// dès qu'on change de `bin_dirs` ou d'`update`, et la sandbox vivante continue
// de faire tourner l'ANCIENNE à chaque boot.
func TestDifferencesSignaleLaFraicheur(t *testing.T) {
	ancien := mixinExemple(t)
	nouveau := ancien
	nouveau.Fraicheur = []string{"bash", "-c", "autre chose"}

	d := Differences(ancien, nouveau)
	if len(d) != 1 {
		t.Fatalf("une seule différence attendue ; obtenu : %v", d)
	}
	if !strings.Contains(d[0], "fraîcheur") {
		t.Errorf("la différence doit nommer la fraîcheur ; obtenu : %q", d[0])
	}
	// Le script est multiligne et peut faire des dizaines de lignes : le rendre
	// noierait les autres différences dans le terminal.
	if strings.Contains(d[0], "autre chose") {
		t.Errorf("le script ne doit pas être rendu en entier ; obtenu : %q", d[0])
	}
}

// L'ordre doit être déterministe : ces lignes vont sur le terminal de
// l'utilisateur à chaque attache, et l'ordre d'itération d'une map Go est
// aléatoire.
func TestDifferencesEstDeterministe(t *testing.T) {
	ancien := mixinExemple(t)
	ancien.Egress = []string{"a.test", "b.test", "c.test"}
	ancien.Env = map[string]string{"A": "1", "B": "1", "C": "1", "D": "1"}
	nouveau := ancien
	nouveau.Egress = []string{"z.test", "y.test"}
	nouveau.Env = map[string]string{"Z": "1", "Y": "1"}

	reference := Differences(ancien, nouveau)
	if len(reference) < 6 {
		t.Fatalf("le cas de test doit produire plusieurs différences ; obtenu : %v", reference)
	}
	for i := 0; i < 20; i++ {
		if d := Differences(ancien, nouveau); !slices.Equal(d, reference) {
			t.Fatalf("tour %d : %v, attendu %v", i, d, reference)
		}
	}
}
