# Validation den V2

*den · réécriture V2 · périmètre · 12 nov. 2026 · sbx v0.53.0*

Ces affirmations remplacent la relecture de la spec. Répondez ligne par ligne ; vos réponses sont enregistrées au fil de l'eau et je les relis depuis la session. La spec sera alignée sur cette fiche, jamais l'inverse.

## Contexte

den v1 lance des sandbox sbx multi-repos, mais il réimplémente des fonctions que sbx offre désormais nativement et s'écarte de sbx à chaque version. La spec fixe le périmètre d'une réécriture, den V2, qui garde le même usage et ne fait plus que ce que sbx ne sait pas faire. den reste un outil autonome : il lit sa configuration dans un dossier projet ou sous ~/.den/, quel que soit le reste du contenu de ce dossier. Un « oui » fixe le contenu de la première version (V2.0) et l'ordre des lots suivants ; il engage à lancer des essais jetables sur sbx avant d'écrire le plan.

## Glossaire

- **dossier projet** : N'importe quel dossier qui contient un fichier de configuration den ; den y lit ses nests. Un dépôt partagé par une équipe peut en être un. den ne s'occupe pas du reste de son contenu.
- **nest** : Groupe nommé de repos, avec l'environnement de VM associé, défini dans un dossier projet ou globalement sous ~/.den/.
- **instance** : Dans cette fiche : une VM lancée à partir d'une nest, nommée d'après la nest, suivie du nom de la branche quand on lance avec -w (exemple : am.feature-123). En v1, le mot désignait seulement la seconde partie du nom.
- **dossier de run** : Dossier non versionné, propriété de den, propre à une instance : il contient le sbxenv.yaml généré de l'instance.
- **image de base** : Image de VM lourde et stable (PHP, Node, bibliothèques système), enregistrée comme template sbx ; c'est ce que den build produit déjà en v1.
- **kit** : Extension sbx qui ajoute un outil à une VM : installation, variables d'environnement, hôtes réseau autorisés, credentials.
- **profil** : Configuration Claude nommée (pro, perso) : settings, plugins, CLAUDE.md, skills.
- **essai jetable** : Test court sur une VM créée puis supprimée, qui répond oui ou non à une question technique avant d'écrire le plan.
- **am** : Projet personnel de l'auteur, avec sa propre VM et ses outils ; une nest de den v1 aujourd'hui.
- **dg** : Environnement Digitaleo : VM aux outils de l'entreprise, sur un groupe de repos Digitaleo ; aujourd'hui une source d'équipe de den v1.
- **configuration den** : Fichier YAML de den qui déclare les nests (repos, image de base, kits, réseau, profil, ports) : dans un dossier projet, ou sous ~/.den/ pour les nests globales.
- **bascule** : Moment où l'auteur arrête d'utiliser den v1 et passe ses VM sur V2.0.
- **lot** : Groupe de fonctions livrées ensemble après la première version, dans l'ordre de la roadmap.

Réponse vide = ligne non traitée. La fiche gagne sur le document qu'elle valide.

## A — Vos décisions

Je vérifie seulement que ma transcription est fidèle à ce que vous avez dit.

### Positionnement

- **A1.** den V2 est une réécriture, développée sur une branche dédiée de ce dépôt, et garde le nom den ; v1 reste sur main jusqu'à la bascule. *(§1 Intention)*
  - **Réponse : Fidèle**
- **A2.** den V2 configure et pilote sbx : il peut offrir des commandes courtes comme up ou shell, mais dès que sbx fait une chose nativement, den la lui délègue. *(§1 Intention)*
  - **Réponse : Fidèle**

### Usage

- **A3.** den V2 garde les commandes de v1 : den up avec un nom de nest, den up avec -w et un nom de branche, den shell, den ls, den exec. *(§5 Usage de la première version)*
  - **Réponse : Fidèle**
- **A4.** Tout ce qui se fait en interactif se fait aussi par script avec den exec, qui crée la VM si besoin, exécute la commande, renvoie son code de sortie et ne pose aucune question. *(§5 Usage de la première version)*
  - **Réponse : Fidèle**
- **A5.** La première version est utilisable quand elle remplace ce qui tourne aujourd'hui : une VM pour am, une VM pour dg, et des worktrees créés dans les repos choisis pour travailler dedans. *(§5 Usage de la première version)*
  - **Réponse : Fidèle**

### Configuration

- **A9.** L'emplacement des worktrees reste un réglage, comme en v1 : un dossier central commun à tous les repos, ou à côté de chaque repo. *(§10 Worktrees et chemins)*
  - **Réponse : Fidèle**
- **A10.** Pour une nest globale, le sbxenv.yaml généré reste caché sous ~/.den/. *(§6.1 Génération de sbxenv.yaml)*
  - **Réponse : Fidèle**
- **A11.** Les sources d'équipe de den v1 disparaissent : un dossier projet versionné, partagé par git, les remplace. *(§2 Non-objectifs)*
  - **Réponse : Fidèle**
- **A12.** La configuration de la nest désigne le dossier où l'agent démarre dans la VM, par exemple la racine du dossier projet ou un repo ; den le transmet à sbx. *(§5 Usage de la première version)*
  - **Réponse : Fidèle**

### Environnement

- **A14.** L'environnement d'une VM se compose de trois couches : l'image de base, rarement reconstruite ; des kits, un par outil, ajoutés souvent ; la configuration den, qui choisit base, kits, repos, réseau, profil et ports. *(§7 Trois couches)*
  - **Réponse : Fidèle**
- **A15.** La première version garde une commande den build qui construit l'image de base, et inclut la conversion des stacks am et dg actuelles. *(§5 Usage de la première version)*
  - **Réponse : Fidèle**

### Profils

- **A16.** Une VM utilise un profil Claude nommé et réutilisable ; pro et perso n'ont jamais le même dossier .claude. *(§9 Profils)*
  - **Réponse : Fidèle**
- **A17.** Dans une VM, Claude tourne toujours en bypassPermissions. *(§9 Profils)*
  - **Réponse : Fidèle**
- **A18.** Partager l'historique et les sessions de Claude entre VM est une option voulue plus tard, pas dans la première version. *(§11 Roadmap)*
  - **Réponse : Fidèle**

### Plus tard

- **A19.** Une commande den autorise ou bloque un domaine sur la VM en cours ; avec --save, elle l'écrit aussi dans la configuration den du dossier courant pour les VM suivantes. *(§8 Réseau)*
  - **Réponse : Fidèle**
- **A20.** Un cron ou un événement lance une commande den qui fait travailler un agent autonome dans une VM. *(§11 Roadmap)*
  - **Réponse : Fidèle**
- **A21.** Chaque job autonome choisit son espace de travail : un clone fait dans la VM (option --clone de sbx), ou un worktree d'un repo de l'hôte. *(§11 Roadmap)*
  - **Réponse : Fidèle**
- **A22.** À terme, den lance lui-même les jobs selon son propre planning, mais cela vient après la première version. *(§11 Roadmap)*
  - **Réponse : Fidèle**

### Configuration

- **A23.** Pour une nest d'un dossier projet, le sbxenv.yaml généré est visible dans un dossier de run non versionné de ce dossier projet, et on peut relancer la VM avec sbx seul, sans den. *(§6.1 Génération de sbxenv.yaml)*
  - **Réponse : Fidèle**
- **A24.** Pour réutiliser des outils entre plusieurs dossiers projet, on partage des kits : sbx sait les charger depuis un dépôt git ou un registre d'images OCI. *(§2 Non-objectifs)*
  - **Réponse : Fidèle**

### Plus tard

- **A25.** Le prompt d'un agent autonome fixe ce qu'il a le droit de faire ; exemple : s'il voit une CVE, il code le correctif, ouvre une PR et vous envoie un message pour la relire. *(§11 Roadmap)*
  - **Réponse : Fidèle**

## B — Mes choix par défaut

Les vrais points à trancher. Chaque ligne donne le choix retenu, puis l'alternative.

### Configuration

- **B1.** La façon de monter les worktrees dans la VM n'est pas fixée : un essai jetable vérifie d'abord que git fonctionne quand un worktree est monté au chemin du clone principal, et le choix suit son résultat. *(§10 Worktrees et chemins)*
  - Conséquence : Si l'essai réussit, un chemin écrit vers le clone principal mène au worktree dans la VM ; s'il échoue, den monte les worktrees à leur propre chemin, comme v1.
  - Sinon : Fixer tout de suite le montage des worktrees à leur propre chemin, comme v1, sans essai.
  - Réponse à la note : Vous demandiez l'essai avant toute affirmation : la ligne ne promet plus rien, elle fait dépendre le choix de l'essai.
  - Contexte, non validé : Problème : le fichier .git d'un worktree pointe vers le dossier .git du clone principal, que ce montage masque. Correctif proposé, à prouver par un essai jetable : den monte ce dossier .git ailleurs dans la VM et pose par-dessus un petit fichier .git généré qui pointe vers lui. Si l'essai échoue, l'alternative s'applique.
  - **Réponse : Choix retenu** — spike test avant d'affirmer ca
- **B3.** den tient sous ~/.den/ une liste locale des instances de la machine, avec le dossier projet de chacune, ce qui permet à den ls de les retrouver toutes. *(§12 Arbitrages)* — _Jamais discuté_
  - Sinon : Pas de liste locale : den ls lit seulement sbx ls.
  - **Réponse : Choix retenu**
- **B4.** den rm supprime la VM, le dossier de run de l'instance et ses worktrees, définitivement. *(§5 Usage de la première version)* — _Jamais discuté_
  - Sinon : den rm déplace les worktrees dans une corbeille récupérable, comme v1.
  - **Réponse : Choix retenu**

### Profils

- **B5.** Un profil est un fichier de settings Claude, avec un CLAUDE.md et des skills en option ; den le passe à Claude au lancement avec --settings, ce qui l'emporte sur le fichier de settings utilisateur que le kit claude de sbx réécrit à chaque création. *(§12 Arbitrages)* — _Jamais discuté_
  - Sinon : Un profil est un dossier .claude complet, monté comme dossier de configuration de Claude, comme v1 ; den doit alors y écrire lui-même bypassPermissions et les réglages de premier lancement.
  - **Réponse : Choix retenu**
- **B6.** den vise le kit claude de sbx. Un essai jetable examine toutes les pistes avant de l'écarter : votre jeton OAuth d'abonnement, une clé d'API, un petit kit qui corrige l'authentification, un kit claude personnalisé déclaré comme raccourci dans les réglages de sbx. *(§9 Profils)*
  - Conséquence : den ne revient à l'agent shell que si toutes ces pistes échouent, et l'essai dit pourquoi.
  - Sinon : Ne rien tester : les VM gardent l'agent shell de sbx avec Claude installé par den, comme aujourd'hui.
  - Réponse à la note : Vous demandiez une vérification en profondeur : l'essai couvre maintenant quatre pistes avant tout abandon du kit claude.
  - **Réponse : Choix retenu** — verifier en profondeur avant d'abandonner.
- **B7.** Les VM utilisent le relais de l'agent SSH de l'hôte, actif par défaut dans sbx : la VM signe avec vos clés sans pouvoir les lire ; den ne monte plus de dossier de clés privées. *(§9 Profils)* — _Jamais discuté_
  - Sinon : Garder en option le montage d'un dossier de clés SSH dans la VM, comme v1.
  - **Réponse : Choix retenu**

### Environnement

- **B8.** Les hôtes réseau autorisés pour une nest sont déclarés dans un petit kit généré par den, que sbx applique à la création : rien ne s'exécute sur l'hôte, donc rien n'attend votre approbation et den exec reste non interactif. *(§12 Arbitrages)* — _Jamais discuté_
  - Sinon : den lance des commandes sbx policy sur l'hôte après la création ; sbx demande d'approuver ces commandes à chaque fois, sauf si on active un réglage qui les approuve une fois pour toutes.
  - **Réponse : Choix retenu**
- **B9.** Dans la première version, une nest liste ses kits et ils sont posés à la création de la VM ; ajouter un kit à une VM qui tourne vient dans le lot suivant. *(§12 Arbitrages)*
  - Sinon : Ajouter un kit à une VM qui tourne, et le sauvegarder dans la configuration, dès la première version.
  - **Réponse : Choix retenu**
- **B10.** Les ports publiés d'une nest font partie de la première version, parce que am en publie cinq aujourd'hui. *(§5 Usage de la première version)* — _Jamais discuté_
  - Sinon : Les ports attendent un lot ultérieur ; on les publie avec sbx ports en attendant.
  - **Réponse : Choix retenu**

### Bascule

- **B11.** À la bascule, les VM créées par v1 sont supprimées ou recréées ; den V2 ne lit pas les fichiers d'état de v1. *(§11 Roadmap)* — _Jamais discuté_
  - Sinon : den V2 reprend les VM de v1 et lit leurs fichiers d'état pour les piloter.
  - **Réponse : Choix retenu**
- **B12.** den V2 exige sbx v0.53.0 ou plus récent. *(§3 Contraintes)* — _Jamais discuté_
  - Conséquence : v0.53.0 est la version installée sur votre machine ; viser v0.50.0 obligerait den à éviter tout ce que sbx a ajouté depuis.
  - Sinon : den V2 accepte sbx à partir de v0.50.0, la dernière version sondée en détail.
  - **Réponse : Choix retenu**

### Méthode

- **B13.** Les essais jetables passent avant le plan, et le plan ne décrit que les solutions retenues ; je les lance sur des VM créées puis supprimées, après votre accord. *(§12 Arbitrages)*
  - Sinon : Écrire le plan tout de suite avec les deux branches de chaque question ouverte.
  - **Réponse : Choix retenu**
- **B14.** Après la première version, l'ordre des lots est : changements à chaud sauvegardés (réseau, kits), puis agents autonomes, puis credentials par VM, puis historique partagé, puis den comme planificateur. *(§11 Roadmap)*
  - Sinon : Les agents autonomes passent en premier, avant les changements à chaud sauvegardés.
  - **Réponse : Choix retenu**

### Usage

- **B15.** den exec remplace den run de v1 : une seule commande crée la VM si besoin puis exécute la commande. *(§5 Usage de la première version)* — _Jamais discuté_
  - Sinon : Garder deux commandes comme en v1 : den run crée puis exécute, den exec exécute seulement dans une VM qui tourne.
  - **Réponse : Choix retenu**

### Configuration

- **B16.** Un nom de nest est unique sur la machine : den refuse un second dossier projet qui déclare une nest au nom déjà pris, et nomme le fichier en cause. *(§12 Arbitrages)* — _Jamais discuté_
  - Sinon : Deux dossiers projet peuvent avoir une nest du même nom ; le nom de chaque instance porte alors le nom de son dossier projet.
  - **Réponse : Choix retenu**
- **B17.** Par défaut, les worktrees sont rangés dans un dossier central sous ~/.den/, comme en v1, hors de tout dossier projet : une VM qui monte un dossier projet ne voit pas les worktrees des autres tâches. *(§10 Worktrees et chemins)* — _Jamais discuté_
  - Conséquence : Le réglage permet toujours de choisir l'autre emplacement, nest par nest.
  - Sinon : Par défaut, les worktrees sont rangés dans le dossier de run de chaque instance, dans le dossier projet : une suppression tient en un dossier, mais chaque VM qui monte le dossier projet voit et peut modifier les worktrees des autres tâches.
  - **Réponse : Choix retenu**

## C — Ce que vous n'avez pas à valider

Faits relevés sur sbx v0.53.0 (aide des commandes, chaînes du binaire, réglages) et dans la documentation Claude Code. Si un de ces points contredit A ou B, c'est A ou B qui gagne. sbxenv.yaml fusionne plusieurs fichiers dans l'ordre, comme docker compose -f ; il n'a aucun champ pour les règles réseau. Un dossier hôte est monté au même chemin absolu dans la VM, sauf si l'entrée de workspace précise un autre chemin cible. sbx kit add ajoute un kit à une VM existante en recréant son conteneur ; les volumes du kit et le workspace sont conservés. sbx template save fait un instantané d'une VM réutilisable comme image ; sbx ne construit pas encore en local une image décrite par un Dockerfile. Le kit claude de sbx lance claude avec --dangerously-skip-permissions et réécrit ~/.claude/settings.json avec bypassPermissions à chaque création. Claude Code n'accepte bypassPermissions que depuis les settings utilisateur, les settings managés ou un flag ; un fichier de projet ne peut pas l'activer. Précédence des settings Claude Code, du plus fort au plus faible : managé, --settings, projet local, projet, utilisateur. sbx relaie l'agent SSH de l'hôte par défaut : la VM signe sans lire les clés. Le mode fournisseur (clé d'API ou OAuth) n'a pas de flag : il découle du secret enregistré avec sbx secret set. sbx run -w fixe le dossier où démarre la session de l'agent. Non vérifié, à prouver par essai jetable : depuis sbx v0.51, le kit claude casserait l'authentification par jeton OAuth (constat du dépôt brain) ; sbx policy ls affiche une politique locale nommée default-deny-all alors qu'un refus total local était impossible sur v0.50.
