# Validation défauts Claude

*den V2 · réglages Claude Code par défaut dans les VM Digitaleo · 17 nov. 2026*

Ces affirmations remplacent la relecture du design. Répondez aux exceptions ; vos réponses sont enregistrées au fil de l'eau. Le design sera aligné sur cette fiche, jamais l'inverse.

## Contexte

Aujourd'hui, une VM lancée avec den depuis le template du dépôt den-env démarre Claude Code sans marketplace dgdev ni statusline : chacun doit écrire son propre kit pour les obtenir. Le design ajoute à den un moyen de poser des réglages Claude Code par défaut dans la VM, puis den-env l'utilise pour que toute VM Digitaleo reçoive dgdev et sa statusline. Un « oui » engage deux changements : la commande de démarrage du kit den-claude dans den, et un nouveau kit ajouté au template de den-env, avec une section README.

## Glossaire

- **kit** : Dossier qu'sbx ajoute à une VM ; une VM reçoit une liste ordonnée de kits, ceux du template puis ceux de la personne, et le files/home/ de chaque kit est copié dans le home de la VM.
- **kit perso** : Kit facultatif propre à une personne, placé après les kits du template dans la liste ; celui de Nicolas livre son ~/.claude/settings.json complet, ses skills et son CLAUDE.md.
- **commande de démarrage du kit den-claude** : Commande que den fait exécuter à chaque démarrage de la VM, après la copie des fichiers de tous les kits.
- **fragment de défauts** : Fichier JSON qu'un kit dépose dans la VM et qui ne contient que des réglages Claude Code par défaut ; den fusionne chaque fragment dans les réglages utilisateur de la VM, et les valeurs déjà présentes dans ce fichier l'emportent. Plusieurs kits peuvent en déposer un chacun.
- **réglages utilisateur de la VM** : Le ~/.claude/settings.json de la VM, pas le .claude/settings.json d'un dépôt (qui passe déjà au-dessus chez Claude Code).

Réponse vide = ligne non traitée. La fiche gagne sur le document qu'elle valide.

## A — Vos décisions

Je vérifie seulement que ma transcription est fidèle à ce que vous avez dit.

- **A1.** Le README de den-env indique à chaque personne de poser DGDEV_USER et DGDEV_DEV_ROOTS dans la section env: de son ~/.den/den.yaml, que den transmet à la VM ; den-env ne fournit aucune valeur pour ces deux variables. *(Objectifs)*
  - **Réponse : Fidèle**
- **A2.** DGDEV_DOCKER_IMAGES_PHP reste dans la configuration personnelle de Nicolas et n'entre pas dans den-env. *(Non-objectifs)*
  - **Réponse : Fidèle**
- **A3.** Un nouveau kit, ajouté à la liste des kits du template de den-env, apporte les réglages par défaut à toute VM Digitaleo, que la personne ait un kit perso ou non. *(Intention)*
  - Conséquence : Un kit perso qui livre son propre ~/.claude/settings.json ne fait pas disparaître les défauts : ils sont fusionnés dans ce fichier après sa copie.
  - **Réponse : Fidèle**
- **A4.** Le kit perso de Nicolas n'est pas modifié. *(Non-objectifs)*
  - **Réponse : Fidèle**
- **A5.** Par défaut, seule la marketplace dgdev est déclarée ; les autres marketplaces et plugins Digitaleo se déclarent projet par projet, dans le .claude/settings.json du dépôt. *(Objectifs)*
  - **Réponse : Fidèle**
- **A6.** Ces réglages sont des valeurs par défaut : une clé déjà présente dans les réglages utilisateur de la VM, posée par un kit perso ou modifiée dans la VM, garde sa valeur ; le défaut ne remplit que les clés absentes. *(Intention)*
  - **Réponse : Fidèle**
- **A7.** den porte le mécanisme de fusion, dans la commande de démarrage du kit den-claude ; le kit de den-env ne livre qu'un fragment de défauts, sans script. *(Approches)*
  - Conséquence : Tout autre template peut livrer ses propres défauts de la même façon, sans écrire de fusion.
  - **Réponse : Fidèle**

## B — Mes choix par défaut

Les vrais points à trancher. Chaque ligne donne le choix retenu, puis l'alternative.

### Contenu des défauts Digitaleo

- **B1.** Le fragment de défauts Digitaleo déclare la marketplace dgdev et active aussi le plugin Claude Code qu'elle publie (distinct de la commande dgdev). *(Objectifs)* — _Jamais discuté_
  - Sinon : Déclarer seulement la marketplace dgdev ; chaque personne active le plugin elle-même.
  - **Réponse : Choix retenu**
- **B2.** La statusline par défaut d'une VM Digitaleo est la commande dgdev statusline, sans option. *(Objectifs)*
  - Sinon : Aucune statusline par défaut ; chaque personne la configure dans son kit perso.
  - **Réponse : Choix retenu**

### Mécanisme dans den

- **B3.** Les kits déposent leurs fragments de défauts dans le dossier ~/.den/claude-settings.d/ de la VM, réservé à den. *(Décisions)* — _Jamais discuté_
  - Conséquence : Le nom du dossier dit que c'est den qui le lit ; Claude Code ne le lit pas et ne peut pas le confondre avec ses propres fichiers.
  - Sinon : Les déposer dans un dossier sous ~/.claude/, à côté des fichiers de Claude Code.
  - **Réponse : Choix retenu**
- **B4.** Dans la fusion, un objet JSON se fusionne clé par clé à toute profondeur ; une liste se remplace en entier. *(Décisions)* — _Jamais discuté_
  - **Ce que montre la figure** : Avec un fragment de défauts qui autorise « git status » et des réglages utilisateur qui autorisent « ls », le choix retenu garde seulement « ls » ; l'alternative garde les deux.

    | Critère | Remplacer (retenu) | Concaténer |
    |---|---|---|
    | Liste autorisée dans le fragment de défauts | git status | git status |
    | Liste autorisée dans les réglages utilisateur | ls | ls |
    | Liste obtenue | ls | git status, ls |

  - Conséquence : Si les réglages utilisateur de la VM ont leur propre liste de permissions autorisées, la liste du fragment de défauts disparaît.
  - Sinon : Concaténer les listes du fragment de défauts et celles des réglages utilisateur de la VM.
  - **Réponse : Choix retenu**
- **B5.** Quand deux fragments de défauts posent la même clé, celui dont le nom de fichier vient en dernier dans l'ordre alphabétique l'emporte. *(Décisions)* — _Jamais discuté_
  - Conséquence : L'auteur d'un kit règle cet ordre par un préfixe numérique dans le nom du fichier (10-…, 50-…).
  - Sinon : Refuser le démarrage de la VM quand deux fragments de défauts posent la même clé.
  - **Réponse : Choix retenu**
- **B6.** La fusion des défauts s'exécute à chaque démarrage de la VM. *(Décisions)*
  - Conséquence : Une valeur modifiée dans la VM reste, même si un fragment de défauts pose la même clé. Une clé par défaut supprimée dans la VM revient au démarrage suivant : pour la neutraliser, un kit perso lui donne une autre valeur.
  - Sinon : Fusionner une seule fois, au premier démarrage : un défaut ajouté plus tard au template n'atteint plus une VM existante.
  - **Réponse : Choix retenu**
- **B7.** Un fragment de défauts qui n'est pas un objet JSON fait échouer le démarrage de la VM, avec une erreur qui nomme le fichier. *(Décisions)* — _Jamais discuté_
  - Sinon : Ignorer ce fragment de défauts, afficher un avertissement, et démarrer la VM.
  - **Réponse : Choix retenu**
- **B8.** La VM temporaire que den build lance pour construire l'image du template ne reçoit pas les réglages par défaut. *(Décisions)*
  - Conséquence : Rien ne change pour le build : cette VM n'ouvre aucune session Claude Code.
  - **Réponse : D'accord**

### Vérification

- **B9.** Les tests de den vérifient seulement le texte de la commande de fusion ; celui qui implémente vérifie son comportement à la main, avec jq sur le Mac puis dans une VM recréée. *(Laissé au constructeur)* — _Jamais discuté_
  - Sinon : Ajouter un test qui exécute jq, en dérogeant à la règle des tests de den qui n'autorise aucun processus autre que git.
  - **Réponse : Choix retenu**

## C — Ce que vous n'avez pas à valider

Faits établis pendant la session. Si un de ces points contredit A ou B, c'est A ou B qui gagne. Vérifié dans la VM oauth en cours d'exécution : dgdev est installé dans /usr/local/bin, et `dgdev statusline` affiche la ligne DIGITALEO (code de sortie 0). Vérifié dans la même VM : dgdev lit son utilisateur et ses dossiers de travail dans DGDEV_USER et DGDEV_DEV_ROOTS ; aucun fichier de configuration dgdev n'est nécessaire. Vérifié dans la même VM : `dgdev statusline` fonctionne aussi sans DGDEV_USER ni DGDEV_DEV_ROOTS. Source secondaire (commentaire du kit den-claude) : la commande de démarrage du kit den-claude s'exécute après la copie des fichiers de tous les kits, à chaque démarrage. Vérifié : sbx copie les fichiers d'un kit sans leur bit d'exécution ; c'est pourquoi la statusline appelle dgdev directement et non un script livré par un kit. Non vérifié : quand deux kits livrent le même fichier, sbx garde celui du dernier kit ; un kit d'équipe qui livrerait son propre ~/.claude/settings.json serait donc effacé par un kit perso. De mémoire, non vérifié : Claude Code n'a aucune couche de réglages sous ~/.claude/settings.json ; ses réglages d'entreprise (managed settings) passent au-dessus et imposent leurs valeurs, d'où leur rejet. Les tests de den ne lancent aucun processus autre que git. Dans le dépôt den-env, le travail part d'une branche créée depuis la branche den-v2. Le CLAUDE.md de den et le README de den décrivent la nouvelle convention.
