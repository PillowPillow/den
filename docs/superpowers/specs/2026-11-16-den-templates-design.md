# den V2 — templates partagés entre dev homes

*den · branche `v2` · 16 nov. 2026 · sbx v0.53.0 `2c62838`*

Ce document fixe la conception du remplacement de `bases:` par `templates:`. Il prolonge
`2026-11-12-den-v2-scope-design.md` ; en cas d'écart sur ce sujet, ce document gagne. Il est aligné sur
la fiche de validation `2026-11-16-den-templates-validation.md` (39 lignes approuvées) ; en cas d'écart,
la fiche gagne.

Marquage : **[décidé]** = choisi par l'utilisateur pendant la session du 16 nov. 2026 ;
**[choix rédacteur]** = choisi par le rédacteur, à corriger à la relecture ; **[code]** = lu dans le
code de la branche `v2` (commit `1491a23`) ; **[aide]** = sortie `--help` de sbx v0.53.0 ;
**[non vérifié]** = hypothèse à prouver par une mesure ; **[mesuré]** = observé le 16 nov. 2026 sur sbx
v0.53.0 ; **[validé]** = tranché par l'utilisateur sur la fiche de validation.

## 1. Intention

Un **dev home** est un dépôt qui sert de point d'entrée à un environnement de travail (Digitaleo, les
projets Kampn legacy, de futurs projets indépendants). Chaque dev home versionne un `den.yaml` qui liste
tous les projets utilisables ; au lancement, l'utilisateur choisit ceux de la session (`--only`, déjà
présent).

La plupart des dev homes utilisent la même VM : celle de Digitaleo. Sa définition (image de départ, kits,
allowlist, scripts de build) doit vivre dans **un seul dépôt**, et chaque dev home doit **pointer** dessus
sans en recopier le contenu.

Aujourd'hui c'est impossible [code] : une base n'est cherchée que dans le `den.yaml` du projet (remontée
depuis le cwd) puis dans le fichier global (`nest.FindBase`, `internal/nest/resolve.go:158`). Un nest
déclaré dans un dépôt ne peut pas utiliser une base déclarée dans un autre. La commande actuelle
contourne en se plaçant dans `den-env` et en montant le dev home par `--repo`.

Second défaut corrigé au passage : l'image officielle sur laquelle `den build` démarre est un digest
embarqué dans le binaire den, remplaçable seulement par `agents.claude.image` dans le fichier global, digest
obligatoire [code]. Une nouvelle image sbx demande donc une release de den. C'est de la configuration, pas
du code [décidé].

## 2. Objectifs, non-objectifs, anti-objectifs

**Objectifs**

- O1. Un `den.yaml` (projet ou global) nomme des templates et dit où vit leur définition : un dépôt git
  distant ou un dossier local [décidé].
- O2. Un lieu (dépôt ou dossier) expose un ou plusieurs templates par un fichier `den-template.yaml` à sa
  racine [décidé].
- O3. Un nest choisit son template ; sinon il prend `default_template` [décidé].
- O4. `default_template` peut être directement une référence d'image officielle ; dans ce cas il n'y a
  aucun build [décidé].
- O5. Quand la définition distante a avancé, `den up` le dit et affiche la commande de mise à jour. den ne
  met jamais la machine à jour seul [décidé].
- O6. Aucun changement de code den quand sbx publie une nouvelle image officielle [décidé].
- O7. Une machine neuve n'a besoin que du clone du dev home et du `repos:` de son fichier global pour
  lancer une session.

**Non-objectifs**

- Télécharger une image construite. Le template reste construit localement par `den build`
  (`sbx template` n'a ni `pull` ni `push` local [aide]).
- Publier des kits sur un registre OCI (`sbx kit push`, marqué EXPERIMENTAL [aide]).
- Associer `repos.local.json` du dev home aux montages den (point 10 du handoff, « à voir »).
- L'arrêt de la VM en fin de prompt (lot L2), une commande `den stop`.
- Une période de compatibilité pour `bases:` : seul l'utilisateur emploie V2 [choix rédacteur].

**Anti-objectifs** (échec même si ça marche)

- Un préfixe `<source>:` sur les noms, ou une famille de commandes `den source` / `den lint` (retirés par
  le périmètre V2, ligne A11).
- Un dev home qui liste les kits, l'allowlist ou les étapes de build du template partagé.
- Un build par Dockerfile.
- Un chemin machine ou un secret dans un fichier versionné.
- Une allowlist qui change sur la machine sans décision de l'utilisateur.

## 3. Vocabulaire

- **template** : remplace « base ». Un nom, plus le lieu de sa définition. Le résultat construit est un
  template sbx, d'où le mot.
- **lieu** : un dépôt git (`repo:`) ou un dossier (`dir:`) qui porte un `den-template.yaml` à sa racine.
- **définition** : l'entrée d'un template dans `den-template.yaml` (image de départ, kits, allowlist,
  build).
- **image officielle** : une image publiée par sbx. Elle s'écrit par son adresse complète (ex.
  `docker.io/docker/sandbox-templates:claude-code-docker`) ou par son **nom court**, le tag seul (ex.
  `claude-code-docker`), que den complète avec `docker.io/docker/sandbox-templates:` [validé].

## 4. Approches considérées

| | Approche | Compromis | Sort |
|---|---|---|---|
| A | den récupère lui-même le dépôt de définition ; le `den.yaml` porte le pointeur | den gagne du code git réseau ; un dev home cloné fait confiance à une URL | **retenue**, dans la forme `templates:` proposée par l'utilisateur (§5) |
| B | L'utilisateur clone le dépôt de définition ; le fichier global le nomme | pas de code réseau, mais un clone manuel par machine et un avis de version qui écrit dans un clone que l'utilisateur édite | écartée |
| C | Publication par sbx (kits sur registre OCI, image en tar) | registre et CI nécessaires, le dev home doit lister les kits, artefact de plusieurs Go | écartée |

Déjà refusées avant cette session, non reproposées : build Dockerfile, `base: <clé repo>:<base>`, kits par
URL `git+https` dans chaque dev home.

## 5. Conception

### 5.1 `den.yaml` (global et projet, mêmes clés) [décidé]

```yaml
# ~/.den/den.yaml  ou  <dev-home>/den.yaml
templates:
  digitaleo:
    repo: git@gitlab.example:digitaleo/den-env.git
    # name: digitaleo        # optionnel ; défaut : la clé de l'entrée
    # ref: main              # optionnel ; défaut : la branche par défaut du dépôt [choix rédacteur]
  legacy:
    dir: ~/.den/templates    # le dossier porte den-template.yaml à sa racine
    name: kampn              # le lieu expose plusieurs templates
default_template: digitaleo  # un nom de template, ou une référence d'image

nests:
  dev:
    repos: [...]             # prend default_template
  old:
    template: legacy
    repos: [...]
```

- Une entrée porte exactement un de `repo:` ou `dir:` ; les deux ou aucun est une erreur de chargement.
- `name:` est une clé séparée, pas un suffixe `LIEU:NOM` : une adresse git contient déjà `:` [décidé].
- `dir:` relatif s'ancre au dossier du fichier qui le déclare ; `~` est développé (règle actuelle des
  chemins) [choix rédacteur].
- `nests.<n>.template` et `default_template` acceptent un nom de template ou une image officielle
  [validé].
- Les clés `bases:`, `nests.<n>.base` et `agents:` disparaissent. Le décodage strict les refuse ; le
  message nomme le fichier et la clé de remplacement [choix rédacteur].

**Résolution** [choix rédacteur] :

1. Template d'un nest : `nests.<n>.template`, sinon `default_template` du fichier qui déclare le nest,
   sinon `default_template` du fichier global.
2. **Aucune valeur n'est écrite dans le code de den** [validé]. Si rien ne fixe le template, den refuse et
   affiche les images officielles disponibles : il interroge Docker Hub
   (`https://hub.docker.com/v2/repositories/docker/sandbox-templates/tags`, sans authentification
   [mesuré] : 226 tags) et affiche les noms courts, sans les variantes datées (`-0.11.0`) ni nocturnes
   (`-nightly`). Hors ligne ou en cas d'erreur, il affiche seulement l'adresse de la page des tags. La
   requête passe par une dépendance injectée dans `cli.Deps`, pour que les tests n'ouvrent aucun socket.
3. Lecture d'une valeur (`default_template` ou `nests.<n>.template`) [validé] : den cherche d'abord un
   template déclaré de ce nom, dans `templates:` du fichier qui déclare le nest, puis du fichier global ;
   le projet masque le global. S'il n'en trouve pas, il traite la valeur comme une image officielle
   (adresse complète telle quelle, sinon nom court complété). Pas de règle de forme sur `/` ou `:`.
4. Conséquence : une faute de frappe dans un nom de template fait chercher une image de ce nom. Quand sbx
   ne trouve pas l'image, l'erreur de den dit qu'aucun template ni aucune image ne porte ce nom, et nomme
   les fichiers lus.

### 5.2 `den-template.yaml` (racine du lieu) [décidé pour l'existence et le rôle ; contenu : choix rédacteur]

```yaml
schema: 1
templates:
  digitaleo:
    from: docker.io/docker/sandbox-templates:claude-code-docker   # optionnel
    kits: [kits/ssh-known-hosts, kits/shell-zsh, stacks/base/kit]
    network:
      allow: [digitaleo.com:443, "*.digitaleo.com:443"]
    build:
      includes: [lib/common.sh]
      steps: [stacks/base/provision/gh.sh, stacks/base/provision/pnpm.sh]
      network:
        allow: [cdn.playwright.dev]
  kampn:
    from: digitaleo          # un template frère du même fichier
    build:
      steps: [stacks/legacy/php.sh]
```

- Même contenu que le bloc `bases:` actuel, moins `image:`. Décodage strict, comme `den.yaml`.
- Les chemins sont relatifs au fichier.
- Lieu `repo:` : un chemin qui sort du lieu (`..`, absolu, `~`, lien symbolique vers l'extérieur) est
  refusé, pour `kits`, `includes` et `steps` : un dépôt distant ne doit pas faire lire un fichier de la
  machine. Un kit qui n'est pas un chemin (référence que sbx résout lui-même) reste accepté et passe tel
  quel, comme aujourd'hui [code] [choix rédacteur].
- Lieu `dir:` : **la même règle stricte** [validé]. Aucun chemin ne sort du dossier.
- `from:` absent : le build démarre de `default_template` du fichier global s'il désigne une image
  officielle ; s'il est absent ou nomme un template, `den build` refuse et demande une image de départ
  [validé].
- `from:` = nom d'un frère : si ce frère n'est pas construit, `den build` le construit d'abord,
  automatiquement, et annonce chaque build avant de le lancer [validé]. Un cycle entre frères est une
  erreur de chargement.
- Le tag du template sbx construit n'est plus déclaré : den le dérive, `<nom dans den-template.yaml>:v2`.

### 5.3 Copie locale d'un lieu `repo:`

- **Une copie par template construit**, pas par URL [choix rédacteur] :
  `<denHome>/gen/.templates/<tag sans ':v2'>/`. Le point initial suit la règle de `.kits` et `.build` :
  aucun nom de sandbox ne peut posséder ce dossier. Deux templates du même dépôt ont deux copies ;
  mettre l'un à jour ne touche pas les kits ni l'allowlist de l'autre.
- La copie est toujours au commit de la fiche (§5.5). `den build` prépare la nouvelle version dans un
  dossier d'attente, construit depuis lui, et ne remplace la copie et la fiche qu'après un build réussi.
  Un build raté laisse la copie, la fiche et le template sbx précédents intacts. Les kits, l'allowlist
  et l'image bougent donc ensemble, au même moment.
- `den up` vérifie que le commit de la copie est celui de la fiche ; sinon il refuse et donne
  `den build <template>`.
- den ne lit jamais la copie d'une façon qui exécute son contenu sur l'hôte : pas de hooks git
  (`core.hooksPath=/dev/null`), pas de sous-modules.
- Un lieu `dir:` est lu en place, sans copie, comme une base aujourd'hui.

### 5.4 Commandes

**`den build <template> [--pull]`**

- Résout `<template>` comme un nest le ferait depuis le cwd (projet puis global).
- Lieu `repo:` sans copie locale : den clone. Si l'URL vient d'un fichier **projet** et n'a jamais été
  récupérée sur cette machine, den affiche l'URL et demande confirmation ; sans TTY il refuse et nomme
  `--yes` [choix rédacteur]. Raison : la définition porte l'allowlist et des scripts exécutés dans la VM ;
  cloner un dev home ne doit pas suffire à faire confiance à une URL.
- `--pull` : récupère `ref` et avance la copie avant de construire. Sans `--pull`, den construit la copie
  telle qu'elle est.
- Le build lui-même ne change pas (VM de build, étapes, `sbx template save`, suppression) [code].
- En fin de build, den écrit la fiche du template (§5.5).
- Image du kit `den-claude` pendant un build : la référence `from` si c'est une image ; si `from` nomme un
  frère, l'image racine lue dans la fiche de ce frère.
- Un template qui est une référence d'image n'a pas de build : `den build` le dit et sort en erreur.

**`den up`**

- Template nommé : refuse si le template sbx n'existe pas, si la copie locale manque, ou si la fiche
  manque ; le message donne `den build <template>` (et le dossier d'où le lancer pour un fichier projet).
  `den up` ne clone jamais et ne construit jamais.
- Avis de version, lieu `repo:` : un `git ls-remote <url> <ref>` avec un délai court (3 s
  [choix rédacteur]). Si le commit distant diffère du commit de la fiche :
  `template digitaleo: a newer definition is available (<8 hex> -> <8 hex>); run 'den build digitaleo --pull'`.
  Hors ligne, délai dépassé ou erreur git : aucun message, aucun échec.
- Avis de dérive, lieu `dir:` : den compare le hash de la définition (fichier, kits, includes, steps) au
  hash de la fiche ; s'il diffère : `template legacy: its definition changed since the last build; run 'den build legacy'`.
- Avis d'image, tous lieux, `from` en tag : pas de vérification réseau en V2.0 [choix rédacteur]. La
  fiche garde le digest résolu pour qu'un lot ultérieur puisse l'ajouter.
- Les avis vont sur stderr et ne bloquent jamais `den up`.
- `den exec` et `den shell` n'émettent pas d'avis (stdout et stderr de `exec` restent ceux de la
  commande) [choix rédacteur].

**Image du kit `den-claude` sur un template construit** [choix rédacteur]

- `den up` donne au kit l'image racine de la fiche : le digest résolu s'il a été enregistré, sinon la
  référence. `sandboxOptions.template` porte le tag construit, comme aujourd'hui [code]. Un tag qui a
  bougé depuis le build ne change donc pas une VM créée sur un ancien instantané.
- [non vérifié] le moyen de lire le digest d'un tag sans docker (sbx, ou l'inspection du template de
  build). Si aucun n'existe, la fiche garde la référence seule ; l'effet d'un tag déplacé sous un
  instantané est alors à mesurer dans le même spike que ci-dessous.

**Nest sur une référence d'image (sans build)**

- Le kit `den-claude` reçoit la référence comme image ; aucun `sandboxOptions.template` n'est écrit.
- [mesuré] avec `docker.io/docker/sandbox-templates:claude-code-docker` : `sbx --no-input env create
  --force --skip-host-commands` crée la VM depuis un kit `kind: sandbox` dont l'image est ce tag, sans
  `sandboxOptions.template` ; sbx télécharge lui-même les couches manquantes ; le PID 1 est `tini`. Après
  `sbx run --detach --name <instance>` (l'appel de `startDetached`), la VM tourne encore 4 minutes plus
  tard et `sbx exec` répond. Sans cet appel, la VM créée s'arrête en moins de 100 s ; non comparé à une
  VM créée depuis un template, donc pas attribué au tag.
- [non vérifié] les autres images officielles (`claude-code`, `claude-code-minimal`, `shell`, …) : ni le
  maintien en vie, ni la présence de Claude Code.
- Une image dont l'ENTRYPOINT rend la main arrête la VM (S6e, `docker.io/sbx/claude`). den ne peut pas le
  savoir avant le démarrage : l'erreur de `den up` nomme la clé `default_template` ou `template` à corriger.
- Les mesures précédentes (S6e comprise) sont dans `docs/superpowers/specs/2026-11-12-den-v2-spikes.md`.

### 5.5 Fiche d'un template construit

`<denHome>/state/v2/templates/<tag sans ':v2'>.yaml`, écrite de façon atomique, estampillée d'un schéma,
comme les fiches d'instance :

| Champ | Rôle |
|---|---|
| `template` | nom dans `den-template.yaml` |
| `tag` | tag du template sbx (`digitaleo:v2`) |
| `source` | `repo` + URL + `ref`, ou `dir` + chemin absolu |
| `commit` | commit construit (lieu `repo:`) |
| `definitionHash` | hash de la définition construite |
| `from` | image racine de la chaîne `from` : référence demandée, et digest résolu si den a pu le lire |
| `builtAt` | date du build |

**Collision** : deux entrées qui mènent au même tag depuis deux sources différentes (ex. `digitaleo` du
projet et `digitaleo` du global, sur deux dépôts) ne peuvent pas coexister. `den build` refuse quand la
fiche existante nomme une autre source, et donne le remède (`--force` pour remplacer) ; `den up` refuse
quand la source résolue diffère de celle de la fiche [choix rédacteur].

### 5.6 Ce qui ne change pas

- Les overlays en lecture seule (`Plan.Overlays`) : tout fichier que den ou sbx lit pour l'instance et qui
  se trouve sous un montage lecture-écriture reste couvert. `den-template.yaml` et la copie locale entrent
  dans cette liste.
- `--skip-host-commands` sur chaque `env create` / `env rm`.
- La fiche d'instance garde le nom du template et son tag à la place de `base` / `image` ; son schéma
  passe à 2. **Le nouveau den ne lit pas le schéma 1** [validé] : l'utilisateur supprime toutes ses
  instances avec le den actuel avant d'installer le nouveau. Conséquence [code] : `registry.decode`
  refuse tout autre schéma (`internal/registry/registry.go:136`) et `checkSiblings` refuse dès qu'une
  fiche voisine est illisible (`internal/instance/remove.go:124`), donc une instance oubliée bloque
  `den rm` ; le message nomme le fichier de la fiche à supprimer à la main.
- `configHash` : la définition résolue et la référence `from` y entrent, à la place de l'image d'agent.
- Toute commande git passe par `cli.Deps.Git`. Les tests clonent un dépôt local par chemin : aucun socket.

## 6. Conversion (fait partie du livrable)

0. Avant d'installer le nouveau den : `den rm` de chaque instance avec le den actuel. Le 16 nov. 2026,
   `sbx ls` montre une instance, `am` (arrêtée).
1. `~/.den/den.yaml` : `bases.pillow` devient `~/.den/templates/den-template.yaml` (template `pillow`,
   scripts et kits `pillow-env` et `ssh-known-hosts` déplacés dans ce dossier, puisqu'aucun chemin ne sort
   du lieu) ; le fichier global gagne `templates.pillow.dir: ~/.den/templates` et `default_template`. Les
   kits de profil (`~/.den/kits/perso`) ne sont pas concernés : `profiles:` reste dans `den.yaml`.
2. `den-env` (branche `den-v2`) : `den-template.yaml` à la racine, avec le template `digitaleo` repris de
   `bases.digitaleo`.
3. Dev home Digitaleo : un `den.yaml` avec `templates.digitaleo.repo`, `default_template: digitaleo` et les
   nests `dg`. Les nests quittent le `den.yaml` de `den-env`. **Ouvert** : quels nests, sous quels noms
   — à décider avec l'utilisateur au moment de la conversion, pas un prérequis du code.
4. Rebuild des deux templates ; les instances existantes sont recréées (`den rm` puis `den up`).
5. README et `CLAUDE.md` : vocabulaire `template`, nouvelle arborescence du den home.

## 7. Laissé au constructeur

- Le découpage en paquets (où vit la résolution des lieux, où vit la fiche).
- Le format exact du slug de la copie locale et le calcul du hash de définition.
- Les textes d'erreur, dans la convention du dépôt : nommer le fichier à corriger et le remède.
- L'ordre des tâches, sous une contrainte : la mesure encore [non vérifié] de §5.4 (lecture du digest
  d'un tag) vient en premier.
