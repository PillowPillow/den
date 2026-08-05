# `den spawn` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** faire du spawn une sous-commande ordinaire (`den spawn <nest> [repo...]`), supprimer la forme nue `den <nest>`, et rendre à den un refus qui liste ses commandes.

**Architecture:** `configureSpawn` — qui posait `RunE`, `Args` et six flags **sur le root** — devient `newSpawnCmd`, une sous-commande construite comme `newLsCmd`. Le root garde un `RunE` réduit à `cmd.Help()` (obligatoire : cobra court-circuite `ValidateArgs` sur un root non-runnable) et prend un `Args` maison, `unknownCommand`, qui rend le message de refus complet. `withSuggestion` et `warnAboutShadowedNests` disparaissent : la première greffait une suggestion sur un échec de résolution de nest, la seconde s'excusait d'une collision qui cesse d'exister.

**Tech Stack:** Go 1.2x · cobra v1.10.2 · Taskfile · tests standard `testing`, goldens comparés à la main.

**Spec:** `docs/superpowers/specs/2026-08-05-spawn-command-design.md` — à lire avant de commencer.

## Global Constraints

- **Version cible : v1.3.0.** Breaking, mais den n'a pas encore d'utilisateur : pas de shim, pas de fenêtre de dépréciation, pas de v2.
- **Le code, les commentaires et les messages utilisateur sont en anglais.** La spec et les plans sont en français.
- **Style dominant : le long commentaire « pourquoi » au site de décision** — ce qui a été rejeté, et quelle régression le choix empêche. Du code laconique détonne visiblement ; il faut égaler la densité alentour.
- **Aucun test n'appelle `t.Parallel()`, n'ouvre de socket, ni ne lance de processus.**
- **`task check`** (lint » typecheck » test, fail-fast) doit passer à la fin de chaque tâche. `task test` seul lance `go test -count=1 ./...` ; `gofmt` est **imposé**, pas conseillé.
- **Les goldens n'ont pas de flag `-update`** : ils s'écrivent à la main.
- **Le message d'erreur ne porte PAS de préfixe `den: `** : `cmd/den/main.go` imprime déjà `fmt.Fprintln(os.Stderr, "den:", err)`.

---

## File Structure

| Fichier | Responsabilité après le chantier |
|---|---|
| `internal/cli/root.go` | assemble l'arbre cobra ; **porte désormais** `unknownCommand` / `unknownCommandError` et `SuggestionsMinimumDistance` |
| `internal/cli/spawn.go` | **`newSpawnCmd`** seul — plus de `configureSpawn`, plus de `withSuggestion` |
| `internal/cli/nest.go` | `den nest ls` / `den nest show` — **`warnAboutShadowedNests` supprimée** |
| `internal/cli/source.go` | inchangé sauf le message post-`source add`, qui nomme `den spawn` |
| `internal/cli/testdata/unknown-command.golden` | **nouveau** — le refus complet, gelé |

Rien ne bouge hors de `internal/cli`.

---

### Task 1: `unknownCommandError`, le message de refus

**Files:**
- Modify: `internal/cli/root.go` (ajouter en fin de fichier, après `argsBetween`)
- Test: `internal/cli/root_test.go` (ajouter en fin de fichier)

**Interfaces:**
- Consumes: rien (cobra seul)
- Produces: `func unknownCommand(cmd *cobra.Command, args []string) error` — une `cobra.PositionalArgs`, consommée par la Task 2 comme `root.Args`. Et `func unknownCommandError(root *cobra.Command, arg string) error`, sa moitié testable.

Cette tâche n'est **pas encore câblée** : elle ajoute une fonction et ses tests, l'arbre reste inchangé et vert. C'est ce qui rend la Task 2 relisible — la Task 2 ne fait plus que du déplacement.

- [ ] **Step 1: Écrire les tests qui échouent**

Ajouter en fin de `internal/cli/root_test.go` :

```go
// testTree builds a throwaway root with two subcommands, for the tests of
// unknownCommandError below.
//
// A hand-built tree rather than NewRootCmd(): the message's SHAPE is what
// these tests pin — the padding, the order, the suggestion — and pinning it
// against den's real command list would make every future `AddCommand` break
// tests that are not about it. The real list is frozen ONCE, in the golden of
// TestUnknownFirstArgumentListsTheCommands.
func testTree() *cobra.Command {
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.SuggestionsMinimumDistance = 2
	root.AddCommand(&cobra.Command{
		Use: "doctor", Short: "Diagnose", Run: func(*cobra.Command, []string) {},
	})
	root.AddCommand(&cobra.Command{
		Use: "ls", Short: "List live sandboxes", Run: func(*cobra.Command, []string) {},
	})
	return root
}

// The refusal must carry THREE things, and this test exists because any one of
// them can be dropped without the others noticing: what den did not
// understand, what den does understand, and where the bare form went.
func TestUnknownCommandErrorNamesTheArgumentAndTheCommands(t *testing.T) {
	err := unknownCommandError(testTree(), "api")
	if err == nil {
		t.Fatal("an unknown command must produce an error")
	}
	got := err.Error()

	if !strings.Contains(got, `unknown command "api"`) {
		t.Errorf("the refusal must quote what den did not understand; got:\n%s", got)
	}
	// The Short matters as much as the name: a bare list of names is a
	// vocabulary, not a contract.
	if !strings.Contains(got, "  ls          List live sandboxes") {
		t.Errorf("every command must come with its Short, cobra-padded; got:\n%s", got)
	}
	if !strings.Contains(got, "`den spawn <nest>`") {
		t.Errorf("the refusal must carry the migration line; got:\n%s", got)
	}
	// No `den: ` prefix: cmd/den/main.go already prints one, and a second
	// would read as a doubled program name.
	if strings.HasPrefix(got, "den:") {
		t.Errorf("the error value must not prefix itself with `den: `; got:\n%s", got)
	}
}

// The suggestion is what `withSuggestion` used to graft onto a nest-resolution
// failure. It moves here, where it answers the question actually asked —
// "which command did you mean" — instead of "this nest does not exist, and by
// the way".
func TestUnknownCommandErrorSuggestsACloseCommand(t *testing.T) {
	got := unknownCommandError(testTree(), "doctr").Error()

	if !strings.Contains(got, "`den doctor`") {
		t.Errorf("a one-letter typo must suggest the command; got:\n%s", got)
	}
}

// A far name suggests nothing: a suggestion offered at random teaches the
// reader to skip the line that matters.
func TestUnknownCommandErrorSuggestsNothingForAFarName(t *testing.T) {
	got := unknownCommandError(testTree(), "zzzz").Error()

	if strings.Contains(got, "did you mean") {
		t.Errorf("no suggestion must be made for a far name; got:\n%s", got)
	}
	// The list is still there: a far name is exactly when the reader needs it.
	if !strings.Contains(got, "Commands:") {
		t.Errorf("the command list must come even without a suggestion; got:\n%s", got)
	}
}

// SuggestionsMinimumDistance at 0 makes SuggestionsFor return prefix matches
// ONLY. This test is what keeps Task 2's move of that assignment honest: drop
// it while moving configureSpawn's body and `den doctr` silently stops
// suggesting anything.
func TestUnknownCommandErrorNeedsTheSuggestionDistance(t *testing.T) {
	root := testTree()
	root.SuggestionsMinimumDistance = 0

	if got := unknownCommandError(root, "doctr").Error(); strings.Contains(got, "den doctor") {
		t.Errorf("at distance 0 cobra suggests nothing: this test pins the field, not the wording; got:\n%s", got)
	}
}

// Zero argument is the ONLY case that must pass: it is `den` alone, which
// prints the help.
func TestUnknownCommandAcceptsNoArgument(t *testing.T) {
	if err := unknownCommand(testTree(), nil); err != nil {
		t.Errorf("`den` alone must be accepted, the RunE prints the help; got: %v", err)
	}
	if err := unknownCommand(testTree(), []string{"api"}); err == nil {
		t.Error("a first argument that is not a command must be refused")
	}
}
```

- [ ] **Step 2: Lancer les tests, vérifier qu'ils échouent**

```bash
go test ./internal/cli/ -run TestUnknownCommand -count=1
```

Attendu : échec de compilation, `undefined: unknownCommandError` / `undefined: unknownCommand`.

- [ ] **Step 3: Écrire l'implémentation**

Ajouter en fin de `internal/cli/root.go`, après `argsBetween` :

```go
// unknownCommand is the root's Args validator, and the root has one for a
// reason worth writing down: WITHOUT it, cobra's own legacyArgs answers
// `unknown command "api" for "den"` and stops there. It never lists the
// commands, because the list lives in the USAGE, and the root sets
// SilenceUsage — lifting that flag would dump the full usage under every
// subcommand's failure too (cobra checks the root's flag as well as the
// command's), which is precisely what it was set to prevent.
//
// So den writes the message itself. Two cobra constraints govern the shape,
// both read in cobra's source rather than assumed:
//
//  1. Find() only falls back on legacyArgs when the found command's Args is
//     nil. A non-nil Args on the root therefore REPLACES cobra's message with
//     this one — which is the point.
//  2. execute() returns flag.ErrHelp on !Runnable() BEFORE calling
//     ValidateArgs. A root without a RunE would print its help and exit 0 on
//     `den api`. The root keeps a RunE for that reason alone; it is reached
//     only when this validator let the call through, i.e. with no argument.
//
// A flag error still wins over this one: ParseFlags runs before ValidateArgs,
// so `den api --detach` says `unknown flag: --detach`. Accepted — both are
// non-zero refusals, and making the argument win would mean disabling flag
// parsing on the root, costing --den-home and --help.
func unknownCommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return unknownCommandError(cmd, args[0])
}

// unknownCommandError renders the refusal: what den did not understand, what
// it does understand, and where the bare form went.
//
// Split from unknownCommand so it can be tested against a throwaway tree
// instead of den's real command list — the shape of the message is not the
// same contract as its content, and the latter is frozen once, in a golden.
//
// The list comes from root.Commands(), NEVER from a constant: a command added
// tomorrow appears here without anyone thinking about it. cobra sorts that
// slice by name, so the order is deterministic and a golden can hold it.
//
// The migration line is STATIC. A kinder version would read the den home to
// say "api is a nest, type `den spawn api`" — rejected: it would put a
// fallible config.Home, hence a second class of error, on the most banal
// error path of the whole CLI. The fixed line carries the whole migration for
// nothing.
func unknownCommandError(root *cobra.Command, arg string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "unknown command %q", arg)
	if candidates := root.SuggestionsFor(arg); len(candidates) > 0 {
		quoted := make([]string, len(candidates))
		for i, c := range candidates {
			quoted[i] = fmt.Sprintf("`den %s`", c)
		}
		fmt.Fprintf(&b, "\n\ndid you mean %s?", strings.Join(quoted, " or "))
	}
	// The padding is cobra's own (minNamePadding = 11, widened by any longer
	// name), so this block is indistinguishable from what `den help` prints.
	// A reader must not have to notice they are looking at a second renderer.
	pad := 11
	for _, sub := range root.Commands() {
		if sub.IsAvailableCommand() && len(sub.Name()) > pad {
			pad = len(sub.Name())
		}
	}
	b.WriteString("\n\nCommands:")
	for _, sub := range root.Commands() {
		if !sub.IsAvailableCommand() {
			continue
		}
		fmt.Fprintf(&b, "\n  %-*s %s", pad, sub.Name(), sub.Short)
	}
	b.WriteString("\n\n`den <nest>` no longer spawns: use `den spawn <nest>`.")
	b.WriteString("\nRun `den help <command>` for details.")
	return errors.New(b.String())
}
```

Ajouter `"errors"` et `"strings"` aux imports de `root.go`.

- [ ] **Step 4: Lancer les tests, vérifier qu'ils passent**

```bash
go test ./internal/cli/ -run TestUnknownCommand -count=1
task check
```

Attendu : PASS, puis `task check` vert (rien n'est câblé, l'arbre est intact).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/root.go internal/cli/root_test.go
git commit -m "feat(cli): unknownCommandError — le refus qui liste les commandes

Pas encore câblé. cobra ne peut pas rendre ce message lui-même : legacyArgs
s'arrête à \`unknown command\`, la liste vit dans l'usage, et le root tait
l'usage — le lever le lèverait aussi sous chaque échec de sous-commande."
```

---

### Task 2: le spawn devient une sous-commande

**Files:**
- Modify: `internal/cli/spawn.go` (réécrit — `configureSpawn` et `withSuggestion` supprimées)
- Modify: `internal/cli/root.go:97-170` (`NewRootCmdWith`)
- Modify: `internal/cli/nest.go:76` et `:132-159` (`warnAboutShadowedNests` supprimée)
- Test: `internal/cli/spawn_test.go`, `internal/cli/root_test.go`, `internal/cli/hostile_test.go`, `internal/cli/root_deps_test.go`, `internal/cli/nest_test.go`

**Interfaces:**
- Consumes: `unknownCommand` (Task 1)
- Produces: `func newSpawnCmd(denHome *string, deps spawn.Deps) *cobra.Command` — la sous-commande, consommée par `NewRootCmdWith` et par les helpers de test `runSpawn` / `runSpawnWithInput`.

C'est la tâche de bascule : l'arbre est rouge entre le Step 2 et le Step 6, par construction — la forme nue ne peut pas coexister avec son remplacement.

- [ ] **Step 1: Écrire `newSpawnCmd` à la place de `configureSpawn`**

Remplacer **tout** `internal/cli/spawn.go` par :

```go
package cli

import (
	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/spawn"
	"github.com/spf13/cobra"
)

// newSpawnCmd builds `den spawn <nest> [repo...]`, an ordinary subcommand.
//
// It was not one until 2026-08-05: the root ITSELF carried the spawn's RunE,
// Args and six flags, so that `den api` spawned. That form is gone, and the
// spec 2026-08-05-spawn-command-design.md says why in full. The short version,
// because a reader will be tempted to bring it back for the two keystrokes it
// saved:
//
//   - the six flags lived on root.Flags(), so `den --help` showed them with no
//     owner, and `den --detach` alone fell through to cmd.Help() and swallowed
//     the flag in silence — the §2 "den refuses rather than normalizing in
//     silence" broken on den's most visible surface;
//   - every unknown first argument was a valid nest name by construction, so
//     no token could ever produce "this is not a command, here is what den
//     does". withSuggestion existed to apologize for that, from the wrong end;
//   - a nest named `ls` was unreachable for life. den knew and said so
//     (warnAboutShadowedNests), which is a warning, not a fix. `den spawn ls`
//     is the fix.
//
// deps is a PARAMETER rather than built here, like newDoctorCmd: that is what
// makes the flag-to-spawn.Options wiring checkable — an unwired flag is
// silent — without a test having to run the real `sbx`.
func newSpawnCmd(denHome *string, deps spawn.Deps) *cobra.Command {
	var o spawn.Options

	cmd := &cobra.Command{
		Use:   "spawn <nest> [repo...]",
		Short: "Spawn or attach a nest's sandbox",
		// atLeastOneArg, not an upper-bounded validator: the arguments past
		// the first are repos, and nothing caps how many a spawn may mount.
		Args: atLeastOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Nest = args[0]
			// Raw: nest.Resolve expands the tilde and absolutizes against the
			// working directory, which internal/spawn reads. Doing it here
			// would put path resolution on the cobra side of the boundary,
			// where no test of the cascade could reach it.
			o.Repos = args[1:]
			home, err := config.Home(*denHome)
			if err != nil {
				return err
			}
			// Local copy: Out, Err and In are decided here, at run time,
			// because they alone depend on the command (and hence on a test's
			// SetOut/SetErr/SetIn). The empty-agent warning goes to Err so it
			// never mixes into the stdout a caller might pipe; the `-i`
			// checklist reads In for the same reason. The terminal probe stays
			// in deps — it describes the machine, not the command.
			d := deps
			d.Out = cmd.OutOrStdout()
			d.Err = cmd.ErrOrStderr()
			d.In = cmd.InOrStdin()
			return spawn.Spawn(cmd.Context(), home, o, d)
		},
	}

	cmd.Flags().StringVarP(&o.Worktree, "worktree", "w", "", "worktree to propagate across all repos")
	cmd.Flags().StringVar(&o.Agent, "agent", "", "agent to use (default: defaults.agent)")
	cmd.Flags().StringSliceVar(&o.Without, "without", nil, "exclude these optional repos")
	cmd.Flags().StringSliceVar(&o.Only, "only", nil, "keep only these optional repos")
	cmd.Flags().BoolVar(&o.Detach, "detach", false, "do not attach a shell after the spawn")
	cmd.Flags().BoolVarP(&o.Interactive, "interactive", "i", false,
		"pick the nest's optional repos from a checklist (contradicts --only/--without)")
	return cmd
}
```

- [ ] **Step 2: Câbler le root**

Dans `internal/cli/root.go`, `NewRootCmdWith` — remplacer la déclaration du root par :

```go
	root := &cobra.Command{
		Use:           "den",
		Short:         "Simple, repeatable sbx sandboxes",
		SilenceUsage:  true,
		SilenceErrors: true,
		// See unknownCommand: a non-nil Args is what replaces cobra's bare
		// "unknown command" with den's own listing, and the RunE below is what
		// keeps ValidateArgs reachable at all.
		Args: unknownCommand,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	// Explicit, because cobra does NOT apply it on this path: its default of 2
	// is set in findSuggestions(), which serves the "unknown command" branch
	// den never takes (the root has a RunE). unknownCommandError calls
	// SuggestionsFor directly, and at 0 it returns prefix matches only —
	// `den doctr` would suggest nothing.
	root.SuggestionsMinimumDistance = 2
```

Puis, à la place du bloc `configureSpawn(root, &denHome, spawn.Deps{...})` en fin de fonction, poser la sous-commande **avec les autres** — l'ordre n'a plus d'importance, le commentaire « LAST » disparaît :

```go
	// `den spawn` is ASSEMBLED here from the very fields newLsCmd just got:
	// deps.Sbx is the single source. Out/Err/In are left unset, newSpawnCmd's
	// RunE overwrites them on every run from the command itself (the only way
	// to follow a test's SetOut).
	root.AddCommand(newSpawnCmd(&denHome, spawn.Deps{
		Sbx:       deps.Sbx,
		Git:       deps.Git,
		Policy:    deps.Policy,
		Freshness: deps.Freshness,
		SSHAgent:  deps.SSHAgent,
		IsTTY:     deps.IsTTY,
		// The real OS, named at the wiring site like every other system
		// access: spawn has no SystemDeps constructor to hold it (see
		// spawn.Deps), and a field left implicit here is a dependency the
		// reader has to hunt for.
		GOOS: runtime.GOOS,
		// The real clock for the source-staleness hint (spawn.Deps.Now): nil
		// is what the package's own tests want (no source touched, no clock
		// owed), but a live den wiring this field to nothing would silently
		// drop the hint for every user, forever.
		Now: time.Now,
	}))
```

- [ ] **Step 3: Supprimer `warnAboutShadowedNests`**

Dans `internal/cli/nest.go` : supprimer la fonction (l. 132-159) et son appel (l. 76, avec le commentaire qui l'accompagne). L'avertissement conseillait « renomme ce nest, tu ne pourras jamais le spawner » — `den spawn ls` le spawne, le conseil est devenu faux.

- [ ] **Step 4: Transposer les helpers de test**

Dans `internal/cli/spawn_test.go`, `runSpawn` et `runSpawnWithInput` construisaient un root nu puis appelaient `configureSpawn`. Ils enregistrent maintenant la sous-commande et préfixent `spawn` :

```go
// runSpawn runs the spawn command on a given den home, with injected access.
// Same reason as runDoctor: without injection, the flag-to-spawn.Options
// wiring is unverifiable anywhere, and any test reaching `sbx create` would
// try to run the real binary.
//
// The tree is BARE — the spawn and nothing else. Tests that need den's real
// command list (the refusal, the suggestion) go through runFullRoot instead.
func runSpawn(t *testing.T, home string, deps spawn.Deps, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newSpawnCmd(&home, deps))
	return executeCmd(t, root, append([]string{"spawn"}, args...)...)
}
```

et, identiquement, `runSpawnWithInput` :

```go
func runSpawnWithInput(t *testing.T, home string, deps spawn.Deps, input string, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "den", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newSpawnCmd(&home, deps))
	root.SetIn(strings.NewReader(input))
	return executeCmd(t, root, append([]string{"spawn"}, args...)...)
}
```

`runFullRoot` ne change **pas** : ce sont ses appelants qui passent `"spawn"` en tête.

- [ ] **Step 5: Transposer et supprimer les tests**

**Supprimer** de `internal/cli/spawn_test.go` :

- `TestSubcommandsStayPriority` — il n'y a plus de compétition entre un nom de nest et un nom de commande. `TestVersionPrintsTheVersion` couvre déjà `den version`.
- les six tests de `withSuggestion` : `TestATypoOnASubcommandIsSuggested`, `TestAFarNameSuggestsNothing`, `TestANestThatExistsButIsUnreadableSuggestsNothing`, `TestTheSuggestionOnlyConcernsTheTypedName`, `TestASourceReferenceNeverSuggestsASubcommand`, `TestATypoOnASubcommandIsStillSuggestedWithPositionals`. La propriété « une faute de frappe est suggérée » est reprise par la Task 1 et la Task 3.

Retirer aussi les imports devenus inutilisés (`io/fs`, `github.com/PillowPillow/den/internal/nest`).

**Supprimer** de `internal/cli/nest_test.go` : `TestNestLsWarnsAboutNestsShadowedByASubcommand` (l. 173) et le test voisin qui exige un stderr vide sans collision (l. 210).

**Préfixer `spawn`** aux appels suivants :

| Fichier:ligne | Avant | Après |
|---|---|---|
| `spawn_test.go:56` | `run(t, "api", "--den-home", dir)` | `run(t, "spawn", "api", "--den-home", dir)` |
| `spawn_test.go:70` | `run(t, "api")` | `run(t, "spawn", "api")` |
| `hostile_test.go:67` | `runFullRoot(t, home, "api", "-w", "+wip")` | `runFullRoot(t, home, "spawn", "api", "-w", "+wip")` |
| `hostile_test.go:104` | `runFullRoot(t, home, "api")` | `runFullRoot(t, home, "spawn", "api")` |
| `hostile_test.go:150` | `runFullRoot(t, home, "api", "-w", "feat1")` | `runFullRoot(t, home, "spawn", "api", "-w", "feat1")` |
| `hostile_test.go:225` | `runFullRoot(t, home, "api")` | `runFullRoot(t, home, "spawn", "api")` |
| `hostile_test.go:248` | `runFullRoot(t, home, "api")` | `runFullRoot(t, home, "spawn", "api")` |
| `root_deps_test.go:164` | `executeCmd(t, NewRootCmdWith(deps), "--den-home", home, "api", "--detach")` | `… "--den-home", home, "spawn", "api", "--detach"` |

Les appels à `runSpawn` / `runSpawnWithInput` ne changent **pas** : le helper préfixe pour eux.

**Renommer** dans `spawn_test.go` : `TestDenNestRoutesToTheSpawn` → `TestSpawnRoutesToTheSpawn`, `TestDenNestWithoutFlagGoesThroughDenHome` → `TestSpawnWithoutFlagGoesThroughDenHome` (leurs corps ne changent que par le `"spawn"` ajouté).

- [ ] **Step 6: Lancer les tests**

```bash
task check
```

Attendu : vert. `TestUnknownFirstArgumentIsANestNotFound` (root_test.go) et `TestANestHomonymOfASubcommandSpawnsNormally` (spawn_test.go) échouent **encore** à ce stade — c'est la Task 3 qui les remplace. Si l'on veut un commit vert ici, les traiter maintenant en suivant la Task 3 ; sinon, enchaîner sans commiter et ne commiter qu'à la fin de la Task 3.

Choix recommandé : **enchaîner**. Ces deux tests portent exactement le contrat qui s'inverse ; les modifier deux fois serait du bruit.

- [ ] **Step 7: Vérifier à la main l'aide et le refus**

```bash
go run ./cmd/den
go run ./cmd/den api
go run ./cmd/den --detach
```

Attendu, dans l'ordre : une aide où `spawn` figure dans « Available Commands » et où la section « Flags » ne contient plus que `--den-home` et `-h` ; le refus complet avec la liste ; `unknown flag: --detach`.

---

### Task 3: le refus, gelé contre l'arbre réel

**Files:**
- Create: `internal/cli/testdata/unknown-command.golden`
- Modify: `internal/cli/root_test.go:194` (`TestUnknownFirstArgumentIsANestNotFound` remplacé), et la table de `TestWrongArgumentCountNamesTheUsageLine`
- Modify: `internal/cli/spawn_test.go` (`TestANestHomonymOfASubcommandSpawnsNormally`)

**Interfaces:**
- Consumes: `unknownCommand` câblé (Task 2), `runFullRoot` (spawn_test.go)
- Produces: rien

- [ ] **Step 1: Écrire le golden**

Créer `internal/cli/testdata/unknown-command.golden` avec **exactement** ce contenu (deux espaces d'indentation, noms complétés à 11 colonnes, une newline finale) :

```
unknown command "api"

Commands:
  build       Build stack images, in dependency order
  completion  Generate the autocompletion script for the specified shell
  doctor      Diagnose den's configuration and environment
  help        Help about any command
  init        Create a den home from the shipped example
  lint        Validate a source checkout (stacks, nests, references, confinement)
  ls          List live sandboxes
  nest        Inspect the declared nests
  ports       Show where a sandbox's declared ports land on the host
  rm          Destroy a sandbox (the agent profile persists)
  sh          Open a shell in an existing sandbox
  source      Manage team source repositories (stacks/nests shared over git)
  spawn       Spawn or attach a nest's sandbox
  version     Print den's version

`den <nest>` no longer spawns: use `den spawn <nest>`.
Run `den help <command>` for details.
```

`help` et `completion` en font partie : cobra les enregistre dans `ExecuteC` **avant** `Find`, donc avant le validateur. Ce sont de vraies commandes, elles appartiennent au contrat.

- [ ] **Step 2: Remplacer `TestUnknownFirstArgumentIsANestNotFound`**

Dans `internal/cli/root_test.go`, remplacer le test (l. ~186-216, commentaire compris) par :

```go
// The whole refusal is a contract, so it is frozen whole.
//
// This test replaces TestUnknownFirstArgumentIsANestNotFound, which asserted
// the exact opposite — that `den doesnotexist` reported a missing nest FILE.
// That was the price of "the root IS the spawn command" (spec §11), and the
// spec of 2026-08-05 stopped paying it.
//
// A golden rather than a handful of Contains: the point is that den answers
// with EVERYTHING it can do. A test that checked three lines would go green on
// a listing that had silently lost the other eleven.
func TestUnknownFirstArgumentListsTheCommands(t *testing.T) {
	// DEN_HOME is pinned even though this path never reads it: if the refusal
	// ever regressed into a spawn, the test would otherwise run against the
	// developer's real ~/.den — and the real `sbx`.
	t.Setenv("DEN_HOME", t.TempDir())

	_, err := run(t, "api")
	if err == nil {
		t.Fatal("an unknown first argument must be refused: `den <nest>` no longer spawns")
	}

	path := filepath.Join("testdata", "unknown-command.golden")
	want, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading %s: %v", path, readErr)
	}
	if got := err.Error() + "\n"; got != string(want) {
		t.Errorf("the refusal is a contract.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// The suggestion, against den's REAL command list: Task 1 pins its shape on a
// throwaway tree, this pins that the root actually carries the distance.
func TestUnknownFirstArgumentSuggestsTheCloseCommand(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	_, err := run(t, "doctr")
	if err == nil {
		t.Fatal("`den doctr` must be refused")
	}
	if !strings.Contains(err.Error(), "`den doctor`") {
		t.Errorf("error = %q, expected it to suggest `den doctor`", err)
	}
}

// The flag that used to vanish. Before 2026-08-05 the six spawn flags lived on
// root.Flags(), so `den --detach` parsed cleanly, fell through to cmd.Help()
// and exited 0 — the flag silently discarded, which §2 forbids. Now the root
// does not know it.
//
// Without this test, a future PersistentFlags on the root reopens the hole
// without a sound.
func TestASpawnFlagOnTheRootIsRefused(t *testing.T) {
	t.Setenv("DEN_HOME", t.TempDir())

	for _, flag := range []string{"--detach", "-w", "--only"} {
		t.Run(flag, func(t *testing.T) {
			if _, err := run(t, flag); err == nil {
				t.Errorf("`den %s` must be refused: that flag belongs to `den spawn`", flag)
			}
		})
	}
}
```

Vérifier que `os`, `filepath` et `strings` sont bien importés dans `root_test.go` (ils le sont déjà).

- [ ] **Step 3: Ajouter le cas `spawn` à la table d'arité**

Dans `TestWrongArgumentCountNamesTheUsageLine` (`root_test.go`), remplacer le commentaire qui explique que le root n'est plus un site d'`argsBetween` par :

```go
// The root itself is not one of these sites: its Args is unknownCommand, which
// refuses on identity, not on count. `den spawn` is the site that exercises
// the "one argument expected" wording with an unbounded maximum — every
// argument past the first is a repo, and nothing caps how many a spawn may
// mount, so its "too many" branch is unreachable by design.
```

et ajouter en tête de la table :

```go
		{
			"spawn, missing argument",
			[]string{"spawn"},
			"den spawn: one argument expected, none received — usage: den spawn <nest> [repo...] [flags]",
		},
```

- [ ] **Step 4: Renforcer le test de nest homonyme**

Dans `internal/cli/spawn_test.go`, remplacer `TestANestHomonymOfASubcommandSpawnsNormally` par :

```go
// A nest that carries a SUBCOMMAND'S OWN NAME spawns. Not a lookalike — the
// name itself.
//
// Until 2026-08-05 this was impossible: cobra routed `den ls` to the
// subcommand before the argument reached the root's RunE, so a nest named `ls`
// was unreachable for life. den knew, and warned about it in `den nest ls`
// (warnAboutShadowedNests, deleted with this change) — a warning is not a fix.
// Making the spawn a subcommand removes the collision instead of commenting
// on it, and this test is what says so.
func TestANestHomonymOfASubcommandSpawnsNormally(t *testing.T) {
	home := denHomeSpawnable(t)
	if err := os.WriteFile(filepath.Join(home, "nests", "ls.yaml"),
		[]byte("stack: devx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := runFullRoot(t, home, "spawn", "ls")
	if err != nil {
		t.Fatalf("a nest named \"ls\" must spawn; got: %v", err)
	}
	// Without this assertion, a spawn that did nothing at all would pass: it
	// must be sandbox "ls" that actually spawns.
	var created bool
	for _, call := range f.Calls {
		joined := strings.Join(call, " ")
		if strings.HasPrefix(joined, "create ") && strings.Contains(joined, "ls") {
			created = true
		}
	}
	if !created {
		t.Errorf("no `create` for sandbox \"ls\"; calls: %v", f.Calls)
	}
	if len(f.Attaches) != 1 {
		t.Errorf("the spawn must attach; attaches: %v", f.Attaches)
	}
}
```

- [ ] **Step 5: Lancer la suite**

```bash
task check
```

Attendu : vert. Si le golden diverge, **lire le diff avant de le recopier** : une Short modifiée est une vraie régression documentaire, pas un golden à rafraîchir.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/ 
git commit -m "feat(cli)!: \`den spawn <nest>\` — la forme nue \`den <nest>\` disparaît

Le root portait le RunE, les Args et six flags du spawn. Trois défauts en
découlaient : \`den --detach\` seul tombait sur cmd.Help() et avalait le flag ;
aucun premier argument ne pouvait produire la liste des commandes, puisque tout
token inconnu était un nom de nest valide ; un nest nommé \`ls\` était
injoignable à vie, et den ne savait qu'en avertir.

withSuggestion et warnAboutShadowedNests partent avec la collision qu'elles
commentaient. Le refus est gelé dans un golden : il liste tout ce que den sait
faire, et dit où la forme nue est passée.

BREAKING CHANGE: \`den <nest>\` ne spawne plus, utiliser \`den spawn <nest>\`."
```

---

### Task 4: la documentation et le seul message qui nommait la forme nue

**Files:**
- Modify: `internal/cli/source.go:40`
- Modify: `README.md`, `CLAUDE.md`, `CHANGELOG.md`
- Modify: `docs/superpowers/specs/2026-07-27-den-cli-design.md` §5, §6, §11
- Modify: commentaires dans `internal/sbx/ls.go`, `internal/sbx/runner.go`, `internal/sbx/template.go`, `internal/config/stack.go`, `internal/config/validate.go`, `internal/config/name.go`, `internal/worktree/worktree.go`, `internal/agent/gate.go`, `internal/cli/nest.go`

**Interfaces:**
- Consumes: le comportement livré par les Tasks 2 et 3
- Produces: rien

- [ ] **Step 1: Corriger le seul message de production**

`internal/cli/source.go:40` imprime après un `den source add` réussi :

```go
			"source %q installed — its objects are addressed %s:<name> (e.g. `den %s:<nest>`)\n",
```

→

```go
			"source %q installed — its objects are addressed %s:<name> (e.g. `den spawn %s:<nest>`)\n",
```

- [ ] **Step 2: Recenser les occurrences restantes**

```bash
grep -rn 'den <nest>\|den <name>' README.md CLAUDE.md internal/ docs/superpowers/specs/2026-07-27-den-cli-design.md
grep -nE 'den (api|web|scratch|corp:)' README.md docs/superpowers/specs/2026-07-27-den-cli-design.md
```

Toutes ces occurrences nomment un geste utilisateur qui change de nom. Aucune autre que celle du Step 1 n'est une sortie de programme : le reste est commentaire ou documentation. Les traiter toutes.

- [ ] **Step 3: README**

- l. 81 — la ligne du tableau : `` `den <nest> [repo...]` `` → `` `den spawn <nest> [repo...]` ``
- l. 97 — « Options of `den <nest>`: » → « Options of `den spawn`: »
- l. 110 — `` `den api -w feature/123` `` → `` `den spawn api -w feature/123` ``
- l. 125-128 — le bloc d'exemples des repos à la volée : préfixer `spawn` sur les quatre lignes
- l. 177, 295, 361, 372 — `` `den <nest>` `` → `` `den spawn` ``
- l. 390, 392, 440 — la sortie de `den source add` et les exemples `den corp:backend` → `den spawn corp:backend`

Vérifier au passage le quickstart en tête de fichier, si son bloc de commandes montre la forme nue.

- [ ] **Step 4: CLAUDE.md**

Section « What this is » : `` `den <nest> [repo...]` `` → `` `den spawn <nest> [repo...]` ``. Et, dans « Architecture », la phrase sur `deps.Sbx` partagé par `ls`, `sh`, `ports` et spawn reste vraie — ne pas y toucher.

Ajouter une ligne à « Stale artifacts — don't trust these » :

```markdown
- Les plans et handoffs datés sous `docs/superpowers/` disent `den <nest>` pour spawner. C'était
  vrai à leur date : la forme nue a été remplacée par `den spawn <nest>` le 2026-08-05 (spec
  `2026-08-05-spawn-command-design.md`). Traduire en lisant, comme pour `make` → `task`.
```

- [ ] **Step 5: Spec 2026-07-27**

- §5, tableau : la ligne `den <nest> [repo...]` devient `den spawn <nest> [repo...]`
- §6, titre : « Data flow du spawn — `den spawn <nest> [-w <wt>] …` », et les mentions dans le corps
- §11, tableau : les deux lignes qui nomment `den <nest>` (spawn-or-attach ; sandbox arrêtée sous `--detach`), et le paragraphe « Une décision, deux situations » qui cite `den <nest>`
- l. 218-220 : les exemples `den scratch ~/dev/a`

Ne **pas** réécrire l'analyse §11 de l'identité par le nom : elle porte sur les noms de sandbox, que ce chantier ne touche pas.

- [ ] **Step 6: CHANGELOG**

Ajouter l'entrée v1.3.0 en tête, dans le format des entrées existantes. Elle doit porter la rupture : c'est la seule trace qu'un lecteur en aura, puisqu'elle sort en mineure.

```markdown
### Changed

- **BREAKING — `den <nest>` ne spawne plus : `den spawn <nest>`.** Le spawn est une sous-commande
  comme les autres. Ses six flags (`-w`, `--agent`, `--without`, `--only`, `--detach`, `-i`) ont
  quitté le root, où `den --detach` seul les avalait en silence. Un premier argument inconnu liste
  désormais tout ce que den sait faire, et un nest homonyme d'une sous-commande — `ls`, `rm`, `sh` —
  redevient spawnable : `den spawn ls`.
```

Ne **pas** réécrire la ligne v1.2.0 « Repos on the fly : `den <nest> [repo...]` » : les entrées passées sont historiques.

- [ ] **Step 7: Commentaires de code**

Mettre à jour les `den <nest>` des commentaires listés en tête de tâche. Ils décrivent un geste utilisateur, pas une API : la substitution est mécanique (`den <nest>` → `den spawn`), sauf là où la phrase oppose deux commandes (`internal/sbx/ls.go:124`, « shared by `den <nest>` (spawn-or-attach) and `den sh` »), où il faut relire la phrase entière.

- [ ] **Step 8: Vérifier**

```bash
task check
grep -rn 'den <nest>' README.md CLAUDE.md internal/ cmd/
```

Attendu : suite verte, et le `grep` ne renvoie plus que les fichiers **datés** de `docs/superpowers/` (plans, handoffs), qui ne se réécrivent pas.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "docs: \`den spawn\` partout, et le message de \`source add\` avec

internal/cli/source.go était la seule sortie de production à nommer la forme
nue (\"e.g. \`den corp:<nest>\`\"). Le reste est README, spec §5/§6/§11,
CLAUDE.md, CHANGELOG et commentaires. Les plans et handoffs datés ne se
réécrivent pas — CLAUDE.md dit maintenant comment les lire."
```

---

## Self-Review

**Couverture de la spec.** §2 (le nom, la suppression de la forme nue) → Task 2. §3 (surface, flags déplacés, `root.Use`, `atLeastOneArg`) → Tasks 2 et 3 Step 3. §4 (le refus, ses deux contraintes cobra, le flag qui gagne, `SuggestionsMinimumDistance`) → Tasks 1 et 3. §5 (nest homonyme, `warnAboutShadowedNests`, contrainte d'ordre) → Tasks 2 Step 3 et 3 Step 4. §6 (architecture, `newSpawnCmd`, `withSuggestion`) → Task 2. §7 (tests) → Tasks 2 Step 5 et 3. §8 (docs, message de production, CHANGELOG) → Task 4. §9 (hors périmètre) → rien à faire, issue #50.

**Cohérence des noms.** `unknownCommand` / `unknownCommandError` / `newSpawnCmd` / `runSpawn` / `runSpawnWithInput` / `runFullRoot` sont employés à l'identique d'une tâche à l'autre. `atLeastOneArg` et `executeCmd` préexistent et ne changent pas de signature.

**Point de fragilité, nommé plutôt que caché.** Le golden de la Task 3 contient les `Short` de quatorze commandes, dont deux (`help`, `completion`) écrites par cobra. Une montée de version de cobra qui reformulerait l'une des deux fait rougir ce test. C'est voulu : la liste EST le contrat, et une commande dont la description change sans que personne le voie est exactement ce que le golden existe pour attraper.
