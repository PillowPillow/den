package sbx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
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

			e := &Exec{Bin: "sh", DelaiDeDrainage: 50 * time.Millisecond}
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
// `exec sleep 5` (pas de fork) ou un binaire direct.
//
// ⚠️ HYPOTHÈSE, et non un fait : que `sbx` laisse lui aussi un descendant tenant
// le tube (superviseur de microVM) n'a JAMAIS été observé — `sbx` n'est pas
// installé, tout ce qui précède est mesuré sur `sh`. Si l'hypothèse est fausse,
// cette borne ne sert jamais en production ; elle ne coûte rien pour autant, le
// drainage écourté d'un succès étant désormais rendu comme un succès. Inscrite
// au spec §14.1 avec ce qui la falsifierait.
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

	e := &Exec{Bin: "sh", DelaiDeDrainage: 50 * time.Millisecond}
	debut := time.Now()
	if _, err := e.Run(ctx, "-c", "sleep 5"); err == nil {
		t.Fatal("erreur attendue, obtenu nil")
	}
	if ecoule := time.Since(debut); ecoule > 2*time.Second {
		t.Errorf("Run a mis %v à rendre la main : la borne d'attente ne s'applique pas "+
			"(le petit-fils tient le tube pendant 5 s)", ecoule)
	}
}

// LE CHEMIN NOMINAL, celui qu'aucun test ne couvrait quand WaitDelay a été
// introduit : `sbx create` RÉUSSIT en laissant derrière lui un descendant qui a
// hérité du tube stdout. Le contexte n'est JAMAIS annulé ici.
//
// WaitDelay n'est pas armé seulement par l'annulation du contexte : son minuteur
// démarre aussi dès que Wait constate la sortie du process. Un succès dont les
// tubes sont fermés par WaitDelay fait rendre exec.ErrWaitDelay À LA PLACE de
// nil — donc « den n'a pas pu créer la sandbox » alors que la sandbox EXISTE, et
// l'abandon de la séquence de spawn (ni settle, ni attache).
//
// C'est exactement le scénario que la borne invoque pour se justifier — un
// superviseur qui survit à `sbx create` — et qui reste une HYPOTHÈSE sur `sbx`
// (spec §14.1) : le script ci-dessous est du `sh`, pas du `sbx`.
func TestExecRunNEchouePasQuandUnDescendantSurvitAUnSuccesDuProcess(t *testing.T) {
	cas := []struct {
		nom     string
		script  string
		attendu string
	}{
		{"descendant lancé après l'écriture", "echo demarree; sleep 5 &", "demarree\n"},
		{"descendant lancé avant l'écriture", "sleep 5 & echo cree", "cree\n"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			e := &Exec{Bin: "sh", DelaiDeDrainage: 50 * time.Millisecond}
			debut := time.Now()

			sortie, err := e.Run(context.Background(), "-c", c.script)
			if err != nil {
				t.Fatalf("le process est sorti avec SUCCÈS : Run ne doit pas rendre d'erreur ; err = %v", err)
			}
			if string(sortie) != c.attendu {
				t.Errorf("sortie = %q, attendue %q : fermer le tube ne doit pas perdre "+
					"ce que le fils direct a écrit avant de sortir", sortie, c.attendu)
			}
			// La borne doit tout de même s'appliquer : sans elle, ce test
			// passerait en attendant les 5 s du descendant, et ne prouverait
			// plus que le succès est rendu SANS attendre.
			if ecoule := time.Since(debut); ecoule > 2*time.Second {
				t.Errorf("Run a mis %v : la borne d'attente ne s'applique plus", ecoule)
			}
		})
	}
}

// La condition de la propriété précédente, isolée : le copieur d'os/exec draine
// le tube EN CONTINU, donc ce que le fils direct a écrit avant de sortir est
// déjà collecté quand WaitDelay ferme le tube.
//
// 240 Kio, très au-delà du tampon d'un tube (64 Kio) : une troncature se verrait
// ici, là où « demarree\n » tiendrait dans le tampon quoi qu'il arrive. Ce que
// le DESCENDANT écrirait après la fermeture est perdu, et c'est voulu.
func TestExecRunNeTronquePasLaSortieDejaEcriteParLeFilsDirect(t *testing.T) {
	const lignes = 40000
	e := &Exec{Bin: "sh", DelaiDeDrainage: 50 * time.Millisecond}

	sortie, err := e.Run(context.Background(), "-c", "yes ligne | head -n 40000; sleep 5 &")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if got := bytes.Count(sortie, []byte("\n")); got != lignes {
		t.Errorf("sortie tronquée : %d lignes sur %d attendues (%d octets)",
			got, lignes, len(sortie))
	}
}

// etatDeSortie rend un *os.ProcessState RÉEL pour le code de sortie demandé.
// Fabriqué en exécutant un process : les champs d'os.ProcessState ne sont pas
// exportés, et un double maison ne prouverait rien du type réellement inspecté.
func etatDeSortie(t *testing.T, script string) *os.ProcessState {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	_ = cmd.Run() // l'échec est attendu pour les codes non nuls
	if cmd.ProcessState == nil {
		t.Fatalf("aucun ProcessState pour %q", script)
	}
	return cmd.ProcessState
}

// Les trois conditions de la garde, une par une. Les tests de bout en bout ne
// couvrent que la première : os/exec ne rend ErrWaitDelay que sur un statut de
// succès, si bien que les deux autres portent sur des états qu'on ne sait pas
// provoquer à la demande. Sans ce test, elles resteraient sans preuve — et une
// garde trop large convertirait un ÉCHEC de sbx en succès silencieux.
func TestDrainageEcourteSurSuccesExigeLesTroisConditions(t *testing.T) {
	succes := etatDeSortie(t, "true")
	echec := etatDeSortie(t, "exit 3")
	autreErreur := errors.New("panne quelconque")

	cas := []struct {
		nom    string
		err    error
		errCtx error
		etat   *os.ProcessState
		veut   bool
	}{
		{"drainage écourté sur un succès", exec.ErrWaitDelay, nil, succes, true},
		{"drainage écourté, enveloppé", fmt.Errorf("x : %w", exec.ErrWaitDelay), nil, succes, true},
		{"autre erreur", autreErreur, nil, succes, false},
		{"aucune erreur", nil, nil, succes, false},
		{"contexte annulé", exec.ErrWaitDelay, context.Canceled, succes, false},
		{"délai du contexte dépassé", exec.ErrWaitDelay, context.DeadlineExceeded, succes, false},
		{"process sorti en échec", exec.ErrWaitDelay, nil, echec, false},
		{"process jamais démarré", exec.ErrWaitDelay, nil, nil, false},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			if got := drainageEcourteSurSucces(c.err, c.errCtx, c.etat); got != c.veut {
				t.Errorf("drainageEcourteSurSucces(%v, %v, %v) = %v, attendu %v",
					c.err, c.errCtx, c.etat, got, c.veut)
			}
		})
	}
}

// La valeur NULLE d'Exec doit être la valeur sûre : `&Exec{Bin: "sbx"}` sans
// réglage explicite doit être borné, pas suspendu sans fin. Le sens de 0 est
// inversé entre le champ (« prends le défaut ») et cmd.WaitDelay (« attends
// indéfiniment »), et c'est exactement le genre d'inversion qu'un refactor
// perd. Vérifié sur delaiEffectif plutôt qu'en exécutant un process : la borne
// est déjà prouvée par le test précédent, ici c'est le CHOIX du défaut.
func TestDelaiEffectifNeRendJamaisZero(t *testing.T) {
	if d := delaiEffectif(0); d != delaiDrainageDefaut {
		t.Errorf("delaiEffectif(0) = %v, attendu le défaut %v (0 = attente sans fin côté os/exec)",
			d, delaiDrainageDefaut)
	}
	if d := delaiEffectif(-1); d != delaiDrainageDefaut {
		t.Errorf("delaiEffectif(-1) = %v, attendu le défaut %v", d, delaiDrainageDefaut)
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
