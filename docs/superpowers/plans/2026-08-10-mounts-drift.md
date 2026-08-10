# Mount drift detection — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** avertir, à l'attache d'une sandbox vivante, quand `mounts:` a bougé depuis le `sbx create` — un mount sans `link:` ajouté ou repointé, et un `ro:` retourné.

**Architecture :** aucune donnée nouvelle n'est enregistrée. `sbx ls --json` rapporte `workspaces` suffixe `:ro` compris (mesuré 2026-08-10, sbx v0.37.1), donc `spawn` tient déjà les deux côtés : `live.Workspaces` (la VM) et `r.Mounts` (la configuration d'aujourd'hui). Une fonction de rapport les compare, sur l'orthographe **exacte** de l'argv. Elle avertit, ne refuse jamais, ne recrée jamais.

**Tech stack :** Go 1.2x, `gopkg.in/yaml.v3`, `sbx.Fake` pour les tests. Runner : `task`.

## Global Constraints

- Spec de référence : `docs/superpowers/specs/2026-08-10-mounts-drift-design.md`. Issue : #56.
- Branche de travail : `feat/mounts-drift` (déjà créée, elle porte le commit du spec).
- `task check` (lint » typecheck » test, fail-fast) doit passer avant chaque commit. `gofmt` est **imposé**, pas conseillé.
- Aucun test n'appelle `t.Parallel()`, n'ouvre de socket ni ne lance de processus.
- Code, commentaires et messages utilisateur en **anglais**. Le spec et ce plan sont en français.
- Le style dominant est un long commentaire « pourquoi » au site de décision : ce qui a été rejeté, et quelle régression le choix empêche. Du code laconique ne ressemble visiblement pas au voisinage.
- `internal/cli` n'importe ni `net`, ni `hash/fnv`, ni `os/exec` — verrouillé par `internal/ports/hermeticity_test.go`. Ce plan ne touche pas `internal/cli`.
- Toute la comparaison porte sur l'orthographe **exacte** : ne jamais retirer le suffixe `:ro`, contrairement à `reportMissingGitDirs` et `reportUnmountedRepos`.

---

## Structure des fichiers

| Fichier | Rôle |
|---|---|
| `internal/spawn/spawn.go` (modifié) | `mountWorkspace` (orthographe unique d'une entrée d'argv), `mountMode`, `reportUnmountedMounts`, et son appel sur la branche d'attache |
| `internal/spawn/spawn_test.go` (modifié) | les huit cas du spec |
| `docs/superpowers/specs/2026-07-27-den-cli-design.md` (modifié) | §14.0 : la mesure du 2026-08-10 |

Aucun fichier créé : `spawn.go` porte déjà les trois fonctions sœurs (`reportDrift`, `reportMissingGitDirs`, `reportUnmountedRepos`, `reportNestChangedSinceCreation`) et la nouvelle appartient à ce groupe. La séparer serait une décomposition par couche technique, pas par responsabilité.

---

### Task 1 : `mountWorkspace` — une seule orthographe

**Files:**
- Modify: `internal/spawn/spawn.go:611-618` (la boucle des mounts) et la zone des helpers en fin de fichier
- Test: `internal/spawn/spawn_test.go`

**Interfaces:**
- Consomme : `nest.Mount{Host, Link, RO, Key}` (`internal/nest/resolve.go:36`)
- Produit : `func mountWorkspace(m nest.Mount) string` — la tâche 2 et la tâche 3 l'appellent

- [ ] **Step 1: écrire le test qui échoue**

À ajouter à la fin de `internal/spawn/spawn_test.go` :

```go
// mountWorkspace is the SINGLE spelling of a mount in the create argv. The
// report of task 2 compares against it, so a second copy of `host + ":ro"`
// would drift and make the warning fire on every attach with nothing changed.
func TestMountWorkspaceSpellsTheROSuffix(t *testing.T) {
	rw := mountWorkspace(nest.Mount{Host: "/h/docs", Key: "mounts[0]"})
	if rw != "/h/docs" {
		t.Errorf("read-write mount = %q, want %q", rw, "/h/docs")
	}
	ro := mountWorkspace(nest.Mount{Host: "/h/docs", RO: true, Key: "mounts[0]"})
	if ro != "/h/docs:ro" {
		t.Errorf("read-only mount = %q, want %q", ro, "/h/docs:ro")
	}
}
```

Vérifier que `internal/spawn/spawn_test.go` importe déjà `"github.com/PillowPillow/den/internal/nest"`. Sinon, l'ajouter au bloc d'import.

- [ ] **Step 2: lancer le test, vérifier qu'il échoue**

Run: `go test ./internal/spawn/ -run TestMountWorkspaceSpellsTheROSuffix -count=1`
Expected: FAIL — `undefined: mountWorkspace`

- [ ] **Step 3: écrire l'implémentation minimale**

Ajouter dans `internal/spawn/spawn.go`, juste au-dessus de `reportDrift` :

```go
// mountWorkspace renders ONE `mounts:` entry as `sbx create` receives it.
//
// `<path>:ro` is sbx's own read-only syntax (`sbx create --help`).
//
// It exists as a function, rather than inline in the workspace loop, because
// reportUnmountedMounts compares what the VM reports against this EXACT
// spelling. Two copies of `host + ":ro"` would drift one day, and the warning
// would then fire on every attach with nothing changed — a permanent warning
// stops being read, including the day it tells the truth. Same lesson already
// paid by stringNode (internal/agent/mixin.go).
func mountWorkspace(m nest.Mount) string {
	if m.RO {
		return m.Host + ":ro"
	}
	return m.Host
}
```

- [ ] **Step 4: brancher la boucle d'argv dessus**

Dans `internal/spawn/spawn.go`, remplacer le corps de la boucle (lignes 611-618) :

```go
	for _, m := range r.Mounts {
		// The `:ro` spelling lives in mountWorkspace, shared with
		// reportUnmountedMounts — see its comment for why it is not inline here.
		workspaces = append(workspaces, mountWorkspace(m))
	}
```

- [ ] **Step 5: lancer les tests, vérifier qu'ils passent**

Run: `go test ./internal/spawn/ -count=1`
Expected: PASS, `TestSpawnAppendsROSuffixForReadOnlyMounts` et `TestSpawnMountsEveryConfigMountAfterTheRepos` compris — ils couvrent déjà la boucle d'argv et prouvent que la refonte n'a rien changé.

- [ ] **Step 6: commit**

```bash
task check
git add internal/spawn/spawn.go internal/spawn/spawn_test.go
git commit -m "refactor: mountWorkspace — one spelling of a mount in the create argv (#56)"
```

---

### Task 2 : `reportUnmountedMounts` — le verdict « absent »

Couvre le cas 1 de l'issue (mount sans `link:` ajouté) et le repointage, plus le **silence**, qui est la moitié qui garde l'avertissement lisible.

**Files:**
- Modify: `internal/spawn/spawn.go` (nouvelle fonction, et son appel vers la ligne 692)
- Test: `internal/spawn/spawn_test.go`

**Interfaces:**
- Consomme : `mountWorkspace` (tâche 1), `live.Workspaces []string` (`internal/sbx/ls.go:56`), `r.Mounts []nest.Mount`
- Produit : `func reportUnmountedMounts(out io.Writer, sandboxName string, mounted []string, mounts []nest.Mount)` — la tâche 3 étend son corps

- [ ] **Step 1: écrire les tests qui échouent**

À ajouter à la fin de `internal/spawn/spawn_test.go` :

```go
// The blind spot #56 names: a mount with NO `link:` never reaches the mixin's
// link argv (LinkCommand filters it out), so the drift comparison of
// internal/agent stays silent on it. The VM's own workspace list is the
// primary source that does see it.
func TestSpawnWarnsWhenALiveSandboxDoesNotMountANewLinklessMount(t *testing.T) {
	docs := t.TempDir()
	denHome, repo := denTestMounts(t, "mounts:\n  - host: "+docs+"\n")

	// The sandbox is live and was created WITHOUT the mount: the day-2 case.
	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{Output: []byte(
		`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`)}
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callStartingWith(f, "create") != nil {
		t.Fatal("a live sandbox must be attached, never re-created")
	}

	log := out.String()
	if !strings.Contains(log, "does not mount what `mounts:` now says") {
		t.Errorf("log = %q, expected the mounts warning header", log)
	}
	if !strings.Contains(log, docs) {
		t.Errorf("log = %q, expected it to name the host path", log)
	}
	if !strings.Contains(log, "mounts[0]") {
		t.Errorf("log = %q, expected it to name the config key to fix — den's errors "+
			"name the key, and a user with several mounts cannot find the line otherwise", log)
	}
	if !strings.Contains(log, "den rm api") {
		t.Errorf("log = %q, expected the remedy", log)
	}
}

// A permanent warning stops being read: silence is the contract when the VM
// already carries exactly what `mounts:` says.
func TestSpawnDoesNotWarnWhenTheLiveSandboxMountsEveryConfiguredMount(t *testing.T) {
	docs := t.TempDir()
	denHome, repo := denTestMounts(t, "mounts:\n  - host: "+docs+"\n")

	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{Output: []byte(
		`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `","` + docs + `"]}]}`)}
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "does not mount what `mounts:` now says") {
		t.Errorf("log = %q, expected no mounts warning", out.String())
	}
}

// No `mounts:` at all must read the VM's workspace list for nothing and print
// nothing — the config that every den home starts with.
func TestSpawnDoesNotWarnAboutMountsWhenThereAreNone(t *testing.T) {
	denHome, repo := denTest(t)

	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{Output: []byte(
		`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`)}
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "does not mount what `mounts:` now says") {
		t.Errorf("log = %q, expected no mounts warning", out.String())
	}
}
```

- [ ] **Step 2: lancer les tests, vérifier qu'ils échouent**

Run: `go test ./internal/spawn/ -run 'TestSpawnWarnsWhenALiveSandboxDoesNotMountANewLinklessMount|TestSpawnDoesNotWarnWhenTheLiveSandboxMountsEveryConfiguredMount|TestSpawnDoesNotWarnAboutMountsWhenThereAreNone' -count=1`
Expected: le premier FAIL sur `expected the mounts warning header` ; les deux autres PASSENT déjà (rien n'imprime encore). C'est normal : ce sont des tests de non-régression pour les tâches 2 et 3.

**Lancer les trois, pas seulement le rouge.** Le test qui échoue est le SEUL qui prouve que l'appel de l'étape 4 existe : écrire la fonction sans la brancher laisse deux tests verts sur trois, et un implémenteur qui ne lance que les silencieux se croit arrivé.

- [ ] **Step 3: écrire l'implémentation minimale**

Ajouter dans `internal/spawn/spawn.go`, après `reportNestChangedSinceCreation` :

```go
// reportUnmountedMounts warns that a LIVE sandbox does not carry what
// `mounts:` says today.
//
// It exists because TWO edits to `mounts:` reach nothing else (#56):
//
//   - a mount with NO `link:` — legitimate, and the shape env-var consumers
//     want — is filtered out of the mixin's link argv by LinkCommand, so
//     agent.Differences cannot see it;
//   - a `ro:` flip is a `sbx create` flag, never present in the boot shell at
//     all.
//
// The primary source is the VM: `sbx ls --json` reports its workspaces WITH
// the `:ro` suffix (measured 2026-08-10, sbx v0.37.1 — spec §14.0). Nothing
// new has to be recorded on the host for this comparison to exist.
//
// UNLIKE reportMissingGitDirs and reportUnmountedRepos, the `:ro` suffix is
// NOT stripped before comparing: for a repo it is a mount option and noise,
// here it IS the bit under test.
//
// Warn, never refuse, never recreate — the doctrine of its three siblings.
// Mounts are fixed at create time, so the edit takes effect at the next
// create; refusing would break a `den spawn` that worked yesterday over a
// harmless YAML edit, and recreating would destroy work in progress.
//
// A mount REMOVED from the configuration stays deliberately out of scope:
// live.Workspaces is FLAT — repos, git dirs, agent profile and mounts are
// indistinguishable in it — so "on the VM, absent from the config" also fires
// on a moved worktree, a dropped repo and a flipped --agent. Telling them
// apart needs a manifest record, which the mounts design refused
// (2026-08-07-mounts-design.md:253-259).
//
// Deliberate overlap with the "link phase changed" line of agent.Differences:
// adding a mount that HAS a link fires both. They answer different questions,
// and Links remains the ONLY detector of a link-target-only edit (same host, new
// `link:`), which no workspace comparison can see.
func reportUnmountedMounts(out io.Writer, sandboxName string, mounted []string, mounts []nest.Mount) {
	if len(mounts) == 0 {
		return
	}
	present := make(map[string]bool, len(mounted))
	for _, w := range mounted {
		present[w] = true
	}
	var lines []string
	for _, m := range mounts {
		if present[mountWorkspace(m)] {
			continue
		}
		lines = append(lines, fmt.Sprintf("  - %s (%s) is not mounted\n", m.Host, m.Key))
	}
	if len(lines) == 0 {
		return // a permanent warning stops being read
	}
	fmt.Fprintf(out,
		"warning: sandbox %s does not mount what `mounts:` now says — mounts are fixed "+
			"at create time:\n", sandboxName)
	for _, l := range lines {
		fmt.Fprint(out, l)
	}
	fmt.Fprintf(out, "  `den rm %s` then relaunch to apply it.\n", sandboxName)
}
```

- [ ] **Step 4: appeler la fonction sur la branche d'attache**

Dans `internal/spawn/spawn.go`, juste après l'appel à `reportUnmountedRepos` (ligne 692) :

```go
		// After the repos, because the repos are what the user came for: a
		// mount is support material, and reading its warning first would bury
		// the line saying the code itself is not there.
		reportUnmountedMounts(d.Out, sandboxName, live.Workspaces, r.Mounts)
```

- [ ] **Step 5: lancer les tests, vérifier qu'ils passent**

Run: `go test ./internal/spawn/ -count=1`
Expected: PASS, les trois nouveaux compris.

- [ ] **Step 6: commit**

```bash
task check
git add internal/spawn/spawn.go internal/spawn/spawn_test.go
git commit -m "feat: warn when a live sandbox misses a mount the config now declares (#56)"
```

---

### Task 3 : le verdict « retourné » — `ro:` ↔ `rw:`

Sans lui, un `ro:` retourné produirait la ligne « is not mounted » à propos d'un répertoire qui **est** monté, en lecture seule : une affirmation fausse imprimée à chaque attache.

**Files:**
- Modify: `internal/spawn/spawn.go` (corps de `reportUnmountedMounts`, plus un helper `mountMode`)
- Test: `internal/spawn/spawn_test.go`

**Interfaces:**
- Consomme : `reportUnmountedMounts` (tâche 2), `mountWorkspace` (tâche 1)
- Produit : `func mountMode(ro bool) string` — rend `"read-only"` ou `"read-write"`

- [ ] **Step 1: écrire les tests qui échouent**

À ajouter à la fin de `internal/spawn/spawn_test.go` :

```go
// A `ro:` flip is a `sbx create` flag: it never reaches the boot shell, so the
// mixin comparison is blind to it. Both directions get their OWN line —
// saying "is not mounted" about a directory that IS mounted, read-only, is a
// false statement den would print on every attach.
func TestSpawnWarnsWhenAMountIsMountedWithTheOtherROBit(t *testing.T) {
	t.Run("config now says read-only", func(t *testing.T) {
		docs := t.TempDir()
		denHome, repo := denTestMounts(t, "mounts:\n  - host: "+docs+"\n    ro: true\n")

		// The VM mounts the bare host: it was created read-write.
		f, d := fakeDeps()
		f.Responses["ls --json"] = sbx.Response{Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `","` + docs + `"]}]}`)}
		var out bytes.Buffer
		d.Out = &out

		if err := Spawn(context.Background(), denHome,
			Options{Nest: "api", Detach: true}, d); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		log := out.String()
		if !strings.Contains(log, "is mounted read-write, but `mounts:` now says read-only") {
			t.Errorf("log = %q, expected the flip line naming BOTH sides", log)
		}
		if strings.Contains(log, "is not mounted") {
			t.Errorf("log = %q: the directory IS mounted — den must not claim otherwise", log)
		}
	})

	t.Run("config now says read-write", func(t *testing.T) {
		docs := t.TempDir()
		denHome, repo := denTestMounts(t, "mounts:\n  - host: "+docs+"\n")

		// The VM mounts it `:ro`: it was created read-only.
		f, d := fakeDeps()
		f.Responses["ls --json"] = sbx.Response{Output: []byte(
			`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `","` + docs + `:ro"]}]}`)}
		var out bytes.Buffer
		d.Out = &out

		if err := Spawn(context.Background(), denHome,
			Options{Nest: "api", Detach: true}, d); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		log := out.String()
		if !strings.Contains(log, "is mounted read-only, but `mounts:` now says read-write") {
			t.Errorf("log = %q, expected the flip line naming BOTH sides", log)
		}
		if strings.Contains(log, "is not mounted") {
			t.Errorf("log = %q: the directory IS mounted — den must not claim otherwise", log)
		}
	})
}
```

- [ ] **Step 2: lancer les tests, vérifier qu'ils échouent**

Run: `go test ./internal/spawn/ -run TestSpawnWarnsWhenAMountIsMountedWithTheOtherROBit -count=1`
Expected: FAIL sur `expected the flip line naming BOTH sides` — la tâche 2 imprime `is not mounted` dans les deux sous-cas.

- [ ] **Step 3: écrire l'implémentation minimale**

Dans `internal/spawn/spawn.go`, remplacer la boucle de `reportUnmountedMounts` :

```go
	for _, m := range mounts {
		want := mountWorkspace(m)
		if present[want] {
			continue
		}
		// The OTHER spelling of the same host. Tested before "not mounted",
		// because that message would otherwise be a false statement about a
		// directory the VM really does mount — printed on every attach, which
		// is how a warning stops being read.
		other := m.Host + ":ro"
		if m.RO {
			other = m.Host
		}
		if present[other] {
			lines = append(lines, fmt.Sprintf(
				"  - %s (%s) is mounted %s, but `mounts:` now says %s\n",
				m.Host, m.Key, mountMode(!m.RO), mountMode(m.RO)))
			continue
		}
		lines = append(lines, fmt.Sprintf("  - %s (%s) is not mounted\n", m.Host, m.Key))
	}
```

Et ajouter le helper, juste sous `reportUnmountedMounts` :

```go
// mountMode names a `ro:` bit the way the user reads it. Both sides are named
// in the flip line — "read-only" alone leaves the reader guessing which end of
// the sentence describes the VM.
func mountMode(ro bool) string {
	if ro {
		return "read-only"
	}
	return "read-write"
}
```

- [ ] **Step 4: lancer les tests, vérifier qu'ils passent**

Run: `go test ./internal/spawn/ -count=1`
Expected: PASS, les deux sous-tests compris.

- [ ] **Step 5: commit**

```bash
task check
git add internal/spawn/spawn.go internal/spawn/spawn_test.go
git commit -m "feat: name both sides of a ro:/rw: flip instead of claiming the mount is absent (#56)"
```

---

### Task 4 : verrouiller le sucre `ssh.dir`, et consigner la mesure au §14

**Files:**
- Test: `internal/spawn/spawn_test.go`
- Modify: `docs/superpowers/specs/2026-07-27-den-cli-design.md` (§14.0, le bloc `sbx ls`)

**Interfaces:**
- Consomme : `reportUnmountedMounts` (tâches 2 et 3), `denTestSSH` (`spawn_test.go:40`)
- Produit : rien de nouveau pour les tâches suivantes

- [ ] **Step 1: écrire le test qui échoue (ou qui passe déjà — voir l'étape 2)**

À ajouter à la fin de `internal/spawn/spawn_test.go` :

```go
// `ssh.mode: mount` is SUGAR: nest.resolveMounts desugars it into an ordinary
// `mounts:` entry, so editing ssh.dir under a live sandbox is covered by the
// same code with no branch of its own. Locked here because the day someone
// reintroduces an `if SSHMode == …` in spawn, this is the test that fails.
//
// The key printed is `ssh.dir`, NOT `mounts[0]`: the entry appears in no
// `mounts:` block of the user's config.yaml, and sending them to a key they
// never wrote is the defect nest.Mount.Key exists to prevent.
func TestSpawnWarnsAboutAnEditedSSHDirLikeAnyOtherMount(t *testing.T) {
	// CREATED, like every other `ssh.mode: mount` test in this file
	// (TestSpawnMountsTheRepoBeforeTheAgentProfileAndSSH): mount mode refuses a
	// missing ssh.dir before any side effect, and that refusal would abort this
	// spawn long before the warning under test.
	sshDir := filepath.Join(t.TempDir(), "ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	denHome, repo := denTestSSH(t, "  mode: mount\n  dir: "+sshDir+"\n")

	// The VM was created with a DIFFERENT ssh.dir — it does not mount this one.
	f, d := fakeDeps()
	f.Responses["ls --json"] = sbx.Response{Output: []byte(
		`{"sandboxes":[{"name":"api","status":"running","workspaces":["` + repo + `"]}]}`)}
	var out bytes.Buffer
	d.Out = &out

	if err := Spawn(context.Background(), denHome,
		Options{Nest: "api", Detach: true}, d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	log := out.String()
	if !strings.Contains(log, sshDir) {
		t.Errorf("log = %q, expected it to name the ssh dir the VM does not mount", log)
	}
	if !strings.Contains(log, "ssh.dir") {
		t.Errorf("log = %q, expected the key `ssh.dir`, not a mounts[N] the user never wrote", log)
	}
}
```

- [ ] **Step 2: lancer le test**

Run: `go test ./internal/spawn/ -run TestSpawnWarnsAboutAnEditedSSHDirLikeAnyOtherMount -count=1`
Expected: PASS **du premier coup**. C'est délibéré : le sucre passe par le même chemin de code, et ce test est un verrou de non-régression, pas un red-green. S'il ÉCHOUE, le désucrage de `nest.resolveMounts` ne fait pas ce que le spec dit et il faut s'arrêter là plutôt que d'adapter le test.

- [ ] **Step 3: consigner la mesure au §14.0**

Dans `docs/superpowers/specs/2026-07-27-den-cli-design.md`, sous le bloc `sbx ls [--json] [-q]` (vers la ligne 1050), ajouter :

```
  `workspaces` porte le suffixe `:ro` d'un mount read-only TEL QUEL (mesuré 2026-08-10, sbx
    v0.37.1, sandbox jetable créée avec `<path>:ro` puis détruite) :
      "workspaces": ["<…>/probe-rw", "<…>/probe-ro:ro"]
    C'est ce qui permet à spawn.reportUnmountedMounts de voir un `ro:` retourné sans rien
    enregistrer côté hôte (#56, spec 2026-08-10-mounts-drift-design.md).
  ⚠️ Cette machine porte sbx v0.37.1, alors que tout le reste du présent relevé date de v0.35.0.
    Le reste est à re-mesurer.
```

- [ ] **Step 4: lancer la suite complète**

Run: `task check`
Expected: lint, typecheck et tests passent.

- [ ] **Step 5: commit**

```bash
git add internal/spawn/spawn_test.go docs/superpowers/specs/2026-07-27-den-cli-design.md
git commit -m "test: lock ssh.dir on the mounts path, and record the :ro round-trip in §14 (#56)"
```

---

### Task 5 : ouvrir la PR

**Files:** aucun

- [ ] **Step 1: relire le diff complet**

```bash
git diff main...feat/mounts-drift
```

- [ ] **Step 2: vérifier que la suite passe une dernière fois**

Run: `task check`
Expected: PASS

- [ ] **Step 3: pousser et ouvrir la PR**

```bash
git push -u origin feat/mounts-drift
gh pr create --title "feat: mount drift is readable from the VM (#56)" --body "$(cat <<'EOF'
Closes #56.

The issue offered two homes for the missing data, "neither free": a new key in
the mixin YAML (sbx tolerance unmeasured), or a record in internal/manifest.

A third one exists and is nearly free: `sbx ls --json` reports workspaces with
the `:ro` suffix intact, and spawn already holds both sides at attach. Measured
2026-08-10 on sbx v0.37.1 with a throwaway sandbox; recorded in spec §14.0.

Covered: a link-less mount added, a mount repointed, a `ro:`/`rw:` flip (both
directions get their own line — claiming "not mounted" about a read-only mount
would be a false statement printed on every attach). `ssh.dir` rides the same
path through the `ssh.mode: mount` sugar.

Out of scope, deliberately: a mount REMOVED from the config. `live.Workspaces`
is flat, so the removal is indistinguishable from a moved worktree or a dropped
repo. Written up in the design doc with the reason.

Design: `docs/superpowers/specs/2026-08-10-mounts-drift-design.md`
EOF
)"
```

---

## Auto-revue

**Couverture du spec.** Les huit tests du spec sont placés : 1 et 2 (ajout, repointage) en tâche 2, 3 et 4 (les deux retournements) en tâche 3, 5 (silence) en tâche 2, 6 (`ssh.dir`) en tâche 4, 7 (aucun `mounts:`) en tâche 2, 8 (orthographe unique) en tâche 1. Les trois livrables du spec — `spawn.go`, `spawn_test.go`, §14 — ont chacun leur tâche. Pas de changement de README, comme le spec le dit.

**Placeholders.** Aucun « TBD », aucun « ajouter la gestion d'erreur », chaque étape de code porte son bloc.

**Cohérence des types.** `mountWorkspace(nest.Mount) string` est défini en tâche 1 et appelé sous ce nom en tâches 2 et 3. `mountMode(bool) string` est défini et appelé en tâche 3. `reportUnmountedMounts(io.Writer, string, []string, []nest.Mount)` est défini en tâche 2 et son corps étendu en tâche 3 avec la même signature. Les en-têtes de message assertés par les tests (`does not mount what `mounts:` now says`, `is mounted read-only, but `mounts:` now says read-write`) sont identiques mot pour mot entre les tests et l'implémentation.

**Écart assumé.** L'étape 2 de la tâche 2 déclare que deux des trois tests passent avant l'implémentation, et l'étape 2 de la tâche 4 qu'elle passe du premier coup. Ce sont des verrous de non-régression, pas des red-green, et c'est écrit à l'endroit où l'implémenteur pourrait croire à une erreur.
