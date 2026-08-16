# Design — `den spawn` devient `den up` / `den run`

Date : 2026-08-16
Statut : validé en brainstorming, prêt pour le plan
Tranche : 2 sur 2. La tranche 1 est `2026-08-14-den-exec-shell-design.md` (`den exec` exige une
commande, pas de `--` ; `bash -l` déménage dans `den shell`).
Suivi : [#72](https://github.com/PillowPillow/den/issues/72)

## 1. Le problème

La tranche 1 a donné à `den exec` la forme de `docker compose exec` et a fait naître `den shell`.
Elle a laissé `den spawn` intact, et l'a écrit noir sur blanc (§ « Divergences assumées », l. 232-236) :

> `den spawn` garde `--` et garde son shell par défaut. […] Pendant un temps, deux commandes sœurs se
> lisent différemment. Ce n'est pas un coût accepté pour la durée d'une release : c'est la raison
> d'être de la tranche 2, qui peut suivre immédiatement.

L'écart se lit en deux lignes :

```
den exec  api go test          # pas de `--`, et den REFUSE `den exec api -- go test`
den spawn api -- go test       # `--` EXIGÉ
```

Et `den spawn` fait à lui seul ce que compose tient en deux verbes :

| compose | prend une commande ? | den au 2026-08-16 |
|---|---|---|
| `docker compose up` | non | `den spawn <nest>` — crée-ou-rattache, puis ouvre un shell |
| `docker compose run SERVICE [COMMAND]` | oui, optionnelle | `den spawn <nest> -- <cmd>` |

**Le `--` de `den spawn` n'est pas une coquetterie**, et c'est ce qui rend cette tranche plus dure que
la première. `den exec` prend UN nom de sandbox : la fin des positionnels est connue au premier
jeton, et `SetInterspersed(false)` suffit. `den spawn` prend `<nest> [repo...]` — variadique — donc
`den spawn api ~/dev/hotfix go test` a deux lectures réelles (deux repos ; ou un repo puis une
commande) qu'aucun réglage de parseur ne tranche. C'est l'objet du §4.

## 2. Ce qui a été mesuré

Rien ci-dessous n'est supposé. Les quatre mesures datent du **2026-08-16** et chacune décide une
ligne du contrat.

**a) `docker compose up` est un crée-ou-rattache** (Docker Compose v5.3.1) :

```
$ docker compose up -d          # projet neuf
 Container upprobe-api-1  Created / Starting / Started
$ docker compose up -d          # le même, déjà vivant
 Container upprobe-api-1  Running          ← ni recréé, ni redémarré
```

Cette mesure existe pour une raison précise : elle retire l'objection du §2 de
`2026-08-05-spawn-command-design.md`, qui refusait `den up` en écrivant « `den up` (idiome compose)
ment sur la sémantique — c'est un spawn-**or-attach**, pas un démarrage ». La prémisse est fausse :
`compose up` est lui aussi un crée-ou-rattache, et `--no-recreate` n'existe dans son aide que parce
que le cas « les conteneurs existent déjà » est un cas normal du verbe. `up` ne ment donc sur rien
que compose ne dise déjà.

**b) `docker compose run` met les montages ad hoc derrière un drapeau répétable** :

```
docker compose run [OPTIONS] SERVICE [COMMAND] [ARGS...]
  -v, --volume stringArray   Bind mount a volume
```

compose a exactement le problème de den — une commande variadique ET des montages à la volée — et il
le tranche par un drapeau, pas par un séparateur. C'est l'argument central du §4.

**c) pflag 1.0.9, `StringArrayVar` sous `SetInterspersed(false)`**, dans la forme réelle de l'arbre
de den (sous-commande d'un root portant la persistante `--den-home`) :

| Entrée | `args` reçus | `--repo` | `--only` |
|---|---|---|---|
| `run --repo ~/dev/b --repo ~/dev/a api go test -v` | `[api go test -v]` | `[~/dev/b ~/dev/a]` | — |
| `run --repo=/x,y --only a,b api sh` | `[api sh]` | `[/x,y]` | `[a b]` |
| `--den-home /h run -w feat/x --repo /z api go build` | `[api go build]` | `[/z]` | — |
| `run api --repo /late go test` | `[api --repo /late go test]` | `[]` | — |

Quatre conséquences, toutes portantes :

1. **L'ordre de frappe est conservé.** `2026-08-04-adhoc-repos-design.md` en dépend : l'ordre des
   repos décide l'argv de `sbx` (`[positionnels…] [déclarés…] [common git dirs…] [profil agent]`) et
   donc le `StartDir`. Un drapeau répétable le rend aussi fidèlement qu'un positionnel.
2. **`StringArray` ne coupe PAS sur la virgule, `StringSlice` si.** `--repo` doit être un
   `StringArrayVar` : un chemin peut contenir une virgule. `--only` / `--without` restent des
   `StringSliceVar` — ils adressent des *basenames* de repo, où la virgule n'a pas cours.
3. **La persistante `--den-home` reste lue** à gauche de la sous-commande, comme en tranche 1.
4. **Un `--repo` tapé APRÈS le nom du nest n'est pas analysé** : il part dans la VM comme un jeton
   de la commande. C'est la panne que la tranche 1 a fermée par un ensemble fermé de refus, et
   `--repo` doit y entrer (§5).

**d) L'ensemble fermé est DÉRIVABLE.** Dans le validateur `Args`, après que cobra a fusionné les
persistantes :

```
den --den-home /h run -T api go test
  args = ["api" "go" "test"]
  cmd.Flags().VisitAll → --den-home | --help (-h) | --no-tty (-T, novalue) | --repo | --worktree (-w)
```

Le walk voit les drapeaux de den, la persistante du root, le raccourci de chacun, et `NoOptDefVal`
distingue un booléen d'un drapeau à valeur — c'est exactement l'information que les remèdes du §5
doivent reconstruire. Il voit AUSSI `--help` / `-h`, qui doivent être exclus : la mesure 3 de la
tranche 1 a décidé que `den exec api --help` demande son aide au programme, dans le sandbox. Cette
exclusion cesse d'être un hasard de rédaction pour devenir une ligne écrite.

## 3. Les noms

**`den spawn` disparaît. `den up` et `den run` naissent.** Aucun alias, aucune fenêtre de
dépréciation : den est en alpha avec un seul utilisateur au 2026-08-16, et la rupture est assumée
telle quelle.

Deux points qu'un lecteur cherchera :

- **Le §2 de `2026-08-05-spawn-command-design.md` est amendé, pas ignoré.** Il refusait `den up` sur
  la prémisse que la mesure (a) démonte, et il réservait la famille `spawn` / `agent` / `review` de
  la spec 2026-07-27 §5. Cette famille perd son premier terme : `den agent` et `den review`, s'ils
  naissent, prendront un nest sans que `up` les gêne — ce sont des verbes différents pour des gestes
  différents, pas trois orthographes d'une porte.
- **`internal/spawn` GARDE son nom**, ainsi que `spawn.Spawn`, `spawn.Options` et `spawn.Enter`. Le
  paquet nomme le geste interne (créer une microVM), que deux commandes appellent désormais. Le
  renommer coûterait un diff de plusieurs centaines de lignes pour aligner un nom de paquet sur un
  nom de commande — et den n'aligne pas ces deux espaces ailleurs non plus (`internal/sbx` sert
  `ls`, `rm`, `ports`, `exec`).

**Aucun alias `spawn`.** Deux orthographes d'une porte est le défaut que le commentaire de #60
(`internal/cli/exec.go:72-77`) et la factorisation `enterSandbox` de la tranche 1 existent pour
empêcher. La migration passe par un message (§6), pas par une seconde porte.

## 4. Le contrat

```
den up  [flags] <nest>
den run [flags] <nest> <cmd> [args...]
```

`<nest>` est la chaîne que l'utilisateur tape : `api`, `corp:backend`. Le nom de sandbox
`<nest>[.<instance>]` en dérive (`--as`, `-w`), inchangé.

- **`den up <nest>`** : crée-ou-rattache, puis `bash -l`. Le comportement est celui de
  `den spawn <nest>` d'aujourd'hui, inchangé — seule l'écriture des repos à la volée bouge.
- **`den run <nest> <cmd> [args...]`** : crée-ou-rattache, puis la commande. C'est
  `den spawn <nest> -- <cmd>` d'aujourd'hui. Tout ce qui suit le nom du nest est la commande,
  verbatim, ses propres drapeaux compris.
- **`--` n'existe plus dans la famille.** Ni sur `up` (qui ne prend aucune commande), ni sur `run`
  (qui refuse le séparateur avec un remède, comme `den exec`).
- **Les repos à la volée passent derrière `--repo <path>`, répétable, sur les deux commandes.**

**`den run` n'est PAS le `run` éphémère de compose.** `docker compose run` fabrique un conteneur
jetable, à côté du projet, que `--rm` supprime à la sortie. den n'a pas cet objet : `den run` entre
dans LA sandbox du nest, la crée si elle n'existe pas, et la laisse vivante après. Divergence
assumée et nommée ici pour qu'un lecteur de compose ne la découvre pas à l'usage ; inventer une
sandbox jetable est hors périmètre (§10).

### Pourquoi `--repo` et pas `--`

Trois formes ont été pesées.

| Forme | Ce qu'elle coûte |
|---|---|
| **`--repo` répétable sur `up` et `run`** (retenue) | rupture sur les repos à la volée : `den spawn api ~/dev/hotfix` → `den up api --repo ~/dev/hotfix` |
| `run` garde `--` | `den run` et `den exec` continuent de se lire différemment — l'un EXIGE `--`, l'autre le REFUSE avec un message. C'est la divergence que cette tranche existe pour fermer, rendue permanente |
| césure : `up` positionnel, `run` avec `--repo` | un repo à la volée s'écrit de deux façons entre deux commandes sœurs. C'est la collision que den refuse ailleurs (`--workdir` épelé long partout, `enterSandbox` partagé), et `--only` / `--without` n'adresseraient qu'une des deux orthographes |

La forme retenue est celle de compose (mesure b), et elle rend `up` / `run` identiques à la paire
`shell` / `exec` de la tranche 1 : `Args` exactement 1 contre au moins 2, `SetInterspersed(false)`
d'un seul côté, aucun `--` nulle part. La rupture sur les repos à la volée est réelle et l'issue #72
l'autorise ; elle porte sur la voie la moins fréquentée de la commande.

Aucun raccourci d'une lettre pour `--repo`. compose épelle `-v`, que den ne peut pas reprendre sans
mentir (`-v` n'est pas un volume ici, et `-w` est déjà la worktree) ; inventer `-r` pour un drapeau
tapé rarement rouvrirait la collision de lettres que le §« `--workdir` reste épelé long » de la
tranche 1 refuse.

## 5. Matrice des drapeaux, et les refus

Le troisième état — **enregistré, toujours refusé** — n'est pas une invention : c'est le sort de `-T`
sur `den shell` (tranche 1, `internal/cli/shell.go:27-29`), au motif qu'un refus nommé vaut mieux
que `unknown flag`.

| Drapeau | `up` | `run` | Note |
|---|---|---|---|
| `--repo` (nouveau) | ✓ | ✓ | répétable, ordre conservé, `StringArrayVar` |
| `-w` / `--worktree`, `--as`, `--agent` | ✓ | ✓ | inchangés |
| `--only`, `--without`, `-i` | ✓ | ✓ | la contradiction `-i` × `--only`/`--without` est inchangée (`spawn.go:325`) |
| `--workdir` | ✓ | ✓ | reste épelé long, définitivement — `-w` est la worktree |
| `--detach` | ✓ (= `up -d` de compose) | enregistré, refusé | atteint la contradiction EXISTANTE `spawn.go:231` |
| `-T` / `--no-tty` | enregistré, refusé | ✓ | atteint la contradiction EXISTANTE `spawn.go:254` |
| `SetInterspersed(false)` | **non** | **oui** | |
| `Args` | un validateur à lui (voir plus bas) | ≥ 2 positionnels | |

**`up` n'arme pas `SetInterspersed(false)`**, et c'est une décision, pas un oubli : le
raisonnement de `internal/cli/shell.go:93-100` s'applique mot pour mot. `up` ne prend aucune
commande, donc aucun drapeau n'a de second propriétaire possible, et l'entrelacement achète une
chose — `den up api -T` atteint le refus NOMMÉ de `-T` au lieu d'être refusé pour son ARITÉ par
`exactlyOneArg`, message qui ne nomme ni le drapeau ni le remède.

### Le second positionnel de `den up`

`exactlyOneArg` (`internal/cli/root.go:263`) ne convient PAS ici, et c'est le seul endroit où cette
tranche ajoute un message plutôt que d'en déplacer un. Le geste que la rupture rend le plus probable
est la mémoire des doigts :

```
$ den up api ~/dev/hotfix
```

Sous `exactlyOneArg`, l'utilisateur lit « exactly one argument expected: 2 received — usage: … »
(`argsBetween`, `root.go:278-296`), qui ne nomme ni `--repo`, ni ce qui a changé. `den up` porte donc
son propre validateur, qui refuse tout positionnel au-delà du premier en NOMMANT la migration :

```
den up: extra arguments — ad-hoc repos go behind --repo now — write `den up --repo ~/dev/hotfix api`
```

Le remède est construit par le constructeur partagé du §5 (les drapeaux remontent à gauche du nom),
pas recollé à la main, et il entre dans `TestRunRemediesAreThemselvesLegal` au même titre que ceux de
`run`.

**Le même geste sur `den run` n'est PAS rattrapable, et c'est assumé.**
`den run api ~/dev/hotfix go test` donne au sandbox une commande `~/dev/hotfix go test`, qui échoue à
l'intérieur. den ne peut pas faire mieux sans deviner : distinguer un chemin d'un nom de programme
demanderait un `os.Stat` sur le premier jeton de la commande, c'est-à-dire exactement la
normalisation silencieuse que le §2 de la spec 2026-07-27 refuse. `den up` peut nommer la migration
parce qu'il n'a AUCUNE lecture pour un second positionnel ; `den run` en a une, légitime.

### Les refus de `den run`

Mêmes formes, même code que `den exec` :

```
$ den run api
den run: no command given — write `den run api go test`, or `den up api` for a shell

$ den run api --repo ~/dev/hotfix go test
den run: den's flags go before the nest name — write `den run --repo ~/dev/hotfix api go test`

$ den run api -- go test
den run: `--` is not needed — write `den run api go test`
```

`execShape` / `execRewrite` / `execLine` (`internal/cli/exec.go:57-213`) cessent d'appartenir à
`exec` et deviennent le constructeur de remèdes partagé. La propriété que la tranche 1 a durement
acquise — **un remède proposé est lui-même accepté par den** — vaut alors des deux côtés :
`TestExecRemediesAreThemselvesLegal` gagne son jumeau `run`. C'est la moitié « remède » qui avait
pourri sans qu'un `strings.Contains` s'en aperçoive ; elle ne doit pas pourrir deux fois.

### L'ensemble fermé devient dérivé

`execFlags` (`internal/cli/exec.go:39-44`) énumère quatre drapeaux à la main. `den run` en porte
quatorze orthographes (`-T`, `--no-tty`, `-w`, `--worktree`, `--as`, `--agent`, `--only`,
`--without`, `-i`, `--interactive`, `--detach`, `--workdir`, `--repo`, `--den-home`), plus les formes
`--x=valeur`. Une liste manuelle de quatorze entrées se désynchronise au premier drapeau ajouté, en
silence, et rouvre la panne de la mesure (c.4).

L'ensemble est donc **dérivé de `cmd.Flags()`** dans le validateur, où la mesure (d) montre que tout
est visible : nom long, raccourci, et `NoOptDefVal` pour savoir si le drapeau prend une valeur — la
donnée exacte que le champ `placeholder` d'`execFlag` porte à la main aujourd'hui.

Deux points que la dérivation doit écrire, faute de quoi elle change un comportement décidé :

- **`--help` et `-h` sont exclus explicitement.** La mesure 3 de la tranche 1 veut que
  `den exec api mytool --help` demande son aide à `mytool`, dans le sandbox. La liste manuelle les
  omettait par construction ; le walk les rend, donc l'exclusion devient une ligne de code et un test.
- **`exec` passe à la dérivation aussi.** Garder une liste manuelle d'un côté et un walk de l'autre
  serait deux mécanismes pour un contrat, sur les deux commandes que ce chantier existe à réconcilier.

## 6. Où passent les deux contradictions, et la migration

Deux refus de `internal/spawn/spawn.go` nomment `` `--` `` dans leur remède. Le séparateur
disparaissant, ces deux chaînes seraient des **conseils morts** le jour de la livraison — la panne
exacte que la tranche 1 a corrigée après revue (`den shell` proposait « donne une commande après
`--` » à une famille qui refuse `--`).

| Site | Aujourd'hui | Demain |
|---|---|---|
| `spawn.go:231` — `--detach` + commande | « --detach and a command after `--` contradict each other — drop one: … » | « …`den run` runs a command inside the sandbox — use `den up --detach <nest>` » |
| `spawn.go:254` — `-T` sans commande | « …give a command after `--`, or drop -T » | « …give a command with `den run -T <nest> <cmd>`, or drop -T » |

**Les deux refus restent atteignables tôt**, et c'est vérifié plutôt que supposé : ils sont à
l'étape 0 de `Spawn`, aux lignes 231 et 254, tandis que `config.LoadGlobal` est ligne 260 et
`source.Locate` juste après. `den up api -T` refuse donc sur `-T` sans lire une seule ligne de
configuration — un nest cassé ou une source absente ne peut pas voler le message.

**Aucun des deux contrôles ne déménage dans `internal/cli`.** `internal/spawn` possède la
contradiction entre champs de `spawn.Options` ; un second contrôle côté cobra serait deux sources
pour un verdict, ce que la tranche 1 a déjà refusé (`enterOptions`, « un probe porté mais jamais
consulté est la première moitié d'un second verdict »). Les deux drapeaux sont donc ENREGISTRÉS sur
la commande où ils n'ont pas de sens, et le refus qu'ils atteignent est celui qui existe déjà.

Le refus de `-T` de `internal/cli/shell.go:75-79` n'est pas touché : `den shell` entre dans une
sandbox vivante, il ne spawne pas, et sa contradiction porte sur la commande elle-même.

**La migration tient dans une ligne statique.** `spawn` → `up` est à distance d'édition 5, `spawn` →
`run` à 4, tous deux au-dessus du seuil 2 de `SuggestionsFor` : cobra ne suggérera rien sur
`den spawn api`. La ligne d'`unknownCommand` (`internal/cli/root.go`), qui porte déjà la migration
de 2026-08-05, absorbe la seconde :

```
`den <nest>` and `den spawn <nest>` no longer spawn: use `den up <nest>`, or `den run <nest> <cmd>`.
```

Elle reste **statique** : elle ne lit pas le den home, pour la raison du §4 de
`2026-08-05-spawn-command-design.md` — mettre une lecture de configuration faillible sur le chemin
d'erreur le plus banal du CLI.

## 7. Portée

Plus large que la tranche 1, qui tenait dans `internal/cli`.

| Fichier | Changement |
|---|---|
| `internal/cli/spawn.go` | supprimé → `up.go` + `run.go`, partageant un corps `spawnNest` comme `exec`/`shell` partagent `enterSandbox` |
| `internal/cli/exec.go` | `execFlags` manuel → ensemble dérivé de `cmd.Flags()` ; `execShape`/`execRewrite`/`execLine` deviennent partagés ; `spawnArgs` disparaît avec `--` |
| `internal/spawn/spawn.go` | deux chaînes de remède (§6). Aucun changement de logique |
| `internal/cli/root.go` | deux `AddCommand` au lieu d'un, ligne de migration |
| `internal/cli/source.go:40` | imprime `den spawn corp:<nest>` → `den up corp:<nest>` — la seule sortie de production qui nomme `spawn` |
| `internal/cli/testdata/unknown-command.golden` | à la main (il n'y a pas de `-update`) : ligne `spawn` retirée, `run` et `up` insérées dans l'ordre alphabétique, ligne de migration réécrite |
| `README.md` | tableau des commandes (l. 81), « Options of `den spawn` » (l. 98), et les exemples de repos à la volée (l. 168-171) qui passent tous à `--repo` |
| `CHANGELOG.md` | une entrée sous `Changed`, rupture assumée, sans fenêtre de dépréciation |
| `CLAUDE.md`, spec `2026-07-27-den-cli-design.md` §5/§6/§11 | les mentions de la forme de commande |

Les 49 occurrences de `den spawn` dans le dépôt (hors `.claude/worktrees/`, qui double chaque
grep) se relisent une à une : la plupart sont des commentaires nommant un geste utilisateur, et une
seule est une sortie (`source.go`).

`internal/sbx`, `internal/nest`, `internal/manifest`, `internal/policy`, `internal/worktree` :
intacts. Ce chantier porte sur l'endroit où les arguments entrent, pas sur ce qu'un spawn fait.

## 8. Tests

**Qui s'inversent ou déménagent** — ce qu'une revue doit regarder :

| Aujourd'hui | Demain |
|---|---|
| `TestSpawnRefusesNoTTYWithNoCommand` | même nom sur `up` ; assertion sur le message ENTIER, qui doit nommer `den run` |
| `TestSpawnRefusesDetachWithACommand` | déménage sur `run` ; message ENTIER, qui doit nommer `den up --detach` |
| `TestSpawnWithoutANestNamesTheUsageLine` | se scinde : `up` (arité) et `run` (« no command given ») |
| les tests de repos positionnels de `spawn_test.go` | réécrits en `--repo`, dont un qui garde l'ORDRE de frappe sur deux `--repo` |

L'assertion sur le message **entier** n'est pas du zèle : la tranche 1 a livré un remède mort parce
qu'un `strings.Contains(err, "-T")` ne regardait pas la moitié qui avait pourri.

**Nouveaux** :

- `TestRunPassesTheCommandsOwnFlagsThrough` — `den run api go test -v` ; garde la mesure (c) ;
- `TestRunPassesHelpToTheSandbox` — `den run api mytool --help` ; garde l'exclusion explicite du §5,
  qui est la plus facile à casser sans s'en apercevoir en passant à la dérivation ;
- `TestRunRefusesDenFlagsAfterTheNestName` — `--repo`, `-T`, `--` ;
- `TestRunRemediesAreThemselvesLegal` — le jumeau de la propriété de la tranche 1 ;
- `TestUpKeepsInterspersedFlags` — `den up api -T` atteint le refus nommé, pas l'erreur d'arité ;
- `TestUpNamesTheRepoFlagOnASecondPositional` — `den up api ~/dev/hotfix` ; message ENTIER, le
  remède doit nommer `--repo` et rester légal ;
- `TestRepoFlagDoesNotSplitOnComma` — garde la mesure (c.2), c'est-à-dire le choix
  `StringArrayVar` contre `StringSliceVar`, invisible à la lecture ;
- `TestUpStillReadsDenHomeBeforeTheSubcommand` — garde la mesure (c.3) sur la persistante.

Les conventions du dépôt tiennent sans aménagement : aucun `t.Parallel()`, aucun socket, aucun
processus, `sbx.Fake` suffit, et `worktree.NeutralizeGitEnvironment()` reste appelé dans le
`TestMain` de `cli`.

## 9. Divergences assumées

1. **`--workdir` contre le `-w` de compose.** Permanente, héritée de la tranche 1 : `-w` est la
   worktree de den, et le sens compose (workdir) ne peut pas l'avoir.
2. **`den run` n'est pas éphémère** (§4). compose jette son conteneur, den garde sa sandbox.
3. **Pas de `-v` pour `--repo`.** compose épelle `-v` un volume ; den monte des repos, et un
   raccourci d'une lettre coûterait une collision pour un drapeau rare.
4. **`--only` / `--without` n'adressent toujours QUE les repos déclarés.** Le §6 de
   `2026-08-04-adhoc-repos-design.md` le décidait pour des positionnels (« un repo à la volée se
   retire en ne le tapant pas ») ; l'argument ne dépend pas de l'orthographe et vaut mot pour mot
   pour `--repo`. Rien ne change non plus dans la fusion des listes ni dans `checkUniqueNames`, qui
   garde l'unicité des basenames. Écrit ici parce qu'un drapeau, contrairement à un positionnel,
   INVITE la question — et que l'inventer au moment du plan serait un élargissement silencieux.
5. **`den run -d` n'existe pas.** compose a `run -d` (détacher LA COMMANDE). den a
   `den up --detach`, qui est le `up -d` de compose. Hors périmètre, nommé pour qu'un lecteur ne le
   prenne pas pour un oubli.

## 10. Hors périmètre

- Une sandbox jetable (`--rm`) : den n'a pas cet objet, et l'inventer est un chantier à soi seul.
- `den run -d` (§9.4).
- Le sort de `--agent` (issue #50), porté à l'identique comme en 2026-08-05.
- Les noms `den agent` / `den review` réservés par la spec 2026-07-27 §5 : ce chantier libère la
  place de `spawn` dans cette famille, il ne décide rien de ce qu'ils deviendront.
