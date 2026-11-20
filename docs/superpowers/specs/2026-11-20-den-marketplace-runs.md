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
