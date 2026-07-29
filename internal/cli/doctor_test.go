package cli

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/doctor"
)

// runDoctor exécute `den doctor` sur un den home donné avec des accès système
// injectés. Le test ne doit rien devoir à la machine qui l'exécute : sans cette
// injection, le contrat de sortie de la commande (« non-zéro si un check
// échoue ») n'est vérifiable nulle part.
func runDoctor(t *testing.T, home string, deps doctor.Deps) (string, error) {
	t.Helper()
	cmd := newDoctorCmd(&home, deps)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(nil)
	err := cmd.Execute()
	return out.String(), err
}

// depsSaines : sbx présent, tous les repos sur disque, git assez récent.
func depsSaines() doctor.Deps {
	return doctor.Deps{
		LookPath: func(string) (string, error) { return "/usr/local/bin/sbx", nil },
		// doctor ne regarde que l'erreur, jamais le FileInfo.
		Stat: func(string) (os.FileInfo, error) { return nil, nil },
		// Injectée comme les deux autres : sans elle, `den doctor` rendrait ici
		// le verdict du git du POSTE, et le contrat de sortie de la commande
		// deviendrait vert ou rouge selon la machine qui lance la suite.
		VersionGit: func() (string, error) { return "git version 2.43.0\n", nil },
		// Même raison, et elle est ici plus tranchante encore : sans injection,
		// `den doctor` avertirait ou non selon que la session qui lance la
		// suite a un agent SSH en marche.
		Getenv: func(nom string) string {
			if nom == "SSH_AUTH_SOCK" {
				return "/tmp/den-test/agent-ssh.sock"
			}
			return ""
		},
	}
}

func TestDoctorReussitQuandToutVaBien(t *testing.T) {
	home := denHomeDeTest(t)
	out, err := runDoctor(t, home, depsSaines())
	if err != nil {
		t.Fatalf("attendu une sortie nulle sur une config saine, obtenu : %v\n%s", err, out)
	}
	if !strings.Contains(out, "tout est en ordre") {
		t.Errorf("sortie = %q, attendu le message final de succès", out)
	}
	if strings.Contains(out, "[FAIL]") {
		t.Errorf("sortie = %q, aucun échec attendu", out)
	}
}

// LA propriété de D2, et la raison d'être du troisième niveau : un
// avertissement ne change PAS le code de sortie de `den doctor`. Sans elle, le
// contrôle d'SSH_AUTH_SOCK rendrait `den doctor` rouge sur toute machine où
// l'on travaille en local sans agent SSH — une configuration parfaitement
// saine, que den n'a aucun moyen de distinguer d'une configuration fautive.
//
// Le test exerce la commande ENTIÈRE (Execute), et non doctor.Run : c'est cobra
// qui traduit l'erreur rendue par RunE en code de retour, et c'est donc là que
// la propriété se vérifie. `err == nil` est exactement ce que `den doctor`
// rendra à un rc de 0.
func TestDoctorNEchouePasSurUnAvertissement(t *testing.T) {
	home := denHomeDeTest(t)
	deps := depsSaines()
	// Aucun agent SSH. La config du fixture ne déclare pas `ssh:`, donc le mode
	// est le défaut, agent-forward — c'est le cas nominal, pas un cas tordu.
	deps.Getenv = func(string) string { return "" }

	out, err := runDoctor(t, home, deps)
	if err != nil {
		t.Fatalf("un avertissement ne doit pas faire échouer den doctor, obtenu : %v\n%s", err, out)
	}
	if !strings.Contains(out, "[warn]") {
		t.Errorf("sortie = %q, attendu une ligne [warn]", out)
	}
	if strings.Contains(out, "[FAIL]") {
		t.Errorf("sortie = %q, aucun échec attendu : un agent SSH absent n'en est pas un", out)
	}
	// L'avertissement doit rester lisible dans le résumé final : « tout est en
	// ordre » sous une ligne [warn] se lirait comme un affichage résiduel.
	if strings.Contains(out, "tout est en ordre") {
		t.Errorf("sortie = %q, « tout est en ordre » contredit la ligne [warn]", out)
	}
	if !strings.Contains(out, "avertissement") {
		t.Errorf("sortie = %q, le résumé final doit signaler l'avertissement", out)
	}
	if !strings.Contains(out, "SSH_AUTH_SOCK") {
		t.Errorf("sortie = %q, attendu le diagnostic nommant SSH_AUTH_SOCK", out)
	}
}

// I1 — la COMBINAISON que rien n'exerçait : un avertissement ET un échec dans
// le même diagnostic. Les deux tests voisins n'en produisent jamais qu'un seul
// à la fois — TestDoctorNEchouePasSurUnAvertissement pose `Getenv → ""` sur une
// configuration saine, TestDoctorEchoueQuandSbxManque garde le socket — et
// aucun des deux n'atteint donc le seul endroit où la question se pose.
//
// Ce que ce test tient, et que RIEN ne tenait : l'ORDRE des deux blocs de
// sortie de newDoctorCmd. Le bloc « échec » doit précéder le bloc
// « avertissement », faute de quoi le second rend nil le premier n'ayant jamais
// été atteint. Mesuré avant d'écrire ce test, les deux blocs intervertis :
// la suite ENTIÈRE reste verte (rc=0), et le binaire rend 0 sur une
// installation cassée en affichant « aucun échec » sous une ligne [FAIL].
//
// Un avertissement doit donc être exactement ce qu'il annonce : sans effet sur
// le code de sortie, dans les DEUX sens — il n'en crée pas (test voisin), et il
// n'en efface pas (celui-ci).
func TestDoctorEchoueMemeAvecUnAvertissement(t *testing.T) {
	home := denHomeDeTest(t)
	deps := depsSaines()
	// L'avertissement : aucun agent SSH, sur une configuration dont le mode SSH
	// est le défaut (agent-forward — le fixture ne déclare pas de bloc `ssh:`).
	deps.Getenv = func(string) string { return "" }
	// L'échec, INDÉPENDANT du premier : sbx absent du PATH. Deux causes sans
	// rapport, pour qu'aucune ne puisse être tenue pour un effet de l'autre.
	deps.LookPath = func(string) (string, error) { return "", errors.New("introuvable dans le PATH") }

	out, err := runDoctor(t, home, deps)
	if err == nil {
		t.Fatalf("un échec doit RESTER un échec en présence d'un avertissement : "+
			"`den doctor` rendrait 0 sur une installation cassée ; sortie :\n%s", out)
	}
	// L'erreur ne compte QUE les échecs : un avertissement compté comme échec
	// serait l'autre moitié du défaut.
	if !strings.Contains(err.Error(), "1 diagnostic(s) en échec") {
		t.Errorf("erreur = %q, attendu le décompte des seuls échecs (1) ; "+
			"l'avertissement ne doit pas y être compté", err.Error())
	}
	// Les deux lignes coexistent : l'avertissement ne masque pas l'échec, et
	// l'échec ne fait pas taire l'avertissement.
	if !strings.Contains(out, "[FAIL]") {
		t.Errorf("sortie = %q, attendu une ligne [FAIL]", out)
	}
	if !strings.Contains(out, "[warn]") {
		t.Errorf("sortie = %q, attendu une ligne [warn] : un échec ne doit pas faire taire l'avertissement", out)
	}
	// L'épilogue ne doit RIEN affirmer de rassurant. « aucun échec » sous une
	// ligne [FAIL] est auto-contradictoire, et c'est ce que la sortie affichait
	// avec les deux blocs intervertis.
	for _, interdit := range []string{"aucun échec", "tout est en ordre"} {
		if strings.Contains(out, interdit) {
			t.Errorf("sortie = %q, ne doit pas contenir %q alors qu'un diagnostic est en échec",
				out, interdit)
		}
	}
}

func TestDoctorEchoueQuandSbxManque(t *testing.T) {
	home := denHomeDeTest(t)
	deps := depsSaines()
	deps.LookPath = func(string) (string, error) { return "", errors.New("introuvable dans le PATH") }

	out, err := runDoctor(t, home, deps)
	if err == nil {
		t.Fatal("attendu une erreur : sbx manquant est un échec de diagnostic")
	}
	if !strings.Contains(out, "[FAIL]") {
		t.Errorf("sortie = %q, attendu une ligne [FAIL]", out)
	}
	if !strings.Contains(out, "sbx") {
		t.Errorf("sortie = %q, attendu le diagnostic de sbx", out)
	}
	// Les autres diagnostics doivent quand même s'afficher : on ne s'arrête
	// jamais au premier problème.
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("sortie = %q, attendu le diagnostic de config.yaml malgré l'échec de sbx", out)
	}
}

// Les tests ci-dessus construisent la commande directement pour injecter leurs
// Deps, ce qui laisse le câblage de root.go (newDoctorCmd branché sur l'arbre
// racine avec doctor.DepsSysteme()) sans couverture. Celui-ci passe par
// NewRootCmd : il n'assert que l'atteignabilité et la sortie, jamais le code de
// retour lié à sbx.
func TestDoctorEstCableDansLArbreRacine(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())
	out, err := run(t, "doctor")
	// config.yaml est absent : au moins un diagnostic échoue, que sbx soit
	// installé ou non sur la machine.
	if err == nil {
		t.Error("attendu une erreur : config.yaml est absent du den home")
	}
	if !strings.Contains(out, "den home:") {
		t.Errorf("sortie = %q, attendu l'en-tête de doctor", out)
	}
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("sortie = %q, attendu le diagnostic de config.yaml", out)
	}
}

func TestDoctorEchoueSurConfigAbsente(t *testing.T) {
	out, err := runDoctor(t, t.TempDir(), depsSaines())
	if err == nil {
		t.Error("attendu une erreur quand la config est absente")
	}
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("sortie = %q, attendu une mention de config.yaml", out)
	}
}
