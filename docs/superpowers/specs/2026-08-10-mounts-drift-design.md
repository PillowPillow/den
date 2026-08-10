# `mounts:` — détecter la dérive d'un mount sans lien et d'un `ro:` retourné

Date : 2026-08-10
Issue : [#56](https://github.com/PillowPillow/den/issues/56)
Statut : conception validée, implémentation à faire

## Le problème

`den` avertit d'une dérive de mixin à l'attache : on édite la configuration, on ré-attache, et den
signale que la VM vivante porte toujours ce qu'elle a reçu à sa création. Pour `mounts:`, cette
comparaison porte sur **l'argv de la phase de lien** (`internal/agent/mixin.go:31`, comparé à
`internal/agent/drift.go:149`).

Deux éditions de `mounts:` n'atteignent jamais cet argv, et dérivent donc en silence total :

1. **Un mount sans `link:`.** Légitime et supporté — la sandbox atteint le chemin à son emplacement
   hôte, ce que veulent les consommateurs par variable d'environnement. `LinkCommand` filtre ces
   entrées (`internal/agent/links.go:30-34`), donc en ajouter, en retirer ou en repointer une ne
   change rien dans `Links`.
2. **Un retournement `ro:` ↔ `rw:`.** Le read-only est un drapeau de `sbx create`, ajouté en suffixe
   `<host>:ro` à l'argv des workspaces (`internal/spawn/spawn.go:611-618`). Il n'apparaît jamais
   dans le shell de boot.

Dans les deux cas le mount est fixé à la création : l'édition n'a réellement aucun effet avant un
respawn. Ce silence est exactement le mode de défaillance que l'avertissement de dérive existe pour
supprimer — voir le commentaire-pourquoi de `internal/agent/drift.go:150-153`.

## La prémisse de l'issue est falsifiée — mesure du 2026-08-10

L'issue posait deux domiciles possibles pour la donnée manquante, « ni l'un ni l'autre gratuit » :
une clé nouvelle dans le YAML du mixin (tolérance de `sbx` **non mesurée**), ou un enregistrement
dans `internal/manifest`.

Il en existe un troisième, et il est presque gratuit : **la VM elle-même**. `sbx ls --json` rapporte
`workspaces`, suffixe `:ro` compris, et den tient déjà les deux côtés au moment de l'attache — les
`workspaces` recalculés depuis la configuration, et `live.Workspaces` lus sur la VM. C'est la
technique de source primaire que `reportMissingGitDirs` et `reportUnmountedRepos` emploient déjà.

Le suffixe `:ro` était l'inconnue. Le §14 relevait la *forme* du schéma (`["/p","/p:ro"]`,
2026-07-28) sans qu'aucun relevé ne porte sur un mount réellement read-only, et le
`strings.TrimSuffix` de `Sandbox.Workdir` n'en est pas une preuve : `workspaces[0]` est toujours le
premier repo, et `:ro` est refusé sur un positionnel (spec 2026-08-04 §5) — ce `TrimSuffix` est
défensif, pas un témoignage.

**Mesuré le 2026-08-10, sbx v0.37.1**, sur une sandbox jetable créée puis détruite :

```
sbx create shell <…>/probe-rw <…>/probe-ro:ro --name den-probe-ro
sbx ls --json
  → "workspaces": ["<…>/probe-rw", "<…>/probe-ro:ro"]
sbx rm --force den-probe-ro
```

Le suffixe **fait l'aller-retour**. Les deux cas de l'issue sont donc lisibles depuis la source
primaire, sans nouvel enregistrement, sans version de schéma à faire bouger, et la question non
mesurée (« `sbx` tolère-t-il une clé inconnue dans un manifeste de kit ? ») devient sans objet.

**À noter au passage** : `sbx version` répond `v0.37.1` sur cette machine, alors que tout le relevé
du §14 date de v0.35.0. Le reste du relevé est à re-mesurer, hors périmètre de ce changement.

## Décision — comparer à la VM, pas à un enregistrement

Une fonction `reportUnmountedMounts` dans `internal/spawn/spawn.go`, sur la branche d'attache, après
`reportUnmountedRepos`. Entrées : `live.Workspaces` et `r.Mounts`.

Elle **avertit**, ne refuse jamais et ne recrée jamais — la doctrine de ses trois sœurs. Refuser
casserait un `den spawn` qui marchait hier sur une édition YAML anodine ; recréer détruirait un
travail en cours dans la VM.

### La différence tranchante avec les sœurs : ne pas retirer le `:ro`

`reportMissingGitDirs` (`spawn.go:1358`) et `reportUnmountedRepos` (`spawn.go:1420`) retirent tous
deux le suffixe `:ro` avant de comparer, parce que pour un repo c'est une option de montage et non
une partie du chemin.

Cette fonction-ci ne doit **pas** le retirer : le suffixe **est** le bit `ro:` sous test. La
normalisation décrite juste en dessous porte donc sur la partie chemin seulement, et rattache le
suffixe tel qu'elle l'a trouvé.

### Normaliser au point de comparaison, jamais au chargement

`normalizeWorkspace` passe **les deux** côtés — ce que la VM rapporte et ce que `mountWorkspace`
rend — par un `filepath.Clean` de la partie chemin. La comparaison est donc insensible à
l'orthographe (`/p/`, `//p`, `/p/./q`), sans que rien d'autre ne bouge.

Nettoyer `mounts[].host` et `ssh.dir` **au chargement** (`config.LoadGlobalUnvalidated`) est la
correction qui paraît la plus propre, elle a été écrite sur cette branche, et elle a été retirée.
Cette chaîne n'alimente pas que l'argv de `sbx create` : elle alimente aussi `agent.LinkCommand`,
dont la sortie est enregistrée dans le mixin au `sbx create` et **n'est jamais réécrite** sur une
sandbox vivante — den ne réapplique rien à une VM qui tourne. Toute sandbox créée avant la mise à
jour comparerait donc sa phase de lien enregistrée, telle que tapée, à une phase fraîchement
nettoyée, et signalerait `link phase changed` à **chaque** attache, indéfiniment, avec `sbx rm
--force` pour remède — un avertissement permanent dont le remède détruit une VM qui porte du
travail non commité. Second coût : `filepath.Clean` est purement **lexical**, donc nettoyer
`/a/current/../shared` au chargement fait monter à den un autre répertoire que celui que l'OS
résout dès que `current` est un lien symbolique. Et rien n'était cassé côté argv : `sbx create
/Users/me/docs/` fonctionne.

Les deux côtés sont normalisés, pas seulement celui de den. **Mesuré le 2026-08-10** (§14.0,
sondes) : `sbx` normalise `workspaces` **lexicalement** — un slash final donné à `create` ne
ressort pas de `ls --json`. Le côté VM est donc toujours déjà propre et le côté configuration est
le seul à pouvoir être sale ; normaliser les deux reste néanmoins ce qui est écrit, parce que c'est
correct sans dépendre de cette mesure et que ça le reste si `sbx` change d'avis.

Aucune résolution de lien symbolique, et ce n'est plus une prudence mais une **mesure** (§14.0,
même sonde) : `sbx` rend `/tmp/x` verbatim, jamais `/private/tmp/x`, alors que `/tmp` est un lien
symbolique sur macOS. Sa normalisation a donc exactement la sémantique de `filepath.Clean`.
`filepath.EvalSymlinks` côté den **divergerait** de sbx au lieu de l'affiner — et toucherait en
plus le disque sur un chemin d'avertissement, ajoutant un mode de défaillance le jour où le
répertoire hôte a disparu, exactement le jour où cet avertissement vaut le plus.

### Une seule définition de l'orthographe

Un helper `mountWorkspace(m nest.Mount) string` rend `m.Host`, suffixé `:ro` quand `m.RO`. Il est
appelé **par la boucle d'argv du point 4** (`spawn.go:611-618`) *et* par le rapport.

Deux copies de `host + ":ro"` divergeraient un jour, et l'avertissement se déclencherait alors à
**chaque** attache sans que rien n'ait bougé — un avertissement permanent cesse d'être lu, y compris
le jour où il dit vrai. C'est la leçon déjà payée par `stringNode` (`mixin.go:160-179`).

### Trois verdicts par mount configuré, pour que le message ne mente pas

| état sur la VM | ligne |
|---|---|
| chemin équivalent présent, même suffixe (comparaison normalisée) | silence |
| même hôte, autre suffixe | ``- /p (mounts[2]) is mounted read-only, but `mounts:` now says read-write`` |
| hôte absent | ``- /p (mounts[2]) is not mounted`` |

Le cas du retournement mérite sa propre ligne. Dire « n'est pas monté » d'un répertoire qui **est**
monté, en lecture seule, est une affirmation fausse que den imprimerait à chaque attache. Les deux
directions du retournement sont couvertes, chacune nommant celle qu'elle a lue.

`Key` est nommé (`mounts[2]`, `ssh.dir`) : les erreurs de den nomment la clé à corriger, et les refus
de `LinkCommand` le font déjà (`links.go:79`).

**L'en-tête aussi est dérivé des `Key`**, jamais figé sur `mounts:`. Un utilisateur en `ssh: {mode:
mount}` sans aucun bloc `mounts:` serait sinon envoyé vers une clé absente de son `config.yaml` —
le défaut même que `Mount.Key` existe pour empêcher, réintroduit une ligne au-dessus de celle qui
le corrige. Trois formes : `mounts:` seul (le cas courant, formulation inchangée), `ssh.dir` seul,
et « `mounts:` and `ssh.dir` now say » quand les deux sont présents. La constante est
`nest.SSHDirKey` : deux orthographes en laisseraient passer une (même règle que
`config.SSHLinkTarget`).

**La ligne de retournement garde `mounts:`, faute ouverte.** Pour une entrée `ssh.dir` elle envoie
toujours vers un bloc que l'utilisateur peut ne pas avoir. La substituer par `ssh.dir` serait pire :
`resolveMounts` fixe `RO: false` lui-même (« ssh écrit known_hosts »), donc la clé `ssh.dir` ne dit
**rien** sur le bit `ro:` et « `ssh.dir` now says read-write » attribuerait à une clé de
configuration une affirmation qu'elle ne porte pas. Nommer la source honnête ici demande une
décision de formulation, pas une substitution.

### Couverture gratuite : `ssh.dir`

Le sucre `ssh.mode: mount` se désucre en une entrée ordinaire de `mounts:` (`nest.resolveMounts`).
Éditer `ssh.dir` sur une sandbox vivante est donc couvert par le même code, sans une branche de plus.

### Le recouvrement avec la ligne de dérive `Links` est gardé, délibérément

Ajouter un mount **avec** un `link:` déclenche deux lignes : `link phase changed` (depuis
`Differences`) et `is not mounted` (la nouvelle).

Elles ne sont pas dédoublonnées, parce qu'elles ne répondent pas à la même question, et surtout
parce que `Links` reste le **seul** détecteur d'une édition qui ne touche que le lien (même hôte,
`link:` nouveau) : aucune comparaison de workspaces ne peut la voir, l'hôte n'ayant pas bougé.
À écrire au site de décision.

## Hors périmètre, délibérément

**Un mount retiré de la configuration.** `live.Workspaces` est plat : repos, git dirs, profil
d'agent et mounts y sont indiscernables. Un « présent sur la VM, absent de la configuration » se
déclencherait donc aussi sur un worktree déplacé, un repo retiré et un `--agent` retourné —
indiscernables, et déjà couverts par `reportNestChangedSinceCreation` pour le côté repos.

Distinguer demanderait un enregistrement des mounts dans `internal/manifest`, ce que la conception
des mounts a refusé (`2026-08-07-mounts-design.md:253-259`) et ce qui poserait en plus la question du
schéma : `manifest.decode` refuse tout `Schema` différent de 1, donc passer à 2 rendrait **tous** les
manifestes existants inutilisables et ferait retomber `den rm` sur la dérivation — la dégradation
même que ce paquet existe pour empêcher.

Le prix accepté : après avoir retiré une entrée de `mounts:`, la VM vivante continue d'exposer le
répertoire hôte, sans que den le dise. Le mount reste inerte (rien ne le lie), et l'édition prend
effet au prochain create.

**Le nettoyage des chemins de `repos:` — d'abord jugé hors périmètre, puis REPRIS.** Il avait été
écarté comme « sans objet une fois le nettoyage au chargement retiré » : c'est vrai du chemin des
mounts, faux de celui des repos, et la sonde A tranche. `reportUnmountedRepos` construit son
ensemble `present` depuis l'écho de sbx — **normalisé lexicalement** (§14.0) — et le compare à
`expected[i]`, qui vient de `repos:` et n'est jamais nettoyé : `config.LoadGlobalUnvalidated` et
`nest.LoadNest` ne font qu'`ExpandPath`, et l'asymétrie est déjà écrite noir sur blanc à
`nest/repos.go:53-59` — un chemin de **ligne de commande** EST nettoyé par `parseRepoArg`, un
chemin **déclaré** ne l'est pas.

Donc `repos: {api: ~/dev/api/}` imprimait **deux** lignes fausses à chaque attache : « is not
mounted » d'un repo qui l'est, et — `movedStart` comparant le workdir normalisé de la VM à
`expected[0]` non normalisé — « the shell starts in … » nommant le répertoire où le shell se
trouve déjà. Corrigé au même endroit et de la même façon : normalisation des deux côtés **à la
comparaison**. Deux différences avec le chemin des mounts, toutes deux tenues : ici le suffixe
`:ro` reste **retiré** (c'est une option de montage, pas le bit sous test), donc le chemin est
nettoyé APRÈS le `TrimSuffix` au lieu de passer par `normalizeWorkspace` qui le réattache ; et le
garde « workdir vide » est testé sur la valeur **brute**, avant tout `Clean`, puisque
`filepath.Clean("")` vaut `.`.

**Ce défaut est ANTÉRIEUR à #56.** Rien sur cette branche ne l'a introduit ; elle le ferme parce
qu'il est de la même classe et qu'il est désormais mesuré au lieu d'être hypothétique.

**Un garde « sandbox arrêtée » — hors périmètre parce que MESURÉ sans objet, pas parce que reporté.**
`ports` est absent de `sbx ls --json` dès que le statut n'est pas `running` (§14.0), et un rapport
qui hériterait de ce piège dirait « is not mounted » de chaque mount configuré, juste au-dessus de
la ligne « sandbox arrêtée ». Mesuré le 2026-08-10 (§14.0, sondes) : `workspaces` **survit** à
`sbx stop`, clé présente et complète. Le piège ne se transpose donc pas, ni ici ni chez les trois
sœurs. Rien à écrire.

**Toute tentative d'appliquer un mount à une sandbox vivante**, comme la conception des mounts l'avait
déjà écrit : les mounts sont fixés à la création côté sbx, den ne réapplique rien à l'attache, et
prétendre le contraire donne un den qui ment sur l'état de la VM.

## Tests

Dans `internal/spawn`, avec `sbx.Fake`, sans socket ni processus :

1. un mount sans `link:` ajouté après la création → avertissement nommant le chemin et la clé ;
2. un mount repointé → avertissement (l'hôte nouveau est absent de la VM) ;
3. `rw:` → `ro:` sur une VM qui monte l'hôte nu → ligne « mounted read-write … now says read-only » ;
4. `ro:` → `rw:` sur une VM qui monte `<host>:ro` → la ligne symétrique ;
5. configuration inchangée → **silence** (le cas qui garde l'avertissement lisible) ;
6. `ssh.dir` édité en `ssh.mode: mount` → couvert par le même chemin de code ;
7. aucun `mounts:` → silence, sans lecture de `live.Workspaces` ;
8. `mountWorkspace` est la seule orthographe : le test compare l'argv produit au point 4 et
   l'attendu du rapport sur la même entrée ;
9. orthographes divergentes, **dans les deux sens** — configuration sale / VM propre, puis
   configuration propre / VM sale (la sandbox d'avant la mise à jour) → silence. `sbx.Fake` renvoie
   la chaîne qu'on lui a donnée : un test dont les deux côtés portent la **même** orthographe ne
   prouve rien, donc la réponse `ls --json` est écrite à la main, différente de la configuration ;
10. retournement `ro:` avec des orthographes divergentes → la ligne de retournement, pas « is not
    mounted » ;
11. le chemin **affiché** reste celui que l'utilisateur a tapé : il grep son `config.yaml` avec ;
12. hôte non canonique **avec** un `link:`, mixin enregistré rendu depuis cette même orthographe →
    `reportDrift` reste **muet**. C'est le test qui échoue si le nettoyage revient au chargement ;
13. `ssh.dir` seul, puis `mounts:` + `ssh.dir` → l'en-tête nomme la ou les clés réellement en cause ;
14. orthographes divergentes sur un chemin de `repos:`, **dans les deux sens** → silence, sur les
    DEUX moitiés de l'avertissement (« is not mounted » et « the shell starts in ») ;
15. `movedStart` isolé : deux repos montés, le nest nommant le premier avec un slash final, rien
    de manquant → silence. L'orthographe sale vient du **nest**, jamais de `Options.Repos` : un
    positionnel passe par `parseRepoArg`, qui nettoie déjà, et le test ne prouverait rien.

Dans `internal/config` :

16. `host:` et `ssh.dir` survivent au chargement **tels que tapés** (l'inverse de ce que cette
    branche avait d'abord verrouillé) ;
17. `ssh.dir: " ~/x"` → trimé **avant** `ExpandPath`, donc étendu ; l'assertion porte sur la valeur
    étendue, un trim placé après passerait un test sur l'espace tout en laissant `~/` littéral.

## Livrables

- `internal/spawn/spawn.go` : `mountWorkspace`, `normalizeWorkspace`, `reportUnmountedMounts`, appel
  sur la branche d'attache, commentaire-pourquoi au site de décision (recouvrement avec `Links`,
  non-retrait du `:ro`, normalisation à la comparaison et pas au chargement).
- `internal/nest/resolve.go` : `SSHDirKey`, la seule orthographe de la clé que l'en-tête relit.
- `internal/config/config.go` : `ssh.dir` trimé avant `ExpandPath`, comme son frère
  `mounts[].host` ; **aucun** `filepath.Clean` au chargement, avec le pourquoi au site.
- `internal/spawn/spawn.go` : `reportUnmountedRepos` normalise elle aussi les deux côtés — défaut
  **antérieur** à #56, repris ici parce qu'il est de la même classe et désormais mesuré.
- `internal/spawn/spawn_test.go` : les cas 1 à 15 ci-dessus.
- `internal/config/config_test.go` : les cas 16 et 17.
- `docs/superpowers/specs/2026-07-27-den-cli-design.md` §14.0 : les mesures du 2026-08-10 et leur
  date — `:ro` fait l'aller-retour dans `sbx ls --json` ; `workspaces` est normalisé
  **lexicalement** par sbx ; sbx ne résout **aucun** lien symbolique ; `workspaces` **survit** à
  `sbx stop`, contrairement à `ports` — plus la note que la machine porte v0.37.1 quand le relevé
  date de v0.35.0, et la liste des commandes que les sondes ont réellement exercées.
- Pas de changement de README : ce changement n'ajoute aucune surface de commande ni aucune option.
