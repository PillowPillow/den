package sbx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Sortie RÉELLE de `sbx ls --json` (sbx v0.35.0, relevée le 2026-07-28).
const sortieLsReelle = `{
  "sandboxes": [
    {
      "name": "den",
      "id": "4f13dddf-d7fd-44fa-a36c-2c7fa458a8dc",
      "agent": "shell",
      "status": "running",
      "workspaces": [
        "/Users/polochon/Development/Pillow/den",
        "/Users/polochon/.claude_sbx",
        "/Users/polochon/Development/Digitaleo/go.dgdev:ro"
      ]
    }
  ]
}`

func TestLsDecodeLaSortieReelle(t *testing.T) {
	f := &Fake{Reponses: map[string]Reponse{
		"ls --json": {Sortie: []byte(sortieLsReelle)},
	}}

	boxes, err := Ls(context.Background(), f)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(boxes) != 1 {
		t.Fatalf("boxes = %v, attendu 1", boxes)
	}
	b := boxes[0]
	if b.Nom != "den" || b.Agent != "shell" || b.Statut != "running" {
		t.Errorf("sandbox = %+v", b)
	}
	if len(b.Workspaces) != 3 {
		t.Errorf("Workspaces = %v, attendu 3", b.Workspaces)
	}
	// Workdir sert de -w à l'attache : le suffixe :ro doit être retiré, et
	// c'est le PREMIER workspace (le repo, pas le profil agent).
	if got := b.Workdir(); got != "/Users/polochon/Development/Pillow/den" {
		t.Errorf("Workdir = %q", got)
	}
}

func TestLsAttribueNestEtWorktree(t *testing.T) {
	f := &Fake{Reponses: map[string]Reponse{
		"ls --json": {Sortie: []byte(
			`{"sandboxes":[{"name":"api.feat12","status":"running","workspaces":["/w"]}]}`)},
	}}

	boxes, err := Ls(context.Background(), f)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if boxes[0].Nest() != "api" || boxes[0].Worktree() != "feat12" {
		t.Errorf("nest/worktree = %q/%q", boxes[0].Nest(), boxes[0].Worktree())
	}
}

// Tout ce qui s'affiche est trié (convention du dépôt) — et sbx ne garantit
// aucun ordre.
func TestLsTriParNom(t *testing.T) {
	f := &Fake{Reponses: map[string]Reponse{
		"ls --json": {Sortie: []byte(
			`{"sandboxes":[{"name":"zeta"},{"name":"alpha"},{"name":"mu"}]}`)},
	}}

	boxes, err := Ls(context.Background(), f)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	for i, attendu := range []string{"alpha", "mu", "zeta"} {
		if boxes[i].Nom != attendu {
			t.Errorf("boxes[%d].Nom = %q, attendu %q", i, boxes[i].Nom, attendu)
		}
	}
}

func TestLsAucuneSandbox(t *testing.T) {
	f := &Fake{Reponses: map[string]Reponse{
		"ls --json": {Sortie: []byte(`{"sandboxes":[]}`)},
	}}

	boxes, err := Ls(context.Background(), f)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(boxes) != 0 {
		t.Errorf("boxes = %v, attendu vide", boxes)
	}
}

// Un JSON illisible doit produire un message qui contient la sortie brute :
// sans elle, un changement de schéma sbx est indiagnosticable.
func TestLsSortieIllisible(t *testing.T) {
	f := &Fake{Reponses: map[string]Reponse{
		"ls --json": {Sortie: []byte("pas du json")},
	}}

	if _, err := Ls(context.Background(), f); err == nil {
		t.Fatal("une sortie non-JSON doit produire une erreur")
	} else if !contientTout(err.Error(), "sbx ls", "pas du json") {
		t.Errorf("message peu actionnable : %v", err)
	}
}

// Une sortie JSON valide mais au mauvais schéma (sbx renommerait "sandboxes")
// ne doit pas se lire comme « aucune sandbox » : c'est indiscernable d'un
// vrai zéro pour l'appelant, et den ls/sh/rm affirmeraient alors à tort
// qu'aucune sandbox ne tourne. La sortie brute doit rester dans le message,
// pour la même raison que pour un JSON illisible.
func TestLsCleSandboxesAbsente(t *testing.T) {
	f := &Fake{Reponses: map[string]Reponse{
		"ls --json": {Sortie: []byte(`{"autrechose":[]}`)},
	}}

	if _, err := Ls(context.Background(), f); err == nil {
		t.Fatal("une clé sandboxes absente doit produire une erreur")
	} else if !contientTout(err.Error(), "sbx ls", "sandboxes", "autrechose") {
		t.Errorf("message peu actionnable : %v", err)
	}
}

// La clé "sandboxes" présente mais vide ou nulle reste un succès à zéro
// sandbox : sbx ls --json n'a jamais été observé sans sandbox vivante, et
// rien ne garantit lequel des deux JSON produit. Les deux doivent marcher.
func TestLsZeroSandboxeVideOuNil(t *testing.T) {
	for _, sortie := range []string{`{"sandboxes":[]}`, `{"sandboxes":null}`} {
		f := &Fake{Reponses: map[string]Reponse{
			"ls --json": {Sortie: []byte(sortie)},
		}}

		boxes, err := Ls(context.Background(), f)
		if err != nil {
			t.Fatalf("sortie %s : erreur inattendue : %v", sortie, err)
		}
		if len(boxes) != 0 {
			t.Errorf("sortie %s : boxes = %v, attendu vide", sortie, boxes)
		}
	}
}

func TestLsPropageLErreurDuRunner(t *testing.T) {
	sentinelle := errors.New("sbx introuvable")
	f := &Fake{Defaut: Reponse{Err: sentinelle}}

	if _, err := Ls(context.Background(), f); !errors.Is(err, sentinelle) {
		t.Errorf("err = %v, attendu la sentinelle enveloppée", err)
	}
}

func TestTrouve(t *testing.T) {
	boxes := []Sandbox{{Nom: "api"}, {Nom: "api.feat12"}, {Nom: "web"}}

	b := Trouve(boxes, "api.feat12")
	if b == nil {
		t.Fatalf("Trouve(api.feat12) = nil, attendu la sandbox")
	}
	// L'ADRESSE compte : les appelants lisent Statut et Workspaces sur la
	// sandbox trouvée. Une copie d'un autre élément passerait un test sur le
	// seul Nom.
	if b != &boxes[1] {
		t.Errorf("Trouve doit rendre l'élément de la tranche ; obtenu %v", b)
	}
	if Trouve(boxes, "absente") != nil {
		t.Errorf("Trouve(absente) doit rendre nil")
	}
	// Le nom est cherché ENTIER : « api » ne doit pas capturer « api.feat12 »,
	// sinon un `den api` attacherait dans la sandbox du worktree.
	if b := Trouve([]Sandbox{{Nom: "api.feat12"}}, "api"); b != nil {
		t.Errorf("Trouve ne doit pas faire de correspondance par préfixe ; obtenu %v", b)
	}
}

// VerifieEnMarche est la garde partagée par `den <nest>` et `den sh` : les deux
// chemins finissent par un `sbx exec`, et les deux sont faux dans une VM
// arrêtée.
//
// LISTE BLANCHE, et c'est le cœur du test : les valeurs de `status` que sbx peut
// émettre ne sont pas connues ici (sbx n'est pas installable sur cette machine).
// Une liste noire {"exited","stopped"} laisserait passer tout statut qu'une
// version ultérieure introduirait — d'où les cas « paused », « Running » et « ».
func TestVerifieEnMarche(t *testing.T) {
	if err := (Sandbox{Nom: "api", Statut: "running"}).VerifieEnMarche(); err != nil {
		t.Errorf("une sandbox « running » doit passer ; obtenu : %v", err)
	}

	for _, statut := range []string{"exited", "stopped", "paused", "Running", ""} {
		t.Run("statut="+statut, func(t *testing.T) {
			err := Sandbox{Nom: "api", Statut: statut}.VerifieEnMarche()
			if err == nil {
				t.Fatalf("un statut %q ne doit pas être traité comme en marche", statut)
			}
			// Format d'erreur du projet : le contexte, le détail, et les valeurs
			// disponibles. Sans le statut LU, l'utilisateur ne sait pas de quoi
			// den se plaint ; sans le statut ATTENDU, il ne sait pas ce qui
			// aurait convenu.
			if !contientTout(err.Error(), "api", statut, "running") {
				t.Errorf("le message doit rendre la sandbox, le statut lu et le statut attendu ; obtenu : %v", err)
			}
		})
	}
}

// Le message ne doit nommer QUE des sous-commandes sbx ATTESTÉES.
//
// `sbx start` n'apparaît dans aucun relevé (plan 2 : create, ls, exec, ports,
// policy check, rm --force ; sbx-devbox ajoute stop, template save, secret,
// inspect, login) et sbx n'est pas installable ici : personne ne peut le
// falsifier. Suggérer une commande peut-être inexistante à l'utilisateur est
// pire qu'un commentaire faux — le commentaire ne trompe qu'un développeur.
func TestVerifieEnMarcheNeSuggereQueDesCommandesAttestees(t *testing.T) {
	err := Sandbox{Nom: "api", Statut: "exited"}.VerifieEnMarche()
	if err == nil {
		t.Fatal("un statut « exited » doit produire une erreur")
	}
	if strings.Contains(err.Error(), "sbx start") {
		t.Errorf("`sbx start` n'est attesté nulle part et ne doit pas être suggéré ; obtenu : %v", err)
	}
	// Et la remédiation doit exister : un refus sans porte de sortie oblige
	// l'utilisateur à deviner.
	if !strings.Contains(err.Error(), "sbx rm --force api") {
		t.Errorf("le message doit donner la remédiation exacte ; obtenu : %v", err)
	}
}

func contientTout(s string, morceaux ...string) bool {
	for _, m := range morceaux {
		if !strings.Contains(s, m) {
			return false
		}
	}
	return true
}
