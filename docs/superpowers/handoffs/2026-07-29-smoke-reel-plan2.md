# Smoke réel du CLI den — 2026-07-29

Premier essai du binaire contre un **vrai `sbx`** (v0.35.0, `/opt/homebrew/bin/sbx`), sur un banc
isolé : `DEN_HOME` dans un scratchpad, deux dépôts jouets, `worktree_root` absolu hors des dépôts.
Deux microVM réellement créées puis détruites. La sandbox `den` de l'utilisateur n'a pas été touchée
(arrêtée avant, arrêtée après).

Le handoff de fin d'implémentation posait trois risques « invérifiables ici ». Ils étaient
vérifiables : `sbx` est installé sur cette machine.

## Les trois risques du handoff : levés

| # | Risque | Verdict | Preuve |
|---|---|---|---|
| 1 | argv de `sbx create` | **levé** | `sbx create shell --help` classe `--name`, `--template`, `--kit` en **Global Flags** → persistants, donc l'ordre de den (`create --flags… shell PATH`) est valide. Confirmé par deux VM réellement créées. |
| 2 | A11 (montage au même chemin absolu) | **levé** | Doc sbx : « mounted inside the sandbox at the same path as on the host ». Empirique : marqueur écrit dans `$CLAUDE_CONFIG_DIR` depuis la VM `jouet`, relu depuis `jouet2.essai`. Le profil agent est bien partagé entre nests. |
| 3 | agent-forward (`SSH_AUTH_SOCK`) | **levé** | Dans la VM : `SSH_AUTH_SOCK=/run/ssh-agent.sock`, socket présent, `ssh-add -l` liste les clés réelles de l'hôte (teleport + sbx-devbox). |

Vérifié au passage : le schéma de `sbx ls --json` est **identique** à la fixture anonymisée
(`sandboxes` + `name`/`id`/`agent`/`status`/`workspaces`). Aucune dérive.

## F1 — `-w` : git est mort dans la VM (bloquant)

`den <nest> -w <nom>` crée bien le worktree au bon endroit et la branche, mais **ne monte que le
worktree**, pas le dépôt principal.

```
sbx ls --json → jouet2.essai : [ …/worktrees/essai/repo-jouet2, …/agents/claude ]
```

Or le `.git` d'un worktree lié est un *fichier* :

```
gitdir: /…/scratchpad/repo-jouet2/.git/worktrees/repo-jouet2
```

Ce chemin n'existe pas dans la VM. Résultat, **toute** commande git y échoue :

```
$ sbx exec jouet2.essai sh -c 'cd …/worktrees/essai/repo-jouet2 && git branch --show-current'
fatal: not a git repository: /…/scratchpad/repo-jouet2/.git/worktrees/repo-jouet2
```

Cause **prouvée**, pas supposée : en recréant le chemin manquant à l'intérieur de la VM (mkdir root
+ `sbx cp` du `.git` du dépôt principal), git repart immédiatement — `git branch --show-current` →
`essai`, `git log` → OK.

Conséquence : l'agent enfermé dans un worktree ne peut ni `status`, ni `diff`, ni `commit`, ni
`push`. C'est le cas d'usage central de `-w`.

### Le correctif, mesuré

Trois montages ont été essayés pour de vrai, chacun dans sa propre VM.

| Montage | `status` / `log` | `commit` | Arbre principal exposé ? |
|---|---|---|---|
| worktree seul (**actuel**) | ❌ `fatal: not a git repository` | ❌ | non |
| worktree + dépôt principal, rw | ✅ | ✅ (visible sur l'hôte aussitôt) | **oui, en écriture** |
| worktree + **`<repo>/.git` seul**, rw | ✅ | ✅ (`ec33555` remonté sur l'hôte) | **non** — `ls <repo>/` ne montre que `.git` |

**Retenir le troisième.** Monter le dépôt principal entier remet à la VM l'écriture sur l'arbre de
travail de l'hôte et sur les dossiers d'admin des autres worktrees — précisément l'isolation que
`-w` existe pour donner. Monter `<repo>/.git` fournit l'admin dir, les objets et les refs (tout ce
dont le commit a besoin) et laisse l'arbre principal invisible.

L'écriture est bien nécessaire, et le `:ro` ne suffit pas — mesuré : admin dir passé en lecture
seule, `status` et `log` continuent de passer, `commit` échoue sur
`Unable to create …/index.lock: Permission denied`. Un montage `:ro` donnerait donc une VM où git
*a l'air* de marcher jusqu'au premier commit.

Grâce à A11 (chemins hôte identiques dans la VM) le lien `gitdir` se résout tel quel, sans
réécriture. À noter tout de même : `git worktree add` de ce poste (git 2.50.1) accepte
`--relative-paths`, qui écrirait un `gitdir:` relatif — piste pour un correctif qui ne dépende plus
du chemin absolu, non évaluée ici. `sbx create --clone` (dépôt cloné dans le conteneur, remote
`sandbox-<nom>` côté hôte) est l'autre alternative non évaluée.

## F2 — VM arrêtée : den envoie détruire là où `sbx exec` suffirait

sbx arrête les sandboxes inactives tout seul (constaté à quelques minutes d'inactivité). den refuse
alors d'attacher :

```
den: sandbox "jouet" : statut lu "stopped", attendu "running" — den n'attache pas dans une VM
arrêtée ; … ou détruis-la puis relance : `den rm jouet`
```

Mais `sbx exec` **relance** une sandbox arrêtée de façon transparente :

```
$ sbx exec jouet sh -c 'echo VIVANTE'
Sandbox jouet started successfully
VIVANTE
```

La liste blanche de `internal/sbx/ls.go` (`StatutEnMarche = "running"`, tout le reste refusé) est
donc plus stricte que sbx lui-même, et son message oriente vers la destruction de la VM là où une
simple reprise suffisait. Avec l'arrêt automatique, le cas est la norme, pas l'exception : c'est ce
qu'on rencontre à chaque retour sur une VM `--detach`.

Et il y a bien quelque chose à perdre. Mesuré : un fichier écrit dans `$HOME` de la VM (couche
conteneur, pas un montage), puis `sbx stop`, puis `sbx exec` — le fichier est toujours là. L'arrêt
préserve l'état ; `den rm` le détruit. Le remède affiché coûte donc plus cher que la panne.

**Réserve à lever avant de corriger.** Une justification possible du refus n'a pas été testée : les
`commands.startup` des kits se rejouent-elles à la reprise d'une VM arrêtée ? Si elles ne se
rejouent pas, une VM redémarrée peut être fonctionnellement incomplète (config git globale, mise à
jour de l'agent), et le fail-closed a une vraie raison d'être — auquel cas c'est le *message* qu'il
faut corriger, pas la politique. À trancher avant de toucher à `ls.go`.

## F3 — dossier de worktree vide après `den rm` (confirmé)

Le point déjà consigné est réel : après `den rm jouet2.essai --force`, `worktrees/essai/` subsiste,
vide. Cosmétique.

Le reste du chemin `rm` est correct : refus **avant** destruction (la VM survit au refus, vérifié),
worktree horodaté en corbeille avec son contenu non commité préservé
(`trash/20260729-122252-jouet2.essai-repo-jouet2/a.txt`), worktree désenregistré côté git, et
**branche `essai` conservée** — aucune perte.

## F4 — `-w feat/essai` refusé (par conception, mais c'est la première friction)

```
den: worktree "feat/essai" : le caractère "/" est interdit — ce nom devient un nom de sandbox…
```

Refus propre, avant effet de bord, message qui explique le pourquoi. Mais `feat/xxx` est le nom de
branche que tout le monde tape en premier. À arbitrer : den pourrait garder le nom de branche réel
et n'aplatir (`/` → `-`) que le nom de sandbox.

## F5 — la policy réseau : deux messages vrais qui ont l'air de se contredire

Après ajout de `proxy.golang.org` à la config, sur VM vivante, den dit d'abord que la sandbox ne le
connaît pas (dérive), puis « attente de la policy réseau (4 hôte(s))… » — et l'hôte répond 200
depuis la VM.

Pas un bug. `sbx policy ls` montre une `local-policy` **globale** qui autorise 197 hôtes pour
*toutes* les sandboxes. den pose la bonne question à sbx (« est-ce autorisé ? » → oui) et signale
séparément que le mixin du `create` ne le déclare pas (→ vrai). Les deux affirmations portent sur
des objets différents.

À savoir tout de même : le garde-fou d'egress de den n'est pas plus fort que la policy globale de
la machine. Sur un poste avec une `local-policy` large, `egress:` dans la config den documente
l'intention plus qu'il ne la contraint.

## F6 — README périmé

`README.md` annonce encore « Le spawn (`den <nest>`), les ports et le build arrivent dans les
incréments suivants » et son tableau ne liste que `nest ls`, `nest show`, `doctor`, `version`. Le
CLI expose en réalité `den <nest>`, `ls`, `rm`, `sh`, `nest`, `doctor`, `version`, plus
`--worktree/-w`, `--detach`, `--only`, `--without`, `--agent`. Hors périmètre du test, mais à
corriger avant toute diffusion.

## Ce qui a marché sans réserve

- `den doctor` sur `DEN_HOME` absent : diagnostic actionnable, pas de panic ni d'erreur `stat` brute.
- `den doctor` sur config réelle : 8 diagnostics verts, dont `ssh.mode` qui affiche le `SSH_AUTH_SOCK` de l'hôte.
- `den nest ls` / `den nest show` : résolution complète (stack, image, agent, env, egress, worktrees).
- Spawn de bout en bout, deux fois, image `docker.io/library/devx:v1` + kits réels de `sbx-devbox`.
- Ré-exécution sur VM vivante : `sandbox jouet déjà vivante : attache` — **pas de second create**.
- Détection de dérive : diff précis (`egress ajouté à la config : proxy.golang.org`) et remède exact.
- `den ls` : recompose `nest` + `worktree` depuis le nom de sandbox (`jouet2.essai` → nest `jouet2`, worktree `essai`).

## Ordre de traitement suggéré

1. **F1** — `-w` est inutilisable en l'état, et la panne est muette jusqu'à la première commande git.
   Correctif mesuré : monter `<repo>/.git` en écriture à côté du worktree.
2. **F2** — se déclenche à chaque retour sur une VM détachée, et le remède affiché détruit un état
   qui aurait survécu. Lever d'abord la réserve sur les `commands.startup`.
3. **F6** — le README décrit un autre CLI que celui qui est livré.
4. F4, F3 — arbitrage puis cosmétique.

À savoir, distinct des bugs : **F5**. Sur ce poste, `egress:` dans la config den documente
l'intention plus qu'il ne la contraint — la `local-policy` globale autorise déjà 197 hôtes pour
toutes les sandboxes. Rien à corriger dans den, mais à ne pas confondre avec un confinement réseau.

## Portée de ce smoke

Quatre microVM créées et détruites. Fin de test : `sbx ls` ne montre que la sandbox `den` de
l'utilisateur, arrêtée comme au départ ; `sbx policy ls` ne garde que sa policy et la
`local-policy`. Aucun dépôt de l'utilisateur n'a été monté ni modifié — les deux dépôts d'essai
étaient des `git init` jetables du scratchpad.

Non couvert : les ports (`ports:` des nests), `--only` / `--without`, `--agent`, le `den sh`
interactif (testé seulement dans ses refus), et la rejouabilité des `commands.startup` (cf. F2).
