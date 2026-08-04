# den — Sources d'équipe (distribution et partage de stacks)

Date : 2026-08-04
Statut : validé (brainstorming avec Nicolas), en attente de plan d'implémentation
Spec mère : `2026-07-27-den-cli-design.md` — ce document l'étend, il ne la remplace pas.

## 1. Problème

Une équipe veut partager ses stacks (et des nests prêts à l'emploi) via un repo git interne —
typiquement sur un VPN, joignable depuis les machines des développeurs mais pas depuis l'extérieur,
et sans aucun rapport avec le GitHub du produit. Les besoins :

- **Installer** ce contenu dans den et le **mettre à jour** facilement.
- **Contribuer** : un collègue modifie le repo, pousse, les autres récupèrent.
- **Valider** : outiller le développement du repo d'équipe (localement et en CI) pour qu'une
  config cassée soit refusée avant d'atteindre les collègues.

Contrainte structurante : le repo est parfois **injoignable** (hors VPN). Un spawn hors VPN ne doit
jamais casser ni ralentir.

## 2. Concepts et décisions

### 2.1 Une *source* est un clone git au layout d'un den home partiel

```
<repo équipe>/
  stacks/<n>/stack.yaml · provision/ · kit/
  lib/
  kits/
  nests/<n>.yaml
```

**Pas de `config.yaml` dans une source.** Le config perso (agents, ssh, egress baseline, defaults)
reste la propriété de chaque machine. Rejeté : « le den home entier est un checkout du repo
d'équipe » — simple, mais il écrase les préférences perso et interdit d'avoir plusieurs sources.

den clone sous `~/.den/sources/<nom>/`. Le layout étant identique à celui du den home, les chemins
relatifs internes d'une stack (`../../lib/common.sh`, `../../kits/ssh-known-hosts`) résolvent
inchangés — **dans SA source**, jamais dans le den home local. `config.LoadStack` ne change pas :
seule la racine de résolution diffère.

### 2.2 Pas de registre parallèle

Cohérent avec « identité = chemin » (spec mère §2) : une source installée EST un dossier de
`sources/` qui est un clone git. L'URL vit dans le remote du clone, la fraîcheur dans la date du
dernier fetch (mtime de `FETCH_HEAD`), l'état dans `git status`. `den source ls` lit le disque et
git, rien d'autre. Rejeté : un `sources.yaml` d'inventaire — deuxième source de vérité à garder
synchrone avec le disque, exactement la classe de bug que l'approche A de la spec mère évite.

### 2.3 Adressage : préfixe de source

Les objets d'une source s'adressent `<source>:<nom>` : stack `corp:dgdevx`, nest `corp:backend`.
Le séparateur `:` est choisi contre `/` : il ne ressemble pas à un chemin (une stack de source
n'est PAS adressable par chemin relatif depuis le den home), et il survit tel quel dans un scalaire
YAML non quoté.
Le local reste sans préfixe. Collision impossible par construction, provenance visible dans les
messages d'erreur et les hints. Rejetés : la fusion plate « local gagne » (masquage silencieux,
contraire au fail-loud de den) et la fusion plate « collision = refus » (une source qui ajoute une
stack casse les utilisateurs qui avaient le même nom en local).

- Le **nom de source** obéit au même charset que les nests (`[A-Za-z0-9][A-Za-z0-9+-]*`, point
  exclu) : il devient préfixe d'adressage puis composant de nom de sandbox. `den source add`
  refuse un `--name` (ou un basename d'URL) illégal, avec le remède « passe `--name` ».
- `defaults.stack: corp:devx` dans le config perso est légal (un `:` non suivi d'espace reste un
  scalaire YAML plain).
- **Nom de sandbox** : `:` est illégal pour `sbx create --name`. Un nest `corp:backend` produit la
  sandbox `corp-backend`, par le même aplatissement que `-w` applique aux branches
  (`worktree.Flatten`). Une collision d'aplatissement (un nest local `corp-backend` a déjà une
  sandbox vivante) est un **refus au spawn**, jamais une normalisation silencieuse — normaliser
  casserait l'aller-retour nom de nest → nom de sandbox → `den ls` (spec mère §2).

### 2.4 Repo keys : les nests deviennent partageables

Un nest d'équipe ne peut pas porter de chemins machine-spécifiques. Une entrée de `repos:` porte
donc `path:` **ou** `key:`, mutuellement exclusifs (même forme de refus que `base`/`parent`) :

```yaml
# nests/backend.yaml (dans la source corp)
stack: corp:dgdevx
repos:
  - { key: review-mgmt }
  - { key: front-app, optional: true, url: git@gitlab.corp:front/app.git }
```

Le mapping clé → chemin local est **personnel**, dans le config perso :

```yaml
# ~/.den/config.yaml
repos:
  review-mgmt: ~/dev/review-mgmt
  front-app: ~/dev/front
```

- Clé non mappée au spawn = refus **avant tout effet de bord** (phase de résolution, spec mère §6) :
  « add `review-mgmt:` under `repos:` in ~/.den/config.yaml (clone: git@gitlab.corp:...) ».
- `url:` est purement indicative — elle enrichit le message de refus, den ne clone jamais un repo
  de travail. Rejeté : proposer le clone à l'interactif — du confort contre un mécanisme de plus
  (choix du chemin, mode non-TTY), YAGNI en v1.
- Les nests **locaux** peuvent aussi utiliser `key:` : mécanisme unique, pas réservé aux sources.
- Rejeté : un fichier `repos.yaml` dédié — un fichier de plus dans la cascade pour un gain de
  lisibilité marginal.

## 3. Commandes

| Commande | Effet |
|---|---|
| `den source add <url> [--name n]` | clone dans `sources/<n>/` (défaut : basename de l'URL), lint post-clone, **refus + suppression du clone** si invalide |
| `den source update [n]` | fetch, **lint de l'arbre fetché AVANT fast-forward** ; source invalide = refus, le clone reste sur l'ancien HEAD sain |
| `den source ls` | nom, URL, HEAD, date du dernier fetch, état lint |
| `den source rm <n>` | supprime le clone ; **refus si le working tree est sale** (contributions non poussées) |
| `den lint <path>` | valide un checkout arbitraire ; exit ≠ 0 pour la CI du repo d'équipe |

**Contribution** = éditer `~/.den/sources/corp/` directement, commit, push — le clone est un repo
git ordinaire, den n'ajoute aucun rite. En contrepartie, `den source update` **refuse** de toucher
un working tree sale (« commit or discard first ») : den ne détruit jamais du travail non poussé.

## 4. Fraîcheur : explicite + hint

Seul `den source update` touche le réseau. Au spawn, den lit le clone tel quel et affiche un
**hint** si le dernier fetch date de plus de 7 jours — un hint, jamais un refus, jamais de réseau.
Hors VPN tout fonctionne ; le hint dit simplement que la source n'a pas pu être rafraîchie
récemment.

Rejetés : l'auto-fetch au spawn (latence à chaque spawn, comportement réseau implicite, et un
timeout hors VPN à chaque fois) et le TTL configurable (un mécanisme de plus, un comportement
dépendant de l'horloge).

## 5. Validation : `den lint <path>`

Une seule implémentation, trois consommateurs : la CI du repo d'équipe, `den source add`
(post-clone) et `den source update` (pré-fast-forward, fail-closed).

Couverture v1 :

- YAML strict (`KnownFields(true)` — déjà la règle partout, spec mère §12).
- DAG `parent` : résolvable dans la source, sans cycle.
- Chemins `kit`, `kits`, `provision.includes`, `provision.steps` : existants **et confinés à la
  source** — un `../../../` qui s'échappe du checkout est un refus. Une source est un objet
  distribué : un chemin qui sort de son arbre dépend de la machine qui la reçoit, donc n'est pas
  partageable.
- Noms légaux (nests : `[A-Za-z0-9][A-Za-z0-9+-]*`, point exclu — spec mère §2).
- `image:` non vide sur toute stack (spec mère §4.2).
- `path:`/`key:` mutuellement exclusifs sur chaque entrée `repos:`.

Hors périmètre v1 : dry-run de build (exigerait `sbx`, donc une machine provisionnée — la CI du
repo d'équipe n'en a pas).

## 6. Cas limites

- **Une source cassée ne casse jamais un spawn qui ne l'utilise pas.** Le chargement d'une source
  est paresseux, déclenché par une référence `corp:...`. Un clone corrompu, supprimé à la main ou
  invalide n'est une erreur que pour les commandes qui le nomment — et `den source ls` le montre.
- **Source injoignable** (hors VPN) : `den source update` échoue avec le message git, le clone
  local reste utilisable, le spawn ne le remarque pas.
- **Fast-forward impossible** (histoire réécrite côté équipe) : refus avec remède (« le repo
  d'équipe a réécrit son histoire ; `den source rm` puis `den source add` si tu n'as pas de
  travail local »). den ne rebase ni ne merge jamais tout seul.

## 7. Tests

- Remotes `file://` locaux — aucun test n'ouvre de socket réseau, conformément aux conventions
  (CLAUDE.md). Git réel derrière `worktree.NeutralizeGitEnvironment()` dans `TestMain`, comme les
  packages `cli`, `spawn`, `worktree` le font déjà.
- Les opérations git des sources passent par l'exécutable git via le même style d'injection que le
  reste de `cli.Deps` si un point d'accès système nouveau apparaît ; sinon, git est déjà toléré
  dans les packages qui le neutralisent.
- `den lint` se teste sur des checkouts fixtures sous `testdata/` — aucun réseau, aucun sbx.
