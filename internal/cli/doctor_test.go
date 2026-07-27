package cli

import (
	"strings"
	"testing"
)

func TestDoctorSurConfigSaine(t *testing.T) {
	denHomeDeTest(t)
	out, err := run(t, "doctor")
	// sbx n'est pas installé sur la machine de test : la commande DOIT échouer,
	// mais après avoir affiché tous les diagnostics.
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("sortie = %q, attendu le diagnostic de config.yaml", out)
	}
	if !strings.Contains(out, "sbx") {
		t.Errorf("sortie = %q, attendu le diagnostic de sbx", out)
	}
	_ = err // le code de sortie dépend de la présence de sbx sur la machine
}

func TestDoctorEchoueSurConfigAbsente(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())
	out, err := run(t, "doctor")
	if err == nil {
		t.Error("attendu une erreur quand la config est absente")
	}
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("sortie = %q, attendu une mention de config.yaml", out)
	}
}
