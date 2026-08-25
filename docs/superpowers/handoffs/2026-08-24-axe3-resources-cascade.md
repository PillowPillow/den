# HANDOFF — Axe 3 : `resources:` dans la cascade (issue #90)

> Pour l'agent qui prend cet axe **sans contexte de conversation**. Lis ce fichier, puis
> `CLAUDE.md`, puis le spec §14.2 et §14.0. Réponds en français ; écris le code, les commentaires et
> les messages utilisateur **en anglais**.
>
> Écrit le **2026-08-24**. Les handoffs datés ne sont jamais réécrits. L'ordre de confiance est
> **le code, puis le spec, puis `CLAUDE.md`**, les handoffs en dernier.

## ⚠️ Tu n'es pas seul — trois axes tournent EN PARALLÈLE

| Axe | Issue | Branche | Nature |
|---|---|---|---|
| 1 — gouvernance | #88 | `feat/converge-names-undeclared-sbx-state` | code |
| 2 — positionnement `sbx env` | #89 | `spec/sbx-env-positioning` | spec, aucun code |
| 3 — `resources:` dans la cascade | **#90** | `feat/resources-in-cascade` | **toi** |

**Trois règles, à lire avant d'ouvrir un fichier.**

1. **Le spec §14 est en APPEND-ONLY cette semaine.** Toute mesure contre un `sbx` réel va dans une
   **nouvelle sous-section datée** portant ton numéro d'issue (« ### Sonde du 2026-XX-XX — #90 … »).
   Tu ne modifies **jamais** une sous-section existante : l'axe 1 écrit dans le même fichier.
2. **Tu ne touches PAS `CLAUDE.md`.** Les mises à jour s'y font dans une PR d'intégration après les
   merges. Note ce que tu voudrais y voir dans la description de ta PR.
3. **`internal/sbx/fake.go` appartient à l'axe 1** — mais tes tests tournent contre lui. Voir §7.

## 1. Ta mission, en une phrase

Un nest ne peut pas dire quelle taille il fait. Donne-lui `resources:`, résolu par la cascade
existante, émis en `--cpus` / `--memory`.

**C'est l'axe sans question stratégique ouverte.** Du travail de feature ordinaire sur une cascade
qui existe déjà. Si tu prends un axe à froid, c'est celui-là.

## 2. Le constat

`sbx.CreateArgv` (`internal/sbx/argv.go`) envoie exactement :

```
create --name <n> --template <image> --kit <k>... shell <workspace>...
```

`sbx create` v0.39.0 offre en plus :

```
--cpus int            --deny-network <host>  (répétable)
-m/--memory string    -e/--env KEY=VALUE     (répétable)
--profile string      --env-file <path>      (répétable)
--clone               -p/--publish <spec>    (répétable)
--static-mcp <names>  -q/--quiet
--no-share-skills     (caché, derrière un feature gate)
```

Quatre d'entre eux — `--clone`, `--cpus`, `--profile`, `-m/--memory` — sont **attestés au spec
§14.0 depuis le 2026-07-31**. den n'en a jamais envoyé un seul, en un an.

Concrètement : chaque sandbox den reçoit le défaut de sbx, *« 50% of host memory, max 32 GiB »* et
tous les CPU de l'hôte. Un nest qui lance une suite de tests Go et un nest qui lance trois services
plus une base sont dimensionnés à l'identique.

## 3. Ce qui est MESURÉ (spec §14.2, PR #91)

- **`-m/--memory` refuse en dessous de 1 GiB, et le refus est CÔTÉ SERVEUR** — il arrive **après**
  `✓ image ready`, pas à l'analyse des drapeaux :

  ```
  $ sbx create --memory 512m --name den-probe-mem shell <dir>
     ✓ image ready
  ERROR: request failed: 400 Bad Request: invalid memory "512m": memory 512m is below the minimum of 1 GiB
  ```

  Aucun résidu : la sandbox n'est pas créée. **C'est l'argument décisif pour que den valide la
  valeur lui-même** — relayer verbatim coûte un tirage d'image avant d'échouer.
- `--cpus 0` signifie *auto* d'après l'aide de `sbx create`. Un `cpus:` absent doit donc **omettre
  le drapeau**, jamais envoyer `0` : les deux sont équivalents pour sbx aujourd'hui, mais par
  coïncidence, et den ne construit pas sur des coïncidences.
- `sbx ls --json` porte toujours exactement `{agent, id, name, status, workspaces}`, quatre
  sandboxes vérifiées. **Aucun champ de date.** La colonne « âge » du spec §5 reste **INFAISABLE** —
  c'est désormais une mesure, plus une absence de mesure. **Ne la rouvre pas.**

## 4. Le travail, dans l'ordre

### 4.1 `resources:` dans la cascade — fais ça d'abord, et peut-être ça seulement

Un bloc `resources:` résolu par la cascade existante
(`config.yaml` ← `stacks/<n>/stack.yaml` ← `nests/<n>.yaml` ← drapeaux), consommé par
`nest.Resolve` dans `*nest.Resolved`, émis par `CreateArgv` en `--cpus` / `--memory`.

```yaml
resources:
  cpus: 4
  memory: 8g
```

Points de conception, chacun à justifier par un commentaire au point de décision — c'est le style
dominant du dépôt, et du code terse détonnerait visiblement :

- **Valide `memory` toi-même.** La grammaire est celle de sbx (unités binaires : `1024m`, `8g`).
  Le §3 ci-dessus donne la raison : un refus serveur coûte un tirage d'image. Refuse avant le
  premier effet de bord.
- **Ordre de la séquence de spawn (spec §6).** Tout ce qui est refusable à partir de la seule
  configuration l'est **avant le premier effet de bord**, pour qu'un refus ne laisse jamais un
  worktree orphelin. Un `resources:` invalide se refuse donc au même endroit que les contradictions
  de drapeaux et le nom de sandbox — pas au moment du `create`.
- **La branche attach ne réapplique RIEN à une VM vivante** (spec §6). Un nest dont le `resources:`
  a changé après la création de la sandbox doit **avertir de la dérive**, comme den avertit déjà
  pour la dérive de mixin et les répertoires git manquants. Ni silence, ni recréation.
- **YAML strict** (`KnownFields(true)`) : une faute de frappe `memroy:` est une erreur de
  chargement, jamais un silence. Le spec §12 donne la raison de fond.
- `sbx.Create` est une struct dont l'argv est testé par goldens. **Les goldens s'éditent à la
  main** — vérifié le 2026-08-24 : `Taskfile.yml` n'a que `build`, `test`, `typecheck`, `lint`,
  `check`, et aucun test ne déclare de drapeau `-update`.
- `CreateArgv` **garde sa propre entrée** (lis `checkWorkspace` et le commentaire sur le filtrage
  des `--kit` vides). C'est délibéré : la fonction est exportée et prend une struct que n'importe
  qui peut remplir. Tes nouveaux champs suivent la même règle.

### 4.2 `--deny-network` à la création

Le modèle d'egress de den est fail-closed et se stabilise par une boucle (spec §7). L'aide de sbx
dit qu'un deny local *« can only narrow, never widen, egress »*, ce qui le rend sûr sous la même
doctrine. Mais deux mécaniques pour un concept **est** le mode de défaillance : tranche si ça mérite
sa place à côté de l'allowlist existante **avant** d'implémenter, et écris la réponse.

### 4.3 `--profile` (profil de gouvernance)

den a `internal/policy`. **Sonde d'abord** : personne ne sait ce qu'est réellement un profil de
gouvernance sbx. Ne décide pas avant de savoir.

## 5. Ton périmètre EXACT

**Tu possèdes, en écriture :**

- `internal/config/config.go`, `internal/config/stack.go` — le schéma
- `internal/nest/resolve.go` — la résolution de cascade
- `internal/sbx/argv.go` — `Create` et `CreateArgv` — **tu en es le seul propriétaire ce tour-ci**
- `internal/spawn/spawn.go` — l'avertissement de dérive sur la branche attach
- `internal/*/testdata/*.golden` liés à l'argv de create
- `docs/superpowers/specs/…` — **en ajout seul**, nouvelle sous-section datée #90

**Tu ne touches pas :**

- `internal/converge/**`, `internal/doctor/**`, `internal/sbx/fake.go` — **axe 1**
- `CLAUDE.md` — PR d'intégration
- toute sous-section §14 déjà écrite

## 6. Ce qui n'est délibérément PAS dans cette issue

- **`--clone`** — un clone in-container au lieu d'un bind mount est un **troisième mode de
  workspace** à côté des modes worktree et mount de den. C'est une conception, pas un drapeau.
- **`--static-mcp`** — va avec la question de gouvernance MCP, **axe 1**.
- **`-e/--env` et `--env-file`** — den injecte déjà l'environnement par le mixin généré. Les
  déplacer vers des drapeaux est un refactor sans gain visible. Passe, sauf si une raison apparaît.
- **`-p/--publish` à la création** — violerait « les ports ne se publient qu'à la demande »,
  verrouillé par `internal/ports/hermeticity_test.go`, qui échoue avec un message de graphe
  d'imports si tu casses l'invariant.
- **`--no-share-skills`** — mesuré **inerte** (feature gate éteint ; deux sandboxes créées avec et
  sans sont indiscernables). **Reporté** par décision explicite jusqu'à ce que le gate s'ouvre. Ce
  n'est pas passé de l'axe 1 à toi : c'est abandonné pour ce tour. Livrer un drapeau sans effet
  observable brouillerait la prémisse de ton issue, qui est « des drapeaux à valeur visible ».

## 7. ⚠️ Le point de collision avec l'axe 1 : `internal/sbx/fake.go`

`sbx.Fake` est un **fichier de production** que `policy`, `cli` et `agent` consomment. **L'axe 1
en est propriétaire en écriture** — il y ajoute des comportements pour ses nouveaux lecteurs
(`sbx mcp ls`, `sbx skills ls`).

Toi, tu ne l'édites pas, **mais tes tests tournent contre lui**. Si ta suite casse sans que tu aies
touché un fichier, regarde d'abord si l'axe 1 a changé — et non seulement étendu — le dispatch de
`Fake`. Le contrat convenu est qu'un tel changement est un événement « stop et coordonne » de son
côté ; s'il t'atteint quand même, remonte-le au lieu de contourner localement.

## 8. Invariants du dépôt qui te concernent

- **Aucun test n'ouvre de socket, ne lance de process, ni n'appelle `t.Parallel()`.**
- Les paquets qui lancent du vrai git (`cli`, `spawn`, `worktree`) appellent
  `worktree.NeutralizeGitEnvironment()` dans `TestMain`. **Sans ça, la suite a réellement commité
  dans des dépôts étrangers** via un `GIT_DIR` hérité.
- **`internal/spawn` n'importe jamais `internal/ports`**, et **`internal/cli` n'importe ni `net`,
  ni `hash/fnv`, ni `os/exec`**.
- Les erreurs nomment **le fichier à corriger et le remède** (`fix \`repos:\` in <path>`, « run
  \`den doctor\` »). den refuse plutôt que de normaliser en silence (spec §2).
- **`task check`** avant chaque commit.

## 9. Démarrage

```bash
cd /Users/polochon/Development/Pillow/den
git worktree add .claude/worktrees/axe3-resources \
    -b feat/resources-in-cascade docs/sbx-v0.39.0-survey
cd .claude/worktrees/axe3-resources
task check     # doit être vert AVANT que tu écrives quoi que ce soit
```

Tu branches sur `docs/sbx-v0.39.0-survey` et non sur `main` **volontairement** : c'est la branche
qui porte le spec §14.2, donc les pointeurs de ce handoff résolvent tout de suite. Quand la PR #91
sera mergée, un `git rebase main` suffit. Si elle est déjà mergée quand tu lis ceci, branche sur
`main`.

Ton worktree est **propre pour les greps** : `.gitignore` exclut `.claude/**` sauf `settings.json`
et `commands/`, donc ton arbre ne contient pas les worktrees des autres axes.

**Premier pas concret** : lis `internal/sbx/argv.go` en entier — commentaires compris, ils portent
les raisons — puis `nest.Resolve`. Écris le test de cascade **avant** le champ (red-green) : la
question qui décide de tout est « qu'est-ce qui gagne quand la stack dit 4 CPU et le nest 8 ? ».

## 10. Fini quand

- `task check` vert.
- Un nest peut déclarer `resources:` ; `stack.yaml` et `config.yaml` aussi, et l'ordre de
  précédence est testé.
- Un `memory:` invalide est refusé **par den**, avant le premier effet de bord, avec un message qui
  nomme le fichier et le remède.
- Un `cpus:` absent **omet** le drapeau.
- Un `resources:` modifié sur une sandbox vivante **avertit** de la dérive et ne recrée rien.
- Les goldens d'argv sont à jour, édités à la main.
- La description de PR dit ce que tu voudrais voir écrit dans `CLAUDE.md`, sans l'y écrire.
