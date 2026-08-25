# HANDOFF — Axe 2 : positionnement de den face à `sbx env` (issue #89)

> Pour l'agent qui prend cet axe **sans contexte de conversation**. Lis ce fichier, puis
> `CLAUDE.md`, puis le spec §1, §6 et §14.2. Réponds en français ; le spec s'écrit **en français**,
> le code et les messages utilisateur en anglais.
>
> Écrit le **2026-08-24**. Les handoffs datés ne sont jamais réécrits. L'ordre de confiance est
> **le code, puis le spec, puis `CLAUDE.md`**, les handoffs en dernier.

## ⚠️ Tu n'es pas seul — trois axes tournent EN PARALLÈLE

| Axe | Issue | Branche | Nature |
|---|---|---|---|
| 1 — gouvernance | #88 | `feat/converge-names-undeclared-sbx-state` | code |
| 2 — positionnement `sbx env` | **#89** | `spec/sbx-env-positioning` | **toi** — spec, aucun code |
| 3 — `resources:` dans la cascade | #90 | `feat/resources-in-cascade` | code |

**Trois règles, à lire avant d'ouvrir un fichier.**

1. **Le spec §14 est en APPEND-ONLY cette semaine.** Tes mesures vont dans une **nouvelle
   sous-section datée** portant ton numéro d'issue (« ### Sonde du 2026-XX-XX — #89 … ») du fichier
   `docs/superpowers/specs/2026-07-27-den-cli-design.md`. Tu ne modifies **jamais** une sous-section
   existante : les axes 1 et 3 écrivent dans le même fichier. Ton **propre** spec, lui, est un
   fichier neuf : aucun conflit possible.
2. **Tu ne touches PAS `CLAUDE.md`.** PR d'intégration après les merges.
3. **Tu n'écris AUCUN code de production.** Ni `internal/**`, ni les goldens. Ton livrable est un
   document. Si tu te surprends à modifier le chemin de spawn, tu as quitté ton axe.

## 1. Ta mission, en une phrase

sbx v0.39.0 embarque `sbx env` : un modèle déclaratif qui **compose**. Décide, par écrit et avec
des faits mesurés, si den continue de piloter `sbx create` ou devient un **générateur** de
`.sbxenv.yaml`.

**C'est une question de positionnement, pas une feature.** Ton livrable est un spec, pas du code.

## 2. Le constat

Aide de `sbx env`, verbatim :

> Manage a sandbox environment declared in a .sbxenv.yaml file.
> The file describes the agent, optional mixin kits, workspace mounts, environment variables,
> secrets to provision, and per-service credential bindings.

Cette liste, ce sont les entrées de spawn de den, article par article.

Ce qui en fait une vraie question et non une curiosité — `sbx env create --help`, verbatim :

> Each PATH may be a directory (the file is <PATH>/.sbxenv.yaml) or the path to the environment
> file itself. Passing more than one PATH deep-merges them in order (docker-compose `-f`
> semantics): later files override earlier ones. Values may reference environment variables with
> ${VAR} / $VAR (and ${VAR:-default}).

`.sbxenv.yaml` n'est **pas** plat-par-sandbox. Il **compose**. Un deep-merge dans l'ordre de
déclaration a la même forme que la cascade de den :
`config.yaml ← stacks/<n>/stack.yaml ← nests/<n>.yaml ← drapeaux`.

Sous-commandes : `env create`, `env exec`, `env rm`, `env run`. `env run` crée-si-besoin puis
attache, et ré-attache une sandbox existante **sans re-provisionner** — c'est le verdict
créer-ou-attacher de `den up`. `env rm` retire la sandbox **et ses ressources scopées**, les secrets
étant provisionnés au scope de la sandbox de l'environnement.

`sbx env` est marqué **EXPERIMENTAL** : *« this command may change or be removed in future
releases »*.

## 3. Les arguments, des deux côtés — à instruire, pas à recopier

### Pour que den garde `create`

- La cascade de den résout en **un** `*nest.Resolved` avant le premier effet de bord
  (`nest.Resolve`), et toute la doctrine d'ordonnancement du spec §6 en dépend : tout ce qui est
  refusable à partir de la seule configuration est refusé **avant** qu'un worktree existe. Confier
  la sémantique de fusion à sbx sort ce jugement de den.
- `sbx env` est EXPERIMENTAL.
- Le mixin de den est généré, fail-closed, et doit être superposé **en dernier** — `sbx.Create`
  sépare `MixinKit` de `StackKits` en deux champs pour rendre l'inversion **impossible**, pas
  seulement improbable (le dispatcher sbx fait `exit $rc` à la première défaillance, ce qui prive
  les kits suivants de leurs commandes de démarrage).
- L'identité de den est le nom de sandbox `<nest>[.<instance>]`, et **tout** s'y accroche :
  `den ls`/`sh`/`rm`/`ports`, la policy scopée, le cache de mixins, la corbeille de worktrees.
  `sbx env` s'accroche à un chemin de fichier.

### Pour que den émette `.sbxenv.yaml`

- Des secrets scopés à l'environnement, retirés par `env rm`, c'est exactement le cycle de vie que
  den écrit à la main dans `internal/converge` et `internal/manifest`.
- Un `.sbxenv.yaml` généré est un **artefact lisible** de ce que den a décidé. Aujourd'hui cette
  trace est `state/sandboxes/<sandbox>.yaml`, un format propre à den que seul den lit.
- Une sandbox gérée par den deviendrait joignable par des commandes `sbx` nues — ce qui compte le
  jour où den n'est pas installé sur une machine.
- L'interpolation et le deep-merge sont deux mécaniques que den n'aurait pas à maintenir.

## 4. Le travail, dans l'ordre — et l'ordre est le sujet

### 4.1 Mesurer le schéma réel

L'aide nomme les champs, **pas leurs types ni leur imbrication**. Écris un `.sbxenv.yaml` minimal à
la main, lance `sbx env create` sur un répertoire jetable, détruis-le. Consigne le schéma dans une
nouvelle sous-section §14 datée #89.

Le dépôt a une doctrine sur ce point, et elle t'oblige : **ce dépôt atteste le comportement de
`sbx`, il ne l'extrapole pas.** Tout ce que tu écris sur `.sbxenv.yaml` porte sa date et dit s'il
est mesuré ou déduit.

### 4.2 Répondre aux inconnues bloquantes

Aucune conception ne tient tant que ces quatre-là ne sont pas tranchées **par la mesure** :

1. `kits:` **préserve-t-il l'ordre de déclaration** ? Le mixin de den DOIT être en dernier. Si
   l'ordre n'est pas garanti, la branche « den émet `.sbxenv.yaml` » est morte à cet endroit précis.
2. Peut-on y déclarer un `--kit` pointant un **répertoire généré** (le mixin de den vit sous
   `<denHome>/cache/mixins/<sandbox>`) ?
3. Porte-t-il l'**egress / la policy**, ou la policy reste-t-elle hors bande ? Le settle-loop
   fail-closed du spec §7 est le cœur de den.
4. Peut-on **nommer** la sandbox, ou le nom dérive-t-il du chemin ? Un nom dérivé casse
   `<nest>[.<instance>]`, donc l'identité entière.

### 4.3 Seulement ensuite, proposer

2 ou 3 approches avec leurs compromis et **une recommandation**, selon le flux de brainstorming
habituel du dépôt.

Une issue probable, à nommer d'emblée plutôt qu'à découvrir : **l'hybride**, où den garde la
résolution et émet `.sbxenv.yaml` comme **export** — un `den export <nest>` — sans faire passer le
spawn par là. Ça achèterait l'artefact lisible et le scénario « den pas installé » sans céder
l'ordonnancement du §6. Instruis-la comme les autres ; ne la privilégie pas parce qu'elle est
citée ici.

## 5. Ton périmètre EXACT

**Tu possèdes, en écriture :**

- `docs/superpowers/specs/2026-08-24-sbx-env-positioning-design.md` — **ton livrable**, fichier neuf
- `docs/superpowers/specs/2026-07-27-den-cli-design.md` — **en ajout seul**, une nouvelle
  sous-section §14 datée #89

**Tu ne touches pas :**

- `internal/**` — aucun code de production, aucun test, aucun golden
- `CLAUDE.md` — PR d'intégration
- toute sous-section §14 déjà écrite

## 6. Ce qui n'est PAS dans ton axe

- **La gouvernance / les collisions** (`sbx setup`, skills, MCP) — axe 1, issue #88.
- **Les drapeaux de `sbx create`** (`--cpus`, `--memory`, `--profile`, `--deny-network`) —
  axe 3, issue #90.
- **Le blocage de `den exec -T`** — l'assistant s'ouvre quand
  `isTerminal(stdin) && isTerminal(stdout)`, les descripteurs et non le drapeau `-it`. Bug du chemin
  de spawn, il reste dans **#87**.
- **Toute modification du chemin de spawn**, même « juste pour essayer ». Si ta conclusion est
  qu'il faut le changer, elle s'écrit dans le spec et se réalise dans une autre issue.

## 7. Démarrage

```bash
cd /Users/polochon/Development/Pillow/den
git worktree add .claude/worktrees/axe2-sbx-env \
    -b spec/sbx-env-positioning docs/sbx-v0.39.0-survey
cd .claude/worktrees/axe2-sbx-env
```

Tu branches sur `docs/sbx-v0.39.0-survey` et non sur `main` **volontairement** : c'est la branche
qui porte le spec §14.2, donc les pointeurs de ce handoff résolvent tout de suite. Quand la PR #91
sera mergée, un `git rebase main` suffit. Si elle est déjà mergée quand tu lis ceci, branche sur
`main`.

Ton worktree est **propre pour les greps** : `.gitignore` exclut `.claude/**` sauf `settings.json`
et `commands/`, donc ton arbre ne contient pas les worktrees des autres axes.

**Premier pas concret** : `sbx env create --help`, `sbx env run --help`, `sbx env rm --help` en
entier, puis un `.sbxenv.yaml` minimal sur un répertoire jetable. Ne conçois rien avant d'avoir vu
le schéma refuser ou accepter quelque chose.

**Sondes jetables : la règle du dépôt.** Crée, mesure, détruis (`sbx rm --force`), et vérifie
qu'aucun résidu ne reste. Les sondes du 2026-08-10 consignées au §14.0 sont le modèle.

## 8. Fini quand

- Un fichier `docs/superpowers/specs/2026-08-24-sbx-env-positioning-design.md` existe et est commité.
- Les **quatre inconnues du §4.2** y sont répondues, chacune avec la mesure qui l'a tranchée, ou
  explicitement marquée non mesurable et pourquoi.
- Une nouvelle sous-section §14 datée #89 porte le schéma réel de `.sbxenv.yaml` tel que mesuré.
- 2 ou 3 approches sont exposées avec leurs compromis, et **une** est recommandée, avec sa raison.
- Aucun fichier sous `internal/` n'a changé.
- L'humain a relu le spec et l'a approuvé avant toute suite. **Ne passe pas à un plan
  d'implémentation sans cette approbation** — c'est une décision de positionnement, pas une tâche.
