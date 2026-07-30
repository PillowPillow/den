# Warn « agent SSH vide » — spawn + doctor

Date : 2026-07-30. Statut : validé.

## Problème

En `ssh.mode: agent-forward` (le défaut), sbx transmet fidèlement le socket de
l'agent hôte dans la sandbox (`/run/ssh-agent.sock` — vérifié sur un sbx réel).
Mais si l'agent hôte n'a **aucune identité**, la sandbox hérite d'un agent
vide : `git@github.com: Permission denied (publickey)`, sans `~/.ssh` de
secours (ce serait le mode `mount`).

Cas réel : macOS, l'agent launchd démarre vide ; les push hôte marchent parce
que ssh lit les clés sur disque — chemin inexistant dans la sandbox. Le
problème existe aussi sous Linux/WSL (agent non lancé, ou lancé sans clé).

`den doctor` ne voit aujourd'hui que l'absence de `SSH_AUTH_SOCK`
(doctor.go, contrôle 4quater). Angle mort : socket présent, agent vide ou
injoignable. Le spawn, lui, ne dit rien.

## Décision

**Warn non bloquant au spawn + même contrôle dans doctor.** Pas d'auto-load
(effet de bord trousseau, spécifique macOS), pas d'échec bloquant (les usages
HTTPS/lecture seule n'ont pas besoin de SSH).

## Conception

### 1. Détection partagée — `internal/sshagent`

Exécute `ssh-add -l` en héritant l'environnement (donc `SSH_AUTH_SOCK`).
Trois états, portés par les exit codes standardisés d'OpenSSH (identiques
macOS / Linux / WSL) :

| Exit code | État | Sens |
|---|---|---|
| 0 | `Cles` | agent joignable, ≥1 identité (le stdout donne le compte) |
| 1 | `AgentVide` | agent joignable, 0 identité |
| 2 ou erreur exec | `AgentInjoignable` | socket mort, agent absent, ou `ssh-add` hors PATH |

Exécuteur injectable (même esprit que `doctor.Deps`) pour tester les trois
états sans agent réel.

### 2. Doctor — extension du contrôle 4quater

Uniquement si `ssh.mode == "agent-forward"` :

- `SSH_AUTH_SOCK` absent/vide → warn actuel, inchangé.
- Socket présent → interroge l'agent :
  - `Cles` → ok, affiche le nombre d'identités.
  - `AgentVide` → warn « agent sans identité — les sandboxes auront un accès
    SSH refusé (publickey) » + commande fix (§4).
  - `AgentInjoignable` → warn « SSH_AUTH_SOCK pointe sur un agent
    injoignable » + commande fix.

Les autres contrôles doctor ne bougent pas.

### 3. Spawn — contrôle avant `sbx create`

Si `ssh.mode == "agent-forward"` : même détection, warn sur **stderr**, le
spawn continue dans tous les cas. Le message précise que le fix agit **sans
respawn** : le socket transmis est un proxy vivant, l'agent rechargé côté hôte
est visible immédiatement dans la sandbox (vérifié sur sbx réel).

Modes `mount` et `none` : aucun contrôle, aucun message.

### 4. Commande fix par OS

Une fonction, sélection sur `runtime.GOOS` :

- `darwin` → `ssh-add --apple-use-keychain ~/.ssh/<clé>`
- autres (linux, WSL compris) → `ssh-add` (charge les clés par défaut de
  `~/.ssh`)

## Tests

- `internal/sshagent` : table sur les trois exit codes + erreur exec (fake).
- Doctor : socket présent × trois états → verdict et message attendus ;
  socket absent → comportement actuel préservé ; modes `mount`/`none` →
  aucun appel à l'agent.
- Spawn : warn visible sur stderr en `AgentVide`, spawn non interrompu ;
  silence en `Cles` et en modes `mount`/`none`.
- Commande fix : une assertion par branche GOOS.

## Hors scope

Auto-load du trousseau, échec bloquant, détection des remotes nécessitant
SSH, propagation `~/.ssh` (mode `mount` existant).
