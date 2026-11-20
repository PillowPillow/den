# Validation marketplace den

*den · marketplace Claude Code · 20 nov. 2026*

34 affirmations remplacent la relecture de la spec. Répondez aux exceptions ; le bouton « reste » de chaque section valide les autres lignes.

## Contexte

den reçoit un plugin Claude Code : des skills, lancés dans Claude Code sur le poste de l'utilisateur, qui vérifient les prérequis, écrivent le `den.yaml`, créent un profil et diagnostiquent les pannes. La fiche couvre le périmètre du plugin, ses garde-fous et le contenu du premier lot. Un « oui » engage l'écriture du plan d'implémentation sur cette base ; la spec est ensuite alignée sur la fiche.

## Glossaire

- **premier lot** : Les skills livrés en premier, avec la marketplace et ses tests.
- **second lot** : Les skills livrés ensuite : `den:profile` (scinder et entretenir un profil), `den:template`, `den:kit`, `den:team`.

Réponse vide = ligne non traitée. La fiche gagne sur le document qu'elle valide.

## A — Vos décisions

Je vérifie seulement que ma transcription est fidèle à ce que vous avez dit.

- **A1.** den livre une marketplace Claude Code dont les skills aident à lancer, configurer et personnaliser des nests, des kits et des profils. *(§ Intention)*
  - **Réponse : Fidèle**
- **A2.** Le skill de profil sert aussi à des kits de profil qui existent déjà, comme vos kits `pro` et `perso` de `~/.den/kits`, et pas seulement à en créer un depuis zéro. *(§ Les skills, den:profile)*
  - **Réponse : Fidèle**
- **A3.** Le premier utilisateur visé est un coéquipier qui installe den pour la première fois. *(§ Intention)*
  - **Réponse : Fidèle**
- **A4.** Le plugin vit dans le dépôt de den (`PillowPillow/den`), dans `plugins/den/`, et non dans un dépôt séparé. *(§ Approches)*
  - **Réponse : Fidèle**
- **A5.** La marketplace sert le plugin du dernier tag de release, jamais celui de `main`. *(§ Garde-fous contre la dérive)*
  - Conséquence : Le commit de release met aussi à jour le tag nommé dans `marketplace.json`, et le plugin n'est installable depuis GitHub qu'après la première release qui le contient.
  - **Réponse : Fidèle**

## B — Mes choix par défaut

Les vrais points à trancher. Chaque ligne donne le choix retenu, puis l'alternative.

### Périmètre

- **B1.** Tous les skills tournent dans Claude Code sur votre poste ; aucun n'est fait pour tourner dans une VM. *(§ Intention)*
  - Contexte, non validé : Une VM ne peut ni éditer `~/.den` ni lancer `den`.
  - **Réponse : D'accord**
- **B2.** Le plugin est générique : il ne livre aucun template, kit ou nest d'équipe. *(§ Non-objectifs)* — _Jamais discuté_
  - Conséquence : Le contenu d'équipe reste dans le dépôt ou la marketplace de l'équipe.
  - Sinon : Le plugin livre aussi le contenu d'une équipe (son template, ses kits, ses nests).
  - **Réponse : Choix retenu**
- **B3.** Le plugin contient des skills seulement : ni serveur MCP, ni hook, ni agent. *(§ Non-objectifs)* — _Jamais discuté_
  - Sinon : Ajouter au plugin un hook ou un agent dès le premier lot.
  - **Réponse : Choix retenu**
- **B4.** Aucun skill n'aide à migrer depuis den v1. *(§ Non-objectifs)* — _Jamais discuté_
  - Sinon : Ajouter un skill de migration depuis v1.
  - **Réponse : Choix retenu**
- **B5.** Une commande `den check`, qui valide un `den.yaml` sans créer de VM, fait l'objet d'une spec séparée, écrite après le premier lot. *(§ Non-objectifs)* — _Posé deux fois, sans réponse_
  - Conséquence : D'ici là, un skill valide un `den.yaml` en créant l'instance. Le coût porte surtout sur `den:nest`.
  - Sinon : `den check` est écrit et livré avant les skills, qui s'appuient dessus dès le départ.
  - **Réponse : Choix retenu**
- **B6.** Le premier lot contient quatre skills : `den:setup`, `den:profile` (créer un profil), `den:nest` et `den:troubleshoot`. *(§ Portée du plan et livraison)*
  - Conséquence : Scinder ou entretenir un profil, créer un template, écrire un kit et partager des nests arrivent au second lot.
  - Sinon : Les skills des deux lots sont livrés ensemble.
  - **Réponse : Choix retenu**
- **B27.** Le plan du second lot est écrit après les remarques du premier coéquipier qui aura installé den avec le plugin. *(§ Portée du plan et livraison)* — _Jamais discuté_
  - Sinon : Les plans des deux lots sont écrits maintenant.
  - **Réponse : Choix retenu**
- **B7.** Le premier lot sort avec la prochaine version mineure de den. *(§ Portée du plan et livraison)* — _Jamais discuté_
  - Sinon : Le premier lot sort dans une version de correction, sans attendre d'autre contenu.
  - **Réponse : Choix retenu**

### Tenir les skills alignés sur den

- **B8.** Les pages de `docs/` que les skills consultent (référence du `den.yaml`, commandes, templates, sécurité, SSH, fichiers) sont copiées octet pour octet dans le plugin, et un test échoue quand une copie diffère de l'original. *(§ Garde-fous contre la dérive)*
  - Conséquence : Après toute modification d'une de ces pages, une tâche `task plugin-sync` refait les copies avant le commit.
  - Sinon : Chaque skill porte son propre texte, résumé à partir de `docs/`, sans test de comparaison.
  - **Réponse : Choix retenu**
- **B9.** Les tableaux « prérequis » et « dépannage » quittent le README pour une page de `docs/`, afin que le test de copie les couvre. *(§ Disposition)* — _Jamais discuté_
  - Conséquence : Le README renvoie à cette page au lieu de porter les tableaux.
  - Sinon : Les deux tableaux restent dans le README ; les skills en portent leur propre version, sans test.
  - **Réponse : Choix retenu**
- **B10.** Le plugin porte le même numéro de version que den, et la commande de release monte les deux dans le même commit. *(§ Garde-fous contre la dérive)*
  - Sinon : Le plugin a son propre numéro de version, indépendant de celui de den.
  - **Réponse : Choix retenu**
- **B11.** Quand la version du binaire den et celle du plugin diffèrent (majeure ou mineure), le skill le signale, donne la commande qui met à jour le plus ancien des deux, et continue. *(§ Garde-fous contre la dérive)* — _Jamais discuté_
  - Sinon : Le skill s'arrête tant que les deux versions diffèrent.
  - **Réponse : Choix retenu**
- **B12.** Les fichiers `SKILL.md` sont écrits en anglais, comme tout message de den. *(§ Les skills)* — _Jamais discuté_
  - Sinon : Les fichiers `SKILL.md` sont écrits en français.
  - **Réponse : Choix retenu**

### Ce qu'un skill ne fait jamais seul

- **B13.** Aucun skill ne lit, n'affiche ni n'écrit le jeton Claude : il donne les commandes à lancer dans votre terminal, puis vérifie avec `sbx secret ls`. *(§ Anti-objectifs)*
  - **Réponse : D'accord**
- **B14.** Aucun skill ne lance `den up` : il crée et vérifie l'instance par une commande sans dialogue (par exemple `den exec demo -- true`), puis vous donne la commande `den up` à lancer. *(§ Règles communes aux skills)*
  - Contexte, non validé : `den up` attache Claude de façon interactive, ce qu'un skill ne peut pas piloter.
  - **Réponse : D'accord**
- **B15.** Toute écriture dans un `den.yaml` est une édition minimale, et le skill montre le diff avant d'écrire. *(§ Règles communes aux skills)*
  - Conséquence : Un `den.yaml` existant n'est jamais réécrit en entier ; ses commentaires restent.
  - **Réponse : D'accord**
- **B16.** Un skill n'ajoute une adresse à la liste réseau autorisée (`network.allow` ou `sbx policy`) que si vous l'avez nommée et avez vu la modification. *(§ Anti-objectifs)*
  - **Réponse : D'accord**
- **B28.** Un skill ne lance `den rm` qu'après votre confirmation, et jamais avec `--force`. *(§ Anti-objectifs)*
  - **Réponse : D'accord**
- **B17.** Quand la politique réseau globale de sbx manque, `den:setup` propose `sbx policy init balanced` et le lance après votre accord. *(§ Les skills, den:setup)* — _Jamais discuté_
  - Conséquence : Cette commande s'applique à toutes les VM du poste.
  - Sinon : `den:setup` vous donne la commande et ne la lance jamais lui-même.
  - **Réponse : Choix retenu**
- **B18.** `den:troubleshoot` établit la cause par une commande, propose la correction, et ne l'applique qu'après accord quand elle modifie un fichier ou supprime une instance. *(§ Les skills, den:troubleshoot)* — _Jamais discuté_
  - Sinon : `den:troubleshoot` ne fait que diagnostiquer et n'applique aucune correction.
  - **Réponse : Choix retenu**

### Premier usage et profil

- **B19.** Quand `den:setup` trouve un `den.yaml` de projet depuis le dossier courant, il vous le montre et vous propose de créer l'instance d'un de ses nests ; sinon il écrit un premier nest dans `~/.den/den.yaml`. *(§ Les skills, den:setup)* — _Jamais discuté_
  - Conséquence : Une équipe qui partage déjà des nests n'en reçoit pas une copie privée.
  - Sinon : `den:setup` écrit toujours un nest dans `~/.den/den.yaml`, sans regarder le projet.
  - **Réponse : Choix retenu**
- **B20.** `den:profile` crée un kit de profil (le dossier de kit qu'un profil nomme) depuis le `~/.claude` de votre poste : `CLAUDE.md`, skills, agents, styles de sortie et `settings.json`, chacun proposé avant copie. *(§ Les skills, den:profile)*
  - Conséquence : Les fichiers d'identifiants, d'état et d'historique de Claude ne sont jamais copiés.
  - **Réponse : D'accord**
- **B21.** Dans le `settings.json` copié, `den:profile` retire par défaut ce qui ne vaut que sur votre poste : les hooks, la ligne d'état et les dossiers autorisés qui nomment un chemin absolu, et la clé `sandbox`. *(§ Les skills, den:profile)* — _Jamais discuté_
  - Sinon : `den:profile` copie le `settings.json` entier et signale seulement les clés à risque.
  - **Réponse : Choix retenu**
- **B29.** Pour la clé `env` du `settings.json`, `den:profile` montre le nom des variables, jamais leur valeur, et ne garde que celles que vous choisissez. *(§ Les skills, den:profile)*
  - **Réponse : D'accord**
- **B22.** Les clés `enabledPlugins` et `extraKnownMarketplaces` ne sont gardées par défaut dans un profil que si un essai, fait pendant l'implémentation, montre que les plugins s'installent dans une VM. *(§ Les skills, den:profile)* — _Non vérifié_
  - Sinon : Ces deux clés sont toujours retirées par défaut, sans essai.
  - **Réponse : Choix retenu**
- **B23.** Au second lot, `den:profile` propose de partager un kit entre plusieurs profils, au lieu d'un kit complet par profil. *(§ Les skills, den:profile)* — _Jamais discuté_
  - Sinon : Chaque profil garde un seul kit complet, avec ses fichiers en double d'un profil à l'autre.
  - Contexte, non validé : Vos kits `pro` et `perso` livrent aujourd'hui les mêmes skills et le même style de sortie en double. Le kit partagé porterait ses réglages Claude dans `claude-settings.d`, que den fusionne sous le `settings.json` au démarrage de la VM ; seul le kit propre à un profil livrerait un `settings.json`. Ce montage à deux kits n'est pas vérifié : il est essayé dans une VM avant livraison.
  - **Réponse : Choix retenu**
- **B24.** Le skill qui aide à écrire un kit ne décrit que ce que den y ajoute ; pour la grammaire générale d'un kit, il renvoie à la documentation de sbx. *(§ Les skills, den:kit)* — _Jamais discuté_
  - Sinon : Le skill recopie aussi la grammaire générale des kits sbx.
  - **Réponse : Choix retenu**

### Validation et documentation

- **B25.** Chaque skill est exécuté une fois pour de vrai sur votre poste, avec un den home vide, et le résultat est consigné dans un fichier de validation du dépôt, distinct de cette page. *(§ Validation)* — _Jamais discuté_
  - Conséquence : Les cas « prérequis manquant » de `den:setup` restent marqués « non vérifié » jusqu'à la première installation par un coéquipier.
  - Sinon : Chaque skill est aussi validé sur un second poste ou un second compte avant la release.
  - **Réponse : Choix retenu**
- **B26.** Le README reçoit une courte section « Avec Claude Code », placée avant « First run » ; le chemin manuel reste. *(§ Documentation)* — _Jamais discuté_
  - Sinon : Le README mène d'abord au plugin, et le chemin manuel passe dans une page de `docs/`.
  - **Réponse : Choix retenu**

## C — Ce que vous n'avez pas à valider

Une marketplace est un fichier `.claude-plugin/marketplace.json` à la racine d'un dépôt. À l'installation, Claude Code ne copie que le dossier du plugin : un skill ne peut pas lire `docs/` du dépôt. Source : la documentation de Claude Code, lue par un sous-agent le 20 nov. 2026 ; à relire pendant l'implémentation avant de s'y fier. Un skill de plugin s'appelle `/den:&lt;nom&gt;` et vit dans `plugins/den/skills/&lt;nom&gt;/SKILL.md`. Aucune commande de den n'a d'option d'essai à blanc : un `den.yaml` ne se valide aujourd'hui que par une commande qui crée une VM (recherche dans `internal/cli`). Les commandes que la spec reprend du README (vérification de la politique réseau, stockage du jeton, template par défaut) correspondent au dépôt : vérifié par un sous-agent de relecture. Trois tests Go gardent le plugin : copies de `docs/`, numéro de version et tag épinglé, structure des fichiers. Leur emplacement et le nom de la tâche de copie sont choisis pendant l'implémentation. Suite hors spec : `.claude/commands/release.md` nomme encore `make` ; la commande réelle est `task check`.
