package config

import (
	"strconv"
	"strings"
	"testing"
)

func globalValide() *Global {
	return &Global{
		Agents: map[string]Agent{
			"claude": {ConfigDir: "/tmp/claude", Update: "claude update", BinDirs: []string{"$HOME/.local/bin"}},
		},
		Defaults:       Defaults{Agent: "claude", Stack: "devx"},
		SSH:            SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
		WorktreeRoot:   "/tmp/wt",
	}
}

func TestValidateConfigCorrecte(t *testing.T) {
	if errs := globalValide().Validate(); len(errs) != 0 {
		t.Errorf("attendu 0 erreur, obtenu %v", errs)
	}
}

// Les trois modes SSH du spec §10 sont acceptés. `none` n'était couvert par
// aucun test : rien ne prouvait qu'un mode déclaré au spec passait la validation.
func TestValidateAccepteLesModesSSHDuSpec(t *testing.T) {
	for _, mode := range []string{"agent-forward", "mount", "none"} {
		t.Run(mode, func(t *testing.T) {
			g := globalValide()
			g.SSH.Mode = mode
			if mode == "mount" {
				g.SSH.Dir = "/tmp/ssh_sbx" // requis dans ce mode uniquement
			}
			if errs := g.Validate(); len(errs) != 0 {
				t.Errorf("mode %q refusé alors qu'il est déclaré au spec §10 : %v", mode, errs)
			}
		})
	}
}

func TestValidateDetecteLesFautes(t *testing.T) {
	cas := []struct {
		nom     string
		muter   func(*Global)
		attendu string
	}{
		{"agent par defaut absent du registre", func(g *Global) { g.Defaults.Agent = "codex" }, "codex"},
		{"agent par defaut vide", func(g *Global) { g.Defaults.Agent = "" }, "defaults.agent"},
		{"stack par defaut vide", func(g *Global) { g.Defaults.Stack = "" }, "defaults.stack"},
		{"registre d'agents vide", func(g *Global) { g.Agents = nil }, "agents"},
		{"mode ssh inconnu", func(g *Global) { g.SSH.Mode = "vpn" }, "ssh.mode"},
		{"layout worktree inconnu", func(g *Global) { g.WorktreeLayout = "eparpille" }, "worktree_layout"},
		{"mode mount sans dir", func(g *Global) { g.SSH.Mode = "mount"; g.SSH.Dir = "" }, "ssh.dir"},
		{"agent sans commande update", func(g *Global) {
			a := g.Agents["claude"]
			a.Update = ""
			g.Agents["claude"] = a
		}, "update"},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			g := globalValide()
			c.muter(g)
			errs := g.Validate()
			if len(errs) == 0 {
				t.Fatalf("attendu au moins une erreur mentionnant %q", c.attendu)
			}
			var tout []string
			for _, e := range errs {
				tout = append(tout, e.Error())
			}
			joint := strings.Join(tout, " | ")
			if !strings.Contains(joint, c.attendu) {
				t.Errorf("erreurs = %q, attendu une mention de %q", joint, c.attendu)
			}
		})
	}
}

// T2-min-5 : asymétrie entre les deux juges d'un même champ. validate.go testait
// `Update == ""` là où agent.CommandeFraicheur teste `strings.TrimSpace(...)`.
// Un `update: "   "` passait donc `den doctor` en vert et n'échouait qu'au
// spawn, au moment le plus tardif et le moins lisible. Les deux doivent juger
// pareil, et c'est le plus strict qui gagne.
func TestValidateRefuseUnUpdateBlanc(t *testing.T) {
	for _, blanc := range []string{"   ", "\t", "\n"} {
		t.Run(strconv.Quote(blanc), func(t *testing.T) {
			g := globalValide()
			a := g.Agents["claude"]
			a.Update = blanc
			g.Agents["claude"] = a

			errs := g.Validate()
			if len(errs) == 0 {
				t.Fatalf("update = %q : attendu un refus, la commande de fraîcheur le rejette déjà au spawn", blanc)
			}
			// L'erreur visée, pas une autre : globalValide() est par ailleurs saine,
			// donc toute erreur non liée à update signalerait un défaut du fixture.
			var tout []string
			for _, e := range errs {
				tout = append(tout, e.Error())
			}
			joint := strings.Join(tout, " | ")
			if !strings.Contains(joint, "agents.claude.update") {
				t.Errorf("erreurs = %q, attendu la clé agents.claude.update", joint)
			}
		})
	}
}

// --- D4 : bin_dirs est une liste de CHEMINS, pas de code (T2-min-4) ---------
//
// agent.CommandeFraicheur injecte les bin_dirs littéralement dans un
// `export PATH=%q`, sans échappement, parce que le `$HOME` qu'ils contiennent
// DOIT être expansé par le bash de la VM (invariant 1 de CommandeFraicheur).
// Mesuré sur bash : cette même absence d'échappement fait exécuter `$(...)` et
// les backticks, rend une tabulation en `\t` littéral, et laisse un élément
// vide devenir « le répertoire courant » dans le PATH.
//
// Échapper serait donc contradictoire — cela tuerait `$HOME`. Le défaut est de
// TYPAGE : ces valeurs ne sont pas des chemins, et c'est là qu'on les refuse.
// Aucune escalade de privilège n'est en jeu (c'est la config de l'utilisateur,
// et `update:` est du shell arbitraire par contrat) : ce qui est corrigé, c'est
// qu'un champ documenté « liste de chemins » accepte autre chose que des chemins.
func TestValidateRefuseUnBinDirQuiNEstPasUnChemin(t *testing.T) {
	cas := []struct {
		nom     string
		valeur  string
		attendu string // fragment que le message doit porter
	}{
		{"substitution de commande", "/opt/$(id -un)", "$("},
		{"backtick", "/opt/`id -un`", "`"},
		{"substitution imbriquee dans une expansion", "${x:-$(id)}", "$("},
		{"tabulation", "/opt/a\tb", "caractère de contrôle"},
		{"retour a la ligne", "/opt/a\nb", "caractère de contrôle"},
		{"entree vide", "", "vide"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			g := globalValide()
			a := g.Agents["claude"]
			a.BinDirs = []string{c.valeur}
			g.Agents["claude"] = a

			errs := g.Validate()
			if len(errs) == 0 {
				t.Fatalf("bin_dirs[0] = %q : attendu un refus, obtenu aucune erreur", c.valeur)
			}
			var tout []string
			for _, e := range errs {
				tout = append(tout, e.Error())
			}
			joint := strings.Join(tout, " | ")
			// La clé INDEXÉE : avec quatre bin_dirs, « bin_dirs » seul ne dit
			// pas lequel corriger.
			if !strings.Contains(joint, "agents.claude.bin_dirs[0]") {
				t.Errorf("erreurs = %q, attendu la clé indexée agents.claude.bin_dirs[0]", joint)
			}
			if !strings.Contains(joint, c.attendu) {
				t.Errorf("erreurs = %q, attendu la cause nommée (%q)", joint, c.attendu)
			}
		})
	}
}

// La contrepartie indispensable : `$HOME` et `${HOME}` visent le home DE LA VM
// et doivent traverser INTACTS (invariant 1 de agent.CommandeFraicheur). Un
// refus trop large les casserait, et le test ci-dessus ne le verrait pas.
func TestValidateAccepteLesBinDirsLegitimes(t *testing.T) {
	for _, valeur := range []string{
		"$HOME/.local/bin",
		"${HOME}/.local/bin",
		"/usr/local/bin",
		"$HOME/.claude/local",
	} {
		t.Run(valeur, func(t *testing.T) {
			g := globalValide()
			a := g.Agents["claude"]
			a.BinDirs = []string{valeur}
			g.Agents["claude"] = a
			if errs := g.Validate(); len(errs) != 0 {
				t.Errorf("bin_dirs[0] = %q refusé alors qu'il doit traverser intact : %v", valeur, errs)
			}
		})
	}
}

func TestValidateCumuleLesErreurs(t *testing.T) {
	g := globalValide()
	g.SSH.Mode = "vpn"
	g.WorktreeLayout = "eparpille"
	if errs := g.Validate(); len(errs) < 2 {
		t.Errorf("attendu au moins 2 erreurs cumulées, obtenu %d : %v", len(errs), errs)
	}
}

func TestValidateDeterminismeTriAgents(t *testing.T) {
	// Les maps Go itèrent dans un ordre aléatoire. Validate() doit trier les noms
	// d'agents avant de les parcourir, sinon l'ordre des erreurs varie entre les
	// exécutions. Ce test prouve que le tri fonctionne en créant deux agents
	// fautifs (alpha, zeta sans Update) et en vérifiant que les erreurs
	// apparaissent dans l'ordre alphabétique.
	g := globalValide()
	g.Agents = map[string]Agent{
		"zeta":  {ConfigDir: "/tmp/zeta", Update: "", BinDirs: []string{}},
		"alpha": {ConfigDir: "/tmp/alpha", Update: "", BinDirs: []string{}},
	}
	g.Defaults.Agent = "alpha"

	errs := g.Validate()
	if len(errs) < 2 {
		t.Fatalf("attendu au moins 2 erreurs, obtenu %d", len(errs))
	}

	// Extrait les messages et vérifie l'ordre
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}

	// alpha.update doit apparaître avant zeta.update
	alphaIdx := -1
	zetaIdx := -1
	for i, msg := range msgs {
		if strings.Contains(msg, "alpha.update") {
			alphaIdx = i
		}
		if strings.Contains(msg, "zeta.update") {
			zetaIdx = i
		}
	}

	if alphaIdx == -1 || zetaIdx == -1 {
		t.Fatalf("attendu erreurs alpha.update et zeta.update, obtenu %v", msgs)
	}
	if alphaIdx >= zetaIdx {
		t.Errorf("ordre des erreurs non trié : alpha à %d, zeta à %d (messages : %v)", alphaIdx, zetaIdx, msgs)
	}
}
