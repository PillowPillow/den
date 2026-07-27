# den

CLI générique pour piloter des sandboxes [sbx](https://…) : une commande pour démarrer une microVM
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
| `den nest ls` | liste les nests déclarés |
| `den nest show <n>` | affiche un nest entièrement résolu (stack, agent, egress, repos) |
| `den doctor` | diagnostique la configuration et l'environnement |
| `den version` | version du binaire |

Le spawn (`den <nest>`), les ports et le build arrivent dans les incréments suivants — voir
`docs/superpowers/plans/`.

## Conception

`docs/superpowers/specs/2026-07-27-den-cli-design.md`.
