# Plan — `den init` : le binaire sait créer son propre `~/.den`

**Date** : 2026-08-03
**Branche** : `feat/den-init` (au-dessus de `build/release-pipeline`, PR #36)

## Contexte

Le pipeline de release (#36 : goreleaser, cask Homebrew, archives, `go install`) a ouvert un trou :
`README.md` §Bootstrapping dit encore

```bash
cp -R examples/den-home ~/.den
```

ce qui ne fonctionne QUE depuis un checkout. Un utilisateur `brew install --cask`, `go install` ou
archive n'a aucun `examples/den-home` sur disque, donc aucun moyen de créer `~/.den`. Les trois
canaux ajoutés par #36 n'ont pas de chemin de bootstrap.

Deux options ont été rejetées, et le sont définitivement :

- **création paresseuse au premier run** (dans `config.Home` ou dans le spawn) : contraire à §2 — den
  refuse plutôt que de normaliser en silence — et écrire de la config comme effet de bord d'un
  `den ls` est un effet de bord non demandé.
- **hook `hooks.post.install` du cask écrivant dans `$HOME`** : un cask ne doit pas écrire dans le
  home ; il tourne à l'installation, avant que l'utilisateur ait choisi son `DEN_HOME` ; et il
  laisserait `go install` sans rien. Le cask ship le binaire, point (le hook `xattr` actuel touche
  le staged path, il reste).

Un den « zero-config » n'est pas viable non plus : après défauts, restent irréductibles
`agents.<n>.update` (den ne peut pas deviner la commande de mise à jour d'un agent arbitraire —
`validate.go:90`, spec §9.1), `defaults.agent` / `defaults.stack` (`validate.go:104-116`), et un
stack avec `image:` que l'utilisateur doit avoir `den build`. Un fichier de config est incompressible.

Le mécanisme retenu est donc : **`den init` matérialise un home depuis un template embarqué dans le
binaire (`go:embed`)** — seul mécanisme qui couvre cask + `go install` + archive uniformément.

## Contraintes globales

Elles lient CHAQUE tâche.

1. **`make lint`, `make typecheck` et `make test` doivent être verts** en fin de tâche.
   `make lint` = `go vet ./...` + `gofmt -l .` vide : gofmt est imposé, pas conseillé.
   `make test` = `go test -count=1 ./...` (`-count=1` défait le cache).
2. **Hermétisme des tests** (CLAUDE.md, spec §12) : aucun `t.Parallel()`, aucune socket, aucun
   processus lancé. **Aucun test n'écrit dans le vrai `~/.den`** : un test de `den init` passe
   TOUJOURS par `t.TempDir()` + `--den-home`. Un test qui pourrait, sur une machine mal configurée,
   toucher `$HOME` est un échec de revue.
3. **Aucun nouveau champ dans `cli.Deps`.** Le repo écrit sous le den home via `os.*` directement
   (`agent/mixin.go:225`, `spawn/spawn.go:369`, `worktree/worktree.go:460`) ; `cli.Deps`
   (`internal/cli/root.go:26-63`) n'injecte que ce qui SORT du home ou touche une ressource globale
   de la machine (sockets, processus, tty). L'hermétisme vient de la redirection de `denHome`.
4. **Langue** : code, commentaires et messages utilisateur en **anglais**. Le spec et les handoffs
   sous `docs/superpowers/` sont en **français**. `README.md` est en anglais.
5. **Style de commentaires** : le commentaire long « pourquoi » au site de décision — ce qui a été
   rejeté et quelle régression le choix évite. Du code terse détonne visiblement ; s'aligner sur la
   densité environnante.
6. **Doctrine d'erreur** (spec §2) : une erreur nomme le fichier à corriger ET le remède. den refuse
   plutôt que de normaliser en silence.
7. **YAML strict inchangé** (`KnownFields(true)`, spec §12) : aucune tâche n'assouplit le décodage.
8. **Goldens** : comparés à la main, il n'y a **pas** de flag `-update`. Un golden se modifie à la
   main.
9. **Commits** : conventional commits, au moins un commit par tâche.
10. **README et spec sont UN contrat avec l'implémentation** (CLAUDE.md : une divergence est
    désormais un bug, plus une phase). `internal/cli/ports_test.go` (C17, ~:1214) teste déjà que la
    ligne §5 du spec correspond à la signature réelle de `den ports` — la même discipline s'applique
    à toute commande ajoutée.

## Ordre et dépendances

```
T1 (template réparé) ──┐
                       ├──> T3 (den init + embed) ──> T5 (docs)
T2 (config_dir défaut)─┘
T4 (remède fs.ErrNotExist)  — indépendante
```

T1 bloque T3 : `go:embed` ne peut pas représenter un répertoire vide, donc le bug `kit: ./kit` doit
mourir AVANT d'embarquer quoi que ce soit. T2 bloque T3 : c'est ce qui rend `den init` une copie
bête, sans substitution.

---

## Task 1 — le home d'exemple passe `den doctor` (sauf le repo d'exemple)

### Problème

`examples/den-home/stacks/devx/stack.yaml` déclare `kit: ./kit`, mais `git ls-files examples/` ne
renvoie que trois fichiers : il n'y a pas de répertoire `kit/` (git ne suit pas un répertoire vide).
Vérifié empiriquement sur cette machine :

```
$ go run ./cmd/den --den-home examples/den-home doctor
[FAIL] stack devx       kit not found: .../examples/den-home/stacks/devx/kit
[FAIL] nest example     repo not found: /Users/polochon/dev/my-project
den: 2 failing check(s)   exit=1
```

Deux conséquences : (a) le flux `cp -R` actuel produit déjà un home qui échoue même après que
l'utilisateur ait corrigé les deux fichiers que le README nomme ; (b) `go:embed` ne peut PAS
embarquer un répertoire vide, donc une copie octet héritera du bug sans échappatoire.

Et `README.md:42-44` promet « deux diagnostics attendus : `sbx` manquant si non installé, et le repo
d'exemple introuvable ». Sur cette machine `sbx` EST installé et doctor échoue quand même deux fois.
La phrase est fausse.

### À faire

1. `examples/den-home/stacks/devx/stack.yaml` : supprimer la ligne `kit: ./kit`. Les kits sont
   optionnels — `DeclaredKits` (`internal/config/stack.go:77-86`) filtre les entrées vides. Garder un
   exemple commenté si utile, en s'assurant qu'il reste commenté.
2. `README.md:42-44` : corriger la phrase pour dire la vérité — après un bootstrap, le seul
   diagnostic bloquant attendu est le repo d'exemple introuvable (plus `sbx` manquant s'il ne l'est
   pas). Ne pas promettre un nombre qui dépend de la machine sans le dire.

### Test

Ajouter, dans le package qui possède déjà la validation de l'exemple (`internal/config/example_test.go`
tient `TestExampleDenHomeIsValid`) ou dans `internal/doctor`, un test qui fait tourner `doctor.Run`
sur `examples/den-home` avec des `doctor.Deps` **injectées** (`internal/doctor/fake.go` existe) et
qui affirme que **le seul check bloquant est celui du nest d'exemple** (`repo not found`). C'est ce
qui verrouille la promesse du README au lieu de la laisser dériver.

Le test doit être hermétique : `doctor.Deps` fournit `LookPath`, `Stat`, `GitVersion`, `Getenv`,
`SSHAgent`, `GOOS` — aucun accès réel à la machine, et `GOOS` explicite pour que le message ne
dépende pas de l'OS du runner (même raison que le commentaire de `doctor.Deps.GOOS`).

### Critères d'acceptation

- `stack.yaml` d'exemple ne déclare plus de kit inexistant.
- Un test échoue si un futur ajout au home d'exemple réintroduit un check bloquant autre que le nest.
- `README.md` ne promet plus un décompte de diagnostics faux.

---

## Task 2 — `config_dir` prend un défaut, au chargement

### Problème

`agents.<n>.config_dir` est **requis sans défaut** (`internal/config/validate.go:83-84`), et le
template d'exemple le code en dur : `config_dir: ~/.den/agents/claude`. Copié tel quel par un
`den init --den-home /tmp/foo`, il produirait une config qui pointe vers le VRAI `~/.den`.

La correction naïve serait de substituer le home résolu au moment du `init`. C'est le mauvais
mécanisme, pour deux raisons :

- le repo contient déjà le bon précédent, un champ plus loin : `worktree_root` se défausse à
  `filepath.Join(denHome, "worktrees")` dans `LoadGlobalUnvalidated` (`config.go:73-75`), et
  `denHome` est en portée dans la boucle même qui expand `ConfigDir` (`config.go:83-88`) ;
- la substitution CRÉE un défaut qu'elle prétend éviter : `den init --den-home /tmp/foo` figerait
  `/tmp/foo/agents/claude` dans le fichier ; si le home bouge ensuite (ou si `DEN_HOME` change), le
  profil agent pointe silencieusement vers l'ancien emplacement. Un défaut calculé au CHARGEMENT se
  recalcule contre le home vivant.

Ce n'est pas une violation de « refuser plutôt que normaliser » : den normalise déjà exactement cette
forme d'absence pour `worktree_root` et pour `ssh.mode` (`config.go:67-75`).

### À faire

1. `internal/config/config.go`, dans la boucle `for name, a := range g.Agents` (:83-88) : si
   `a.ConfigDir == ""`, le remplir avec `filepath.Join(denHome, "agents", name)` **avant**
   `ExpandPath`. Le test doit être `== ""` exactement, pas `TrimSpace(...) == ""` — c'est la même
   forme que `worktree_root` (`config.go:73` teste `== ""`, `validate.go:157` refuse le blanc), et
   c'est ce qui laisse `config_dir: "   "` atteindre la validation au lieu d'être silencieusement
   remplacé.
2. `internal/config/validate.go:83-84` : le `required` ne peut plus se déclencher sur l'absence (elle
   est défaussée) ; **garder** le refus du blanc-seulement, sur le modèle documenté en
   `validate.go:148-151`. Le message doit rester exact : il ne peut plus dire « required » pour un
   champ qui a un défaut.
3. Le commentaire « pourquoi » au site du défaut doit dire ce que la substitution-au-init aurait
   cassé (le chemin figé qui survit au déplacement du home). C'est la décision, pas la ligne de code,
   qui mérite le commentaire.
4. `examples/den-home/config.yaml` : supprimer les DEUX lignes qui codent `~/.den` en dur —
   `config_dir:` (désormais défaussé) et `worktree_root: ~/.den/worktrees` (déjà défaussé par
   `config.go:73`, confirmé par `TestLoadGlobalDefaultsApplied`). Le template devient portable sous
   n'importe quel `--den-home`, ce qui est la précondition de la tâche 3.
5. **Spec §4.1** (`docs/superpowers/specs/2026-07-27-den-cli-design.md`, autour des lignes 103-130,
   en français) : consigner le défaut de `config_dir`. Le spec montre `config_dir: ~/.den/agents/claude`
   à la ligne 110 ; il doit désormais dire que le champ est optionnel et que son défaut est
   `<den home>/agents/<nom de l'agent>`. Si `worktree_root` (ligne 126) n'y est pas non plus documenté
   comme défaussable, le corriger dans le même geste — le spec est la source de vérité sur l'intention
   et une divergence est un bug.

### Tests

- le défaut est appliqué en l'absence de `config_dir`, et il est relatif au **den home courant** (pas
  littéralement `~/.den`) — même forme d'assertion que `TestLoadGlobalDefaultsApplied`
  (`config_test.go:114-118`) ;
- `config_dir: "   "` est toujours refusé, avec un message exact ;
- un `config_dir` explicite gagne toujours, et son `~` est toujours expandé (non-régression de
  `TestLoadGlobalFullFields`).

### Critères d'acceptation

- `examples/den-home/config.yaml` ne contient plus la chaîne `~/.den`.
- Un chargement sous `--den-home <tmp>` résout `config_dir` sous `<tmp>`, jamais sous le vrai home.
- Spec §4.1 décrit le défaut.

---

## Task 3 — `den init`

### Mécanisme d'embed

`go:embed` ne peut pas remonter au-dessus du répertoire de son package, donc un embed depuis
`internal/…` obligerait à dupliquer `examples/den-home` dans le package. Deux options ont été
écartées : un test de parité octet entre les deux copies (c'est exactement la forme « deux copies
synchronisées à la main » que les commentaires « sole definition » du repo interdisent —
`paths.go:39-42`, `mixin.go` mixinDir/mixinPath), et la suppression d'`examples/` (on perd l'exemple
consultable, et `internal/config/example_test.go:16` le lit).

**La racine du module ne contient aucun package Go.** L'option retenue est donc : un
`embed.go` à la racine du dépôt, `package den`, portant `//go:embed examples/den-home` et exposant
un `embed.FS` exporté. Une seule copie, pas de test de parité, `examples/` reste consultable et
`example_test.go` reste intact.

Précaution : `go:embed` ignore les fichiers commençant par `.` ou `_` et ne peut pas embarquer un
répertoire vide (cf. tâche 1). Vérifier que l'arbre embarqué contient bien les trois fichiers
attendus — un test le fait, ce n'est pas à croire sur parole.

### Sémantique

**`den init` refuse quand `config.yaml` existe déjà.** Sonder `config.GlobalPath(denHome)`
(`paths.go:40-42`, la définition unique) : si le fichier est là, refuser avec
`already initialized: <chemin>` et sortir en erreur, sans rien écrire.

La sémantique alternative — « par fichier, crée ce qui manque, ne réécris jamais » — a un bug de
résurrection : un utilisateur qui supprime `nests/example.yaml` volontairement le verrait recréé par
tout `den init` ultérieur, et un nest d'exemple ressuscité n'est pas inerte (il apparaît dans
`den nest ls` et fait ÉCHOUER `den doctor` sur `repo not found`, `doctor.go:373-377`). Sonder le
fichier et non le répertoire règle aussi le cas de l'utilisateur qui a fait `mkdir ~/.den` : le
répertoire existant ne bloque pas.

Pas de flag `--force` : personne ne l'a demandé, il est destructeur, et le refus qui nomme un chemin
est le style de la maison.

### À faire

1. `embed.go` à la racine (`package den`) : l'`embed.FS` du home d'exemple, avec le commentaire
   « pourquoi la racine » (le `..` interdit par `go:embed`, la copie unique).
2. Un package `internal/deninit` (nom à confirmer par l'implémenteur, cohérent avec le voisinage)
   exposant une fonction testable qui prend `(denHome string, src fs.FS, out io.Writer)` :
   - refuse si `config.GlobalPath(denHome)` existe ;
   - recopie l'arbre embarqué sous `denHome` en créant les répertoires nécessaires ;
   - imprime ce qui a été créé, puis les deux étapes suivantes (éditer le nest, lancer `den doctor`).
   Le `fs.FS` est un PARAMÈTRE, pas une variable de package : c'est ce qui permet à un test de
   fournir un `fstest.MapFS` et de ne rien devoir au contenu réel d'`examples/`.
3. `internal/cli/init.go` : `newInitCmd(&denHome)` sur le modèle de `newDoctorCmd`
   (`internal/cli/doctor.go`) — `Args: noArgs`, résolution via `config.Home(*denHome)`, écriture sur
   `cmd.OutOrStdout()`. Le câbler dans `NewRootCmdWith` (`root.go`), auprès des autres
   `root.AddCommand(...)`. **Aucun nouveau champ `cli.Deps`** (contrainte globale 3).
4. Permissions : répertoires `0o755`, fichiers `0o644`. Ne PAS pré-créer `agents/` ni `cache/mixins/`
   — `spawn.go:369` crée le profil (0o755) et `mixin.go:225` le cache (0o700), parents inclus,
   paresseusement ; les pré-créer n'ajouterait qu'une troisième politique de permissions à défendre.
   Ne pas créer `lib/` non plus : c'est une convention optionnelle (README:218-219, spec §80, §337)
   qu'aucun loader ne consomme.
5. Pas de machinerie anti-traversée : `init` écrit des noms relatifs FIXES sous un home déjà résolu
   par `filepath.Abs` (`paths.go:33`) ; contrairement au `sandboxName` contrôlé par l'utilisateur de
   `mixin.go:214-223`, aucun composant de chemin n'est attaquable ici.

### Tests

Tous hermétiques, `t.TempDir()` uniquement :

- home vierge : les trois fichiers attendus sont créés, aux bons chemins relatifs ;
- le home ainsi créé **charge et valide** (`config.LoadGlobal`) — c'est le lien avec T2 : sans
  substitution, un home créé sous `/tmp/...` doit résoudre `config_dir` sous `/tmp/...` ;
- `config.yaml` déjà présent : refus, message nommant le chemin, **et aucun fichier écrit ni
  écrasé** (vérifier le contenu de l'existant après l'appel) ;
- répertoire existant mais vide : accepté (le cas `mkdir ~/.den`) ;
- l'arbre embarqué contient bien les fichiers attendus (garde-fou contre un `go:embed` qui n'aurait
  rien pris).

### Critères d'acceptation

- `den init --den-home <tmp>` suivi de `den doctor --den-home <tmp>` donne un home dont le SEUL
  échec est le repo du nest d'exemple.
- Un second `den init` sur le même home refuse et n'écrit rien.
- Aucun test n'écrit hors de son `t.TempDir()`.

---

## Task 4 — le remède `den init` là où le config manque, et nulle part ailleurs

### Problème

Sur une machine fraîche, la première commande d'un utilisateur brew est `den <nest>` ou
`den doctor`, jamais `den init`. Aujourd'hui il reçoit un `file does not exist` nu, alors que
CLAUDE.md pose que l'erreur nomme le fichier à corriger ET le remède.

### À faire

Un seul site, pas N. Dans `LoadGlobalUnvalidated` (`internal/config/config.go:52-60`), la lecture
échoue déjà dans un wrap unique :

```go
return nil, fmt.Errorf("reading %s: %w", path, &FileError{Err: err})
```

Y ajouter le remède **uniquement** quand `errors.Is(err, fs.ErrNotExist)`. Tous les appelants de
`LoadGlobal` en héritent, et `internal/doctor/doctor.go:196-199`
(`add("config.yaml", false, "%v", err)`) aussi — c'est la doctrine « définition unique » de
`paths.go:39`.

Le gardage est la moitié importante de la tâche : un config **illisible** (YAML cassé, permission
refusée, `is a directory`) ne doit PAS proposer `den init` — `den init` refuserait de toute façon
(tâche 3), et suggérer d'initialiser un home qui existe déjà invite à écraser. La chaîne d'erreurs
survit au wrap (`%w` en `config.go:59`, `FileError.Unwrap` en `file.go:47`), donc `errors.Is` est le
bon test.

### Tests

- config absent : le message contient le chemin ET le remède `den init` ;
- permission refusée (ou `is a directory`) : le message ne contient PAS `den init` — se construire
  le cas via `t.TempDir()` (un répertoire nommé `config.yaml` donne `EISDIR` sans dépendre des
  droits du runner, ce qui reste hermétique) ;
- YAML invalide : pas de remède `den init` non plus (l'échec vient de `DecodeYAMLStrict`, pas du
  wrap — vérifier que rien ne l'y a ajouté) ;
- `errors.Is(err, fs.ErrNotExist)` reste vrai après l'ajout (non-régression de la chaîne :
  `internal/nest` et `internal/spawn` en dépendent, cf. le godoc de `FileError`).

### Critères d'acceptation

- `den doctor --den-home <tmp vide>` affiche le remède sur la ligne `config.yaml`.
- Aucun autre mode d'échec de lecture ne le mentionne.

---

## Task 5 — les points d'entrée décrivent `den init`

Une commande qui n'est pas dans les deux tables n'existe pas, selon la discipline du repo (contrainte
globale 10).

### À faire

1. **`README.md` §Bootstrapping** : remplacer le bloc `cp -R examples/den-home ~/.den` par

   ```bash
   den init
   $EDITOR ~/.den/nests/example.yaml
   den doctor
   ```

   Deux corrections portées par ce bloc : `den doctor`, pas `./den doctor` (un utilisateur brew ou
   `go install` n'a pas de `./den`), et le fichier à éditer est le **nest**, pas `config.yaml` —
   après la tâche 2, `config.yaml` peut n'avoir besoin d'aucune retouche, alors que le nest pointe
   toujours vers `~/dev/my-project`. La phrase sur les diagnostics attendus (déjà retouchée en T1)
   doit rester cohérente avec ce flux.
2. **Table des commandes du README** (`README.md:53-64`) : ajouter la ligne `den init`.
3. **Spec §5** (`docs/superpowers/specs/2026-07-27-den-cli-design.md:214-225`, **en français**) :
   ajouter la ligne correspondante dans la table. Respecter la forme des lignes existantes
   (`| \`den <commande>\` | rôle |`). `internal/cli/ports_test.go` (C17) ne teste que la ligne
   `den ports`, mais la même discipline s'applique : la ligne doit décrire la signature réelle.
4. **`docs/superpowers/handoffs/HANDOFF.md`** (~:202) : il documente le bootstrap `cp -R` et se
   déclare courant à `v1.0.0`. Le mettre à jour, sinon il devient exactement l'artefact périmé que
   CLAUDE.md catalogue.

### Critères d'acceptation

- Aucune occurrence restante de `cp -R examples/den-home` dans README ou HANDOFF.
- `den init` apparaît dans la table README et dans la table spec §5.
- La suite reste verte (C17 lit le spec : une table cassée le fait échouer).
