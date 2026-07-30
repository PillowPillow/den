# HANDOFF — Exécution du Plan 2 (`den`) en mode ultracode

> Pour l'agent qui reprend **sans contexte de conversation**, avec l'orchestration multi-agents
> activée. Lis ce fichier en entier, puis `HANDOFF.md`, puis le spec, puis le plan. Réponds en
> français (préférence utilisateur).
>
> Écrit le **2026-07-28**, à la fin de la rédaction du Plan 2.

## 0. TL;DR

- **Le Plan 2 est écrit, revu, et PAS exécuté.** Une seule ligne de code du plan n'existe encore.
- **Fichier :** `docs/superpowers/plans/2026-07-28-den-plan2-spawn.md` — 17 tâches, 128 étapes.
- **Rien n'est committé** de la session du 2026-07-28 : le plan lui-même est un fichier non suivi,
  et `run.sh` l'est toujours aussi (décision utilisateur en attente, cf. `HANDOFF.md` §12).
- **Ta première action :** lire le plan en entier, puis exécuter la **tâche 1** (amendement du spec)
  **avant** de lire le spec comme une source de vérité. Cinq de ses affirmations sont fausses et la
  tâche 1 existe pour ça.
- **Contrainte dure :** `sbx` **n'est pas installé** dans cette sandbox. Aucun agent ne peut vérifier
  quoi que ce soit contre le vrai `sbx`. Tout agent qui rapporte « j'ai vérifié que le spawn
  fonctionne » se trompe ou ment.

## 1. État exact du dépôt

```
Branche : main, 35 commits, AUCUN push (origin configuré mais vide, pas d'upstream)
Suivi   : docs/superpowers/plans/2026-07-28-den-plan2-spawn.md  → NON COMMITTÉ
          run.sh                                                → NON SUIVI (décision en attente)
Code    : Plan 1 intégralement livré et vert (4 paquets, suite hermétique)
          Plan 2 : rien
```

Vérifie avant de commencer :

```bash
cd /Users/polochon/Development/Pillow/den
go test -count=1 ./... && go vet ./... && gofmt -l .
```
Attendu : 4 paquets `ok`, `vet` silencieux, `gofmt -l` n'imprime rien. Si c'est rouge **avant** que
tu aies touché à quoi que ce soit, arrête-toi et signale-le — ce n'est pas prévu.

## 2. Ce que la session du 2026-07-28 a produit (et pourquoi ça compte)

L'utilisateur a sondé la CLI `sbx` réelle sur sa machine. Ce sondage a **falsifié trois décisions
verrouillées** du spec. Ce ne sont pas des préférences, ce sont des faits :

| Ce que le spec disait | Ce qui est vrai | Conséquence |
|---|---|---|
| État par labels (`den.managed=1`…), décision n°10 | **`sbx create` n'a aucun `--label`** | l'identité passe par le nom `<nest>.<worktree>` |
| `den ls` affiche l'âge (§5) | `sbx ls --json` n'a **aucun champ de date** | colonne supprimée |
| Kits : `network.allow` et `env` | Vrai schéma : **`caps.network.allow`** et **`environment.variables`** | tout le rendu du mixin |

Plus un fait qui n'apparaissait nulle part : **`sbx create --name` refuse `/` et `_`** (« letters,
numbers, hyphens, periods, plus signs and minus signs »). Un `-w feature/123` — la forme la plus
courante d'un nom de branche — aurait produit un nom illégal. Le plan le **refuse** explicitement
plutôt que de le normaliser : normaliser casserait l'aller-retour `den <nest> -w <wt>` → nom de
sandbox → `den ls`, qui est désormais le seul porteur d'état.

**La surface `sbx` complète et vérifiée est en tête du Plan 2**, section « Faits `sbx` établis ».
Ne la redécouvre pas, ne la contredis pas, et surtout **ne cherche pas à la re-sonder** : `sbx` est
absent d'ici, tu obtiendrais `command not found` et conclurais n'importe quoi.

## 3. Décisions prises avec l'utilisateur — ne pas re-litiger

1. **Séparateur `.`** entre nest et worktree, `.` interdit dans les deux noms. Motif : avec `-`,
   `mon-api-feat` est ambigu, et lever l'ambiguïté exigerait de consulter la liste des nests — une
   sandbox deviendrait inattribuable dès la suppression de son nest.
2. **Kits transverses par stack** : `kits: [...]` dans `stack.yaml`, chemins relatifs au dossier de
   la stack. `policy-baseline` **disparaît** en tant que kit (son contenu est déjà `egress:` dans
   `config.yaml`).
3. **Périmètre Plan 2** = celui du handoff d'origine. `den ports` et le flag `-i` sont **repoussés au
   Plan 3**, `den build` au Plan 4.
4. **Nest illisible** : `ListNests` liste les sains et signale les cassés ; `LoadNest` reste dur.
5. **Attache par `sbx exec -it … bash -l`**, jamais `sbx run` (qui lance le flavor de l'image).
6. **Agent positionnel `shell`** au `sbx create`.

## 4. Les cinq pièges qui feront échouer un agent naïf

Par ordre de probabilité décroissante. Le premier est celui qui te coûtera le plus cher.

### 4.1 Régénérer un golden file pour faire passer un test

C'est **le** mode d'échec de ce plan. Trois tâches produisent des golden files (T2, T7, T9), et
chacune verrouille un invariant qui ne se voit qu'au boot d'une VM. Un sous-agent bloqué sur un test
golden rouge va vouloir régénérer le fichier attendu. C'est presque toujours faux.

**Règle non négociable :** un golden ne change **qu'après** que les assertions sémantiques dédiées
(qui existent, séparément, dans chaque tâche) sont vertes. Si `TestCommandeFraicheurNExpansePasHOME`
échoue, le golden n'est pas le problème — le code a expansé `$HOME` côté hôte, et la sandbox
démarrera avec un PATH pointant sur un chemin qui n'existe pas dans la VM.

Écris cette règle dans le brief de **chaque** sous-agent qui touche à un golden.

### 4.2 Croire qu'on a vérifié quelque chose

`sbx` est absent. `Fake.Attach` n'ouvre aucun tty. Aucun test n'exécute un vrai `create`. Les golden
files figent **ce que `--help` permet de déduire**, pas ce que sbx accepte.

Un rapport de tâche qui dit « le spawn fonctionne » est faux. Le formulaire correct est « la suite
est verte et l'argv assemblé correspond au golden ». La différence n'est pas cosmétique : le premier
spawn réel est un test qui n'a pas encore été passé, et le Plan 2 le dit en section « Risques ».

### 4.3 Paralléliser l'implémentation sur un module Go partagé

Voir §6. Les tâches 13/14/15 modifient toutes `internal/cli/root.go`. Lancées en parallèle sans
worktrees isolés, elles se marchent dessus ; avec worktrees isolés, elles produisent trois versions
divergentes du même fichier qu'il faudra fusionner à la main.

### 4.4 Contredire une décision verrouillée parce qu'elle paraît sous-optimale

Le séparateur `.`, l'agent positionnel `shell`, la disparition de `policy-baseline`, le refus de
normaliser `feature/123` : chacune a une justification écrite. Un agent qui « améliore » l'une
d'elles casse un invariant documenté ailleurs. Si une décision te paraît fausse, **signale-la**,
n'agis pas dessus.

### 4.5 Écrire en anglais

Messages utilisateur, commentaires, messages de commit : **français**. Erreurs au format
`contexte : détail`, nommant toujours le chemin complet et listant les valeurs disponibles. C'est
une convention établie par le Plan 1 sur ~900 lignes de code ; la rompre rend le produit incohérent.

## 5. Affectation des modèles

Le principe : **le modèle suit l'enjeu, pas la taille**. Une tâche de 40 lignes qui porte une
propriété de sûreté mérite plus qu'une tâche de 200 lignes d'affichage.

Trois critères font monter en gamme : (a) la sortie est **consommée par la VM** et non par un test,
(b) l'erreur est **invisible avant le boot réel**, (c) l'opération est **destructive**.

| # | Tâche | Modèle | Effort | Pourquoi ce niveau |
|---|---|---|---|---|
| 1 | Amender le spec | Sonnet | medium | Édits textuels entièrement spécifiés ; le Step 8 les vérifie par `grep` |
| 2 | `CommandeFraicheur` + golden §9.1 | **Opus** | high | (a)+(b) — le script part dans la VM ; un `$HOME` expansé ne se voit qu'au boot |
| 3 | Noms de sandbox | Sonnet | medium | Fonction pure, tests exhaustifs fournis |
| 4 | `Resolved.Env` + `DenHome` | Sonnet | medium | Cascade simple, tests fournis |
| 5 | `Stack.Kits` | Sonnet | low | Trois lignes de code, un champ |
| 6 | `Runner` + `Fake` | Sonnet | medium | Mécanique, mais **relire** : tout le reste en dépend |
| 7 | Mixin généré + golden | **Opus** | high | (a)+(b) — déterminisme `yaml.Node`, sortie lue par sbx |
| 8 | `sbx.Ls` | Sonnet | medium | Décodage JSON d'un schéma connu |
| 9 | `ArgvCreate` + goldens | **Opus** | high | (b) — l'ordre des `--kit` est l'erreur la plus coûteuse du plan |
| 10 | `internal/worktree` | **Opus** | high | (c) — pilote un vrai `git`, supprime des dossiers |
| 11 | `policy.Settle` | **Opus** | high | Propriété de sûreté fail-closed ; le piège du `*bool` est subtil |
| 12 | `den <nest>` (orchestration) | **Opus** | **xhigh** | La logique la plus dense ; l'**ordre** settle→attache est une propriété de sûreté |
| 13 | `den ls` | Sonnet | medium | Affichage |
| 14 | `den sh` | Sonnet | medium | Câblage mince |
| 15 | `den rm` | **Opus** | high | (c) — détruit des worktrees ; l'ordre de destruction protège le travail non commité |
| 16 | `ListNests` tolérante | Sonnet | medium | Changement de signature mécanique, appelants listés |
| 17 | Configs hostiles | **Opus** | **max** | C'est **la** tâche qui a trouvé les trois bugs du Plan 1 |
| — | Revue **par tâche** | **Opus** | high | |
| — | Revue **finale de branche** | **Opus** | xhigh | |

**Haiku** : je ne lui confierais aucune tâche de ce plan. Son usage rentable ici serait purement
mécanique (lancer la suite, rapporter un `git status`), et ces gestes coûtent moins cher inline
qu'en délégation.

**Fable** : je n'ai pas de base pour l'affecter à une tâche de ce plan plutôt qu'à une autre. Plutôt
que d'inventer une justification, je le laisse de côté — si l'utilisateur veut l'essayer, la tâche
la moins risquée serait la 5 ou la 14, où un écart se voit immédiatement au test.

**Ne descends pas en gamme sur les tâches 2, 7, 9, 11, 12 et 17 pour économiser.** Ce sont les six
qui portent la sûreté du produit, et cinq d'entre elles produisent du code dont l'erreur est
**silencieuse** jusqu'au premier boot d'une microVM.

## 6. Comment orchestrer — et où l'orchestration ne sert à rien

Sois honnête sur ce point : **ce plan est majoritairement séquentiel**. Le fan-out n'aide que par
endroits, et l'appliquer partout produirait des conflits de fusion sur un module Go unique.

### 6.1 Ce qui parallélise vraiment

Le graphe de dépendances autorise deux vagues :

```
T1 (spec) ────────────────── bloque tout
   │
   ├── vague A (5 agents) : T2 · T3 · T4 · T5 · T6 · T16
   │      fichiers disjoints : agent/ · sbx+config/nom · nest/resolve · config/stack · sbx/runner · nest/nest
   │
   ├── vague B (4 agents) : T7 (←2,4) · T8 (←6) · T10 (←3) · T11 (←6)
   │
   ├── T9  (←3,4,5,7)   séquentiel
   ├── T12 (←7,8,9,10,11) séquentiel — le point de convergence
   ├── T13 · T14 · T15   ⚠️ SÉQUENTIELS : les trois modifient internal/cli/root.go
   └── T17               séquentiel, en dernier
```

⚠️ **Piège de la vague A** : T3 modifie `internal/config/nom.go` **et** `internal/nest/nest.go`,
pendant que T5 modifie `internal/config/stack.go` et T4 `internal/nest/resolve.go`. Les fichiers
sont disjoints, donc fusionnables — mais chaque agent lance `go test ./...` dans son propre worktree
et ne voit pas les autres. **Prévois une étape d'intégration** après chaque vague : fusionner, puis
relancer la suite complète. Un vert obtenu en isolation ne prouve rien sur l'assemblage.

Si tu utilises `isolation: "worktree"`, sache qu'elle coûte ~200-500 ms et du disque par agent :
elle se justifie pour la vague A et B, pas pour les tâches séquentielles.

### 6.2 Ce qui parallélise le mieux : la vérification, pas la production

C'est là que l'ultracode paie sur ce projet. Le Plan 1 l'a mesuré : **les trois bugs trouvés par la
revue finale sont tous sortis d'une manipulation du binaire assemblé sur des configurations
adverses** — aucun de la lecture du code, aucun des revues par tâche.

Deux fan-outs à haute valeur :

- **Tâche 17** : les 12 configurations hostiles du tableau sont **indépendantes**. Un agent par
  ligne, chacun construisant le binaire et exerçant son cas, puis synthèse. C'est le fan-out le plus
  rentable du plan.
- **Revue adversariale** après T12 et en fin de branche : plusieurs vérificateurs à **lentilles
  distinctes** plutôt que N copies du même relecteur. Lentilles utiles ici : *frontière hôte/VM*
  (un chemin hôte a-t-il fuité vers la VM, ou l'inverse ?), *fail-closed* (existe-t-il un chemin qui
  attache sans que la policy soit passée ?), *destructivité* (un `rm` peut-il perdre du travail non
  commité ?), *déterminisme* (une map itérée sans tri ?).

### 6.3 Ce que je ne parallélarais pas

L'implémentation de T12. C'est le point de convergence de six modules ; la découper entre plusieurs
agents recréerait le problème que le plan a précisément résolu en la sortant de `internal/cli`.

## 7. Méthode par tâche (héritée du Plan 1, elle a marché)

Un sous-agent neuf par tâche, avec dans son brief :

1. Le chemin du plan **et le numéro de tâche** — pas le contenu recopié, il doit lire la source.
2. Les **Global Constraints** du plan (elles ne sont pas répétées dans chaque tâche).
3. La règle des golden files (§4.1 ci-dessus) si la tâche en touche un.
4. L'interdiction d'invoquer `sbx` et de faire du réseau dans les tests.
5. L'obligation de rapporter la **sortie réelle** des commandes de vérification, pas un résumé.

Puis une **revue de tâche** avant de passer à la suivante. Le Plan 1 a déclenché quatre rounds de
correction par ce mécanisme ; c'est le rendement principal de la méthode.

Le journal du Plan 1 est dans `.superpowers/sdd/2026-07-27-den-plan1-fondations/` (dossier
git-ignoré, décision de suppression toujours en attente). Le ledger `progress.md` consigne tous les
arbitrages — lis-le s'il est encore là.

## 8. Ce qui restera vrai après le Plan 2

À écrire dans le handoff de fin d'exécution, sans l'oublier :

- **Le smoke e2e n'aura pas eu lieu.** C'est le premier geste après le plan, sur une machine où
  `sbx` existe. Deux points s'y révéleront et pas avant : l'ordre réel de layering des `--kit`
  (à lire dans `/var/log/sbx-kit-startup.log`) et le fonctionnement de `ssh.mode: agent-forward`,
  qui suppose que sbx forwarde le socket tout seul — plausible d'après `run.sh`, jamais vérifié.
- **Plan 3** = `internal/ports` + `den ports` + le flag `-i`. **Plan 4** = `den build` et le DAG.
- Les décisions utilisateur toujours en attente : publier `main` sur `origin`, supprimer ou garder
  `.superpowers/sdd/…`, committer ou ignorer `run.sh`.

## 9. Ordre de lecture

1. Ce fichier.
2. `docs/superpowers/handoffs/HANDOFF.md` — contexte du Plan 1, vocabulaire, faits sbx des spikes,
   dette parquée. Son §14 est périmé (il annonce l'écriture du Plan 2, qui est faite).
3. `docs/superpowers/plans/2026-07-28-den-plan2-spawn.md` — **en entier avant la première tâche**,
   y compris les sections « Faits sbx établis », « Décisions de ce plan » et « Risques connus ».
4. `docs/superpowers/specs/2026-07-27-den-cli-design.md` — **seulement après la tâche 1**.
