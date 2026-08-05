# Design — `den spawn`, le spawn redevient une sous-commande

**Date :** 2026-08-05
**Auteur :** Nicolas Gaignoux (conception assistée)
**Statut :** validé en brainstorming, prêt pour le plan d'implémentation
**Amende :** spec 2026-07-27 (`den-cli-design.md`) §5, §6, §11
**Version cible :** v1.3.0 — breaking, mais den n'a pas encore d'utilisateur ; pas de shim, pas de
fenêtre de dépréciation, pas de v2.

---

## 1. Le problème

La spec 2026-07-27 §5 fait du root lui-même la commande de spawn : `den <nest> [repo...]`. Le
premier argument est un nom de nest sauf s'il matche une sous-commande. `internal/cli/spawn.go`
implémente ça en posant `RunE`, `Args` et six flags **sur le root**.

Trois défauts, du plus concret au plus structurel.

**a) Un flag avalé en silence.** Les six flags du spawn (`-w`, `--agent`, `--without`, `--only`,
`--detach`, `-i`) vivent sur `root.Flags()`. `den --help` rend donc une liste de commandes suivie de
six flags sans propriétaire — le lecteur ne peut pas savoir à quelle commande ils s'appliquent. Pire,
`den --detach` sans argument tombe sur la branche `len(args) == 0 → cmd.Help()` : den imprime l'aide,
sort 0, et **le flag disparaît sans un mot**. C'est le §2 de la spec 2026-07-27 — « den refuse plutôt
que de normaliser en silence » — violé sur la surface la plus visible du CLI.

**b) Aucun endroit où dire ce que den sait faire.** Tout premier argument inconnu est un nom de nest
valide par construction : il n'existe pas de token qui puisse produire « ceci n'est pas une commande,
voici les contrats ». `den api` rend une erreur de résolution de nest, jamais une liste de commandes.
La spec §11 avait accepté ce prix ; `withSuggestion` (`internal/cli/spawn.go:95-129`) est la
rustine — elle greffe un « did you mean » sur l'échec de résolution, avec 35 lignes de commentaire
pour expliquer pourquoi elle ne peut pas faire mieux.

**c) Un trou de contrat que den ne sait qu'annoncer.** `TestSubcommandsStayPriority` verrouille la
priorité des sous-commandes, `TestANestHomonymOfASubcommandSpawnsNormally` vérifie qu'un nest nommé
`doctr` spawne. Les deux sont vrais ensemble, et leur conjonction dit : **un nest nommé `ls`, `sh`,
`rm`, `build`, `doctor`, `init`, `nest`, `ports`, `source`, `lint`, `version`, `help` ou
`completion` est injoignable à vie.**

den le sait et le dit : `warnAboutShadowedNests` (`internal/cli/nest.go:143`) compare chaque nest
aux noms **et aux alias** de `root.Commands()` et avertit sur stderr — « nest "ls" is shadowed by
the `den ls` subcommand … Rename it to be able to spawn it ». Une fonction, un appel
(`nest.go:76`), quinze lignes de commentaire et deux tests (`nest_test.go:173`, `:210`) dont le seul
objet est de **s'excuser** d'une collision que den ne peut pas résoudre. C'est le meilleur argument
pour ce chantier : le §5 ci-dessous ne rend pas le message plus aimable, il supprime la collision et
tout ce qui la commentait.

S'ajoute la complexité de lecture : `NewRootCmdWith` porte un commentaire « LAST : configureSpawn
pose Args sur le root, ce qui n'a de sens qu'une fois toutes les sous-commandes enregistrées »
(`root.go:150`), une contrainte d'ordre invisible qu'un `AddCommand` mal placé casse en silence.

---

## 2. La décision

**Le spawn devient une sous-commande ordinaire. La forme nue `den <nest>` disparaît.**

```
den spawn <nest> [repo...] [-w <wt>] [--without r] [--only r] [-i] [--agent a] [--detach]
```

Le nom `spawn` n'est pas un choix de goût : le vocabulaire est verrouillé depuis la spec 2026-07-27.
Son §2 dit « **objet spawnable** … on le **spawn** », son §6 s'intitule « Data flow du spawn », le
paquet s'appelle `internal/spawn`. Et son §5 réserve `den agent <nest> [ticket]` et
`den review <name>`, tous deux preneurs de nest : `spawn` / `agent` / `review` est la famille
cohérente. `den up` (idiome compose) ment sur la sémantique — c'est un spawn-**or-attach**, pas un
démarrage — et `den open` entre en collision avec `ports --open` / `deps.Open`.

Aucune dépréciation. den n'a pas encore d'utilisateur : porter deux contrats pendant N versions
coûterait plus cher que la rupture ne rapporte.

---

## 3. Surface après changement

| Commande | Rôle |
|---|---|
| `den spawn <nest> [repo...]` | **spawn-or-attach** + shell — inchangé quant au comportement |
| toutes les autres | inchangées |

`den` sans argument : imprime l'aide, sort 0. Inchangé.

Les six flags du spawn passent de `root.Flags()` à `spawnCmd.Flags()`. Conséquence directe et
voulue : `den --detach` devient `unknown flag: --detach`, sortie non-zéro. Le trou (a) est fermé par
construction, pas par un test qui surveille une politesse.

`Args: atLeastOneArg` sur `den spawn`. Le validateur existe déjà (`root.go:242`) et son commentaire
— la branche « trop d'arguments » est inatteignable parce que les arguments au-delà du premier sont
des repos, sans plafond — reste exact.

`root.Use` revient à `"den"`. Il vaut déjà ça (`root.go:102`) et n'était écrasé en
`"den <nest> [repo...]"` que par `configureSpawn` ; supprimer l'écrasement suffit. Conséquence à ne
pas découvrir dans un golden : `argsBetween` rend `cmd.UseLine()`, donc les erreurs d'arité du spawn
citent désormais `den spawn <nest> [repo...]` — plus précis qu'avant, mais le texte change.

---

## 4. Le refus, et pourquoi il n'est pas gratuit

L'intuition « on retire `root.Args`, cobra reprend la main » est **fausse à moitié**, et l'écrire ici
évite qu'un lecteur la retente.

Avec `root.Args == nil`, cobra rétablit bien `legacyArgs`, qui rend
`unknown command "api" for "den"` suivi de ses suggestions. Mais **il ne rend pas la liste des
commandes** : le root porte `SilenceUsage: true`, et cobra ne rend l'usage que si ni la commande ni
le root ne le taisent. Lever `SilenceUsage` sur le root le lèverait pour **toutes** les
sous-commandes — un `den spawn api` qui refuse sur une stack cassée dumperait l'usage complet
par-dessus son diagnostic, ce qui est exactement la raison pour laquelle le flag a été posé.

Or la liste des contrats est la moitié de ce que ce chantier existe pour donner.

**Donc `root.Args` reste non-nil**, mais cesse d'être `ArbitraryArgs` : il devient `unknownCommand`,
un validateur qui rend le message complet. Deux contraintes cobra le gouvernent, toutes deux
vérifiées dans la source de cobra plutôt que supposées :

1. `Find` n'appelle `legacyArgs` **que** si `commandFound.Args == nil`. Un `Args` non-nil sur le root
   remplace donc le message cobra par le nôtre — c'est le but.
2. `execute()` teste `!c.Runnable()` **avant** `ValidateArgs`. Un root sans `RunE` renverrait
   `flag.ErrHelp` et notre validateur ne tournerait jamais : `den api` imprimerait l'aide et sortirait
   **0**. Le root **garde donc un `RunE`**, réduit à `cmd.Help()` pour zéro argument — branche
   atteinte seulement quand le validateur a laissé passer, c'est-à-dire `len(args) == 0`.

Sortie de `den api` :

```
den: unknown command "api"

Commands:
  build     Build stack images, in dependency order
  doctor    Diagnose den's configuration and environment
  init      Create a den home from the shipped example
  lint      Validate a source checkout (stacks, nests, references, confinement)
  ls        List live sandboxes
  nest      Inspect the declared nests
  ports     Show where a sandbox's declared ports land on the host
  rm        Destroy a sandbox (the agent profile persists)
  sh        Open a shell in an existing sandbox
  source    Manage team source repositories (stacks/nests shared over git)
  spawn     Spawn or attach a nest's sandbox
  version   Print den's version

`den <nest>` no longer spawns: use `den spawn <nest>`.
Run `den help <command>` for details.
```

Trois propriétés de ce message, chacune décidée :

- **La liste vient de `root.Commands()`**, jamais d'une constante. Une commande ajoutée demain y
  apparaît sans que personne y pense — c'est déjà le raisonnement qui gouverne les candidats de
  `withSuggestion`.
- **La ligne de migration est statique.** Elle ne consulte pas le den home. Un refus qui lirait
  `nests/api.yaml` pour dire « c'est un nest, tapez `den spawn api` » serait plus aimable, et a été
  rejeté : il met une lecture de configuration — donc un `config.Home` faillible, donc une seconde
  classe d'erreur — sur le chemin d'erreur le plus banal du CLI. La ligne fixe porte toute la
  migration pour un coût nul.
- **`den doctr` garde son « did you mean »**, via `root.SuggestionsFor` — la fonction que
  `withSuggestion` appelait déjà. Ce qui disparaît est la greffe sur l'échec de résolution de nest,
  pas la suggestion elle-même. Et elle redevient exacte : elle répond désormais à « quelle commande
  voulais-tu », alors qu'elle répondait à « ce nest n'existe pas, et au fait ».

**Un flag inconnu gagne sur la commande inconnue.** `cobra.execute()` appelle `ParseFlags` — donc
`FlagErrorFunc` en cas d'échec — **avant** `ValidateArgs`. Sur `den api --detach`, l'utilisateur lit
donc `unknown flag: --detach`, pas `unknown command "api"`. C'est accepté et non corrigé : les deux
sont des refus non-zéro, et faire gagner l'argument exigerait de désactiver le parsing de flags sur
le root, ce qui coûterait `--den-home` et `--help`. Écrit ici pour qu'un lecteur du message ci-dessus
ne le croie pas universel.

`root.SuggestionsMinimumDistance = 2`, posé explicitement dans `configureSpawn` avec le commentaire
« cobra ne l'applique pas sur ce chemin », **reste nécessaire** : `unknownCommand` appellera
`SuggestionsFor` directement, exactement comme `withSuggestion` le faisait, sans passer par
`findSuggestions()` qui porte le défaut de 2. À 0, `SuggestionsFor` ne rend que les préfixes et
`den doctr` ne suggère plus rien. **La ligne déménage dans `NewRootCmdWith`**, à côté des
assignations `Use` / `SilenceUsage` du root — elle ne part pas avec `configureSpawn`. Un implémenteur
qui la supprime avec le reste ne casse rien de visible : seul
`TestUnknownFirstArgumentSuggestsTheCloseCommand` (§7) le rattrape, et une spec ne doit pas confier
un fait de câblage à un test.

---

## 5. Ce que le changement débloque

Un nest homonyme d'une sous-commande redevient joignable : `den spawn ls` spawne le nest `ls`. Le
trou (c) se ferme **sans ajouter la moindre validation** — l'ambiguïté n'existait que parce que les
deux espaces de noms partageaient la première position.

`TestANestHomonymOfASubcommandSpawnsNormally` devient donc une assertion strictement plus forte :
elle passe de `doctr` (un nom qui *ressemble* à une commande) à `ls` (un nom qui **est** une
commande), cas que la forme nue ne pouvait pas servir.

**`warnAboutShadowedNests` disparaît** (`internal/cli/nest.go:132-159`), avec son appel
(`nest.go:76`) et ses deux tests (`nest_test.go:173`, `:210`). Un avertissement qui dit « renomme ce
nest, tu ne pourras jamais le spawner » n'a plus de référent : `den spawn ls` le spawne. Le laisser
serait pire que du bruit — il donnerait un conseil faux.

La contrainte d'ordre dans `NewRootCmdWith` disparaît : `newSpawnCmd` s'enregistre comme les autres,
`unknownCommand` se pose sur le root indépendamment.

---

## 6. Architecture

`configureSpawn(root, denHome, deps)` → `newSpawnCmd(denHome, deps) *cobra.Command`, dans la forme
de `newLsCmd` / `newShCmd` : `deps` reste un **paramètre**, jamais construit dans la fonction, parce
que c'est ce qui rend le câblage flag → `spawn.Options` vérifiable sans `sbx` réel.

Le corps du `RunE` ne change pas : il pose `o.Nest = args[0]`, `o.Repos = args[1:]` bruts (la
résolution de chemin reste du côté `nest.Resolve`, hors de cobra), résout `config.Home`, copie
`deps` localement pour y poser `Out`/`Err`/`In` depuis la commande — la règle « ces trois-là seuls
dépendent de la commande, donc du `SetOut` d'un test » est inchangée — et appelle `spawn.Spawn`.
Ce qui disparaît est l'enveloppe `withSuggestion` autour du retour.

`withSuggestion` est supprimée entièrement, avec ses 35 lignes de commentaire. Y compris la branche
« une référence de source ne suggère jamais une sous-commande », qui n'avait de raison d'être que
parce qu'un nom de nest et un nom de commande partageaient la première position.

Rien ne bouge hors de `internal/cli`. `internal/spawn`, `nest`, `config`, `policy`, `manifest`,
`source` ne voient pas ce changement : il porte sur l'endroit où vivent les commandes, pas sur ce
qu'un spawn fait.

---

## 7. Tests

**Supprimés** (`internal/cli/nest_test.go`) — les deux tests de l'avertissement de collision :
`TestNestLsWarnsAboutNestsShadowedByASubcommand` et son pendant « stderr reste vide sans
collision ». Ils gardaient une propriété qui cesse d'exister.

**Supprimés** (`internal/cli/spawn_test.go`) — les six tests de `withSuggestion` :
`TestATypoOnASubcommandIsSuggested`, `TestAFarNameSuggestsNothing`,
`TestANestThatExistsButIsUnreadableSuggestsNothing`, `TestTheSuggestionOnlyConcernsTheTypedName`,
`TestASourceReferenceNeverSuggestsASubcommand`,
`TestATypoOnASubcommandIsStillSuggestedWithPositionals`. La propriété qu'ils gardaient — « une faute
de frappe sur une commande est suggérée » — est reprise par le test de `unknownCommand` ci-dessous,
sur un chemin où elle est vraie sans condition.

**Transposés** : tout appel qui passait un nom de nest en premier argument prend `spawn` en tête —
`spawn_test.go`, `hostile_test.go`, `root_deps_test.go`. `TestSubcommandsStayPriority` perd son
objet (il n'y a plus de compétition) et est remplacé par le test de nest homonyme ci-dessous.

**Renforcés** : `TestANestHomonymOfASubcommandSpawnsNormally` cible `ls` au lieu de `doctr`, et
vérifie qu'un `create` porte bien le nom `ls`.

**Inversé** : `TestUnknownFirstArgumentIsANestNotFound` (`root_test.go:194`) devient
`TestUnknownFirstArgumentListsTheCommands` — comparaison **golden**, parce que le message est une
sortie utilisateur multi-lignes et que c'est la convention du dépôt (`internal/*/testdata/*.golden`,
comparés à la main, pas de `-update`).

**Nouveaux** :

- `TestUnknownFirstArgumentSuggestsTheCloseCommand` — `den doctr` nomme `den doctor`.
- `TestASpawnFlagOnTheRootIsRefused` — `den --detach` rend `unknown flag`, sortie non-zéro. C'est le
  défaut (a) verrouillé : sans ce test, un futur `PersistentFlags` le rouvrirait sans bruit.
- `TestSpawnWithoutANestNamesTheUsageLine` — `den spawn` seul passe par `atLeastOneArg`.

Les conventions du dépôt tiennent : aucun `t.Parallel()`, aucun socket, aucun processus ;
`worktree.NeutralizeGitEnvironment()` reste appelé dans le `TestMain` de `cli`.

---

## 8. Documentation à amender

| Document | Ce qui change |
|---|---|
| `docs/superpowers/specs/2026-07-27-den-cli-design.md` §5 | la ligne `den <nest> [repo...]` du tableau devient `den spawn <nest> [repo...]` |
| idem §6 | titre « Data flow du spawn — `den <nest> [-w <wt>] …` » et ses occurrences dans le corps |
| idem §11 | les deux lignes du tableau qui nomment `den <nest>` (sandbox arrêtée sous `--detach`, spawn-or-attach) |
| `README.md` | l. 81 (tableau des commandes), 97 (« Options of `den <nest>` »), 177, 295, 361, 372 |
| `CLAUDE.md` | la mention `den <nest> [repo...]` de la section « What this is » |
| `CHANGELOG.md` | l'entrée v1.3.0 (voir ci-dessous) ; la ligne v1.2.0 « Repos on the fly : `den <nest> [repo...]` » est **historique**, elle ne se réécrit pas |

**Un message de production nomme la forme nue**, et il est le seul :
`internal/cli/source.go:40` imprime après un `den source add` réussi « its objects are addressed
`<n>:<name>` (e.g. `den <n>:<nest>`) » → devient `den spawn <n>:<nest>`. `README.md:390` cite cette
sortie et suit. Les autres occurrences de `den <nest>` dans le code (`internal/sbx/ls.go`,
`internal/config/*.go`, `internal/worktree/worktree.go`, `internal/agent/gate.go`,
`internal/cli/nest.go`) sont des **commentaires** : elles nomment un geste utilisateur qui change de
nom, donc elles se mettent à jour aussi, mais aucune n'est une sortie.

**Le changelog porte la rupture.** Livrer un breaking en **mineure** est légitime — den n'a pas
encore d'utilisateur — mais c'est alors la seule trace qu'un lecteur aura. L'entrée v1.3.0 doit dire
que `den <nest>` a disparu et nommer `den spawn <nest>`, comme le fait la ligne de migration du §4.
Même exigence, autre artefact.

Le §11 de la spec 2026-07-27 garde son analyse de l'identité par le nom : elle porte sur les noms de
sandbox, que ce changement ne touche pas. Seules ses mentions de la **forme de commande** bougent.

---

## 9. Hors périmètre

Le sort de `--agent` — faut-il monter **tous** les profils d'agent plutôt qu'un seul, sachant que
l'installation de claude/codex est décidée par l'image de stack et non par den — est une vraie
question, ouverte en **issue #50**. Elle touche §9/§9.1/§9.2, le mixin généré et le contrat des
stacks. Ce chantier-ci porte `--agent` à l'identique.

Aucun refus n'est ajouté sur les noms de nest homonymes de sous-commandes : le §5 les rend
joignables, il n'y a plus rien à refuser.
