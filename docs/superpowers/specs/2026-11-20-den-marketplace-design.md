# Marketplace Claude Code de den — design

Date : 2026-11-20. Taille : projet (un plugin, huit skills, deux tranches).
Validé ligne par ligne le 2026-11-20 : `2026-11-20-den-marketplace-validation.md` (la fiche gagne sur ce document).

## Intention

Un coéquipier qui installe den pour la première fois doit atteindre un `den up` qui marche sur ses
propres repos, avec sa propre configuration Claude dans la VM, sans lire tout le README. Aujourd'hui
ce chemin passe par cinq prérequis à vérifier, un jeton à stocker, un `den.yaml` écrit à la main et
un kit de profil monté à la main.

den livre donc un **plugin Claude Code** dont les skills tournent dans Claude Code **sur l'hôte**.
Ils vérifient, écrivent la configuration, lancent den, et diagnostiquent. Une VM ne peut ni éditer
`~/.den` ni lancer `den` : aucun skill de ce plugin n'est fait pour tourner dans une VM.

## Objectifs

- Une marketplace installable en deux commandes depuis le dépôt `PillowPillow/den`.
- Premier usage : prérequis vérifiés, jeton guidé, premier nest écrit, instance créée et vérifiée.
- Profil : un kit de profil créé depuis la configuration Claude de l'hôte, déclaré sous `profiles:`.
- Les faits que les skills énoncent sont ceux de `docs/`, et un test échoue quand ils divergent.
- Le plugin et le binaire den portent le même numéro de version.

## Non-objectifs

- Contenu d'équipe (template d'équipe, kits d'équipe, nests d'équipe) : il reste dans le dépôt ou la
  marketplace de l'équipe. Le plugin den est générique.
- Distribution de kits, de templates ou de profils par la marketplace.
- Migration depuis v1 : v1 est retiré depuis v2.0.0.
- Toute modification du CLI. `den check` (validation d'un `den.yaml` sans effet de bord) est un
  besoin constaté, traité dans une spec séparée.
- Serveur MCP, hooks, agents dans le plugin : des skills seulement.

## Anti-objectifs

Chacun est un échec même si le skill « marche ».

- Un skill qui lit, affiche ou écrit le jeton Claude, ou toute valeur de secret.
- Un skill qui élargit le réseau (`network.allow`, `sbx policy`) sans que l'utilisateur ait nommé
  l'hôte et vu la modification.
- Un skill qui réécrit un `den.yaml` existant en entier : il perd les commentaires. Les skills font
  des éditions minimales et montrent le diff avant d'écrire.
- Un skill qui énonce un fait absent de ses pages de référence, ou qui en contredit un.
- Un skill qui lance `den rm --force`, ou `den rm` sans confirmation.
- Un skill qui touche aux chemins de v1 (`config.yaml`, `stacks/`, `nests/`, `state/sandboxes/`).

## Contraintes (faits)

Format du plugin, rapporté par un sous-agent depuis la doc Claude Code le 2026-11-20 (citations
issues d'un résumé : le constructeur relit les pages avant de s'y fier) :

- Une marketplace est un `.claude-plugin/marketplace.json` à la racine d'un dépôt ; champs requis
  `name`, `owner.name`, `plugins[].name`, `plugins[].source`. Une `source` relative commence par
  `./` et ne contient pas `..`.
- À l'installation, Claude Code copie **seulement le dossier du plugin** dans son cache. Un skill ne
  peut pas lire `docs/` du dépôt.
- Un skill de plugin s'appelle `/<plugin>:<skill>` ; il vit dans `skills/<nom>/SKILL.md`.
  `${CLAUDE_PLUGIN_ROOT}` est substitué dans le contenu d'un `SKILL.md`.
- La version vient de `.claude-plugin/plugin.json`. Sans elle, chaque commit compte comme une mise à
  jour. La mise à jour automatique est désactivée par défaut pour une marketplace tierce.
- Un `.claude/settings.json` de projet peut déclarer la marketplace (`extraKnownMarketplaces`) ;
  chaque coéquipier lance quand même une commande d'installation.

Faits de den, lus dans le dépôt :

- Aucune commande n'a de `--dry-run` ni de `--json` : un `den.yaml` ne se valide que par une commande
  qui crée une VM.
- `den up` attache Claude de façon interactive : un skill ne peut pas le piloter. `den exec <nest> --
  <cmd>` crée l'instance si besoin, lance une commande et rend son code de sortie.
- `den exec` sans terminal refuse quand un repo du nest n'a pas de dossier.
- La première création tire l'image : plusieurs minutes.
- `docs/security.md` : lire un `den.yaml` de projet venu d'un tiers avant `den up`.

## Approches

1. **Retenue par Nicolas — le plugin dans le dépôt den.** `marketplace.json` à la racine, plugin
   dans `plugins/den/`. Version du plugin = version de den, montée par la release existante. Les
   pages de `docs/` dont les skills ont besoin sont copiées dans le plugin, et un test Go échoue
   quand une copie diffère. Coût : des pages en double dans l'arbre ; le clone de la marketplace
   contient le code Go.
2. Écartée — dépôt `den-marketplace` séparé. Les skills sortent sans release de den, mais deux
   releases à tenir alignées, et aucun test ne peut comparer les skills à `docs/`.
3. Écartée comme base — skills minces, vérité dans le CLI (`den check`, `--help`). Pas de doc en
   double, mais du CLI à écrire avant le premier skill, et `--help` ne couvre ni les kits ni les
   templates. La pièce `den check` est gardée pour une spec séparée.
4. Écartée — skills qui lisent la doc sur GitHub à l'exécution : réseau requis à chaque usage, et
   faux quand le binaire installé est plus ancien que `main`.

## Disposition

```
.claude-plugin/marketplace.json        # name: den, owner: PillowPillow, un plugin épinglé sur un tag
plugins/den/
  .claude-plugin/plugin.json           # name: den, version, description
  skills/<nom>/SKILL.md                # un dossier par skill
  reference/                           # copies de docs/, octet pour octet
    den-yaml.md commands.md templates.md security.md ssh.md files.md
```

Installation, à vérifier par le constructeur avant de l'écrire dans le README :
`claude plugin marketplace add PillowPillow/den`, puis `claude plugin install den@den`.

`migrating-from-v1.md` n'est pas copié. Le tableau de dépannage et les prérequis vivent dans le
README, qui n'est pas copié non plus : `den:setup` et `den:troubleshoot` portent leur propre
procédure. Ces deux tableaux quittent donc le README pour une page de `docs/`, copiée dans le plugin
et gardée par le test de synchronisation ; le README renvoie à cette page.

## Garde-fous contre la dérive

- **Copies de référence.** Un test Go, hermétique (lecture de fichiers seulement), échoue quand
  `plugins/den/reference/<page>.md` diffère de `docs/<page>.md`, ou quand une page attendue manque.
  Une tâche `task plugin-sync` refait les copies. (Des liens symboliques sont
  écartés : leur sort dans le cache du plugin est inconnu.)
- **Version.** `plugin.json` porte la version de den sans le `v`. Un test Go échoue quand elle
  diffère de la dernière section de `CHANGELOG.md`. `.claude/commands/release.md` est modifié : le
  commit de release porte le changelog, `plugin.json` **et** `marketplace.json`.
- **Épinglage sur le tag de release (décidé par Nicolas).** L'entrée du plugin dans
  `marketplace.json` n'est pas un chemin relatif : c'est une source git qui nomme le sous-dossier
  `plugins/den` du dépôt et le tag de la release (`ref: vX.Y.Z` ; forme `git-subdir` selon le
  rapport, à vérifier). Un coéquipier reçoit donc les skills de la dernière release, qui
  correspondent au binaire publié, et jamais ceux de `main`. Le commit de release met à jour ce
  `ref` avec `plugin.json`. Un test Go échoue quand le `ref` diffère de `v` + la version de
  `plugin.json`. Tant qu'aucune release ne contient le plugin, `marketplace.json` n'est pas
  installable depuis GitHub : le plugin s'essaie depuis un chemin local.
- **Structure.** Un test Go vérifie que chaque `skills/*/SKILL.md` a un frontmatter avec `name` et
  `description`, et que `marketplace.json` et `plugin.json` sont du JSON valide avec leurs champs
  requis.
- **Écart binaire / plugin.** Chaque skill commence par `den version` et lit la version du plugin
  dans `${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json`. Majeure ou mineure différentes : le skill
  le dit, nomme la commande de mise à jour, et continue. `den` absent : le skill renvoie à
  `den:setup`. Un `den --help` qui liste `doctor` ou `init` est v1 : le skill s'arrête et le dit.

## Règles communes aux skills

- Un skill tient ses faits de `reference/`. Il lit la page utile au moment d'agir, il ne la récite
  pas de mémoire.
- Toute écriture dans un `den.yaml` : édition minimale, diff montré, puis écriture.
- Le skill vérifie par une commande ce qu'une commande peut établir (`sbx version`, `sbx policy
  ls`, `den ls`) au lieu de le demander à l'utilisateur.
- Une commande interactive ou qui manipule un secret est donnée à l'utilisateur, à lancer dans son
  propre terminal. Le skill reprend ensuite par une vérification (`sbx secret ls`).
- `den up` n'est jamais lancé par un skill. Le skill crée et vérifie l'instance par
  `den exec <nest> -- true`, en arrière-plan quand l'image doit être tirée, puis donne
  `den up <nest>` à l'utilisateur.
- Un `den.yaml` de projet trouvé depuis le dossier courant est montré à l'utilisateur (repos montés,
  hôtes ouverts, template, kits) avant toute création.

## Les skills

### Tranche 1 — premier usage

**`den:setup`** — de rien à une instance qui marche.
1. Prérequis, chacun par sa commande de vérification du README : plateforme, sbx ≥ `sbx.MinVersion`,
   sbx connecté, politique réseau globale (`sbx policy check network example.com:443` doit répondre
   `Denied`), `claude`, `git`. Pour chaque manque : la commande d'installation, donnée à
   l'utilisateur. `sbx policy init balanced` est global à toutes les VM : le skill le propose et
   attend l'accord.
2. den installé et V2 ; sinon les méthodes d'installation du README.
3. Jeton : si `sbx secret ls` ne liste aucun secret pour `api.anthropic.com`, le skill donne les
   commandes du README (`claude setup-token`, puis `read -rs` et `sbx secret set-custom`) à lancer
   dans un terminal. Il ne voit jamais le jeton.
4. Nest : si un `den.yaml` de projet est trouvé depuis le dossier courant, le skill le montre et
   propose ses nests. Sinon il demande les repos, puis écrit `~/.den/den.yaml` (ou y ajoute un nest)
   avec `default_template: claude-code-docker` quand aucun template n'est déclaré.
5. Création et vérification par `den exec <nest> -- true`, puis `den up <nest>` donné à
   l'utilisateur.
6. Suite proposée : `den:profile`.

**`den:profile`**, travail « créer » — un kit de profil depuis la configuration Claude de l'hôte.
- Lit `~/.claude` de l'hôte et propose quoi copier dans `~/.den/kits/<nom>/files/home/.claude/` :
  `CLAUDE.md`, `skills/`, `agents/`, `output-styles/`, `settings.json`.
- Ne copie jamais `~/.claude.json`, `.credentials.json`, ni aucun fichier d'état ou d'historique.
- `settings.json` est filtré clé par clé, avec l'utilisateur. Sont signalées, et retirées par
  défaut : toute commande ou chemin absolu de l'hôte (`hooks`, `statusLine`,
  `permissions.additionalDirectories`), `sandbox`, et `env` (le skill montre les **noms** des
  variables, jamais les valeurs, et l'utilisateur choisit lesquelles garder).
- Écrit le `spec.yaml` minimal (`kind: mixin`), déclare le profil sous `profiles:` du fichier global
  et, sur accord, `profile:`.
- Dit quelles instances existantes doivent être recréées (`den rm` puis `den up`) pour recevoir le
  profil ; il ne les supprime pas lui-même sans confirmation.
- À vérifier par le constructeur dans une VM : `enabledPlugins` et `extraKnownMarketplaces` copiés
  de l'hôte fonctionnent-ils dans la VM (installation des plugins, hôtes réseau requis) ? Le résultat
  décide si ces deux clés sont gardées ou retirées par défaut.

**`den:nest`** — ajouter ou modifier un nest : repos (chemin ou origine), repos optionnels,
`network.allow`, `ports`, `template`, `profile`, fichier global ou fichier de projet. Rappelle la
règle de `docs/den-yaml.md` sur le changement d'un nest (instance à recréer).

**`den:troubleshoot`** — du symptôme à la correction. Pour chaque ligne du tableau de dépannage du
README et de `docs/ssh.md` : la commande qui établit la cause (`sbx policy check network …
--sandbox <instance>`, `ssh-add -l`, `den ls`, `sbx ls`), puis la correction. Le skill propose la
correction ; il ne l'applique qu'après accord quand elle modifie un fichier ou supprime une instance.

### Tranche 2 — personnalisation

**`den:profile`**, travaux « scinder » et « maintenir ».
- Scinder : plusieurs profils (pro, perso) qui partagent un kit commun, par exemple
  `pro: [~/.den/kits/common, ~/.den/kits/pro]`. Le kit commun porte ses réglages en défauts
  `files/home/.den/claude-settings.d/*.json` ; seul le kit propre au profil livre un
  `settings.json`, car le gagnant entre deux kits qui livrent ce fichier n'est pas vérifié. Le
  constructeur vérifie ce montage à deux kits dans une VM avant de livrer ce travail.
- Maintenir : ajouter un skill ou un réglage à un profil, reporter un changement de l'hôte, lister
  les instances à recréer.

**`den:template`** — créer un dossier de template (`den-template.yaml`, `steps/`, `kits/`), le
déclarer sous `templates:`, vérifier les deux contraintes d'image de `docs/templates.md` (`jq`
présent, le point d'entrée garde la VM en vie), puis `den build <template>`.

**`den:kit`** — écrire un kit `mixin` avec ce que den y ajoute : défauts `claude-settings.d`,
règles réseau, fichiers sous `files/home/`. Pour la grammaire générale d'un kit, le skill renvoie à
la doc sbx ; il ne la recopie pas.

**`den:team`** — partager des nests : `den.yaml` de projet, clés de repo par origine, `den scan`,
et la ligne à mettre dans le `.claude/settings.json` du projet pour déclarer la marketplace.

## Validation

- Les trois tests Go ci-dessus tournent dans `task check`.
- Chaque skill est exécuté une fois pour de vrai avant sa livraison, sur un den home jetable
  (`--den-home`), et le résultat est consigné dans
  `docs/superpowers/specs/2026-11-20-den-marketplace-runs.md` : ce qui a été lancé, ce qui a
  été observé, ce qui reste non vérifié. `den:setup` est validé sur un poste, ou un compte, sans
  `~/.den`.
- La profondeur des évaluations automatiques de skills (déclenchement, variantes) est laissée au
  constructeur.

Qui valide, et où :

- Un den home vide (`--den-home`) sur le poste de Nicolas suffit pour tous les skills. sbx connecté,
  le jeton stocké et la politique réseau y sont déjà en place.
- `--den-home` n'isole pas l'état global de sbx (`sbx policy`, `sbx secret`). Les branches
  « prérequis manquant » de `den:setup` ne sont donc pas exécutées sur ce poste : la fiche de
  validation les consigne « non vérifié » jusqu'à la première installation par un coéquipier, qui
  en est le vrai test. Ce retour est reporté dans la fiche.

## Portée du plan et livraison

- Le premier plan couvre la marketplace, les garde-fous et la **tranche 1** seulement. La tranche 2
  a son propre plan, écrit après le retour du premier coéquipier.
- Un skill ne renvoie qu'à un skill qui existe. En tranche 1, `den:profile` n'annonce que le travail
  « créer », et la ligne de `.claude/settings.json` de projet (prévue dans `den:team`) est écrite
  dans le README.
- Le test de structure vérifie les skills présents ; il ne fixe pas une liste de huit.
- La tranche 1 sort avec la prochaine version mineure de den. Avant cette release, le plugin
  s'essaie depuis un chemin local (`claude plugin marketplace add <clone>`).

## Documentation

- README : une section courte « Avec Claude Code » avant « First run », avec les deux commandes
  d'installation et `/den:setup`. Le chemin manuel reste.
- `CLAUDE.md` : le plugin, `task plugin-sync`, et la règle « une page de `docs/` modifiée se
  recopie ».
- `CHANGELOG.md` : une entrée à la release qui livre la tranche 1.

## Laissé au constructeur

- L'emplacement du paquet de tests Go et le nom exact de la tâche de synchronisation.
- Le texte des `SKILL.md` (anglais, comme tout message utilisateur de den) et leurs descriptions de
  déclenchement.
- L'ordre de livraison à l'intérieur de chaque tranche.

## Suites nommées, hors de cette spec

- `den check` : valider un `den.yaml` sans créer de VM. Les skills l'adoptent quand il existe.
- `.claude/commands/release.md` nomme encore `make lint && make typecheck && make test` ; la commande
  réelle est `task check`.
