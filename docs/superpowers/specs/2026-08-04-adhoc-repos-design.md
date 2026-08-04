# Spec — repos à la volée : `den <nest> [path...]`

**Date** : 2026-08-04
**Statut** : validé (brainstorming du 2026-08-04)
**Complète** : la spec CLI `2026-07-27-den-cli-design.md` — amende ses §4.3, §5 et §6.

## Problème

Aujourd'hui un repo ne peut entrer dans une sandbox que par `repos:` dans `nests/<n>.yaml`.
Monter un dépôt pour une session — un hotfix, un dépôt qu'on lit une fois, un scratch — impose
d'éditer un fichier de config, puis de l'éditer à nouveau pour le retirer. `sbx run -t <template>
AGENT PATH [PATH...]` fait ce geste en une frappe ; den, qui existe pour rendre `sbx` plus simple,
le rend ici plus lourd.

Le besoin est **additif** : les repos déclarés restent ce qu'ils sont, les repos à la volée
s'ajoutent. Un nest peut aussi n'en déclarer aucun et ne servir que de porteur de stack, egress,
env, ports et profil agent.

## Ce qui n'est PAS le problème

Un `den run` sans fichier nest. L'identité d'un objet est son chemin (spec §2), et le nom de
sandbox `<nest>[.<worktree>]` est le SEUL handle : `den ls`/`sh`/`rm`, la policy scopée, le cache
de mixins et la poubelle des worktrees en dépendent tous. Un spawn sans nest n'a aucune source de
nom — il faudrait en inventer une, ce qui est un amendement du §2, pas un drapeau. Écarté ici,
à chiffrer séparément s'il est demandé.

## Interface utilisateur

Les repos à la volée sont les **arguments positionnels** qui suivent le nom du nest.

```bash
den scratch ~/dev/a ~/dev/b     # nest sans `repos:` — les deux dépôts viennent de la ligne
den api ~/dev/hotfix            # additif : les repos d'api PLUS hotfix
den scratch .                   # le cwd
den api -w feat/x ~/dev/hotfix  # -w propage un worktree sur hotfix comme sur les repos d'api
den nest show scratch ~/dev/a   # dry-run : la résolution, sans rien créer
```

`root.Args` passe de `atMostOneArg` à variadique : `args[0]` est le nest, `args[1:]` les repos.
Le routage cobra ne consulte que `args[0]`, donc aucune sous-commande n'est affectée, et
`den doctr ~/x` conserve sa suggestion — elle est accrochée à `nest.NestNotFoundError`, pas au
nombre d'arguments. `den nest show` passe symétriquement de `exactlyOneArg` à « au moins un » :
c'est le dry-run de la feature, et la surface de test hermétique qui va avec.

## Décisions verrouillées

1. **Le fichier nest reste obligatoire.** Ce qui devient facultatif, c'est son `repos:`. Un nest
   sans repos déclaré spawn déjà aujourd'hui : aucun garde-fou n'existe sur la liste vide.

2. **Traitement uniforme.** Un repo à la volée EST un repo : `-w` lui propage un worktree et monte
   son common git dir, exactement comme à un `repos:` déclaré. Un chemin non-git sous `-w` est
   refusé (den refuse plutôt que normaliser, §2), pas monté tel quel en silence.

3. **Un seul point de fusion.** Les positionnels entrent dans `nest.Options`, `nest.Resolve` les
   fusionne dans `Resolved.Repos`, et **rien en aval ne change**. C'est ce qui rend « uniforme »
   vrai par construction plutôt que par répétition : worktrees, common git dirs, ordre des
   workspaces, mixin et argv n'ont aucune branche à ajouter.

4. **Les positionnels passent devant les déclarés** dans `Resolved.Repos`. `Workspaces[0]` décide
   du répertoire de démarrage du shell (`sbx.Sandbox.Workdir`), et le geste « je monte X à la
   volée » veut dire « je viens travailler dans X ».

   **Cette garantie ne vaut qu'au `create`.** Sur une sandbox déjà vivante, le cwd vient de
   `live.Workdir()` — l'ordre figé par le `create` d'origine — quels que soient les positionnels
   tapés aujourd'hui. Un `den api` du jour 1 crée la sandbox `api` avec `[api1, api2, agent]` ; un
   `den api ~/dev/hotfix` du jour 2 attache cette même sandbox, ne monte pas `hotfix` et démarre
   dans `api1`. C'est la moitié surprenante pour qui a choisi « le premier positionnel gagne », donc
   l'avertissement de l'étape 6 doit dire les deux : ni monté, ni pris comme répertoire de départ.

5. **`:ro` refusé.** `sbx create` l'accepte, mais il contredit la décision 2 : sous `-w`, le common
   git dir doit rester en écriture (monté `:ro`, `status` marche et `commit` meurt sur
   « Unable to create index.lock »). Et `repos:` n'a pas de champ équivalent — n'ouvrir la porte
   que d'un côté rendrait le modèle asymétrique. Refus explicite, pas un chemin qui n'existe pas.

6. **`--without` / `--only` n'adressent pas les positionnels.** Ils filtrent les `repos:` déclarés,
   comme aujourd'hui. Un repo à la volée se retire en ne le tapant pas.

7. **Les positionnels n'entrent PAS dans l'identité.** `den scratch ~/dev/a` et
   `den scratch ~/dev/b` visent la même sandbox `scratch` : la seconde commande attache la
   première et ne monte rien de neuf. C'est la conséquence directe du §2 — l'identité est
   `<nest>[.<worktree>]`, et rien d'autre ne la compose. C'est aussi la façon la plus probable de
   se faire mordre par un nest « scratch », qui est le cas d'usage vedette ici : l'avertissement de
   l'étape 6 est le SEUL signal, ce qui est la raison pour laquelle il nomme chaque chemin non monté
   plutôt que d'annoncer un compte. Rendre deux montages différents distinguables serait un
   changement de dérivation du nom de sandbox — hors scope, à chiffrer à part.

## Modèle

```go
// internal/nest/resolve.go
type Options struct {
    Agent   string
    Without []string
    Only    []string
    Repos   []string // chemins bruts de la ligne de commande
    Cwd     string   // pour résoudre les relatifs ; requis si Repos est non vide
}

// internal/nest/nest.go
type Repo struct {
    Path     string `yaml:"path"`
    Optional bool   `yaml:"optional"`
    AdHoc    bool   `yaml:"-"` // origine : décide quel endroit l'erreur dit de corriger
}
```

`Cwd` est un paramètre, pas un `os.Getwd()` interne : la couche pure reste pure, et `den scratch .`
devient testable sans dépendre du cwd du test. `spawn.Spawn` et `den nest show` font l'appel
système et le passent. Un `Repos` non vide avec un `Cwd` vide est une **erreur**, pas une résolution
contre le cwd du process : ce serait un repli silencieux sur exactement l'accès système que le
paramètre existe pour sortir de là, et il ne se verrait qu'à l'exécution, sur le mauvais chemin.

`AdHoc` porte `yaml:"-"` : le décodage strict (`KnownFields(true)`) refuserait de toute façon un
`adhoc:` écrit à la main, mais le tag dit l'intention plutôt que de la laisser dépendre d'un effet
du décodeur.

### Normalisation d'un positionnel

Fonction pure `parseRepoArg(cwd, raw string) (Repo, error)`, non exportée comme `selectRepos` et
`checkUniqueNames` : `Resolve` est le seul appelant, et exporter ouvrirait un second chemin
d'entrée dans la fusion. Dans cet ordre :

1. **refus de `:ro`** — avant tout traitement de chemin, sinon l'utilisateur reçoit « chemin
   introuvable » sur un dossier qui existe ;
2. `config.ExpandPath` — le `~`, comme pour `repos:`, `ssh.dir` et `config_dir` ;
3. `filepath.Abs` contre `cwd` — `ExpandPath` ne traite QUE le tilde, et `sbx.checkWorkspace`
   rejette tout chemin relatif. C'est cette étape, et elle seule, qui fait marcher `.` et `../x`.

Une chaîne vide est refusée : `sbx create` recevrait un positionnel vide, qui ne monte rien.

### Fusion

Dans `nest.Resolve`, après `selectRepos` :

```
Resolved.Repos = [positionnels...] ++ selectRepos(déclarés, without, only)
```

puis `checkUniqueNames` sur la liste **fusionnée**. Le basename est l'identité d'un repo — il
adresse `--without`/`--only`, il devient `worktree_root/<wt>/<repo>` et une position dans l'argv de
`sbx create`. Une collision entre un positionnel et un déclaré rend les trois ambigus : erreur
dure, message nommant les deux origines (`déclaré dans nests/api.yaml` / `ligne de commande`),
jamais un silence.

`checkUniqueNames` tourne donc **deux fois**, et les deux appels ne disent pas la même chose. Celui
de `LoadNest` juge un fichier seul et garde son message actuel (« it must be unique within the
nest ») : à ce moment-là la ligne de commande n'existe pas, et l'évoquer enverrait corriger un
endroit qui n'est pour rien dans la collision. Celui de la fusion doit nommer l'origine de chaque
doublon — la fonction prend donc de quoi la dire, plutôt qu'un second exemplaire à garder en phase.

## Séquence de spawn

L'ordre du §6 est inchangé. Seule l'étape 2 gagne du travail, et elle le gagne parce que tout ce
qui est refusable doit l'être **avant le premier effet de bord** — un refus ne doit jamais laisser
un worktree orphelin derrière lui.

### Étape 2 — existence

La boucle actuelle `os.Stat` sur chaque repo garde sa forme ; son message se branche sur
`Repo.AdHoc` :

- déclaré : `` nest "api": repo not found: <p> — fix `repos:` in <fichier> `` (inchangé) ;
- à la volée : `` repo not found: <p> — given on the command line ``.

### Étape 2 — pré-vol git sous `-w` (correction incluse)

`worktree.Ensure` appelle `checkRepo` (`internal/worktree/worktree.go:579`), qui fait un `os.Stat`
et **rien d'autre** : la non-git-ité n'est découverte qu'au `git worktree add`, en étape 3, APRÈS
que les worktrees des repos précédents ont été créés. C'est exactement la régression que
l'ordonnancement de `Spawn` existe pour empêcher, et elle est déjà là aujourd'hui pour un `repos:`
déclaré qui pointe sur un dossier non-git ; les positionnels la rendent atteignable en une frappe.

Correction, ciblée sur ce que la feature traverse : quand `-w` est posé, l'étape 2 sonde
`worktree.CommonGitDir` sur **chaque** repo, déclaré comme à la volée. C'est une lecture pure, elle
donne le verdict git avant tout effet de bord, et l'étape 3 **réutilise** le résultat au lieu de
rappeler git une seconde fois.

Le cache est clé sur `repo.Path`, pas sur le rang dans la liste : deux entrées peuvent viser le même
dépôt (un clone et l'un de ses worktrees), cas que l'étape 3 dédoublonne déjà côté `gitDirs`
(`internal/spawn/spawn.go:354`). Clé sur le chemin, l'alias retombe sur la même sonde et la
réutilisation ne réintroduit pas l'appel qu'elle supprime.

```
$ den api -w feat/x ~/data
erreur: ~/data n'est pas un dépôt git — `-w` propage un worktree sur chaque
  repo du spawn. Retirez `-w`, ou le chemin.
(aucun worktree créé)
```

### Étape 6 — branche attach

`sbx create` prend les workspaces en positionnels et den ne réapplique **rien** à une VM vivante :
les montages sont figés au `create`. `den api ~/dev/hotfix` sur une sandbox déjà là ne peut pas
monter `hotfix`.

On **avertit sans refuser** — même doctrine que `reportDrift` (refuser casserait un `den <nest>`
qui marchait hier ; recréer détruirait un travail en cours), et même forme que
`reportMissingGitDirs` (`internal/spawn/spawn.go:451`), pas un second mécanisme. La drift de mixin
existante ne peut pas couvrir ce cas : les montages sont de l'argv, pas du contenu de mixin.

```
sandbox api already live: attaching
! ~/dev/hotfix is not mounted in this sandbox, and the shell starts in
  /Users/x/dev/api1 as before: mounts and start directory are both fixed at
  create time — `den rm api` then respawn to change either
```

L'avertissement dit les **deux** moitiés parce que la décision 4 en promet une que l'attach ne peut
pas tenir : ne parler que du montage laisserait l'utilisateur croire qu'il va au moins atterrir dans
le chemin qu'il vient de taper. Le répertoire de départ est nommé en toutes lettres, pas décrit —
c'est celui qu'il va voir dans son prompt.

La comparaison porte sur les chemins **calculés** (donc le worktree sous `-w`) contre
`live.Workspaces`, suffixe `:ro` retiré comme le fait `Sandbox.Workdir`. Effet de bord gratuit : le
même avertissement couvre le cas d'un `repos:` ajouté au yaml après le `create`, aujourd'hui
silencieux.

## Ordre des workspaces

```
[positionnels...] [repos déclarés...] [common git dirs...] [profil agent] [ssh.dir si mode "mount"]
```

Aucun code spécial n'est nécessaire pour que les positionnels passent devant le profil agent : ils
sont dans `r.Repos`, que la boucle de l'étape 3 consomme avant tout le reste.

| nest | commande | montages | shell démarre dans |
|---|---|---|---|
| `api` (2 repos) | `den api ~/dev/hotfix` | hotfix, api1, api2, profil agent | hotfix |
| `scratch` (0 repo) | `den scratch ~/dev/a ~/dev/b` | a, b, profil agent | a |
| `scratch` (0 repo) | `den scratch` | profil agent | profil agent (inchangé) |

La dernière ligne est le comportement actuel, conservé tel quel : un nest sans repos ni positionnel
monte son seul profil agent. Rien de neuf n'est refusé ici.

## Tests

Hermétique de bout en bout : aucun `t.Parallel()`, aucun socket, aucun process. La seule brique
système (`os.Getwd`) est sortie de la couche pure par `Options.Cwd`.

**`internal/nest`** — `ParseRepoArg` : `:ro` refusé, `~/x` expansé, `.` et `../x` résolus contre un
`cwd` fourni par le test, chaîne vide refusée. Fusion : ordre positionnels-puis-déclarés, collision
de basename entre les deux origines, `--without` nommant un positionnel.

**`internal/spawn`**, sur `sbx.Fake` :

- nest sans `repos:` + 2 positionnels → argv `[a, b, profil-agent]` ;
- nest avec repos + 1 positionnel → `[adhoc, decl1, decl2, gitdirs…, profil-agent]` ;
- `-w` + positionnel git → `worktree.Ensure` appelé dessus, common git dir monté ;
- `-w` + positionnel non-git → refus, et le `Git` factice n'enregistre **aucun** `worktree add` —
  c'est cette assertion qui prouve « avant le premier effet de bord », pas le message ;
- branche attach → golden de l'avertissement, qui doit porter ses **deux** moitiés (chemin non
  monté ET répertoire de départ inchangé, nommé) : c'est la seule chose qui rattrape la promesse
  que la décision 4 ne tient qu'au `create` ;
- `den scratch ~/dev/a` puis `den scratch ~/dev/b` sur la même sandbox vivante → attach, aucun
  `sbx create`, avertissement nommant `b` : le témoin de la décision 7 ;
- `TestSpawnAddsNoWorkspaceOutsideMountMode` reste vert tel quel : c'est le témoin que la feature
  n'a pas fait fuiter un workspace ailleurs.

**`internal/cli`** — câblage des positionnels (un argument non câblé est silencieux),
`den doctr ~/x` suggère toujours `den doctor`, golden de `den nest show scratch ~/dev/a`.

Goldens édités à la main : il n'existe pas de drapeau `-update` dans ce dépôt.

## Docs

README et spec divergents sont un bug, pas une phase : les deux bougent dans le même changement.

- spec CLI §5 : la ligne devient `den <nest> [path...] [-w <wt>] …` ;
- spec CLI §6 : l'étape de sélection des repos décrit la fusion et le pré-vol git ;
- spec CLI §4.3 : note disant que `repos:` peut être vide, et que le nest garde son rôle (stack,
  egress, env, ports, agents) ;
- README : ligne du tableau des commandes + section spawn avec les trois gestes.

## Hors scope (YAGNI)

- `:ro` sur un positionnel (décision 5) ;
- un `den run` sans fichier nest — amendement du §2, à chiffrer séparément ;
- l'adressage des positionnels par `--without` / `--only` (décision 6) ;
- rendre un montage à la volée persistant (`den nest add-repo`) : le point de la feature est de ne
  PAS écrire dans la config.
