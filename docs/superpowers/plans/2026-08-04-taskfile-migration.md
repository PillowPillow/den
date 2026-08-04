# Migration du runner de tâches vers go-task — plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remplacer le `Makefile` de den par un `Taskfile.yml` go-task, à sémantique de gate identique, et purger les six endroits qui nomment encore `make`.

**Architecture:** Un `Taskfile.yml` porte les quatre gates existantes (`build`, `test`, `typecheck`, `lint`) plus un agrégat `check` qui les compose. Le `Makefile` est supprimé — pas de shim délégant, qui pourrait dériver. Les deux workflows GitHub passent de trois `run: make X` à un seul `run: task check`. Le reste est de la documentation et des commentaires.

**Tech Stack:** go-task v3 (probé en v3.52.0), Go (toolchain de `go.mod`), GitHub Actions, `arduino/setup-task@v2`.

**Spec de référence :** `docs/superpowers/specs/2026-08-04-taskfile-migration-design.md`

## Global Constraints

- **La sémantique des quatre gates ne change pas d'un caractère.** `test` reste `go test -count=1 ./...`, `typecheck` reste `go build ./...`, `lint` reste `go vet ./...` puis `test -z "$(gofmt -l .)"`, `build` reste le même `go build -ldflags` sur le même symbole. Aucun linter n'est ajouté : **pas de golangci-lint, pas de gofumpt**, quoi qu'en fasse `go.dgdev`.
- **Symbole ldflags, verbatim :** `github.com/PillowPillow/den/internal/cli.Version`. Toute divergence casse silencieusement `den version`.
- **Expression de version, verbatim :** `git describe --tags --always --dirty 2>/dev/null || echo dev`.
- **`VERSION` ne doit JAMAIS être déclarée sous un `vars:` de premier niveau.** Task évalue les vars globales `sh:` *eagerly* : un `task test` exécuterait `git describe` pour une valeur qu'il ne lit pas. Elle vit dans la tâche `build`.
- **Version de go-task en CI : `3.x`**, et `repo-token: ${{ secrets.GITHUB_TOKEN }}`. Sans le token l'action se fait rate-limiter par l'API GitHub ; sans la contrainte de version une v4 casserait un CI dont le code n'a pas bougé.
- **Code, commentaires et messages user-facing en anglais.** Ce plan et le spec sont en français ; tout ce qui atterrit dans le repo est en anglais (convention `CLAUDE.md`).
- **Le style de commentaire de den est le long « why » au site de décision** — ce qui a été rejeté, et quelle régression le choix prévient. Un `Taskfile.yml` nu détonnerait visiblement.
- **Ne pas toucher** : `install.sh`, la formule Homebrew, `.gitignore`, `docs/superpowers/plans/*` antérieurs, et les handoffs **datés**. Ce sont des documents historiques : leurs `make lint && make test` sont corrects *pour leur date*.
- **Aucune tâche ne déclare `sources:`, `generates:` ni `status:`** — c'est ce qui garantit qu'aucun répertoire `.task/` n'apparaît et que `.gitignore` reste intact.

## Note sur la forme des tâches

Ce plan n'a pas de cycle red/green classique : la migration ne produit aucun code de production nouveau, et les gates **sont** les tests. Chaque tâche porte donc une vérification exécutable et son résultat attendu, à lancer et à lire avant de commiter. La seule assertion de fond du chantier vit dans la Task 1 : le stamp de version doit survivre, et c'est la chose qu'une suite verte ne verrait pas.

## Structure des fichiers

| Fichier | Responsabilité | Tâche |
|---|---|---|
| `Taskfile.yml` | **créé** — l'unique runner : 4 gates + `check` | 1 |
| `Makefile` | **supprimé** | 1 |
| `.github/workflows/ci.yml` | installe task, lance `task check` | 2 |
| `.github/workflows/release.yml` | idem, job `test` | 2 |
| `README.md` | dit au contributeur comment installer task et builder | 3 |
| `CLAUDE.md` | bloc Commands + note d'artefact périmé | 3 |
| `.goreleaser.yaml` | commentaire d'en-tête | 4 |
| `internal/cli/version.go` | commentaire de `resolveVersion` | 4 |
| `internal/cli/version_test.go` | commentaires d'en-tête et de cas | 4 |

**Ordre voulu.** La Task 2 (CI) vient tôt et non en dernier parce qu'elle porte le seul blocage connu du chantier — les écritures `.github/` sont refusées par une règle globale. Un blocage doit se manifester avant le travail cosmétique, pas après. Entre la Task 1 et la Task 3, le `README.md` mentira (il dira `make build` alors que le `Makefile` n'existe plus) : c'est assumé, la fenêtre est interne à la branche.

---

### Task 1: Le Taskfile remplace le Makefile

**Files:**
- Create: `Taskfile.yml`
- Delete: `Makefile`

**Interfaces:**
- Consumes: rien.
- Produces: les cinq noms de tâches sur lesquels toutes les tâches suivantes s'appuient — `build`, `test`, `typecheck`, `lint`, `check`. La Task 2 invoque `task check` ; les Tasks 3 et 4 les nomment en prose.

- [ ] **Step 1: Constater la ligne de base**

Avant de rien changer, établir que l'arbre est vert *avec make*. Sinon une rougeur post-migration sera imputée à tort à la migration.

```bash
make lint && make typecheck && make test && echo "BASELINE GREEN"
```

Attendu : `BASELINE GREEN` en dernière ligne. Si ça ne l'est pas, **arrêter** et signaler — le chantier ne démarre pas sur un arbre rouge.

- [ ] **Step 2: Relever la réponse de référence du stamp**

C'est le contrat que la migration doit préserver. Le noter pour comparer au Step 6.

```bash
make build && ./den version
```

Attendu : une chaîne de la forme `den v1.0.0-N-gSHA` (avec `-dirty` si l'arbre est sale), **jamais** `den dev`. Noter la valeur exacte.

- [ ] **Step 3: Créer `Taskfile.yml`**

Contenu intégral du fichier :

```yaml
# The task runner den's four gates and its one build live in. `-X ...cli.Version`
# is what makes a binary say which code it runs; without it `den version` answers
# `dev` on a user's machine, which names nothing. The value comes from
# `git describe`, so a tagged build says `v1.0.0` and every other one says where
# it sits relative to the last tag (`v1.0.0-3-gabc1234-dirty`) — the release case
# and the working-tree case, from one expression. The `|| echo dev` covers the two
# ways describe legitimately fails (no git, or a tarball with no .git); an
# unreadable version is a worse answer than `dev`.
version: "3"

tasks:
  build:
    desc: Build den with the version stamped in — the only documented way to build
    # VERSION is declared HERE, and not under a top-level `vars:`, because Task
    # evaluates global dynamic vars EAGERLY: every `task test` would shell out to
    # `git describe` for a value it never reads. Probed against task v3.52.0 on
    # 2026-08-04 — a global var whose command writes to stderr emitted its trace
    # under a `task` invocation that never mentions it. Scoping it to its one
    # consumer is the whole fix.
    #
    # And spell `{{.VERSION}}` carefully. Task renders an unknown variable as the
    # empty string, silently, exit 0 (probed the same day): a typo here does not
    # fail the build, it ships `Version=` — a binary that answers `den ` with
    # nothing after it. This is the same trap the Makefile's `$$(...)` note
    # guarded against, under a different spelling, which is why the check that
    # settles a release is `./den version` AGREEING with `git describe` rather
    # than merely differing from `dev`.
    vars:
      VERSION:
        sh: git describe --tags --always --dirty 2>/dev/null || echo dev
    cmds:
      - go build -ldflags "-X github.com/PillowPillow/den/internal/cli.Version={{.VERSION}}" -o den ./cmd/den

  test:
    desc: Run the suite with the build cache defeated
    # -count=1 is not decoration: a plain `go test` can report a pass that belongs
    # to an earlier tree.
    cmds:
      - go test -count=1 ./...

  typecheck:
    desc: Compile every package
    cmds:
      - go build ./...

  lint:
    desc: go vet plus an enforced gofmt
    # Two cmds rather than one `&&`: Task stops at the first failing command
    # either way, but a red run then names which of the two gates went down
    # instead of collapsing both into a single exit code. gofmt is enforced here,
    # not advised.
    cmds:
      - go vet ./...
      - test -z "$(gofmt -l .)"

  check:
    desc: The three gates, fail-fast — what CI runs, and what to run before a commit
    # The sequence predates the task: ci.yml and release.yml both spelled it out
    # as three separate steps, and the plans repeat it by hand. Naming it removes
    # the duplication and, with it, the chance the two workflows drift apart.
    cmds:
      - task: lint
      - task: typecheck
      - task: test
```

- [ ] **Step 4: Supprimer le `Makefile`**

```bash
git rm Makefile
```

Pas de shim délégant : deux runners pour un geste, c'est exactement la double vérité que ce chantier supprime.

- [ ] **Step 5: Vérifier les gates**

```bash
task check && echo "CHECK GREEN"
```

Attendu : les trois gates s'enchaînent, `CHECK GREEN` en dernière ligne.

Puis vérifier le fail-fast, qui est la propriété que `check` doit à `&&` :

```bash
task lint && task typecheck && task test && echo "GATES GREEN INDIVIDUALLY"
```

Attendu : `GATES GREEN INDIVIDUALLY`.

- [ ] **Step 6: Vérifier le stamp — l'acceptation réelle**

`task check` vert ne prouve rien sur le stamp. Ces deux assertions sont le cœur du chantier.

```bash
task build && ./den version
```

Attendu : **exactement** la valeur notée au Step 2. Une réponse `den dev` signifie que les ldflags ne passent plus — probablement un `{{.VERSION}}` mal orthographié, que rien d'autre ne signalerait.

```bash
go build -o /tmp/den-plain ./cmd/den && /tmp/den-plain version && rm /tmp/den-plain
```

Attendu : `den dev`. C'est le « tell » documenté — un binaire qui répond `dev` a contourné le runner — et il doit survivre à la migration. S'il répond une vraie version, la propriété est perdue.

- [ ] **Step 7: Vérifier qu'aucun cache n'est apparu**

```bash
test ! -e .task && echo "NO TASK CACHE" ; git status --short
```

Attendu : `NO TASK CACHE`, et un `git status` qui ne montre que `Taskfile.yml` ajouté, `Makefile` supprimé — pas de `.task/`, pas de fichier surprise. Si `.task/` existe, une tâche a acquis un `sources:`/`status:` qu'elle ne devait pas avoir.

- [ ] **Step 8: Commit**

```bash
git add Taskfile.yml
git commit -m "build: go-task replaces make as the one task runner

den drove four gates through a Makefile while go.dgdev, the other Go repo,
drives the same gates through go-task. Two runners for one gesture buys
nothing, so the Makefile goes rather than gaining a delegating shim that could
drift from the Taskfile behind it.

The gates keep their semantics to the character — dgdev's golangci-lint and
gofumpt stay out, and so do its eleven other tasks. One task is added, check,
and it comes from this repo rather than from dgdev: both workflows already ran
lint/typecheck/test as three separate steps, so the sequence existed and simply
had no name.

VERSION sits inside build rather than in a top-level vars: block because Task
evaluates global dynamic vars eagerly — dgdev's Taskfile therefore shells out to
git on every task test, and copying that shape by mimicry would import the
defect along with the runner.

The Makefile's long note on \$\$ versus \$ documented a make hazard that does not
exist here; the comment is not dropped but re-aimed at the Task one."
```

---

### Task 2: Les deux workflows GitHub

**Files:**
- Modify: `.github/workflows/ci.yml` (en-tête l. 1-2, job `checks`)
- Modify: `.github/workflows/release.yml` (job `test`)

**Interfaces:**
- Consumes: la tâche `check` produite par la Task 1.
- Produces: rien pour les tâches suivantes.

> **⚠️ Blocage à lever avant de commencer.** Les écritures dans `.github/` sont refusées par une règle globale (`~/.claude/settings.json`). Cette tâche est **le seul endroit fonctionnellement cassant** du chantier : merger la suppression du `Makefile` sans elle met `main` au rouge au premier push. Deux issues, à trancher avec l'utilisateur avant le Step 1 : lever le deny le temps de la tâche puis le restaurer, ou fournir les deux diffs à appliquer à la main. Ne pas contourner la règle.

- [ ] **Step 1: Modifier l'en-tête de `ci.yml`**

Remplacer les deux premières lignes :

```yaml
# The same three gates a local dev runs (make lint / typecheck / test), on every
# push and PR. The `checks` job is hermetic by construction — no socket, no
```

par :

```yaml
# The same three gates a local dev runs (`task check`), on every push and PR.
# The `checks` job is hermetic by construction — no socket, no
```

- [ ] **Step 2: Modifier le job `checks` de `ci.yml`**

Remplacer :

```yaml
  checks:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: make lint
      - run: make typecheck
      - run: make test
```

par :

```yaml
  checks:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      # `version` is pinned to the v3 line and not left to float: the action
      # otherwise installs whatever is newest, and a task v4 released on a
      # Tuesday would break a CI whose code has not moved. `repo-token` is what
      # keeps the release lookup off the unauthenticated GitHub API rate limit.
      - uses: arduino/setup-task@v2
        with:
          version: 3.x
          repo-token: ${{ secrets.GITHUB_TOKEN }}
      - run: task check
```

Le job `install-script` n'est pas touché : il n'utilise pas make.

- [ ] **Step 3: Modifier le job `test` de `release.yml`**

Remplacer :

```yaml
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: make lint
      - run: make typecheck
      - run: make test
```

par :

```yaml
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - uses: arduino/setup-task@v2
        with:
          version: 3.x
          repo-token: ${{ secrets.GITHUB_TOKEN }}
      - run: task check
```

Le job `release` n'est pas touché : goreleaser ne passe pas par le runner.

- [ ] **Step 4: Vérifier qu'il ne reste aucun `make` dans les workflows**

```bash
grep -rn "make " .github/workflows/ ; echo "exit=$?"
```

Attendu : aucune ligne, `exit=1` (grep ne trouve rien). Toute occurrence restante est une régression.

- [ ] **Step 5: Vérifier que le YAML reste valide**

```bash
python3 -c "import yaml,sys; [yaml.safe_load(open(f)) for f in ['.github/workflows/ci.yml','.github/workflows/release.yml']]; print('YAML OK')"
```

Attendu : `YAML OK`. Un YAML cassé n'échoue pas au commit, il échoue au push — trop tard.

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/ci.yml .github/workflows/release.yml
git commit -m "ci: both workflows install go-task and run the one check task

The three run: make steps become a single run: task check in each workflow.
They were already running the same sequence twice over, and folding it into the
named task removes the way the two could drift apart unnoticed.

setup-task is pinned to the v3 line rather than left to float — the action
otherwise installs whatever is newest, and a task v4 would break a CI whose code
has not moved — and gets repo-token so its release lookup does not hit the
unauthenticated API rate limit.

install-script and the release job are untouched: neither went through make."
```

---

### Task 3: La documentation lue par un humain

**Files:**
- Modify: `README.md` (section « From a checkout », ≈ l. 42-52)
- Modify: `CLAUDE.md` (bloc Commands l. 13-21 ; section « Stale artifacts »)
- Modify: `docs/superpowers/handoffs/HANDOFF.md` (l. 22, 27, 164, 167)

**Interfaces:**
- Consumes: les noms de tâches produits par la Task 1.
- Produces: rien.

Cette tâche est séparée de la Task 4 parce qu'elle porte de vraies décisions de rédaction — quelle commande d'installation montrer, et où — là où la Task 4 est un renommage mécanique. Un relecteur peut légitimement rejeter l'une en acceptant l'autre.

- [ ] **Step 1: Réécrire le bloc build du `README.md`**

Remplacer :

```markdown
From a checkout:

```bash
make build
```

Every path stamps the version into the binary, so `den version` names the code it runs — the
release tag (`v1.0.0`) via Homebrew, `go install` and releases (including `install.sh`), and where you stand relative to
it (`v1.0.0-3-gabc1234-dirty`) via `make build`. A plain `go build` in a checkout is the one
build that names nothing: it answers `dev`, which is the documented tell that the build skipped
`make`.
```

par :

```markdown
From a checkout — the build runs through [go-task](https://taskfile.dev), so it comes first:

```bash
brew install go-task/tap/go-task   # or, with a Go toolchain already there:
go install github.com/go-task/task/v3/cmd/task@latest

task build
```

Every path stamps the version into the binary, so `den version` names the code it runs — the
release tag (`v1.0.0`) via Homebrew, `go install …/cmd/den@v1.0.0` and releases (including
`install.sh`), and where you stand relative to it (`v1.0.0-3-gabc1234-dirty`) via `task build`.
Building from a checkout without the runner — a plain `go build`, or a `go install ./cmd/den` —
is what names nothing: it answers `dev`, the documented tell that the build skipped `task`.
```

Les deux commandes d'installation sont montrées et pas une seule : `brew` ne couvre pas Linux ni WSL, et un repo qui vient de shipper un installeur `curl | sh` pour cette raison exacte ne peut pas supposer Homebrew chez le contributeur.

**Deux amendements par rapport au README actuel, dus à la Task 5 et non au renommage.** Le paragraphe d'origine disait `go install` à plat ; après la Task 5 la distinction devient réelle et doit être écrite : `go install …/cmd/den@v1.0.0` télécharge un module et garde son tag, tandis qu'un `go install ./cmd/den` depuis un checkout répond `dev` comme n'importe quel build local. Et il disait « **the one** build that names nothing », ce qui n'est plus vrai puisqu'ils sont maintenant deux — d'où « Building from a checkout without the runner », qui nomme la propriété (l'origine du build) plutôt que d'énumérer.

- [ ] **Step 2: Réécrire le bloc Commands de `CLAUDE.md`**

Remplacer :

```markdown
make test        # go test -count=1 ./...   (-count=1 defeats the cache — plain `go test` can pass stale)
make typecheck   # go build ./...
make lint        # go vet ./... && test -z "$(gofmt -l .)"   — gofmt is enforced, not advisory
make build       # go build -ldflags "-X .../cli.Version=$(git describe --tags --always --dirty)" -o den ./cmd/den
                 # the ONLY documented way to build: a plain `go build` answers `den dev`
```

par :

```markdown
task check       # lint » typecheck » test, fail-fast — what CI runs, and what to run before a commit
task test        # go test -count=1 ./...   (-count=1 defeats the cache — plain `go test` can pass stale)
task typecheck   # go build ./...
task lint        # go vet ./... then test -z "$(gofmt -l .)"   — gofmt is enforced, not advisory
task build       # go build -ldflags "-X .../cli.Version=$(git describe --tags --always --dirty)" -o den ./cmd/den
                 # the ONLY documented way to build: a plain `go build` answers `den dev`
```

`task check` passe en tête : c'est la commande à lancer par défaut, les quatre autres sont ce qu'on lance quand on sait déjà laquelle on veut.

- [ ] **Step 3: Ajouter la note d'artefact périmé dans `CLAUDE.md`**

Dans la section `## Stale artifacts — don't trust these`, ajouter en fin de liste :

```markdown
- Il n'y a plus de `Makefile` : le runner est `Taskfile.yml` depuis le 2026-08-04. Les plans
  datés et les handoffs sous `docs/superpowers/` disent encore `make lint && make test` — c'est
  correct **à leur date** et ils ne sont pas réécrits. Traduire en `task check` en les lisant.
```

Cette section existe précisément pour ce cas : un agent qui lit un plan de la semaine dernière et tape `make lint` obtient un `No such file or directory` sans savoir pourquoi.

- [ ] **Step 4: Corriger `docs/superpowers/handoffs/HANDOFF.md`**

`HANDOFF.md` **n'est pas** un document historique : `CLAUDE.md` dit qu'il a été réécrit le 2026-08-03 et qu'il est à jour au tag `v1.0.0`. Il est donc vivant, et ses trois mentions de `make` sont fausses après la Task 1. Ce sont les **handoffs datés à côté de lui** (`2026-07-*`, `2026-08-*`) qui ne se touchent pas.

Ligne 22, remplacer :

```markdown
- **699 fonctions de test, 12 paquets testés sur 13, vert** (`make test`, qui passe `-count=1` : un `go test` nu
```

par :

```markdown
- **699 fonctions de test, 12 paquets testés sur 13, vert** (`task test`, qui passe `-count=1` : un `go test` nu
```

Ligne 27, remplacer :

```markdown
- **v1.0.0 est taguée** (2026-08-03, tag annoté sur `e964606`). `make build` stampe
```

par :

```markdown
- **v1.0.0 est taguée** (2026-08-03, tag annoté sur `e964606`). `task build` stampe
```

Ligne 164, remplacer :

```markdown
**#10 est FAIT** (PR #34). `make build` stampe `git describe --tags --always --dirty` dans
```

par :

```markdown
**#10 est FAIT** (PR #34). `task build` stampe `git describe --tags --always --dirty` dans
```

Ligne 167 — celle-ci n'est **pas** un renommage. Elle invoque un mode de panne propre à make. Remplacer :

```markdown
`dev` : un `Version` vide, ce que produit un `$(...)` non échappé dans une recette make, n'est pas
`dev` non plus. C'est l'**accord** de `./den version` et de `git describe` qui vaut preuve.
```

par :

```markdown
`dev` : un `Version` vide, ce que produit un `{{.VERSION}}` mal orthographié dans le Taskfile —
Task rend une variable inconnue comme chaîne vide, sans erreur et en sortie 0 — n'est pas `dev`
non plus. C'est l'**accord** de `./den version` et de `git describe` qui vaut preuve.
```

Le raisonnement du handoff survit intact : le piège n'a pas disparu avec le Makefile, il a changé d'orthographe. C'est exactement pourquoi le contrôle qui tranche reste l'accord avec `git describe` et non « la sortie n'est pas `dev` ».

- [ ] **Step 5: Vérifier qu'il ne reste aucun `make` dans la doc vivante**

```bash
grep -rn "make \(build\|test\|lint\|typecheck\)\|Makefile" \
  README.md CLAUDE.md docs/superpowers/handoffs/HANDOFF.md ; echo "exit=$?"
```

Attendu : aucune ligne, `exit=1`. Une occurrence restante dans ces trois fichiers est une régression ; celles sous `docs/superpowers/plans/` et dans les handoffs **datés** sont attendues et ne doivent pas être touchées.

- [ ] **Step 6: Commit**

```bash
git add README.md CLAUDE.md docs/superpowers/handoffs/HANDOFF.md
git commit -m "docs: the entry points name task, and say how to get it

README gains the install line before the build block — both forms, because brew
covers neither Linux nor WSL and a repo that just shipped a curl|sh installer
for that exact reason cannot assume Homebrew on a contributor's machine.

CLAUDE.md leads with task check rather than the four gates: it is the command to
run by default, the others are for when you already know which one you want.

The stale-artifacts section gains the Makefile's obituary. Dated plans and
handoffs still say make lint && make test, which is correct for their date and
is not rewritten — but an agent reading one last week's plan and typing make
lint gets a No such file with no explanation, and that section exists for
precisely that.

HANDOFF.md is corrected rather than left alone: it is the live one, rewritten
last August and current at v1.0.0, unlike the dated handoffs beside it. Its
line on an empty Version needed more than a rename — it named an unescaped
\$(...) in a make recipe, and the heir to that trap is a misspelled
{{.VERSION}}, which Task renders as the empty string with no error and exit 0.
The handoff's argument survives untouched: what settles a release is still
./den version AGREEING with git describe, not merely differing from dev."
```

---

### Task 4: Les commentaires qui nomment encore le Makefile

**Files:**
- Modify: `.goreleaser.yaml:1-7`
- Modify: `internal/cli/version.go:5-14`
- Modify: `internal/cli/version_test.go:5-10`, `:12-16`, `:33-38`

**Interfaces:**
- Consumes: rien.
- Produces: rien.

**Vérifié au préalable :** `version_test.go` ne fait qu'*évoquer* le Makefile en commentaire. Aucune assertion, aucun golden, aucune string de test ne contient `make`. Cette tâche ne touche donc pas le comportement de la suite — ce qui compte, puisque les goldens de ce repo n'ont pas de flag `-update` et se corrigent à la main.

- [ ] **Step 1: `.goreleaser.yaml` — l'en-tête**

Remplacer les lignes 1 à 5 :

```yaml
# goreleaser is the release twin of `make build`: both exist to keep one promise,
# a shipped binary names the code it runs. The ldflags below MUST target the same
# symbol as the Makefile (github.com/PillowPillow/den/internal/cli.Version) and
# use {{ .Tag }} — not {{ .Version }} — because `git describe` (the Makefile
# path) answers `v1.0.0` with the leading v, and two install paths answering
```

par :

```yaml
# goreleaser is the release twin of `task build`: both exist to keep one promise,
# a shipped binary names the code it runs. The ldflags below MUST target the same
# symbol as the Taskfile (github.com/PillowPillow/den/internal/cli.Version) and
# use {{ .Tag }} — not {{ .Version }} — because `git describe` (the Taskfile
# path) answers `v1.0.0` with the leading v, and two install paths answering
```

Les deux lignes suivantes (`# in a bug report.` et ce qui précède) sont inchangées.

- [ ] **Step 2: `internal/cli/version.go` — le commentaire de `resolveVersion`**

Remplacer :

```go
// The ldflags stamp (Makefile, goreleaser) wins whenever it ran: `git describe`
// distinguishes a dirty checkout from a tag, which module build info never
// does. Build info is the rescue for `go install …/cmd/den@vX`, the one
// install path that bypasses the Makefile yet still carries the tag — without
```

par :

```go
// The ldflags stamp (Taskfile, goreleaser) wins whenever it ran: `git describe`
// distinguishes a dirty checkout from a tag, which module build info never
// does. Build info is the rescue for `go install …/cmd/den@vX`, the one
// install path that bypasses `task build` yet still carries the tag — without
```

- [ ] **Step 3: `internal/cli/version_test.go` — l'en-tête du fichier**

Remplacer :

```go
// The Makefile is "the ONLY documented way to build" precisely because a plain
// `go build` leaves Version at "dev". But `go install …/cmd/den@v1.0.0` — the
// reflex of every Go developer — bypasses the Makefile AND carries the tag in
// the module build info. resolveVersion is the arbitration that rescues that
// path: ldflags first (the Makefile and goreleaser both stamp it), build info
// second, "dev" only when neither knows better.
```

par :

```go
// `task build` is "the ONLY documented way to build" precisely because a plain
// `go build` leaves Version at "dev". But `go install …/cmd/den@v1.0.0` — the
// reflex of every Go developer — bypasses the Taskfile AND carries the tag in
// the module build info. resolveVersion is the arbitration that rescues that
// path: ldflags first (the Taskfile and goreleaser both stamp it), build info
// second, "dev" only when neither knows better.
```

- [ ] **Step 4: `internal/cli/version_test.go` — les deux commentaires de cas**

Dans `TestResolveVersionPrefersLdflagsStamp`, remplacer :

```go
	// When the Makefile or goreleaser stamped a version, build info must not
```

par :

```go
	// When the Taskfile or goreleaser stamped a version, build info must not
```

Dans `TestResolveVersionKeepsDevWhenBuildInfoIsDevel`, remplacer :

```go
	// tell that the binary skipped `make build`.
```

par :

```go
	// tell that the binary skipped `task build`.
```

- [ ] **Step 5: Vérifier que la suite est toujours verte**

Un commentaire ne peut pas casser un test — sauf s'il a été édité de travers et a mangé une ligne de code. C'est ce que ce step attrape.

```bash
task check && echo "CHECK GREEN"
```

Attendu : `CHECK GREEN`. En particulier `gofmt` doit rester silencieux : une édition de commentaire qui déborde sur l'indentation le réveille.

- [ ] **Step 6: Vérifier qu'il ne reste aucun `make` hors historique**

```bash
grep -rn "Makefile\|make build\|make test\|make lint\|make typecheck" \
  --include="*.go" --include="*.yaml" --include="*.yml" --include="*.md" . \
  | grep -v "docs/superpowers/plans/" \
  | grep -v "docs/superpowers/handoffs/2026-" \
  | grep -v "docs/superpowers/specs/2026-08-04-taskfile" \
  | grep -v "^./.claude/"
echo "exit=$?"
```

Attendu : aucune ligne, `exit=1`. Les exclusions sont les documents historiques (jamais réécrits), le spec et le plan de ce chantier (qui parlent du Makefile au passé, légitimement), et les worktrees. `HANDOFF.md` n'est pas exclu et ne doit pas ressortir : il a été corrigé à la Task 3.

- [ ] **Step 7: Commit**

```bash
git add .goreleaser.yaml internal/cli/version.go internal/cli/version_test.go
git commit -m "docs: the comments naming the Makefile name the Taskfile

Comments only — no assertion, golden, or test string in version_test.go ever
contained the word, which is why the suite is untouched by this. It matters
because this repo's goldens carry no -update flag and are fixed by hand.

goreleaser's header still has to point at whatever the local build is the twin
of, and resolveVersion's rescue is still for the install path that bypasses it;
only the runner's name moved."
```

---

---

### Task 5: Le « tell » `dev` cassé par Go 1.24+

**Ajoutée le 2026-08-04, après la Task 1.** Ne figurait pas au plan d'origine.

**Files:**
- Modify: `internal/cli/version.go` (`resolveVersion`, `displayVersion`)
- Modify: `internal/cli/version_test.go` (les 4 tests existants + 3 nouveaux)

**Interfaces:**
- Consumes: rien.
- Produces: `resolveVersion(ldflags, buildinfo string, fromLocalVCS bool) string` — **la signature change**, elle gagne un troisième paramètre. La Task 4 édite les commentaires de ces deux fichiers et passe donc après celle-ci.

**Ordre d'exécution révisé :** 1 ✅ → **2** (CI) → **5** (celle-ci) → **3** (doc) → **4** (commentaires). La Task 5 précède les Tasks 3 et 4 parce que celles-ci réécrivent les phrases qui affirment la propriété que celle-ci répare. Leur texte, lui, ne change pas : le correctif rend ces phrases vraies à nouveau plutôt que de les amender.

#### Le problème

`README.md`, `CLAUDE.md`, `internal/cli/version.go` et `internal/cli/version_test.go` affirment tous qu'un `go build` nu répond `den dev`, « the documented tell that the build skipped the runner ». **C'est faux depuis Go 1.24**, et ça l'était déjà avant cette migration — reproduit sur le checkout `main` intact (`a28f04a`), toolchain Go 1.26.1 :

```
$ go build -o /tmp/den-plain ./cmd/den && /tmp/den-plain version
den v1.1.1-0.20260804111234-a28f04a21c08+dirty
```

Go renseigne désormais `debug.BuildInfo.Main.Version` avec une pseudo-version dérivée du VCS. `resolveVersion` voit un `buildinfo` qui n'est ni `""` ni `"(devel)"`, et le retourne. La suite ne l'attrape pas : `TestResolveVersionKeepsDevWhenBuildInfoIsDevel` teste `resolveVersion("dev", "(devel)")`, or Go ne dit plus `(devel)` quand il y a un `.git`.

#### Le discriminant, établi par mesure

Trois chemins probés sur cette machine (Go 1.26.1) le 2026-08-04 :

| Chemin de build | `Main.Version` | réglages `vcs.*` |
|---|---|---|
| `go build` dans un checkout git | `v0.0.0-20260804112638-05a5bd888ac6` | **présents** (`vcs.revision`, `vcs.time`, `vcs.modified`) |
| `go build` sur la même source sans `.git` | `(devel)` | absents |
| `go install …/cmd/den@v1.0.1` (proxy) | `v1.0.1` | **absents** |

La présence de `vcs.revision` sépare donc exactement « construit depuis un checkout local » — donc en contournant le runner — de « construit depuis un module téléchargé », qui est le seul cas que le fallback existe pour secourir. C'est un signal structurel, pas un motif de chaîne : inutile de reconnaître la forme d'une pseudo-version, forme qui a d'ailleurs changé entre versions de Go.

- [ ] **Step 1: Écrire les tests qui échouent**

Ajouter à `internal/cli/version_test.go` :

```go
// THE regression: Go 1.24+ stamps Main.Version from VCS, so a plain `go build`
// in a checkout no longer answers "(devel)" — it answers a pseudo-version, which
// the old two-argument arbitration happily returned. den then named a version
// nobody can check out. Probed on Go 1.26.1, 2026-08-04.
func TestResolveVersionKeepsDevForALocalVCSBuild(t *testing.T) {
	got := resolveVersion("dev", "v1.1.1-0.20260804111234-a28f04a21c08+dirty", true)
	if got != "dev" {
		t.Fatalf("a plain `go build` must stay the documented dev tell, got %q", got)
	}
}

// A local checkout that happens to sit on a tag is still a build that skipped
// the runner. The rule keys on WHERE the build came from, not on whether the
// string it carries looks respectable.
func TestResolveVersionKeepsDevForALocalVCSBuildEvenOnACleanTag(t *testing.T) {
	got := resolveVersion("dev", "v1.0.1", true)
	if got != "dev" {
		t.Fatalf("a local build must not borrow a tag it did not stamp, got %q", got)
	}
}

// The case that must NOT regress: `task build` runs inside a checkout, so
// fromLocalVCS is true there too. The ldflags stamp still has to win — reading
// the new flag before the stamp would break the one documented way to build.
func TestResolveVersionPrefersLdflagsEvenFromALocalVCSBuild(t *testing.T) {
	got := resolveVersion("v1.0.1-5-g364136e-dirty", "v1.1.1-0.20260804111234-a28f04a21c08+dirty", true)
	if got != "v1.0.1-5-g364136e-dirty" {
		t.Fatalf("the ldflags stamp lost to the VCS pseudo-version: %q", got)
	}
}
```

Et mettre à jour les quatre tests existants pour la nouvelle signature. Ils décrivent tous des builds qui ne viennent pas d'un checkout local, donc `false` :

- `TestResolveVersionPrefersLdflagsStamp` : `resolveVersion("v1.0.0-3-gabc1234-dirty", "v1.0.0", false)`
- `TestResolveVersionFallsBackToBuildInfoOnGoInstall` : `resolveVersion("dev", "v1.0.0", false)`
- `TestResolveVersionKeepsDevWhenBuildInfoIsDevel` : `resolveVersion("dev", "(devel)", false)`
- `TestResolveVersionKeepsDevWhenBuildInfoIsEmpty` : `resolveVersion("dev", "", false)`

- [ ] **Step 2: Lancer les tests, vérifier qu'ils échouent**

```bash
go test ./internal/cli/ -run TestResolveVersion -count=1
```

Attendu : échec de compilation d'abord (`resolveVersion` prend 2 arguments, pas 3). C'est l'échec attendu à ce stade — la signature n'existe pas encore.

- [ ] **Step 3: Modifier `resolveVersion`**

Remplacer la fonction et son commentaire par :

```go
// resolveVersion arbitrates between the two ways a binary can know its version.
// The ldflags stamp (Taskfile, goreleaser) wins whenever it ran: `git describe`
// distinguishes a dirty checkout from a tag, which module build info never
// does. Build info is the rescue for `go install …/cmd/den@vX`, the one
// install path that bypasses `task build` yet still carries the tag — without
// this fallback that binary answers "den dev" and a bug report against it
// names no code.
//
// fromLocalVCS is what keeps that rescue from swallowing the case it was never
// meant to cover. Since Go 1.24 the toolchain stamps Main.Version from VCS
// state, so a plain `go build` in a checkout no longer answers the "(devel)"
// placeholder — it answers a pseudo-version like
// v1.1.1-0.20260804111234-a28f04a21c08+dirty, which is not a version anybody
// can check out and which the old arbitration returned as though it were.
// Probed on Go 1.26.1 (2026-08-04): a build from a local checkout always
// carries a vcs.revision setting, a build from a downloaded module never does,
// and `go install …@v1.0.1` answers a bare "v1.0.1" with no vcs settings at
// all. Keying on WHERE the build came from is therefore exact, where matching
// the shape of a pseudo-version would only be a guess about a format Go has
// already changed once.
//
// The ldflags check stays first on purpose: `task build` also runs inside a
// checkout, so fromLocalVCS is true for the one documented way to build.
func resolveVersion(ldflags, buildinfo string, fromLocalVCS bool) string {
	if ldflags != "" && ldflags != "dev" {
		return ldflags
	}
	if !fromLocalVCS && buildinfo != "" && buildinfo != "(devel)" {
		return buildinfo
	}
	return "dev"
}
```

- [ ] **Step 4: Modifier `displayVersion`**

Remplacer la fonction et son commentaire par :

```go
// displayVersion is the impure twin: it feeds resolveVersion the process's own
// build info. It stays a thin reader — finding vcs.revision and copying
// Main.Version, nothing else — so the arbitration above remains fully testable
// without faking debug.ReadBuildInfo.
func displayVersion() string {
	buildinfo := ""
	fromLocalVCS := false
	if info, ok := debug.ReadBuildInfo(); ok {
		buildinfo = info.Main.Version
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				fromLocalVCS = true
				break
			}
		}
	}
	return resolveVersion(Version, buildinfo, fromLocalVCS)
}
```

- [ ] **Step 5: Lancer les tests, vérifier qu'ils passent**

```bash
go test ./internal/cli/ -run TestResolveVersion -count=1 -v
```

Attendu : les sept tests passent.

- [ ] **Step 6: Vérifier le comportement réel — c'est le point de la tâche**

Les tests unitaires prouvent l'arbitrage, pas ce que Go raconte réellement à `den`. Les deux mesures :

```bash
go build -o /tmp/den-plain ./cmd/den && /tmp/den-plain version && rm /tmp/den-plain
```

Attendu : `den dev`. C'était `den v1.1.1-0.…+dirty` avant le correctif — c'est toute la tâche.

```bash
task build && ./den version && git describe --tags --always --dirty
```

Attendu : les deux dernières lignes s'accordent, à `den ` près. Le stamp du runner ne doit **pas** avoir été atteint par le correctif.

- [ ] **Step 7: Lancer les gates**

```bash
task check && echo "CHECK GREEN"
```

Attendu : `CHECK GREEN`. La suite complète, pas seulement `-run TestResolveVersion`.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/version.go internal/cli/version_test.go
git commit -m "fix(version): a plain go build answers dev again under Go 1.24+

README, CLAUDE.md and version.go all promise that a bare go build answers
den dev — the documented tell that a binary skipped the runner. It has not
been true since Go 1.24 started stamping Main.Version from VCS state: a
checkout build answers a pseudo-version, resolveVersion saw something that
was neither empty nor (devel), and returned it. den named a version nobody
can check out. Reproduced on untouched main before any of this branch landed.

The suite missed it because it asserts on (devel), the placeholder Go emits
only when there is no .git to read.

resolveVersion now takes whether the build came from a local checkout, which
three probes on Go 1.26.1 show is the exact discriminator: a checkout build
always carries vcs.revision, a downloaded module never does, and
go install …@v1.0.1 answers a bare v1.0.1 with no vcs settings — that last
one being the only case the buildinfo rescue was ever for. Matching the shape
of a pseudo-version instead would be a guess about a format Go has already
changed once.

The ldflags check stays first: task build also runs inside a checkout, so the
new flag is true for the one documented way to build too."
```

## Vérification finale du chantier

À lancer une fois les quatre tâches faites, avant toute PR.

```bash
task check || { echo "FAIL: gates"; exit 1; }

# Le contrôle qui tranche n'est PAS « la sortie n'est pas dev ». Un `Version`
# vide — ce que produit un {{.VERSION}} mal orthographié, que Task rend en
# chaîne vide sans erreur — n'est pas `dev` non plus. C'est l'ACCORD avec
# git describe qui vaut preuve.
task build
expected="den $(git describe --tags --always --dirty)"
got="$(./den version)"
[ "$got" = "$expected" ] || { echo "FAIL: stamp — got '$got', expected '$expected'"; exit 1; }

go build -o /tmp/den-plain ./cmd/den
[ "$(/tmp/den-plain version)" = "den dev" ] || { echo "FAIL: the dev tell is gone"; exit 1; }
rm /tmp/den-plain

test ! -e Makefile || { echo "FAIL: Makefile survived"; exit 1; }
test ! -e .task || { echo "FAIL: a .task cache appeared"; exit 1; }

echo "MIGRATION COMPLETE"
```

Attendu : `MIGRATION COMPLETE`, sans aucune ligne `FAIL:` avant.

Les deux assertions sur `den version` comptent, et pour des raisons différentes. La première vérifie que le binaire buildé par le runner **s'accorde** avec `git describe` — pas seulement qu'il diffère de `dev`, ce qu'un `Version=` vide ferait aussi en shippant un `den ` avec rien derrière. La seconde vérifie que le « tell » documenté survit : un `go build` nu doit toujours répondre `dev`, sans quoi on perd le seul signal qui dit qu'un binaire a contourné le runner. C'est la propriété qu'une suite verte ne verrait pas, et la raison d'être de tout le dispositif ldflags.
