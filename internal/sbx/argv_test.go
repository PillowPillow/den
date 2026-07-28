package sbx

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func createComplet() Create {
	return Create{
		Nom:       "api.feat12",
		Image:     "docker.io/library/dgdevx:v1",
		KitsStack: []string{"/den/kits/ssh-known-hosts", "/den/stacks/dgdevx/kit"},
		KitMixin:  "/den/cache/mixins/api.feat12",
		Workspaces: []string{
			"/den/worktrees/feat12/api",
			"/den/worktrees/feat12/front",
			"/home/moi/.den/agents/claude",
			"/home/moi/.ssh_sbx",
		},
	}
}

func TestArgvCreateStructure(t *testing.T) {
	argv, err := ArgvCreate(createComplet())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if argv[0] != "create" {
		t.Errorf("argv[0] = %q, attendu create", argv[0])
	}
	// `sbx create [flags] AGENT PATH [PATH...]` : l'agent positionnel est
	// OBLIGATOIRE. L'omettre produit « unknown agent ».
	iAgent := slices.Index(argv, AgentPositionnel)
	if iAgent < 0 {
		t.Fatalf("l'agent positionnel %q est absent : %v", AgentPositionnel, argv)
	}
	// Tout ce qui suit l'agent est un chemin, rien d'autre.
	for _, a := range argv[iAgent+1:] {
		if strings.HasPrefix(a, "-") {
			t.Errorf("un flag (%q) traîne après l'agent positionnel : %v", a, argv)
		}
	}
	if !slices.Equal(argv[iAgent+1:], createComplet().Workspaces) {
		t.Errorf("positionnels = %v, attendu les workspaces dans l'ordre", argv[iAgent+1:])
	}
}

// L'invariant le plus coûteux du plan : le mixin est fail-closed et le
// dispatcher sbx sort au premier échec, privant les kits SUIVANTS de leurs
// startup commands. Il doit donc être le DERNIER --kit.
func TestArgvCreateMixinEnDernierKit(t *testing.T) {
	argv, err := ArgvCreate(createComplet())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	var kits []string
	for i, a := range argv {
		if a == "--kit" && i+1 < len(argv) {
			kits = append(kits, argv[i+1])
		}
	}
	if len(kits) != 3 {
		t.Fatalf("kits = %v, attendu 3", kits)
	}
	if kits[len(kits)-1] != "/den/cache/mixins/api.feat12" {
		t.Errorf("le mixin doit être le DERNIER --kit ; kits = %v", kits)
	}
	// Et l'ordre des kits de stack est préservé : c'est un ordre de layering.
	if kits[0] != "/den/kits/ssh-known-hosts" || kits[1] != "/den/stacks/dgdevx/kit" {
		t.Errorf("ordre des kits de stack non préservé : %v", kits)
	}
}

// Le chargement de `Stack.Kits` conserve les entrées vides (une puce YAML
// malformée donne une chaîne vide dans la liste). Rien en amont ne les filtre :
// c'est ici que la neutralisation a lieu, et un `--kit ""` ferait échouer le
// dispatcher de kits — donc le boot entier, mixin compris.
func TestArgvCreateIgnoreLesKitsVides(t *testing.T) {
	c := createComplet()
	c.KitsStack = []string{"/den/kits/ssh-known-hosts", "", "/den/stacks/dgdevx/kit"}

	argv, err := ArgvCreate(c)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	var kits []string
	for i, a := range argv {
		if a == "--kit" && i+1 < len(argv) {
			kits = append(kits, argv[i+1])
		}
	}
	if slices.Contains(kits, "") {
		t.Errorf("un kit vide a été émis ; kits = %v ; argv = %v", kits, argv)
	}
	// Et l'entrée vide est bien tombée, pas seulement décalée.
	attendu := []string{
		"/den/kits/ssh-known-hosts",
		"/den/stacks/dgdevx/kit",
		"/den/cache/mixins/api.feat12",
	}
	if !slices.Equal(kits, attendu) {
		t.Errorf("kits = %v, attendu %v", kits, attendu)
	}
}

func TestArgvCreateNomEtTemplate(t *testing.T) {
	argv, err := ArgvCreate(createComplet())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if i := slices.Index(argv, "--name"); i < 0 || argv[i+1] != "api.feat12" {
		t.Errorf("--name absent ou faux : %v", argv)
	}
	// L'image part VERBATIM : c'est l'utilisateur qui décide si elle porte un
	// registre (docker.io/library/…) ou non. den ne préfixe rien.
	if i := slices.Index(argv, "--template"); i < 0 || argv[i+1] != "docker.io/library/dgdevx:v1" {
		t.Errorf("--template absent ou faux : %v", argv)
	}
}

// Aucun --label : sbx n'en a pas (vérifié le 2026-07-28). Ce test est le
// garde-fou contre une réintroduction depuis le spec d'origine.
func TestArgvCreateNEmetJamaisDeLabel(t *testing.T) {
	argv, err := ArgvCreate(createComplet())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if slices.Contains(argv, "--label") {
		t.Errorf("sbx create n'a pas de --label ; argv = %v", argv)
	}
}

func TestArgvCreateRefuseLesEntreesIncompletes(t *testing.T) {
	cas := map[string]func(c *Create){
		"nom vide":            func(c *Create) { c.Nom = "" },
		"nom non sandboxable": func(c *Create) { c.Nom = "mon_api" },
		"image vide":          func(c *Create) { c.Image = "" },
		"mixin absent":        func(c *Create) { c.KitMixin = "" },
		"aucun workspace":     func(c *Create) { c.Workspaces = nil },
	}
	for nom, casse := range cas {
		c := createComplet()
		casse(&c)
		if _, err := ArgvCreate(c); err == nil {
			t.Errorf("%s : doit être refusé", nom)
		}
	}
}

// Le contenu des workspaces est gardé ICI, et pas chez l'appelant : c'est
// `ArgvCreate` qui transforme ces valeurs en ligne de commande, et « l'appelant
// garantit que les chemins sont absolus » est un contrat écrit nulle part.
// `config.ExpandPath` n'expanse qu'un « ~ » en tête : un `worktree_root:
// mes-worktrees` dans config.yaml arriverait relatif jusqu'ici.
func TestArgvCreateRefuseLesWorkspacesDouteux(t *testing.T) {
	cas := []struct {
		nom        string
		workspaces []string
		position   string // la position doit figurer dans l'erreur
		fautif     string // le chemin fautif aussi
	}{
		{"entrée vide", []string{"/dev/api", ""}, "n°2", ""},
		{"chemin relatif", []string{"/dev/api", "mes-worktrees/api"}, "n°2", "mes-worktrees/api"},
		{"chemin lu comme un flag", []string{"/dev/api", "-api"}, "n°2", "-api"},
		{"le repo lui-même relatif", []string{"mes-worktrees/api"}, "n°1", "mes-worktrees/api"},
		{"suffixe :ro sans chemin", []string{"/dev/api", ":ro"}, "n°2", ":ro"},
	}
	for _, cas := range cas {
		c := createComplet()
		c.Workspaces = cas.workspaces
		_, err := ArgvCreate(c)
		if err == nil {
			t.Errorf("%s : doit être refusé", cas.nom)
			continue
		}
		// Une garde qui dit « un workspace est invalide » sans dire lequel
		// laisse l'utilisateur chercher dans une liste qu'il n'a pas écrite.
		if !strings.Contains(err.Error(), cas.position) {
			t.Errorf("%s : l'erreur ne situe pas le workspace (%s) : %v", cas.nom, cas.position, err)
		}
		if cas.fautif != "" && !strings.Contains(err.Error(), cas.fautif) {
			t.Errorf("%s : l'erreur ne nomme pas le chemin fautif %q : %v", cas.nom, cas.fautif, err)
		}
	}
}

// Le « :ro » est une option de montage, pas une partie du chemin : la garde
// doit le retirer avant de juger l'absoluité. Sans ce test, un resserrement
// casserait le montage en lecture seule sans que rien ne le signale.
func TestArgvCreateAccepteLeSuffixeRO(t *testing.T) {
	c := createComplet()
	c.Workspaces = []string{"/dev/api", "/home/moi/.ssh_sbx:ro"}

	argv, err := ArgvCreate(c)
	if err != nil {
		t.Fatalf("le suffixe :ro doit rester accepté : %v", err)
	}
	// Et il part VERBATIM : c'est sbx qui l'interprète, pas den.
	if !slices.Contains(argv, "/home/moi/.ssh_sbx:ro") {
		t.Errorf("le suffixe :ro doit traverser intact : %v", argv)
	}
}

// Le nom composé porte un point : la validation doit accepter le séparateur
// tout en refusant les caractères que `sbx create --name` rejette.
func TestArgvCreateAccepteLeNomCompose(t *testing.T) {
	for _, nom := range []string{"api", "api.feat12", "mon-api.feat-2"} {
		c := createComplet()
		c.Nom = nom
		if _, err := ArgvCreate(c); err != nil {
			t.Errorf("%q doit être accepté : %v", nom, err)
		}
	}
}

func TestArgvCreateGolden(t *testing.T) {
	cas := []struct {
		fichier string
		c       Create
	}{
		{"create-minimal.golden", Create{
			Nom:        "api",
			Image:      "devx:v1",
			KitMixin:   "/den/cache/mixins/api",
			Workspaces: []string{"/dev/api", "/home/moi/.den/agents/claude"},
		}},
		{"create-complet.golden", createComplet()},
	}
	for _, c := range cas {
		argv, err := ArgvCreate(c.c)
		if err != nil {
			t.Fatalf("%s : %v", c.fichier, err)
		}
		chemin := filepath.Join("testdata", c.fichier)
		attendu, err := os.ReadFile(chemin)
		if err != nil {
			t.Fatalf("lecture de %s : %v", chemin, err)
		}
		got := strings.Join(argv, "\n") + "\n"
		if got != string(attendu) {
			t.Errorf("%s\n--- obtenu ---\n%s\n--- attendu ---\n%s", chemin, got, attendu)
		}
	}
}
