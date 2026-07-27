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

// depsSaines : sbx présent, tous les repos sur disque.
func depsSaines() doctor.Deps {
	return doctor.Deps{
		LookPath: func(string) (string, error) { return "/usr/local/bin/sbx", nil },
		// doctor ne regarde que l'erreur, jamais le FileInfo.
		Stat: func(string) (os.FileInfo, error) { return nil, nil },
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
