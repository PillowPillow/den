# Spec — le manifeste de sandbox : den se souvient de ce qu'il a monté

**Date** : 2026-08-05
**Statut** : validé (brainstorming du 2026-08-05)
**Complète** : la spec CLI `2026-07-27-den-cli-design.md` — amende ses §3 (layout de `~/.den`), §6
(séquence de spawn) et §7 (`den rm`), et prolonge `2026-08-04-adhoc-repos-design.md`, dont la
fonctionnalité n'a aujourd'hui aucun chemin de nettoyage.

## Problème

`den rm` ne sait pas ce qu'il détruit : il le **re-dérive** au moment du `rm`, en relisant
`nests/<n>.yaml` et le `repos:` de `config.yaml`, puis en recalculant le chemin de chaque worktree
depuis `worktree_layout` + `worktree_root` + le composant aplati du nom de sandbox
(`internal/cli/rm.go:97`, `cleanWorktrees`).

Cette dérivation suppose que la configuration d'aujourd'hui décrit encore le spawn d'hier. Elle
échoue dès que ce n'est plus vrai :

| cas | ce qui se passe aujourd'hui |
|---|---|
| repo monté à la volée (`den api /tmp/hotfix`) | il n'est déclaré dans **aucun** fichier : son worktree n'est jamais nettoyé, et rien ne le signale |
| `--without` / `--only` au spawn | `rm` itère **tous** les repos du nest, y compris ceux que ce spawn n'a jamais montés |
| nest édité ou supprimé entre spawn et `rm` | warning « nest unreadable », worktrees abandonnés sur disque |
| `repos:` de `config.yaml` modifié depuis | `rm` vise un autre chemin que celui réellement créé |
| clé démappée depuis le spawn | `rm.go:163` abandonne explicitement le répertoire avec un warning |
| `worktree_root` / `worktree_layout` modifiés | le chemin recalculé ne désigne plus le répertoire créé |
| nom de worktree | `-w feature/12` crée la sandbox `api.feature-12` ; la branche telle que tapée n'est plus récupérable nulle part, et `den ls` ne peut afficher que la forme aplatie |

La cause est unique : **la création est un événement, et den n'en garde aucune trace.** Chaque
lecteur ultérieur rejoue une déduction à partir d'un état qui a pu bouger.

## Ce qui n'est PAS le problème

- **Reconstruire une sandbox.** Le manifeste décrit ce qui a été fait, il ne rejoue rien. den ne
  remonte jamais rien sur une VM vivante (§6, branche attach) et ce n'est pas changé ici.
- **Remplacer le mixin.** `cache/mixins/<sandbox>/spec.yaml` reste ce qu'il est : un kit au schéma
  de `sbx` (schemaVersion 2, env / egress / freshness). Il n'a pas de place pour la comptabilité de
  den, et den n'a pas à polluer un schéma qui ne lui appartient pas.
- **Le profil agent.** Jamais touché par `rm`, ni avant ni après (`rm.go:24`).
- **Les repos montés tels quels** (sans `-w`). Ce sont les répertoires de travail de l'utilisateur ;
  den n'en dispose pas.

## Décisions

### D1 — Le manifeste vit sur l'hôte, pas dans la sandbox

`<denHome>/state/sandboxes/<sandbox>.yaml`, un fichier par sandbox.

Un fichier écrit *dans* la VM serait illisible précisément dans le cas qui motive la fonctionnalité :
`den rm` sur une VM qui ne boote plus, une VM stoppée, ou une sandbox que `sbx` a déjà perdue. Et
les worktrees sont des artefacts **hôte** : leur registre appartient au côté hôte.

`state/`, pas `cache/` : le spec §3 déclare `cache/` reconstructible, et un repo monté à la volée
n'est reconstructible depuis rien. Un futur `den clean` qui viderait `cache/` effacerait la seule
trace d'un worktree portant du travail non commité. `state/` est un nouveau répertoire de premier
niveau, à ajouter au §3 avec sa règle : **jamais purgé automatiquement**.

Un fichier par sandbox, pas un registre unique : deux `den` concurrents dans deux terminaux
écrivent deux fichiers distincts, sans verrou, et chaque lecteur ne veut de toute façon qu'une
sandbox. Le nom de sandbox sert de nom de fichier sans échappement — il est déjà validé comme
composant de chemin légal (`sbx.ValidateSandboxName`, que `cleanWorktrees` appelle déjà avant de
construire un chemin), exactement la propriété dont `cache/mixins/<sandbox>/` dépend.

Décodage strict (`KnownFields(true)`) comme partout dans den. Permissions `0700` / `0600`, comme le
mixin : aucune raison d'être plus laxiste que le voisin immédiat.

### D2 — Contenu

```yaml
schema: 1
sandbox: corp-api.feat12
nest:
  ref: corp:api                                   # ce que l'utilisateur a tapé
  file: /Users/x/.den/sources/corp/nests/api.yaml # ce qui a été lu
worktree:
  name: feat12          # le composant aplati, tel qu'il est dans le nom de sandbox
  branch: feature/12    # la branche TELLE QUE TAPÉE — ce que l'aplatissement détruit
  layout: central
  root: /Users/x/.den/worktrees
repos:
  - name: api
    origin: key                                   # key | path | command-line
    key: api
    repo: /Users/x/dev/api                        # le repo principal
    mount: /Users/x/.den/worktrees/feat12/api     # ce que sbx a réellement reçu
    worktree: true                                # den l'a créé → rm doit le reprendre
  - name: hotfix
    origin: command-line
    repo: /tmp/hotfix
    mount: /tmp/hotfix
    worktree: false                               # rm n'y touche JAMAIS
git_dirs:
  - /Users/x/dev/api/.git
```

Le champ qui porte la fonctionnalité est `worktree: true|false`. `mount` est le chemin réellement
passé à `sbx create` : plus de recalcul, donc plus de dépendance à `worktree_root` /
`worktree_layout` au moment du `rm`. `origin: command-line` est ce qui rend un montage à la volée
nettoyable, puisqu'aucune re-dérivation ne peut le retrouver. `branch` est la seule mémoire de la
branche avant aplatissement.

**Pas d'horodatage.** den injecte ses horloges (`Freshness`, `Policy`) ; en ajouter une ici ferait
traverser un paramètre à tout `Spawn` pour un champ qu'aucun lecteur ne consulte.

### D3 — Écrit une fois, sur la branche create

Position dans la séquence §6 : **après les worktrees et le profil agent, avant `sbx create`**
(`internal/spawn/spawn.go`, entre l'étape 4 et l'appel `d.Sbx.Run(ctx, argv...)`).

Les worktrees sont créés avant la VM. Un `sbx create` qui échoue laisse donc des répertoires sur
disque : écrire le manifeste avant la VM est la seule position où ce cas laisse une trace. Le
corollaire assumé est qu'un manifeste peut exister sans sandbox — c'est précisément ce que D6
apprend à ramasser.

**Jamais réécrit à l'attache**, même doctrine que `WriteMixin` et pour la même raison : le fichier
décrit ce que *cette VM* a reçu à sa création, et le réécrire détruirait la référence. Un respawn du
même nom le remplace.

**Un échec d'écriture est un refus**, avant `sbx create`. À ce point den vient d'imprimer le chemin
de chaque worktree créé (`spawn.go:519`) : le refus les nomme, et l'utilisateur n'a pas en plus une
VM à détruire.

### D4 — `den rm` rejoue le manifeste

`rm` garde son garde-fou actuel : il exige une sandbox vivante (`rm.go:56`). Ce qui change est ce
qu'il nettoie.

- Pour chaque entrée `worktree: true`, `worktree.Remove` reçoit `mount` et `repo` **tels
  qu'enregistrés**, au lieu de recalculer le chemin. `worktree.Target` gagne un chemin explicite qui
  l'emporte sur le calcul ; le calcul reste, pour le chemin légacé de D5.
- Les entrées `worktree: false` ne sont jamais touchées.
- `rm` ne lit plus le nest ni le `repos:` de `config.yaml` pour décider quoi nettoyer.

Conséquences directes, chacune un test : un repo monté en ligne de commande est repris ; un nest
supprimé entre-temps ne bloque plus rien ; un `worktree_root` déplacé depuis le spawn ne fait plus
viser à côté ; une clé démappée depuis n'abandonne plus le répertoire ; un `--without` au spawn
n'entraîne plus la reprise d'un repo que ce spawn n'a jamais monté.

**Suppression du manifeste** : `rm` le supprime quand il a repris tout ce qu'il listait. Avec
`--keep-worktrees` il le **garde**, et le dit — les répertoires survivent, leur registre aussi, et
D6 pourra les retrouver.

### D5 — Manifeste absent ou illisible : repli, jamais blocage

Sans manifeste (sandbox créée avant cette version, ou hors den), `rm` retombe sur la dérivation
actuelle **en le disant**. Le chemin légacé meurt de lui-même quand les vieilles sandboxes
disparaissent.

Un manifeste présent mais illisible ou corrompu produit un warning nommant le fichier, puis le même
repli. Ce n'est jamais bloquant : un `den rm` qui refuse laisse l'utilisateur avec une VM vivante
qu'il ne peut plus détruire — exactement ce que `rm.go` évite déjà explicitement (doctrine T13/T16,
la même raison pour laquelle il utilise `LoadGlobalUnvalidated` et ne valide que les deux champs
qu'il consomme).

### D6 — Les orphelins : `den ls` signale, `den doctor --fix` ramasse

Un manifeste sans sandbox vivante (VM détruite par `sbx rm` hors den, boot raté, `rm
--keep-worktrees`) est un **orphelin**.

- **`den ls` le signale**, sur une ligne distincte nommant les worktrees restés sur disque. Il a déjà
  le runner `sbx` et liste déjà les sandboxes vivantes : la comparaison est gratuite. Fail-open
  strict — un manifeste absent ou illisible ne doit jamais casser `den ls`, la commande qu'on tape
  quand tout va mal.
- **`den doctor` sans drapeau le rapporte**, comme un `Check` de niveau warning, et nomme le remède.
- **`den doctor --fix` le ramasse** : les worktrees orphelins partent à la corbeille par
  `worktree.Remove`, avec la même règle stricte qu'au `rm` — un worktree portant des modifications
  non commitées arrête tout. `den doctor --fix --force` est le même consentement que `den rm
  --force`, avec le même effet : le worktree sale part quand même à la corbeille, jamais à la
  poubelle. Le manifeste n'est supprimé qu'une fois tout ce qu'il liste effectivement repris.

**Frontière de paquets — l'invariant de doctor est préservé littéralement.**
`internal/doctor/doctor.go:2` dit « No side effects, no network », et le paquet ne lance jamais
`sbx` (il vérifie seulement sa présence dans le PATH). Rien de cela ne change :

- `internal/doctor` gagne une fonction **pure** qui, *étant donné* la liste des sandboxes vivantes
  et les manifestes lus sur disque, retourne les orphelins. Aucun nouveau champ dans `doctor.Deps`,
  aucune dépendance à `sbx`.
- `internal/cli` fournit la liste vivante — il a déjà `deps.Sbx` — et **porte la mutation**, comme
  `cli.cleanWorktrees` la porte déjà pour `rm`. `--fix` est un drapeau de `cmd/doctor` côté cli.

Quand `sbx` est absent du PATH, la liste vivante est inconnue : le check est **sauté** et le dit.
Le rapporter comme orphelin ferait crier den sur des sandboxes parfaitement saines. doctor
diagnostique déjà l'absence de `sbx` en 1ᵉ position, et c'est cette ligne-là qui porte le vrai
problème.

### D7 — Les autres lecteurs

- **`den ls`** : la colonne WORKTREE affiche la branche telle que tapée plutôt que le composant
  aplati, et le nest est nommé par sa référence préfixée (`corp:api`, pas `corp-api`). Fail-open.
- **Attache** : den compare aujourd'hui la configuration *actuelle* aux workspaces vivants, ce qui
  confond deux situations. Le manifeste les sépare : « le nest a changé depuis la création »
  (manifeste vs config) et « la VM n'a pas ce que la config dit » (config vs `sbx ls`). Seule la
  première a un remède honnête à proposer — `den rm` puis respawn — puisque den ne remonte jamais
  rien sur une VM vivante.

## Forme

Nouveau paquet feuille **`internal/manifest`** :

- écrit par `internal/spawn`, lu par `internal/cli` (`rm`, `ls`, `doctor`) et par `internal/doctor`
  (la fonction pure de D6) ;
- il n'importe ni `spawn` ni `cli` ; il fait de l'IO fichier et de la sérialisation, comme
  `agent.WriteMixin` ;
- aucune contrainte nouvelle pour `internal/ports/hermeticity_test.go` : pas de `net`, pas de
  `hash/fnv`, pas de `os/exec` ;
- le chemin est défini **à un seul endroit** (`manifestPath(denHome, sandbox)`), écriture et lecture
  obligées de s'accorder — le piège que `mixinDir` / `mixinPath` documente déjà, avec son test de
  round-trip.

## Preuves

Aucun test ne parle au réseau ni ne lance de process ; `worktree.Git` reste faké, `sbx.Fake`
également.

1. round-trip : ce que l'écriture produit, la lecture le rend à l'identique (modèle
   `TestReadMixinRereadsWhatWriteMixinWrote`) ;
2. un golden du fichier rendu, comparé à la main — il n'y a pas de `-update` dans ce dépôt ;
3. un repo monté en ligne de commande est repris par `rm` — le trou actuel, aujourd'hui invisible ;
4. nest supprimé entre spawn et `rm` : nettoyage complet quand même ;
5. `worktree_root` changé dans `config.yaml` entre les deux : `rm` reprend le répertoire d'origine ;
6. clé démappée depuis le spawn : le worktree est repris, plus de warning d'abandon ;
7. `--without` au spawn : `rm` ne touche pas au repo exclu ;
8. `worktree: false` : `rm` ne déplace jamais un repo monté tel quel ;
9. sandbox légacée sans manifeste : repli, avec la mention ;
10. manifeste corrompu : warning nommant le fichier, repli, et la VM est bien détruite ;
11. `sbx create` échoue après création des worktrees : le manifeste existe et `den ls` signale
    l'orphelin ;
12. `rm --keep-worktrees` : le manifeste survit, et `den doctor` rapporte l'orphelin ;
13. `den doctor --fix` avec un worktree sale : refus, comme au `rm`, sauf `--force` ;
14. `sbx` absent du PATH : le check d'orphelins est sauté et le dit, aucun faux positif.

## Ce que ça n'apporte pas

- Rien n'est réparé **dans** la VM : un manifeste ne remonte pas un volume oublié sur une sandbox
  vivante. Le remède reste `den rm` puis respawn.
- L'invariant « un manifeste peut exister sans sandbox » est le prix assumé de D3. C'est un état
  légitime, pas une anomalie — D6 est ce qui le rend adressable.
