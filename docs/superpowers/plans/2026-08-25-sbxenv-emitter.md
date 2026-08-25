# Plan d'implémentation — den compile vers `.sbxenv.yaml`

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal :** den cesse d'assembler un argv `sbx create` et devient un émetteur de `.sbxenv.yaml`
consommé par `sbx env create` / `sbx env rm`.

**Architecture :** un émetteur pur (`internal/sbx/env.go`) remplace `internal/sbx/argv.go` un pour
un ; l'enregistrement de création passe de `state/sandboxes/<sandbox>.yaml` à un **répertoire**
`state/sandboxes/<sandbox>/` portant deux fichiers (`manifest.yaml` pour den, `.sbxenv.yaml` pour
sbx) ; `den rm` lit le second et le passe à `sbx env rm`, refuse quand il est illisible, et
`--force` bascule sur `sbx rm --force` par le nom.

**Tech Stack :** Go 1.x, `gopkg.in/yaml.v3` (déjà dépendance), `task check` (lint » typecheck »
test, fail-fast), goldens comparés à la main (**il n'existe aucun flag `-update`**).

**Spec :** `docs/superpowers/specs/2026-08-24-sbx-env-positioning-design.md` (#89), qui étend
`docs/superpowers/specs/2026-07-27-den-cli-design.md` (§6, §14.4, §14.5).

**Porte de relecture :** le spec se conditionne trois fois (ligne 6, ligne 13, §9) à une relecture
humaine avant tout plan. **Cette relecture a eu lieu le 2026-08-25** — l'humain a demandé le plan.
Ce plan n'a pas sauté sa propre précondition.

## Global Constraints

- **Langue.** Code, commentaires et messages utilisateur en **anglais**. Ce plan et les specs sont
  en français. Le style dominant est le long commentaire « pourquoi » au site de décision : ce qui
  a été rejeté, et quelle régression le choix empêche. Du code terse détonne visiblement.
- **`schemaVersion` est la chaîne `"1"`**, jamais l'entier `1` (§14.4 : `unsupported schemaVersion
  "2" (supported: 1)`), et den l'écrit en dur. Toute autre valeur est un refus (décision 5).
- **`agent:` est obligatoire** et vaut `sbx.PositionalAgent` (= `"shell"`), pour la raison que
  `argv.go` documente déjà : le flavor de l'image décide, den attache par `sbx exec`.
- **Aucune clé hors du tableau mesuré au §14.4.** L'émetteur n'écrit que : `schemaVersion`,
  `agent`, `name`, `workspace`, `additionalWorkspaces`, `kits`, `sandboxOptions`.
- **Jamais émis** : `ports:` (décision 9), `secrets:` / `registries:` / `bindings:` (décision 10),
  `sandboxOptions.profile` (décision 13), `env:` (le mixin le porte), et **aucun `${VAR}`** — den
  résout tout avant d'émettre (§5.5 point 2).
- **Chemins absolus uniquement** (§14.4 : un chemin relatif ne résout ni contre le répertoire du
  fichier, ni contre le cwd).
- **Le mixin est le DERNIER kit**, structurellement : deux paramètres distincts, jamais une liste
  unique ordonnée par convention (§5.5 point 3).
- **`sbx env run` n'est JAMAIS appelé** (décision 4) : il attache, et l'attache depuis un terminal
  est la branche qui ouvre l'assistant `sbx setup` (§14.2). den attache par `sbx exec -it`.
- **Rien ne casse en chemin.** `internal/sbx/argv.go`, ses tests et
  `testdata/create-resources.golden` restent en place **et verts** jusqu'à la tâche de bascule
  (décision 13). `task check` passe à chaque commit.
- **Hermétisme des tests** : aucun `t.Parallel()`, aucune socket, aucun process. `sbx` n'est jamais
  appelé depuis un test — `sbx.Fake` (fichier de production, `internal/sbx/fake.go`) est le double.
- **Doctrine d'ordonnancement (§6 spec mère)** : tout ce qui est refusable depuis la seule
  configuration est refusé **avant le premier effet de bord**.

**Non-objectif explicite, nommé pour qu'un relecteur ne le lise pas comme un chemin fantôme :**
`internal/build/sandbox.go:48` (`build.CreateArgv`) continue de piloter `sbx create`. Le §5.7
interdit un second chemin **autour du moteur de spawn et de teardown** ; une sandbox de build est un
autre objet, avec son propre cycle (`create` → N × `exec` → `stop` → `template save` → `rm`), et le
§5.4 garde `internal/build` explicitement. Ce plan n'y touche pas.

---

## Structure des fichiers

| Fichier | Responsabilité | Tâche |
|---|---|---|
| `internal/manifest/manifest.go` | ajoute la disposition en répertoire, garde la lecture legacy | 2 |
| `internal/manifest/manifest_test.go` | les deux dispositions, la migration, `List` qui descend | 2 |
| `internal/cli/ls.go`, `internal/cli/rm.go` | récupération du nom de sandbox depuis un chemin de record | 2 |
| `internal/sbx/env.go` | **neuf** — l'émetteur pur | 3 |
| `internal/sbx/env_test.go`, `env_resources_test.go` | goldens, sous-ensemble de clés, garde-fous | 3 |
| `internal/sbx/testdata/env-*.golden` | **neufs** — 3 goldens | 3 |
| `internal/spawn/spawn.go` (branche création, ~l.1360-1405) | émet puis `sbx env create` | 4 |
| `internal/sbx/argv.go` + 2 fichiers de test + `create-resources.golden` | **supprimés** | 4 |
| `internal/cli/rm.go` | chemin normal `sbx env rm`, refus, `--force` double sens | 5, 6 |
| `internal/lint/lint.go` | nom de sandbox légal + kits résolvables | 7 |

---

## Tâche 1 : la sonde `:ro` — arête bloquante, à mener AVANT d'écrire l'émetteur

**Pourquoi elle existe.** Le spec ne la nomme pas, et elle décide la forme de l'émetteur.
`spawn.mountWorkspace` (`internal/spawn/spawn.go:1989`) produit `host + ":ro"` pour un
`mounts: [{host: X, ro: true}]`, et `sbx create` accepte ce suffixe dans un positionnel. Mais le
§14.4 mesure `WorkspaceMount` comme portant **`path` seul — ni `ro`, ni `target`, ni `clone`**. Si
`path: /x:ro` n'est pas interprété comme un montage lecture seule, un nest qui déclare `ro: true`
compile aujourd'hui vers un montage **écriture**, en silence — une régression de sécurité que
personne ne verrait.

Le §5.7 fixe la conduite : une limitation se **documente**, se **sonde**, ou remonte chez Docker.
Elle ne se contourne pas. Cette tâche fait la sonde et fige la conduite.

**Files:**
- Modify: `docs/superpowers/specs/2026-07-27-den-cli-design.md` (§14.4, **append-only** — ajouter
  une sous-partie « Sonde du 2026-08-25 — `:ro` dans un `path` de workspace », ne rien réécrire)

**Interfaces:**
- Consumes: rien.
- Produces: le verdict que la tâche 3 consomme — soit `:ro` passe dans `path`, soit l'émetteur
  refuse un mount `ro: true` en nommant la limitation.

- [ ] **Step 1: préparer un répertoire de sonde hors du repo**

Le scratchpad, jamais le repo monté (la sonde crée une VM ; rien ne doit atterrir côté hôte).

```bash
P=/private/tmp/claude-501/den-sbxenv-probe && mkdir -p "$P/ws-a" "$P/ws-b" && cd "$P"
```

- [ ] **Step 2: écrire le fichier de sonde**

```bash
cat > "$P/.sbxenv.yaml" <<EOF
schemaVersion: "1"
agent: shell
name: den-probe-ro
workspace:
  path: $P/ws-a
additionalWorkspaces:
  - path: $P/ws-b:ro
EOF
```

- [ ] **Step 3: créer, stdin fermé**

`< /dev/null` est obligatoire : un workspace inexistant déclenche une invite interactive (§14.4), et
en automatisation ça bloque. Fermer stdin transforme le blocage en `ERROR: user cancelled
operation`, qui est une réponse.

Run: `sbx env create "$P/.sbxenv.yaml" < /dev/null`

Trois issues possibles, et chacune décide :
1. **Refus de résolution de chemin, avant tout effet de bord** ⇒ `:ro` n'est pas accepté dans
   `path`. Verdict : NON.
2. **Création réussie** ⇒ passer au step 4, qui tranche entre « accepté et lecture seule » et
   « accepté et monté en écriture sous un nom qui contient `:ro` ».
3. Invite interactive puis `user cancelled` ⇒ sbx a lu `/…/ws-b:ro` comme un chemin littéral
   inexistant. Verdict : NON.

- [ ] **Step 4: si la sandbox existe, mesurer le montage réel**

```bash
sbx ls --json
sbx exec den-probe-ro sh -lc 'ls -1 /ws-b 2>&1; touch /ws-b/probe-write 2>&1; echo rc=$?'
```

Verdict OUI seulement si l'écriture est **refusée** (`Read-only file system`). Une écriture qui
passe est le pire des cas : accepté, et pas lecture seule.

- [ ] **Step 5: détruire la sandbox**

Aucune sonde ne laisse de résidu (c'est la discipline du §14.4 : trois sandboxes jetables, `sbx ls
--json` vide après coup).

```bash
sbx env rm -f "$P/.sbxenv.yaml" ; sbx ls --json ; rm -rf "$P"
```

- [ ] **Step 6: consigner la mesure au §14.4, en append-only**

Ajouter à la fin du §14.4, avant la ligne `---` qui ouvre le §14.5, une sous-partie portant : la
date (2026-08-25), le binaire (`/opt/homebrew/bin/sbx`, vérifier par `sbx version`), le fichier de
sonde verbatim, la sortie verbatim, et le verdict en une phrase. Ne réécrire **aucune** ligne
existante du §14.4 — la sous-section est append-only, comme le §14.5 le dit d'elle-même.

- [ ] **Step 7: commit**

```bash
git add docs/superpowers/specs/2026-07-27-den-cli-design.md
git commit -m "docs(spec): probe whether :ro survives in a .sbxenv.yaml workspace path"
```

---

## Tâche 2 : `state/sandboxes/<sandbox>/` — la disposition en répertoire, en expansion seule

**Pourquoi d'abord.** Le fichier émis et le manifeste git cohabitent (§5.6, décision 6), et
`state/` **n'est jamais purgé** : chaque sandbox déjà créée sur la machine porte l'ancien
`state/sandboxes/<sandbox>.yaml`. Cette tâche fait vivre les deux dispositions ensemble, sans
qu'aucune émission n'existe encore. C'est un `expand` ; il n'y aura pas de `contract`.

**Le demi-contrat qui ne se referme jamais, et il faut l'écrire dans le code :** den ne supprime
jamais un enregistrement qu'il n'a pas su lire — il peut appartenir à un den plus récent (§11 de la
spec mère, doctrine T13/T16). Le lecteur legacy est donc **permanent**, pas une phase de migration.
Sans ce commentaire, un lecteur futur le supprimera comme du code mort.

**Le défaut à fermer, vérifié dans le code :** `manifest.List`
(`internal/manifest/manifest.go:326`) saute `e.IsDir()`. Sans changement, chaque record de la
nouvelle disposition devient **invisible** — pas « broken », invisible — pour `den ls`, `den
doctor` et le `mountGuard` de `den rm`. Un `den rm` de sandbox A cesserait alors de voir que la
sandbox B monte le même worktree, et déplacerait le workspace d'une VM vivante à la corbeille.
C'est exactement la perte de données que `newMountGuard` existe pour empêcher.

**Files:**
- Modify: `internal/manifest/manifest.go` (`Path`, `Write`, `Read`, `Remove`, `List`, + 2 fonctions
  neuves)
- Modify: `internal/cli/ls.go:52` et `internal/cli/rm.go:487` (récupération du nom depuis le chemin)
- Test: `internal/manifest/manifest_test.go`

**Interfaces:**
- Consumes: rien.
- Produces:
  - `func SandboxDir(denHome, sandboxName string) (string, error)` → `<denHome>/state/sandboxes/<name>/`
  - `func Path(denHome, sandboxName string) (string, error)` → **change de valeur** :
    `<denHome>/state/sandboxes/<name>/manifest.yaml`
  - `func LegacyPath(denHome, sandboxName string) (string, error)` → l'ancien
    `<denHome>/state/sandboxes/<name>.yaml`
  - `func SbxEnvPath(denHome, sandboxName string) (string, error)` →
    `<denHome>/state/sandboxes/<name>/.sbxenv.yaml` (aucun écrivain avant la tâche 4)
  - `func SandboxOf(recordPath string) string` → le nom de sandbox d'un chemin de record, les deux
    dispositions

- [ ] **Step 1: écrire les tests qui échouent**

Dans `internal/manifest/manifest_test.go` :

```go
// The two layouts coexist for good: den never deletes a record it could not
// read (spec §11), so the legacy reader is permanent, not a migration phase.
func TestReadFindsBothLayouts(t *testing.T) {
	home := t.TempDir()

	// New layout, written by Write.
	if err := Write(home, Manifest{Sandbox: "api", Nest: "api"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := Read(home, "api"); err != nil {
		t.Errorf("reading back what Write wrote: %v", err)
	}

	// Legacy layout, hand-written where a pre-directory den left it.
	legacy, err := LegacyPath(home, "front")
	if err != nil {
		t.Fatalf("LegacyPath: %v", err)
	}
	if err := os.WriteFile(legacy, []byte("schema: 1\nsandbox: front\nnest: front\n"), 0o600); err != nil {
		t.Fatalf("writing the legacy record: %v", err)
	}
	m, err := Read(home, "front")
	if err != nil {
		t.Fatalf("reading a legacy record: %v", err)
	}
	if m.Sandbox != "front" {
		t.Errorf("Sandbox = %q, want front", m.Sandbox)
	}
}

// The new layout wins when both exist: Write only ever produces the new one, so
// a legacy file beside it is the OLDER truth.
func TestReadPrefersTheDirectoryLayout(t *testing.T) {
	home := t.TempDir()
	if err := Write(home, Manifest{Sandbox: "api", Nest: "current"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	legacy, err := LegacyPath(home, "api")
	if err != nil {
		t.Fatalf("LegacyPath: %v", err)
	}
	if err := os.WriteFile(legacy, []byte("schema: 1\nsandbox: api\nnest: stale\n"), 0o600); err != nil {
		t.Fatalf("writing the legacy record: %v", err)
	}
	m, err := Read(home, "api")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if m.Nest != "current" {
		t.Errorf("Nest = %q, want current — the directory layout must win", m.Nest)
	}
}

// The defect this task exists to close: List skipped directories, so a
// new-layout record was INVISIBLE to `den ls`, `den doctor` and rm's
// mountGuard — which would then move a live sibling's workspace to the trash.
func TestListDescendsIntoTheDirectoryLayout(t *testing.T) {
	home := t.TempDir()
	if err := Write(home, Manifest{Sandbox: "api", Nest: "api"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	legacy, err := LegacyPath(home, "front")
	if err != nil {
		t.Fatalf("LegacyPath: %v", err)
	}
	if err := os.WriteFile(legacy, []byte("schema: 1\nsandbox: front\nnest: front\n"), 0o600); err != nil {
		t.Fatalf("writing the legacy record: %v", err)
	}
	got, broken, err := List(home)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(broken) != 0 {
		t.Errorf("broken = %v, want none", broken)
	}
	var names []string
	for _, m := range got {
		names = append(names, m.Sandbox)
	}
	if !slices.Equal(names, []string{"api", "front"}) {
		t.Errorf("List = %v, want [api front]", names)
	}
}

// A directory holding an unreadable manifest is BROKEN, never skipped: the
// mountGuard reads broken records through LaxMounts, and a skipped one protects
// no worktree at all.
func TestListReportsABrokenDirectoryRecord(t *testing.T) {
	home := t.TempDir()
	dir, err := SandboxDir(home, "api")
	if err != nil {
		t.Fatalf("SandboxDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte("schema: 99\n"), 0o600); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	_, broken, err := List(home)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(broken) != 1 {
		t.Fatalf("broken = %v, want exactly one", broken)
	}
	if SandboxOf(broken[0].Path) != "api" {
		t.Errorf("SandboxOf(%q) = %q, want api", broken[0].Path, SandboxOf(broken[0].Path))
	}
}

// SandboxOf is what rm.go and ls.go use instead of trimming ".yaml" off a
// basename: under the directory layout the basename is "manifest.yaml", the
// same for every sandbox, and trimming it would name every record "manifest".
func TestSandboxOfBothLayouts(t *testing.T) {
	for path, want := range map[string]string{
		"/h/state/sandboxes/api.yaml":               "api",
		"/h/state/sandboxes/api.feat12.yaml":        "api.feat12",
		"/h/state/sandboxes/api/manifest.yaml":      "api",
		"/h/state/sandboxes/api.feat12/manifest.yaml": "api.feat12",
		"/h/state/sandboxes/.yaml":                  "",
	} {
		if got := SandboxOf(path); got != want {
			t.Errorf("SandboxOf(%q) = %q, want %q", path, got, want)
		}
	}
}

// Remove takes the manifest away and the directory WITH it — but only when the
// directory is empty. A .sbxenv.yaml den could not read stays, and keeps the
// directory alive with it (spec §11: den never deletes what it could not read).
func TestRemoveKeepsADirectoryHoldingAnUnreadableFile(t *testing.T) {
	home := t.TempDir()
	if err := Write(home, Manifest{Sandbox: "api", Nest: "api"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	dir, err := SandboxDir(home, "api")
	if err != nil {
		t.Fatalf("SandboxDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".sbxenv.yaml"), []byte("schemaVersion: \"9\"\n"), 0o600); err != nil {
		t.Fatalf("writing the env record: %v", err)
	}
	if err := Remove(home, "api"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.yaml")); !os.IsNotExist(err) {
		t.Errorf("the manifest survived Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".sbxenv.yaml")); err != nil {
		t.Errorf("den deleted a file it could not read: %v", err)
	}
}
```

Ajouter à l'import du fichier de test : `"path/filepath"`, `"slices"` si absents.

- [ ] **Step 2: lancer, vérifier que ça échoue**

Run: `go test ./internal/manifest/ -count=1`
Expected: FAIL — `undefined: SandboxDir`, `undefined: LegacyPath`, `undefined: SbxEnvPath`,
`undefined: SandboxOf`.

- [ ] **Step 3: implémenter dans `internal/manifest/manifest.go`**

Remplacer le bloc `Dir`/`Path` par :

```go
// SandboxDir is where BOTH records of one sandbox live: the manifest den
// replays, and the .sbxenv.yaml sbx consumes (spec 2026-08-24 §5.6). A
// directory rather than two sibling files because `den rm` removes them as one
// unit, and a directory is what makes "removed both, or neither" expressible.
//
// Name validation happens here for the reason Path's own doc gives:
// sbx.SplitName is total and validates nothing, and filepath.Join CLEANS a
// ".." into a real traversal instead of rejecting it.
func SandboxDir(denHome, sandboxName string) (string, error) {
	if err := sbx.ValidateSandboxName(sandboxName); err != nil {
		return "", err
	}
	return filepath.Join(Dir(denHome), sandboxName), nil
}

// Path is where the manifest lives TODAY. Write only ever produces this one.
func Path(denHome, sandboxName string) (string, error) {
	dir, err := SandboxDir(denHome, sandboxName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "manifest.yaml"), nil
}

// LegacyPath is where a den older than the directory layout left the manifest,
// and reading it is PERMANENT — not a migration phase anyone gets to delete
// later. state/ is never purged, so every sandbox created before this change
// still has its record here; and den never deletes a record it could not read
// (spec §11), so den never converts one either. A den that stopped reading this
// path would silently lose the mount table of every older sandbox — which is
// how `den rm` starts guessing, and guessing wrong moves a live VM's workspace
// to the trash (doctrine T13/T16).
func LegacyPath(denHome, sandboxName string) (string, error) {
	if err := sbx.ValidateSandboxName(sandboxName); err != nil {
		return "", err
	}
	return filepath.Join(Dir(denHome), sandboxName+".yaml"), nil
}

// SbxEnvPath is the .sbxenv.yaml den emits for this sandbox — sbx's half of the
// record, and a hard input of `den rm` (spec §5.8).
func SbxEnvPath(denHome, sandboxName string) (string, error) {
	dir, err := SandboxDir(denHome, sandboxName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".sbxenv.yaml"), nil
}

// SandboxOf recovers a sandbox name from the path of one of its records, in
// EITHER layout.
//
// It replaces `strings.TrimSuffix(filepath.Base(p), ".yaml")`, which ls.go and
// rm.go each did by hand: under the directory layout every basename is
// "manifest.yaml", so that trim would name every record "manifest" — one name
// for all of them, colliding in rm's mountGuard map and naming the wrong
// sandbox in `den ls`.
//
// An empty result means "this path names nobody" — a file called exactly
// ".yaml". Callers already treat that as an unknown sharer rather than a claim.
func SandboxOf(recordPath string) string {
	base := filepath.Base(recordPath)
	if base == "manifest.yaml" {
		return filepath.Base(filepath.Dir(recordPath))
	}
	return strings.TrimSuffix(base, ".yaml")
}
```

`Read` prend la disposition neuve d'abord, la legacy ensuite :

```go
func Read(denHome, sandboxName string) (Manifest, error) {
	path, err := Path(denHome, sandboxName)
	if err != nil {
		return Manifest{}, fmt.Errorf("reading manifest: %w", err)
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		// The legacy layout, and the ORDER is the decision: Write only ever
		// produces the directory layout, so a legacy file beside one is the
		// older truth. Falling back rather than merging keeps that unambiguous.
		legacy, lerr := LegacyPath(denHome, sandboxName)
		if lerr != nil {
			return Manifest{}, fmt.Errorf("reading manifest: %w", lerr)
		}
		path = legacy
		content, err = os.ReadFile(legacy)
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("reading manifest %s: %w", path, &config.FileError{Err: err})
	}
	m, err := decode(content)
	if err != nil {
		return Manifest{}, fmt.Errorf("reading manifest %s: %w", path, err)
	}
	return m, nil
}
```

`Remove` retire le manifeste puis tente le répertoire, dont l'échec est un succès :

```go
func Remove(denHome, sandboxName string) error {
	path, err := Path(denHome, sandboxName)
	if err != nil {
		return fmt.Errorf("removing manifest: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	legacy, err := LegacyPath(denHome, sandboxName)
	if err != nil {
		return fmt.Errorf("removing manifest: %w", err)
	}
	if err := os.Remove(legacy); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", legacy, err)
	}
	// os.Remove on a directory succeeds ONLY when it is empty, and that is the
	// whole mechanism: a .sbxenv.yaml den could not read is still sitting there,
	// the removal fails, and the file survives — den never deletes what it could
	// not read (spec §11). The error is deliberately DROPPED: `den rm` did
	// everything it was asked, and failing here would refuse a completed
	// removal (doctrine T13/T16).
	dir, err := SandboxDir(denHome, sandboxName)
	if err != nil {
		return fmt.Errorf("removing manifest: %w", err)
	}
	_ = os.Remove(dir)
	return nil
}
```

`List` descend d'un niveau :

```go
	for _, e := range entries {
		path := filepath.Join(Dir(denHome), e.Name())
		if e.IsDir() {
			// One level, never a walk: the layout is exactly
			// state/sandboxes/<sandbox>/manifest.yaml, and a walk would start
			// reporting whatever else a user dropped under state/.
			//
			// A directory with NO manifest is skipped, not broken: `den rm
			// --force` leaves exactly that shape behind when it keeps an
			// unreadable .sbxenv.yaml, and reporting it forever would train the
			// user to ignore the row (spec §2 refuses noise as much as silence).
			path = filepath.Join(path, "manifest.yaml")
			if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
				continue
			}
		} else if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		content, err := os.ReadFile(path)
		// … the rest of the loop body is UNCHANGED: read, decode, and append to
		// out or to broken.
	}
```

- [ ] **Step 4: lancer les tests du paquet**

Run: `go test ./internal/manifest/ -count=1`
Expected: PASS.

- [ ] **Step 5: brancher les deux consommateurs qui recomposaient le nom à la main**

`internal/cli/rm.go:487` — remplacer
`sandbox := strings.TrimSuffix(filepath.Base(b.Path), ".yaml")` par
`sandbox := manifest.SandboxOf(b.Path)`, et actualiser le commentaire au-dessus : ce n'est plus
`manifest.Path` qui compose « `<sandbox>.yaml` », c'est `manifest.SandboxOf` qui lit les deux
dispositions.

`internal/cli/ls.go:52` — même remplacement, même actualisation de commentaire.

- [ ] **Step 6: la suite complète**

Run: `task check`
Expected: PASS. Si `internal/cli` échoue sur un golden de `den ls` / `den doctor`, c'est un chemin
de record affiché : corriger le golden **à la main** (il n'y a pas de `-update`).

- [ ] **Step 7: commit**

```bash
git add internal/manifest internal/cli/ls.go internal/cli/rm.go
git commit -m "feat(manifest): the record becomes a directory, and the legacy file stays readable"
```

---

## Tâche 3 : l'émetteur, fonction pure, sans aucun appelant

**Files:**
- Create: `internal/sbx/env.go`
- Test: `internal/sbx/env_test.go`, `internal/sbx/env_resources_test.go`
- Create: `internal/sbx/testdata/env-minimal.golden`, `env-complete.golden`, `env-resources.golden`

**Interfaces:**
- Consumes: `ValidateSandboxName`, `ValidateCPUs`, `ValidateMemory`, `PositionalAgent`
  (`internal/sbx`, déjà là) ; le verdict de la tâche 1.
- Produces:
  - `const EnvSchemaVersion = "1"`
  - `type Env struct { Name, Image string; StackKits []string; MixinKit string; Workspaces []string; CPUs *int; Memory string }`
  - `func EnvFile(e Env) ([]byte, error)`

**Note de forme :** `Env` porte **exactement** les champs de `Create` aujourd'hui. C'est voulu : le
spec annonce un échange à somme nulle (§5.4), et un émetteur qui prendrait plus d'entrées que
l'argv qu'il remplace serait un élargissement de périmètre déguisé.

- [ ] **Step 1: écrire les tests qui échouent**

`internal/sbx/env_test.go` :

```go
package sbx

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func completeEnv() Env {
	return Env{
		Name:      "api.feat12",
		Image:     "docker.io/library/dgdevx:v1",
		StackKits: []string{"/den/kits/ssh-known-hosts", "/den/stacks/dgdevx/kit"},
		MixinKit:  "/den/cache/mixins/api.feat12",
		Workspaces: []string{
			"/den/worktrees/feat12/api",
			"/den/worktrees/feat12/front",
			"/home/me/.den/agents/claude",
			"/home/me/.ssh_sbx",
		},
	}
}

// The measured §14.4 key set, and NOTHING else may appear. This is a NEGATIVE
// test on purpose: it is what makes the seam claim of §5.5 point 4 real rather
// than aspirational — a future field added to the doc struct fails here before
// it ever reaches a real sbx.
func TestEnvFileWritesNoUnmeasuredKey(t *testing.T) {
	out, err := EnvFile(completeEnv())
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("the emitted file does not parse: %v", err)
	}
	allowed := map[string]bool{
		"schemaVersion": true, "agent": true, "name": true, "workspace": true,
		"additionalWorkspaces": true, "kits": true, "sandboxOptions": true,
	}
	for k := range doc {
		if !allowed[k] {
			t.Errorf("emitted key %q is outside the measured §14.4 set", k)
		}
	}
	// ports:, secrets:, registries: and bindings: are decisions 9 and 10 —
	// never emitted while their effect is unmeasured.
	for _, forbidden := range []string{"ports", "secrets", "registries", "bindings", "env", "mcp"} {
		if _, ok := doc[forbidden]; ok {
			t.Errorf("%q is emitted, and the spec forbids it", forbidden)
		}
	}
	opts, _ := doc["sandboxOptions"].(map[string]any)
	if _, ok := opts["profile"]; ok {
		t.Error("sandboxOptions.profile is emitted — decision 13 forbids it: den has nothing to point it at")
	}
}

// schemaVersion is a STRING, and the test is worth its line: written as the int
// 1 it round-trips through YAML as `schemaVersion: 1`, which sbx refuses with
// "unsupported schemaVersion" — a refusal that would only appear against a real
// binary.
func TestEnvFilePinsSchemaVersionAsAString(t *testing.T) {
	out, err := EnvFile(completeEnv())
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	if !strings.Contains(string(out), `schemaVersion: "1"`) {
		t.Errorf("schemaVersion is not the quoted string \"1\":\n%s", out)
	}
}

// The most expensive invariant of the design, unchanged from the argv it
// replaces: the mixin is fail-closed and sbx's dispatcher exits on the first
// failure, depriving LATER kits of their startup commands. Measured §14.4:
// kits: preserves declaration order.
func TestEnvFileMixinIsTheLastKit(t *testing.T) {
	out, err := EnvFile(completeEnv())
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	var doc struct {
		Kits []string `yaml:"kits"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"/den/kits/ssh-known-hosts", "/den/stacks/dgdevx/kit", "/den/cache/mixins/api.feat12"}
	if !slices.Equal(doc.Kits, want) {
		t.Errorf("kits = %v, want %v", doc.Kits, want)
	}
}

// The workspace is ALWAYS written, never left to the default — and the default
// is why: sbx resolves an omitted workspace to the directory of the environment
// file (§14.4), and den's file lives under <denHome>/state/sandboxes/<name>/.
// An omission would silently mount den's own state directory into the VM.
func TestEnvFileAlwaysWritesTheWorkspace(t *testing.T) {
	out, err := EnvFile(completeEnv())
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	var doc struct {
		Workspace struct {
			Path string `yaml:"path"`
		} `yaml:"workspace"`
		Additional []struct {
			Path string `yaml:"path"`
		} `yaml:"additionalWorkspaces"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The FIRST workspace is the repo: Sandbox.Workdir depends on it for the attach.
	if doc.Workspace.Path != "/den/worktrees/feat12/api" {
		t.Errorf("workspace.path = %q, want the first workspace", doc.Workspace.Path)
	}
	if len(doc.Additional) != 3 {
		t.Fatalf("additionalWorkspaces = %v, want the other three", doc.Additional)
	}
	if doc.Additional[0].Path != "/den/worktrees/feat12/front" {
		t.Errorf("additionalWorkspaces[0] = %q, want the second workspace", doc.Additional[0].Path)
	}
}

// No ${VAR} ever leaves the emitter (§5.5 point 2): interpolation is a
// convenience for a human writing by hand, and a hazard in a generated file —
// den resolved everything already, and a ${VAR} would re-open the resolution to
// whatever shell runs sbx.
func TestEnvFileEmitsNoInterpolation(t *testing.T) {
	e := completeEnv()
	e.Workspaces = append(e.Workspaces, "/tmp/${HOME}/x")
	if _, err := EnvFile(e); err == nil {
		t.Error("a workspace carrying ${VAR} must be refused: den resolves before emitting")
	}
}

func TestEnvFileRejectsIncompleteEntries(t *testing.T) {
	for name, mutate := range map[string]func(*Env){
		"no name":       func(e *Env) { e.Name = "" },
		"bad name":      func(e *Env) { e.Name = "api." },
		"no image":      func(e *Env) { e.Image = "" },
		"no mixin":      func(e *Env) { e.MixinKit = "" },
		"no workspace":  func(e *Env) { e.Workspaces = nil },
		"relative path": func(e *Env) { e.Workspaces = []string{"relative/path"} },
	} {
		e := completeEnv()
		mutate(&e)
		if _, err := EnvFile(e); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func TestEnvFileGolden(t *testing.T) {
	cases := []struct {
		file string
		e    Env
	}{
		{"env-minimal.golden", Env{
			Name:       "api",
			Image:      "devx:v1",
			MixinKit:   "/den/cache/mixins/api",
			Workspaces: []string{"/dev/api", "/home/me/.den/agents/claude"},
		}},
		{"env-complete.golden", completeEnv()},
		// A THIRD file rather than resources folded into completeEnv(): the two
		// above are what proves sandboxOptions carries no cpus/memory when
		// nothing declares them, and folding would spend that proof to save a file.
		{"env-resources.golden", func() Env {
			e := completeEnv()
			n := 4
			e.CPUs = &n
			e.Memory = "8g"
			return e
		}()},
	}
	for _, c := range cases {
		got, err := EnvFile(c.e)
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		path := filepath.Join("testdata", c.file)
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
		}
	}
}
```

`internal/sbx/env_resources_test.go` :

```go
package sbx

import (
	"strings"
	"testing"
)

// Absent means ABSENT: a den declaring no `resources:` emits a sandboxOptions
// carrying the template alone (§5.5 point 7).
func TestEnvFileOmitsResourcesWhenUnset(t *testing.T) {
	out, err := EnvFile(completeEnv())
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	for _, key := range []string{"cpus:", "memory:"} {
		if strings.Contains(string(out), key) {
			t.Errorf("%q is emitted while nothing declared it:\n%s", key, out)
		}
	}
}

// A WRITTEN zero is a value someone can mean: `sbx create --help` documents
// `--cpus 0` as "auto: all host CPUs". That is the entire reason CPUs is a
// pointer, and emitting `cpus: 0` for an ABSENCE would say something the user
// could have chosen to say.
func TestEnvFileEmitsExplicitZeroCPUs(t *testing.T) {
	e := completeEnv()
	zero := 0
	e.CPUs = &zero
	out, err := EnvFile(e)
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	if !strings.Contains(string(out), "cpus: 0") {
		t.Errorf("an explicitly written 0 must be emitted:\n%s", out)
	}
}

// BOUNDARY guard, the doctrine CreateArgv stated for its own inputs and this
// emitter inherits: nest.Resolve refuses these values one layer up, where the
// message names the yaml file to fix — but EnvFile is exported and takes a
// struct anyone can fill, and the values it does not guard are the ones sbx
// rejects SERVER-side, after pulling the image (§14.5).
func TestEnvFileGuardsItsOwnResources(t *testing.T) {
	for name, mutate := range map[string]func(*Env){
		"negative cpus": func(e *Env) { n := -1; e.CPUs = &n },
		"bogus memory":  func(e *Env) { e.Memory = "1bb" },
	} {
		e := completeEnv()
		mutate(&e)
		if _, err := EnvFile(e); err == nil {
			t.Errorf("%s: must be refused before the image pull", name)
		}
	}
}
```

- [ ] **Step 2: lancer, vérifier que ça échoue**

Run: `go test ./internal/sbx/ -run TestEnvFile -count=1`
Expected: FAIL — `undefined: Env`, `undefined: EnvFile`.

- [ ] **Step 3: implémenter `internal/sbx/env.go`**

```go
package sbx

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnvSchemaVersion is the ONLY schemaVersion den emits, and it is a STRING:
// sbx answers `unsupported schemaVersion "2" (supported: 1)`, and the int 1
// round-trips as an unquoted scalar sbx refuses.
//
// Pinning it is the mechanism that makes the seam argument true rather than
// aspirational (spec 2026-08-24 §5.5 point 1): a schema evolution becomes a
// visible refusal, never a silently wrong emission. `sbx env` is EXPERIMENTAL
// on all four subcommands — this constant is where that bet is localized.
const EnvSchemaVersion = "1"

// Env is what den compiles a nest into: the input of EnvFile.
//
// It carries EXACTLY the fields Create carried, and that is the design: the
// spec announces a zero-sum exchange with argv.go (§5.4), so an emitter taking
// more inputs than the argv it replaces would be a widened scope in disguise.
//
// MixinKit is a field SEPARATE from StackKits, not the last element of one
// list: the mixin is fail-closed and sbx's dispatcher does `exit $rc` on the
// first failure, which deprives later kits of their startup commands. Two
// fields make the inversion impossible; a single list would only make it
// unlikely — and §14.4 measured that `kits:` preserves declaration order, so
// the position is expressible and therefore load-bearing.
type Env struct {
	Name       string   // sandbox name → `name:`, which wins over the file's directory
	Image      string   // → sandboxOptions.template, which overrides the agent's image
	StackKits  []string // cross-cutting kits then the stack kit, layering order
	MixinKit   string   // generated mixin directory — ALWAYS the last kit
	// Workspaces: host paths mounted, in order. The FIRST one becomes
	// `workspace:` and must be the repo (or its worktree): Sandbox.Workdir
	// depends on it for the attach. The rest become additionalWorkspaces.
	Workspaces []string
	// CPUs is sandboxOptions.cpus, and NIL means "write no key at all" — the
	// same contract the `--cpus` flag had, for the same measured reason: `sbx
	// create --help` documents `--cpus 0` as "auto: all host CPUs", so a
	// written 0 is a value someone can mean and must stay distinguishable from
	// silence.
	CPUs *int
	// Memory is sandboxOptions.memory, VERBATIM in the spelling the user wrote
	// — sbx's grammar is the authority (ParseMemory mirrors it). Empty writes
	// no key.
	Memory string
}

// envDoc is the ON-DISK shape, and it is unexported on purpose: it is the one
// place the §14.4 key set is spelled out, and nothing outside this file gets to
// grow a field on it. TestEnvFileWritesNoUnmeasuredKey is what holds the line.
//
// Absent from it, deliberately: `ports:` (decision 9 — den publishes on
// demand, and the create-time behaviour of `ports:` is DEDUCED, not measured),
// `secrets:` / `registries:` / `bindings:` (decision 10 — their real lifecycle
// is unmeasured, and den does not relay a field whose effect it does not know),
// `env:` (den's mixin carries it), `mcp:`, and sandboxOptions.profile
// (decision 13 — probed: `sbx policy profile ls` answers "No policy profiles
// found" and no subcommand creates one, so den has nothing to point it at).
type envDoc struct {
	SchemaVersion        string         `yaml:"schemaVersion"`
	Agent                string         `yaml:"agent"`
	Name                 string         `yaml:"name"`
	Workspace            envWorkspace   `yaml:"workspace"`
	AdditionalWorkspaces []envWorkspace `yaml:"additionalWorkspaces,omitempty"`
	Kits                 []string       `yaml:"kits,omitempty"`
	SandboxOptions       envOptions     `yaml:"sandboxOptions"`
}

// envWorkspace carries `path` ALONE, because §14.4 measured WorkspaceMount as
// carrying path alone — no `ro`, no `target`, no `clone`.
type envWorkspace struct {
	Path string `yaml:"path"`
}

type envOptions struct {
	// A pointer with omitempty: yaml.v3's isZero reports a non-nil pointer as
	// NON-empty even when it addresses 0, which is exactly the distinction
	// Env.CPUs exists to preserve.
	CPUs     *int   `yaml:"cpus,omitempty"`
	Memory   string `yaml:"memory,omitempty"`
	Template string `yaml:"template"`
}

// EnvFile renders the .sbxenv.yaml den hands to `sbx env create`.
//
// It replaces CreateArgv one for one (spec 2026-08-24 §5.4) and inherits its
// doctrine: it is exported, it takes a struct anyone can fill, so it guards its
// own input even where nest.Resolve already refused the same values one layer
// up — the ones it does not guard are the ones sbx rejects SERVER-side, after
// pulling the image (§14.5), and §6 of the mother spec wants the refusal before
// the first side effect.
func EnvFile(e Env) ([]byte, error) {
	// Single source of truth, shared with internal/agent: validating
	// component-by-component let "api." through, which sbx would really create
	// and `sbx ls` would split back into "api".
	if err := ValidateSandboxName(e.Name); err != nil {
		return nil, err
	}
	if strings.TrimSpace(e.Image) == "" {
		return nil, fmt.Errorf(
			"sandbox %q: no image — the stack must declare `image:` in stack.yaml", e.Name)
	}
	if strings.TrimSpace(e.MixinKit) == "" {
		return nil, fmt.Errorf(
			"sandbox %q: missing generated mixin — it carries the egress, env and freshness "+
				"command, an emission without it would produce a mute VM", e.Name)
	}
	if len(e.Workspaces) == 0 {
		return nil, fmt.Errorf(
			"sandbox %q: no workspace to mount — `sbx env create` requires at least one path", e.Name)
	}
	for i, w := range e.Workspaces {
		if err := checkEnvWorkspace(e.Name, i, w); err != nil {
			return nil, err
		}
	}
	if e.CPUs != nil {
		if err := ValidateCPUs(*e.CPUs); err != nil {
			return nil, fmt.Errorf("sandbox %q: %w", e.Name, err)
		}
	}
	if err := ValidateMemory(e.Memory); err != nil {
		return nil, fmt.Errorf("sandbox %q: %w", e.Name, err)
	}

	doc := envDoc{
		SchemaVersion: EnvSchemaVersion,
		// The image's FLAVOR decides what actually runs, not this field — an
		// image snapshotted from the claude base launches `claude` whatever is
		// written here, and den attaches via `sbx exec ... bash -l` anyway. The
		// same honest choice PositionalAgent documents for the argv.
		Agent:     PositionalAgent,
		Name:      e.Name,
		Workspace: envWorkspace{Path: e.Workspaces[0]},
		SandboxOptions: envOptions{
			CPUs:     e.CPUs,
			Memory:   e.Memory,
			Template: e.Image,
		},
	}
	for _, w := range e.Workspaces[1:] {
		doc.AdditionalWorkspaces = append(doc.AdditionalWorkspaces, envWorkspace{Path: w})
	}
	// BOUNDARY guard, not a duplicate of config.Stack.DeclaredKits — which
	// already filters empty entries at production's one caller. An empty kit
	// reference would reach sbx, which has no reason to reject it cleanly.
	for _, k := range e.StackKits {
		if k == "" {
			continue
		}
		doc.Kits = append(doc.Kits, k)
	}
	doc.Kits = append(doc.Kits, e.MixinKit) // always last, see the type's doc

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	// Two spaces, fixed here rather than left to the default: the goldens are
	// compared byte for byte and there is no -update flag, so the indent is part
	// of the contract this file owns.
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("rendering the environment file of %s: %w", e.Name, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("rendering the environment file of %s: %w", e.Name, err)
	}
	return buf.Bytes(), nil
}

// checkEnvWorkspace guards one entry of Workspaces, identified by its position
// (index 0) in the list.
//
// Two refusals, each from a measurement (§14.4): a RELATIVE path resolved
// neither against the file's directory nor against the process cwd, in two
// attempts — so it names nothing at all; and a `${VAR}` is sbx's interpolation,
// useful to a human writing by hand and dangerous in a generated file, where it
// would re-open a resolution den has already made (§5.5 point 2).
func checkEnvWorkspace(sandboxName string, i int, w string) error {
	if strings.TrimSpace(w) == "" {
		return fmt.Errorf(
			"sandbox %q: workspace #%d empty — it would mount nothing", sandboxName, i+1)
	}
	// A literal "$" is refused OUTRIGHT, not just "${": sbx interpolates both
	// `${VAR}` and `$VAR` (§14.4), so a path holding a bare dollar is already
	// ambiguous to the consumer. den resolves every path before emitting, and an
	// emitted variable would re-open that resolution to whatever environment runs
	// sbx. A real path carrying a dollar therefore cannot be mounted through the
	// emitter, and the message says so rather than implying only ${VAR} is at stake.
	if strings.Contains(w, "$") {
		return fmt.Errorf(
			"sandbox %q: workspace #%d (%q) contains \"$\", which sbx reads as a variable "+
				"reference — den resolves every path before emitting, so a path holding a dollar "+
				"cannot be emitted", sandboxName, i+1, w)
	}
	if !filepath.IsAbs(w) {
		return fmt.Errorf(
			"sandbox %q: workspace #%d (%q) is not an absolute path — `.sbxenv.yaml` resolves a "+
				"relative path against nothing at all (measured, spec §14.4)",
			sandboxName, i+1, w)
	}
	return nil
}
```

**Le point que la tâche 1 décide.** `checkEnvWorkspace` ne mentionne pas `:ro` ci-dessus. Selon le
verdict :

- **Verdict OUI** (`path: /x:ro` monte en lecture seule) : ajouter au début de `checkEnvWorkspace`
  le strip que `argv.go` faisait — `path := strings.TrimSuffix(w, ":ro")` — et juger `path`, en
  citant la sonde du 2026-08-25 dans le commentaire.
- **Verdict NON** : le refus va dans **`nest.Resolve`**, pas seulement dans l'émetteur. `EnvFile`
  tourne à l'étape 6 du spawn, quand les worktrees existent déjà (étape 3) : y refuser violerait la
  doctrine d'ordonnancement du §6. `nest.Resolve` construit les `Mount` et leur pose déjà une `Key`
  (`internal/nest/resolve.go:122`, `fmt.Sprintf("mounts[%d]", i)`) **exactement pour qu'un message
  nomme la clé YAML à corriger**. Deux édits, donc, et un test de chacun :

  1. **Dans `nest.Resolve`**, là où les mounts sont assemblés : refuser `m.RO`, en nommant `m.Key`
     et le fichier. Test : un nest portant `mounts: [{host: …, ro: true}]` est refusé **avant** tout
     worktree (le paquet `nest` n'en crée aucun ; le test de `spawn` correspondant vérifie qu'aucun
     répertoire n'apparaît).
  2. **Dans `checkEnvWorkspace`**, le garde-fou de frontière, pour la raison que `EnvFile` documente
     déjà pour tous ses autres : la fonction est exportée et prend une struct que n'importe qui
     remplit.

  Le texte du garde-fou de frontière :

```go
	// MEASURED 2026-08-25 (spec §14.4): a `.sbxenv.yaml` WorkspaceMount carries
	// `path` alone, and the ":ro" suffix `sbx create` accepts in a positional is
	// NOT honoured here. Refusing is the doctrine: the alternative is emitting a
	// read-write mount for a nest that asked for read-only, in silence.
	if strings.HasSuffix(w, ":ro") {
		return fmt.Errorf(
			"sandbox %q: workspace #%d (%q) is read-only, and `sbx env` cannot express that — "+
				"`.sbxenv.yaml` carries a path alone. Drop `ro: true` from `mounts:` in the nest, "+
				"or keep this nest on a den that still drives `sbx create`", sandboxName, i+1, w)
	}
```

- [ ] **Step 4: écrire les trois goldens**

`internal/sbx/testdata/env-minimal.golden` :

```yaml
schemaVersion: "1"
agent: shell
name: api
workspace:
  path: /dev/api
additionalWorkspaces:
  - path: /home/me/.den/agents/claude
kits:
  - /den/cache/mixins/api
sandboxOptions:
  template: devx:v1
```

`internal/sbx/testdata/env-complete.golden` :

```yaml
schemaVersion: "1"
agent: shell
name: api.feat12
workspace:
  path: /den/worktrees/feat12/api
additionalWorkspaces:
  - path: /den/worktrees/feat12/front
  - path: /home/me/.den/agents/claude
  - path: /home/me/.ssh_sbx
kits:
  - /den/kits/ssh-known-hosts
  - /den/stacks/dgdevx/kit
  - /den/cache/mixins/api.feat12
sandboxOptions:
  template: docker.io/library/dgdevx:v1
```

`internal/sbx/testdata/env-resources.golden` :

```yaml
schemaVersion: "1"
agent: shell
name: api.feat12
workspace:
  path: /den/worktrees/feat12/api
additionalWorkspaces:
  - path: /den/worktrees/feat12/front
  - path: /home/me/.den/agents/claude
  - path: /home/me/.ssh_sbx
kits:
  - /den/kits/ssh-known-hosts
  - /den/stacks/dgdevx/kit
  - /den/cache/mixins/api.feat12
sandboxOptions:
  cpus: 4
  memory: 8g
  template: docker.io/library/dgdevx:v1
```

- [ ] **Step 5: lancer, réconcilier l'encodeur et les goldens**

Run: `go test ./internal/sbx/ -run TestEnvFile -count=1`

Attendu : PASS. Si `TestEnvFileGolden` échoue **uniquement** sur de l'indentation ou l'ordre de
`- path:`, c'est l'encodeur qui a raison et le golden qui est faux : recopier le bloc `--- got ---`
dans le fichier `testdata/` **à la main**, puis relancer. Il n'existe pas de flag `-update`, et
c'est délibéré. Si l'écart porte sur une **clé** (présente, absente, mal nommée), c'est l'émetteur
qui est faux : corriger `env.go`.

- [ ] **Step 6: la suite complète**

Run: `task check`
Expected: PASS. `argv.go` est intact et ses goldens sont verts — c'est la contrainte de la
décision 13, et cette tâche n'a encore aucun appelant.

- [ ] **Step 7: commit**

```bash
git add internal/sbx/env.go internal/sbx/env_test.go internal/sbx/env_resources_test.go internal/sbx/testdata
git commit -m "feat(sbx): emit a resolved .sbxenv.yaml, pinned to schemaVersion 1"
```

---

## Tâche 4 : la bascule — spawn émet, `sbx env create` crée, `argv.go` disparaît

**Files:**
- Modify: `internal/spawn/spawn.go` (branche création, autour de la ligne 1385)
- Delete: `internal/sbx/argv.go`, `internal/sbx/argv_test.go`, `internal/sbx/argv_resources_test.go`
- Delete: `internal/sbx/testdata/create-complete.golden`, `create-minimal.golden`,
  `create-resources.golden`
- Modify: les tests qui scriptaient l'argv — `internal/cli/up_test.go:284`,
  `internal/cli/hostile_test.go:93,123`, `internal/spawn/spawn_test.go:2690`
- Modify: les commentaires qui citent `sbx.CreateArgv` — `internal/nest/resolve.go:203`,
  `internal/config/stack.go:64,106,187,195`, `internal/config/validate.go:270`

**Interfaces:**
- Consumes: `sbx.EnvFile`, `sbx.Env` (tâche 3) ; `manifest.SbxEnvPath` (tâche 2).
- Produces: un `sbx.Runner.Run(ctx, "env", "create", <path>)` à la place de
  `Run(ctx, "create", …)`.

**Un seul commit**, et c'est la contrainte de la décision 13 : `argv.go` reste vert **jusqu'à ce
que** l'émetteur le remplace, donc la suppression et la bascule ne peuvent pas être deux commits
dont le premier laisse la branche cassée.

- [ ] **Step 1: écrire le test qui échoue, dans `internal/spawn/spawn_test.go`**

```go
// The engine change, stated where a reader looks for it: den emits a resolved
// file and hands its PATH to `sbx env create`. `sbx env run` is never called
// (decision 4) — it attaches, and an attach from a terminal is the branch that
// opens the `sbx setup` wizard (§14.2).
func TestSpawnCreatesThroughSbxEnv(t *testing.T) {
	denHome, _ := denTest(t)          // internal/spawn/spawn_test.go:31
	f, d := fakeDeps()                // :200 — an sbx.Fake with no live sandbox
	if err := Spawn(context.Background(), denHome, Options{Nest: "api"}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	home := denHome

	var envCreate []string
	for _, call := range f.Calls {
		if len(call) >= 2 && call[0] == "env" && call[1] == "create" {
			envCreate = call
		}
		if call[0] == "create" {
			t.Errorf("den still drives `sbx create`: %v", call)
		}
		if len(call) >= 2 && call[0] == "env" && call[1] == "run" {
			t.Errorf("`sbx env run` is never called (decision 4): %v", call)
		}
	}
	if envCreate == nil {
		t.Fatal("no `sbx env create` call")
	}
	if len(envCreate) != 3 {
		t.Fatalf("env create takes exactly one path: %v", envCreate)
	}
	want, err := manifest.SbxEnvPath(home, "api")
	if err != nil {
		t.Fatalf("SbxEnvPath: %v", err)
	}
	if envCreate[2] != want {
		t.Errorf("env create %q, want %q", envCreate[2], want)
	}
	// The file is on disk BEFORE the call, and it is the record `den rm` reads.
	content, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("the emitted file is missing: %v", err)
	}
	if !strings.Contains(string(content), `schemaVersion: "1"`) {
		t.Errorf("the emitted file is not a pinned .sbxenv.yaml:\n%s", content)
	}
}
```

- [ ] **Step 2: lancer, vérifier que ça échoue**

Run: `go test ./internal/spawn/ -run TestSpawnCreatesThroughSbxEnv -count=1`
Expected: FAIL — `den still drives \`sbx create\`` et `no \`sbx env create\` call`.

- [ ] **Step 3: remplacer le bloc de création dans `internal/spawn/spawn.go`**

Remplacer l'appel à `sbx.CreateArgv` + `d.Sbx.Run(ctx, argv...)` par :

```go
		// The emitted file, written BEFORE the call that consumes it and kept
		// afterwards: it is not a temporary, it is sbx's half of the creation
		// record (spec 2026-08-24 §5.6). `sbx env rm` resolves the sandbox FROM
		// the file set it is passed, so `den rm` reads this exact path back —
		// which is why it lives under state/ (never purged) and not cache/.
		envFile, err := sbx.EnvFile(sbx.Env{
			Name:       sandboxName,
			Image:      r.Stack.Image,
			StackKits:  r.Stack.DeclaredKits(),
			MixinKit:   mixinDir,
			Workspaces: workspaces,
			// Already validated by nest.Resolve, before the worktrees above
			// existed. EnvFile checks them again as a boundary guard, and that
			// duplication is the doctrine, not an oversight.
			CPUs:   r.Resources.CPUs,
			Memory: r.Resources.Memory,
		})
		if err != nil {
			return err
		}
		envPath, err := manifest.SbxEnvPath(r.DenHome, sandboxName)
		if err != nil {
			return err
		}
		// 0600, like the manifest beside it: the file lists every path den
		// mounts, and nothing justifies making that world-readable. The
		// directory already exists — manifest.Write created it a few lines up —
		// but MkdirAll stays, so this block does not silently depend on the
		// order of two writes.
		if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(envPath, envFile, 0o600); err != nil {
			return err
		}
		fmt.Fprintf(d.Out, "creating sandbox %s (image %s)...\n", sandboxName, r.Stack.Image)
		// `env create`, never `env run`: run attaches, and an attach from a
		// terminal is the branch that opens the `sbx setup` wizard on a machine
		// that has never been prompted (§14.2). den attaches by `sbx exec -it`
		// further down, as it always has (decision 4).
		if _, err := d.Sbx.Run(ctx, "env", "create", envPath); err != nil {
			return fmt.Errorf("creating sandbox %s: %w", sandboxName, err)
		}
```

- [ ] **Step 4: lancer le test ciblé**

Run: `go test ./internal/spawn/ -run TestSpawnCreatesThroughSbxEnv -count=1`
Expected: PASS.

- [ ] **Step 5: supprimer l'ancien moteur**

```bash
git rm internal/sbx/argv.go internal/sbx/argv_test.go internal/sbx/argv_resources_test.go
git rm internal/sbx/testdata/create-complete.golden internal/sbx/testdata/create-minimal.golden internal/sbx/testdata/create-resources.golden
```

`PositionalAgent` **ne part pas avec le fichier** : `internal/build/sandbox.go:61` et
`internal/config/stack.go:64` s'en servent, et `env.go` aussi. Le déplacer tel quel en tête de
`internal/sbx/env.go`, commentaire compris.

- [ ] **Step 6: réparer les appelants et les commentaires, en une passe**

Run: `go build ./... && grep -rn "CreateArgv" --include='*.go' internal | grep -v "build\."`

Chaque occurrence restante est soit un test qui scriptait l'argv (`internal/cli/up_test.go:284`,
`internal/cli/hostile_test.go:93,123`, `internal/spawn/spawn_test.go:2690`), soit un commentaire qui
cite `sbx.CreateArgv` comme le lieu d'un refus (`internal/nest/resolve.go:203`,
`internal/config/stack.go`, `internal/config/validate.go:270`). Les tests : réécrire l'assertion
contre `{"env", "create", <path>}` et contre le CONTENU du fichier émis. Les commentaires :
remplacer `sbx.CreateArgv` par `sbx.EnvFile` — le refus n'a pas changé de nature, seulement de site.
`internal/build/CreateArgv` n'est PAS concerné (non-objectif nommé en tête de plan).

- [ ] **Step 7: la suite complète**

Run: `task check`
Expected: PASS. Les goldens de `internal/cli` qui rendaient une ligne `creating sandbox …` sont
inchangés — le message n'a pas bougé.

- [ ] **Step 8: commit**

```bash
git add -A
git commit -m "feat(spawn): compile the nest into .sbxenv.yaml and create through sbx env"
```

---

## Tâche 5 : `den rm` — le chemin normal passe par `sbx env rm`, et le refus est la règle

**Mesuré le 2026-08-25, `sbx env rm --help` :** `sbx env rm [PATH...] [flags]`, chaque PATH est un
répertoire ou le fichier lui-même, `-f/--force` saute les invites de confirmation, et
`--prune-bindings` retire les bindings **que l'environnement déclare** — den n'en déclare aucun
(décision 10), donc den ne passe jamais ce drapeau.

**L'ordre est une décision, pas un détail.** La validation de l'enregistrement arrive **avant** le
nettoyage des worktrees. L'inverse déplacerait les worktrees à la corbeille, puis refuserait — un
refus qui a déjà agi n'est pas un refus.

**La conséquence à assumer, et le spec la nomme (§5.8 : « fichier absent, tronqué, ou écrit par un
den plus récent ⇒ den refuse ») :** toute sandbox créée **avant** la bascule n'a pas de
`.sbxenv.yaml`, donc son `den rm` refuse et demande `--force`. C'est voulu — `sbx env rm` résout la
sandbox depuis le fichier, et den n'a rien d'autre à lui donner — mais le message doit distinguer
les deux causes, parce que le remède est le même et la surprise ne l'est pas :

- **Aucun fichier** : « cette sandbox est antérieure à l'émetteur (ou n'a pas été créée par den) ».
- **Fichier illisible** : l'erreur de `CheckEnvFile`, telle quelle.

Les deux nomment `den rm --force <sandbox>`. Un test par cause.

**Files:**
- Modify: `internal/cli/rm.go` (`RunE`, autour des lignes 88-107)
- Create: `internal/sbx/envread.go` — le lecteur strict du fichier émis
- Test: `internal/cli/rm_test.go`, `internal/sbx/envread_test.go`

**Interfaces:**
- Consumes: `manifest.SbxEnvPath` (tâche 2), `sbx.EnvSchemaVersion` (tâche 3).
- Produces: `func CheckEnvFile(path string) error` dans `internal/sbx` — nil si den a écrit ce
  fichier et le comprend ; une erreur nommant le fichier sinon.

- [ ] **Step 1: écrire les tests qui échouent**

`internal/sbx/envread_test.go` :

```go
package sbx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckEnvFileAcceptsWhatEnvFileWrote(t *testing.T) {
	out, err := EnvFile(completeEnv())
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	path := filepath.Join(t.TempDir(), ".sbxenv.yaml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := CheckEnvFile(path); err != nil {
		t.Errorf("den refuses a file it wrote itself: %v", err)
	}
}

func TestCheckEnvFileRefusesWhatDenCannotVouchFor(t *testing.T) {
	for name, content := range map[string]string{
		"truncated":     "schemaVersion: \"1\"\nage",
		"newer schema":  "schemaVersion: \"2\"\nagent: shell\nname: api\n",
		"unknown key":   "schemaVersion: \"1\"\nagent: shell\nname: api\nfutureKey: 1\n",
		"empty":         "",
		"no name":       "schemaVersion: \"1\"\nagent: shell\n",
	} {
		path := filepath.Join(t.TempDir(), ".sbxenv.yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := CheckEnvFile(path); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func TestCheckEnvFileRefusesAMissingFile(t *testing.T) {
	if err := CheckEnvFile(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("an absent record must be refused, not treated as empty")
	}
}
```

`internal/cli/rm_test.go` — deux tests, bâtis sur les helpers déjà là : `writeConfig`,
`minimalConfig`, `lsWith`, `executeCmdWithSbx`, et `f.HasCalled` (voir `TestRmDestroysTheSandbox`
`:97` et `TestRmDoesNotDestroyTheSandboxWhenAWorktreeIsDirty` `:357`).

```go
// writeEnvRecord puts a .sbxenv.yaml where den will look for it. Written
// through sbx.EnvFile rather than by hand: a hand-written fixture would drift
// from what den really emits, and this test's whole subject is that den reads
// back its own emission.
func writeEnvRecord(t *testing.T, denHome, sandbox string) string {
	t.Helper()
	out, err := sbx.EnvFile(sbx.Env{
		Name:       sandbox,
		Image:      "devx:v1",
		MixinKit:   filepath.Join(denHome, "cache", "mixins", sandbox),
		Workspaces: []string{"/dev/api", "/profile/claude"},
	})
	if err != nil {
		t.Fatalf("EnvFile: %v", err)
	}
	path, err := manifest.SbxEnvPath(denHome, sandbox)
	if err != nil {
		t.Fatalf("SbxEnvPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The normal path: den hands sbx the file it emitted, and `-f` is not optional
// — `sbx env rm` prompts for confirmation without it (measured 2026-08-25), and
// a prompt in a non-interactive `den rm` blocks forever.
func TestRmRemovesThroughSbxEnvRm(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	envPath := writeEnvRecord(t, denHome, "api")

	f := &sbx.Fake{Responses: lsWith("api")}
	if _, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !f.HasCalled("env", "rm", "-f", envPath) {
		t.Errorf("den did not destroy through `sbx env rm`; calls: %v", f.Calls)
	}
	if f.HasCalled("rm", "--force", "api") {
		t.Errorf("den destroyed by name on the normal path; calls: %v", f.Calls)
	}
}

// The refusal, and the whole point of it: `sbx env rm` resolves the sandbox FROM
// the file, so an unreadable file is not a detail den can route around (§5.7 —
// a limitation is documented, never worked around by a second permanent path).
// The message must name the file AND the flag that unblocks the same command.
func TestRmRefusesAnUnreadableEnvRecordAndNamesForce(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	envPath := writeEnvRecord(t, denHome, "api")
	// A NEWER den's file: good YAML, a schemaVersion this den does not emit.
	if err := os.WriteFile(envPath, []byte("schemaVersion: \"9\"\nagent: shell\nname: api\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	f := &sbx.Fake{Responses: lsWith("api")}
	_, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "api")

	if err == nil {
		t.Fatal("den destroyed a sandbox whose record it could not read")
	}
	if !strings.Contains(err.Error(), envPath) {
		t.Errorf("the refusal does not name the file: %v", err)
	}
	if !strings.Contains(err.Error(), "den rm --force api") {
		t.Errorf("the refusal does not name the remedy: %v", err)
	}
	// Nothing was destroyed, on either route.
	if f.HasCalled("env", "rm", "-f", envPath) || f.HasCalled("rm", "--force", "api") {
		t.Errorf("den destroyed after refusing; calls: %v", f.Calls)
	}
	// And the record it could not read survives (spec §11).
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("den deleted a record it could not read: %v", err)
	}
}
```

**Un troisième test, et il porte l'ordre**, qui est la vraie décision de cette tâche : bâtir la
fixture de `TestRmDoesNotDestroyTheSandboxWhenAWorktreeIsDirty` (`:357`) — worktree réel, fichier
`draft.txt` **propre**, pas sale — avec un `.sbxenv.yaml` illisible, puis vérifier que
`filepath.Join(path, "draft.txt")` existe encore après le refus. Un refus qui a déjà déplacé des
répertoires à la corbeille n'est pas un refus.

- [ ] **Step 2: lancer, vérifier que ça échoue**

Run: `go test ./internal/sbx/ ./internal/cli/ -run 'CheckEnvFile|TestRmRemovesThroughSbxEnvRm|TestRmRefusesAnUnreadableEnvRecord' -count=1`
Expected: FAIL — `undefined: CheckEnvFile`, puis les deux assertions de `rm`.

- [ ] **Step 3: implémenter `internal/sbx/envread.go`**

```go
package sbx

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// CheckEnvFile answers ONE question: did den write this file, and does this den
// still understand it?
//
// It is what `den rm` consults before handing the path to `sbx env rm`, which
// resolves the sandbox FROM the file set it is passed (§14.4). A file den
// cannot vouch for would therefore resolve to a sandbox den cannot predict —
// possibly another one — so a refusal here is the honest answer and `--force`
// is the documented way past it (spec §5.8).
//
// STRICT, like every other decode in den (spec §12): an unknown key is a load
// error, never a silence. The most common such file is a NEWER den's — good
// YAML, refused on a field this version does not know — and that is exactly the
// case where guessing is worst.
//
// It never repairs, never rewrites, and never deletes: den does not delete a
// file it could not read (spec §11), and this function is the reader that
// establishes it.
func CheckEnvFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(content))
	dec.KnownFields(true)
	var doc envDoc
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if doc.SchemaVersion != EnvSchemaVersion {
		return fmt.Errorf(
			"reading %s: schemaVersion %q, but this den only emits %q — the file was written by "+
				"another version of den", path, doc.SchemaVersion, EnvSchemaVersion)
	}
	if doc.Name == "" {
		return fmt.Errorf(
			"reading %s: no `name:` — den cannot tell which sandbox this file resolves to", path)
	}
	return nil
}
```

- [ ] **Step 4: brancher `internal/cli/rm.go`**

Dans `RunE`, **avant** le bloc `keepWorktrees` / `cleanWorktrees` :

```go
			// BEFORE any reclaim, and that order is the decision: the reverse
			// would move worktrees to the trash and only then refuse — a refusal
			// that has already acted is not a refusal.
			envPath, err := manifest.SbxEnvPath(home, name)
			if err != nil {
				return err
			}
			envErr := sbx.CheckEnvFile(envPath)
			if envErr != nil && !force {
				// The remedy is named IN the message, and that is what keeps this
				// refusal on the right side of doctrine T13/T16: what the doctrine
				// forbids is a refusal with no way out, not a refusal. The exit is
				// immediate, documented, and in the command the user is already in.
				return fmt.Errorf("%w\nden cannot hand this record to `sbx env rm`, which resolves "+
					"the sandbox from the file it is given.\nto destroy %s by name instead, run: "+
					"den rm --force %s", envErr, name, args[0])
			}
```

Puis remplacer la destruction :

```go
			if envErr == nil {
				// `-f` is not optional: `sbx env rm` prompts for confirmation
				// without it (measured 2026-08-25), and a prompt in a
				// non-interactive `den rm` blocks forever.
				//
				// --prune-bindings is deliberately NOT passed: it removes the
				// bindings THIS environment declares, and den declares none
				// (decision 10 — bindings are not emitted while their lifecycle
				// is unmeasured). Passing it would ask sbx to prune a set den
				// never wrote.
				if _, err := runner.Run(cmd.Context(), "env", "rm", "-f", envPath); err != nil {
					return err
				}
			} else {
				// The conceded fallback of §5.8 — conceded, not designed. See the
				// warning next task prints before we get here.
				if _, err := runner.Run(cmd.Context(), "rm", "--force", name); err != nil {
					return err
				}
			}
```

- [ ] **Step 5: lancer les tests ciblés**

Run: `go test ./internal/sbx/ -run CheckEnvFile -count=1 && go test ./internal/cli/ -run 'TestRmRemovesThroughSbxEnvRm|TestRmRefusesAnUnreadableEnvRecord' -count=1`
Expected: PASS.

- [ ] **Step 6: migrer les fixtures de `den rm` — c'est le vrai coût de cette tâche**

Ce n'est pas un ajustement d'assertion : **chaque fixture existante de `rm_test.go` construit un den
home SANS `.sbxenv.yaml`**, donc avec le refus ajouté au step 4 elle prend la branche de refus et
n'atteint plus aucune destruction. Quatorze tests, nommés :

`TestRmDestroysTheSandbox` (:97), `TestRmNeverTouchesTheAgentProfile` (:112),
`TestRmKeepWorktreesLeavesDiskUntouched` (:147), `TestRmWithNoWorktreeCleansUpNothing` (:178),
`TestRmUnreadableNestDoesNotPreventDestruction` (:234),
`TestRmDestroysTheSandboxDespiteAnUnrelatedConfigFault` (:270),
`TestRmRejectsAnUnknownWorktreeLayout` (:302), `TestRmUnreadableNestWritesToStderr` (:331),
`TestRmDoesNotDestroyTheSandboxWhenAWorktreeIsDirty` (:357), `TestRmRespectsThePerRepoLayout`
(:437), `TestRmAnnouncesNothingWhenTheWorktreeHasAlreadyDisappeared` (:481),
`TestRmNamesTheTrashEvenWhenPruningFails` (:548), `TestRmSbxFailureSurfaces` (:597),
`TestRmBoundsGitProbesWithADeadline` (:612).

Le geste mécanique, pour chacun : ajouter `writeEnvRecord(t, denHome, "<sandbox>")` après le
`writeConfig`, et remplacer les assertions `f.HasCalled("rm", "--force", …)` par
`f.HasCalled("env", "rm", "-f", envPath)`.

**Deux ne sont PAS mécaniques**, et les traiter mécaniquement casserait ce qu'ils affirment :

- `TestRmRejectsANonCanonicalSandboxName` (:202) doit continuer de refuser **avant** que
  `manifest.SbxEnvPath` compose quoi que ce soit — c'est `sandboxNameOf` qui refuse, en amont. Ne
  pas lui donner de record : vérifier seulement que le refus est toujours celui du nom.
- `TestRmUnreadableNestDoesNotPreventDestruction` (:234) affirme qu'un **autre** fichier illisible
  (le nest) n'empêche pas la destruction. Lui donner un `.sbxenv.yaml` **lisible** à côté du nest
  cassé, sinon le test se met à prouver l'inverse de son nom.

- [ ] **Step 7: la suite complète**

Run: `task check`
Expected: PASS. La ligne `sandbox %s destroyed …` est inchangée, donc les goldens de
`internal/cli/testdata` ne bougent pas.

- [ ] **Step 8: commit**

```bash
git add internal/sbx/envread.go internal/sbx/envread_test.go internal/cli/rm.go internal/cli/rm_test.go
git commit -m "feat(rm): destroy through sbx env rm, and refuse an unreadable record"
```

---

## Tâche 6 : `--force` porte deux sens, et den dit lequel il exerce

**Ce qui rend le double sens acceptable** (§5.9) : l'utilisateur qui force pour retirer un worktree
sale hérite du second sens sans l'avoir demandé. den doit donc **annoncer** quand il exerce le
second, et **se taire** quand il ne l'exerce pas — un avertissement qui se déclenche toujours n'est
plus lu (§2 de la spec mère refuse le bruit autant que le silence).

**Files:**
- Modify: `internal/cli/rm.go`
- Test: `internal/cli/rm_test.go`

**Interfaces:**
- Consumes: la branche `envErr != nil` de la tâche 5.
- Produces: rien de nouveau — deux messages.

- [ ] **Step 1: écrire les deux tests qui échouent**

```go
// Second sense exercised: den says so BEFORE acting, names why, and names what
// is left behind. `sbx env rm` is what removes the sandbox-scoped secrets; a
// destruction by name does not, so the user has to be told and given the
// command.
func TestRmForceAnnouncesTheByNameDestruction(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	envPath := writeEnvRecord(t, denHome, "api")
	if err := os.WriteFile(envPath, []byte("schemaVersion: \"9\"\nagent: shell\nname: api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &sbx.Fake{Responses: lsWith("api")}
	out, err := executeCmdWithSbx(t, f, "--den-home", denHome, "rm", "--force", "api")
	if err != nil {
		t.Fatalf("--force must destroy, not refuse: %v", err)
	}
	if !f.HasCalled("rm", "--force", "api") {
		t.Errorf("--force did not destroy by name; calls: %v", f.Calls)
	}
	if !strings.Contains(out, envPath) {
		t.Errorf("the announcement does not name the unreadable record:\n%s", out)
	}
	if !strings.Contains(out, "by name") {
		t.Errorf("the announcement does not say the destruction is by name:\n%s", out)
	}
	if !strings.Contains(out, "sbx secret ls --sandbox api") {
		t.Errorf("the announcement does not name how to see the secrets left behind:\n%s", out)
	}
	// The unreadable file SURVIVES: den never deletes what it could not read.
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("den deleted a record it could not read: %v", err)
	}
}

// First sense only: `--force` used to reclaim a dirty worktree on a sandbox
// whose record reads fine must say NOTHING about the second sense. A warning
// that fires on every forced removal stops being read.
func TestRmForceStaysSilentAboutTheSecondSense(t *testing.T) {
	denHome := t.TempDir()
	writeConfig(t, denHome, minimalConfig)
	writeEnvRecord(t, denHome, "api") // READABLE: --force serves the first sense alone
	f := &sbx.Fake{Responses: lsWith("api")}
	stdout, stderr, err := executeCmdWithSbxSeparateStreams(t, f, "--den-home", denHome, "rm", "--force", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout + stderr
	for _, noise := range []string{"by name", "sbx secret", "scoped secrets"} {
		if strings.Contains(out, noise) {
			t.Errorf("den mentions the second sense of --force when it did not exercise it (%q):\n%s", noise, out)
		}
	}
}
```

- [ ] **Step 2: lancer, vérifier que ça échoue**

Run: `go test ./internal/cli/ -run 'TestRmForce' -count=1`
Expected: FAIL — les deux, sur des messages absents.

- [ ] **Step 3: implémenter l'annonce dans `internal/cli/rm.go`**

Dans la branche `envErr != nil && force`, juste avant le nettoyage des worktrees :

```go
			if envErr != nil && force {
				// Printed BEFORE anything is destroyed, and only on this branch.
				// `--force` carries two senses (spec §5.9) — "reclaim a dirty
				// worktree" and "destroy by name when the record is unreadable" —
				// and the user who asked for the first inherits the second without
				// asking. Saying which one den exercises is what makes one flag
				// with two senses acceptable; a silent second sense would make
				// --force a switch whose reach the user no longer knows.
				//
				// The mirror rule matters as much: when --force serves the first
				// sense ALONE, den says nothing about the second. A warning that
				// fires on every forced removal stops being read.
				fmt.Fprintf(out, "creation record unreadable (%v)\n", envErr)
				fmt.Fprintf(out, "--force: destroying %s by name instead — sbx removes the "+
					"sandbox, and the secrets scoped to it are left in place\n", name)
				fmt.Fprintf(out, "  to see them:    sbx secret ls --sandbox %s\n", name)
				fmt.Fprintf(out, "  to remove one:  sbx secret rm <service> --sandbox %s\n", name)
				fmt.Fprintf(out, "  den leaves %s alone: it may belong to another version of den\n",
					envPath)
			}
```

- [ ] **Step 4: lancer les tests ciblés**

Run: `go test ./internal/cli/ -run 'TestRmForce' -count=1`
Expected: PASS.

- [ ] **Step 5: la suite complète**

Run: `task check`
Expected: PASS.

- [ ] **Step 6: commit**

```bash
git add internal/cli/rm.go internal/cli/rm_test.go
git commit -m "feat(rm): --force names which of its two senses it exercises"
```

---

## Tâche 7 : `den lint` vérifie qu'un nest compile

**Pourquoi ici** (§6.4) : `internal/lint` est le **juge unique** partagé par `den lint`, `den source
add` (qui refuse **et supprime** le clone) et `den source update` (qui refuse avant le
fast-forward). Sous le rôle de compilateur, une source valide au sens actuel pourrait produire une
émission que `sbx env create` refuse — exactement la divergence que le juge unique existe pour
empêcher. Deux contrôles, et un troisième qui ne dépend pas de ce spec.

**Files:**
- Modify: `internal/lint/lint.go` (`checkNest`, `checkStack`)
- Test: `internal/lint/lint_test.go`

**Interfaces:**
- Consumes: `sbx.ValidateSandboxName`, `sbx.ValidateMemory`, `sbx.ValidateCPUs`.
- Produces: rien — trois refus de plus dans la liste que `Run` rend.

- [ ] **Step 1: écrire les tests qui échouent**

Le paquet a déjà `writeTree(t, map[string]string)` (`internal/lint/lint_test.go:12`) et
`errsString(errs)` (`:156`) — les réutiliser, ne pas en écrire d'autres.

```go
// A nest whose derived sandbox name is illegal compiles to a .sbxenv.yaml that
// `sbx env create` refuses. Catching it at the source is the single-judge
// property: `den source add` refuses AND deletes the clone, `den source update`
// refuses before the fast-forward — one refusal there instead of N refusals on
// every consumer's machine.
func TestRunNestThatCannotNameASandbox(t *testing.T) {
	root := writeTree(t, map[string]string{
		"den-source.yaml":       "name: corp\n",
		"stacks/devx/stack.yaml": "image: devx:v1\n",
		// "a" is one character, and sbx's name rule is a WHOLE-name one:
		// `^[a-zA-Z0-9][a-zA-Z0-9.-]+$` needs two (MinNameLength, measured).
		"nests/a.yaml": "stack: devx\n",
	})
	errs := Run(root)
	if !strings.Contains(errsString(errs), "a") || len(errs) == 0 {
		t.Errorf("lint accepted a nest that cannot name a sandbox: %v", errs)
	}
}

// The third check of §6.4, and it does not depend on the emitter: sbx refuses a
// too-small `memory:` SERVER-side, AFTER pulling the image (§14.5). nest.Resolve
// already refuses it before the first side effect — but once per spawn, on every
// machine. lint owns the same validators and a far better occasion.
func TestRunIllegalStackMemory(t *testing.T) {
	root := writeTree(t, map[string]string{
		"den-source.yaml":        "name: corp\n",
		"stacks/devx/stack.yaml": "image: devx:v1\nresources:\n  memory: 512m\n",
	})
	errs := Run(root)
	if !strings.Contains(errsString(errs), "memory") {
		t.Errorf("lint accepted a memory sbx refuses after the image pull: %v", errs)
	}
}
```

Vérifier la valeur seuil contre `internal/sbx/resources.go` avant d'écrire `512m` : le test doit
citer une taille que `ValidateMemory` refuse RÉELLEMENT, pas une supposée. Si `512m` passe, prendre
la plus petite valeur que le validateur refuse et le dire dans le commentaire du test.

- [ ] **Step 2: lancer, vérifier que ça échoue**

Run: `go test ./internal/lint/ -count=1`
Expected: FAIL sur les deux — lint accepte encore.

- [ ] **Step 3: implémenter dans `internal/lint/lint.go`**

Dans `checkNest`, après les contrôles existants :

```go
	// Under the compiler role, a nest must compile to a LEGAL .sbxenv.yaml —
	// and the name is the half a source can get wrong on its own: `name:` is
	// what sbx uses to name the sandbox, and its charset is `sbx create
	// --name`'s. Checked here rather than only at spawn because lint is the
	// single judge (spec 2026-08-04 §5): it must never accept what a spawn
	// would later refuse.
	//
	// The INSTANCE is empty here on purpose: a source ships a nest, never a
	// worktree label, so the shortest name a spawn of it can produce is the
	// bare nest name — and sbx's own rule is a WHOLE-name one (MinNameLength,
	// `^[a-zA-Z0-9][a-zA-Z0-9.-]+$`), which per-component validation does not
	// cover.
	if _, err := sbx.SandboxName(n.Name, ""); err != nil {
		errs = append(errs, fmt.Errorf("nest %q: %w", n.Name, err))
	}
```

Dans `checkStack`, pour `resources:` :

```go
	// The SAME validators nest.Resolve uses, at a better occasion: `den source
	// add` refuses and deletes the clone, `den source update` refuses before the
	// fast-forward. An illegal size must die at the source, once, not N times on
	// its consumers — and sbx only refuses it server-side, after the image pull
	// (§14.5).
	if s.Resources.CPUs != nil {
		if err := sbx.ValidateCPUs(*s.Resources.CPUs); err != nil {
			errs = append(errs, fmt.Errorf("stack %q: %w", s.Name, err))
		}
	}
	if err := sbx.ValidateMemory(s.Resources.Memory); err != nil {
		errs = append(errs, fmt.Errorf("stack %q: %w", s.Name, err))
	}
```

`internal/lint` n'importe pas encore `internal/sbx` : ajouter l'import. Vérifier qu'aucun cycle
n'apparaît (`internal/sbx` importe `internal/config`, jamais `internal/lint`) —
`go build ./...` tranche.

Le contrôle « chaque référence de kit résout vers un répertoire » existe déjà via
`checkDeclaredPath` (`internal/lint/lint.go:327`) : vérifier qu'il couvre bien `kits:` d'un stack,
et l'étendre seulement s'il ne le fait pas. Ne pas dupliquer un juge.

- [ ] **Step 4: lancer les tests du paquet**

Run: `go test ./internal/lint/ -count=1`
Expected: PASS.

- [ ] **Step 5: la suite complète**

Run: `task check`
Expected: PASS.

- [ ] **Step 6: commit**

```bash
git add internal/lint
git commit -m "feat(lint): a nest must compile to a legal .sbxenv.yaml"
```

---

## Tâche 8 : la documentation rattrape le moteur

**Files:**
- Modify: `CLAUDE.md` (le paragraphe « A positioning decision is pending on how den drives sbx »)
- Modify: `README.md` (toute mention de `sbx create` comme moteur de spawn)
- Modify: `docs/superpowers/specs/2026-08-24-sbx-env-positioning-design.md` (statut)

- [ ] **Step 1: `CLAUDE.md`**

Le paragraphe actuel dit que la conception est « specified, not implemented », que `argv.go` est
« still the live path », et qu'il ne faut pas le supprimer avant que l'émetteur existe. Les trois
phrases sont devenues fausses. Le remplacer par ce qui est vrai : l'émetteur est
`internal/sbx/env.go`, le fichier émis vit dans `state/sandboxes/<sandbox>/.sbxenv.yaml`, den crée
par `sbx env create` et détruit par `sbx env rm -f`, `--force` bascule sur `sbx rm --force` par le
nom, la cascade `resources:` sort en `sandboxOptions`, et `internal/build` pilote toujours
`sbx create` — c'est un autre objet, pas un chemin fantôme.

Actualiser aussi la section « What den mounted is recorded, not re-derived » : le chemin du record a
changé, et la lecture de l'ancien est permanente.

- [ ] **Step 2: `README.md`**

Run: `grep -n "sbx create" README.md`
Chaque occurrence décrivant le **spawn** devient `sbx env create`. Celles qui décrivent `den build`
restent.

Ajouter une ligne que l'utilisateur va rencontrer une fois : **une sandbox créée avant l'émetteur
n'a pas de `.sbxenv.yaml`, donc son `den rm` refuse et demande `den rm --force <sandbox>`.** C'est
le §5.8 appliqué au parc existant, pas un défaut ; le dire ici évite qu'il soit découvert au moment
de détruire.

- [ ] **Step 3: le statut du spec**

Ligne 6 : `**Statut :** validé en brainstorming, **en attente de relecture humaine**…` devient
`**Statut :** implémenté — plan `docs/superpowers/plans/2026-08-25-sbxenv-emitter.md`, relecture
humaine du 2026-08-25`. Ligne 12-13 (« Ce document ne décrit aucun code écrit ») et le §9 (« Aucune
ligne d'`internal/**` écrite POUR ce spec ») portent la même fausseté : les corriger, sans réécrire
les décisions.

- [ ] **Step 4: la suite complète**

Run: `task check`
Expected: PASS.

- [ ] **Step 5: commit**

```bash
git add CLAUDE.md README.md docs/superpowers/specs/2026-08-24-sbx-env-positioning-design.md
git commit -m "docs: den compiles — the emitter shipped"
```

---

## Ce que ce plan ne fait PAS, et pourquoi

- **`ports:`, `secrets:`, `registries:`, `bindings:` restent non émis** (décisions 9 et 10). den ne
  relaie pas un champ dont il ignore l'effet. Le départ d'une part de `internal/converge` reste un
  **candidat**, pas une décision : il attend une sonde dédiée et sa propre issue (§5.4, §7).
- **`internal/build` garde `sbx create`.** Non-objectif nommé en tête de plan, avec sa raison.
- **`internal/manifest` ne perd rien.** Le manifeste git porte `repos[].mount`, `key`, `origin`,
  `worktree`, `git_dirs` ; `.sbxenv.yaml` ne porte que des chemins de workspace résolus,
  indistinguables entre un worktree, un `.git` et le profil agent. Deux enregistrements, deux
  publics, aucun recouvrement de sens (§5.4).
- **`spawn.reportResourceDrift`, `internal/policy/settle.go`, `internal/agent`, `internal/worktree`,
  `internal/ports`, `internal/source`, `internal/doctor` ne sont pas touchés** (§5.4).
- **Aucune migration des records existants.** `state/` n'est jamais purgé, den ne convertit rien, et
  le lecteur legacy est permanent (§11).
