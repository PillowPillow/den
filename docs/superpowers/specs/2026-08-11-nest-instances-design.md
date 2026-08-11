# Spec — instances de nest : `--as` et `select: prompt`

**Date** : 2026-08-11
**Statut** : validé (brainstorming du 2026-08-11)
**Complète** : `2026-07-27-den-cli-design.md` (§2 identité, §6 séquence de spawn, §8 ports),
`2026-08-04-adhoc-repos-design.md` (lève sa décision 7, explicitement différée),
`2026-08-05-spawn-command-design.md` (nouveaux drapeaux de `den spawn`).

## Problème

Deux manques, et le second est la cause du premier.

**1. Un nest par fonctionnalité n'est pas tenable.** Une équipe microservices — Digitaleo, une
dizaine de repos ouverts selon ce qu'on touche — doit aujourd'hui écrire un `nests/<n>.yaml` par
combinaison de repos, puis l'effacer. `optional:` + `-i` couvrent le cas d'un nest à deux ou trois
repos facultatifs ; ils ne couvrent pas un nest « générique » de trente repos dont on en veut
quatre, et dont la combinaison change chaque semaine.

**2. Deux montages différents ne sont pas distinguables.** L'identité est le nom de sandbox
`<nest>[.<worktree>]` (§2), et la spec `2026-08-04-adhoc-repos-design.md` a verrouillé, décision 7,
que les repos donnés en ligne de commande n'entrent PAS dans l'identité :

> `den scratch ~/dev/a` et `den scratch ~/dev/b` visent la même sandbox `scratch` : la seconde
> commande attache la première et ne monte rien de neuf. […] Rendre deux montages différents
> distinguables serait un changement de dérivation du nom de sandbox — hors scope, à chiffrer à
> part.

C'est ce chiffrage. Sans lui, un nest générique est cassé par construction : la sélection du
mardi attache la sandbox du lundi et ne monte rien. Et le manque se lit aussi hors nest générique —
faire tourner deux analyses concurrentes sur **les mêmes repos**, dans deux microVMs séparées, sans
worktree, n'a aujourd'hui aucune écriture.

**Le nommage est donc la primitive, le nest dynamique la feature.** L'ordre de cette spec le suit.

## Ce qui n'est PAS le problème

- **Un `den run` sans nest.** Déjà écarté par `2026-08-04-adhoc-repos-design.md`, pour la même
  raison qu'ici : un spawn sans nest n'a aucune source de nom. Non rouvert.
- **Un nouveau sélecteur.** `internal/spawn/interactive.go` EST la checklist, `--only` / `--without`
  sa forme non interactive, `nest.Resolve` la règle de sélection. Cette spec ajoute un point
  d'entrée et une valeur de départ, elle ne réécrit aucune des trois.
- **Un nom d'instance engendré par den.** Écarté explicitement (décision 4) : faire tourner deux
  instances identiques doit être une action VOULUE, jamais un effet de bord.
- **Rendre la sélection persistante dans un fichier.** Écrire `nests/<label>.yaml` à chaque spawn
  réintroduirait exactement le fichier par fonctionnalité que cette spec supprime. Le seul
  enregistrement est le manifeste, que den écrit déjà (`internal/manifest`).

## Interface utilisateur

```bash
# La primitive : nommer l'instance.
den spawn dg:op-inscription --as analyse-a     # sandbox dg-op-inscription.analyse-a
den spawn dg:op-inscription --as analyse-b     # dg-op-inscription.analyse-b — mêmes 9 repos, 2 VMs

# Le nest générique.
den spawn dg:digitaleo                          # checklist, départ VIDE → sandbox dg-digitaleo
den spawn dg:digitaleo --as leo-fix             # checklist → dg-digitaleo.leo-fix
den spawn dg:digitaleo --as leo-fix --only php.flow,js.ai-assistant   # sans invite
den spawn dg:digitaleo --as leo-fix | cat       # REFUS : pas de terminal, pas de --only
```

### Le nom de sandbox

Le second composant devient l'**instance**. Il n'est plus « le worktree », il est « ce qui
distingue deux sandboxes du même nest » — ce que le worktree était le seul à savoir faire.

| commande | sandbox | `worktree:` du manifeste |
|---|---|---|
| `den spawn dg:api` | `dg-api` | absent |
| `den spawn dg:api -w feature/123` | `dg-api.feature-123` | `{name: feature-123, branch: feature/123}` |
| `den spawn dg:api --as reco` | `dg-api.reco` | absent |
| `den spawn dg:api -w feature/123 --as reco` | `dg-api.reco` | `{name: reco, branch: feature/123}` |

`-w` REMPLIT le composant avec la branche aplatie — la règle d'aujourd'hui, inchangée. `--as` ÉCRASE
ce remplissage. Rien d'autre ne bouge : `sbx.SandboxName` garde sa signature à deux composants,
`sbx.SplitName` garde ses deux retours, et aucun des dix appelants de `SplitName` ne change de
forme.

`--as` traverse `config.ValidateSandboxComponent` et `config.FlattenSandboxComponent`, comme `-w` :
une seule définition de charset, un seul aplatissement, aucun second chemin à garder synchronisé.

### `select:` — le nest générique

Nouvelle clé de nest, deux valeurs :

```yaml
# nests/digitaleo.yaml
stack: base
select: prompt          # défaut : all
repos:
  - { key: php.baseo, optional: true }
  - { key: php.flow,  optional: true }
  # … 28 autres, tous optional
```

- `all` (défaut) : le comportement d'aujourd'hui. Tous les optionnels sont montés, `-i` reste
  opt-in et sa checklist démarre TOUT COCHÉ.
- `prompt` : le nest n'a pas de sélection par défaut. Avec un terminal, la checklist s'ouvre sans
  `-i` et démarre **VIDE**. Sans terminal et sans `--only`, den refuse.

Une valeur inconnue est une erreur de chargement, pas un silence : le décodage est strict
(`KnownFields(true)`) et `select:` est validé à la même place que le reste du nest.

## Décisions verrouillées

1. **Le second composant est réutilisé, pas doublé.** Un troisième composant
   (`<nest>.<worktree>.<label>`) rendrait worktree et instance indépendants, au prix de faire
   grandir `sbx.SplitName` de deux à trois retours et, avec elle, `sbx/ls.go`, `cli/rm.go`,
   `cli/ports.go`, `cli/reference.go`, `agent/mixin.go`, `source/decode.go` et
   `manifest/manifest.go`. Le bénéfice serait de lire la branche DANS le nom ; or la branche telle
   que tapée est déjà ailleurs, et à un meilleur endroit.

2. **La branche vit dans le manifeste, jamais dans le nom.** `manifest.Worktree` sépare déjà `Name`
   (le composant aplati) de `Branch` (la branche telle que tapée) — l'aplatissement étant lossy,
   le manifeste est déjà « le seul endroit où l'original survit une fois le spawn terminé ». `--as`
   ne fait qu'élargir cet écart déjà existant : `Name` devient le label, `Branch` ne bouge pas.
   C'est ce qui rend la décision 1 bon marché plutôt qu'astucieuse.

3. **`--as` vaut pour tout nest, pas seulement les génériques.** Deux analyses concurrentes sur
   `dg:op-inscription` sont le cas d'usage nommé par l'utilisateur, et il n'a rien à voir avec
   `select:`. Lier `--as` à `select: prompt` inventerait un couplage que rien ne demande.

4. **den n'engendre JAMAIS un nom d'instance.** Sans `--as`, la sandbox est `<nest>` — l'instance
   par défaut du nest, celle d'aujourd'hui. Une seconde instance existe parce que quelqu'un a tapé
   `--as`, jamais parce que den a horodaté un spawn. Un label engendré (`dg-digitaleo.20260811-1432`)
   a été proposé et REFUSÉ : il fait de « deux VMs identiques » l'effet de bord par défaut de la
   commande la plus banale, alors que c'est l'opération la plus coûteuse que den sache déclencher.
   Corollaire assumé : `d.Now` n'entre pas dans le chemin de nommage, et il n'y a aucune horloge à
   injecter pour cette feature.

5. **Le contrôle de vivacité passe DEVANT la checklist.** Aujourd'hui `-i` s'exécute au début de
   `Spawn` (spawn.go:203) et la liste des sandboxes est lue bien plus tard (spawn.go:606). Sous
   `select: prompt`, l'ordre s'inverse : si la sandbox visée est vivante, den attache et n'ouvre
   AUCUNE invite. Faire choisir des repos qui ne seront pas montés est précisément le silence que
   §2 interdit — et l'invite serait posée à quelqu'un qui n'a aucun moyen de deviner qu'elle ne
   sert à rien.

   Le message d'attache nomme le remède :

   ```
   sandbox dg-digitaleo already live: attaching
     its repos come from its creation: php.baseo, php.flow
     to run a different set alongside it, spawn `--as <label>`
   ```

   Les repos cités viennent du manifeste, pas d'une re-dérivation — c'est exactement ce pour quoi
   `internal/manifest` existe.

6. **Une invite ne peut pas être littéralement obligatoire.** `spawn` refuse déjà `-i` sans
   terminal, et `den exec` existe pour les pipes et la CI (v1.6.0). Un nest qui EXIGE une invite
   serait inutilisable en headless. `select: prompt` se lit donc : invite quand il y a un terminal
   et aucun drapeau de sélection ; refus nommant `--only` sinon.

   ```
   error: nest digitaleo selects its repos at spawn time and there is no terminal —
     use `--only php.baseo,php.flow` to make the same selection without a prompt
   ```

7. **`--only a,b --as x` est l'équivalent EXACT de la checklist confirmée sur a et b.** Même
   invariant que celui déjà verrouillé pour `-i` par
   `TestInteractiveProducesTheSameArgvAsTheEquivalentWithout` : la checklist est une source
   d'entrée placée devant `nest.Resolve`, jamais une seconde règle de sélection. Le test s'étend à
   `select: prompt`, il ne se duplique pas.

8. **La checklist d'un `select: prompt` démarre VIDE, celle de `-i` reste TOUT COCHÉ.** Ce n'est
   pas une incohérence, ce sont deux questions différentes. `-i` sur un nest ordinaire répond
   « lesquels des optionnels retirer », et son départ tout coché EST l'invariant de la décision 7
   ci-dessus (à ne pas confondre avec la décision 7 d'adhoc-repos, citée en décision 10) :
   confirmer sans rien toucher doit produire ce que `den spawn` seul produit. `select: prompt`
   répond « lesquels monter » : un nest sans sélection par défaut n'en a, par définition, aucune à
   proposer, et trente cases cochées feraient d'une ligne vide un montage de trente repos.

9. **Une clé non mappée ne coûte rien tant que son repo n'est pas sélectionné.** C'est la
   propriété qui rend un nest générique de trente repos utilisable : personne ne mappe — ni ne
   clone — les trente. Elle est DÉJÀ vraie et déjà verrouillée : `nest.Resolve` appelle
   `selectRepos` (resolve.go:271) AVANT `resolveRepoKeys` (resolve.go:279), et le commentaire de
   `resolveRepoKeys` dit pourquoi l'ordre porte la charge — résoudre d'abord rendait le refus
   inéluctable, et `optional:` ne voulait alors rien dire pour une entrée `key:`.

   `select: prompt` en hérite par construction : la checklist n'écrit que dans le `without` que
   `selectRepos` consomme, et elle affiche `Repo.Name()`, qui rend la clé et ne demande aucun
   chemin. Aucune ligne n'est à écrire pour obtenir ce comportement — mais un test le fixe
   (surface de test), parce qu'un jour quelqu'un rendra l'ordre « plus logique ».

   Ce que cette spec ajoute : la checklist **annote** les clés non mappées, `g.Repos` étant déjà
   chargé quand elle s'ouvre (`LoadGlobal` précède `interactiveWithout` dans `Spawn`).

   ```
   nest digitaleo: 30 optional repo(s) — none selected
     1 [ ] php.baseo
     2 [ ] php.flow      (not mapped in ~/.den/config.yaml)
   ```

   L'annotation n'interdit RIEN : cocher une entrée non mappée reste possible, et le refus qui
   suit est le message habituel de `resolveRepoKeys`, qui nomme la clé, le fichier et l'URL de
   clone. Refuser la coche à la place ferait de la checklist un second juge de la carte des
   repos, alors que le seul juge est `resolveRepoKeys` — et ce serait un refus muet sur la seule
   surface où l'utilisateur ne voit pas encore ce qu'il demande.

10. **Un label n'est pas une sélection.** Deux spawns avec le même `--as` et des `--only` différents
   visent la même sandbox : le second attache le premier. C'est la décision 7 d'adhoc-repos, et
   elle est INCHANGÉE — le label entre dans l'identité, les repos toujours pas. den ne détecte pas
   un label « mal réutilisé » ; `reportUnmountedRepos` (spawn.go:762), qui nomme déjà chaque chemin
   non monté sur une sandbox vivante, est le seul signal, et il suffit.

## Modèle

```go
// internal/spawn/spawn.go
type Options struct {
    // …
    Worktree string // -w, inchangé
    Instance string // --as : écrase le composant que -w remplit
}

// internal/nest/nest.go
type Nest struct {
    // …
    Select string `yaml:"select"` // "" (= all) | "all" | "prompt"
}
```

`Select` est une chaîne et non un booléen `Generic` : `all` est une valeur qu'on peut écrire pour
dire « ce nest monte tout », et une troisième valeur future (`prompt-required`, par exemple) ne
demanderait pas de casser le type. Le zéro `""` vaut `all`, donc aucun nest existant ne change de
comportement en étant relu.

### Aplatissement et validation

Aucune fonction nouvelle. Dans `spawn.go`, le bloc qui aplatit `-w` (spawn.go:343) reçoit une
branche : si `o.Instance` est non vide, c'est LUI qu'on aplatit et valide, et le résultat de `-w`
n'alimente plus que `manifest.Worktree.Branch`. `sbx.SandboxName(nestComponent, instance)`
(spawn.go:399) est appelée telle quelle.

### Ce que `den ls` doit corriger

`internal/cli/ls.go:116` retombe aujourd'hui sur le composant 2 quand l'enregistrement ne porte pas
de bloc `worktree:` — un repli « fail-open » écrit quand composant 2 ne pouvait être qu'un worktree
aplati. Avec `--as reco` sans `-w`, ce repli imprimerait `reco` dans la colonne WORKTREE : une
branche qui n'existe pas. Le repli ne vaut donc plus que pour les sandboxes SANS enregistrement
(legacy, ou créées hors den) ; dès qu'un manifeste est lu, l'absence de bloc `worktree:` signifie
« pas de worktree », et se rend comme telle.

Une colonne INSTANCE apparaît à côté : c'est ce que composant 2 vaut désormais, et c'est le nom que
`den exec` / `den rm` prennent en argument.

## Ce qui ne change pas

- **`internal/manifest`** — `Worktree{Name, Branch, Layout, Root}` couvre déjà le cas. Zéro champ
  ajouté, zéro bump de `Schema`.
- **`internal/cli/rm.go`** — il lit l'enregistrement ; la dérivation legacy
  (`cleanWorktreesLegacy`) ne tourne qu'en l'absence d'enregistrement, et den en écrit un à chaque
  `create`. Une sandbox `--as` en a donc toujours un.
- **`sbx.SandboxName` / `sbx.SplitName`** — signatures et sémantique de découpe identiques.
- **Les fenêtres de ports** — `internal/cli/ports.go:20` sème la fenêtre sur le NEST, jamais sur la
  sandbox, précisément pour que l'URL de §8 ne dépende pas du worktree qui tourne. Une seconde
  instance tombe donc sur une fenêtre décalée, non canonique, et `ports.Window.Canonical` le dit
  déjà. Aucun travail.
- **L'ordre `selectRepos` → `resolveRepoKeys` dans `nest.Resolve`** — il porte déjà la décision 9
  et n'est pas touché. Il gagne seulement un test qui le fixe.
- **`internal/lint`** — il valide `select:` comme le reste, sans règle nouvelle : la clé n'est pas
  une référence, elle ne peut pas être préfixée par une source.

## Ordre de livraison — non optionnel

Le décodage est strict partout et `internal/lint` est fail-closed dans `den source add` (qui
refuse **et supprime** le clone) comme dans `den source update`. Un `select:` ajouté à
`digitaleo-den-env` avant que l'équipe ait monté de version fait donc **échouer le
`den source update` de chaque collègue**, et supprime le clone de qui tente un `source add`.

1. Livrer den avec `--as` et `select:`.
2. Faire monter de version l'équipe (`den version` le dit).
3. **Puis** éditer la source `dg` : ajouter `nests/digitaleo.yaml`, et une ligne dans son README
   qui nomme la version minimale de den.

## Limites assumées

1. **Deux sandboxes montent le même working tree.** C'est le cas d'usage nommé — deux analyses
   concurrentes sur les mêmes dossiers — et den n'a aucun chemin lecture seule à offrir :
   `2026-08-04-adhoc-repos-design.md`, décision 5, a refusé `:ro` parce qu'un common git dir monté
   `:ro` fait mourir `commit` sur « Unable to create index.lock ». Deux VMs qui écrivent le même
   index git peuvent le corrompre. den l'autorise ; l'écriture concurrente est la décision de
   l'humain. `-w` reste la réponse quand les deux instances écrivent.

2. **Une seconde instance est explicite, jamais implicite.** `den spawn dg:digitaleo` vise toujours
   la même sandbox ; seul `--as` en crée une autre. Le coût est symétrique : deux instances sont
   deux noms à retenir, et `den ls` est l'endroit où on les relit.

3. **Fenêtre de ports non canonique pour l'instance suivante.** L'URL bookmarkable de §8 appartient
   à la fenêtre canonique du nest ; la seconde instance est décalée. `den ports` le signale déjà.

4. **La checklist reste `bufio`, sans TUI.** Trente entrées se cochent en tapant des numéros
   (`> 3 7 12`). den n'a que `cobra` et `yaml.v3` comme dépendances, et c'est une propriété
   annoncée (binaire statique, HANDOFF §8) — une bibliothèque TUI achèterait le curseur et la
   couleur au prix de cette propriété. Si trente entrées se révèlent pénibles à l'usage, un filtre
   par préfixe tapé se code dans le même `bufio` ; hors scope ici, à chiffrer sur mesure réelle.

## Surface de test

Hermétique intégralement : aucun socket, aucun process, aucune horloge (décision 4).

- **Nommage** : table `-w` seul / `--as` seul / les deux / aucun → nom de sandbox, et bloc
  `worktree:` du manifeste attendu pour chaque ligne.
- **`--as`** : aplatissement (`--as feat/x` → `feat-x`), refus d'un composant vide ou hostile,
  refus d'un nom commençant par `-`.
- **`select:`** : décodage strict d'une valeur inconnue ; `""` vaut `all` (aucun nest existant ne
  change) ; `prompt` sans terminal et sans `--only` refuse, en nommant `--only`.
- **Checklist** : départ vide sous `prompt`, départ tout coché sous `-i` — les deux dans le même
  test de table, pour que la décision 8 se lise en un endroit.
- **Équivalence** : extension de `TestInteractiveProducesTheSameArgvAsTheEquivalentWithout` à
  `select: prompt` — même argv pour la checklist confirmée sur {a,b} et pour `--only a,b`.
- **Clés non mappées** (décision 9) : un nest `select: prompt` dont la moitié des clés n'est pas
  mappée spawn sans erreur dès lors que les non mappées ne sont pas cochées — le test fixe l'ordre
  `selectRepos` → `resolveRepoKeys`, que rien n'empêche aujourd'hui d'inverser. Et : une entrée non
  mappée est annotée dans la checklist ; cochée, elle produit le refus de `resolveRepoKeys`, mot
  pour mot.
- **Attache** : sous `prompt`, une sandbox vivante n'ouvre aucune invite (l'entrée du test est un
  reader qui échoue si on le lit) et le message nomme `--as`.
- **`den ls`** : colonne INSTANCE ; WORKTREE vide quand un manifeste sans bloc `worktree:` est lu ;
  repli sur le composant 2 conservé quand il n'y a AUCUN manifeste.
