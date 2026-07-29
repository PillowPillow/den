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
    env: { CLAUDE_CONFIG_DIR: "{config_dir}" }   # {config_dir} résolu au chemin HÔTE — cf. A11 §14.1
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
  SOME_VAR: value                   # {config_dir} y est substitué comme dans l'env de l'agent (§4.1)
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
   créée **sans suivi** (`--no-track`) depuis la branche par défaut du repo, découverte par
   `git symbolic-ref --short refs/remotes/origin/HEAD` ; **repli sur le HEAD courant** quand le dépôt
   n'a pas d'`origin/HEAD` (un dépôt purement local est légitime). Checkout si la branche existe
   déjà — son point de départ n'est alors pas retouché. **Idempotent** (skip si déjà présent, à
   condition que le dossier soit bien la **racine d'un worktree de ce repo**). Conflit (branche
   différente sur ce worktree) → stop actionnable.
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
9. **Attache.** `sbx exec -it -w <workdir> <name> bash -l` → shell, sauf `--detach`. Les flags
   restent **avant** le nom de sandbox : la signature est `sbx exec [flags] SANDBOX COMMAND
   [ARG...]`, donc un `-w` postposé serait lu comme un argument de la COMMAND et arriverait tel
   quel à `bash -l`. Pas
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

**Ce que den fait, exactement, pour chacun des trois modes** (vérifié en tâche 18) :

| mode | flags de `sbx create` | mixin | workspaces (positionnels de l'argv) |
|---|---|---|---|
| `agent-forward` (défaut) | inchangés | inchangée | inchangés |
| `mount` | inchangés | inchangée | **+ `ssh.dir`**, en dernier |
| `none` | inchangés | inchangée | inchangés |

`agent-forward` n'ajoute donc **rien** : il repose entièrement sur le fait que le process
`sbx create` **hérite de l'environnement de den**, `SSH_AUTH_SOCK` compris — `internal/sbx` ne
renseigne jamais `cmd.Env`, et cet héritage est tenu par un test qui rougit si quelqu'un le
renseigne. C'est délibéré des deux côtés où l'on serait tenté de faire autrement :

- **pas dans l'argv** : aucun flag `sbx` attesté ne transmet un socket d'agent (les seuls flags de
  `sbx create` relevés le 2026-07-28 sont `--clone --cpus --kit --memory --name --profile --quiet
  --template`) ;
- **pas dans le mixin** : une valeur de socket hôte écrite dans un kit serait périmée dès la
  session suivante, et trompeuse — le kit décrit la VM, pas la session qui l'a lancée.

Le seul cas où ce mode ne donne rien est un hôte **sans agent SSH en marche**. `den doctor` le
signale alors en **avertissement** (`[warn] ssh.mode`), pas en échec : travailler en local sans
dépôt distant est légitime, et den n'a aucun moyen de savoir si l'utilisateur a besoin de SSH.

Reste **non vérifiable sans `sbx` installé** : que `sbx` propage effectivement ce socket **dans**
la microVM. C'est l'hypothèse **A10** du §14.1, falsifiable au premier smoke réel par un
`git push` depuis la VM.

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
  spawn/                 # orchestration de `den <nest>` (§6), hors cli pour rester testable
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

### 14.0 Surface `sbx` ATTESTÉE (relevé du 2026-07-28, v0.35.0)

Sondée sur la machine de l'utilisateur. **C'est le seul relevé de référence du dépôt** : ce qui
n'y figure pas n'est pas attesté, et ne doit être ni suggéré à l'utilisateur dans un message, ni
tenu pour acquis dans le code. Étendre cette liste demande un **relevé**, pas une intuition.
(Recopié ici depuis le plan 2, qui n'est pas un fichier suivi — la trace, elle, doit l'être.)

```
sbx create [flags] AGENT PATH [PATH...]
  flags : --clone --cpus int --kit strings (répétable) -m/--memory --name --profile -q -t/--template
  AGENT ∈ {claude, codex, copilot, cursor, docker-agent, droid, gemini, kiro, opencode, shell}
  PATH accepte le suffixe `:ro`
  --name : « letters, numbers, hyphens, periods, plus signs and minus signs only »
  ⚠️ AUCUN --label. La décision verrouillée n°10 (état par labels) est FALSIFIÉE → identité par le nom.

sbx ls [--json] [-q]
  {"sandboxes":[{"name","id","agent","status","workspaces":["/p","/p:ro"]}]}
  ⚠️ aucun champ de date/création → la colonne « âge » du §5 est INFAISABLE.

sbx exec [flags] SANDBOX COMMAND [ARG...]
  flags utiles : -i/--interactive -t/--tty -d/--detach -w/--workdir -u/--user

sbx ports SANDBOX [--publish spec] [--unpublish spec] [--json]
  spec : [[HOST_IP:]HOST_PORT:]SANDBOX_PORT[/PROTOCOL]

sbx policy check network [--sandbox SANDBOX] [--json] [--verbose] TARGET
  « Bare hosts and IP literals are evaluated with port 443. »
  → une entrée egress nue est cohérente entre le mixin et le check, sans normalisation.

sbx rm --force NAME
```

`sbx-devbox` ajoute `stop`, `template save`, `secret`, `inspect`, `login`. **`sbx start`
n'apparaît dans aucun relevé** — c'est la raison pour laquelle la remédiation de
`internal/sbx/ls.go` ne le propose pas (tenue par
`TestVerifieEnMarcheNeSuggereQueDesCommandesAttestees`).

**Schéma de kit réel** (relevé sur `sbx-devbox/lib/*/spec.yaml`, pas sur la documentation) :

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

Les clés sont `caps.network.allow` et `environment.variables` — **pas** `network.allow` ni `env`,
que ce spec écrivait avant la tâche 1.

**Deux pièges du dispatcher de kits** (`/etc/durable-startup.d/run.sh`, vérifiés empiriquement ;
journal dans `/var/log/sbx-kit-startup.log`) :

1. Chaque commande passe par `su -s /bin/sh -c … agent`, un `su` **non-login** : le PATH n'inclut
   pas `~/.local/bin`, et tout binaire user-local sort en `127`. D'où l'`export PATH` explicite
   généré dans le mixin (§9.1, `agent.CommandeFraicheur`).
2. Le dispatcher fait `exit $rc` au **premier** échec : une commande non-zéro **prive tous les kits
   suivants** de leurs startup commands. Ce qui est fail-closed se layere donc **en dernier** —
   c'est **toute** la justification de `sbx.Create.KitMixin` comme champ séparé des `KitsStack`, et
   de l'invariant « le mixin est le dernier `--kit` », verrouillé par 3 tests. Ce que ce relevé
   n'atteste PAS : que `sbx` applique les `--kit` dans l'ordre de la ligne de commande (voir
   « Hypothèses assumées », §14.1).

### Questions ouvertes et risques restants

- **Surface `sbx` figée le 2026-07-28** (v0.35.0), ci-dessus : `policy check network [--sandbox S]
  [--json] TARGET` confirmé (`--sandbox` existe, l'évaluation scopée est donc possible) ; `--label`
  **n'existe pas** → identité par le nom. À revalider si sbx passe en v0.37+.
- **Nettoyage des worktrees au `rm`** : par défaut `den` retire les worktrees qu'il a créés ;
  `--keep-worktrees` pour conserver.

  **`den` ne supprime jamais un worktree : il le DÉPLACE** vers
  `<den_home>/trash/<horodatage>-<nest>-<repo>`, puis élague l'enregistrement (`git worktree prune`).
  Repli sous `<worktree_root>/.trash` — ou `<repo>/.den/.trash` en layout per-repo — quand
  `os.Rename` échoue en `EXDEV`, `den_home` et `worktree_root` étant deux réglages indépendants qui
  peuvent vivre sur deux systèmes de fichiers. Rétention de 30 jours, purgée à chaque mise à la
  corbeille réussie.

  Ce n'est pas une précaution d'implémentation, c'est la conclusion de cinq tours de relecture de
  `internal/worktree` : l'énumération des façons dont `git status` cache du travail **ne converge
  pas** (git ajoute un mécanisme de cache par version — untracked cache en 2.8, fsmonitor par hook en
  2.16, `core.fsmonitor=true` en 2.37), et le filet de `git worktree remove` tombe **avec** celui de
  `status`, parce que c'est le même code. Il n'y a pas de second filet. La corbeille ne ferme donc
  pas un membre de plus : elle fait passer tous les membres futurs, et l'angle mort assumé ci-dessous,
  de « perte de données » à « un dossier que l'utilisateur remonte d'un `mv` ». Ce qu'elle ne rend
  pas : le dossier déplacé n'est plus un worktree opérationnel, son enregistrement ayant été élagué —
  on récupère des fichiers. Les commits, eux, n'ont jamais été en jeu : la branche survit.

- **« dirty » ne veut PAS dire « `git status` n'est pas vide »**. Le sens a été payé sur cinq tours de
  correction et une lecture naïve le reperdrait. `den` refuse sans `--force` si le worktree porte
  l'un de ces quatre états :
  1. des modifications de fichiers **suivis** non commitées, renommages compris ;
  2. des fichiers **non suivis** — demandés explicitement, `status.showUntrackedFiles = no` étant un
     réglage de performance répandu qui les cacherait sinon ;
  3. des fichiers **ignorés isolés** (`.env`, base sqlite locale) : exactement ce qu'on ne commite pas
     ET qu'on ne retrouve pas. Les **dossiers réellement ignorés** (`node_modules/`, `target/`) sont
     au contraire écartés, sans quoi `den rm` serait inutilisable sur tout projet JS ou Python ;
  4. des fichiers **marqués localement** `skip-worktree` / `assume-unchanged` dont le contenu diffère
     réellement de l'index — git ne rapporte rien sur eux, ni dans `status`, ni dans le filet de
     `remove`.

  `core.fsmonitor` est neutralisé à l'appel (`-c core.fsmonitor=`) : un démon menteur ou périmé
  répond « rien n'a changé », et git le croit.

  **Angle mort assumé** : un secret placé dans un dossier réellement ignoré reste invisible au
  verdict. C'est le prix explicite de l'utilisabilité sur `node_modules/`, et l'un des cas que la
  corbeille rend réversible.
- **Emplacement final** du dépôt CLI (nouveau repo) et migration de l'exemple `sbx-devbox` vers
  `~/.den/stacks/`.
- **Commande `update` de codex** (§4.1) : placeholder non vérifié, à confirmer le jour où l'agent
  codex est réellement branché. Celle de claude (`claude update`) est validée en VM.
- **Factorisation de repos partagés entre nests** : non traitée en v1 — chaque nest liste ses repos
  en entier, quitte à dupliquer. Risque assumé. Si la duplication devient douloureuse, ajouter un
  `extends: <nest>` explicite, pas des anchors YAML (qui rendraient les fichiers illisibles et
  contourneraient le décodage strict du §12).

---

## 14.1 Hypothèses non vérifiées contre un `sbx` réel (inventaire A1→A11)

**Versé le 2026-07-28** (tâche 17b), depuis l'inventaire dressé en tâche 11 ; **A10 versée le
2026-07-29** (tâche 18), **A11 le 2026-07-29** (revue finale). Ces affirmations sont
**invérifiables contre un `sbx` réel** : il n'est pas installé sur la machine de développement, et
aucune ne peut être tranchée sans lui. Elles ne sont **pas** des bugs connus — ce sont les endroits
où la suite ne prouve rien.

**A1→A9 d'une part, A10 et A11 de l'autre, ne sont pas de la même espèce.** A1→A9 portent sur le
**contrat CLI** de `sbx` — argv, forme de la sortie, codes de retour — que le double de test
(`sbx.Fake`) exerce réellement : elles sont **vertes contre le double**. A10 et A11 portent sur des
comportements d'**exécution de la microVM** qu'aucun double ne touche, et pour lesquels « vert
contre `sbx.Fake` » ne veut rien dire. La moitié hôte d'A10 (l'héritage de l'environnement par le
process `sbx`) est, elle, tenue par des tests ; c'est le saut hôte → invité qui n'est tenu par rien.
A11 n'a même pas cette moitié-là : elle est entièrement in-VM.

**Ce que la tâche 11 a fait de la plupart d'entre elles :** neutraliser leur conséquence plutôt
qu'attendre le smoke. Quand la colonne « den y survit » dit oui, la fausseté de l'hypothèse ne
casse plus den ; il reste utile de la vérifier, mais ce n'est plus bloquant.

| # | Hypothèse sur `sbx` | Ce qui la falsifierait au premier smoke réel | den y survit ? |
|---|---|---|---|
| A1 | `sbx` sort en **échec** quand un hôte est simplement refusé par la policy | Un hôte refusé fait sortir `sbx` en 0 : le settle-loop conclurait à une erreur au 1er tour en accusant un hôte innocent | **Oui** — `hoteAutorise` décode la sortie AVANT de conclure à l'erreur : un `allowed` exploitable est un verdict, on boucle |
| A2 | Le verdict part sur **stdout** | Verdict sur stderr ⇒ stdout vide ⇒ den échoue en disant « sortie vide », sans attacher | **Oui**, fail-closed et message dédié — mais den n'attachera jamais tant que ce n'est pas corrigé |
| A3 | stdout porte le verdict dans sa **première valeur JSON** | Une bannière AVANT le JSON : den refuse (délibéré — on ne cherche pas un verdict au milieu d'un flux incompris). Du bruit APRÈS est toléré | **Oui**, et c'est un choix explicite, pas un effet de bord |
| A4 | **L'argv lui-même** : `policy check network --sandbox S --json HÔTE` — noms des flags, leur ordre, l'existence de `--json`, le nom du champ `allowed`, le code de sortie sur hôte refusé | **Tout faux, tout vert.** Le double répond à l'argv qu'on lui donne : aucun test ne peut détecter que la commande n'existe pas sous cette forme | **NON — le trou le plus lourd de l'inventaire.** Seul un `sbx` réel, ou un `den doctor` qui sonde la commande, peut trancher |
| A5 | Le `Run` d'un runner **honore le `ctx`** | Le double l'ignorait : une annulation en cours de passe accusait un hôte au lieu de dire « interrompu » | **Oui** — `Settle` reconnaît l'annulation lui-même, et depuis 17b `sbx.Exec.Run` joint le motif du contexte à sa chaîne d'erreurs |
| A6 | `sbx` répond **vite** à `policy check` | 20 hôtes lents : le temps réel dépasse les 60 s annoncés, le timeout n'étant vérifié qu'entre deux tours et non pendant une passe. Le `ctx` de l'appelant reste la vraie borne | **Partiellement** — la borne existe, sa granularité est le tour |
| A7 | La réponse ne dépend que de `(sandbox, hôte)`, jamais transitoire | Un `sbx` qui flanche une fois (VM pas encore prête) ferait échouer tout le settle | **Oui**, même correctif qu'A1 |
| A8 | La sandbox existe et tourne quand `policy check` est appelé | Idem A1 | **Oui** pour la garde de nom de sandbox |
| A9 | `sbx` ne rend **jamais** `allowed:false` en échouant pour une raison **étrangère à la policy** | Une sandbox inexistante diagnostiquée `not found` **tout en** rendant `allowed:false` : den brûlerait ses 60 s en accusant l'allowlist | **Oui** — la dernière erreur runner observée est jointe au message de timeout, la vraie cause reste visible |
| A10 | `sbx` **propage `SSH_AUTH_SOCK` de son propre environnement jusque dans la microVM** — c'est tout ce sur quoi repose `ssh.mode: agent-forward`, qui est le **défaut** | Un `git push` sur un remote SSH depuis la VM échoue en `Permission denied (publickey)` alors que `den doctor` affiche `[ok] ssh.mode agent-forward, SSH_AUTH_SOCK=…` sur l'hôte | **Partiellement.** Ce que den contrôle est prouvé : le process `sbx` hérite bien de l'environnement de den (`cmd.Env` laissé nil, tenu par `TestExec{Run,Attach}TransmetLEnvironnementDeDen`), et `den doctor` **avertit** quand la variable manque côté hôte. Ce que den ne peut pas contrôler — le saut hôte → microVM — n'a **aucun** substitut : si l'hypothèse est fausse, le repli est `ssh.mode: mount`, qui lui est testé |
| A11 | `sbx` **monte chaque workspace au MÊME chemin absolu dans la VM que sur l'hôte** : le chemin hôte d'un workspace est aussi son chemin in-VM. C'est ce qui rend légitimes (1) la substitution de `{config_dir}` par un chemin **hôte** dans `CLAUDE_CONFIG_DIR` (`internal/nest/resolve.go`, `jetonConfigDir`) et (2) le `-w <chemin hôte>` de **toutes** les attaches (`sbx exec -it -w …`, `den <nest>` comme `den sh`) | **Deux symptômes sans lien apparent, et aucun message d'erreur.** (1) L'agent redemande `/login` **à chaque spawn** : `CLAUDE_CONFIG_DIR` pointe un chemin qui n'existe pas dans la VM, l'agent y crée un profil neuf — c'est précisément ce que `config_dir` existe pour éviter, et rien ne le signale. (2) `sbx exec -w <chemin hôte>` échoue, ou dépose le shell ailleurs que dans le code. Le smoke tranche en une ligne : `sbx exec <sandbox> pwd` après un `den <nest>`, comparé au premier workspace de `sbx ls --json` | **NON.** Aucun repli, aucune neutralisation : den n'a **aucun** moyen d'apprendre le chemin in-VM sans `sbx`. Même espèce qu'A10 — comportement d'**exécution de la microVM**, pour lequel « vert contre `sbx.Fake` » ne veut rien dire. Si elle est fausse, il faudra une source de vérité pour le chemin in-VM (sonde `sbx exec … pwd`, ou un champ de `sbx ls --json`) |

### Hypothèses assumées de den lui-même

Celles-ci ne portent pas sur `sbx` mais sur des choix de den, tous **délibérés**. Elles sont
écrites ici pour qu'aucune ne passe pour un oubli.

- **La liste blanche de statut `{"running"}`** (tâche 14, `internal/sbx/ls.go`) : den ne considère
  vivante qu'une sandbox dont le statut est exactement `running`. Un statut **transitoire**
  (`starting`, `booting`, `resuming`…) serait donc traité comme « absente », et `den <nest>`
  tenterait de recréer une sandbox en train de démarrer. **Comportement non changé** : la liste
  blanche est le choix sûr tant que l'ensemble réel des statuts de `sbx` n'est pas connu.
  *Falsifié par :* un `sbx ls --json` réel montrant un statut intermédiaire.
- **L'ordre des `--kit`** n'est vérifié que par nos propres tests (le mixin en dernier). Que sbx
  applique bien les kits dans l'ordre de la ligne de commande n'est vérifié nulle part.
  *Falsifié par :* la lecture de `/var/log/sbx-kit-startup.log` au premier smoke.
- **`sbx` laisserait derrière lui un descendant qui hérite des tubes** (superviseur de microVM
  survivant à `sbx create`). C'est l'hypothèse qui **justifie** la borne de drainage de
  `sbx.Exec.Run` (`delaiDrainageDefaut`, 2 s). Elle n'a **jamais été observée sur `sbx`** : tout ce
  qui est mesuré l'a été sur `sh` (dash forke pour `sleep 5`, et le petit-fils tient le tube —
  5,007 s au lieu de 21 ms). Si l'hypothèse est fausse, la borne ne se déclenche simplement jamais.
  Elle ne coûte rien dans les deux cas depuis le correctif : un drainage écourté sur un process
  **sorti avec succès** est rendu comme un succès, et non plus comme un échec — c'est la régression
  qu'a coûtée la première version, mesurée de bout en bout (`sbx create` réussi + superviseur :
  30,0 s et succès avant la borne, **échec** avec la borne seule, 2,0 s et succès avec le
  correctif).
  *Falsifié par :* au premier smoke réel, un `pgrep -P` sur le `sbx create` — ou, plus simplement,
  un `den <nest>` qui ne rend la main qu'après exactement 2 s alors que `sbx` a répondu tout de
  suite (signe que la borne a bien servi).
- **`ssh.mode: agent-forward` n'ajoute AUCUN argument** — vérifié dans `internal/spawn/spawn.go` :
  seul le mode `mount` produit un effet (un workspace en plus, et le contrôle d'existence de
  `ssh.dir`). `agent-forward`, qui est pourtant le **défaut**, et `none` sont aujourd'hui
  indiscernables pour den. Le forwarding est donc entièrement à la charge de `sbx`. **C'est la
  tâche 18**, pas une régression.
  *Falsifié par :* un agent SSH indisponible dans la VM alors que `ssh.mode` vaut `agent-forward`.
- **L'image de stack n'est pas contrôlée avant `sbx create`.** Le §11 prévoit « lance
  `den build <stack>` » quand l'image manque, mais `den build` est du **Plan 4** et n'existe pas.
  den passe l'image verbatim à `--template` et se contente de relayer le refus de `sbx`. Forme
  exacte du message, relevée sur le binaire — **avec un `sbx` FACTICE**, `sbx` n'étant pas installé :
  le texte après le dernier « : » est celui du double, pas celui de `sbx`, et seule l'enveloppe
  autour est de den :

  ```
  den: création de la sandbox api : sbx create --name api --template devx:v1 --kit
  <den_home>/cache/mixins/api shell <repo> <den_home>/agents/claude : template "devx:v1" not found
  ```

  Deux écarts, donc, et pas un seul : le message ne dit pas **quoi faire** (le §11 promet le conseil
  `den build`), et il **incruste l'argv complet** de `sbx create` avant d'en venir à la cause. Aucun
  contrôle n'est ajouté ici : il exigerait d'interroger sbx sur ses templates, et retomberait sous
  **A4**.
  *Falsifié par :* un premier spawn sur une stack jamais construite, avec un `sbx` réel — qui dira
  autre chose que « template … not found », le libellé ci-dessus étant inventé par le double.
