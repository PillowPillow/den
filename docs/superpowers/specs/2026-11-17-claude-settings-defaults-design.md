# Défauts de settings Claude portés par un kit — design court

Date : 2026-11-17. Taille : petit changement (den + den-env).
Validé ligne par ligne : `2026-11-17-claude-settings-defaults-validation.md` (la fiche gagne sur ce document).

## Intention

Réduire la friction d'adoption de den et du dev home Digitaleo : toute personne qui lance une VM du
template `digitaleo` reçoit des réglages Claude Code par défaut (marketplace et plugin `dgdev`,
statusline `dgdev statusline`) sans écrire de kit perso. Un kit placé plus loin dans la liste (un
kit perso, par exemple) surcharge ces défauts clé par clé.

## Objectifs

- Un kit pose des **défauts** de `~/.claude/settings.json` sans livrer le fichier lui-même.
- Le `settings.json` déjà présent dans la VM (copié par un kit perso, ou modifié dans la VM) gagne
  toujours sur un défaut.
- Le template `digitaleo` livre ces défauts : marketplace `dgdev`, `enabledPlugins."dgdev@dgdev"` (le
  plugin Claude Code, distinct de la commande `dgdev`), `statusLine` = `dgdev statusline` sans option.
- Les défauts s'appliquent à toute VM du template, avec ou sans kit perso : un kit perso qui livre son
  propre `~/.claude/settings.json` ne les efface pas, ils y sont fusionnés après la copie.
- Le README de den-env indique de poser `DGDEV_USER` et `DGDEV_DEV_ROOTS` dans `env:` du
  `~/.den/den.yaml` de chaque poste (den transmet cette section à la VM) ; den-env n'en fournit
  aucune valeur. `dgdev statusline` fonctionne sans elles (vérifié dans la VM `oauth`).

## Non-objectifs

- Les autres marketplaces et plugins Digitaleo : chaque projet les déclare dans son
  `.claude/settings.json`.
- `DGDEV_DOCKER_IMAGES_PHP` : reste dans la config perso de Nicolas.
- Le kit `perso` n'est pas modifié.

## Anti-objectifs

- Un défaut qui écrase une valeur perso : échec même si la fonctionnalité « marche ».
- Une imposition des valeurs d'équipe (managed settings de Claude Code) : écartée.

## Approches

1. **Retenue — convention portée par den.** L'étape `setup.startup` du kit `den-claude`
   (`internal/sbxenv/den-claude/spec.yaml`) fusionne aussi chaque `~/.den/claude-settings.d/*.json`
   *sous* `~/.claude/settings.json`. Un template livre un fichier JSON dans le `files/home/` d'un
   de ses kits, sans script. den possède déjà cette étape de fusion ; la convention sert à tout
   template.
2. Écartée — script dans le kit de den-env : chaque template réécrit la fusion, den ignore la
   convention.
3. Écartée — managed settings de Claude Code : impose au lieu de proposer un défaut.
4. Écartée — `files/home/.claude/settings.json` dans le kit d'équipe : un kit suivant remplace
   probablement le fichier entier (non vérifié), aucun défaut ne survit sous un kit perso.

## Décisions

- **Emplacement** : `~/.den/claude-settings.d/*.json` dans la VM (choix de Claude : chemin possédé
  par den, à côté de `~/.den/repos.json`, hors de l'espace de noms de Claude Code).
- **Sémantique de fusion** : les fragments sont lus dans l'ordre lexical de leur nom ; un fragment
  plus loin gagne sur un fragment plus tôt ; le `settings.json` existant gagne sur tous. Fusion
  récursive des objets (`*` de jq) ; un tableau ou un scalaire est remplacé en entier. Le résultat
  est écrit dans `~/.claude/settings.json` : une clé déjà présente garde sa valeur, le défaut ne
  remplit que les clés absentes. L'auteur d'un kit règle l'ordre entre fragments par un préfixe
  numérique du nom de fichier (`10-…`, `50-…`).
  Forme : `jq -s 'reduce .[] as $x ({}; . * $x)' <fragments triés> <settings.json ou {}>`.
- **Moment** : à chaque démarrage, après la copie des `files/home/` de tous les kits (fait déjà
  établi pour l'étape existante). Conséquence assumée : une valeur changée dans la VM reste ; une
  clé de défaut supprimée revient au démarrage suivant (pour la neutraliser, un kit perso lui donne
  une autre valeur).
- **Erreurs** : un fragment qui n'est pas un objet JSON fait échouer le démarrage avec un message
  qui nomme le fichier, comme l'étape existante pour `settings.json`. Aucun répertoire, ou un
  répertoire vide : rien ne change.
- **VM de build** : `sbxenv.DenClaudeBuildKit` n'a pas de bloc `setup:` ; elle ne reçoit pas les
  défauts. Inchangé.
- **den-env** (branche depuis `den-v2`, clone `~/Development/Digitaleo/den-env`) : un kit mixin
  `kits/claude-defaults/` avec `files/home/.den/claude-settings.d/50-digitaleo.json`, ajouté à
  `templates.digitaleo.kits` de `den-template.yaml` ; une section README pour `DGDEV_USER` et
  `DGDEV_DEV_ROOTS`.

## Laissé au constructeur

- Le texte exact du shell dans `spec.yaml`, dans le style de l'étape existante (`merge`, fichier
  temporaire, `mv`).
- Tests : `render_test.go` vérifie le texte du script (aucun test ne lance `sh` ni `jq`, convention
  du dépôt). La sémantique jq se vérifie à la main avec `jq` sur l'hôte, puis dans une VM recréée.
- Mise à jour de `CLAUDE.md` (paragraphe `den-claude`) et de la doc utilisateur qui décrit les kits.
