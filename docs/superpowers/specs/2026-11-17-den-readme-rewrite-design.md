# Réécriture du README de den V2 — design

Date : 2026-11-17. Branche : `docs/readme-rewrite` (depuis `v2`).

## Intention

Un inconnu trouve le dépôt public `github.com/PillowPillow/den`. Sans aide, il doit pouvoir passer de
« qu'est-ce que c'est ? » à Claude Code qui tourne dans une VM sbx sur son propre dépôt, en sachant
ce que la VM peut faire sur sa machine.

Le README actuel (591 lignes) est une référence exacte mais pas un parcours : schéma, listes de refus,
digests, hachages. Le chemin d'un nouveau venu y est enfoui, et plusieurs étapes y échouent (voir
« Constats »).

## Objectifs

- Un README court (environ 250 lignes) qui mène un inconnu sur macOS jusqu'à un `den up` qui marche.
- Toute la référence conservée, déplacée dans `docs/`, sans perte d'information vraie.
- Chaque affirmation vérifiée contre le code, l'aide de sbx ou une exécution ; une affirmation non
  vérifiée est soit retirée, soit marquée comme telle.
- Chaque document relu par un agent Fable qui joue un utilisateur et vérifie que les usages sont
  couverts (voir « Revue »).

## Non-objectifs

- Pas de site de documentation (MkDocs, GitHub Pages).
- Pas de prise en charge annoncée de Linux ni de WSL : macOS seulement, Linux viendra après.
- Pas de version publiée de V2 : l'installation se fait depuis la branche `v2` avec `task build`.
- Pas de changement de code. Une contradiction entre le README et le code est notée pour un ticket
  séparé, pas corrigée ici.
- Pas de traduction : README et `docs/*.md` restent en anglais.

## Anti-objectifs (échec même si « ça marche »)

- Un README qui cache un risque : permissions contournées, `.git` en écriture, agent SSH partagé,
  politique réseau globale de sbx.
- Un nom d'employeur, un hôte privé ou un chemin du mainteneur dans un document public.
- Un premier lancement qui exige `den build`, un kit de profil ou un dépôt optionnel.
- Une même règle écrite deux fois (README et `docs/`), qui finira par diverger : le README résume en
  une ligne et pointe vers la page de référence.

## Contraintes

- sbx v0.53.0 ; `sbx env`, `kit` et `skills` sont expérimentaux. Le README le dit dès son en-tête.
- Le style de l'existant : phrases courtes, erreurs qui nomment le remède. Commandes et messages
  cités tels quels.
- `docs/superpowers/` reste l'historique interne en français ; aucun document public n'y renvoie.
- La fonctionnalité « repo map » (`DEN_REPOS`, `~/.claude/rules/den-instance.md`) est en cours sur
  `v2`. La documentation suit l'état de `v2` au moment de la rédaction ; la branche est rebasée sur
  `v2` avant publication.

## Approches

| Approche | Avantages | Coûts | Décision |
|---|---|---|---|
| **A. README court + `docs/`** | un inconnu réussit en lisant une page ; la référence reste complète | huit fichiers à tenir à jour avec le code | **retenue** par le mainteneur |
| B. Un seul README long, réordonné (guide puis référence, ~700 lignes) | un seul fichier | le lecteur doit défiler loin pour l'essentiel : le problème actuel, mieux rangé | écartée |
| C. Site de documentation | navigation, recherche | trop lourd pour une préversion limitée à macOS | écartée |

## Décisions du mainteneur

1. **Connexion de Claude**, dans cet ordre de préférence :
   - un jeton : clé d'API Anthropic, ou `CLAUDE_CODE_OAUTH_TOKEN` pour un abonnement ;
   - en dernier recours `/login` dans la VM, avec son défaut : se reconnecter à chaque recréation.
2. **Politique réseau globale de sbx** : `balanced` par défaut (`sbx policy init balanced`).
3. **Installation** : V2 seulement, depuis la branche `v2`.
4. **Plateforme** : macOS seulement pour l'instant.
5. **`.git` en écriture** (dépôt monté sans `-w`) : risque résiduel accepté, documenté.
6. **Approche A**, avec une revue Fable par document.
7. **Branche** : la réécriture arrive sur `v2` seulement. `main` (v1) ne change pas ; un inconnu ne
   voit le nouveau README qu'à la bascule.
8. **Agent SSH, défaut pour un inconnu** : « chargez seulement les clés que vos agents peuvent
   utiliser », avec `ssh.agentForwardingEnabled` comme interrupteur. « Charger toutes les clés »
   reste décrit comme une option, avec son risque. Cela remplace le texte du commit `77871e2`.
9. **`docs/source-authoring.md`** (v1 uniquement) est supprimé.

## Choix du rédacteur (à corriger par le mainteneur)

- **Premier lancement sur une image officielle**, sans `den build` : `checkTemplate` accepte une
  image sans construction (`internal/instance/prepare.go:267`).
- **Les hôtes Digitaleo** de la section SSH deviennent `example.com`.
- **Installation** : le README de `v2` dit en une ligne que Homebrew, `install.sh` et
  `go install …@latest` installent v1, et que ce binaire répond lui aussi à `den` (`den version` le
  distingue).

## Constats qui guident la réécriture

Vérifiés par la revue Fable du 2026-11-17 ou en session, sauf mention contraire.

- La connexion de Claude n'est ni faite par den ni documentée. Méthode vérifiée (installation du
  mainteneur) : secret personnalisé global sur `api.anthropic.com`, variable `CLAUDE_CODE_OAUTH_TOKEN`,
  jeton produit par `claude setup-token`, stocké par
  `printf '%s' "$TOKEN" | sbx secret set-custom --host api.anthropic.com --env CLAUDE_CODE_OAUTH_TOKEN --value-stdin`.
- **Clé d'API : non vérifiée.** Le kit `den-claude` n'a pas de bloc `credentials`
  (`internal/sbxenv/den-claude/spec.yaml`), donc `sbx secret set anthropic` n'atteint probablement
  pas Claude (spike S3, piste c). Méthode probable : le même secret personnalisé avec
  `--env ANTHROPIC_API_KEY`. À tester avec une vraie clé avant publication ; sinon le README ne
  recommande que le jeton OAuth.
- Le démarrage rapide actuel échoue deux fois : `den build example` vise un template non déclaré, et
  le dépôt optionnel `~/src/demo-docs` est gardé par défaut puis exigé
  (`internal/nest/select.go:84-85`, `internal/instance/prepare.go:230-237`).
- `network.allow` s'ajoute à la politique globale de sbx (`local-policy`, qui s'applique à toutes les
  VM) ; le README n'en dit rien. Une règle ajoutée à la main ouvre un hôte à toutes les VM.
- La VM écrit le `.git` d'un dépôt monté sans `-w` (hooks, config exécutés ensuite sur l'hôte). Avec
  `-w`, le fichier `.git` est en lecture seule, mais le répertoire git commun est monté sur
  `/den-git/<repo>`.
- Prérequis absents : macOS, sbx et `sbx login` (compte Docker), Go 1.26 et `task` pour construire,
  git pour `-w`.
- Vocabulaire employé avant d'être défini : nest, instance, template, kit, profile, den home.
- Contenu interne dans le README : politique `--no-input`, hachages de `den rm`, chemins de staging,
  règles de reconstruction des siblings, renvoi aux documents français.
- Contradiction mineure : README « sans nom de nest, liste les nests » ; le code renvoie une erreur
  qui contient la liste (`internal/cli/up.go:41`). Ticket séparé.

## Structure cible

### `README.md`

1. Ce qu'est den (trois phrases), son statut (préversion V2, sbx v0.53.0, commandes sbx
   expérimentales) et ce qu'il n'est pas (pas de bac à sable propre, Claude seulement, pas de
   frontière plus dure que sbx).
2. Modèle de sécurité en dix lignes : permissions contournées dans la VM ; dépôts montés en écriture,
   `.git` compris ; réseau = politique globale de sbx + listes de den ; agent SSH partagé par toutes
   les VM. Lien vers `docs/security.md`.
3. Prérequis, un tableau avec une commande de vérification par ligne : macOS, sbx (`sbx version`),
   `sbx login` (`sbx ls`), `sbx policy init balanced`, connexion de Claude, Go 1.26 et `task`, git.
4. Connexion de Claude : jeton OAuth (commandes ci-dessus), clé d'API (si vérifiée), `/login` en
   dernier recours.
5. Installation depuis `v2` : `git clone`, `git checkout v2`, `task build`, `den version`.
6. Glossaire : nest, instance, template, kit, profile, den home.
7. Premier lancement : un `~/.den/den.yaml` d'un dépôt sur une image officielle, `den up`, puis
   `den shell`, `den ls`, `den rm`. Dire que le dépôt apparaît au même chemin absolu et que Claude y
   démarre.
8. Étapes suivantes, une ligne et un lien chacune : worktrees (`-w`), réseau, ports, profil (votre
   configuration Claude), template personnalisé (`den build`), `den.yaml` de projet, SSH, layers.
9. Aide-mémoire des commandes.
10. Dépannage : pas connecté à sbx, image dont l'entrypoint sort (statut 137),
    `Permission denied (publickey)`, VM inconnue de den (« Foreign »), hôte refusé par la politique.
11. Désinstallation : `den rm` par instance, `sbx template rm <name>:v2`, suppression du den home.
12. Liens vers `docs/`.

### `docs/`

| Fichier | Contenu repris du README actuel |
|---|---|
| `docs/den-yaml.md` | « The `den.yaml` reference », « Nests », « Profiles » |
| `docs/templates.md` | « Templates », définition `den-template.yaml`, `den build`, siblings |
| `docs/commands.md` | « Commands » (toutes les commandes et options) |
| `docs/security.md` | modèle complet : overlays en lecture seule, « Layers », `--skip-host-commands`, `.git`, risques résiduels |
| `docs/ssh.md` | section « SSH » actuelle, hôtes neutralisés |
| `docs/files.md` | « Where things live » |
| `docs/migrating-from-v1.md` | « Coexistence with v1 », vocabulaire v1 disparu |

Les exemples suivent : `examples/den-home/den.yaml` devient l'exemple du premier lancement (un dépôt,
une image officielle, sans profil ni port ni dépôt optionnel). `examples/den-template/` est relié à
`docs/templates.md` par une entrée `dir:`.

## Revue Fable de chaque document

Après la rédaction de chaque document, un agent Fable le relit en lecture seule, dans le rôle d'un
utilisateur qui découvre den. Il reçoit la liste des usages ci-dessous, cherche dans le document de
quoi réaliser chacun, et vérifie chaque affirmation contre le code et l'aide de sbx. Il rend : les
usages non couverts, les affirmations fausses (fichier:ligne des deux côtés), les termes non définis,
les liens morts. Les corrections passent avant le document suivant.

Usages à couvrir (chaque usage a un document responsable) :

| Usage | Document |
|---|---|
| comprendre ce qu'est den et ses risques avant d'installer | README |
| installer les prérequis et vérifier chacun | README |
| connecter Claude (jeton, `/login`) | README |
| lancer Claude sur un dépôt | README |
| ouvrir un shell, lister, supprimer une instance | README, `commands.md` |
| monter plusieurs dépôts, dont des optionnels | `den-yaml.md` |
| travailler sur une branche en worktree | README (lien), `commands.md` |
| ouvrir un hôte réseau à un nest, vérifier ce qu'une VM atteint | `den-yaml.md`, `security.md` |
| publier un port | `den-yaml.md` |
| apporter sa configuration Claude (profil) | `den-yaml.md` |
| construire un template personnalisé | `templates.md` |
| partager un `den.yaml` de projet avec une équipe | `den-yaml.md`, `templates.md` |
| utiliser SSH et git en SSH dans la VM | `ssh.md` |
| ajouter des champs sbx non modélisés (layers) | `security.md` |
| retrouver ce que den écrit sur le disque | `files.md` |
| diagnostiquer un échec courant | README |
| tout désinstaller | README |
| migrer depuis v1 | `migrating-from-v1.md` |

## Vérification avant publication

- La branche est d'abord rebasée sur `v2` (la repo map y est déjà, documentée dans le README de `v2`).
- Le premier lancement du README est exécuté tel quel sur un den home vide (`DEN_HOME` temporaire),
  sur l'image `docker.io/docker/sandbox-templates:claude-code-docker`, la seule mesurée qui garde la
  VM en vie. Cela crée une VM : l'exécution demande l'accord du mainteneur au moment de
  l'implémentation. **C'est la dernière étape avant la fusion** : sans elle, la branche n'est pas
  fusionnée (choix du rédacteur).
- La méthode par clé d'API est testée avec une vraie clé fournie par le mainteneur, ou retirée.
- Chaque lien relatif est vérifié.
- Contrôle des noms privés (choix du rédacteur) : `grep -rniE 'digitaleo|polochon|/Users/|docs/superpowers'`
  sur `README.md`, `docs/*.md` (hors `docs/superpowers/`) et `examples/` ne renvoie rien. Il ne
  couvre ni `CHANGELOG.md` (historique publié), ni `CLAUDE.md` (notes internes), ni
  `docs/superpowers/` (jamais réécrit), ni les fixtures de test (pas de changement de code).

## Laissé au rédacteur

La formulation, la longueur exacte de chaque section, l'ordre interne de chaque fichier de `docs/`,
et le découpage des commits.
