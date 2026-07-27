package cli

import (
	"bytes"
	"strings"
	"testing"
)

// exécute la commande racine avec des arguments et retourne sa sortie standard.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestVersionAfficheLaVersion(t *testing.T) {
	Version = "1.2.3"
	out, err := run(t, "version")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(out, "1.2.3") {
		t.Errorf("sortie = %q, attendu contenant %q", out, "1.2.3")
	}
}

func TestCommandeInconnueEchoue(t *testing.T) {
	if _, err := run(t, "nexistepas"); err == nil {
		t.Error("attendu une erreur pour une commande inconnue, obtenu nil")
	}
}
