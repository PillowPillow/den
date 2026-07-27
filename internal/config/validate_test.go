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
