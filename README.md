# den

CLI générique pour piloter des sandboxes `sbx` : une commande pour démarrer une microVM
multi-projet, sans retaper mixin, kits et policy à la main.

## Installation

```bash
go build -o den ./cmd/den
```

## Amorçage

```bash
cp -R examples/den-home ~/.den
$EDITOR ~/.den/config.yaml
./den doctor
```

Au premier `den doctor`, deux diagnostics sont attendus tant que rien n'a été adapté : l'absence de
`sbx` s'il n'est pas installé, et le repo d'exemple `~/dev/mon-projet` introuvable. Les deux
disparaissent une fois `~/.den/config.yaml` et `~/.den/nests/exemple.yaml` ajustés.

`~/.den/` est la source unique de vérité. La variable `DEN_HOME` (ou le flag `--den-home`) permet
d'en utiliser un autre — c'est ce qui rend `den` testable et scriptable.

## Commandes disponibles

| Commande | Rôle |
|---|---|
| `den <nest>` | spawn-or-attach : crée la microVM du nest si elle n'existe pas, s'y attache sinon |
| `den ls` | liste les sandboxes vivantes, avec leur nest et leur worktree |
| `den sh <name>` | ouvre un shell dans une sandbox existante |
| `den rm <name>` | détruit une sandbox et nettoie les worktrees que den a créés (le profil agent persiste) |
| `den nest ls` | liste les nests déclarés |
| `den nest show <n>` | affiche un nest entièrement résolu (stack, agent, egress, repos) |
| `den doctor` | diagnostique la configuration et l'environnement |
| `den version` | version du binaire |

Options de `den <nest>` :

| Option | Effet |
|---|---|
| `-w`, `--worktree <nom>` | propage un worktree de ce nom sur **tous** les repos du nest, et suffixe le nom de sandbox (`api.feat12`) |
| `--detach` | prépare la sandbox sans y attacher de shell |
| `--only <repo,…>` | ne garder que ces repos optionnels (les repos requis restent montés) |
| `--without <repo,…>` | exclure ces repos optionnels |
| `--agent <nom>` | surcharge `defaults.agent` |

Options de `den rm` : `--keep-worktrees` (conserver les worktrees), `--force` (les supprimer même
s'ils portent des modifications non commitées ; sans lui, den refuse **avant** de toucher à la VM).

Une sandbox arrêtée — ce que `sbx` fait tout seul au bout de quelques minutes d'inactivité —
n'est pas une panne : `den <nest>` et `den sh` la reprennent, avec son état.

Ce qui n'est pas encore livré : `den ports` (les `ports:` d'un nest sont chargés et affichés, mais
rien ne les publie) et `den build` (le DAG des images). Voir `docs/superpowers/plans/`.

## Conception

`docs/superpowers/specs/2026-07-27-den-cli-design.md`.
