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

**Classe : autre.** Pas d'invite, réseau autorisé, `git clone` manuel réussi en quelques secondes ;
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
