package sbx

import (
	"context"
	"errors"
	"testing"
)

func TestFakeEnregistreLesAppels(t *testing.T) {
	f := &Fake{}
	if _, err := f.Run(context.Background(), "ls", "--json"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if _, err := f.Run(context.Background(), "rm", "--force", "api"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if len(f.Appels) != 2 {
		t.Fatalf("Appels = %v, attendu 2 appels", f.Appels)
	}
	if !f.AAppele("rm", "--force") {
		t.Errorf("AAppele(rm --force) doit être vrai ; appels : %v", f.Appels)
	}
	if !f.AAppele("ls") {
		t.Errorf("AAppele(ls) doit être vrai ; appels : %v", f.Appels)
	}
	if f.AAppele("create") {
		t.Errorf("AAppele(create) doit être faux ; appels : %v", f.Appels)
	}
	if got := f.DernierAppel(); len(got) != 3 || got[0] != "rm" {
		t.Errorf("DernierAppel = %v", got)
	}
}

func TestFakeReponseScriptee(t *testing.T) {
	attendue := []byte(`{"sandboxes":[]}`)
	f := &Fake{Reponses: map[string]Reponse{
		"ls --json": {Sortie: attendue},
	}}

	got, err := f.Run(context.Background(), "ls", "--json")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if string(got) != string(attendue) {
		t.Errorf("sortie = %q, attendu %q", got, attendue)
	}
}

func TestFakeReponseParDefaut(t *testing.T) {
	sentinelle := errors.New("boom")
	f := &Fake{Defaut: Reponse{Err: sentinelle}}

	if _, err := f.Run(context.Background(), "n-importe", "quoi"); !errors.Is(err, sentinelle) {
		t.Errorf("err = %v, attendu la sentinelle par défaut", err)
	}
}

// Attach est enregistré comme un appel : les tests de `den <nest>` doivent
// pouvoir asserter QUE l'attache a eu lieu, et avec quels arguments.
func TestFakeAttachEstEnregistre(t *testing.T) {
	f := &Fake{}
	if err := f.Attach(context.Background(), "exec", "-it", "api", "bash", "-l"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !f.AAppele("exec", "-it", "api") {
		t.Errorf("l'attache doit être enregistrée ; appels : %v", f.Appels)
	}
}

func TestFakeAttachPeutEchouer(t *testing.T) {
	sentinelle := errors.New("tty indisponible")
	f := &Fake{ErreurAttach: sentinelle}
	if err := f.Attach(context.Background(), "exec", "-it", "api"); !errors.Is(err, sentinelle) {
		t.Errorf("err = %v, attendu la sentinelle", err)
	}
}

// Run doit renvoyer une copie de la sortie scriptée : un appelant qui
// modifierait la slice reçue ne doit pas corrompre les appels suivants du
// même Fake.
func TestFakeRunRenvoieUneCopieDeLaSortie(t *testing.T) {
	f := &Fake{Defaut: Reponse{Sortie: []byte("original")}}

	premier, err := f.Run(context.Background(), "ls", "--json")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	premier[0] = 'X'

	second, err := f.Run(context.Background(), "ls", "--json")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if string(second) != "original" {
		t.Errorf("sortie = %q, attendu %q (la mutation du premier appel ne doit pas fuiter)", second, "original")
	}
}

// Run et Attach sont irréconciliables (cf. runner.go) : un Run dont l'argv
// commence par "exec" ne doit jamais se faire passer pour une attache, et une
// attache doit rester détectable indépendamment des Run. Deux traces
// distinctes, pas une clé qu'il faudrait deviner dans Appels.
func TestFakeDistingueRunEtAttach(t *testing.T) {
	f := &Fake{}
	if _, err := f.Run(context.Background(), "exec", "-it", "api", "bash", "-l"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if f.AAttache("exec") {
		t.Errorf("un Run ne doit jamais compter comme une attache ; attaches : %v", f.Attaches)
	}

	if err := f.Attach(context.Background(), "exec", "-it", "api"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !f.AAttache("exec", "-it", "api") {
		t.Errorf("l'attache doit être enregistrée dans Attaches ; attaches : %v", f.Attaches)
	}
	if !f.AAppele("exec", "-it", "api") {
		t.Errorf("Attach doit continuer d'alimenter Appels aussi")
	}
}
