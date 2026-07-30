# HANDOFF — Plan 2 (Spawn), reprise en cours de tâche 12

**Date :** 2026-07-28 · **Branche :** `main` · **HEAD :** `35354c4` · **Rien n'est poussé.**

---

## 1. Ta première action

Lis **`.superpowers/sdd/2026-07-28-den-plan2-spawn/progress.md`**. C'est le registre de la
session : il porte chaque arbitrage, chaque mesure et chaque dette, dans l'ordre. Ce handoff n'en
est que la porte d'entrée — **en cas de contradiction, le registre fait foi**, et `git log` fait foi
sur le registre.

Le plan est dans `docs/superpowers/plans/2026-07-28-den-plan2-spawn.md`. Les briefs par tâche sont
déjà extraits dans `.superpowers/sdd/2026-07-28-den-plan2-spawn/task-N-brief.md` — **ne redonne
jamais le plan entier à un implémenteur**.

## 2. État exact

**Tâches 1 à 11 closes.** Suite verte : 9 paquets `ok`, `go vet` et `gofmt -l` muets.

| # | Tâche | État |
|---|---|---|
| 1–9 | spec, agent, sbx, config, nest | ✅ closes (voir registre) |
| 10 | `internal/worktree` | ✅ close — **5 fix rounds**, le maximum de la méthode |
| 11 | `policy.Settle` | ✅ close — 3 fix rounds |
| **12** | **`internal/spawn` + `den <nest>`** | **⏳ EN COURS — fix round 1/5 dispatché, pas encore atterri** |
| 13 | `den ls` | à faire (Sonnet) |
| 14 | `den sh` | à faire — **périmètre élargi, voir §4** |
| 15 | `den rm` | à faire — **périmètre très élargi, voir §4** |
| 16 | `ListNests` tolérante | à faire (Sonnet) |
| 17 | Configs hostiles | à faire (Opus max) — **périmètre élargi** |
| **18** | **`ssh.mode: agent-forward`** | **nouvelle, décidée aujourd'hui, voir §4** |

**T13, T14 et T15 modifient toutes `internal/cli/root.go` → strictement séquentielles.**

L'arbre de travail ne porte que des fichiers d'avant la session : `run.sh`, le plan 2, le handoff
ultracode (non suivis) et `HANDOFF.md` modifié.

## 3. Où en est exactement la tâche 12

Implémentée au commit `35354c4`. Revue rendue : **spec ✅ intégralement, qualité non approuvée**.

**Un fix round est parti chez `p2-impl-t12` et n'a pas encore produit de commit.** À la reprise :
vérifier `git log 35354c4..HEAD`. S'il n'y a rien, l'agent est mort avec la session — **redispatche
un implémenteur neuf** avec le brief, le rapport (`task-12-report.md`), la revue
(`task-12-review.md`) et les findings ci-dessous.

Contenu du fix round dispatché :

- **C1 (Critique)** — le settle-loop n'est verrouillé que sur le chemin `create`. Le déplacer dans
  la branche `else` laisse la suite **verte** : sur ce code, `den api` sur une sandbox vivante
  attache **sans vérifier la policy réseau**, et c'est le chemin de chaque spawn après le premier.
  Correctif : une ligne d'assertion dans `TestSpawnAttacheSansRecreerSiLaSandboxExiste`.
- **I1** — **les 5 flags de `den <nest>` sont débranchables sans faire rougir la suite**
  (`&o.X` → `new(T)` survit pour `-w`, `--detach`, `--without`, `--only`, `--agent`).
- **I2** — `--only` et `--agent` ne sont verrouillés d'**aucun** côté de la frontière cli/spawn.
- **Mineurs** : aligner le **spec §6.9** sur l'argv d'attache réel (il écrit encore les flags après
  le nom de sandbox) ; ajouter le contexte d'erreur au `create` ; asserter les effets de bord disque
  dans `TestSpawnStoppeAvantCreateSiUnRepoManque` ; verrouiller `r.DenHome` par un test à den home
  relatif.

Ce qui est **explicitement hors de ce fix round** : I3, I4 et le concern n°1 de l'implémenteur,
tous versés à la tâche 14 (§4).

## 4. Les décisions prises aujourd'hui, à ne pas re-litiger

Six décisions utilisateur ont été prises en séance. Les quatre premières sont déjà appliquées.

1. **Exécution directe sur `main`** (pas de branche, pas de worktree) → orchestration **séquentielle**,
   jamais deux implémenteurs en parallèle.
2. **Charset des noms resserré** (`^[A-Za-z0-9][A-Za-z0-9+-]*$`) + spec §2 amendé.
3. **Branche de worktree depuis la branche par défaut** (`git symbolic-ref --short
   refs/remotes/origin/HEAD`, repli sur le HEAD courant), `--no-track`. Spec l.190 réécrit, question
   ouverte l.409 supprimée.
4. **Fichiers ignorés à la suppression** : ne bloquer que les entrées ignorées **isolées** (celles
   qui ne finissent pas par `/`), après `git check-ignore` sur le chemin **sans** son slash final.
5. **CORBEILLE (structurelle) — à implémenter en T15.** `Retire` ne supprimera plus jamais : il
   renomme vers `<den_home>/trash/<horodatage>-<nest>-<repo>` puis `git worktree prune`. Motif :
   le relecteur a **prouvé que l'énumération des façons dont `git status` cache du travail ne
   converge pas**, et que le filet propre de `git worktree remove` tombe **avec** celui de `status`
   (même code) — il n'y a pas de second filet. Chiffré : ~40-60 lignes, repli **EXDEV**, politique
   de rétention (`den gc` ou purge au-delà de N jours), amendement du spec §14.
6. **AVERTIR ET ATTACHER sur dérive de configuration — à implémenter en T14.** Sur une sandbox
   vivante, rien ne réapplique le mixin : un `egress:` **rétréci** passe le settle-loop en silence.
   → comparer le mixin calculé à celui du `create`, **avertir** en nommant ce qui a changé, puis
   attacher. Écartés : refuser (casse un flux qui marchait pour un YAML anodin) et recréer
   (destruction non demandée d'une VM avec du travail en cours).
7. **TÂCHE 18 — `ssh.mode: agent-forward`.** C'est le **défaut** de `config.go` et il n'est
   implémenté **nulle part** : toute sandbox spawnée sort sans accès SSH, silencieusement. À caler
   **après T17 et après le premier smoke test réel**, le mécanisme côté `sbx` étant invérifiable ici.

## 5. Périmètres élargis en cours de route

**T14 (`den sh`) doit réconcilier la branche spawn-or-attach EN ENTIER**, comme un seul item. Trois
défauts y vivent et se corrigent au même endroit — remplacer `sbx.Existe` par un `sbx.Ls` + recherche,
qui rend d'un coup le `Workdir()` réel **et** le `Statut` :
- la dérive de configuration (décision 6 ci-dessus) — **le plus urgent des trois** ;
- `sbx.Existe` **ignore `Statut`** : une sandbox `exited` est traitée comme vivante ;
- le `-w` est recalculé depuis la config actuelle alors que la VM monte les workspaces de son
  `create` d'origine. `sbx.Sandbox.Workdir()` existe depuis T8 **exactement pour ça** et n'a
  toujours aucun consommateur.

**T15 (`den rm`) porte 21 points de dette consolidée**, listés en bloc dans le registre. Les trois
structurels : la corbeille, l'amendement du **spec §14** (« dirty » n'y veut plus dire ce que le code
fait — sans ça T15 re-dérivera la mauvaise définition et **perdra la règle payée sur 5 fix rounds**),
et la construction du `context` de `den rm` avec bornage des sondes git.

**T17** hérite de : `internal/sbx` **et** `internal/worktree` à ajouter à l'exclusion du grep
`os/exec` (les tests y lancent légitimement des processus) ; une 13ᵉ config hostile (aucun contrôle
d'existence sur `Kit`/`Kits`) ; une 14ᵉ (`worktree_layout: centrl` retombe silencieusement sur
`central`) ; `config_dir` vide qui atteint la VM ; `bin_dirs` contenant `$(...)` **exécuté** par le
bash de la VM au boot ; le plancher de version git non déclaré ; et l'inventaire A1→A9 des hypothèses
non falsifiables sur `sbx`.

## 6. Méthode — ce qui a marché, et pourquoi

Un implémenteur neuf par tâche, une revue par tâche, fix rounds en réveillant le **même**
implémenteur, puis re-relecture **scopée au diff du correctif**. Le contrôleur vérifie **lui-même**
la suite et les invariants portants avant de dispatcher, plutôt que de croire les rapports.

**La constante des douze tâches : aucun fix round n'a jamais été déclenché par du code faux.** À
chaque fois le code était juste et c'est le **test** qui ne prouvait rien, ou une garde qui manquait.
Tous les défauts trouvés passaient une suite verte.

**Le motif dominant, trouvé une douzaine de fois : une assertion verte parce qu'un TIERS rattrape
den** — `git` (3×), `os/exec` (2×), le système, la config git du poste, un double trop complaisant,
et une fois **Linux**, qui masquait une régression ne frappant que macOS.

**Ce qui a le mieux payé : demander aux relecteurs de MESURER, pas de raisonner.** Ils ont
brute-forcé 23 644 noms, monté un banc `yaml.v3` autonome hors dépôt, lu la source de `watchCtx`,
compilé leur sonde **deux fois** (contre l'ancien commit et le nouveau) pour qualifier des
régressions au lieu de constater des états, balayé 20 100 couples de paramètres, et fait leurs
propres tests de mutation. Aucun de ces résultats n'était atteignable en lisant un diff.

**Et ce qui a fermé la tâche 10, ce n'est pas un correctif de plus mais une question d'architecture** :
demander au relecteur si l'énumération convergeait, au lieu de lui demander un tour de vérification.
Sa réponse a produit la décision corbeille, qui rend inoffensifs tous les cas futurs.

À reconduire : exiger la mesure, exiger la **mutation tueuse nommée** pour chaque test, et poser la
question d'architecture quand un même motif revient trois fois.

## 7. Pièges d'orchestration

- **Collision de noms d'agents** : toujours adresser un agent par le nom **retourné au spawn**,
  jamais par celui demandé. Préfixer par `p2-`.
- **Un rapport qui parle d'une tâche non dispatchée = homonyme**, pas ton agent : vérifier `git log`
  avant toute conclusion.
- **Les implémenteurs ont eu raison contre le contrôleur quatre fois**, chaque fois sur mesure
  (`cmd.Cancel`, `GIT_DIR=` vidé vs retiré, lire `core.ignoreStat`, et une prémisse fausse sur
  `git worktree remove`). Quand un agent conteste une consigne **avec une mesure**, il a
  probablement raison.
- **Le `core.excludesfile` de cette machine est corrompu** (`/home/agent/.gitignore_global`, 5 octets
  NUL avant `.sbx`) : git y ignore **tout chemin finissant par `/`**. Neutraliser
  (`GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null`) dans toute sonde touchant `.gitignore`.

## 8. Contraintes qui n'ont pas bougé

- **`sbx` n'est pas installé.** Aucun agent ne peut vérifier quoi que ce soit contre lui. Tout rapport
  affirmant qu'un spawn fonctionne est faux.
- **Un golden ne change qu'après** que les assertions sémantiques dédiées sont vertes. Jamais de
  régénération pour faire passer un test.
- **Français** partout ; **messages de commit sans accents**.
- **Format d'erreur** : `contexte : détail`, nommant le chemin complet et les valeurs disponibles.
- **Aucune dépendance nouvelle.**
- Fichiers temporaires dans le scratchpad de session, **jamais** dans le dépôt.
