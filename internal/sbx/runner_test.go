package sbx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// L'échec de PREMIER CONTACT — sbx pas encore installé — est le plus probable
// de tous, et c'est le seul que l'utilisateur puisse réparer lui-même. Le
// message qu'il produit doit donc être en français, dire quoi faire, et ne pas
// laisser l'utilisateur seul devant « exec: "sbx": executable file not found in
// $PATH » (message d'os/exec, en anglais, sans remède).
//
// `den doctor` est nommé parce qu'il diagnostique EXACTEMENT ce cas
// (internal/doctor/doctor.go, étape 1 : « binaire sbx introuvable dans le
// PATH ») — et parce qu'avant ce test, `den doctor` n'apparaissait dans AUCUN
// message utilisateur du projet, seulement dans des commentaires.
//
// L'absence de l'anglais est assertée SÉPARÉMENT de la présence du français :
// un message qui ajouterait la traduction sans retirer la ligne d'os/exec
// resterait vert sur la seule assertion de présence.
func TestExecRunBinaireIntrouvableRendUnMessageActionnableEnFrancais(t *testing.T) {
	const bin = "den-binaire-qui-nexiste-pas-x7q"
	e := &Exec{Bin: bin}

	_, err := e.Run(context.Background(), "ls", "--json")
	if err == nil {
		t.Fatal("un binaire absent doit produire une erreur")
	}
	message := err.Error()

	for _, attendu := range []string{bin, "introuvable dans le PATH", "den doctor"} {
		if !strings.Contains(message, attendu) {
			t.Errorf("le message doit contenir %q ; obtenu : %s", attendu, message)
		}
	}
	for _, anglais := range []string{"executable file not found", "exec: "} {
		if strings.Contains(message, anglais) {
			t.Errorf("reste d'anglais %q dans le message : %s", anglais, message)
		}
	}
	// La chaîne d'erreurs ne doit pas être sacrifiée au message : c'est elle qui
	// permet à un appelant de reconnaître ce cas sans comparer des chaînes.
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("exec.ErrNotFound doit rester repérable dans la chaîne ; err = %v", err)
	}
}

// Même exigence sur Attach : c'est par lui que passent `den sh` et l'attache
// finale de `den <nest>`. Le message n'a aucune raison d'être en anglais d'un
// côté et en français de l'autre.
func TestExecAttachBinaireIntrouvableRendUnMessageActionnableEnFrancais(t *testing.T) {
	const bin = "den-binaire-qui-nexiste-pas-x7q"
	e := &Exec{Bin: bin}

	err := e.Attach(context.Background(), "exec", "-it", "api", "bash", "-l")
	if err == nil {
		t.Fatal("un binaire absent doit produire une erreur")
	}
	message := err.Error()

	for _, attendu := range []string{bin, "introuvable dans le PATH", "den doctor"} {
		if !strings.Contains(message, attendu) {
			t.Errorf("le message doit contenir %q ; obtenu : %s", attendu, message)
		}
	}
	if strings.Contains(message, "executable file not found") {
		t.Errorf("reste d'anglais dans le message : %s", message)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("exec.ErrNotFound doit rester repérable dans la chaîne ; err = %v", err)
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
//
// # Pourquoi ce test se synchronise sur un témoin plutôt que sur l'horloge
//
// Sa précondition — le process TOURNE ENCORE quand le contexte est interrompu —
// était supposée, jamais observée : la version précédente interrompait après
// 20 ms fixes. Deux défauts distincts en sont sortis, tous deux dans la sonde et
// aucun dans Run :
//
//  1. Les DEUX interrupteurs couraient. Le cas « délai dépassé » portait une
//     échéance à 20 ms, et une goroutine commune appelait en plus cancel() après
//     20 ms ; celui qui gagnait fixait ctx.Err(). Quand la goroutine passait la
//     première, le motif observé était Canceled et les quatre assertions
//     tombaient d'un coup. Mesuré : 2 échecs sur 12 sous charge CPU, et un échec
//     sur une suite complète `go test ./...`.
//  2. L'interruption devançait le fork/exec. Latence mesurée entre Start et le
//     premier octet exécuté par le script, sur 300 tirages : p50 = 0,7 ms,
//     max = 1,0 ms à vide ; p50 = 8,5 ms, p99 = 22,2 ms, MAX = 26,2 ms sous
//     saturation CPU 12×. Une échéance à 20 ms tombe donc RÉGULIÈREMENT avant le
//     démarrage : Cmd.Start rend alors ctx.Err() sans rien lancer, il n'y a
//     aucun *exec.ExitError à retrouver, et le test échouait sur la seule
//     assertion qui exige un process réellement tué.
//
// D'où le témoin : le script crée un fichier avant de dormir, et son existence
// est ASSERTÉE après coup. Le cas « annulation » devient exact — la goroutine
// attend le témoin puis annule, sans aucune hypothèse d'horloge. Le cas « délai
// dépassé » ne peut pas l'être, une échéance courant dès sa création : sa marge
// est portée à 500 ms, soit 19× le pire démarrage mesuré sous saturation, et le
// témoin y sert de garde — s'il manquait, l'échec nommerait la marge à revoir au
// lieu d'accuser une propriété de Run qui n'a pas été exercée.
func TestExecRunMotifDAnnulationSurvitALaMortDuProcess(t *testing.T) {
	// margeEcheance : voir la godoc ci-dessus. 500 ms = 19× le MAX mesuré
	// (26,2 ms) sous saturation CPU 12×.
	const margeEcheance = 500 * time.Millisecond

	cas := []struct {
		nom string
		// contexte rend un contexte dont l'interruption EN VOL est déjà armée,
		// et dont c'est le SEUL interrupteur. temoin est le fichier que le
		// script crée en démarrant.
		contexte       func(t *testing.T, temoin string) context.Context
		motif          error
		motifAbsent    error
		messageAttendu string
	}{
		{
			nom: "annulation",
			contexte: func(t *testing.T, temoin string) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				// Aucune échéance sur ce contexte : ctx.Err() ne peut valoir
				// que context.Canceled. Et l'annulation n'est déclenchée
				// qu'une fois le démarrage CONSTATÉ : ce cas-ci ne fait aucune
				// hypothèse de durée.
				go func() {
					attendTemoin(temoin)
					cancel()
				}()
				return ctx
			},
			motif:          context.Canceled,
			motifAbsent:    context.DeadlineExceeded,
			messageAttendu: "interrompu (annulé)",
		},
		{
			nom: "délai dépassé",
			contexte: func(t *testing.T, _ string) context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), margeEcheance)
				// cancel n'est appelé qu'APRÈS le corps du test : pendant le
				// Run, l'échéance est le seul interrupteur, et ctx.Err() ne peut
				// valoir que context.DeadlineExceeded.
				t.Cleanup(cancel)
				return ctx
			},
			motif:          context.DeadlineExceeded,
			motifAbsent:    context.Canceled,
			messageAttendu: "interrompu (délai dépassé)",
		},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			temoin := filepath.Join(t.TempDir(), "demarre")
			ctx := c.contexte(t, temoin)

			e := &Exec{Bin: "sh", DelaiDeDrainage: 50 * time.Millisecond}
			// `exec sleep 5` et non `sleep 5` : sans exec, dash forke et le
			// petit-fils garde le tube (cf.
			// TestExecRunBorneLAttenteQuandUnPetitFilsGardeLeTube).
			_, err := e.Run(ctx, "-c", "touch "+temoin+"; exec sleep 5")
			if err == nil {
				t.Fatal("erreur attendue, obtenu nil")
			}
			// LA PRÉCONDITION, vérifiée et non supposée : tout ce test porte sur
			// une interruption survenue PENDANT l'exécution. Si le process n'a
			// jamais démarré, Cmd.Start a rendu ctx.Err() sans rien lancer — un
			// cas déjà couvert par TestExecRunContexteAnnuleAvantDemarrage, et
			// qui ne prouve rien de celui-ci.
			if _, errTemoin := os.Stat(temoin); errTemoin != nil {
				t.Fatalf("le process n'a jamais démarré (témoin %s absent) : l'interruption a "+
					"devancé le fork/exec, ce test ne mesure alors rien de ce qu'il prétend — "+
					"augmente la marge d'échéance (%v)", temoin, margeEcheance)
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

// attendTemoin bloque jusqu'à l'apparition du fichier que le script crée en
// démarrant, et rend la main au plus tard au bout de limiteTemoin.
//
// Appelée depuis une goroutine : elle ne peut donc pas faire échouer le test
// elle-même (t.Fatalf hors de la goroutine de test est interdit). Elle abandonne
// silencieusement à l'expiration, et c'est le Stat du corps du test qui
// diagnostique — un seul endroit qui juge, celui qui a le droit de le faire.
func attendTemoin(temoin string) {
	// 100× le pire démarrage mesuré sous saturation CPU 12× (26,2 ms) : cette
	// borne n'existe que pour ne pas laisser fuir une goroutine si le script
	// échoue à démarrer, jamais pour arbitrer une course.
	const limiteTemoin = 3 * time.Second
	echeance := time.Now().Add(limiteTemoin)
	for time.Now().Before(echeance) {
		if _, err := os.Stat(temoin); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
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
//
// L'annulation attend le témoin, pour la même raison que
// TestExecRunMotifDAnnulationSurvitALaMortDuProcess (voir sa godoc) — mais ici
// le risque n'était pas un échec, c'était un SUCCÈS À VIDE : annulée avant que
// dash n'ait forké (jusqu'à 26,2 ms de latence de démarrage mesurée sous
// saturation, pour un délai fixé à 20 ms), Run rendait la main aussitôt, sans
// petit-fils, sans tube tenu et sans que la borne ne serve — et l'assertion
// « moins de 2 s » passait quand même. Un test qui passe à vide est
// indiscernable d'un test qui passe.
//
// LA FORME DU SCRIPT EST LE TEST. `sleep 5 & touch …; wait` place le fork AVANT
// le témoin : quand le témoin apparaît, le petit-fils existe déjà et a hérité du
// tube. La forme naïve — `touch …; sleep 5` — fait l'inverse et RUINE le test,
// mesuré : le témoin y précède le fork, l'annulation tue dash avant qu'il ait
// forké, aucun petit-fils ne tient le tube, et Run rend la main en 13 ms même
// avec WaitDelay désarmé. Le tableau, contexte annulé sur témoin :
//
//	"touch T; sleep 5"        WaitDelay=0     →   13 ms   (aucun petit-fils)
//	"sleep 5 & touch T; wait" WaitDelay=0     → 5001 ms   (le tube est tenu)
//	"sleep 5 & touch T; wait" WaitDelay=50ms  →   59 ms   (la borne s'applique)
//	"sleep 5 & touch T; wait" WaitDelay=2s    → 2006 ms   (à sa valeur)
//
// C'est la deuxième ligne qui donne sa valeur à la troisième : sans elle, la
// borne n'est pas ce qui fait rendre la main.
func TestExecRunBorneLAttenteQuandUnPetitFilsGardeLeTube(t *testing.T) {
	temoin := filepath.Join(t.TempDir(), "demarre")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		attendTemoin(temoin)
		cancel()
	}()

	e := &Exec{Bin: "sh", DelaiDeDrainage: 50 * time.Millisecond}
	debut := time.Now()
	// Pas d'`exec` ici, contrairement à l'autre test, et c'est tout le sujet :
	// le `&` forke un petit-fils, et c'est lui qui garde le tube. L'ordre
	// fork-puis-témoin est ce qui rend la précondition observable (godoc).
	if _, err := e.Run(ctx, "-c", "sleep 5 & touch "+temoin+"; wait"); err == nil {
		t.Fatal("erreur attendue, obtenu nil")
	}
	// La précondition, vérifiée : sans témoin, le petit-fils n'a pas été forké,
	// aucun tube n'est tenu, et l'assertion de durée ci-dessous serait vraie
	// pour la mauvaise raison.
	if _, err := os.Stat(temoin); err != nil {
		t.Fatalf("le témoin %s est absent : le petit-fils n'a jamais été forké, aucun tube "+
			"n'était tenu, et la borne d'attente n'a donc pas été exercée", temoin)
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

// L'accès SSH de TOUTE sandbox repose sur un mécanisme que rien, dans ce
// fichier, ne rend visible : ni Run ni Attach ne renseignent cmd.Env, donc le
// process sbx hérite de l'environnement de den — et avec lui de SSH_AUTH_SOCK,
// le socket de l'agent SSH de l'hôte. C'est le seul support de
// `ssh.mode: agent-forward`, qui est le DÉFAUT de la configuration
// (internal/config/config.go), et qui n'ajoute ni argument à l'argv de
// `sbx create` ni entrée au mixin.
//
// Rien dans le code n'exprimait cette dépendance : renseigner `cmd.Env = …`,
// pour n'importe quelle bonne raison — neutraliser une variable, restreindre
// l'environnement — couperait l'accès SSH de toutes les sandboxes sans casser
// un seul autre test. Ces deux tests-ci sont ce qui l'interdit ; le précédent
// existe déjà dans le projet (internal/worktree pose bien un cmd.Env pour
// neutraliser la configuration git).
//
// CE QUI EST PROUVÉ ICI, et rien de plus : le process lancé par den reçoit
// l'environnement de den. Que sbx propage ensuite ce socket DANS la microVM
// n'est pas vérifiable sans sbx, et reste une hypothèse consignée au spec.
//
// La valeur est POSÉE par le test (t.Setenv) et non présupposée : asserter que
// SSH_AUTH_SOCK est non vide sans l'avoir posé rendrait le test vert sur un
// poste où un agent tourne, et rouge partout ailleurs. t.Setenv interdit
// t.Parallel dans le test qui l'appelle.
func TestExecRunTransmetLEnvironnementDeDen(t *testing.T) {
	const socket = "/tmp/den-test-agent-ssh-run.sock"
	t.Setenv("SSH_AUTH_SOCK", socket)

	e := &Exec{Bin: "sh"}
	out, err := e.Run(context.Background(), "-c", `printf %s "$SSH_AUTH_SOCK"`)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if vu := string(out); vu != socket {
		t.Errorf("SSH_AUTH_SOCK vu par le process = %q, attendu %q — le process lancé par Run "+
			"doit hériter de l'environnement de den (cmd.Env laissé nil) ; sans cet héritage, "+
			"`ssh.mode: agent-forward`, qui est le défaut, ne donne plus aucun accès SSH aux sandboxes",
			vu, socket)
	}
}

// Même propriété sur Attach. Elle compte autant : `den sh` et l'attache finale
// de `den <nest>` passent par là, et un environnement amputé n'y serait pas
// plus visible que sur Run.
//
// Le témoin passe par un FICHIER et non par stdout, parce qu'Attach branche
// délibérément cmd.Stdout sur celui du processus courant — il n'y a rien à
// capturer sans détourner os.Stdout de toute la suite. Le fichier vit dans
// t.TempDir() : rien n'est écrit hors des bornes du test.
func TestExecAttachTransmetLEnvironnementDeDen(t *testing.T) {
	const socket = "/tmp/den-test-agent-ssh-attach.sock"
	t.Setenv("SSH_AUTH_SOCK", socket)
	temoin := filepath.Join(t.TempDir(), "socket-vu")

	e := &Exec{Bin: "sh"}
	// `sh -c SCRIPT ARG0` : le premier argument après le script devient $0.
	if err := e.Attach(context.Background(), "-c", `printf %s "$SSH_AUTH_SOCK" > "$0"`, temoin); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	contenu, err := os.ReadFile(temoin)
	if err != nil {
		t.Fatalf("lecture du témoin %s : %v", temoin, err)
	}
	if vu := string(contenu); vu != socket {
		t.Errorf("SSH_AUTH_SOCK vu par le process = %q, attendu %q — le process lancé par Attach "+
			"doit hériter de l'environnement de den (cmd.Env laissé nil)", vu, socket)
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
