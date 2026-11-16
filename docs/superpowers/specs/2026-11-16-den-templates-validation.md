# Validation templates den

*den V2 · templates partagés entre dev homes · 16 nov. 2026*

39 affirmations remplacent la relecture de la spec. Répondez seulement aux exceptions ; vos réponses sont enregistrées au fil de l'eau. La spec sera alignée sur cette fiche, jamais l'inverse.

## Contexte

Aujourd'hui, un nest ne peut utiliser qu'une base déclarée dans son propre den.yaml ou dans celui de la machine (sous ~/.den/), et l'image officielle de départ est figée dans le code de den. La spec remplace les bases par des templates dont la définition vit dans un dépôt git ou un dossier, pour que plusieurs dev homes partagent la même VM sans la recopier. Un « oui » ici fixe le comportement à développer sur la branche v2 ; un « non » rouvre le point avant le plan.

## Glossaire

- **dev home** : Dépôt qui sert de point d'entrée à un environnement de travail (Digitaleo, projets Kampn legacy…) et qui porte son propre den.yaml.
- **template** : Remplace « base » : un nom, plus le lieu où vit sa définition. Une fois construit par den build, c'est un template sbx, l'instantané de VM dont partent les instances.
- **lieu** : Dépôt git ou dossier local qui porte un fichier den-template.yaml à sa racine.
- **définition** : Ce que den-template.yaml dit d'un template : image de départ, kits, allowlist, étapes de build.
- **image officielle** : Image de VM publiée par sbx (par exemple celle qui contient Claude Code), sur laquelle un build démarre ou qu'une instance utilise telle quelle.

Réponse vide = ligne non traitée. La fiche gagne sur le document qu'elle valide.

## A — Vos décisions

Je vérifie seulement que ma transcription est fidèle à ce que vous avez dit.

- **A1.** Chaque dev home versionne un den.yaml dont les nests listent tous les dépôts de code montables dans la VM ; au lancement, vous choisissez les dépôts de la session. *(Intention)*
  - **Réponse : Fidèle**
- **A2.** Un den.yaml nomme des templates et dit, pour chacun, quel lieu porte sa définition : un dépôt git distant ou un dossier local. *(Conception, den.yaml)*
  - **Réponse : Fidèle**
- **A3.** Le den.yaml de la machine (sous ~/.den/) et le den.yaml d'un projet acceptent les mêmes clés de templates et se comportent de la même façon. *(Conception, den.yaml)*
  - **Réponse : Fidèle**
- **A4.** Un lieu expose un ou plusieurs templates par un fichier den-template.yaml placé à sa racine. *(Conception, den-template.yaml)*
  - **Réponse : Fidèle**
- **A5.** Dans den.yaml, une entrée de template porte une clé facultative « name » qui dit quel template du lieu elle désigne ; sans elle, den cherche dans le lieu un template du même nom que l'entrée. *(Conception, den.yaml)*
  - Conséquence : Pas de forme « adresse:nom » : une adresse git contient déjà des deux-points.
  - **Réponse : Fidèle**
- **A6.** Chaque nest peut nommer son template ; sinon il prend celui du réglage « default_template » de son den.yaml. *(Conception, den.yaml)*
  - **Réponse : Fidèle**
- **A7.** Le réglage « default_template » d'un den.yaml peut contenir directement la référence d'une image officielle au lieu d'un nom de template ; l'instance démarre alors sur cette image, sans den build. *(Conception, den.yaml)*
  - **Réponse : Fidèle**
- **A8.** Quand la définition d'un dépôt distant a de nouveaux commits, den le signale au lancement d'une instance et affiche la commande de mise à jour ; den ne met jamais la machine à jour seul. *(Objectifs)*
  - **Réponse : Fidèle**
- **A9.** L'image officielle est de la configuration dans den.yaml, plus une valeur figée dans le code : une nouvelle image sbx ne demande pas de nouvelle version de den. *(Intention)*
  - **Réponse : Fidèle**
- **A10.** Le template reste construit sur chaque machine par den build, à partir d'une VM ; pas de Dockerfile, pas d'image téléchargée. *(Non-objectifs)*
  - **Réponse : Fidèle**

## B — Mes choix par défaut

Les vrais points à trancher. Chaque ligne donne le choix retenu, puis l'alternative.

### Résolution

- **B1.** Un nom de template se cherche d'abord dans le den.yaml du projet, puis dans celui de la machine ; si les deux déclarent le même nom de template, le projet l'emporte. *(choix du rédacteur)*
  - Sinon : den refuse un nom de template déclaré dans les deux fichiers.
  - **Réponse : Choix retenu**
- **B29.** Pour lire « default_template », den cherche d'abord un template déclaré de ce nom ; s'il n'en trouve pas, il traite la valeur comme une image officielle. *(votre note sur la règle de forme)*
  - Conséquence : Une faute de frappe dans un nom de template fait chercher une image officielle de ce nom ; l'erreur dit alors qu'aucun template ni aucune image ne porte ce nom.
  - Sinon : Règle de forme : une valeur qui contient « / » ou « : » est une image, toute autre est un nom de template.
  - Réponse à la note : Votre note remplace la règle de forme par cette recherche en deux temps.
  - **Réponse : Choix retenu**
- **B30.** Une image officielle peut s'écrire par son nom court, par exemple « claude-code-docker » ; den le complète avec l'adresse du dépôt d'images de sbx sur Docker Hub. Une adresse complète reste acceptée telle quelle. *(suite de votre note sur la règle de forme)* — _Jamais discuté_
  - Conséquence : Les noms courts sont ceux que den affiche quand « default_template » manque.
  - Sinon : Seule l'adresse complète de l'image est acceptée.
  - **Réponse : Choix retenu**
- **B3.** La clé « template » d'un nest accepte elle aussi directement une image officielle à la place d'un nom de template. *(choix du rédacteur)*
  - Sinon : Seul « default_template » accepte une image ; un nest ne nomme que des templates.
  - **Réponse : Choix retenu**
- **B4.** Quand ni le den.yaml du projet ni celui de la machine ne fixent « default_template », den refuse de démarrer et affiche la liste des images officielles disponibles. *(choix du rédacteur)*
  - Conséquence : Mesuré aujourd'hui : le registre public Docker Hub renvoie cette liste par une requête sans authentification (226 tags pour les images de sbx). Seule l'image Claude Code avec docker a été mesurée comme gardant la VM en marche ; les autres ne sont pas vérifiées.
  - Option Liste en ligne (choix retenu) : den interroge Docker Hub et affiche les noms courts des images (sans les variantes datées ni nocturnes) ; hors ligne, il affiche seulement l'adresse de la page qui les liste.
  - Option Adresse seule : den n'interroge rien et affiche l'adresse de la page Docker Hub qui liste les images.
  - Option Valeur dans le code : den ne refuse pas : il utilise le tag de l'image officielle Claude Code écrit dans son code.
  - Réponse à la note : Oui, la liste se récupère en ligne : je l'ai vérifié par une requête au registre Docker Hub. Le refus avec liste devient le choix ; il reste à dire d'où vient la liste.
  - **Réponse : Liste en ligne** — den refuse et doit donner la liste des images offi dispos, a voir si on peut recuperer ca depuis une requete en ligne ? 
- **B5.** Une entrée de template peut fixer la branche ou le tag git à suivre ; sans cela, den suit la branche par défaut du dépôt. *(choix du rédacteur)*
  - Sinon : Pas de réglage : den suit toujours la branche par défaut.
  - **Réponse : Choix retenu**

### Migration

- **B6.** Les clés « bases » et « agents » de den.yaml sont supprimées sans période de transition ; den refuse un fichier qui les contient et nomme la clé de remplacement. *(choix du rédacteur)*
  - Conséquence : Votre den.yaml de machine et celui du dépôt den-env doivent être convertis avant le premier usage du nouveau den.
  - Sinon : den lit encore « bases » et « agents » pendant une période de transition.
  - **Réponse : Choix retenu**
- **B7.** Le nouveau den ne lit pas la fiche d'une instance créée par le den actuel : vous supprimez toutes vos instances avec le den actuel avant de l'installer. *(choix du rédacteur)*
  - Conséquence : Une instance oubliée bloque den rm avec le nouveau den ; le message d'erreur nomme le fichier de la fiche à supprimer à la main.
  - Sinon : Le nouveau den sait encore lire ces fiches, et den rm supprime ces instances normalement.
  - Réponse à la note : Vous avez retenu la suppression préalable des instances : c'est maintenant le choix de la ligne.
  - **Réponse : Choix retenu**
- **B8.** Les nests Digitaleo déclarés aujourd'hui dans le den.yaml du dépôt den-env sont déplacés dans le den.yaml du dev home Digitaleo ; je vous demanderai lesquels garder au moment de ce déplacement. *(choix du rédacteur)*
  - **Réponse : D'accord**

### Définition

- **B9.** Un seul fichier den-template.yaml décrit tous les templates d'un lieu, chacun avec les rubriques d'une base actuelle (image de départ, kits, allowlist, étapes de build) ; les chemins qu'il cite partent du dossier qui le contient. *(choix du rédacteur)*
  - Sinon : den-template.yaml ne contient que la liste des templates, et chaque template a son sous-dossier avec son propre fichier de description.
  - **Réponse : Choix retenu**
- **B10.** Le nom du template sbx construit n'est plus écrit à la main : den le dérive du nom du template. *(choix du rédacteur)*
  - Sinon : Le fichier de définition déclare le nom du template sbx, comme aujourd'hui.
  - **Réponse : Choix retenu**
- **B11.** Un template dont la définition ne déclare pas d'image de départ se construit sur l'image officielle fixée par « default_template » du den.yaml de la machine ; si ce réglage est absent ou nomme un template, den build refuse et demande une image de départ. *(choix du rédacteur)*
  - Sinon : L'image de départ est obligatoire dans chaque définition.
  - Réponse à la note : Ligne modifiée par votre réponse sur l'absence de « default_template » : il n'y a plus de valeur écrite dans le code de den sur laquelle retomber.
  - **Réponse : Choix retenu**
- **B12.** Un template peut prendre comme image de départ un autre template du même den-template.yaml ; si cet autre n'est pas construit, den build le construit d'abord, automatiquement. *(choix du rédacteur)*
  - Conséquence : Un seul den build peut enchaîner deux builds et durer deux fois plus longtemps ; den annonce chaque build avant de le lancer.
  - Sinon : den refuse le build tant que l'autre template n'est pas construit, et donne la commande.
  - Réponse à la note : Vous avez retenu la construction automatique : c'est maintenant le choix de la ligne.
  - **Réponse : Choix retenu**
- **B13.** Dans un dépôt distant, den-template.yaml ne peut citer que des fichiers situés dans ce dépôt. *(choix du rédacteur)*
  - Conséquence : Un dépôt distant ne peut pas faire lire à den un fichier de votre machine.
  - Sinon : Un dépôt distant peut aussi citer des chemins absolus de la machine.
  - **Réponse : Choix retenu**
- **B26.** Dans un dossier local aussi, den-template.yaml ne peut citer que des fichiers situés dans ce dossier. *(choix du rédacteur)*
  - Conséquence : À la conversion, les kits du template pillow, aujourd'hui sous ~/.den/kits/, sont déplacés dans le dossier du template.
  - Sinon : Dans un dossier local, den-template.yaml peut citer n'importe quel chemin de la machine.
  - Réponse à la note : Vous avez retenu la même règle stricte que pour un dépôt distant : c'est maintenant le choix de la ligne.
  - **Réponse : Choix retenu**

### Mise à jour

- **B14.** Pour un template nommé digitaleo, la commande de mise à jour est « den build digitaleo --pull » : elle récupère les nouveaux commits puis reconstruit ; sans --pull, den reconstruit depuis les commits déjà récupérés. *(choix du rédacteur)*
  - Sinon : « den build digitaleo » récupère toujours la dernière version.
  - **Réponse : Choix retenu**
- **B15.** den garde sous ~/.den/ une copie du dépôt par template construit, arrêtée au commit construit ; mettre à jour un template ne change ni les kits ni l'allowlist d'un autre template du même dépôt. *(choix du rédacteur)*
  - Conséquence : Kits, allowlist et image d'un template changent toujours au même moment.
  - Sinon : Une seule copie par dépôt, partagée par ses templates : une mise à jour les déplace tous.
  - **Réponse : Choix retenu**
- **B16.** Un build qui échoue laisse intacts la copie du dépôt, le template sbx et les instances de la version précédente. *(choix du rédacteur)*
  - Sinon : Un build qui échoue laisse la copie du dépôt au nouveau commit, sans template sbx correspondant.
  - **Réponse : Choix retenu**
- **B17.** La première fois qu'un den.yaml de projet fait récupérer une adresse git inconnue de la machine, den affiche l'adresse et demande confirmation. *(choix du rédacteur)*
  - Conséquence : Cloner un dev home ne suffit pas à faire exécuter des scripts venus d'une adresse arbitraire dans une VM ; sans terminal interactif, il faut l'option --yes.
  - Sinon : Aucune confirmation : den récupère l'adresse écrite dans le fichier.
  - **Réponse : Choix retenu**
- **B18.** Si le den.yaml d'un projet et celui de la machine déclarent un template du même nom avec deux lieux différents, un seul peut être construit à la fois : den build refuse le second et propose --force pour écraser le template sbx du premier. *(choix du rédacteur)*
  - Sinon : den distingue les deux par un suffixe dans le nom du template sbx et les laisse coexister.
  - **Réponse : Choix retenu**

### den up

- **B19.** den up ne récupère rien et ne construit rien : si le template n'est pas construit, il refuse et donne la commande den build. *(choix du rédacteur)*
  - Sinon : Au premier usage, den up récupère la définition et construit le template lui-même.
  - **Réponse : Choix retenu**
- **B20.** À chaque den up sur un template venu d'un dépôt distant, den interroge ce dépôt pendant 3 secondes au plus ; hors ligne ou en cas d'erreur, il ne dit rien et continue. *(choix du rédacteur)*
  - Sinon : Interroger au plus une fois par jour et garder le résultat.
  - **Réponse : Choix retenu**
- **B21.** Pour un lieu en dossier local, den up signale que la définition a changé depuis le dernier build et donne la commande. *(choix du rédacteur)*
  - Sinon : Aucun avis pour un dossier local.
  - **Réponse : Choix retenu**
- **B22.** den ne vérifie pas si l'image officielle désignée par un tag a changé depuis le build ; ce contrôle est laissé à un lot ultérieur, sans date. *(choix du rédacteur)*
  - Sinon : den up vérifie aussi l'image officielle et signale une version plus récente.
  - **Réponse : Choix retenu**
- **B23.** Les avis de version n'apparaissent qu'avec den up, jamais avec den exec ni den shell. *(choix du rédacteur)*
  - Conséquence : La sortie de den exec reste celle de la commande lancée.
  - Sinon : Les afficher aussi avec den exec et den shell.
  - **Réponse : Choix retenu**
- **B24.** Une instance sur un template construit garde l'image officielle enregistrée au build, même si le tag a bougé depuis. *(choix du rédacteur)*
  - Sinon : Prendre l'image que le tag désigne au moment de den up.
  - **Réponse : Choix retenu**

### Périmètre

- **B25.** Hors de ce lot : faire correspondre le fichier repos.local.json d'un dev home (ses chemins de dépôts) aux montages de den. *(choix du rédacteur)*
  - **Réponse : D'accord**
- **B27.** Hors de ce lot : une commande den stop. *(choix du rédacteur)*
  - **Réponse : D'accord**
- **B28.** Hors de ce lot : arrêter la VM quand le prompt lancé par den exec se termine. *(choix du rédacteur)*
  - **Réponse : D'accord**

## C — Ce que vous n'avez pas à valider

Faits lus dans le code de la branche v2 ou dans l'aide de sbx v0.53.0, et détails laissés au plan : découpage en paquets, format des fichiers d'état, textes d'erreur. Aujourd'hui den cherche une base dans le den.yaml du projet, puis dans celui de la machine, et nulle part ailleurs. sbx sait enregistrer et charger un template local, mais pas le télécharger depuis un serveur. Docker Hub renvoie sans authentification la liste des tags des images officielles de sbx (mesuré le 16 nov. 2026). Une ancienne fiche d'instance illisible bloque aujourd'hui toutes les suppressions par den rm. Hypothèses non vérifiées, à mesurer avant le plan : sbx crée une VM directement depuis une image officielle désignée par un tag, sans template construit. den peut lire l'empreinte exacte d'une image désignée par un tag sans passer par docker.
