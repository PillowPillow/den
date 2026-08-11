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
  - *build* : orchestration DAG des images (`den build`). den possède la séquence de bout en bout,
    du `sbx create` au `sbx template save` ; une stack ne fournit que ses scripts de
    provisionnement (§6).
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
`den spawn <nest> -w <wt>` → nom de sandbox → `den ls`.

---

## 3. Layout de `~/.den/`

```
~/.den/
  config.yaml              # défauts globaux (agents, ssh, egress baseline, worktree, defaults)
  stacks/
    devx/
      stack.yaml           # image + base|parent (DAG) + provision + kit + egress stack
      provision/           # scripts joués DANS la VM de build, un `sbx exec` par entrée (§6)
        go-tools.sh · gh.sh · playwright.sh
      kit/spec.yaml        # overlay env/policy de la stack
    dgdevx/
      stack.yaml           # parent: devx
      provision/ · kit/spec.yaml
  lib/                     # fichiers `includes:` partagés entre stacks — définitions shell
    common.sh              #   atteint depuis une stack par `../../lib/common.sh`
  nests/
    review.yaml
    fullstack.yaml
  kits/                    # kits transverses non-egress, layerés avant `kit` (cf. §4.2)
    ssh-known-hosts/
  sources/<n>/             # clones git de sources d'équipe (même layout partiel) — spec 2026-08-04
  worktrees/               # worktrees générés (layout central par défaut)
    <wt>/<repo>/
  cache/                   # optionnel, reconstructible — jamais source de vérité
    build/<stack>/         #   dossier vide monté dans la VM de build (§6)
  state/                   # trace des créations — JAMAIS purgé automatiquement
    sandboxes/<sandbox>.yaml #   ce que den a réellement monté (spec 2026-08-05)
```

`lib/` n'a **aucune sémantique pour den** : c'est l'emplacement conventionnel des fichiers qu'une
stack cite dans `includes:`, rien de plus. den ne lit que `stacks/*/stack.yaml` et les chemins que
ceux-ci déclarent ; un `lib/` absent n'est un défaut que pour la stack qui le référence.

`state/` **n'est pas un cache** — un repo monté depuis la ligne de commande n'est reconstructible
depuis rien, et le fichier est la seule trace d'un worktree pouvant porter du travail non commité.

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
    config_dir: ~/.den/agents/claude       # optionnel — défaut : <den home>/agents/<nom de l'agent>,
                                           #   recalculé au CHARGEMENT contre le den home courant
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
worktree_root: ~/.den/worktrees          # optionnel — défaut : <den home>/worktrees, même mécanique
                                         #   que config_dir ci-dessus
egress:                                  # allowlist baseline, TOUTES sandboxes
  - api.anthropic.com
  - github.com
  - registry.npmjs.org
```

### 4.2 `stacks/<n>/stack.yaml` (recette image)

```yaml
image: dgdevx:v1        # passé à `sbx create --template` au spawn ; produit par `den build`
parent: devx            # DAG de build (build devx avant dgdevx) — exclusif avec `base`
base: claude            # RACINES SEULEMENT : positionnel AGENT de `sbx create`, qui choisit
                        #   l'image de départ quand il n'y a pas de `--template`
provision:              # optionnel : ce que den joue DANS la VM de build (§6)
  includes:             #   optionnel : concaténés en tête de CHAQUE step
    - ../../lib/common.sh
  steps:                #   ordonné : un `sbx exec` par entrée
    - ./provision/glab.sh
    - ./provision/dgdev.sh
kit: ./kit              # kit par défaut de la stack (env + egress toolchain)
kits:                   # optionnel : kits transverses layerés AVANT `kit`
  - ../../kits/ssh-known-hosts
egress: []              # ajouts egress niveau stack
```

Les chemins de `kit`, `kits`, `provision.includes` et `provision.steps` sont résolus
**relativement au dossier de la stack**. L'ordre de `kits` est préservé : c'est un ordre de
layering, pas un ensemble. Celui de `includes` et de `steps` aussi, pour la raison donnée au §6.

`base` et `parent` sont **mutuellement exclusifs**, et une stack qui déclare `provision.steps` doit
en poser exactement un — sans quoi den ne sait pas de quoi partir. Les déclarer tous les deux est un
refus et non un ordre de priorité : deux origines pour une même image est une contradiction, pas une
préférence à arbitrer. Une stack **sans `provision.steps`** n'en pose aucun des deux : elle n'est pas
constructible, et c'est une configuration déclarée (§6).

`image:` est **obligatoire**, sur *toute* stack — constructible ou non. Un `image:` vide (ou blanc)
est un refus au **chargement** (`config.LoadStack`), jamais une valeur que den complète. Décidé le
2026-08-03 contre la tentation de ne l'exiger que des stacks constructibles, à la forme des deux
refus voisins ci-dessus : le vide voyage dans **trois** directions, et seule la première se voit.

1. **Constructible.** La stack se construit entièrement, `sbx template save <S>-build ""` s'exécute,
   den annonce le succès. Le `den spawn` suivant répond « image&nbsp;&nbsp;n'est pas construite —
   lance `den build devx` » (l'espace doublé est la place de l'image), c'est-à-dire exactement ce que
   l'utilisateur vient de faire. C'est la **boucle fermée** du §6 : den possédant `template save` en
   corrige le NOM, rien n'en corrigeait la vacuité.
2. **En tant que `parent:`.** L'enfant reçoit un `ParentImage` vide, que `build.CreateArgv` lit comme
   « stack racine », d'où un repli sur le `base:` de l'enfant — qu'une stack déclarant `parent:` n'a
   pas. Le refus « no origin » tombe alors sur l'ENFANT, en nommant *son* `stack.yaml`, qui déclare
   déjà le `parent:` que le message réclame : mauvais fichier, remède déjà appliqué. Et il tombe dans
   le pré-vol de chaîne, donc **une** stack sans `image:` refuse le `den build` **entier**. C'est ce
   cas, et lui seul, qui impose le contrôle **inconditionnel** : il survit intact à un contrôle gardé
   par `Buildable()` dès que le parent sans image est lui-même tirable — vérifié contre
   `build.CreateArgv` le 2026-08-03 avant de trancher.
3. **Tirable.** `sbx.CreateArgv` refuse bien un `image:` vide, mais seulement au `create` du spawn,
   *après* les worktrees et le profil agent. Refuser au chargement, c'est l'ordonnancement tenu
   partout ailleurs (§6) — tout ce qui est rejetable depuis la config seule l'est avant le premier
   effet de bord — et cela rend à ce garde-là son rôle de **garde-frontière**, celui que sa propre
   doctrine lui assigne.

### 4.3 `nests/<n>.yaml` (objet spawnable)

```yaml
stack: dgdevx
env:                                 # optionnel, per-nest → injecté via le mixin généré
  SOME_VAR: value                   # {config_dir} y est substitué comme dans l'env de l'agent (§4.1)
egress:                              # optionnel, per-nest → permissions.network.allow scopé sandbox
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

**`repos:` est facultatif.** Un nest qui n'en déclare aucun reste un objet spawnable complet — il
porte sa stack, son egress, son env, ses ports et ses profils agents — et reçoit ses dépôts en
positionnels : `den spawn scratch ~/dev/a ~/dev/b`. Les positionnels sont additifs et passent
**devant** les `repos:` déclarés, parce que `Workspaces[0]` décide du répertoire où démarre le shell
attaché. Ils n'entrent pas dans l'identité : `den spawn scratch ~/dev/a` et `den spawn scratch
~/dev/b` visent la même sandbox `scratch`. Détail : `2026-08-04-adhoc-repos-design.md`.

**Règles de fusion** (cascade) : `global ← stack ← nest ← flags CLI`.
**Egress effectif** = `baseline ∪ stack.egress ∪ nest.egress` (dédupliqué), appliqué **scopé à la
sandbox**.

---

## 5. Surface de commandes (v1)

| Commande | Rôle |
|---|---|
| `den init` | crée un den home à partir de l'exemple embarqué (`config.yaml`, `nests/example.yaml`, `stacks/devx/stack.yaml`) ; refuse si `config.yaml` existe déjà |
| `den spawn <nest> [repo...] [-w <wt>] [--without r] [--only r] [-i] [--agent a] [--detach]` | **spawn-or-attach** + shell ; les `repo...` sont des repos montés à la volée, additifs aux `repos:` du nest et placés devant eux |
| `den ls` | sandboxes vivantes (`sbx ls --json` filtré sur le motif de nommage, colonnes nom/nest/worktree/statut/workspaces) |
| `den sh <name>` | shell dans une sandbox existante |
| `den ports <name> [--add H:C]` | **publie à la demande** la fenêtre déclarée + affiche le tableau |
| `den rm <name> [--keep-worktrees]` | teardown (profil agent persiste ; worktrees nettoyés sauf `--keep`) |
| `den build [<stack>] [--force]` | build image(s), ordre DAG |
| `den doctor [--fix] [--force]` | valide config, teste egress, présence/login sbx ; signale les manifestes sans sandbox et, sous `--fix`, récupère leurs worktrees (spec 2026-08-05) |
| `den nest ls` / `den nest show <n> [repo...]` | inspecter les nests déclarés |
| `den source add\|update\|ls\|rm`, `den lint <path>` | sources d'équipe (clones git partagés) — voir spec 2026-08-04 |

Réservé (hors v1, nommage figé) : `den agent <nest> [ticket]`, `den review <name>`.

---

## 6. Data flow du spawn — `den spawn <nest> [-w <wt>] …`

1. **Résolution.** Charge `config.yaml` + `nests/<nest>.yaml` + `stacks/<stack>/stack.yaml`.
   Fusion en cascade.
2. **Sélection des repos.** Requis toujours inclus ; optionnels filtrés par `--without`/`--only`
   ou **checklist interactive** (`-i`).
   La sélection produit `[positionnels…] ++ selectRepos(déclarés)`, dont l'unicité des basenames est
   **vérifiée sur la liste fusionnée** : une collision est une erreur, pas un doublon écarté en
   silence — le basename adresse `--without`/`--only`, devient `worktree_root/<wt>/<repo>` et une
   position dans l'argv de `sbx create`, donc deux homonymes rendent les trois ambigus (§2). Sous
   `-w`, la git-ité de chaque repo est sondée à ce moment — avant tout effet de bord — et le common
   git dir obtenu est réutilisé plus bas plutôt que redemandé à git.
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
   l'**egress nest** en **`permissions.network.allow`** ; et en **dernière** `setup.startup` la
   **commande de fraîcheur de l'agent** (§9.1). Dernière et pas ailleurs : elle est fail-closed, et
   le dispatcher sbx interrompt toute la suite au premier échec. Si un `mounts:` porte un `link:`,
   sa phase de liens devient la **première** entrée de `setup.startup`, avant la fraîcheur
   (§10.1).
6. **Assemblage `sbx create`** :
   `--name <nest>[.<wt>]`, `--template <stack.image>`,
   `--kit <stacks/<stack>/kits[i]>…  --kit stacks/<stack>/kit  --kit <mixin généré>`
   (**le mixin généré reste le dernier `--kit`** — même raison qu'au point 5),
   agent positionnel **`shell`** (obligatoire : `sbx create [flags] AGENT PATH [PATH...]`), puis
   positionnels = chemins worktree/repo + `config_dir` + **les mounts de `mounts:`, en dernier**
   (`~/.ssh_sbx` en fait partie via le sucre `ssh.mode=mount`, §10.1).
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

**Limite connue du teardown — LEVÉE le 2026-08-05** (spec `2026-08-05-sandbox-manifest-design.md`,
D3/D4) : den écrit à la création un manifeste `state/sandboxes/<sandbox>.yaml` de ce qu'il a
réellement monté, et `den rm` le rejoue au lieu de re-dériver. Le positionnel est donc nettoyé comme
le reste, et la « moitié asymétrique » décrite ci-dessous n'a jamais été implémentée : elle est
sans objet. Le paragraphe est conservé pour l'historique du raisonnement.

**Limite connue du teardown.** `den rm` ne nettoie PAS le worktree d'un repo passé en positionnel.
Un positionnel ne fait pas partie de l'identité (décision 7) : den ne le persiste nulle part, et
`den rm` reconstruit ce qu'il doit nettoyer à partir du seul nom de sandbox, via les `repos:` du
nest — il ne peut donc pas savoir que ce worktree a existé. Le répertoire et son enregistrement git
restent en place ; en layout `per-repo` l'orphelin atterrit sous `<repo>/.den/<wt>`, dans le repo de
l'utilisateur, avec la ligne `.den/` que den a ajoutée à son `.git/info/exclude`. Le cas préexistait
pour un repo retiré de `repos:` avant le teardown ; les positionnels en font le chemin ordinaire.
Le correctif tient en deux moitiés asymétriques : en layout central, énumérer `worktree_root/<wt>/*`
et traiter les entrées que `repos:` n'explique pas ; en `per-repo`, l'énumération est impossible
faute du chemin du repo, et il ne reste qu'à avertir. Cette asymétrie mérite son propre changement
plutôt qu'un passager sur celui-ci.

### Build DAG — `den build [stack] [--force]`
- Parse tous les `stacks/*/stack.yaml` → graphe via `parent`.
- `den build dgdevx` → construit `devx` d'abord **si son image manque**, puis `dgdevx`.
  `den build` (sans arg) → tout, ordre topologique.
- **den possède la séquence de build entière** : `create` → N × `exec` → `stop` → `template save` →
  `rm`. Une stack ne fournit que le contenu des `exec`, sous `provision:` (§4.2). `--force`
  reconstruit aussi les ancêtres.

> **Amendement du 2026-08-03.** Ce qui suit remplace le modèle livré par #8, où chaque nœud lançait
> son `stacks/<n>/build.sh` **sur l'hôte**, inchangé, et où `versions.lock` était « tenu à jour par
> les `build.sh` ». Le `build.sh` par stack **disparaît** ; `versions.lock` sort du modèle den pour
> la v0 — den ne prétend plus rien sur le versionnage des outils, les épingles restent où elles sont
> déjà, en dur dans les scripts. La raison du remplacement est en tête de sous-section, ce n'est pas
> une préférence de style.

#### Pourquoi den possède la séquence

Le modèle #8 laissait le nom de l'image s'écrire **deux fois sans lien** : `image:` dans
`stack.yaml`, et le tag passé à `sbx template save` dans le script. `Execute` ne contrôlait rien
après le script — le code de sortie, et c'est tout. Éditer l'un sans l'autre produisait ceci :

1. `den build devx` → le script tourne, sort en 0, den annonce le succès ;
2. `den spawn` → `checkStackImage` ne trouve pas `image:` dans l'inventaire → « lance
   `den build devx` » ;
3. …ce que l'utilisateur vient de faire, avec succès.

Une boucle fermée dont les deux messages sont **individuellement exacts**. C'est exactement la
classe de panne que le §11 existe pour tuer — le `403 Forbidden` d'un cran au-dessus : un diagnostic
vrai qui désigne le mauvais coupable. den faisant lui-même le `template save`, le nom est correct
**par construction** et la panne n'a plus d'endroit où exister.

Deux gains suivent, et ils comptent presque autant :

- **La justification de `Script`/`ExecScript` était circulaire.** « L'implémentation réelle lance un
  process, donc on l'isole derrière une interface » : elle est intestable *parce qu'*elle est
  arbitraire. La séquence den est cinq formes d'argv à travers `sbx.Runner`, et `sbx.Fake` existe
  déjà en fichier de production pour ça. Il ne reste à `internal/build` que du **filesystem** — lire
  les fichiers de `provision:`, créer le scratch — rejouable sur un dossier temporaire, là où la
  surface intestable croissait avec chaque script utilisateur.
- **Le teardown devient un invariant.** C'est le `trap` que chaque script devait écrire à la main et
  qu'aucun test ne pouvait vérifier.

#### La séquence, pour une stack `S` d'image `I`

| # | Commande | Note |
|---|---|---|
| 1 | `sbx create --name S-build [--template <image du parent>] <shell\|base> <scratch>` | `--template` + positionnel `shell` si `parent:` ; positionnel `base` sinon |
| 2 | `sbx exec S-build -- bash -lc "<includes><step i>"` | une fois par entrée de `steps`, dans l'ordre |
| 3 | `sbx stop S-build` | |
| 4 | `sbx template save S-build I` | **den passe `I`** — c'est tout le point |
| 5 | `sbx rm --force S-build` | **différé** : court aussi sur échec de 2, 3 ou 4 |

Le positionnel n'est porteur qu'en l'absence de `--template`. Avec une image, c'est son **flavor**
qui décide de la commande attachée, doctrine déjà écrite dans `sbx.PositionalAgent` — d'où `shell`
pour les dérivées, qui ne promet rien qu'il ne tienne. Sans image, il **choisit la base** :
`base: claude` → `docker/sandbox-templates:claude-code-docker`. C'est le seul endroit où den doit
connaître un agent sbx, et la seule raison d'être de la clé `base`.

Le `<scratch>` est un dossier vide sous `~/.den/cache/build/<S>/` : `sbx create` exige au moins un
chemin, et monter le `/tmp` de l'hôte dans une VM de build n'a aucune justification.

**Un step n'a aucun accès au système de fichiers de l'hôte, et c'est délibéré.** La seule matière
hôte qui entre dans la VM est le **texte** des fichiers `includes:` et `steps:`, transporté dans le
payload de l'`exec` ; tout le reste — paquets, binaires, `.deb`, archives — s'obtient **par le
réseau**, sous la policy egress. C'est une restriction réelle par rapport au modèle #8, où le script
tournait sur l'hôte et pouvait lire n'importe quel fichier. Vérifié contre `sbx-devbox` le
2026-08-03 : aucun build n'en a besoin. Le seul fichier hôte que ses deux scripts lisent est
`lib/common.sh`, qui est précisément le cas d'`includes:` ; les `.env.dist` des dossiers de stack
sont du **runtime** et le disent (« sourcé EN PLUS du kit par la session, pas figé dans l'image »).
Le jour où un build aurait vraiment besoin d'un fichier hôte, ce sera un ajout conscient à cette
liste, pas un trou qu'on découvre.

#### Sonde egress du 2026-08-03 — ce qui est attesté, et ce qui ne l'est pas

Le paragraphe ci-dessus engage la VM de build à joindre les dépôts de paquets : c'est par là que
passe *tout* ce qu'un step installe. Une sonde a été lancée sur la machine de l'utilisateur ce
jour-là, puis démontée.

| Mesuré | Verdict |
|---|---|
| `sbx create --name den-egress-probe claude <scratch>` — l'argv **exact** que den construit pour une stack RACINE, sans kit | accepté, sortie 0, agent `claude` |
| `sbx exec <name> -- bash -lc '<payload>'` | fonctionne, `--` compris |
| `curl` vers `deb.debian.org`, **depuis l'intérieur** de la VM | HTTP 200 |
| `curl` vers `registry.npmjs.org`, depuis l'intérieur de la VM | HTTP 200 |

Une VM de build **sans kit** joint donc les dépôts, et la séquence du §6 n'a pas à y injecter de kit
egress pour que le modèle tienne.

**Le caveat, et c'est tout le caveat.** Sur cette machine, `sbx policy check network --json`
rapportait `"governance": {"active": false}` — globalement **et** dans le contexte de la sandbox. La
sonde n'atteste donc que le **cas permissif** : gouvernance inactive, tout passe, ce qui est
exactement ce qu'on observerait d'une VM sans aucune policy. Autrement dit elle prouve que rien dans
l'argv de den ne ferme le réseau ; elle ne prouve rien sur ce qu'une gouvernance active laisserait
passer.

**Question ouverte, à trancher avant le premier vrai build sous gouvernance active** : ce qu'obtient
une VM de build sans kit quand `governance.active` vaut `true`. La réponse décide si la séquence doit
poser un kit egress sur la VM de build, et lequel — une stack déclare déjà `egress:` (§4.2) pour ses
*spawns*, et rien ne dit aujourd'hui que la même liste convienne à son *build* (un build tire des
paquets que le runtime n'a plus besoin de joindre). Tant que ce n'est pas mesuré, ce n'est pas écrit
ici : ce dépôt atteste le comportement de `sbx` avec sa date, il ne l'extrapole pas.

**Le smoke réel n°3 du 2026-08-03 (issue #31) n'a pas fermé cette question, et l'a laissée ouverte
délibérément.** `sbx policy check network deb.debian.org --json` y rendait encore
`"governance": {"active": false}` : aucun banc à gouvernance active n'existe sur cette machine, et
l'absence de banc ne ferme pas une question. Ce que ce smoke a fermé est le RESTE de la séquence —
`stop`, `template save`, l'appariement des références nues, la chaîne parent, les chemins d'échec
(relevé au §14.0 « commandes relevées le 2026-08-03 », détail dans
`docs/superpowers/handoffs/2026-08-03-smoke-reel-3.md`).

Un fait mesuré ce jour-là que toute stack doit connaître, et qui appartient à l'image de base et non
à den : **la VM de build joue son propre `apt-get` au démarrage.** Un step qui appelle `apt-get`
immédiatement court dessus et sort en `100` (`Could not get lock /var/lib/apt/lists/lock`). Le step
doit attendre le verrou. den rapporte l'échec correctement — il ne le masque pas — mais il ne peut
pas l'éviter à la place de la stack.

**Une sandbox `S-build` préexistante est un refus, pas un `rm --force` préalable.** `S-build` est un
nom de nest parfaitement légal — le charset des composants (§2) l'autorise — donc un nettoyage
aveugle peut détruire une vraie sandbox de l'utilisateur. Le teardown étant différé, un résidu ne
survit qu'à un den tué au SIGKILL : assez rare pour mériter un œil humain, et le message nomme
`sbx rm --force S-build`. C'est la doctrine du §2 — den refuse plutôt que de normaliser en silence —
appliquée à la seule opération destructrice de la commande.

#### `provision:` — ce que la stack fournit

`steps` est joué **un `sbx exec` par entrée**, ce qui donne le nommage de l'étape fautive :

```
stack "devx": step 2/3 $DEN_HOME/stacks/devx/provision/gh.sh failed: exit status 1
```

**Le chemin est ABSOLU, et c'est arbitré** (issue #30). Ce spec a longtemps montré
`./provision/gh.sh`, un chemin relatif, alors que `config.LoadStack` résout `provision.steps` en
chemins absolus dès le chargement (`resolveAgainst`) : den n'a jamais émis autre chose qu'un chemin
absolu, et c'était **l'exemple** qui avait tort. Il est corrigé plutôt que le rendu, pour la doctrine
du §2 — le message nomme le fichier à ouvrir, et un chemin cliquable dans un terminal qui les résout
vaut mieux qu'un chemin court. `$DEN_HOME` est écrit ici pour que l'exemple ne dépende pas de la
machine qui l'a rédigé ; le message réel porte le chemin expansé.

Le prix de ce nommage est que chaque `exec` ouvre **un shell neuf**. Entre deux steps, **seul le
système de fichiers de la VM persiste** : les paquets installés et les binaires posés restent, les
variables, fonctions et `cwd` meurent avec le process. Un step qui doit transmettre une variable à un
step suivant l'écrit (`/etc/profile.d/…`), il ne l'`export`e pas.

`includes` est la réponse à cette perte, et **ce n'est pas un script joué en premier** : son texte
est concaténé **en tête de chaque step**. La différence est observable, pas cosmétique —

| contenu placé dans `includes` | si c'était « joué d'abord » | réalité (réinjecté par step) |
|---|---|---|
| `common::gh() { … }` | visible au step 1 seulement | visible dans **tous** les steps |
| `export PATH=…` | perdu dès le step 2 | présent dans **tous** les steps |
| `apt-get install …` | une fois | **N fois** |

D'où le contrat, qui est la raison d'être du mot : **`includes` définit, il n'agit pas.** Un effet de
bord y est rejoué une fois par step, en silence. Le contrat n'est pas vérifiable par den — il est
écrit ici, et le nom de la clé est choisi pour qu'un `apt-get install` s'y lise comme une faute avant
toute documentation. Mesuré le 2026-08-03 : le `lib/common.sh` de `sbx-devbox` le respecte **sans
modification** — cinq définitions de fonctions, et au niveau racine un seul `set -euo pipefail`,
idempotent et souhaitable dans chaque step.

`includes` est **optionnel** : une stack dont les steps sont autonomes ne le déclare pas, et den
n'injecte alors aucun préfixe.

Deux servitudes du modèle #8 disparaissent : les fichiers n'ont plus besoin du **bit exécutable** ni
d'un **shebang**, puisque den lit leur contenu au lieu de les lancer — le `chmod +x` oublié, mesuré
le 2026-08-03 et diagnostiqué tardivement, n'a plus de cas. En contrepartie le shell cesse d'être le
choix du script : den envoie `bash -lc`, le `-l` étant requis pour que le `PATH` de la base (go,
node) soit chargé.

**Ce que l'implémentation (#8) fixe en plus, et pourquoi.** L'amendement ci-dessus ne remplace que
le **modèle d'exécution** — qui lance quoi, et où. Tout l'**arbitrage** de #8 survit intact : il
répond à « quelles stacks, dans quel ordre, lesquelles sauter », questions qu'aucun changement de
séquence ne touche.

- **La cible est toujours reconstruite.** L'utilisateur l'a nommée ; seuls ses ancêtres sont un
  moyen. Corollaire : l'inventaire n'est **jamais** interrogé sur l'image de la cible.
- **`den build` (tout) et `--force` ne lisent aucun inventaire.** Ils reconstruisent par définition,
  donc ils ne peuvent pas être mis en échec par un `sbx template ls` cassé. Seule la forme qui
  arbitre réellement paie l'appel — `SbxImages` le lit **au plus une fois** par plan.
- **Un inventaire illisible est un refus, pas un fail-open** (contrairement au contrôle d'image du
  spawn, §14.1, qui n'améliore qu'un message) : sauter un build non justifié produit exactement le
  403 que la commande existe pour éviter, et construire quand même dépense des minutes non
  demandées. Le message nomme `den build <cible> --force`.
- **Ordre déterministe** : racines triées par nom, ancêtres émis avant leurs descendants. Un `parent`
  étant unique, il ne reste aucune égalité sous les racines — d'où le golden
  `internal/build/testdata/order-all.golden`.
- **Appariement de l'image** : `sbx.NormalizeImageRef` qualifie des deux côtés avant comparaison —
  `devx:v1` ↔ `docker.io/library/devx` + `v1`, `library/devx` est un *namespace*, `ghcr.io/…` et
  `localhost[:port]/…` sont des *registres*, un `:` avant le dernier `/` est un port et non un tag,
  et un tag vide (`devx:`) n'est pas complété en `latest` (il ne doit apparier aucune image).
  Une référence **épinglée par digest** (`devx@sha256:…`) est inarbitrable par construction —
  `sbx template ls` ne rapporte que `repository` + `tag`, jamais de digest — donc elle n'apparie
  rien (`sbx.IsDigestRef`) : `den build` reconstruit l'ancêtre (dépenser un build plutôt que sauter
  sans justification), le contrôle du spawn se tait (voir §11 et §14.1).
- **Un `image:` vide est un refus au chargement, inconditionnel** (§4.2, qui porte les trois
  conséquences et la raison de l'inconditionnalité). C'est le complément indispensable de « den
  possède `template save` » : la séquence rend le nom correct par construction, elle ne le rend pas
  non vide. Sans ce refus, la boucle fermée que tout cet amendement existe pour tuer se rouvre
  telle quelle, un cran plus bas.
- **Une stack sans `provision.steps` n'est pas constructible, et c'est une configuration déclarée** :
  son `image:` peut nommer une image de registre que `sbx` tire. Elle est **sautée et nommée**,
  jamais un refus — c'est la même réponse que le contrôle d'image du spawn (§14.1), et les deux
  doivent s'accorder. Mesuré le 2026-08-03 : elles ne s'accordaient pas, et un `den build` sur un den
  comportant une seule stack tirable ne construisait **rien** en exigeant un script pour la stack que
  den avait déjà décidé de ne pas construire. **Seule exception** : la stack *nommée* par
  l'utilisateur, qui est un refus — une ligne « sauté » se lirait comme un succès.
- **Tous les fichiers de `provision:` sont lus avant le premier `create`.** Bâtir quatre minutes
  d'image de base pour découvrir ensuite qu'un `includes:` ou un `steps:` de la chaîne est illisible,
  c'est dépenser ce temps pour atteindre un refus que den pouvait rendre instantanément. La lecture
  est **anticipée**, pas seulement l'existence contrôlée : den doit de toute façon disposer du texte
  pour composer le payload, donc rien n'est relu deux fois. `Plan` tranchant déjà le cas, ce contrôle
  reste un **garde-fou** (forme du garde de `agent.RenderMixin`) : `Step` est une structure exportée
  nue, et un plan construit à la main ne doit pas atteindre la moitié de la chaîne avant de découvrir
  le trou.
- **Une stack cassée ne coule pas le build** (doctrine `config.LoadStacks`) : nommée sur stderr, non
  construite. Elle n'est un refus que si elle est la cible ou un ancêtre de la cible. Mesuré le
  2026-08-03 : utilisée comme `parent:` d'une stack saine, elle coulait tout le `den build` sans
  cible. Corrigé : les stacks dont l'ascendance atteint une stack illisible — ou un `parent:` qui
  n'existe pas — sont **exclues et nommées** (`build.Excluded`), les saines construites ; le remède
  diffère selon la cause (stack illisible : réparer *son* fichier ; parent inexistant : réparer la
  ligne `parent:` du déclarant). Un **cycle** reste un refus même sans cible : c'est une
  contradiction qu'aucune stack ne peut contourner.

---

## 7. Policy réseau & settle-loop (douleur #1)

- Egress effectif (§4) posé en **`permissions.network.allow` du mixin généré** → **auto-scopé** à la sandbox
  au `create` (aucune règle globale qui fuite d'un projet à l'autre) et **présent dès le
  create-time** (pas de pose paresseuse — la propagation sbx n'est pas instantanée).
- **Settle-loop fail-closed** : après create, `den` boucle jusqu'à ce que **tous** les hôtes soient
  autorisés, **timeout borné**. Si un hôte ne passe pas → `den` **n'attache pas**, liste les hôtes
  bloqués, sort en erreur. Jamais de « ça marche à moitié ».
- **Un tour coûte deux appels, pas un par hôte** (2026-08-05). Le prix d'un `policy check` est le
  **process** `sbx`, pas le travail de policy : mesuré, `sbx --version` — qui n'appelle même pas le
  daemon — coûte 390 ms contre 486 ms pour un `policy check`, et vingt process concurrents prennent
  6,4 s contre 7,8 s en série (paralléliser plafonne à ~30 %). Sur une allowlist de 26 hôtes la
  passe séquentielle coûtait ~12 s d'un spawn de ~36 s. Donc, par tour :
  1. **un** `sbx policy ls <name> --json` dit **ce que contient** la règle scopée — den y cherche
     verbatim les chaînes qu'il a lui-même écrites dans le mixin, jamais une sémantique de glob
     (réimplémenter l'autorisateur est la façon dont les deux divergent) ;
  2. **un** `sbx policy check network --sandbox <name> <témoin>` dit si cette règle est **vivante**.
     Le listing seul ne suffit pas : mesuré sur quatre spawns consécutifs, `policy ls` montre la
     règle **83 à 172 ms avant** que `policy check` n'autorise l'hôte qu'elle seule couvre —
     attacher sur le listing seul serait le demi-démarrage que ce § interdit, en plus étroit.
     Le témoin est choisi parmi les hôtes qu'**aucune autre** règle active ne mentionne : l'allowlist
     de den atterrit en **une seule** règle scopée, donc sa propagation est atomique.
- **Repli** : listing illisible, schéma `policy ls` inconnu, ou règle `deny` active (une question de
  précédence que den ne modélise pas délibérément) → passe autoritaire hôte par hôte, comme avant,
  concurrente et bornée. Un hôte simplement **pas encore** listé n'est pas un repli : c'est la
  propagation ordinaire, le tour dort et regarde à nouveau.
- **Asymétrie assumée** : un `policy ls` que den ne reconnaît pas ne refuse **jamais** l'attache (le
  fast path est une optimisation), là où un `policy check` illisible, lui, refuse.

**Schéma de kit (relevé sur les kits réels, pas déduit) :** `schemaVersion: 2`, `kind: mixin`,
`name`, `version`, `description` ; les capacités réseau vivent sous **`permissions.network.allow`** (liste
de `host`, `host:port`, `ip` ou `ip:port`), les variables sous **`environment.variables`**, les
commandes de boot sous **`setup.startup[].command`** (tableau argv). `sbx policy check network`
évalue un hôte nu **sur le port 443** : une entrée egress nue est donc cohérente de bout en bout,
den ne normalise rien.

---

## 8. Modèle de ports

Objectif : **URLs stables bookmarkables** en usage courant **et** anti-collision quand plusieurs
sandboxes tournent.

- **Fenêtre déterministe par nest** : `base = 9000 + hash(nest.name) % 900 * 10` → **10 ports par
  nest**, stable pour ce nom. Surchargeable via `ports.base`. La plage couverte est **9000–17990**
  (900 blocs de 10) : la formule est juste, mais un lecteur qui en déduit « un port à quatre
  chiffres » se trompe — `gamma` tombe sur 11340 (mesuré au smoke n°2).
- **Offset par ordre de déclaration** : port déclaré *i* → `host = base + i`.
- **L'argument est un nom de SANDBOX**, pas de nest : `den ports <sandbox>`, comme `den sh` et
  `den rm`. Les ports sont publiés dans une VM vivante, et seul un nom de sandbox dit laquelle. La
  **fenêtre**, elle, est semée par le **nest** auquel ce nom appartient (`sbx.SplitName`) : §8 promet
  une URL bookmarkable *par nest*, et une fenêtre hachée depuis `api.feat12` donnerait à chaque
  worktree sa propre base.
- **Publication à la demande, et d'abord une LECTURE** : `den ports <sandbox>` lit ce que cette
  sandbox **publie déjà** (champ `ports` de `sbx ls --json`, §14.0), puis :
  - elle est déjà publiée sur une fenêtre du nest → **den la réutilise** et ne publie que ce qui
    manque. Le bloc le plus bas gagne, donc une sandbox polluée revient d'elle-même sur sa fenêtre
    canonique ;
  - sinon → il **scanne** `127.0.0.1:base..base+9` ; si libre, il publie via
    `sbx ports --publish 127.0.0.1:H:C` (cas courant, URL canonique) ; si occupée, il **décale la
    fenêtre entière** au bloc de 10 suivant et **avertit** (non-canonique, cette instance
    seulement).

  **La lecture vient avant le scan, et ce n'est pas un détail d'implémentation.** Le scan lit
  l'hôte, un port lié ne nomme personne, et la publication de l'exécution *précédente de den* est
  exactement ce qui rendait le bloc canonique occupé : relire le tableau décalait la fenêtre à
  chaque fois et empilait les publications (3 ports déclarés → 9 publications en 3 exécutions,
  échec dur à la 11ᵉ), et une relance `--add` à l'identique échouait en `409 … already published`
  après avoir lié une fenêtre de plus. `sbx ports --publish` **n'est pas idempotent** : republier
  par-dessus n'est pas une option, il faut savoir ce qui est déjà là. Issues #15 et #22.
- **La sandbox doit tourner.** Une sandbox **arrêtée** ne porte pas le champ `ports` du tout alors
  que ses publications reviennent à la reprise, et `sbx ports --publish` n'y redémarre rien (là où
  `sbx exec` le fait) : `den ports` **démarre** donc la sandbox arrêtée, l'annonce, et **relit**
  avant de résoudre. Voir §11 pour la décision d'ensemble.
- **Éphémères** (docker compose, ports non déclarés) : OS-assigned, affichés mais jamais « stables ».
- **Sécurité — non négociable** : toujours `127.0.0.1`, **jamais `0.0.0.0`**. « Accès depuis
  l'extérieur » → **pas** de bind LAN ; `den` imprime un **tunnel SSH prêt-à-coller**
  (`ssh -L H:127.0.0.1:H you@hôte`), l'auth déléguée à SSH.

  Sur `loopback_lock`, ce spec disait « **refusé** hors loopback même si forcé ». La v1 **n'offre
  aucun moyen de forcer**, et c'est le texte qui est corrigé, pas le code (issue #19). Le refus
  existe (`ports.Resolve`, derrière `Options.HostIP`), mais **aucun chemin CLI ne renseigne ce
  champ** : la seule chose non-loopback qu'un utilisateur peut taper, `--add 0.0.0.0:H:C`, est
  rejetée plus tôt par `ParseAdd`. La garde est donc un **contrôle de frontière sur une structure
  exportée à champs publics**, délibérément inatteignable depuis la CLI — pas un oubli, et pas une
  raison d'ajouter un drapeau qui élargirait la surface v1 pour satisfaire une phrase. Le marqueur
  `[loopback-locked]` du tableau reste **informatif** : il dit ce que le nest a déclaré.
- **Deux contrats réglés par #6, à ne pas reperdre** :
  - un nest **sans `ports:`** ne place aucune fenêtre : ni en-tête `window:`, ni ligne `remote?`, ni
    scan de l'hôte. Le tableau se rend quand même (les paires `--add` sont tout l'intérêt de la
    commande sur un tel nest) ;
  - `--add` a un **contrat d'abort honnête** : den publie une paire à la fois, donc un refus de
    `sbx` en cours de route s'arrête là — ce qui était publié le reste, aucun tableau n'est rendu, et
    l'erreur nomme le port. Un argv groupé n'aurait pas pu le nommer.

**Affichage type :**
```
nest: web   sandbox: web.feat123   window: 9100-9109 (canonical)
  NAME  CONTAINER  URL
  vite  5173       http://127.0.0.1:9100   [opened]
  api   3000       http://127.0.0.1:9101
  cdp   9223       http://127.0.0.1:9102   [loopback-locked]
  remote?  ssh -L 9100:127.0.0.1:9100 you@$(hostname)
```

`http://` sur **toutes** les lignes, y compris CDP — ce spec écrivait `ws://` et c'est l'exemple qui
avait tort. `ports.publish` ne déclare aucun protocole : den devinerait depuis le nom d'un port, et
devinerait faux sur la ligne que l'utilisateur est le plus susceptible de coller.

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
registre, injectée par `den` en `setup.startup` du mixin généré.

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

### 9.2 Comment la porte est TENUE (et non seulement promise)

Écrit après le smoke réel n°2 (issue #18) : jusque-là, **aucun code ne portait la promesse
ci-dessus**. den attendait la policy réseau et rien du dispatcher de kits — il annonçait `ready` et
sortait 0 environ **35 s avant** la fin de la commande de fraîcheur, et quand celle-ci **échouait**
il ne le disait ni alors ni jamais, pas même à la ré-attache. L'agent n'avait jamais été mis à jour.

La porte est observable parce que le journal du dispatcher est **lisible par machine**
(`/var/log/sbx-kit-startup.log`, format relevé au §14.0). den y lit le verdict de **son propre
mixin**, identifié par le nom de kit qu'il génère lui-même (`den-<sandbox>`, les `.` aplatis en
`-`) — **une seule fonction** pour les deux usages, sans quoi la porte surveillerait un kit que
personne ne génère.

Quatre verdicts, et ce qu'ils coûtent :

| verdict | ce que den fait |
|---|---|
| **passé** (`ok <chemin>`) | rien : c'est le cas ordinaire, et l'annoncer à chaque spawn enterrerait les lignes qui comptent |
| **échoué** (`fail <chemin> exit=<n>`, ou un kit **antérieur** qui a fait avorter la passe) | **refuse**, sur les deux chemins, en portant la ligne du journal. Le §9.1 dit fail-closed ; c'est la même discipline que la settle-loop, qui refuse déjà d'attacher sur une policy non posée |
| **pas encore rendu** | une `note:` — pas un `warning:` : sous `--detach` elle s'imprime sur presque chaque spawn, et un avertissement sur le chemin heureux apprend à sauter ceux qui comptent |
| **absent** (passe complète sans jamais nommer le mixin de den) | un `warning:` : ce n'est pas un agent périmé mais une VM sans mixin den — ce à quoi ressemble une sandbox créée par un den plus ancien. Attendre n'y changerait rien |

**Où den ATTEND — l'arbitrage.** Attendre coûte la différence entre un spawn de 7,6 s et un de
~42 s (mesuré), donc l'endroit a été tranché, pas supposé :

- **il attache un shell** → il **attend**, en l'annonçant. L'utilisateur est sur le point de lancer
  l'agent : c'est à ça que sert la sandbox, et lui en donner un périmé est exactement l'échec que
  le §9.1 existe pour empêcher ;
- **`--detach`** → il **lit une fois** et continue. Personne n'est devant un prompt, l'appelant est
  en général un script qui ne touchera pas à l'agent, et la prochaine attache rattrape. Un verdict
  **déjà** rendu refuse quand même : la ré-attache est précisément un journal qui en porte un ;
- **budget épuisé sans verdict** → une `note:`, jamais un refus : den a attendu ce qu'il promettait,
  et un dispatcher encore au travail n'est pas une preuve d'agent périmé ;
- **sandbox laissée arrêtée sous `--detach`** → porte **sautée**. Lire le journal est un `sbx exec`,
  qui redémarre la VM : la réveiller pour l'inspecter contredirait la ligne que `--detach` imprime
  sur cette même sandbox (§11). Rien n'est perdu — le dispatcher **rejoue** au redémarrage suivant
  (mesuré, §14.0), donc la porte est réévaluée exactement quand la sandbox revient.

- **`den sh <sandbox>` sur une sandbox qui TOURNE** → il **lit une fois**, comme `--detach` et pour
  la raison inverse : la sandbox est déjà debout, le journal porte donc déjà le verdict qui existe,
  et faire patienter une ré-entrée ordinaire pour un verdict non arrivé la taxerait sans rien
  attraper. Un verdict **échoué** refuse, exactement comme sur le chemin du §6 ;
- **`den sh <sandbox>` sur une sandbox ARRÊTÉE** → il **attend**, en l'annonçant, parce que la
  première règle de cette liste s'applique telle quelle : den attache un shell, et il démarre la VM
  pour le faire. Lire une seule fois y serait pire qu'inutile — le `sbx exec … cat` redémarre la
  VM, le dispatcher **rejoue**, et `ParseKitLog` ne lit que le **dernier** bloc : la lecture unique
  tomberait donc sur un bloc qui vient de commencer et n'a rien rendu, imprimerait une `note:` et
  ouvrirait un shell pendant que l'agent se met à jour, le `fail` du bloc précédent devenu
  invisible. C'est le silence de #18 reconstitué à l'intérieur du correctif de #27.

**La porte tient sur les DEUX chemins depuis l'issue #27.** Elle ne tenait d'abord que sur
`den spawn` : `den sh` est une commande à part, qui ne passe pas par la séquence du §6, et sur le
banc la sandbox `gamma` dont la porte venait d'être prouvée fermée (`fail … exit=1`) refusait
`den spawn <nest> --agent broken` **et** donnait un shell en silence à `den sh gamma`. Une garantie tenue
par une porte sur deux est plus trompeuse que pas de garantie du tout, d'autant que `den sh` sur une
sandbox **arrêtée** la redémarre — c'est-à-dire exactement le « une sandbox démarre » dont parle le
§9.1. L'arbitrage du verdict vit désormais dans **une seule fonction** (`spawn.reportFreshness`) :
deux portes qui liraient le même journal et en tireraient des conclusions différentes seraient la
même classe de défaut, reconstituée.

---

## 10. Modèle SSH

- **`agent-forward` (défaut)** : forwarde le *socket* de l'agent SSH → aucune clé n'entre dans la
  VM (pas d'exfil du matériel). Forwarde toutes les identités chargées (scoping via un agent
  dédié à la clé `~/.ssh_sbx` si besoin).
- **`mount ~/.ssh_sbx`** (override courant à l'usage) : équivaut à `mounts: [{host: <ssh.dir>,
  link: $HOME/.ssh}]` (§10.1) — le mount seul ne suffit pas, puisque `sbx` monte au chemin
  **hôte** alors que `ssh` lit `$HOME/.ssh` ; c'est la phase de lien qui comble l'écart. Un agent
  compromis peut lire la clé → mais clé **dédiée, scopée, révocable** → blast-radius borné et
  connu. Simple, headless-ready **de fait** désormais, et non plus seulement d'intention : le lien
  est ce qui rend le mode utilisable sans personne au clavier. Expose **exactement** cette clé,
  rien d'autre.
- **`none`** : réservé au futur flux autonome.

**Ce que den fait, exactement, pour chacun des trois modes** (vérifié en tâche 18 ; colonnes
`mixin` et `workspaces` revues le 2026-08-07 avec l'arrivée de `mounts:`, §10.1) :

| mode | flags de `sbx create` | mixin | workspaces (positionnels de l'argv) |
|---|---|---|---|
| `agent-forward` (défaut) | inchangés | inchangée | inchangés |
| `mount` | inchangés | **+ la phase de liens, en PREMIER `setup.startup`** | **+ les mounts, en dernier** |
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

Ce mode ne donne rien dans **trois** états de l'hôte, et non un seul : le socket prouve qu'un
mandataire existe, jamais qu'une clé répond derrière lui.

| état de l'hôte | ce que den observe | ce que la sandbox hérite |
|---|---|---|
| `SSH_AUTH_SOCK` **absent ou vide** | la variable manque de l'environnement de den | aucun agent |
| agent **joignable mais sans identité** | `ssh-add -l` répond, la liste est vide | un agent vivant et vide |
| agent **injoignable** | socket mort, aucun agent en marche, ou `ssh-add` hors du PATH | rien qui réponde |

Les trois sont des **avertissements** (`[warn] ssh.mode`), jamais des échecs : travailler en local
sans dépôt distant est légitime, et den n'a aucun moyen de savoir si l'utilisateur a besoin de SSH.
Les deux derniers sont ceux qui coûtaient cher : tant que den ne regardait que la variable, ils
passaient pour un `[ok]` et ne se manifestaient que **dans** la VM, par un `git push` en
`Permission denied (publickey)` loin de la cause et sans `~/.ssh` de repli (ce repli-là, c'est
`ssh.mode: mount`).

**Deux surfaces, une seule sonde** (`internal/sshagent` : un `ssh-add -l` borné à 2 s, injecté
comme le reste pour que les trois états soient rejouables sans agent réel sur la machine de test) :

- `den doctor` **nomme le compte d'identités** quand tout va bien — `[ok] ssh.mode agent-forward,
  SSH_AUTH_SOCK=… (N identities)`. Un `[ok]` muet ne laisserait repérer ni un socket périmé ni un
  agent qui a perdu ses clés ;
- `den spawn` en `agent-forward` **avertit sur stderr**, avant le `sbx create` et aussi quand il
  attache à une sandbox déjà vivante. Sur stderr et pas stdout : c'est l'environnement de den qui
  est en cause, pas la sandbox que le reste de la sortie décrit.

Le remède diffère selon l'état, et les messages le disent : un `ssh-add` côté hôte prend effet
**dans une sandbox déjà lancée** (le socket forwardé est un mandataire vivant), alors qu'un socket
absent au moment du `create` n'apparaîtra jamais dans une VM déjà bootée — là, il faut relancer den.

Reste **non vérifiable sans `sbx` installé** : que `sbx` propage effectivement ce socket **dans**
la microVM. C'est l'hypothèse **A10** du §14.1, falsifiable au premier smoke réel par un
`git push` qui échoue depuis la VM alors que la sonde hôte, elle, a vu des clés.

### 10.1 `mounts:` — le mécanisme générique

`ssh.mode: mount` n'est pas un mécanisme à part : c'est du sucre au-dessus d'un besoin plus
large — faire entrer un dossier hôte dans la VM et le rendre atteignable là où l'outil le
cherche — que den exprime maintenant directement. Conçu et mesuré le 2026-08-07, détail complet
dans `docs/superpowers/specs/2026-08-07-mounts-design.md`.

**Surface de configuration :**

```yaml
# ~/.den/config.yaml
mounts:
  - host: ~/.digitaleo       # chemin HÔTE, expansé par den
    link: $HOME/.digitaleo   # chemin VM, expansé par la VM
  - host: ~/.aws
    link: $HOME/.aws
    ro: true
```

Trois champs. `link` et `ro` sont optionnels : un mount sans `link` n'est atteignable qu'à son
chemin hôte — ce qui suffit quand l'outil consommateur lit une variable d'environnement, comme la
config de l'agent. `ro: true` se mappe sur le suffixe `:ro` natif de `sbx` ; défaut :
lecture-écriture (ne jamais monter `.ssh` en `ro` — `ssh` écrit `known_hosts`).

**La règle d'expansion**, énoncée comme une règle et non laissée implicite, parce que la
confondre **est** le bug qui a déclenché ce design :

- **`host:` est un chemin HÔTE**, expansé par den (`ExpandPath`, comme `repos:` et `ssh.dir`).
- **`link:` est un chemin VM**, expansé par le bash de la VM.

Deux machines, deux `$HOME` : `/Users/polochon` et `/home/agent`. Un `link:` préfixé de `~/` est
réécrit en `$HOME/` avant émission — bash n'expanse pas `~` entre doubles guillemets, et le lien
doit rester entre doubles guillemets pour que `$HOME` s'y expanse dans la VM. Les deux formes
(`~/x` et `$HOME/x`) sont acceptées dans la config et désignent le même chemin VM. `host:` et
`link:` sont par ailleurs coupés des espaces superflus, et `link:` perd un `/` final, tous deux au
chargement.

**Global uniquement, pas la cascade.** `mounts:` vit dans `~/.den/config.yaml` seul, ni stack, ni
nest. Ce n'est pas de la paresse : den **refuse déjà** un `path:` sur un nest venu d'une source,
parce qu'un chemin hôte ne voyage pas d'une machine à l'autre — c'est toute la raison de
l'indirection `key:` de `repos:`, et `den lint` en est le juge. Un stack de la source `dg`
déclarant `host: ~/.digitaleo` réintroduirait exactement ce que ce lint existe pour refuser. Si
des mounts par stack deviennent nécessaires, il leur faudra la même indirection par clé que
`repos:` — un design à part entière, pas une extension gratuite.

**`ssh.mode: mount` devient du sucre** : `ssh.mode: mount` + `ssh.dir: X` se résout exactement en
`{host: X, link: $HOME/.ssh}`, injecté dans la liste de mounts. Les deux clés restent — le
basculement `agent-forward` / `mount` reste une décision de sécurité réelle, deux postures
distinctes — mais le chemin de code privé du mode a disparu : retirer `mounts:` fait mourir
`ssh.mode: mount` avec lui, parce que plus rien d'autre ne le porte.

**Le lien, au boot de la VM.** La mixin de den porte `setup.startup` ; la phase de liens en
devient la **première** entrée (la fraîcheur de l'agent reste la dernière, spec §9.1 — sinon le
tout premier boot tournerait sur des chemins non liés). Par mount porteur d'un `link:`, elle
refuse plutôt que de créer un lien dans le vide ou d'écraser des données :

| cible avant boot | action |
|---|---|
| absente | `ln -sfn HOST LINK` |
| déjà un lien symbolique | `ln -sfn HOST LINK` — idempotent, et réécrit un lien qui pointait ailleurs (voulu : la config fait autorité sur la VM) |
| répertoire **vide** | `rmdir LINK` puis `ln -sfn HOST LINK` |
| répertoire **non vide**, ou fichier | refuse, fail-closed, en nommant le chemin |
| source absente dans la VM | refuse, fail-closed : `den mounts: FATAL <src> is not present in the VM (from <key>)` |

Aucun `rm -rf` n'apparaît dans cette phase : `rmdir` échoue de lui-même sur un répertoire non
vide, donc même une écriture concurrente ne peut mener qu'à un boot refusé, jamais à une donnée
détruite.

**Mesuré le 2026-08-07** (`sbx` v0.37.1, den `v1.3.1-14-gf895ffa`) : les trois issues du tableau
ci-dessus se comportent comme prévu sur une vraie sandbox, l'ordre des `setup.startup` est bien
celui déclaré et le dispatcher s'arrête à la première défaillance (le boot refusé n'exécute jamais
la commande de fraîcheur qui le suit), et l'ordre des workspaces tient côté `sbx` — dépôt d'abord,
mounts en dernier, ce qui protège le `-w` de l'attache.

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
| Image stack absente | Stop → « lance `den build <stack>` ». Lu sur `sbx template ls --json` **avant** `sbx create`, et **seulement** si la stack est constructible — elle déclare `provision.steps` (sinon den n'a pas de remède à proposer) — et que l'`image:` n'est pas épinglée par digest (l'inventaire ne rapporte aucun digest, §14.1). Un inventaire en échec est fail-open : `sbx` refuse de lui-même |
| Chemin repo introuvable | Stop **avant** tout create |
| Worktree `<wt>` déjà pris par une autre branche | Stop → propose `--attach-worktree` ou autre nom |
| Policy non settled dans le timeout | **Fail-closed**, n'attache pas, liste les hôtes bloqués |
| `sbx` absent / pas loggé | Message doctor-style (`den doctor`) |
| Mise à jour de l'agent impossible au boot | **Fail-closed** après 3 tentatives (§9.1) : le kit sort non-zéro. Layeré en dernier → aucun autre kit lésé. Diagnostic dans `/var/log/sbx-kit-startup.log` — que **den lit** : il **refuse** d'ouvrir la sandbox et cite la ligne (§9.2) |
| Nom de sandbox déjà vivant | **Spawn-or-attach** (pas une erreur) : attache |
| Sandbox **arrêtée**, et l'opération exige une VM vivante (`den ports`) | den la **démarre**, l'annonce sur stderr, puis **relit** son état. Voir la décision ci-dessous |
| Sandbox **arrêtée** sous `den spawn --detach` | den ne démarre rien et **ne dit pas `ready`** : il dit qu'elle reste arrêtée, que son état est préservé, et quelles commandes la relancent |

**Une décision, deux situations** (issues #16 et #17, écrite ici parce qu'elle gouverne deux
commandes) :

> den réveille une sandbox **seulement si l'opération elle-même exige une VM vivante**, et il
> **n'annonce jamais un état qu'il n'a pas vérifié**.

Le §2 (« den refuse plutôt que de normaliser en silence ») porte sur l'**intention** de
l'utilisateur — une clé mal tapée, une contradiction de drapeaux. Publier un port n'a rien
d'ambigu : ça exige un endpoint. Un refus nommerait bien l'état et le remède, mais la seule suite
possible serait de lancer une commande qui démarre la VM — celle que den refusait de faire. F2 avait
déjà tranché dans ce sens pour `den sh` et `den spawn`, où `sbx exec` redémarre de façon
transparente ; `sbx ports` ne partage pas ce comportement, d'où un `500 Internal Server Error: … no
container endpoint with IP address found` qui ne nommait ni la cause ni le remède.

Le même principe appliqué à `--detach` donne le geste **inverse** : la vérité qu'achèterait un
réveil vivrait ~45 s (sbx range les sandboxes inactives à cette vitesse, mesuré), et le contrat de
`--detach` est précisément de **ne pas** entrer dans la VM. L'opération n'exige pas de VM vivante ;
c'est la phrase qui doit être vraie.

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
  build/                 # graphe `parent`, ordre, arbitrage d'image, séquence de build (§6)
  doctor/                # diagnostic de la configuration et de l'environnement
  spawn/                 # orchestration de `den spawn` (§6), hors cli pour rester testable
```

**Tests (TDD) :**
- **Unitaires sur la logique pure** (cœur de la valeur, zéro sbx) : cascade de config,
  union/dédup egress, calcul fenêtre de ports + anti-collision, sélection repos, rendu du mixin
  YAML, **assemblage de l'argv `sbx create`** (golden files : on assert la commande exacte).
- **`build/` ne lance plus aucun process** depuis l'amendement du §6 : la séquence
  `create`/`exec`/`stop`/`save`/`rm` passe entière par `sbx.Runner`, donc par `sbx.Fake`, et le
  golden porte sur la **suite d'argv** d'un build à deux stacks. Il ne subsiste que du filesystem
  (lecture des fichiers de `provision:`, création du scratch), rejouable sur un dossier temporaire
  comme `worktree/` le fait déjà. C'est ce que le modèle précédent interdisait — lancer un
  `build.sh` réel n'est pas testable, et cette surface-là croissait avec chaque script utilisateur.
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
9. Policy **déclarative** (baseline ∪ stack ∪ nest) matérialisée en `permissions.network.allow` scopé +
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

**Deux dates.** Le relevé initial est du 2026-07-28 ; le **smoke réel n°2 du 2026-07-31** l'a
complété et en a corrigé une affirmation. Chaque entrée ci-dessous porte sa date quand elle n'est
pas de la première.

### v0.38.0 renomme le schéma de kit (relevé du 2026-08-10)

La machine est passée en **v0.38.0** (`sbx version: v0.38.0 c022b146…`) et le `den spawn` est mort
**avant toute création de VM**, sur le premier `--kit` :

```
ERROR: resolve kits: kit "…/kits/ssh-known-hosts": artifact: invalid spec.yaml:
  yaml: unmarshal errors:
    line 15: field commands not found in type spec.specFileV2
```

Relevé par sondage de `sbx kit validate` (un kit minimal par clé candidate). Le type s'appelle
toujours `specFileV2` et `schemaVersion: 2` n'a pas bougé : **c'est la forme de la v2 qui a
changé**, pas sa version.

| avant (≤ v0.35) | après (v0.38) |
|---|---|
| `caps.network.allow` | **`permissions.network.allow`** |
| `commands.startup[].command` | **`setup.startup[].command`** |
| `environment.variables` | inchangé |

Autres clés de premier niveau acceptées par `specFileV2` : `agentInstructions`, `credentials`,
`requires`, `volumes`, `ports`, et `sandbox` (refusée pour `kind: mixin`). Sous `setup` :
`startup`, `install`, `files`. Sous `permissions` : `network`. `schemaVersion: 1` valide encore
`caps:` + `commands:` — c'est une porte de sortie, pas la nôtre : den n'écrit que la forme v2
courante.

**Ordre des `setup.startup` re-mesuré le 2026-08-10**, sur v0.38.0, avec un kit sonde à deux
entrées (`echo FIRST` puis `echo SECOND`, sandbox `den-order-probe`, template `digitaleo:v1`) :
`/tmp/den-order.txt` contient `FIRST` puis `SECOND`, et les entrées apparaissent sous
`/etc/durable-startup.d/002-startup-den-order-probe`. **Les deux entrées tournent, dans l'ordre
déclaré.** Un renommage de clé n'est pas une promesse sur le runtime, et « la fraîcheur en
DERNIER » est un invariant de sûreté (§9.1) : il fallait le re-mesurer, pas le supposer.

### `sbx exec` non interactif (relevé du 2026-08-10, v0.38.0)

Mesuré contre une sandbox vivante (`swimspot`), sur la machine de l'utilisateur.

- **Le code de retour de la commande interne remonte** : `sbx exec <sb> sh -c 'exit 42'` → `rc=42`.
  C'est ce qui rend `den exec` utilisable en CI, et c'était le fait bloquant de l'issue #60.
- **stdout et stderr restent séparés sans `-it`** : `sh -c 'echo OUT; echo ERR >&2' 2>fichier`
  laisse `OUT` sur la stdout et écrit `ERR` dans le fichier. C'est ce qui interdit de réutiliser
  `Runner.Stream`, qui les fusionne délibérément.
- **Une stdin redirigée atteint la commande SANS `-i`** : `printf '…' | sbx exec <sb> cat` rend le
  texte. Divergence mesurée avec `docker exec`, qui exige `-i`.
- **`sbx exec -d` NE DÉTACHE PAS** — la documentation du drapeau est falsifiée. `--help` annonce
  « Detached mode: run command in the background » ; mesuré, `-d` bloque 5 s sur `sleep 5` (6 s
  sans lui), relaie la stdout et rend le code de l'enfant (`exit 42` → `rc=42`). À signaler en
  amont.
- **`-t` sans terminal attaché JETTE la sortie**, en silence, tout en rendant le code de retour :
  `sbx exec -it <sb> echo hello </dev/null >fichier` laisse le fichier VIDE, là où la même commande
  sans `-t` écrit `hello` ; `sbx exec -it <sb> sh -c 'exit 42'` rend bien `rc=42`. C'est ce qui rend
  la règle « `-it` seulement si `Deps.IsTTY` » obligatoire et non préférentielle.
- **Une sandbox arrêtée est redémarrée** : « If the sandbox is stopped, it is started first »,
  `sbx exec --help`. Source : le texte d'aide du binaire, **pas** une mesure du jour — le relevé du
  2026-07-28 l'avait mesuré sur v0.35.0.

### La liste des commandes (2026-07-31, `sbx --help` sur v0.35.0)

```
completion  cp  create  daemon  diagnose  exec  help  kit  login  logout  ls  policy
ports  reset  rm  run  secret  setup  ssh  stop  template  tui  version
```

⚠️ **Correction du relevé du 2026-07-28**, qui affirmait « `sbx-devbox` ajoute `stop`, `template
save`, `secret`, `inspect`, `login` » : **`stop`, `secret`, `login` et `template` sont dans le `sbx`
de BASE**, pas ajoutés par `sbx-devbox`. Et `inspect` n'est pas une commande de premier niveau du
tout : c'est `sbx policy inspect`.

**`sbx start` n'apparaît toujours dans aucun relevé** — c'est la raison pour laquelle la
remédiation de `internal/sbx/ls.go` ne le propose pas (tenue par
`TestVerifieEnMarcheNeSuggereQueDesCommandesAttestees`). C'est `sbx exec` qui redémarre.

**`sbx version`** rend `sbx version: v0.35.0 <sha>` ; **il n'existe pas de drapeau `--version`**.
v0.35.0 annonce **v0.37.1** comme disponible : tout ce qui suit est à re-relever au passage.

### Les commandes que den utilise

```
sbx create [flags] AGENT PATH [PATH...]
  flags : --clone --cpus int --kit strings (répétable) -m/--memory --name --profile -q -t/--template
  AGENT ∈ {claude, codex, copilot, cursor, docker-agent, droid, gemini, kiro, opencode, shell}
  PATH accepte le suffixe `:ro`
  --name : « letters, numbers, hyphens, periods, plus signs and minus signs only »
  ⚠️ AUCUN --label. La décision verrouillée n°10 (état par labels) est FALSIFIÉE → identité par le nom.
  échec sur template inconnu (2026-07-31) :
    ERROR: request failed: 403 Forbidden: pull failed for image "<image>"   (stderr, exit 1, aucun résidu)

sbx ls [--json] [-q]
  {"sandboxes":[{"name","id","agent","status","ports":[…],"workspaces":["/p","/p:ro"]}]}
  ⚠️ aucun champ de date/création → la colonne « âge » du §5 est INFAISABLE.
  `ports` (2026-07-31) : {"host_ip","host_port","sandbox_port","protocol"}
    — présent UNIQUEMENT si status vaut "running". Une sandbox arrêtée ne porte pas la clé du tout,
      alors que ses publications reviennent à la reprise. Une lecture sur sandbox arrêtée dit donc
      « ne publie rien » d'une VM qui publie : c'est un piège, pas une réponse (#16).
  `workspaces` porte le suffixe `:ro` d'un mount read-only TEL QUEL (mesuré 2026-08-10, sbx
    v0.37.1, sandbox jetable créée avec `<path>:ro` puis détruite) :
      "workspaces": ["<…>/probe-rw", "<…>/probe-ro:ro"]
    C'est ce qui permet à spawn.reportUnmountedMounts de voir un `ro:` retourné sans rien
    enregistrer côté hôte (#56, spec 2026-08-10-mounts-drift-design.md).
  `workspaces` est NORMALISÉ LEXICALEMENT par sbx (mesuré 2026-08-10, trois sondes jetables,
    détruites) :
      création `sbx create --name den-probe-a shell /Users/…/den-probe-a/` (slash final)
        → `ls --json` rend "/Users/…/den-probe-a" — le slash final est RETIRÉ.
      création `sbx create --name den-probe-b shell /tmp/den-probe-b`
        → `ls --json` rend "/tmp/den-probe-b" VERBATIM, et non "/private/tmp/den-probe-b",
          alors que `/tmp` est un lien symbolique vers `/private/tmp` sur macOS.
    Donc : la normalisation de sbx est PUREMENT LEXICALE — exactement la sémantique de
    `filepath.Clean`, et sbx ne résout AUCUN lien symbolique. Le côté VM est toujours déjà
    propre ; le côté configuration est le seul à pouvoir être sale. C'est ce qui ferme le
    choix de spawn.normalizeWorkspace : `filepath.Clean` des deux côtés est la normalisation
    correcte ET complète, et `filepath.EvalSymlinks` serait une divergence, pas un
    raffinement (#56).
  `workspaces` SURVIT à l'arrêt, contrairement à `ports` (mesuré 2026-08-10, même sonde) :
      `sbx stop den-probe-a` puis `ls --json`
        → {"status":"stopped","workspaces":["/Users/…/den-probe-a"]}
    La clé est présente et complète sur une sandbox arrêtée. Le piège de `ports` ci-dessus ne
    se transpose donc PAS : ni spawn.reportUnmountedMounts ni ses trois sœurs n'ont besoin
    d'un garde « sandbox arrêtée ». Mesuré, et non déduit du fait que `ports` l'est.
  ⚠️ Cette machine porte sbx v0.37.1, alors que tout le reste du présent relevé date de v0.35.0.
    Le reste est à re-mesurer.

sbx exec [flags] SANDBOX COMMAND [ARG...]
  flags utiles : -i/--interactive -t/--tty -d/--detach -w/--workdir -u/--user
  REDÉMARRE une sandbox arrêtée (« Sandbox <name> started successfully » sur stderr, puis la
  commande tourne). Mesuré à 1,4 s pour un `sbx exec <name> true`. `sbx ports` ne le fait PAS —
  c'est cette asymétrie qui a produit l'issue #16.

sbx ports SANDBOX [--publish spec] [--unpublish spec] [--json]
  spec  : [[HOST_IP:]HOST_PORT:]SANDBOX_PORT[/PROTOCOL]   (unpublish : HOST_IP optionnel, HOST_PORT requis)
  HOST_PORT omis → port hôte éphémère alloué automatiquement.
  HOST_IP omis (2026-07-31) → loopback, ÉTENDU selon le protocole : tcp/udp lient à la fois
    127.0.0.1 ET ::1 (ou 127.0.0.1 seul si la sandbox est IPv4-only) ; tcp4/udp4 → 127.0.0.1 seul ;
    tcp6/udp6 → ::1 seul. Protocoles : tcp, tcp4, tcp6, udp, udp4, udp6 ; défaut tcp.
    → écrire l'adresse EN ENTIER (ce que den fait) est ce qui garde ::1 hors du bind.
  sans drapeau → liste ; `--json` (2026-07-31) rend un TABLEAU JSON NU (pas un objet), même schéma
    que le champ `ports` ci-dessus. Sur sandbox arrêtée : « No published ports ».
  N'EST PAS IDEMPOTENT (2026-07-31). Deux 409 sous le même code, de causes différentes :
    409 Conflict: port <IP>:<P>/tcp is already published        ← cette sandbox publie déjà ce port hôte
    409 Conflict: … failed to bind host port <IP>:<P>: address already in use   ← un process de l'hôte
  Le premier est clé sur le PORT HÔTE SEUL, quel que soit le port conteneur visé : `9500:8080`
    publié, `--publish 9500:9999` échoue aussi ; `--publish 9503:8080` passe (même conteneur, autre
    port hôte : autorisé).
  Sur sandbox ARRÊTÉE : 500 Internal Server Error: … no container endpoint with IP address found.

sbx policy check network [--sandbox SANDBOX] [--json] [--verbose] TARGET
  « Bare hosts and IP literals are evaluated with port 443. »
  → une entrée egress nue est cohérente entre le mixin et le check, sans normalisation.
  Répond AUSSI sur une sandbox arrêtée (2026-07-31).

sbx rm --force NAME
```

### Les commandes relevées le 2026-07-31

`sbx template ls --json` **est utilisée par den v1** depuis #8 (`den build`, et le contrôle d'image
du spawn au §11). `template save` l'est aussi, et est attestée depuis le **2026-08-03** — voir
« Les commandes relevées le 2026-08-03 » plus bas. `template rm` et `template load` ne le sont
toujours pas : den ne les appelle pas.

```
sbx template {ls,save,rm,load}
  sbx template ls [--json]
  { "images": [ { "id", "repository", "tag", "flavor", "created_at", "size" }, … ] }
  ex. { "id":"11a2e5ef4234", "repository":"docker.io/library/devx", "tag":"v1",
        "flavor":"claude-code-docker", "created_at":"2026-07-27T06:44:57Z", "size":6477492753 }
  ⚠️ `repository` et `tag` sont SÉPARÉS, là où une stack écrit `image: docker.io/library/devx:v1`
     d'un seul tenant, et rien n'oblige à qualifier — `devx:v1` doit s'apparier à
     `docker.io/library/devx` + `v1`. C'était la seule vraie question de design restante de #8 :
     tranchée par `sbx.NormalizeImageRef`, qui qualifie les DEUX côtés avant comparaison (règles et
     cas limites au §6).

sbx policy {allow,deny,init,inspect,log,ls,profile,reset}   (en plus de `check`)
  sbx policy ls [--wide]
  sbx policy inspect <policy-id|policy-name|rule-id|rule-name>  → chaque ressource et sa décision

sbx ssh setup
  provisionne ~/.ssh/config avec un bloc `Host *.sbx` routé par sandboxd — c'est SSH *vers* la
  sandbox, sans rapport avec le tunnel hôte du §8. NON EXÉCUTÉ au smoke : ça écrit dans le vrai
  fichier de l'utilisateur.
```

### Les commandes relevées le 2026-08-03 (smoke réel n°3, v0.35.0 inchangée)

Ce sont les **deux maillons du milieu** de la séquence de build du §6 : ceux qui produisent
réellement l'image, et qui n'avaient jamais touché un `sbx` réel avant ce relevé (issue #31). Relevé
complet : `docs/superpowers/handoffs/2026-08-03-smoke-reel-3.md`.

`sbx version` rend **v0.35.0**, la version même contre laquelle tout le §14.0 a été attesté : le
re-relevé annoncé plus haut n'est **pas** dû, la v0.37.1 n'est toujours pas installée.

> ⚠️ **PÉRIMÉ depuis le 2026-08-10.** `sbx version` rend maintenant
> `sbx version: v0.37.1 2d4f32448c7a94d7fa525517dfca21aa36599829` : la v0.37.1 EST installée, et le
> re-relevé annoncé plus haut EST dû. Voir « Le relevé du 2026-08-10 » plus bas — il dit ce que ce
> smoke-là a réellement retouché, et **rien d'autre du §14.0 n'est ré-attesté**.

```
sbx stop SANDBOX [SANDBOX...]
  positionnel, variadique. « Sandbox 'NAME' stopped », sortie 0.
  ⚠️ REJOUÉE sur une sandbox DÉJÀ arrêtée : même message, même sortie 0 — c'est un NO-OP, pas un
     refus. C'est la propriété dont dépend le teardown différé de `buildOne` : le chemin heureux
     arrête la VM avant de sauver puis `rm --force` par-dessus, un build interrompu atteint le
     teardown SANS `stop` préalable, et les deux ordres sont légaux.

sbx template save SANDBOX TAG [-o/--output FICHIER]
  POSITIONNEL. Pas de `--tag`, pas de `-t`. C'est l'argv que `internal/build/execute.go` envoie.
  Succès : « Snapshotting image in sandbox … » puis « Save complete. To use the image as a
  template: sbx run -t docker.io/library/<TAG qualifié> AGENT [WORKSPACE] », sortie 0.

  ⚠️ REFUSE une sandbox qui TOURNE, et refuse en posant D'ABORD une question interactive :
       « Sandbox NAME is running and must be stopped before saving. Stop it now? (y/N): »
       puis, sans tty, « ERROR: cannot save a running sandbox; stop it first with: sbx stop NAME »
     Le `stop` avant `save` du §6 n'est donc pas de l'hygiène, il est OBLIGATOIRE — et plus
     fortement que supposé : sans lui den ne récolterait pas une erreur propre, il injecterait une
     invite `(y/N)` dans un build non interactif, dont le sink n'est pas un tty (`sbx.Exec.Stream`).

  ⚠️ RÉFÉRENCE NUE : `save … den-smoke3:v1` produit `docker.io/library/den-smoke3:v1` dans
     `template ls --json`. Les deux côtés complètent IDENTIQUEMENT — l'hypothèse de
     `sbx.NormalizeImageRef` est mesurée, pas extrapolée. C'était la mesure dont dépendait la boucle
     ouverte de #8 : si les complétions avaient divergé, `den build` aurait réussi et `den spawn`
     aurait réclamé un build à perpétuité. Le `flavor` est HÉRITÉ de la base, pas recalculé.

  ⚠️ ÉCRASEMENT d'un `TAG` existant : succès SILENCIEUX, sortie 0, et un `id` NEUF. Pas de refus,
     pas d'invite, pas de `--force` propre. L'ancien `id` disparaît de `template ls --json` — il n'y
     figure pas comme « untagged », il n'y figure plus. Conséquence pour den : `--force` n'a PAS
     besoin d'un `template rm` préalable, `execute.go` a raison de sauver par-dessus le nom. Le prix
     est un jeu de calques orphelins par reconstruction, que `sbx template ls` ne montre pas et que
     den ne se propose pas de réclamer.

sbx policy check network TARGET
  TARGET est OBLIGATOIRE : sans lui, « ERROR: accepts 1 arg(s), received 0 ». Le relevé du
  2026-07-28 plus haut l'écrivait déjà HORS crochets — il était juste, ceci ne le corrige pas, ça
  mesure le message exact du refus.
```

**Schéma de kit réel** (relevé sur `sbx-devbox/lib/*/spec.yaml`, pas sur la documentation ;
**renommé par sbx entre v0.35.0 et v0.38.0**, relevé du 2026-08-10) :

```yaml
schemaVersion: 2
kind: mixin
name: <identifiant>
version: 1.0.0
description: >-
  ...
permissions:
  network:
    allow: ["api.anthropic.com:443", "github.com:22"]
environment:
  variables:
    CLAUDE_CONFIG_DIR: /chemin/hote
setup:
  startup:
    - command: ["bash", "-c", "..."]
```

Les clés sont `permissions.network.allow`, `environment.variables` et `setup.startup` — **pas**
`network.allow` ni `env`, que ce spec écrivait avant la tâche 1, et **plus** `caps.network.allow`
ni `commands.startup`, que sbx ≤ v0.35 lisait. Le renommage est un **refus dur** au `sbx create`
(`field caps not found in type spec.permissionsBlockV2`), pas une dépréciation silencieuse : un
`den spawn` meurt avant toute création de VM. `schemaVersion: 1` accepte encore l'ancienne
orthographe — den n'y retombe pas, il n'écrit que la nouvelle. Détail du relevé en §14.0.

**Conséquence côté relecture** : `agent.ReadMixin` décode les DEUX orthographes. Les mixins déjà
posés sous `cache/mixins/` portent l'ancienne, `cache/` n'est jamais purgé par den, et chacun est
la référence de drift d'une sandbox encore vivante. Ne lire que la nouvelle ferait rapporter un
egress vidé et une fraîcheur disparue à **chaque** attach — un faux avertissement permanent, ce
qui apprend à l'utilisateur à ne plus lire le rapport de drift. Expand–contract : l'ancienne clé
part quand le dernier mixin écrit avant le 2026-08-10 a disparu, ce que rien ici ne sait dater.

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

**Format du journal du dispatcher** (`/var/log/sbx-kit-startup.log`, relevé le **2026-07-31** sur
v0.35.0 — c'est ce relevé qui rend la porte du §9.1 observable, cf. `internal/agent/gate.go`) :

```
=== dispatcher run 2026-07-31T15:34:24Z ===
> /etc/durable-startup.d/001-startup-claude/000-cmd.sh
ok /etc/durable-startup.d/001-startup-claude/000-cmd.sh
> /etc/durable-startup.d/002-startup-den-alpha/000-cmd.sh
agent claude: up to date
ok /etc/durable-startup.d/002-startup-den-alpha/000-cmd.sh
=== dispatcher complete ===
```

- une ligne `> <chemin>` par commande, puis **`ok <chemin>`** ou **`fail <chemin> exit=<n>`** ; entre
  les deux, la sortie de la commande elle-même (texte arbitraire — seules les lignes `ok `/`fail `
  sont un verdict) ;
- le dossier du kit de den est **`<NNN>-startup-<nom du kit>`**, donc `002-startup-den-alpha` pour la
  sandbox `alpha`. Le nom du kit est celui que den génère (`agent.MixinName` : `den-` + le nom de
  sandbox dont les `.` sont aplatis en `-`), vérifié sur une sandbox à worktree :
  `alpha.wt2` → `002-startup-den-alpha-wt2`. **Le préfixe numérique `002-` n'est pas documenté** et
  n'est pas une position que den choisit : la reconnaissance se fait sur le suffixe du segment de
  chemin, jamais sur ce préfixe ;
- **le fichier s'ACCUMULE** : un redémarrage ajoute un nouveau bloc `=== dispatcher run … ===`. Le
  dispatcher **rejoue** donc bien ses startup commands quand une sandbox arrêtée est relancée par
  `sbx exec` — c'est la réserve n°6 laissée ouverte par le smoke réel n°2, mesurée ici. Seul le
  **dernier** bloc décrit la sandbox telle qu'elle est.

### Le relevé du 2026-08-10 (smoke réel n°4, **v0.37.1** — issue #57)

**La version a changé.** `sbx version` rend
`sbx version: v0.37.1 2d4f32448c7a94d7fa525517dfca21aa36599829`. Tout le reste du §14.0 reste
attesté contre **v0.35.0** et n'est **pas** ré-attesté ici : la règle du §14.0 vaut telle quelle —
ce qui n'est pas dans un relevé n'est pas attesté. Ce smoke a exercé sans divergence visible
`create` (avec `--name` et `--template`), `exec`, `rm --force`, `ls --json` et
`template ls --json`. Il ne dit **rien** des autres commandes sur v0.37.1. Le re-relevé complet
annoncé plus bas reste dû.

**La question mesurée** (une seule) : que contient `/home/agent/.ssh` dans une sandbox fraîchement
créée ? C'est la prémisse dont dépend `ssh.mode: mount` — la phase de lien est fail-closed et refuse
un répertoire **non vide** à la cible (§10.1), donc une image de base qui sème un `known_hosts` ou
un `config` par défaut ferait mourir au boot **chaque** spawn `ssh.mode: mount`.

**Réponse : `/home/agent/.ssh` est ABSENT.** Mesuré par `ls -A /home/agent` dans une sandbox neuve,
sur les quatre images disponibles sur la machine de mesure :

```
docker/sandbox-templates:shell-docker         (stock)   → .ssh absent
docker/sandbox-templates:claude-code-docker   (stock)   → .ssh absent
devx:v1                                       (dérivée) → .ssh absent
godev:v1                                      (dérivée) → .ssh absent
```

Le `$HOME` du dispatcher et celui de `sbx exec` sont le même : `id` rend
`uid=1000(agent) gid=1000(agent) groups=1000(agent),27(sudo),1001(docker)`, et `/home/agent` est
`drwxr-xr-x agent agent` — donc le `rmdir` de la phase de lien a bien le droit d'écriture sur le
parent, ce qui est le vrai verrou et pas seulement la vacuité du répertoire.

**Vérifié de bout en bout, pas déduit.** Un `den spawn --detach` réel, sur un `DEN_HOME` jetable
déclarant `ssh:{mode: mount, dir: <jetable>}` et l'image stock `shell-docker`, a démarré. Le journal
du dispatcher :

```
> /etc/durable-startup.d/002-startup-den-probe/000-cmd.sh
den mounts: linking 1 mount(s)
den mounts: /home/agent/.ssh -> /private/tmp/.../denhome/ssh
ok /etc/durable-startup.d/002-startup-den-probe/000-cmd.sh
```

et dans la VM, `/home/agent/.ssh` est un symlink `agent:agent` vers le chemin hôte. C'est la phase
de lien **réelle**, sous le `su … agent` du dispatcher, pas un rejeu à la main.

**A11 est re-mesurée par ce même smoke, dans les deux sens.** Le §14.1 la donne **fermée depuis le
2026-07-29** ; c'est le commentaire de `internal/agent/links.go` qui la disait encore « still
unverified », et c'est lui qui était périmé — corrigé le 2026-08-10. Ici : `den_link` a passé son test
`[ ! -e "$src" ]` sur le chemin **hôte** du répertoire monté, et un fichier créé côté hôte dans ce
répertoire est ressorti par `ls -A /home/agent/.ssh` côté VM. `sbx` monte donc bien un workspace au
**même chemin absolu** dans la VM. Le refus « source absente » de `linkFunc` reste néanmoins en
place : il garde le cas d'un chemin hôte qui disparaît, qu'A11 ne couvre pas.

**Ce que ce relevé ferme.** L'inquiétude de l'issue #57 s'évapore : aucune des deux branches de
travail qu'elle prévoyait (un message nommant le cas « l'image a posé des fichiers », et la question
de savoir si `ssh.mode: mount` pourrait traiter un répertoire de défauts d'image comme vide) n'a
lieu d'être — il n'y a pas de répertoire du tout. Personne n'a à trancher l'affaiblissement du
fail-closed.

### Les sondes du 2026-08-10 (trois questions de `workspaces`, v0.37.1 — #56)

Trois sondes jetables, séparées du smoke n°4 ci-dessus et lancées le même jour, sur la même
v0.37.1. Elles répondent aux trois questions que la conception de la dérive des mounts
(`2026-08-10-mounts-drift-design.md`) portait encore comme **non mesurées**. Le détail chiffré est
écrit à sa place, dans l'entrée `sbx ls` du relevé ci-dessus ; en résumé :

1. **sbx normalise les chemins de workspace**, lexicalement : un slash final donné à `create` ne
   ressort pas de `ls --json`. Le côté VM est donc toujours déjà propre, et le côté configuration
   est le seul à pouvoir être sale — ce qui confirme la direction du correctif : normaliser à la
   comparaison, sur les deux côtés.
2. **sbx ne résout PAS les liens symboliques** : `/tmp/x` ressort `/tmp/x` et non
   `/private/tmp/x`. Sa normalisation a donc exactement la sémantique de `filepath.Clean`.
   `filepath.EvalSymlinks` côté den divergerait de sbx au lieu de l'affiner : c'est désormais
   **mesuré**, et non plus une simple prudence.
3. **`workspaces` survit à `stop`** : la clé reste présente et complète sur une sandbox arrêtée.
   Le piège de `ports` ne se transpose pas, donc aucun garde « sandbox arrêtée » n'est requis —
   « mesuré, ne s'applique pas », et non « non mesuré, reporté ».

Ces sondes ont exercé `create`, `ls --json`, `stop` et `rm --force` sur v0.37.1 ; elles ne disent
rien des autres commandes. Les quatre artefacts ont été détruits.

### Questions ouvertes et risques restants

- **Surface `sbx` relevée le 2026-07-28 puis complétée le 2026-07-31** (v0.35.0), ci-dessus :
  `policy check network [--sandbox S] [--json] TARGET` confirmé **contre un `sbx` réel** ; `--label`
  **n'existe pas** → identité par le nom. v0.35.0 annonce v0.37.1 : **tout le §14.0 est à re-relever
  au passage**, pas à extrapoler. **Le 2026-08-10 la v0.37.1 est installée** (relevé ci-dessus) :
  le re-relevé n'est plus « annoncé », il est **dû**, et il dépasse la portée du smoke n°4 qui n'a
  exercé que `create`, `exec`, `rm --force`, `ls --json` et `template ls --json`.
- **Portée de la policy egress d'un nest (F5) — la phrase juste.** L'`egress:` d'un nest est un
  **élargissement** de la policy de la machine, **jamais un rétrécissement**. den ne peut pas rendre
  une sandbox *moins* connectée que la `local-policy` de l'hôte ne l'autorise déjà (197 règles sur
  la machine de mesure, dont `fs-read-allow-all` et `fs-write-allow-all`, et beaucoup de jokers :
  `**.github.com:443`, `**.amazonaws.com:443`, `**.googleapis.com:443`, `**.docker.io:443`).

  **Mais elle est réellement appliquée, et scopée à la sandbox** — mesuré dans les deux sens le
  2026-07-31, ce que le smoke n°1 avait conclu trop vite. Un même hôte, deux sandboxes, deux
  verdicts, décidés par ce que le nest a déclaré : `example.com` est **refusé** pour un nest qui ne
  le déclare pas (`"allowed": false, "deny_kind": "implicit"`, exit 1) et **autorisé** pour celui
  qui le déclare (exit 0). Ce que `egress:` achète vraiment, c'est donc l'accès à ce qui est
  **en dehors** de la baseline — une base de projet sur `10.22.11.54:27017`, un hôte interne — et
  cette part-là est bel et bien enforced.

  Corollaire à ne pas reperdre : `sbx policy inspect` **n'est pas un miroir** du mixin de den. La
  policy de kit observée portait **quatre** allows là où le mixin en déclarait trois (`openrouter.ai`
  ajouté par sbx). Aucune détection de dérive ne doit se construire sur cette différence.
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
2026-07-29** (tâche 18), **A11 le 2026-07-29** (revue finale). Ces affirmations étaient
**invérifiables contre un `sbx` réel** : il n'était pas installé sur la machine de développement.
Elles ne sont **pas** des bugs connus — ce sont les endroits où la suite ne prouve rien.

> ### ✅ Ce que les smokes réels ont FERMÉ (2026-07-31)
>
> Le tableau ci-dessous est conservé **tel qu'il a été écrit**, parce qu'il documente le raisonnement
> qui a rendu la plupart de ces hypothèses non bloquantes. Ce qui suit est le verdict.
>
> | # | Verdict | Preuve |
> |---|---|---|
> | **A1** | **fermée** | `policy check network` sort en **1** sur un hôte refusé (`example.com`), en 0 sur un hôte autorisé |
> | **A2** | **fermée** | Le verdict part bien sur **stdout** ; stderr est vide |
> | **A3** | **fermée** | stdout porte le JSON comme **première et unique** valeur : ni bannière, ni préambule. (La bannière de mise à jour apparaît dans `sbx ls` nu, jamais sous `--json`.) |
> | **A4** | **fermée — c'était le trou le plus lourd de l'inventaire** | L'argv exact `sbx policy check network --sandbox S --json TARGET` fonctionne, le champ s'appelle bien `allowed`, le code de sortie est 1 sur refus. Mesuré, pas doublé |
> | **A8** | **fermée, et au-delà** | `policy check` répond **aussi sur une sandbox arrêtée** : la settle-loop de den a tourné jusqu'au bout contre une VM à l'arrêt |
> | **A11** | **fermée, deux fois** | Le chemin hôte EST le chemin in-VM : un fichier marqueur écrit sur l'hôte avant le spawn est lisible dans la VM à `$CLAUDE_CONFIG_DIR`, pour le profil par défaut comme pour celui sélectionné par `--agent`. Et `sbx exec -w <chemin hôte>` dépose bien le shell dans le bon dossier (pty réel, `pwd` = premier workspace de `sbx ls --json`) |
> | **ordre des `--kit`** | **fermée** | `/var/log/sbx-kit-startup.log` : `001-startup-claude`, `001-startup-shell`, puis `002-startup-den-<sandbox>` **en dernier**. sbx applique bien les `--kit` dans l'ordre de la ligne de commande ; l'invariant « le mixin en dernier » tient |
>
> **Non fermé, et pourquoi ce n'est pas un oubli.** **A5** (`ctx` honoré), **A6** (`policy check`
> répond vite), **A7** (pas de réponse transitoire) et **A9** (`allowed:false` jamais rendu pour une
> raison étrangère à la policy) portent toutes sur ce que den fait **quand `sbx` se comporte mal**.
> Aucune n'est atteignable **en usage normal** : il faut *provoquer* la panne — une annulation en
> pleine passe (`^C` pendant `waiting for network policy`), un nest à ~20 hôtes egress lents pour
> voir si le budget de 60 s est réellement respecté, un `policy check` contre une sandbox détruite en
> cours de boucle. Utiliser den correctement ne les traverse jamais. Elles restent donc ouvertes par
> **construction du banc**, pas par négligence — et la colonne « den y survit » du tableau explique
> pourquoi aucune n'est bloquante : leur conséquence a été neutralisée en tâche 11 plutôt qu'attendue.
>
> **A10** reste **partiellement** ouverte au même titre : le socket est bien présent dans la VM
> (`SSH_AUTH_SOCK=/run/ssh-agent.sock`), mais le `git push` de bout en bout sur un remote SSH n'a pas
> été rejoué au smoke n°2 (il l'avait été au n°1 au niveau des clés listées).
>
> Deux autres points du tableau ci-dessous, à lire avec la même prudence :
> - **la liste blanche de statut** : les seuls statuts que `sbx ls --json` a produits sont `running`
>   et `stopped`. Aucune valeur transitoire n'a jamais été observée — mais rien n'oblige une à
>   apparaître : c'est un **point de donnée**, pas une fermeture ;
> - **la borne de drainage de 2 s** : `den spawn --detach` rend la main en 6 à 7,6 s, jamais à une
>   valeur épinglée sur 2,0 s, donc la borne ne s'est jamais déclenchée. Ni confirmée ni falsifiée.

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
| A10 | `sbx` **propage `SSH_AUTH_SOCK` de son propre environnement jusque dans la microVM** — c'est tout ce sur quoi repose `ssh.mode: agent-forward`, qui est le **défaut** | Un `git push` sur un remote SSH depuis la VM échoue en `Permission denied (publickey)` alors que `den doctor` affiche `[ok] ssh.mode agent-forward, SSH_AUTH_SOCK=… (N identities)` sur l'hôte — c'est-à-dire une sonde hôte qui a bel et bien **vu des clés**. Un socket présent devant un agent vide ou mort ne falsifie plus rien : c'est désormais un `[warn]`, pas un `[ok]` (§10) | **Partiellement.** Ce que den contrôle est prouvé : le process `sbx` hérite bien de l'environnement de den (`cmd.Env` laissé nil, tenu par `TestExec{Run,Attach}TransmetLEnvironnementDeDen`), et `den doctor` **avertit** dans les trois états où l'agent hôte ne donnerait rien — variable absente ou vide, agent joignable mais sans identité, agent injoignable (§10) —, `den spawn` faisant le même constat sur stderr. Ce que den ne peut pas contrôler — le saut hôte → microVM — n'a **aucun** substitut : si l'hypothèse est fausse, le repli est `ssh.mode: mount`, qui lui est testé |
| A11 | `sbx` **monte chaque workspace au MÊME chemin absolu dans la VM que sur l'hôte** : le chemin hôte d'un workspace est aussi son chemin in-VM. C'est ce qui rend légitimes (1) la substitution de `{config_dir}` par un chemin **hôte** dans `CLAUDE_CONFIG_DIR` (`internal/nest/resolve.go`, `jetonConfigDir`) et (2) le `-w <chemin hôte>` de **toutes** les attaches (`sbx exec -it -w …`, `den spawn` comme `den sh`) | **Deux symptômes sans lien apparent, et aucun message d'erreur.** (1) L'agent redemande `/login` **à chaque spawn** : `CLAUDE_CONFIG_DIR` pointe un chemin qui n'existe pas dans la VM, l'agent y crée un profil neuf — c'est précisément ce que `config_dir` existe pour éviter, et rien ne le signale. (2) `sbx exec -w <chemin hôte>` échoue, ou dépose le shell ailleurs que dans le code. Le smoke tranche en une ligne : `sbx exec <sandbox> pwd` après un `den spawn`, comparé au premier workspace de `sbx ls --json` | **NON.** Aucun repli, aucune neutralisation : den n'a **aucun** moyen d'apprendre le chemin in-VM sans `sbx`. Même espèce qu'A10 — comportement d'**exécution de la microVM**, pour lequel « vert contre `sbx.Fake` » ne veut rien dire. Si elle est fausse, il faudra une source de vérité pour le chemin in-VM (sonde `sbx exec … pwd`, ou un champ de `sbx ls --json`) |

### Hypothèses assumées de den lui-même

Celles-ci ne portent pas sur `sbx` mais sur des choix de den, tous **délibérés**. Elles sont
écrites ici pour qu'aucune ne passe pour un oubli.

- **La liste blanche de statut `{"running"}`** (tâche 14, `internal/sbx/ls.go`) : den ne considère
  vivante qu'une sandbox dont le statut est exactement `running`. Un statut **transitoire**
  (`starting`, `booting`, `resuming`…) serait donc traité comme « absente », et `den spawn`
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
  un `den spawn` qui ne rend la main qu'après exactement 2 s alors que `sbx` a répondu tout de
  suite (signe que la borne a bien servi).
- **`ssh.mode: agent-forward` n'ajoute AUCUN argument** — vérifié dans `internal/spawn/spawn.go` :
  seul le mode `mount` produit un effet (un workspace en plus, et le contrôle d'existence de
  `ssh.dir`). `agent-forward`, qui est pourtant le **défaut**, et `none` restent indiscernables
  **dans l'argv** : la seule chose qui les distingue est l'avertissement d'agent vide, que den
  n'émet qu'en `agent-forward` (§10) et qui ne change rien à ce que `sbx` reçoit. Le forwarding est
  donc entièrement à la charge de `sbx`. **C'est la tâche 18**, pas une régression.
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

  **FALSIFIÉE le 2026-07-31, et dans les deux moitiés.** Le vrai refus de `sbx` n'est pas un
  « not found » :

  ```
  $ sbx create --name X --template denghost:v1 shell <repo>
  ERROR: request failed: 403 Forbidden: pull failed for image "denghost:v1"      (stderr, exit 1)
  ```

  sbx traite un template inconnu comme un **pull de registre** sur un nom non qualifié, donc le
  message que l'utilisateur voit parle d'**autorisation**, pas de build manquant. Deux conséquences
  pour #8 : (1) den **ne peut pas** filtrer sur « not found » pour décider s'il doit suggérer
  `den build` ; (2) il n'a plus à essayer — **`sbx template ls [--json]` existe** (§14.0), donc
  contrôler l'image **avant** `sbx create` est possible sans dépendance à un runtime de conteneurs,
  et c'est le seul moyen honnête de dire « lance `den build <stack>` » plutôt que de relayer un 403.
  Aucune sandbox résiduelle n'est laissée par ce refus (`sbx ls --json` → `{"sandboxes":[]}`).

  Les **deux critiques de l'enveloppe de den** ci-dessus, elles, sont confirmées contre un binaire
  réel : le message ne dit pas quoi faire, et il incruste l'argv complet de `sbx create` avant d'en
  venir à la cause.

  **FERMÉE par #8** (`internal/spawn/spawn.go`, `checkStackImage`) : den lit
  `sbx template ls --json` **avant** `sbx create` et refuse en nommant le remède du §11 — « lance
  `den build <stack>` ». Le contrôle est placé à l'étape 2quater de la séquence, ce qui a fait
  **remonter la lecture `sbx ls --json`** (le verdict créer-ou-attacher) au-dessus des worktrees :
  refuser plus bas aurait laissé un worktree git par dépôt derrière soi. Trois silences délibérés,
  chacun évitant un refus que den ne saurait justifier :

  - une stack **sans `provision.steps`** n'est pas contrôlée du tout. Son `image:` peut nommer une
    image de registre que `sbx` sait tirer, et « lance `den build` » sur une stack que den ne sait
    pas construire n'est pas un conseil mais une deuxième erreur ;
  - une `image:` **épinglée par digest** n'est pas contrôlée non plus (`sbx.IsDigestRef`) :
    l'inventaire ne rapporte aucun digest, donc il ne peut ni confirmer ni infirmer l'épingle, et
    lire son silence comme « absente » refuserait un spawn sur une image présente ;
  - un `sbx template ls` **en échec** est fail-open. Le contrôle améliore un message, il ne garde
    rien : `sbx` refuse toujours le create de lui-même si l'image manque vraiment, donc un
    diagnostic en panne ne doit pas interdire un spawn.
