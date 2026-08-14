# Design — `den exec` prend la forme `docker compose`, `den shell` naît

Date : 2026-08-14
Statut : validé, prêt pour le plan
Tranche : 1 sur 2. La tranche 2 (`den spawn` → `up` / `run`) a sa propre spec, pas encore écrite.

## Le problème

`den exec <sandbox>` sans commande ouvre un shell. C'est la décision de la spec
`2026-08-10-den-exec-design.md` (l. 93) : « Sans commande ⇒ `bash -l`, exactement ce que `den sh`
fait aujourd'hui. La voie interactive n'est pas un cas particulier, c'est l'argument par défaut. »

Cette décision est argumentée par la CONTINUITÉ avec le `den sh` d'avant #60, pas par le contrat.
Or la même spec dit s'être modelée sur `docker compose exec` — et `docker compose exec` exige une
commande. den a donc emprunté à compose la moitié TTY du contrat (`-T`) et pas la moitié
positionnelle. Un utilisateur qui connaît compose lit `den exec api` et attend un refus.

Le second écart est plus coûteux à l'usage : den EXIGE `--` avant la commande, compose non.
`internal/cli/exec.go:16-27` justifie cette exigence par une ambiguïté qui n'existe pas —
« `den exec api go test` a deux lectures : une sandbox `api` qui lance `go test`, ou trois noms de
sandbox ». `execArgs` refuse déjà plus d'un nom (`dash != 1`), donc la seconde lecture n'a jamais
été atteignable. L'ambiguïté RÉELLE porte sur les DRAPEAUX : dans `den exec api go test -v`, à qui
appartient `-v` ? Compose la ferme avec `SetInterspersed(false)`, que den ne pose nulle part
(grep sur `internal/`, zéro occurrence au 2026-08-14).

## Ce qui a été mesuré

**`docker compose` v2, sondé le 2026-08-14** (`docker compose exec --help`, `run --help`) :

```
docker compose exec [OPTIONS] SERVICE COMMAND [ARGS...]
docker compose run  [OPTIONS] SERVICE [COMMAND] [ARGS...]
```

`COMMAND` est obligatoire sur `exec`, optionnel sur `run`. Aucun `--`. `-T` coupe le TTY, `-w`
donne le workdir, `-d` détache.

**cobra, sondé le 2026-08-14** dans l'arbre RÉEL de den (commande `exec` enfant d'un `root` portant
la persistante `--den-home`), avec `Flags().SetInterspersed(false)` :

| Entrée | `args` reçus | `ArgsLenAtDash()` | drapeaux den |
|---|---|---|---|
| `exec api go test -v` | `["api","go","test","-v"]` | −1 | — |
| `exec -T api go build` | `["api","go","build"]` | −1 | `-T`=true |
| `--den-home /x exec api bash` | `["api","bash"]` | −1 | `den-home`=/x |
| `exec api --help` | `["api","--help"]` | −1 | — |
| `exec api -h` | `["api","-h"]` | −1 | — |
| `exec api -- go test` | `["api","--","go","test"]` | −1 | — |
| `exec api -T -- go build` | `["api","-T","--","go","build"]` | −1 | `-T`=**false** |

Quatre conséquences, toutes portantes :

1. **`SetInterspersed(false)` survit au parent.** Cobra fusionne les persistantes dans le même
   FlagSet avant `ParseFlags` ; la fusion ne réarme pas l'entrelacement. `--den-home` continue
   d'être lue.
2. **`ArgsLenAtDash()` rend −1 dans TOUS les cas, et `--` survit comme argument ordinaire.** Cobra
   ne le consomme plus. den peut donc reconnaître l'ancienne forme EXACTEMENT, par comparaison de
   chaîne, sans heuristique.
3. **Cobra n'intercepte PAS `--help` ni `-h` placés après le nom de sandbox.** `den exec api --help`
   lancera `--help` DANS la VM, comme compose. Ce n'est pas un effet de bord toléré : c'est le
   comportement voulu, et il n'aurait pas été acquis d'office — il est mesuré, pas supposé.
4. **Les drapeaux de den passent à GAUCHE du nom de sandbox.** `den exec api -T -- go build`, la
   forme que le README donne en exemple (l. 83), devient `den exec -T api go build`. C'est la
   rupture réelle de cette tranche ; l'absence de shell par défaut n'en est qu'une conséquence
   visible.

## Le contrat

```
den exec  [-T] [--workdir <path>] <sandbox> <cmd> [args...]
den shell [--workdir <path>] <sandbox>
```

`<sandbox>` est la chaîne que l'utilisateur tape : `api`, `api.reco`, `corp:api`. C'est le nom de
sandbox `<nest>[.<instance>]` que `den rm` et `den ports` prennent déjà, pas un nom de nest — den
n'introduit pas de troisième terme pour la même chaîne.

- **`den exec` EXIGE une commande.** Plus de `bash -l` implicite. Tout ce qui suit le nom de
  sandbox est la commande, verbatim, drapeaux compris.
- **`--` n'est plus nécessaire, et n'est plus consommé.** Voir les refus ci-dessous.
- **`den shell <sandbox>` lance `bash -l`** — le comportement d'aujourd'hui, octet pour octet.
- **`-T`** garde son sens sur `den exec`. Sur `den shell` il est REFUSÉ (un shell de login sans
  terminal ne vaut rien). Le drapeau est tout de même ENREGISTRÉ sur `den shell`, et ce n'est pas
  un oubli : un refus nommé vaut mieux que `unknown flag: -T`, qui est l'argument que #60 tenait
  déjà. Ne pas « simplifier » en le retirant.
- **`--workdir` reste épelé long, sur les deux commandes.** Compose épelle le workdir `-w` ; den ne
  peut pas — `-w` est la worktree de `den spawn`, et un même caractère pour deux sens entre
  commandes sœurs est la collision que den refuse ailleurs. Divergence assumée, pas oubliée.
- **Code de retour, séparation stdout/stderr, `spawn.StartDir`, barrière de fraîcheur §9.1,
  avertissement ssh-agent** : inchangés, et PARTAGÉS entre `exec` et `shell` (voir Factorisation).

## Les refus

Deux, pas trois. Aucun n'est une fenêtre de migration : den est en 1.x mais en alpha, avec un seul
utilisateur au 2026-08-14, et les ruptures sont assumées telles quelles. Les deux refus restent
parce qu'ils sont JUSTES en régime permanent, pas parce qu'ils accompagnent une transition.

**1. Aucune commande.**

```
$ den exec api
den exec: no command given — write `den exec api go test`, or `den shell api` for a shell
```

C'est une erreur du nouveau contrat, pas un garde-fou : zéro positionnel après le nom n'a plus de
lecture. Le message est aussi le seul endroit où l'utilisateur APPREND que `den shell` existe — la
liste de commandes inconnues le nomme, mais il faut se tromper de commande pour la voir.

**2. Le premier jeton après le nom de sandbox est un drapeau de den, ou `--`.**

```
$ den exec api -T go build
den exec: den's flags go before the sandbox name — write `den exec -T api go build`

$ den exec api -- go test
den exec: `--` is not needed — write `den exec api go test`
```

Un seul contrôle, deux messages. Sans lui, den passe `-T` à la VM et l'utilisateur lit
`bash: -T: command not found` venu de l'intérieur du sandbox — un échec qui ne nomme rien de ce
qu'il faut corriger. Le contrôle porte sur un ensemble FERMÉ : `-T`, `--no-tty`, `--workdir`,
`--workdir=…`, `--`. Aucun programme réel ne porte ces noms, donc le refus ne peut pas avaler une
commande légitime. `den exec api --help` n'en fait PAS partie et passe à la VM (mesure 3).

## `den shell`, et pourquoi ce n'est pas une seconde porte

`den shell` construit le MÊME `spawn.Command` avec `Argv` nil et le passe au MÊME `spawn.Enter`.
`internal/spawn` n'est pas touché : `Command.Argv` vide ⇒ `bash -l` reste où il est
(`internal/spawn/enter.go:85`), et ses appelants deviennent `den shell` et `den spawn` au lieu de
`den exec` et `den spawn`. Le commentaire d'`enter.go` dit que le défaut est placé là « plutôt qu'à
chaque site d'appel » pour empêcher `den exec` et `den spawn` de dériver ; ce changement le
confirme au lieu de le défaire.

**Factorisation.** `newExecCmd` et `newShellCmd` ne diffèrent que par deux choses : l'argv et le
refus de `-T`. Le corps commun descend dans un `enterSandbox` non exporté que les deux appellent.
Pas deux copies : l'avertissement du commentaire de #60 (`internal/cli/exec.go:72-77`) est
exactement que deux orthographes d'une porte finissent par dériver.

**`den sh` reste mort.** Aucun alias. La distance d'édition de `sh` à `shell` est 3, au-dessus du
seuil 2 de `SuggestionsFor` : `den sh` ne suggérera pas `den shell`. C'est le coût que #60 avait
déjà mesuré et accepté pour `exec`, et il ne change pas de nature ici — la liste qui suit le refus
nomme toutes les commandes, `shell` comprise.

**Où est passé l'invariant « `-T` sans commande ».** `2026-08-10-den-exec-design.md` (l. 113-119)
grave que ce refus est identique OCTET POUR OCTET sur `den exec` et `den spawn`, en citant le §2 :
une contradiction ne doit pas se lire comme deux règles. L'invariant SURVIT, mais sa paire change.
Sur `den exec`, « pas de commande » est désormais sa propre erreur, et `-T` n'y contredit plus rien.
La paire devient **`den shell` ↔ `den spawn`** : les deux commandes qui peuvent attacher un shell,
avec le même message. Un lecteur de l'ancienne spec cherchera l'invariant sur `den exec` ; il est
ici, déplacé, pas dissous.

## Portée

`internal/spawn`, `internal/sbx`, `internal/nest`, `internal/manifest` : intacts. La tranche est
entièrement dans `internal/cli`.

| Fichier | Changement |
|---|---|
| `internal/cli/exec.go` | `execArgs` remplacé : ≥ 2 positionnels + le refus « drapeau ou `--` en tête de commande ». `Flags().SetInterspersed(false)`. Le refus `-T`-sans-commande sort. Le corps descend dans `enterSandbox`. |
| `internal/cli/shell.go` (nouveau) | `newShellCmd` : mêmes dépendances, `Argv` nil, refus de `-T`. |
| `internal/cli/root.go` | un `AddCommand`. |
| `internal/cli/exec_test.go` | 32 tests : ~20 réécrivent leur argv (`-- cmd` → `cmd`), 4 s'inversent ou déménagent. |
| `internal/cli/shell_test.go` (nouveau) | les tests de la voie shell qui quittent `exec_test.go`. |
| `internal/cli/testdata/unknown-command.golden` | **deux** éditions à la main (il n'y a pas de `-update`) : la ligne `shell`, et le `Short` d'`exec` qui perd « or open a shell ». |
| `README.md` | tableau des commandes (l. 83), bloc d'options de `den exec` (l. 143), et les 29 occurrences de `den exec` dans les docs à relire. |
| `CHANGELOG.md` | une ligne sous `Changed`, rupture assumée, sans fenêtre de dépréciation. |

## Tests

Les quatre qui s'inversent ou déménagent sont ceux qu'une revue doit regarder :

| Aujourd'hui | Demain |
|---|---|
| `TestExecRefusesACommandWithoutTheDoubleDash` | `TestExecRunsACommandWithoutTheDoubleDash` |
| `TestExecRefusesZeroPositionalsBeforeTheDoubleDash` | `TestExecRefusesASandboxWithNoCommand` — assertion : le message NOMME `den shell` |
| `TestExecRefusesNoTTYWithNoCommand` | déménage en `TestShellRefusesNoTTY` |
| `TestExecRunsTheCommandAfterTheDoubleDash` | `TestExecRunsTheCommand` |

Nouveaux :

- `TestExecRefusesItsOwnFlagsAfterTheSandboxName` (`-T`, `--workdir`, `--`) ;
- `TestExecPassesTheCommandsOwnFlagsThrough` — `den exec api go test -v` ; garde la mesure 1 ;
- `TestExecPassesHelpToTheSandbox` — `den exec api --help` ; garde la mesure 3, qui est la plus
  facile à casser sans s'en apercevoir ;
- `TestExecStillReadsDenHomeBeforeTheSubcommand` — garde la mesure 1 sur la persistante ;
- toute la voie shell dupliquée depuis `exec_test.go` : workdir, barrière §9.1, avertissement
  ssh-agent, chatter sur stdout.

Aucun test n'ouvre de socket ni ne lance de processus : la convention du dépôt tient sans
aménagement, `sbx.Fake` suffit.

## Divergences assumées

1. **`den spawn` garde `--` et garde son shell par défaut.** Il prend `<nest> [repo...]` — des
   positionnels variadiques — donc le séparateur y est réellement portant : `den spawn api
   ~/dev/hotfix go test` est ambigu, et `SetInterspersed(false)` n'y répond pas. Pendant un temps,
   deux commandes sœurs se lisent différemment. Ce n'est pas un coût accepté pour la durée d'une
   release : c'est la raison d'être de la tranche 2, qui peut suivre immédiatement.
2. **`--workdir` contre le `-w` de compose.** Permanent, pas transitoire : la collision avec la
   worktree existera encore quand `spawn` sera devenu `up`.
3. **`-d` de compose (`exec -d`, détache la commande) n'existe pas sur `den exec`.** den a
   `spawn --detach`, qui veut dire « n'attache pas de shell » — le `up -d` de compose, pas le
   `exec -d`. Hors périmètre ; nommé pour qu'un lecteur ne le prenne pas pour un oubli.

## Ce qui suit

Tranche 2, spec séparée, suivie par [#72](https://github.com/PillowPillow/den/issues/72) :
`den spawn` → `den up` / `den run`, sur la césure que compose tient déjà
(`up` ne prend pas de commande, `run` en prend une optionnelle) alors que `den spawn` fait les deux.
La grammaire reste VERBE-D'ABORD, tranchée le 2026-08-14 : `den up <nest>`, jamais `den <nest> up`.
La forme nom-d'abord a été supprimée le 2026-08-05 (`2026-08-05-spawn-command-design.md`, l. 27-29
et 36-37) parce que tout premier argument inconnu était un nom de nest valide par construction — il
n'existait aucun jeton capable de produire « ceci n'est pas une commande, voici les contrats » — et
parce qu'un nest nommé `ls` était injoignable à vie. La ressusciter pour `shell` ou `up`
rouvrirait les deux. C'est aussi la forme de compose, qui est verbe-d'abord de bout en bout.
