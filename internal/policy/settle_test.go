package policy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/sbx"
)

// optionsTest : horloge et sommeil injectés, pour que la suite reste instantanée.
func optionsTest(t *testing.T) (Options, *int) {
	t.Helper()
	dormi := 0
	faux := time.Unix(0, 0)
	return Options{
		Timeout:    60 * time.Second,
		Intervalle: 2 * time.Second,
		Sommeil: func(d time.Duration) {
			dormi++
			faux = faux.Add(d)
		},
		Maintenant: func() time.Time { return faux },
	}, &dormi
}

func autorise(sandbox string, hotes ...string) map[string]sbx.Reponse {
	m := make(map[string]sbx.Reponse, len(hotes))
	for _, h := range hotes {
		cle := strings.Join([]string{"policy", "check", "network", "--sandbox", sandbox, "--json", h}, " ")
		m[cle] = sbx.Reponse{Sortie: []byte(`{"allowed": true}`)}
	}
	return m
}

func TestSettlePasseQuandToutEstAutorise(t *testing.T) {
	o, dormi := optionsTest(t)
	f := &sbx.Fake{Reponses: autorise("api", "github.com", "api.anthropic.com")}

	err := Settle(context.Background(), f, "api", []string{"github.com", "api.anthropic.com"}, o)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if *dormi != 0 {
		t.Errorf("aucun sommeil ne doit être nécessaire quand tout passe du premier coup (%d)", *dormi)
	}
}

// L'argv exact importe : --sandbox scope l'évaluation à la policy de CETTE
// sandbox. Sans lui, on validerait la policy globale — un vert qui ne prouve
// rien sur ce qu'on vient de poser.
func TestSettleInterrogeLaPolicyScopee(t *testing.T) {
	o, _ := optionsTest(t)
	f := &sbx.Fake{Reponses: autorise("api", "github.com")}

	if err := Settle(context.Background(), f, "api", []string{"github.com"}, o); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !f.AAppele("policy", "check", "network", "--sandbox", "api", "--json", "github.com") {
		t.Errorf("argv attendu avec --sandbox ; appels : %v", f.Appels)
	}
}

// Fail-closed : un hôte qui ne passe jamais doit sortir en erreur, en NOMMANT
// les hôtes bloqués — c'est tout ce que l'utilisateur aura pour diagnostiquer.
func TestSettleEchoueEnNommantLesHotesBloques(t *testing.T) {
	o, _ := optionsTest(t)
	reponses := autorise("api", "github.com")
	f := &sbx.Fake{
		Reponses: reponses,
		Defaut:   sbx.Reponse{Sortie: []byte(`{"allowed": false}`)},
	}

	err := Settle(context.Background(), f, "api", []string{"github.com", "bloque.exemple.test"}, o)
	if err == nil {
		t.Fatal("un hôte durablement bloqué doit produire une erreur (fail-closed)")
	}
	if !strings.Contains(err.Error(), "bloque.exemple.test") {
		t.Errorf("le message doit nommer l'hôte bloqué ; obtenu : %v", err)
	}
	if strings.Contains(err.Error(), "github.com") {
		t.Errorf("le message ne doit PAS lister les hôtes déjà passés ; obtenu : %v", err)
	}
}

// La propagation n'étant pas instantanée, un hôte d'abord refusé puis autorisé
// doit finir par passer — c'est la raison d'être de la boucle.
func TestSettleAttendLaPropagation(t *testing.T) {
	o, dormi := optionsTest(t)
	f := &fakeProgressif{autoriseApres: 3}

	if err := Settle(context.Background(), f, "api", []string{"lent.exemple.test"}, o); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if *dormi == 0 {
		t.Error("la boucle doit avoir dormi au moins une fois")
	}
	if f.appels < 3 {
		t.Errorf("appels = %d, attendu au moins 3", f.appels)
	}
}

// Un champ `allowed` absent = changement de schéma sbx. Le lire comme false
// ferait tourner la boucle jusqu'au timeout en accusant le réseau.
func TestSettleRefuseUneSortieSansChampAllowed(t *testing.T) {
	o, _ := optionsTest(t)
	f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte(`{"autre": "chose"}`)}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("une sortie sans champ allowed doit échouer immédiatement")
	}
	if !strings.Contains(err.Error(), "allowed") || !strings.Contains(err.Error(), `{"autre": "chose"}`) {
		t.Errorf("le message doit nommer le champ manquant et montrer la sortie brute ; obtenu : %v", err)
	}
}

func TestSettleSansHote(t *testing.T) {
	o, _ := optionsTest(t)
	f := &sbx.Fake{Defaut: sbx.Reponse{Err: errors.New("ne doit pas être appelé")}}

	if err := Settle(context.Background(), f, "api", nil, o); err != nil {
		t.Errorf("une allowlist vide n'est pas une erreur : %v", err)
	}
	if len(f.Appels) != 0 {
		t.Errorf("aucun appel ne doit être fait ; appels : %v", f.Appels)
	}
}

func TestSettleRespecteLAnnulationDuContexte(t *testing.T) {
	o, _ := optionsTest(t)
	ctx, annule := context.WithCancel(context.Background())
	annule()

	f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte(`{"allowed": false}`)}}
	err := Settle(ctx, f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("un contexte annulé doit interrompre la boucle")
	}
	// « err != nil » seul ne prouverait rien : l'horloge injectée finit par
	// atteindre le timeout, et une boucle qui IGNORE le contexte renverrait elle
	// aussi une erreur — celle du fail-closed. Ce qu'on vérifie, c'est que
	// l'interruption vient bien de l'annulation, et qu'aucun appel n'a été tenté
	// sur un contexte déjà mort.
	if !errors.Is(err, context.Canceled) {
		t.Errorf("l'erreur doit envelopper context.Canceled ; obtenu : %v", err)
	}
	if len(f.Appels) != 0 {
		t.Errorf("aucun appel sbx sur un contexte annulé ; appels : %v", f.Appels)
	}
}

// Un hôte déjà autorisé n'est plus réinterrogé : chaque tour ne sonde QUE les
// hôtes encore bloqués. Sans ça, une allowlist de vingt hôtes dont un seul est
// lent multiplierait par vingt les appels à sbx à chaque tour, et le message de
// timeout pourrait se remettre à citer des hôtes qui, eux, passent.
func TestSettleNeReinterrogePasLesHotesDejaPasses(t *testing.T) {
	o, _ := optionsTest(t)
	f := &fakeParHote{autoriseApres: map[string]int{"lent.exemple.test": 3}}

	if err := Settle(context.Background(), f, "api", []string{"rapide.exemple.test", "lent.exemple.test"}, o); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if n := f.appels["rapide.exemple.test"]; n != 1 {
		t.Errorf("l'hôte autorisé du premier coup a été interrogé %d fois, attendu 1 ; appels : %v", n, f.appels)
	}
	if n := f.appels["lent.exemple.test"]; n != 3 {
		t.Errorf("l'hôte lent a été interrogé %d fois, attendu 3 ; appels : %v", n, f.appels)
	}
}

// Rien n'oblige un appelant à passer par OptionsDefaut() : les Options se
// construisent aussi à la main. Des champs laissés à zéro doivent produire une
// erreur nommant les fautifs — jamais une panique de nil sur Sommeil ou
// Maintenant, et surtout jamais un settle-loop silencieusement désarmé (un
// Timeout à zéro rend la boucle sans patience, un Intervalle à zéro la fait
// marteler sbx sans répit). Deviner à la place de l'appelant ferait de la seule
// garde réseau de den une garde qui ne garde rien, sans le dire.
func TestSettleRefuseDesOptionsIncompletes(t *testing.T) {
	completes := func() Options {
		return Options{
			Timeout:    30 * time.Second,
			Intervalle: time.Second,
			Sommeil:    func(time.Duration) {},
			Maintenant: time.Now,
		}
	}
	sansTimeout := completes()
	sansTimeout.Timeout = 0
	sansIntervalle := completes()
	sansIntervalle.Intervalle = 0
	sansSommeil := completes()
	sansSommeil.Sommeil = nil
	sansMaintenant := completes()
	sansMaintenant.Maintenant = nil
	// Une RELATION absurde, que l'inspection champ par champ ne voit pas :
	// 30 s de sommeil réel pour un timeout annoncé à 1 s.
	intervalleTropGrand := completes()
	intervalleTropGrand.Timeout = time.Second
	intervalleTropGrand.Intervalle = 30 * time.Second

	cas := []struct {
		nom     string
		options Options
		champ   string
		// sortie : ce que renvoie sbx. Choisie pour que la SUPPRESSION de la
		// validation fasse échouer le cas vite (erreur nil ou panique), au lieu
		// de le laisser boucler.
		sortie string
	}{
		{"tout à zéro", Options{}, "Timeout", `{"allowed": true}`},
		{"sans Timeout", sansTimeout, "Timeout", `{"allowed": true}`},
		{"sans Intervalle", sansIntervalle, "Intervalle", `{"allowed": true}`},
		{"sans Sommeil", sansSommeil, "Sommeil", `{"allowed": false}`},
		{"sans Maintenant", sansMaintenant, "Maintenant", `{"allowed": false}`},
		{"Intervalle plus grand que Timeout", intervalleTropGrand, "Intervalle", `{"allowed": true}`},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte(c.sortie)}}

			err := Settle(context.Background(), f, "api", []string{"github.com"}, c.options)
			if err == nil {
				t.Fatal("des Options incomplètes doivent être refusées")
			}
			if !strings.Contains(err.Error(), c.champ) {
				t.Errorf("le message doit nommer le champ %s ; obtenu : %v", c.champ, err)
			}
			if !strings.Contains(err.Error(), "OptionsDefaut") {
				t.Errorf("le message doit orienter vers OptionsDefaut() ; obtenu : %v", err)
			}
			if len(f.Appels) != 0 {
				t.Errorf("des Options invalides ne doivent produire AUCUN appel sbx ; appels : %v", f.Appels)
			}
		})
	}
}

// OptionsDefaut doit rester acceptable par Settle : c'est le seul constructeur
// documenté, et le test ci-dessus n'aurait aucune valeur si la validation
// refusait aussi les options par défaut.
func TestOptionsDefautEstAcceptee(t *testing.T) {
	f := &sbx.Fake{Reponses: autorise("api", "github.com")}

	if err := Settle(context.Background(), f, "api", []string{"github.com"}, OptionsDefaut()); err != nil {
		t.Fatalf("OptionsDefaut() doit être utilisable telle quelle : %v", err)
	}
}

// ---------------------------------------------------------------------------
// Round de correction 1 : les chemins que la première suite n'atteignait pas.
// ---------------------------------------------------------------------------

// horloge est l'horloge injectée, mais qui se souvient : elle donne accès au
// temps ÉCOULÉ, pas seulement au nombre de sommeils. Sans ça, ni la valeur de
// Timeout ni celle d'Intervalle ne sont observables depuis un test.
type horloge struct {
	dormi   int
	debut   time.Time
	courant time.Time
}

func nouvelleHorloge() *horloge {
	d := time.Unix(0, 0)
	return &horloge{debut: d, courant: d}
}

func (h *horloge) options(timeout, intervalle time.Duration) Options {
	return Options{
		Timeout:    timeout,
		Intervalle: intervalle,
		Sommeil: func(d time.Duration) {
			h.dormi++
			h.courant = h.courant.Add(d)
		},
		Maintenant: func() time.Time { return h.courant },
	}
}

func (h *horloge) ecoule() time.Duration { return h.courant.Sub(h.debut) }

// C-1 — quand l'invocation de sbx échoue SANS verdict exploitable, den doit
// échouer. C'est la branche qui décide si den attache un shell alors que la
// policy n'a jamais pu être vérifiée : un fail-open y serait le « ça marche à
// moitié » que le §7 interdit.
func TestSettleEchoueQuandSbxEchoueSansVerdict(t *testing.T) {
	o, _ := optionsTest(t)
	f := &sbx.Fake{Defaut: sbx.Reponse{Err: errors.New("boom")}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("un sbx qui échoue sans rien rendre d'exploitable doit faire échouer Settle")
	}
	if !strings.Contains(err.Error(), "github.com") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("le message doit nommer l'hôte sondé et l'échec sous-jacent ; obtenu : %v", err)
	}
}

// C-1 — le code de sortie de sbx n'est PAS le verdict. Personne ici ne peut
// confirmer que `sbx policy check` sort en 0 pour un hôte simplement refusé ;
// den ne doit donc pas en dépendre. Si la sortie porte un `allowed`
// exploitable, c'est LUI qui tranche, et la boucle continue de boucler.
func TestSettleUtiliseLeVerdictMemeQuandSbxSortEnErreur(t *testing.T) {
	t.Run("refus avec code de sortie non nul : on boucle, puis fail-closed", func(t *testing.T) {
		h := nouvelleHorloge()
		f := &sbx.Fake{Defaut: sbx.Reponse{
			Sortie: []byte(`{"allowed": false}`),
			Err:    errors.New("exit status 1"),
		}}

		err := Settle(context.Background(), f, "api", []string{"github.com"}, h.options(60*time.Second, 2*time.Second))
		if err == nil {
			t.Fatal("un hôte durablement refusé doit produire une erreur")
		}
		if h.dormi == 0 {
			t.Errorf("le refus doit être traité comme un VERDICT, donc réessayé : %d sommeil(s)", h.dormi)
		}
		if !strings.Contains(err.Error(), "fail-closed") {
			t.Errorf("l'échec doit être celui du timeout fail-closed, pas un échec de transport ; obtenu : %v", err)
		}
		if strings.Contains(err.Error(), "exit status 1") {
			t.Errorf("le code de sortie ne doit pas être présenté comme la cause ; obtenu : %v", err)
		}
	})

	// L'asymétrie est délibérée : croire un « oui » sorti d'une invocation qui a
	// échoué (stdout tronqué, flag inconnu, sandbox absente) serait le seul
	// chemin par lequel ce paquet ouvrirait un shell sur une policy jamais
	// vérifiée. Un « non » est sûr à croire, un « oui » ne l'est pas.
	t.Run("autorisation avec code de sortie non nul : refusée quand même", func(t *testing.T) {
		o, _ := optionsTest(t)
		f := &sbx.Fake{Defaut: sbx.Reponse{
			Sortie: []byte(`{"allowed": true}`),
			Err:    errors.New("exit status 1"),
		}}

		err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
		if err == nil {
			t.Fatal("un « oui » rendu par une commande qui a échoué ne doit pas suffire à attacher")
		}
		if !strings.Contains(err.Error(), "exit status 1") {
			t.Errorf("le message doit montrer l'échec de la commande ; obtenu : %v", err)
		}
	})
}

// C-1 / I-5 — une sortie qui n'est pas du JSON du tout (usage, message d'aide,
// erreur en clair) doit échouer, et le message doit MONTRER cette sortie.
func TestSettleEchoueSurUneSortieIllisible(t *testing.T) {
	o, _ := optionsTest(t)
	f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte("usage: sbx policy check network [flags]")}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("une sortie non-JSON doit faire échouer Settle")
	}
	if !strings.Contains(err.Error(), "illisible") || !strings.Contains(err.Error(), "usage: sbx policy check network") {
		t.Errorf("le message doit se dire illisible et montrer la sortie ; obtenu : %v", err)
	}
}

// I-5 — le cas d'un sbx qui écrirait son verdict sur stderr, ou qui
// refuserait --json : stdout est vide. Le message censé montrer la sortie
// brute ne doit pas se terminer sur un deux-points et rien.
func TestSettleNommeUneSortieVide(t *testing.T) {
	o, _ := optionsTest(t)
	f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte("   \n")}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("une sortie vide doit faire échouer Settle")
	}
	if !strings.Contains(err.Error(), "vide") {
		t.Errorf("le message doit dire explicitement que la sortie est vide ; obtenu : %v", err)
	}
	if strings.HasSuffix(strings.TrimSpace(err.Error()), ":") {
		t.Errorf("le message ne doit pas se terminer par un deux-points sans rien ; obtenu : %v", err)
	}
}

// I-5 (suite) — une sortie qui n'est pas du JSON peut contenir n'importe quoi,
// y compris des séquences d'échappement et des octets nuls. Les recracher tels
// quels dans un message d'erreur brouille le terminal de celui qui les lit ;
// c'est ce que le %q évite, et rien ne l'observait depuis que la sortie vide a
// sa propre branche.
func TestSettleEchappeUneSortieNonImprimable(t *testing.T) {
	o, _ := optionsTest(t)
	f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte("\x1b[2Joups\x00")}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("une sortie non-JSON doit faire échouer Settle")
	}
	if strings.ContainsAny(err.Error(), "\x00\x1b") {
		t.Errorf("le message ne doit pas contenir d'octets de contrôle bruts ; obtenu : %q", err.Error())
	}
	if !strings.Contains(err.Error(), "oups") {
		t.Errorf("le message doit tout de même montrer la sortie ; obtenu : %v", err)
	}
}

// I-6 — sur un défaut de schéma, la sortie brute est montrée (le brief l'exige)
// et elle peut contenir des hôtes qui ne sont ni bloqués ni même sondés. Le
// message doit donc CADRER ce qu'il montre : nommer l'hôte réellement sondé et
// annoncer que ce qui suit est la sortie de sbx, pas un verdict de den.
func TestSettleCadreLaSortieBruteSurUnDefautDeSchema(t *testing.T) {
	o, _ := optionsTest(t)
	verbeuse := `{"policy":{"allow":["github.com","api.anthropic.com"]},"verdict":"deny"}`
	f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte(verbeuse)}}

	err := Settle(context.Background(), f, "api", []string{"bloque.exemple.test"}, o)
	if err == nil {
		t.Fatal("une sortie sans champ allowed doit échouer immédiatement")
	}
	if !strings.Contains(err.Error(), "bloque.exemple.test") {
		t.Errorf("le message doit nommer l'hôte réellement sondé ; obtenu : %v", err)
	}
	if !strings.Contains(err.Error(), "Sortie brute :") {
		t.Errorf("le message doit annoncer la sortie brute avant de la montrer ; obtenu : %v", err)
	}
	if !strings.Contains(err.Error(), verbeuse) {
		t.Errorf("le message doit montrer la sortie brute ; obtenu : %v", err)
	}
}

// A3 — du bruit APRÈS la valeur JSON (ligne de log, bannière, NDJSON) ne doit
// pas empêcher den d'attacher : le verdict est dans la première valeur.
// json.Unmarshal, lui, refuse tout contenu qui suit la valeur.
func TestSettleTolereDuBruitApresLeJSON(t *testing.T) {
	o, _ := optionsTest(t)
	f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte("{\"allowed\": true}\n{\"warn\":\"cache miss\"}\n")}}

	if err := Settle(context.Background(), f, "api", []string{"github.com"}, o); err != nil {
		t.Fatalf("le verdict de la première valeur JSON doit suffire : %v", err)
	}
}

// I-3 — en production, l'annulation arrive PENDANT une passe, pas entre deux
// tours : sbx est tué, le runner renvoie une erreur de transport qui n'enveloppe
// aucun motif de contexte (mesuré : « signal: killed »). Settle doit reconnaître
// l'annulation lui-même, et ne pas accuser un hôte d'un Ctrl-C.
func TestSettleReconnaitLAnnulationPendantUnePasse(t *testing.T) {
	o, _ := optionsTest(t)
	ctx, annule := context.WithCancel(context.Background())
	defer annule()
	f := &fakeAnnulant{annule: annule}

	err := Settle(ctx, f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("une annulation pendant une passe doit interrompre Settle")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("l'erreur doit envelopper context.Canceled ; obtenu : %v", err)
	}
	if !strings.Contains(err.Error(), "interrompue") {
		t.Errorf("le message doit parler d'attente interrompue ; obtenu : %v", err)
	}
	if strings.Contains(err.Error(), "github.com") {
		t.Errorf("un Ctrl-C n'est pas la faute de l'hôte sondé ; obtenu : %v", err)
	}
}

// I-4 — le scope --sandbox est ce qui donne sa valeur à toute la vérification.
// Un nom vide part tel quel dans l'argv et ne prouve plus rien.
func TestSettleRefuseUnNomDeSandboxInvalide(t *testing.T) {
	cas := []struct {
		nom     string
		sandbox string
	}{
		{"vide", ""},
		{"non canonique", "api."},
		{"caractère interdit", "api/feat"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			o, _ := optionsTest(t)
			f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte(`{"allowed": true}`)}}

			err := Settle(context.Background(), f, c.sandbox, []string{"github.com"}, o)
			if err == nil {
				t.Fatalf("un nom de sandbox %s doit être refusé", c.nom)
			}
			if len(f.Appels) != 0 {
				t.Errorf("aucune sonde ne doit partir sur un scope invalide ; appels : %v", f.Appels)
			}
		})
	}
}

// Mineur — un « - » égaré dans le YAML d'allowlist produit un hôte vide. Sondé
// tel quel, il enverrait `--json ""` et Settle pourrait rendre nil.
func TestSettleRefuseUnHoteVide(t *testing.T) {
	o, _ := optionsTest(t)
	f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte(`{"allowed": true}`)}}

	err := Settle(context.Background(), f, "api", []string{"github.com", ""}, o)
	if err == nil {
		t.Fatal("un hôte vide dans l'allowlist doit être refusé")
	}
	if !strings.Contains(err.Error(), "vide") {
		t.Errorf("le message doit dire que l'hôte est vide ; obtenu : %v", err)
	}
	if len(f.Appels) != 0 {
		t.Errorf("aucune sonde ne doit partir avec une allowlist invalide ; appels : %v", f.Appels)
	}
}

// Mineur — un hôte présent deux fois (nest + stack) ne doit être sondé qu'une
// fois par tour, et n'apparaître qu'une fois dans le message.
func TestSettleDedoublonneLAllowlist(t *testing.T) {
	h := nouvelleHorloge()
	f := &fakeParHote{autoriseApres: map[string]int{"a.test": 99}}

	err := Settle(context.Background(), f, "api", []string{"a.test", "a.test"}, h.options(5*time.Second, 2*time.Second))
	if err == nil {
		t.Fatal("l'hôte ne passe jamais : erreur attendue")
	}
	// 3 sommeils ⇒ 4 tours ; un seul hôte à sonder par tour.
	if n := f.appels["a.test"]; n != 4 {
		t.Errorf("hôte sondé %d fois, attendu 4 (une seule fois par tour) ; appels : %v", n, f.appels)
	}
	if n := strings.Count(err.Error(), "a.test"); n != 1 {
		t.Errorf("l'hôte apparaît %d fois dans le message, attendu 1 : %v", n, err)
	}
}

// I-2 — jusqu'ici seule la PRÉSENCE d'un sommeil était vérifiée, jamais sa
// durée, et l'existence d'un timeout, jamais sa valeur. Un den qui patiente dix
// fois trop longtemps, ou qui martèle sbx deux fois plus vite, passait la CI.
// L'horloge injectée sait pourtant exactement combien de temps a « passé ».
func TestSettleRespecteTimeoutEtIntervalle(t *testing.T) {
	cas := []struct {
		timeout    time.Duration
		intervalle time.Duration
		sommeils   int // = ceil(timeout / intervalle)
	}{
		{60 * time.Second, 2 * time.Second, 30},
		{5 * time.Second, 2 * time.Second, 3},
		{3 * time.Second, 2 * time.Second, 2},
		{2 * time.Second, 2 * time.Second, 1},
	}
	for _, c := range cas {
		t.Run(c.timeout.String()+"/"+c.intervalle.String(), func(t *testing.T) {
			h := nouvelleHorloge()
			f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte(`{"allowed": false}`)}}

			err := Settle(context.Background(), f, "api", []string{"bloque.test"}, h.options(c.timeout, c.intervalle))
			if err == nil {
				t.Fatal("erreur de timeout attendue")
			}
			if !strings.Contains(err.Error(), "fail-closed") {
				t.Fatalf("c'est le timeout fail-closed qui doit sortir ; obtenu : %v", err)
			}
			if h.dormi != c.sommeils {
				t.Errorf("dormi %d fois, attendu %d : la durée du sommeil n'est pas Intervalle", h.dormi, c.sommeils)
			}
			if attendu := time.Duration(c.sommeils) * c.intervalle; h.ecoule() != attendu {
				t.Errorf("temps écoulé %s, attendu %s : la limite n'est pas Maintenant()+Timeout", h.ecoule(), attendu)
			}
			// Une sonde par tour, et un tour de plus que de sommeils.
			if n := len(f.Appels); n != c.sommeils+1 {
				t.Errorf("%d sondes, attendu %d", n, c.sommeils+1)
			}
		})
	}
}

// I-2 — les valeurs d'OptionsDefaut() sont la patience réelle de la seule garde
// réseau du projet ; rien ne les verrouillait.
func TestOptionsDefautValeurs(t *testing.T) {
	o := OptionsDefaut()
	if o.Timeout != 60*time.Second {
		t.Errorf("Timeout = %s, attendu 1m0s", o.Timeout)
	}
	if o.Intervalle != 2*time.Second {
		t.Errorf("Intervalle = %s, attendu 2s", o.Intervalle)
	}
	if o.Sommeil == nil || o.Maintenant == nil {
		t.Error("OptionsDefaut() doit fournir une horloge réelle complète")
	}
}

// I-1 — une horloge injectée qui n'avance pas passe valide() (qui inspecte des
// valeurs, pas une relation) et faisait boucler Settle SANS FIN. C'est
// atteignable avec du code de production correct et des Options légales : un
// double d'horloge naïf à la tâche 12 bloquerait `go test ./...` du projet
// entier sans que rien ne désigne policy.
func TestSettleBorneLeNombreDeTours(t *testing.T) {
	o := Options{
		Timeout:    60 * time.Second,
		Intervalle: 2 * time.Second,
		Sommeil:    func(time.Duration) {},                      // ne fait rien
		Maintenant: func() time.Time { return time.Unix(0, 0) }, // n'avance jamais
	}
	f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte(`{"allowed": false}`)}}

	fini := make(chan error, 1)
	go func() { fini <- Settle(context.Background(), f, "api", []string{"github.com"}, o) }()

	select {
	case err := <-fini:
		if err == nil {
			t.Fatal("une horloge figée doit produire une erreur, pas un succès")
		}
		if !strings.Contains(err.Error(), "horloge") {
			t.Errorf("le message doit désigner l'horloge, pas accuser le réseau ; obtenu : %v", err)
		}
	// Seul endroit du paquet où une horloge RÉELLE intervient : c'est le
	// filet qui transforme « le test se bloque pour toujours » en « le test
	// échoue en nommant la cause ». Il ne coûte rien quand la borne existe.
	case <-time.After(2 * time.Second):
		t.Fatal("Settle n'a jamais rendu la main : la boucle n'est pas bornée en nombre de tours")
	}
}

// fakeAnnulant annule le contexte PENDANT la passe, puis échoue comme le ferait
// un sbx tué : une erreur de transport qui n'enveloppe aucun motif de contexte.
type fakeAnnulant struct {
	annule context.CancelFunc
	appels int
}

func (f *fakeAnnulant) Run(_ context.Context, _ ...string) ([]byte, error) {
	f.appels++
	f.annule()
	return nil, errors.New("signal: killed")
}

func (f *fakeAnnulant) Attach(_ context.Context, _ ...string) error { return nil }

// fakeProgressif autorise l'hôte à partir du n-ième appel.
type fakeProgressif struct {
	appels        int
	autoriseApres int
}

func (f *fakeProgressif) Run(_ context.Context, _ ...string) ([]byte, error) {
	f.appels++
	if f.appels >= f.autoriseApres {
		return []byte(`{"allowed": true}`), nil
	}
	return []byte(`{"allowed": false}`), nil
}

func (f *fakeProgressif) Attach(_ context.Context, _ ...string) error { return nil }

// fakeParHote compte les interrogations hôte par hôte (l'hôte est le dernier
// argument de l'argv) et n'autorise chacun qu'à partir du n-ième appel LE
// CONCERNANT. Un hôte absent d'autoriseApres passe du premier coup.
type fakeParHote struct {
	appels        map[string]int
	autoriseApres map[string]int
}

func (f *fakeParHote) Run(_ context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("argv vide")
	}
	hote := args[len(args)-1]
	if f.appels == nil {
		f.appels = make(map[string]int)
	}
	f.appels[hote]++
	if f.appels[hote] >= f.autoriseApres[hote] {
		return []byte(`{"allowed": true}`), nil
	}
	return []byte(`{"allowed": false}`), nil
}

func (f *fakeParHote) Attach(_ context.Context, _ ...string) error { return nil }
