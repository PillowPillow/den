# Migration du Makefile vers go-task

**Date** : 2026-08-04
**Statut** : validé, prêt à planifier

## Pourquoi

den déclare quatre gates (`build`, `test`, `typecheck`, `lint`) dans un Makefile. Le
runner de tâches de l'autre projet Go de l'auteur — `go.dgdev` — est go-task. Faire
diverger deux repos sur le runner de tâches n'achète rien : c'est le même geste
(`task test`) qui doit marcher des deux côtés. Cette migration normalise le runner,
et **rien d'autre**.

Ce que la migration n'est explicitement PAS :

- **Pas un changement de linters.** `go.dgdev` lance `golangci-lint` et `gofumpt`, et
  porte un `.golangci.yml`. den n'en a pas. Porter le *runner* n'est pas porter les
  *linters* : adopter golangci-lint serait une décision séparée, avec son propre coût
  CI et son propre lot de findings à trier. Les quatre gates de den gardent leur
  sémantique au caractère près.
- **Pas un élargissement du set de tâches.** `go.dgdev` a seize tâches (`coverage`,
  `vulncheck`, `mod:tidy`, `release:snapshot`, `completions`…). den en aura cinq. Les
  onze autres répondent à des besoins que den n'a pas ; les ajouter « pendant qu'on y
  est » serait du bloat spéculatif.

## La seule tâche nouvelle : `check`

Un seul agrégat est ajouté, et il est justifié par des faits du repo, pas par imitation
de dgdev :

- `.github/workflows/ci.yml` et `.github/workflows/release.yml` lancent tous deux la
  **même** séquence `lint` → `typecheck` → `test`, dupliquée en trois lignes chacun.
- `CLAUDE.md` et les plans sous `docs/superpowers/plans/` répètent `make lint && make
  test` huit fois.

Cette séquence existe donc déjà ; elle n'a simplement pas de nom. La composition
native de Task (`task: <nom>`) est la seule chose que Task fait strictement mieux que
make sur ce repo — un Makefile l'exprimerait par un chaînage `&&` ou une dépendance de
cible, tous deux plus fragiles.

## Faits établis empiriquement

Les deux points suivants ont été vérifiés sur `task` v3.52.0 avant rédaction, parce
qu'ils gouvernent le design et qu'une supposition fausse sur l'un des deux produirait
une régression silencieuse.

### 1. Les variables globales `sh:` sont évaluées EAGERLY

Un `task <n'importe quoi>` déclenche l'évaluation de **toutes** les vars globales
`sh:`, y compris celles que la tâche appelée ne référence jamais. Vérifié : une var
globale dont la commande écrit sur stderr a émis sa trace lors d'un `task noop` qui ne
la mentionne pas.

Conséquence directe sur le Taskfile de `go.dgdev`, qui déclare `VERSION`, `COMMIT` et
`GOPATH` au niveau global : chaque `task test` là-bas fait tourner `git describe`,
`git rev-parse` et `go env GOPATH` pour rien. C'est bénin, mais c'est un défaut, et le
copier par mimétisme serait le pire des motifs.

**Donc** : `VERSION` est déclarée **dans** la tâche `build`, pas au niveau global.
Vérifié : `task test` ne shelle plus sur git.

### 2. `test -z "$(gofmt -l .)"` survit au shell embarqué

Task n'exécute pas les commandes via `/bin/sh` mais via un interpréteur embarqué
(mvdan/sh). La substitution `$(...)` et le builtin `test -z` y fonctionnent, et un
`test` qui échoue propage bien un exit non-zéro jusqu'au code de retour de `task`.

C'est ce qui autorise à transcrire `lint` littéralement plutôt qu'à le réécrire.

### 3. Fail-fast

Vérifié sur les deux axes :

- **Entre `cmds:` d'une même tâche** : la commande suivant un échec n'est pas lancée.
- **Entre sous-tâches d'un `check`** : `lint` rouge ⇒ `typecheck` et `test` ne tournent
  jamais, et `check` sort non-zéro.

`check` a donc exactement la sémantique de `make lint && make typecheck && make test`.

### 4. Aucun répertoire `.task/`

Task ne crée son cache `.task/` que pour les tâches déclarant `sources:`, `generates:`
ou `status:`. Aucune des cinq n'en déclare. `.gitignore` reste inchangé.

## Le Taskfile

```yaml
version: "3"

tasks:
  build:
    desc: Build den with the version stamped in
    vars:
      VERSION:
        sh: git describe --tags --always --dirty 2>/dev/null || echo dev
    cmds:
      - go build -ldflags "-X github.com/PillowPillow/den/internal/cli.Version={{.VERSION}}" -o den ./cmd/den

  test:
    desc: Run the suite with the cache defeated
    cmds:
      - go test -count=1 ./...

  typecheck:
    desc: Compile every package
    cmds:
      - go build ./...

  lint:
    desc: go vet + enforced gofmt
    cmds:
      - go vet ./...
      - test -z "$(gofmt -l .)"

  check:
    desc: The three gates CI runs
    cmds:
      - task: lint
      - task: typecheck
      - task: test
```

Trois écarts assumés par rapport au Makefile, chacun pour une raison nommée :

1. **`VERSION` scopée à `build`** — voir fait établi n°1.
2. **`$$(...)` devient `{{.VERSION}}`** — le long commentaire du Makefile expliquant
   pourquoi `$$` et pas `$` documentait un piège **make** (make expand `$(...)`
   lui-même, ne trouve pas la variable, et substitue silencieusement la chaîne vide,
   shippant `Version=`). Ce piège n'existe pas sous Task et le commentaire devient
   caduc. Il est remplacé par le commentaire du piège **Task** : la var globale eager.
   Le commentaire ne disparaît pas ; il change de sujet parce que le danger a changé.
3. **`lint` en deux `cmds:` au lieu d'un `&&`** — même fail-fast (vérifié), mais un
   échec nomme lequel des deux gates a sauté, ce que le `&&` ne fait pas.

## Périmètre des modifications

| Fichier | Action | Nature |
|---|---|---|
| `Taskfile.yml` | créé | — |
| `Makefile` | supprimé | — |
| `.github/workflows/ci.yml` | job `checks` : `arduino/setup-task@v2` puis un seul `run: task check` | **fonctionnel** |
| `.github/workflows/release.yml` | job `test` : idem | **fonctionnel** |
| `README.md` (≈ l. 44-52) | `make build` → `task build` ; ajout de la ligne d'installation de task ; le « tell » `dev` renomme `make` → `task` | doc |
| `CLAUDE.md` (l. 13-19) | bloc Commands réécrit, `task check` documenté | doc |
| `.goreleaser.yaml` (l. 1-4) | « Makefile » → « Taskfile » | commentaire |
| `internal/cli/version.go` (l. 6-9) | idem | commentaire |
| `internal/cli/version_test.go` (l. 5-38) | idem | commentaire |

**Vérifié** : `version_test.go` ne fait qu'*évoquer* le Makefile en commentaire. Aucune
assertion, aucun golden, aucune string de test ne contient `make`. La migration ne
touche donc pas le comportement de la suite — ce qui compte, puisque les goldens de ce
repo n'ont pas de flag `-update` et se corrigent à la main.

`arduino/setup-task@v2` doit recevoir deux entrées, chacune contre un mode de panne
distinct : `repo-token: ${{ secrets.GITHUB_TOKEN }}`, parce que l'action résout la
dernière release de task via l'API GitHub et qu'un runner non authentifié se fait
rate-limiter ; et `version: 3.x`, parce que sans contrainte l'action installe la
dernière version tout court — une v4 sortie un mardi casserait alors un CI dont le
code n'a pas bougé.

### Intouchés, délibérément

- `install.sh` et la formule Homebrew : aucune référence à make.
- `.gitignore` : voir fait établi n°4.
- `docs/superpowers/plans/*` et les handoffs **datés** : convention de `CLAUDE.md` —
  ce sont des documents historiques décrivant l'état à leur date, jamais réécrits. Les
  `make lint && make test` qu'ils contiennent sont corrects *pour leur date*.

## Vérification d'acceptation

`task check` vert **ne suffit pas**. Il prouve que les gates tournent, pas que le
stamp de version a survécu — or le stamp est précisément ce que le Makefile
documentait sur douze lignes, et le seul endroit où la migration peut casser quelque
chose sans qu'un test le voie.

```bash
task build && ./den version     # doit répondre v1.0.0-N-gSHA[-dirty], jamais "dev"
go build -o /tmp/den-plain ./cmd/den && /tmp/den-plain version && rm /tmp/den-plain   # doit toujours répondre "dev"
```

Les deux assertions comptent. La première prouve que les ldflags passent encore ; la
seconde prouve que le « tell » documenté — un binaire qui répond `dev` a contourné le
runner — survit à la migration. Un design qui ferait répondre `dev` au premier, ou une
vraie version au second, aurait perdu la propriété que tout ce dispositif existe pour
tenir.

> **Amendement du 2026-08-04, pendant l'implémentation.** La seconde assertion était
> **fausse au moment où ce spec a été écrit**, et pas à cause de la migration : depuis
> que Go stampe `Main.Version` depuis l'état du VCS, un `go build` nu répond une
> pseudo-version et non `dev`. Reproduit sur `main` intact (`a28f04a`), toolchain Go
> 1.26.1. La suite ne l'attrapait pas — elle assertait sur `(devel)`, le placeholder que
> Go n'émet plus dès qu'il y a un `.git`.
>
> L'utilisateur a tranché de réparer plutôt que de différer, parce que ce chantier
> réécrit précisément les phrases qui affirment cette propriété. Une **Task 5** a donc
> été ajoutée au plan (`docs/superpowers/plans/2026-08-04-taskfile-migration.md`) :
> `resolveVersion` gagne un paramètre disant si le build vient d'un checkout local,
> détecté par la présence d'un réglage `vcs.revision`. Le correctif rend l'assertion
> ci-dessus vraie ; il ne la modifie pas. Le périmètre de la branche s'en trouve élargi
> d'un correctif qui n'est pas une migration de runner — c'est assumé et daté ici pour
> qu'une relecture ultérieure ne prenne pas ce spec pour la description complète de la
> branche.

## Blocage connu

Les écritures dans `.github/` sont refusées par une règle globale (`~/.claude/settings.json`).
Les deux workflows sont **le seul endroit où la migration est fonctionnellement
cassante** : merger la suppression du `Makefile` sans toucher CI met `main` au rouge
au premier push. La levée temporaire du deny, ou l'application manuelle des deux
diffs, est un prérequis d'implémentation — pas une finition.

## Hors scope

`test -z "$(gofmt -l .)"` avale la liste des fichiers non formatés : le gate échoue
sans dire lesquels. Le papercut est réel et préexistant ; le corriger dans cette
migration mélangerait un changement de comportement à une transcription mécanique.
