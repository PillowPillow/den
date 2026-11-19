# den V2 — clés de repo portées par leur origine, `den scan`

Date : 2026-11-17. Branche : `v2`. Taille : projet. Statut : approuvé le 2026-11-17 (fiche
`2026-11-17-den-scan-repos-validation.md`, 35 lignes acceptées ; la fiche gagne sur ce document).
Amendé le 2026-11-19 après la revue de la PR #102 (§9) : sur les points du §9, ce document gagne sur la fiche.
Remplace la décision D1 de `2026-11-17-den-repo-map-design.md` (« la seule correspondance clé → chemin est
`repos:` du global, écrit à la main ») et son non-but « découvrir des repos automatiquement ».

## 1. Intention

Ajouter un repo à un nest oblige aujourd'hui à écrire son chemin absolu à la main dans `repos:` du
`~/.den/den.yaml` global, sous une clé que le `den.yaml` de projet réutilise. L'utilisateur ne veut plus
écrire ces chemins. Le chemin change d'un poste à l'autre ; l'origine git, non. Le `den.yaml` de projet
déclare donc chaque clé avec son origine, et `den scan` trouve les clones sur le disque et enregistre leur
chemin. La clé reste le nom du repo : `api` et `back` sont des fins d'origine banales, un nom tiré de
l'origine entrerait en collision.

## 2. Décisions de l'utilisateur (2026-11-17)

- U1. Un `den.yaml` de projet déclare ses clés : chacune porte **soit** `origin:`, **soit** `path:`.
- U2. `path:` monte le dossier tel quel, sans passer par la table. Un chemin relatif et `~` restent résolus
  comme aujourd'hui, par rapport au dossier du `den.yaml` (`internal/config/load.go:288`).
- U3. Le `~/.den/den.yaml` global déclare des clés avec un chemin absolu et une origine facultative.
- U4. `origin:` est résolue en chemin par une table que `den scan` remplit (approche A, §5).
- U5. La table vit dans l'état de den (`state/v2/repos.yaml`), pas dans le `den.yaml` de l'utilisateur.
- U6. Un conflit de clés casse.
- U7. Deux clones de la même origine : un avertissement, et le premier trouvé gagne si l'origine n'a pas
  encore de chemin. L'utilisateur corrige à la main s'il le faut.
- U8. Les clés restent le nom du repo ; pas de champ `name:`, pas de nom tiré de l'origine.
- U9. Alias SSH et `insteadOf` : non supportés pour le moment.
- U10. La commande s'appelle `den scan`.

Décisions du 2026-11-19 (revue de la PR #102) :

- U11. Une clé sans chemin est un avertissement, pas un refus : `den up` la signale et demande confirmation ;
  si l'utilisateur valide, l'instance démarre sans elle (§6.3, étape 4).
- U12. `--yes` de `den scan` ne change pas : le doublon planté qui gagne au premier scan reste un résidu
  accepté, documenté dans `docs/commands.md` (§7).

## 3. Buts, non-buts, anti-buts

Buts :
- G1. Un `den.yaml` de projet est portable : clés et origines, aucun chemin propre à un poste sauf les
  `path:` choisis exprès. Une clé donne le même `/den-git/<clé>`, le même `start:` et le même `wt/` partout.
- G2. `den scan [dossier]` remplit la table sans édition manuelle.
- G3. `den up` sur une origine sans chemin liste en une fois toutes les origines manquantes avec leur
  ligne `git clone`, nomme `den scan` pour celles déjà clonées, puis demande s'il démarre sans elles (U11).
- G4. La liste « Machine » de la carte des repos d'une VM continue de lister les repos du poste qu'aucun
  nest ne monte.
- G5. Le `~/.den/den.yaml` actuel de l'utilisateur (36 entrées `clé: /chemin`) se charge sans modification.

Non-buts :
- Résoudre les alias SSH (`Host gitlab-perso`) et les réécritures `insteadOf` (U9). Le remède est un `path:`
  pour la clé dans le global (§6.3).
- Relire les remotes d'un clone à chaque `den up`.
- Cloner un repo absent.
- Proposer un bloc `repos:` prêt à coller pour les clones sans clé (offert, non retenu).
- `den scan --list`, la suggestion « vouliez-vous dire » sur une origine mal tapée, `--prune` : plus tard.

Anti-buts (échec même si ça marche) :
- den réécrit le `den.yaml` de l'utilisateur.
- Un jeton (`https://user:token@host/…`) atterrit dans un fichier de den ou dans la VM.
- Une VM change, sans que l'humain le voie, le dossier qu'un autre nest monte.
- Le nom d'un repo dépend du poste.

## 4. Contraintes

- C1. Les repos sont montés en lecture-écriture : une VM peut réécrire `.git/config` d'un repo monté ou y
  planter un faux clone. Le code traite déjà ce fichier comme hostile (`internal/template/copy.go:50`).
- C2. `git remote get-url` applique `insteadOf` et lit la config du repo : den lit `.git/config` lui-même.
- C3. Aucun test ne lance de processus autre que git (CLAUDE.md).
- C4. Décodage strict partout (`KnownFields(true)`).
- C5. `Repo.Name` est unique par nest (`internal/nest/resolve.go:232`) ; il nomme `/den-git/<name>`,
  `start:`, `--only`/`--without`. Le dossier de worktree central utilise le nom du dossier du clone
  (`internal/worktree/worktree.go:137`), et `Prepare` le recalcule pour un recreate (`prepare.go:195`).
- C7. La carte des repos de la VM ne casse pas (§6.8).
- C6. `den rm` et l'attache lisent les chemins enregistrés (`internal/instance/prepare.go:158`).

## 5. Approches

- **A — retenue. Une table origine → chemin persistée, écrite par `den scan` après confirmation.** L'humain
  voit et confirme les lignes avant l'écriture : c'est la défense contre une VM qui réécrit le remote de son
  repo pour capter l'origine d'un autre nest (C1). Coût : une commande de plus, et une entrée périmée quand
  un clone change de place (§6.6).
- B — écartée. `repo_roots: [...]` dans le global, résolu à chaque `den up` en lisant les `.git/config`.
  Pas de table ni de commande, mais un remote modifié prend effet au `up` suivant sans que l'humain le voie.
- Nommage (U8) — écartés : le dernier segment de l'origine (collisions `api`/`back` refusées, ou
  renommage implicite quand on ajoute un repo), le nom du dossier local (dépend du poste), un champ `name:`
  (autant garder la clé).

## 6. Conception

### 6.1 Identité d'une origine

Une origine se normalise en identité `hôte/chemin` :
- Formes reconnues : `scheme://[user[:mdp]@]hôte[:port]/chemin` (`ssh`, `git`, `http`, `https`) et la forme
  scp `[user@]hôte:chemin` (ce qui suit `:` est un chemin, jamais un port).
- On retire le schéma, l'identifiant et le mot de passe, le port, le suffixe `.git` et le `/` final
  *(appel de Claude : le port dépend du protocole, pas du repo)*.
- Toute l'identité passe en minuscules *(appel de Claude : GitHub et GitLab ignorent la casse)*.
- Une URL qu'aucune forme ne reconnaît (chemin local, `file://`) est refusée dans un `den.yaml`, et ignorée
  sans bruit sur un clone.

Exemple : `git@gitlab.com:Dig/api.git`, `ssh://git@gitlab.com:2222/dig/api` et
`https://jeton@gitlab.com/dig/api` donnent `gitlab.com/dig/api`.

### 6.2 Schéma

```yaml
# dev-home/den.yaml (commité, identique sur chaque poste)
repos:
  front-api: {origin: git@gitlab.com:dig/front/api.git}
  back-api:  {origin: git@gitlab.com:dig/back/api.git}
  scratch:   {path: ../scratch}
nests:
  oauth:
    repos: [{key: front-api}, {key: back-api, optional: true}]
    start: front-api

# ~/.den/den.yaml
repos:
  js.box: /Users/polochon/Development/Digitaleo/js.box          # forme actuelle, inchangée
  perso:  {path: /Users/polochon/src/perso, origin: git@github.com:me/perso.git}
```

- `repos:` devient légal dans un `den.yaml` de projet (retrait du refus `internal/config/load.go:124`).
- Une valeur de `repos:` est un scalaire (raccourci de `{path: …}`) ou un mapping `{origin, path}` :
  - projet : exactement un de `origin` et `path` (U1) ;
  - global : `path`, `origin`, ou les deux (U3).
- `RepoRef` d'un nest est inchangé : `{key}` ou `{path}`, plus `optional`.
- Une clé est un nom de dossier légal : pas de `/`, ni `.`, ni `..`, pas de point initial *(appel de Claude :
  elle nomme `/den-git/<clé>` et `wt/…/<clé>`)*.

### 6.3 Résolution d'une clé dans `nest.Resolve`

Portée : le fichier qui déclare le nest et le global.

1. **Conflit (U6).** Une clé déclarée des deux côtés refuse si les deux portent une `origin` d'identités
   différentes, ou si les deux portent un `path` différent. Le message nomme les deux fichiers.
2. **Chemin.** Le `path` du global gagne s'il existe : c'est le chemin de la clé sur ce poste, et le remède
   aux alias et aux doublons mal tranchés. Sinon le `path` du projet. Sinon l'identité de l'`origin`
   cherchée dans la table d'état.
3. **Vérification du chemin global.** Le projet donne une `origin` à la clé et le global lui donne un `path`
   sans `origin` : den up refuse si aucun remote de ce dossier (lu comme au §6.5, étape 2) n'a la même
   identité, en nommant les deux fichiers. Sans cette vérification, un projet d'équipe qui réutilise un nom
   de clé que l'utilisateur emploie pour un autre repo monterait ce repo sans erreur. Un global qui porte
   `path` **et** `origin` de même identité n'est pas vérifié : c'est l'utilisateur qui l'affirme, et c'est le
   remède d'un alias SSH, dont le remote ne donne pas la bonne identité.
4. **Absente.** Toutes les clés sans chemin du nest sont réunies dans une seule liste (G3), chacune avec
   `git clone <origine>`, puis « déjà cloné : `den scan <dossier où vivent vos clones>` ». Est absente
   aussi une clé dont l'entrée de la table pointe un dossier qui n'est plus un clone de cette identité
   (§6.6). `nest.Resolve` ne refuse pas : il range la liste dans `Resolved.MissingErr`, et `Prepare`
   décide une fois l'état de l'instance connu (U11) :
   - instance vivante : un avertissement, sans question, puisque ses montages sont fixés à sa création ;
   - création ou recreate sur un TTY : la liste sur stderr, puis « start <instance> without … ? », posée
     après toutes les autres vérifications. Oui : l'instance démarre sans ces repos. Non : rien n'est créé ;
   - hors TTY, et toujours pour `den exec` qui ne demande jamais : refus, avec la liste ;
   - un `start:` qui ne trouve plus de dossier parce qu'il visait une clé absente : refus qui nomme la liste.

   Les clés absentes entrent dans l'empreinte de configuration (seulement quand il y en a, pour ne pas
   déplacer celle des autres instances) : une fois la clé clonée, `den up` sur l'instance vivante signale
   la dérive. Une clé `optional` absente reste un avertissement, sans question.

- `Repo.Name` d'une entrée `key:` = la clé ; d'une entrée `path:` du nest = le nom du dossier, comme
  aujourd'hui.
- Le dossier de worktree central reste `wt/<wt>/<nom du dossier du clone>` : `worktree.Path` ne change pas
  *(appel de Claude : `Prepare` recalcule ce chemin et le compare au record (`internal/instance/prepare.go:195`) ;
  le passer à la clé casserait le recreate d'une instance enregistrée dont la clé diffère du dossier, comme
  `go-dgdev` → `go.dgdev`)*. Avec `-w` en disposition `central`, deux repos d'un nest au même nom de dossier
  sont refusés, puisqu'ils iraient au même `wt/<wt>/<nom>`.
- `MachineRepos` = les clés du global résolues, puis les entrées de la table dont le chemin n'y figure pas,
  nommées par le nom de leur dossier. Un chemin n'est listé qu'une fois (un fork porte deux identités) ; un
  chemin qui n'existe plus est omis.
- Les messages qui citent `repos.<clé>` (`internal/instance/prepare.go:261`, `internal/sbxenv/repomap.go:87`)
  nomment le fichier qui déclare la clé.

### 6.4 Table d'état `state/v2/repos.yaml`

```yaml
schema: 1
repos:
  gitlab.com/dig/front/api:
    path: /Users/polochon/Development/Digitaleo/front-api
```

Écrite seulement par `den scan`, de façon atomique (même procédé que `internal/registry`). Seule l'identité
est stockée, jamais d'identifiant ni de jeton. `state/` n'est jamais purgé.

### 6.5 `den scan [dossier] [--yes]`

`scan` ne lit aucun `den.yaml` pour décider quoi enregistrer et ne choisit aucune clé.

1. **Parcours.** Racine : l'argument, sinon le cwd. Profondeur maximale : 20 niveaux. Le parcours :
   - ne suit aucun lien symbolique ;
   - saute le dossier de den (`DEN_HOME`), `~/Library`, `node_modules`, `vendor`, et tout dossier dont le nom
     commence par un point ;
   - reconnaît un clone à un **dossier** `.git` et ne descend pas dans un clone ; un fichier ou un lien
     `.git` (worktree, sous-module) n'est pas un clone, et le parcours n'y descend pas non plus : un
     worktree est monté en écriture dans une VM, qui pourrait y planter un faux clone ;
   - ignore un chemin qui contient un caractère que `unicode.IsPrint` refuse (contrôle, format, bidi) ou
     de l'UTF-8 invalide : ce nom serait affiché à la confirmation et écrit dans la carte de chaque VM ;
   - ignore un dossier illisible ;
   - affiche sa progression ; lancé depuis l'intérieur d'un clone, il le signale et propose le parent.
2. **Remotes.** den lit `.git/config` lui-même (C2) : chaque `[remote "…"] url`, sans `insteadOf` ni
   `[include]`. Chaque remote reconnu rattache son identité au clone : un fork avec `origin` et `upstream`
   répond aux deux.
3. **Doublons (U7).** Une identité trouvée dans plusieurs clones : avertissement qui nomme tous les chemins.
   Gagne d'abord un clone dont le remote nommé `origin` porte l'identité, puis l'ordre lexical des chemins
   *(appel de Claude : sinon l'identité amont peut aller au fork ; l'ordre reste stable)*. Un second clone
   trouvé alors que l'entrée est déjà valide est signalé aussi.
4. **Plan.** Pour chaque identité trouvée : absente de la table → **ajout** ; présente et valide (le dossier
   existe et porte encore cette identité) → rien ; présente et invalide → **remplacement**. Une entrée
   invalide qu'aucun clone ne remplace est signalée périmée et conservée. Toute identité trouvée est
   enregistrée, qu'une clé la déclare ou non (G4).
5. **Confirmation.** Affichés d'abord : remplacements, doublons, identités déclarées par une clé ; le reste
   des ajouts tient en un compte *(appel de Claude : une liste de 40 lignes se valide sans être lue)*. Sur un
   TTY, den demande confirmation. `--yes` vaut pour les ajouts seulement : un remplacement demande toujours
   la confirmation, et hors TTY il est listé « non écrit » *(appel de Claude : un remplacement déplace un
   nest existant, c'est le cas de l'anti-but sur la VM)*. Hors TTY sans `--yes`, den refuse. Avec `--yes`,
   aucune ligne n'est confirmée : les ajouts qu'aucune clé ne déclare restent un compte, et un doublon est
   tranché par la règle de l'étape 3 (U12).
6. **Rapport final.** Les clés à `origin` toujours sans chemin, déclarées par le `den.yaml` de projet du cwd,
   les fichiers de l'index `state/v2/nests.yaml` et le global : listées, avec leur `git clone`.

`scan` ne lit que des fichiers et ne lance aucun processus.

### 6.6 Entrées périmées

Un clone déplacé, supprimé ou remplacé par un autre repo : `nest.Resolve` vérifie chaque entrée de la table
qu'il utilise avec `repos.Valid` (le dossier est un clone, un remote porte l'identité ; lecture de
`.git/config`, aucun processus). Une entrée invalide rend la clé absente (§6.3, étape 4) : la ligne nomme le
dossier périmé, puis `git clone` et `den scan`. Le `scan` suivant remplace l'entrée (§6.5, étape 4).
*(Avant l'amendement du 2026-11-19, seul un dossier disparu était vu, au `os.Stat` de `checkRepos` ; un
autre repo cloné au même chemin était monté sans un mot.)*

### 6.7 Migration

Les 36 entrées `clé: /chemin` du global se chargent telles quelles (G5). Les nests de `dev-home` gardent
leurs `key:`. Rendre `dev-home/den.yaml` portable revient à y déclarer `repos: {clé: {origin: …}}` ; le
`path` du global reste prioritaire tant qu'il existe, sans conflit puisque le projet ne porte pas de `path`.

### 6.8 Carte des repos montés dans la VM : non-régression

La carte (`2026-11-17-den-repo-map-design.md`, `internal/sbxenv/repomap.go`, `internal/instance/repomap.go`)
ne doit pas casser. Invariants :
- Inchangés : `schema: 1`, les champs JSON (`name`, `key`, `path`, `optional`, `branch`), les trois listes,
  `~/.den/repos.json`, `DEN_REPOS`, `~/.claude/rules/den-instance.md`, le texte Markdown et la ligne de
  remède (`den rm …`, puis `den up … --repo <host path>`).
- `mounted` et `nest_not_mounted` : `name` = `Repo.Name`, `key` = la clé, `path` = le chemin hôte, comme
  aujourd'hui. Seul effet visible : une clé différente de son dossier (`go-dgdev`) donne `name: go-dgdev`
  au lieu de `go.dgdev`, et la mention « (key …) » disparaît puisqu'elle égale le nom.
- `machine` : les clés du global, nommées par leur clé comme aujourd'hui, puis les entrées de la table
  (nouvelles), nommées par leur dossier. Dédoublonnage par chemin et omission des chemins absents (§6.3).
- Une instance vivante garde la carte écrite à sa création ; seule une création ou un recreate la réécrit.
- Les goldens `internal/sbxenv/testdata/repomap.*.golden` restent identiques octet pour octet pour une
  configuration qui n'utilise que des clés du global au nom de leur dossier ; un golden nouveau couvre les
  entrées de la table et une clé à `origin`.

## 7. Résidu accepté

Une VM qui réécrit le remote d'un repo monté peut faire proposer une identité *nouvelle* vers son dossier.
Sur un TTY sans `--yes`, la ligne apparaît dans la confirmation si une clé déclare cette identité ; sinon
elle tient dans le compte des ajouts, et avec `--yes` rien n'est confirmé (U12). Au premier scan, un faux
clone planté peut gagner un doublon si son remote s'appelle `origin` et que son chemin trie avant (§6.5,
étape 3) ; la ligne `duplicate` le montre. Une entrée valide ne bouge jamais ; un `path` du global gagne
toujours.

## 8. Laissé au constructeur

- La forme exacte des messages et de l'affichage du plan.
- L'emplacement du code : un paquet `internal/repos` (normalisation, parcours, table), suggéré.
- La mise à jour de `docs/den-yaml.md`, de `CLAUDE.md` (huit commandes, `repos:` en projet), du README et des
  goldens (édités à la main, pas de `-update`).

## 9. Amendements du 2026-11-19 (revue de la PR #102)

- **Clé absente** : avertissement et confirmation au lieu d'un refus (U11, G3, §6.3 étape 4). Contredit la
  fiche, partie C : les clés absentes entrent désormais dans l'empreinte de configuration.
- **Entrée périmée** : vérifiée à chaque `den up` par `repos.Valid` (§6.6). `repos.Valid` exige aussi un
  dossier `.git` : un lien `.git` n'est pas un clone, comme pour le parcours.
- **Origine** : une `origin` doit commencer par une lettre ou un chiffre et ne contenir que des lettres, des
  chiffres, `-._~:/@%+=,` ou des caractères non ASCII, sur l'URL brute comme sur l'identité décodée
  (`%24` donnerait `$`). Raison : den imprime l'origine dans une ligne `git clone` à coller, et un `den.yaml`
  de projet (d'un collègue, ou réécrit par une VM) pourrait y glisser `$(…)`. L'erreur ne répète pas
  l'entrée.
- **Caractères invisibles** : une identité, une clé de `repos:` et un chemin parcouru refusent tout
  caractère que `unicode.IsPrint` refuse (contrôle, format, bidi) ; une identité et une clé refusent
  aussi l'UTF-8 invalide.
- **Parcours** : pas de descente dans un dossier dont `.git` est un fichier ou un lien (§6.5, étape 1).
- **`--yes`** : inchangé, résidu documenté (U12, §6.5 étape 5, §7).
- Laissés : la double lecture du global par `den scan` et de la racine par le parcours (une petite lecture
  chacune) ; la limite de 128 Kio de `--argjson` dans la fusion des réglages de `den-claude` (taille
  irréaliste, non testable sans VM).
