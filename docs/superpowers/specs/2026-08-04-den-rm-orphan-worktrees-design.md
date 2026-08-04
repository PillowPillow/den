# Spec — `den rm` récupère les worktrees orphelins

**Date** : 2026-08-04
**Statut** : validé (brainstorming du 2026-08-04)
**Complète** : la spec CLI `2026-07-27-den-cli-design.md` — amende sa §6, « Limite connue du
teardown ».
**Issue** : [#46](https://github.com/PillowPillow/den/issues/46)

## Problème

`den rm` laisse derrière lui le worktree d'un repo passé en ligne de commande.

```bash
den api -w feat ~/dev/hotfix   # monte hotfix, crée ~/.den/worktrees/feat/hotfix
den rm api.feat                # « sandbox api.feat destroyed »
```

`~/.den/worktrees/feat/hotfix` est toujours sur le disque, ainsi que son enregistrement dans
`~/dev/hotfix/.git/worktrees/feat`. L'utilisateur l'apprend plus tard, quand `git branch -d feat`
refuse avec « already checked out at … ».

`cleanWorktrees` (`internal/cli/rm.go:130`) itère sur `n.Repos` — la liste **déclarée**, lue par
`nest.LoadNest` sans `Resolve`, donc sans les positionnels. Or `Spawn`
(`internal/spawn/spawn.go:409`) appelle `worktree.Ensure` pour **chaque** entrée de `r.Repos`,
ad-hoc comprises : c'est la décision 2 de la feature, « un montage ad-hoc EST un repo ».

La cause est structurelle et énoncée dans le code lui-même
(`internal/worktree/worktree.go:141-143`) : den ne garde aucun état au-delà du nom de sandbox, et
`worktree.Path` a besoin d'un `repoPath` que den n'a plus, un positionnel ne faisant
délibérément pas partie de l'identité de la sandbox (décision 7).

**Périmètre réel** : c'est l'**élargissement** d'un trou préexistant, pas un trou neuf. Supprimer
un repo du `repos:` d'un nest puis lancer `den rm` orpheline son worktree de la même façon. Les
repos en ligne de commande en font le chemin ordinaire au lieu de la conséquence d'une édition de
config.

## Principe de la correction

Le worktree porte lui-même de quoi retrouver son dépôt : un `.git` qui pointe vers le common dir.
En layout **central**, `<worktree_root>/<wt>/` est donc énumérable, et chaque entrée est
identifiable **sans que den ait rien stocké**. C'est l'inverse exact de `worktree.Path`.

En layout **per-repo**, `Path` renvoie `<repoPath>/.den/<wt>` : sans le chemin du repo, den n'a
nulle part où regarder. L'énumération est impossible, seul un avertissement l'est.

## §1 — API : `worktree.Orphans`

Nouvelle fonction dans `internal/worktree/worktree.go`, à côté de `Path` dont elle est l'inverse.
Elle vit là et pas dans `internal/cli` pour deux raisons : la connaissance de la mise en page est
déjà là, et `cli` reste sans `os` ni git.

```go
type Orphan struct {
	Dir      string // <root>/<wt>/<name>
	RepoPath string // dépôt récupéré depuis le .git de Dir
}

// Orphans énumère les répertoires de worktree de wt sous root que `known` ne
// couvre pas, en récupérant le dépôt de chacun depuis son propre .git.
// Central uniquement : per-repo n'a nulle part où regarder.
func Orphans(ctx context.Context, g Git, root, wt string, known []string) ([]Orphan, []error, error)
```

- `[]error` : une raison de saut par entrée écartée, destinée à `warnW`.
- `error` final : uniquement un échec de `os.ReadDir` autre que `NotExist` — un
  `<root>/<wt>` absent est le cas nominal (aucun worktree), pas une erreur.
- `os.ReadDir` renvoie les entrées **triées**, ce qui suffit à stabiliser les goldens de `cli`.

## §2 — Les garde-fous, et pourquoi `checkOwnership` ne suffit plus

Aujourd'hui `Remove` est sûr parce que l'appelant fournit `RepoPath` **depuis la config** et que
`checkOwnership` prouve que le répertoire appartient bien à ce dépôt. Si le `RepoPath` est dérivé
**du répertoire** (`identify` → `--git-common-dir` → `repoDir`), ce contrôle ne peut plus jamais
échouer : on demande à git à qui appartient le répertoire, puis on affirme qu'il lui appartient.
Le garde-fou qui rendait vraie la phrase « den ne retire que des répertoires qu'il a lui-même
posés » s'évapore. Il est remplacé par six contrôles explicites, tous bâtis sur des helpers déjà
présents dans le fichier :

| # | Contrôle | Ce que le saut protège |
|---|---|---|
| 1 | l'entrée est un répertoire | un fichier égaré n'est jamais touché |
| 2 | `identify(dir)` réussit | ce n'est pas du git : répertoire de l'utilisateur |
| 3 | `samePath(root, dir)` | git a répondu pour un dépôt **ancêtre** ; `dir` est un simple dossier |
| 4 | `!samePath(repoDir(common), dir)` | `dir` est un **clone principal** garé sous `worktree_root` — le mettre à la corbeille serait pire que le bug corrigé |
| 5 | `Path("central", root, wt, recovered) == dir` | basename différent (le repo est lui-même un worktree lié) |
| 6 | `worktreeEntry(recovered, dir)` → `registered` | preuve la plus forte que ce chemin est bien un worktree enregistré de ce dépôt |

Le contrôle **5** n'est pas cosmétique. `Remove` **recalcule** le chemin via
`Path(c.Layout, c.Root, c.Worktree, c.RepoPath)` — délibérément, cf. le commentaire de `Target` :
den dérive, il ne fait pas confiance à un chemin fourni. Si le repo passé en ligne de commande
était lui-même un worktree lié, `repoDir(common)` remonte au worktree **principal**, dont le
basename peut différer du répertoire énuméré. `Remove` ferait alors un `Stat` sur un chemin
inexistant, prendrait la branche « déjà disparu », lancerait un `prune` et renverrait `""` :
l'orphelin resterait sur le disque et `den rm` n'en dirait **rien**. En cas de non-correspondance :
avertir en nommant le répertoire, et sauter.

**Rejeté** : ajouter un champ « chemin explicite » à `Target` pour contourner ce recalcul.
L'invariant est porteur — c'est lui qui interdit à un appelant d'envoyer `Remove` n'importe où.

**Déduplication** : elle est la fonction du contrôle 5 elle-même. `dir` est écarté dès qu'un repo
de `known` vérifie `Path("central", root, wt, repo) == dir` — la comparaison passe par la fonction
qui a **posé** le répertoire, pas par une heuristique de basename.

## §3 — Réécriture de `cleanWorktrees`

L'énumération a lieu **avant** la boucle des repos déclarés. Ce n'est pas un choix de style :
`removeParentDir` supprime `<root>/<wt>` dès qu'il se vide, donc une boucle déclarée exécutée
d'abord peut effacer le répertoire qu'on s'apprêtait à lire.

1. énumérer (central uniquement) → `[]Orphan` + raisons de saut ;
2. construire **une** liste de travail : repos déclarés, puis orphelins ;
3. **une** boucle `worktree.Remove`, sémantique inchangée — un `context.WithTimeout` par entrée,
   annonce du chemin de corbeille pour chaque worktree réellement déplacé.

**Partage des échecs**, conforme à la doctrine déjà écrite en `rm.go:75-86` (T13/T16) :

- *résolution* → best-effort. Un orphelin dont le dépôt a disparu fait échouer `identify` : on
  avertit sur `warnW` et on continue. Sans cela `den rm` sortirait en erreur **avant** `sbx rm` et
  laisserait l'utilisateur avec une VM vivante qu'il ne peut plus détruire.
- *retrait* → strict. Un worktree récupéré mais sale ou verrouillé arrête tout, comme aujourd'hui.

**`--force` et le refus sur modifications non commitées s'appliquent aux entrées récupérées comme
aux autres** : un orphelin n'est pas un permis de supprimer du travail. C'est précisément pourquoi
les orphelins passent par le `Remove` existant et non par un second chemin de retrait — corbeille,
verrou, `prune` et refus restent en un seul endroit.

## §4 — Les deux branches décidées en brainstorming

**Nest illisible + layout central** (`rm.go:117-127`, aujourd'hui un simple avertissement) :
l'énumération n'ayant plus besoin de `n.Repos`, den nettoie **réellement** — `Orphans` avec
`known = nil`. L'avertissement « nest illisible » reste imprimé : l'utilisateur doit savoir que
la résolution a échoué. Bénéfice de bord : le cas préexistant « repo retiré du yaml » est corrigé
du même geste.

**Layout per-repo** : avertissement **à chaque** teardown, formulé au conditionnel. den ne garde
aucun état : il ne peut pas savoir si des positionnels ont été utilisés. Le message ne doit donc
jamais **affirmer** qu'un reliquat existe, seulement nommer `<repo>/.den/<wt>` comme l'endroit à
vérifier si un repo a été passé en ligne de commande. Bruyant pour qui n'en passe jamais ; c'est le
prix de l'honnêteté sur un état que den n'a pas.

## §5 — Hors périmètre (hypothèse assumée)

La ligne `.den/` écrite par `excludeDenDir` (`internal/worktree/worktree.go:758`) dans le
`.git/info/exclude` du dépôt en layout per-repo **n'est pas retirée**. L'issue la qualifie
elle-même d'inerte et jamais commitée ; éditer le fichier d'exclusion d'un utilisateur — dont il a
pu modifier le contenu depuis — est plus risqué que de laisser la ligne.

## §6 — Tests

- `internal/worktree/worktree_test.go` : récupération et garde-fous. Le paquet exécute du vrai git
  et appelle `NeutralizeGitEnvironment()` dans son `TestMain`.
- `internal/cli/rm_test.go` : câblage, ordre des retraits, texte des avertissements, non-régression
  du refus sale et de `--force` sur une entrée récupérée.

Deux cas ont un test **explicite**, parce qu'une suite verte les manquerait sinon — l'un mange un
répertoire, l'autre ment :

- **garde-fou 4** : un clone principal garé sous `worktree_root` doit être **sauté** ;
- **garde-fou 5** : un repo qui est lui-même un worktree lié, dont le basename récupéré diffère,
  doit être **sauté avec avertissement** et non silencieusement no-opé.

## Documentation à mettre à jour

- `README.md`, paragraphe `den rm` : la limite décrite disparaît en central, subsiste en per-repo.
- `docs/superpowers/specs/2026-07-27-den-cli-design.md` §6, « Limite connue du teardown » : même
  amendement.
