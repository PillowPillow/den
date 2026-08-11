# Instances de nest (`--as`) et nests génériques (`select: prompt`) — plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** donner au second composant du nom de sandbox le rôle d'**instance** (`--as`), puis
bâtir dessus le nest générique qui choisit ses repos au spawn (`select: prompt`).

**Architecture:** `--as` remplit le composant que `-w` remplissait, et rien d'autre ne bouge —
`sbx.SandboxName` / `sbx.SplitName` gardent leur forme, et le répertoire de worktree continue d'être
nommé d'après la branche aplatie (`worktree.Name.Dir`). `select: prompt` est un point d'entrée de
plus vers la checklist qui existe déjà (`internal/spawn/interactive.go`), et la checklist reste
traduite en `--without` avant `nest.Resolve` : une seule règle de sélection, jamais deux.

**Tech Stack:** Go, `cobra`, `yaml.v3`. Aucune dépendance nouvelle — c'est une propriété annoncée
(binaire statique). Runner de tâches : `task` (`Taskfile.yml`).

**Spec:** `docs/superpowers/specs/2026-08-11-nest-instances-design.md`. Ses « décisions
verrouillées » sont numérotées 1 à 11 et le plan y renvoie par numéro.

## Global Constraints

- `task check` (lint » typecheck » test, fail-fast) doit passer avant chaque commit. `gofmt` est
  **enforced**, pas advisory.
- `go test -count=1 ./...` — le `-count=1` défait le cache ; un `go test` nu peut passer sur du
  périmé.
- Aucun test n'appelle `t.Parallel()`, n'ouvre de socket ni ne lance de process.
- Code, commentaires et messages utilisateur en **anglais**. Ce plan et la spec sont en français.
- Style dominant : un long commentaire « why » au site de décision — ce qui a été écarté et quelle
  régression le choix évite. Du code terse détonne visiblement ; épouser la densité alentour.
- Les erreurs nomment le fichier à corriger et le remède.
- Les goldens (`internal/*/testdata/*.golden`) se comparent à la main : **il n'y a pas de flag
  `-update`**, on les édite manuellement.
- Exclure `.claude/worktrees/feat+spawn-interactive/` des greps : c'est une copie fantôme de
  l'arbre, chaque recherche y rend des doublons.
- Ne JAMAIS toucher à `sbx.SandboxName` / `sbx.SplitName` : leurs signatures sont un invariant de
  cette spec (décision 1).

---

## Structure des fichiers

| Fichier | Rôle | Tâche |
|---|---|---|
| `internal/cli/spawn.go` | drapeau `--as` → `spawn.Options.Instance` | 1 |
| `internal/spawn/spawn.go` | `Options.Instance`, calcul du nom, remontée du contrôle de vivacité, entrée `select: prompt` | 1, 5, 6 |
| `internal/spawn/instance_test.go` | **créé** — table de nommage `-w` / `--as` / les deux / aucun | 1 |
| `internal/nest/nest.go` | champ `Select`, constantes, validation dans `LoadNest` | 3 |
| `internal/nest/nest_test.go` | décodage strict et validation de `select:` | 3 |
| `internal/spawn/interactive.go` | départ vide, annotation des clés non mappées, refus sans TTY | 4, 5 |
| `internal/spawn/interactive_test.go` | tests du rendu et de l'équivalence | 4, 5 |
| `internal/sbx/ls.go` | `Sandbox.Worktree()` → `Sandbox.Instance()` | 2 |
| `internal/cli/ls.go` | colonne INSTANCE, fin du repli sur le composant 2 | 2 |
| `README.md` | `--as`, `select:`, et la table des drapeaux de `den spawn` | 7 |

L'ordre est celui des dépendances : le nommage (1-2) est la primitive, `select:` (3-5) se pose
dessus, la remontée du contrôle de vivacité (6) est ce que `select: prompt` exige, et la doc (7)
ferme.

---

### Task 1: `--as` — le drapeau, l'option, le nom de sandbox

**Files:**
- Modify: `internal/spawn/spawn.go` (struct `Options` ~ligne 129 ; bloc de nommage 343-399)
- Modify: `internal/cli/spawn.go` (déclaration des drapeaux, fin de `newSpawnCmd`)
- Create: `internal/spawn/instance_test.go`

**Interfaces:**
- Consumes: `config.FlattenSandboxComponent(kind, name string) (string, error)`,
  `sbx.SandboxName(nest, instance string) (string, error)`,
  `worktree.Name{Dir, Branch string}` — tous existants, inchangés.
- Produces: `spawn.Options.Instance string` — lu par les tâches 5 et 6 ; le drapeau CLI `--as`.

- [ ] **Step 1: Write the failing test**

Créer `internal/spawn/instance_test.go`. `denTestOptional` et `fakeDeps` existent déjà dans
`internal/spawn/interactive_test.go` — même paquet, on les réutilise, on ne les redéclare pas.

```go
package spawn

import (
	"context"
	"strings"
	"testing"
)

// sandboxNameFrom spawns with the given options and returns the name `sbx
// create` received. Asserting on the ARGV rather than on an internal variable
// is what makes the test survive a refactor of the naming block: the name that
// matters is the one sbx is told, not the one den computed.
func sandboxNameFrom(t *testing.T, denHome string, o Options) string {
	t.Helper()
	f, d := fakeDeps()
	if err := Spawn(context.Background(), denHome, o, d); err != nil {
		t.Fatalf("spawn %+v: %v", o, err)
	}
	for _, call := range f.Calls {
		if len(call) == 0 || call[0] != "create" {
			continue
		}
		for i, arg := range call {
			if arg == "--name" && i+1 < len(call) {
				return call[i+1]
			}
		}
	}
	t.Fatalf("no `create --name` in calls: %v", f.Calls)
	return ""
}

// The naming table of the spec (§ "Le nom de sandbox"). One test, four rows:
// the rule is a single rule and reading it in one place is the point.
func TestInstanceNamesTheSandbox(t *testing.T) {
	for _, c := range []struct {
		name string
		o    Options
		want string
	}{
		{"bare", Options{Nest: "api"}, "api"},
		{"worktree only", Options{Nest: "api", Worktree: "feature/123"}, "api.feature-123"},
		{"instance only", Options{Nest: "api", Instance: "reco"}, "api.reco"},
		{"instance wins over worktree",
			Options{Nest: "api", Worktree: "feature/123", Instance: "reco"}, "api.reco"},
	} {
		t.Run(c.name, func(t *testing.T) {
			denHome, _ := denTestOptional(t)
			if got := sandboxNameFrom(t, denHome, c.o); got != c.want {
				t.Errorf("sandbox name = %q, want %q", got, c.want)
			}
		})
	}
}

// --as goes through the SAME flattening as -w: one charset, one rewrite, no
// second path to keep in sync.
func TestInstanceIsFlattenedLikeAWorktree(t *testing.T) {
	denHome, _ := denTestOptional(t)
	if got := sandboxNameFrom(t, denHome, Options{Nest: "api", Instance: "feat/x"}); got != "api.feat-x" {
		t.Errorf("sandbox name = %q, want %q", got, "api.feat-x")
	}
}

// An instance that cannot be named is refused BEFORE any side effect, like
// every other name den builds.
func TestSpawnRefusesAnUnnameableInstance(t *testing.T) {
	denHome, _ := denTestOptional(t)
	f, d := fakeDeps()
	err := Spawn(context.Background(), denHome, Options{Nest: "api", Instance: "-x"}, d)
	if err == nil {
		t.Fatal("an instance starting with `-` must be refused: it is indistinguishable from a flag")
	}
	if !strings.Contains(err.Error(), "instance") {
		t.Errorf("the refusal must name what is wrong (instance), got: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("refused, yet something was created: %v", f.Calls)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/spawn/ -run TestInstance -count=1`
Expected: FAIL — `unknown field Instance in struct literal`.

- [ ] **Step 3: Add `Options.Instance`**

Dans `internal/spawn/spawn.go`, juste sous le champ `Worktree` de `Options` :

```go
	// Instance is `--as`: the label that goes into the SECOND component of the
	// sandbox name, the one -w fills with the flattened branch. It exists
	// because that component is den's only discriminator — sbx has no
	// --label — so without it two different repo selections of one nest are
	// one sandbox, and the second spawn silently attaches the first
	// (2026-08-04-adhoc-repos-design.md, decision 7, which deferred exactly
	// this).
	//
	// It renames the SANDBOX and nothing else. The worktree directory keeps
	// being named after the flattened branch (worktree.Name.Dir): a label is
	// arbitrary, and two nests spawned `--as x` would otherwise fight over
	// <worktree_root>/x/<repo>.
	Instance string
```

- [ ] **Step 4: Fill the component from `--as`, then from `-w`**

Toujours dans `internal/spawn/spawn.go`, remplacer le bloc `worktreeName := worktree.Name{}` (~343)
par :

```go
	worktreeName := worktree.Name{}
	if o.Worktree != "" {
		flattened, err := config.FlattenSandboxComponent("worktree", o.Worktree)
		if err != nil {
			return err
		}
		worktreeName = worktree.Name{Dir: flattened, Branch: o.Worktree}
	}
	// The second component: `--as` when given, else the flattened branch.
	//
	// NOT worktreeName.Dir under --as, deliberately. Dir names the worktree
	// DIRECTORY (worktree.Path) and lands in the manifest as Worktree.Name;
	// letting the label reach it would put feature/123's worktree under
	// <root>/reco/api, and would make two different nests spawned `--as x`
	// collide on <root>/x/<repo>. A branch is a meaningful discriminator, a
	// label is not.
	instance := worktreeName.Dir
	if o.Instance != "" {
		flattened, err := config.FlattenSandboxComponent("instance", o.Instance)
		if err != nil {
			return err
		}
		instance = flattened
	}
```

Puis, ~ligne 399, remplacer l'appel et l'annonce :

```go
	sandboxName, err := sbx.SandboxName(nestComponent, instance)
	if err != nil {
		return err
	}
	// Announced early: otherwise the user looks for "feature/123" in
	// `den ls` and never finds it — the sandbox carries the flattened name
	// there. Under --as the gap is wider still (the sandbox carries neither
	// the branch nor anything derived from it), so the same line covers both
	// and the condition stays one condition.
	if worktreeName.Branch != "" && worktreeName.Branch != instance {
		fmt.Fprintf(d.Out,
			"worktree %q: branch name kept, sandbox becomes %s\n",
			worktreeName.Branch, sandboxName)
	}
```

- [ ] **Step 5: Wire the flag**

Dans `internal/cli/spawn.go`, sous la ligne `--worktree` :

```go
	cmd.Flags().StringVar(&o.Instance, "as", "",
		"name this instance, to run several sandboxes of one nest side by side")
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/spawn/ ./internal/cli/ -count=1`
Expected: PASS. Si un test existant de `internal/cli` compare l'aide de `den spawn` à un golden,
éditer le golden **à la main** — il n'y a pas de `-update`.

- [ ] **Step 7: Commit**

```bash
git add internal/spawn/spawn.go internal/spawn/instance_test.go internal/cli/spawn.go
git commit -m "feat(spawn): --as names the instance in the sandbox name

sbx has no --label, so the sandbox name is den's only handle and its
second component the only discriminator. -w was the only thing that
could fill it, which made two repo selections of one nest one sandbox.

--as fills that component directly. It renames the sandbox only: the
worktree directory keeps being named after the flattened branch, or two
nests spawned --as x would collide under <worktree_root>/x/<repo>."
```

---

### Task 2: le manifeste et `den ls` disent la vérité sous `--as`

**Files:**
- Modify: `internal/sbx/ls.go` (méthode `Sandbox.Worktree()`)
- Modify: `internal/cli/ls.go` (~ligne 96 l'en-tête, ~116 le repli)
- Modify: `internal/spawn/instance_test.go` (ajouts)
- Modify: `internal/cli/ls_test.go` (ajouts)

**Interfaces:**
- Consumes: `spawn.Options.Instance` (tâche 1), `manifest.Manifest.Worktree *manifest.Worktree`.
- Produces: `sbx.Sandbox.Instance() string` — remplace `Sandbox.Worktree()`, même corps.

- [ ] **Step 1: Write the failing test — le manifeste**

Ajouter à `internal/spawn/instance_test.go` :

```go
// Decision 4: --as renames the SANDBOX, never the worktree directory. The
// manifest is where that separation is observable, and it is also what `den
// rm` replays — a label recorded as Worktree.Name would send worktree.Remove
// at a directory nobody created.
func TestInstanceDoesNotRenameTheWorktreeDirectory(t *testing.T) {
	denHome, _ := denTestOptional(t)
	_, d := fakeDeps()
	o := Options{Nest: "api", Worktree: "feature/123", Instance: "reco"}
	if err := Spawn(context.Background(), denHome, o, d); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	m, err := manifest.Read(denHome, "api.reco")
	if err != nil {
		t.Fatalf("reading the manifest of api.reco: %v", err)
	}
	if m.Worktree == nil {
		t.Fatal("a -w spawn must record a worktree block")
	}
	if m.Worktree.Name != "feature-123" {
		t.Errorf("Worktree.Name = %q, want %q (the flattened BRANCH, not the label)",
			m.Worktree.Name, "feature-123")
	}
	if m.Worktree.Branch != "feature/123" {
		t.Errorf("Worktree.Branch = %q, want %q", m.Worktree.Branch, "feature/123")
	}
}

// --as without -w creates no worktree at all: the record must carry NO
// worktree block, or `den ls` would print a branch that does not exist and
// `den rm` would look for a directory nobody created.
func TestInstanceWithoutWorktreeRecordsNoWorktreeBlock(t *testing.T) {
	denHome, _ := denTestOptional(t)
	_, d := fakeDeps()
	if err := Spawn(context.Background(), denHome, Options{Nest: "api", Instance: "reco"}, d); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	m, err := manifest.Read(denHome, "api.reco")
	if err != nil {
		t.Fatalf("reading the manifest of api.reco: %v", err)
	}
	if m.Worktree != nil {
		t.Errorf("no -w was given, yet a worktree block was recorded: %+v", m.Worktree)
	}
}
```

Ajouter `"github.com/PillowPillow/den/internal/manifest"` aux imports du fichier.

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/spawn/ -run TestInstance -count=1`
Expected: PASS des deux — la tâche 1 a déjà séparé `instance` de `worktreeName.Dir`, et
`manifestOf` (spawn.go:1786) écrit `wt.Dir`. **Ces tests fixent l'acquis** : ils échoueraient si
quelqu'un « simplifiait » en réutilisant `instance` dans le bloc worktree. Si l'un échoue, c'est la
tâche 1 qui est fausse — la corriger là, pas ici.

- [ ] **Step 3: Write the failing test — `den ls`**

`internal/cli/ls_test.go` a déjà son idiome — `testDenHome(t)`, un `sbx.Fake` littéral dont
`Responses["ls --json"]` porte le JSON, `executeCmdWithSbx(t, f, "ls")`, et `lsManifest(t, denHome,
sandbox, mount)` pour poser un enregistrement. **L'employer tel quel ; n'introduire aucun helper.**

Ajouter :

```go
// A sandbox named by --as carries a label in component 2, and a label is not a
// branch. With a record present, the WORKTREE column must read from the record
// alone: the pre-existing fallback on component 2 was written when that
// component could only ever be a flattened branch.
func TestLsDoesNotPrintTheInstanceAsAWorktree(t *testing.T) {
	denHome := testDenHome(t) // nest "api" is declared there
	lsManifest(t, denHome, "api.reco", "/w/api")

	f := &sbx.Fake{Responses: map[string]sbx.Response{
		"ls --json": {Output: []byte(
			`{"sandboxes":[{"name":"api.reco","status":"running","workspaces":["/w/api"]}]}`)},
	}}

	out, err := executeCmdWithSbx(t, f, "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	header := strings.Fields(lines[0])
	expectedHeader := []string{"NAME", "NEST", "INSTANCE", "WORKTREE", "STATUS", "WORKSPACES"}
	if len(header) != len(expectedHeader) {
		t.Fatalf("header = %v, expected %v", header, expectedHeader)
	}
	for i, col := range expectedHeader {
		if header[i] != col {
			t.Errorf("header column %d = %q, expected %q", i, header[i], col)
		}
	}

	fields := strings.Fields(lines[1])
	expectedLine := []string{"api.reco", "api", "reco", "-", "running"}
	if len(fields) < len(expectedLine) {
		t.Fatalf("data line = %v, expected at least %v", fields, expectedLine)
	}
	for i, val := range expectedLine {
		if fields[i] != val {
			t.Errorf("column %d = %q, expected %q; full line: %v", i, fields[i], val, fields)
		}
	}
}
```

Le repli sans enregistrement a DÉJÀ son test — `TestLsFallsBackToTheSandboxNameWithoutARecord`
(ls_test.go:417). Ne pas en écrire un second : il doit rester VERT après la tâche, et c'est lui qui
prouve que le repli n'a pas été supprimé pour les sandboxes legacy.

`TestLsPrintsTheColumns` (ls_test.go:18) assert l'en-tête à cinq colonnes et la ligne
`{"api.feat12", "api", "feat12", "running"}` : **il rougira**, et c'est attendu. Le mettre à jour —
en-tête à six colonnes, ligne `{"api.feat12", "api", "feat12", "feat12", "running"}` — car cette
sandbox n'a aucun enregistrement, donc INSTANCE et WORKTREE valent tous deux le composant 2. Même
chose pour `TestLsShowsTheBranchAsTypedAndThePrefixedNest` (ls_test.go:319) si ses assertions
portent sur des positions de colonne.

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run TestLs -count=1`
Expected: FAIL — pas de colonne INSTANCE, et `reco` apparaît en WORKTREE.

- [ ] **Step 5: Rename `Sandbox.Worktree()` to `Sandbox.Instance()`**

Dans `internal/sbx/ls.go`, remplacer la méthode :

```go
// Instance is the second name component: the label of `--as`, or the
// flattened branch of `-w`, or empty. It is NOT "the worktree" — that
// reading was true only while -w was the sole thing that could fill this
// component. The branch, when there is one, lives in the manifest.
func (s Sandbox) Instance() string {
	_, instance := SplitName(s.Name)
	return instance
}
```

Puis `go build ./...` et corriger chaque appelant : le compilateur les liste, il n'y a rien à
chercher à la main.

- [ ] **Step 6: Fix the `den ls` columns**

Dans `internal/cli/ls.go`, l'en-tête (~ligne 96) :

```go
			fmt.Fprintln(w, "NAME\tNEST\tINSTANCE\tWORKTREE\tSTATUS\tWORKSPACES")
```

Et le bloc du repli (~ligne 116) :

```go
				// The record, when there is one, is the ONLY place these two
				// strings survive: flattening rewrote the branch on its way
				// into the sandbox name, and the ":" of a source reference is
				// not in sbx's --name charset.
				//
				// The fallback on component 2 now applies ONLY when there is
				// no record at all. It was written when that component could
				// only be a flattened branch; under --as it is a label, and
				// printing it as a branch would name something that does not
				// exist in any repository. A record WITHOUT a worktree block
				// means "no worktree", and renders as such.
				instance := b.Instance()
				wt := ""
				m, hasRecord := recorded[b.Name]
				switch {
				case !hasRecord:
					wt = instance
				case m.Worktree != nil:
					wt = m.Worktree.Branch
				}
				if hasRecord && m.Nest.Ref != "" {
					nestName = m.Nest.Ref
				}
				if undeclared {
					nestName += " ?" // not declared in ~/.den/nests
				}
				if instance == "" {
					instance = "-"
				}
				if wt == "" {
					wt = "-"
				}
```

…et ajouter `instance` à la ligne `Fprintf` du tableau, entre `nestName` et `wt`.

- [ ] **Step 7: Run the whole suite**

Run: `task check`
Expected: PASS. Les goldens de `den ls` qui portent l'en-tête du tableau changent : les éditer **à
la main**.

- [ ] **Step 8: Commit**

```bash
git add internal/sbx/ls.go internal/cli/ls.go internal/cli/ls_test.go \
        internal/spawn/instance_test.go internal/cli/testdata
git commit -m "feat(ls): an INSTANCE column, and a WORKTREE column that stops guessing

Sandbox.Worktree() derived the branch from name component 2, which was
sound while -w was the only thing that could fill it. Under --as that
component is a label, so the method becomes Instance() and the branch is
read from the manifest alone.

The fallback on component 2 survives for sandboxes with NO record —
legacy, or created outside den — where it is still the best guess
available."
```

---

### Task 3: la clé `select:`

**Files:**
- Modify: `internal/nest/nest.go` (struct `Nest`, constantes, validation dans `LoadNest`)
- Modify: `internal/nest/nest_test.go`

**Interfaces:**
- Produces: `nest.Nest.Select string`, constantes `nest.SelectAll = "all"` et
  `nest.SelectPrompt = "prompt"`, méthode `(*Nest).PromptsForRepos() bool` — consommées par les
  tâches 4, 5 et 6.

- [ ] **Step 1: Write the failing test**

Ajouter à `internal/nest/nest_test.go` :

```go
func TestSelectDefaultsToAll(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "api", "stack: devx\nrepos:\n  - { path: /dev/api }\n")

	n, err := LoadNest(denHome, "api")
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if n.Select != "" {
		t.Errorf("Select = %q, want the zero value: an existing nest must not change meaning", n.Select)
	}
	if n.PromptsForRepos() {
		t.Error("a nest with no `select:` must not prompt")
	}
}

func TestSelectPromptIsRead(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "api", "stack: devx\nselect: prompt\nrepos:\n  - { path: /dev/api }\n")

	n, err := LoadNest(denHome, "api")
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if !n.PromptsForRepos() {
		t.Error("`select: prompt` must prompt")
	}
}

// den refuses rather than normalizing in silence (spec §2). An unknown value
// is the `egres:` trap of §12 in another guise: taken as "all", a mistyped
// `promt` would mount thirty repos without a word.
func TestSelectRefusesAnUnknownValue(t *testing.T) {
	denHome := t.TempDir()
	writeNest(t, denHome, "api", "stack: devx\nselect: promt\nrepos:\n  - { path: /dev/api }\n")

	_, err := LoadNest(denHome, "api")
	if err == nil {
		t.Fatal("an unknown `select:` value must be refused")
	}
	for _, want := range []string{"promt", "all", "prompt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q — got: %v", want, err)
		}
	}
}
```

`writeNest` : réutiliser le helper d'écriture de nest déjà présent dans `nest_test.go`. S'il porte
un autre nom, garder celui du fichier — ne pas en introduire un second.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/nest/ -run TestSelect -count=1`
Expected: FAIL — `n.Select undefined`.

- [ ] **Step 3: Add the field, the constants and the accessor**

Dans `internal/nest/nest.go`, au-dessus de la struct `Nest` :

```go
// The values of a nest's `select:` — how it decides WHICH of its optional
// repos a spawn mounts.
//
// A string rather than a `generic: true` boolean: "all" is a value one can
// write to state the intent, and a third mode later would not have to break
// the type. The zero value "" means SelectAll, so no existing nest changes
// behaviour by being re-read.
const (
	// SelectAll mounts every optional repo unless --without/--only/-i says
	// otherwise. The historical behaviour, and the default.
	SelectAll = "all"
	// SelectPrompt declares a nest with NO default selection: the repos are
	// chosen at spawn time. It exists for the generic nest of a microservice
	// team — thirty repos of which a session wants four, in a combination
	// that changes weekly — where a nest file per feature is not tenable.
	SelectPrompt = "prompt"
)
```

Dans la struct, sous `Stack` :

```go
	// Select is `select:`; see SelectAll / SelectPrompt. LoadNest refuses any
	// other value.
	Select string `yaml:"select"`
```

Et après la struct :

```go
// PromptsForRepos reports whether this nest chooses its repos at spawn time.
//
// A method rather than `n.Select == SelectPrompt` at each call site: three
// packages ask the question (spawn's entry point, the checklist's starting
// state, the no-terminal refusal), and the zero value has to mean SelectAll in
// all three — a comparison written by hand would eventually be written against
// SelectAll instead, where "" would answer false.
func (n *Nest) PromptsForRepos() bool { return n.Select == SelectPrompt }
```

- [ ] **Step 4: Validate in `LoadNest`**

Dans `LoadNest`, juste après `n.Name = name` et AVANT la boucle sur `n.Repos` :

```go
	// Validated here rather than at spawn: `den nest ls` and `den lint` must
	// see the same refusal, and the earliest reader is the one that gives the
	// user the shortest path to the faulty line.
	switch n.Select {
	case "", SelectAll, SelectPrompt:
	default:
		return nil, fmt.Errorf(
			"nest %q: `select: %s` is not a known mode — use %q (mount every optional repo, "+
				"the default) or %q (choose the repos at spawn time); fix `select:` in %s",
			name, n.Select, SelectAll, SelectPrompt, path)
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/nest/ ./internal/lint/ -count=1`
Expected: PASS. `internal/lint` passe sans changement — `select:` n'est pas une référence, il ne
peut pas porter de préfixe de source.

- [ ] **Step 6: Commit**

```bash
git add internal/nest/nest.go internal/nest/nest_test.go
git commit -m "feat(nest): a select: key, all or prompt

A generic nest — thirty optional repos, four wanted per session — has no
default selection to offer. select: prompt says so declaratively rather
than den deriving it from 'many optional repos', which would silently
turn a two-repo nest into a prompting one.

An unknown value is refused: read as 'all', a mistyped promt: would mount
thirty repos without a word."
```

---

### Task 4: la checklist — départ vide et clés non mappées

**Files:**
- Modify: `internal/spawn/interactive.go` (`promptOptionalRepos`, `interactiveWithout`)
- Modify: `internal/spawn/interactive_test.go`

**Interfaces:**
- Consumes: `nest.Nest.PromptsForRepos()` (tâche 3), `config.Global.Repos map[string]string`.
- Produces: `promptOptionalRepos(out io.Writer, in io.Reader, nestName string, repos []nest.Repo,
  startChecked bool, mapping map[string]string) ([]string, error)` — appelée par la tâche 5.

- [ ] **Step 1: Write the failing test**

Dans `internal/spawn/interactive_test.go`, remplacer le helper `prompt` par une forme qui porte les
deux paramètres, et ajouter les cas :

```go
func prompt(t *testing.T, input string) ([]string, string, error) {
	t.Helper()
	return promptWith(t, input, true, nil)
}

func promptWith(t *testing.T, input string, startChecked bool,
	mapping map[string]string) ([]string, string, error) {
	t.Helper()
	var out bytes.Buffer
	without, err := promptOptionalRepos(&out, strings.NewReader(input), "api",
		optionalRepos(), startChecked, mapping)
	return without, out.String(), err
}

// Decision 9: a `select: prompt` checklist starts EMPTY, and confirming it
// as-is excludes every optional repo. The -i checklist keeps starting full —
// the two answer different questions, and both readings live in this one test.
func TestPromptStartingStateFollowsTheMode(t *testing.T) {
	for _, c := range []struct {
		name         string
		startChecked bool
		want         []string
	}{
		{"-i starts full", true, nil},
		{"select: prompt starts empty", false, []string{"worker", "docs"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			without, _, err := promptWith(t, "\n", c.startChecked, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(without, c.want) {
				t.Errorf("confirming as-is gave --without %v, want %v", without, c.want)
			}
		})
	}
}

// The header keeps its "required repos are always mounted" clause in BOTH
// modes: a select: prompt nest may declare required repos, and "none selected"
// alone would then be a lie.
func TestPromptHeaderAlwaysNamesRequiredRepos(t *testing.T) {
	for _, startChecked := range []bool{true, false} {
		_, out, err := promptWith(t, "\n", startChecked, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, "required repos are always mounted") {
			t.Errorf("startChecked=%v: header lost its required-repos clause:\n%s", startChecked, out)
		}
	}
}

// Decision 10: an unmapped key is ANNOTATED, never hidden and never refused.
// Refusing the tick here would make the checklist a second judge of the repo
// mapping, whose single judge is resolveRepoKeys — and it would be a mute
// refusal on the one surface where the user cannot yet see what they asked
// for.
func TestPromptAnnotatesUnmappedKeys(t *testing.T) {
	repos := []nest.Repo{
		{Path: "/dev/backend"},
		{Key: "worker", Optional: true},
		{Key: "docs", Optional: true},
	}
	var out bytes.Buffer
	if _, err := promptOptionalRepos(&out, strings.NewReader("\n"), "api", repos, false,
		map[string]string{"worker": "/dev/worker"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "docs") || !strings.Contains(rendered, "not mapped") {
		t.Errorf("an unmapped key must be annotated:\n%s", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "worker") && strings.Contains(line, "not mapped") {
			t.Errorf("a MAPPED key must carry no annotation: %q", line)
		}
	}
}
```

Ajouter `"github.com/PillowPillow/den/internal/nest"` aux imports s'il n'y est pas.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/spawn/ -run TestPrompt -count=1`
Expected: FAIL — `too many arguments in call to promptOptionalRepos`.

- [ ] **Step 3: Widen `promptOptionalRepos`**

Dans `internal/spawn/interactive.go` :

```go
// promptOptionalRepos draws the checklist of a nest's OPTIONAL repos and reads
// the toggles until the user confirms. It returns the short names of the repos
// left unchecked — a `--without` list.
//
// startChecked is the initial state of every box, and it is NOT cosmetic. `-i`
// starts full, because confirming an -i checklist without touching it must
// produce exactly what `den spawn` alone produces
// (TestInteractiveProducesTheSameArgvAsTheEquivalentWithout). A `select:
// prompt` nest starts EMPTY, because it has no default selection to propose by
// definition — and thirty ticked boxes would turn an empty line into a
// thirty-repo mount.
//
// mapping is the personal `repos:` of config.yaml, used to ANNOTATE the keys
// it does not carry. Annotation only: ticking an unmapped key stays possible,
// and the refusal that follows is resolveRepoKeys', which names the key, the
// file and the clone URL. Refusing the tick here would make this a second
// judge of the mapping, whose single judge is that function. mapping may be
// nil — every entry then renders unannotated, which is what a path-typed nest
// wants.
//
// Required repos are neither listed nor numbered (spec §6.2): they are always
// mounted, and numbering them would make "1" designate different repos
// depending on how many required ones happen to precede it.
//
// bufio.Scanner, no TUI library. `cobra` and `yaml.v3` are den's only
// dependencies and that is a claimed property (a static binary, HANDOFF §8);
// what this checklist needs — print a list, read a line, toggle — is a dozen
// lines of stdlib. A TUI library would buy cursor movement and colours for the
// price of the one property the project advertises.
func promptOptionalRepos(out io.Writer, in io.Reader, nestName string, repos []nest.Repo,
	startChecked bool, mapping map[string]string) ([]string, error) {
	optional := make([]nest.Repo, 0, len(repos))
	for _, r := range repos {
		if r.Optional {
			optional = append(optional, r)
		}
	}

	keep := make([]bool, len(optional))
	for i := range keep {
		keep[i] = startChecked
	}

	selected := "none selected"
	if startChecked {
		selected = "all selected"
	}
	fmt.Fprintf(out, "nest %s: %d optional repo(s), %s — required repos are always mounted\n",
		nestName, len(optional), selected)

	s := bufio.NewScanner(in)
	for {
		for i, r := range optional {
			box := " "
			if keep[i] {
				box = "x"
			}
			fmt.Fprintf(out, "  %d [%s] %s%s\n", i+1, box, r.Name(), unmappedNote(r, mapping))
		}
		// … le reste du corps est INCHANGÉ …
```

Et, sous la fonction :

```go
// unmappedNote annotates a key-typed repo the personal mapping does not carry.
// Empty for a path-typed repo, and empty for a mapped key: an annotation on
// every line would annotate nothing.
func unmappedNote(r nest.Repo, mapping map[string]string) string {
	if r.Key == "" {
		return ""
	}
	if _, ok := mapping[r.Key]; ok {
		return ""
	}
	return "      (not mapped in config.yaml)"
}
```

- [ ] **Step 4: Update `interactiveWithout`**

Elle gagne les deux paramètres et les transmet. Signature et dernière ligne :

```go
func interactiveWithout(d Deps, n *nest.Nest, mapping map[string]string) ([]string, error) {
	// … corps inchangé jusqu'au return …
	return promptOptionalRepos(d.Out, in, n.Name, n.Repos, !n.PromptsForRepos(), mapping)
}
```

Le seul appelant actuel est dans `Spawn` : lui passer `g.Repos`. La tâche 5 reprend ce site.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/spawn/ -count=1`
Expected: PASS, y compris `TestInteractiveProducesTheSameArgvAsTheEquivalentWithout` —
`!n.PromptsForRepos()` vaut `true` sur un nest ordinaire, donc `-i` démarre toujours tout coché.

- [ ] **Step 6: Commit**

```bash
git add internal/spawn/interactive.go internal/spawn/interactive_test.go
git commit -m "feat(spawn): the checklist knows its starting state and the repo mapping

-i starts full — confirming it untouched must equal a plain spawn. A
select: prompt nest starts empty: it has no default selection by
definition, and thirty ticked boxes would make an empty line a
thirty-repo mount.

Unmapped keys are annotated, not refused: the single judge of the
mapping is resolveRepoKeys, and it names the key, the file and the
clone URL."
```

---

### Task 5: `select: prompt` — le point d'entrée dans `Spawn`

**Files:**
- Modify: `internal/spawn/spawn.go` (le bloc `if o.Interactive` ~309)
- Modify: `internal/spawn/interactive.go` (message de refus sans TTY)
- Modify: `internal/spawn/interactive_test.go`

**Interfaces:**
- Consumes: `nest.Nest.PromptsForRepos()` (tâche 3), `interactiveWithout(d, n, mapping)` (tâche 4).
- Produces: rien de nouveau — c'est du câblage.

- [ ] **Step 1: Write the failing test**

Ajouter à `internal/spawn/interactive_test.go` :

```go
// denTestPrompting is denTestOptional's nest, with `select: prompt` and every
// repo optional — the generic nest of the spec, in miniature.
func denTestPrompting(t *testing.T) string {
	t.Helper()
	denHome, repos := denTestOptional(t)
	write(t, filepath.Join(denHome, "nests", "generic.yaml"),
		"stack: devx\nselect: prompt\nrepos:\n"+
			"  - { path: "+repos["api"]+", optional: true }\n"+
			"  - { path: "+repos["worker"]+", optional: true }\n"+
			"  - { path: "+repos["docs"]+", optional: true }\n")
	return denHome
}

// THE acceptance criterion of the mode, and the same one -i already carries:
// the checklist is a source of input placed in front of nest.Resolve, never a
// second selection rule. Compared on the rendered argv — two selections that
// merely LOOK equivalent are exactly what this test exists to catch.
func TestPromptModeProducesTheSameArgvAsTheEquivalentOnly(t *testing.T) {
	denHome := denTestPrompting(t)

	promptFake, promptDeps := fakeDeps()
	promptDeps.In = strings.NewReader("1 2\n\n") // tick api and worker
	promptDeps.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, promptDeps); err != nil {
		t.Fatalf("prompting spawn: %v", err)
	}

	flagFake, flagDeps := fakeDeps()
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Only: []string{"api", "worker"}}, flagDeps); err != nil {
		t.Fatalf("--only spawn: %v", err)
	}

	if !slices.EqualFunc(promptFake.Calls, flagFake.Calls, slices.Equal) {
		t.Errorf("select: prompt and the equivalent --only must produce the SAME sbx calls\nprompt: %v\n--only: %v",
			promptFake.Calls, flagFake.Calls)
	}
	if !promptFake.HasCalled("create") {
		t.Fatalf("no create to compare; calls: %v", promptFake.Calls)
	}
}

// A prompt cannot be literally mandatory: spawn already refuses -i without a
// terminal, and den exec exists for pipes and CI. The refusal names the
// non-interactive form, in the same breath.
func TestPromptModeRefusesWithoutATerminal(t *testing.T) {
	denHome := denTestPrompting(t)
	f, d := fakeDeps()
	d.IsTTY = func() bool { return false }

	err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, d)
	if err == nil {
		t.Fatal("a prompting nest with no terminal and no --only must be refused")
	}
	if !strings.Contains(err.Error(), "--only") {
		t.Errorf("the refusal must name the non-interactive form, got: %v", err)
	}
	if f.HasCalled("create") {
		t.Errorf("refused, yet something was created: %v", f.Calls)
	}
}

// --only answers the question, so there is nothing left to ask: no terminal is
// needed and none is probed. This is what makes the mode usable from `den
// exec`, a script and CI.
func TestPromptModeWithOnlyNeedsNoTerminal(t *testing.T) {
	denHome := denTestPrompting(t)
	f, d := fakeDeps()
	d.IsTTY = func() bool { return false }

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Only: []string{"api"}}, d); err != nil {
		t.Fatalf("--only on a prompting nest must not need a terminal: %v", err)
	}
	if !f.HasCalled("create") {
		t.Errorf("nothing was created: %v", f.Calls)
	}
}

// -i on a prompting nest is REDUNDANT, not contradictory: it asks for the
// checklist the nest opens anyway. Accepted, and identical.
func TestPromptModeAcceptsRedundantInteractiveFlag(t *testing.T) {
	denHome := denTestPrompting(t)

	bare, bareDeps := fakeDeps()
	bareDeps.In = strings.NewReader("1\n\n")
	bareDeps.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, bareDeps); err != nil {
		t.Fatalf("bare spawn: %v", err)
	}

	withI, withIDeps := fakeDeps()
	withIDeps.In = strings.NewReader("1\n\n")
	withIDeps.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome,
		Options{Nest: "generic", Interactive: true}, withIDeps); err != nil {
		t.Fatalf("-i spawn: %v", err)
	}

	if !slices.EqualFunc(bare.Calls, withI.Calls, slices.Equal) {
		t.Errorf("-i on a prompting nest must change nothing\nbare: %v\n-i:   %v", bare.Calls, withI.Calls)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/spawn/ -run TestPromptMode -count=1`
Expected: FAIL — le nest `generic` spawn ses trois repos sans rien demander.

- [ ] **Step 3: Wire the entry point**

Dans `internal/spawn/spawn.go`, remplacer le bloc `if o.Interactive { … }` (~309) par :

```go
	// The checklist has TWO entry points and ONE implementation: `-i` on any
	// nest, and a `select: prompt` nest that has no default selection to
	// offer. Both write into the SAME `without` list that --without fills, so
	// nest.Resolve keeps applying the one selection rule it already owns.
	//
	// A selection flag answers the question outright, so it silences both
	// entry points — that is what makes a prompting nest usable from `den
	// exec`, a script and CI, and `-i` + a flag is refused far upstream (step
	// 0) as the contradiction it is.
	without := o.Without
	if (o.Interactive || n.PromptsForRepos()) && len(o.Without) == 0 && len(o.Only) == 0 {
		if without, err = interactiveWithout(d, n, g.Repos); err != nil {
			return err
		}
	}
```

- [ ] **Step 4: Make the no-terminal refusal name the right mode**

Dans `internal/spawn/interactive.go`, `interactiveWithout` — le refus actuel s'ouvre sur `-i:`, ce
qui serait faux pour un nest qui n'a reçu aucun drapeau :

```go
	if d.IsTTY == nil || !d.IsTTY() {
		// The prefix follows the entry point: naming `-i` to someone who never
		// typed it sends them looking for a flag they did not use. Both
		// sentences name the same remedy, because there IS only one.
		if n.PromptsForRepos() {
			return nil, fmt.Errorf(
				"nest %s selects its repos at spawn time and there is no terminal on den's input — "+
					"the checklist has nobody to ask, and reading anyway would block a pipe or a CI "+
					"job forever; %s", n.Name, nonInteractiveEquivalents)
		}
		return nil, fmt.Errorf(
			"-i: no terminal on den's input — the checklist has nobody to ask, and reading anyway would "+
				"block a pipe or a CI job forever; %s", nonInteractiveEquivalents)
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/spawn/ -count=1`
Expected: PASS, tous — dont `TestSpawnRefusesInteractiveWithASelectionFlag`, dont le refus vit à
l'étape 0 et n'est pas touché.

- [ ] **Step 6: Commit**

```bash
git add internal/spawn/spawn.go internal/spawn/interactive.go internal/spawn/interactive_test.go
git commit -m "feat(spawn): a select: prompt nest opens the checklist without -i

Two entry points, one implementation: both write into the same
--without list that nest.Resolve already consumes, so the prompt can
never become a second selection rule.

A selection flag silences the prompt, which is what keeps the mode
usable from den exec, a script and CI — a nest that DEMANDED a terminal
would be unusable headless."
```

---

### Task 6: le contrôle de vivacité passe devant la checklist

**Files:**
- Modify: `internal/spawn/spawn.go` (remontée du calcul du nom et de `sbx.Find`)
- Modify: `internal/spawn/interactive_test.go`

**Interfaces:**
- Consumes: tout ce qui précède.
- Produces: rien — c'est un réordonnancement, plus le message d'attache.

- [ ] **Step 1: Write the failing test**

```go
// Decision 6: on a live sandbox, no prompt at all. Asking someone to pick
// repos that cannot be mounted is the silence §2 forbids — and the question
// would be put to somebody with no way to guess it is pointless.
//
// The input is a reader that FAILS if read: an assertion on the rendered
// output would pass on a prompt that was drawn and then ignored.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Fatal("the checklist was opened on a live sandbox: nothing it collects can be mounted")
	return 0, io.EOF
}

func TestPromptModeDoesNotPromptWhenAttaching(t *testing.T) {
	denHome := denTestPrompting(t)

	// Create it once, with a selection.
	_, first := fakeDeps()
	first.In = strings.NewReader("1\n\n")
	first.IsTTY = func() bool { return true }
	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, first); err != nil {
		t.Fatalf("first spawn: %v", err)
	}

	// Attach it. Same name, no flags — the checklist must stay shut.
	f, d := fakeDeps()
	d.In = failingReader{t}
	d.IsTTY = func() bool { return true }
	var out bytes.Buffer
	d.Out = &out
	// The Fake reports `generic` as running — the same scripting every attach
	// test in this package uses (spawn_test.go:309).
	f.Responses["ls --json"] = sbx.Response{
		Output: []byte(`{"sandboxes":[{"name":"generic","status":"running","workspaces":["/w/api"]}]}`),
	}
	if err := Spawn(context.Background(), denHome, Options{Nest: "generic"}, d); err != nil {
		t.Fatalf("attaching spawn: %v", err)
	}
	if !strings.Contains(out.String(), "--as") {
		t.Errorf("the attach message must name the way to run a different set:\n%s", out.String())
	}
}
```

`sbx.Fake` script ses réponses par `Responses` / `Default` : `f.Responses["ls --json"]` est le seul
mécanisme, celui de `TestSpawnAttachesWithoutRecreatingWhenSandboxExists` (spawn_test.go:309). **Ne
rien ajouter à `sbx.Fake`** — c'est un fichier de production partagé par quatre paquets.
Les imports du test ajoutent `"io"` et `"bytes"` si absents.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/spawn/ -run TestPromptModeDoesNotPromptWhenAttaching -count=1`
Expected: FAIL sur le `t.Fatal` du `failingReader` — la checklist s'ouvre avant que den sache que
la sandbox est vivante.

- [ ] **Step 3: Hoist the name computation**

Déplacer le bloc de nommage (celui de la tâche 1, `worktreeName` → `instance` → `nestComponent` →
`sandboxName`) AU-DESSUS de l'appel à `interactiveWithout`, juste après `n.Stack = ref`. Il est
**pur** : il ne lit que `o.Nest`, `o.Worktree`, `o.Instance`, `srcName` et le disque pour la
détection de collision — jamais la sélection. Aucune ligne de son corps ne change.

- [ ] **Step 4: Hoist the liveness read**

Déplacer la lecture `boxes` / `live := sbx.Find(boxes, sandboxName)` (~606) juste sous le bloc de
nommage, et ajouter, avant la checklist :

```go
	// A live sandbox is attached to; its mounts come from its creation and
	// nothing is reapplied (§6). So there is nothing left to choose, and the
	// checklist must not open — asking for a selection that cannot be applied
	// is the silence §2 forbids.
	//
	// The repos come from the RECORD, never from a re-derivation: that is
	// exactly what internal/manifest exists for. No record (legacy sandbox, or
	// one created outside den) simply drops that line — the message keeps its
	// remedy, which is the part that matters.
	if live != nil && n.PromptsForRepos() {
		fmt.Fprintf(d.Out, "sandbox %s already live: attaching\n", sandboxName)
		if m, mErr := manifest.Read(denHome, sandboxName); mErr == nil {
			names := make([]string, 0, len(m.Repos))
			for _, repo := range m.Repos {
				names = append(names, repo.Name)
			}
			fmt.Fprintf(d.Out, "  its repos come from its creation: %s\n", strings.Join(names, ", "))
		}
		fmt.Fprintf(d.Out, "  to run a different set alongside it, spawn `--as <label>`\n")
	}
```

- [ ] **Step 5: Document what the reorder costs, at the site**

Au-dessus du bloc remonté :

```go
	// ORDER, load-bearing. The name is computed and the sandbox list is read
	// BEFORE the checklist, so a live sandbox is attached to without a
	// question nobody can act on.
	//
	// This puts a `sbx ls` READ ahead of the config refusals nest.Resolve
	// carries (an unmapped key, a missing git dir). §6's promise survives
	// verbatim — it is about SIDE EFFECTS ("a refusal never leaves an orphaned
	// worktree") and listing creates nothing. What moves is the order of
	// DIAGNOSTICS: a typo in `repos:` now surfaces after a call to sbx.
	//
	// Unconditional, not reserved to `select: prompt` nests: an order that
	// depends on a configuration key is two spawn sequences to keep true, and
	// §6 describes one.
```

- [ ] **Step 6: Run the whole suite**

Run: `task check`
Expected: PASS. Les tests dont les assertions portent sur l'ORDRE des appels au `sbx.Fake` peuvent
rougir : c'est le coût annoncé du réordonnancement. Corriger l'assertion, jamais l'ordre — et
seulement après avoir vérifié qu'aucun **effet de bord** n'a changé de place (rien qui crée un
worktree, un mixin ou une VM ne doit bouger).

- [ ] **Step 7: Commit**

```bash
git add internal/spawn/spawn.go internal/spawn/interactive_test.go
git commit -m "feat(spawn): read liveness before opening the checklist

A live sandbox is attached to and nothing is reapplied to it, so a
selection collected on that branch can never be mounted. Asking for it
anyway is the silence §2 forbids, put to somebody with no way to guess
the question is pointless.

This moves an `sbx ls` READ ahead of nest.Resolve's config refusals.
§6's promise is about side effects — listing creates nothing — so what
changes is the order of diagnostics, not the guarantee."
```

---

### Task 7: la documentation

**Files:**
- Modify: `README.md` (section `den spawn`, table des drapeaux ; section nests)
- Modify: `docs/superpowers/handoffs/HANDOFF.md`

**Interfaces:** aucune.

- [ ] **Step 1: Document `--as` in the `den spawn` flag table**

Ajouter la ligne, dans l'ordre où les drapeaux sont déclarés dans `newSpawnCmd` :

```markdown
| `--as <label>` | name this instance, to run several sandboxes of one nest side by side |
```

Et, dans le corps de la section, un paragraphe — le README de den est en anglais :

```markdown
### Instances

A sandbox is named `<nest>[.<instance>]`, and the instance is the only thing
that distinguishes two sandboxes of one nest. `-w` fills it with the flattened
branch; `--as` fills it directly:

```bash
den spawn api --as analyse-a
den spawn api --as analyse-b   # same repos, two microVMs
```

`--as` renames the sandbox only. Under `-w`, the worktree directory keeps being
named after the branch, so `den spawn api -w feature/123 --as reco` puts the
worktree where `-w` alone would have put it.

Two instances mounting one working tree is allowed, and den has no read-only
mount to offer: two VMs writing one git index can corrupt it. `--as` is for
read-mostly concurrency. Two writers means two branches, hence `-w` alone —
`git worktree add` refuses a branch already checked out elsewhere, so two
instances cannot share one.
```

- [ ] **Step 2: Document `select:`**

Dans la section qui décrit les clés d'un nest :

```markdown
`select:` says how a nest picks among its optional repos:

- `all` (default) — mount every optional repo, unless `--without` / `--only` /
  `-i` says otherwise.
- `prompt` — the nest has no default selection; the repos are chosen at spawn
  time. With a terminal, the checklist opens without `-i` and starts empty.
  Without one, den refuses and names `--only`, which makes the same selection
  from a script or CI.

A key that is not mapped in your `~/.den/config.yaml` costs nothing as long as
you do not select its repo: that is what makes a thirty-repo generic nest
usable without cloning thirty repositories. The checklist annotates the ones
you have not mapped.
```

- [ ] **Step 3: Update the handoff**

`docs/superpowers/handoffs/HANDOFF.md` est courant et réécrit (contrairement aux handoffs datés).
Y refléter : `--as`, `select:`, la colonne INSTANCE de `den ls`, et le fait que `sbx ls` est
désormais lu avant `nest.Resolve`.

- [ ] **Step 4: Verify**

Run: `task check`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/superpowers/handoffs/HANDOFF.md
git commit -m "docs: --as, select:, and what two instances on one tree cost"
```

---

## Après le plan — la source `dg`

**Hors de ce dépôt, et dans cet ordre — il n'est pas optionnel.** Le décodage est strict partout et
`internal/lint` est fail-closed dans `den source add` (qui refuse **et supprime** le clone) comme
dans `den source update`. Un `select:` ajouté à `digitaleo-den-env` avant que l'équipe ait monté de
version fait échouer le `den source update` de chaque collègue, sur un message qui ressemble mot
pour mot à une faute de frappe :

```
error: nests/digitaleo.yaml: decoding errors:
  line 3: unknown key "select"
```

1. Livrer den (tâches 1-7, une release).
2. Faire monter de version l'équipe.
3. **Puis** ajouter `nests/digitaleo.yaml` à `digitaleo-den-env`, avec dans son README la version
   minimale de den.

---

## Self-review

**Couverture de la spec** — chaque décision verrouillée a sa tâche :

| Décision | Tâche |
|---|---|
| 1. composant 2 réutilisé, `SplitName` intact | 1 (aucune signature touchée) |
| 2. la branche vit dans le manifeste | 2 (tests du manifeste) |
| 3. `--as` vaut pour tout nest | 1 (le nest `api` du fixture n'est pas générique) |
| 4. `--as` ne renomme pas le répertoire de worktree | 1 (impl.) + 2 (tests) |
| 5. den n'engendre jamais de nom | 1 — aucune horloge n'entre nulle part |
| 6. vivacité devant la checklist | 6 |
| 7. l'invite n'est pas obligatoire ; `-i` redondant accepté | 5 |
| 8. `--only` ≡ checklist | 5 (`TestPromptModeProducesTheSameArgvAsTheEquivalentOnly`) |
| 9. départ vide / départ plein | 4 |
| 10. clé non mappée non sélectionnée ne coûte rien ; annotation | 4 |
| 11. un label n'est pas une sélection | rien à écrire — c'est la décision 7 d'adhoc-repos, déjà en place, et `reportUnmountedRepos` en est le signal |

Deux points de la surface de test de la spec ne sont PAS couverts par une tâche et c'est délibéré :

- *« un nest `select: prompt` dont la moitié des clés n'est pas mappée spawn sans erreur »* — la
  propriété est déjà vraie (`nest.Resolve` appelle `selectRepos` avant `resolveRepoKeys`) et la
  tâche 4 la teste au niveau de la checklist. Un test de bout en bout supplémentaire vaudrait, mais
  il ne changerait aucune ligne de production ; l'ajouter dans la tâche 5 si la revue le demande.
- *le golden de l'aide de `den spawn`* — traité en tâche 1 step 6, sans tâche propre, parce qu'il
  n'a pas de cycle de test à lui.

**Placeholders** — aucun « TBD », aucun « similar to Task N », aucun « add error handling ». Le seul
endroit où le plan renvoie à l'existant sans le citer est `writeNest` en tâche 3 : c'est une
instruction de **réutilisation**, et inventer un helper concurrent est précisément le défaut
qu'elle prévient. Les deux autres cas (l'idiome de `ls_test.go`, le scripting du `Fake`) sont
nommés avec leur fichier et leur ligne.

**Tests existants qui rougiront, et c'est attendu** — les nommer ici évite qu'on les prenne pour
des régressions : `TestLsPrintsTheColumns` (ls_test.go:18) et éventuellement
`TestLsShowsTheBranchAsTypedAndThePrefixedNest` (ls_test.go:319) en tâche 2, sur la colonne
ajoutée ; en tâche 6, tout test assertant l'ORDRE des appels au `Fake`. Aucun autre n'a de raison
de bouger — et si un test d'effet de bord bouge (worktree, mixin, VM), c'est le réordonnancement
qui est faux, pas le test.

**Cohérence des types** — `Options.Instance` (tâche 1) est le nom lu en 5 et 6 ; `PromptsForRepos()`
(tâche 3) est appelé en 4, 5, 6 sous ce nom exact ; `promptOptionalRepos(out, in, nestName, repos,
startChecked, mapping)` (tâche 4) est appelé avec cet ordre d'arguments en 4 et 5 ;
`Sandbox.Instance()` (tâche 2) remplace `Sandbox.Worktree()` partout, le compilateur listant les
appelants.
