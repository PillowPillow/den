# Marketplace den — essais

Essais du plan `docs/superpowers/plans/2026-11-20-den-marketplace-lot1.md`. Chaque section dit ce qui a
été lancé, ce qui a été observé, et ce qui reste non vérifié.

## Essai 1 — épinglage

Date : 2026-11-20. Poste : macOS, `claude` 2.1.329. Tâche 1 du plan.

**Montage.** Un clone jetable du dépôt (`<scratchpad>/pin-trial/den`, commit de départ `75cab0e`)
reçoit :

- `plugins/den/.claude-plugin/plugin.json` : `{"name":"den","version":"0.0.1","description":"trial"}` ;
- `plugins/den/skills/probe/SKILL.md` : frontmatter `name: probe`, `description: Trial skill`, corps
  `Reply with exactly this line and nothing else: ROOT=${CLAUDE_PLUGIN_ROOT}` ;
- `.claude-plugin/marketplace.json` :
  `{"name":"den","owner":{"name":"PillowPillow"},"plugins":[{"name":"den","description":"trial","source":{"source":"git-subdir","url":"file://<scratchpad>/pin-trial/den","path":"plugins/den","ref":"plugin-trial"}}]}`.

Un commit dans le clone, puis `git tag plugin-trial` (tag local au clone, jamais poussé).

**Validation hors ligne.** `claude plugin validate <clone>` : `✔ Validation passed with warnings`,
code 0. Un seul avertissement : `description: No marketplace description provided`. Aucune erreur.

**Installation** (l'humain a approuvé la modification de `~/.claude/plugins`) :

```
$ claude plugin marketplace add <clone>
✔ Successfully added marketplace: den (declared in user settings)
$ claude plugin install den@den
✔ Successfully installed plugin: den@den (scope: user)
$ ls -a ~/.claude/plugins/cache/den/den/*/
.claude-plugin  .in_use  skills
$ claude -p "/den:probe"
ROOT=/Users/polochon/.claude/plugins/cache/den/den/0.0.1
```

Le cache ne contient que `.claude-plugin/` et `skills/` (plus le marqueur `.in_use` de Claude Code) :
ni `go.mod`, ni `internal/`, ni `docs/`. Le dossier du cache porte la version de `plugin.json`
(`0.0.1`). `${CLAUDE_PLUGIN_ROOT}` est substitué dans le corps du SKILL.md par ce dossier.

`marketplace add` écrit aussi deux clés dans `~/.claude/settings.json` de l'hôte :
`extraKnownMarketplaces.den` (source `directory`, chemin du clone) et `enabledPlugins["den@den"]: true`.

**Remise en état.**

```
$ claude plugin uninstall den@den
✔ Successfully uninstalled plugin: den (scope: user)
$ claude plugin marketplace remove den
✔ Successfully removed marketplace: den
```

`claude plugin marketplace list` ne nomme plus `den`. Les deux clés de `~/.claude/settings.json` sont
retirées. Le dossier `~/.claude/plugins/cache/den/den/0.0.1/` reste, marqué `.orphaned_at` par Claude
Code ; il n'a pas été supprimé à la main.

**Classe : installation réussie.** Une source `git-subdir` qui nomme le dépôt de la marketplace
lui-même s'installe, ne copie que le sous-dossier du plugin, et `${CLAUDE_PLUGIN_ROOT}` est substitué.
Le repli par une branche poussée n'a pas servi.

**Le `ref` est respecté.** Contre-épreuve ajoutée, sans toucher `~/.claude` de l'hôte : dans le
clone, un commit après le tag change le corps du skill en `AFTER-TAG ROOT=…`. Puis, avec
`CLAUDE_CONFIG_DIR=<scratchpad>/pin-trial/cfg`, `claude plugin marketplace add <clone>` et
`claude plugin install den@den`. Le `SKILL.md` du cache porte le texte du tag (`ROOT=…`, sans
`AFTER-TAG`) alors que `HEAD` porte l'autre : Claude Code extrait `ref`, pas la branche courante.
`known_marketplaces.json` et `settings.json` de l'hôte ne nomment toujours pas `den`.

**Reste non vérifié.**

- L'URL GitHub (`https://github.com/PillowPillow/den.git`) avec un tag `vX.Y.Z` : vérifiée à la
  première release qui contient le plugin (tâche 10, étape 3).

## Essai 2 — plugins dans une VM

Date : 2026-11-20. Tâche 4 du plan. Hôte : den v2.0.0, sbx v0.53.0. VM : template
`claude-code-docker`, Claude Code 2.1.306.

**Montage.** Un den home jetable `<scratchpad>/vm-trial/den-home`, passé par `--den-home` à chaque
commande `den` ; un repo git jetable `<scratchpad>/vm-trial/repo` (un commit vide) ; un kit
`kits/trial/` avec `spec.yaml` minimal (`schemaVersion: "2"`, `kind: mixin`, `name: trial`,
`version: 1.0.0`) et `files/home/.claude/settings.json` :

```json
{"extraKnownMarketplaces":{"caveman":{"source":{"source":"github","repo":"JuliusBrussee/caveman"}}},"enabledPlugins":{"caveman@caveman":true}}
```

`den.yaml` : `schema: 1`, `default_template: claude-code-docker`, `profiles: {trial: [<den home>/kits/trial]}`,
`profile: trial`, nest `mkt-trial` sur le repo jetable. `sbx ls` ne nommait aucune VM `mkt-trial`.

**Création.** `den --den-home <H> exec mkt-trial -- true` : code 0 (`✓ Created sandbox mkt-trial`,
image déjà présente). Dans la VM, `~/.claude/settings.json` porte les deux clés du kit plus
`skipDangerousModePermissionPrompt: true` (fusion de den-claude) ; `~/.claude/plugins` n'existe pas.

**Session humaine** (`den --den-home <H> up mkt-trial`, puis `/plugin`). Aucune invite : ni
confiance du dossier, ni marketplace, ni plugin. `/plugin` ne liste que les quatre plugins
`builtin` (`cc-plugin-agents-md`, `cc-plugin-diff`, `cc-plugin-plugin-authoring`,
`cc-plugin-telemetry`) ; `caveman` absent. La durée de cette session n'a pas été mesurée.

**Constat après la session** (par `den --den-home <H> exec mkt-trial -- …`, avant les sessions
`claude -p --debug` ci-dessous) :

- `ls ~/.claude/plugins/cache` : `No such file or directory` ;
- `cat ~/.claude/plugins/known_marketplaces.json` : `No such file or directory` ;
- `~/.claude/plugins/installed_plugins.json` : `{"version": 2, "plugins": {}}` ; `marketplaces/` vide ;
- `claude plugin marketplace list` : seulement `anthropic-plugin-directory` (Built in) ;
- `claude plugin list` : `No plugins installed.` ;
- `~/.claude.json` : le dossier du repo a `hasTrustDialogAccepted: false` (`CLAUDE_CODE_SANDBOXED=1`
  supprime l'invite sans enregistrer la confiance).

**Réseau.** `sbx policy check network github.com:443 --sandbox mkt-trial` : `Allowed` (de même
`raw.githubusercontent.com:443` et `codeload.github.com:443`). Depuis la VM :
`curl https://github.com/JuliusBrussee/caveman` rend 200, `git ls-remote` rend `HEAD`, et
`git clone https://github.com/JuliusBrussee/caveman.git` réussit (code 0, `.claude-plugin/` présent).

**Journal de Claude Code** (`claude -p --debug` lancé dans la VM, deux fois, la seconde pendant
environ deux minutes ; sessions sans terminal, distinctes de la session humaine, dont aucun journal
n'existe) :

```
Skipping orphaned enabledPlugins entry caveman@caveman: marketplace not registered
[reconcile] 1 marketplace(s): caveman(install)
Plugin caveman@caveman not found in any marketplace, skipping
SSH not configured for GitHub, using HTTPS for JuliusBrussee/caveman
Marketplace checkout probe: no readable HEAD, cloning
git clone: url=https://github.com/JuliusBrussee/caveman.git ref=default timeout=120000ms
```

Aucune ligne de fin ni d'erreur du clone avant la fin de session, 109 s plus tard, donc avant le
délai de 120 s du clone ; `known_marketplaces.json` reste absent et `marketplaces/` vide.

**Classe : autre.** Pas d'invite, réseau autorisé, `git clone` manuel réussi ;
Claude Code lance le clone de la marketplace au démarrage, le clone n'est pas terminé après 109 s,
et le plugin n'est pas installé dans la fenêtre observée. La cause n'est pas établie.

**Décision (règle de la tâche 4) : retirées par défaut.** Le plugin ne s'installe pas sans action
dans la fenêtre observée. `den:profile` retire par défaut `enabledPlugins` et
`extraKnownMarketplaces`, et dit la cause observée : dans une VM, Claude Code n'a pas installé le
plugin déclaré par ces clés pendant une session d'environ deux minutes, alors que le réseau
l'autorisait ; la cause n'est pas établie.

**Nettoyage.** `den --den-home <H> rm mkt-trial` (sans `--force`) : `Removed sandbox mkt-trial`,
code 0 ; `sbx ls` ne nomme plus `mkt-trial` ; `<scratchpad>/vm-trial` supprimé.

**Reste non vérifié.**

- Pourquoi le clone de Claude Code dépasse 109 s alors qu'un `git clone` manuel aboutit.
  Hypothèses non essayées : l'environnement ou le chemin réseau du clone de Claude Code diffère de
  celui du shell ; la version 2.1.306 de Claude Code dans l'image `claude-code-docker`.
- Une session plus longue que 120 s, ou une seconde session : l'essai ne montre pas si le clone
  aboutit après le délai ou à une nouvelle tentative.
- Une installation manuelle dans la VM (`claude plugin marketplace add`, `claude plugin install`).

## Essai 3 — skills

Date : 2026-11-20. Tâche 8 du plan. Hôte : den v2.0.0, sbx v0.53.0, `claude` 2.1.329.

**Montage.** Un repère `<scratchpad>/runs/marker` ; un repo git jetable `<scratchpad>/runs/repo` (un
commit vide) ; le den home `<scratchpad>/runs/den-home`, absent au départ. Aucun `den.yaml` au-dessus
de `<scratchpad>/runs` ; `sbx ls` ne nommait aucune VM `mkt-trial`. L'humain lance, depuis
`<scratchpad>/runs` : `DEN_HOME=<den home> claude --plugin-dir <dépôt>/plugins/den`, accepte la
confiance du dossier, puis lance les quatre skills dans une même session. L'observation vient du
transcript de la session.

**`/den:setup`.**
- Lancé : prérequis par les commandes de `reference/setup.md#prerequisites` (`sw_vers`, `uname -m`,
  `sbx version`, `sbx ls`, `sbx policy ls`, `sbx policy check network example.com:443`,
  `claude --version`, `git --version`), puis `den version`, `den --help`, la version de `plugin.json`,
  le contrôle du jeton, la recherche d'un `den.yaml` de projet.
- Observé : tous les prérequis présents, `Denied` pour `example.com:443`, den v2.0.0 égale au plugin
  2.0.0, pas de v1. Aucun `den.yaml` de projet ; le skill demande le nom du nest et les repos, montre
  le fichier entier comme diff (`schema: 1`, `default_template: claude-code-docker`, nest `mkt-trial`),
  attend l'accord, écrit sous `$DEN_HOME`. `den exec mkt-trial -- true` en arrière-plan : code 0. Le
  skill donne `den up mkt-trial` et ne le lance pas, puis propose `/den:profile`.
- Observé aussi : le bac à sable de Claude Code sur l'hôte bloque le socket du démon sbx ; le modèle
  relance les commandes `sbx` et `den` hors de ce bac à sable.
- **Écart corrigé.** L'étape 3 lançait `sbx secret ls`, dont la table affiche en clair un préfixe de
  chaque secret : un préfixe du jeton OAuth de Claude est entré dans la conversation (B13). Le
  transcript a été supprimé à la demande de l'humain. Correction (commit `056a2e0`) : `rules.md`
  (règles 4 et 6) et l'étape 3 utilisent
  `sbx secret ls --json | jq '{custom: [.custom_secrets[]? | select(any(.targets[]?; . == "api.anthropic.com")) | {scope, env, targets}], services: [.secrets[]? | {scope, name, type}]}'`,
  qui ne rend aucun champ `secret` (vérifié sur l'hôte). Relance de `/den:setup` une fois : le skill
  lance cette commande, conclut « The token is stored globally », trouve le nest `mkt-trial` et
  vérifie l'instance par `den exec mkt-trial -- true` (code 0) ; le transcript de la relance ne
  contient aucun préfixe `sk-ant`.

**`/den:profile`** (profil `trial`).
- Lancé : lecture de `reference/den-yaml.md#profiles`, liste de `~/.claude/` de l'hôte,
  `jq 'del(.env) | map_values(type)'`, `jq '(.env // {}) | keys'`, des filtres `jq` sur
  `permissions` qui rendent des types, des comptes, ou les seules règles qui nomment un chemin.
- Observé : `CLAUDE.md`, `skills/`, `agents/`, `output-styles/` proposés un par un (l'humain refuse
  les quatre). Pour `settings.json`, une question propose de retirer `hooks`, `statusLine`, `sandbox`,
  `enabledPlugins`, `extraKnownMarketplaces`, `pluginConfigs` (avec la cause de l'essai 2) et
  `outputStyle`, et offre « Review keys first » ; `env` : quatre noms proposés, aucune valeur
  affichée. Le kit est écrit par `jq` (dossier créé par `mkdir -p`) ; la vérification ne rend que des
  noms de clés. Diff de `den.yaml` (`profiles.trial` avec un chemin absolu, puisque `DEN_HOME` est
  posé ; `profile: trial` sur accord), écrit après accord ; `den ls` charge le fichier. Le skill
  nomme `mkt-trial` à recréer, avertit que `den rm` est irréversible, et demande avant de le lancer :
  l'humain ne répond pas, `den rm` n'est pas lancé.

**`/den:nest`** (« ajoute un port 8189 exposé »).
- Observé : le skill montre le seul `den.yaml` (global), explique global ou projet, puis propose une
  édition minimale (`ports: - {name: app, sandbox: 8189, host: 8189}`) en diff, signale que le port
  hôte peut être pris (non vérifié par lui) et propose `host: 0`, et rappelle que l'instance doit être
  recréée. L'humain passe au skill suivant sans répondre : rien n'est écrit.

**`/den:troubleshoot`** (« a host is unreachable from the VM »).
- Observé : le skill rapproche le symptôme de la ligne de `reference/setup.md#troubleshooting` et de
  la ligne SSH de `reference/ssh.md#troubleshooting`, puis demande l'hôte, le port et l'instance pour
  lancer `sbx policy check network <host>:<port> --sandbox <instance>`. Aucun hôte n'est nommé :
  aucune commande `sbx policy check` n'est lancée, aucune règle n'est ajoutée.

**Isolement.** `find ~/.den -newer <repère>` n'est pas vide : `gen/amproto.prototype-den-a`,
`gen/amproto.prototype-den-b`, `wt/prototype-den-a`, `wt/prototype-den-b`, et sous `state/v2`
`instances/amproto.prototype-den-{a,b}.yaml`, `nests.yaml`, `repos.yaml` (17:50 à 17:52). La
session de l'essai a duré d'environ 17:37 à 17:52 : la fenêtre recouvre ces écritures, l'heure ne
les exclut donc pas. Attribution probable à un autre travail mené en même temps sur le poste, non
vérifiable aujourd'hui. Appuis : la liste des appels d'outils de la session, lue avant la suppression
du transcript, ne contient que deux commandes `den` (`den exec mkt-trial -- true` et `den ls`,
avec `DEN_HOME` posé au lancement) et ne nomme jamais `amproto` ; `nests.yaml`, `repos.yaml` et
`instances/` du vrai den home ne nomment ni `mkt-trial` ni `<scratchpad>/runs`. Le contrôle par
`find` n'a pas établi l'isolement ; seule cette lecture l'appuie. Décision de l'humain (2026-11-20,
après la revue finale) : ce résidu du point 3 de « Review Focus » est accepté, sans nouvel essai.

**Nettoyage.** `den --den-home <den home> rm mkt-trial` (sans `--force`) : `Removed sandbox mkt-trial`,
code 0 ; `sbx ls` ne nomme plus `mkt-trial`.

**Reste non vérifié.**
- `den:setup` : les branches « prérequis manquant » (sbx absent ou trop ancien, sbx non connecté,
  politique absente ou `allow-all`, `claude` ou `git` absent, den absent ou v1, jeton absent) — non
  vérifié, premier coéquipier (B25). La branche « `den.yaml` de projet trouvé ».
- `den:profile` : la copie de `CLAUDE.md`, `skills/`, `agents/`, `output-styles/` (refusée pendant
  l'essai) ; la voie « Review keys first » et le masquage d'un secret dans une valeur ; la
  déclaration d'un kit existant (A2) ; `den rm` après confirmation.
- `den:nest` : l'écriture du diff et la conservation des commentaires (le fichier de l'essai n'en
  avait pas) ; un `den.yaml` de projet.
- `den:troubleshoot` : le lancement de `sbx policy check` avec un hôte nommé, et l'application d'une
  correction après accord. Résultat attendu du plan non atteint pendant l'essai : aucun hôte nommé.
- Après la correction de `rules.md` (règles 4 et 6), seul `/den:setup` a été relancé ;
  `/den:profile`, `/den:nest` et `/den:troubleshoot` ne l'ont pas été.
- `reference/files.md` (copie de `docs/files.md`) cite encore `sbx secret ls` sans filtre, comme
  commande de l'utilisateur ; la règle 6 l'interdit aux skills.
