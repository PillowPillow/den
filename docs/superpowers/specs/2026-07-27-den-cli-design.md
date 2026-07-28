# Design — `den`, CLI générique pour sandboxes sbx

**Date :** 2026-07-27
**Auteur :** Nicolas Gaignoux (conception assistée)
**Statut :** validé en brainstorming, prêt pour le plan d'implémentation
**North star :** rendre l'usage de `sbx` **simple et répétable** — démarrer une sandbox multi-projet
en une commande, sans retaper mixin/kits/policy à la main. La microVM reste la frontière ;
**seul objectif : protéger la machine hôte** (pas l'infra partagée).

---

## 1. Positionnement & périmètre

- **CLI 100 % générique** en Go, nommée **`den`**. Elle wrappe `sbx` et lit un dossier de
  config **`~/.den/`** qui est **la source unique**. Le dépôt `sbx-devbox` devient un simple
  **exemple** à recopier, pas une dépendance.
- **Périmètre v1 = runtime + build** :
  - *runtime* : spawn-or-attach, shell, ls, ports, rm d'une sandbox ; multi-projet natif.
  - *build* : orchestration DAG des images (`den build`), réutilise les `build.sh` existants.
- **Interactif d'abord.** Le flux **agent autonome** (VM éphémère `--clone`, review→merge,
  prune) est **réservé dans le vocabulaire** mais **hors v1**.
- La CLI vit dans un **nouveau dépôt** (elle-même). `~/.den/` contient la config utilisateur.

### Non-objectifs v1
- Pas de flux autonome (`den agent` / `den review`).
- Pas de sync distant de kits (`den sync`).
- Pas de snapshot/vendoring de plugins agent.
- Pas de sécurisation de l'infra partagée.
- Pas de registry / CI de distribution (solo, build local).

---

## 2. Modèle d'objets & vocabulaire

| Concept | Terme | Emplacement |
|---|---|---|
| La CLI + le home de config | **den** / `~/.den/` | binaire `den` |
| Recette d'**image** buildable (cattle) | **stack** | `~/.den/stacks/<n>/` |
| Overlay env/policy (artefact natif sbx) | **kit** | dans la stack (`kit`), + kits transverses `~/.den/kits/<n>/` |
| **Objet spawnable** (repos + stack + egress + ports) | **nest** 🕳️ | `~/.den/nests/<n>.yaml` |
| La VM qui tourne | *sandbox* (terme sbx) | `sbx ls` |
| Profil d'un agent IA (Claude, Codex…) | **agent profile** | `config_dir` monté RW |

**Identité d'un objet = son chemin.** Une stack est nommée par son dossier (`stacks/<n>/`), un nest
par le basename de son fichier (`nests/<n>.yaml`). Aucun objet ne porte son nom dans son contenu :
une seule source d'identité, non falsifiable.

Un **nest** est un terrier multi-galeries : plusieurs repos co-montés dans une seule VM. On le
**spawn** ; on peut y propager un **worktree** sur tous ses repos d'un coup.

**Contrainte de nommage.** Le nom d'un nest devient un nom de sandbox, que `sbx create --name`
restreint à « letters, numbers, hyphens, periods, plus signs and minus signs ». den impose plus
strict encore sur les nests et les worktrees : `[A-Za-z0-9][A-Za-z0-9+-]*`, **le point exclu** — il
est réservé au rôle de séparateur dans `<nest>.<worktree>` — et le **premier caractère alphanumérique
obligatoire** : un nom qui commence par `-` ou `+` est indiscernable d'un flag, aussi bien pour
`sbx create --name` que pour den lui-même (`den nest show -api` échoue sur un flag inconnu avant
même d'atteindre la résolution du nest). Un `-w feature/123` est donc refusé avec un message
actionnable, jamais normalisé en silence : normaliser casserait l'aller-retour
`den <nest> -w <wt>` → nom de sandbox → `den ls`.

---

## 3. Layout de `~/.den/`

```
~/.den/
  config.yaml              # défauts globaux (agents, ssh, egress baseline, worktree, defaults)
  stacks/
    devx/
      stack.yaml           # image + parent (DAG) + kit + egress stack
      build.sh             # produit l'image devx:v1 (script existant)
      kit/spec.yaml        # overlay env/policy de la stack
    dgdevx/
      stack.yaml           # parent: devx
      build.sh · kit/spec.yaml
  nests/
    review.yaml
    fullstack.yaml
  kits/                    # kits transverses non-egress, layerés avant `kit` (cf. §4.2)
    ssh-known-hosts/
  worktrees/               # worktrees générés (layout central par défaut)
    <wt>/<repo>/
  cache/                   # optionnel, reconstructible — jamais source de vérité
```

La **vérité de « ce qui tourne »** vient de `sbx ls --json` : l'identité de chaque sandbox est
attribuée à un nest (et un worktree) par décomposition de son **nom** — **pas de base de données
parallèle** (approche A). Un cache reconstructible n'est ajouté que si un besoin de statut riche
émerge.

---

## 4. Schémas de configuration

### 4.1 `config.yaml` (défauts globaux)

```yaml
agents:                                  # registre générique — Claude aujourd'hui, Codex demain
  claude:
    config_dir: ~/.den/agents/claude
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }   # {config_dir} résolu au chemin in-VM (== hôte)
    bin_dirs: ["$HOME/.local/bin", "$HOME/.claude/local"]  # cf. §9 (PATH du dispatcher)
    update: "claude update"                     # commande de fraîcheur, jouée au boot
  codex:
    config_dir: ~/.den/agents/codex
    env: { CODEX_HOME: "{config_dir}" }
    bin_dirs: ["$HOME/.local/bin"]
    update: "codex --upgrade"                   # à confirmer le jour où codex arrive
defaults:
  agent: claude
  stack: devx
ssh:
  mode: agent-forward                    # agent-forward (défaut) | mount | none
  dir: ~/.ssh_sbx                        # utilisé si mode=mount (clé dédiée révocable)
worktree_layout: central                 # central | per-repo
worktree_root: ~/.den/worktrees
egress:                                  # allowlist baseline, TOUTES sandboxes
  - api.anthropic.com
  - github.com
  - registry.npmjs.org
```

### 4.2 `stacks/<n>/stack.yaml` (recette image)

```yaml
image: dgdevx:v1        # passé à `sbx create --template`
parent: devx            # DAG de build (build devx avant dgdevx)
kit: ./kit              # kit par défaut de la stack (env + egress toolchain)
kits:                   # optionnel : kits transverses layerés AVANT `kit`
  - ../../kits/ssh-known-hosts
egress: []              # ajouts egress niveau stack
```

Les chemins de `kit` et `kits` sont résolus **relativement au dossier de la stack**. L'ordre de
`kits` est préservé : c'est un ordre de layering, pas un ensemble.

### 4.3 `nests/<n>.yaml` (objet spawnable)

```yaml
stack: dgdevx
env:                                 # optionnel, per-nest → injecté via le mixin généré
  SOME_VAR: value
egress:                              # optionnel, per-nest → caps.network.allow scopé sandbox
  - 10.22.11.54:27017                # ex. IP:port DB projet
repos:
  - { path: ~/dev/review-mgmt }               # requis
  - { path: ~/dev/front-app, optional: true } # décochable à l'interactif
ports:
  base: 9100                         # optionnel ; sinon dérivé du hash(nom)
  publish:
    - { name: vite, container: 5173, open: true }        # host base+0
    - { name: api,  container: 3000 }                    # host base+1
    - { name: cdp,  container: 9223, loopback_lock: true }  # host base+2, bind 127.0.0.1 forcé
agents:                              # optionnel : override du config_dir PAR agent
  claude: ~/.den/agents/claude-fullstack
  codex:  ~/dev/fullstack/.codex
```

**Règles de fusion** (cascade) : `global ← stack ← nest ← flags CLI`.
**Egress effectif** = `baseline ∪ stack.egress ∪ nest.egress` (dédupliqué), appliqué **scopé à la
sandbox**.

---

## 5. Surface de commandes (v1)

| Commande | Rôle |
|---|---|
| `den <nest> [-w <wt>] [--without r] [--only r] [-i] [--agent a] [--detach]` | **spawn-or-attach** + shell |
| `den ls` | sandboxes vivantes (`sbx ls --json` filtré sur le motif de nommage, colonnes nom/nest/worktree/statut/workspaces) |
| `den sh <name>` | shell dans une sandbox existante |
| `den ports <nest> [--add H:C]` | **publie à la demande** la fenêtre déclarée + affiche le tableau |
| `den rm <name> [--keep-worktrees]` | teardown (profil agent persiste ; worktrees nettoyés sauf `--keep`) |
| `den build [<stack>] [--force]` | build image(s), ordre DAG |
| `den doctor` | valide config, teste egress, présence/login sbx |
| `den nest ls` / `den nest show <n>` | inspecter les nests déclarés |

Réservé (hors v1, nommage figé) : `den agent <nest> [ticket]`, `den review <name>`.

---

## 6. Data flow du spawn — `den <nest> [-w <wt>] …`

1. **Résolution.** Charge `config.yaml` + `nests/<nest>.yaml` + `stacks/<stack>/stack.yaml`.
   Fusion en cascade.
2. **Sélection des repos.** Requis toujours inclus ; optionnels filtrés par `--without`/`--only`
   ou **checklist interactive** (`-i`).
3. **Worktrees** (si `-w`). Pour chaque repo sélectionné : `git worktree add` de `<wt>` au chemin
   résolu (`worktree_root/<wt>/<repo>` en central, `<repo>/.den/<wt>` en per-repo). Branche `<wt>`
   depuis la branche par défaut du repo, ou checkout si elle existe déjà. **Idempotent** (skip si
   déjà présent). Conflit (branche différente sur ce worktree) → stop actionnable.
4. **Profil agent** (orthogonal à la stack — sans effet quel que soit le template). Résout l'agent
   actif (défaut global ou `--agent`) ; résout son `config_dir` (**override nest s'il existe, sinon
   global**) ; garantit l'existence du dossier ; le monte **RW**.
5. **Mixin généré.** `den` génère **un seul kit jetable** portant : les **env vars de l'agent**
   (`{config_dir}` → chemin in-VM) et les **env nest**, toutes deux sous **`environment.variables`** ;
   l'**egress nest** en **`caps.network.allow`** ; et en **dernière** `commands.startup` la
   **commande de fraîcheur de l'agent** (§9.1). Dernière et pas ailleurs : elle est fail-closed, et
   le dispatcher sbx interrompt toute la suite au premier échec.
6. **Assemblage `sbx create`** :
   `--name <nest>[.<wt>]`, `--template <stack.image>`,
   `--kit <stacks/<stack>/kits[i]>…  --kit stacks/<stack>/kit  --kit <mixin généré>`
   (**le mixin généré reste le dernier `--kit`** — même raison qu'au point 5),
   agent positionnel **`shell`** (obligatoire : `sbx create [flags] AGENT PATH [PATH...]`), puis
   positionnels = chemins worktree/repo + `config_dir` (+ `~/.ssh_sbx` si `ssh.mode=mount`).
   **Spawn-or-attach** : si le nom existe déjà → attache au lieu de recréer.
7. **Policy + settle-loop** (cf. §7).
8. **SSH** selon `ssh.mode` : `agent-forward` (défaut) / `mount ~/.ssh_sbx` / `none`.
9. **Attache.** `sbx exec -it <name> -w <workdir> bash -l` → shell, sauf `--detach`. Pas
   `sbx run` : celui-ci lance la commande du flavor de l'image (souvent `claude`), n'a aucun flag
   pour la remplacer, et son `-- ARGS` ne fait qu'*ajouter* des arguments. **Les ports ne sont PAS
   publiés au spawn** → `den ports <nest>` à la demande.

### Build DAG — `den build [stack] [--force]`
- Parse tous les `stacks/*/stack.yaml` → graphe via `parent`.
- `den build dgdevx` → construit `devx` d'abord **si son image manque**, puis `dgdevx`.
  `den build` (sans arg) → tout, ordre topologique.
- Chaque nœud lance son `stacks/<n>/build.sh` (inchangé). `--force` reconstruit aussi les ancêtres.
  `versions.lock` tenu à jour par les `build.sh`.

---

## 7. Policy réseau & settle-loop (douleur #1)

- Egress effectif (§4) posé en **`caps.network.allow` du mixin généré** → **auto-scopé** à la sandbox
  au `create` (aucune règle globale qui fuite d'un projet à l'autre) et **présent dès le
  create-time** (pas de pose paresseuse — la propagation sbx n'est pas instantanée).
- **Settle-loop fail-closed** : après create, `den` boucle sur
  `sbx policy check network --sandbox <name> <host>` pour chaque hôte jusqu'à ALLOW, **timeout
  borné**. Si un hôte ne passe pas → `den` **n'attache pas**, liste les hôtes bloqués, sort en
  erreur. Jamais de « ça marche à moitié ».

**Schéma de kit (relevé sur les kits réels, pas déduit) :** `schemaVersion: 2`, `kind: mixin`,
`name`, `version`, `description` ; les capacités réseau vivent sous **`caps.network.allow`** (liste
de `host`, `host:port`, `ip` ou `ip:port`), les variables sous **`environment.variables`**, les
commandes de boot sous **`commands.startup[].command`** (tableau argv). `sbx policy check network`
évalue un hôte nu **sur le port 443** : une entrée egress nue est donc cohérente de bout en bout,
den ne normalise rien.

---

## 8. Modèle de ports

Objectif : **URLs stables bookmarkables** en usage courant **et** anti-collision quand plusieurs
sandboxes tournent.

- **Fenêtre déterministe par nest** : `base = 9000 + hash(nest.name) % 900 * 10` → **10 ports par
  nest**, stable pour ce nom. Surchargeable via `ports.base`.
- **Offset par ordre de déclaration** : port déclaré *i* → `host = base + i`.
- **Publication à la demande** : `den ports <nest>` calcule la fenêtre, **scanne** `127.0.0.1:base..base+9` ;
  si libre → publie via `sbx ports --publish 127.0.0.1:H:C` (cas courant, URL canonique) ;
  si occupée (2e instance) → **décale la fenêtre entière** au bloc de 10 suivant + **avertit**
  (non-canonique, cette instance seulement). La 1re instance garde toujours l'URL canonique.
- **Éphémères** (docker compose, ports non déclarés) : OS-assigned, affichés mais jamais « stables ».
- **Sécurité — non négociable** : toujours `127.0.0.1`, **jamais `0.0.0.0`**. Un port `loopback_lock`
  (CDP/Playwright, non authentifié) est **refusé** hors loopback même si forcé. « Accès depuis
  l'extérieur » → **pas** de bind LAN ; `den` imprime un **tunnel SSH prêt-à-coller**
  (`ssh -L H:127.0.0.1:H you@hôte`), l'auth déléguée à SSH.

**Affichage type :**
```
nest: web   sandbox: web.feat123   window: 9100-9109 (canonical)
  NAME  CONTAINER  URL
  vite  5173       http://127.0.0.1:9100   [opened]
  api   3000       http://127.0.0.1:9101
  cdp   9223       ws://127.0.0.1:9102     [loopback-locked]
  remote?  ssh -L 9100:127.0.0.1:9100 you@$(hostname)
```

---

## 9. Profils agents (générique)

- Un **agent** = un `config_dir` (monté RW, jetable, **persiste** creds/plugins/historique sur
  l'hôte, isolé du vrai `~/.claude`) + des **env vars** qui pointent l'outil vers le chemin in-VM.
- **Pas de snapshot/vendoring** en v1 : le dossier monté RW persiste déjà tout. Coût : `/login` +
  install plugins **la première fois** sur un profil neuf.
- **Override par nest et par agent** : `nests/<n>.yaml → agents: { claude: <path>, … }`. Défaut =
  profil partagé du registre global (un seul login) ; un nest peut demander l'isolation via un
  chemin dédié.
- Monter le `config_dir` + poser l'env est **sans effet** quel que soit le template → appliqué
  **uniformément**, la stack ne le connaît pas.

### 9.1 Fraîcheur de l'agent (obligatoire)

**Exigence : une sandbox ne démarre jamais avec un agent périmé.** L'agent est mis à jour **au boot**
(pas au build de l'image : une image pinne une version qui rancit), via la commande `update` du
registre, injectée par `den` en `commands.startup` du mixin généré.

Deux contraintes du dispatcher sbx (`/etc/durable-startup.d/run.sh`, **vérifiées empiriquement le
2026-07-27**, cf. `/var/log/sbx-kit-startup.log`) — non négociables, elles ont déjà produit un bug :

1. **PATH.** Chaque commande est enveloppée dans `su -s /bin/sh -c … agent`, un `su` **non-login** :
   ni `.profile` ni `.bashrc` ne sont sourcés. Le PATH vaut
   `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`, **sans `~/.local/bin`** où vit le
   binaire de l'agent. Sans `export PATH` explicite → `exit=127`, agent jamais mis à jour, échec
   silencieux à l'usage. → `den` **doit** préfixer la commande par un `export PATH` construit depuis
   `bin_dirs`. (Le kit natif sbx `001-startup-claude/002-cmd.sh` applique déjà cette parade.)
2. **Un échec avorte tout.** Le dispatcher fait `exit $rc` au **premier** échec : les startup
   commands de **tous les kits suivants** sont sautées. → la commande de fraîcheur, **fail-closed**,
   doit être layerée **en dernier** (`--kit` final, cf. §6.6), pour qu'un abort ne prive aucun autre
   kit de son initialisation.

**Sémantique retenue : fail-closed avec retries bornés.** 3 tentatives espacées (10 s) pour absorber
la propagation non instantanée de la policy egress (§7) — sans elles un hoquet réseau avorterait le
boot ; après quoi sortie non-zéro. Ni `|| true` ni silence : c'est précisément le `fail … exit=127`
du journal qui a rendu le bug de 2026-07-27 diagnosticable.

**Testabilité (exigence de build) :** rendu de la commande de fraîcheur = **golden file** (unitaire) ;
comportement (nominal / binaire absent / échec permanent / échec transitoire puis succès) = **script
testable hors VM**, exécuté sous `su` non-login pour reproduire le contexte réel. Un `den doctor`
signale un agent périmé.

---

## 10. Modèle SSH

- **`agent-forward` (défaut)** : forwarde le *socket* de l'agent SSH → aucune clé n'entre dans la
  VM (pas d'exfil du matériel). Forwarde toutes les identités chargées (scoping via un agent
  dédié à la clé `~/.ssh_sbx` si besoin).
- **`mount ~/.ssh_sbx`** (override courant à l'usage) : monte la **clé dédiée** dans la VM. Un
  agent compromis peut la lire → mais clé **dédiée, scopée, révocable** → blast-radius borné et
  connu. Simple, headless-ready. Expose **exactement** cette clé, rien d'autre.
- **`none`** : réservé au futur flux autonome.

---

## 11. État, labels & gestion d'erreurs

**État (approche A) :** `sbx create` **n'a pas de flag `--label`** (vérifié le 2026-07-28,
sbx v0.35.0 : ses seuls flags sont `--clone --cpus --kit --memory --name --profile --quiet
--template`). L'identité d'une sandbox est donc portée par son **nom** : `<nest>` sans worktree,
`<nest>.<worktree>` avec. `den ls` liste `sbx ls --json` et attribue chaque sandbox par
décomposition de son nom. Le séparateur est `.` et non `-` : il est interdit dans les noms de nest
et de worktree, ce qui rend la décomposition **exacte** au lieu de dépendre d'un plus-long-préfixe
contre la liste des nests déclarés — une sandbox reste attribuable même après suppression de son
nest. Cache `~/.den/cache/` reconstructible, jamais source de vérité.

| Situation | Comportement |
|---|---|
| Image stack absente | Stop → « lance `den build <stack>` » |
| Chemin repo introuvable | Stop **avant** tout create |
| Worktree `<wt>` déjà pris par une autre branche | Stop → propose `--attach-worktree` ou autre nom |
| Policy non settled dans le timeout | **Fail-closed**, n'attache pas, liste les hôtes bloqués |
| `sbx` absent / pas loggé | Message doctor-style (`den doctor`) |
| Mise à jour de l'agent impossible au boot | **Fail-closed** après 3 tentatives (§9.1) : le kit sort non-zéro. Layeré en dernier → aucun autre kit lésé. Diagnostic dans `/var/log/sbx-kit-startup.log` |
| Nom de sandbox déjà vivant | **Spawn-or-attach** (pas une erreur) : attache |

---

## 12. Architecture technique & tests

**Stack :** Go · CLI **cobra** · YAML **`yaml.v3`** · binaire statique unique (`go build`).
`sbx` piloté par **exec derrière une interface `sbx.Runner`** (mockable).

**Décodage YAML strict** (`KnownFields(true)`) sur *tous* les fichiers de config : une clé inconnue
est une erreur de chargement, jamais un silence. Une faute de frappe (`egres:` pour `egress:`) qui
passe inaperçue laisse l'allowlist vide et produit une sandbox qui n'atteint plus
`api.anthropic.com`, settle-loop fail-closed sans cause visible — le pire mode de défaillance de ce
projet, et précisément ce que `den doctor` doit attraper.

**Layout du dépôt (la CLI) :**
```
cmd/den/                 # entrée cobra
internal/
  config/                # load + merge cascade (global←stack←nest←flags)
  nest/                  # résolution nest, sélection repos (optional/without/only)
  sbx/                   # adapter exec (interface Runner, mockable)
  worktree/              # git worktree add/list/prune (central|per-repo)
  policy/                # union egress + settle-loop fail-closed
  ports/                 # fenêtre déterministe + scan anti-collision
  agent/                 # résolution agent + mixin généré (env + egress)
```

**Tests (TDD) :**
- **Unitaires sur la logique pure** (cœur de la valeur, zéro sbx) : cascade de config,
  union/dédup egress, calcul fenêtre de ports + anti-collision, sélection repos, rendu du mixin
  YAML, **assemblage de l'argv `sbx create`** (golden files : on assert la commande exacte).
- **`worktree/`** contre des repos git temporaires réels.
- **Smoke e2e** manuel/optionnel : un vrai spawn (nécessite sbx + login) — **hors CI**, gated.

---

## 13. Décisions verrouillées (récap)

1. CLI générique `den`, `~/.den/` source unique ; sbx-devbox = exemple.
2. Périmètre v1 = runtime + build (DAG) ; interactif d'abord ; autonome plus tard.
3. Objets : **stack** (image) · **kit** (overlay sbx) · **nest** (spawnable) · **agent profile**.
4. Multi-projet natif via nest.repos + `-w` (worktree propagé sur tous les repos) ; repos
   optionnels décochables.
5. Worktrees configurables, **défaut central** (`~/.den/worktrees/<wt>/<repo>`).
6. Agents **génériques** (registre), pas de snapshot, `config_dir` RW override par nest & par agent.
7. SSH **défaut `agent-forward`**, `mount ~/.ssh_sbx` override courant.
8. Ports : **fenêtre déterministe + publication à la demande** (`den ports`), loopback-only strict,
   CDP loopback-locked, tunnel SSH pour l'accès distant.
9. Policy **déclarative** (baseline ∪ stack ∪ nest) matérialisée en `caps.network.allow` scopé +
   **settle-loop fail-closed**.
10. État sans DB : identité portée par le **nom de sandbox** `<nest>[.<worktree>]` (`--label`
    n'existe pas dans sbx) ; cache reconstructible optionnel.
11. **Fraîcheur de l'agent au boot**, déclarée dans le registre (`update` + `bin_dirs`), rendue en
    dernière startup command du mixin généré, **fail-closed avec retries bornés** (§9.1).
12. **Identité par le chemin, jamais par le contenu** (§2) ; **décodage YAML strict** sur toute la
    configuration (§12).

---

## 14. Questions ouvertes / risques

- **Découverte des branches par défaut** par repo (worktree `-w`) : `git symbolic-ref` vs config.
- **Surface `sbx` figée le 2026-07-28** (v0.35.0) : `policy check network [--sandbox S] [--json]
  TARGET` confirmé (`--sandbox` existe, l'évaluation scopée est donc possible) ; `--label`
  **n'existe pas** → identité par le nom. À revalider si sbx passe en v0.37+.
- **Nettoyage des worktrees au `rm`** : par défaut on retire les worktrees créés par `den` (git
  worktree remove) ; `--keep-worktrees` pour conserver. Attention aux modifs non commitées → refuser
  si dirty sans `--force`.
- **Emplacement final** du dépôt CLI (nouveau repo) et migration de l'exemple `sbx-devbox` vers
  `~/.den/stacks/`.
- **Commande `update` de codex** (§4.1) : placeholder non vérifié, à confirmer le jour où l'agent
  codex est réellement branché. Celle de claude (`claude update`) est validée en VM.
- **Factorisation de repos partagés entre nests** : non traitée en v1 — chaque nest liste ses repos
  en entier, quitte à dupliquer. Risque assumé. Si la duplication devient douloureuse, ajouter un
  `extends: <nest>` explicite, pas des anchors YAML (qui rendraient les fichiers illisibles et
  contourneraient le décodage strict du §12).
