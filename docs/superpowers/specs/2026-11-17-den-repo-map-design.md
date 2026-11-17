# den V2 — carte des repos par instance

Date : 2026-11-17. Branche : `v2`. Statut : approuvé le 2026-11-17 (fiche `2026-11-17-den-repo-map-validation.md`, 15 lignes acceptées ; la fiche gagne sur ce document).

## 1. Intention

Quand une session démarre dans une VM den, l'agent doit connaître d'emblée les repos qu'il peut toucher :
leur nom, leur chemin dans la VM, et ceux qui ne sont pas là. Aujourd'hui il n'a aucune source fiable :
le dev home Digitaleo lui fait lire `repos.local.json`, un fichier unique à la racine du dev home, et
régénérer ce fichier (`check.py locate`) quand une entrée manque. Avec `start: .`, le dev home est monté en
lecture-écriture : une VM qui régénère le fichier n'y écrit que ses propres repos, et casse l'hôte et les
autres VM qui lisent le même fichier.

den est le seul à savoir ce que chaque instance monte. Il écrit donc lui-même, à la création de chaque
instance, une carte propre à cette instance, hors de tout montage partagé, et la fait charger par l'agent.

## 2. Décisions de l'utilisateur (2026-11-17)

- D1. La seule correspondance clé → chemin de den est `repos:` du `~/.den/den.yaml` global, obligatoire ;
  les `den.yaml` de projet réutilisent ses clés. `repos.local.json` appartient au dev home ; den ne le lit
  ni ne l'écrit, et il pourra disparaître.
- D2. Un repo non monté n'est pas disponible : l'agent le dit et demande à l'humain de l'ajouter. Recréer
  la VM pour ça (perte de son état local : volumes docker, bases, `/home/agent`) est acceptable, car un nest
  est censé monter d'emblée tout ce qu'il faut.
- D3. Claude d'abord. Codex lira la même carte plus tard, dans le chantier qui permettra à den de lancer
  Codex comme agent ; la carte ne doit donc rien contenir de propre à Claude.
- D4. Approche A (§5) retenue pour la livraison à Claude.

## 3. Buts, non-buts, anti-buts

Buts :
- G1. Chaque session (`den up`, `den shell` puis `claude`, `den exec -- claude -p …`) démarre avec la carte
  de son instance dans ses instructions, sans action de l'utilisateur.
- G2. La carte est exacte pour toute la vie de l'instance (sbx fige les montages à la création).
- G3. Les scripts lisent la même carte en JSON via `DEN_REPOS`.
- G4. Aucune instance n'écrit dans un fichier lu par une autre instance ou par l'hôte.

Non-buts :
- Lancer Codex dans une VM, ou livrer la carte à Codex (D3).
- Modifier le dev home (`AGENTS.md`, `check.py`) : c'est le dépôt du dev home qui décidera s'il lit
  `$DEN_REPOS` ou abandonne `repos.local.json`.
- Ajouter un repo à une instance vivante (sbx ne le permet pas ; le remède reste `den rm` puis `den up`).
- Découvrir des repos automatiquement : `repos:` reste écrit à la main (D1).

Anti-buts (échec même si ça marche) :
- den écrit `~/.claude/CLAUDE.md` ou `~/.claude/settings.json` : ce sont les fichiers du kit de profil
  `perso`, que la carte écraserait.
- La carte vit dans un dossier monté en lecture-écriture partagé (le dev home, un repo).
- La carte cite `repos.local.json` ou le dev home : elle sert tout projet, pas seulement Digitaleo.

## 4. Contraintes

- C1. Claude Code charge au lancement chaque fichier de `~/.claude/rules/` sans front matter `paths:`,
  avec la priorité d'un `CLAUDE.md` (doc Claude Code, page memory, « User-level rules », lue le
  2026-11-17). Ce chargement vaut pour toute session, quel que soit le dossier de départ.
- C2. Codex n'a pas de dossier de règles : il lit un seul fichier global, `$CODEX_HOME/AGENTS.override.md`
  sinon `$CODEX_HOME/AGENTS.md` (source Codex, `codex-home/src/instructions/mod.rs`). Raison de D3 : la
  livraison à Codex devra fusionner la carte avec les instructions globales d'un kit de profil.
- C3. Un hook `SessionStart` vivrait dans `~/.claude/settings.json`, fichier du kit `perso` (anti-but).
- C4. `--append-system-prompt-file` sur l'entrypoint du kit ne couvre que la session lancée par `den up` :
  ni `den exec -- claude`, ni un `claude` lancé depuis `den shell`.
- C5. Dans une VM den, Claude tourne en `--dangerously-skip-permissions` et lit un repo monté hors de son
  dossier de départ sans `permissions.additionalDirectories` (mesuré dans `am` le 2026-11-17, lecture de
  `am-frontend/package.json` depuis `am-backend`). La carte n'a donc pas à accorder d'accès, seulement à
  informer. Écriture non mesurée.
- C6. Chaque instance a déjà son propre kit `den-claude` (`<GenDir>/den-claude/`) ; le kit de profil
  `perso` livre ses fichiers par `files/home/…`.
- C7. den monte chaque repo à son chemin hôte ; avec `-w`, le worktree est monté au chemin du clone
  principal. Le chemin VM d'un repo est donc son chemin hôte, et la branche dit ce qu'il contient.

## 5. Approches

| | Approche | Coût | Verdict |
|---|---|---|---|
| A | Un fichier de règles `~/.claude/rules/den-instance.md` livré par le kit `den-claude` de l'instance, plus le JSON et `DEN_REPOS` | dépend du support de `files/` par un kit `kind: sandbox` (à vérifier, §8) | **retenue** (D4) |
| B | Hook `SessionStart` qui injecte la carte | écrase ou est écrasé par `settings.json` du kit `perso` (C3) | écartée |
| C | `--append-system-prompt-file` sur l'entrypoint | rate `den exec -- claude` et `den shell` (C4) | écartée |

## 6. Conception

### 6.1 Contenu de la carte

den la calcule à la création de l'instance (état Recorded compris : elle est recalculée), à partir de la
config, de la sélection et de la fiche de l'instance, jamais d'une découverte. *Choix du rédacteur* : elle
est rendue dans `Create`, après les worktrees et l'écriture de la fiche, avant `sbx env create`, à partir
de la fiche : c'est la fiche qui garde la vraie branche d'un worktree (trouvé en place compris). Le rendu
lui-même reste une fonction pure de ses entrées, testée sans sbx :

- l'instance, le nest, le fichier `den.yaml` qui le déclare, le template, le dossier de départ ;
- **repos montés** : nom (nom de dossier), clé `repos:` s'il y en a une (aucune pour un `--repo` ad hoc ou
  un `path:`), chemin dans la VM, requis ou optionnel, et pour un worktree `-w` la branche ;
- **repos du nest non montés** : les optionnels que `--only`, `--without` ou `-i` ont écartés, avec leur
  chemin hôte (la différence entre `Resolved.Repos` et la sélection). Un optionnel dont la clé manque à
  `repos:` n'y figure pas : `nest.Resolve` l'écarte déjà avec un avertissement (`resolve.go:211`), il n'est
  pas sur cette machine, et l'agent le traite comme tout repo absent. *Choix du rédacteur* : `internal/nest`
  ne change pas ;
- **autres repos de la machine** : les clés de `repos:` du fichier global que le nest ne cite pas, avec
  leur chemin hôte.

Les deux dernières listes servent le remède de D2 : l'agent peut donner la commande exacte.

### 6.2 Deux formes, une source

- `~/.den/repos.json` dans la VM : la carte en JSON, versionnée par un champ `schema: 1`. den exporte
  `DEN_REPOS=/home/agent/.den/repos.json` dans l'`env:` de l'instance. *Choix du rédacteur* : le home de
  l'agent est `/home/agent` (mesuré dans `am`, image `claude-code-docker`) ; le spike du §8 le vérifie pour
  les images que den sait lancer. Un `DEN_REPOS` déjà présent dans un `env:` (global ou nest) est refusé dans
  `Prepare`, avec le fichier à corriger, comme den le fait pour toute collision.
- `~/.claude/rules/den-instance.md` : la même carte en Markdown court, suivie de la règle de D2 :
  « Un repo absent de la liste des repos montés n'est pas disponible dans cette VM. Dis-le, et demande à
  l'humain de l'ajouter : `den rm <instance>` puis `den up <nest> --repo <chemin>` (ou de l'ajouter au
  nest). Ne le cherche pas ailleurs, ne le clone pas. »

Le texte ne mentionne ni Claude ni `repos.local.json` (anti-buts, D3) : Codex pourra recevoir le même
Markdown.

### 6.3 Livraison

den écrit les deux fichiers dans le kit `den-claude` de l'instance (`<GenDir>/den-claude/files/home/…`),
qu'il régénère déjà à chaque création. Rien n'est écrit sous un montage. La VM peut modifier sa copie :
cela ne touche qu'elle.

### 6.4 Cycle de vie

- Création (et recréation d'une instance Recorded) : carte recalculée et réécrite.
- Attache d'une instance Live : rien ne change, comme les montages.
- `den rm` : le kit part avec `<GenDir>`, comme aujourd'hui.
- La liste « autres repos de la machine » donne à la VM le chemin hôte de repos qu'elle ne monte pas. Accepté :
  un chemin ne donne aucun accès (le dossier n'existe pas dans la VM), et l'utilisateur a vu cette liste dans
  la conception sans objection.
- Une clé ajoutée à `repos:` après la création n'apparaît pas dans la carte d'une instance vivante ; elle
  apparaît à la recréation. Accepté : la liste « autres repos » n'est qu'une aide au remède.

## 7. Critères d'acceptation

- A1. Dans une instance créée par `den up`, `claude -p "liste les repos montés et leur chemin"` lancé par
  `den exec` répond avec la liste de la sélection, sans lire de fichier du projet.
- A2. Deux instances du même dev home, avec des sélections différentes, ont chacune leur carte ; aucune
  n'écrit sous le dev home.
- A3. `echo $DEN_REPOS` dans `den shell` donne un fichier JSON lisible dont les repos montés égalent ceux
  de la fiche `state/v2/instances/<instance>.yaml`.
- A4. `~/.claude/CLAUDE.md` et `~/.claude/settings.json` de la VM restent ceux du kit de profil.
- A5. Une instance créée avec `--without libeo` liste `libeo` parmi les repos du nest non montés.

## 8. Laissé au constructeur

- **D'abord, un spike** (VM `den-probe-*` jetable, `--skip-host-commands`) : vérifier qu'un kit
  `kind: sandbox` livre `files/home/…` comme un mixin, et que le home de l'agent est `/home/agent`. Si le kit
  sandbox ne livre pas de fichiers, porter les deux fichiers par un kit mixin par instance
  (`<GenDir>/den-repos/`, hors de tout montage, donc sans overlay), ajouté à la liste des kits de l'instance ;
  le reste de la conception ne change pas.
- Le schéma JSON exact et la mise en forme du Markdown, dans le style des sorties den.
- Si la carte entre dans le hash de config de l'instance (aujourd'hui `configHash`) : une carte dérivée de
  la config et de la sélection n'apporte pas d'information nouvelle ; à trancher sans changer le
  comportement d'attache.
- Les tests (rendu pur et déterministe de la carte, goldens à la main comme le reste de `internal/sbxenv`).
