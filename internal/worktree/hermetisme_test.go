package worktree

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// racineModule remonte depuis le répertoire courant jusqu'au dossier qui
// porte go.mod. `go test` exécute chaque paquet depuis SON PROPRE dossier,
// pas depuis la racine du module : ce balayage doit donc la retrouver
// lui-même plutôt que de supposer un chemin relatif fixe.
func racineModule(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod introuvable en remontant depuis %s", dir)
		}
		dir = parent
	}
}

// TestExigeNeutraliseEnvironnementGitSiGitEstLanceEnClair balaye internal/ et
// cmd/ : tout paquet dont un fichier de test lance git EN CLAIR doit avoir un
// TestMain qui APPELLE NeutraliseEnvironnementGit.
//
// Cette garde existe parce que la même classe de défaut s'est rouverte DEUX
// FOIS après sa première fermeture (voir la godoc de NeutraliseEnvironnementGit,
// dans worktree.go) : internal/worktree avait fermé le trou pour lui-même,
// puis internal/cli l'a rouvert sans le savoir, puis internal/spawn — mesuré :
// un `go test ./internal/spawn/...` lancé sous GIT_DIR/GIT_WORK_TREE désignant
// un dépôt tiers y a ajouté 32 commits, et rien dans la suite ne l'a signalé
// avant une revue humaine.
//
// ANALYSE SYNTAXIQUE (go/ast), PAS UNE REGEXP SUR LE TEXTE BRUT — et c'est la
// raison, pas une préférence de style. Une première version cherchait la
// sous-chaîne « NeutraliseEnvironnementGit » dans le fichier entier : elle
// matchait tout aussi bien un COMMENTAIRE qui cite le nom du helper qu'un
// APPEL réel. Mesuré : un TestMain qui ne neutralise RIEN
// (`os.Exit(m.Run())` seul), précédé d'une godoc qui mentionne
// `worktree.NeutraliseEnvironnementGit`, passait cette garde — la garde
// censée fermer « une assertion verte pour une raison étrangère » était
// elle-même verte pour une raison étrangère. `go/parser` + `go/ast` répond
// aux deux questions qui comptent sans jamais confondre du code avec du
// texte : (a) le fichier appelle-t-il RÉELLEMENT `exec.Command`/
// `exec.CommandContext` avec `"git"` en littéral de chaîne (jamais un
// commentaire, jamais une chaîne construite) ? (b) le CORPS d'un `TestMain`
// contient-il RÉELLEMENT un appel à `NeutraliseEnvironnementGit` ?
//
// Limite assumée et documentée : seuls `exec.Command`/`exec.CommandContext`
// avec `"git"` en littéral sont détectés — un appel indirect (variable,
// constante, helper tiers qui shelle vers git) échapperait à cette garde.
// Compromis délibéré : ces deux formes couvrent les trois occurrences réelles
// rencontrées à ce jour (internal/worktree, internal/cli, internal/spawn), et
// résoudre les indirections demanderait une analyse de types complète
// (golang.org/x/tools/go/packages), une dépendance que ce projet n'admet pas
// (stdlib + cobra + yaml.v3 seulement).
func TestExigeNeutraliseEnvironnementGitSiGitEstLanceEnClair(t *testing.T) {
	racine := racineModule(t)

	for _, sousArbre := range []string{"internal", "cmd"} {
		depart := filepath.Join(racine, sousArbre)
		entrees, err := os.ReadDir(depart)
		if err != nil {
			continue // sous-arbre absent : rien à balayer
		}
		for _, e := range entrees {
			if !e.IsDir() {
				continue
			}
			verifiePaquetHermetique(t, filepath.Join(depart, e.Name()))
		}
	}
}

func verifiePaquetHermetique(t *testing.T, dirPaquet string) {
	t.Helper()
	fichiers, err := os.ReadDir(dirPaquet)
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	var lanceGitBrut []string
	aLeHelper := false

	for _, f := range fichiers {
		if f.IsDir() || !strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		chemin := filepath.Join(dirPaquet, f.Name())
		fichierAST, err := parser.ParseFile(fset, chemin, nil, 0)
		if err != nil {
			t.Fatalf("analyse de %s : %v", chemin, err)
		}

		if aliasExec, importe := aliasImport(fichierAST, "os/exec"); importe {
			if appelleGitEnClair(fichierAST, aliasExec) {
				lanceGitBrut = append(lanceGitBrut, chemin)
			}
		}
		if appelleHelperDepuisTestMain(fichierAST) {
			aLeHelper = true
		}
	}

	if len(lanceGitBrut) > 0 && !aLeHelper {
		t.Errorf(
			"%s : lance git en clair dans ses tests (%v) sans TestMain qui "+
				"APPELLE NeutraliseEnvironnementGit — ajoute un TestMain sur le "+
				"modèle d'internal/cli/main_test.go, sous peine d'écrire dans le "+
				"dépôt désigné par GIT_DIR/GIT_WORK_TREE quand ces variables sont "+
				"exportées (courant sous agents et hooks git)",
			dirPaquet, lanceGitBrut)
	}
}

// aliasImport rend le nom LOCAL sous lequel un fichier importe cheminImport,
// et si cet import est présent. Un alias explicite (`pkgexec "os/exec"`)
// l'emporte ; sans alias, le nom local est le dernier segment du chemin
// (« exec » pour « os/exec »).
func aliasImport(fichier *ast.File, cheminImport string) (alias string, ok bool) {
	for _, imp := range fichier.Imports {
		chemin, err := strconv.Unquote(imp.Path.Value)
		if err != nil || chemin != cheminImport {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, true
		}
		segments := strings.Split(chemin, "/")
		return segments[len(segments)-1], true
	}
	return "", false
}

// appelleGitEnClair dit si le fichier contient un appel RÉEL (un *ast.CallExpr,
// jamais un commentaire ni une chaîne) à exec.Command("git", …) ou
// exec.CommandContext(ctx, "git", …), avec "git" en LITTÉRAL de chaîne — une
// variable ou une constante nommée autrement échapperait, à dessein (voir la
// limite documentée sur TestExigeNeutraliseEnvironnementGitSiGitEstLanceEnClair).
func appelleGitEnClair(fichier *ast.File, aliasExec string) bool {
	trouve := false
	ast.Inspect(fichier, func(n ast.Node) bool {
		appel, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := appel.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		base, ok := sel.X.(*ast.Ident)
		if !ok || base.Name != aliasExec {
			return true
		}
		// exec.Command(name, arg...) : "git" est le premier argument.
		// exec.CommandContext(ctx, name, arg...) : "git" est le second.
		var indexNom int
		switch sel.Sel.Name {
		case "Command":
			indexNom = 0
		case "CommandContext":
			indexNom = 1
		default:
			return true
		}
		if len(appel.Args) <= indexNom {
			return true
		}
		lit, ok := appel.Args[indexNom].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if valeur, err := strconv.Unquote(lit.Value); err == nil && valeur == "git" {
			trouve = true
		}
		return true
	})
	return trouve
}

// appelleHelperDepuisTestMain dit si le fichier déclare une fonction nommée
// TestMain dont le CORPS contient un appel RÉEL à NeutraliseEnvironnementGit —
// qualifié (worktree.NeutraliseEnvironnementGit, pour tout paquet qui importe
// worktree) ou non (le paquet worktree s'appelle lui-même sans qualifier).
// Une simple MENTION du nom — dans une godoc, un commentaire, une chaîne — ne
// compte pas : c'est exactement ce que la version regexp confondait avec un
// appel (voir la godoc de TestExigeNeutraliseEnvironnementGitSiGitEstLanceEnClair).
func appelleHelperDepuisTestMain(fichier *ast.File) bool {
	trouve := false
	for _, decl := range fichier.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "TestMain" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			appel, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := appel.Fun.(type) {
			case *ast.Ident:
				if fun.Name == "NeutraliseEnvironnementGit" {
					trouve = true
				}
			case *ast.SelectorExpr:
				if fun.Sel.Name == "NeutraliseEnvironnementGit" {
					trouve = true
				}
			}
			return true
		})
	}
	return trouve
}
