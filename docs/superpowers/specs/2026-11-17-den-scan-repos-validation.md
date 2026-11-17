# Validation den scan

*den V2 · clés de repo et den scan · 17 nov. 2026*

Ces affirmations remplacent la relecture de la spec. Répondez seulement aux exceptions ; vos réponses sont enregistrées au fil de l'eau et je les relis depuis la session. La spec sera alignée sur cette fiche, jamais l'inverse.

## Contexte

Aujourd'hui, chaque repo qu'un nest monte est déclaré à la main dans le den.yaml global, sous une clé, avec son chemin absolu sur le poste. La spec fait déclarer chaque clé avec l'URL git du repo dans le den.yaml du projet, et ajoute une commande, den scan, qui cherche les clones sur le disque et retient leur chemin. Un « oui » fixe ce que le plan d'implémentation construit ; un « non » rouvre le point avant le code.

## Glossaire

- **identité** : La forme normalisée de l'URL git d'un repo, hôte/chemin (gitlab.com/dig/api) ; deux URL du même repo donnent la même identité.
- **clone** : Un dossier qui contient un dossier .git ; un worktree, dont .git est un fichier, n'en est pas un.
- **table de den scan** : Le fichier ~/.den/state/v2/repos.yaml, écrit seulement par den : pour chaque identité, le chemin du clone sur le poste.
- **carte des repos** : Le fichier que den écrit dans chaque VM à sa création, avec trois listes : les repos montés, les repos du nest laissés de côté, et les autres repos du poste ; l'agent le lit, les scripts aussi via DEN_REPOS.
- **remplacement** : Une ligne de la table de den scan dont le chemin change, parce que l'ancien dossier n'existe plus ou n'a plus de remote de cette identité, et qu'un autre clone la porte.
- **ligne périmée** : Une ligne de la table de den scan dont le dossier n'existe plus ou n'a plus de remote de cette identité, et qu'aucun autre clone ne remplace.
- **alias SSH** : Un nom d'hôte inventé dans ~/.ssh/config (Host gitlab-perso) et utilisé dans l'URL d'un clone à la place du vrai hôte.

Réponse vide = ligne non traitée. La fiche gagne sur le document qu'elle valide.

## A — Vos décisions

Je vérifie seulement que ma transcription est fidèle à ce que vous avez dit.

### Déclarer un repo

- **A1.** Dans un den.yaml de projet, chaque clé de repo porte soit une URL git, soit un chemin, jamais les deux. *(§2 Décisions de l'utilisateur)*
  - **Réponse : Fidèle**
- **A2.** Une clé déclarée avec un chemin monte ce dossier tel quel, sans passer par la table de den scan ; un chemin relatif reste résolu depuis le dossier du den.yaml. *(§2 Décisions de l'utilisateur)*
  - **Réponse : Fidèle**
- **A3.** Le den.yaml global déclare des clés avec un chemin absolu et, en option, une URL git. *(§2 Décisions de l'utilisateur)*
  - **Réponse : Fidèle**
- **A4.** La clé reste le nom du repo dans la VM ; il n'y a ni champ name, ni nom tiré de l'URL git. *(§2 Décisions de l'utilisateur)*
  - Conséquence : Deux repos dont l'URL finit par api cohabitent dans un nest sous deux clés différentes.
  - **Réponse : Fidèle**
- **A5.** Une même clé déclarée par le den.yaml de projet et le den.yaml global avec deux URL git d'identités différentes, ou deux chemins différents, fait échouer la commande. *(§2 Décisions de l'utilisateur)*
  - **Réponse : Fidèle**

### den scan

- **A6.** La commande s'appelle den scan ; elle cherche les clones sous un dossier donné, ou sous le dossier courant, jusqu'à 20 niveaux de profondeur. *(§2 Décisions de l'utilisateur)*
  - **Réponse : Fidèle**
- **A7.** den scan écrit dans l'état de den (~/.den/state/v2/repos.yaml) et ne réécrit jamais votre den.yaml. *(§2 Décisions de l'utilisateur)*
  - **Réponse : Fidèle**
- **A8.** Deux clones du même repo donnent un avertissement, et un seul est retenu si le repo n'a pas encore de chemin ; vous corrigez à la main si besoin. *(§2 Décisions de l'utilisateur)*
  - **Réponse : Fidèle**
- **A9.** den scan écrit après confirmation : vous voyez les lignes avant l'écriture. *(§2 Décisions de l'utilisateur)*
  - **Réponse : Fidèle**

### Limites

- **A10.** Les alias SSH (git@gitlab-perso:…) et les réécritures insteadOf ne sont pas supportés pour le moment. *(§2 Décisions de l'utilisateur)*
  - **Réponse : Fidèle**
- **A11.** La carte des repos écrite dans chaque VM garde son format et continue de lister les repos montés. *(§6.8 Carte des repos)*
  - **Réponse : Fidèle**

## B — Mes choix par défaut

Les vrais points à trancher. Chaque ligne donne le choix retenu, puis l'alternative.

### Reconnaître un repo

- **B1.** Deux URL désignent le même repo si elles ont le même hôte et le même chemin, quel que soit le protocole (SSH ou HTTPS), le port, l'identifiant, le suffixe .git ou la casse. *(§6.1 Identité d'une origine)* — _Jamais discuté_
  - Sinon : Garder le port dans l'identité : un clone SSH sur le port 2222 et un clone HTTPS du même GitLab ne se reconnaissent plus.
  - **Réponse : Choix retenu**
- **B2.** den scan lit lui-même le fichier .git/config de chaque clone au lieu d'appeler git. *(§6.5 den scan)*
  - Conséquence : Une VM qui modifie la config git d'un repo monté ne peut pas faire exécuter quoi que ce soit à den scan.
  - Sinon : Appeler git remote get-url, qui applique les réécritures insteadOf mais lit aussi une config qu'une VM a pu modifier.
  - **Réponse : Choix retenu**
- **B3.** Tous les remotes d'un clone comptent, pas seulement origin : un fork dont upstream pointe vers le repo d'équipe répond aux deux URL. *(§6.5 den scan)* — _Jamais discuté_
  - Sinon : Seul le remote nommé origin compte ; un fork ne répond qu'à l'URL du fork.
  - **Réponse : Choix retenu**
- **B4.** Quand deux clones portent le même repo, den scan retient d'abord celui dont le remote nommé origin porte cette identité, puis le premier chemin par ordre alphabétique. *(§6.5 den scan)*
  - Sinon : Seul l'ordre alphabétique des chemins départage : le repo d'équipe peut aller au fork.
  - **Réponse : Choix retenu**

### Ce que den scan enregistre

- **B5.** den scan enregistre tous les clones qu'il trouve, qu'une clé les déclare ou non. *(§6.5 den scan)* — _Jamais discuté_
  - Conséquence : La carte de chaque VM continue de lister les repos du poste qu'aucun nest ne monte, comme aujourd'hui.
  - Sinon : N'enregistrer que les repos déclarés par une clé : la VM ne voit plus vos autres repos dans la carte.
  - **Réponse : Choix retenu**
- **B6.** La table de den scan ne stocke que l'identité et le chemin, jamais l'URL brute. *(§6.4 Table d'état)*
  - Conséquence : Un jeton présent dans une URL HTTPS n'atterrit jamais dans un fichier de den ni dans la VM.
  - **Réponse : D'accord**
- **B7.** den scan ignore le dossier ~/.den, ~/Library, node_modules, vendor, les dossiers dont le nom commence par un point et les liens symboliques ; il ne descend pas dans un clone. *(§6.5 den scan)*
  - Sinon : Tout parcourir : plus lent, et un faux clone planté dans un repo monté serait trouvé.
  - **Réponse : Choix retenu**
- **B8.** Une ligne de la table de den scan dont le dossier a disparu fait l'objet d'un remplacement si den scan trouve le repo ailleurs ; sinon elle devient une ligne périmée, signalée et conservée. *(§6.5 den scan)* — _Jamais discuté_
  - Sinon : Ajouter une option --prune qui supprime les lignes périmées dès cette livraison.
  - **Réponse : Choix retenu**

### Confirmation

- **B9.** L'écran de confirmation montre d'abord les remplacements, les doublons et les repos déclarés par une clé ; les autres ajouts tiennent en un compte. *(§6.5 den scan)*
  - Sinon : Lister chaque ligne, au risque d'une liste de 40 lignes validée sans être lue.
  - **Réponse : Choix retenu**
- **B10.** Avec --yes, den scan écrit les ajouts sans question, mais un remplacement demande toujours une confirmation. *(§6.5 den scan)*
  - Conséquence : Une VM qui modifie l'URL d'un repo ne peut pas déplacer en silence le dossier qu'un autre nest monte.
  - Sinon : --yes écrit aussi les remplacements sans question.
  - **Réponse : Choix retenu**
- **B11.** Quand stdin n'est pas un terminal et que --yes manque, den scan refuse et nomme --yes. *(§6.5 den scan)*
  - **Réponse : D'accord**
- **B23.** Quand stdin n'est pas un terminal, den scan avec --yes liste chaque remplacement comme non écrit et garde l'ancienne ligne. *(§6.5 den scan)*
  - Sinon : Écrire aussi les remplacements quand stdin n'est pas un terminal.
  - **Réponse : Choix retenu**

### den up

- **B12.** Pour une clé, den up prend le chemin du den.yaml global s'il existe, sinon le chemin du den.yaml de projet, sinon le chemin que la table de den scan donne pour son URL git. *(§6.3 Résolution d'une clé)*
  - Conséquence : Un chemin écrit dans le den.yaml global corrige un doublon mal choisi par den scan.
  - **Réponse : D'accord**
- **B22.** Quand le den.yaml de projet donne une URL git à une clé et que le den.yaml global donne à la même clé un chemin sans URL, den up refuse si aucun remote de ce dossier n'a la même identité, en nommant les deux fichiers. *(§6.3 Résolution d'une clé)* — _Jamais discuté_
  - Conséquence : Pour un alias SSH, vous écrivez dans le den.yaml global le chemin et l'URL du repo ; den ne vérifie alors pas le dossier.
  - Sinon : Le chemin du den.yaml global gagne sans vérification : un projet d'équipe qui réutilise un nom de clé que vous employez pour un autre repo monte votre repo sans erreur.
  - **Réponse : Choix retenu**
- **B13.** Quand plusieurs clés d'un nest n'ont pas de chemin, den up les liste toutes en une fois, chacune avec sa commande git clone, puis nomme den scan pour celles déjà clonées. *(§6.3 Résolution d'une clé)*
  - Sinon : Refuser à la première clé manquante.
  - **Réponse : Choix retenu**
- **B14.** Les 36 lignes clé: /chemin de votre den.yaml global actuel restent valides sans modification. *(§6.2 Schéma)*
  - Conséquence : Le den.yaml du dev home garde ses clés ; le rendre portable revient à y ajouter l'URL git de chaque clé.
  - Sinon : Imposer la nouvelle forme {path: …} et migrer le fichier.
  - **Réponse : Choix retenu**
- **B15.** Une clé doit être un nom de dossier valide : pas de /, ni . ou .., pas de point initial. *(§6.2 Schéma)*
  - **Réponse : D'accord**

### Carte des repos et worktrees

- **B16.** Le dossier d'un worktree créé par -w garde le nom du dossier du clone, pas la clé. *(§6.3 Résolution d'une clé)*
  - Conséquence : Une instance existante dont la clé diffère de son dossier (go-dgdev pour le dossier go.dgdev) se recrée sans erreur.
  - Sinon : Nommer le dossier du worktree d'après la clé.
  - **Réponse : Choix retenu**
- **B24.** Avec -w, den up refuse deux repos d'un même nest dont les dossiers portent le même nom, puisque leurs worktrees iraient au même endroit. *(§6.3 Résolution d'une clé)*
  - Sinon : Ajouter le nom de la clé au dossier du worktree pour ce seul cas.
  - **Réponse : Choix retenu**
- **B17.** Dans la carte des repos, un repo monté prend le nom de sa clé : go-dgdev apparaît sous go-dgdev, plus sous go.dgdev. *(§6.8 Carte des repos)*
  - Conséquence : Le format JSON, les fichiers et le texte de la carte ne changent pas ; une configuration dont chaque clé a le nom de son dossier produit une carte identique à aujourd'hui.
  - Sinon : Garder le nom du dossier dans la carte, différent du nom utilisé par start: et --only.
  - **Réponse : Choix retenu**
- **B18.** La liste « autres repos du poste » de la carte ajoute les clones trouvés par den scan, nommés par leur dossier, liste chaque chemin une fois et omet les chemins qui n'existent plus. *(§6.3 Résolution d'une clé)*
  - **Réponse : D'accord**

### Rapport de den scan

- **B19.** À la fin, den scan liste les clés déclarées avec une URL git qui n'ont toujours pas de chemin, dans le den.yaml de projet du dossier courant, dans ceux depuis lesquels vous avez déjà lancé den, et dans le den.yaml global. *(§6.5 den scan)*
  - **Réponse : D'accord**
- **B20.** Lancé depuis l'intérieur d'un clone, den scan le signale et propose le dossier parent. *(§6.5 den scan)*
  - **Réponse : D'accord**
- **B21.** den scan n'imprime pas de bloc repos: prêt à coller pour les clones qu'aucune clé ne déclare. *(§3 Non-buts)* — _Jamais discuté_
  - Sinon : Imprimer ce bloc, avec le nom du dossier comme clé proposée.
  - **Réponse : Choix retenu**

## C — Ce que vous n'avez pas à valider

Faits vérifiés dans le code ; le plan et les revues s'en chargent. Si un de ces points contredit A ou B, c'est A ou B qui gagne. DEN_REPOS n'entre pas dans l'empreinte de configuration d'une instance : seuls les chemins des repos résolus y entrent. L'index des nests (~/.den/state/v2/nests.yaml) n'enregistre un den.yaml de projet que lorsqu'une commande den tourne depuis ce projet. Au recreate d'une instance, den recalcule le chemin de chaque worktree et le compare à celui enregistré ; un chemin différent refuse. Les instances existantes (den rm, attache) lisent les chemins enregistrés, pas les clés. Le plan compte 11 tâches, chacune avec ses tests et task check ; aucune ne lance de processus autre que git, et den scan n'en lance aucun.
