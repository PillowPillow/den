package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ecrisStack crée <denHome>/stacks/<nom>/stack.yaml et renvoie denHome.
func ecrisStack(t *testing.T, denHome, nom, contenu string) string {
	t.Helper()
	dir := filepath.Join(denHome, "stacks", nom)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
	return denHome
}

func TestLoadStack(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "dgdevx", `
image: dgdevx:v1
parent: devx
kit: ./kit
egress:
  - gitlab.digitaleo.com
`)

	s, err := LoadStack(denHome, "dgdevx")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if s.Name != "dgdevx" || s.Image != "dgdevx:v1" || s.Parent != "devx" {
		t.Errorf("stack = %+v", s)
	}
	if want := filepath.Join(denHome, "stacks", "dgdevx"); s.Dir != want {
		t.Errorf("Dir = %q, attendu %q", s.Dir, want)
	}
	if want := filepath.Join(denHome, "stacks", "dgdevx", "kit"); s.Kit != want {
		t.Errorf("Kit = %q, attendu un chemin absolu %q", s.Kit, want)
	}
	if len(s.Egress) != 1 || s.Egress[0] != "gitlab.digitaleo.com" {
		t.Errorf("Egress = %v", s.Egress)
	}
}

// Le nom d'une stack est le nom de son dossier — cas nominal et unique.
func TestLoadStackNomDeduitDuDossier(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if s.Name != "devx" {
		t.Errorf("Name = %q, attendu %q déduit du dossier", s.Name, "devx")
	}
}

// Même règle que pour les nests : une stack ne porte pas son nom dans son
// contenu. LoadStacks indexait sa map par ce `name:`, alors que LoadStack
// cherche par nom de dossier — deux clés pour un même objet.
func TestLoadStackRejetteUnNomDansLeContenu(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "name: autre\nimage: devx:v1\n")
	_, err := LoadStack(denHome, "devx")
	if err == nil {
		t.Fatal("attendu un rejet : l'identité d'une stack vient de son dossier, pas de son contenu")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("erreur = %q, attendu une mention de la clé `name`", err.Error())
	}
}

// LoadStacks doit indexer par le nom de dossier, la seule identité qui existe :
// c'est par cette clé que defaults.stack et nest.stack sont résolus.
func TestLoadStacksIndexeParLeNomDeDossier(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")

	stacks, err := LoadStacks(denHome)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	s, ok := stacks.Saines["devx"]
	if !ok {
		t.Fatalf("stacks = %v, attendu une entrée sous le nom de dossier %q", stacks.Saines, "devx")
	}
	if s.Name != "devx" {
		t.Errorf("Name = %q, attendu %q", s.Name, "devx")
	}
}

func TestLoadStackRejetteUneCleInconnue(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "image: devx:v1\negres: [github.com]\n")
	_, err := LoadStack(denHome, "devx")
	if err == nil {
		t.Fatal("attendu une erreur sur la clé inconnue `egres`")
	}
	if !strings.Contains(err.Error(), "egres") {
		t.Errorf("erreur = %q, attendu une mention de la clé fautive", err.Error())
	}
}

func TestLoadStackFichierVide(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "")
	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("un stack.yaml vide ne doit pas être une erreur de chargement : %v", err)
	}
	if s.Name != "devx" {
		t.Errorf("Name = %q, attendu %q déduit du dossier", s.Name, "devx")
	}
}

// Une stack absente et une stack illisible appellent deux gestes différents :
// « déclare-la » contre « répare les droits ». `doctor` relaie ce message tel
// quel, il doit donc trancher.
func TestLoadStackAbsente(t *testing.T) {
	denHome := t.TempDir()
	_, err := LoadStack(denHome, "fantome")
	if err == nil {
		t.Fatal("attendu une erreur pour une stack absente")
	}
	if !strings.Contains(err.Error(), "introuvable") {
		t.Errorf("erreur = %q, attendu un message d'absence explicite", err.Error())
	}
	if !strings.Contains(err.Error(), filepath.Join(denHome, "stacks", "fantome")) {
		t.Errorf("erreur = %q, attendu le chemin attendu de la stack", err.Error())
	}
}

func TestLoadStackIllisible(t *testing.T) {
	denHome := t.TempDir()
	// stack.yaml présent mais illisible (ici : c'est un dossier) — ce n'est pas
	// une absence, et le message ne doit pas le prétendre.
	if err := os.MkdirAll(filepath.Join(denHome, "stacks", "devx", "stack.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := LoadStack(denHome, "devx")
	if err == nil {
		t.Fatal("attendu une erreur pour une stack illisible")
	}
	if strings.Contains(err.Error(), "introuvable") {
		t.Errorf("erreur = %q : la stack existe, elle est illisible", err.Error())
	}
	if !strings.Contains(err.Error(), "lecture") {
		t.Errorf("erreur = %q, attendu un message d'erreur de lecture", err.Error())
	}
}

func TestLoadStackRefuseUnNomQuiSortDeDenHome(t *testing.T) {
	racine := t.TempDir()
	denHome := filepath.Join(racine, "home")
	// Une stack parfaitement valide, mais HORS du den home.
	ecrisStack(t, racine, "dehors", "image: dehors:v1\n")

	// <denHome>/stacks/../../stacks/dehors == <racine>/stacks/dehors
	if _, err := LoadStack(denHome, "../../stacks/dehors"); err == nil {
		t.Error("LoadStack a chargé une stack située hors du den home")
	}
	if _, err := LoadStack(denHome, ".."); err == nil {
		t.Error("LoadStack(\"..\") = nil, attendu un rejet")
	}
}

func TestLoadStacksToutes(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisStack(t, denHome, "dgdevx", "image: dgdevx:v1\nparent: devx\n")
	// un dossier sans stack.yaml doit être ignoré silencieusement, pas planter
	if err := os.MkdirAll(filepath.Join(denHome, "stacks", "brouillon"), 0o755); err != nil {
		t.Fatal(err)
	}

	stacks, err := LoadStacks(denHome)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(stacks.Saines) != 2 {
		t.Fatalf("attendu 2 stacks, obtenu %d : %v", len(stacks.Saines), stacks.Saines)
	}
	if stacks.Saines["dgdevx"].Parent != "devx" {
		t.Errorf("parent de dgdevx = %q", stacks.Saines["dgdevx"].Parent)
	}
}

func TestLoadStacksDossierAbsent(t *testing.T) {
	// Pas de dossier stacks/ : ce n'est pas une erreur, c'est un den vide.
	stacks, err := LoadStacks(t.TempDir())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(stacks.Saines) != 0 {
		t.Errorf("attendu 0 stack, obtenu %d", len(stacks.Saines))
	}
}

func TestLoadStackResoutLesKitsTransverses(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", `image: devx:v1
kit: ./kit
kits:
  - ../../kits/ssh-known-hosts
  - /absolu/deja
`)

	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	attendus := []string{
		filepath.Join(denHome, "kits", "ssh-known-hosts"),
		"/absolu/deja",
	}
	if len(s.Kits) != len(attendus) {
		t.Fatalf("Kits = %v, attendu %d entrées", s.Kits, len(attendus))
	}
	for i, a := range attendus {
		if s.Kits[i] != a {
			t.Errorf("Kits[%d] = %q, attendu %q", i, s.Kits[i], a)
		}
	}
}

// L'ordre est un ordre de LAYERING : le trier casserait la sémantique.
func TestLoadStackPreserveLOrdreDesKits(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", `image: devx:v1
kits: [./z-dernier, ./a-premier]
`)

	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if filepath.Base(s.Kits[0]) != "z-dernier" || filepath.Base(s.Kits[1]) != "a-premier" {
		t.Errorf("l'ordre déclaré doit être préservé ; obtenu %v", s.Kits)
	}
}

// KitsDeclares est la SOURCE UNIQUE de « quels kits, dans quel ordre » :
// doctor et le chemin de spawn la consomment tous deux. Ses deux propriétés
// sont donc testées ici, à la source, et non deux fois chez les consommateurs.
func TestStackKitsDeclares(t *testing.T) {
	cas := []struct {
		nom     string
		stack   Stack
		attendu []string
	}{
		{
			// L'ordre de LAYERING : `kits:` (transverses) puis `kit:`. Le
			// mixin est ajouté après par sbx.ArgvCreate et doit rester dernier.
			"ordre de layering : kits: puis kit:",
			Stack{Kits: []string{"/k/transverse", "/k/autre"}, Kit: "/k/devx"},
			[]string{"/k/transverse", "/k/autre", "/k/devx"},
		},
		{
			// La dette du fix round 2 : le filtre ne portait que sur le `kit:`
			// singulier, jamais sur une entrée vide DANS `kits:`.
			"entree vide dans kits: (pluriel)",
			Stack{Kits: []string{"", "/k/transverse", ""}, Kit: "/k/devx"},
			[]string{"/k/transverse", "/k/devx"},
		},
		{
			"kit: singulier vide",
			Stack{Kits: []string{"/k/transverse"}, Kit: ""},
			[]string{"/k/transverse"},
		},
		{
			// Une stack sans kit est valide (spec §4.2) : liste vide, pas nil
			// piégeux ni entrée fantôme.
			"aucun kit declare",
			Stack{},
			[]string{},
		},
		{
			"que des entrees vides",
			Stack{Kits: []string{"", ""}, Kit: ""},
			[]string{},
		},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			obtenu := c.stack.KitsDeclares()
			if !slices.Equal(obtenu, c.attendu) {
				t.Errorf("KitsDeclares() = %v, attendu %v", obtenu, c.attendu)
			}
		})
	}
}

// KitsDeclares ne doit pas écrire dans la stack qu'elle lit : elle est appelée
// deux fois sur le chemin de spawn (contrôle d'existence, puis argv), et un
// aliasing du slice sous-jacent ferait diverger le second appel du premier.
func TestStackKitsDeclaresNeModifiePasLaStack(t *testing.T) {
	s := Stack{Kits: []string{"/k/a", "/k/b"}, Kit: "/k/c"}
	premier := s.KitsDeclares()
	premier[0] = "/k/ECRASE"

	if !slices.Equal(s.Kits, []string{"/k/a", "/k/b"}) {
		t.Errorf("Kits modifiée par l'appelant : %v", s.Kits)
	}
	if second := s.KitsDeclares(); !slices.Equal(second, []string{"/k/a", "/k/b", "/k/c"}) {
		t.Errorf("second appel = %v, attendu identique au premier", second)
	}
}

func TestLoadStackSansKits(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")

	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(s.Kits) != 0 {
		t.Errorf("Kits = %v, attendu vide", s.Kits)
	}
}

// Une stack cassée ne masque PAS les stacks saines — 16ᵉ configuration hostile,
// trouvée en exerçant le binaire assemblé (tâche 17c).
//
// Avant, LoadStack échouait et LoadStacks propageait l'erreur en bloc : une
// faute de frappe dans une stack que PERSONNE n'utilise faisait échouer
// `den <nest>` et `den nest show` sur un nest référençant une stack saine.
// C'est la doctrine que ListNests applique aux nests depuis T16 ; elle
// n'avait jamais été appliquée aux stacks.
func TestLoadStacksUneStackCasseeNeMasquePasLesSaines(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisStack(t, denHome, "autre", "image: autre:v1\nimag: faute-de-frappe\n")

	stacks, err := LoadStacks(denHome)
	if err != nil {
		t.Fatalf("une stack cassée ne doit pas faire échouer le chargement : %v", err)
	}
	if stacks.Saines["devx"] == nil {
		t.Fatalf("la stack saine devx a disparu ; saines = %v", stacks.Noms())
	}
	if len(stacks.Cassees) != 1 || stacks.Cassees[0].Nom != "autre" {
		t.Fatalf("attendu exactement la stack « autre » en cassée ; obtenu %+v", stacks.Cassees)
	}
	// La cause reste attachée : sans elle, doctor ne pourrait dire QUE réparer.
	if !strings.Contains(stacks.Cassees[0].Err.Error(), "imag") {
		t.Errorf("l'erreur de la stack cassée doit nommer la clé fautive ; obtenu : %v",
			stacks.Cassees[0].Err)
	}
	// Et une stack cassée n'est PAS déclarée saine : sans quoi le spawn la
	// prendrait pour bonne et partirait avec une Stack à zéro (image vide).
	if _, ok := stacks.Saines["autre"]; ok {
		t.Error("une stack cassée ne doit pas figurer parmi les saines")
	}
}

// Get doit dire LAQUELLE des deux situations se présente : « illisible » et
// « introuvable » ne se réparent pas pareil. Répondre « introuvable » d'une
// stack qui existe enverrait l'utilisateur créer un fichier qu'il a déjà.
func TestStacksGetDistingueIllisibleDeAbsente(t *testing.T) {
	denHome := t.TempDir()
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisStack(t, denHome, "autre", "image: autre:v1\nimag: faute\n")
	stacks, err := LoadStacks(denHome)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := stacks.Get("devx"); err != nil {
		t.Errorf("une stack saine doit se résoudre : %v", err)
	}

	_, errCassee := stacks.Get("autre")
	if errCassee == nil {
		t.Fatal("une stack cassée ne doit pas se résoudre")
	}
	if !strings.Contains(errCassee.Error(), "illisible") {
		t.Errorf("stack cassée : le message doit dire « illisible », pas « introuvable » ; obtenu : %v", errCassee)
	}

	_, errAbsente := stacks.Get("jamais-declaree")
	if errAbsente == nil {
		t.Fatal("une stack absente ne doit pas se résoudre")
	}
	if !strings.Contains(errAbsente.Error(), "introuvable") {
		t.Errorf("stack absente : le message doit dire « introuvable » ; obtenu : %v", errAbsente)
	}
	// Les stacks proposées sont les SAINES : proposer une stack cassée comme
	// solution de repli enverrait droit dans le mur.
	if strings.Contains(errAbsente.Error(), "autre") {
		t.Errorf("la liste des stacks déclarées ne doit proposer que des stacks saines ; obtenu : %v", errAbsente)
	}
}
