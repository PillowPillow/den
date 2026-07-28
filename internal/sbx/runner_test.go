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

// Un contexte annulé AVANT le démarrage du process est le seul cas où os/exec
// remonte le motif de lui-même : Cmd.Start rend ctx.Err() sans rien lancer.
// C'est le cas facile, et il ne prouve RIEN du cas suivant.
func TestExecRunContexteAnnuleAvantDemarrage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	e := &Exec{Bin: "sh"}
	if _, err := e.Run(ctx, "-c", "true"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, attendu context.Canceled dans la chaîne", err)
	}
}

// Le cas RÉEL du Ctrl-C : le contexte est annulé PENDANT l'exécution. Mesuré
// trois fois (T11 deux fois, puis ici) : os/exec tue le process et Cmd.Wait
// PRÉFÈRE l'erreur du process à celle du contexte, si bien que cmd.Run rend un
// « signal: killed » qui n'enveloppe aucun motif de contexte. Le commentaire
// d'ErreurExec affirmait pourtant la propriété ci-dessous — d'où le relevé
// explicite de ctx.Err() dans Run.
//
// Les deux motifs sont exercés, parce qu'ils NE se comportent pas pareil pour
// l'utilisateur (Ctrl-C contre timeout du settle-loop) et que rien ne garantit
// qu'os/exec les traite identiquement.
func TestExecRunMotifDAnnulationSurvitALaMortDuProcess(t *testing.T) {
	cas := []struct {
		nom            string
		contexte       func() (context.Context, context.CancelFunc)
		motif          error
		motifAbsent    error
		messageAttendu string
	}{
		{
			nom: "annulation",
			contexte: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			motif:          context.Canceled,
			motifAbsent:    context.DeadlineExceeded,
			messageAttendu: "interrompu (annulé)",
		},
		{
			nom: "délai dépassé",
			contexte: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 20*time.Millisecond)
			},
			motif:          context.DeadlineExceeded,
			motifAbsent:    context.Canceled,
			messageAttendu: "interrompu (délai dépassé)",
		},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			ctx, cancel := c.contexte()
			defer cancel()
			go func() {
				time.Sleep(20 * time.Millisecond)
				cancel()
			}()

			e := &Exec{Bin: "sh", DelaiApresAnnulation: 50 * time.Millisecond}
			_, err := e.Run(ctx, "-c", "exec sleep 5")
			if err == nil {
				t.Fatal("erreur attendue, obtenu nil")
			}
			if !errors.Is(err, c.motif) {
				t.Errorf("errors.Is(err, %v) = false ; err = %v", c.motif, err)
			}
			if errors.Is(err, c.motifAbsent) {
				t.Errorf("errors.Is(err, %v) = true : les deux motifs sont confondus ; err = %v",
					c.motifAbsent, err)
			}
			// Le message doit DIRE l'interruption : « signal: killed » nu se lit
			// comme un crash de sbx, et c'est précisément la confusion que le
			// commentaire d'ErreurExec prétendait éviter.
			if !strings.Contains(err.Error(), c.messageAttendu) {
				t.Errorf("message = %q, attendu contenant %q", err.Error(), c.messageAttendu)
			}
			// Sans cette assertion, joindre le motif d'annulation pourrait avoir
			// remplacé la chaîne d'origine au lieu de s'y ajouter : les deux
			// autres propriétés d'ErreurExec passeraient à la trappe.
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Errorf("le *exec.ExitError d'origine a été perdu ; err = %v (%T)", err, err)
			}
		})
	}
}

// Tuer le process ne suffit PAS à débloquer Run : dash forke pour `sleep 5`, et
// le petit-fils garde le tube stdout ouvert — Wait bloque sur la copie tant
// qu'il vit. Mesuré : 5,007 s pour une annulation à 20 ms, contre 21 ms avec
// `exec sleep 5` (pas de fork) ou un binaire direct. sbx supervisant une
// microVM, ce petit-fils peut être un démon qui ne meurt jamais.
//
// Le test ne mesure pas une durée absolue (ce serait flaky sous charge) mais la
// SÉPARATION : rendre la main nettement avant la fin naturelle du petit-fils.
func TestExecRunBorneLAttenteQuandUnPetitFilsGardeLeTube(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	e := &Exec{Bin: "sh", DelaiApresAnnulation: 50 * time.Millisecond}
	debut := time.Now()
	if _, err := e.Run(ctx, "-c", "sleep 5"); err == nil {
		t.Fatal("erreur attendue, obtenu nil")
	}
	if ecoule := time.Since(debut); ecoule > 2*time.Second {
		t.Errorf("Run a mis %v à rendre la main : la borne d'attente ne s'applique pas "+
			"(le petit-fils tient le tube pendant 5 s)", ecoule)
	}
}

// La valeur NULLE d'Exec doit être la valeur sûre : `&Exec{Bin: "sbx"}` sans
// réglage explicite doit être borné, pas suspendu sans fin. Le sens de 0 est
// inversé entre le champ (« prends le défaut ») et cmd.WaitDelay (« attends
// indéfiniment »), et c'est exactement le genre d'inversion qu'un refactor
// perd. Vérifié sur delaiEffectif plutôt qu'en exécutant un process : la borne
// est déjà prouvée par le test précédent, ici c'est le CHOIX du défaut.
func TestDelaiEffectifNeRendJamaisZero(t *testing.T) {
	if d := delaiEffectif(0); d != delaiApresAnnulationDefaut {
		t.Errorf("delaiEffectif(0) = %v, attendu le défaut %v (0 = attente sans fin côté os/exec)",
			d, delaiApresAnnulationDefaut)
	}
	if d := delaiEffectif(-1); d != delaiApresAnnulationDefaut {
		t.Errorf("delaiEffectif(-1) = %v, attendu le défaut %v", d, delaiApresAnnulationDefaut)
	}
	if d := delaiEffectif(50 * time.Millisecond); d != 50*time.Millisecond {
		t.Errorf("delaiEffectif(50ms) = %v : un réglage explicite doit être respecté", d)
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
