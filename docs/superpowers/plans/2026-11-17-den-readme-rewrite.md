# Réécriture du README de den V2 — plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal :** un README court qui mène un inconnu sur macOS jusqu'à un `den up` qui marche, et toute la
référence déplacée dans sept fichiers de `docs/`, chacun relu par un agent Fable.

**Architecture :** documentation seulement. Le README actuel est découpé : chaque section part dans
le fichier de `docs/` qui la possède, puis le README est réécrit autour du parcours d'un inconnu.
Chaque document passe une revue Fable (le « test » de ce plan) avant le suivant.

**Tech Stack :** Markdown (GitHub), `task build` pour le binaire, sbx v0.53.0 pour les vérifications.

**Spec :** `docs/superpowers/specs/2026-11-17-den-readme-rewrite-design.md`

## Global Constraints

- Branche `docs/readme-rewrite`, dans le worktree `.claude/worktrees/readme-rewrite`. Jamais de
  commit dans le checkout principal : un autre agent y travaille.
- README et `docs/*.md` en **anglais** ; ce plan et la spec en français.
- macOS seulement. sbx v0.53.0 ; `sbx env`, `kit` et `skills` sont expérimentaux.
- Installation : V2 depuis la branche `v2` avec `task build`. Une ligne dit que Homebrew,
  `install.sh` et `go install …@latest` installent v1, et que `den version` distingue les deux.
- Politique réseau globale recommandée : `sbx policy init balanced`.
- Connexion de Claude, dans cet ordre : jeton (`CLAUDE_CODE_OAUTH_TOKEN` d'abonnement, clé d'API si
  vérifiée), `/login` en dernier recours (reconnexion à chaque recréation).
- SSH, défaut pour un inconnu : « chargez seulement les clés que vos agents peuvent utiliser » ;
  `ssh.agentForwardingEnabled` est l'interrupteur ; « toutes les clés » est une option avec son risque.
- `.git` en écriture sans `-w` : risque résiduel accepté, écrit.
- Aucun changement de code. Une contradiction README/code va dans la liste « Tickets » du rapport
  final, pas dans le code.
- Chaque affirmation est vérifiée contre le code, `sbx … --help` ou une exécution ; sinon retirée ou
  marquée « unverified ».
- Une règle n'est écrite qu'à un endroit : le README la résume en une ligne et renvoie au document.
- Aucun lien public vers `docs/superpowers/`.

## Outils communs

**Contrôle des noms privés** (doit ne rien renvoyer) :

```bash
grep -rniE 'digitaleo|polochon|/Users/|docs/superpowers' README.md examples/ $(ls docs/*.md 2>/dev/null)
```

**Contrôle des liens relatifs** (doit ne rien renvoyer) :

```bash
for f in README.md docs/*.md; do
  grep -oE '\]\(([^)#:]+)(#[^)]*)?\)' "$f" | sed -E 's/^\]\(([^)#]+).*/\1/' | while read -r l; do
    [ -e "$(dirname "$f")/$l" ] || echo "$f -> $l"
  done
done
```

**Revue Fable** (le test de chaque tâche documentaire) : un agent `general-purpose`, `model: fable`,
avec ce prompt, où `<DOC>` et `<USAGES>` viennent de la tâche :

> You are a developer who has never used den. You found the public repo and read `<DOC>` in
> /Users/polochon/Development/Pillow/den/.claude/worktrees/readme-rewrite/. Read-only: never run
> den, never run an sbx command that creates, changes or removes anything; `sbx … --help`,
> `sbx policy ls`, `sbx settings ls` are fine. Do not read ~/.ssh or ~/.den.
> For each use below, say whether `<DOC>` (and the docs it links to) lets you do it without
> guessing; if not, say what is missing. Then check every factual claim against the Go code under
> internal/ and cmd/ and against sbx help; report each false claim with file:line on both sides.
> Report undefined terms and dead links. Uses: `<USAGES>`.
> Output: uncovered uses, false claims, undefined terms, dead links — each one line with evidence.
> "Nothing to report" if clean. Under 600 words.

Une tâche documentaire est terminée quand la revue rend « Nothing to report » ou que chaque point
remonté est corrigé ou rejeté avec une preuve.

## Review Focus

1. **Un binaire v1 dans le PATH** : l'inconnu a installé den par Homebrew ; `den up` répond avec des
   messages v1. Attendu : le README le prévient à l'installation et `den version` le diagnostique
   (Tâche 8, dépannage).
2. **sbx non connecté** : `sbx ls` échoue, donc tout `den` échoue. Attendu : le prérequis
   `sbx login` avec sa vérification, et une entrée de dépannage (Tâche 8).
3. **Pas de jeton Claude** : Claude répond `Not logged in · Please run /login` dans la VM. Attendu :
   une entrée de dépannage qui renvoie à « Claude login » (Tâche 8).
4. **Politique globale déjà initialisée autrement** (`allow-all`, ou règles ajoutées à la main) :
   `network.allow` ne restreint plus rien. Attendu : `sbx policy ls --wide` et
   `sbx policy check network <host> --sandbox <instance>` documentés (Tâche 5, Tâche 8).
5. **Dépôt qui n'est pas la racine d'un dépôt git, avec `-w`** : refus. Attendu : le refus est
   mentionné dans `docs/commands.md` (Tâche 4).

---

### Task 0 : Rebaser sur `v2`

**Files :** aucun fichier créé.

**Interfaces :**
- Produces : la branche contient la repo map de `v2` (`README.md`, section « Nests », puce
  « Repo map »), à reporter dans `docs/den-yaml.md` (Tâche 2) et `docs/files.md` (Tâche 7).

- [ ] **Step 1 : Rebaser**

Run : `git rebase v2`
Expected : `Successfully rebased`. En cas de conflit sur `README.md` : garder les deux contenus
(la section SSH de `77871e2` et la puce « Repo map » de `v2`).

- [ ] **Step 2 : Vérifier**

Run : `grep -c 'Repo map' README.md && grep -c '^## SSH' README.md && task build && ./den version`
Expected : `1`, `1`, puis une version qui n'est pas `den dev`.

---

### Task 1 : Exemple du premier lancement

**Files :**
- Modify : `examples/den-home/den.yaml` (réécrit en entier)

**Interfaces :**
- Produces : l'exemple cité par le README (Tâche 8) : un nest `demo`, un dépôt par `path:`, l'image
  officielle `claude-code-docker`, sans profil, port, dépôt optionnel ni `repos:`.

- [ ] **Step 1 : Écrire l'exemple**

Contenu exact :

```yaml
schema: 1

default_template: claude-code-docker

nests:
  demo:
    repos:
      - path: ~/src/demo   # any git repository on your machine
```

- [ ] **Step 2 : Vérifier qu'il se charge sans effet de bord**

Run : `D=$(mktemp -d); cp examples/den-home/den.yaml $D/; ./den --den-home $D up 2>&1 | head -5; rm -rf $D`
Expected : une erreur qui liste le nest `demo` (sans nom de nest, `den up` échoue en listant les
nests ; `internal/cli/up.go:41`), donc le fichier se charge. Aucune VM créée (`sbx ls` inchangé).
Ne rien créer dans `~`.

- [ ] **Step 3 : Commit**

```bash
git add examples/den-home/den.yaml
git commit -m "docs(examples): a one-repo first run on an official image"
```

---

### Task 2 : `docs/den-yaml.md`

**Files :**
- Create : `docs/den-yaml.md`

**Interfaces :**
- Consumes : sections « The `den.yaml` reference », « Nests » (repo map comprise), « Profiles » du
  README rebasé.
- Produces : les ancres `#global-file`, `#project-file`, `#nests`, `#network`, `#ports`,
  `#profiles`, que le README (Tâche 8) et `docs/security.md` (Tâche 5) citent.

- [ ] **Step 1 : Écrire le document**

Reprendre les trois sections, dans cet ordre : les deux fichiers (global, projet) et la recherche
du fichier projet ; le bloc YAML de référence ; « Nests » ; une section « Network » qui dit que
`network.allow` **s'ajoute** à la politique globale de sbx (`local-policy`), qu'un `network.allow`
global atteint tous les nests, que `*.` couvre un seul label, avec l'exemple
`sbx policy check network api.example.com:443 --sandbox <instance>` ; « Ports » ; « Profiles ».
Retirer ce qui part ailleurs : `templates:` détaillé (lien vers `templates.md`), `layers:` (lien vers
`security.md`), worktrees (lien vers `commands.md`).

- [ ] **Step 2 : Revue Fable**

`<DOC>` = `docs/den-yaml.md`. `<USAGES>` = mount several repos including optional ones; open a
network host to one nest and check what a VM reaches; publish a port; bring your Claude config
(profile); share a project `den.yaml` with a team.
Expected : « Nothing to report », ou chaque point corrigé.

- [ ] **Step 3 : Contrôles et commit**

Run : le contrôle des noms privés et le contrôle des liens. Expected : aucune sortie.

```bash
git add docs/den-yaml.md
git commit -m "docs: den.yaml reference"
```

---

### Task 3 : `docs/templates.md`

**Files :**
- Create : `docs/templates.md`
- Modify : `examples/den-template/den-template.yaml` seulement si la revue y trouve une erreur

**Interfaces :**
- Consumes : section « Templates » du README rebasé, `examples/den-template/`.
- Produces : l'ancre `#build-your-own-template`, citée par le README.

- [ ] **Step 1 : Écrire le document**

Ordre : image officielle (aucune construction) ; template déclaré (`repo`, `dir`) ; définition
`den-template.yaml` ; `den build` et ses options ; siblings `from:` ; exigence d'ENTRYPOINT (statut
137 avec `docker.io/sbx/claude`, commande `docker image inspect`). Une section
« Build your own template » qui part de `examples/den-template/` déclaré par
`templates: {example: {dir: <chemin du clone>/examples/den-template}}` puis `den build example`.

- [ ] **Step 2 : Revue Fable**

`<DOC>` = `docs/templates.md`. `<USAGES>` = build a custom template from the shipped example;
share a template through a git repo; rebuild after editing a step; understand why a VM stops with
status 137.

- [ ] **Step 3 : Contrôles et commit**

Contrôles des noms privés et des liens, puis :

```bash
git add docs/templates.md examples/den-template
git commit -m "docs: templates guide"
```

---

### Task 4 : `docs/commands.md`

**Files :**
- Create : `docs/commands.md`

**Interfaces :**
- Consumes : sections « Commands » et « Worktrees » du README rebasé.
- Produces : une ancre par commande (`#den-up`, `#den-exec`, `#den-shell`, `#den-ls`, `#den-rm`,
  `#den-build`, `#den-version`) et `#worktrees`.

- [ ] **Step 1 : Écrire le document**

Une section par commande, avec son tableau d'options. Retirer la politique `--no-input` (interne).
Corriger « sans nom de nest, liste les nests » : écrire « fails and lists the nests it can see »
(`internal/cli/up.go:41`). Section « Worktrees » avec le refus quand un dépôt n'est pas la racine
d'un dépôt git (Review Focus 5).

- [ ] **Step 2 : Revue Fable**

`<DOC>` = `docs/commands.md`. `<USAGES>` = open a shell, list and remove an instance; work on a
branch in a worktree; run one command non-interactively and keep stdout clean; understand why
`den rm` refuses.

- [ ] **Step 3 : Contrôles et commit**

```bash
git add docs/commands.md
git commit -m "docs: command reference"
```

---

### Task 5 : `docs/security.md`

**Files :**
- Create : `docs/security.md`

**Interfaces :**
- Consumes : section « Layers », la mention de `--dangerously-skip-permissions` (« Profiles »),
  l'agent partagé de « SSH ».
- Produces : l'ancre `#layers` (citée par `den-yaml.md`) et le résumé en dix lignes que le README
  reprend.

- [ ] **Step 1 : Écrire le document**

Sections : ce que la VM peut faire (permissions contournées, `den-claude/spec.yaml`) ; ce qu'elle
peut écrire (dépôts en écriture, `.git` compris, hooks exécutés sur l'hôte ; avec `-w`, `.git` en
lecture seule mais `/den-git/<repo>` monté) — risque accepté ; ce qu'elle peut atteindre (politique
globale de sbx + listes de den, `balanced`, règles ajoutées à la main, Review Focus 4) ; ce qu'elle
peut signer (lien `ssh.md`) ; ce que den protège (overlays en lecture seule, `--skip-host-commands`) ;
« Layers » ; risques résiduels.

- [ ] **Step 2 : Revue Fable**

`<DOC>` = `docs/security.md`. `<USAGES>` = decide whether it is safe to run den on a work laptop;
add sbx fields den does not model (layers); find out what a VM can reach and tighten it.

- [ ] **Step 3 : Contrôles et commit**

```bash
git add docs/security.md
git commit -m "docs: security model"
```

---

### Task 6 : `docs/ssh.md`

**Files :**
- Create : `docs/ssh.md`

**Interfaces :**
- Consumes : section « SSH » du README (commit `77871e2`).
- Produces : l'ancre `#troubleshooting` de `ssh.md`, citée par le dépannage du README.

- [ ] **Step 1 : Écrire le document**

Reprendre la section SSH avec ces changements : hôtes en `example.com` (`*.dev.example.com:22`,
`git.example.com`) ; défaut « load only the keys your agents may use », avec
`ssh-add -t` et la commande pour une clé ; « load every key » (`ssh-add $(grep -l 'PRIVATE KEY'
~/.ssh/* 2>/dev/null)`) présenté comme option avec son risque ; `ssh.agentForwardingEnabled`
(`sbx settings set ssh.agentForwardingEnabled false`, redémarrage du démon requis) ; section
Linux retirée (macOS seulement). Une section « Troubleshooting » : `The agent has no identities.`,
`Permission denied (publickey)` avec une clé sans `.pub`, `Too many authentication failures`.

- [ ] **Step 2 : Revue Fable**

`<DOC>` = `docs/ssh.md`. `<USAGES>` = use git over SSH in the VM; limit which keys the VMs can
use; turn SSH agent forwarding off; fix `Permission denied (publickey)` in the VM.

- [ ] **Step 3 : Contrôles et commit**

```bash
git add docs/ssh.md
git commit -m "docs: SSH guide, one key by default"
```

---

### Task 7 : `docs/files.md` et `docs/migrating-from-v1.md`

**Files :**
- Create : `docs/files.md`, `docs/migrating-from-v1.md`
- Delete : `docs/source-authoring.md`

**Interfaces :**
- Consumes : « Where things live » (repo map comprise), « Coexistence with v1 », la liste du
  vocabulaire v1 disparu (`CLAUDE.md`, section « Stale artifacts »).

- [ ] **Step 1 : Écrire les deux documents et supprimer le troisième**

`files.md` : le tableau, sans les chemins de staging au-delà d'une ligne. `migrating-from-v1.md` :
coexistence, chemins que V2 ne touche pas, tags `:v1`/`:v2`, refus d'adopter une VM v1, vocabulaire
v1 sans équivalent. Puis `git rm docs/source-authoring.md`, et
`grep -rn source-authoring README.md docs/*.md` ne renvoie rien.

- [ ] **Step 2 : Revue Fable**

`<DOC>` = `docs/files.md` et `docs/migrating-from-v1.md`. `<USAGES>` = find what den writes on
disk; move from v1 to V2 without losing a running VM.

- [ ] **Step 3 : Contrôles et commit**

```bash
git add docs/files.md docs/migrating-from-v1.md
git commit -m "docs: files on disk and migrating from v1; drop the v1 source guide"
```

---

### Task 8 : Le README

**Files :**
- Modify : `README.md` (réécrit en entier, environ 250 lignes)

**Interfaces :**
- Consumes : toutes les ancres des Tâches 2 à 7, l'exemple de la Tâche 1.

- [ ] **Step 1 : Écrire le README**

Les douze sections de la spec (« Structure cible », `README.md`), dans cet ordre. Valeurs fixées :
- Statut : « Pre-release V2, macOS only, tested with sbx v0.53.0; den drives experimental sbx
  commands (`sbx env`, `sbx kit`) that may change. »
- Prérequis, une commande de vérification par ligne : `sw_vers`, `sbx version` (≥ v0.53.0),
  `sbx ls` (connecté), `sbx policy init balanced`, `go version` (≥ 1.26), `task --version`,
  `git --version`.
- Connexion de Claude : `claude setup-token`, puis
  `printf '%s' "$TOKEN" | sbx secret set-custom --host api.anthropic.com --env CLAUDE_CODE_OAUTH_TOKEN --value-stdin` ;
  clé d'API : texte écrit seulement après la Tâche 9, Step 1 ; `/login` en dernier recours.
- Installation : `git clone`, `git checkout v2`, `task build`, `./den version` ; ligne sur v1.
- Premier lancement : copier `examples/den-home/den.yaml` dans `~/.den/den.yaml`, remplacer
  `~/src/demo`, `den up demo`. Dire : le dépôt est au même chemin absolu, Claude y démarre.
- Dépannage, une ligne et un remède chacun : binaire v1 (Review Focus 1), sbx non connecté
  (2), `Not logged in · Please run /login` (3), hôte refusé (4), statut 137, VM « Foreign »,
  `Permission denied (publickey)` (lien `ssh.md#troubleshooting`).
- Désinstallation : `den rm <instance>` par instance, `sbx template rm <name>:v2`, supprimer le den
  home.

- [ ] **Step 2 : Revue Fable**

`<DOC>` = `README.md`. `<USAGES>` = toutes les lignes du tableau « Usages à couvrir » de la spec.

- [ ] **Step 3 : Contrôles et commit**

Run : contrôles des noms privés et des liens ; `wc -l README.md`. Expected : aucune sortie,
environ 250 lignes.

```bash
git add README.md
git commit -m "docs: README for a first-time user"
```

---

### Task 9 : Vérifications avant fusion (HITL)

**Files :** `README.md` seulement si une vérification échoue.

- [ ] **Step 1 : Clé d'API**

Demander une clé d'API au mainteneur. Si fournie : la stocker par
`printf '%s' "$KEY" | sbx secret set-custom --host api.anthropic.com --env ANTHROPIC_API_KEY --value-stdin`,
lancer le premier lancement (Step 2) et vérifier que `claude -p "Reply OK"` répond `OK` dans la VM.
Puis supprimer ce secret (`sbx secret rm`) et rétablir le jeton OAuth. Sinon : la méthode par clé
d'API n'apparaît pas dans le README.

- [ ] **Step 2 : Premier lancement exécuté (accord du mainteneur requis : crée une VM)**

Sur un den home temporaire, suivre le README à la lettre de « Prerequisites » à « First run », avec
un dépôt réel. Expected : Claude démarre dans le dépôt ; `den shell`, `den ls`, `den rm demo`
marchent. Chaque écart corrige le README.

- [ ] **Step 3 : Revue finale de la branche**

Une revue Fable sur tout `README.md` + `docs/*.md`, avec tous les usages de la spec. Puis les deux
contrôles communs.

- [ ] **Step 4 : Pousser et rapporter**

```bash
git push
```

Rapport final : branche, commits, résultat des vérifications, liste « Tickets » (contradictions
README/code trouvées).
