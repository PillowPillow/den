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
