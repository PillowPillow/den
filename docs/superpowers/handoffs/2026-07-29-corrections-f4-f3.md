# Corrections F4 et F3 — 2026-07-29

Suite de `2026-07-29-corrections-f1-f2.md`. Deux correctifs, tenus par des tests et vérifiés sur un
vrai `sbx` (banc à deux dépôts jouets, `DEN_HOME` dans un scratchpad, VM créée puis détruite ; la
sandbox `den` de l'utilisateur n'a pas été touchée).

## F4 — `-w feat/essai` : la branche garde son nom, le nom de sandbox s'aplatit

`-w` reçoit un nom de BRANCHE, et « feature/123 » est le premier que tape qui travaille sur une
forge. den le refusait, parce que ce nom devient aussi un nom de sandbox (charset de `sbx create
--name`, point réservé au séparateur `<nest>.<worktree>`).

**Ce qui est aplati est ce qui devient un NOM** — la sandbox et le dossier de worktree. La branche
garde le nom tapé : c'est celui du `git log`, de la forge et de la PR ; l'aplatir serait renommer le
travail de l'utilisateur.

### Pourquoi le DOSSIER doit être aplati lui aussi

Ce n'est pas un choix de commodité. den n'a pas d'autre état que le nom de sandbox (`--label`
n'existe pas dans sbx), et `den rm` retrouve le dossier à nettoyer à partir de ce seul nom :

```
nom de sandbox → sbx.DecomposeNom → worktree.Chemin → le dossier
```

Un dossier nommé d'après la branche réelle (`<root>/feat/essai/<repo>`) est **inatteignable** depuis
`duo.feat-essai`. D'où `worktree.Nom`, qui porte les deux noms, et l'assertion d'aller-retour dans
`TestSpawnAplatitLeNomDeSandboxEtGardeLaBranche` : elle refait exactement le chemin de `den rm` et
le compare au dossier qu'`Assure` a réellement créé.

### Où vit l'aplatissement, et pourquoi là

`config.AplatitComposantSandbox`, à côté du charset — sa seule définition — et **en amont** de
`ValiderComposantSandbox` et de `sbx.NomSandbox`. Ces deux-là restent stricts : le test qui refuse
« feature/123 » au niveau de `sbx.NomSandbox` est toujours vert, et c'est le signe que
l'assouplissement n'a pas fui dans la source unique du verdict. Tout ce qui consomme ensuite ce nom
— argv de `sbx create`, policy scopée, nom d'entrée de corbeille, `den rm` — continue de recevoir un
composant valide.

Deux propriétés verrouillées par des tests : la sortie passe TOUJOURS
`ValiderComposantSandbox`, et elle ne contient jamais de séparateur de chemin (c'est elle qui
protège `worktree.Chemin` d'un worktree creusé en sous-dossier, voire hors de `worktree_root`).

### Aplatir n'est pas inventer

- Les séries ne fusionnent pas (« feat//x » → « feat--x ») : un caractère tapé, un caractère rendu.
- Un premier caractère non alphanumérique est **refusé**, jamais préfixé d'office — un tel nom est
  indiscernable d'un flag, et den ne choisit pas un nom à la place de l'utilisateur. Le message rend
  le nom TAPÉ, pas sa forme aplatie.

### La collision, et ce qui la rattrape

L'aplatissement n'est pas injectif : « feat/essai » et « feat-essai » visent le même dossier et le
même nom de sandbox. C'est le contrôle de branche qui existait déjà dans `Assure` qui les distingue
— il refuse d'attacher un worktree checkouté sur une autre branche que celle demandée, en nommant
les deux. Testé (`TestAssureRefuseDeuxBranchesQuiSAplatissentPareil`).

### Annoncé

Sans ça, l'utilisateur cherche « feat/essai » dans `den ls` et ne l'y trouve jamais :

```
worktree "feat/essai" : la branche garde son nom, la sandbox devient duo.feat-essai
```

### Mesuré sur le banc

```
$ den duo -w feat/essai --detach
worktree "feat/essai" : la branche garde son nom, la sandbox devient duo.feat-essai
worktree repo-a : …/wt/feat-essai/repo-a
worktree repo-b : …/wt/feat-essai/repo-b
sandbox duo.feat-essai prête (détachée)

hôte  : branch --show-current → feat/essai
        worktree list → …/wt/feat-essai/repo-a  957e617 [feat/essai]
VM    : branch --show-current → feat/essai
        commit 2e657f3 « commit depuis la VM sur feat/essai »  → remonté sur l'hôte ✅
den ls: duo.feat-essai   duo   feat-essai   running   5
```

Les mounts de F1 sont toujours là (les deux `.git`), donc F4 ne les défait pas.

## F3 — le dossier qui portait le worktree part avec lui

En layout central, le worktree vit dans un dossier à lui (`<root>/<wt>/<repo>`). `den rm` le vidait
et laissait la coquille : qui spawne et détruit à la journée finit par regarder une liste de
dossiers vides.

`os.Remove`, pas un `RemoveAll` : **le refus ENOTEMPTY EST le mécanisme.** Il garde le dossier tant
qu'un autre repo du même nest y a son worktree — les `Retire` d'un `den rm` défilent repo par repo,
et seul le dernier le trouve vide — et il garde aussi ce que den n'a pas posé : un fichier de
l'utilisateur, ou la corbeille de repli `<repo>/.den/.trash` en layout per-repo, quand le rename
inter-systèmes de fichiers a dû s'y replier.

L'erreur est ignorée à dessein : le worktree, lui, EST retiré. Faire échouer `den rm` sur un dossier
vide qui résiste ferait passer un détail cosmétique pour un échec de suppression.

Le nettoyage a lieu sur les deux sorties en succès, y compris celle où le dossier avait déjà disparu
(l'utilisateur l'a effacé à la main) — c'est précisément là que la coquille vide traîne le plus
sûrement. Jamais sur le refus « worktree sale », qui doit tout laisser intact.

**Le garde-fou** : `Retire` est exportée, et `Chemin("central", root, "", repo)` a pour dossier
porteur `worktree_root` LUI-MÊME. Un appel sans nom de worktree effacerait donc la racine de
l'utilisateur si elle se trouvait vide. Refusé explicitement, et testé.

### Mesuré sur le banc

```
avant : …/wt/feat-essai/{repo-a,repo-b}
$ den rm duo.feat-essai
worktree envoyé à la corbeille : …/trash/20260729-152602-duo.feat-essai-repo-a
worktree envoyé à la corbeille : …/trash/20260729-152602-duo.feat-essai-repo-b
sandbox duo.feat-essai détruite (le profil de l'agent est conservé)

après : worktree_root PRESENT, vide (0 entrée) ; feat-essai parti
        worktree list → plus que le dépôt principal
        branche feat/essai INTACTE, avec le commit fait dans la VM
```

Le `den rm` a donc aussi traversé l'aller-retour de F4 en production : il n'a que le nom
`duo.feat-essai`, et il a nettoyé les bons dossiers.

## État

`gofmt` propre, `go vet` propre, `go test ./...` → **673 passés, 0 échec**. Deux commits sur `main` :
`2480459` (F4) et `9d1960f` (F3).

## Restant

**F5** reste à savoir, pas un bug (la `local-policy` globale de ce poste autorise 197 hôtes pour
toutes les sandboxes). Puis Plan 3 (`den ports`, `-i`) et Plan 4 (`den build`).
