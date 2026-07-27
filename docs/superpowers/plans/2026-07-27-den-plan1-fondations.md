# `den` — Plan 1 : Fondations & inspection

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Poser le socle de configuration de `den` (chargement + cascade + validation) et le rendre
immédiatement utile par trois commandes d'inspection : `den nest ls`, `den nest show <n>`, `den doctor`.

**Architecture:** Un binaire Go/cobra unique. Toute la logique vit dans `internal/`, en **fonctions
pures testables sans `sbx` ni réseau** : `internal/config` charge `~/.den/config.yaml` et les
`stacks/*/stack.yaml` ; `internal/nest` charge les nests et calcule les dérivations (sélection de
repos, union d'egress, résolution d'agent). `cmd/den` ne fait que du câblage cobra + affichage.
Le dossier `~/.den/` est la source unique ; la variable `DEN_HOME` le surcharge, ce qui rend
l'intégralité du socle testable sur des dossiers temporaires.

**Tech Stack:** Go 1.26 · cobra · `gopkg.in/yaml.v3` · bibliothèque standard uniquement pour le reste.

## Global Constraints

Ces contraintes s'appliquent à **toutes** les tâches, elles ne sont pas répétées ensuite.

- **Module Go :** `github.com/PillowPillow/den` (remote `git@github.com:PillowPillow/den.git` déjà en place).
- **Go 1.26**, binaire statique, aucune dépendance hors `cobra` + `yaml.v3`.
- **TDD strict** : le test est écrit et **exécuté en échec** avant toute implémentation.
- **Zéro réseau, zéro `sbx`, zéro `git` dans les tests de ce plan.** Tout passe par `t.TempDir()` +
  `DEN_HOME`. Un test qui exige un binaire externe est un test à refuser en revue.
- **Messages utilisateur et commentaires en français** (cohérence avec le spec et les kits existants).
- **Déterminisme** : toute liste destinée à un affichage ou à un futur golden file (egress, nests,
  repos) est **triée**. Non négociable : les maps Go ont un ordre d'itération aléatoire, et les
  golden files de l'argv `sbx create` (plan 2) en dépendent.
- **Pas d'expansion de `$HOME` côté hôte pour `bin_dirs`** : ces chemins sont résolus **dans la VM**
  par bash. Seul `~` en tête des chemins *hôte* (`config_dir`, `worktree_root`, `ssh.dir`,
  `repos[].path`) est expansé. Confondre les deux casse le §9.1 du spec.
- **Commits fréquents**, un par tâche minimum, message conventionnel (`feat:`, `test:`, `refactor:`).

**Spec de référence :** `docs/superpowers/specs/2026-07-27-den-cli-design.md` — §3 (layout), §4
(schémas), §5 (commandes), §9/§9.1 (agents et fraîcheur), §12 (archi & tests).

**Hors périmètre de ce plan** (plans suivants, ne pas anticiper) : `internal/sbx`, `internal/worktree`,
`internal/policy`, `internal/ports`, le mixin généré, et les commandes `den <nest>`, `den ls`,
`den sh`, `den rm`, `den ports`, `den build`.

---

## Structure des fichiers

| Fichier | Responsabilité |
|---|---|
| `go.mod`, `go.sum` | Module + dépendances |
| `cmd/den/main.go` | `main()`, appelle `cli.Execute()` |
| `internal/cli/root.go` | Commande racine cobra, flag global `--den-home`, `den version` |
| `internal/cli/nest.go` | `den nest ls` / `den nest show` |
| `internal/cli/doctor.go` | `den doctor` |
| `internal/config/paths.go` | Résolution de `DEN_HOME`, expansion de `~` |
| `internal/config/config.go` | Types + `LoadGlobal` (config.yaml) |
| `internal/config/validate.go` | `(*Global).Validate()` |
| `internal/config/stack.go` | Types + `LoadStack` / `LoadStacks` |
| `internal/nest/nest.go` | Types + `LoadNest` / `ListNests` |
| `internal/nest/repos.go` | `SelectRepos` |
| `internal/nest/egress.go` | `UnionEgress` |
| `internal/nest/resolve.go` | `ResolveAgent`, `Resolve` |
| `internal/doctor/doctor.go` | `Run` — diagnostics, sans effet de bord |

Chaque fichier a une responsabilité unique et reste sous ~150 lignes ; le test vit à côté
(`*_test.go`, même paquet).

---

## Task 1: Bootstrap du module et squelette cobra

**Files:**
- Create: `go.mod`
- Create: `cmd/den/main.go`
- Create: `internal/cli/root.go`
- Test: `internal/cli/root_test.go`
- Create: `.gitignore`

**Interfaces:**
- Consumes: rien (première tâche).
- Produces: `cli.Execute() error` · `cli.NewRootCmd() *cobra.Command` · variable `cli.Version string`.
  Toutes les tâches cobra suivantes enregistrent leur sous-commande sur le résultat de `NewRootCmd()`.

- [ ] **Step 1: Initialiser le module et récupérer les dépendances**

```bash
cd /Users/polochon/Development/Pillow/den
go mod init github.com/PillowPillow/den
go get github.com/spf13/cobra@latest
go get gopkg.in/yaml.v3@latest
```

- [ ] **Step 2: Écrire le test en échec**

`internal/cli/root_test.go` :

```go
package cli

import (
	"bytes"
	"strings"
	"testing"
)

// exécute la commande racine avec des arguments et retourne sa sortie standard.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestVersionAfficheLaVersion(t *testing.T) {
	Version = "1.2.3"
	out, err := run(t, "version")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(out, "1.2.3") {
		t.Errorf("sortie = %q, attendu contenant %q", out, "1.2.3")
	}
}

func TestCommandeInconnueEchoue(t *testing.T) {
	if _, err := run(t, "nexistepas"); err == nil {
		t.Error("attendu une erreur pour une commande inconnue, obtenu nil")
	}
}
```

- [ ] **Step 3: Lancer le test et vérifier qu'il échoue**

Run: `go test ./internal/cli/ -run TestVersion -v`
Expected: FAIL — `undefined: NewRootCmd`, `undefined: Version`.

- [ ] **Step 4: Implémenter le minimum**

`internal/cli/root.go` :

```go
// Package cli câble les commandes cobra de den. Aucune logique métier ici :
// tout ce qui se teste vit dans internal/config, internal/nest, internal/doctor.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version est injectée au build (-ldflags "-X .../internal/cli.Version=...").
var Version = "dev"

// denHome porte la valeur du flag global --den-home ; vide = résolution par défaut.
var denHome string

// NewRootCmd construit un arbre de commandes neuf. Renvoyer une nouvelle instance
// à chaque appel (plutôt qu'un singleton) est ce qui rend les commandes testables.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "den",
		Short:         "Sandboxes sbx simples et répétables",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&denHome, "den-home", "",
		"dossier de config den (défaut : $DEN_HOME ou ~/.den)")

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Affiche la version de den",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "den %s\n", Version)
			return nil
		},
	})
	return root
}

// Execute est le point d'entrée appelé par main.
func Execute() error {
	return NewRootCmd().Execute()
}
```

`cmd/den/main.go` :

```go
package main

import (
	"fmt"
	"os"

	"github.com/PillowPillow/den/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "den:", err)
		os.Exit(1)
	}
}
```

`.gitignore` :

```
/den
/staging/
```

- [ ] **Step 5: Lancer les tests et vérifier qu'ils passent**

Run: `go test ./internal/cli/ -v`
Expected: PASS (2 tests).

- [ ] **Step 6: Vérifier que le binaire se construit et tourne**

Run: `go build -o den ./cmd/den && ./den version`
Expected: `den dev`

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum cmd internal .gitignore
git commit -m "feat: bootstrap du module den et squelette cobra"
```

---

## Task 2: Résolution de `DEN_HOME` et expansion des chemins

**Files:**
- Create: `internal/config/paths.go`
- Test: `internal/config/paths_test.go`

**Interfaces:**
- Consumes: rien.
- Produces:
  - `config.Home(flagValue string) (string, error)` — priorité : `flagValue` > `$DEN_HOME` > `~/.den`.
  - `config.ExpandPath(p string) (string, error)` — expanse un `~` **en tête uniquement**, laisse
    tout le reste intact (dont `$HOME`, destiné à la VM).

- [ ] **Step 1: Écrire le test en échec**

`internal/config/paths_test.go` :

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomePriorites(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("le flag gagne sur tout", func(t *testing.T) {
		t.Setenv("DEN_HOME", "/depuis/env")
		got, err := Home("/depuis/flag")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/depuis/flag" {
			t.Errorf("Home = %q, attendu %q", got, "/depuis/flag")
		}
	})

	t.Run("sinon DEN_HOME", func(t *testing.T) {
		t.Setenv("DEN_HOME", "/depuis/env")
		got, err := Home("")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/depuis/env" {
			t.Errorf("Home = %q, attendu %q", got, "/depuis/env")
		}
	})

	t.Run("sinon ~/.den", func(t *testing.T) {
		t.Setenv("DEN_HOME", "")
		got, err := Home("")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, ".den")
		if got != want {
			t.Errorf("Home = %q, attendu %q", got, want)
		}
	})
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	cas := []struct {
		nom   string
		in    string
		want  string
	}{
		{"tilde seul", "~", home},
		{"tilde en tete", "~/dev/projet", filepath.Join(home, "dev/projet")},
		{"chemin absolu inchange", "/opt/truc", "/opt/truc"},
		{"chemin relatif inchange", "dev/projet", "dev/projet"},
		{"vide inchange", "", ""},
		// $HOME vise le home DE LA VM : den ne doit jamais le resoudre cote hote.
		{"dollar HOME preserve", "$HOME/.local/bin", "$HOME/.local/bin"},
		// un tilde qui n'est pas en tete n'est pas un home.
		{"tilde milieu preserve", "/opt/~/truc", "/opt/~/truc"},
		{"tilde utilisateur non supporte", "~bob/x", "~bob/x"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			got, err := ExpandPath(c.in)
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			if got != c.want {
				t.Errorf("ExpandPath(%q) = %q, attendu %q", c.in, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Lancer le test et vérifier qu'il échoue**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `undefined: Home`, `undefined: ExpandPath`.

- [ ] **Step 3: Implémenter**

`internal/config/paths.go` :

```go
// Package config charge et valide le contenu de ~/.den (config.yaml, stacks/).
package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Home résout le dossier de config den. Priorité : flag > $DEN_HOME > ~/.den.
// C'est ce point d'indirection qui rend tout le socle testable sur des dossiers temporaires.
func Home(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if env := os.Getenv("DEN_HOME"); env != "" {
		return env, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".den"), nil
}

// ExpandPath expanse un « ~ » en tête de chemin. Volontairement minimaliste :
// ni $VAR ni ~user. Les $HOME présents dans bin_dirs visent le home DE LA VM et
// doivent traverser den intacts (cf. spec §9.1).
func ExpandPath(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if p == "~" {
		return h, nil
	}
	return filepath.Join(h, p[2:]), nil
}
```

- [ ] **Step 4: Lancer les tests et vérifier qu'ils passent**

Run: `go test ./internal/config/ -v`
Expected: PASS (tous les sous-tests).

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(config): resolution de DEN_HOME et expansion des chemins"
```

---

## Task 3: Chargement de `config.yaml`

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `config.Home`, `config.ExpandPath` (Task 2).
- Produces:
  - Types `config.Agent{ConfigDir, Env, BinDirs, Update}`, `config.Defaults{Agent, Stack}`,
    `config.SSH{Mode, Dir}`, `config.Global{Agents, Defaults, SSH, WorktreeLayout, WorktreeRoot, Egress}`.
  - `config.LoadGlobal(denHome string) (*Global, error)` — lit `<denHome>/config.yaml`, applique les
    défauts, expanse les chemins hôte.

- [ ] **Step 1: Écrire le test en échec**

`internal/config/config_test.go` :

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ecrisConfig crée un DEN_HOME temporaire contenant le config.yaml fourni.
func ecrisConfig(t *testing.T, contenu string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const configComplet = `
agents:
  claude:
    config_dir: ~/.den/agents/claude
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }
    bin_dirs: ["$HOME/.local/bin", "$HOME/.claude/local"]
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: mount
  dir: ~/.ssh_sbx
worktree_layout: per-repo
worktree_root: ~/perso/wt
egress:
  - api.anthropic.com
  - github.com
`

func TestLoadGlobalChampsComplets(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	g, err := LoadGlobal(ecrisConfig(t, configComplet))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	a, ok := g.Agents["claude"]
	if !ok {
		t.Fatal("agent claude absent du registre")
	}
	if want := filepath.Join(home, ".den/agents/claude"); a.ConfigDir != want {
		t.Errorf("ConfigDir = %q, attendu %q (tilde expansé)", a.ConfigDir, want)
	}
	if a.Update != "claude update" {
		t.Errorf("Update = %q, attendu %q", a.Update, "claude update")
	}
	// $HOME doit traverser intact : il sera résolu dans la VM.
	if len(a.BinDirs) != 2 || a.BinDirs[0] != "$HOME/.local/bin" {
		t.Errorf("BinDirs = %v, attendu $HOME préservé", a.BinDirs)
	}
	if a.Env["CLAUDE_CONFIG_DIR"] != "{config_dir}" {
		t.Errorf("Env = %v, attendu le placeholder {config_dir} intact", a.Env)
	}
	if g.Defaults.Agent != "claude" || g.Defaults.Stack != "devx" {
		t.Errorf("Defaults = %+v", g.Defaults)
	}
	if g.SSH.Mode != "mount" {
		t.Errorf("SSH.Mode = %q, attendu mount", g.SSH.Mode)
	}
	if want := filepath.Join(home, ".ssh_sbx"); g.SSH.Dir != want {
		t.Errorf("SSH.Dir = %q, attendu %q", g.SSH.Dir, want)
	}
	if g.WorktreeLayout != "per-repo" {
		t.Errorf("WorktreeLayout = %q", g.WorktreeLayout)
	}
	if want := filepath.Join(home, "perso/wt"); g.WorktreeRoot != want {
		t.Errorf("WorktreeRoot = %q, attendu %q", g.WorktreeRoot, want)
	}
	if len(g.Egress) != 2 {
		t.Errorf("Egress = %v, attendu 2 entrées", g.Egress)
	}
}

func TestLoadGlobalDefautsAppliques(t *testing.T) {
	denHome := ecrisConfig(t, "defaults:\n  agent: claude\n  stack: devx\n")
	g, err := LoadGlobal(denHome)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if g.SSH.Mode != "agent-forward" {
		t.Errorf("SSH.Mode = %q, attendu le défaut agent-forward", g.SSH.Mode)
	}
	if g.WorktreeLayout != "central" {
		t.Errorf("WorktreeLayout = %q, attendu le défaut central", g.WorktreeLayout)
	}
	// Le défaut est relatif AU den home courant, pas littéralement ~/.den/worktrees :
	// sur un DEN_HOME temporaire, les worktrees doivent rester dans ce home-là.
	if want := filepath.Join(denHome, "worktrees"); g.WorktreeRoot != want {
		t.Errorf("WorktreeRoot = %q, attendu le défaut %q", g.WorktreeRoot, want)
	}
}

func TestLoadGlobalFichierAbsent(t *testing.T) {
	denHome := t.TempDir()
	_, err := LoadGlobal(denHome)
	if err == nil {
		t.Fatal("attendu une erreur quand config.yaml est absent")
	}
	// Le message doit être actionnable : il nomme le chemin cherché.
	if !strings.Contains(err.Error(), filepath.Join(denHome, "config.yaml")) {
		t.Errorf("erreur = %q, attendu le chemin complet du fichier manquant", err.Error())
	}
}

func TestLoadGlobalYamlInvalide(t *testing.T) {
	if _, err := LoadGlobal(ecrisConfig(t, "agents: [ceci n'est pas une map")); err == nil {
		t.Fatal("attendu une erreur sur YAML invalide")
	}
}
```

- [ ] **Step 2: Lancer le test et vérifier qu'il échoue**

Run: `go test ./internal/config/ -run TestLoadGlobal -v`
Expected: FAIL — `undefined: LoadGlobal`.

- [ ] **Step 3: Implémenter**

`internal/config/config.go` :

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Agent décrit une entrée du registre d'agents (spec §4.1 et §9).
type Agent struct {
	ConfigDir string            `yaml:"config_dir"`
	Env       map[string]string `yaml:"env"`
	// BinDirs : chemins IN-VM ajoutés au PATH avant la commande de fraîcheur (spec §9.1).
	// Jamais expansés côté hôte.
	BinDirs []string `yaml:"bin_dirs"`
	// Update : commande de mise à jour de l'agent, jouée au boot de la VM.
	Update string `yaml:"update"`
}

// Defaults porte les choix par défaut quand ni le nest ni les flags ne tranchent.
type Defaults struct {
	Agent string `yaml:"agent"`
	Stack string `yaml:"stack"`
}

// SSH décrit le mode d'accès SSH dans la VM (spec §10).
type SSH struct {
	Mode string `yaml:"mode"` // agent-forward | mount | none
	Dir  string `yaml:"dir"`  // utilisé si mode=mount
}

// Global est le contenu de ~/.den/config.yaml, défauts appliqués et chemins expansés.
type Global struct {
	Agents         map[string]Agent `yaml:"agents"`
	Defaults       Defaults         `yaml:"defaults"`
	SSH            SSH              `yaml:"ssh"`
	WorktreeLayout string           `yaml:"worktree_layout"`
	WorktreeRoot   string           `yaml:"worktree_root"`
	Egress         []string         `yaml:"egress"`
}

// LoadGlobal lit <denHome>/config.yaml, applique les défauts et expanse les chemins hôte.
func LoadGlobal(denHome string) (*Global, error) {
	chemin := filepath.Join(denHome, "config.yaml")
	brut, err := os.ReadFile(chemin)
	if err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", chemin, err)
	}

	var g Global
	if err := yaml.Unmarshal(brut, &g); err != nil {
		return nil, fmt.Errorf("%s : YAML invalide : %w", chemin, err)
	}

	if g.SSH.Mode == "" {
		g.SSH.Mode = "agent-forward"
	}
	if g.WorktreeLayout == "" {
		g.WorktreeLayout = "central"
	}
	if g.WorktreeRoot == "" {
		g.WorktreeRoot = filepath.Join(denHome, "worktrees")
	}

	if g.WorktreeRoot, err = ExpandPath(g.WorktreeRoot); err != nil {
		return nil, err
	}
	if g.SSH.Dir, err = ExpandPath(g.SSH.Dir); err != nil {
		return nil, err
	}
	for nom, a := range g.Agents {
		if a.ConfigDir, err = ExpandPath(a.ConfigDir); err != nil {
			return nil, fmt.Errorf("agent %s : %w", nom, err)
		}
		g.Agents[nom] = a // les valeurs de map ne sont pas adressables
	}
	return &g, nil
}
```

- [ ] **Step 4: Lancer les tests et vérifier qu'ils passent**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(config): chargement de config.yaml avec defauts et expansion"
```

---

## Task 4: Validation de la configuration globale

**Files:**
- Create: `internal/config/validate.go`
- Test: `internal/config/validate_test.go`

**Interfaces:**
- Consumes: `config.Global` (Task 3).
- Produces: `(*Global).Validate() []error` — renvoie **toutes** les erreurs, pas la première.
  `den doctor` (Task 11) affiche cette liste telle quelle.

- [ ] **Step 1: Écrire le test en échec**

`internal/config/validate_test.go` :

```go
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
```

- [ ] **Step 2: Lancer le test et vérifier qu'il échoue**

Run: `go test ./internal/config/ -run TestValidate -v`
Expected: FAIL — `g.Validate undefined`.

- [ ] **Step 3: Implémenter**

`internal/config/validate.go` :

```go
package config

import (
	"fmt"
	"sort"
)

var (
	modesSSH        = []string{"agent-forward", "mount", "none"}
	layoutsWorktree = []string{"central", "per-repo"}
)

// Validate contrôle la cohérence interne de config.yaml et renvoie TOUTES les
// erreurs trouvées. Cumuler plutôt que s'arrêter à la première : `den doctor`
// doit montrer d'un coup tout ce qu'il y a à réparer.
func (g *Global) Validate() []error {
	var errs []error

	if len(g.Agents) == 0 {
		errs = append(errs, fmt.Errorf("agents : le registre est vide, déclare au moins un agent"))
	}

	noms := make([]string, 0, len(g.Agents))
	for nom := range g.Agents {
		noms = append(noms, nom)
	}
	sort.Strings(noms) // déterminisme de l'ordre des erreurs

	for _, nom := range noms {
		a := g.Agents[nom]
		if a.ConfigDir == "" {
			errs = append(errs, fmt.Errorf("agents.%s.config_dir : requis", nom))
		}
		if a.Update == "" {
			errs = append(errs, fmt.Errorf(
				"agents.%s.update : requis — une sandbox ne doit jamais démarrer avec un agent périmé (spec §9.1)", nom))
		}
	}

	switch {
	case g.Defaults.Agent == "":
		errs = append(errs, fmt.Errorf("defaults.agent : requis"))
	default:
		if _, ok := g.Agents[g.Defaults.Agent]; !ok {
			errs = append(errs, fmt.Errorf(
				"defaults.agent : %q est absent du registre (agents déclarés : %v)", g.Defaults.Agent, noms))
		}
	}

	if g.Defaults.Stack == "" {
		errs = append(errs, fmt.Errorf("defaults.stack : requis"))
	}

	if !contient(modesSSH, g.SSH.Mode) {
		errs = append(errs, fmt.Errorf("ssh.mode : %q inconnu (attendu : %v)", g.SSH.Mode, modesSSH))
	}
	if g.SSH.Mode == "mount" && g.SSH.Dir == "" {
		errs = append(errs, fmt.Errorf("ssh.dir : requis quand ssh.mode vaut mount"))
	}

	if !contient(layoutsWorktree, g.WorktreeLayout) {
		errs = append(errs, fmt.Errorf(
			"worktree_layout : %q inconnu (attendu : %v)", g.WorktreeLayout, layoutsWorktree))
	}

	return errs
}

func contient(liste []string, v string) bool {
	for _, e := range liste {
		if e == v {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Lancer les tests et vérifier qu'ils passent**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(config): validation cumulative de config.yaml"
```

---

## Task 5: Chargement des stacks

**Files:**
- Create: `internal/config/stack.go`
- Test: `internal/config/stack_test.go`

**Interfaces:**
- Consumes: `config.Home` (Task 2).
- Produces:
  - Type `config.Stack{Name, Image, Parent, Kit, Egress, Dir}` — `Dir` est le dossier de la stack,
    rempli au chargement (non lu depuis le YAML), `Kit` est **résolu en chemin absolu**.
  - `config.LoadStack(denHome, name string) (*Stack, error)`
  - `config.LoadStacks(denHome string) (map[string]*Stack, error)` — toutes les stacks présentes.

- [ ] **Step 1: Écrire le test en échec**

`internal/config/stack_test.go` :

```go
package config

import (
	"os"
	"path/filepath"
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
name: dgdevx
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

func TestLoadStackNomDeduitDuDossier(t *testing.T) {
	denHome := t.TempDir()
	// `name` absent du YAML : le nom du dossier fait foi.
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	s, err := LoadStack(denHome, "devx")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if s.Name != "devx" {
		t.Errorf("Name = %q, attendu %q déduit du dossier", s.Name, "devx")
	}
}

func TestLoadStackAbsente(t *testing.T) {
	if _, err := LoadStack(t.TempDir(), "fantome"); err == nil {
		t.Fatal("attendu une erreur pour une stack absente")
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
	if len(stacks) != 2 {
		t.Fatalf("attendu 2 stacks, obtenu %d : %v", len(stacks), stacks)
	}
	if stacks["dgdevx"].Parent != "devx" {
		t.Errorf("parent de dgdevx = %q", stacks["dgdevx"].Parent)
	}
}

func TestLoadStacksDossierAbsent(t *testing.T) {
	// Pas de dossier stacks/ : ce n'est pas une erreur, c'est un den vide.
	stacks, err := LoadStacks(t.TempDir())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(stacks) != 0 {
		t.Errorf("attendu 0 stack, obtenu %d", len(stacks))
	}
}
```

- [ ] **Step 2: Lancer le test et vérifier qu'il échoue**

Run: `go test ./internal/config/ -run TestLoadStack -v`
Expected: FAIL — `undefined: LoadStack`.

- [ ] **Step 3: Implémenter**

`internal/config/stack.go` :

```go
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Stack est une recette d'image buildable (spec §4.2).
type Stack struct {
	Name   string   `yaml:"name"`
	Image  string   `yaml:"image"`
	Parent string   `yaml:"parent"` // arête du DAG de build
	Kit    string   `yaml:"kit"`    // relatif au dossier de la stack dans le YAML, absolu après chargement
	Egress []string `yaml:"egress"`

	Dir string `yaml:"-"` // dossier de la stack, rempli au chargement
}

// LoadStack lit <denHome>/stacks/<name>/stack.yaml.
func LoadStack(denHome, name string) (*Stack, error) {
	dir := filepath.Join(denHome, "stacks", name)
	chemin := filepath.Join(dir, "stack.yaml")

	brut, err := os.ReadFile(chemin)
	if err != nil {
		return nil, fmt.Errorf("stack %q : lecture de %s : %w", name, chemin, err)
	}

	var s Stack
	if err := yaml.Unmarshal(brut, &s); err != nil {
		return nil, fmt.Errorf("%s : YAML invalide : %w", chemin, err)
	}

	if s.Name == "" {
		s.Name = name // le dossier fait foi
	}
	s.Dir = dir
	if s.Kit != "" && !filepath.IsAbs(s.Kit) {
		s.Kit = filepath.Join(dir, s.Kit)
	}
	return &s, nil
}

// LoadStacks charge toutes les stacks déclarées. Un dossier sans stack.yaml est
// ignoré (brouillon), un dossier stacks/ absent donne une map vide : un den
// fraîchement créé n'est pas une erreur.
func LoadStacks(denHome string) (map[string]*Stack, error) {
	racine := filepath.Join(denHome, "stacks")
	entrees, err := os.ReadDir(racine)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]*Stack{}, nil
		}
		return nil, fmt.Errorf("lecture de %s : %w", racine, err)
	}

	stacks := make(map[string]*Stack)
	for _, e := range entrees {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(racine, e.Name(), "stack.yaml")); err != nil {
			continue
		}
		s, err := LoadStack(denHome, e.Name())
		if err != nil {
			return nil, err
		}
		stacks[s.Name] = s
	}
	return stacks, nil
}
```

- [ ] **Step 4: Lancer les tests et vérifier qu'ils passent**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(config): chargement des stacks et de leur DAG declare"
```

---

## Task 6: Chargement des nests

**Files:**
- Create: `internal/nest/nest.go`
- Test: `internal/nest/nest_test.go`

**Interfaces:**
- Consumes: `config.ExpandPath` (Task 2).
- Produces:
  - Types `nest.Repo{Path, Optional}` (+ méthode `Name() string`), `nest.PortDecl{Name, Container, Open, LoopbackLock}`,
    `nest.Ports{Base, Publish}`, `nest.Nest{Name, Stack, Env, Egress, Repos, Ports, Agents}`.
  - `nest.LoadNest(denHome, name string) (*Nest, error)`
  - `nest.ListNests(denHome string) ([]*Nest, error)` — **trié par nom**.

- [ ] **Step 1: Écrire le test en échec**

`internal/nest/nest_test.go` :

```go
package nest

import (
	"os"
	"path/filepath"
	"testing"
)

func ecrisNest(t *testing.T, denHome, nom, contenu string) {
	t.Helper()
	dir := filepath.Join(denHome, "nests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, nom+".yaml"), []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
}

const nestComplet = `
name: fullstack
stack: dgdevx
env:
  SOME_VAR: value
egress:
  - 10.22.11.54:27017
repos:
  - { path: ~/dev/review-mgmt }
  - { path: ~/dev/front-app, optional: true }
ports:
  base: 9100
  publish:
    - { name: vite, container: 5173, open: true }
    - { name: cdp, container: 9223, loopback_lock: true }
agents:
  claude: ~/.den/agents/claude-fullstack
`

func TestLoadNest(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	denHome := t.TempDir()
	ecrisNest(t, denHome, "fullstack", nestComplet)

	n, err := LoadNest(denHome, "fullstack")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if n.Name != "fullstack" || n.Stack != "dgdevx" {
		t.Errorf("nest = %+v", n)
	}
	if n.Env["SOME_VAR"] != "value" {
		t.Errorf("Env = %v", n.Env)
	}
	if len(n.Repos) != 2 {
		t.Fatalf("attendu 2 repos, obtenu %d", len(n.Repos))
	}
	if want := filepath.Join(home, "dev/review-mgmt"); n.Repos[0].Path != want {
		t.Errorf("Repos[0].Path = %q, attendu %q (tilde expansé)", n.Repos[0].Path, want)
	}
	if n.Repos[0].Optional {
		t.Error("Repos[0] doit être requis")
	}
	if !n.Repos[1].Optional {
		t.Error("Repos[1] doit être optionnel")
	}
	if got := n.Repos[0].Name(); got != "review-mgmt" {
		t.Errorf("Name() = %q, attendu %q", got, "review-mgmt")
	}
	if n.Ports.Base != 9100 || len(n.Ports.Publish) != 2 {
		t.Errorf("Ports = %+v", n.Ports)
	}
	if !n.Ports.Publish[1].LoopbackLock {
		t.Error("le port cdp doit être loopback_lock")
	}
	if want := filepath.Join(home, ".den/agents/claude-fullstack"); n.Agents["claude"] != want {
		t.Errorf("Agents[claude] = %q, attendu %q", n.Agents["claude"], want)
	}
}

func TestLoadNestNomDeduitDuFichier(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "review", "stack: devx\n")
	n, err := LoadNest(denHome, "review")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if n.Name != "review" {
		t.Errorf("Name = %q, attendu %q déduit du fichier", n.Name, "review")
	}
}

func TestLoadNestAbsent(t *testing.T) {
	if _, err := LoadNest(t.TempDir(), "fantome"); err == nil {
		t.Fatal("attendu une erreur pour un nest absent")
	}
}

func TestListNestsTriParNom(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "web", "stack: devx\n")
	ecrisNest(t, denHome, "api", "stack: devx\n")
	ecrisNest(t, denHome, "review", "stack: devx\n")
	// un fichier non-YAML ne doit pas être ramassé
	if err := os.WriteFile(filepath.Join(denHome, "nests", "NOTES.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	nests, err := ListNests(denHome)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	var noms []string
	for _, n := range nests {
		noms = append(noms, n.Name)
	}
	attendu := []string{"api", "review", "web"}
	if len(noms) != 3 || noms[0] != attendu[0] || noms[1] != attendu[1] || noms[2] != attendu[2] {
		t.Errorf("noms = %v, attendu %v (trié)", noms, attendu)
	}
}

func TestListNestsDossierAbsent(t *testing.T) {
	nests, err := ListNests(t.TempDir())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(nests) != 0 {
		t.Errorf("attendu 0 nest, obtenu %d", len(nests))
	}
}
```

- [ ] **Step 2: Lancer le test et vérifier qu'il échoue**

Run: `go test ./internal/nest/ -v`
Expected: FAIL — `undefined: LoadNest`.

- [ ] **Step 3: Implémenter**

`internal/nest/nest.go` :

```go
// Package nest charge les nests (objets spawnables) et calcule les dérivations
// pures qui en découlent : sélection de repos, union d'egress, résolution d'agent.
package nest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PillowPillow/den/internal/config"
	"gopkg.in/yaml.v3"
)

// Repo est un dépôt co-monté dans la sandbox.
type Repo struct {
	Path     string `yaml:"path"`
	Optional bool   `yaml:"optional"`
}

// Name est le nom court du repo (basename), utilisé par --without/--only.
func (r Repo) Name() string { return filepath.Base(r.Path) }

// PortDecl est un port déclaré par le nest (spec §8).
type PortDecl struct {
	Name         string `yaml:"name"`
	Container    int    `yaml:"container"`
	Open         bool   `yaml:"open"`
	LoopbackLock bool   `yaml:"loopback_lock"`
}

// Ports porte la fenêtre déclarée. Base == 0 => dérivée du hash du nom (plan Ports).
type Ports struct {
	Base    int        `yaml:"base"`
	Publish []PortDecl `yaml:"publish"`
}

// Nest est un objet spawnable (spec §4.3).
type Nest struct {
	Name   string            `yaml:"name"`
	Stack  string            `yaml:"stack"`
	Env    map[string]string `yaml:"env"`
	Egress []string          `yaml:"egress"`
	Repos  []Repo            `yaml:"repos"`
	Ports  Ports             `yaml:"ports"`
	Agents map[string]string `yaml:"agents"` // override du config_dir par agent
}

// LoadNest lit <denHome>/nests/<name>.yaml.
func LoadNest(denHome, name string) (*Nest, error) {
	chemin := filepath.Join(denHome, "nests", name+".yaml")

	brut, err := os.ReadFile(chemin)
	if err != nil {
		return nil, fmt.Errorf("nest %q : lecture de %s : %w", name, chemin, err)
	}

	var n Nest
	if err := yaml.Unmarshal(brut, &n); err != nil {
		return nil, fmt.Errorf("%s : YAML invalide : %w", chemin, err)
	}

	if n.Name == "" {
		n.Name = name // le nom de fichier fait foi
	}
	for i, r := range n.Repos {
		if n.Repos[i].Path, err = config.ExpandPath(r.Path); err != nil {
			return nil, fmt.Errorf("nest %q, repo %q : %w", n.Name, r.Path, err)
		}
	}
	for agent, dir := range n.Agents {
		expanse, err := config.ExpandPath(dir)
		if err != nil {
			return nil, fmt.Errorf("nest %q, agent %q : %w", n.Name, agent, err)
		}
		n.Agents[agent] = expanse
	}
	return &n, nil
}

// ListNests charge tous les nests déclarés, triés par nom. Dossier absent = liste vide.
func ListNests(denHome string) ([]*Nest, error) {
	racine := filepath.Join(denHome, "nests")
	entrees, err := os.ReadDir(racine)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("lecture de %s : %w", racine, err)
	}

	var noms []string
	for _, e := range entrees {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		noms = append(noms, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	sort.Strings(noms)

	nests := make([]*Nest, 0, len(noms))
	for _, nom := range noms {
		n, err := LoadNest(denHome, nom)
		if err != nil {
			return nil, err
		}
		nests = append(nests, n)
	}
	return nests, nil
}
```

- [ ] **Step 4: Lancer les tests et vérifier qu'ils passent**

Run: `go test ./internal/nest/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nest
git commit -m "feat(nest): chargement et listage des nests"
```

---

## Task 7: Sélection des repos (`--without` / `--only`)

**Files:**
- Create: `internal/nest/repos.go`
- Test: `internal/nest/repos_test.go`

**Interfaces:**
- Consumes: `nest.Repo` (Task 6).
- Produces: `nest.SelectRepos(repos []Repo, without, only []string) ([]Repo, error)` — renvoie les
  repos retenus dans **l'ordre de déclaration** (l'ordre compte : il fixe l'ordre des positionnels
  `sbx create` au plan Spawn).

**Règles** (spec §4.3 / §6.2) : les repos requis sont **toujours** inclus ; seuls les optionnels se
filtrent. `--without` et `--only` sont mutuellement exclusifs. Un nom inconnu est une erreur
actionnable, jamais un silence.

- [ ] **Step 1: Écrire le test en échec**

`internal/nest/repos_test.go` :

```go
package nest

import (
	"strings"
	"testing"
)

func repos() []Repo {
	return []Repo{
		{Path: "/dev/api"},                     // requis
		{Path: "/dev/front", Optional: true},   // optionnel
		{Path: "/dev/worker", Optional: true},  // optionnel
	}
}

func noms(rs []Repo) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Name())
	}
	return out
}

func egal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSelectReposCasNominaux(t *testing.T) {
	cas := []struct {
		nom     string
		without []string
		only    []string
		attendu []string
	}{
		{"sans filtre : tout", nil, nil, []string{"api", "front", "worker"}},
		{"without un optionnel", []string{"front"}, nil, []string{"api", "worker"}},
		{"without plusieurs", []string{"front", "worker"}, nil, []string{"api"}},
		{"only un optionnel : les requis restent", nil, []string{"front"}, []string{"api", "front"}},
		{"only un requis : les optionnels tombent", nil, []string{"api"}, []string{"api"}},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			got, err := SelectRepos(repos(), c.without, c.only)
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			if !egal(noms(got), c.attendu) {
				t.Errorf("SelectRepos = %v, attendu %v", noms(got), c.attendu)
			}
		})
	}
}

func TestSelectReposErreurs(t *testing.T) {
	cas := []struct {
		nom     string
		without []string
		only    []string
		attendu string
	}{
		{"without et only ensemble", []string{"front"}, []string{"worker"}, "mutuellement exclusifs"},
		{"without un repo requis", []string{"api"}, nil, "requis"},
		{"without un repo inconnu", []string{"fantome"}, nil, "fantome"},
		{"only un repo inconnu", nil, []string{"fantome"}, "fantome"},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			_, err := SelectRepos(repos(), c.without, c.only)
			if err == nil {
				t.Fatalf("attendu une erreur mentionnant %q", c.attendu)
			}
			if !strings.Contains(err.Error(), c.attendu) {
				t.Errorf("erreur = %q, attendu une mention de %q", err.Error(), c.attendu)
			}
		})
	}
}

func TestSelectReposNeMutePasLEntree(t *testing.T) {
	in := repos()
	if _, err := SelectRepos(in, []string{"front"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(in) != 3 {
		t.Errorf("l'entrée a été mutée : %d repos au lieu de 3", len(in))
	}
}
```

- [ ] **Step 2: Lancer le test et vérifier qu'il échoue**

Run: `go test ./internal/nest/ -run TestSelectRepos -v`
Expected: FAIL — `undefined: SelectRepos`.

- [ ] **Step 3: Implémenter**

`internal/nest/repos.go` :

```go
package nest

import (
	"fmt"
	"sort"
	"strings"
)

// SelectRepos applique --without / --only à la liste déclarée par le nest.
// Les repos requis sont toujours retenus : seuls les optionnels se filtrent.
// L'ordre de déclaration est préservé — il fixe l'ordre des positionnels `sbx create`.
func SelectRepos(repos []Repo, without, only []string) ([]Repo, error) {
	if len(without) > 0 && len(only) > 0 {
		return nil, fmt.Errorf("--without et --only sont mutuellement exclusifs")
	}

	connus := make(map[string]Repo, len(repos))
	for _, r := range repos {
		connus[r.Name()] = r
	}

	verifie := func(flag string, valeurs []string) error {
		for _, v := range valeurs {
			if _, ok := connus[v]; !ok {
				return fmt.Errorf("%s : repo %q inconnu dans ce nest (disponibles : %s)",
					flag, v, strings.Join(nomsTries(repos), ", "))
			}
		}
		return nil
	}
	if err := verifie("--without", without); err != nil {
		return nil, err
	}
	if err := verifie("--only", only); err != nil {
		return nil, err
	}

	exclus := make(map[string]bool, len(without))
	for _, v := range without {
		if !connus[v].Optional {
			return nil, fmt.Errorf("--without : %q est un repo requis de ce nest, il ne peut pas être retiré", v)
		}
		exclus[v] = true
	}

	garde := make(map[string]bool, len(only))
	for _, v := range only {
		garde[v] = true
	}

	out := make([]Repo, 0, len(repos))
	for _, r := range repos {
		switch {
		case !r.Optional: // requis : toujours
		case exclus[r.Name()]:
			continue
		case len(only) > 0 && !garde[r.Name()]:
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func nomsTries(repos []Repo) []string {
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		out = append(out, r.Name())
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Lancer les tests et vérifier qu'ils passent**

Run: `go test ./internal/nest/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nest
git commit -m "feat(nest): selection des repos via --without/--only"
```

---

## Task 8: Union des egress (cascade global ← stack ← nest)

**Files:**
- Create: `internal/nest/egress.go`
- Test: `internal/nest/egress_test.go`

**Interfaces:**
- Consumes: rien.
- Produces: `nest.UnionEgress(listes ...[]string) []string` — union dédupliquée **triée**.

Le tri n'est pas cosmétique : cette liste alimente le `network.allow` du mixin généré, dont l'argv
`sbx create` est assertée en golden file au plan Spawn. Un ordre instable = un golden file qui
clignote.

- [ ] **Step 1: Écrire le test en échec**

`internal/nest/egress_test.go` :

```go
package nest

import "testing"

func TestUnionEgress(t *testing.T) {
	cas := []struct {
		nom     string
		listes  [][]string
		attendu []string
	}{
		{"vide", nil, []string{}},
		{"une seule liste triée", [][]string{{"b.com", "a.com"}}, []string{"a.com", "b.com"}},
		{
			"cascade global stack nest",
			[][]string{{"api.anthropic.com", "github.com"}, {"gitlab.digitaleo.com"}, {"10.22.11.54:27017"}},
			[]string{"10.22.11.54:27017", "api.anthropic.com", "github.com", "gitlab.digitaleo.com"},
		},
		{
			"doublons entre niveaux dedupliques",
			[][]string{{"github.com"}, {"github.com"}, {"github.com", "a.com"}},
			[]string{"a.com", "github.com"},
		},
		{"listes vides ignorees", [][]string{nil, {"a.com"}, {}}, []string{"a.com"}},
		{"chaines vides ignorees", [][]string{{"", "a.com", ""}}, []string{"a.com"}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			got := UnionEgress(c.listes...)
			if len(got) != len(c.attendu) {
				t.Fatalf("UnionEgress = %v, attendu %v", got, c.attendu)
			}
			for i := range got {
				if got[i] != c.attendu[i] {
					t.Fatalf("UnionEgress = %v, attendu %v", got, c.attendu)
				}
			}
		})
	}
}

func TestUnionEgressRenvoieToujoursUneSliceNonNil(t *testing.T) {
	// Le rendu YAML du mixin distingue `allow: []` de `allow: null`.
	if got := UnionEgress(); got == nil {
		t.Error("attendu une slice vide non-nil")
	}
}
```

- [ ] **Step 2: Lancer le test et vérifier qu'il échoue**

Run: `go test ./internal/nest/ -run TestUnionEgress -v`
Expected: FAIL — `undefined: UnionEgress`.

- [ ] **Step 3: Implémenter**

`internal/nest/egress.go` :

```go
package nest

import "sort"

// UnionEgress fusionne les allowlists de la cascade (baseline ∪ stack ∪ nest),
// déduplique et TRIE. Le tri est une exigence de déterminisme : cette liste
// devient le network.allow du mixin généré, asserté en golden file.
func UnionEgress(listes ...[]string) []string {
	vu := make(map[string]bool)
	out := make([]string, 0)
	for _, liste := range listes {
		for _, h := range liste {
			if h == "" || vu[h] {
				continue
			}
			vu[h] = true
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Lancer les tests et vérifier qu'ils passent**

Run: `go test ./internal/nest/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nest
git commit -m "feat(nest): union dedupliquee et triee des egress"
```

---

## Task 9: Résolution complète d'un nest

**Files:**
- Create: `internal/nest/resolve.go`
- Test: `internal/nest/resolve_test.go`

**Interfaces:**
- Consumes: `config.Global`, `config.Stack` (Tasks 3 & 5) ; `nest.Nest`, `SelectRepos`, `UnionEgress`
  (Tasks 6-8).
- Produces:
  - `nest.Options{Agent, Without, Only []string / string}` — surcharges venues des flags CLI.
  - `nest.Resolved{Nest, Stack, AgentName, Agent, AgentConfigDir, Egress, Repos, SSHMode, SSHDir,
    WorktreeLayout, WorktreeRoot}`.
  - `nest.ResolveAgent(g *config.Global, n *Nest, flagAgent string) (string, config.Agent, string, error)`
    → `(nom, définition, configDir résolu, erreur)`.
  - `nest.Resolve(g *config.Global, stacks map[string]*config.Stack, n *Nest, o Options) (*Resolved, error)`.

C'est le point de convergence de la cascade `global ← stack ← nest ← flags` (spec §4). Le plan Spawn
consommera `Resolved` tel quel pour fabriquer le mixin et l'argv `sbx create` — d'où l'importance
que rien n'y soit laissé « à recalculer plus tard ».

- [ ] **Step 1: Écrire le test en échec**

`internal/nest/resolve_test.go` :

```go
package nest

import (
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

func globalTest() *config.Global {
	return &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/home/moi/.den/agents/claude",
				Env:       map[string]string{"CLAUDE_CONFIG_DIR": "{config_dir}"},
				BinDirs:   []string{"$HOME/.local/bin"},
				Update:    "claude update",
			},
			"codex": {ConfigDir: "/home/moi/.den/agents/codex", Update: "codex --upgrade"},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
		WorktreeRoot:   "/home/moi/.den/worktrees",
		Egress:         []string{"api.anthropic.com", "github.com"},
	}
}

func stacksTest() map[string]*config.Stack {
	return map[string]*config.Stack{
		"devx":   {Name: "devx", Image: "devx:v1", Kit: "/den/stacks/devx/kit"},
		"dgdevx": {Name: "dgdevx", Image: "dgdevx:v1", Parent: "devx", Kit: "/den/stacks/dgdevx/kit", Egress: []string{"gitlab.digitaleo.com"}},
	}
}

func nestTest() *Nest {
	return &Nest{
		Name:   "fullstack",
		Stack:  "dgdevx",
		Egress: []string{"10.22.11.54:27017"},
		Repos:  []Repo{{Path: "/dev/api"}, {Path: "/dev/front", Optional: true}},
	}
}

func TestResolveAgentParDefaut(t *testing.T) {
	nom, a, dir, err := ResolveAgent(globalTest(), nestTest(), "")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if nom != "claude" {
		t.Errorf("nom = %q, attendu claude (defaults.agent)", nom)
	}
	if a.Update != "claude update" {
		t.Errorf("Update = %q", a.Update)
	}
	if dir != "/home/moi/.den/agents/claude" {
		t.Errorf("configDir = %q, attendu celui du registre global", dir)
	}
}

func TestResolveAgentFlagSurcharge(t *testing.T) {
	nom, _, dir, err := ResolveAgent(globalTest(), nestTest(), "codex")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if nom != "codex" || dir != "/home/moi/.den/agents/codex" {
		t.Errorf("nom = %q, dir = %q", nom, dir)
	}
}

func TestResolveAgentOverrideParNest(t *testing.T) {
	n := nestTest()
	n.Agents = map[string]string{"claude": "/perso/claude-fullstack"}
	_, _, dir, err := ResolveAgent(globalTest(), n, "")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if dir != "/perso/claude-fullstack" {
		t.Errorf("configDir = %q, attendu l'override du nest", dir)
	}
}

func TestResolveAgentOverrideNestCibleLeBonAgent(t *testing.T) {
	// Le nest surcharge codex ; l'agent actif est claude => l'override ne s'applique pas.
	n := nestTest()
	n.Agents = map[string]string{"codex": "/perso/codex"}
	_, _, dir, err := ResolveAgent(globalTest(), n, "")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if dir != "/home/moi/.den/agents/claude" {
		t.Errorf("configDir = %q, l'override codex n'aurait pas dû s'appliquer à claude", dir)
	}
}

func TestResolveAgentInconnu(t *testing.T) {
	_, _, _, err := ResolveAgent(globalTest(), nestTest(), "gemini")
	if err == nil {
		t.Fatal("attendu une erreur pour un agent inconnu")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Errorf("erreur = %q, attendu la liste des agents disponibles", err.Error())
	}
}

func TestResolveCascadeComplete(t *testing.T) {
	r, err := Resolve(globalTest(), stacksTest(), nestTest(), Options{})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if r.Stack.Image != "dgdevx:v1" {
		t.Errorf("Stack.Image = %q", r.Stack.Image)
	}
	if r.AgentName != "claude" {
		t.Errorf("AgentName = %q", r.AgentName)
	}
	attendu := []string{"10.22.11.54:27017", "api.anthropic.com", "github.com", "gitlab.digitaleo.com"}
	if len(r.Egress) != len(attendu) {
		t.Fatalf("Egress = %v, attendu %v", r.Egress, attendu)
	}
	for i := range attendu {
		if r.Egress[i] != attendu[i] {
			t.Fatalf("Egress = %v, attendu %v", r.Egress, attendu)
		}
	}
	if len(r.Repos) != 2 {
		t.Errorf("Repos = %v, attendu les 2 repos", noms(r.Repos))
	}
	if r.SSHMode != "agent-forward" || r.WorktreeLayout != "central" {
		t.Errorf("SSH/worktree non hérités du global : %+v", r)
	}
}

func TestResolveAppliqueLaSelectionDeRepos(t *testing.T) {
	r, err := Resolve(globalTest(), stacksTest(), nestTest(), Options{Without: []string{"front"}})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if got := noms(r.Repos); len(got) != 1 || got[0] != "api" {
		t.Errorf("Repos = %v, attendu [api]", got)
	}
}

func TestResolveStackDuNestParDefautSiAbsente(t *testing.T) {
	n := nestTest()
	n.Stack = "" // le nest ne tranche pas => defaults.stack
	r, err := Resolve(globalTest(), stacksTest(), n, Options{})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if r.Stack.Name != "devx" {
		t.Errorf("Stack.Name = %q, attendu le défaut devx", r.Stack.Name)
	}
}

func TestResolveStackInconnue(t *testing.T) {
	n := nestTest()
	n.Stack = "fantome"
	_, err := Resolve(globalTest(), stacksTest(), n, Options{})
	if err == nil {
		t.Fatal("attendu une erreur pour une stack inconnue")
	}
	if !strings.Contains(err.Error(), "fantome") {
		t.Errorf("erreur = %q, attendu une mention de la stack manquante", err.Error())
	}
}
```

- [ ] **Step 2: Lancer le test et vérifier qu'il échoue**

Run: `go test ./internal/nest/ -run TestResolve -v`
Expected: FAIL — `undefined: ResolveAgent`, `undefined: Resolve`, `undefined: Options`.

- [ ] **Step 3: Implémenter**

`internal/nest/resolve.go` :

```go
package nest

import (
	"fmt"
	"sort"

	"github.com/PillowPillow/den/internal/config"
)

// Options porte les surcharges issues des flags CLI (dernier niveau de la cascade).
type Options struct {
	Agent   string   // --agent
	Without []string // --without
	Only    []string // --only
}

// Resolved est un nest entièrement résolu : plus rien à recalculer en aval.
// Le plan Spawn le consomme tel quel pour fabriquer le mixin et l'argv sbx create.
type Resolved struct {
	Nest  *Nest
	Stack *config.Stack

	AgentName      string
	Agent          config.Agent
	AgentConfigDir string // override nest s'il existe, sinon registre global

	Egress []string // union triée baseline ∪ stack ∪ nest
	Repos  []Repo   // sélection appliquée, ordre de déclaration

	SSHMode        string
	SSHDir         string
	WorktreeLayout string
	WorktreeRoot   string
}

// ResolveAgent détermine l'agent actif et son config_dir.
// Priorité du nom : flag --agent > defaults.agent.
// Priorité du config_dir : override du nest pour CET agent > registre global.
func ResolveAgent(g *config.Global, n *Nest, flagAgent string) (string, config.Agent, string, error) {
	nom := flagAgent
	if nom == "" {
		nom = g.Defaults.Agent
	}

	a, ok := g.Agents[nom]
	if !ok {
		dispos := make([]string, 0, len(g.Agents))
		for k := range g.Agents {
			dispos = append(dispos, k)
		}
		sort.Strings(dispos)
		return "", config.Agent{}, "", fmt.Errorf(
			"agent %q inconnu (agents déclarés : %v)", nom, dispos)
	}

	configDir := a.ConfigDir
	if n != nil {
		if override, ok := n.Agents[nom]; ok && override != "" {
			configDir = override
		}
	}
	return nom, a, configDir, nil
}

// Resolve applique la cascade complète global ← stack ← nest ← flags.
func Resolve(g *config.Global, stacks map[string]*config.Stack, n *Nest, o Options) (*Resolved, error) {
	nomStack := n.Stack
	if nomStack == "" {
		nomStack = g.Defaults.Stack
	}
	s, ok := stacks[nomStack]
	if !ok {
		dispos := make([]string, 0, len(stacks))
		for k := range stacks {
			dispos = append(dispos, k)
		}
		sort.Strings(dispos)
		return nil, fmt.Errorf(
			"nest %q : stack %q introuvable dans ~/.den/stacks (stacks déclarées : %v)",
			n.Name, nomStack, dispos)
	}

	nomAgent, agent, configDir, err := ResolveAgent(g, n, o.Agent)
	if err != nil {
		return nil, fmt.Errorf("nest %q : %w", n.Name, err)
	}

	repos, err := SelectRepos(n.Repos, o.Without, o.Only)
	if err != nil {
		return nil, fmt.Errorf("nest %q : %w", n.Name, err)
	}

	return &Resolved{
		Nest:           n,
		Stack:          s,
		AgentName:      nomAgent,
		Agent:          agent,
		AgentConfigDir: configDir,
		Egress:         UnionEgress(g.Egress, s.Egress, n.Egress),
		Repos:          repos,
		SSHMode:        g.SSH.Mode,
		SSHDir:         g.SSH.Dir,
		WorktreeLayout: g.WorktreeLayout,
		WorktreeRoot:   g.WorktreeRoot,
	}, nil
}
```

- [ ] **Step 4: Lancer les tests et vérifier qu'ils passent**

Run: `go test ./internal/nest/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nest
git commit -m "feat(nest): resolution en cascade global<-stack<-nest<-flags"
```

---

## Task 10: Commandes `den nest ls` et `den nest show`

**Files:**
- Create: `internal/cli/nest.go`
- Modify: `internal/cli/root.go` (enregistrer la sous-commande)
- Test: `internal/cli/nest_test.go`

**Interfaces:**
- Consumes: `config.Home`, `config.LoadGlobal`, `config.LoadStacks`, `nest.ListNests`, `nest.LoadNest`,
  `nest.Resolve`, `nest.Options`, `nest.Resolved`.
- Produces: `cli.newNestCmd() *cobra.Command`, enregistrée par `NewRootCmd`.

- [ ] **Step 1: Écrire le test en échec**

`internal/cli/nest_test.go` :

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// denHomeDeTest fabrique un ~/.den complet et pointe DEN_HOME dessus.
func denHomeDeTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	ecris := func(rel, contenu string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(contenu), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ecris("config.yaml", `
agents:
  claude:
    config_dir: /tmp/den-agents/claude
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }
    bin_dirs: ["$HOME/.local/bin"]
    update: "claude update"
defaults:
  agent: claude
  stack: devx
egress:
  - api.anthropic.com
`)
	ecris("stacks/devx/stack.yaml", "image: devx:v1\n")
	ecris("stacks/dgdevx/stack.yaml", "image: dgdevx:v1\nparent: devx\negress: [gitlab.digitaleo.com]\n")
	ecris("nests/api.yaml", "stack: devx\nrepos:\n  - { path: /dev/api }\n")
	ecris("nests/fullstack.yaml", `
stack: dgdevx
egress: ["10.22.11.54:27017"]
repos:
  - { path: /dev/api }
  - { path: /dev/front, optional: true }
`)

	t.Setenv("DEN_HOME", dir)
	return dir
}

func TestNestLsListeLesNests(t *testing.T) {
	denHomeDeTest(t)
	out, err := run(t, "nest", "ls")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	for _, attendu := range []string{"api", "fullstack", "devx", "dgdevx"} {
		if !strings.Contains(out, attendu) {
			t.Errorf("sortie = %q, attendu contenant %q", out, attendu)
		}
	}
	// tri : api avant fullstack
	if strings.Index(out, "api") > strings.Index(out, "fullstack") {
		t.Errorf("sortie non triée : %q", out)
	}
}

func TestNestShowAfficheLaResolution(t *testing.T) {
	denHomeDeTest(t)
	out, err := run(t, "nest", "show", "fullstack")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	attendus := []string{
		"fullstack",
		"dgdevx:v1",                   // image de la stack
		"claude",                      // agent résolu
		"/tmp/den-agents/claude",      // config_dir résolu
		"10.22.11.54:27017",           // egress du nest
		"api.anthropic.com",           // egress baseline
		"gitlab.digitaleo.com",        // egress de la stack
		"/dev/front",                  // repo optionnel listé
	}
	for _, a := range attendus {
		if !strings.Contains(out, a) {
			t.Errorf("sortie = %q, attendu contenant %q", out, a)
		}
	}
}

func TestNestShowNestInconnu(t *testing.T) {
	denHomeDeTest(t)
	if _, err := run(t, "nest", "show", "fantome"); err == nil {
		t.Fatal("attendu une erreur pour un nest inconnu")
	}
}

func TestNestShowRespecteLesFlagsDeSelection(t *testing.T) {
	denHomeDeTest(t)
	out, err := run(t, "nest", "show", "fullstack", "--without", "front")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if strings.Contains(out, "/dev/front") {
		t.Errorf("le repo exclu apparaît encore : %q", out)
	}
	if !strings.Contains(out, "/dev/api") {
		t.Errorf("le repo requis a disparu : %q", out)
	}
}
```

- [ ] **Step 2: Lancer le test et vérifier qu'il échoue**

Run: `go test ./internal/cli/ -run TestNest -v`
Expected: FAIL — commande `nest` inconnue.

- [ ] **Step 3: Implémenter**

`internal/cli/nest.go` :

```go
package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/spf13/cobra"
)

func newNestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nest",
		Short: "Inspecter les nests déclarés",
	}
	cmd.AddCommand(newNestLsCmd(), newNestShowCmd())
	return cmd
}

func newNestLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "Liste les nests déclarés",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(denHome)
			if err != nil {
				return err
			}
			nests, err := nest.ListNests(home)
			if err != nil {
				return err
			}
			if len(nests) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "aucun nest déclaré dans %s/nests\n", home)
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NEST\tSTACK\tREPOS\tPORTS")
			for _, n := range nests {
				base := "auto"
				if n.Ports.Base > 0 {
					base = fmt.Sprint(n.Ports.Base)
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", n.Name, n.Stack, len(n.Repos), base)
			}
			return w.Flush()
		},
	}
}

func newNestShowCmd() *cobra.Command {
	var opts nest.Options
	cmd := &cobra.Command{
		Use:   "show <nest>",
		Short: "Affiche un nest entièrement résolu",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.Home(denHome)
			if err != nil {
				return err
			}
			g, err := config.LoadGlobal(home)
			if err != nil {
				return err
			}
			stacks, err := config.LoadStacks(home)
			if err != nil {
				return err
			}
			n, err := nest.LoadNest(home, args[0])
			if err != nil {
				return err
			}
			r, err := nest.Resolve(g, stacks, n, opts)
			if err != nil {
				return err
			}
			ecrisResolution(cmd.OutOrStdout(), r)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "agent à utiliser (défaut : defaults.agent)")
	cmd.Flags().StringSliceVar(&opts.Without, "without", nil, "exclure ces repos optionnels")
	cmd.Flags().StringSliceVar(&opts.Only, "only", nil, "ne garder que ces repos optionnels")
	return cmd
}

func ecrisResolution(w io.Writer, r *nest.Resolved) {
	fmt.Fprintf(w, "nest:   %s\n", r.Nest.Name)
	fmt.Fprintf(w, "stack:  %s (image %s)\n", r.Stack.Name, r.Stack.Image)
	fmt.Fprintf(w, "agent:  %s\n", r.AgentName)
	fmt.Fprintf(w, "  config_dir: %s\n", r.AgentConfigDir)
	fmt.Fprintf(w, "  update:     %s\n", r.Agent.Update)
	fmt.Fprintf(w, "ssh:    %s\n", r.SSHMode)
	fmt.Fprintf(w, "worktrees: %s (%s)\n", r.WorktreeRoot, r.WorktreeLayout)

	fmt.Fprintln(w, "repos:")
	for _, repo := range r.Repos {
		statut := "requis"
		if repo.Optional {
			statut = "optionnel"
		}
		fmt.Fprintf(w, "  - %s (%s)\n", repo.Path, statut)
	}

	fmt.Fprintf(w, "egress (%d):\n", len(r.Egress))
	for _, h := range r.Egress {
		fmt.Fprintf(w, "  - %s\n", h)
	}

	if len(r.Nest.Env) > 0 {
		fmt.Fprintln(w, "env:")
		for _, k := range clesTriees(r.Nest.Env) {
			fmt.Fprintf(w, "  %s=%s\n", k, r.Nest.Env[k])
		}
	}

	if len(r.Nest.Ports.Publish) > 0 {
		fmt.Fprintln(w, "ports déclarés:")
		for _, p := range r.Nest.Ports.Publish {
			marques := []string{}
			if p.Open {
				marques = append(marques, "open")
			}
			if p.LoopbackLock {
				marques = append(marques, "loopback-locked")
			}
			suffixe := ""
			if len(marques) > 0 {
				suffixe = " [" + strings.Join(marques, ", ") + "]"
			}
			fmt.Fprintf(w, "  - %s -> %d%s\n", p.Name, p.Container, suffixe)
		}
	}
}

// clesTriees rend l'affichage des maps déterministe (l'ordre d'itération Go ne l'est pas).
func clesTriees(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

Dans `internal/cli/root.go`, ajoute l'enregistrement juste après celui de `version` :

```go
	root.AddCommand(newNestCmd())
```

- [ ] **Step 4: Lancer les tests et vérifier qu'ils passent**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 5: Vérifier à la main sur le vrai binaire**

```bash
go build -o den ./cmd/den && DEN_HOME=$(mktemp -d) ./den nest ls
```
Expected: `aucun nest déclaré dans /tmp/…/nests` (et pas un panic).

- [ ] **Step 6: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): commandes den nest ls et den nest show"
```

---

## Task 11: Commande `den doctor`

**Files:**
- Create: `internal/doctor/doctor.go`
- Create: `internal/cli/doctor.go`
- Modify: `internal/cli/root.go` (enregistrer la sous-commande)
- Test: `internal/doctor/doctor_test.go`
- Test: `internal/cli/doctor_test.go`

**Interfaces:**
- Consumes: tout `internal/config` et `internal/nest`.
- Produces:
  - `doctor.Check{Nom, OK bool, Detail string}`
  - `doctor.Deps{LookPath func(string) (string, error); Stat func(string) (os.FileInfo, error)}` —
    injection des accès système, **indispensable** pour tester sans `sbx` installé.
  - `doctor.Run(denHome string, d Deps) []Check`
  - `cli.newDoctorCmd() *cobra.Command` — sort en erreur si au moins un `Check` est en échec.

**Périmètre :** ce doctor valide la **configuration** et la **présence de `sbx`**. Le test d'egress
réel annoncé au §5 du spec dépend de `internal/policy` et arrive au plan Spawn.

- [ ] **Step 1: Écrire le test en échec**

`internal/doctor/doctor_test.go` :

```go
package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// depsOK simule un système où sbx est installé et tous les chemins existent.
func depsOK() Deps {
	return Deps{
		LookPath: func(string) (string, error) { return "/usr/local/bin/sbx", nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
	}
}

func denHomeValide(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ecris := func(rel, contenu string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(contenu), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ecris("config.yaml", `
agents:
  claude:
    config_dir: /tmp/den/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`)
	ecris("stacks/devx/stack.yaml", "image: devx:v1\n")
	ecris("nests/api.yaml", "stack: devx\nrepos:\n  - { path: /dev/api }\n")
	return dir
}

func trouve(checks []Check, fragment string) (Check, bool) {
	for _, c := range checks {
		if strings.Contains(c.Nom, fragment) || strings.Contains(c.Detail, fragment) {
			return c, true
		}
	}
	return Check{}, false
}

func tousOK(checks []Check) bool {
	for _, c := range checks {
		if !c.OK {
			return false
		}
	}
	return true
}

func TestRunConfigSaine(t *testing.T) {
	checks := Run(denHomeValide(t), depsOK())
	if len(checks) == 0 {
		t.Fatal("aucun check exécuté")
	}
	if !tousOK(checks) {
		t.Errorf("attendu tous les checks OK, obtenu %+v", checks)
	}
}

func TestRunSbxAbsent(t *testing.T) {
	d := depsOK()
	d.LookPath = func(string) (string, error) { return "", errors.New("introuvable") }
	checks := Run(denHomeValide(t), d)
	c, ok := trouve(checks, "sbx")
	if !ok {
		t.Fatal("aucun check ne concerne sbx")
	}
	if c.OK {
		t.Error("le check sbx devrait échouer quand le binaire est absent")
	}
	if tousOK(checks) {
		t.Error("Run ne doit pas rapporter tout-OK quand sbx manque")
	}
}

func TestRunConfigAbsente(t *testing.T) {
	checks := Run(t.TempDir(), depsOK())
	if tousOK(checks) {
		t.Error("attendu un échec quand config.yaml est absent")
	}
	if _, ok := trouve(checks, "config.yaml"); !ok {
		t.Error("le check en échec devrait nommer config.yaml")
	}
}

func TestRunStackParDefautInconnue(t *testing.T) {
	dir := denHomeValide(t)
	// on supprime la stack devx référencée par defaults.stack
	if err := os.RemoveAll(filepath.Join(dir, "stacks", "devx")); err != nil {
		t.Fatal(err)
	}
	checks := Run(dir, depsOK())
	if tousOK(checks) {
		t.Error("attendu un échec quand defaults.stack n'existe pas")
	}
	if _, ok := trouve(checks, "devx"); !ok {
		t.Error("le check en échec devrait nommer la stack manquante")
	}
}

func TestRunRepoDeNestIntrouvable(t *testing.T) {
	d := depsOK()
	d.Stat = func(p string) (os.FileInfo, error) {
		if p == "/dev/api" {
			return nil, errors.New("introuvable")
		}
		return nil, nil
	}
	checks := Run(denHomeValide(t), d)
	if tousOK(checks) {
		t.Error("attendu un échec quand un repo de nest n'existe pas")
	}
	if _, ok := trouve(checks, "/dev/api"); !ok {
		t.Error("le check en échec devrait nommer le repo manquant")
	}
}

func TestRunAgentSansCommandeUpdate(t *testing.T) {
	dir := t.TempDir()
	contenu := "agents:\n  claude:\n    config_dir: /tmp/c\ndefaults:\n  agent: claude\n  stack: devx\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
	checks := Run(dir, depsOK())
	if _, ok := trouve(checks, "update"); !ok {
		t.Error("un agent sans commande update doit être signalé (spec §9.1)")
	}
}
```

- [ ] **Step 2: Lancer le test et vérifier qu'il échoue**

Run: `go test ./internal/doctor/ -v`
Expected: FAIL — `undefined: Run`, `undefined: Deps`, `undefined: Check`.

- [ ] **Step 3: Implémenter le paquet doctor**

`internal/doctor/doctor.go` :

```go
// Package doctor diagnostique une installation den : configuration cohérente,
// stacks et repos présents, sbx disponible. Aucun effet de bord, aucun réseau.
package doctor

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
)

// Check est le résultat d'un diagnostic unitaire.
type Check struct {
	Nom    string
	OK     bool
	Detail string
}

// Deps injecte les accès système, pour que les tests tournent sans sbx installé
// et sans dépendre de l'arborescence réelle de la machine.
type Deps struct {
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
}

// DepsSysteme renvoie les dépendances réelles.
func DepsSysteme() Deps {
	return Deps{LookPath: exec.LookPath, Stat: os.Stat}
}

// Run exécute tous les diagnostics et renvoie la liste complète, échecs compris.
// On ne s'arrête jamais au premier problème : l'utilisateur doit tout voir d'un coup.
func Run(denHome string, d Deps) []Check {
	var checks []Check
	ajoute := func(nom string, ok bool, format string, args ...any) {
		checks = append(checks, Check{Nom: nom, OK: ok, Detail: fmt.Sprintf(format, args...)})
	}

	// 1. sbx présent
	if chemin, err := d.LookPath("sbx"); err != nil {
		ajoute("sbx", false, "binaire sbx introuvable dans le PATH")
	} else {
		ajoute("sbx", true, "%s", chemin)
	}

	// 2. config.yaml chargeable
	g, err := config.LoadGlobal(denHome)
	if err != nil {
		ajoute("config.yaml", false, "%v", err)
		return checks // sans config, tout le reste est indécidable
	}
	ajoute("config.yaml", true, "%s/config.yaml", denHome)

	// 3. cohérence interne de la config
	erreursConfig := g.Validate()
	for _, e := range erreursConfig {
		ajoute("config", false, "%v", e)
	}
	if len(erreursConfig) == 0 {
		ajoute("config", true, "cohérente")
	}

	// 4. stacks
	stacks, err := config.LoadStacks(denHome)
	if err != nil {
		ajoute("stacks", false, "%v", err)
		stacks = map[string]*config.Stack{}
	} else {
		ajoute("stacks", true, "%d déclarée(s)", len(stacks))
	}
	if g.Defaults.Stack != "" {
		if _, ok := stacks[g.Defaults.Stack]; !ok {
			ajoute("defaults.stack", false,
				"stack %q introuvable dans %s/stacks", g.Defaults.Stack, denHome)
		} else {
			ajoute("defaults.stack", true, "%s", g.Defaults.Stack)
		}
	}

	// 5. profils agents : le dossier peut ne pas exister encore (créé au premier spawn),
	// on ne signale que ce qui est structurellement faux, pas l'absence.
	for nom, a := range g.Agents {
		if a.Update == "" {
			ajoute("agent "+nom, false, "aucune commande update déclarée (spec §9.1)")
		}
	}

	// 6. nests : stack référencée existante, repos présents sur disque
	nests, err := nest.ListNests(denHome)
	if err != nil {
		ajoute("nests", false, "%v", err)
		return checks
	}
	for _, n := range nests {
		nomStack := n.Stack
		if nomStack == "" {
			nomStack = g.Defaults.Stack
		}
		if _, ok := stacks[nomStack]; !ok {
			ajoute("nest "+n.Name, false, "stack %q introuvable", nomStack)
		}
		for _, r := range n.Repos {
			if _, err := d.Stat(r.Path); err != nil {
				ajoute("nest "+n.Name, false, "repo introuvable : %s", r.Path)
			}
		}
	}
	if len(nests) > 0 {
		ajoute("nests", true, "%d déclaré(s)", len(nests))
	}

	return checks
}
```

- [ ] **Step 4: Lancer les tests du paquet doctor**

Run: `go test ./internal/doctor/ -v`
Expected: PASS.

- [ ] **Step 5: Écrire le test de la commande**

`internal/cli/doctor_test.go` :

```go
package cli

import (
	"strings"
	"testing"
)

func TestDoctorSurConfigSaine(t *testing.T) {
	denHomeDeTest(t)
	out, err := run(t, "doctor")
	// sbx n'est pas installé sur la machine de test : la commande DOIT échouer,
	// mais après avoir affiché tous les diagnostics.
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("sortie = %q, attendu le diagnostic de config.yaml", out)
	}
	if !strings.Contains(out, "sbx") {
		t.Errorf("sortie = %q, attendu le diagnostic de sbx", out)
	}
	_ = err // le code de sortie dépend de la présence de sbx sur la machine
}

func TestDoctorEchoueSurConfigAbsente(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())
	out, err := run(t, "doctor")
	if err == nil {
		t.Error("attendu une erreur quand la config est absente")
	}
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("sortie = %q, attendu une mention de config.yaml", out)
	}
}
```

- [ ] **Step 6: Implémenter la commande**

`internal/cli/doctor.go` :

```go
package cli

import (
	"fmt"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/doctor"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnostique la configuration den et l'environnement",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(denHome)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "den home: %s\n\n", home)

			checks := doctor.Run(home, doctor.DepsSysteme())
			echecs := 0
			for _, c := range checks {
				marque := "ok  "
				if !c.OK {
					marque = "FAIL"
					echecs++
				}
				fmt.Fprintf(out, "[%s] %-16s %s\n", marque, c.Nom, c.Detail)
			}

			if echecs > 0 {
				return fmt.Errorf("%d diagnostic(s) en échec", echecs)
			}
			fmt.Fprintln(out, "\ntout est en ordre")
			return nil
		},
	}
}
```

Dans `internal/cli/root.go` :

```go
	root.AddCommand(newDoctorCmd())
```

- [ ] **Step 7: Lancer toute la suite de tests**

Run: `go test ./... -v`
Expected: PASS sur tous les paquets.

- [ ] **Step 8: Vérifier le binaire à la main**

```bash
go build -o den ./cmd/den && DEN_HOME=$(mktemp -d) ./den doctor; echo "exit=$?"
```
Expected: des lignes `[FAIL]` (config absente) et `exit=1`.

- [ ] **Step 9: Commit**

```bash
git add internal
git commit -m "feat(cli): commande den doctor"
```

---

## Task 12: Exemple de `~/.den/` et documentation d'amorçage

**Files:**
- Create: `examples/den-home/config.yaml`
- Create: `examples/den-home/stacks/devx/stack.yaml`
- Create: `examples/den-home/nests/exemple.yaml`
- Create: `README.md`
- Test: `internal/config/example_test.go`

**Interfaces:**
- Consumes: `config.LoadGlobal`, `config.LoadStacks`, `nest.ListNests`.
- Produces: rien de nouveau — cette tâche verrouille par un test que l'exemple livré **charge et
  valide réellement**. Un exemple faux est pire que pas d'exemple.

- [ ] **Step 1: Écrire le test en échec**

`internal/config/example_test.go` :

```go
package config_test

import (
	"path/filepath"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
)

// L'exemple livré dans examples/den-home doit charger et valider sans erreur :
// c'est le point de départ que l'utilisateur recopie dans ~/.den.
func TestExempleDenHomeEstValide(t *testing.T) {
	home := filepath.Join("..", "..", "examples", "den-home")

	g, err := config.LoadGlobal(home)
	if err != nil {
		t.Fatalf("chargement de l'exemple : %v", err)
	}
	if errs := g.Validate(); len(errs) != 0 {
		t.Fatalf("l'exemple ne valide pas : %v", errs)
	}

	stacks, err := config.LoadStacks(home)
	if err != nil {
		t.Fatalf("chargement des stacks de l'exemple : %v", err)
	}
	if _, ok := stacks[g.Defaults.Stack]; !ok {
		t.Errorf("defaults.stack = %q absent des stacks de l'exemple", g.Defaults.Stack)
	}

	nests, err := nest.ListNests(home)
	if err != nil {
		t.Fatalf("chargement des nests de l'exemple : %v", err)
	}
	if len(nests) == 0 {
		t.Fatal("l'exemple ne déclare aucun nest")
	}
	for _, n := range nests {
		if _, err := nest.Resolve(g, stacks, n, nest.Options{}); err != nil {
			t.Errorf("nest %q de l'exemple ne se résout pas : %v", n.Name, err)
		}
	}
}
```

- [ ] **Step 2: Lancer le test et vérifier qu'il échoue**

Run: `go test ./internal/config/ -run TestExemple -v`
Expected: FAIL — le dossier `examples/den-home` n'existe pas.

- [ ] **Step 3: Créer l'exemple**

`examples/den-home/config.yaml` :

```yaml
# Recopie ce dossier dans ~/.den puis adapte-le.
agents:
  claude:
    config_dir: ~/.den/agents/claude
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }
    # Chemins IN-VM ajoutés au PATH avant la commande update (cf. spec §9.1).
    bin_dirs: ["$HOME/.local/bin", "$HOME/.claude/local"]
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: agent-forward
  dir: ~/.ssh_sbx
worktree_layout: central
worktree_root: ~/.den/worktrees
egress:
  - api.anthropic.com
  - github.com
  - registry.npmjs.org
```

`examples/den-home/stacks/devx/stack.yaml` :

```yaml
name: devx
image: devx:v1
kit: ./kit
egress: []
```

`examples/den-home/nests/exemple.yaml` :

```yaml
name: exemple
stack: devx
repos:
  - { path: ~/dev/mon-projet }
```

- [ ] **Step 4: Écrire le README**

`README.md` :

```markdown
# den

CLI générique pour piloter des sandboxes [sbx](https://…) : une commande pour démarrer une microVM
multi-projet, sans retaper mixin, kits et policy à la main.

## Installation

```bash
go build -o den ./cmd/den
```

## Amorçage

```bash
cp -R examples/den-home ~/.den
$EDITOR ~/.den/config.yaml
./den doctor
```

`~/.den/` est la source unique de vérité. La variable `DEN_HOME` (ou le flag `--den-home`) permet
d'en utiliser un autre — c'est ce qui rend `den` testable et scriptable.

## Commandes disponibles

| Commande | Rôle |
|---|---|
| `den nest ls` | liste les nests déclarés |
| `den nest show <n>` | affiche un nest entièrement résolu (stack, agent, egress, repos) |
| `den doctor` | diagnostique la configuration et l'environnement |
| `den version` | version du binaire |

Le spawn (`den <nest>`), les ports et le build arrivent dans les incréments suivants — voir
`docs/superpowers/plans/`.

## Conception

`docs/superpowers/specs/2026-07-27-den-cli-design.md`.
```

- [ ] **Step 5: Lancer toute la suite**

Run: `go test ./... `
Expected: PASS.

- [ ] **Step 6: Vérifier le parcours d'amorçage à la main**

```bash
go build -o den ./cmd/den
rm -rf /tmp/den-demo && cp -R examples/den-home /tmp/den-demo
DEN_HOME=/tmp/den-demo ./den nest ls
DEN_HOME=/tmp/den-demo ./den nest show exemple
```
Expected: `nest ls` liste `exemple` ; `nest show` affiche la stack `devx`, l'agent `claude` et les
3 egress baseline.

- [ ] **Step 7: Commit**

```bash
git add examples README.md internal
git commit -m "docs: exemple de ~/.den valide par un test, et README d'amorcage"
```

---

## Definition of done du Plan 1

- [ ] `go test ./...` passe intégralement, sans réseau, sans `sbx`, sans `git`.
- [ ] `go build -o den ./cmd/den` produit un binaire fonctionnel.
- [ ] `den doctor` sur l'exemple livré ne signale que l'absence de `sbx` si `sbx` n'est pas installé.
- [ ] `den nest ls` et `den nest show <n>` fonctionnent sur `examples/den-home`.
- [ ] Aucune référence à `internal/sbx`, `worktree`, `policy`, `ports` (périmètre des plans suivants).

## Suite

- **Plan 2 — Spawn :** `internal/sbx` (interface `Runner` + argv en golden files), `internal/worktree`,
  `internal/agent` (mixin généré, **avec la commande de fraîcheur en dernière startup command et
  l'`export PATH` construit depuis `bin_dirs`**, cf. spec §9.1), `internal/policy` (settle-loop
  fail-closed), puis `den <nest>`, `den ls`, `den sh`, `den rm`.
- **Plan 3 — Ports :** fenêtre déterministe, scan anti-collision, `den ports`.
- **Plan 4 — Build DAG :** `den build [stack] [--force]`.
