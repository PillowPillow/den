# `den exec` — une seule porte pour l'interactif et le non-interactif

Date : 2026-08-10
Issue : [#60](https://github.com/PillowPillow/den/issues/60)
Statut : conception validée, implémentation à faire

## Le problème

den n'a qu'une porte vers une sandbox vivante, et elle est interactive. `den sh <name>` trouve la
sandbox, joue la barrière de fraîcheur §9.1, puis appelle `spawn.Attach`, qui lance
`sbx exec -it [-w <workdir>] <sandbox> bash -l` (`internal/spawn/spawn.go:1694`).

Aucun moyen de lancer **une** commande dans une sandbox et d'en récupérer la sortie et le code de
retour. Tout appelant non interactif — une étape de CI, un script, une cible `task` — doit soit
piloter un shell interactif, soit contourner den et appeler `sbx` directement, ce qui perd la
résolution du nom de sandbox, la barrière de fraîcheur et l'avertissement ssh-agent que den possède.

## La surface `sbx` mesurée le 2026-08-10 (v0.38.0)

Quatre faits mesurés sur la machine de l'utilisateur, contre la sandbox vivante `swimspot`, plus un
cinquième lu dans le texte d'aide du binaire. Ils appartiennent au §14.0 de la spec de référence,
avec leur date — c'est le seul relevé du dépôt.

**1. Le code de retour de la commande interne remonte.** C'était le fait bloquant : l'issue #60 dit
elle-même que sa falsification déplacerait l'exigence « code de retour » de « plomberie den » vers
« limitation `sbx` que den doit documenter ».

```
sbx exec swimspot sh -c 'exit 42'   →  rc=42
sbx exec swimspot true              →  rc=0
```

**2. stdout et stderr restent SÉPARÉS sans `-it`.**

```
sbx exec swimspot sh -c 'echo OUT; echo ERR >&2' 2>/tmp/err.txt
  → stdout : OUT       /tmp/err.txt : ERR
```

C'est ce qui interdit de réutiliser `Runner.Stream`, qui les fusionne délibérément dans un seul
descripteur (voir `streamSink`, `internal/sbx/runner.go`).

**3. Une stdin redirigée atteint la commande SANS aucun drapeau `-i`.**

```
printf 'hello-from-host\n' | sbx exec swimspot cat   →  hello-from-host
```

Divergence mesurée avec `docker exec`, qui exige `-i` pour garder STDIN ouverte. den ne passe donc
aucun drapeau sur la branche non-TTY.

**4. `sbx exec -d` NE DÉTACHE PAS — la documentation du drapeau est falsifiée.** `sbx exec --help`
annonce « Detached mode: run command in the background ». Mesuré :

```
sbx exec -d swimspot sh -c 'sleep 5'      →  bloque 5 s   (sans -d : 6 s)
sbx exec -d swimspot sh -c 'echo X'       →  X arrive sur la stdout de den
sbx exec -d swimspot sh -c 'exit 42'      →  rc=42
```

Le drapeau est accepté et sans effet observable : la commande est attendue, sa sortie relayée, son
code retourné. C'est ce qui ferme la décision 4 ci-dessous, et c'est un défaut à signaler en amont
indépendamment de #60.

**5. `-t` SANS terminal attaché JETTE la sortie de la commande, en silence.** Mesuré sur une
seconde sandbox vivante (`dg-kafoutche`), stdin et stdout redirigés :

```
sbx exec -it <sb> echo hello  </dev/null >fichier   →  fichier VIDE,   rc=0
sbx exec -t  <sb> echo hello  </dev/null >fichier   →  fichier VIDE,   rc=0
sbx exec     <sb> echo hello  </dev/null >fichier   →  "hello",        rc=0
sbx exec -it <sb> sh -c 'exit 42'                   →  rc=42
```

Le code de retour remonte quand même : c'est la SORTIE qui disparaît. Conséquence directe sur le
contrat ci-dessous — conditionner `-it` à `Deps.IsTTY` n'est pas une préférence, c'est ce qui
empêche `den exec` en CI de perdre chaque octet écrit par la commande.

**6. Une sandbox arrêtée est redémarrée.** « If the sandbox is stopped, it is started first »,
`sbx exec --help` sur v0.38.0. Source : le texte d'aide du binaire, **pas** une mesure du jour — le
relevé du 2026-07-28 l'avait mesuré sur v0.35.0 (`sbx exec <name> true`, 1,4 s).

## Le contrat

```
den exec <sandbox> [--workdir <path>] [-T] [-- <cmd> [args...]]
den spawn <nest> [-w <branch>] [--workdir <path>] [-T] [-- <cmd> [args...]]
```

Modelé sur `docker compose exec`, qui résout déjà « interactif et non interactif à la même porte » :
un TTY est alloué par défaut, `-T` le coupe.

- **Sans commande** ⇒ `bash -l`, exactement ce que `den sh` fait aujourd'hui. La voie interactive
  n'est pas un cas particulier, c'est l'argument par défaut.
- **TTY** ⇒ alloué quand `Deps.IsTTY()` répond vrai et que `-T` est absent. `Deps.IsTTY` existe déjà
  (`internal/cli/root.go:66`) et est déjà injecté pour la checklist `-i` ; on le réutilise, on ne lit
  ni `runtime` ni `os` dans `cli`.
- **Workdir** ⇒ même règle que `spawn.Attach` : le premier workspace **rapporté par la VM**, jamais
  un chemin recalculé depuis la configuration. Le drapeau de surcharge est `--workdir`, épelé long :
  **`-w` est pris** par la worktree de `den spawn`, et lui donner un second sens sur une commande
  sœur est exactement la collision que den refuse ailleurs.
- **Code de retour** ⇒ le code de la commande devient le code de den.
- **Précision venue de l'implémentation (2026-08-10)** : la règle TTY ci-dessus gouverne une
  commande DONNÉE. Sans commande, le terminal reste inconditionnel — un `bash -l` sans terminal ne
  vaut rien, et c'est le comportement que `den sh` et `den spawn` ont toujours eu ; le changer
  toucherait des dizaines d'assertions pour une voie où rien n'est écrit à perdre. `-T` sans
  commande est donc une contradiction, refusée en nommant les deux moitiés.
- **Workdir sur les deux modes.** La commande hérite du même premier workspace que le shell.
  L'alternative — pas de workdir sans `--workdir` — a été écartée : la commande atterrirait dans le
  home de la VM, et `den exec api -- go test ./...` échouerait pour une raison que rien n'affiche.
- **Précision venue de l'implémentation (2026-08-10)** : le refus « `-T` sans commande » n'est pas propre à
  `den exec`. `den spawn` le refuse aussi, dans les mêmes mots, octet pour octet
  (`internal/spawn/spawn.go`, étape 0, à côté du refus `--detach` + commande). Les deux commandes
  partagent le drapeau `-T` ; un utilisateur qui rencontrerait la contradiction refusée sur l'une et
  silencieusement acceptée sur l'autre lirait deux règles là où il n'y en a qu'une — c'est la
  normalisation silencieuse que le §2 de la spec interdit.

## Les quatre décisions

### 1. `den sh` est SUPPRIMÉ

Retenu contre l'alias que l'issue recommandait. `sh` n'est pas le premier mot qui vient pour entrer
dans un nid, `exec` porte les deux modes, et garder les deux noms garde deux entrées dans le tableau
des commandes pour une seule porte.

Suppression **sèche** : pas de commande cachée qui renvoie vers `den exec`. Conséquence mesurée, et
acceptée en connaissance de cause — `internal/cli/root.go:327` remplace la branche « unknown
command » de cobra par le listing de den, et une fois `sh` retiré :

```
SuggestionsFor("sh") = [ls rm]
```

`den sh api` proposera donc `den ls` ou `den rm`, jamais `den exec` (distance d'édition 4, hors de
portée de `SuggestionsMinimumDistance = 2`). L'utilisateur trouve `exec` en lisant le listing des
commandes que le même message imprime, pas par la suggestion. Le golden
`internal/cli/testdata/unknown-command.golden` fige cette sortie.

C'est une rupture, assumée : `den sh` a été livré jusqu'à v1.5.0.

### 2. `den spawn` apprend `-- <cmd>`, dans le même changement

`compose` sépare `run` (fabrique, puis lance) de `exec` (dans celle qui tourne). Le `spawn` de den
est spawn-**ou**-attache, donc plus proche de `run`, et il adresse un **nid** (`corp:api` + `-w`)
là où `exec` adresse une **sandbox** (`corp-api.feat12`).

- `den spawn <nest> [-w b] -- <cmd>` — pour un appelant qui ne connaît pas, et ne doit pas avoir à
  calculer, le nom de la sandbox. Idempotent : crée ou attache, puis lance.
- `den exec <sandbox> -- <cmd>` — pour un appelant qui tient une sandbox vivante depuis `den ls`.

Une seule implémentation partagée : `spawn` finit déjà dans `spawn.Attach`, la queue devient la
commande donnée au lieu de `bash -l`.

### 3. La barrière §9.1 ATTEND, même sans TTY

Aujourd'hui la barrière prend les deux côtés différemment : attacher un shell **attend** le verdict,
parce que l'utilisateur est sur le point de lancer cet agent ; sous `--detach` elle lit une fois et
passe, parce que personne n'est à une invite.

`den exec -- <cmd>` ne rentre dans ni l'un ni l'autre : personne n'est à une invite, et la commande
lancée est très souvent l'agent lui-même. La raison d'être de la barrière — « vous êtes sur le point
de lancer cet agent » — est **plus** vraie ici, pas moins. Elle attend.

### 4. `--detach` + commande : REFUS

`sbx exec -d` ne détache pas (mesure 4 ci-dessus), donc « prépare, puis lance détaché » ne peut pas
être délégué à `sbx`. den devrait détacher lui-même : une cinquième méthode de `sbx.Runner`, la
gestion des orphelins et de leurs journaux, et aucun code de retour à rendre. Hors périmètre.

Le refus nomme les deux drapeaux, comme den refuse déjà `-i` avec `--only`/`--without`.

## Les composants

### `sbx.Runner` gagne une quatrième méthode

La godoc de `Runner` énumère « trois méthodes, parce que les trois usages sont irréconciliables ».
La branche non-TTY en ajoute une quatrième raison, et aucune des trois ne convient :

| méthode | pourquoi elle ne convient pas |
|---|---|
| `Attach` | pose `cmd.Cancel = nil` — l'annulation de contexte ne fait RIEN, ce qui est juste pour un shell (ne pas laisser le tty en mode brut) et faux ici : un Ctrl-C sur un `go test` de trois minutes doit le tuer. Le fichier le dit déjà : « Read Attach's comment before copying its `cmd.Cancel = nil` up here. » |
| `Stream` | fusionne stdout et stderr dans **un** descripteur, exprès (`streamSink`). La mesure 2 dit que `sbx` les garde séparés, et `den exec -T -- go build \| tee log` ne doit pas pousser stderr dans le tuyau. |
| `Run` | capture stdout pour l'analyser ; rien n'est relayé. |

La quatrième passe les trois descripteurs de den **tels quels et non fusionnés**, laisse `cmd.Cancel`
au défaut SIGKILL de `CommandContext` (comme `Stream`), et remplit `ExecError.Cancellation` (contrairement
à `Attach`, qui le laisse nil parce qu'elle laisse délibérément finir le shell).

La branche TTY reste `Attach` : `sbx exec -it`, `cmd.Cancel = nil`, exactement ce qui existe.

`sbx.Fake` (`internal/sbx/fake.go`, fichier de production) gagne l'enregistreur correspondant, comme
il en tient un pour `Attach`.

### Le code de retour, de `sbx` jusqu'à `os.Exit`

Aujourd'hui `cmd/den/main.go` fait `os.Exit(1)` sur n'importe quelle erreur. Il faut qu'un code
d'enfant traverse `sbx.Runner` → `internal/cli` → `main`, **sans** que `internal/cli` importe
`os/exec` : c'est verrouillé par `TestCliImportsNoRawPortOrSystemAccess`
(`internal/ports/hermeticity_test.go`), avec un message d'échec sur le graphe d'imports.

L'extraction vit donc dans `internal/sbx`, qui importe déjà `os/exec` et dont `ExecError.Unwrap`
rend déjà l'`*exec.ExitError` atteignable par `errors.As` :

- `sbx.ExitCodeOf(err) (int, bool)` — l'`errors.As` vers `*exec.ExitError`.
- `*sbx.ChildExit{Code int}` — l'erreur typée qui remonte, construite sur la branche exec seule.

`main.go` distingue alors les deux classes :

```go
var ce *sbx.ChildExit
if errors.As(err, &ce) {
    os.Exit(ce.Code) // pas de préfixe "den:" — l'enfant possède sa propre sortie
}
fmt.Fprintln(os.Stderr, "den:", err)
os.Exit(1)
```

Un enfant qui sort en 1 reste indistinguable d'un échec de den **par le code seul** ; c'est le
préfixe `den:` sur stderr qui les sépare pour un humain, et il n'y a pas de code libre à réserver
sans casser la promesse « le code de la commande devient le code de den ».

### La discipline des flux

Sur la voie non interactive, tout ce que den dit lui-même part sur **stderr** : la ligne « sandbox
arrêtée », les messages de la barrière §9.1, l'avertissement ssh-agent. `spawn.CheckFreshnessOnReentry`
écrit aujourd'hui sur `cmd.OutOrStdout()` ; sur `den exec -T -- go build | tee log`, ce bavardage
corromprait la stdout que l'enfant possède.

La voie interactive garde le comportement actuel.

### Ce que la suppression de `den sh` déplace

`internal/cli/sh.go` ne contient pas que la commande :

- `warnEmptyAgentOnReentry` y est **défini**, pas seulement appelé. Il déménage dans `exec.go`.
- `internal/cli/sh_test.go` fige la ligne « sandbox arrêtée », la barrière §9.1 à la ré-entrée et
  l'ordre « avertissement AVANT l'attache ». Ces cas migrent vers `exec_test.go` — les perdre
  retirerait silencieusement la couverture de la barrière sur la porte de ré-entrée.
- Les goldens (`internal/cli/testdata/`) et `reference.go` listent les commandes. Ils s'éditent à la
  main : il n'existe pas de drapeau `-update`.

## Les invariants à respecter

- `internal/cli` n'importe ni `net`, ni `hash/fnv`, ni `os/exec`. `exec` n'a besoin d'aucune
  dépendance nouvelle : `deps.Sbx` est l'unique `sbx.Runner` partagé et possède déjà l'invocation de
  processus.
- La suite n'ouvre aucune socket et ne lance aucun processus. La décision TTY vient de `Deps.IsTTY`
  injecté, jamais du terminal sous lequel le test se trouve tourner.
- Le nom de sandbox est l'unique poignée (`sbx create` n'a pas de `--label`, sondé le 2026-07-28).
  `exec` le résout par `sandboxNameOf`, comme `den sh` le fait.

## L'ordre de construction

1. **Le code de retour** — `sbx.ExitCodeOf`, `*sbx.ChildExit`, `cmd/den/main.go`. C'est l'arête
   bloquante : `den exec` est inutilisable en CI sans lui, et il touche le chemin partagé par lequel
   toute commande revient.
2. **La quatrième méthode de `Runner`** + son double dans `sbx.Fake`.
3. **`den exec`** — commande, barrière, avertissement déménagé, tests migrés depuis `sh_test.go`.
4. **`-- <cmd>` sur `den spawn`** + le refus `--detach`.
5. **La suppression de `den sh`** — goldens, `reference.go`, README. Mécanique, et c'est ce qui
   touche le plus de fichiers.
6. **Spec §14.0** (les cinq relevés datés) et README (tableau des commandes + tableaux d'options).

## Hors périmètre

Tout ce qui est planifié ou non surveillé. C'est une décision distincte, écrite dans
`docs/superpowers/decisions/2026-08-10-openharness-review-and-roadmap.md` §2, et ce n'est pas ce que
cette issue demande.
