# Marketplace Claude Code de den, premier lot — plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** le dépôt `PillowPillow/den` sert une marketplace Claude Code dont le plugin `den`, épinglé sur le tag de release, livre quatre skills (`den:setup`, `den:profile` « créer », `den:nest`, `den:troubleshoot`) gardés par trois tests Go.

**Architecture:** `.claude-plugin/marketplace.json` à la racine nomme le plugin par une source `git-subdir` (`path: plugins/den`, `ref: vX.Y.Z`). `plugins/den/` porte `plugin.json`, un fichier de règles communes, les skills et `reference/`, copies octet pour octet de pages de `docs/`. Un paquet de tests seul, `internal/plugin`, lit ces fichiers et échoue sur une copie divergente, une version ou un tag désaligné, une structure invalide. Aucun fichier Go ne vit sous `plugins/den/` : ce dossier est copié tel quel dans le cache de Claude Code.

**Tech Stack:** Go (`encoding/json`, `gopkg.in/yaml.v3`), go-task, Markdown (SKILL.md), CLI `claude` 2.1.329.

**Spec:** `docs/superpowers/specs/2026-11-20-den-marketplace-design.md`, validée par `docs/superpowers/specs/2026-11-20-den-marketplace-validation.md` (lignes A1 à B29) ; la fiche gagne sur la spec. Décision ajoutée le 2026-11-20 (HITL) : la nouvelle page de `docs/` ne prend que la section Prerequisites et le tableau Troubleshooting ; `den:setup` ne recopie ni les méthodes d'installation ni les commandes du jeton, il donne le lien vers les sections du README sur GitHub puis vérifie le résultat.

## Faits vérifiés avant ce plan

Sources primaires lues le 2026-11-20 :

- **`git-subdir`** : champs `url`, `path`, `ref`, `sha` ; « `ref`: a branch or tag » ; « Claude Code checks out only that subdirectory » (code.claude.com/docs/en/plugins/marketplace-reference.md). Forme observée dans `~/.claude/plugins/marketplaces/claude-plugins-official/.claude-plugin/marketplace.json` : 110 entrées, dont `{"source": "git-subdir", "url": "https://github.com/42Crunch-AI/claude-plugins.git", "path": "plugins/api-security-testing", "ref": "v1.5.5", …}`.
- **Même dépôt : non vérifié.** La doc dit « Use a relative path for a plugin in a subdirectory of the marketplace repository itself. Use `git-subdir` for a subdirectory of some other repository. » Elle n'interdit pas le même dépôt. La tâche 1 l'essaie avant tout le reste.
- **Commandes** : `claude plugin marketplace add <owner>/<repo>` et `claude plugin install <plugin>@<marketplace>` (create-marketplace.md, install.md ; `claude plugin --help` local). Mise à jour : `claude plugin marketplace update [name]` puis `claude plugin update <plugin>` (`--help` local).
- **`${CLAUDE_PLUGIN_ROOT}`** : « In a plugin skill, Claude Code substitutes `${CLAUDE_PLUGIN_ROOT}` and `${CLAUDE_PLUGIN_DATA}` » dans le corps Markdown (skills.md).
- **Cache** : « Files outside the plugin directory aren't copied » (loading.md).
- **Version** : `plugin.json` `version` d'abord, puis l'entrée de marketplace ; ne pas mettre `version` dans l'entrée (`claude plugin validate` avertit).
- **Démarrage** : « A marketplace that settings declare but `known_marketplaces.json` lacks: Claude Code clones it, then … downloads enabled plugins » ; « Until you save trust for the added directory, Claude Code skips its `extraKnownMarketplaces` entries » (loading.md). L'effet dans une VM reste à essayer (tâche 4).
- `claude --plugin-dir <path>` charge un plugin pour une session sans toucher `~/.claude/plugins` : les essais de skills l'utilisent.
- Un paquet Go qui ne contient que des `_test.go` passe `go build ./...`, `go vet ./...` et `go test ./...` (essayé dans le scratchpad).
- Pousser un tag `v*` déclenche `.github/workflows/release.yml` (publication et cask). **Aucun essai ne pousse un tag.**

## Global Constraints

- `task check` passe à la fin de chaque tâche.
- Aucun test n'appelle `t.Parallel()`, n'ouvre de socket, ni ne lance de processus. Les tests de `internal/plugin` lisent des fichiers seulement.
- Aucun fichier `.go` sous `plugins/den/`.
- SKILL.md, règles, messages et commentaires en anglais ; ce plan et le fichier des essais en français.
- Pages copiées : `den-yaml.md commands.md templates.md security.md ssh.md files.md setup.md`, de `docs/` vers `plugins/den/reference/`, octet pour octet. `migrating-from-v1.md` et `README.md` ne sont pas copiés.
- `plugin.json` porte la version de den sans `v` ; `marketplace.json` porte `ref: v` + cette version. Valeurs initiales : `2.0.0` et `v2.0.0` (dernière section de `CHANGELOG.md`). Le commit de release les monte.
- Pas de `sha` dans l'entrée `git-subdir` : un commit de release ne peut pas contenir son propre SHA.
- Pas de `version` dans l'entrée de marketplace.
- Aucun skill ne lit, n'affiche ni n'écrit le jeton ou une valeur de secret (B13), ne lance `den up` (B14), ne réécrit un `den.yaml` en entier (B15), n'élargit le réseau sans hôte nommé et diff vu (B16), ne lance `den rm` sans confirmation ni avec `--force` (B28), ne touche aux chemins de v1 (`config.yaml`, `stacks/`, `nests/`, `state/sandboxes/`).
- Les skills résolvent le den home par `${DEN_HOME:-$HOME/.den}`, jamais par un `~/.den` en dur dans une commande.
- `CHANGELOG.md` ne reçoit aucune section sur la branche : l'entrée s'écrit par `/release` (tâche 10).
- `.claude/commands/release.md` : `make` → `task check` est hors périmètre (suite nommée de la spec). Ne pas le faire.

## Review Focus

1. Le `ref` oublié au commit de release : `marketplace.json` pointe un ancien tag, le coéquipier reçoit d'anciens skills. Attendu : le test échoue avant le commit (tâche 2, `TestMarketplaceRefIsReleaseTag` ; tâche 9, `release.md` lance ce test avant le commit).
2. Une page de `docs/` modifiée sans `task plugin-sync` : attendu, `task check` rouge avec le remède nommé (tâche 3, `TestReferenceCopiesMatchDocs`).
3. Un essai de skill avec `DEN_HOME` jetable qui écrit dans le vrai `~/.den` : attendu, aucun fichier du vrai den home modifié (tâche 8, vérification `find ~/.den -newer <repère>` vide).
4. Un essai qui crée une instance `demo` alors qu'une VM `demo` existe (Foreign) : attendu, les essais utilisent le nest `mkt-trial` et le suppriment ensuite (tâches 4 et 8).
5. Un `settings.json` d'hôte avec `env`, `hooks`, `statusLine` : attendu, aucune valeur de `env` n'apparaît dans la conversation, et les clés d'hôte sont retirées par défaut (tâche 6, essai consigné en tâche 8).

---

### Task 1: Essai — épinglage `git-subdir` sur le même dépôt, substitution et cache

Bloquante pour toute la suite (A5). Elle ne modifie pas le dépôt ; elle modifie la configuration Claude de l'hôte, puis la remet en état.

**Files:**
- Create: `docs/superpowers/specs/2026-11-20-den-marketplace-runs.md` (section « Essai 1 — épinglage »)

- [ ] **Step 1: Préparer un clone jetable**

```bash
S=<scratchpad>/pin-trial && rm -rf "$S" && git clone -q /Users/polochon/Development/Pillow/den "$S/den"
```
Dans `$S/den`, écrire `plugins/den/.claude-plugin/plugin.json` (`{"name":"den","version":"0.0.1","description":"trial"}`), un skill `plugins/den/skills/probe/SKILL.md` (frontmatter `name: probe`, `description: Trial skill`, corps : `Reply with exactly this line and nothing else: ROOT=${CLAUDE_PLUGIN_ROOT}`), et `.claude-plugin/marketplace.json` :

```json
{"name":"den","owner":{"name":"PillowPillow"},"plugins":[{"name":"den","description":"trial","source":{"source":"git-subdir","url":"file://<S>/den","path":"plugins/den","ref":"plugin-trial"}}]}
```
Commit dans le clone, puis `git tag plugin-trial` (tag local au clone, jamais poussé, pas de préfixe `v`).

- [ ] **Step 2: Valider hors ligne** — `claude plugin validate "$S/den"`. Attendu : aucune erreur.

- [ ] **Step 3: Installer (HITL : l'humain approuve la modification de `~/.claude/plugins`)**

```bash
claude plugin marketplace add "$S/den"
claude plugin install den@den
ls ~/.claude/plugins/cache/den/den/*/
claude -p "/den:probe"
```
Attendu : le dossier du cache contient `.claude-plugin/` et `skills/`, et ni `go.mod` ni `internal/` ; la réponse est `ROOT=` suivi d'un chemin sous `~/.claude/plugins/cache/den/den/`.

- [ ] **Step 4: Remettre en état**

```bash
claude plugin uninstall den@den
claude plugin marketplace remove den
```
Attendu : `claude plugin marketplace list` ne nomme plus `den`.

- [ ] **Step 5: Consigner** dans le fichier des essais : commandes lancées, sortie observée, et ce qui reste non vérifié (l'URL `https://github.com/PillowPillow/den.git` au lieu de `file://`, vérifiée à la première release). **Si l'installation échoue, arrêter le plan et présenter le résultat à l'humain** : A5 interdit de servir `main`, aucun repli n'est à choisir seul.

- [ ] **Step 6: Commit**

```bash
git add docs/superpowers/specs/2026-11-20-den-marketplace-runs.md
git commit -m "docs: record the git-subdir pin trial for the den marketplace"
```

---

### Task 2: Manifestes et tests de structure, de version et de tag

**Files:**
- Create: `.claude-plugin/marketplace.json`
- Create: `plugins/den/.claude-plugin/plugin.json`
- Create: `plugins/den/rules.md` (contenu en tâche 5 ; ici une ligne de titre `# Rules for every den skill`)
- Test: `internal/plugin/plugin_test.go`

**Interfaces:**
- Produces: `func moduleRoot(t *testing.T) string` (même marche que `internal/worktree/hermeticity_test.go`), `func readPluginVersion(t *testing.T, root string) string`, utilisés par la tâche 3. Paquet `plugin`, fichiers `_test.go` seulement.

- [ ] **Step 1: Écrire les tests**

- `TestMarketplaceManifest` : décodage strict (`json.Decoder.DisallowUnknownFields`) de `.claude-plugin/marketplace.json` dans une struct `{Name; Owner{Name}; Plugins[]{Name; Description; Source{Source, URL, Path, Ref}}}`. Assertions : `Name == "den"`, `Owner.Name == "PillowPillow"`, un seul plugin, `Plugins[0].Name == "den"`, `Source.Source == "git-subdir"`, `Source.URL == "https://github.com/PillowPillow/den.git"`, `Source.Path == "plugins/den"`. Une clé `sha` ou `version` fait échouer le décodage.
- `TestPluginManifest` : décodage strict de `plugins/den/.claude-plugin/plugin.json` (`name`, `version`, `description`) ; `name == "den"`, `version` correspond à `^\d+\.\d+\.\d+$`, `description` non vide.
- `TestPluginVersionMatchesChangelog` : la première ligne de `CHANGELOG.md` qui correspond à `^## v(\d+\.\d+\.\d+) ` donne la version ; elle égale celle de `plugin.json`. Message d'échec : `plugin.json version %q differs from CHANGELOG.md's latest section v%s: bump plugins/den/.claude-plugin/plugin.json in the release commit`.
- `TestMarketplaceRefIsReleaseTag` : `Source.Ref == "v" + version`. Message : `marketplace.json ref %q is not v%s: the release commit must move it with plugin.json`.
- `TestSkillsHaveFrontmatter` : pour chaque `plugins/den/skills/*/SKILL.md`, le fichier commence par `---\n`, le bloc jusqu'au `---` suivant se décode en `{name, description}` ; `name` égale le nom du dossier ; `description` non vide ; le corps contient `${CLAUDE_PLUGIN_ROOT}/rules.md`. Le test vérifie les skills présents et exige au moins un dossier seulement après la tâche 5 : jusque-là, zéro skill passe.
- `TestNoGoFileInPlugin` : aucun fichier `*.go` sous `plugins/den/`.

- [ ] **Step 2: `go test ./internal/plugin/ -count=1`** — FAIL (fichiers absents).

- [ ] **Step 3: Écrire les deux manifestes** — `marketplace.json` : `name: den`, `owner.name: PillowPillow`, `description` du plugin en une phrase anglaise, source `git-subdir` avec `ref: "v2.0.0"`. `plugin.json` : `name: den`, `version: "2.0.0"`, `description` identique.

- [ ] **Step 4: `go test ./internal/plugin/ -count=1`** — PASS. Puis `claude plugin validate .` — aucune erreur.

- [ ] **Step 5: `task check`** — PASS.

- [ ] **Step 6: Commit**

```bash
git add .claude-plugin plugins/den internal/plugin
git commit -m "feat: den plugin manifests pinned on the release tag, with their tests"
```

---

### Task 3: Page `docs/setup.md`, copies de référence et `task plugin-sync`

**Files:**
- Create: `docs/setup.md`
- Modify: `README.md` (sections Prerequisites et Troubleshooting, table Documentation)
- Create: `plugins/den/reference/{den-yaml,commands,templates,security,ssh,files,setup}.md`
- Modify: `Taskfile.yml` (tâche `plugin-sync`)
- Test: `internal/plugin/reference_test.go`

**Interfaces:**
- Consumes: `moduleRoot` (tâche 2).
- Produces: les ancres `docs/setup.md#prerequisites` et `docs/setup.md#troubleshooting`, et `reference/setup.md`, lues par les skills des tâches 5 à 7.

- [ ] **Step 1: Écrire `TestReferenceCopiesMatchDocs`** — `pages := []string{"den-yaml.md", "commands.md", "templates.md", "security.md", "ssh.md", "files.md", "setup.md"}`. Pour chaque page : `bytes.Equal(docs/<p>, plugins/den/reference/<p>)`, sinon `t.Errorf("plugins/den/reference/%s differs from docs/%s: run `task plugin-sync`", p, p)`. Puis la liste des `*.md` de `plugins/den/reference/` égale `pages` (un fichier en trop ou absent échoue, message `reference/ must hold exactly %v`).

- [ ] **Step 2: `go test ./internal/plugin/ -run TestReferenceCopiesMatchDocs -count=1`** — FAIL.

- [ ] **Step 3: Créer `docs/setup.md`** — titre `# Host setup and troubleshooting`, puis `## Prerequisites` (la section entière du README : plateformes, tableau, paragraphe `sbx policy init`) et `## Troubleshooting` (le tableau entier). Réécrire les liens pour leur nouvel emplacement : `docs/x.md` → `x.md` ; `#install` → `../README.md#install` ; `#log-claude-in` → `../README.md#log-claude-in`. Aucune autre modification du texte.

- [ ] **Step 4: Modifier `README.md`** — sous `## Prerequisites` et `## Troubleshooting`, une phrase chacune qui renvoie à `docs/setup.md#prerequisites` et `docs/setup.md#troubleshooting` ; les titres restent, pour que le chemin manuel se lise dans l'ordre. Ajouter la ligne `| [docs/setup.md](docs/setup.md) | prerequisites and troubleshooting |` en tête de la table Documentation. Vérifier : `grep -rn 'README.md#prerequisites\|README.md#troubleshooting' --include='*.md' --include='*.go' --include='*.sh' . | grep -v docs/superpowers` ne rend rien.

- [ ] **Step 5: Ajouter la tâche `plugin-sync` au `Taskfile.yml`**, avec un commentaire « pourquoi » à la densité du fichier (copies et non liens symboliques : leur sort dans le cache du plugin est inconnu ; la liste doit égaler `pages` de `reference_test.go`, que le test fait respecter) :

```yaml
  plugin-sync:
    desc: Copy the docs/ pages the den plugin reads into plugins/den/reference/
    cmds:
      - for p in den-yaml commands templates security ssh files setup; do cp "docs/$p.md" "plugins/den/reference/$p.md"; done
```

- [ ] **Step 6: `task plugin-sync && go test ./internal/plugin/ -count=1`** — PASS. Contre-épreuve : ajouter une ligne à `docs/ssh.md`, `go test ./internal/plugin/ -count=1` → FAIL nommant `task plugin-sync` ; annuler la ligne.

- [ ] **Step 7: `task check`** — PASS.

- [ ] **Step 8: Commit**

```bash
git add docs/setup.md README.md Taskfile.yml plugins/den/reference internal/plugin/reference_test.go
git commit -m "docs: move prerequisites and troubleshooting to docs/setup.md, copied into the den plugin"
```

---

### Task 4: Essai — `enabledPlugins` et `extraKnownMarketplaces` dans une VM

Décide le défaut de `den:profile` pour ces deux clés (B22). HITL : l'humain lance `den up` et regarde `/plugin`.

**Files:**
- Modify: `docs/superpowers/specs/2026-11-20-den-marketplace-runs.md` (section « Essai 2 — plugins dans une VM »)

- [ ] **Step 1: Préparer un den home jetable** — `export DEN_HOME=<scratchpad>/vm-trial/den-home` ; un repo git vide `<scratchpad>/vm-trial/repo` ; un kit `$DEN_HOME/kits/trial/` avec `spec.yaml` minimal (`schemaVersion: "2"`, `kind: mixin`, `name: trial`, `version: 1.0.0`) et `files/home/.claude/settings.json` :

```json
{"extraKnownMarketplaces":{"caveman":{"source":{"source":"github","repo":"JuliusBrussee/caveman"}}},"enabledPlugins":{"caveman@caveman":true}}
```
`$DEN_HOME/den.yaml` : `schema: 1`, `default_template: claude-code-docker`, `profiles: {trial: [<DEN_HOME>/kits/trial]}`, `profile: trial`, nest `mkt-trial` avec le repo jetable. Vérifier d'abord `sbx ls` : aucune VM `mkt-trial`.

- [ ] **Step 2: Créer l'instance** — `den exec mkt-trial -- true` (en arrière-plan : la première création tire l'image). Attendu : code 0.

- [ ] **Step 3: Observer (HITL)** — l'humain lance `DEN_HOME=… den up mkt-trial`, accepte ou non les invites, tape `/plugin`, quitte. Puis :

```bash
den exec mkt-trial -- ls /home/agent/.claude/plugins/cache
den exec mkt-trial -- cat /home/agent/.claude/plugins/known_marketplaces.json
sbx policy check network github.com:443 --sandbox mkt-trial
```

- [ ] **Step 4: Classer le résultat** — un des quatre : installé sans action ; bloqué par l'invite de confiance ; bloqué par le réseau (`github.com` refusé) ; autre (sortie citée). Règle de décision : **gardées par défaut** seulement si le plugin s'installe sans modifier le réseau ni répondre à une invite autre que la confiance du dossier ; sinon **retirées par défaut**, et `den:profile` dit la cause observée (avec la cause réseau, il propose de les garder si l'utilisateur nomme l'hôte, selon B16).

- [ ] **Step 5: Nettoyer** — `den rm mkt-trial` (instance créée par cet essai, sans `--force`), puis `rm -rf <scratchpad>/vm-trial`. Attendu : `sbx ls` ne nomme plus `mkt-trial`.

- [ ] **Step 6: Consigner** (commandes, sorties, classe, décision) et commit :

```bash
git add docs/superpowers/specs/2026-11-20-den-marketplace-runs.md
git commit -m "docs: record the plugins-in-a-VM trial that sets den:profile's default"
```

---

### Task 5: Règles communes et `den:setup`

**Files:**
- Modify: `plugins/den/rules.md`
- Create: `plugins/den/skills/setup/SKILL.md`
- Modify: `internal/plugin/plugin_test.go` (`TestSkillsHaveFrontmatter` exige au moins un skill)

- [ ] **Step 1: Durcir le test** — `TestSkillsHaveFrontmatter` échoue sur zéro dossier sous `skills/`. `go test ./internal/plugin/ -count=1` → FAIL.

- [ ] **Step 2: Écrire `rules.md`** — règles numérotées, en anglais, chacune une instruction :
  1. Den home : `${DEN_HOME:-$HOME/.den}` dans toute commande ; jamais un `~/.den` en dur.
  2. Version : lancer `den version` (sortie `den vX.Y.Z`) et lire `${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json`. `den` absent : renvoyer à `/den:setup` (sauf dans `den:setup`). `den --help` qui liste `doctor` ou `init` : c'est v1, s'arrêter et le dire (`reference/setup.md#troubleshooting`). Majeure ou mineure différentes : le dire, donner la commande qui met à jour le plus ancien (den : le lien `https://github.com/PillowPillow/den#install` ; plugin : `claude plugin marketplace update den` puis `claude plugin update den@den`), et continuer (B11).
  3. Faits : lire la page utile de `${CLAUDE_PLUGIN_ROOT}/reference/` au moment d'agir ; n'énoncer aucun fait de den absent de ces pages.
  4. Vérifier par une commande ce qu'une commande établit (`sbx version`, `sbx policy ls`, `sbx secret ls`, `den ls`, `sbx ls`).
  5. `den.yaml` : édition minimale, diff montré, accord, puis écriture ; jamais de réécriture entière.
  6. Secrets : ne jamais lire, afficher ni écrire le jeton ou une valeur de secret ; une commande interactive ou qui touche un secret est donnée à l'utilisateur pour son terminal, puis vérifiée.
  7. `den up` : jamais lancé ; créer et vérifier par `den exec <nest> -- true` en arrière-plan, puis donner `den up <nest>`.
  8. `den rm` : seulement après confirmation, jamais `--force`.
  9. Réseau : n'ajouter un hôte à `network.allow` ou à `sbx policy` que si l'utilisateur l'a nommé et a vu la modification.
  10. `den.yaml` de projet trouvé depuis le dossier courant : le montrer (repos montés, hôtes ouverts, template, kits) avant toute création ; voir `reference/security.md`.
  11. Ne jamais toucher `config.yaml`, `stacks/`, `nests/`, `state/sandboxes/` du den home (v1).
  12. Skills existants seulement : n'annoncer que `/den:setup`, `/den:profile` (créer un profil), `/den:nest`, `/den:troubleshoot`.

- [ ] **Step 3: Écrire `skills/setup/SKILL.md`** — frontmatter `name: setup`, `description` de déclenchement (« Use when installing den for the first time, or when den, sbx or the Claude token is not set up yet… »). Corps : `Read ${CLAUDE_PLUGIN_ROOT}/rules.md and follow it.`, puis les étapes de la spec § den:setup :
  1. Prérequis, chacun par la commande « Check » de `reference/setup.md#prerequisites` : plateforme, `sbx version` ≥ v0.53.0, `sbx ls`, `sbx policy ls` puis `sbx policy check network example.com:443` = `Denied`, `claude --version`, `git --version`. Pour chaque manque : la commande « Install » de la page, donnée à l'utilisateur. Politique absente : proposer `sbx policy init balanced`, dire qu'elle vaut pour toutes les VM du poste, la lancer après accord (B17).
  2. den : `command -v den` puis la règle de version. Absent : donner `https://github.com/PillowPillow/den#install`, attendre, revérifier.
  3. Jeton : `sbx secret ls` ; aucun secret pour `api.anthropic.com` : donner `https://github.com/PillowPillow/den#log-claude-in`, attendre, revérifier par `sbx secret ls`.
  4. Nest : chercher un `den.yaml` en remontant depuis le dossier courant ; trouvé : le montrer (règle 10) et proposer ses nests (B19). Sinon : demander les repos, puis créer ou éditer `<den home>/den.yaml` (règle 5) avec `schema: 1`, `default_template: claude-code-docker` si aucun template n'est déclaré, et le nest.
  5. `den exec <nest> -- true` en arrière-plan (plusieurs minutes au premier tirage), puis donner `den up <nest>`.
  6. Proposer `/den:profile`.

- [ ] **Step 4: `go test ./internal/plugin/ -count=1`** — PASS ; `claude plugin validate plugins/den` — aucune erreur ; `task check` — PASS.

- [ ] **Step 5: Commit**

```bash
git add plugins/den/rules.md plugins/den/skills/setup internal/plugin/plugin_test.go
git commit -m "feat: den:setup skill and the rules every den skill follows"
```

---

### Task 6: `den:profile` — créer un profil

Dépend de la tâche 4 (défaut de `enabledPlugins` et `extraKnownMarketplaces`).

**Files:**
- Create: `plugins/den/skills/profile/SKILL.md`

- [ ] **Step 1: Écrire le SKILL.md** — `name: profile` ; `description` de déclenchement (créer un profil depuis la configuration Claude de l'hôte, ou y ranger des kits existants comme `~/.den/kits/pro`, A2) ; ne nomme que le travail « créer ». Corps, après la ligne `rules.md` :
  1. Lire `reference/den-yaml.md#profiles`. Demander le nom du profil ; refuser un nom déjà défini sous `profiles:` sans accord. Un kit qui existe déjà sous `<den home>/kits/<nom>` (avec `spec.yaml`) se déclare tel quel, sans copie.
  2. Lister `~/.claude/` de l'hôte et proposer, un par un, `CLAUDE.md`, `skills/`, `agents/`, `output-styles/`, `settings.json`, vers `<den home>/kits/<nom>/files/home/.claude/`. Ne jamais copier `~/.claude.json`, `.credentials.json`, `projects/`, `history.jsonl`, `todos/`, `statsig/`, `shell-snapshots/`, `plugins/`, ni aucun autre fichier d'état ou d'historique.
  3. `settings.json` clé par clé, avec l'utilisateur. Retirées par défaut et signalées : `hooks`, `statusLine`, `permissions.additionalDirectories` dont une entrée est un chemin absolu, `sandbox` (B21). `env` : montrer les **noms** seulement (par exemple `jq 'env | keys'`), jamais les valeurs ; garder ceux que l'utilisateur choisit (B29). `enabledPlugins` et `extraKnownMarketplaces` : le défaut et sa cause viennent de l'essai de la tâche 4.
  4. Écrire `spec.yaml` minimal (`schemaVersion: "2"`, `kind: mixin`, `name: <nom>`, `version: 1.0.0`).
  5. Ajouter `profiles.<nom>: [<den home>/kits/<nom>]` au fichier global (règle 5) et, sur accord, `profile: <nom>`.
  6. `den ls` : nommer les instances qui doivent être recréées (`den rm <instance>` puis `den up <nest>`) ; `den rm` seulement après confirmation (règle 8).

- [ ] **Step 2: `go test ./internal/plugin/ -count=1`**, `claude plugin validate plugins/den`, `task check` — PASS.

- [ ] **Step 3: Commit**

```bash
git add plugins/den/skills/profile
git commit -m "feat: den:profile skill creates a profile kit from the host's Claude configuration"
```

---

### Task 7: `den:nest` et `den:troubleshoot`

**Files:**
- Create: `plugins/den/skills/nest/SKILL.md`
- Create: `plugins/den/skills/troubleshoot/SKILL.md`

- [ ] **Step 1: Écrire `nest/SKILL.md`** — `name: nest`. Corps : lire `reference/den-yaml.md` ; choisir le fichier (global ou projet, `reference/den-yaml.md#project-file`) ; éditer repos (chemin ou origine, `#repos`), repos optionnels (`#optional-repos`), `network.allow` (règle 9), `ports`, `template` (`reference/templates.md#choosing-a-template-for-a-nest`), `profile` ; diff, accord, écriture (règle 5). Après une modification d'un nest qui a une instance (`den ls`) : citer `#changing-a-nest` et proposer `den rm` puis `den up` (règle 8). Validation : `den exec <nest> -- true` (B5 : pas de `den check`).

- [ ] **Step 2: Écrire `troubleshoot/SKILL.md`** — `name: troubleshoot`. Corps : partir du symptôme ; le rapprocher d'une ligne de `reference/setup.md#troubleshooting` ou de `reference/ssh.md#troubleshooting` ; établir la cause par une commande (`sbx policy check network <host>:<port> --sandbox <instance>`, `ssh-add -l`, `den ls`, `sbx ls`, `den version`, `sbx version`) ; proposer la correction de la page ; l'appliquer après accord quand elle modifie un fichier ou supprime une instance (B18), et seulement par les règles 5, 8 et 9. Symptôme absent des deux pages : le dire, ne pas inventer de cause (règle 3).

- [ ] **Step 3: `go test ./internal/plugin/ -count=1`**, `claude plugin validate plugins/den`, `task check` — PASS.

- [ ] **Step 4: Commit**

```bash
git add plugins/den/skills/nest plugins/den/skills/troubleshoot
git commit -m "feat: den:nest and den:troubleshoot skills"
```

---

### Task 8: Essais réels des quatre skills

HITL : l'humain lance chaque session et répond aux questions du skill.

**Files:**
- Modify: `docs/superpowers/specs/2026-11-20-den-marketplace-runs.md` (section « Essai 3 — skills »)

- [ ] **Step 1: Préparer** — `touch <scratchpad>/runs/marker` ; `export DEN_HOME=<scratchpad>/runs/den-home` (dossier absent) ; un repo jetable `<scratchpad>/runs/repo` ; `sbx ls` : aucune VM `mkt-trial`. Lancer depuis `<scratchpad>/runs` (aucun `den.yaml` de projet au-dessus) : `claude --plugin-dir /Users/polochon/Development/Pillow/den/plugins/den`.

- [ ] **Step 2: Lancer chaque skill une fois**
  - `/den:setup` : nest `mkt-trial` sur le repo jetable ; attendu : `den.yaml` créé sous `$DEN_HOME` après diff, `den exec mkt-trial -- true` réussi, `den up mkt-trial` donné, jamais lancé.
  - `/den:profile` : profil `trial` ; attendu : noms de `env` sans valeurs, clés d'hôte retirées par défaut, `profiles:` ajouté après diff, `mkt-trial` nommé à recréer.
  - `/den:nest` : ajouter `ports` ou un repo optionnel à `mkt-trial` ; attendu : édition minimale, commentaires conservés.
  - `/den:troubleshoot` : symptôme « a host is unreachable from the VM » ; attendu : `sbx policy check network … --sandbox mkt-trial` lancé, aucune règle ajoutée sans hôte nommé.

- [ ] **Step 3: Vérifier l'isolement** — `find ~/.den -newer <scratchpad>/runs/marker` ne rend rien. Puis `den rm mkt-trial` (sans `--force`), `sbx ls` sans `mkt-trial`.

- [ ] **Step 4: Corriger** — chaque écart observé devient une modification du SKILL.md ou de `rules.md`, relancée une fois.

- [ ] **Step 5: Consigner** — par skill : ce qui a été lancé, ce qui a été observé, ce qui reste non vérifié. Les branches « prérequis manquant » de `den:setup` sont marquées « non vérifié — premier coéquipier » (B25).

- [ ] **Step 6: `task check`** — PASS. Commit :

```bash
git add docs/superpowers/specs/2026-11-20-den-marketplace-runs.md plugins/den
git commit -m "docs: record one real run of each den skill"
```

---

### Task 9: README, CLAUDE.md et commande de release

**Files:**
- Modify: `README.md` (section « With Claude Code » avant « First run »)
- Modify: `CLAUDE.md`
- Modify: `.claude/commands/release.md`

- [ ] **Step 1: README** — `## With Claude Code`, avant `## First run` (B26) : les deux commandes `claude plugin marketplace add PillowPillow/den` et `claude plugin install den@den`, puis `/den:setup` dans Claude Code ; une phrase : le plugin sert les skills de la dernière release de den ; la ligne de `.claude/settings.json` de projet qui déclare la marketplace pour une équipe (`extraKnownMarketplaces` → `den` → `{"source": {"source": "github", "repo": "PillowPillow/den"}}`), forme à revérifier dans la doc settings de Claude Code avant de l'écrire. Le chemin manuel reste.

- [ ] **Step 2: CLAUDE.md** — dans « What this is » ou une section courte « Plugin » : `.claude-plugin/marketplace.json` et `plugins/den/` (skills, `rules.md`, `reference/`), épinglage `git-subdir` sur `vX.Y.Z` ; « une page de `docs/` copiée sous `plugins/den/reference/` se recopie par `task plugin-sync` avant le commit, sinon `internal/plugin` échoue » ; ajouter `task plugin-sync` au bloc Commands ; aucun fichier Go sous `plugins/den/`.

- [ ] **Step 3: release.md**
  - `allowed-tools` : ajouter `Bash(go test:*)`.
  - Préflight : « a release commit must carry the changelog and nothing else » devient « the changelog, `plugins/den/.claude-plugin/plugin.json` and `.claude-plugin/marketplace.json`, and nothing else ».
  - Après l'étape 4, une étape « Bump the plugin » : `version` de `plugin.json` = `X.Y.Z`, `ref` de `marketplace.json` = `vX.Y.Z`, puis `go test ./internal/plugin/ -count=1` doit passer avant le commit (un `ref` oublié échouerait sinon en CI après le push du tag).
  - Étape 5 : `git add CHANGELOG.md plugins/den/.claude-plugin/plugin.json .claude-plugin/marketplace.json`.

- [ ] **Step 4: `task check`** — PASS. Commit :

```bash
git add README.md CLAUDE.md .claude/commands/release.md
git commit -m "docs: den plugin in the README and CLAUDE.md; the release bumps the plugin"
```

---

### Task 10: Release du premier lot (HITL, s'arrête avant le push)

Après la fusion de la branche dans `main`.

- [ ] **Step 1: `/release minor`** — B7. L'étape 4 de `/release` écrit l'entrée de `CHANGELOG.md` ; ligne attendue sous `### Added` : « A Claude Code plugin: `claude plugin marketplace add PillowPillow/den`, then `claude plugin install den@den`, gives `/den:setup`, `/den:profile`, `/den:nest` and `/den:troubleshoot`. » La nouvelle étape monte `plugin.json` et `ref`.
- [ ] **Step 2:** `/release` s'arrête à son étape 7. L'humain donne le go du push.
- [ ] **Step 3: Après publication** — sur un poste, `claude plugin marketplace add PillowPillow/den`, `claude plugin install den@den`, `/den:setup` répond ; consigner dans le fichier des essais la vérification de l'URL GitHub restée ouverte en tâche 1.
