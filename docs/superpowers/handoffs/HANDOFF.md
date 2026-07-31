# HANDOFF — `den` (CLI générique pour sandboxes sbx)

> Pour l'agent qui reprend le sujet **sans contexte de conversation**. Lis ce fichier en entier,
> puis le spec, avant toute action. Réponds en français (préférence utilisateur).
>
> Dernière mise à jour : **2026-07-28**, à la fin de la rédaction du Plan 2.

> ⚠️ **Le Plan 2 est écrit et pas exécuté.** Si tu reprends le sujet pour l'implémenter, lis
> d'abord `docs/superpowers/handoffs/2026-07-28-HANDOFF-plan2-ultracode.md` : il porte l'état
> courant, l'affectation des modèles par tâche et les pièges d'orchestration. Ce fichier-ci reste
> la référence pour le **contexte** (vocabulaire, décisions verrouillées, faits sbx des spikes).

## 0. TL;DR — où on en est

- **Phase actuelle : Plan 1 exécuté et vérifié. Plan 2 écrit, revu, PAS exécuté.**
- ⚠️ **Le sondage de la CLI `sbx` du 2026-07-28 a falsifié la décision verrouillée n°10**
  (état par labels) : `sbx create` n'a aucun `--label`. L'identité d'une sandbox passe désormais par
  son **nom** `<nest>[.<worktree>]`. Deux autres points du spec sont tombés — la colonne « âge » de
  `den ls` et le schéma de kit (`caps.network.allow` / `environment.variables`, pas `network.allow`
  / `env`). **La tâche 1 du Plan 2 amende le spec ; elle n'a pas encore été exécutée, donc le spec
  du dépôt porte encore ces trois erreurs.**
- **Spec (source de vérité) :** `docs/superpowers/specs/2026-07-27-den-cli-design.md`.
  ⚠️ Il a été **amendé** pendant l'exécution du Plan 1 (§2, §4.2, §4.3, §12, §13, §14) — voir §2bis
  ci-dessous. Lis la version du dépôt, pas ton souvenir.
- **34 commits sur `main`. Rien n'est poussé** : `origin` (`git@github.com:PillowPillow/den.git`)
  est configuré mais vide, et `main` n'a pas d'upstream. La publication attend une décision de
  l'utilisateur (cf. §12).
- **Découpage retenu (4 plans incrémentaux)**, chacun livrant un logiciel testable seul :
  1. **Fondations & inspection** — `docs/superpowers/plans/2026-07-27-den-plan1-fondations.md` ✅ **exécuté**
  2. **Spawn** — `sbx.Runner`, worktree, mixin, policy, `den <nest>`/`ls`/`sh`/`rm` — **à écrire**
  3. **Ports** — fenêtre déterministe, anti-collision, `den ports` — à écrire
  4. **Build DAG** — `den build` — à écrire
- **Prochaine étape :** écrire le **Plan 2 — Spawn** (`superpowers:writing-plans`). Lis le §11, il
  contient trois points que le plan 2 doit intégrer et qui ne se déduisent pas du code.

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
   **publication À LA DEMANDE** via `den ports <name>` (PAS au spawn), scan anti-collision +
   décalage de fenêtre si occupée (1re instance garde l'URL canonique). **Loopback-only strict**
   (`127.0.0.1`, jamais `0.0.0.0`), CDP/Playwright **loopback-locked**. Accès distant = **tunnel
   SSH imprimé** (`ssh -L`), jamais de bind LAN.
9. **Policy déclarative** : egress = baseline global ∪ stack ∪ nest, matérialisé en `network.allow`
   d'un **mixin généré** (auto-scopé à la sandbox, posé au create-time), + **settle-loop
   fail-closed** (`sbx policy check` en boucle avant d'attacher ; sinon n'attache pas).
10. **État sans DB** (approche A + un peu de B) : labels sbx (`den.managed=1`, `den.nest`,
    `den.worktree`) ; `den ls` = `sbx ls` filtré. Cache `~/.den/cache/` optionnel, reconstructible.

## 2bis. Décisions prises PENDANT le Plan 1 (spec amendé en conséquence)

Ces cinq points ne figuraient pas dans le spec d'origine. Ils y sont désormais, et le code les
applique. **Ne les re-litige pas, et surtout ne les contredis pas dans le plan 2.**

11. **Identité d'un objet = son chemin, jamais son contenu.** Une stack est nommée par son dossier
    (`stacks/<n>/`), un nest par le basename de son fichier (`nests/<n>.yaml`). **Le champ `name:`
    a été retiré des deux schémas** — l'écrire est désormais une erreur de chargement.
    *Pourquoi :* le champ créait deux sources d'identité concurrentes. Un nest `api.yaml` déclarant
    `name: fullstack` sortait non trié de `den nest ls` et n'était pas ouvrable par
    `den nest show fullstack`. L'option « un fichier unique `nests.yaml` » a été étudiée puis
    écartée (elle créerait deux conventions d'identité dans `~/.den`, les stacks ne pouvant pas
    fusionner puisqu'elles portent des fichiers ; et son seul bénéfice réel — les anchors YAML —
    est déjà couvert par la cascade pour l'egress). **Spec §2 (règle générale), §4.2, §4.3, §13 (décision 12).**
12. **Décodage YAML strict** (`KnownFields(true)`) sur les trois loaders. Une clé inconnue est une
    erreur de chargement nommant le fichier, la ligne et la clé — jamais un silence.
    *Pourquoi :* `egres:` au lieu de `egress:` produisait une allowlist vide et un `den doctor` qui
    certifiait « cohérente ». Au plan 2 ça devient une sandbox qui n'atteint pas `api.anthropic.com`
    et un settle-loop fail-closed sans cause visible. **Spec §12.**
13. **Les noms courts de repos d'un nest doivent être uniques.** Deux repos homonymes
    (`/x/api` et `/y/api`) sont rejetés au chargement, avec les deux chemins nommés.
    *Pourquoi :* `--without api` en retirait deux silencieusement, et au plan 2 le layout
    `worktree_root/<wt>/<repo>` collisionnerait sur le même dossier.
14. **Un nom d'objet est un identifiant, pas un chemin** : `/`, `\` et `..` sont refusés dans les
    noms de nest et de stack (`den nest show ../../etc/passwd` sortait de `DEN_HOME`).
15. **`config.Home()` renvoie toujours un chemin absolu.** Un `--den-home` relatif fabriquait des
    chemins de worktree relatifs, qui seraient partis tels quels vers `git worktree` et `sbx create`.

## 3. Le mixin généré (mécanisme clé — plan 2)

À chaque spawn, `den` génère **UN seul kit jetable** portant : (a) les env vars de l'agent actif
(`{config_dir}` → chemin in-VM), (b) les `env` du nest, (c) l'egress du nest en `network.allow`.
Il remplace le `mktemp` manuel du TUTO actuel. Layering au `create` :
`--kit policy-baseline --kit stacks/<stack>/kit --kit <mixin généré>`.

## 4. Surface de commandes v1

**Livrées par le Plan 1 :** `den version` · `den nest ls` · `den nest show <n> [--agent a]
[--without r] [--only r]` · `den doctor` · flag global `--den-home`.

**À venir :** `den <nest> [-w wt] [--without r] [--only r] [-i] [--agent a] [--detach]`
(spawn-or-attach+shell) · `den ls` · `den sh <name>` · `den ports <name> [--add H:C]` ·
`den rm <name> [--keep-worktrees]` · `den build [stack] [--force]`.

## 5. Data flow spawn (résumé — détail au §6 du spec)

résolution (cascade global←stack←nest←flags) → sélection repos → worktrees (idempotent) →
profil agent (mount RW `config_dir`, orthogonal à la stack) → mixin généré → assemblage
`sbx create` (labels, spawn-or-attach) → policy + settle-loop fail-closed → ssh → attache.
**Ports non publiés au spawn.**

La première moitié (résolution + sélection repos) est **faite** : `nest.Resolve` la livre.

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

## 7. Ce que le Plan 1 a livré (état réel du code)

```
cmd/den/main.go                 15 l.   main() → cli.Execute()
internal/cli/root.go            51 l.   NewRootCmd, flag --den-home (scopé à l'instance), version
internal/cli/nest.go           144 l.   den nest ls | show
internal/cli/doctor.go          46 l.   den doctor (Deps injectables)
internal/config/paths.go        45 l.   Home() (absolu), ExpandPath() (~ en tête SEULEMENT)
internal/config/yaml.go         66 l.   DecodeYAMLStrict + francisation des clés inconnues
internal/config/nom.go          30 l.   ValiderNom (refuse séparateurs et ..)
internal/config/config.go       78 l.   types + LoadGlobal
internal/config/validate.go     64 l.   (*Global).Validate() — cumulative, toutes les erreurs
internal/config/stack.go        83 l.   Stack + LoadStack / LoadStacks
internal/nest/nest.go          119 l.   types + LoadNest / ListNests (trié)
internal/nest/repos.go          92 l.   SelectRepos (--without/--only) + unicité des noms courts
internal/nest/egress.go         22 l.   UnionEgress (dédupliqué, trié, non-nil)
internal/nest/resolve.go        99 l.   Options, Resolved, ResolveAgent, Resolve
internal/doctor/doctor.go      118 l.   Check, Deps, Run — sans effet de bord
examples/den-home/             config.yaml + stacks/devx + nests/exemple (validé par un test)
```

Chaque fichier a son `*_test.go` à côté. **Vérifié le 2026-07-27 sur `cf9c1b7`** :
`go test -count=1 ./...` vert sur les 4 paquets, `go build -o den ./cmd/den`, `go vet ./...` et
`gofmt -l .` propres. Suite **hermétique** : aucun test n'importe `net/http` ni `os/exec`, aucune
écriture hors `t.TempDir()`, `sbx` n'est jamais invoqué (injecté via `doctor.Deps`), aucun test ne
touche au vrai `~/.den`.

**Méthode :** TDD strict, un implémenteur par tâche, une revue de tâche après chacune, quatre rounds
de correction déclenchés par les revues, puis une revue finale de branche et une vague de correction
unique. Le journal complet est dans `.superpowers/sdd/2026-07-27-den-plan1-fondations/` (dossier
git-ignoré : ledger `progress.md`, briefs, rapports d'implémentation, rapports de revue). **Il sera
peut-être supprimé** — le ledger y consigne tous les arbitrages, lis-le s'il est encore là.

## 8. Choix techniques

Go 1.26 · CLI **cobra** · YAML **`yaml.v3`** · binaire statique · aucune autre dépendance ·
`sbx` piloté par **exec derrière une interface `sbx.Runner` (mockable)** — à écrire au plan 2.
Layout : `cmd/den/` + `internal/{config,nest,sbx,worktree,policy,ports,agent,cli,doctor}`.
**TDD** : unitaires sur la logique pure (cascade config, union egress, calcul ports+anti-collision,
sélection repos, rendu mixin, **argv `sbx create` en golden files**) ; `worktree/` sur repos git
temp réels ; smoke e2e manuel hors CI.

Conventions établies par le Plan 1, à suivre : messages et commentaires **en français** ; erreurs au
format `contexte : détail` nommant toujours le chemin complet et listant les valeurs disponibles ;
**tout ce qui s'affiche ou se sérialise est trié** (`slices.Sorted(maps.Keys(m))` pour les clés de
map) ; helpers de test au patron `ecrisX(t, denHome, …)` sur `t.TempDir()` ; `internal/cli` ne fait
que du câblage cobra et de l'affichage.

## 9. Dette connue et parquée (à trancher au plan 2)

- **`LoadNest`/`LoadGlobal` ne distinguent pas « absent » d'« illisible »** (seul `LoadStack` le
  fait). Vérifié : ça ne produit aucun diagnostic trompeur dans `doctor`, c'est cosmétique.
- **`TestLoadStacksIndexeParLeNomDeDossier` n'a plus de mordant** : l'identité étant verrouillée en
  amont, les deux clés ne peuvent plus diverger. Le code est bon, c'est le nom du test qui sur-promet.
- **Le fichier de Plan 1 est périmé sur un point** : ses snippets YAML contiennent encore `name:`,
  devenu illégal (décision 11). C'est un artefact historique, il n'a volontairement pas été réécrit.
  **Le spec, lui, est à jour.** En cas de doute, le spec gagne.
- Divers mineurs consignés au ledger (`.superpowers/sdd/…/progress.md`) : assertions perfectibles,
  couvertures étroites. Rien de fonctionnel.

## 10. Piège sbx à connaître (vérifié le 2026-07-27, pas théorique)

Les `commands.startup` des kits sont jouées par `/etc/durable-startup.d/run.sh` dans la VM :

1. chaque commande passe par `su -s /bin/sh -c … agent`, un `su` **non-login** → PATH sans
   `~/.local/bin` → tout binaire user-local est introuvable (`exit=127`). Il faut un `export PATH`
   explicite. C'est ce qui a fait que `claude update` ne tournait jamais au boot.
2. le dispatcher fait `exit $rc` au **premier** échec → une commande qui sort non-zéro **prive tous
   les kits suivants** de leurs startup commands. Ce qui est fail-closed doit être layeré **en dernier**.

Journal : `/var/log/sbx-kit-startup.log`. Encodé au §9.1 du spec. Un kit `lib/agent-claude` corrigé
et testé attend d'être installé dans `sbx-devbox/lib/` (voir `den/staging/lib/agent-claude/`).

## 11. Ce que le Plan 2 doit intégrer (ne se déduit pas du code)

1. **Écrire le golden file du §9.1 en premier.** Le rendu de la commande de fraîcheur doit contenir
   **littéralement** `$HOME/.local/bin` : c'est le garde-fou de la frontière hôte/VM. `bin_dirs` ne
   subit aucune transformation côté hôte — `ExpandPath` n'expanse qu'un `~` **en tête** et laisse
   `$HOME` intact, et c'est testé (`internal/config/paths_test.go`). Un plan qui expanserait
   `bin_dirs` casserait le §9.1 sans que rien ne le signale avant le premier boot.
2. **`Resolved` n'est pas tout à fait « plus rien à recalculer »**, malgré son commentaire :
   - l'**env n'est ni fusionné ni substitué** — `Resolved` expose `Agent.Env` (avec `{config_dir}`
     encore littéral) et `Nest.Env` séparément, alors que le mixin a besoin de leur union avec
     `{config_dir}` résolu vers `AgentConfigDir`. La substitution est une règle de cascade, pas
     d'affichage : sa place est dans `nest.Resolve`, sous forme d'un champ `Env` déjà prêt.
   - **`denHome` n'y figure pas**, alors que le mixin généré devra s'écrire quelque part sous
     `~/.den/`. L'ajouter maintenant évite de repasser la valeur en paramètre dans quatre modules.
3. **L'identité doit boucler** : `den nest ls` → `den nest show <n>` → `den <n>` → label
   `den.nest=<n>` → `den ls`. Elle est fermée depuis la décision 11 ; le plan 2 doit dire
   explicitement que c'est **ce nom-là** (le basename du fichier) qui devient le nom de sandbox,
   la valeur du label et la graine du hash de la fenêtre de ports (§8 du spec).

Leçon de process, transférable : les trois bugs trouvés par la revue finale du Plan 1 sont tous
sortis d'une **manipulation du binaire assemblé sur des configurations adverses**, aucun de la
lecture du code ni des revues par tâche. Prévois au Plan 2 une tâche explicite « exercer le binaire
sur des configs hostiles », plutôt qu'un simple pas-à-pas de vérification nominale.

## 12. Décisions en attente de l'utilisateur

- **Publier `main` sur `origin`** (première publication du dépôt, 34 commits) ou garder en local.
  Rien n'a été poussé.
- **Supprimer ou garder** `.superpowers/sdd/2026-07-27-den-plan1-fondations/` (git-ignoré ; contient
  le ledger et tous les rapports de revue).
- **`run.sh`** est resté non suivi à la racine. Sa version qui était indexée au début de la session
  (et qui différait du working tree) a été sauvegardée dans le scratchpad de la session — donc
  perdue si la session est nettoyée. À trancher : le committer, ou l'ignorer.

## 13. Docs de référence

**Dans ce dépôt (`den`) :**
- `docs/superpowers/specs/2026-07-27-den-cli-design.md` — **LE spec**, à jour (amendé le 2026-07-27).
- `docs/superpowers/plans/2026-07-27-den-plan1-fondations.md` — Plan 1, exécuté ; périmé sur `name:`.
- `README.md` — amorçage utilisateur (`cp -R examples/den-home ~/.den`, puis `den doctor`).

**Dans le dépôt voisin `sbx-devbox`** (monté `:ro` depuis la sandbox — non modifiable de l'intérieur) :
- `docs/design/2026-07-24-sbx-dedicated-repo-design.md` — design du repo de stacks.
- `docs/design/sbx-sandbox-support.md` + `...-challenge.md` — spikes validés + challenge adversarial.
- `stacks/devx/TUTO.md` — le workflow manuel que `den` automatise.

## 14. Prochaine action concrète

~~Écrire le Plan 2~~ — **fait le 2026-07-28** :
`docs/superpowers/plans/2026-07-28-den-plan2-spawn.md` (17 tâches, 128 étapes).

**Prochaine action : l'exécuter.** Le handoff dédié
`docs/superpowers/handoffs/2026-07-28-HANDOFF-plan2-ultracode.md` porte l'ordre des tâches, le
graphe de dépendances (deux vagues parallélisables, le reste séquentiel), l'affectation des modèles
et les cinq pièges qui font échouer un agent naïf sur ce plan.

**Sa tâche 1 doit passer en premier** : elle amende le spec, dont cinq affirmations sont falsifiées
par le sondage `sbx` du 2026-07-28. Tant qu'elle n'est pas faite, le spec du dépôt ment.

Ordre d'implémentation global : ~~`config/` → `nest/`~~ (faits) → `sbx/` (Runner + argv golden
files) → `worktree/` → `agent/`+mixin → `policy/`+settle-loop → `spawn/`+CLI → *(Plan 3)* `ports/`
→ *(Plan 4)* `build`.
