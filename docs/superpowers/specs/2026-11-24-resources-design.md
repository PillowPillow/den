# `resources:` — CPU et mémoire d'une nest — design court

Date : 2026-11-24. Taille : petit changement. Issue : #90.
Validé ligne par ligne : `2026-11-24-resources-validation.md` (la fiche gagne sur ce document).

## Intention

Une nest déclare la taille de sa VM. Aujourd'hui chaque instance reçoit le défaut de sbx (50 % de la
mémoire de l'hôte, bornée à 512 MiB–32 GiB, et tous les CPU) : une nest qui fait tourner une suite de
tests et une nest qui fait tourner trois services et une base ont la même VM.

## Objectifs

- `resources: {cpus, memory}` au niveau haut du `den.yaml` global et dans une nest.
- Fusion clé par clé : la nest gagne sur le global, pour chaque clé séparément (le global peut fixer
  `memory`, la nest `cpus`).
- Rendu dans `sbxenv.yaml` sous `sandboxOptions.cpus` / `sandboxOptions.memory`. Une clé absente n'est
  pas écrite (jamais `cpus: 0`).
- Une instance Live dont les ressources ont changé reçoit l'avertissement de dérive habituel ; den ne
  la recrée pas.

## Non-objectifs

- Pas de `resources:` dans `den-template.yaml` (approche B écartée, voir plus bas).
- Pas de colonne dans `den ls` : les records ne portent pas les ressources.
- Pas de `--clone`, pas de `profile` sbx, pas de refus réseau à la création (hors périmètre de #90).

## Contraintes

- Mesuré sur sbx v0.54.0 : `sandboxOptions` accepte `cpus` et `memory` (une clé inconnue y est
  refusée au parse, avant toute création). `sbx create --help` : mémoire en unités binaires (`512m`,
  `8g`), minimum 512 MiB, maximum la mémoire de l'hôte, `cpus` 0 = auto.
- Décodage strict (`KnownFields(true)`) : `memroy:` est une erreur de chargement qui nomme le fichier.
- Un projet `den.yaml` ne porte pas `resources:` au niveau haut (même règle que `auto_trust`,
  `add_dirs`) ; il le porte dans une nest.
- `configHash` : les champs entrent avec `omitempty`, pour qu'une instance créée avant ce changement
  garde son hash.
- `render.go` n'écrit `sandboxOptions:` que si `Template` est posé ; les ressources ne doivent pas en
  dépendre.

## Approches

**Où déclarer** (choix de l'utilisateur : A).

- **A — global et nest seulement.** Comme `auto_trust`. La valeur vit là où la VM est créée. Coût : une
  nest répète la valeur qu'un template exigerait.
- **B — aussi dans `den-template.yaml`.** Écartée : ce fichier entre dans le `DefinitionHash`, donc
  changer la mémoire demanderait une reconstruction inutile du template ; un template donné par nom
  d'image officielle n'a pas ce fichier. Ajout possible plus tard sans casser A.

**Ce que den vérifie** (choix de l'utilisateur : 1).

- **1 — le format seulement.** `memory` : entier positif suivi de `m` ou `g`. `cpus` : entier ≥ 1.
  Les bornes restent à sbx, qui les a déjà changées une fois (1 GiB en v0.39, 512 MiB en v0.54) et
  dont le maximum dépend de l'hôte. Coût : une valeur hors bornes échoue chez sbx, sur v0.39 après le
  pull de l'image (non vérifié sur v0.54).
- **2 — format et bornes.** Écartée : den recopie une règle mouvante, et lire la mémoire de l'hôte
  ajoute une entrée à `cli.Deps`.
- **3 — aucune vérification.** Écartée : `8gb` échouerait tard, sans nommer le `den.yaml`.

## Décisions de l'assistant (à relire)

- `cpus` : entier ≥ 1. `0`, un négatif et une fraction sont refusés ; pour `0`, le remède dit
  « retirez `cpus` pour utiliser tous les CPU » : den refuse plutôt que traduire.
- Unités acceptées : `m` (MiB) et `g` (GiB), en minuscules, comme les exemples de sbx. `8G`, `8gb`,
  `8` sont refusés avec un exemple valide.
- La vérification du format a lieu au chargement (`internal/config`), pas dans `Prepare` : elle se
  décide depuis le fichier seul et nomme ce fichier. Conséquence acceptée : une erreur de format dans
  une nest fait échouer le chargement du fichier entier, comme une clé inconnue.
- Docs : `docs/den-yaml.md` décrit la clé, puis `task plugin-sync`.

## Fichiers

- `internal/config/schema.go`, `internal/config/load.go` — schéma, format, refus au niveau haut d'un
  projet
- `internal/nest/resolve.go` — fusion clé par clé dans `*nest.Resolved`
- `internal/sbxenv/render.go` — `sandboxOptions`
- `internal/instance/prepare.go`, `create.go` — `configHash`, passage au rendu
- `docs/den-yaml.md`, `plugins/den/reference/den-yaml.md`
