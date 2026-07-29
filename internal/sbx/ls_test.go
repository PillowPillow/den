package sbx

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
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

// Une sortie TRONQUÉE — JSON valide jusqu'à la coupe — doit échouer bruyamment,
// jamais se lire comme une liste courte.
//
// Ce cas n'est pas théorique : Run borne le drainage des tubes (voir
// delaiDrainageDefaut) et rend désormais un SUCCÈS quand le process est sorti
// proprement, même si un descendant tenait encore le tube. La sortie du fils
// direct est complète (mesuré, cf. runner_test.go), mais c'est précisément le
// genre de propriété qu'un changement d'os/exec ou de sbx peut retirer sans
// prévenir — et une liste de sandboxes silencieusement amputée ferait recréer
// par `den <nest>` une sandbox qui tourne déjà.
func TestLsSortieTronquee(t *testing.T) {
	complet := `{"sandboxes":[{"name":"api","status":"running"},{"name":"web","status":"running"}]}`
	for _, coupe := range []int{len(complet) - 1, len(complet) / 2, 20} {
		tronquee := complet[:coupe]
		t.Run(tronquee, func(t *testing.T) {
			f := &Fake{Reponses: map[string]Reponse{"ls --json": {Sortie: []byte(tronquee)}}}

			if _, err := Ls(context.Background(), f); err == nil {
				t.Fatalf("une sortie tronquée doit produire une erreur, pas une liste amputée ; sortie = %q", tronquee)
			} else if !contientTout(err.Error(), "sbx ls", tronquee) {
				t.Errorf("message peu actionnable : %v", err)
			}
		})
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

// Ls ne doit pas RÉPÉTER la sous-commande que l'erreur du runner nomme déjà.
//
// Mesuré sur le binaire, sbx absent du PATH :
//
//	den: sbx ls : sbx ls --json : exec: "sbx": executable file not found in $PATH
//
// « sbx ls » deux fois sur la première ligne qu'un nouvel utilisateur voit :
// une fois par l'enveloppe de Ls, une fois par ErreurExec.Error, qui rend
// TOUJOURS le binaire et son argv complet. C'est l'enveloppe qui est de trop —
// elle n'ajoute rien que l'argv ne dise mieux (« ls --json » plutôt que « ls »).
//
// L'*ErreurExec est fabriqué ici plutôt que produit par un vrai Exec : c'est le
// type que la production fait remonter, et le fabriquer permet d'exercer la
// composition Ls+ErreurExec sans dépendre d'un binaire quelconque.
func TestLsNeRepetePasLaSousCommande(t *testing.T) {
	f := &Fake{Defaut: Reponse{Err: &ErreurExec{
		Bin:  "sbx",
		Args: []string{"ls", "--json"},
		Err:  errors.New("exit status 1"),
	}}}

	_, err := Ls(context.Background(), f)
	if err == nil {
		t.Fatal("une erreur du runner doit remonter")
	}
	message := err.Error()
	if n := strings.Count(message, "sbx ls"); n != 1 {
		t.Errorf("« sbx ls » apparaît %d fois, attendu 1 fois ; message : %s", n, message)
	}
}

// Et le cas de premier contact, vu depuis Ls — le chemin qu'empruntent les
// QUATRE commandes qui touchent sbx (`den ls`, `den <nest>`, `den sh`,
// `den rm`) : toutes appellent Ls avant quoi que ce soit d'autre.
func TestLsBinaireAbsentRendUnMessageActionnable(t *testing.T) {
	f := &Fake{Defaut: Reponse{Err: &ErreurExec{
		Bin:  "sbx",
		Args: []string{"ls", "--json"},
		Err:  exec.ErrNotFound,
	}}}

	_, err := Ls(context.Background(), f)
	if err == nil {
		t.Fatal("un binaire absent doit produire une erreur")
	}
	message := err.Error()
	if !contientTout(message, "sbx", "introuvable dans le PATH", "den doctor") {
		t.Errorf("message peu actionnable : %s", message)
	}
	if strings.Contains(message, "executable file not found") {
		t.Errorf("reste d'anglais dans le message : %s", message)
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
			//
			// strconv.Quote et non le statut nu : sur le sous-cas statut="",
			// `strings.Contains(err, "")` est vrai PAR CONSTRUCTION et n'assert
			// rien du tout (mesuré : en retirant s.Statut du message, ce sous-cas
			// restait vert alors que les quatre autres rougissaient). La forme
			// quotée est celle que le message rend — `%q` — et elle discrimine
			// aussi le statut vide.
			if !contientTout(err.Error(), "api", strconv.Quote(statut), strconv.Quote(StatutEnMarche)) {
				t.Errorf("le message doit rendre la sandbox, le statut lu et le statut attendu ; obtenu : %v", err)
			}
		})
	}
}

// Le message ne doit nommer QUE des sous-commandes sbx ATTESTÉES.
//
// Suggérer à l'utilisateur une commande peut-être inexistante est pire qu'un
// commentaire faux : le commentaire ne trompe qu'un développeur.
//
// LISTE BLANCHE — et c'est tout l'objet de ce test. Une version antérieure
// interdisait la seule chaîne « sbx start » : liste NOIRE d'un élément, qui ne
// disait rien de `sbx resume` ni d'un `den up --force` inventé demain (mesuré :
// message enrichi des deux, suite entière verte). C'était exactement l'anti-
// patron que VerifieEnMarche argumente contre, dix lignes plus haut, pour les
// statuts. On extrait donc TOUT ce que le message présente comme une commande —
// les segments entre backticks — et on exige que chacun soit attesté.
func TestVerifieEnMarcheNeSuggereQueDesCommandesAttestees(t *testing.T) {
	// DEUX sources d'attestation, et elles ne se valent pas :
	//
	//   - les sous-commandes `sbx` viennent du relevé du 2026-07-28, versé au
	//     spec §14.0 : create, ls, exec, ports, policy check, rm --force
	//     (sbx-devbox ajoute stop, template save, secret, inspect, login).
	//     `sbx start` n'y figure pas, et sbx n'est pas installable ici :
	//     personne ne peut le falsifier. Étendre cette liste demande un RELEVÉ,
	//     pas une intuition ;
	//   - `den rm` est une commande de DEN, attestée par la source du dépôt
	//     (internal/cli/rm.go, newRmCmd). L'argument « on ne sait pas si elle
	//     existe » ne s'y applique pas : on la lit.
	attestees := []string{"sbx ls", "sbx rm --force api", "den rm api"}

	err := Sandbox{Nom: "api", Statut: "exited"}.VerifieEnMarche()
	if err == nil {
		t.Fatal("un statut « exited » doit produire une erreur")
	}
	message := err.Error()

	commandes := entreBackticks(message)
	if len(commandes) == 0 {
		t.Fatalf("le message doit présenter sa remédiation entre backticks ; obtenu : %v", err)
	}
	for _, c := range commandes {
		if !slices.Contains(attestees, c) {
			t.Errorf("commande NON ATTESTÉE proposée à l'utilisateur : %q ; message : %v", c, err)
		}
	}

	// Second filet, indépendant des backticks : TOUTE occurrence de « sbx <mot> »
	// dans le message, backtickée ou non, doit nommer une sous-commande attestée.
	// Sans lui, le test ne tient que par la convention typographique du dépôt —
	// mesuré, deux commandes inventées SANS backticks laissaient la suite verte.
	for _, sc := range sousCommandesSbx(message) {
		if !slices.Contains([]string{"ls", "rm"}, sc) {
			t.Errorf("sous-commande sbx NON ATTESTÉE proposée à l'utilisateur : %q ; message : %v", sc, err)
		}
	}

	// Et la porte de sortie doit être là : un refus sans remédiation oblige
	// l'utilisateur à deviner.
	if !slices.Contains(commandes, "sbx rm --force api") {
		t.Errorf("le message doit donner la remédiation exacte ; obtenu : %v", err)
	}

	// La remédiation de DEN passe d'abord : `den rm api` fait ce que fait
	// `sbx rm --force api` ET nettoie les worktrees que den a créés pour cette
	// sandbox (internal/cli/rm.go, nettoieWorktrees). Envoyer l'utilisateur
	// directement sur sbx lui laisse des worktrees orphelins sous
	// worktree_root, sans rien lui dire.
	if !slices.Contains(commandes, "den rm api") {
		t.Errorf("le message doit proposer `den rm api`, qui nettoie aussi les worktrees ; obtenu : %v", err)
	}
	if strings.Index(message, "den rm api") > strings.Index(message, "sbx rm --force api") {
		t.Errorf("`den rm api` doit précéder `sbx rm --force api` : c'est la remédiation complète, "+
			"l'autre est le repli ; obtenu : %v", err)
	}
}

// entreBackticks rend les segments `…` d'un message, c'est-à-dire tout ce que
// den présente à l'utilisateur comme une commande à taper.
func entreBackticks(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "`")
		if i < 0 {
			return out
		}
		reste := s[i+1:]
		j := strings.Index(reste, "`")
		if j < 0 {
			return out
		}
		out = append(out, reste[:j])
		s = reste[j+1:]
	}
}

// sousCommandesSbx rend le mot qui suit chaque « sbx » du message, backticks ou
// pas.
//
// CE QUE CE FILET N'ATTRAPE PAS, et il faut le dire plutôt que de laisser croire
// le contraire : une commande `den` inventée écrite hors backticks (« den up
// --force ») passe au travers, parce que « den » apparaît légitimement en prose
// dans le message (« den n'attache pas… ») et qu'aucune heuristique ne sépare le
// mot français du nom de programme. C'est la liste blanche des backticks qui
// couvre ce cas-là, et elle seule.
func sousCommandesSbx(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "sbx ")
		if i < 0 {
			return out
		}
		s = s[i+len("sbx "):]
		fin := strings.IndexFunc(s, func(r rune) bool {
			return !unicode.IsLetter(r) && r != '-'
		})
		if fin < 0 {
			fin = len(s)
		}
		if fin > 0 {
			out = append(out, s[:fin])
		}
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
