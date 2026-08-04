# Spec — `install.sh` : installation sans brew

**Date** : 2026-08-04
**Statut** : validé (brainstorming du 2026-08-04)
**Complète** : la spec CLI `2026-07-27-den-cli-design.md` (§ distribution) et le pipeline goreleaser (`.goreleaser.yaml`, `release.yml`).

## Problème

Homebrew couvre macOS (cask via `homebrew-tap`) mais pas les distribs Linux sans brew ni
WSL. Aujourd'hui ces utilisateurs doivent télécharger l'archive à la main ou passer par
`go install` (qui exige une toolchain Go). Il faut un chemin d'installation en une
commande, sans prérequis autre que `curl` + `tar`.

## Interface utilisateur

```sh
curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | sh
```

Variables d'environnement, toutes optionnelles :

| Variable | Défaut | Rôle |
|---|---|---|
| `DEN_VERSION` | latest release | Pinner une version exacte (`v1.0.0`) — rollback, CI |
| `DEN_INSTALL_DIR` | `~/.local/bin` | Destination du binaire |

`DEN_VERSION` doit porter le `v` initial : `1.0.1` est **refusé**, pas normalisé. Sans le
`v`, le nom d'archive resterait valide sous une URL de download inexistante, et le 404
accuserait la release d'une faute qui est dans l'environnement de l'appelant.

En invocation pipe, l'assignation se place sur `sh`, pas sur `curl` — chaque commande d'un
pipeline reçoit son propre environnement, donc `DEN_VERSION=… curl … | sh` laisserait `sh`
sans la variable : `curl -fsSL … | DEN_VERSION=v1.0.0 sh`.

Le script est servi depuis `main` via raw.githubusercontent : zéro infra, corrigeable par
simple merge, versionné avec le repo. Rejeté : domaine court (infra à maintenir), asset de
release (script figé jusqu'à la release suivante).

## Comportement

### 1. Résolution de la version

Sans `DEN_VERSION` : `curl -fsSLI -o /dev/null -w '%{url_effective}'
https://github.com/PillowPillow/den/releases/latest` — curl suit la redirection et le tag
est lu dans le dernier segment de l'URL finale. Rejeté : l'API
`api.github.com/releases/latest`, qui plafonne à 60 requêtes/h non authentifiées et
imposerait un parsing JSON sans `jq` (dépendance qu'on refuse d'exiger).

### 2. Détection de plateforme

`uname -s` → `darwin|linux`, `uname -m` → `amd64|arm64` (en mappant `x86_64` et
`aarch64`). WSL se présente comme `Linux` : aucun cas spécial. Toute plateforme hors
matrice est **refusée** avec un message qui nomme les quatre combinaisons supportées et
l'URL des releases — convention du repo : l'erreur nomme le remède, jamais de
normalisation silencieuse.

### 3. Téléchargement et vérification

- Archive `den_<version sans v>_<os>_<arch>.tar.gz` + `checksums.txt` depuis
  `releases/download/<tag>/` (le naming vient du `name_template` de `.goreleaser.yaml` —
  toute divergence est attrapée par le smoke CI, cf. §CI).
- Vérification sha256 obligatoire : `sha256sum` (Linux) ou `shasum -a 256` (macOS).
  **Aucun des deux disponible = refus**, pas d'installation non vérifiée — même
  fail-closed que le settle-loop de spawn.
- Travail dans un répertoire `mktemp -d`, nettoyé par `trap` même en cas d'échec.

### 4. Installation

- `mkdir -p` de la destination si absente, puis installation en deux temps :
  `install -m 755 den "$DEN_INSTALL_DIR/.den.new.$$"` suivi d'un `mv -f` sur
  `$DEN_INSTALL_DIR/den`. Le remplacement est ainsi un `rename(2)` atomique — jamais de
  binaire tronqué sur le PATH — et une réinstallation pendant que `den` tourne n'échoue
  pas en ETXTBSY. Le fichier de staging est couvert par le `trap`.
- Pas de sudo : `~/.local/bin` est le standard XDG, présent dans le PATH des distribs
  récentes. Rejeté : `/usr/local/bin` + sudo (un `curl | sh` qui demande sudo mérite la
  méfiance qu'il inspire) ; auto-détection de la destination (comportement variable selon
  machine, imprévisible).
- Si la destination n'est pas dans `$PATH` : warning avec la ligne exacte à ajouter au rc
  du shell.
- Dernière ligne : sortie de `"$DEN_INSTALL_DIR/den" version` — la preuve que le binaire
  installé répond, pas une promesse.

### macOS et Gatekeeper

Le hook `xattr` du cask ne s'applique qu'à brew, mais un binaire téléchargé par `curl` ne
reçoit pas l'attribut `com.apple.quarantine` (posé par les navigateurs) : pas de blocage
Gatekeeper, pas de hook à reproduire.

## Contraintes d'implémentation

- **POSIX sh strict** — pas de bashisme. Le script doit tourner sous dash/ash (Debian
  minimal, Alpine, WSL). `shellcheck` avec shell=sh le verrouille.
- Anglais pour le code et les messages (convention repo).
- Erreurs : nommer le fichier/la commande à corriger et le remède.

## CI

Deux ajouts à `ci.yml` :

1. **shellcheck** sur `install.sh` (lint, statique, sans réseau).
2. **Smoke** : sur `ubuntu-latest`, exécuter `install.sh` contre la vraie latest release
   et assert que `den version` répond le tag attendu. C'est le seul garde-fou contre un
   renommage d'archive côté goreleaser — le naming de l'archive est une interface entre
   `.goreleaser.yaml` et `install.sh`, et rien d'autre ne la teste.

## Docs

- README : section « Install script » après la section brew, avec mention explicite de
  WSL, de `DEN_VERSION` et de `DEN_INSTALL_DIR`.

## Hors scope (YAGNI)

Windows natif (PowerShell), paquets apt/rpm/AUR, auto-update, commande de
désinstallation, support d'autres arches (riscv64…).
