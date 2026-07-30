# `den` — Plan 2 : Spawn

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rendre `den <nest>` réel — résoudre un nest, propager ses worktrees, générer le mixin,
assembler `sbx create`, attendre que la policy réseau soit posée, puis attacher un shell ; plus les
commandes de cycle de vie `den ls`, `den sh`, `den rm`.

**Architecture:** Le Plan 1 a livré la résolution pure (`config` + `nest`). Ce plan ajoute les
modules qui **touchent le monde**, chacun derrière une interface injectable pour rester testable
sans microVM : `internal/sbx` (interface `Runner` + assemblage d'argv), `internal/worktree`
(interface `Git`), `internal/policy` (settle-loop fail-closed), `internal/agent` (mixin généré).
`internal/cli` orchestre et n'affiche. **Aucun test de ce plan ne lance `sbx`** ; les tests
`worktree` sont les seuls à lancer un vrai `git`, sur des dépôts créés dans `t.TempDir()`.

**Tech Stack:** Go 1.26 · cobra · `gopkg.in/yaml.v3` · `encoding/json` (stdlib) · `os/exec`.

---

## Global Constraints

Ces contraintes s'appliquent à **toutes** les tâches, elles ne sont pas répétées ensuite.

- **Module Go :** `github.com/PillowPillow/den`. **Go 1.26**, aucune dépendance nouvelle — `cobra`
  et `yaml.v3` sont les seules autorisées, `encoding/json` couvre les sorties `sbx --json`.
- **TDD strict** : le test est écrit et **exécuté en échec** avant toute implémentation.
- **Zéro `sbx` dans les tests.** `sbx` n'est pas installé sur la machine de développement (vérifié
  le 2026-07-28 : `which sbx` → absent depuis la sandbox). Tout passe par `sbx.Fake`. Un test qui
  exige le binaire `sbx` est un test à refuser en revue.
- **`git` autorisé uniquement dans `internal/worktree`**, sur des dépôts créés par le test dans
  `t.TempDir()`. Jamais sur un dépôt de la machine.
- **Messages utilisateur et commentaires en français.** Erreurs au format `contexte : détail`,
  nommant toujours le chemin complet et listant les valeurs disponibles.
- **Déterminisme** : toute liste ou map destinée à un affichage ou à un golden file est **triée**
  (`slices.Sorted(maps.Keys(m))`). Les golden files en dépendent directement.
- **Pas d'expansion de `$HOME` côté hôte pour `bin_dirs`** : ces chemins sont résolus **dans la VM**
  par bash. `config.ExpandPath` n'expanse qu'un `~` **en tête**. Confondre les deux casse le §9.1
  du spec sans qu'aucun test ne le signale avant le premier boot réel.
- **`internal/cli` ne fait que du câblage cobra et de l'affichage.** Toute logique testable
  descend dans un module `internal/`.
- **Commits fréquents**, un par tâche minimum, message conventionnel (`feat:`, `fix:`, `test:`,
  `refactor:`, `docs:`).

**Spec de référence :** `docs/superpowers/specs/2026-07-27-den-cli-design.md`. ⚠️ **La tâche 1
l'amende** : cinq de ses affirmations sont falsifiées par le sondage de la CLI `sbx` du 2026-07-28.
Lis le spec **après** la tâche 1, jamais avant.

**Hors périmètre de ce plan** (plans suivants, ne pas anticiper) : `internal/ports` et `den ports`
(Plan 3), le flag `-i` de sélection interactive des repos (Plan 3), `den build` et le DAG (Plan 4),
le flux autonome `den agent` / `den review` (hors v1).

---

## Faits `sbx` établis — ne pas les redécouvrir, ne pas les contredire

Sondés sur la machine de l'utilisateur le **2026-07-28** (sbx v0.35.0). Tout ce plan en dépend.

```
sbx create [flags] AGENT PATH [PATH...]
  flags : --clone --cpus int --kit strings (répétable) -m/--memory --name --profile -q -t/--template
  AGENT ∈ {claude, codex, copilot, cursor, docker-agent, droid, gemini, kiro, opencode, shell}
  PATH accepte le suffixe `:ro`
  --name : « letters, numbers, hyphens, periods, plus signs and minus signs only »
  ⚠️ AUCUN --label. La décision verrouillée n°10 du spec (état par labels) est FALSIFIÉE.

sbx ls [--json] [-q]
  {"sandboxes":[{"name","id","agent","status","workspaces":["/p","/p:ro"]}]}
  ⚠️ aucun champ de date/création → la colonne « âge » du spec §5 est INFAISABLE.

sbx exec [flags] SANDBOX COMMAND [ARG...]
  flags utiles : -i/--interactive -t/--tty -d/--detach -w/--workdir -u/--user

sbx ports SANDBOX [--publish spec] [--unpublish spec] [--json]
  spec : [[HOST_IP:]HOST_PORT:]SANDBOX_PORT[/PROTOCOL]

sbx policy check network [--sandbox SANDBOX] [--json] [--verbose] TARGET
  « Bare hosts and IP literals are evaluated with port 443. »
  → une entrée egress nue est cohérente entre le mixin et le check, sans normalisation.

sbx rm --force NAME
```

**Schéma de kit réel** (relevé sur `sbx-devbox/lib/*/spec.yaml`, pas sur le spec) :

```yaml
schemaVersion: 2
kind: mixin
name: <identifiant>
version: 1.0.0
description: >-
  ...
caps:
  network:
    allow: ["api.anthropic.com:443", "github.com:22"]
environment:
  variables:
    CLAUDE_CONFIG_DIR: /chemin/hote
commands:
  startup:
    - command: ["bash", "-c", "..."]
```

Le spec écrit `network.allow` et `env` : **c'est faux**, les clés réelles sont `caps.network.allow`
et `environment.variables`. La tâche 1 corrige le spec.

**Deux pièges du dispatcher** (`/etc/durable-startup.d/run.sh`, vérifiés empiriquement, journal dans
`/var/log/sbx-kit-startup.log`) :

1. Chaque commande passe par `su -s /bin/sh -c … agent`, un `su` **non-login** : PATH sans
   `~/.local/bin` → tout binaire user-local sort en `127`. Il faut un `export PATH` explicite.
2. Le dispatcher fait `exit $rc` au **premier** échec : une commande non-zéro **prive tous les kits
   suivants** de leurs startup commands. Ce qui est fail-closed se layere **en dernier**.

---

## Décisions de ce plan (prises avec l'utilisateur le 2026-07-28)

| Point | Décision | Fondement |
|---|---|---|
| Identité d'une sandbox | **Convention de nommage**, `--label` n'existant pas | sondage `create --help` |
| Séparateur nest/worktree | **`.`** — `<nest>.<wt>` ; `.` interdit dans les deux noms | voir tâche 3 |
| Colonne « âge » de `den ls` | **Supprimée** | absente de `sbx ls --json` |
| Agent positionnel du `create` | **`shell`** | den attache par `exec`, cf. tâche 9 |
| Attache | `sbx exec -it <name> -w <workdir> bash -l` | `sbx run` lance le flavor de l'image, pas un shell |
| Kits transverses | **`kits: [...]` dans `stack.yaml`**, chemins relatifs au dossier de la stack | utilisateur |
| `policy-baseline` | **Disparaît en tant que kit** — son contenu est déjà `egress:` dans `config.yaml`, matérialisé dans le mixin généré | évite un doublon de source |
| Nest illisible | `ListNests` **liste les sains et signale les cassés** ; `LoadNest` reste dur | utilisateur |
| Emplacement du mixin | `<denHome>/cache/mixins/<sandbox>/spec.yaml` | `cache/` est déclaré reconstructible (spec §3) ; un `mktemp` s'évapore et rend le boot indébogable |

---

## Structure des fichiers

| Fichier | Responsabilité |
|---|---|
| `internal/config/nom.go` *(modifié)* | + `ValiderComposantSandbox` — charset compatible `sbx --name` |
| `internal/config/stack.go` *(modifié)* | + champ `Kits []string`, résolu relativement au dossier de la stack |
| `internal/nest/nest.go` *(modifié)* | `ListNests` tolérante ; nom de nest validé au chargement |
| `internal/nest/resolve.go` *(modifié)* | `Resolved` gagne `DenHome` et `Env` (fusionné + substitué) |
| `internal/sbx/runner.go` | Interface `Runner`, implémentation `Exec` |
| `internal/sbx/fake.go` | `Fake` — double de test partagé par tous les paquets consommateurs |
| `internal/sbx/nom.go` | `NomSandbox`, `DecomposeNom` |
| `internal/sbx/ls.go` | `Ls` — décodage de `sbx ls --json` |
| `internal/sbx/argv.go` | `ArgvCreate` — assemblage pur, verrouillé par golden files |
| `internal/agent/fraicheur.go` | `CommandeFraicheur` — §9.1, golden file |
| `internal/agent/mixin.go` | `RendMixin` / `EcrisMixin` — golden file |
| `internal/worktree/worktree.go` | `Chemin`, `Assure`, `Retire` — derrière l'interface `Git` |
| `internal/policy/settle.go` | `Settle` — settle-loop fail-closed, horloge injectée |
| `internal/spawn/spawn.go` | Orchestration de `den <nest>` (spec §6) + `Attache` — hors `cli` pour rester testable sans cobra ni tty |
| `internal/cli/spawn.go` | Câblage cobra de `den <nest>` sur la racine |
| `internal/cli/ls.go` | `den ls` |
| `internal/cli/sh.go` | `den sh <name>` |
| `internal/cli/rm.go` | `den rm <name>` |
| `internal/cli/root.go` *(modifié)* | `NewRootCmdAvec(deps, runner)` — accès au monde injectés |

Chaque fichier a son `*_test.go` à côté. Golden files sous `testdata/` du paquet concerné.

---

## Ordre des tâches

La tâche 1 (spec) passe en premier : elle corrige des affirmations fausses que les tâches suivantes
liraient comme vraies. La tâche 2 honore le §11.1 du handoff — « écrire le golden du §9.1 en
premier », parce que c'est le garde-fou qui empêche d'expanser `bin_dirs` côté hôte.

| # | Tâche | Dépend de |
|---|---|---|
| 1 | Amender le spec (5 corrections dictées par le sondage) | — |
| 2 | `agent.CommandeFraicheur` — golden §9.1 | 1 |
| 3 | `sbx.NomSandbox` / `DecomposeNom` + charset | 1 |
| 4 | `Resolved.DenHome` + `Resolved.Env` fusionné | 1 |
| 5 | `Stack.Kits` | 1 |
| 6 | `sbx.Runner` + `sbx.Fake` | 1 |
| 7 | `agent.RendMixin` / `EcrisMixin` — golden | 2, 4 |
| 8 | `sbx.Ls` | 6 |
| 9 | `sbx.ArgvCreate` — golden | 3, 4, 5, 7 |
| 10 | `internal/worktree` | 3 |
| 11 | `policy.Settle` — fail-closed | 6 |
| 12 | `den <nest>` — orchestration | 7, 8, 9, 10, 11 |
| 13 | `den ls` | 8 |
| 14 | `den sh` | 8 |
| 15 | `den rm` | 8, 10 |
| 16 | `ListNests` tolérante + `doctor` (dette Plan 1) | 1 |
| 17 | Exercer le binaire sur des configurations hostiles | toutes |

---

## Task 1: Amender le spec — cinq affirmations falsifiées

Le sondage de la CLI `sbx` du 2026-07-28 a invalidé cinq points du spec. Tant qu'ils y figurent,
chaque implémenteur les lira comme la source de vérité et codera contre une réalité qui n'existe
pas. C'est une tâche de documentation, mais elle est bloquante.

**Files:**
- Modify: `docs/superpowers/specs/2026-07-27-den-cli-design.md`

**Interfaces:**
- Consumes: rien.
- Produces: le spec corrigé — toutes les tâches suivantes s'y réfèrent.

- [ ] **Step 1: Corriger §11 et §13.10 — l'état ne peut pas reposer sur des labels**

Dans **§11**, remplacer le paragraphe « **État (approche A + un peu de B) :** … » par :

```markdown
**État (approche A) :** `sbx create` **n'a pas de flag `--label`** (vérifié le 2026-07-28,
sbx v0.35.0 : ses seuls flags sont `--clone --cpus --kit --memory --name --profile --quiet
--template`). L'identité d'une sandbox est donc portée par son **nom** : `<nest>` sans worktree,
`<nest>.<worktree>` avec. `den ls` liste `sbx ls --json` et attribue chaque sandbox par
décomposition de son nom. Le séparateur est `.` et non `-` : il est interdit dans les noms de nest
et de worktree, ce qui rend la décomposition **exacte** au lieu de dépendre d'un plus-long-préfixe
contre la liste des nests déclarés — une sandbox reste attribuable même après suppression de son
nest. Cache `~/.den/cache/` reconstructible, jamais source de vérité.
```

Dans **§13**, remplacer la ligne 10 par :

```markdown
10. État sans DB : identité portée par le **nom de sandbox** `<nest>[.<worktree>]` (`--label`
    n'existe pas dans sbx) ; cache reconstructible optionnel.
```

- [ ] **Step 2: Corriger §5 — la colonne « âge » n'existe pas**

Dans le tableau §5, remplacer la ligne `den ls` par :

```markdown
| `den ls` | sandboxes vivantes (`sbx ls --json` filtré sur le motif de nommage, colonnes nom/nest/worktree/statut/workspaces) |
```

`sbx ls --json` renvoie exactement `{"sandboxes":[{"name","id","agent","status","workspaces":[…]}]}` :
aucun champ de date, l'âge n'est pas calculable.

- [ ] **Step 3: Corriger §6.5, §6.6 et §7 — le vrai schéma de kit**

Partout où le spec écrit `network.allow`, écrire **`caps.network.allow`** ; partout où il décrit les
variables d'environnement d'un kit, préciser **`environment.variables`**. Ajouter à la fin du §7 :

```markdown
**Schéma de kit (relevé sur les kits réels, pas déduit) :** `schemaVersion: 2`, `kind: mixin`,
`name`, `version`, `description` ; les capacités réseau vivent sous **`caps.network.allow`** (liste
de `host`, `host:port`, `ip` ou `ip:port`), les variables sous **`environment.variables`**, les
commandes de boot sous **`commands.startup[].command`** (tableau argv). `sbx policy check network`
évalue un hôte nu **sur le port 443** : une entrée egress nue est donc cohérente de bout en bout,
den ne normalise rien.
```

- [ ] **Step 4: Corriger §3, §4.2 et §6.6 — où vivent les kits transverses**

Le §6 fait layerer `--kit policy-baseline` mais le §3 ne déclare nulle part où ce kit vivrait.
Décision : **il n'existe plus**. Son contenu est déjà la clé `egress:` de `config.yaml`,
matérialisée dans le mixin généré — le garder serait une seconde source de vérité pour la même
allowlist. Les kits transverses **non-egress** (ex. `ssh-known-hosts`, qui pose des empreintes SSH
via une startup command) se déclarent par stack.

Dans **§4.2**, remplacer le bloc YAML par :

```yaml
image: dgdevx:v1        # passé à `sbx create --template`
parent: devx            # DAG de build (build devx avant dgdevx)
kit: ./kit              # kit par défaut de la stack (env + egress toolchain)
kits:                   # optionnel : kits transverses layerés AVANT `kit`
  - ../../kits/ssh-known-hosts
egress: []              # ajouts egress niveau stack
```

Ajouter juste après : « Les chemins de `kit` et `kits` sont résolus **relativement au dossier de la
stack**. L'ordre de `kits` est préservé : c'est un ordre de layering, pas un ensemble. »

Dans **§6.6**, remplacer la ligne des `--kit` par :

```markdown
   `--kit <stacks/<stack>/kits[i]>…  --kit stacks/<stack>/kit  --kit <mixin généré>`
   (**le mixin généré reste le dernier `--kit`** — même raison qu'au point 5),
```

- [ ] **Step 5: Corriger §6.6 et §6.9 — agent positionnel et attache**

`sbx create` **exige un agent positionnel** avant les chemins, et `sbx run` attache la commande du
*flavor de l'image* (une image snapshotée depuis la base claude lance `claude`, pas un shell — cf.
`sbx-devbox/stacks/devx/TUTO.md`). Remplacer dans **§6.6** la ligne des positionnels par :

```markdown
   agent positionnel **`shell`** (obligatoire : `sbx create [flags] AGENT PATH [PATH...]`), puis
   positionnels = chemins worktree/repo + `config_dir` (+ `~/.ssh_sbx` si `ssh.mode=mount`).
```

et au **§6.9** remplacer la phrase d'attache par :

```markdown
9. **Attache.** `sbx exec -it <name> -w <workdir> bash -l` → shell, sauf `--detach`. Pas
   `sbx run` : celui-ci lance la commande du flavor de l'image (souvent `claude`), n'a aucun flag
   pour la remplacer, et son `-- ARGS` ne fait qu'*ajouter* des arguments. **Les ports ne sont PAS
   publiés au spawn** → `den ports <nest>` à la demande.
```

- [ ] **Step 6: Ajouter la contrainte de charset au §2**

À la fin du §2, ajouter :

```markdown
**Contrainte de nommage.** Le nom d'un nest devient un nom de sandbox, que `sbx create --name`
restreint à « letters, numbers, hyphens, periods, plus signs and minus signs ». den impose plus
strict encore sur les nests et les worktrees : `[A-Za-z0-9+-]+`, **le point exclu** — il est réservé
au rôle de séparateur dans `<nest>.<worktree>`. Un `-w feature/123` est donc refusé avec un message
actionnable, jamais normalisé en silence : normaliser casserait l'aller-retour
`den <nest> -w <wt>` → nom de sandbox → `den ls`.
```

- [ ] **Step 7: Retirer les questions ouvertes désormais tranchées du §14**

Supprimer les deux puces « **Sémantique exacte de `sbx policy check`** » et « **Format des labels
sbx** ». Les remplacer par une seule :

```markdown
- **Surface `sbx` figée le 2026-07-28** (v0.35.0) : `policy check network [--sandbox S] [--json]
  TARGET` confirmé (`--sandbox` existe, l'évaluation scopée est donc possible) ; `--label`
  **n'existe pas** → identité par le nom. À revalider si sbx passe en v0.37+.
```

- [ ] **Step 8: Vérifier qu'il ne reste aucune trace des affirmations corrigées**

Run:
```bash
cd /Users/polochon/Development/Pillow/den
grep -n -- "--label\|den\.managed\|den\.nest\|den\.worktree" docs/superpowers/specs/2026-07-27-den-cli-design.md
grep -n "network\.allow" docs/superpowers/specs/2026-07-27-den-cli-design.md | grep -v "caps\.network\.allow"
grep -n "âge" docs/superpowers/specs/2026-07-27-den-cli-design.md
```
Expected: les trois commandes ne renvoient **aucune ligne**. Si l'une renvoie quelque chose, la
corriger avant de committer.

- [ ] **Step 9: Commit**

```bash
git add docs/superpowers/specs/2026-07-27-den-cli-design.md
git commit -m "docs(spec): aligne le spec sur la surface sbx reelle (pas de --label, pas d'age, caps.network.allow)"
```

---

## Task 2: `agent.CommandeFraicheur` — le golden du §9.1

Première tâche de code, sur consigne explicite du handoff §11.1 : le rendu doit contenir
**littéralement** `$HOME/.local/bin`. C'est le garde-fou de la frontière hôte/VM. Une
implémentation qui expanserait `bin_dirs` côté hôte produirait un PATH pointant sur le home de
l'utilisateur *hôte* — chemin qui n'existe pas dans la VM — et le bug ne serait visible qu'au
premier boot réel.

**Files:**
- Create: `internal/agent/fraicheur.go`
- Create: `internal/agent/fraicheur_test.go`
- Create: `internal/agent/testdata/fraicheur-claude.golden`

**Interfaces:**
- Consumes: `config.Agent{ConfigDir, Env, BinDirs, Update string}` (Plan 1, inchangé).
- Produces:
  - `func CommandeFraicheur(nomAgent string, a config.Agent) ([]string, error)` — renvoie l'argv
    complet `["bash", "-c", <script>]` à placer en **dernière** `commands.startup` du mixin.
    Erreur si `a.Update` est vide.

- [ ] **Step 1: Écrire le test qui échoue**

```go
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
)

func agentClaude() config.Agent {
	return config.Agent{
		ConfigDir: "/home/moi/.den/agents/claude",
		Env:       map[string]string{"CLAUDE_CONFIG_DIR": "{config_dir}"},
		BinDirs:   []string{"$HOME/.local/bin", "$HOME/.claude/local"},
		Update:    "claude update",
	}
}

// Le $HOME des bin_dirs vise le home DE LA VM : il doit traverser den INTACT.
// C'est l'invariant que le handoff §11.1 impose de verrouiller en premier.
func TestCommandeFraicheurNExpansePasHOME(t *testing.T) {
	argv, err := CommandeFraicheur("claude", agentClaude())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(argv) != 3 || argv[0] != "bash" || argv[1] != "-c" {
		t.Fatalf("argv attendu [bash -c <script>], obtenu %q", argv)
	}
	script := argv[2]
	if !strings.Contains(script, `export PATH="$HOME/.local/bin:$HOME/.claude/local:$PATH"`) {
		t.Errorf("le script doit poser le PATH avec les bin_dirs LITTÉRAUX ; obtenu :\n%s", script)
	}
	if home, _ := os.UserHomeDir(); home != "" && strings.Contains(script, home) {
		t.Errorf("le home HÔTE %q a fuité dans le script destiné à la VM :\n%s", home, script)
	}
}

// Le dispatcher sbx sort au premier échec : la commande doit être fail-closed,
// avec des tentatives bornées pour absorber la propagation de la policy egress.
func TestCommandeFraicheurEstFailClosed(t *testing.T) {
	argv, err := CommandeFraicheur("claude", agentClaude())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	script := argv[2]
	for _, attendu := range []string{"claude update", "exit 127", "exit 1", "exit 0", "sleep 10"} {
		if !strings.Contains(script, attendu) {
			t.Errorf("le script doit contenir %q ; obtenu :\n%s", attendu, script)
		}
	}
}

func TestCommandeFraicheurSansBinDirs(t *testing.T) {
	a := agentClaude()
	a.BinDirs = nil
	argv, err := CommandeFraicheur("claude", a)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if strings.Contains(argv[2], "export PATH") {
		t.Errorf("sans bin_dirs, aucune ligne export PATH ne doit être émise :\n%s", argv[2])
	}
}

func TestCommandeFraicheurRefuseUpdateVide(t *testing.T) {
	a := agentClaude()
	a.Update = ""
	if _, err := CommandeFraicheur("claude", a); err == nil {
		t.Fatal("un agent sans commande update doit être refusé (spec §9.1)")
	}
}

// Golden file : filet de régression sur le rendu exact.
func TestCommandeFraicheurGolden(t *testing.T) {
	argv, err := CommandeFraicheur("claude", agentClaude())
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	chemin := filepath.Join("testdata", "fraicheur-claude.golden")
	attendu, err := os.ReadFile(chemin)
	if err != nil {
		t.Fatalf("lecture du golden : %v", err)
	}
	if got := argv[2]; got != string(attendu) {
		t.Errorf("rendu != %s\n--- obtenu ---\n%s\n--- attendu ---\n%s", chemin, got, attendu)
	}
}
```

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/agent/ -run TestCommandeFraicheur -v`
Expected: FAIL — `undefined: CommandeFraicheur` (le paquet ne compile pas encore).

- [ ] **Step 3: Implémenter**

Créer `internal/agent/fraicheur.go` :

```go
// Package agent résout le profil de l'agent actif et génère le mixin jetable
// layeré au `sbx create` (spec §5, §9).
package agent

import (
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// tentativesFraicheur borne les essais de mise à jour. Trois essais espacés de
// 10 s absorbent la propagation NON instantanée de la policy egress (spec §7) :
// sans eux, un simple hoquet réseau au boot avorterait tout le démarrage.
const tentativesFraicheur = 3

// CommandeFraicheur rend l'argv de la commande de mise à jour de l'agent, à
// placer en DERNIÈRE commands.startup du DERNIER kit (spec §9.1).
//
// Deux invariants non négociables :
//
//  1. Les bin_dirs sont injectés LITTÉRALEMENT dans un `export PATH`. Ils
//     contiennent des `$HOME` qui visent le home DE LA VM ; les expanser côté
//     hôte produirait un chemin inexistant dans la microVM. Le dispatcher sbx
//     enveloppe chaque commande dans un `su` NON-login, dont le PATH ne
//     contient pas ~/.local/bin — sans cette ligne, `claude` sort en 127.
//  2. Le script est fail-closed. Le dispatcher fait `exit $rc` au premier
//     échec, ce qui prive les kits SUIVANTS de leurs startup commands : c'est
//     précisément pourquoi ce kit se layere en dernier.
func CommandeFraicheur(nomAgent string, a config.Agent) ([]string, error) {
	if strings.TrimSpace(a.Update) == "" {
		return nil, fmt.Errorf(
			"agent %q : aucune commande update déclarée — une sandbox ne doit jamais démarrer "+
				"avec un agent périmé (spec §9.1)", nomAgent)
	}

	// Le binaire à sonder est le premier mot de la commande update. C'est une
	// convention, pas une déduction : elle est documentée dans le message
	// d'erreur du script pour que le diagnostic reste lisible en VM.
	binaire := strings.Fields(a.Update)[0]

	var b strings.Builder
	b.WriteString("set -uo pipefail\n\n")
	if len(a.BinDirs) > 0 {
		// Guillemets doubles, sans échappement : $HOME doit être expansé par le
		// bash de la VM, pas par den.
		fmt.Fprintf(&b, "# su non-login : PATH minimal, on rétablit les bin_dirs de l'agent.\n")
		fmt.Fprintf(&b, "export PATH=%q\n\n", strings.Join(a.BinDirs, ":")+":$PATH")
	}
	fmt.Fprintf(&b, "if ! command -v %s >/dev/null 2>&1; then\n", binaire)
	fmt.Fprintf(&b, "  echo \"agent %s : FATAL binaire %s introuvable (PATH=$PATH)\" >&2\n", nomAgent, binaire)
	b.WriteString("  exit 127\n")
	b.WriteString("fi\n\n")
	b.WriteString("tentative=1\n")
	fmt.Fprintf(&b, "while [ \"$tentative\" -le %d ]; do\n", tentativesFraicheur)
	fmt.Fprintf(&b, "  if sortie=\"$(%s 2>&1)\"; then\n", a.Update)
	fmt.Fprintf(&b, "    echo \"agent %s : à jour\"\n", nomAgent)
	b.WriteString("    exit 0\n")
	b.WriteString("  fi\n")
	fmt.Fprintf(&b, "  echo \"agent %s : tentative ${tentative}/%d échouée :\" >&2\n", nomAgent, tentativesFraicheur)
	b.WriteString("  echo \"$sortie\" >&2\n")
	fmt.Fprintf(&b, "  if [ \"$tentative\" -lt %d ]; then\n", tentativesFraicheur)
	b.WriteString("    sleep 10\n")
	b.WriteString("  fi\n")
	b.WriteString("  tentative=$((tentative + 1))\n")
	b.WriteString("done\n\n")
	fmt.Fprintf(&b, "echo \"agent %s : FATAL mise à jour impossible après %d tentatives (fail-closed)\" >&2\n",
		nomAgent, tentativesFraicheur)
	b.WriteString("exit 1\n")

	return []string{"bash", "-c", b.String()}, nil
}
```

- [ ] **Step 4: Générer le golden, puis le relire à l'œil**

Le golden est un **filet de régression**, pas le filet de sécurité : les invariants portants
(`$HOME` littéral, fail-closed) sont assertés par les tests dédiés du Step 1, qui doivent passer
**avant** que le golden soit figé.

Run:
```bash
cd /Users/polochon/Development/Pillow/den
mkdir -p internal/agent/testdata
go test ./internal/agent/ -run 'TestCommandeFraicheurNExpansePasHOME|TestCommandeFraicheurEstFailClosed|TestCommandeFraicheurSansBinDirs|TestCommandeFraicheurRefuseUpdateVide' -v
```
Expected: **PASS** sur les quatre. Ne pas continuer sinon.

Puis écrire `internal/agent/testdata/fraicheur-claude.golden` avec exactement ce contenu :

```
set -uo pipefail

# su non-login : PATH minimal, on rétablit les bin_dirs de l'agent.
export PATH="$HOME/.local/bin:$HOME/.claude/local:$PATH"

if ! command -v claude >/dev/null 2>&1; then
  echo "agent claude : FATAL binaire claude introuvable (PATH=$PATH)" >&2
  exit 127
fi

tentative=1
while [ "$tentative" -le 3 ]; do
  if sortie="$(claude update 2>&1)"; then
    echo "agent claude : à jour"
    exit 0
  fi
  echo "agent claude : tentative ${tentative}/3 échouée :" >&2
  echo "$sortie" >&2
  if [ "$tentative" -lt 3 ]; then
    sleep 10
  fi
  tentative=$((tentative + 1))
done

echo "agent claude : FATAL mise à jour impossible après 3 tentatives (fail-closed)" >&2
exit 1
```

⚠️ Le fichier ne doit **pas** se terminer par une ligne vide surnuméraire : le script finit sur
`exit 1\n`. Si `go test` signale un écart de fin de fichier, corriger le **golden**, jamais le test.

- [ ] **Step 5: Vérifier que le script est du bash valide**

Le golden est du code qui tournera dans une VM ; une erreur de syntaxe ne se verrait qu'au boot.

Run:
```bash
cd /Users/polochon/Development/Pillow/den
bash -n internal/agent/testdata/fraicheur-claude.golden && echo "syntaxe bash OK"
```
Expected: `syntaxe bash OK`

- [ ] **Step 6: Lancer toute la suite**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime aucun fichier.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/
git commit -m "feat(agent): commande de fraicheur fail-closed avec PATH litteral (spec 9.1)"
```

---

## Task 3: `sbx.NomSandbox` / `DecomposeNom` — l'identité qui boucle

Le handoff §11.3 l'exige : l'identité doit boucler `den nest ls` → `den nest show <n>` → `den <n>`
→ nom de sandbox → `den ls`. Sans `--label`, **le nom porte tout**. Il doit donc être décomposable
sans ambiguïté et sans consulter d'état externe.

Pourquoi `.` et pas `-` comme séparateur : avec `-`, un nest nommé `mon-api` et un worktree `feat`
donnent `mon-api-feat`, indistinguable du nest `mon` avec le worktree `api-feat`. Le désambiguïser
exigerait un plus-long-préfixe contre la liste des nests déclarés — donc une sandbox deviendrait
inattribuable dès que son nest est supprimé. En interdisant `.` dans les deux composants, un
`strings.Cut(nom, ".")` est **exact et sans état**.

**Files:**
- Create: `internal/sbx/nom.go`
- Create: `internal/sbx/nom_test.go`
- Modify: `internal/config/nom.go` (ajout de `ValiderComposantSandbox`)
- Modify: `internal/config/nom_test.go`
- Modify: `internal/nest/nest.go` (`LoadNest` valide aussi le charset)

**Interfaces:**
- Consumes: `config.ValiderNom(genre, nom string) error` (Plan 1, inchangé).
- Produces:
  - `func config.ValiderComposantSandbox(genre, nom string) error` — impose `[A-Za-z0-9+-]+`.
  - `func sbx.NomSandbox(nest, worktree string) (string, error)` — `nest` ou `nest.worktree`.
  - `func sbx.DecomposeNom(nom string) (nest, worktree string)` — inverse exact ; worktree vaut
    `""` s'il n'y a pas de séparateur.

- [ ] **Step 1: Écrire le test qui échoue — le charset**

Ajouter à `internal/config/nom_test.go` :

```go
func TestValiderComposantSandbox(t *testing.T) {
	valides := []string{"api", "mon-api", "api2", "v1+beta", "A-B"}
	for _, nom := range valides {
		if err := ValiderComposantSandbox("nest", nom); err != nil {
			t.Errorf("%q doit être accepté, refusé avec : %v", nom, err)
		}
	}

	// Le point est réservé au séparateur <nest>.<worktree> ; l'underscore et le
	// slash sont refusés par `sbx create --name` lui-même.
	invalides := []string{"", "mon.api", "mon_api", "feature/123", "mon api", "café"}
	for _, nom := range invalides {
		if err := ValiderComposantSandbox("nest", nom); err == nil {
			t.Errorf("%q doit être refusé", nom)
		}
	}
}

// Le message doit nommer le caractère fautif : « invalide » sans dire quoi
// oblige l'utilisateur à deviner.
func TestValiderComposantSandboxMessageActionnable(t *testing.T) {
	err := ValiderComposantSandbox("worktree", "feature/123")
	if err == nil {
		t.Fatal("attendu une erreur")
	}
	for _, attendu := range []string{"worktree", "feature/123", "/"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit contenir %q ; obtenu : %v", attendu, err)
		}
	}
}
```

(Ajouter `"strings"` aux imports du fichier de test s'il n'y est pas déjà.)

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/config/ -run TestValiderComposantSandbox -v`
Expected: FAIL — `undefined: ValiderComposantSandbox`.

- [ ] **Step 3: Implémenter le charset**

Ajouter à `internal/config/nom.go` :

```go
// motifComposantSandbox : ce qu'un composant de nom de sandbox peut contenir.
//
// `sbx create --name` accepte « letters, numbers, hyphens, periods, plus signs
// and minus signs ». den est PLUS strict d'un cran : le point est exclu, parce
// qu'il sert de séparateur dans `<nest>.<worktree>` et que la décomposition doit
// rester exacte sans consulter la liste des nests déclarés.
var motifComposantSandbox = regexp.MustCompile(`^[A-Za-z0-9+-]+$`)

// ValiderComposantSandbox contrôle qu'un nom peut devenir un composant de nom de
// sandbox. Le message nomme le premier caractère fautif : « nom invalide » seul
// force l'utilisateur à deviner lequel.
func ValiderComposantSandbox(genre, nom string) error {
	if nom == "" {
		return fmt.Errorf("%s : le nom est vide", genre)
	}
	if motifComposantSandbox.MatchString(nom) {
		return nil
	}
	for _, r := range nom {
		if !strings.ContainsRune(
			"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+-", r) {
			return fmt.Errorf(
				"%s %q : le caractère %q est interdit — ce nom devient un nom de sandbox, "+
					"limité à lettres, chiffres, « - » et « + » (le « . » est réservé au "+
					"séparateur <nest>.<worktree>)", genre, nom, string(r))
		}
	}
	return fmt.Errorf("%s %q : nom invalide", genre, nom)
}
```

Ajouter `"regexp"` aux imports de `internal/config/nom.go`.

- [ ] **Step 4: Lancer le test — il doit passer**

Run: `go test ./internal/config/ -run TestValiderComposantSandbox -v`
Expected: PASS

- [ ] **Step 5: Écrire le test qui échoue — nom de sandbox**

Créer `internal/sbx/nom_test.go` :

```go
package sbx

import "testing"

func TestNomSandbox(t *testing.T) {
	cas := []struct {
		nest, worktree, attendu string
	}{
		{"api", "", "api"},
		{"api", "feat12", "api.feat12"},
		{"mon-api", "feat", "mon-api.feat"},
	}
	for _, c := range cas {
		got, err := NomSandbox(c.nest, c.worktree)
		if err != nil {
			t.Errorf("NomSandbox(%q,%q) : erreur inattendue %v", c.nest, c.worktree, err)
			continue
		}
		if got != c.attendu {
			t.Errorf("NomSandbox(%q,%q) = %q, attendu %q", c.nest, c.worktree, got, c.attendu)
		}
	}
}

func TestNomSandboxRefuseComposantsIllegaux(t *testing.T) {
	cas := []struct{ nest, worktree string }{
		{"mon.api", "feat"},     // point dans le nest
		{"api", "feature/123"},  // slash dans le worktree (cas réel : nom de branche)
		{"api", "feat.12"},      // point dans le worktree
		{"", "feat"},            // nest vide
	}
	for _, c := range cas {
		if _, err := NomSandbox(c.nest, c.worktree); err == nil {
			t.Errorf("NomSandbox(%q,%q) doit échouer", c.nest, c.worktree)
		}
	}
}

// L'aller-retour est l'invariant central : sans --label, le nom EST l'état.
func TestDecomposeNomEstLInverseExact(t *testing.T) {
	cas := []struct{ nest, worktree string }{
		{"api", ""},
		{"api", "feat12"},
		{"mon-api", "feat-2"},
		{"a+b", "c-d"},
	}
	for _, c := range cas {
		nom, err := NomSandbox(c.nest, c.worktree)
		if err != nil {
			t.Fatalf("NomSandbox(%q,%q) : %v", c.nest, c.worktree, err)
		}
		nest, wt := DecomposeNom(nom)
		if nest != c.nest || wt != c.worktree {
			t.Errorf("aller-retour de %q : obtenu (%q,%q), attendu (%q,%q)",
				nom, nest, wt, c.nest, c.worktree)
		}
	}
}

// Une sandbox créée à la main hors den ne doit pas faire paniquer la décomposition.
func TestDecomposeNomEtranger(t *testing.T) {
	nest, wt := DecomposeNom("sandbox-cree-a-la-main")
	if nest != "sandbox-cree-a-la-main" || wt != "" {
		t.Errorf("obtenu (%q,%q)", nest, wt)
	}
	// Deux points : seul le premier sépare, le reste appartient au worktree —
	// den ne produit jamais ça, mais den ls doit rester total.
	nest, wt = DecomposeNom("a.b.c")
	if nest != "a" || wt != "b.c" {
		t.Errorf("obtenu (%q,%q), attendu (a, b.c)", nest, wt)
	}
}
```

- [ ] **Step 6: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/sbx/ -run 'TestNomSandbox|TestDecomposeNom' -v`
Expected: FAIL — le paquet `internal/sbx` n'existe pas encore.

- [ ] **Step 7: Implémenter**

Créer `internal/sbx/nom.go` :

```go
// Package sbx pilote la CLI `sbx` : nommage des sandboxes, assemblage des
// arguments, exécution derrière une interface mockable.
package sbx

import (
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// SeparateurNom sépare le nest du worktree dans un nom de sandbox.
//
// `sbx create --name` autorise le point, et den l'interdit dans les noms de nest
// et de worktree : la décomposition est donc EXACTE, sans consulter la liste des
// nests. Avec un « - » comme séparateur, `mon-api-feat` serait ambigu (nest
// `mon-api`+wt `feat`, ou nest `mon`+wt `api-feat`) et il faudrait un
// plus-long-préfixe contre les nests déclarés — une sandbox deviendrait
// inattribuable dès la suppression de son nest.
const SeparateurNom = "."

// NomSandbox construit le nom de sandbox d'un nest, éventuellement worktreeé.
// Ce nom est l'unique porteur d'état de den : `--label` n'existe pas dans sbx.
func NomSandbox(nest, worktree string) (string, error) {
	if err := config.ValiderComposantSandbox("nest", nest); err != nil {
		return "", err
	}
	if worktree == "" {
		return nest, nil
	}
	if err := config.ValiderComposantSandbox("worktree", worktree); err != nil {
		return "", err
	}
	return nest + SeparateurNom + worktree, nil
}

// DecomposeNom est l'inverse de NomSandbox. Fonction TOTALE : elle ne valide
// rien et n'échoue jamais, parce qu'elle s'applique aussi aux sandboxes créées
// hors den que `sbx ls` remonte. Un nom sans séparateur est un nest sans
// worktree.
func DecomposeNom(nom string) (nest, worktree string) {
	nest, worktree, _ = strings.Cut(nom, SeparateurNom)
	return nest, worktree
}
```

- [ ] **Step 8: Lancer le test — il doit passer**

Run: `go test ./internal/sbx/ -v`
Expected: PASS

- [ ] **Step 9: Faire valider le nom des nests au chargement**

Un nest dont le nom ne peut pas devenir un nom de sandbox doit être refusé **au chargement**, pas
au moment du spawn : `den nest ls` doit déjà le signaler.

Ajouter le test à `internal/nest/nest_test.go` :

```go
func TestLoadNestRefuseUnNomNonSandboxable(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "mon_api", "stack: devx\nrepos: []\n")

	if _, err := LoadNest(denHome, "mon_api"); err == nil {
		t.Fatal("un nom de nest non convertible en nom de sandbox doit être refusé au chargement")
	}
}
```

(`ecrisNest` est le helper existant du paquet ; s'il porte un autre nom, réutiliser celui du
fichier — ne pas en créer un second.)

Puis, dans `internal/nest/nest.go`, à l'intérieur de `LoadNest`, juste après l'appel existant à
`config.ValiderNom("nest", name)` :

```go
	// Le nom d'un nest devient un nom de sandbox (sbx n'a pas de --label) : le
	// refuser ici plutôt qu'au spawn fait remonter le problème dès `den nest ls`.
	if err := config.ValiderComposantSandbox("nest", name); err != nil {
		return nil, err
	}
```

- [ ] **Step 10: Vérifier que l'exemple livré reste chargeable**

`examples/den-home/nests/exemple.yaml` porte le nom `exemple` — conforme. Le test d'exemple du
Plan 1 doit continuer à passer.

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 11: Commit**

```bash
git add internal/sbx/ internal/config/nom.go internal/config/nom_test.go internal/nest/nest.go internal/nest/nest_test.go
git commit -m "feat(sbx): nom de sandbox <nest>.<worktree> decomposable sans etat"
```

---

## Task 4: `Resolved.DenHome` + `Resolved.Env` fusionné

Handoff §11.2 : `Resolved` prétend en commentaire qu'il n'y a « plus rien à recalculer », et c'est
faux sur deux points. L'env n'est ni fusionné ni substitué (`Resolved` expose `Agent.Env` avec
`{config_dir}` encore littéral, et `Nest.Env` séparément), et `denHome` n'y figure pas alors que le
mixin devra s'écrire sous `~/.den/`. Les corriger **maintenant** évite de repasser la valeur en
paramètre dans quatre modules.

La substitution de `{config_dir}` est une **règle de cascade**, pas une règle d'affichage : sa place
est dans `nest.Resolve`.

**Files:**
- Modify: `internal/nest/resolve.go`
- Modify: `internal/nest/resolve_test.go`
- Modify: `internal/cli/nest.go` (appelant de `Resolve` + affichage de l'env fusionné)
- Modify: `internal/cli/nest_test.go`

**Interfaces:**
- Consumes: `config.Global`, `config.Stack`, `nest.Nest` (Plan 1).
- Produces: signature **changée** —
  `func Resolve(denHome string, g *config.Global, stacks map[string]*config.Stack, n *Nest, o Options) (*Resolved, error)`
  et `Resolved` gagne `DenHome string` et `Env map[string]string`.

- [ ] **Step 1: Écrire le test qui échoue**

Ajouter à `internal/nest/resolve_test.go` :

```go
func TestResolveFusionneEtSubstitueLEnv(t *testing.T) {
	g := &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/home/moi/.den/agents/claude",
				Env:       map[string]string{"CLAUDE_CONFIG_DIR": "{config_dir}"},
				Update:    "claude update",
			},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
		WorktreeRoot:   "/home/moi/.den/worktrees",
	}
	stacks := map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1", Dir: "/d/stacks/devx"}}
	n := &Nest{Name: "api", Stack: "devx", Env: map[string]string{"SOME_VAR": "value"}}

	r, err := Resolve("/home/moi/.den", g, stacks, n, Options{})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if r.DenHome != "/home/moi/.den" {
		t.Errorf("DenHome = %q, attendu /home/moi/.den", r.DenHome)
	}
	// {config_dir} doit être résolu : le mixin ne sait pas le faire, et le
	// chemin visé est un chemin HÔTE (sbx monte au même chemin dans la VM).
	if got := r.Env["CLAUDE_CONFIG_DIR"]; got != "/home/moi/.den/agents/claude" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, attendu le config_dir résolu", got)
	}
	if got := r.Env["SOME_VAR"]; got != "value" {
		t.Errorf("SOME_VAR = %q, attendu value", got)
	}
}

// Cascade : global ← stack ← nest ← flags. Le nest gagne sur l'agent.
func TestResolveEnvDuNestGagneSurCelleDeLAgent(t *testing.T) {
	g := &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/profil",
				Env:       map[string]string{"PARTAGEE": "agent", "PROPRE": "agent"},
				Update:    "claude update",
			},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
	}
	stacks := map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}
	n := &Nest{Name: "api", Stack: "devx", Env: map[string]string{"PARTAGEE": "nest"}}

	r, err := Resolve("/d", g, stacks, n, Options{})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if r.Env["PARTAGEE"] != "nest" {
		t.Errorf("PARTAGEE = %q, attendu nest (le nest est plus bas dans la cascade)", r.Env["PARTAGEE"])
	}
	if r.Env["PROPRE"] != "agent" {
		t.Errorf("PROPRE = %q, attendu agent", r.Env["PROPRE"])
	}
}

// L'override de config_dir par nest doit se propager DANS l'env substitué,
// sinon la VM pointerait sur le profil partagé alors que le nest a demandé
// l'isolation.
func TestResolveSubstitueLOverrideDeConfigDirDuNest(t *testing.T) {
	g := &config.Global{
		Agents: map[string]config.Agent{
			"claude": {
				ConfigDir: "/profil/partage",
				Env:       map[string]string{"CLAUDE_CONFIG_DIR": "{config_dir}"},
				Update:    "claude update",
			},
		},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
	}
	stacks := map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}
	n := &Nest{Name: "api", Stack: "devx", Agents: map[string]string{"claude": "/profil/isole"}}

	r, err := Resolve("/d", g, stacks, n, Options{})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if r.Env["CLAUDE_CONFIG_DIR"] != "/profil/isole" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, attendu /profil/isole", r.Env["CLAUDE_CONFIG_DIR"])
	}
}

func TestResolveEnvJamaisNil(t *testing.T) {
	g := &config.Global{
		Agents:         map[string]config.Agent{"claude": {ConfigDir: "/p", Update: "u"}},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
	}
	stacks := map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}
	r, err := Resolve("/d", g, stacks, &Nest{Name: "api", Stack: "devx"}, Options{})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if r.Env == nil {
		t.Error("Env doit être une map vide, jamais nil : le mixin itère dessus sans garde")
	}
}
```

Les appels existants à `Resolve` dans ce fichier de test prennent désormais un premier argument
`denHome` : les mettre à jour (`Resolve("/d", g, stacks, n, opts)`).

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/nest/ -v`
Expected: FAIL — `too many arguments in call to Resolve` puis `r.Env undefined`.

- [ ] **Step 3: Implémenter**

Dans `internal/nest/resolve.go`, remplacer la structure `Resolved` et la fonction `Resolve` :

```go
// jetonConfigDir est le marqueur substitué dans les valeurs d'env de l'agent.
// Il vise un chemin HÔTE : sbx monte chaque workspace au MÊME chemin absolu
// dans la VM, donc le chemin hôte du profil est aussi son chemin in-VM.
const jetonConfigDir = "{config_dir}"

// Resolved est un nest entièrement résolu : plus rien à recalculer en aval.
type Resolved struct {
	DenHome string // le mixin généré s'écrit sous <DenHome>/cache/mixins/

	Nest  *Nest
	Stack *config.Stack

	AgentName      string
	Agent          config.Agent
	AgentConfigDir string // override nest s'il existe, sinon registre global

	// Env est l'union PRÊTE À POSER : env de l'agent (avec {config_dir} déjà
	// substitué) ∪ env du nest, le nest gagnant. La substitution est une règle
	// de cascade, pas d'affichage : elle appartient ici, pas au mixin.
	Env map[string]string

	Egress []string // union triée baseline ∪ stack ∪ nest
	Repos  []Repo   // sélection appliquée, ordre de déclaration

	SSHMode        string
	SSHDir         string
	WorktreeLayout string
	WorktreeRoot   string
}

// fusionneEnv applique la cascade agent ← nest et substitue {config_dir}.
// Renvoie toujours une map non-nil : les consommateurs itèrent sans garde.
func fusionneEnv(agentEnv, nestEnv map[string]string, configDir string) map[string]string {
	out := make(map[string]string, len(agentEnv)+len(nestEnv))
	for k, v := range agentEnv {
		out[k] = strings.ReplaceAll(v, jetonConfigDir, configDir)
	}
	for k, v := range nestEnv {
		out[k] = v // le nest est plus bas dans la cascade : il gagne
	}
	return out
}

// Resolve applique la cascade complète global ← stack ← nest ← flags.
func Resolve(denHome string, g *config.Global, stacks map[string]*config.Stack, n *Nest, o Options) (*Resolved, error) {
	nomStack := n.Stack
	if nomStack == "" {
		nomStack = g.Defaults.Stack
	}
	s, ok := stacks[nomStack]
	if !ok {
		dispos := slices.Sorted(maps.Keys(stacks))
		return nil, fmt.Errorf(
			"nest %q : stack %q introuvable dans %s/stacks (stacks déclarées : %v)",
			n.Name, nomStack, denHome, dispos)
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
		DenHome:        denHome,
		Nest:           n,
		Stack:          s,
		AgentName:      nomAgent,
		Agent:          agent,
		AgentConfigDir: configDir,
		Env:            fusionneEnv(agent.Env, n.Env, configDir),
		Egress:         UnionEgress(g.Egress, s.Egress, n.Egress),
		Repos:          repos,
		SSHMode:        g.SSH.Mode,
		SSHDir:         g.SSH.Dir,
		WorktreeLayout: g.WorktreeLayout,
		WorktreeRoot:   g.WorktreeRoot,
	}, nil
}
```

Ajouter `"strings"` aux imports de `internal/nest/resolve.go`.

- [ ] **Step 4: Mettre à jour l'appelant CLI**

Dans `internal/cli/nest.go`, `newNestShowCmd` : remplacer `nest.Resolve(g, stacks, n, opts)` par
`nest.Resolve(home, g, stacks, n, opts)`.

Dans `ecrisResolution`, l'env affiché doit devenir l'env **résolu** — c'est celui qui partira dans
la VM, et l'afficher non substitué a été la source du doute qui a motivé cette tâche. Remplacer le
bloc `if len(r.Nest.Env) > 0 { … }` par :

```go
	if len(r.Env) > 0 {
		fmt.Fprintln(w, "env (résolu):")
		// L'ordre d'itération des maps Go n'est pas déterministe : tout ce qui
		// s'affiche est trié.
		for _, k := range slices.Sorted(maps.Keys(r.Env)) {
			fmt.Fprintf(w, "  %s=%s\n", k, r.Env[k])
		}
	}
```

- [ ] **Step 5: Ajouter le test CLI correspondant**

Ajouter à `internal/cli/nest_test.go` un test qui vérifie que `den nest show` affiche l'env
substitué. Le fichier possède déjà des helpers d'écriture de `~/.den` temporaire ; les réutiliser.

```go
func TestNestShowAfficheLEnvSubstitue(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, `agents:
  claude:
    config_dir: /profil/claude
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	sortie, err := executeCmd(t, "--den-home", denHome, "nest", "show", "api")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(sortie, "CLAUDE_CONFIG_DIR=/profil/claude") {
		t.Errorf("l'env affiché doit être substitué ; obtenu :\n%s", sortie)
	}
	if strings.Contains(sortie, "{config_dir}") {
		t.Errorf("le jeton {config_dir} ne doit jamais s'afficher ; obtenu :\n%s", sortie)
	}
}
```

⚠️ Les helpers (`ecrisConfig`, `ecrisStack`, `ecrisNest`, `executeCmd`) existent déjà dans le paquet
`cli` sous **un nom donné par le Plan 1**. Ouvrir `internal/cli/nest_test.go` et réutiliser les noms
réels ; ne pas en créer de nouveaux.

- [ ] **Step 6: Lancer les tests — ils doivent passer**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 7: Commit**

```bash
git add internal/nest/ internal/cli/
git commit -m "feat(nest): Resolved porte denHome et l'env fusionne avec {config_dir} substitue"
```

---

## Task 5: `Stack.Kits` — les kits transverses

Le `run.sh` réel layere quatre kits avant le mixin : `policy-baseline`, `ssh-known-hosts`,
`stacks/devx/kit`, `stacks/dgdevx/kit`. Le modèle den n'en absorbe qu'un (`kit`). `policy-baseline`
disparaît (son contenu **est** `egress:` dans `config.yaml`), mais `ssh-known-hosts` n'a pas
d'équivalent déclaratif : c'est une `commands.startup`. D'où `kits:` dans `stack.yaml`.

**Files:**
- Modify: `internal/config/stack.go`
- Modify: `internal/config/stack_test.go`
- Modify: `examples/den-home/stacks/devx/stack.yaml`

**Interfaces:**
- Consumes: `config.Stack` (Plan 1).
- Produces: `Stack.Kits []string` — chemins **absolus** après chargement, ordre de déclaration
  préservé (c'est un ordre de layering).

- [ ] **Step 1: Écrire le test qui échoue**

Ajouter à `internal/config/stack_test.go` :

```go
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
```

(`ecrisStack` est le helper existant du paquet — réutiliser son nom réel. Ajouter `"path/filepath"`
aux imports si absent.)

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/config/ -run TestLoadStack -v`
Expected: FAIL — `s.Kits undefined`, et pour le premier cas une erreur de décodage strict
`clé inconnue "kits"` (ce qui prouve au passage que le décodage strict du Plan 1 fonctionne).

- [ ] **Step 3: Implémenter**

Dans `internal/config/stack.go`, ajouter le champ à la structure `Stack`, juste après `Kit` :

```go
	// Kits : kits transverses layerés AVANT Kit (ex. ssh-known-hosts).
	// Relatifs au dossier de la stack dans le YAML, absolus après chargement.
	// L'ORDRE EST SIGNIFIANT : c'est un ordre de layering sbx, pas un ensemble —
	// ne jamais le trier.
	Kits []string `yaml:"kits"`
```

Puis, dans `LoadStack`, juste après le bloc qui absolutise `s.Kit` :

```go
	for i, k := range s.Kits {
		if k != "" && !filepath.IsAbs(k) {
			s.Kits[i] = filepath.Join(dir, k)
		}
	}
```

- [ ] **Step 4: Lancer le test — il doit passer**

Run: `go test ./internal/config/ -run TestLoadStack -v`
Expected: PASS

- [ ] **Step 5: Documenter la clé dans l'exemple livré**

Remplacer `examples/den-home/stacks/devx/stack.yaml` par :

```yaml
image: devx:v1
# kit: ./kit          # kit propre à la stack (env + egress toolchain)
# kits:               # kits transverses, layerés AVANT `kit`. L'ordre compte.
#   - ../../kits/ssh-known-hosts
```

⚠️ Vérifier d'abord le contenu actuel du fichier : s'il déclare déjà `kit:` ou `parent:`, conserver
ces lignes et n'ajouter que le commentaire `kits:`. Le test d'exemple du Plan 1 charge ce dossier —
il doit continuer à passer.

- [ ] **Step 6: Lancer toute la suite**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 7: Commit**

```bash
git add internal/config/ examples/
git commit -m "feat(config): kits transverses par stack, ordre de layering preserve"
```

---

## Task 6: `sbx.Runner` + `sbx.Fake`

Tout accès à la CLI `sbx` passe par cette interface. `sbx` n'étant pas installé sur la machine de
développement, **c'est le seul point de contact avec le monde** pour les tâches 8, 11, 12, 13, 14
et 15 : sa qualité conditionne leur testabilité.

Deux méthodes et pas une seule, parce que les deux usages sont irréconciliables : `Run` capture la
sortie pour la parser (`ls --json`, `policy check --json`), `Attach` branche les tty du processus
courant pour donner un shell interactif à l'utilisateur (`exec -it … bash -l`).

**Files:**
- Create: `internal/sbx/runner.go`
- Create: `internal/sbx/fake.go`
- Create: `internal/sbx/fake_test.go`

**Interfaces:**
- Consumes: rien.
- Produces:
  - `type Runner interface { Run(ctx context.Context, args ...string) ([]byte, error); Attach(ctx context.Context, args ...string) error }`
  - `func NewExec(bin string) Runner` — `bin` vide ⇒ `"sbx"`.
  - `type Fake struct { Appels [][]string; Reponses map[string]Reponse; Defaut Reponse; ErreurAttach error }`
  - `type Reponse struct { Sortie []byte; Err error }`
  - `func (f *Fake) DernierAppel() []string`
  - `func (f *Fake) AAppele(prefixe ...string) bool`

- [ ] **Step 1: Écrire le test qui échoue**

Créer `internal/sbx/fake_test.go` :

```go
package sbx

import (
	"context"
	"errors"
	"testing"
)

func TestFakeEnregistreLesAppels(t *testing.T) {
	f := &Fake{}
	if _, err := f.Run(context.Background(), "ls", "--json"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if _, err := f.Run(context.Background(), "rm", "--force", "api"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if len(f.Appels) != 2 {
		t.Fatalf("Appels = %v, attendu 2 appels", f.Appels)
	}
	if !f.AAppele("rm", "--force") {
		t.Errorf("AAppele(rm --force) doit être vrai ; appels : %v", f.Appels)
	}
	if !f.AAppele("ls") {
		t.Errorf("AAppele(ls) doit être vrai ; appels : %v", f.Appels)
	}
	if f.AAppele("create") {
		t.Errorf("AAppele(create) doit être faux ; appels : %v", f.Appels)
	}
	if got := f.DernierAppel(); len(got) != 3 || got[0] != "rm" {
		t.Errorf("DernierAppel = %v", got)
	}
}

func TestFakeReponseScriptee(t *testing.T) {
	attendue := []byte(`{"sandboxes":[]}`)
	f := &Fake{Reponses: map[string]Reponse{
		"ls --json": {Sortie: attendue},
	}}

	got, err := f.Run(context.Background(), "ls", "--json")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if string(got) != string(attendue) {
		t.Errorf("sortie = %q, attendu %q", got, attendue)
	}
}

func TestFakeReponseParDefaut(t *testing.T) {
	sentinelle := errors.New("boom")
	f := &Fake{Defaut: Reponse{Err: sentinelle}}

	if _, err := f.Run(context.Background(), "n-importe", "quoi"); !errors.Is(err, sentinelle) {
		t.Errorf("err = %v, attendu la sentinelle par défaut", err)
	}
}

// Attach est enregistré comme un appel : les tests de `den <nest>` doivent
// pouvoir asserter QUE l'attache a eu lieu, et avec quels arguments.
func TestFakeAttachEstEnregistre(t *testing.T) {
	f := &Fake{}
	if err := f.Attach(context.Background(), "exec", "-it", "api", "bash", "-l"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !f.AAppele("exec", "-it", "api") {
		t.Errorf("l'attache doit être enregistrée ; appels : %v", f.Appels)
	}
}

func TestFakeAttachPeutEchouer(t *testing.T) {
	sentinelle := errors.New("tty indisponible")
	f := &Fake{ErreurAttach: sentinelle}
	if err := f.Attach(context.Background(), "exec", "-it", "api"); !errors.Is(err, sentinelle) {
		t.Errorf("err = %v, attendu la sentinelle", err)
	}
}

// Garde-fou de compilation : Fake doit rester substituable à Runner.
var _ Runner = (*Fake)(nil)
```

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/sbx/ -run TestFake -v`
Expected: FAIL — `undefined: Fake`, `undefined: Runner`, `undefined: Reponse`.

- [ ] **Step 3: Implémenter le Runner réel**

Créer `internal/sbx/runner.go` :

```go
package sbx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner est le SEUL point de contact de den avec la CLI sbx. Tout passe par
// là, ce qui rend l'intégralité du reste testable sans microVM — sbx n'est même
// pas installé sur la machine de développement.
//
// Deux méthodes, parce que les deux usages sont irréconciliables :
//   - Run capture stdout pour le parser (`ls --json`, `policy check --json`).
//   - Attach branche les tty du processus courant pour rendre un shell
//     interactif à l'utilisateur (`exec -it … bash -l`) ; il n'y a rien à
//     capturer, et capturer casserait l'interactivité.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
	Attach(ctx context.Context, args ...string) error
}

// Exec est l'implémentation réelle, adossée au binaire sbx du PATH.
type Exec struct {
	Bin string
}

// NewExec construit un Runner réel. bin vide ⇒ « sbx » (résolu via le PATH).
func NewExec(bin string) Runner {
	if bin == "" {
		bin = "sbx"
	}
	return &Exec{Bin: bin}
}

// Run exécute sbx et renvoie stdout. En cas d'échec, stderr est INTÉGRÉ au
// message : sbx y met le diagnostic utile, et une erreur « exit status 1 » nue
// est inexploitable pour l'utilisateur comme pour le mainteneur.
func (e *Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("%s %s : %s", e.Bin, strings.Join(args, " "), detail)
	}
	return stdout.Bytes(), nil
}

// Attach donne la main à sbx sur les tty du processus courant.
func (e *Exec) Attach(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s : %w", e.Bin, strings.Join(args, " "), err)
	}
	return nil
}
```

- [ ] **Step 4: Implémenter le Fake**

Créer `internal/sbx/fake.go` :

```go
package sbx

import (
	"context"
	"slices"
	"strings"
)

// Reponse est ce que le Fake renvoie pour un appel donné.
type Reponse struct {
	Sortie []byte
	Err    error
}

// Fake est le double de test de Runner.
//
// Il vit dans le paquet de production et non dans un `_test.go` À DESSEIN :
// policy, cli et agent en ont tous besoin, et un double par paquet dériverait
// aussitôt du contrat réel. `internal/` en borne déjà la portée.
type Fake struct {
	// Appels enregistre chaque invocation, Run et Attach confondues, dans
	// l'ordre. C'est sur lui que portent les assertions.
	Appels [][]string

	// Reponses associe une réponse à un appel exact, clé = args joints par un
	// espace (ex. "ls --json").
	Reponses map[string]Reponse

	// Defaut sert quand aucune entrée de Reponses ne correspond.
	Defaut Reponse

	// ErreurAttach est renvoyée par Attach. Le fait que l'attache ait eu lieu
	// reste enregistré dans Appels même si elle échoue.
	ErreurAttach error
}

func (f *Fake) Run(_ context.Context, args ...string) ([]byte, error) {
	f.Appels = append(f.Appels, slices.Clone(args))
	if r, ok := f.Reponses[strings.Join(args, " ")]; ok {
		return r.Sortie, r.Err
	}
	return f.Defaut.Sortie, f.Defaut.Err
}

func (f *Fake) Attach(_ context.Context, args ...string) error {
	f.Appels = append(f.Appels, slices.Clone(args))
	return f.ErreurAttach
}

// DernierAppel renvoie le dernier appel enregistré, ou nil s'il n'y en a aucun.
func (f *Fake) DernierAppel() []string {
	if len(f.Appels) == 0 {
		return nil
	}
	return f.Appels[len(f.Appels)-1]
}

// AAppele indique si un appel a commencé par ce préfixe d'arguments. Assertion
// par préfixe et non par égalité : un test qui vérifie « on a bien fait un
// create » ne doit pas casser parce qu'un chemin de plus a été monté.
func (f *Fake) AAppele(prefixe ...string) bool {
	for _, a := range f.Appels {
		if len(a) >= len(prefixe) && slices.Equal(a[:len(prefixe)], prefixe) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Lancer le test — il doit passer**

Run: `go test ./internal/sbx/ -v`
Expected: PASS sur tous les tests du paquet (ceux de la tâche 3 inclus).

- [ ] **Step 6: Lancer toute la suite**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 7: Commit**

```bash
git add internal/sbx/
git commit -m "feat(sbx): interface Runner (Run/Attach) et double de test Fake"
```

---

## Task 7: `agent.RendMixin` / `EcrisMixin` — le kit jetable

Le mécanisme clé du plan (handoff §3). À chaque spawn, den génère **un seul** kit portant l'env
résolu, l'egress de la cascade en `caps.network.allow`, et en **dernière** startup command la
commande de fraîcheur de la tâche 2. Il remplace le `mktemp` manuel du `run.sh` actuel.

Le rendu est fait à la main via `yaml.Node` plutôt que par `yaml.Marshal` d'une map : l'ordre
d'itération des maps Go est aléatoire, et un golden file ne tolère pas l'aléatoire. Les clés de map
(`environment.variables`) sont émises **triées**, conformément à la convention du dépôt.

**Files:**
- Create: `internal/agent/mixin.go`
- Create: `internal/agent/mixin_test.go`
- Create: `internal/agent/testdata/mixin-complet.golden`

**Interfaces:**
- Consumes: `CommandeFraicheur` (tâche 2), `nest.Resolved` (tâche 4), `sbx.NomSandbox` (tâche 3).
- Produces:
  - `type Mixin struct { NomSandbox string; Env map[string]string; Egress []string; Fraicheur []string }`
  - `func RendMixin(m Mixin) ([]byte, error)` — YAML déterministe.
  - `func MixinDepuis(r *nest.Resolved, nomSandbox string) (Mixin, error)` — assemble depuis un
    nest résolu, en appelant `CommandeFraicheur`.
  - `func EcrisMixin(denHome, nomSandbox string, m Mixin) (string, error)` — écrit
    `<denHome>/cache/mixins/<nomSandbox>/spec.yaml` et renvoie le **dossier** (c'est lui qu'attend
    `--kit`).

- [ ] **Step 1: Écrire le test qui échoue**

Créer `internal/agent/mixin_test.go` :

```go
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
)

func mixinExemple(t *testing.T) Mixin {
	t.Helper()
	fraicheur, err := CommandeFraicheur("claude", agentClaude())
	if err != nil {
		t.Fatalf("CommandeFraicheur : %v", err)
	}
	return Mixin{
		NomSandbox: "api.feat12",
		Env: map[string]string{
			"CLAUDE_CONFIG_DIR": "/home/moi/.den/agents/claude",
			"SOME_VAR":          "value",
		},
		Egress:    []string{"api.anthropic.com", "github.com"},
		Fraicheur: fraicheur,
	}
}

func TestRendMixinPorteLesTroisCharges(t *testing.T) {
	out, err := RendMixin(mixinExemple(t))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	rendu := string(out)

	// Le schéma RÉEL de sbx : caps.network.allow et environment.variables.
	// Le spec d'origine écrivait network.allow et env — c'était faux.
	for _, attendu := range []string{
		"schemaVersion: 2",
		"kind: mixin",
		"caps:",
		"network:",
		"allow:",
		"- api.anthropic.com",
		"environment:",
		"variables:",
		"CLAUDE_CONFIG_DIR: /home/moi/.den/agents/claude",
		"SOME_VAR: value",
		"commands:",
		"startup:",
	} {
		if !strings.Contains(rendu, attendu) {
			t.Errorf("le mixin doit contenir %q ; obtenu :\n%s", attendu, rendu)
		}
	}
}

// La fraîcheur est fail-closed et le dispatcher sbx sort au premier échec :
// elle doit être la DERNIÈRE startup command du DERNIER kit.
func TestRendMixinMetLaFraicheurEnDerniereStartup(t *testing.T) {
	m := mixinExemple(t)
	out, err := RendMixin(m)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	rendu := string(out)

	iStartup := strings.Index(rendu, "startup:")
	iUpdate := strings.Index(rendu, "claude update")
	if iStartup < 0 || iUpdate < 0 || iUpdate < iStartup {
		t.Fatalf("la commande de fraîcheur doit apparaître sous commands.startup ; obtenu :\n%s", rendu)
	}
	// Et le $HOME des bin_dirs traverse intact jusque dans le YAML.
	if !strings.Contains(rendu, "$HOME/.local/bin") {
		t.Errorf("le $HOME des bin_dirs doit survivre au rendu YAML ; obtenu :\n%s", rendu)
	}
}

// Déterminisme : deux rendus successifs doivent être identiques, sinon le
// golden file est un piège à faux positifs et le mixin change à chaque spawn.
func TestRendMixinEstDeterministe(t *testing.T) {
	m := mixinExemple(t)
	for i := 0; i < 20; i++ {
		a, err := RendMixin(m)
		if err != nil {
			t.Fatalf("erreur inattendue : %v", err)
		}
		b, err := RendMixin(m)
		if err != nil {
			t.Fatalf("erreur inattendue : %v", err)
		}
		if string(a) != string(b) {
			t.Fatalf("rendu non déterministe à l'itération %d :\n%s\n---\n%s", i, a, b)
		}
	}
}

// Le nom d'un kit ne peut pas porter le séparateur de nom de sandbox.
func TestRendMixinNommeLeKitSansPoint(t *testing.T) {
	out, err := RendMixin(mixinExemple(t))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(string(out), "name: den-api-feat12") {
		t.Errorf("le nom du kit doit être den-api-feat12 ; obtenu :\n%s", out)
	}
}

// Un nest sans egress ni env ne doit pas produire de sections vides : une
// `allow: []` vide vaut « rien d'autorisé » et non « pas de contrainte ».
func TestRendMixinOmetLesSectionsVides(t *testing.T) {
	fraicheur, err := CommandeFraicheur("claude", agentClaude())
	if err != nil {
		t.Fatalf("CommandeFraicheur : %v", err)
	}
	out, err := RendMixin(Mixin{NomSandbox: "api", Fraicheur: fraicheur})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	rendu := string(out)
	if strings.Contains(rendu, "caps:") {
		t.Errorf("aucun egress ⇒ pas de section caps ; obtenu :\n%s", rendu)
	}
	if strings.Contains(rendu, "environment:") {
		t.Errorf("aucune variable ⇒ pas de section environment ; obtenu :\n%s", rendu)
	}
}

func TestMixinDepuisAssembleLeNestResolu(t *testing.T) {
	g := &config.Global{
		Agents:         map[string]config.Agent{"claude": agentClaude()},
		Defaults:       config.Defaults{Agent: "claude", Stack: "devx"},
		SSH:            config.SSH{Mode: "agent-forward"},
		WorktreeLayout: "central",
		Egress:         []string{"github.com"},
	}
	stacks := map[string]*config.Stack{"devx": {Name: "devx", Image: "devx:v1"}}
	n := &nest.Nest{Name: "api", Stack: "devx", Egress: []string{"10.22.11.54:27017"}}

	r, err := nest.Resolve("/home/moi/.den", g, stacks, n, nest.Options{})
	if err != nil {
		t.Fatalf("Resolve : %v", err)
	}

	m, err := MixinDepuis(r, "api")
	if err != nil {
		t.Fatalf("MixinDepuis : %v", err)
	}
	if m.NomSandbox != "api" {
		t.Errorf("NomSandbox = %q", m.NomSandbox)
	}
	// L'egress vient de la cascade, déjà unionné et trié par nest.Resolve.
	if len(m.Egress) != 2 || m.Egress[0] != "10.22.11.54:27017" || m.Egress[1] != "github.com" {
		t.Errorf("Egress = %v, attendu la cascade unionnée et triée", m.Egress)
	}
	if m.Env["CLAUDE_CONFIG_DIR"] != "/home/moi/.den/agents/claude" {
		t.Errorf("Env = %v, {config_dir} doit être substitué", m.Env)
	}
	if len(m.Fraicheur) != 3 {
		t.Errorf("Fraicheur = %v, attendu un argv [bash -c script]", m.Fraicheur)
	}
}

func TestEcrisMixinEcritSousCache(t *testing.T) {
	denHome := t.TempDir()
	dir, err := EcrisMixin(denHome, "api.feat12", mixinExemple(t))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	attendu := filepath.Join(denHome, "cache", "mixins", "api.feat12")
	if dir != attendu {
		t.Errorf("dir = %q, attendu %q", dir, attendu)
	}
	// C'est le DOSSIER qu'on passe à `--kit`, et sbx y cherche spec.yaml.
	if _, err := os.Stat(filepath.Join(dir, "spec.yaml")); err != nil {
		t.Errorf("spec.yaml doit exister dans %s : %v", dir, err)
	}
}

// Un spawn répété doit réécrire, pas empiler : le mixin est reconstructible et
// reflète la config COURANTE, jamais celle du spawn précédent.
func TestEcrisMixinEstIdempotent(t *testing.T) {
	denHome := t.TempDir()
	m := mixinExemple(t)

	if _, err := EcrisMixin(denHome, "api", m); err != nil {
		t.Fatalf("premier écrit : %v", err)
	}
	m.Egress = []string{"nouveau.exemple.test"}
	dir, err := EcrisMixin(denHome, "api", m)
	if err != nil {
		t.Fatalf("second écrit : %v", err)
	}

	contenu, err := os.ReadFile(filepath.Join(dir, "spec.yaml"))
	if err != nil {
		t.Fatalf("lecture : %v", err)
	}
	if !strings.Contains(string(contenu), "nouveau.exemple.test") {
		t.Errorf("le second écrit doit remplacer le premier ; obtenu :\n%s", contenu)
	}
	if strings.Contains(string(contenu), "api.anthropic.com") {
		t.Errorf("le contenu du premier écrit ne doit pas survivre ; obtenu :\n%s", contenu)
	}
}

func TestRendMixinGolden(t *testing.T) {
	out, err := RendMixin(mixinExemple(t))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	chemin := filepath.Join("testdata", "mixin-complet.golden")
	attendu, err := os.ReadFile(chemin)
	if err != nil {
		t.Fatalf("lecture du golden : %v", err)
	}
	if string(out) != string(attendu) {
		t.Errorf("rendu != %s\n--- obtenu ---\n%s\n--- attendu ---\n%s", chemin, out, attendu)
	}
}
```

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/agent/ -run 'TestRendMixin|TestMixinDepuis|TestEcrisMixin' -v`
Expected: FAIL — `undefined: Mixin`, `undefined: RendMixin`.

- [ ] **Step 3: Implémenter**

Créer `internal/agent/mixin.go` :

```go
package agent

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/PillowPillow/den/internal/nest"
	"gopkg.in/yaml.v3"
)

// versionMixin est figée : le mixin est régénéré à chaque spawn et n'a pas de
// cycle de vie propre. Une version variable ferait diverger les golden files
// sans rien apporter.
const versionMixin = "0.0.0"

// Mixin est le kit jetable généré par den à chaque spawn (spec §6.5).
type Mixin struct {
	NomSandbox string
	Env        map[string]string // déjà fusionné et substitué par nest.Resolve
	Egress     []string          // déjà unionné et trié par nest.Resolve
	Fraicheur  []string          // argv, cf. CommandeFraicheur
}

// MixinDepuis assemble le mixin d'un nest résolu.
func MixinDepuis(r *nest.Resolved, nomSandbox string) (Mixin, error) {
	fraicheur, err := CommandeFraicheur(r.AgentName, r.Agent)
	if err != nil {
		return Mixin{}, err
	}
	return Mixin{
		NomSandbox: nomSandbox,
		Env:        r.Env,
		Egress:     r.Egress,
		Fraicheur:  fraicheur,
	}, nil
}

// RendMixin sérialise le mixin au schéma sbx réel (schemaVersion 2).
//
// Le YAML est construit nœud par nœud plutôt que par yaml.Marshal d'une map :
// l'ordre d'itération des maps Go est aléatoire, et un golden file ne tolère
// pas l'aléatoire. Les clés d'environment.variables sont émises TRIÉES.
func RendMixin(m Mixin) ([]byte, error) {
	racine := &yaml.Node{Kind: yaml.MappingNode}

	ajoute := func(cle string, valeur *yaml.Node) {
		racine.Content = append(racine.Content, scalaire(cle), valeur)
	}

	ajoute("schemaVersion", scalaire("2"))
	ajoute("kind", scalaire("mixin"))
	// Le nom d'un kit ne peut pas porter le séparateur de nom de sandbox.
	ajoute("name", scalaire("den-"+strings.ReplaceAll(m.NomSandbox, ".", "-")))
	ajoute("version", scalaire(versionMixin))
	ajoute("description", scalaire(fmt.Sprintf(
		"Mixin genere par den pour la sandbox %s. Regenere a chaque spawn, "+
			"ne pas editer a la main.", m.NomSandbox)))

	// Sections omises si vides : une `allow: []` vide signifierait « rien
	// d'autorise », pas « pas de contrainte ».
	if len(m.Egress) > 0 {
		reseau := &yaml.Node{Kind: yaml.MappingNode}
		reseau.Content = append(reseau.Content, scalaire("allow"), sequence(m.Egress))
		caps := &yaml.Node{Kind: yaml.MappingNode}
		caps.Content = append(caps.Content, scalaire("network"), reseau)
		ajoute("caps", caps)
	}

	if len(m.Env) > 0 {
		vars := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range slices.Sorted(maps.Keys(m.Env)) {
			vars.Content = append(vars.Content, scalaire(k), scalaire(m.Env[k]))
		}
		env := &yaml.Node{Kind: yaml.MappingNode}
		env.Content = append(env.Content, scalaire("variables"), vars)
		ajoute("environment", env)
	}

	if len(m.Fraicheur) > 0 {
		entree := &yaml.Node{Kind: yaml.MappingNode}
		entree.Content = append(entree.Content, scalaire("command"), sequence(m.Fraicheur))
		startup := &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{entree}}
		commands := &yaml.Node{Kind: yaml.MappingNode}
		commands.Content = append(commands.Content, scalaire("startup"), startup)
		ajoute("commands", commands)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(racine); err != nil {
		return nil, fmt.Errorf("rendu du mixin de %s : %w", m.NomSandbox, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("rendu du mixin de %s : %w", m.NomSandbox, err)
	}
	return buf.Bytes(), nil
}

// scalaire construit un nœud scalaire. Un contenu multiligne passe en style
// littéral (« | ») : c'est le seul style qui préserve un script bash sans
// échappement, et le script de fraîcheur en est un.
func scalaire(v string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Value: v}
	if strings.Contains(v, "\n") {
		n.Style = yaml.LiteralStyle
	}
	return n
}

func sequence(vals []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode}
	for _, v := range vals {
		n.Content = append(n.Content, scalaire(v))
	}
	return n
}

// EcrisMixin matérialise le mixin sous <denHome>/cache/mixins/<sandbox>/ et
// renvoie le DOSSIER — c'est ce que `sbx create --kit` attend.
//
// Sous cache/ et non dans un mktemp : cache/ est déclaré reconstructible par le
// spec §3, et un mixin qui s'évapore rend indébogable un boot raté. Il est
// réécrit à chaque spawn et reflète toujours la configuration courante.
func EcrisMixin(denHome, nomSandbox string, m Mixin) (string, error) {
	dir := filepath.Join(denHome, "cache", "mixins", nomSandbox)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("création de %s : %w", dir, err)
	}
	contenu, err := RendMixin(m)
	if err != nil {
		return "", err
	}
	chemin := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(chemin, contenu, 0o644); err != nil {
		return "", fmt.Errorf("écriture de %s : %w", chemin, err)
	}
	return dir, nil
}
```

- [ ] **Step 4: Lancer les tests hors golden — ils doivent passer**

Run:
```bash
go test ./internal/agent/ -run 'TestRendMixinPorte|TestRendMixinMet|TestRendMixinEstDeterministe|TestRendMixinNomme|TestRendMixinOmet|TestMixinDepuis|TestEcrisMixin' -v
```
Expected: PASS sur tous. Ne pas figer le golden avant.

- [ ] **Step 5: Générer le golden depuis le rendu réel**

Le golden est un filet de régression ; les invariants portants sont déjà assertés au Step 4. Le
rendu exact de `yaml.v3` (indentation des blocs littéraux, chevron de fin) est difficile à
prédire à la main — on le **capture** puis on le **relit**.

Run:
```bash
cd /Users/polochon/Development/Pillow/den
mkdir -p internal/agent/testdata
cat > /tmp/gen_golden_test.go <<'EOF'
package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenereGolden(t *testing.T) {
	out, err := RendMixin(mixinExemple(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", "mixin-complet.golden"), out, 0o644); err != nil {
		t.Fatal(err)
	}
}
EOF
cp /tmp/gen_golden_test.go internal/agent/gen_golden_test.go
go test ./internal/agent/ -run TestGenereGolden -v
rm internal/agent/gen_golden_test.go
cat internal/agent/testdata/mixin-complet.golden
```

**Relire la sortie et vérifier à l'œil** :
- `schemaVersion: 2` / `kind: mixin` / `name: den-api-feat12` présents ;
- `caps.network.allow` contient `api.anthropic.com` et `github.com`, **triés** ;
- `environment.variables` contient `CLAUDE_CONFIG_DIR` **avant** `SOME_VAR` (tri) ;
- `commands.startup[0].command` est bien `[bash, -c, <script>]` et le script contient
  **littéralement** `$HOME/.local/bin` ;
- aucun chemin du home de la machine de développement n'apparaît.

Si l'un de ces points est faux, c'est l'**implémentation** qu'il faut corriger, pas le golden.

- [ ] **Step 6: Lancer le test golden — il doit passer**

Run: `go test ./internal/agent/ -v`
Expected: PASS sur tous les tests du paquet.

- [ ] **Step 7: Vérifier que le mixin rendu se relit comme du YAML valide**

Un mixin syntaxiquement cassé ne se verrait qu'au `sbx create`.

Ajouter à `internal/agent/mixin_test.go` :

```go
func TestRendMixinEstDuYAMLRelisible(t *testing.T) {
	out, err := RendMixin(mixinExemple(t))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	var relu struct {
		SchemaVersion int    `yaml:"schemaVersion"`
		Kind          string `yaml:"kind"`
		Caps          struct {
			Network struct {
				Allow []string `yaml:"allow"`
			} `yaml:"network"`
		} `yaml:"caps"`
		Commands struct {
			Startup []struct {
				Command []string `yaml:"command"`
			} `yaml:"startup"`
		} `yaml:"commands"`
	}
	if err := yaml.Unmarshal(out, &relu); err != nil {
		t.Fatalf("le mixin rendu doit être du YAML relisible : %v\n%s", err, out)
	}
	if relu.SchemaVersion != 2 || relu.Kind != "mixin" {
		t.Errorf("en-tête relu = %d/%q", relu.SchemaVersion, relu.Kind)
	}
	if len(relu.Caps.Network.Allow) != 2 {
		t.Errorf("allow relu = %v", relu.Caps.Network.Allow)
	}
	if len(relu.Commands.Startup) != 1 || len(relu.Commands.Startup[0].Command) != 3 {
		t.Fatalf("startup relu = %v", relu.Commands.Startup)
	}
	// Le script doit rester exécutable après l'aller-retour YAML.
	if !strings.Contains(relu.Commands.Startup[0].Command[2], "$HOME/.local/bin") {
		t.Errorf("le script relu a perdu ses bin_dirs :\n%s", relu.Commands.Startup[0].Command[2])
	}
}
```

Ajouter `"gopkg.in/yaml.v3"` aux imports du fichier de test.

Run: `go test ./internal/agent/ -run TestRendMixinEstDuYAMLRelisible -v`
Expected: PASS

- [ ] **Step 8: Lancer toute la suite**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 9: Commit**

```bash
git add internal/agent/
git commit -m "feat(agent): mixin genere (caps.network.allow, environment.variables, fraicheur en dernier)"
```

---

## Task 8: `sbx.Ls` — lire l'état réel

Sans `--label`, `sbx ls --json` est la **seule** source de vérité sur ce qui tourne. Le schéma est
figé par le sondage du 2026-07-28.

**Files:**
- Create: `internal/sbx/ls.go`
- Create: `internal/sbx/ls_test.go`

**Interfaces:**
- Consumes: `Runner` (tâche 6), `DecomposeNom` (tâche 3).
- Produces:
  - `type Sandbox struct { Nom, ID, Agent, Statut string; Workspaces []string }`
  - `func (s Sandbox) Nest() string` / `func (s Sandbox) Worktree() string`
  - `func (s Sandbox) Workdir() string` — premier workspace, suffixe `:ro` retiré.
  - `func Ls(ctx context.Context, r Runner) ([]Sandbox, error)` — trié par nom.
  - `func Existe(ctx context.Context, r Runner, nom string) (bool, error)`

- [ ] **Step 1: Écrire le test qui échoue**

Créer `internal/sbx/ls_test.go` :

```go
package sbx

import (
	"context"
	"errors"
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

func TestLsPropageLErreurDuRunner(t *testing.T) {
	sentinelle := errors.New("sbx introuvable")
	f := &Fake{Defaut: Reponse{Err: sentinelle}}

	if _, err := Ls(context.Background(), f); !errors.Is(err, sentinelle) {
		t.Errorf("err = %v, attendu la sentinelle enveloppée", err)
	}
}

func TestExiste(t *testing.T) {
	f := &Fake{Reponses: map[string]Reponse{
		"ls --json": {Sortie: []byte(`{"sandboxes":[{"name":"api"}]}`)},
	}}

	ok, err := Existe(context.Background(), f, "api")
	if err != nil || !ok {
		t.Errorf("Existe(api) = %v, %v", ok, err)
	}
	ok, err = Existe(context.Background(), f, "absente")
	if err != nil || ok {
		t.Errorf("Existe(absente) = %v, %v", ok, err)
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
```

Ajouter `"strings"` aux imports du fichier de test.

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/sbx/ -run 'TestLs|TestExiste' -v`
Expected: FAIL — `undefined: Ls`, `undefined: Sandbox`.

- [ ] **Step 3: Implémenter**

Créer `internal/sbx/ls.go` :

```go
package sbx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Sandbox est une sandbox telle que `sbx ls --json` la décrit.
//
// Le schéma est celui de sbx v0.35.0, relevé le 2026-07-28 :
//
//	{"sandboxes":[{"name","id","agent","status","workspaces":["/p","/p:ro"]}]}
//
// Il n'y a AUCUN champ de date : l'âge d'une sandbox n'est pas calculable, et
// la colonne « âge » du spec §5 a été retirée en conséquence.
//
// Le décodage est volontairement tolérant aux champs inconnus (contrairement au
// YAML de configuration, strict) : cette sortie vient d'un outil tiers, pas de
// l'utilisateur. Un champ ajouté par une version ultérieure de sbx ne doit pas
// casser `den ls`.
type Sandbox struct {
	Nom        string   `json:"name"`
	ID         string   `json:"id"`
	Agent      string   `json:"agent"`
	Statut     string   `json:"status"`
	Workspaces []string `json:"workspaces"`
}

// Nest est le nest d'origine, déduit du nom — sbx n'ayant pas de labels, le nom
// est le seul porteur d'état.
func (s Sandbox) Nest() string {
	nest, _ := DecomposeNom(s.Nom)
	return nest
}

// Worktree est le worktree d'origine, vide si la sandbox n'en porte pas.
func (s Sandbox) Worktree() string {
	_, wt := DecomposeNom(s.Nom)
	return wt
}

// Workdir est le répertoire de travail naturel : le PREMIER workspace, qui est
// par construction le premier repo (ou son worktree) — den les monte avant le
// profil agent. Le suffixe « :ro » est retiré : c'est une option de mount, pas
// une partie du chemin.
func (s Sandbox) Workdir() string {
	if len(s.Workspaces) == 0 {
		return ""
	}
	return strings.TrimSuffix(s.Workspaces[0], ":ro")
}

// Ls liste les sandboxes vivantes, triées par nom.
func Ls(ctx context.Context, r Runner) ([]Sandbox, error) {
	sortie, err := r.Run(ctx, "ls", "--json")
	if err != nil {
		return nil, fmt.Errorf("sbx ls : %w", err)
	}

	var doc struct {
		Sandboxes []Sandbox `json:"sandboxes"`
	}
	if err := json.Unmarshal(sortie, &doc); err != nil {
		// La sortie brute est dans le message : sans elle, un changement de
		// schéma côté sbx serait indiagnosticable.
		return nil, fmt.Errorf("sbx ls : sortie JSON illisible (%w) : %s", err, string(sortie))
	}

	sort.Slice(doc.Sandboxes, func(i, j int) bool {
		return doc.Sandboxes[i].Nom < doc.Sandboxes[j].Nom
	})
	return doc.Sandboxes, nil
}

// Existe indique si une sandbox de ce nom tourne déjà. C'est ce qui fait du
// spawn un « spawn-or-attach » (spec §6.6) : un nom déjà vivant n'est pas une
// erreur.
func Existe(ctx context.Context, r Runner, nom string) (bool, error) {
	boxes, err := Ls(ctx, r)
	if err != nil {
		return false, err
	}
	for _, b := range boxes {
		if b.Nom == nom {
			return true, nil
		}
	}
	return false, nil
}
```

- [ ] **Step 4: Lancer le test — il doit passer**

Run: `go test ./internal/sbx/ -v`
Expected: PASS

- [ ] **Step 5: Lancer toute la suite**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 6: Commit**

```bash
git add internal/sbx/
git commit -m "feat(sbx): Ls decode sbx ls --json et attribue nest/worktree par le nom"
```

---

## Task 9: `sbx.ArgvCreate` — l'argv en golden files

Le spec §12 l'exige nommément : « assemblage de l'argv `sbx create` (golden files : on assert la
commande exacte) ». C'est le point où une erreur est la plus coûteuse — un kit dans le mauvais
ordre ne se voit qu'au boot, et se manifeste comme un agent périmé ou des startup commands
manquantes, jamais comme une erreur d'assemblage.

Le type `Create` sépare `KitsStack` de `KitMixin` **à dessein** : le mixin est fail-closed et le
dispatcher sbx avorte tout au premier échec, donc il doit être le dernier `--kit`. Une liste unique
laisserait l'appelant se tromper d'ordre ; deux champs rendent l'invariant impossible à violer.

**Files:**
- Create: `internal/sbx/argv.go`
- Create: `internal/sbx/argv_test.go`
- Create: `internal/sbx/testdata/create-minimal.golden`
- Create: `internal/sbx/testdata/create-complet.golden`

**Interfaces:**
- Consumes: `config.ValiderComposantSandbox` (tâche 3).
- Produces:
  - `type Create struct { Nom, Image string; KitsStack []string; KitMixin string; Workspaces []string }`
  - `func ArgvCreate(c Create) ([]string, error)`
  - `const AgentPositionnel = "shell"`

- [ ] **Step 1: Écrire le test qui échoue**

Créer `internal/sbx/argv_test.go` :

```go
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
```

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/sbx/ -run TestArgvCreate -v`
Expected: FAIL — `undefined: Create`, `undefined: ArgvCreate`, `undefined: AgentPositionnel`.

- [ ] **Step 3: Implémenter**

Créer `internal/sbx/argv.go` :

```go
package sbx

import (
	"fmt"
	"strings"

	"github.com/PillowPillow/den/internal/config"
)

// AgentPositionnel est l'agent passé à `sbx create`.
//
// « shell » et non « claude » : `sbx create [flags] AGENT PATH [PATH...]` exige
// un agent positionnel (l'omettre donne « unknown agent »), mais la commande
// réellement attachée est décidée par le FLAVOR de l'image, pas par cet
// argument — une image snapshotée depuis la base claude lance `claude` quoi
// qu'on écrive ici. den attache de toute façon par `sbx exec … bash -l`, donc
// « shell » est le choix honnête : il ne promet rien qu'il ne tienne.
const AgentPositionnel = "shell"

// Create décrit un `sbx create` à assembler.
//
// KitMixin est un champ SÉPARÉ de KitsStack, et non le dernier élément d'une
// liste unique : le mixin est fail-closed et le dispatcher sbx fait `exit $rc`
// au premier échec, ce qui prive les kits suivants de leurs startup commands.
// Il DOIT être layeré en dernier. Deux champs rendent l'inversion impossible ;
// une liste unique ne ferait que la rendre improbable.
type Create struct {
	Nom       string   // nom de sandbox, cf. NomSandbox
	Image     string   // passée verbatim à --template
	KitsStack []string // kits transverses puis kit de la stack, ordre de layering
	KitMixin  string   // dossier du mixin généré — TOUJOURS le dernier --kit
	// Workspaces : chemins hôte montés, dans l'ordre. Le PREMIER doit être le
	// repo (ou son worktree) : Sandbox.Workdir en dépend pour l'attache.
	// Suffixe « :ro » accepté par sbx.
	Workspaces []string
}

// ArgvCreate assemble l'argv complet de `sbx create`, sans le nom du binaire.
func ArgvCreate(c Create) ([]string, error) {
	// Le nom porte le séparateur : on valide chaque composant, pas le tout.
	nest, worktree := DecomposeNom(c.Nom)
	if err := config.ValiderComposantSandbox("nom de sandbox", nest); err != nil {
		return nil, err
	}
	if worktree != "" {
		if err := config.ValiderComposantSandbox("nom de sandbox", worktree); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(c.Image) == "" {
		return nil, fmt.Errorf(
			"sandbox %q : aucune image — la stack doit déclarer `image:` dans stack.yaml", c.Nom)
	}
	if strings.TrimSpace(c.KitMixin) == "" {
		return nil, fmt.Errorf(
			"sandbox %q : mixin généré manquant — il porte l'egress, l'env et la commande "+
				"de fraîcheur, un create sans lui produirait une VM muette", c.Nom)
	}
	if len(c.Workspaces) == 0 {
		return nil, fmt.Errorf(
			"sandbox %q : aucun workspace à monter — `sbx create` exige au moins un chemin", c.Nom)
	}

	argv := []string{"create", "--name", c.Nom, "--template", c.Image}
	for _, k := range c.KitsStack {
		if k == "" {
			continue
		}
		argv = append(argv, "--kit", k)
	}
	argv = append(argv, "--kit", c.KitMixin) // toujours en dernier, cf. doc du type
	argv = append(argv, AgentPositionnel)
	argv = append(argv, c.Workspaces...)
	return argv, nil
}
```

- [ ] **Step 4: Lancer les tests hors golden — ils doivent passer**

Run:
```bash
go test ./internal/sbx/ -run 'TestArgvCreateStructure|TestArgvCreateMixin|TestArgvCreateNom|TestArgvCreateNEmet|TestArgvCreateRefuse|TestArgvCreateAccepte' -v
```
Expected: PASS sur tous.

- [ ] **Step 5: Écrire les golden files**

Créer `internal/sbx/testdata/create-minimal.golden` — un argument par ligne, saut de ligne final :

```
create
--name
api
--template
devx:v1
--kit
/den/cache/mixins/api
shell
/dev/api
/home/moi/.den/agents/claude
```

Créer `internal/sbx/testdata/create-complet.golden` :

```
create
--name
api.feat12
--template
docker.io/library/dgdevx:v1
--kit
/den/kits/ssh-known-hosts
--kit
/den/stacks/dgdevx/kit
--kit
/den/cache/mixins/api.feat12
shell
/den/worktrees/feat12/api
/den/worktrees/feat12/front
/home/moi/.den/agents/claude
/home/moi/.ssh_sbx
```

- [ ] **Step 6: Lancer le test golden — il doit passer**

Run: `go test ./internal/sbx/ -run TestArgvCreateGolden -v`
Expected: PASS. En cas d'écart, comparer ligne à ligne : c'est **l'implémentation** qui doit
changer si l'ordre diffère, le golden seulement si l'écart est un espace ou un saut de ligne final.

- [ ] **Step 7: Lancer toute la suite**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 8: Commit**

```bash
git add internal/sbx/
git commit -m "feat(sbx): assemblage de l'argv create, mixin structurellement en dernier kit"
```

---

## Task 10: `internal/worktree` — propager un worktree sur tous les repos

Le multi-projet natif du spec (§13.4) : `-w <wt>` crée le worktree sur **tous** les repos
sélectionnés et les co-monte dans une seule VM. Idempotent : re-spawner le même nest avec le même
`-w` ne doit rien casser.

C'est le **seul module de ce plan dont les tests lancent un vrai `git`** — sur des dépôts créés
dans `t.TempDir()`, comme le spec §12 l'exige.

**Files:**
- Create: `internal/worktree/worktree.go`
- Create: `internal/worktree/worktree_test.go`

**Interfaces:**
- Consumes: `nest.Repo` (Plan 1) — champs `Path string`, méthode `Name() string`.
- Produces:
  - `type Git interface { Run(ctx context.Context, dir string, args ...string) ([]byte, error) }`
  - `func NewGit() Git`
  - `func Chemin(layout, root, wt, cheminRepo string) string`
  - `func Assure(ctx context.Context, g Git, layout, root, wt string, cheminRepo string) (string, error)`
  - `func Retire(ctx context.Context, g Git, cheminRepo, cheminWorktree string, force bool) error`

- [ ] **Step 1: Écrire le test qui échoue — les chemins d'abord**

Créer `internal/worktree/worktree_test.go` :

```go
package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChemin(t *testing.T) {
	cas := []struct {
		layout, root, wt, repo, attendu string
	}{
		{"central", "/den/worktrees", "feat12", "/dev/api", "/den/worktrees/feat12/api"},
		{"per-repo", "/den/worktrees", "feat12", "/dev/api", "/dev/api/.den/feat12"},
	}
	for _, c := range cas {
		if got := Chemin(c.layout, c.root, c.wt, c.repo); got != c.attendu {
			t.Errorf("Chemin(%s,…) = %q, attendu %q", c.layout, got, c.attendu)
		}
	}
}

// depotTest crée un dépôt git réel avec un commit, dans t.TempDir().
func depotTest(t *testing.T, nom string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), nom)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmds := [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@exemple.test"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "initial"},
	}
	for _, c := range cmds {
		cmd := exec.Command("git", c...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v : %v\n%s", c, err, out)
		}
	}
	return dir
}

func TestAssureCreeLeWorktreeEtLaBranche(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()

	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if chemin != filepath.Join(root, "feat12", "api") {
		t.Errorf("chemin = %q", chemin)
	}
	if _, err := os.Stat(filepath.Join(chemin, ".git")); err != nil {
		t.Errorf("le worktree doit exister : %v", err)
	}
	if got := brancheDe(t, chemin); got != "feat12" {
		t.Errorf("branche = %q, attendu feat12", got)
	}
}

// Idempotence : re-spawner le même nest avec le même -w ne doit rien casser.
func TestAssureEstIdempotent(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()

	premier, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("premier appel : %v", err)
	}
	second, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("second appel : %v", err)
	}
	if premier != second {
		t.Errorf("chemins divergents : %q puis %q", premier, second)
	}
}

// Le worktree existe mais sur une AUTRE branche : arrêt actionnable (spec §11),
// jamais un checkout silencieux qui déplacerait le travail de l'utilisateur.
func TestAssureRefuseUnWorktreeSurUneAutreBranche(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()

	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	basculeSur(t, chemin, "autre")

	_, err = Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err == nil {
		t.Fatal("un worktree sur une autre branche doit produire une erreur")
	}
	for _, attendu := range []string{chemin, "feat12", "autre"} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("le message doit contenir %q pour être actionnable ; obtenu : %v", attendu, err)
		}
	}
}

// La branche existe déjà côté repo : on la checkout, on ne tente pas de la
// recréer (git refuserait).
func TestAssureReutiliseUneBrancheExistante(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()
	git(t, repo, "branch", "feat12")

	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if got := brancheDe(t, chemin); got != "feat12" {
		t.Errorf("branche = %q, attendu feat12", got)
	}
}

func TestAssureRepoInexistant(t *testing.T) {
	root := t.TempDir()
	_, err := Assure(context.Background(), NewGit(), "central", root, "feat12", "/n/existe/pas")
	if err == nil {
		t.Fatal("un repo inexistant doit produire une erreur")
	}
	if !strings.Contains(err.Error(), "/n/existe/pas") {
		t.Errorf("le message doit nommer le chemin fautif ; obtenu : %v", err)
	}
}

func TestRetireSupprimeLeWorktree(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()
	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}

	if err := Retire(context.Background(), NewGit(), repo, chemin, false); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if _, err := os.Stat(chemin); !os.IsNotExist(err) {
		t.Errorf("le worktree doit avoir disparu de %s", chemin)
	}
}

// Spec §14 : refuser si dirty sans --force. Perdre du travail non commité
// serait le pire effet de bord possible pour un `den rm`.
func TestRetireRefuseUnWorktreeSale(t *testing.T) {
	repo := depotTest(t, "api")
	root := t.TempDir()
	chemin, err := Assure(context.Background(), NewGit(), "central", root, "feat12", repo)
	if err != nil {
		t.Fatalf("préparation : %v", err)
	}
	if err := os.WriteFile(filepath.Join(chemin, "brouillon.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Retire(context.Background(), NewGit(), repo, chemin, false); err == nil {
		t.Fatal("un worktree avec des modifications non commitées doit être refusé sans force")
	}
	if _, err := os.Stat(chemin); err != nil {
		t.Errorf("le worktree refusé doit être INTACT : %v", err)
	}

	if err := Retire(context.Background(), NewGit(), repo, chemin, true); err != nil {
		t.Fatalf("avec force, la suppression doit passer : %v", err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v dans %s : %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func brancheDe(t *testing.T, dir string) string {
	t.Helper()
	return git(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
}

func basculeSur(t *testing.T, dir, branche string) {
	t.Helper()
	git(t, dir, "checkout", "-b", branche)
}
```

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/worktree/ -v`
Expected: FAIL — le paquet n'existe pas.

- [ ] **Step 3: Implémenter**

Créer `internal/worktree/worktree.go` :

```go
// Package worktree propage un worktree git sur les repos d'un nest. C'est le
// seul module de den qui pilote git ; comme sbx, il le fait derrière une
// interface pour rester substituable.
package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git est l'accès à la CLI git, injecté pour rester substituable.
type Git interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

type gitExec struct{}

// NewGit renvoie l'accès réel au git du PATH.
func NewGit() Git { return gitExec{} }

func (gitExec) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("git %s (dans %s) : %s", strings.Join(args, " "), dir, detail)
	}
	return stdout.Bytes(), nil
}

// Chemin calcule où vit le worktree wt du repo, selon le layout (spec §13.5).
//
//	central  : <root>/<wt>/<repo>     — défaut, tous les worktrees d'un même
//	                                    wt voisins, ce qui rend le co-montage
//	                                    multi-repo lisible
//	per-repo : <repo>/.den/<wt>       — pour qui préfère garder ses worktrees
//	                                    près de leur dépôt
func Chemin(layout, root, wt, cheminRepo string) string {
	if layout == "per-repo" {
		return filepath.Join(cheminRepo, ".den", wt)
	}
	return filepath.Join(root, wt, filepath.Base(cheminRepo))
}

// Assure garantit l'existence du worktree wt pour ce repo et renvoie son
// chemin. Idempotent : si le worktree existe déjà SUR LA BONNE BRANCHE, il est
// laissé tel quel.
//
// Un worktree existant sur une AUTRE branche est une erreur, jamais un checkout
// silencieux : basculer la branche d'un worktree où l'utilisateur travaille
// déplacerait son travail sans qu'il l'ait demandé.
func Assure(ctx context.Context, g Git, layout, root, wt, cheminRepo string) (string, error) {
	if _, err := os.Stat(cheminRepo); err != nil {
		return "", fmt.Errorf("repo introuvable : %s", cheminRepo)
	}

	chemin := Chemin(layout, root, wt, cheminRepo)

	if _, err := os.Stat(chemin); err == nil {
		actuelle, err := brancheCourante(ctx, g, chemin)
		if err != nil {
			return "", fmt.Errorf(
				"%s existe déjà mais n'est pas un worktree git exploitable : %w", chemin, err)
		}
		if actuelle != wt {
			return "", fmt.Errorf(
				"le worktree %s est sur la branche %q, pas %q — choisis un autre nom de worktree "+
					"ou bascule ce dossier sur %q à la main", chemin, actuelle, wt, wt)
		}
		return chemin, nil // déjà en place : idempotent
	}

	if err := os.MkdirAll(filepath.Dir(chemin), 0o755); err != nil {
		return "", fmt.Errorf("création de %s : %w", filepath.Dir(chemin), err)
	}

	// `git worktree add <chemin> <branche>` si la branche existe déjà,
	// `-b <branche>` sinon : git refuse de recréer une branche existante.
	args := []string{"worktree", "add", chemin, wt}
	if !brancheExiste(ctx, g, cheminRepo, wt) {
		args = []string{"worktree", "add", "-b", wt, chemin}
	}
	if _, err := g.Run(ctx, cheminRepo, args...); err != nil {
		return "", fmt.Errorf("création du worktree %q de %s : %w", wt, cheminRepo, err)
	}
	return chemin, nil
}

// Retire supprime un worktree. Refuse si l'arbre est sale et que force est
// faux : perdre du travail non commité serait le pire effet de bord d'un
// `den rm` (spec §14).
func Retire(ctx context.Context, g Git, cheminRepo, cheminWorktree string, force bool) error {
	if _, err := os.Stat(cheminWorktree); os.IsNotExist(err) {
		return nil // déjà retiré : idempotent
	}

	if !force {
		sale, err := estSale(ctx, g, cheminWorktree)
		if err != nil {
			return err
		}
		if sale {
			return fmt.Errorf(
				"le worktree %s contient des modifications non commitées — commite-les, ou relance "+
					"avec --force pour les perdre, ou avec --keep-worktrees pour garder le dossier",
				cheminWorktree)
		}
	}

	args := []string{"worktree", "remove", cheminWorktree}
	if force {
		args = append(args, "--force")
	}
	if _, err := g.Run(ctx, cheminRepo, args...); err != nil {
		return fmt.Errorf("suppression du worktree %s : %w", cheminWorktree, err)
	}
	return nil
}

func brancheCourante(ctx context.Context, g Git, dir string) (string, error) {
	out, err := g.Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func brancheExiste(ctx context.Context, g Git, cheminRepo, branche string) bool {
	_, err := g.Run(ctx, cheminRepo, "show-ref", "--verify", "--quiet", "refs/heads/"+branche)
	return err == nil
}

func estSale(ctx context.Context, g Git, dir string) (bool, error) {
	// --porcelain inclut les fichiers non suivis : un brouillon non ajouté est
	// exactement le travail qu'on ne veut pas détruire.
	out, err := g.Run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("état de %s : %w", dir, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}
```

- [ ] **Step 4: Lancer le test — il doit passer**

Run: `go test ./internal/worktree/ -v`
Expected: PASS sur tous les tests.

Si `git init -b main` échoue (git < 2.28), le test doit être corrigé pour utiliser
`git init` puis `git checkout -b main` — ne pas modifier l'implémentation pour ça.

- [ ] **Step 5: Lancer toute la suite**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 6: Commit**

```bash
git add internal/worktree/
git commit -m "feat(worktree): creation idempotente, refus de bascule de branche et de suppression sale"
```

---

## Task 11: `policy.Settle` — le settle-loop fail-closed

La douleur #1 du spec (§7). La propagation des règles réseau n'est **pas instantanée** (constaté aux
spikes) : après le `create`, den boucle sur `sbx policy check network --sandbox <name> <hôte>` pour
chaque hôte de l'allowlist jusqu'à ce que tous passent, avec un timeout borné. Si un hôte reste
bloqué, den **n'attache pas** : jamais de « ça marche à moitié ».

Un piège précis à éviter : si la sortie JSON ne porte pas le champ `allowed`, un décodage naïf le
lit comme `false` et la boucle tourne jusqu'au timeout en accusant le réseau, alors que la cause est
un changement de schéma côté sbx. Le champ est donc décodé en `*bool` et son absence est une erreur
immédiate qui montre la sortie brute.

**Files:**
- Create: `internal/policy/settle.go`
- Create: `internal/policy/settle_test.go`

**Interfaces:**
- Consumes: `sbx.Runner`, `sbx.Fake` (tâche 6).
- Produces:
  - `type Options struct { Timeout, Intervalle time.Duration; Sommeil func(time.Duration); Maintenant func() time.Time }`
  - `func OptionsDefaut() Options` — 60 s de timeout, 2 s d'intervalle.
  - `func Settle(ctx context.Context, r sbx.Runner, sandbox string, hotes []string, o Options) error`

- [ ] **Step 1: Écrire le test qui échoue**

Créer `internal/policy/settle_test.go` :

```go
package policy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/sbx"
)

// optionsTest : horloge et sommeil injectés, pour que la suite reste instantanée.
func optionsTest(t *testing.T) (Options, *int) {
	t.Helper()
	dormi := 0
	faux := time.Unix(0, 0)
	return Options{
		Timeout:    60 * time.Second,
		Intervalle: 2 * time.Second,
		Sommeil: func(d time.Duration) {
			dormi++
			faux = faux.Add(d)
		},
		Maintenant: func() time.Time { return faux },
	}, &dormi
}

func autorise(sandbox string, hotes ...string) map[string]sbx.Reponse {
	m := make(map[string]sbx.Reponse, len(hotes))
	for _, h := range hotes {
		cle := strings.Join([]string{"policy", "check", "network", "--sandbox", sandbox, "--json", h}, " ")
		m[cle] = sbx.Reponse{Sortie: []byte(`{"allowed": true}`)}
	}
	return m
}

func TestSettlePasseQuandToutEstAutorise(t *testing.T) {
	o, dormi := optionsTest(t)
	f := &sbx.Fake{Reponses: autorise("api", "github.com", "api.anthropic.com")}

	err := Settle(context.Background(), f, "api", []string{"github.com", "api.anthropic.com"}, o)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if *dormi != 0 {
		t.Errorf("aucun sommeil ne doit être nécessaire quand tout passe du premier coup (%d)", *dormi)
	}
}

// L'argv exact importe : --sandbox scope l'évaluation à la policy de CETTE
// sandbox. Sans lui, on validerait la policy globale — un vert qui ne prouve
// rien sur ce qu'on vient de poser.
func TestSettleInterrogeLaPolicyScopee(t *testing.T) {
	o, _ := optionsTest(t)
	f := &sbx.Fake{Reponses: autorise("api", "github.com")}

	if err := Settle(context.Background(), f, "api", []string{"github.com"}, o); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !f.AAppele("policy", "check", "network", "--sandbox", "api", "--json", "github.com") {
		t.Errorf("argv attendu avec --sandbox ; appels : %v", f.Appels)
	}
}

// Fail-closed : un hôte qui ne passe jamais doit sortir en erreur, en NOMMANT
// les hôtes bloqués — c'est tout ce que l'utilisateur aura pour diagnostiquer.
func TestSettleEchoueEnNommantLesHotesBloques(t *testing.T) {
	o, _ := optionsTest(t)
	reponses := autorise("api", "github.com")
	f := &sbx.Fake{
		Reponses: reponses,
		Defaut:   sbx.Reponse{Sortie: []byte(`{"allowed": false}`)},
	}

	err := Settle(context.Background(), f, "api", []string{"github.com", "bloque.exemple.test"}, o)
	if err == nil {
		t.Fatal("un hôte durablement bloqué doit produire une erreur (fail-closed)")
	}
	if !strings.Contains(err.Error(), "bloque.exemple.test") {
		t.Errorf("le message doit nommer l'hôte bloqué ; obtenu : %v", err)
	}
	if strings.Contains(err.Error(), "github.com") {
		t.Errorf("le message ne doit PAS lister les hôtes déjà passés ; obtenu : %v", err)
	}
}

// La propagation n'étant pas instantanée, un hôte d'abord refusé puis autorisé
// doit finir par passer — c'est la raison d'être de la boucle.
func TestSettleAttendLaPropagation(t *testing.T) {
	o, dormi := optionsTest(t)
	f := &fakeProgressif{autoriseApres: 3}

	if err := Settle(context.Background(), f, "api", []string{"lent.exemple.test"}, o); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if *dormi == 0 {
		t.Error("la boucle doit avoir dormi au moins une fois")
	}
	if f.appels < 3 {
		t.Errorf("appels = %d, attendu au moins 3", f.appels)
	}
}

// Un champ `allowed` absent = changement de schéma sbx. Le lire comme false
// ferait tourner la boucle jusqu'au timeout en accusant le réseau.
func TestSettleRefuseUneSortieSansChampAllowed(t *testing.T) {
	o, _ := optionsTest(t)
	f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte(`{"autre": "chose"}`)}}

	err := Settle(context.Background(), f, "api", []string{"github.com"}, o)
	if err == nil {
		t.Fatal("une sortie sans champ allowed doit échouer immédiatement")
	}
	if !strings.Contains(err.Error(), "allowed") || !strings.Contains(err.Error(), `{"autre": "chose"}`) {
		t.Errorf("le message doit nommer le champ manquant et montrer la sortie brute ; obtenu : %v", err)
	}
}

func TestSettleSansHote(t *testing.T) {
	o, _ := optionsTest(t)
	f := &sbx.Fake{Defaut: sbx.Reponse{Err: errors.New("ne doit pas être appelé")}}

	if err := Settle(context.Background(), f, "api", nil, o); err != nil {
		t.Errorf("une allowlist vide n'est pas une erreur : %v", err)
	}
	if len(f.Appels) != 0 {
		t.Errorf("aucun appel ne doit être fait ; appels : %v", f.Appels)
	}
}

func TestSettleRespecteLAnnulationDuContexte(t *testing.T) {
	o, _ := optionsTest(t)
	ctx, annule := context.WithCancel(context.Background())
	annule()

	f := &sbx.Fake{Defaut: sbx.Reponse{Sortie: []byte(`{"allowed": false}`)}}
	if err := Settle(ctx, f, "api", []string{"github.com"}, o); err == nil {
		t.Fatal("un contexte annulé doit interrompre la boucle")
	}
}

// fakeProgressif autorise l'hôte à partir du n-ième appel.
type fakeProgressif struct {
	appels        int
	autoriseApres int
}

func (f *fakeProgressif) Run(_ context.Context, _ ...string) ([]byte, error) {
	f.appels++
	if f.appels >= f.autoriseApres {
		return []byte(`{"allowed": true}`), nil
	}
	return []byte(`{"allowed": false}`), nil
}

func (f *fakeProgressif) Attach(_ context.Context, _ ...string) error { return nil }
```

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/policy/ -v`
Expected: FAIL — le paquet n'existe pas.

- [ ] **Step 3: Implémenter**

Créer `internal/policy/settle.go` :

```go
// Package policy attend que la policy réseau d'une sandbox soit effectivement
// posée, avant que den n'y attache un shell.
package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/PillowPillow/den/internal/sbx"
)

// Options paramètre la boucle. Sommeil et Maintenant sont injectés pour que les
// tests n'attendent pas réellement.
type Options struct {
	Timeout    time.Duration
	Intervalle time.Duration
	Sommeil    func(time.Duration)
	Maintenant func() time.Time
}

// OptionsDefaut : 60 s de patience, un sondage toutes les 2 s. La propagation
// observée aux spikes se compte en secondes, jamais en minutes ; 60 s laisse une
// marge large sans transformer un vrai blocage en attente interminable.
func OptionsDefaut() Options {
	return Options{
		Timeout:    60 * time.Second,
		Intervalle: 2 * time.Second,
		Sommeil:    time.Sleep,
		Maintenant: time.Now,
	}
}

// Settle boucle jusqu'à ce que TOUS les hôtes soient autorisés dans le contexte
// de cette sandbox, ou jusqu'au timeout.
//
// Fail-closed (spec §7) : si un hôte ne passe pas, den n'attache pas. Une
// sandbox qui démarre à moitié — agent sans accès à api.anthropic.com, install
// qui échoue à mi-parcours — coûte plus cher à diagnostiquer qu'un refus net.
//
// Le scope --sandbox est essentiel : l'allowlist est posée en caps.network.allow
// d'un mixin auto-scopé à la sandbox. Interroger la policy GLOBALE validerait
// autre chose que ce qu'on vient de poser.
func Settle(ctx context.Context, r sbx.Runner, sandbox string, hotes []string, o Options) error {
	if len(hotes) == 0 {
		return nil
	}

	restants := slices.Clone(hotes)
	limite := o.Maintenant().Add(o.Timeout)

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("sandbox %s : attente de la policy interrompue : %w", sandbox, err)
		}

		var encoreBloques []string
		for _, h := range restants {
			ok, err := autorise(ctx, r, sandbox, h)
			if err != nil {
				return err
			}
			if !ok {
				encoreBloques = append(encoreBloques, h)
			}
		}
		restants = encoreBloques

		if len(restants) == 0 {
			return nil
		}
		if !o.Maintenant().Before(limite) {
			slices.Sort(restants) // déterminisme du message
			return fmt.Errorf(
				"sandbox %s : la policy réseau n'autorise toujours pas %d hôte(s) après %s — "+
					"den n'attache pas (fail-closed). Hôtes bloqués : %s. "+
					"Vérifie l'allowlist du nest et de la stack, puis "+
					"`sbx policy check network --sandbox %s --verbose <hôte>`",
				sandbox, len(restants), o.Timeout, strings.Join(restants, ", "), sandbox)
		}
		o.Sommeil(o.Intervalle)
	}
}

// autorise interroge la policy pour UN hôte, dans le contexte de la sandbox.
func autorise(ctx context.Context, r sbx.Runner, sandbox, hote string) (bool, error) {
	sortie, err := r.Run(ctx, "policy", "check", "network", "--sandbox", sandbox, "--json", hote)
	if err != nil {
		return false, fmt.Errorf("sandbox %s : vérification de %s : %w", sandbox, hote, err)
	}

	// Allowed est un POINTEUR : un champ absent doit se distinguer d'un `false`.
	// Confondre les deux ferait tourner la boucle jusqu'au timeout en accusant
	// le réseau, alors que la cause serait un changement de schéma côté sbx.
	var doc struct {
		Allowed *bool `json:"allowed"`
	}
	if err := json.Unmarshal(sortie, &doc); err != nil {
		return false, fmt.Errorf(
			"sandbox %s : sortie de `sbx policy check network` illisible (%w) : %s",
			sandbox, err, string(sortie))
	}
	if doc.Allowed == nil {
		return false, fmt.Errorf(
			"sandbox %s : la sortie de `sbx policy check network` ne porte pas de champ "+
				"\"allowed\" — le schéma de sbx a probablement changé : %s",
			sandbox, string(sortie))
	}
	return *doc.Allowed, nil
}
```

- [ ] **Step 4: Lancer le test — il doit passer**

Run: `go test ./internal/policy/ -v`
Expected: PASS

- [ ] **Step 5: Vérifier que la suite reste instantanée**

Un `time.Sleep` réel qui aurait fui dans les tests se verrait ici.

Run: `go test ./internal/policy/ -v 2>&1 | tail -3`
Expected: le temps total du paquet est inférieur à `0.5s`.

- [ ] **Step 6: Lancer toute la suite**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 7: Commit**

```bash
git add internal/policy/
git commit -m "feat(policy): settle-loop fail-closed scope a la sandbox, horloge injectee"
```

---

## Task 12: `internal/spawn` + `den <nest>` — l'orchestration

Le cœur du plan. Tout ce qui précède converge ici, dans l'ordre du spec §6.

L'orchestration vit dans un **nouveau paquet `internal/spawn`** et non dans `internal/cli` :
la contrainte du dépôt veut que `cli` ne fasse que du câblage cobra et de l'affichage, et cette
séquence est la logique la plus dense du projet — elle doit être testable sans cobra. Le layout du
spec §12 ne mentionne pas ce paquet ; c'est une extension assumée, à ajouter au spec en fin de tâche.

**Files:**
- Create: `internal/spawn/spawn.go`
- Create: `internal/spawn/spawn_test.go`
- Create: `internal/cli/spawn.go`
- Create: `internal/cli/spawn_test.go`
- Modify: `internal/cli/root.go`
- Modify: `docs/superpowers/specs/2026-07-27-den-cli-design.md` (§12, layout)

**Interfaces:**
- Consumes: tout ce qui précède.
- Produces:
  - `type Deps struct { Sbx sbx.Runner; Git worktree.Git; Policy policy.Options; Sortie io.Writer }`
  - `type Options struct { Nest, Worktree, Agent string; Without, Only []string; Detach bool }`
  - `func Spawn(ctx context.Context, denHome string, o Options, d Deps) error`

- [ ] **Step 1: Écrire le test qui échoue**

Créer `internal/spawn/spawn_test.go` :

```go
package spawn

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
)

// denTest construit un ~/.den temporaire complet avec un dépôt git réel.
func denTest(t *testing.T) (denHome, repo string) {
	t.Helper()
	denHome = t.TempDir()
	repo = filepath.Join(t.TempDir(), "api")

	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@exemple.test"},
		{"config", "user.name", "T"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", c...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v : %v\n%s", c, err, out)
		}
	}

	ecris(t, filepath.Join(denHome, "config.yaml"), `agents:
  claude:
    config_dir: `+filepath.Join(denHome, "agents", "claude")+`
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }
    bin_dirs: ["$HOME/.local/bin"]
    update: "claude update"
defaults:
  agent: claude
  stack: devx
ssh:
  mode: agent-forward
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
egress:
  - github.com
`)
	ecris(t, filepath.Join(denHome, "stacks", "devx", "stack.yaml"), "image: devx:v1\n")
	ecris(t, filepath.Join(denHome, "nests", "api.yaml"), "stack: devx\nrepos:\n  - { path: "+repo+" }\n")
	return denHome, repo
}

func ecris(t *testing.T, chemin, contenu string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(chemin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chemin, []byte(contenu), 0o644); err != nil {
		t.Fatal(err)
	}
}

// policyInstantanee : mêmes réglages que le défaut, mais sans attente réelle.
func policyInstantanee() policy.Options {
	o := policy.OptionsDefaut()
	o.Sommeil = func(time.Duration) {}
	return o
}

// depsTest : sbx factice qui répond « aucune sandbox » puis « tout autorisé ».
func depsTest() (*sbx.Fake, Deps) {
	f := &sbx.Fake{
		Reponses: map[string]sbx.Reponse{
			"ls --json": {Sortie: []byte(`{"sandboxes":[]}`)},
		},
		Defaut: sbx.Reponse{Sortie: []byte(`{"allowed": true}`)},
	}
	return f, Deps{
		Sbx:    f,
		Git:    worktree.NewGit(),
		Policy: policyInstantanee(),
		Sortie: io.Discard,
	}
}

func TestSpawnSequenceNominale(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if !f.AAppele("create", "--name", "api") {
		t.Errorf("un create doit avoir eu lieu ; appels : %v", f.Appels)
	}
	if !f.AAppele("policy", "check", "network", "--sandbox", "api") {
		t.Errorf("le settle-loop doit avoir tourné ; appels : %v", f.Appels)
	}
	if !f.AAppele("exec", "-it", "api") {
		t.Errorf("l'attache doit avoir eu lieu ; appels : %v", f.Appels)
	}
}

// L'ordre est une propriété de sûreté : attacher avant que la policy soit
// posée, c'est exactement le « ça marche à moitié » que le spec §7 interdit.
func TestSpawnAttacheApresLeSettleLoop(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	iPolicy, iExec := -1, -1
	for i, a := range f.Appels {
		if len(a) > 0 && a[0] == "policy" && iPolicy < 0 {
			iPolicy = i
		}
		if len(a) > 0 && a[0] == "exec" {
			iExec = i
		}
	}
	if iPolicy < 0 || iExec < 0 || iExec < iPolicy {
		t.Errorf("l'attache (%d) doit suivre le settle-loop (%d) ; appels : %v", iExec, iPolicy, f.Appels)
	}
}

// Fail-closed de bout en bout : policy bloquée ⇒ aucune attache.
func TestSpawnNAttachePasSiLaPolicyNePasse(t *testing.T) {
	denHome, _ := denTest(t)
	f := &sbx.Fake{
		Reponses: map[string]sbx.Reponse{"ls --json": {Sortie: []byte(`{"sandboxes":[]}`)}},
		Defaut:   sbx.Reponse{Sortie: []byte(`{"allowed": false}`)},
	}
	d := Deps{Sbx: f, Git: worktree.NewGit(), Policy: policyInstantanee(), Sortie: io.Discard}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err == nil {
		t.Fatal("une policy qui ne passe pas doit faire échouer le spawn")
	}
	if f.AAppele("exec") {
		t.Errorf("aucune attache ne doit avoir lieu ; appels : %v", f.Appels)
	}
}

// Spawn-or-attach (spec §11) : un nom déjà vivant n'est pas une erreur.
func TestSpawnAttacheSansRecreerSiLaSandboxExiste(t *testing.T) {
	denHome, _ := denTest(t)
	f := &sbx.Fake{
		Reponses: map[string]sbx.Reponse{
			"ls --json": {Sortie: []byte(`{"sandboxes":[{"name":"api","status":"running","workspaces":["/w"]}]}`)},
		},
		Defaut: sbx.Reponse{Sortie: []byte(`{"allowed": true}`)},
	}
	d := Deps{Sbx: f, Git: worktree.NewGit(), Policy: policyInstantanee(), Sortie: io.Discard}

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if f.AAppele("create") {
		t.Errorf("aucun create ne doit avoir lieu sur une sandbox vivante ; appels : %v", f.Appels)
	}
	if !f.AAppele("exec", "-it", "api") {
		t.Errorf("l'attache doit avoir lieu ; appels : %v", f.Appels)
	}
}

func TestSpawnAvecWorktree(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feat12"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if !f.AAppele("create", "--name", "api.feat12") {
		t.Errorf("le nom doit porter le worktree ; appels : %v", f.Appels)
	}
	chemin := filepath.Join(denHome, "worktrees", "feat12", "api")
	if _, err := os.Stat(chemin); err != nil {
		t.Errorf("le worktree doit exister en %s : %v", chemin, err)
	}
	// Et il doit être monté DANS la VM, en premier positionnel.
	argv := appelCommencantPar(f, "create")
	iShell := slices.Index(argv, "shell")
	if iShell < 0 || argv[iShell+1] != chemin {
		t.Errorf("le worktree doit être le premier workspace ; argv = %v", argv)
	}
}

// Un nom de worktree issu d'un nom de branche doit être refusé AVANT tout
// effet de bord : ni worktree créé, ni sandbox.
func TestSpawnRefuseUnWorktreeNonSandboxable(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()

	err := Spawn(context.Background(), denHome, Options{Nest: "api", Worktree: "feature/123"}, d)
	if err == nil {
		t.Fatal("un worktree contenant / doit être refusé")
	}
	if len(f.Appels) != 0 {
		t.Errorf("aucun appel sbx ne doit avoir eu lieu ; appels : %v", f.Appels)
	}
	if _, err := os.Stat(filepath.Join(denHome, "worktrees")); err == nil {
		t.Error("aucun worktree ne doit avoir été créé")
	}
}

// Spec §11 : « Chemin repo introuvable → stop AVANT tout create ».
func TestSpawnStoppeAvantCreateSiUnRepoManque(t *testing.T) {
	denHome, repo := denTest(t)
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err == nil {
		t.Fatal("un repo introuvable doit faire échouer le spawn")
	} else if !strings.Contains(err.Error(), repo) {
		t.Errorf("le message doit nommer le repo manquant ; obtenu : %v", err)
	}
	if f.AAppele("create") {
		t.Errorf("aucun create ne doit avoir eu lieu ; appels : %v", f.Appels)
	}
}

func TestSpawnDetachNAttachePas(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !f.AAppele("create", "--name", "api") {
		t.Errorf("le create doit avoir lieu ; appels : %v", f.Appels)
	}
	if f.AAppele("exec") {
		t.Errorf("--detach ne doit pas attacher ; appels : %v", f.Appels)
	}
}

// Le profil agent est monté RW et doit exister : sbx créerait sinon un dossier
// vide au mount, et l'agent repartirait de zéro à chaque spawn.
func TestSpawnCreeLeProfilAgent(t *testing.T) {
	denHome, _ := denTest(t)
	_, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if _, err := os.Stat(filepath.Join(denHome, "agents", "claude")); err != nil {
		t.Errorf("le config_dir de l'agent doit exister : %v", err)
	}
}

func TestSpawnEcritLeMixin(t *testing.T) {
	denHome, _ := denTest(t)
	f, d := depsTest()

	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	spec := filepath.Join(denHome, "cache", "mixins", "api", "spec.yaml")
	contenu, err := os.ReadFile(spec)
	if err != nil {
		t.Fatalf("le mixin doit être écrit en %s : %v", spec, err)
	}
	if !strings.Contains(string(contenu), "github.com") {
		t.Errorf("le mixin doit porter l'egress de la cascade :\n%s", contenu)
	}
	// Et c'est bien lui le dernier --kit.
	argv := appelCommencantPar(f, "create")
	dernierKit := ""
	for i, a := range argv {
		if a == "--kit" && i+1 < len(argv) {
			dernierKit = argv[i+1]
		}
	}
	if dernierKit != filepath.Dir(spec) {
		t.Errorf("dernier --kit = %q, attendu %q", dernierKit, filepath.Dir(spec))
	}
}

func appelCommencantPar(f *sbx.Fake, tete string) []string {
	for _, a := range f.Appels {
		if len(a) > 0 && a[0] == tete {
			return a
		}
	}
	return nil
}
```

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/spawn/ -v`
Expected: FAIL — le paquet n'existe pas.

- [ ] **Step 3: Implémenter**

Créer `internal/spawn/spawn.go` :

```go
// Package spawn orchestre la séquence complète de `den <nest>` (spec §6).
//
// Il vit hors de internal/cli à dessein : c'est la logique la plus dense du
// projet, et elle doit être testable sans cobra ni tty.
package spawn

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/PillowPillow/den/internal/agent"
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
)

// Deps injecte les accès au monde, pour que la séquence entière soit testable
// sans microVM.
type Deps struct {
	Sbx    sbx.Runner
	Git    worktree.Git
	Policy policy.Options
	Sortie io.Writer
}

// Options porte les flags de `den <nest>`.
type Options struct {
	Nest     string
	Worktree string
	Agent    string
	Without  []string
	Only     []string
	Detach   bool
}

// Spawn exécute la séquence du spec §6, dans l'ordre :
// résolution → sélection repos → worktrees → profil agent → mixin →
// sbx create (ou attache si la sandbox vit déjà) → settle-loop → attache.
//
// L'ordre n'est pas une commodité : le settle-loop PRÉCÈDE l'attache parce
// qu'attacher avant que la policy soit posée produit exactement le « ça marche
// à moitié » que le spec §7 interdit.
func Spawn(ctx context.Context, denHome string, o Options, d Deps) error {
	// 1. Résolution de la cascade.
	g, err := config.LoadGlobal(denHome)
	if err != nil {
		return err
	}
	stacks, err := config.LoadStacks(denHome)
	if err != nil {
		return err
	}
	n, err := nest.LoadNest(denHome, o.Nest)
	if err != nil {
		return err
	}
	r, err := nest.Resolve(denHome, g, stacks, n, nest.Options{
		Agent: o.Agent, Without: o.Without, Only: o.Only,
	})
	if err != nil {
		return err
	}

	// Le nom se calcule AVANT tout effet de bord : un worktree non
	// sandboxable (« feature/123 ») doit être refusé sans avoir rien créé.
	nomSandbox, err := sbx.NomSandbox(o.Nest, o.Worktree)
	if err != nil {
		return err
	}

	// 2. Les repos doivent tous exister AVANT le moindre create (spec §11).
	for _, repo := range r.Repos {
		if _, err := os.Stat(repo.Path); err != nil {
			return fmt.Errorf(
				"nest %q : repo introuvable : %s — corrige `repos:` dans %s/nests/%s.yaml",
				o.Nest, repo.Path, denHome, o.Nest)
		}
	}

	// 3. Worktrees, si demandés. Le premier workspace doit rester le premier
	// repo : Sandbox.Workdir en dépend pour l'attache.
	workspaces := make([]string, 0, len(r.Repos)+2)
	for _, repo := range r.Repos {
		chemin := repo.Path
		if o.Worktree != "" {
			chemin, err = worktree.Assure(ctx, d.Git, r.WorktreeLayout, r.WorktreeRoot, o.Worktree, repo.Path)
			if err != nil {
				return err
			}
			fmt.Fprintf(d.Sortie, "worktree %s : %s\n", repo.Name(), chemin)
		}
		workspaces = append(workspaces, chemin)
	}

	// 4. Profil agent : monté RW, il doit exister — sinon sbx crée un dossier
	// vide au mount et l'agent repart de zéro à chaque spawn.
	if err := os.MkdirAll(r.AgentConfigDir, 0o755); err != nil {
		return fmt.Errorf("création du profil de l'agent %s (%s) : %w", r.AgentName, r.AgentConfigDir, err)
	}
	workspaces = append(workspaces, r.AgentConfigDir)
	if r.SSHMode == "mount" {
		if r.SSHDir == "" {
			return fmt.Errorf("ssh.mode vaut « mount » mais ssh.dir n'est pas déclaré dans %s/config.yaml", denHome)
		}
		workspaces = append(workspaces, r.SSHDir)
	}

	// 5. Mixin généré.
	m, err := agent.MixinDepuis(r, nomSandbox)
	if err != nil {
		return err
	}
	dirMixin, err := agent.EcrisMixin(denHome, nomSandbox, m)
	if err != nil {
		return err
	}

	// 6. Spawn-or-attach : un nom déjà vivant n'est pas une erreur (spec §11).
	vivante, err := sbx.Existe(ctx, d.Sbx, nomSandbox)
	if err != nil {
		return err
	}
	if vivante {
		fmt.Fprintf(d.Sortie, "sandbox %s déjà vivante : attache\n", nomSandbox)
	} else {
		kits := append([]string{}, r.Stack.Kits...)
		if r.Stack.Kit != "" {
			kits = append(kits, r.Stack.Kit)
		}
		argv, err := sbx.ArgvCreate(sbx.Create{
			Nom:        nomSandbox,
			Image:      r.Stack.Image,
			KitsStack:  kits,
			KitMixin:   dirMixin,
			Workspaces: workspaces,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(d.Sortie, "création de la sandbox %s (image %s)…\n", nomSandbox, r.Stack.Image)
		if _, err := d.Sbx.Run(ctx, argv...); err != nil {
			return err
		}
	}

	// 7. Settle-loop fail-closed AVANT toute attache.
	if len(r.Egress) > 0 {
		fmt.Fprintf(d.Sortie, "attente de la policy réseau (%d hôte(s))…\n", len(r.Egress))
	}
	if err := policy.Settle(ctx, d.Sbx, nomSandbox, r.Egress, d.Policy); err != nil {
		return err
	}

	// 8. Attache.
	if o.Detach {
		fmt.Fprintf(d.Sortie, "sandbox %s prête (détachée) — `den sh %s` pour y entrer\n",
			nomSandbox, nomSandbox)
		return nil
	}
	return Attache(ctx, d.Sbx, nomSandbox, premier(workspaces))
}

// Attache ouvre un shell interactif dans la sandbox.
//
// `sbx exec` et non `sbx run` : run attache la commande du FLAVOR de l'image
// (souvent `claude`), n'a aucun flag pour la remplacer, et son `-- ARGS` ne fait
// qu'ajouter des arguments.
func Attache(ctx context.Context, r sbx.Runner, nomSandbox, workdir string) error {
	argv := []string{"exec", "-it", nomSandbox}
	if workdir != "" {
		argv = []string{"exec", "-it", "-w", workdir, nomSandbox}
	}
	return r.Attach(ctx, append(argv, "bash", "-l")...)
}

func premier(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return filepath.Clean(s[0])
}
```

⚠️ `TestSpawnSequenceNominale` asserte `f.AAppele("exec", "-it", "api")`. Avec un workdir, l'argv
devient `exec -it -w <dir> api`. **Adapter les assertions du test** pour qu'elles correspondent à
l'argv réellement produit — ou, si l'on préfère un argv stable, placer `-w` **après** le nom de
sandbox (`sbx exec` accepte les flags avant `SANDBOX`, donc les deux formes marchent ; choisir
celle qui rend les tests les plus lisibles et s'y tenir).

- [ ] **Step 4: Lancer le test — il doit passer**

Run: `go test ./internal/spawn/ -v`
Expected: PASS sur tous les tests.

- [ ] **Step 5: Câbler la commande cobra**

Créer `internal/cli/spawn.go` :

```go
package cli

import (
	"github.com/PillowPillow/den/internal/policy"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
)

// configureSpawn fait de la racine elle-même la commande de spawn : `den <nest>`
// n'est pas une sous-commande, c'est l'argument par défaut. cobra retombe sur le
// RunE de la racine quand args[0] ne correspond à aucune sous-commande.
func configureSpawn(root *cobra.Command, denHome *string) {
	var o spawn.Options

	root.Use = "den <nest> [flags]"
	root.Args = cobra.MaximumNArgs(1)
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		o.Nest = args[0]
		home, err := config.Home(*denHome)
		if err != nil {
			return err
		}
		return spawn.Spawn(cmd.Context(), home, o, spawn.Deps{
			Sbx:    sbx.NewExec(""),
			Git:    worktree.NewGit(),
			Policy: policy.OptionsDefaut(),
			Sortie: cmd.OutOrStdout(),
		})
	}

	root.Flags().StringVarP(&o.Worktree, "worktree", "w", "", "worktree à propager sur tous les repos")
	root.Flags().StringVar(&o.Agent, "agent", "", "agent à utiliser (défaut : defaults.agent)")
	root.Flags().StringSliceVar(&o.Without, "without", nil, "exclure ces repos optionnels")
	root.Flags().StringSliceVar(&o.Only, "only", nil, "ne garder que ces repos optionnels")
	root.Flags().BoolVar(&o.Detach, "detach", false, "ne pas attacher de shell après le spawn")
}
```

Ajouter `"github.com/PillowPillow/den/internal/config"` aux imports.

Dans `internal/cli/root.go`, appeler `configureSpawn(root, &denHome)` **après** les
`root.AddCommand(...)`, juste avant le `return root`.

- [ ] **Step 6: Vérifier que les sous-commandes existantes ne sont pas capturées**

Créer `internal/cli/spawn_test.go` :

```go
package cli

import (
	"strings"
	"testing"
)

// La racine devient la commande de spawn : les sous-commandes existantes ne
// doivent surtout pas être avalées comme des noms de nest.
func TestLesSousCommandesRestentPrioritaires(t *testing.T) {
	sortie, err := executeCmd(t, "version")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.HasPrefix(sortie, "den ") {
		t.Errorf("`den version` doit rester la commande version ; obtenu : %q", sortie)
	}
}

func TestDenSansArgumentAfficheLAide(t *testing.T) {
	sortie, err := executeCmd(t)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(sortie, "nest") {
		t.Errorf("`den` seul doit afficher l'aide ; obtenu : %q", sortie)
	}
}
```

(Réutiliser le helper `executeCmd` réel du paquet.)

- [ ] **Step 7: Ajouter `internal/spawn` au layout du spec**

Dans `docs/superpowers/specs/2026-07-27-den-cli-design.md`, §12, bloc « Layout du dépôt », ajouter
sous `agent/` :

```
  spawn/                 # orchestration de `den <nest>` (spec §6), hors cli pour rester testable
```

- [ ] **Step 8: Lancer toute la suite**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 9: Commit**

```bash
git add internal/spawn/ internal/cli/ docs/
git commit -m "feat(spawn): den <nest> orchestre worktrees, mixin, create, settle-loop et attache"
```

---

## Task 13: `den ls`

Sans labels, `den ls` est `sbx ls --json` dont chaque nom est décomposé. Une sandbox dont le nest
n'est pas déclaré dans `~/.den/nests/` est affichée quand même, marquée — la masquer ferait
disparaître de la vue une VM bel et bien vivante sur la machine.

**Files:**
- Create: `internal/cli/ls.go`
- Create: `internal/cli/ls_test.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- Consumes: `sbx.Ls`, `sbx.Sandbox` (tâche 8), `nest.ListNests` (Plan 1).
- Produces: `func newLsCmd(denHome *string, r sbx.Runner) *cobra.Command`.

- [ ] **Step 1: Écrire le test qui échoue**

Créer `internal/cli/ls_test.go` :

```go
package cli

import (
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
)

func TestLsAfficheLesColonnes(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(
			`{"sandboxes":[{"name":"api.feat12","agent":"shell","status":"running","workspaces":["/w/api","/p"]}]}`)},
	}}

	sortie, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "ls")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	for _, attendu := range []string{"NAME", "NEST", "WORKTREE", "STATUS", "api.feat12", "api", "feat12", "running"} {
		if !strings.Contains(sortie, attendu) {
			t.Errorf("la sortie doit contenir %q ; obtenu :\n%s", attendu, sortie)
		}
	}
	// L'âge n'existe pas dans sbx ls --json : ne jamais prétendre le connaître.
	if strings.Contains(strings.ToUpper(sortie), "AGE") {
		t.Errorf("aucune colonne d'âge ne doit exister ; obtenu :\n%s", sortie)
	}
}

// Une sandbox dont le nest n'est pas déclaré reste visible, mais marquée : la
// masquer ferait disparaître de la vue une VM vivante sur la machine.
func TestLsMarqueLesSandboxesNonDeclarees(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(
			`{"sandboxes":[{"name":"api","status":"running"},{"name":"inconnue","status":"running"}]}`)},
	}}

	sortie, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "ls")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(sortie, "inconnue") {
		t.Errorf("une sandbox non déclarée doit rester visible ; obtenu :\n%s", sortie)
	}
	if !strings.Contains(sortie, "?") {
		t.Errorf("elle doit être marquée ; obtenu :\n%s", sortie)
	}
}

func TestLsAucuneSandbox(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)

	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(`{"sandboxes":[]}`)},
	}}

	sortie, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "ls")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(sortie, "aucune sandbox") {
		t.Errorf("obtenu :\n%s", sortie)
	}
}
```

Ce test a besoin de deux ajouts au harnais existant du paquet `cli` :

```go
// configMinimale : une config.yaml valide et sans surprise, réutilisable.
const configMinimale = `agents:
  claude:
    config_dir: /profil/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
`

// executeCmdAvecSbx exécute l'arbre de commandes avec un Runner sbx injecté.
func executeCmdAvecSbx(t *testing.T, r sbx.Runner, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmdAvec(doctor.DepsSysteme(), r)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}
```

⚠️ Adapter au harnais réel : le paquet possède déjà `executeCmd` et des helpers d'écriture. Si
`NewRootCmd` ne prend pas encore de Runner, c'est le Step 3 qui l'introduit.

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/cli/ -run TestLs -v`
Expected: FAIL — `undefined: executeCmdAvecSbx`, `unknown command "ls"`.

- [ ] **Step 3: Rendre le Runner injectable depuis la racine**

Dans `internal/cli/root.go`, extraire un constructeur paramétré et garder `NewRootCmd` comme façade.
C'est le même patron que `newDoctorCmd(denHome, deps)` du Plan 1 : les accès système entrent par
paramètre pour que les tests n'aient pas besoin du binaire réel.

```go
// NewRootCmd construit l'arbre de commandes avec les accès système réels.
func NewRootCmd() *cobra.Command {
	return NewRootCmdAvec(doctor.DepsSysteme(), sbx.NewExec(""))
}

// NewRootCmdAvec prend ses accès au monde en paramètre : c'est ce qui permet
// aux tests d'exercer `den ls`, `den sh` et `den rm` sans que sbx soit installé.
func NewRootCmdAvec(deps doctor.Deps, runner sbx.Runner) *cobra.Command {
	var denHome string

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
	root.AddCommand(newNestCmd(&denHome))
	root.AddCommand(newDoctorCmd(&denHome, deps))
	root.AddCommand(newLsCmd(&denHome, runner))
	configureSpawn(root, &denHome, runner)
	return root
}
```

⚠️ `configureSpawn` prend désormais le runner : adapter sa signature (tâche 12, Step 5) pour qu'il
utilise `runner` au lieu de `sbx.NewExec("")`.

- [ ] **Step 4: Implémenter `den ls`**

Créer `internal/cli/ls.go` :

```go
package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/spf13/cobra"
)

func newLsCmd(denHome *string, runner sbx.Runner) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "Liste les sandboxes vivantes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(boxes) == 0 {
				fmt.Fprintln(out, "aucune sandbox vivante")
				return nil
			}

			// Les nests déclarés servent uniquement à MARQUER les sandboxes
			// inconnues, jamais à les filtrer : une VM vivante doit rester
			// visible même si son nest a été supprimé depuis.
			declares := map[string]bool{}
			nests, _ := nest.ListNests(home) // un ~/.den cassé ne doit pas masquer sbx
			for _, n := range nests {
				declares[n.Name] = true
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tNEST\tWORKTREE\tSTATUS\tWORKSPACES")
			for _, b := range boxes {
				nomNest := b.Nest()
				if !declares[nomNest] {
					nomNest += " ?" // non déclaré dans ~/.den/nests
				}
				wt := b.Worktree()
				if wt == "" {
					wt = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n", b.Nom, nomNest, wt, b.Statut, len(b.Workspaces))
			}
			return w.Flush()
		},
	}
}
```

- [ ] **Step 5: Lancer le test — il doit passer**

Run: `go test ./internal/cli/ -v`
Expected: PASS sur tous les tests du paquet, y compris ceux du Plan 1.

- [ ] **Step 6: Lancer toute la suite**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/
git commit -m "feat(cli): den ls, attribution par le nom et marquage des sandboxes non declarees"
```

---

## Task 14: `den sh <name>`

Un shell dans une sandbox existante. Le `-w` vient du premier workspace remonté par `sbx ls --json` :
sans lui, l'utilisateur atterrit dans le home de la VM plutôt que dans son code.

**Files:**
- Create: `internal/cli/sh.go`
- Create: `internal/cli/sh_test.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- Consumes: `sbx.Ls` (tâche 8), `spawn.Attache` (tâche 12).
- Produces: `func newShCmd(runner sbx.Runner) *cobra.Command`.

- [ ] **Step 1: Écrire le test qui échoue**

Créer `internal/cli/sh_test.go` :

```go
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

// `sbx run` lancerait le flavor de l'image (souvent claude) : jamais.
func TestShNUtiliseJamaisSbxRun(t *testing.T) {
	f := &sbx.Fake{Reponses: map[string]sbx.Reponse{
		"ls --json": {Sortie: []byte(`{"sandboxes":[{"name":"api","workspaces":["/w"]}]}`)},
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
}
```

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/cli/ -run TestSh -v`
Expected: FAIL — `unknown command "sh"`.

- [ ] **Step 3: Implémenter**

Créer `internal/cli/sh.go` :

```go
package cli

import (
	"fmt"
	"sort"

	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

func newShCmd(runner sbx.Runner) *cobra.Command {
	return &cobra.Command{
		Use:   "sh <name>",
		Short: "Ouvre un shell dans une sandbox existante",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nom := args[0]
			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			for _, b := range boxes {
				if b.Nom == nom {
					// Le workdir vient du premier workspace : sans lui,
					// l'utilisateur atterrit dans le home de la VM, pas dans
					// son code.
					return spawn.Attache(cmd.Context(), runner, b.Nom, b.Workdir())
				}
			}

			noms := make([]string, 0, len(boxes))
			for _, b := range boxes {
				noms = append(noms, b.Nom)
			}
			sort.Strings(noms)
			if len(noms) == 0 {
				return fmt.Errorf("sandbox %q introuvable — aucune sandbox ne tourne", nom)
			}
			return fmt.Errorf("sandbox %q introuvable (vivantes : %v)", nom, noms)
		},
	}
}
```

Dans `internal/cli/root.go`, ajouter `root.AddCommand(newShCmd(runner))`.

- [ ] **Step 4: Lancer le test — il doit passer**

Run: `go test ./internal/cli/ -v`
Expected: PASS

- [ ] **Step 5: Lancer toute la suite et committer**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

```bash
git add internal/cli/
git commit -m "feat(cli): den sh attache un shell dans le workdir de la sandbox"
```

---

## Task 15: `den rm <name> [--keep-worktrees] [--force]`

Teardown. Le profil agent **persiste toujours** (c'est tout l'intérêt d'un `config_dir` monté RW) ;
les worktrees créés par den sont retirés sauf `--keep-worktrees`, et jamais s'ils sont sales sans
`--force`.

**Files:**
- Create: `internal/cli/rm.go`
- Create: `internal/cli/rm_test.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- Consumes: `sbx.Ls`, `sbx.DecomposeNom`, `worktree.Chemin`, `worktree.Retire`.
- Produces: `func newRmCmd(denHome *string, runner sbx.Runner, g worktree.Git) *cobra.Command`.

- [ ] **Step 1: Écrire le test qui échoue**

Créer `internal/cli/rm_test.go` :

```go
package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/PillowPillow/den/internal/sbx"
)

func lsAvec(noms ...string) map[string]sbx.Reponse {
	var b strings.Builder
	b.WriteString(`{"sandboxes":[`)
	for i, n := range noms {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"name":"` + n + `","status":"running","workspaces":["/w"]}`)
	}
	b.WriteString(`]}`)
	return map[string]sbx.Reponse{"ls --json": {Sortie: []byte(b.String())}}
}

func TestRmSupprimeLaSandbox(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	f := &sbx.Fake{Reponses: lsAvec("api")}

	if _, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !f.AAppele("rm", "--force", "api") {
		t.Errorf("appels : %v", f.Appels)
	}
}

// Le profil agent persiste : c'est toute la raison d'être d'un config_dir
// monté RW. Un den rm qui l'effacerait obligerait à refaire /login.
func TestRmNeToucheJamaisAuProfilAgent(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	profil := filepath.Join(denHome, "agents", "claude")
	if err := os.MkdirAll(profil, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{Reponses: lsAvec("api")}

	if _, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if _, err := os.Stat(profil); err != nil {
		t.Errorf("le profil agent doit survivre au rm : %v", err)
	}
}

func TestRmNomInconnu(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	f := &sbx.Fake{Reponses: lsAvec("api")}

	_, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "absente")
	if err == nil {
		t.Fatal("un nom inconnu doit produire une erreur")
	}
	if !strings.Contains(err.Error(), "api") {
		t.Errorf("le message doit lister les sandboxes vivantes ; obtenu : %v", err)
	}
	if f.AAppele("rm") {
		t.Errorf("aucun rm ne doit être tenté ; appels : %v", f.Appels)
	}
}

// --keep-worktrees : la sandbox part, les dossiers restent.
func TestRmKeepWorktreesNeTouchePasAuDisque(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	wt := filepath.Join(denHome, "worktrees", "feat12", "api")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}

	if _, err := executeCmdAvecSbx(t, f, "--den-home", denHome,
		"rm", "api.feat12", "--keep-worktrees"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("--keep-worktrees doit préserver %s : %v", wt, err)
	}
	if !f.AAppele("rm", "--force", "api.feat12") {
		t.Errorf("la sandbox doit tout de même être supprimée ; appels : %v", f.Appels)
	}
}

// Une sandbox sans worktree n'a rien à nettoyer : le nettoyage ne doit pas
// s'inventer un chemin.
func TestRmSansWorktreeNeNettoieRien(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configMinimale)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisNest(t, denHome, "api", "stack: devx\nrepos: []\n")
	f := &sbx.Fake{Reponses: lsAvec("api")}

	sortie, err := executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if strings.Contains(sortie, "worktree") {
		t.Errorf("aucun nettoyage de worktree ne doit être annoncé ; obtenu :\n%s", sortie)
	}
	_ = slices.Contains // garde l'import utile si les assertions évoluent
}
```

Ajouter `"os"` et `"path/filepath"` aux imports du fichier de test.

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/cli/ -run TestRm -v`
Expected: FAIL — `unknown command "rm"`.

- [ ] **Step 3: Implémenter**

Créer `internal/cli/rm.go` :

```go
package cli

import (
	"fmt"
	"sort"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/nest"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/worktree"
	"github.com/spf13/cobra"
)

func newRmCmd(denHome *string, runner sbx.Runner, g worktree.Git) *cobra.Command {
	var garderWorktrees, force bool

	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Détruit une sandbox (le profil agent persiste)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nom := args[0]
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}

			boxes, err := sbx.Ls(cmd.Context(), runner)
			if err != nil {
				return err
			}
			trouvee := false
			for _, b := range boxes {
				if b.Nom == nom {
					trouvee = true
					break
				}
			}
			if !trouvee {
				noms := make([]string, 0, len(boxes))
				for _, b := range boxes {
					noms = append(noms, b.Nom)
				}
				sort.Strings(noms)
				return fmt.Errorf("sandbox %q introuvable (vivantes : %v)", nom, noms)
			}

			out := cmd.OutOrStdout()

			// Les worktrees d'abord : si l'un est sale, on s'arrête AVANT de
			// détruire la sandbox. L'inverse laisserait l'utilisateur sans VM
			// et avec un message d'erreur sur un dossier.
			if !garderWorktrees {
				if err := nettoieWorktrees(cmd, home, nom, g, force, out); err != nil {
					return err
				}
			}

			if _, err := runner.Run(cmd.Context(), "rm", "--force", nom); err != nil {
				return err
			}
			fmt.Fprintf(out, "sandbox %s détruite (le profil de l'agent est conservé)\n", nom)
			return nil
		},
	}
	cmd.Flags().BoolVar(&garderWorktrees, "keep-worktrees", false,
		"conserver les worktrees créés par den")
	cmd.Flags().BoolVar(&force, "force", false,
		"supprimer les worktrees même s'ils portent des modifications non commitées")
	return cmd
}

// nettoieWorktrees retire les worktrees que den a créés pour cette sandbox.
// Best-effort sur la RÉSOLUTION (un nest supprimé depuis ne doit pas empêcher
// de détruire la sandbox), strict sur la SUPPRESSION (un worktree sale arrête
// tout — cf. worktree.Retire).
func nettoieWorktrees(cmd *cobra.Command, home, nomSandbox string, g worktree.Git, force bool, out io.Writer) error {
	nomNest, wt := sbx.DecomposeNom(nomSandbox)
	if wt == "" {
		return nil // pas de worktree : rien à nettoyer
	}

	gl, err := config.LoadGlobal(home)
	if err != nil {
		return err
	}
	n, err := nest.LoadNest(home, nomNest)
	if err != nil {
		fmt.Fprintf(out, "nest %q illisible : worktrees non nettoyés (%v)\n", nomNest, err)
		return nil
	}

	for _, repo := range n.Repos {
		chemin := worktree.Chemin(gl.WorktreeLayout, gl.WorktreeRoot, wt, repo.Path)
		if err := worktree.Retire(cmd.Context(), g, repo.Path, chemin, force); err != nil {
			return err
		}
		fmt.Fprintf(out, "worktree retiré : %s\n", chemin)
	}
	return nil
}
```

Ajouter `"io"` aux imports. Dans `internal/cli/root.go`, ajouter
`root.AddCommand(newRmCmd(&denHome, runner, worktree.NewGit()))`.

- [ ] **Step 4: Lancer le test — il doit passer**

Run: `go test ./internal/cli/ -v`
Expected: PASS

- [ ] **Step 5: Verrouiller l'ordre de destruction**

L'ordre « worktrees d'abord, sandbox ensuite » est une propriété de sûreté : l'inverse laisserait
l'utilisateur sans VM **et** avec un message d'erreur sur un dossier. Ajouter à
`internal/cli/rm_test.go` :

```go
func TestRmNeDetruitPasLaSandboxSiUnWorktreeEstSale(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, `agents:
  claude:
    config_dir: /profil/claude
    update: "claude update"
defaults:
  agent: claude
  stack: devx
worktree_layout: central
worktree_root: `+filepath.Join(denHome, "worktrees")+`
`)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")

	// Dépôt git réel + worktree réel, sale.
	repo := filepath.Join(t.TempDir(), "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@exemple.test"},
		{"config", "user.name", "T"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", c...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v : %v\n%s", c, err, out)
		}
	}
	ecrisNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: "+repo+" }\n")

	chemin, err := worktree.Assure(context.Background(), worktree.NewGit(),
		"central", filepath.Join(denHome, "worktrees"), "feat12", repo)
	if err != nil {
		t.Fatalf("préparation du worktree : %v", err)
	}
	if err := os.WriteFile(filepath.Join(chemin, "brouillon.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Reponses: lsAvec("api.feat12")}
	_, err = executeCmdAvecSbx(t, f, "--den-home", denHome, "rm", "api.feat12")

	if err == nil {
		t.Fatal("un worktree sale doit faire échouer le rm")
	}
	if !strings.Contains(err.Error(), chemin) {
		t.Errorf("le message doit nommer le worktree fautif ; obtenu : %v", err)
	}
	// LA propriété : la sandbox est INTACTE, et le worktree aussi.
	if f.AAppele("rm", "--force", "api.feat12") {
		t.Errorf("la sandbox ne doit PAS avoir été détruite ; appels : %v", f.Appels)
	}
	if _, err := os.Stat(filepath.Join(chemin, "brouillon.txt")); err != nil {
		t.Errorf("le travail non commité doit être intact : %v", err)
	}

	// Et avec --force, tout part.
	f2 := &sbx.Fake{Reponses: lsAvec("api.feat12")}
	if _, err := executeCmdAvecSbx(t, f2, "--den-home", denHome,
		"rm", "api.feat12", "--force"); err != nil {
		t.Fatalf("avec --force, le rm doit passer : %v", err)
	}
	if !f2.AAppele("rm", "--force", "api.feat12") {
		t.Errorf("appels : %v", f2.Appels)
	}
}
```

Ajouter `"context"`, `"os/exec"` et `"github.com/PillowPillow/den/internal/worktree"` aux imports
du fichier de test.

Run: `go test ./internal/cli/ -run TestRmNeDetruitPas -v`
Expected: PASS

- [ ] **Step 6: Lancer toute la suite et committer**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

```bash
git add internal/cli/
git commit -m "feat(cli): den rm nettoie les worktrees avant la sandbox, profil agent preserve"
```

---

## Task 16: `ListNests` tolérante — la dette du Plan 1

Handoff §9 : `ListNests` échoue en bloc au premier nest illisible. Un seul `nests/casse.yaml` avec
une clé mal orthographiée fait sortir `den nest ls` en erreur **sans lister les autres**, et masque
toute la section nests de `den doctor`. Le décodage strict (décision 12) a élargi la classe des
« illisibles », donc la question a gagné en poids.

Décision de l'utilisateur : **lister les sains, signaler les cassés**. `LoadNest` — qui reçoit un
nom précis — reste dur : quand on demande *ce* nest-là, une erreur est la seule réponse honnête.

**Files:**
- Modify: `internal/nest/nest.go`
- Modify: `internal/nest/nest_test.go`
- Modify: `internal/cli/nest.go`
- Modify: `internal/cli/nest_test.go`
- Modify: `internal/doctor/doctor.go`
- Modify: `internal/doctor/doctor_test.go`
- Modify: `internal/cli/ls.go` (appelant)

**Interfaces:**
- Produces: signature **changée** —
  `func ListNests(denHome string) ([]*Nest, []NestCasse, error)` avec
  `type NestCasse struct { Nom string; Err error }`.
  La troisième valeur est réservée aux échecs **structurels** (dossier `nests/` illisible), pas aux
  nests individuels.

- [ ] **Step 1: Écrire le test qui échoue**

Ajouter à `internal/nest/nest_test.go` :

```go
func TestListNestsListeLesSainsEtSignaleLesCasses(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "api", "stack: devx\nrepos: []\n")
	ecrisNest(t, denHome, "casse", "stack: devx\negres:\n  - typo.exemple.test\n")
	ecrisNest(t, denHome, "web", "stack: devx\nrepos: []\n")

	nests, casses, err := ListNests(denHome)
	if err != nil {
		t.Fatalf("un nest fautif ne doit pas être une erreur structurelle : %v", err)
	}

	if len(nests) != 2 || nests[0].Name != "api" || nests[1].Name != "web" {
		t.Errorf("les nests sains doivent être listés et triés ; obtenu %v", nomsDe(nests))
	}
	if len(casses) != 1 || casses[0].Nom != "casse" {
		t.Fatalf("le nest fautif doit être signalé ; obtenu %v", casses)
	}
	// Le diagnostic doit rester exploitable : fichier, ligne, clé.
	msg := casses[0].Err.Error()
	for _, attendu := range []string{"casse.yaml", "egres"} {
		if !strings.Contains(msg, attendu) {
			t.Errorf("le diagnostic doit contenir %q ; obtenu : %s", attendu, msg)
		}
	}
}

func TestListNestsToutSain(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "api", "stack: devx\nrepos: []\n")

	nests, casses, err := ListNests(denHome)
	if err != nil || len(nests) != 1 || len(casses) != 0 {
		t.Errorf("nests=%v casses=%v err=%v", nomsDe(nests), casses, err)
	}
}

func TestListNestsDossierAbsent(t *testing.T) {
	nests, casses, err := ListNests(t.TempDir())
	if err != nil {
		t.Errorf("un ~/.den sans dossier nests n'est pas une erreur : %v", err)
	}
	if len(nests) != 0 || len(casses) != 0 {
		t.Errorf("nests=%v casses=%v", nests, casses)
	}
}

// Demander UN nest précis reste dur : répondre « il est cassé » est la seule
// réponse honnête quand on a nommé celui-là.
func TestLoadNestResteDur(t *testing.T) {
	denHome := t.TempDir()
	ecrisNest(t, denHome, "casse", "egres: [x]\n")

	if _, err := LoadNest(denHome, "casse"); err == nil {
		t.Fatal("LoadNest doit rester dur sur un nest illisible")
	}
}

func nomsDe(nests []*Nest) []string {
	out := make([]string, 0, len(nests))
	for _, n := range nests {
		out = append(out, n.Name)
	}
	return out
}
```

- [ ] **Step 2: Lancer le test pour vérifier qu'il échoue**

Run: `go test ./internal/nest/ -run TestListNests -v`
Expected: FAIL — `assignment mismatch: 3 variables but ListNests returns 2 values`.

- [ ] **Step 3: Implémenter**

Dans `internal/nest/nest.go`, remplacer `ListNests` :

```go
// NestCasse est un nest présent sur disque mais non chargeable.
type NestCasse struct {
	Nom string
	Err error
}

// ListNests charge tous les nests déclarés, triés par nom.
//
// Un nest illisible ne masque PAS les autres : il est renvoyé à part. Le
// décodage strict rend un simple `egres:` fatal au chargement, et faire
// disparaître toute la liste pour une faute de frappe dans un fichier laisserait
// l'utilisateur sans le moyen de voir lequel — `den nest ls` et `den doctor`
// sont précisément les outils censés le lui dire.
//
// L'erreur renvoyée est réservée aux échecs STRUCTURELS (dossier nests/
// illisible) : là, il n'y a rien à lister du tout.
func ListNests(denHome string) ([]*Nest, []NestCasse, error) {
	racine := filepath.Join(denHome, "nests")
	entrees, err := os.ReadDir(racine)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("lecture de %s : %w", racine, err)
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
	var casses []NestCasse
	for _, nom := range noms {
		n, err := LoadNest(denHome, nom)
		if err != nil {
			casses = append(casses, NestCasse{Nom: nom, Err: err})
			continue
		}
		nests = append(nests, n)
	}
	return nests, casses, nil
}
```

- [ ] **Step 4: Mettre à jour `den nest ls`**

Dans `internal/cli/nest.go`, `newNestLsCmd` : après la boucle d'affichage et le `w.Flush()`,
signaler les cassés et **sortir en erreur** — la liste s'affiche, mais le code de retour dit
qu'il y a quelque chose à réparer.

```go
			nests, casses, err := nest.ListNests(home)
			if err != nil {
				return err
			}
			if len(nests) == 0 && len(casses) == 0 {
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
			if err := w.Flush(); err != nil {
				return err
			}

			if len(casses) > 0 {
				fmt.Fprintln(cmd.OutOrStdout())
				for _, c := range casses {
					fmt.Fprintf(cmd.OutOrStdout(), "! %s : %v\n", c.Nom, c.Err)
				}
				return fmt.Errorf("%d nest(s) illisible(s) sur %d", len(casses), len(nests)+len(casses))
			}
			return nil
```

- [ ] **Step 5: Mettre à jour `doctor`**

Dans `internal/doctor/doctor.go`, section 6, remplacer le bloc d'appel :

```go
	// 6. nests : stack référencée existante, repos présents sur disque
	nests, casses, err := nest.ListNests(denHome)
	if err != nil {
		ajoute("nests", false, "%v", err)
		return checks
	}
	// Un nest cassé est signalé nommément et n'empêche pas de diagnostiquer les
	// autres : c'est précisément le rôle de doctor.
	for _, c := range casses {
		ajoute("nest "+c.Nom, false, "illisible : %v", c.Err)
	}
```

Le reste de la section (boucle sur `nests`) est inchangé. Ajuster la ligne finale :

```go
	if len(nests) > 0 || len(casses) > 0 {
		ajoute("nests", len(casses) == 0, "%d déclaré(s), %d illisible(s)", len(nests), len(casses))
	}
```

- [ ] **Step 6: Mettre à jour `den ls`**

Dans `internal/cli/ls.go`, l'appel `nests, _ := nest.ListNests(home)` devient
`nests, _, _ := nest.ListNests(home)`. Le commentaire existant (« un ~/.den cassé ne doit pas
masquer sbx ») reste valable et vaut maintenant aussi pour les nests individuels.

- [ ] **Step 7: Ajouter le test doctor**

Ajouter à `internal/doctor/doctor_test.go` :

```go
func TestRunSignaleUnNestCasseSansMasquerLesAutres(t *testing.T) {
	denHome := t.TempDir()
	ecrisConfig(t, denHome, configValide)
	ecrisStack(t, denHome, "devx", "image: devx:v1\n")
	ecrisNest(t, denHome, "sain", "stack: devx\nrepos: []\n")
	ecrisNest(t, denHome, "casse", "egres: [x]\n")

	checks := Run(denHome, depsTest())

	var vuCasse, vuSain bool
	for _, c := range checks {
		if c.Nom == "nest casse" && !c.OK {
			vuCasse = true
		}
		if c.Nom == "nests" {
			vuSain = true
		}
	}
	if !vuCasse {
		t.Errorf("le nest cassé doit être signalé ; checks : %+v", checks)
	}
	if !vuSain {
		t.Errorf("la section nests doit rester présente ; checks : %+v", checks)
	}
}
```

(Réutiliser les helpers et constantes réels du paquet `doctor`.)

- [ ] **Step 8: Lancer toute la suite**

Run: `go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 9: Mettre à jour le handoff**

Dans `docs/superpowers/handoffs/HANDOFF.md`, §9, supprimer la puce « **`ListNests` échoue en bloc
au premier nest illisible** » — la dette est réglée.

- [ ] **Step 10: Commit**

```bash
git add internal/ docs/
git commit -m "fix(nest): un nest illisible n'en masque plus les autres (dette plan 1)"
```

---

## Task 17: Exercer le binaire assemblé sur des configurations hostiles

Leçon de process du Plan 1, consignée au handoff §11 : **les trois bugs trouvés par la revue finale
sont tous sortis d'une manipulation du binaire assemblé sur des configurations adverses** — aucun de
la lecture du code, aucun des revues par tâche. Cette tâche existe pour reproduire ce mode de
détection, pas pour faire un pas-à-pas nominal.

**Files:**
- Create: `internal/cli/hostile_test.go`
- Modify: selon ce que la tâche révèle.

**Interfaces:**
- Consumes: tout.
- Produces: des correctifs, ou la preuve écrite qu'il n'y en avait pas à faire.

- [ ] **Step 1: Construire le binaire et l'exercer à la main**

Run:
```bash
cd /Users/polochon/Development/Pillow/den
go build -o /tmp/den ./cmd/den
export DEN_HOSTILE=$(mktemp -d)
mkdir -p "$DEN_HOSTILE"/{nests,stacks/devx}
```

Puis exercer **chacun** de ces cas et **noter la sortie obtenue** :

| # | Configuration hostile | Attendu |
|---|---|---|
| 1 | `~/.den` totalement vide | message nommant `config.yaml`, pas un panic |
| 2 | `config.yaml` vide (0 octet) | `doctor` liste les champs requis manquants |
| 3 | nest référençant une stack inexistante | erreur nommant la stack **et** listant les stacks déclarées |
| 4 | `den nest-inexistant` | erreur nommant le chemin attendu, aucun appel sbx |
| 5 | `den api -w feature/123` | refus nommant le `/`, **aucun** worktree créé |
| 6 | `den ../../etc/passwd` | refus (garde `ValiderNom` du Plan 1) |
| 7 | nest avec `repos: []` | `sbx create` a-t-il au moins un workspace ? (le profil agent suffit-il ?) |
| 8 | `worktree_root` relatif dans `config.yaml` | chemin absolu dans l'argv, jamais relatif |
| 9 | `config_dir` d'agent pointant sur un fichier, pas un dossier | erreur claire, pas un mount silencieux |
| 10 | nest déclarant `agents: { inconnu: /x }` | ignoré ou signalé — mais jamais un crash |
| 11 | deux nests, l'un cassé | `den nest ls` liste le sain (tâche 16) |
| 12 | `egress` contenant une chaîne vide | absente du mixin (`UnionEgress` filtre déjà) |

- [ ] **Step 2: Consigner chaque écart**

Pour chaque ligne dont la sortie réelle diffère de l'attendu, écrire d'abord un **test de
régression** dans `internal/cli/hostile_test.go`, le voir échouer, puis corriger.

Modèle :

```go
package cli

import (
	"strings"
	"testing"
)

// Cas 7 : un nest sans repo. `sbx create` exige au moins un chemin ; le profil
// agent en est un, mais l'utilisateur doit comprendre ce qu'il obtient.
func TestSpawnNestSansRepo(t *testing.T) {
	// … monter un ~/.den temporaire, exécuter, asserter le message …
}
```

- [ ] **Step 3: Vérifier qu'aucun chemin de la machine ne fuit dans un artefact**

Le mixin est écrit sous `~/.den/cache/` et part dans la VM. Un chemin hôte y est **attendu** (sbx
monte au même chemin), mais un `$HOME` expansé par erreur ne l'est pas.

Run:
```bash
cd /Users/polochon/Development/Pillow/den
grep -rn "$HOME" internal/agent/testdata/ || echo "aucun home expanse dans les goldens"
grep -c 'export PATH' internal/agent/testdata/fraicheur-claude.golden
```
Expected: la première commande n'affiche que des `$HOME` **littéraux** (non expansés) ; la seconde
affiche `1`.

- [ ] **Step 4: Vérifier l'hermétisme de la suite**

Le Plan 1 avait établi cet invariant ; ce plan ajoute des modules qui touchent le monde.

Run:
```bash
cd /Users/polochon/Development/Pillow/den
grep -rn "os/exec" --include="*_test.go" internal/ \
  | grep -v "internal/worktree" | grep -v "internal/spawn" | grep -v "internal/cli/rm_test.go"
grep -rn "net/http" --include="*_test.go" internal/
grep -rln "os.Getenv(\"HOME\")\|UserHomeDir" --include="*_test.go" internal/
grep -rn "\"sbx\"" --include="*_test.go" internal/
```
Expected: aucune des quatre ne renvoie de ligne. Seuls `worktree`, `spawn` et `cli/rm_test.go` ont
le droit d'invoquer `git` (ils ont besoin de dépôts réels) ; **aucun** test ne doit invoquer `sbx`,
faire du réseau, ni lire le home réel. Tout écart est à corriger, pas à documenter.

- [ ] **Step 5: Vérifier qu'aucun test ne touche au vrai `~/.den`**

Run:
```bash
cd /Users/polochon/Development/Pillow/den
ls ~/.den 2>/dev/null && cp -R ~/.den /tmp/den-backup-avant
go test -count=1 ./...
diff -r ~/.den /tmp/den-backup-avant 2>/dev/null && echo "~/.den intact" || echo "~/.den ABSENT (normal) ou MODIFIE (bug)"
```
Expected: `~/.den intact`, ou l'absence de `~/.den` avant comme après.

- [ ] **Step 6: Nettoyer et lancer la suite complète**

Run:
```bash
cd /Users/polochon/Development/Pillow/den
rm -rf "$DEN_HOSTILE" /tmp/den /tmp/den-backup-avant
go test -count=1 ./... && go vet ./... && gofmt -l .
```
Expected: tous les paquets `ok`, `go vet` silencieux, `gofmt -l` n'imprime rien.

- [ ] **Step 7: Commit**

```bash
git add internal/
git commit -m "test(cli): regressions issues de l'exercice du binaire sur des configs hostiles"
```

Si aucun écart n'a été trouvé, committer tout de même les tests de régression écrits au Step 2 pour
les cas les plus risqués (5, 7, 8) — leur valeur est de **rester** vrais.

---

## Ce que ce plan NE livre pas

À écrire explicitement dans le handoff en fin d'exécution :

- **`den ports`** et tout `internal/ports` — Plan 3. La fenêtre déterministe, le scan
  anti-collision et le tunnel SSH imprimé ne sont pas abordés ici. Les champs `Ports` du nest sont
  chargés (Plan 1) et affichés par `den nest show`, mais **rien ne les publie**.
- **Le flag `-i`** (checklist interactive des repos optionnels) — Plan 3. `--without` / `--only`
  couvrent le besoin en attendant.
- **`den build`** et le DAG des stacks — Plan 4. `den <nest>` échoue si l'image de la stack n'existe
  pas, avec le message du spec §11 (« lance `den build <stack>` ») — vérifier que ce message sort
  bien de `sbx create` et le reformuler si nécessaire.
- **Le smoke e2e réel** (un vrai spawn sur une vraie machine avec sbx installé) : impossible ici,
  `sbx` n'étant pas dans la sandbox de développement. **C'est le premier geste à faire après ce
  plan**, et c'est là que se révéleront les écarts restants entre l'argv assemblé et le
  comportement réel de sbx v0.35.0.

---

## Risques connus de ce plan

1. **L'argv `create` n'a jamais été exécuté contre le vrai `sbx`.** Les golden files figent ce que
   le sondage de `--help` permet de déduire, pas ce que sbx accepte. Le premier spawn réel est un
   test, et il faut le traiter comme tel.
2. **`sbx exec -it` dans un test ne prouve rien de l'interactivité.** `Fake.Attach` n'exerce aucun
   tty. Si l'attache se révèle bancale au smoke e2e, la cause sera dans `Exec.Attach`, pas dans la
   logique.
3. **La sémantique de `--clone`** n'est pas utilisée ici, alors que le spike SSH note qu'un chemin
   passé à `sbx create` devient un mount qui **écrase** le dossier hôte s'il est vide. Le worktree
   étant créé par den *avant* le create, il n'est jamais vide — mais c'est un invariant à ne pas
   casser : ne jamais passer à `sbx create` un chemin que den n'a pas garanti peuplé.
4. **`sbx ls --json` sans sandbox** n'a pas été observé. Le champ `sandboxes` pourrait valoir `null`
   plutôt que `[]` ; le décodage Go traite les deux identiquement (slice nil), donc le risque est
   couvert — mais si la clé change de nom, `den ls` deviendra silencieusement vide. Le test
   `TestLsSortieIllisible` ne couvre pas ce cas : un JSON valide au mauvais schéma passe.
5. **`ssh.mode: agent-forward` n'ajoute aucun argument** dans ce plan : le mode `mount` monte
   `ssh.dir` en workspace, `none` ne fait rien, et `agent-forward` — le **défaut** — repose sur le
   fait que sbx forwarde le socket de l'agent SSH de l'hôte tout seul. C'est **plausible mais non
   vérifié** : le `run.sh` réel ne pose rien et l'accès git fonctionne, ce qui va dans ce sens, mais
   le spike SSH exportait un `SSH_AUTH_SOCK` explicite pour sa sonde. Si `git push` échoue depuis la
   VM au smoke e2e, c'est le premier endroit où regarder — et le correctif serait de propager
   `SSH_AUTH_SOCK` à l'environnement du `sbx create`, pas de le mettre dans le mixin (une valeur de
   socket hôte n'a de sens que pour le processus qui lance la VM).
6. **L'ordre des `--kit` n'est vérifié que par nos propres tests.** Si sbx layerait les kits dans
   l'ordre inverse de la ligne de commande, la commande de fraîcheur cesserait d'être en dernier et
   le piège du dispatcher se rouvrirait — silencieusement. À confirmer au smoke e2e en lisant
   `/var/log/sbx-kit-startup.log` : les kits doivent y apparaître dans l'ordre de l'argv.
