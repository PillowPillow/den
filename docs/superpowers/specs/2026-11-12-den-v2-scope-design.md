# den V2 — périmètre et conception

*den · réécriture V2 · 12 nov. 2026 · sbx v0.53.0 `2c62838`*

Ce document fixe le périmètre de den V2 et prépare la roadmap. Il ne décrit pas l'implémentation. Il est
aligné sur la fiche de validation `2026-11-12-den-v2-scope-validation.md` (37 lignes approuvées) ; en
cas d'écart, la fiche gagne.

Sources : la session de cadrage du 12 nov. 2026, la carte `docs/den–sbx Boundary Map.html` (sondes sbx
v0.49.1 et v0.50.0), et des sondes en lecture seule sur sbx v0.53.0 (aide de toutes les commandes,
chaînes du binaire, `sbx settings ls`).

Marquage des faits : **[aide]** = sortie `--help` ou commande sbx v0.53.0 ; **[binaire]** = chaînes du
binaire sbx v0.53.0 ; **[mesuré]** = observé sur une VM (version indiquée) ; **[doc]** = documentation
officielle Claude Code ; **[non vérifié]** = hypothèse à prouver.

## 1. Intention

den v1 fait exactement ce que l'utilisateur veut à l'usage. Le problème est ailleurs : den gère trop de
choses, ajoute des fonctions que sbx offre désormais nativement (kit généré, boucle egress, plomberie de
secrets, pipeline de build), et s'écarte de sbx à chaque version de sbx.

den V2 est une **réécriture**, développée sur une branche dédiée de ce dépôt, sous le nom `den` ; v1 reste
sur `main` jusqu'à la bascule. Elle garde le même usage et réduit l'intérieur à ce que sbx ne sait pas
faire. den **configure et pilote sbx** : il génère la configuration sbx, lance sbx, et peut offrir des
commandes courtes (`up`, `shell`) ; dès que sbx fait une chose nativement, den la lui délègue.

den est un **outil autonome**. Il lit sa configuration dans un dossier projet ou sous `~/.den/`, et ne
s'occupe pas du reste du contenu de ce dossier. Une organisation de travail comme le « dev home » (conçu
dans une session séparée) peut s'en servir, mais n'impose rien à den.

Deux usages :

- **Dev quotidien** (V2.0) : une VM par environnement (`am`, `dg`), avec les repos choisis et des
  worktrees créés pour l'occasion.
- **Agents autonomes** (lot L2) : un cron ou un événement lance une commande den ; une VM exécute un agent
  avec un prompt, sans humain.

## 2. Objectifs, non-objectifs, anti-objectifs

**Objectifs**

- Mêmes commandes que v1 : `den up <nest>`, `den up <nest> -w <branche>`, `den shell`, `den ls`,
  `den exec`.
- Tout ce qui se fait en interactif se fait aussi par script avec `den exec`, qui crée la VM si besoin,
  exécute la commande, renvoie son code de sortie et ne pose aucune question.
- Une mise à jour de sbx ne casse pas den. den n'expose rien que sbx offre déjà.
- La définition d'un environnement est versionnable et reproductible par un collègue.

**Non-objectifs**

- Remplacer sbx sur une couche qu'il couvre : secrets, ports, politique réseau, CPU/mémoire, installation
  des kits.
- Le cloud sbx (`--cloud`, `sbx schedule`).
- Lire les fichiers d'état de v1 ou piloter les VM créées par v1 (§11, bascule).
- Les sources d'équipe de v1 (`den source`, `den lint`, préfixe `<source>:`). Un dossier projet
  versionné, partagé par git, les remplace. Pour réutiliser des outils entre dossiers projet, on partage
  des kits : sbx les charge depuis un dépôt git (`git+https://…#dir=<kit>`) ou un registre OCI
  (`sbx kit push|pull|sign`) [aide].

**Anti-objectifs** (échec même si cela « marche »)

- den réimplémente une fonction que sbx fournit nativement.
- Une sandbox lancée par den ne peut plus être manipulée avec `sbx` directement (l'utilisateur ajoute
  souvent une règle avec `sbx policy allow` en direct).
- den impose une structure au dossier projet au-delà de son propre fichier de configuration.

## 3. Contraintes

- Doctrines v1 conservées : refuser avant le premier effet de bord, YAML strict (clé inconnue = erreur),
  ne jamais supprimer ce que den n'a pas créé, `rm` ne laisse jamais une VM orpheline.
- `sbx env` est **EXPERIMENTAL** [aide]. V2 suit ses changements.
- Plancher sbx : **v0.53.0**, la version installée.

## 4. Glossaire

- **dossier projet** : n'importe quel dossier qui contient un fichier de configuration den.
- **configuration den** : fichier YAML strict qui déclare les nests (repos, image de base, kits, réseau,
  profil, ports, dossier de démarrage) ; dans un dossier projet, ou sous `~/.den/` pour les nests
  globales. Nom et schéma : au builder.
- **nest** : groupe nommé de repos, avec l'environnement de VM associé.
- **instance** : une VM lancée à partir d'une nest, nommée d'après la nest, suivie du nom de la branche
  avec `-w` (exemple : `am.feature-123`). En v1, le mot désignait seulement la seconde partie du nom.
- **dossier de run** : dossier non versionné, propriété de den, propre à une instance ; il contient le
  `sbxenv.yaml` généré.
- **image de base** : image de VM lourde et stable (PHP, Node, bibliothèques système), enregistrée comme
  template sbx.
- **kit** : extension sbx qui ajoute un outil à une VM (installation, env, hôtes réseau, credentials).
- **profil** : configuration Claude nommée (`pro`, `perso`).
- **essai jetable** : test court sur une VM `den-probe-*` créée puis supprimée, qui répond oui ou non.

## 5. Usage V2.0 (périmètre de sortie)

Critère de sortie : V2.0 remplace ce qui tourne aujourd'hui — une VM pour `am`, une VM pour `dg`, et des
worktrees créés dans les repos choisis pour travailler dedans.

| Commande | Comportement |
|---|---|
| `den up <nest>` | Crée ou rattache la VM : base, kits, réseau, ports, profil, repos choisis. |
| `den up <nest> -w <branche>` | Crée d'abord un worktree `<branche>` dans chaque repo choisi, puis le monte. |
| `den shell [<instance>]` | Ouvre un shell dans une VM qui tourne. |
| `den ls` | Liste les instances de la machine (registre local × `sbx ls`). |
| `den exec <nest> [-w <b>] -- <cmd>` | Crée la VM si besoin, exécute la commande sans session interactive, renvoie son code de sortie, ne pose aucune question. **Remplace `den run` de v1.** |
| `den rm <instance>` | Supprime la VM, le dossier de run et les worktrees de l'instance, **définitivement** (pas de corbeille). |
| `den build <base>` | Construit l'image de base déclarée dans la configuration : liste ordonnée d'étapes ; den enchaîne create → exec des étapes → `sbx template save` → rm, comme v1. |

- **Repos optionnels** : on choisit à l'appel quels repos de la nest monter (équivalent v1 `--only`,
  `--without`, `-i`).
- **Ports** : la nest déclare ses ports publiés ; ils font partie de V2.0 (`am` en publie cinq).
- **Dossier de démarrage** : la configuration de la nest désigne où l'agent démarre (racine du dossier
  projet, un repo…) ; den le transmet à sbx (`-w/--workdir` : « Directory to start the agent session in:
  absolute, or relative to the sandbox workspace » [aide]). Les `CLAUDE.md`/`AGENTS.md` et les settings
  de projet suivent ce choix : Claude Code lit le `.claude/settings.json` du seul dossier de démarrage,
  et `CLAUDE.md` aussi dans les dossiers parents [doc].
- **Conversion** : V2.0 inclut la conversion des stacks `am` et `dg` actuelles en image de base + kits.

## 6. Ce que den fait, ce que sbx fait

| den (seul à le faire) | sbx (délégué) |
|---|---|
| Lire la configuration den, résoudre la nest et la sélection de repos | Créer, démarrer, arrêter, supprimer la VM (`sbx env`) |
| Créer et supprimer les worktrees sur N repos | Monter les dossiers (`additionalWorkspaces`) |
| Nommer l'instance ; tenir le registre local | Secrets et leur portée (`secrets:`, `sbx secret`) |
| Générer et empiler les `sbxenv.yaml` | Politique réseau en vigueur (`sbx policy`) |
| Choisir les kits par nest ; générer le kit réseau de la nest | Installer les kits |
| Construire l'image de base (`den build`) | Instantané (`sbx template save`) |
| Profils nommés | Skills (`sbx skills`), MCP (`sbx mcp`), agent SSH, ports |

### 6.1 Génération de `sbxenv.yaml`

- `sbx env` fusionne plusieurs fichiers dans l'ordre, sémantique `docker-compose -f` ; les listes se
  concatènent, sauf ports, serveurs MCP et workspaces additionnels, où l'entrée tardive gagne sur la même
  clé [aide].
- Variables développées : `${{ env.args.NAME }}`, `${{ env.projectDir }}`, `${{ env.fileDir }}`.
  « Nothing else is expanded » [aide].
- Les tableaux `entrypoint`/`cmd` remplacent le lancement de l'agent « for this environment only » [aide].
- Secrets et bindings sont provisionnés à la création ; « changes require recreating the sandbox » [aide].
- **Visibilité** : nest globale → `sbxenv.yaml` caché sous `~/.den/`. Nest d'un dossier projet → visible
  dans un dossier de run non versionné de ce dossier projet, et la VM se relance avec `sbx` seul.
- Contrainte à prouver (essai S4) : `sbxenv.yaml` doit être hors de tout dossier monté, ou à la racine du
  workspace [doc relevée par la session dev home, non vérifié en pratique].

### 6.2 Registre et noms

- den tient sous `~/.den/` un registre local des instances de la machine, avec le dossier projet de
  chacune ; `den ls` le croise avec `sbx ls`.
- Un nom de nest est unique sur la machine : den refuse un second dossier projet qui déclare une nest au
  nom déjà pris, et nomme le fichier en cause.

## 7. Environnement : trois couches

| Couche | Contenu | Rythme | Mécanisme |
|---|---|---|---|
| 1. Image de base | Runtimes lourds | Rare ; rebuild accepté | `den build` → `sbx template save` ; `sandboxOptions.template` |
| 2. Kits | Un outil = un kit : install, env, règles réseau, credentials | Fréquent | `kits:` à la création ; `sbx kit add` à chaud (L1) |
| 3. Configuration den | Choix de base + kits + repos + réseau + profil + ports | Quotidien | den génère `sbxenv.yaml` |

- **V2.0** : une nest liste ses kits (dossier local, git, OCI) ; ils sont posés à la création.
- **L1** : `den kit add <outil> [--save]` lance `sbx kit add` sur les VM vivantes de la nest. Aide sbx :
  le conteneur est recréé, volumes du kit et workspace conservés ; la VM doit porter les labels
  « recreate-aware » [aide] → essai S2.
- La base reste construite par den : sbx n'offre pas encore de build d'image natif en local.
  `sbx template save` fait l'instantané [aide] ; le kit builder construit des kits « source-form » avec un
  cache Docker [aide] ; le schéma v3 accepte un `dockerfile`, mais « Dockerfile builds are accepted in
  the schema but not yet built by the runtime » [binaire] → veille S5.
- Docker **dans** la VM (pile `am`) est une autre question, inchangée : l'image `claude-code-docker`
  embarque un Docker Engine.
- Réglage local à intégrer : `kit.shortcuts.shell` pointe vers un kit local sous `~/.config/` [aide,
  contenu non lu].

## 8. Réseau

- **V2.0** : les hôtes autorisés d'une nest sont déclarés dans un petit kit généré par den
  (`permissions.network`), que sbx applique à la création. Rien ne s'exécute sur l'hôte, donc rien
  n'attend d'approbation et `den exec` reste non interactif. Écarté : des commandes `sbx policy` en
  `lifecycle.postCreate` (comme le dépôt brain), qui demandent une approbation à chaque fois sauf réglage
  `env.rememberHostCommands`.
- **L1** : `den allow|deny <domaine>` sur la VM en cours via `sbx policy allow|deny --sandbox` ; avec
  `--save`, l'hôte est aussi écrit dans la configuration den du dossier courant pour les VM suivantes.
  `den allow` sans domaine liste les requêtes bloquées (`sbx policy approval ls`).
- Faits : un refus l'emporte sur un `allow` de sandbox ; `*.x` ne couvre pas l'apex [mesuré 0.50.0].
  `--deny-network` à la création ajoute un refus par sandbox [aide]. `sbxenv.yaml` n'a aucun champ réseau
  (`unknown field "egress"`) [mesuré 0.50.0]. Contradiction non élucidée : un refus total local était
  impossible sur v0.50, mais `sbx policy ls` affiche une politique locale `default-deny-all` [aide].

## 9. Profils, settings, SSH

- **Profils** nommés et réutilisables ; `pro` et `perso` n'ont jamais le même dossier `.claude`. Un
  profil est un **fichier de settings** Claude, plus un `CLAUDE.md` et des skills en option. den le passe
  au lancement avec `--settings`, ce qui l'emporte sur le fichier de settings utilisateur que le kit
  `claude` réécrit à chaque création. Précédence, du plus fort au plus faible : managé > `--settings` >
  projet local > projet > utilisateur ; les listes fusionnent [doc].
- **`bypassPermissions` toujours dans la VM.** Le kit `claude` l'assure déjà : entrypoint
  `claude --dangerously-skip-permissions`, et `~/.claude/settings.json` réécrit avec
  `"permissions": {"defaultMode": "bypassPermissions"}` et `skipDangerousModePermissionPrompt` [binaire].
  Claude Code n'accepte `bypassPermissions` que depuis les settings utilisateur, managés ou un flag,
  jamais depuis un fichier de projet (depuis v2.1.257) [doc]. Si l'agent `shell` reste utilisé, den pose
  lui-même ce réglage dans le profil.
- **Agent `claude` de sbx (essai S3, en profondeur).** den vise le kit `claude`. Aujourd'hui, `am` et
  `dg-oauth` tournent avec l'agent `shell` et montent `~/.den/agents/claude` comme `CLAUDE_CONFIG_DIR`
  [aide : `sbx ls`]. Constat du dépôt brain, non revérifié sur v0.53 : depuis v0.51, le kit `claude`
  retire l'en-tête `Authorization` vers `api.anthropic.com`, ce qui casse le jeton OAuth
  (docker/sbx-releases#11, #144). L'essai examine quatre pistes avant tout abandon : le jeton OAuth
  d'abonnement, une clé d'API, un petit kit qui corrige l'authentification, un kit `claude` personnalisé
  déclaré en raccourci (`kit.shortcuts.claude`). den ne revient à l'agent `shell` que si toutes échouent,
  et l'essai dit pourquoi.
- **Modes fournisseur** : pas de flag ; le mode vient du secret résolu, exposé sous
  `SBX_CRED_<SERVICE>_MODE=apikey|oauth|none` [binaire]. Clés : `sbx secret set anthropic|openai|…`,
  globale ou `--sandbox` [aide].
- **Skills et MCP** : magasin `sbx skills` (défaut `readonly`) ; registre `sbx mcp` [aide].
- **SSH** : relais de l'agent SSH de l'hôte, actif par défaut (`ssh.agentForwardingEnabled = true`) : la
  VM signe sans lire les clés [aide]. den ne monte plus de dossier de clés privées. `sbx setup ssh` permet
  `ssh <nom>.sbx` [aide].
- **Plus tard (L4)** : partager l'historique et les sessions de Claude entre VM, en option de profil.

## 10. Worktrees et chemins

- **Emplacement : un réglage, comme en v1** (`worktree_layout` `central|per-repo`, `worktree_root`,
  `internal/config/config.go:77`). **Défaut : dossier central sous `~/.den/`**, hors de tout dossier
  projet, pour qu'une VM qui monte un dossier projet ne voie pas les worktrees des autres tâches.
  L'autre emplacement reste disponible par nest.
- sbx monte un chemin hôte au même chemin absolu dans la VM ; `additionalWorkspaces` accepte
  `PATH[:TARGET][:ro]` ; un `:ro` peut viser un seul fichier [aide].
- **Montage des worktrees : non fixé, décidé par l'essai S1.** Hypothèse testée : monter chaque worktree
  au chemin du clone principal, pour qu'un chemin écrit vers le clone principal (dans un `CLAUDE.md`, un
  script, une config) mène au worktree. Problème : le fichier `.git` du worktree pointe vers
  `<main>/.git/worktrees/<nom>`, masqué par ce montage. Correctif à essayer : monter le `<main>/.git` hôte
  sur `/den-git/<repo>`, et poser en `:ro` par-dessus `<main>/.git` un fichier `.git` généré
  (`gitdir: /den-git/<repo>/worktrees/<nom>`) ; `commondir` est relatif et se résout. Si l'essai échoue,
  den monte les worktrees à leur propre chemin, comme v1.
- Risque hérité de v1 : la VM écrit le `.git` commun en lecture-écriture (un montage `:ro` casse le
  commit sur `index.lock`), donc elle peut poser un hook qui s'exécutera sur l'hôte.

## 11. Roadmap

**Essais jetables avant le plan** (lancés par l'assistant après accord de l'utilisateur ; le plan ne
décrit que les solutions retenues) :

- **S1** — montage d'un worktree au chemin du clone principal + correctif git : `git status`, `commit`,
  `log` dans la VM ; `git worktree list/prune` intact sur l'hôte.
- **S2** — `sbx kit add` sur une VM créée par `sbx env` ; une règle réseau d'un kit ajouté à chaud
  s'applique-t-elle ?
- **S3** — kit `claude` : les quatre pistes du §9.
- **S4** — `sbxenv.yaml` dans un dossier de run situé sous un dossier monté : accepté ?
- **S5** — (veille, ne bloque pas V2.0) sbx construit-il en local un kit v3 décrit par un `dockerfile` ?

**Lots**

- **V2.0** — §5 en entier, pour `am` et `dg`. **Bascule** : les VM v1 sont supprimées ou recréées ;
  V2 ne lit pas les fichiers d'état de v1.
- **L1** — changements à chaud sauvegardés : `den allow|deny [--save]`, `den kit add [--save]`,
  application aux VM vivantes d'une nest.
- **L2** — agents autonomes : un cron ou un événement lance une commande den qui fait travailler un agent
  dans une VM. Le prompt fixe ce qu'il a le droit de faire (exemple : sur une CVE, coder le correctif,
  ouvrir une PR, envoyer un message de relecture). Chaque job choisit son espace de travail : un clone
  fait dans la VM (`--clone`) ou un worktree d'un repo de l'hôte. Faits utiles : un `sbx exec` au premier
  plan garde la VM éveillée ; `exec` sort en 125 quand sbx échoue ; nom de VM unique par job [mesuré
  0.50.0] ; `sbx run --rm` [aide].
- **L3** — credentials par VM (un secret scopé n'est pris qu'à la création [mesuré 0.49.1]).
- **L4** — historique et sessions partagés entre VM.
- **L5** — den comme planificateur (son propre cron). La conception de L2 ne doit pas l'empêcher.

## 12. Laissé au builder

- Langage et structure du code (Go en v1 ; rien n'impose de changer).
- Nom et schéma du fichier de configuration den, dans le respect du YAML strict.
- Nom et emplacement exact du dossier de run.
- Parties de v1 réutilisables (worktree, config, manifest sont candidats).
- Comportement de `den up` sans nom de nest, aligné sur v1.
