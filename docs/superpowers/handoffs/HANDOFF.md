# HANDOFF — `den` (CLI générique pour sandboxes sbx)

> Pour l'agent qui reprend le sujet **sans contexte de conversation**. Lis ce fichier en entier,
> puis le spec, avant toute action. Réponds en français (préférence utilisateur).

## 0. TL;DR — où on en est

- **Phase actuelle : spec validé, Plan 1 écrit et vérifié. Rien n'est encore codé dans le dépôt.**
- **Spec (source de vérité) :** `docs/superpowers/specs/2026-07-27-den-cli-design.md`.
  ⚠️ Le dépôt `den` **n'a aucun commit** à ce jour (remote `git@github.com:PillowPillow/den.git`
  configuré, module Go `github.com/PillowPillow/den`).
- **Découpage retenu (4 plans incrémentaux)**, chacun livrant un logiciel testable seul :
  1. **Fondations & inspection** — `docs/superpowers/plans/2026-07-27-den-plan1-fondations.md` ✅ écrit
  2. **Spawn** — `sbx.Runner`, worktree, mixin, policy, `den <nest>`/`ls`/`sh`/`rm` — à écrire
  3. **Ports** — fenêtre déterministe, anti-collision, `den ports` — à écrire
  4. **Build DAG** — `den build` — à écrire
- **Prochaine étape :** exécuter le Plan 1 en TDD (`superpowers:subagent-driven-development` ou
  `superpowers:executing-plans`), ou écrire le Plan 2.
- Le Plan 1 a été **vérifié par compilation réelle** : tout son code Go build, `go vet` et `gofmt`
  passent, et sa suite de tests est verte. Ce n'est pas du pseudo-code.

## 1. Mission

Rendre l'usage de `sbx` (microVM jetables) **simple et répétable** via une CLI Go **`den`**.
Aujourd'hui : `sbx` brut + kits + tutos, `create` verbeux (mixin jetable pour l'env agent,
empilement de `--kit`, policy à poser à la main). `den` absorbe tout ça.

**North star : protéger la machine hôte.** La microVM est la frontière. On ne sécurise PAS
l'infra partagée. Toute décision de design se tranche par « est-ce que ça perce la frontière hôte ? ».

## 2. Décisions VERROUILLÉES (ne pas re-litiger — validées par l'utilisateur)

1. **CLI générique `den`**, dossier de config **`~/.den/` = source unique**. Le dépôt courant
   `sbx-devbox` devient un simple **exemple** à recopier dans `~/.den/stacks/`, pas une dépendance.
2. **Périmètre v1 = runtime + build** (DAG). **Interactif d'abord.** Flux agent autonome
   (`den agent`/`den review`, VM éphémère `--clone`) = **réservé dans le vocabulaire, hors v1**.
   Pas de sync distant, pas de snapshot plugins.
3. **Vocabulaire :** `den` (CLI/home) · **stack** = recette d'image buildable · **kit** = overlay
   env/policy natif sbx · **nest** = objet spawnable (repos+stack+egress+ports) · *sandbox* = la VM.
4. **Multi-projet natif :** un nest liste des repos ; `-w <worktree>` crée le worktree sur **tous**
   les repos et les co-monte dans une seule VM. Repos **optionnels décochables** à l'interactif (`-i`).
5. **Worktrees configurables, défaut central** : `~/.den/worktrees/<wt>/<repo>/`
   (`worktree_layout: central|per-repo`).
6. **Agents génériques** (registre dans `config.yaml`, Claude aujourd'hui, Codex demain) : chaque
   agent = `config_dir` (monté RW, persiste, isolé du vrai `~/.claude`) + env vars. **Pas de
   snapshot/vendoring.** Override du `config_dir` **par nest ET par agent**, en map plate :
   `nests/x.yaml → agents: { claude: ~/chemin, codex: ~/chemin }`.
7. **SSH défaut `agent-forward`** (aucune clé dans la VM) ; `mount ~/.ssh_sbx` (clé dédiée
   révocable) = override courant à l'usage ; `none` réservé au futur autonome.
8. **Ports** : fenêtre déterministe par nest (`base = 9000 + hash(nom)%900*10`, 10 ports),
   **publication À LA DEMANDE** via `den ports <nest>` (PAS au spawn), scan anti-collision +
   décalage de fenêtre si occupée (1re instance garde l'URL canonique). **Loopback-only strict**
   (`127.0.0.1`, jamais `0.0.0.0`), CDP/Playwright **loopback-locked**. Accès distant = **tunnel
   SSH imprimé** (`ssh -L`), jamais de bind LAN.
9. **Policy déclarative** : egress = baseline global ∪ stack ∪ nest, matérialisé en `network.allow`
   d'un **mixin généré** (auto-scopé à la sandbox, posé au create-time), + **settle-loop
   fail-closed** (`sbx policy check` en boucle avant d'attacher ; sinon n'attache pas).
10. **État sans DB** (approche A + un peu de B) : labels sbx (`den.managed=1`, `den.nest`,
    `den.worktree`) ; `den ls` = `sbx ls` filtré. Cache `~/.den/cache/` optionnel, reconstructible.

## 3. Le mixin généré (mécanisme clé)

À chaque spawn, `den` génère **UN seul kit jetable** portant : (a) les env vars de l'agent actif
(`{config_dir}` → chemin in-VM), (b) les `env` du nest, (c) l'egress du nest en `network.allow`.
Il remplace le `mktemp` manuel du TUTO actuel. Layering au `create` :
`--kit policy-baseline --kit stacks/<stack>/kit --kit <mixin généré>`.

## 4. Surface de commandes v1

`den <nest> [-w wt] [--without r] [--only r] [-i] [--agent a] [--detach]` (spawn-or-attach+shell) ·
`den ls` · `den sh <name>` · `den ports <nest> [--add H:C]` · `den rm <name> [--keep-worktrees]` ·
`den build [stack] [--force]` · `den doctor` · `den nest ls|show`.

## 5. Data flow spawn (résumé — détail au §6 du spec)

résolution (cascade global←stack←nest←flags) → sélection repos → worktrees (idempotent) →
profil agent (mount RW `config_dir`, orthogonal à la stack) → mixin généré → assemblage
`sbx create` (labels, spawn-or-attach) → policy + settle-loop fail-closed → ssh → attache.
**Ports non publiés au spawn.**

## 6. Faits sbx VALIDÉS (ne pas re-tester, viennent des spikes — cf. docs/design/sbx-*.md)

- Boot microVM ~38 s, workspace **direct-monté au MÊME chemin absolu hôte** dans la VM.
- Réseau = **proxy côté hôte, routage par hostname** ; **DNS guest MORT** (le proxy route quand
  même les HTTP(S) par hostname → pas besoin de `/etc/hosts` pour le HTTP proxifié ; seul le
  raw-TCP par IP, ex. Mongo, aurait besoin d'IP).
- Policy : `deny-all` + allowlist ; **propagation NON instantanée** → poser les règles au
  create-time (kit-embedded `network.allow` auto-scopé) + settle-loop.
- `sbx ports <name> --publish 127.0.0.1:H:C` = **post-create**, publie vers le loopback hôte only.
- `dockerd` démarre tout seul dans la VM (docker compose OK). CDP non authentifié = loopback only.
- Profil Claude : monter un `~/.claude_sbx` **jetable** RW (persiste), **jamais** le vrai `~/.claude`.

## 7. Choix techniques

Go · CLI **cobra** · YAML **`yaml.v3`** · binaire statique · `sbx` piloté par **exec derrière une
interface `sbx.Runner` (mockable)**. Layout : `cmd/den/` + `internal/{config,nest,sbx,worktree,policy,ports,agent}`.
**TDD** : unitaires sur la logique pure (cascade config, union egress, calcul ports+anti-collision,
sélection repos, rendu mixin, **argv `sbx create` en golden files**) ; `worktree/` sur repos git
temp réels ; smoke e2e manuel hors CI.

## 8. Questions ouvertes à trancher au build (§14 du spec)

- Découverte de la branche par défaut par repo pour `-w` (`git symbolic-ref` ?).
- Sémantique/nom exacts de `sbx policy check` (module `policy/`).
- `sbx create --label` supporté ? sinon fallback = préfixe de nommage.
- Nettoyage worktrees au `rm` : retirer par défaut, refuser si dirty sans `--force`, `--keep-worktrees`.
- Emplacement final du **nouveau dépôt** de la CLI + migration de l'exemple `sbx-devbox` → `~/.den/stacks/`.

## 9. Docs de référence

**Dans ce dépôt (`den`) :**
- `docs/superpowers/specs/2026-07-27-den-cli-design.md` — **LE spec** (à lire en entier).
- `docs/superpowers/plans/2026-07-27-den-plan1-fondations.md` — Plan 1, prêt à exécuter.

**Dans le dépôt voisin `sbx-devbox`** (monté `:ro` depuis la sandbox — non modifiable de l'intérieur) :
- `docs/design/2026-07-24-sbx-dedicated-repo-design.md` — design du repo de stacks.
- `docs/design/sbx-sandbox-support.md` + `...-challenge.md` — spikes validés + challenge adversarial.
- `stacks/devx/TUTO.md` — le workflow manuel que `den` automatise.

## 10. Piège sbx à connaître (vérifié le 2026-07-27, pas théorique)

Les `commands.startup` des kits sont jouées par `/etc/durable-startup.d/run.sh` dans la VM :

1. chaque commande passe par `su -s /bin/sh -c … agent`, un `su` **non-login** → PATH sans
   `~/.local/bin` → tout binaire user-local est introuvable (`exit=127`). Il faut un `export PATH`
   explicite. C'est ce qui a fait que `claude update` ne tournait jamais au boot.
2. le dispatcher fait `exit $rc` au **premier** échec → une commande qui sort non-zéro **prive tous
   les kits suivants** de leurs startup commands. Ce qui est fail-closed doit être layeré **en dernier**.

Journal : `/var/log/sbx-kit-startup.log`. Encodé au §9.1 du spec. Un kit `lib/agent-claude` corrigé
et testé attend d'être installé dans `sbx-devbox/lib/` (voir `den/staging/lib/agent-claude/`).

## 11. Prochaine action concrète

1. Exécuter le **Plan 1** (`superpowers:subagent-driven-development` recommandé), ou
2. Écrire le **Plan 2 — Spawn** avec `superpowers:writing-plans`.

Ordre d'implémentation global inchangé : `config/` → `nest/` → `sbx/` (Runner+argv golden files) →
`worktree/` → `agent/`+mixin → `policy/`+settle-loop → `ports/` → `build`.
