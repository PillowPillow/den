# den V2 — résultats des essais S1 à S4

*den · réécriture V2 · 12 nov. 2026 · sbx v0.53.0 `2c62838`*

Ce document répond aux essais S1 à S4 du §11 de `2026-11-12-den-v2-scope-design.md`. Chaque essai a
tourné sur une VM jetable `den-probe-*`, créée par `sbx env create --force --no-input` à partir d'un
`sbxenv.yaml` écrit pour l'occasion (agent `shell` sauf en S3), avec des repos jetables créés dans le
dossier temporaire de la session. Aucun repo réel, aucun dossier de clés n'a été monté.

Marquage : **[mesuré 0.53.0]** = observé sur une VM jetable le 12 nov. 2026 ; **[non vérifié]** =
hypothèse.

Nettoyage : toutes les VM `den-probe-*` sont supprimées. `sbx secret ls`, `sbx policy ls`,
`sbx settings ls` et `sbx ls` sont identiques à l'état relevé avant les essais ; `am`, `dg-oauth` et
`harness2` n'ont pas changé.

Contexte qui influence les résultats : le réglage `kit.shortcuts.shell` pointe vers
`~/.config/sbx-kits/shell/spec.yaml`, un kit v2 `kind: sandbox` (image
`docker/sandbox-templates:shell-docker`, entrypoint `bash -l`, aucun réseau ni credential). Tous les
essais `agent: shell` composent ce kit, pas le `shell` intégré (v3 depuis sbx v0.51.0).

## Synthèse

| Essai | Verdict | Conséquence pour le plan |
|---|---|---|
| S1 | **Retenu**, avec un verrou | Worktree monté au chemin du clone principal + `.git` commun sur `/den-git/<repo>` + fichier `.git` généré en `:ro`. den **verrouille** chaque worktree qu'il crée (`git worktree lock --reason den`) ; sans verrou, une VM peut effacer les métadonnées d'un worktree voisin. `den rm` doit utiliser `remove -f -f` ou déverrouiller d'abord. |
| S2 | **Retenu** | `sbx kit add` à chaud marche sur une VM `sbx env` ; la règle réseau s'applique tout de suite. den doit tenir lui-même la liste des kits ajoutés : `sbx env plan` ne les voit pas. |
| S3 | **Piste (d) retenue** (décision de l'utilisateur) ; (a) et (c) échouent ; (b) non essayée | Un kit `sandbox` v2 maison (`den-claude`), sans bloc `credentials`, fait marcher le jeton OAuth, avec l'image de base locale en `sandboxOptions.template`. **Coût : toute sandbox den est v1/v2 seulement** ; les kits v3 publiés (`docker.io/sbx/*`) y sont inutilisables. |
| S4 | **Accepté** | Le dossier de run peut être sous un dossier monté. sbx monte chaque fichier d'env en `:ro` ; l'attaque par renommage prévue par sbx échoue en pratique. |

## S1 — worktree monté au chemin du clone principal

**Montage essayé** (dans `sbxenv.yaml`, `additionalWorkspaces` accepte une clé `target:`) :

```yaml
additionalWorkspaces:
  - path: <wts>/main.feat          # le worktree…
    target: <repos>/main           # …au chemin du clone principal
  - path: <repos>/main/.git        # le .git commun de l'hôte
    target: /den-git/main
  - path: <gen>/main.git           # fichier généré : "gitdir: /den-git/main/worktrees/main.feat"
    target: <repos>/main/.git
    readOnly: true
```

**Faits** [mesuré 0.53.0] :

- sbx accepte un montage de fichier `:ro` dont la cible est dans un autre montage. Dans la VM :
  `/den-git/main` rw, `<repos>/main` rw, `<repos>/main/.git` ro.
- `commondir` (`../..`) se résout : `git rev-parse --git-dir` = `/den-git/main/worktrees/main.feat`,
  `--git-common-dir` = `/den-git/main`, `--show-toplevel` = `<repos>/main`.
- `git status` (branche `feat`), `git commit` et `git log` marchent dans la VM.
- Sur l'hôte après le commit : `git worktree list` montre le commit de la VM sur `feat` ;
  `git worktree prune --dry-run` ne propose rien ; `worktrees/main.feat/gitdir` garde le chemin hôte
  (la VM ne le réécrit pas) ; `git log` du worktree hôte montre le commit.
- **Risque prune** : la VM voit son propre worktree `prunable` (le chemin hôte du worktree n'existe pas
  dans la VM), et `git worktree prune --dry-run` annonce `Removing worktrees/main.feat: gitdir file
  points to non-existent location`. Le `.git` commun est monté en écriture, donc un `prune` réel (ou
  un `git gc`) dans la VM supprime les métadonnées **sur l'hôte**.
- Monter aussi le worktree à son propre chemin protège ce worktree-là, mais **pas les voisins** : avec
  un second worktree `main.other` non monté (cas d'une autre instance de la même nest), la VM annonce
  `Removing worktrees/main.other`. Une VM peut donc casser le worktree d'une autre instance.
- **Correctif retenu : le verrou.** Après `git worktree lock --reason den` sur les deux worktrees
  (côté hôte), et **sans** quatrième montage : `git worktree prune -v` puis
  `git -c gc.worktreePruneExpire=now gc` réels dans la VM ne suppriment rien ; `git commit` marche ;
  `git worktree list` montre `locked`.
- Le verrou protège contre un accident, pas contre un agent : la VM peut lancer
  `git worktree unlock` elle-même (le `.git` commun est en écriture).
- Côté hôte, un worktree verrouillé refuse `git worktree remove` et `remove -f` (`cannot remove a
  locked working tree, lock reason: den`) ; `remove -f -f` marche. `den rm` doit faire l'un ou
  l'autre : `unlock` puis `remove`, ou `remove -f -f`.

**Verdict** : la solution du §10 est retenue (trois montages), et den verrouille chaque worktree
qu'il crée. Le risque hérité reste : le `.git` commun est monté en écriture, donc la VM peut poser un
hook qui s'exécutera sur l'hôte, ou déverrouiller un worktree.

## S2 — `sbx kit add` sur une VM créée par `sbx env`

**Essai** : VM `sbx env` (agent `shell`), puis `sbx kit add` d'un mixin v2 local qui autorise
`example.org`.

**Faits** [mesuré 0.53.0] :

- Avant : `sbx policy check network --sandbox den-probe-s2 example.org` → `Denied` ; `curl` depuis la
  VM → 403.
- `sbx kit add --no-input den-probe-s2 <mixin>` est accepté (≈ 2 s) : la VM créée par `sbx env` porte
  les labels « recreate-aware ». sbx recrée le conteneur (« swap container »).
- Après : `policy check` → `Allowed` ; `curl` → 200. `sbx policy ls` montre une politique `kit`
  `sandbox:den-probe-s2 network: 1 allow`.
- Conservés à travers le swap : un fichier dans le workspace, `/home/agent/vmstate` **et**
  `/tmp/boot`.
- Le kit ajouté survit à `sbx stop` puis `sbx env run --detach --force --no-input` : `curl` → 200.
- `sbx env plan` sur le fichier d'origine ne signale **aucune** dérive (« unchanged ») ; `sbx env exec`
  marche ; `sbx env rm` supprime la VM et la politique du kit (0 politique `probe-s2` restante).

**Verdict** : L1 `den kit add` peut s'appuyer sur `sbx kit add`. Comme `sbx env` ignore les kits
ajoutés à chaud, den doit écrire le kit dans la configuration (`--save`) ou dans son registre, faute de
quoi la prochaine recréation le perd sans prévenir [non vérifié : recréation non essayée].

## S3 — kit `claude` de sbx, quatre pistes

**Constat de départ** [mesuré 0.53.0] : le kit `claude` intégré est un kit **v3**
(`docker.io/sbx/claude@sha256:4c3f06ab…`, Claude Code 2.1.322). Dans la VM,
`CLAUDE_CODE_OAUTH_TOKEN` contient le jeton de remplacement du secret personnalisé global (29
caractères) et `SBX_CRED_ANTHROPIC_MODE=none`. Le kit écrit `~/.claude/settings.json` avec
`bypassPermissions`, `skipDangerousModePermissionPrompt` et des hooks `sbx-claude-hook`.

| Piste | Résultat [mesuré 0.53.0] |
|---|---|
| (a) jeton OAuth d'abonnement | **Échec.** `claude auth status` dit `loggedIn: true, authMethod: oauth_token`, mais `claude -p` répond `Not logged in · Please run /login`. Appel direct à `api.anthropic.com/v1/messages` avec `Authorization: Bearer $CLAUDE_CODE_OAUTH_TOKEN` : **401** `x-api-key header is required` depuis le kit `claude`, **200** depuis une VM `shell` avec le même secret global. Le kit `claude` retire toujours l'en-tête `Authorization` (docker/sbx-releases#11, #144). |
| (b) clé d'API | **Non essayée.** Aucun secret de service `anthropic` n'est enregistré ; l'essai demande une clé d'API à l'utilisateur. Si elle marche, c'est la seule piste qui garde le kit `claude` v3 intégré ; le coût passe de l'abonnement à la facturation API. |
| (c) petit kit correctif | **Échec de composition.** Un mixin v2 qui ajoute une entrée de routage `anthropic` (`scheme: bearer`, `required: false`) est refusé : `cannot mix v3 kits with v1/v2 kits in one sandbox`. Un correctif devrait être un kit v3, donc une image publiée dans un registre ; non essayé. De plus, une entrée de routage n'injecte que le secret de service `anthropic`, pas le secret personnalisé qui porte le jeton OAuth. |
| (d) kit `claude` personnalisé | **Succès.** Kit v2 `kind: sandbox`, `name: den-claude`, image `docker.io/sbx/claude@sha256:4c3f06ab…`, entrypoint `["claude", "--dangerously-skip-permissions"]`, **aucun** bloc `credentials`, chargé par `kits:` avec `agent: den-claude` (le réglage `kit.shortcuts.claude` n'a pas été touché). Appel direct : **200**. `claude -p "Reply with the single word OK"` → `OK`, code 0. |

Notes sur (d) :

- Sans bloc `credentials`, sbx n'écrit pas `~/.claude/settings.json` (fichier absent). den doit poser
  lui-même `bypassPermissions` dans le profil (§9 le prévoyait pour l'agent `shell`).
- **Avec l'image de base locale** : VM `den-claude` + `sandboxOptions.template:
  docker.io/library/pillow:v1` (template présent seulement dans le magasin sbx, comme une sortie de
  `den build`) + le mixin réseau v2 de S2. `sbx inspect` → `image: docker.io/library/pillow:v1` : le
  template l'emporte sur l'image du kit. Claude Code y est en 2.1.306 (binaire du template, pas le
  2.1.322 du kit). `claude -p` → `OK`, code 0. Le mixin réseau compose : `example.org` `Allowed`,
  `curl` 200. Le kit `den-claude` ne fournit donc que l'entrypoint ; l'image de base fournit Claude
  Code, et `den build` doit l'y installer.
- **Verrou v2/v3** : sbx refuse de mélanger kits v3 et v1/v2 dans une sandbox. Avec (d), tous les kits
  d'une sandbox den sont v1/v2 ; les kits v3 publiés par Docker (`docker.io/sbx/*`) y sont
  inutilisables. Le message d'erreur de sbx pousse vers v3 : si sbx abandonne v2, la piste (d) casse.
  C'est un risque pour l'objectif « une mise à jour de sbx ne casse pas den ».
- Un premier `sbx env exec` a fini une fois en code 137 (SIGKILL). Relance ciblée : `exec` de 90 s
  (`sleep 90`) sur VM arrêtée, `den-claude` et `shell` en parallèle → les deux `alive`, code 0. Non
  reproduit ; cause inconnue.

**Verdict** : la piste (d) marche de bout en bout avec l'image de base locale. Elle garde l'abonnement
(jeton OAuth du secret personnalisé global) au prix du verrou v2/v3. La piste (b) garderait le kit v3
intégré au prix de la facturation API ; elle demande une clé pour être essayée.

**Décision de l'utilisateur (12 nov. 2026) : piste (d).** den fournit le kit `den-claude` ; toute
sandbox den compose des kits v1/v2 seulement ; la piste (b) n'est pas essayée.

## S4 — `sbxenv.yaml` dans un dossier de run sous un dossier monté

**Essai** : `sbxenv.yaml` dans `<projet>/.den/run/x/`, `workspace:` = `<projet>` en chemin absolu.

**Faits** [mesuré 0.53.0] :

- Accepté. Un montage depuis `/private/tmp` est accepté aussi.
- sbx monte le fichier d'env en `:ro` dans la VM (`virtiofs (ro,…)`). `sbx env plan` avertit :
  « environment file mounted read-only, inside a directory the sandbox can rename ».
- **L'attaque par renommage échoue** : écrire le fichier → `Read-only file system` ; `mv` du dossier
  parent `x` → `Read-only file system` ; `mv` du grand-parent `.den` → `Read-only file system`. Le
  reste du workspace et les autres fichiers du dossier de run restent modifiables.
- Avec deux fichiers empilés (`sbxenv.yaml` + `layer2.yaml`) **présents à la création**, les deux sont
  `:ro`. Un fichier ajouté à la pile **après** la création est monté en lecture-écriture (`plan` :
  « environment file mounted read-write into the sandbox »).
- Témoin : un dossier de run hors de tout montage ne produit aucun avertissement de fichier (`plan`
  seul).
- Non interactif : sur une VM existante, `sbx env exec --no-input` marche ; `sbx env create` refuse
  (`already exists`) ; `plan` affiche « needs your approval » même après un `create --force`, sans
  bloquer `exec`.

**Verdict** : un dossier de run visible dans le dossier projet est possible. Règles pour den : la pile
de fichiers d'une instance est fixée à la création (toute modification de la pile = recréation), et
den ne lit dans le dossier de run aucun autre fichier que les `sbxenv.yaml` de la pile, puisque la VM
peut écrire tout le reste.

## Points ouverts

- S3 : surveiller à chaque version de sbx que les kits v1/v2 restent acceptés (condition de la
  piste (d)).
- S2 : comportement d'une recréation `sbx env` d'une VM qui avait un kit ajouté à chaud [non vérifié].
- S3 (d) : le code 137 isolé, non reproduit.

## S6–S7 — essais du 13 nov. 2026 (sbx v0.53.0 `2c62838`)

Lancés après la revue finale, sur décision de l'utilisateur (briefing D1, D2). VM jetables `den-probe-overlay` et
`den-probe-profile`, supprimées ensuite. Ils fondent P12 et P13 de l'amendement du plan.

### S6 — montage en lecture seule par-dessus un dossier monté : **tenu**

Montage : le dossier projet en espace de travail en lecture-écriture ; `den.yaml` (fichier), `kits/` et `steps/`
(dossiers) en `additionalWorkspaces` `readOnly: true` au même chemin. L'agent (uid 1000) a `sudo` sans mot de passe.

- Écrire, tronquer, `chmod`, supprimer, renommer `den.yaml` : refusé (« Read-only file system », « Device or resource
  busy »). Remplacer par un autre fichier ou un lien symbolique : refusé. Écrire, ajouter, supprimer, renommer dans
  `kits/` et `steps/` : refusé.
- En root : refusé. Après `sudo umount` de la surcouche et `sudo mount -o remount,rw` de l'espace de travail : toujours
  refusé — l'hôte applique la lecture seule par chemin [mesuré].
- Après un arrêt automatique puis un redémarrage de la VM : protection intacte. Remplacement atomique de `den.yaml`
  côté hôte : la VM voit le nouveau contenu, toujours en lecture seule.
- Reste possible : créer un **nouveau** fichier à côté (`sub/den.yaml`), qui atteint l'hôte. Accepté par l'utilisateur.

### S7 — configuration Claude par kit : **`files/home/` oui, `agentInstructions` d'un mixin non**

Kit `kind: sandbox` (copie de `den-claude` + `agentInstructions`) et mixin (`agentInstructions` +
`files/home/.claude/{settings.json, output-styles/, agents/}`). Premier `env create` en échec sur un délai réseau au
tirage de l'image ; le second passe.

- `files/home/.claude/…` est copié tel quel dans `/home/agent/.claude/` à la création ; `settings.json` reste celui
  du kit (aucun réglage injecté par notre kit d'agent) [mesuré].
- `agentInstructions` : sbx écrit dans la VM un `CLAUDE.md` (17 Ko de consignes sbx + le texte du kit `sandbox`) dans
  le dossier **parent** de l'espace de travail ; le texte d'un **mixin** va dans `kits-agent-context/<kit>.md`, que
  `CLAUDE.md` cite seulement (« à lire si pertinent »). Ce n'est donc pas une consigne globale. Rien n'est écrit sur
  l'hôte [mesuré].
- Non vérifié : Claude applique-t-il le style de sortie et les agents copiés ? `claude -p` sort en 137, puis un second
  essai ne rend rien. À vérifier en tâche 14 (`/status`).

## S6b–S6d — essais du 13 nov. 2026, pendant la tâche 16 (sbx v0.53.0 `2c62838`)

VM jetables `den-probe-s6b`, `den-probe-s6c`, `den-probe-s6d`, supprimées ensuite. Espace de travail `A/` ; `B/` en
`additionalWorkspaces` en lecture-écriture, comme un dépôt monté ; les surcouches en `readOnly: true` au même chemin,
listées après `B/`.

### S6b — surcouche sous un dépôt monté : **tenue**

`B/kits` (dossier) et `B/den.yaml` (fichier) en surcouche. Ajouter, tronquer, `touch` : « Read-only file system » ;
`rm`, `mv` : « Device or resource busy » ; en root aussi ; après `sudo umount B/den.yaml`, l'écriture échoue toujours
[mesuré]. Écrire ailleurs dans `B/` et créer `B/sub/den.yaml` : possible, atteint l'hôte (le reste accepté de S6).

### S6c — surcouche à l'emplacement d'un lien symbolique : **`env create` échoue**

`B/kitlink` et `B/den.yaml` sont des liens vers des cibles hors de `B/`. Surcouche avec pour source le lien, ou avec
pour source la cible résolue, et pour cible le chemin du lien : « failed to run sandbox container », aucune VM.
Témoin sans lien (`C/kits` sous `C/`) : créé. `sbx -D` ne nomme pas la cause [mesuré]. D'où le refus d'un fichier
protégé dont le chemin traverse un lien sous la racine du montage.

### S6d — renommer un parent d'une surcouche : **refusé**

Surcouches `B/a/k` (dossier) et `B/p/q/f.sh` (fichier). `mv` du parent (`B/a`, `B/p/q`) et du grand-parent (`B/p`),
avec `sudo` et après `sudo umount B/a/k` : « Read-only file system » ; planter `B/a/k/spec.yaml` : refusé. Fichiers
de l'hôte intacts [mesuré]. L'hôte protège chaque ancêtre d'un chemin en surcouche dans le montage.
