package sbx

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Ces tests exercent Exec, PAS sbx : le binaire invoqué est toujours "sh" (ou
// un nom inventé pour le cas "introuvable"), jamais "sbx". Ils restent
// hermétiques et rapides (millisecondes) — aucun réseau, rien en dehors du
// processus courant.

// Un binaire introuvable doit laisser exec.ErrNotFound repérable dans la
// chaîne : c'est ce qui permet à un appelant de distinguer « sbx absent du
// PATH » d'un échec applicatif quelconque.
func TestExecRunBinaireIntrouvable(t *testing.T) {
	e := &Exec{Bin: "den-binaire-qui-nexiste-pas-x7q"}
	if _, err := e.Run(context.Background(), "ls"); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("err = %v, attendu exec.ErrNotFound dans la chaîne", err)
	}
}

// Le code de sortie doit rester accessible via errors.As, et stderr doit
// survivre dans le message : les deux sont perdus si l'erreur n'est pas
// enveloppée avec %w.
func TestExecRunCodeSortieEtStderrPreserves(t *testing.T) {
	e := &Exec{Bin: "sh"}
	_, err := e.Run(context.Background(), "-c", "echo boum >&2; exit 3")
	if err == nil {
		t.Fatalf("erreur attendue, obtenu nil")
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("errors.As(err, &exitErr) doit réussir ; err = %v (%T)", err, err)
	}
	if exitErr.ExitCode() != 3 {
		t.Errorf("code de sortie = %d, attendu 3", exitErr.ExitCode())
	}
	if !strings.Contains(err.Error(), "boum") {
		t.Errorf("message = %q, doit contenir la sortie stderr (%q)", err.Error(), "boum")
	}
}

// Un contexte déjà annulé doit laisser context.Canceled repérable : un Ctrl-C
// pendant un `create` ne doit pas produire un message indiscernable d'un
// échec sbx quelconque.
func TestExecRunContexteAnnule(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	e := &Exec{Bin: "sh"}
	if _, err := e.Run(ctx, "-c", "true"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, attendu context.Canceled dans la chaîne", err)
	}
}

// Attach est la SEULE méthode où l'annulation du contexte ne doit RIEN faire :
// un Ctrl-C tapé dans le shell de la sandbox est délivré par le driver tty au
// groupe de processus, pas relayé via le contexte de den ; si le contexte se
// termine pendant qu'Attach tourne, le shell ne doit ni être tué, ni voir son
// issue normale remplacée par une erreur de contexte.
func TestExecAttachIgnoreAnnulationContexte(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	e := &Exec{Bin: "sh"}
	if err := e.Attach(ctx, "-c", "sleep 0.05"); err != nil {
		t.Errorf("erreur inattendue : %v (l'annulation du contexte ne doit pas affecter Attach)", err)
	}
}
