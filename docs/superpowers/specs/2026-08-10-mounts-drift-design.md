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

Cette fonction-ci ne doit **pas** le retirer : le suffixe **est** le bit `ro:` sous test. Elle
compare l'orthographe exacte de l'argv.

### Une seule définition de l'orthographe

Un helper `mountWorkspace(m nest.Mount) string` rend `m.Host`, suffixé `:ro` quand `m.RO`. Il est
appelé **par la boucle d'argv du point 4** (`spawn.go:611-618`) *et* par le rapport.

Deux copies de `host + ":ro"` divergeraient un jour, et l'avertissement se déclencherait alors à
**chaque** attache sans que rien n'ait bougé — un avertissement permanent cesse d'être lu, y compris
le jour où il dit vrai. C'est la leçon déjà payée par `stringNode` (`mixin.go:160-179`).

### Trois verdicts par mount configuré, pour que le message ne mente pas

| état sur la VM | ligne |
|---|---|
| orthographe exacte présente | silence |
| même hôte, autre suffixe | ``- /p (mounts[2]) is mounted read-only, but `mounts:` now says read-write`` |
| hôte absent | ``- /p (mounts[2]) is not mounted`` |

Le cas du retournement mérite sa propre ligne. Dire « n'est pas monté » d'un répertoire qui **est**
monté, en lecture seule, est une affirmation fausse que den imprimerait à chaque attache. Les deux
directions du retournement sont couvertes, chacune nommant celle qu'elle a lue.

`Key` est nommé (`mounts[2]`, `ssh.dir`) : les erreurs de den nomment la clé à corriger, et les refus
de `LinkCommand` le font déjà (`links.go:79`).

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
   l'attendu du rapport sur la même entrée.

## Livrables

- `internal/spawn/spawn.go` : `mountWorkspace`, `reportUnmountedMounts`, appel sur la branche
  d'attache, commentaire-pourquoi au site de décision (recouvrement avec `Links`, non-retrait du
  `:ro`).
- `internal/spawn/spawn_test.go` : les huit cas ci-dessus.
- `docs/superpowers/specs/2026-07-27-den-cli-design.md` §14.0 : la mesure du 2026-08-10 et sa date —
  `:ro` fait l'aller-retour dans `sbx ls --json` — plus la note que la machine porte v0.37.1 quand le
  relevé date de v0.35.0.
- Pas de changement de README : ce changement n'ajoute aucune surface de commande ni aucune option.
