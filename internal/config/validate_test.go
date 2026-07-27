package config

import (
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
