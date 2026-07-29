# Corrections F1 et F2, et les vérifications qui les précèdent — 2026-07-29

Suite du smoke réel (`2026-07-29-smoke-reel-plan2.md`). Trois vérifications d'abord, deux
corrections ensuite, chacune tenue par des tests et revérifiée sur un vrai `sbx` (v0.35.0).

Banc : `DEN_HOME` dans un scratchpad, **deux** dépôts jouets (le smoke précédent n'en avait qu'un),
image `docker.io/library/devx:v1` + kits réels de `sbx-devbox`. VM créée puis détruite. La sandbox
`den` de l'utilisateur n'a pas été touchée (arrêtée avant, arrêtée après).

## Les vérifications

### V1 — l'attache interactive marche (risque n°2 du plan, jamais exercé)

`Fake.Attach` n'exerçait aucun tty : `sbx exec -it` n'avait jamais été prouvé, alors que c'est la
commande principale. Exercé sous un vrai pty (`script -q /dev/null`), sur les deux chemins :

```
agent@duo:/…/wt/essai/repo-a$
logout                                   rc=0
```

Prompt réel, `-w` au bon endroit, bracketed paste, sortie propre. Pour `den sh` comme pour
`den <nest>`.

### V2 — les `commands.startup` SE REJOUENT à la reprise (la réserve de F2)

C'était la seule justification possible du refus d'attacher dans une VM arrêtée. Sonde :
`/var/log/sbx-kit-startup.log`, compteur de `dispatcher run`, avant/après un `stop` puis un `exec`.

```
avant stop : runs=2
sbx stop → stopped
sbx exec  : Sandbox duo.essai started successfully
            MARQUEUR-COUCHE          ← état de la couche conteneur préservé
            runs=3                   ← chaîne complète des kits rejouée
```

La chaîne rejouée est complète et **dans l'ordre de l'argv**, mixin de den en dernier
(`003-startup-den-duo-essai`) — ce qui lève aussi le risque n°6 du plan sur l'ordre des `--kit`.
Une VM reprise n'est donc pas fonctionnellement incomplète. **Réserve levée : c'est la politique
qu'il fallait corriger, pas seulement le message.**

Vérifié au passage : `sbx policy check` répond sur une VM arrêtée sans la redémarrer — le
settle-loop n'a pas besoin d'être déplacé.

### V3 — la forme du correctif F1 en multi-repo

Le smoke précédent n'avait mesuré qu'un dépôt. Avec deux, la VM montait :

```
…/wt/essai/repo-a
…/wt/essai/repo-b
…/agents/claude
```

Donc **un `.git` par repo**, pas un seul. Et un piège que le test a trouvé : git rend le chemin
**résolu** (`/private/var/…`) là où den manipule celui de la config (`/var/…`). C'est la forme
résolue qu'il faut monter — c'est elle que le `gitdir:` du worktree désigne, et dans la microVM il
n'y a plus de lien symbolique pour rattraper l'écart.

### V4 — le settle-loop interroge bien la policy SCOPÉE d'une VM arrêtée

Le vrai risque de F2, et il n'était pas dans le handoff précédent : `policy.Settle` s'exécute
maintenant sur une sandbox arrêtée. S'il ne pouvait pas lire la policy de CETTE sandbox, F2
remplacerait un refus explicite par un échec fail-closed à chaque retour sur une VM `--detach` —
pire que ce qu'il corrige.

Un simple `allowed: true` ne prouvait rien : la `local-policy` globale de ce poste autorise déjà 197
hôtes pour toutes les sandboxes (F5). Sonde discriminante — un hôte **refusé globalement**, mis dans
l'`egress:` du nest, donc autorisé uniquement par le kit de cette sandbox :

```
VM running : hote-improbable-xyz.example  sandbox duo.essai = True
                                          sandbox den       = False   ← bien scopé, pas global
VM stopped : hote-improbable-xyz.example  sandbox duo.essai = True
```

La policy scopée est lisible à l'arrêt. Le settle-loop n'a pas besoin d'être déplacé après le
réveil.

### V5 — les mounts survivent au stop/resume (la composition F1 × F2)

Sinon F1 ne tiendrait que sur les VM fraîches. Après `stop` puis reprise :

```
workspaces remontés : les 2 worktrees, les 2 .git, le profil agent
git log  → commit précédent  ✅
commit   → f4eac52 « commit apres reprise » → visible sur l'hôte  ✅
```

## F1 — `-w` : git est mort dans la VM (corrigé)

`internal/spawn/spawn.go` monte désormais, pour chaque repo et **seulement sous `-w`**, le dossier
git commun en écriture, à côté du worktree. La question passe par
`worktree.DossierGitCommun` (`git rev-parse --git-common-dir`) et non par un
`filepath.Join(repo, ".git")` : quand le repo du nest est lui-même un worktree lié, le join rendrait
le fichier de renvoi, qui ne porte ni objets ni refs.

Vérifié sur le banc, deux repos, après correctif :

```
workspaces : …/wt/essai/repo-a  …/wt/essai/repo-b
             …/repos/repo-a/.git  …/repos/repo-b/.git  …/agents/claude

repo-a : git status ✅  branch essai ✅  commit 1715341 ✅ → visible sur l'hôte
repo-b : commit 31511ec ✅ → visible sur l'hôte, worktree list à jour
ls -a …/repos/repo-a  →  .  ..  .git       ← l'arbre principal reste invisible
```

Tests : `TestSpawnMonteLeDossierGitDeChaqueRepoAvecUnWorktree` (forme exacte de la liste **plus**
l'assertion qui lui donne sa raison : le `gitdir:` du worktree doit tomber sous le workspace monté —
aucun `filepath.Join` ne la satisfait), `TestSpawnNeMonteAucunDossierGitSansWorktree` (la
contrepartie : hors `-w`, le dépôt entier est déjà monté), et trois tests sur
`DossierGitCommun`, dont le repo qui est lui-même un worktree lié.

### L'autre moitié de F1 : les sandboxes créées AVANT le correctif

Elles tournent toujours et ne montent pas les `.git`. `signaleDerive` y est aveugle — leur mixin n'a
pas bougé — donc l'utilisateur s'y rattache et ne découvre le problème qu'à sa première commande
git : exactement la panne muette que F1 corrige. `Spawn` compare désormais, sur la branche attache,
les dossiers git attendus aux `workspaces` que la VM remonte, et avertit :

```
attention : la sandbox api.feat12 ne monte pas le dossier git de ses dépôts — git y est inopérant…
  - /…/api/.git absent des workspaces de la VM
  cette sandbox précède le correctif ; rien ne remonte un mount à une VM en marche :
  `den rm api.feat12` puis relance.
```

Avertir, pas refuser : la VM porte peut-être du travail, et la décision de détruire est à
l'utilisateur. Le `:ro` est retiré avant comparaison. Vérifié contre du vrai `sbx ls` qu'une VM
correcte ne déclenche RIEN (le faux positif serait le pire des deux).

## F2 — VM arrêtée : reprise, pas destruction (corrigé)

`sbx.VerifieEnMarche` → **`VerifieAttachable`**, qui accepte `running` et `stopped`. La liste
blanche reste fail-closed pour tout le reste : les autres valeurs de `status` ne sont pas relevées,
et une liste noire laisserait passer un statut d'erreur introduit demain.

`EstArretee` est un prédicat SÉPARÉ, exprès : « faut-il prévenir ? » et « faut-il refuser ? » sont
deux questions, et les confondre est ce qui a produit le refus.

Le libellé est « arrêtée : elle redémarre à l'attache (son état est conservé) » et non « reprise » :
sous `--detach`, den ne lance aucun `exec`, donc rien ne redémarre à ce moment-là. Le message est
vrai des deux côtés.

Vérifié sur le banc :

```
statut avant : stopped
$ den sh duo.essai
sandbox duo.essai arrêtée : elle redémarre à l'attache (son état est conservé)
Sandbox duo.essai started successfully
agent@duo:/…/wt/essai/repo-a$
statut après : running
```

Les deux chemins sont couverts (`internal/spawn` et `internal/cli/sh.go`) : rien au niveau de
`VerifieAttachable` ne garantit que `den sh` l'appelle, et une politique élargie d'un seul côté
rouvrirait le défaut de l'autre.

## F6 — README (corrigé)

Il annonçait le spawn comme « à venir » et ne listait que quatre commandes. Il décrit maintenant les
huit commandes réelles, les options de `den <nest>` et de `den rm`, la reprise d'une VM arrêtée, et
nomme ce qui n'est PAS livré (`den ports`, `den build`).

## Un rouge antérieur, corrigé au passage

`TestAssureRefuseUnWorktreeAppartenantAUnAutreRepo` échouait sur `main` avant toute modification —
même piège `/var` vs `/private/var` : il comparait un chemin rendu par git à un chemin de `t.TempDir`
sans résoudre les liens. Le dépôt était intact ; c'est l'assertion qui était fausse sur macOS. Elle
résout désormais les deux côtés, comme `memeChemin` le fait déjà partout ailleurs dans ce paquet.

## Vérifié aussi, rien à corriger

- `--without` sur un repo **requis** : refus nommé (« il ne peut pas être retiré »).
- `--only <optionnel>` : garde les requis + celui demandé. Sémantique conforme.
- `--agent inconnu` : refus qui liste les agents déclarés.
- `den rm` sur une VM **arrêtée** : fonctionne, corbeille horodatée, profil agent conservé.
- Un `repos:` ne porte que `path` et `optional` — pas de `:ro`. Monter le `.git` en écriture ne
  contredit donc aucune intention déclarée par l'utilisateur. (`run.sh` passe des `:ro` à
  `sbx create` à la main, mais c'est hors de la config den.)

## Restant

Inchangé depuis le smoke : **F4** (`-w feat/x` refusé — garder le nom de branche réel et n'aplatir
que le nom de sandbox), **F3** (dossier de worktree vide après `den rm`, cosmétique), **F5** (à
savoir, pas un bug). Puis Plan 3 (`den ports`, `-i`) et Plan 4 (`den build`).

## État

`gofmt` propre, `go vet` propre, `go test ./...` → **661 passés, 0 échec**. Rien n'est commité, et le
travail est sur `main` — à déplacer sur une branche avant de figer quoi que ce soit.
