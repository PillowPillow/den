package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
)

func TestShAttacheDansLeWorkdir(t *testing.T) {
	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api","/profil"]}]}`)},
	}}

	if _, err := executeCmdAvecSbx(t, f, "sh", "api"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	var attache []string
	for _, a := range f.Appels {
		if len(a) > 0 && a[0] == "exec" {
			attache = a
		}
	}
	if attache == nil {
		t.Fatalf("aucune attache ; appels : %v", f.Appels)
	}
	if !slices.Contains(attache, "-w") || !slices.Contains(attache, "/w/api") {
		t.Errorf("l'attache doit poser le workdir sur le premier workspace ; obtenu : %v", attache)
	}
	if !slices.Contains(attache, "bash") {
		t.Errorf("l'attache doit lancer un shell ; obtenu : %v", attache)
	}
}

// Complément indispensable au test ci-dessus, qui scanne f.Appels : Appels
// CONFOND Run et Attach (cf. sbx/fake.go), donc un `Run("exec", …)` — shell muet,
// sans tty — le satisfait tout autant qu'une vraie attache. Seul f.Attaches
// distingue les deux. Ce test verrouille aussi le `-it` et l'argv COMPLET, dans
// l'ordre : `sbx exec [flags] SANDBOX COMMAND`, un `-w` postposé arriverait tel
// quel à `bash -l` au lieu de fixer le répertoire.
func TestShAttacheAvecUnTtyEtPasUnRun(t *testing.T) {
	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w/api","/profil"]}]}`)},
	}}

	if _, err := executeCmdAvecSbx(t, f, "sh", "api"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !f.AAttache("exec", "-it", "-w", "/w/api", "api", "bash", "-l") {
		t.Errorf("l'attache doit être un Attach, argv complet et ordonné ; attaches : %v", f.Attaches)
	}
}

// `sbx run` lancerait le flavor de l'image (souvent claude) : jamais.
//
// Le `"status":"running"` du fixture n'est pas décoratif : `den sh` refuse
// désormais toute sandbox dont le statut n'est pas explicitement « en marche »
// (cf. TestShRefuseUneSandboxQuiNeTournePas), et un fixture sans `status`
// n'atteindrait plus l'attache du tout.
func TestShNUtiliseJamaisSbxRun(t *testing.T) {
	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w"]}]}`)},
	}}

	if _, err := executeCmdAvecSbx(t, f, "sh", "api"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if f.AAppele("run") {
		t.Errorf("den sh ne doit jamais passer par `sbx run` ; appels : %v", f.Appels)
	}
}

// Un nom inexistant doit lister ce qui tourne : « not found » seul oblige
// l'utilisateur à relancer une autre commande pour savoir quoi taper.
func TestShNomInconnu(t *testing.T) {
	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(`{"sandboxes":[{"name":"api"},{"name":"web"}]}`)},
	}}

	_, err := executeCmdAvecSbx(t, f, "sh", "absente")
	if err == nil {
		t.Fatal("un nom de sandbox inconnu doit produire une erreur")
	}
	for _, attendu := range []string{"absente", "api", "web"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit contenir %q ; obtenu : %v", attendu, err)
		}
	}
	if len(f.Attaches) != 0 {
		t.Errorf("un nom inconnu ne doit attacher nulle part ; attaches : %v", f.Attaches)
	}
}

// La même garde que sur `den <nest>`, sur l'AUTRE chemin : les deux finissent
// par un `sbx exec`, et les deux sont faux dans une VM arrêtée. Un `den sh` qui
// ouvre un shell dans une sandbox `exited` n'est pas moins faux qu'un
// `den <nest>` qui le fait — et c'est bien le même défaut, pas un cousin.
//
// Prouvé ICI et pas seulement dans internal/spawn : rien, au niveau de
// sbx.VerifieEnMarche, ne garantit que newShCmd l'appelle. C'est exactement le
// genre de propriété vraie d'un côté et oubliée de l'autre.
func TestShRefuseUneSandboxQuiNeTournePas(t *testing.T) {
	for _, statut := range []string{"exited", "stopped", "paused", "Running", ""} {
		t.Run("statut="+statut, func(t *testing.T) {
			f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
				"ls --json": {Sortie: []byte(
					`{"sandboxes":[{"name":"api","status":"` + statut + `","workspaces":["/w/api"]}]}`)},
			}}

			_, err := executeCmdAvecSbx(t, f, "sh", "api")
			if err == nil {
				t.Fatalf("un statut %q ne doit pas donner lieu à une attache", statut)
			}
			if !strings.Contains(err.Error(), statut) || !strings.Contains(err.Error(), "running") {
				t.Errorf("le message doit rendre le statut lu et celui attendu ; obtenu : %v", err)
			}
			if len(f.Attaches) != 0 {
				t.Errorf("aucune attache dans une VM arrêtée ; attaches : %v", f.Attaches)
			}
		})
	}
}

// Aucune sandbox vivante : le message ne peut pas proposer de liste, il doit le
// DIRE. « (vivantes : []) » enverrait l'utilisateur chercher une faute de frappe
// dans une liste vide.
func TestShSansAucuneSandbox(t *testing.T) {
	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(`{"sandboxes":[]}`)},
	}}

	_, err := executeCmdAvecSbx(t, f, "sh", "absente")
	if err == nil {
		t.Fatal("un nom de sandbox inconnu doit produire une erreur")
	}
	if !strings.Contains(err.Error(), "aucune sandbox") {
		t.Errorf("le message doit dire qu'aucune sandbox ne tourne ; obtenu : %v", err)
	}
}
