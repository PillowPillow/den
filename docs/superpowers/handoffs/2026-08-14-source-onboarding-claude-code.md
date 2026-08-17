# HANDOFF — Source onboarding pour une nouvelle session Claude Code

Date : 2026-08-14

## Mission

Implémenter le plan de source onboarding de Den. Le résultat principal est un moteur de convergence
strictement déclaratif partagé par :

```text
den init --source <url>
den source add
den source configure
den source update
den source status
den doctor
```

Digitaleo est le tracer bullet. Le moteur reste générique.

Aucun code de production de cette fonctionnalité n’est encore implémenté. La spec, le plan, le
spike sbx et ses fixtures sont prêts.

## Point de reprise exact

Le dépôt Den est :

```text
/Users/polochon/Development/Pillow/den
```

État vérifié avant la création de ce handoff :

```text
branch: feature/source-onboarding
HEAD:   8088340 docs: capture sbx inspection contract
base:   738a202 docs: plan source onboarding implementation
spec:   40dcbc3 docs: design source onboarding convergence
```

`feature/source-onboarding` part du commit factuel du prototype
`prototype/sbx-inspection-contract`. Ne retourne pas sur la branche de prototype pour implémenter.

La suite `GOCACHE=/tmp/den-go-cache go test ./...` passait intégralement à `8088340`.

## Entrées obligatoires

Lis ces artefacts entièrement, dans cet ordre, avant toute modification :

1. `CLAUDE.md`
2. `docs/superpowers/specs/2026-08-14-source-onboarding-design.md`
3. `docs/superpowers/plans/2026-08-14-source-onboarding.md`
4. `docs/superpowers/prototypes/2026-08-14-sbx-inspection-contract.md`
5. `internal/converge/testdata/secret-ls.txt`
6. `internal/converge/testdata/policy-ls.json`

Inspecte ensuite les trois sources primaires suivantes sans afficher de secret :

```text
/Users/polochon/Development/Pillow/den
/Users/polochon/.den
/Users/polochon/Development/Digitaleo/digitaleo-den-env
```

Le chemin initialement cité `../../Digitaleo/digitaleo-dev-env` n’existe pas. Le dépôt observé et
utilisé par le plan est `../../Digitaleo/digitaleo-den-env`.

Ne prends pas `docs/superpowers/handoffs/HANDOFF.md` comme autorité sur cette fonctionnalité. Il
décrit principalement l’état historique de Den. Pour le source onboarding, l’ordre d’autorité est :

```text
code et sorties observées
> source-onboarding-design.md
> CLAUDE.md
> source-onboarding.md
> ce handoff
> autres handoffs historiques
```

## Décisions validées et verrouillées

- Le point d’entrée est `den init --source <url>`.
- Le manifeste `den-source.yaml` est strictement déclaratif.
- Toutes les nouvelles clés YAML utilisent `snake_case`.
- Aucun hook shell, commande arbitraire ou plugin de ressource n’est autorisé.
- `den` et `sbx` sont déjà installés. Le wizard vérifie leurs versions sans les installer.
- La source recommande son namespace dans `metadata.name`. `--name` reste une surcharge.
- La source publie explicitement tous ses nests et stacks sous `exports`.
- L’installation configure et inspecte tous les exports. Elle ne sélectionne et ne spawn aucun nest.
- Il n’existe aucune notion de `selected_nests` ou `prepare_nests`.
- Les dépôts de travail sont détectés et mappés. Den ne les clone jamais.
- `repository_roots` est une réponse temporaire du wizard. Il ne va pas dans la configuration
  durable.
- `source-config/<name>.yaml` contient la version exacte et les mappings `repos` propres à cette
  source.
- `state/sources/<name>.yaml` est un reçu géré par Den, pas une configuration utilisateur.
- Un dépôt absent rend les nests concernés `not_ready`. Il ne fait pas échouer l’installation.
- Le statut agrégé utilise exactement `ready`, `partially_ready`, `blocked` et `unknown`.
- Une ressource gérée par Den qui échoue bloque l’installation.
- Le fichier de réponses utilise le même moteur typé que le wizard interactif.
- `den source update` calcule un plan et demande confirmation avant mutation.
- La version fonctionnelle cible est un SemVer exact. Une commande ordinaire ne fetch jamais.
- Il n’y a pas de migration automatique de `config.yaml.repos`.
- Les variables et credentials MCP restent hors périmètre.
- Le comportement historique de `den init` sans `--source` et des sources sans manifeste reste
  compatible.

## Séparation des fichiers

Le modèle validé est :

```text
<source>/den-source.yaml
    contrat d’équipe versionné

~/.den/source-config/<name>.yaml
    configuration durable éditable par l’utilisateur
    schema_version, version exacte, repos

answer-file.yaml
    paramètres temporaires d’une exécution
    repository_roots, références de credentials depuis l’environnement

~/.den/state/sources/<name>.yaml
    reçu d’application géré par Den
```

La configuration personnelle est propre à chaque installation de source. Ne réintroduis ni
`repository_roots`, ni sélection de nests dans `source-config`.

## Résultat empirique du spike sbx

Le prototype a observé le binaire installé suivant :

```text
sbx version: v0.38.0 c022b14634c4bea846ca12870d1d5e97d5868b54
```

Contrat observé :

- `sbx secret ls -g` n’a pas de sortie JSON.
- La première table utilise `SCOPE TYPE NAME SECRET`.
- Les types observés incluent `service` et `registry`.
- `(stored)` et `(oauth configured)` indiquent tous deux un service présent.
- Une seconde table optionnelle `CUSTOM SECRETS` utilise
  `SCOPE TARGETS ENV PLACEHOLDER SECRET`.
- Den identifie un custom secret par scope, targets et variable d’environnement.
- Den ne lit jamais les fragments masqués ni le placeholder.
- `sbx policy ls --type network --source local --decision allow --json` retourne un objet
  `{ "rules": [...] }`.
- Den compare les entrées exactes de `rules[].resources`.

Les deux commandes ont échoué avec l’erreur keychain macOS `-50` dans le sandbox d’exécution. Elles
ont réussi avec le même binaire et le même profil hors sandbox. Inférence à partir de cette
différence : un appel sbx qui doit lire le Keychain peut nécessiter une exécution hors sandbox.
Conclusion d’implémentation : toute erreur d’observation produit `unknown`, jamais « credential
absent » ou « policy absente ».

Les sorties réelles temporaires ont été supprimées. Les fixtures commitées sont entièrement
synthétiques. Ne remplace pas ces fixtures par des sorties utilisateur réelles.

## État de la source Digitaleo

Le dépôt réel est :

```text
/Users/polochon/Development/Digitaleo/digitaleo-den-env
```

État observé le 2026-08-14 :

```text
branch: feature/acli
HEAD:   170d687 feat(stacks): add acli to the base stack
main:   df65e1d
status: clean
```

Le commit `170d687` n’est pas sur `main`. Considère ce checkout comme lecture seule pendant les
tâches Den 1 à 13. Avant Task 14, inspecte à nouveau l’état et isole le travail source onboarding
dans une branche ou un worktree approprié. Ne mélange jamais les commits du dépôt Den et ceux du
dépôt Digitaleo.

## Méthode d’exécution

Le plan contient 15 tâches TDD. Exécute-les dans l’ordre. Les tâches ont des blocking edges et le
plan nomme les interfaces produites et consommées.

Au démarrage de la session :

1. Charge les skills Superpowers applicables avant toute action.
2. Utilise `superpowers:subagent-driven-development` si l’utilisateur autorise explicitement les
   sub-agents. Sinon, utilise `superpowers:executing-plans` en exécution directe.
3. Utilise `superpowers:test-driven-development` avant chaque modification de production.
4. Fais le cycle red-green demandé par chaque tâche.
5. Utilise `superpowers:systematic-debugging` au premier test inattendu ou comportement sbx
   inattendu.
6. Utilise `superpowers:verification-before-completion` avant chaque commit et avant toute annonce
   de réussite.
7. Demande une code review aux checkpoints prévus par le workflow choisi.

Le prochain travail est Task 1, « Make the global default stack optional ». Aucun checkbox du plan
n’est encore réalisé.

Commits attendus : un commit cohérent par tâche dans Den. Task 14 produit des commits séparés dans
le dépôt Digitaleo. Ne réécris pas les commits de spec, plan ou prototype.

## Contraintes de sécurité et d’architecture

- N’écris jamais une valeur de credential dans argv, un plan, un log, une erreur, une config, un
  reçu ou une fixture.
- Utilise stdin pour les registry passwords.
- `sbx v0.38.0` impose actuellement `set-custom --value`. Le runner doit masquer l’argument sensible
  dans ses erreurs et dans le fake.
- Toute écriture personnelle ou de reçu utilise un remplacement atomique et des permissions
  privées.
- Les tests utilisent un `DEN_HOME` temporaire. Ils ne modifient jamais `/Users/polochon/.den`.
- Tout accès système reste injecté. Respecte `cli.Deps` et le runner sbx partagé décrits dans
  `CLAUDE.md`.
- Le parsing YAML reste strict avec `KnownFields(true)`.
- Les plans et rendus ont un ordre déterministe.
- La phase de planification n’effectue aucune mutation.
- Le reçu `applying` rend l’application reprenable. Une version partielle ne devient jamais la
  version personnelle active.
- Une source sans `den-source.yaml` garde le chemin legacy existant.

## Vérifications attendues

À chaque tâche, exécute les tests ciblés indiqués dans le plan. Avant un commit, exécute au minimum
les tests des paquets modifiés.

Avant de déclarer l’implémentation Den terminée :

```bash
task check
task build
```

Puis exécute les tests d’acceptance hermétiques de Task 13 avec un binaire construit et des dépôts
temporaires. Ne transforme pas les tests hermétiques en appels sbx réels.

Avant de déclarer Task 14 terminée, lance le lint et les validations prévues contre la source
Digitaleo dans son propre checkout isolé.

## Fog of war explicite

- Le format sbx réussi est fermé par le prototype et ses fixtures.
- Le comportement du Keychain dans un harness restreint est fermé : l’inspection doit être
  autorisée hors sandbox ou retourner `unknown`.
- La coexistence avec le commit Digitaleo `feature/acli` doit être résolue au début de Task 14 à
  partir de l’état Git alors observé. Ne la décide pas maintenant depuis ce handoff.
- Toute divergence entre le plan et le code actuel doit être tranchée depuis le code et un test,
  puis documentée. Ne contourne pas une divergence silencieusement.

## Message de démarrage conseillé pour Claude Code

```text
Reprends l’implémentation source onboarding de Den depuis
docs/superpowers/handoffs/2026-08-14-source-onboarding-claude-code.md.

Lis entièrement tous les artefacts obligatoires listés dans ce handoff. Vérifie ensuite l’état Git
et la baseline. Commence à Task 1 du plan avec le workflow Superpowers applicable et un cycle TDD
red-green. Ne modifie pas ~/.den et considère le checkout Digitaleo actuel comme lecture seule
jusqu’à Task 14. Signale toute divergence empirique avant de modifier le plan.
```
