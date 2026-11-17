# Clés de repo portées par leur origine, `den scan` — plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** un `den.yaml` de projet déclare ses clés de repo avec une `origin` git ; `den scan` trouve les clones sur le disque et enregistre leur chemin dans une table d'état ; `den up` résout la clé par cette table.

**Architecture:** un paquet feuille `internal/origin` normalise une URL en identité `hôte/chemin`. `internal/config` accepte `repos:` (clé → `{origin, path}`) dans les deux fichiers. Un paquet `internal/repos` lit les remotes d'un `.git/config`, tient la table `state/v2/repos.yaml`, parcourt le disque et calcule le plan d'un scan. `nest.Resolve` résout une clé par le global, le projet puis la table. `internal/cli/scan.go` orchestre la commande.

**Tech Stack:** Go, cobra, gopkg.in/yaml.v3, `prompt.Prompter`.

**Spec:** `docs/superpowers/specs/2026-11-17-den-scan-repos-design.md` (§ cités ci-dessous).

## Global Constraints

- Aucun test n'appelle `t.Parallel()`, n'ouvre de socket, ni ne lance de processus autre que git. `den scan` ne lance **aucun** processus (§6.5).
- Décodage strict (`config.DecodeYAMLStrict`) pour tout fichier de den ; une clé inconnue est une erreur qui nomme le fichier.
- Toute écriture d'état passe par `config.WriteFileAtomic`.
- Identité : `hôte/chemin`, sans schéma, identifiant, mot de passe, port, suffixe `.git`, `/` final ; en minuscules (§6.1).
- La table ne stocke que l'identité et le chemin, jamais une URL brute (anti-but jeton).
- Profondeur maximale du parcours : 20. Sauts : `DEN_HOME`, `~/Library`, `node_modules`, `vendor`, dossiers à point initial, liens symboliques (§6.5.1).
- Les goldens `internal/sbxenv/testdata/repomap.*.golden` ne changent pas (§6.8). Pas de flag `-update`.
- `worktree.Path` ne change pas (§6.3).
- Messages, code et commentaires en anglais ; commentaires selon CLAUDE.md (le « pourquoi » au site de décision).
- `task check` passe à la fin de chaque tâche.

## Review Focus

1. Une origine `https://user:token@host/a/b` dans un `.git/config` : le jeton n'apparaît ni dans `state/v2/repos.yaml`, ni dans la sortie de `den scan` (Task 3, Task 10).
2. Un lien symbolique vers `/` ou vers un ancêtre sous la racine du scan : jamais suivi, le scan termine (Task 7).
3. Le `~/.den/den.yaml` actuel (36 entrées `clé: /chemin` scalaires) se charge sans modification et résout comme aujourd'hui (Task 4, Task 5).
4. Hors TTY avec `--yes` et un remplacement en attente : les ajouts sont écrits, le remplacement est listé « not written », l'ancienne entrée reste (Task 10).
5. Recreate d'une instance enregistrée avec `-w` dont la clé diffère du dossier (`go-dgdev` → `go.dgdev`) : `checkRecordedWorktrees` passe (Task 6).

---

### Task 1: `internal/origin` — identité d'une URL

**Files:**
- Create: `internal/origin/origin.go`
- Test: `internal/origin/origin_test.go`

**Interfaces:**
- Produces: `func Identity(url string) (string, error)` — `hôte/chemin` en minuscules ; erreur pour une forme non reconnue. Paquet sans dépendance interne (importé par `config` et `repos`).

- [ ] **Step 1: Écrire le test table `TestIdentity`**

```go
cases := []struct{ in, want string }{
	{"git@gitlab.com:Dig/api.git", "gitlab.com/dig/api"},
	{"ssh://git@gitlab.com:2222/dig/api.git", "gitlab.com/dig/api"},
	{"https://gitlab.com/dig/api", "gitlab.com/dig/api"},
	{"https://user:token@gitlab.com/dig/api.git/", "gitlab.com/dig/api"},
	{"git://github.com/org/repo", "github.com/org/repo"},
	{"gitlab.com:dig/front/api.git", "gitlab.com/dig/front/api"},
	{"git@host:2222/dig/api", "host/2222/dig/api"}, // scp form: after ':' is a path
}
```
et `TestIdentityRefuses` : `"/abs/path"`, `"../rel"`, `"file:///x/y"`, `""`, `"https://host"` (chemin vide), `"ftp://h/a"` → erreur.

- [ ] **Step 2: `go test ./internal/origin/ -count=1`** — FAIL (paquet absent).
- [ ] **Step 3: Implémenter `Identity`.** Schémas acceptés : `ssh`, `git`, `http`, `https` via `net/url` ; sinon forme scp `[user@]hôte:chemin` où `hôte` ne contient ni `/` ni `\`, et la chaîne ne commence ni par `/` ni par `.`. L'erreur ne recopie pas l'URL (elle peut porter un jeton) : `"not a git remote URL (expected scheme://host/path or [user@]host:path)"`.
- [ ] **Step 4: `go test ./internal/origin/ -count=1`** — PASS.
- [ ] **Step 5: Commit** `feat(origin): normalize a git remote URL into a host/path identity`

### Task 2: `internal/repos` — remotes d'un clone

**Files:**
- Create: `internal/repos/remotes.go`
- Test: `internal/repos/remotes_test.go`

**Interfaces:**
- Consumes: `origin.Identity`.
- Produces: `type Remote struct{ Name, Identity string }` ; `func Remotes(cloneDir string) ([]Remote, error)` — lit `<cloneDir>/.git/config`, renvoie un `Remote` par `[remote "<name>"] url = …` reconnu, dans l'ordre du fichier ; URL non reconnue ignorée ; erreur seulement si le fichier est illisible.

- [ ] **Step 1: Tests.** `TestRemotesReadsEveryRemote` : un `.git/config` avec `[core]`, `[remote "origin"] url = git@gitlab.com:me/api.git`, `[remote "upstream"]\n\turl = https://gitlab.com/dig/api` → `[{origin gitlab.com/me/api} {upstream gitlab.com/dig/api}]`. `TestRemotesIgnoresInsteadOfAndInclude` : `[url "git@gitlab.com:"] insteadOf = gl:`, `[include] path = x`, `[remote "origin"] url = gl:dig/api` → `[{origin gl/dig/api}]` (forme scp brute, aucune réécriture). `TestRemotesSkipsUnrecognized` : `url = /local/path` → vide. Commentaires `#`/`;`, valeurs entre guillemets, espaces autour de `=`.
- [ ] **Step 2: `go test ./internal/repos/ -count=1`** — FAIL.
- [ ] **Step 3: Implémenter** un lecteur ligne à ligne minimal (sections `[remote "x"]`, clés insensibles à la casse `url`). Pas d'appel à git (C2) ; commentaire d'une ligne au site : le fichier est réinscriptible par une VM et `git remote get-url` appliquerait `insteadOf`.
- [ ] **Step 4: PASS.**
- [ ] **Step 5: Commit** `feat(repos): read a clone's remotes from .git/config without git`

### Task 3: `internal/repos` — table d'état

**Files:**
- Create: `internal/repos/table.go`
- Test: `internal/repos/table_test.go`

**Interfaces:**
- Produces:
  - `const TableSchema = 1`
  - `type Entry struct{ Path string \`yaml:"path"\` }`
  - `type Table struct{ Schema int \`yaml:"schema"\`; Repos map[string]Entry \`yaml:"repos"\` }` — clé = identité.
  - `func TablePath(denHome string) string` → `<denHome>/state/v2/repos.yaml`
  - `func ReadTable(denHome string) (Table, error)` — fichier absent → table vide (`Repos` non nil) ; décodage strict ; schéma ≠ 1 → erreur qui nomme le fichier.
  - `func WriteTable(denHome string, t Table) error` — estampille `Schema`, `config.WriteFileAtomic`.

- [ ] **Step 1: Tests.** `TestReadTableMissingIsEmpty`, `TestTableRoundTrip`, `TestReadTableRefusesUnknownKey` (champ `url:` sous une entrée → erreur contenant le chemin du fichier), `TestReadTableRefusesOtherSchema`.
- [ ] **Step 2: FAIL.** — **Step 3: Implémenter.** — **Step 4: PASS.**
- [ ] **Step 5: Commit** `feat(repos): state/v2/repos.yaml maps an origin identity to its clone`

### Task 4: `config` — `repos:` déclare `{origin, path}` dans les deux fichiers

**Files:**
- Modify: `internal/config/schema.go:14` (`Repos`), ajout de `RepoDecl`
- Modify: `internal/config/load.go:124-126` (retrait du refus en projet), `:182-190` (validation), `absolutize` (chemins des déclarations)
- Modify: `internal/nest/resolve.go:209-230` (compile : `.Path`)
- Test: `internal/config/load_test.go`

**Interfaces:**
- Consumes: `origin.Identity`.
- Produces:
  - `type RepoDecl struct{ Origin string \`yaml:"origin"\`; Path string \`yaml:"path"\` }` avec `func (d *RepoDecl) UnmarshalYAML(n *yaml.Node) error` : scalaire → `Path` ; mapping → décodage strict des deux champs (un champ inconnu est refusé).
  - `File.Repos map[string]RepoDecl`
  - `func ValidateRepoKey(key string) error` — refuse vide, `/`, `\`, `.`, `..`, point initial.
  - Après `Load`, `RepoDecl.Path` est absolu (même `abs` que les autres chemins).

- [ ] **Step 1: Tests.**
  - `TestGlobalReposScalarStillLoads` : `repos: {js.box: /x/js.box}` global → `Repos["js.box"] == RepoDecl{Path: "/x/js.box"}`.
  - `TestGlobalRepoMayCarryOriginAndPath`.
  - `TestProjectReposNeedExactlyOneOfOriginAndPath` : `{}` et `{origin: …, path: …}` en projet → message `repos.<key>: set exactly one of `origin` and `path``.
  - `TestProjectReposRelativePathIsAnchored` : `scratch: {path: ../scratch}` → absolu sous le dossier du fichier.
  - `TestReposRefusesBadOrigin` : `origin: /local` → `repos.<key>.origin: not a git remote URL …`, nom du fichier dans l'erreur.
  - `TestReposRefusesBadKey` : clés `a/b`, `.x`, `..`.
  - `TestReposRefusesUnknownField` : `{origin: …, url: …}`.
  - Retirer/adapter le test existant qui attend `repos: is global-only` pour un projet.
- [ ] **Step 2: `go test ./internal/config/ -count=1`** — FAIL.
- [ ] **Step 3: Implémenter.** Dans `nest.Resolve`, remplacer `raw, ok := global.File.Repos[ref.Key]` par la lecture de `.Path` (comportement inchangé à ce stade ; la Task 5 réécrit cette boucle).
- [ ] **Step 4: `task check`** — PASS.
- [ ] **Step 5: Commit** `feat(config): repos: declares a key's origin or path, in project files too`

### Task 5: `nest.Resolve` — résolution d'une clé, nom = clé, liste « machine »

**Files:**
- Modify: `internal/nest/resolve.go` (`Repo`, `Resolved.MachineRepos`, boucle `n.Repos`)
- Modify: `internal/instance/repomap.go:42-51` (consomme la tranche)
- Create: `internal/nest/repos.go` (résolution des clés, erreur des clés manquantes)
- Test: `internal/nest/repos_test.go`, tests existants de `MachineRepos` adaptés

**Interfaces:**
- Consumes: `config.RepoDecl`, `origin.Identity`, `repos.ReadTable`.
- Produces:
  - `Repo` gagne `Origin string` (URL telle que déclarée) et `DeclaredIn string` (fichier qui déclare la clé, `""` pour un `path:` du nest).
  - `type MachineRepo struct{ Name, Path string }` ; `Resolved.MachineRepos []MachineRepo` — clés du global résolues (triées par clé, nommées par la clé), puis entrées de la table dont le chemin n'est pas déjà listé (triées par chemin, nommées `filepath.Base`) ; un chemin listé une fois ; un chemin absent du disque (`os.Stat` échoue) omis.
  - `type MissingRepo struct{ Key, Origin, DeclaredIn string; Optional bool }` ; `type MissingReposError struct{ Nest string; Missing []MissingRepo }` avec `Error()` :

```
nest "oauth": 2 repos have no folder on this machine:
  - front-api: git clone git@gitlab.com:dig/front/api.git   (declared in /p/den.yaml)
  - back-api: git clone git@gitlab.com:dig/back/api.git   (declared in /p/den.yaml)
already cloned? run `den scan <folder that holds your clones>`
```

Règles (§6.3), portée = fichier du nest + global :
1. Conflit : les deux côtés portent une `origin` d'identités différentes, ou un `path` différent → erreur `repo key %q: %s says %s, %s says %s — one key names one repo; rename one`.
2. Chemin : `path` du global, sinon `path` du projet, sinon `Repos[Identity(origin)]` de la table.
3. Clé déclarée nulle part : erreur actuelle (« not in repos: of … ; add it there ») en nommant les deux fichiers.
4. Sans chemin : toutes les clés non optionnelles réunies dans un seul `*MissingReposError` ; une optionnelle → avertissement actuel.
5. `Repo.Name` = la clé pour une entrée `key:` ; `filepath.Base` pour un `path:` du nest. Le contrôle d'unicité existant (`seen`) reste sur `Name`.

- [ ] **Step 1: Tests** (`internal/nest/repos_test.go`, DEN_HOME temporaire, table écrite par `repos.WriteTable`) :
  - `TestKeyResolvesThroughTheTable` : projet `api: {origin: git@gitlab.com:dig/api.git}`, table `gitlab.com/dig/api → /c/api` → `Repo{Name:"api", Path:"/c/api", Key:"api"}`.
  - `TestGlobalPathWinsOverTheTable`.
  - `TestScalarGlobalKeyResolvesAsToday` (Review Focus 3).
  - `TestConflictingOriginsRefuse`, `TestConflictingPathsRefuse`, `TestSameOriginBothSidesIsNoConflict` (`git@…` d'un côté, `https://…` de l'autre).
  - `TestMissingKeysAreListedTogether` : deux clés sans chemin → un `*MissingReposError` à deux entrées, texte contenant les deux `git clone`.
  - `TestKeyNamesTheRepo` : clé `go-dgdev` sur `/c/go.dgdev` → `Name == "go-dgdev"`.
  - `TestTwoKeysEndingInApiCoexist` : `front-api`, `back-api` sur `/a/api`, `/b/api` → accepté.
  - `TestMachineReposAddTableEntriesOnce` : une clé globale et une entrée de table au même chemin → listé une fois sous la clé ; une entrée de table à chemin inexistant → omise.
- [ ] **Step 2: `go test ./internal/nest/ -count=1`** — FAIL.
- [ ] **Step 3: Implémenter** dans `internal/nest/repos.go` ; `repoMapOf` itère `p.Resolved.MachineRepos` dans l'ordre reçu.
- [ ] **Step 4: `task check`** — PASS, goldens `repomap.*` inchangés.
- [ ] **Step 5: Commit** `feat(nest): a repo key resolves through its origin, and names the repo`

### Task 6: `instance` — remède des clés, `-w` et noms de dossier

**Files:**
- Modify: `internal/instance/prepare.go:258-265` (`repoFix`), ajout de `checkWorktreeFolders`
- Test: `internal/instance/prepare_test.go`, `internal/instance/repomap_test.go`

**Interfaces:**
- Consumes: `nest.Repo.DeclaredIn`, `nest.MachineRepo`.
- Produces: `func checkWorktreeFolders(res *nest.Resolved, repos []nest.Repo) error` — appelé par `Prepare` quand `-w` et `res.Layout == "central"`, avant tout effet de bord.

- [ ] **Step 1: Tests.**
  - `TestRepoFixNamesTheDeclaringFile` : clé déclarée en projet, dossier absent → message `fix `repos.api` in <projet>/den.yaml`.
  - `TestWorktreeRefusesTwoReposWithTheSameFolderName` : clés `front-api`→`/a/api`, `back-api`→`/b/api`, `-w feat` → erreur `repos front-api and back-api share the folder name "api", and -w puts both at <root>/feat/api — set worktrees.layout: per-repo, or drop one`. Sans `-w` : accepté.
  - `TestRecreateKeepsTheWorktreeOfARenamedKey` (Review Focus 5) : record avec worktree `wt/feat/go.dgdev`, clé `go-dgdev` → `checkRecordedWorktrees` ne refuse pas.
  - `TestRepoMapListsTableEntries` : `repoMapOf` place une entrée de table dans `Machine`, nommée par son dossier.
- [ ] **Step 2: FAIL.** — **Step 3: Implémenter.** — **Step 4: `task check` PASS.**
- [ ] **Step 5: Commit** `feat(instance): name the file that declares a missing repo key; -w refuses a shared folder name`

### Task 7: `internal/repos` — parcours du disque

**Files:**
- Create: `internal/repos/walk.go`
- Test: `internal/repos/walk_test.go`

**Interfaces:**
- Consumes: `Remotes`.
- Produces:
  - `type Clone struct{ Path string; Remotes []Remote }`
  - `type WalkOptions struct{ MaxDepth int; Skip []string; Progress func(dirs, clones int) }` — `Skip` : chemins absolus sautés avec leur contenu ; `Progress` appelé toutes les 1000 dossiers puis à la fin, peut être nil.
  - `func Walk(root string, o WalkOptions) ([]Clone, error)` — clones triés par chemin ; erreur seulement si `root` est illisible.
  - `func EnclosingClone(dir string) (string, bool)` — le premier ancêtre de `dir` (strict) qui contient un dossier `.git`.

Règles (§6.5.1) : un dossier contenant un **dossier** `.git` est un clone, on n'y descend pas ; `root` lui-même peut être un clone ; un fichier `.git` n'en fait pas un ; liens symboliques jamais suivis (`os.Lstat` / `fs.DirEntry.Type()`) ; sauts par nom : `node_modules`, `vendor`, nom à point initial ; dossier illisible ignoré ; profondeur relative à `root`, `root` = 0.

- [ ] **Step 1: Tests.** `TestWalkFindsClonesAndStopsInside` (un faux clone dans un clone n'est pas listé), `TestWalkSkipsWorktreeFiles`, `TestWalkSkipsNamedDirs`, `TestWalkSkipsListedPaths`, `TestWalkDoesNotFollowSymlinks` (lien vers `/` et vers `root`, Review Focus 2), `TestWalkRespectsMaxDepth`, `TestWalkRootIsAClone`, `TestEnclosingClone`.
- [ ] **Step 2: FAIL.** — **Step 3: Implémenter** avec `os.ReadDir` récursif. — **Step 4: PASS.**
- [ ] **Step 5: Commit** `feat(repos): walk a folder for clones without following links`

### Task 8: `internal/repos` — plan d'un scan

**Files:**
- Create: `internal/repos/plan.go`
- Test: `internal/repos/plan_test.go`

**Interfaces:**
- Consumes: `Clone`, `Table`, `Remotes`.
- Produces:
  - `type Change struct{ Identity, Path, Old string }` — `Old` vide pour un ajout.
  - `type Duplicate struct{ Identity, Chosen string; Others []string }`
  - `type Plan struct{ Adds, Replaces []Change; Duplicates []Duplicate; Stale []string }` — tranches triées par identité.
  - `func Valid(path, identity string) bool` — le dossier existe et un de ses remotes porte l'identité.
  - `func BuildPlan(clones []Clone, t Table, valid func(path, identity string) bool) Plan`
  - `func (t *Table) Apply(changes ...Change)`

Règles (§6.5.3-4) : pour chaque identité trouvée, candidats = clones qui la portent ; gagnant = un clone dont le remote `origin` la porte, sinon le premier par chemin. Plus d'un candidat → `Duplicate` (aussi quand l'entrée existante est valide ; `Chosen` = alors le chemin de la table). Entrée absente → `Adds` ; présente et `valid` → rien ; présente et invalide → `Replaces` (`Old` = ancien chemin). Entrée de table invalide sans candidat → `Stale`.

- [ ] **Step 1: Tests.** `TestPlanAddsNewIdentities`, `TestPlanKeepsAValidEntry`, `TestPlanReplacesAnInvalidEntry`, `TestPlanReportsStale`, `TestPlanPrefersTheOriginRemote` (fork `origin=me/api`, `upstream=dig/api` à `/a`, clone `origin=dig/api` à `/z` → `dig/api` va à `/z`), `TestPlanBreaksTiesByPath`, `TestPlanReportsADuplicateOfAValidEntry`, `TestApply`.
- [ ] **Step 2: FAIL.** — **Step 3: Implémenter.** — **Step 4: PASS.**
- [ ] **Step 5: Commit** `feat(repos): plan a scan — adds, replacements, duplicates, stale entries`

### Task 9: `nest` — origines déclarées, pour le rapport du scan

**Files:**
- Create: `internal/nest/declared.go`
- Test: `internal/nest/declared_test.go`

**Interfaces:**
- Produces:
  - `type Declared struct{ Key, Origin, Identity, File string }`
  - `func DeclaredOrigins(denHome, cwd string) ([]Declared, error)` — clés à `origin` du projet trouvé depuis `cwd`, des fichiers de l'index `state/v2/nests.yaml`, et du global ; un fichier illisible de l'index est sauté (lecture indulgente, jamais de `Claim`) ; une erreur du global ou du projet du cwd est renvoyée. Dédoublonné par (fichier, clé), trié par fichier puis clé.

- [ ] **Step 1: Tests.** `TestDeclaredOriginsReadsProjectIndexAndGlobal`, `TestDeclaredOriginsSkipsAnUnreadableIndexedFile`, `TestDeclaredOriginsDoesNotClaim` (`nests.yaml` inchangé après l'appel).
- [ ] **Step 2: FAIL.** — **Step 3: Implémenter** avec `readIndex`, `config.FindProject`, `config.Load`. — **Step 4: PASS.**
- [ ] **Step 5: Commit** `feat(nest): list the origin keys den can see, for den scan's report`

### Task 10: `den scan`

**Files:**
- Create: `internal/cli/scan.go`
- Modify: `internal/cli/root.go:92` (`root.AddCommand(newScanCmd(deps, &denHome))`)
- Modify: `internal/cli/up_test.go` (champ `tty bool` du fixture, lu par `IsTTY`)
- Test: `internal/cli/scan_test.go`

**Interfaces:**
- Consumes: `repos.Walk`, `repos.EnclosingClone`, `repos.ReadTable`, `repos.BuildPlan`, `repos.Valid`, `repos.WriteTable`, `Table.Apply`, `nest.DeclaredOrigins`, `deps.Prompt.Confirm`, `deps.IsTTY`.
- Produces: `den scan [folder] [--yes]` (`cobra.MaximumNArgs(1)`).

Déroulé (§6.5) :
1. Racine = argument (via `config.ExpandPath`, absolu) sinon cwd. `Skip` = den home et `<home>/Library` (`os.UserHomeDir`). `MaxDepth` = 20. `Progress` écrit `scanned N folders, M clones` sur `Err`.
2. `EnclosingClone(root)` trouvé → `Err` : `<root> is inside the clone <c>; to find its siblings, run `den scan <parent of c>``.
3. Plan, puis affichage sur `Out`, dans cet ordre : remplacements (`replace <identity>: <old> → <new>`), doublons (`duplicate <identity>: using <chosen>, not <others>`), ajouts dont l'identité est déclarée (`add <identity> → <path>`), puis `and N other clones` ; entrées périmées (`stale <identity>: <path> no longer holds it`). Seules des identités et des chemins sont imprimés (Review Focus 1).
4. Rien à écrire → pas de question.
5. Écriture :
   - TTY sans `--yes` : une confirmation `write N additions and M replacements to <table path>?` ; non → rien n'est écrit, sortie 0, `nothing written`.
   - TTY avec `--yes` : ajouts écrits sans question ; s'il y a des remplacements, confirmation `replace M entries?`.
   - Hors TTY avec `--yes` : ajouts écrits ; chaque remplacement listé `not written (needs a terminal): …`.
   - Hors TTY sans `--yes` : erreur `den scan found N changes; run it in a terminal, or with --yes to write the additions`.
6. Rapport : chaque `Declared` dont l'identité n'a pas de chemin après écriture (table + `path` du global) → `still missing: <key> (<file>): git clone <origin>`.

- [ ] **Step 1: Tests** (fixture `upFixture`, clones créés avec un `.git/config` écrit à la main, sans git) :
  - `TestScanWritesAdditionsWithYes` (hors TTY) → table contient l'identité.
  - `TestScanAsksOnATerminal` (`tty`, `ConfirmAnswers: []bool{true}`) ; `TestScanWritesNothingWhenRefused`.
  - `TestScanRefusesWithoutTerminalOrYes`.
  - `TestScanNeverReplacesWithoutATerminal` (Review Focus 4).
  - `TestScanAsksBeforeReplacingEvenWithYes`.
  - `TestScanNeverPrintsOrStoresAToken` (Review Focus 1) : remote `https://u:s3cret@h/a/b` → `s3cret` absent de `Out`, `Err` et du fichier de table.
  - `TestScanReportsMissingDeclaredKeys` ; `TestScanSaysWhenRunInsideAClone` ; `TestScanSkipsTheDenHome`.
- [ ] **Step 2: `go test ./internal/cli/ -run Scan -count=1`** — FAIL.
- [ ] **Step 3: Implémenter `newScanCmd(deps Deps, denHome *string) *cobra.Command`.**
- [ ] **Step 4: `task check`** — PASS.
- [ ] **Step 5: Commit** `feat(cli): den scan records where each origin is cloned`

### Task 11: Documentation

**Files:**
- Modify: `docs/den-yaml.md` (§ « Repos », lignes 20, 30-45, 78, 102-105, 135-175)
- Modify: `CLAUDE.md` (« Seven commands » → huit avec `scan` ; `repos` n'est plus global-only ; paragraphe Config ; la table `state/v2/repos.yaml` dans « Layout on disk »)
- Modify: `README.md` (section qui présente `repos:` / les commandes, si elle existe)

- [ ] **Step 1:** Décrire : `repos:` clé → `{origin}` / `{path}` en projet, `{path, origin}` ou scalaire en global ; ordre de résolution ; `den scan [folder] [--yes]` et ses règles de confirmation ; alias SSH non supportés (remède : `path` dans le global) ; la clé nomme le repo dans la VM.
- [ ] **Step 2:** `task check` — PASS ; `task build && ./den scan --help` affiche l'usage.
- [ ] **Step 3: Commit** `docs: repo keys carry their origin; den scan`
