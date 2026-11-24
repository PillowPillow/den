# Validation resources den

*den V2 · issue #90 · 24 nov. 2026*

Ces affirmations remplacent la relecture du design court. Répondez seulement aux exceptions ; le design sera aligné sur cette fiche, jamais l'inverse.

## Contexte

Aujourd'hui chaque VM créée par den reçoit la taille par défaut de sbx : tous les CPU, et 50 % de la mémoire de l'hôte, ramenés entre 512 MiB et 32 GiB. Le changement ajoute une clé `resources:` (`cpus`, `memory`) au den.yaml, que den écrit dans le sbxenv.yaml de l'instance. La section A reprend les deux choix que vous avez faits pendant la session : vous vérifiez ma transcription. La section B liste les choix que j'ai faits seul : vous les tranchez. Un « oui » partout fixe le comportement que je code ensuite.

## Glossaire

- **avertissement de dérive** : Ligne `warning: … changed since … was created — run den rm … then den up …` que `den up` affiche quand la configuration d'une instance Live a changé depuis sa création.

Réponse vide = ligne non traitée. La fiche gagne sur le document qu'elle valide.

## A — Vos décisions

Je vérifie seulement que ma transcription est fidèle à ce que vous avez choisi.

- **A1.** `resources:` se déclare à la racine du den.yaml global (hors de `nests:`) et dans une nest, quel que soit le den.yaml qui la déclare ; un template (`den-template.yaml`) ne peut pas en porter. *(design court, section Approches — où déclarer)*
  - Conséquence : Une nest qui utilise un template gourmand en mémoire répète la valeur elle-même ; un défaut porté par le template reste possible plus tard sans rien casser.
  - **Réponse : Fidèle**
- **A2.** den vérifie seulement le format des valeurs ; les bornes restent vérifiées par sbx (sur sbx v0.54.0 : 512 MiB de mémoire au minimum, la mémoire de l'hôte au maximum). *(design court, section Approches — ce que den vérifie)*
  - Conséquence : Une valeur bien formée mais hors bornes (par exemple `256m`, ou plus que la mémoire de l'hôte) passe den et échoue chez sbx, peut-être après le téléchargement de l'image du template.
  - **Réponse : Fidèle**

## B — Mes choix par défaut

Les vrais points à trancher. Chaque ligne donne le choix retenu, puis l'alternative.

### Où et comment la valeur s'applique

- **B1.** La fusion se fait clé par clé : le den.yaml global peut fixer `memory` et la nest `cpus`, et la VM reçoit les deux. *(design court, section Objectifs)*
  - Sinon : La nest remplace le bloc entier : une nest qui déclare seulement `cpus` perd la `memory` du global.
  - **Réponse : Choix retenu**
- **B2.** La racine d'un den.yaml de projet (hors de `nests:`) refuse `resources:`, comme elle refuse déjà `auto_trust` et `add_dirs`. *(design court, section Contraintes)* — _Jamais discuté_
  - Sinon : La racine d'un den.yaml de projet accepte `resources:` comme défaut de toutes ses nests, entre le global et la nest.
  - **Réponse : Choix retenu**
- **B3.** Une clé absente partout n'est pas écrite dans le sbxenv.yaml : sbx applique son propre défaut. *(design court, section Objectifs)* — _Jamais discuté_
  - Sinon : den écrit toujours une valeur explicite (par exemple 4 CPU et 8g) quand rien n'est déclaré.
  - **Réponse : Choix retenu**
- **B4.** Les deux valeurs comptent dans la comparaison qui déclenche l'avertissement de dérive : quand `resources:` change dans le den.yaml après la création d'une instance Live, `den up` affiche l'avertissement et se rattache à la VM telle qu'elle est, sans la recréer. *(design court, section Objectifs)*
  - Conséquence : Pour appliquer la nouvelle taille, l'utilisateur lance `den rm` puis `den up`, comme pour tout autre changement de configuration.
  - Sinon : Les valeurs ne comptent pas dans cette comparaison : den ne signale rien, et la nouvelle taille s'applique seulement à la prochaine création.
  - **Réponse : Choix retenu**

### Vérification du format

- **B5.** `cpus` accepte seulement un entier de 1 ou plus ; `0`, un nombre négatif et une fraction sont refusés, et pour `0` le message dit « retirez `cpus` pour utiliser tous les CPU ». *(design court, section Décisions de l'assistant)* — _Jamais discuté_
  - Sinon : `cpus: 0` est accepté et transmis à sbx, qui le lit comme « tous les CPU ».
  - **Réponse : Choix retenu**
- **B6.** `memory` accepte seulement un entier suivi de `m` (MiB) ou `g` (GiB) en minuscule, les unités binaires de sbx (`512m`, `8g`) ; `8G`, `8gb` et `8` sont refusés avec un exemple valide. *(design court, section Décisions de l'assistant)* — _Jamais discuté_
  - Sinon : den accepte aussi les majuscules et les suffixes `k`/`t`, et les transmet tels quels à sbx.
  - **Réponse : Choix retenu**
- **B7.** Le format est vérifié au chargement du den.yaml, et l'erreur nomme ce fichier. *(design court, section Décisions de l'assistant)* — _Jamais discuté_
  - Conséquence : Une erreur de format dans une nest fait échouer le chargement de tout le fichier : den refuse alors toutes les nests de ce fichier, comme il le fait aujourd'hui pour une clé inconnue.
  - Sinon : Le format est vérifié à la préparation de l'instance : seule la nest fautive est refusée.
  - **Réponse : Choix retenu**

### Affichage

- **B8.** `den ls` n'affiche pas les ressources des instances. *(design court, section Non-objectifs)* — _Jamais discuté_
  - Sinon : `den ls` gagne une colonne CPU/mémoire, ce qui oblige à enregistrer les ressources dans le record de l'instance.
  - **Réponse : Choix retenu**

## C — Ce que vous n'avez pas à valider

Faits vérifiés, fichiers touchés et détail technique. Si un de ces points contredit A ou B, c'est A ou B qui gagne. Mesuré sur sbx v0.54.0 : le bloc `sandboxOptions` du sbxenv.yaml accepte `cpus` et `memory`. Un fichier avec une clé inconnue dans ce bloc est refusé à la lecture, avant toute création de VM. `sbx create --help` (v0.54.0) : mémoire en unités binaires (`512m`, `8g`), minimum 512 MiB, maximum la mémoire de l'hôte, avertissement au-delà de 75 % ; `cpus` 0 veut dire « tous les CPU de l'hôte ». Mesuré sur sbx v0.39 seulement, non vérifié sur v0.54.0 : sbx refusait une mémoire trop basse côté serveur, après avoir tiré l'image. Aujourd'hui `internal/sbxenv/render.go` n'écrit `sandboxOptions:` que si un template est posé ; les ressources n'en dépendront pas. Les deux valeurs entrent dans le hash de configuration de l'instance avec `omitempty` : une instance créée avant ce changement garde son hash et n'affiche pas de dérive après la mise à jour de den. Fichiers : `internal/config/schema.go`, `internal/config/load.go`, `internal/nest/resolve.go`, `internal/sbxenv/render.go`, `internal/instance/prepare.go`, `internal/instance/create.go`, `docs/den-yaml.md` puis `task plugin-sync`.
