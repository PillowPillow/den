# Validation carte des repos den

*den V2 · carte des repos par instance · 17 nov. 2026*

Ces affirmations remplacent la relecture de la conception. Répondez ligne par ligne ; vos réponses sont enregistrées au fil de l'eau et je les relis depuis la session. La conception sera alignée sur cette fiche, jamais l'inverse.

## Contexte

Quand den crée une VM, l'agent qui y tourne ne sait pas quels repos il peut toucher ni où ils sont. Le dev home Digitaleo comble ce manque avec un fichier partagé que chaque VM peut réécrire, ce qui casse l'hôte et les autres VM. La conception fait écrire par den, à la création de chaque instance, une carte des repos propre à cette instance, que Claude charge tout seul. Un « oui » fixe ce que je développe dans den ; un « non » rouvre le point avant le code.

## Glossaire

- **carte des repos** : Liste écrite par den à la création d'une instance : les repos montés dans la VM avec leur chemin, ceux du nest qui ne le sont pas, et les autres repos connus de la machine.
- **dev home** : Dépôt qui sert de point d'entrée à un environnement de travail (ici Digitaleo) : son den.yaml liste les nests, et les sessions démarrent dans son dossier.
- **kit de profil** : Kit de votre machine (profil perso ou pro) qui copie votre configuration Claude dans la VM : ~/.claude/CLAUDE.md, ~/.claude/settings.json, vos skills.
- **repos.local.json** : Fichier non versionné du dev home qui donne le chemin de chaque repo sur la machine ; un script du dev home (check.py locate) le régénère en scannant le dossier parent.
- **fichier de règles** : Fichier Markdown placé dans ~/.claude/rules/ ; Claude Code le charge à chaque lancement comme un CLAUDE.md, sans toucher aux autres fichiers de ~/.claude.
- **DEN_REPOS** : Variable d'environnement que den poserait dans la VM : le chemin du fichier JSON de la carte des repos, pour les scripts.
- **kit de l'instance** : Kit que den génère pour une seule instance, sous ~/.den/gen/, et qui lance Claude dans la VM ; den le réécrit à chaque création de l'instance. Distinct du kit de profil.
- **fiche de l'instance** : Fichier où den enregistre ce qu'il a créé pour une instance (~/.den/state/v2/instances/), dont la vraie branche de chaque worktree.

## Références de la spec

- **D1** : Décision 1 de la conception : la seule correspondance clé → chemin de den est repos: du fichier global.
- **D2** : Décision 2 de la conception : un repo non monté n'est pas disponible, l'agent demande de l'ajouter.
- **D3** : Décision 3 de la conception : Claude d'abord, Codex reprendra la même carte plus tard.
- **D4** : Décision 4 de la conception : la livraison à Claude par un fichier de règles.

Réponse vide = ligne non traitée. La fiche gagne sur le document qu'elle valide.

## A — Vos décisions

Je vérifie seulement que ma transcription est fidèle à ce que vous avez dit.

- **A1.** La seule correspondance entre une clé de repo et son chemin, pour den, est la section repos: du fichier ~/.den/den.yaml de la machine ; les den.yaml des projets citent ces clés. *(D1)*
  - **Réponse : Fidèle**
- **A2.** repos.local.json appartient au dev home : den ne le lit pas, ne l'écrit pas, et la carte des repos n'en parle pas. *(D1)*
  - Conséquence : Garder, lire ou supprimer ce fichier se décide dans le dépôt du dev home, en dehors de ce travail.
  - **Réponse : Fidèle**
- **A3.** Quand l'agent a besoin d'un repo que sa VM ne monte pas, il dit que ce repo n'est pas disponible et vous demande de l'ajouter ; il ne le cherche pas ailleurs et ne le clone pas. *(D2)*
  - Conséquence : Ajouter un repo veut dire recréer la VM (den rm puis den up) et perdre son état local : volumes docker, bases, /home/agent. Vous l'acceptez, car un nest doit monter d'emblée tout ce qu'il faut.
  - **Réponse : Fidèle**
- **A4.** Ce travail livre la carte des repos à Claude seulement ; Codex la recevra plus tard, dans un autre travail qui permettra à den de lancer Codex comme agent. *(D3)*
  - Conséquence : La carte ne doit rien contenir de propre à Claude, pour que Codex puisse lire le même texte.
  - **Réponse : Fidèle**
- **A5.** den livre la carte des repos à Claude par un fichier de règles, ~/.claude/rules/den-instance.md, sans toucher aux fichiers de votre kit de profil. *(D4)*
  - **Ce que montre la figure** : Seul le fichier de règles couvre les trois façons de lancer Claude sans toucher aux fichiers du kit de profil.

    | Critère | Fichier de règles (retenu) | Script lancé par Claude à l'ouverture de session (hook, déclaré dans settings.json) | Option ajoutée à la commande claude que lance den up |
    |---|---|---|---|
    | Session ouverte par den up | chargé | chargé | chargé |
    | den exec -- claude | chargé | chargé | absent |
    | claude lancé depuis den shell | chargé | chargé | absent |
    | Fichiers du kit de profil (CLAUDE.md, settings.json) | intacts | settings.json en conflit | intacts |

  - **Réponse : Fidèle**

## B — Mes choix par défaut

Les vrais points à trancher. Chaque ligne donne le choix retenu, puis l'alternative.

### Ce que contient la carte

- **B1.** La carte des repos a trois listes : les repos montés (nom, chemin dans la VM, branche d'un worktree), les repos marqués optional: true dans le nest que --only, --without ou -i ont écartés, et les autres clés de repos: de la machine avec leur chemin. *(§6.1 Contenu de la carte)*
  - **Ce que montre la figure** : Pour une instance oauth créée sans libeo, la carte liste oauth.messengeo.net et neuf autres repos montés, libeo comme repo du nest non monté, et php.flow parmi les autres repos de la machine.

    | Critère | Repos montés | Repos du nest non montés | Autres repos de la machine |
    |---|---|---|---|
    | Contenu | nom, chemin dans la VM, requis ou optionnel, branche si worktree | nom et chemin sur l'hôte | clé de repos: et chemin sur l'hôte |
    | Exemple pour oauth créé avec --without libeo | oauth.messengeo.net → /Users/polochon/Development/Digitaleo/oauth.messengeo.net | libeo | php.flow, js.dam… |
    | Sert à | savoir où travailler | proposer de recréer avec ce repo | proposer de l'ajouter au nest ou en --repo |

  - Sinon : Une seule liste, les repos montés ; l'agent ne peut alors pas proposer la commande exacte pour ajouter un repo.
  - **Réponse : Choix retenu**
- **B2.** Un repo marqué optional: true dans le nest, dont la clé manque à repos: du fichier global, n'apparaît dans aucune liste : den l'écarte déjà avec un avertissement, et l'agent le traite comme tout repo absent. *(§6.1 Contenu de la carte)*
  - Sinon : Le lister à part comme « déclaré par le nest, absent de cette machine », ce qui demande de changer la façon dont den lit le nest.
  - **Réponse : Choix retenu**
- **B3.** Pour un worktree créé avec -w, la carte montre la branche réelle, lue dans la fiche de l'instance, même quand le worktree existait déjà sur une autre branche que celle tapée. *(§6.1 Contenu de la carte)*
  - Sinon : Montrer la branche tapée sur la ligne de commande.
  - **Réponse : Choix retenu**
- **B4.** Le texte de la carte des repos ne nomme ni Claude, ni le dev home, ni repos.local.json ; il finit par la règle : un repo non monté n'est pas disponible, dis-le et demande d'ajouter le repo (den rm puis den up --repo, ou l'ajout au nest). *(§6.2 Deux formes, une source)*
  - Sinon : Un texte écrit pour Claude et le dev home Digitaleo, à réécrire pour Codex et les autres projets.
  - **Réponse : Choix retenu**

### Où vit la carte

- **B5.** La même carte existe aussi en JSON dans la VM, /home/agent/.den/repos.json, et den exporte DEN_REPOS avec ce chemin pour les scripts. *(§6.2 Deux formes, une source)*
  - Sinon : Seulement le texte Markdown pour l'agent ; un script devrait alors l'analyser.
  - **Réponse : Choix retenu**
- **B6.** den écrit les fichiers de la carte des repos dans le kit de l'instance, sous ~/.den/gen/, hors de tout dossier monté, pour qu'aucune VM ne puisse réécrire la carte d'une autre ; sbx les copie dans le home de la VM à sa création. *(§6.3 Livraison)*
  - **Ce que montre la figure** : den lit repos: du fichier global et le nest du dev home, écrit la carte dans le kit de l'instance sous ~/.den/gen, et sbx la copie dans le home de la VM, où Claude la charge ; le dossier du dev home monté n'est jamais écrit par den.
    1. ~/.den/den.yaml (repos:) → den up : chemins des clés
    2. dev-home/den.yaml (nest) → den up : repos du nest
    3. den up → kit de l'instance (~/.den/gen/oauth/) : écrit la carte
    4. kit de l'instance (~/.den/gen/oauth/) → ~/.claude/rules/den-instance.md : copié par sbx
    5. kit de l'instance (~/.den/gen/oauth/) → ~/.den/repos.json (DEN_REPOS) : copié par sbx
    6. ~/.claude/rules/den-instance.md → Claude : chargé au lancement
    7. Claude → dev home monté (lecture-écriture) : travaille dedans
  - Sinon : Les écrire dans le dossier du dev home (par exemple .den/run/oauth/), qui est monté en lecture-écriture et partagé.
  - **Réponse : Choix retenu**
- **B7.** La carte des repos est écrite à la création de l'instance et ne change plus ; une clé ajoutée à repos: ensuite n'y apparaît qu'après den rm puis den up. *(§6.4 Cycle de vie)*
  - Sinon : La réécrire à chaque den up, même sur une instance qui tourne, ce que la copie du kit à la création ne permet pas sans mécanisme supplémentaire.
  - **Réponse : Choix retenu**

### Garde-fous

- **B8.** Si votre env: (global ou nest) définit déjà DEN_REPOS, den refuse de créer l'instance et nomme le fichier à corriger. *(§6.2 Deux formes, une source)*
  - Sinon : Votre valeur gagne, et la carte JSON reste sans variable qui la désigne.
  - **Réponse : Choix retenu**
- **B9.** Chaque VM voit le chemin sur l'hôte de tous les repos de repos:, même ceux qu'elle ne monte pas ; un chemin ne donne aucun accès, puisque le dossier n'existe pas dans la VM. *(§6.4 Cycle de vie)*
  - Sinon : Ne donner que les clés des repos non montés, sans chemin ; l'agent ne peut plus écrire la commande --repo exacte.
  - **Réponse : Choix retenu**

### Construction

- **B10.** Le développement commence par un essai sur une VM jetable, pour vérifier deux points : que le kit de l'instance sait copier des fichiers dans le home de la VM, et que ce home est /home/agent. Si le premier point échoue, un second kit généré par instance porte les deux fichiers ; rien d'autre ne change. *(§8 Laissé au constructeur)*
  - **Réponse : D'accord**

## C — Ce que vous n'avez pas à valider

Faits vérifiés, sur lesquels la conception s'appuie. Si un de ces points contredit A ou B, c'est A ou B qui gagne. Claude Code charge à chaque lancement chaque fichier de ~/.claude/rules/ qui n'a pas de filtre paths:, comme un CLAUDE.md (documentation Claude Code, page memory, lue le 17 nov.). Codex n'a pas de dossier de règles : il lit un seul fichier global, ~/.codex/AGENTS.override.md sinon ~/.codex/AGENTS.md (code source de Codex). Livrer la carte à Codex demandera de la fusionner avec vos instructions Codex globales. Hook de démarrage : il vivrait dans ~/.claude/settings.json, que votre kit de profil fournit déjà ; l'un écraserait l'autre. Écarté. Option --append-system-prompt-file sur la commande de lancement : elle ne couvre que la session ouverte par den up, pas den exec -- claude ni un claude lancé depuis den shell. Écartée. Accès aux repos : dans une VM den, Claude tourne sans demande de permission et lit un repo monté hors de son dossier de départ sans réglage additionalDirectories (mesuré dans am : lecture de am-frontend depuis am-backend). La carte informe, elle n'accorde rien. Écriture non mesurée. Chemins : den monte chaque repo à son chemin sur l'hôte ; le chemin dans la VM est donc le même que sur le Mac. Montages figés : sbx fixe les montages d'une VM à sa création ; ajouter un repo demande den rm puis den up.
