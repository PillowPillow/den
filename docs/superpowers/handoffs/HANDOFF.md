# HANDOFF — `den` (CLI générique pour sandboxes sbx)

> Pour l'agent qui reprend le sujet **sans contexte de conversation**. Lis ce fichier, puis
> `CLAUDE.md`, puis le spec. Réponds en français (préférence utilisateur) ; écris le code, les
> commentaires et les messages utilisateur **en anglais**.
>
> Dernière mise à jour : **2026-08-03**, après la merge de `den build` (#8, PR #29).

> ⚠️ **Ce fichier a menti pendant cinq jours.** Sa version précédente, datée du 2026-07-28,
> annonçait « Plan 2 écrit et PAS exécuté », « 34 commits, rien n'est poussé », et des conventions
> françaises. Les quatre plans sont exécutés et mergés. Si un document de `docs/superpowers/` te
> décrit un état antérieur, c'est un **artefact historique** : les plans et les handoffs datés ne
> sont jamais réécrits, seul le spec l'est. En cas de doute, l'ordre est **le code, puis le spec,
> puis `CLAUDE.md`** — les handoffs et les plans arrivent en dernier.

## 0. TL;DR — où on en est

- **Les quatre plans sont livrés et mergés sur `main`.** Fondations, spawn, ports, build DAG.
  `origin` est `git@github.com:PillowPillow/den.git` et `main` y est poussé.
- **697 fonctions de test, 12 paquets, vert** (`make test`, qui passe `-count=1` : un `go test` nu
  peut passer sur du cache périmé).
- **La suite est hermétique et n'invoque jamais `sbx`.** Elle ne prouve donc rien sur le binaire.
  Ce qui est attesté contre un `sbx` réel l'est par des **smokes datés** et consigné au spec §14.0
  et §14.1, avec sa date. Ce dépôt atteste le comportement de `sbx` ; il ne l'extrapole pas.
- **v1 n'est pas taguée.** `Version = "dev"`, aucun tag. Ce qui reste est au §8 ci-dessous.

## 1. Mission

Rendre l'usage de `sbx` (microVM jetables) **simple et répétable** via une CLI Go **`den`**. Une
commande spawne (ou ré-attache) une VM multi-projets sans retaper stack, kits, politique d'egress et
worktrees à la main. `~/.den/` est la source unique de vérité ; `DEN_HOME` / `--den-home` la
redirige, et c'est ce qui rend toute la suite hermétique.

**North star : protéger la machine hôte.** La microVM est la frontière. On ne sécurise PAS l'infra
partagée. Toute décision de design se tranche par « est-ce que ça perce la frontière hôte ? ».

## 2. Décisions VERROUILLÉES (ne pas re-litiger — validées par l'utilisateur)

1. **CLI générique `den`**, dossier de config **`~/.den/` = source unique**. Le dépôt voisin
   `sbx-devbox` est un simple **exemple** à recopier dans `~/.den/stacks/`, pas une dépendance.
2. **Périmètre v1 = runtime + build** (DAG). **Interactif d'abord.** Flux agent autonome
   (`den agent` / `den review`, VM éphémère `--clone`), sync distant de kits, snapshot de plugins
   agent et registry/CI de distribution sont **hors v1** (spec §1, « Non-objectifs v1 »).
3. **Vocabulaire :** `den` (CLI/home) · **stack** = recette d'image constructible · **kit** = overlay
   env/policy natif sbx · **nest** = objet spawnable (repos+stack+egress+ports) · *sandbox* = la VM.
4. **Multi-projet natif :** un nest liste des repos ; `-w <worktree>` crée le worktree sur **tous**
   les repos et les co-monte dans une seule VM. Repos **optionnels décochables** à l'interactif
   (`-i`).
5. **Worktrees configurables, défaut central** : `~/.den/worktrees/<wt>/<repo>/`
   (`worktree_layout: central|per-repo`).
6. **Agents génériques** (registre dans `config.yaml`) : chaque agent = `config_dir` (monté RW,
   persiste, isolé du vrai `~/.claude`) + env vars. **Pas de snapshot/vendoring.** Override du
   `config_dir` **par nest ET par agent**, en map plate.
7. **SSH défaut `agent-forward`** (aucune clé dans la VM) ; `mount ~/.ssh_sbx` (clé dédiée
   révocable) = override courant ; `none` réservé au futur autonome.
8. **Ports** : fenêtre déterministe par nest (`base = 9000 + hash(nom)%900*10`, 10 ports),
   **publication À LA DEMANDE** via `den ports <name>` (PAS au spawn), lecture de l'existant
   avant scan, décalage de fenêtre si occupée. **Loopback-only strict** (`127.0.0.1`, jamais
   `0.0.0.0`). Accès distant = **tunnel SSH imprimé**, jamais de bind LAN.
9. **Policy déclarative** : egress = baseline global ∪ stack ∪ nest, matérialisé en
   `caps.network.allow` d'un **mixin généré** (auto-scopé à la sandbox, posé au create-time), +
   **settle-loop fail-closed** (`sbx policy check` en boucle avant d'attacher ; sinon n'attache pas).
10. ~~**État sans DB par labels sbx**~~ — **FALSIFIÉE le 2026-07-28** : `sbx create` n'a aucun
    `--label`. **L'identité d'une sandbox est son nom** `<nest>[.<worktree>]`, et c'est la seule
    poignée : `den ls`/`sh`/`rm`/`ports`, la policy scopée, le cache de mixins et la corbeille de
    worktrees s'y accrochent tous. `sbx.SandboxName` / `sbx.SplitName` possèdent le format.

### Décisions prises pendant l'exécution, spec amendé en conséquence

11. **Identité d'un objet = son chemin, jamais son contenu.** Une stack est nommée par son dossier,
    un nest par le basename de son fichier. Le champ `name:` a été **retiré** des deux schémas :
    l'écrire est une erreur de chargement. *Pourquoi :* deux sources d'identité concurrentes.
12. **Décodage YAML strict** (`KnownFields(true)`) sur tous les loaders. Une clé inconnue est une
    erreur nommant le fichier, la ligne et la clé — jamais un silence. *Pourquoi :* `egres:` au lieu
    d'`egress:` vide l'allowlist, et le settle-loop fail-closed meurt ensuite sans cause visible.
13. **Les noms courts de repos d'un nest sont uniques.** Deux homonymes sont rejetés au chargement,
    les deux chemins nommés.
14. **Un nom d'objet est un identifiant, pas un chemin** : `/`, `\` et `..` sont refusés.
15. **`config.Home()` renvoie toujours un chemin absolu.**
16. **den possède la séquence de build entière** (amendement du 2026-08-03) : `create` → N × `exec`
    → `stop` → `template save` → `rm`. Le `build.sh` par stack est **supprimé** ; une stack déclare
    `provision.includes` / `provision.steps`, et `image:` est **obligatoire**. C'est den qui passe
    l'image à `template save`, donc `image:` et ce qui est réellement sauvé ne peuvent plus diverger.

## 3. Le mixin généré (mécanisme clé)

À chaque spawn, den génère **UN seul kit jetable** portant : (a) les env vars de l'agent actif
(`{config_dir}` → chemin hôte, qui est aussi le chemin in-VM), (b) les `env` du nest, (c) l'egress
du nest en `caps.network.allow`. Layering au `create` :
`--kit policy-baseline --kit stacks/<stack>/kit --kit <mixin généré>`. **Le mixin est layeré en
dernier** — voir le piège du §6, et l'ordre est mesuré, pas supposé (spec §14.1).

## 4. Surface de commandes v1 (toute livrée)

`den <nest> [-w wt] [--without r] [--only r] [-i] [--agent a] [--detach]` (spawn-or-attach + shell) ·
`den ls` · `den sh <name>` · `den ports <name> [--add H:C]` · `den rm <name> [--keep-worktrees]` ·
`den build [stack] [--force]` · `den nest ls|show` · `den doctor` · `den version` · flag global
`--den-home`.

`<name>` est toujours un **nom de sandbox** (`<nest>[.<worktree>]`), jamais un nom de nest : les
ports sont publiés dans une VM vivante, et seul un nom de sandbox dit laquelle. La **fenêtre** de
ports, elle, est semée par le nest auquel ce nom appartient (`sbx.SplitName`).

## 5. Architecture — les invariants qui ne se devinent pas

Ils sont écrits et justifiés dans `CLAUDE.md`, qui est chargé à chaque session. En résumé :

- **Cascade de config** : global ← stack ← nest ← flags, résolue par `nest.Resolve`.
- **Tout accès système passe par `cli.Deps`**, et `deps.Sbx` est le `sbx.Runner` **unique** partagé
  par `ls`, `sh`, `ports` et spawn. Câbler une implémentation réelle en dur casse l'hermétisme.
- **L'ordre de la séquence de spawn est délibéré** : tout ce qui est refusable depuis la seule
  config est refusé **avant le premier effet de bord**, pour qu'un refus ne laisse jamais un
  worktree orphelin.
- **`internal/spawn` n'importe jamais `internal/ports`**, et `internal/cli` n'importe ni `net`, ni
  `hash/fnv`, ni `os/exec` : verrouillé par `internal/ports/hermeticity_test.go`.

## 6. Piège sbx à connaître (vérifié le 2026-07-27, pas théorique)

Les `commands.startup` des kits sont jouées par `/etc/durable-startup.d/run.sh` dans la VM :

1. chaque commande passe par `su -s /bin/sh -c … agent`, un `su` **non-login** → PATH sans
   `~/.local/bin` → tout binaire user-local est introuvable (`exit=127`). Il faut un `export PATH`
   explicite. C'est ce qui faisait que `claude update` ne tournait jamais au boot.
2. le dispatcher fait `exit $rc` au **premier** échec → une commande qui sort non-zéro **prive tous
   les kits suivants** de leurs startup commands. Ce qui est fail-closed doit être layeré **en
   dernier**.

Journal : `/var/log/sbx-kit-startup.log`. Encodé au §9.1 du spec, et c'est ce journal que den lit
pour tenir la **porte de fraîcheur de l'agent** (une sandbox ne démarre pas avec un agent périmé).

Un kit `lib/agent-claude` corrigé et testé attend d'être installé dans `sbx-devbox/lib/` — voir
`staging/lib/agent-claude/`.

## 7. Ce qui est attesté contre un `sbx` réel, et ce qui ne l'est pas

**Ne redémontre rien depuis le code.** Le spec §14.0 porte la surface `sbx` relevée avec sa date, et
le §14.1 l'inventaire des hypothèses A1→A11 avec, pour chacune, ce qui l'a fermée ou pourquoi elle
reste ouverte. Trois smokes réels ont eu lieu : **2026-07-29** (spawn), **2026-07-31** (ports,
sélection de repos, `-i`, `--agent`, `den sh`), **2026-08-03** (sonde egress et argv de build).

Deux avertissements qui coûtent cher si on les oublie :

- tout le §14.0 est relevé contre **v0.35.0**, qui annonçait v0.37.1 disponible. Au prochain smoke,
  commencer par `sbx version` et re-relever si le binaire a bougé ;
- « vert contre `sbx.Fake` » ne veut **rien dire** pour un comportement d'exécution de la microVM.
  Le double répond à l'argv qu'on lui donne, y compris à un argv qui n'existe pas.

## 8. Ce qui reste pour taguer v1

Ordre imposé, la première étape bloque la dernière :

1. **#31 — smoke réel n°3 sur `den build`.** Le maillon qui produit l'image (`sbx stop`,
   `sbx template save <sandbox> <image>`) n'a jamais touché un `sbx` réel. Exige la machine de
   l'utilisateur : aucun agent ne peut le produire. Il porte aussi la seule chose que la suite ne
   peut pas prouver de la porte du §9.1 sur `den sh` : qu'un journal se **remplit** pendant
   l'attente (le double rend toujours les mêmes octets).
2. **#10** — `-ldflags` remplissant `Version`, README final, tag `v1.0.0`. Strictement dernier.

## 9. Docs de référence

**Dans ce dépôt :**

- `CLAUDE.md` — les invariants d'architecture et les artefacts périmés à ne pas croire. Chargé à
  chaque session.
- `docs/superpowers/specs/2026-07-27-den-cli-design.md` — **LE spec**, source de vérité sur
  l'intention, amendé en continu. Une divergence spec/livré est un **bug dans l'un des deux**, pas
  une phase.
- `docs/superpowers/decisions/` — arbitrages datés (avertissement d'agent SSH vide, plan de clôture
  de la v1).
- `docs/superpowers/plans/` et `docs/superpowers/handoffs/2026-*` — **historiques, jamais réécrits**.
  Ils décrivent l'état à leur date.
- `README.md` — amorçage utilisateur (`cp -R examples/den-home ~/.den`, puis `den doctor`).

**Dans le dépôt voisin `sbx-devbox`** (monté `:ro` — non modifiable depuis la sandbox) :

- `docs/design/sbx-sandbox-support.md` + `...-challenge.md` — spikes validés et challenge adversarial.
- `stacks/devx/TUTO.md` — le workflow manuel que den automatise.
