# den — positionnement face à `sbx env` : den compile, sbx exécute

**Date :** 2026-08-24
**Auteur :** Nicolas Gaignoux (conception assistée)
**Issue :** #89 — axe 2 de l'évolution de den face à sbx v0.39.0
**Statut :** validé en brainstorming, **en attente de relecture humaine** avant tout plan
d'implémentation
**Spec mère :** `2026-07-27-den-cli-design.md` — ce document l'étend, il ne la remplace pas. Les
mesures qui le fondent vivent au **§14.4** de la spec mère ; ce document ne les recopie pas, il les
cite.

**Ce document ne décrit aucun code écrit** — la conception qu'il arrête n'est pas implémentée, et
ce spec attend une relecture avant tout plan.

**Mise à jour du 2026-08-25 — cette branche devient le tronc.** Les axes #88 et #90 ne seront pas
fusionnés dans `main` séparément : leur travail arrive ici ou il est perdu. Les deux sont donc
fusionnés dans cette branche, et `internal/` porte désormais leur code. Ce que ça change pour la
lecture de ce spec, et rien d'autre :

- `internal/sbx/argv.go` n'appartient plus à un autre axe. Il porte maintenant les deux drapeaux
  `--cpus` / `--memory` de #90, et ce spec dit au §5.4 lequel de ses deux morceaux survit.
- La cascade `resources:` que #90 a livrée est **une entrée de l'émetteur**, pas un concurrent :
  §5.5 point 7 dit comment elle sort.
- Le travail de #88 (`den doctor` nomme l'état sbx non déclaré) est **orthogonal** au changement de
  moteur : il observe ce que sbx écrit sur la machine, ce que `.sbxenv.yaml` ne touche pas.
- La collision de numérotation `§14.3` entre les trois axes est arbitrée dans la spec mère, par
  ordre d'issue : §14.3 = #88, §14.4 = #89 (ce spec), §14.5 = #90.

---

## 1. La question

sbx v0.39.0 embarque `sbx env` : un modèle déclaratif qui **compose**. Un `.sbxenv.yaml` déclare
l'agent, les kits, les workspaces, les variables d'environnement, les secrets et les liaisons de
credentials — la liste des entrées de spawn de den, article par article. Et `sbx env create`
accepte plusieurs fichiers, fusionnés en profondeur dans l'ordre, sémantique `docker-compose -f`.

C'est la cascade de den, à l'intérieur de sbx. La question est donc de positionnement :
**den continue-t-il de piloter `sbx create`, ou devient-il un générateur de `.sbxenv.yaml` ?**

Elle n'est pas rhétorique. Docker maintient sbx. Toute surface que den maintient et que sbx couvre
désormais nativement est une surface entretenue pour rien.

---

## 2. Le critère de décision

**Le critère retenu est le coût de maintenance, pas la minimisation du risque.**

Ce point est explicite parce qu'il a fait changer la recommandation en cours de conception. Sous un
critère de risque, le statu quo gagne toujours : il ne retire rien mais ne coûte rien non plus.
Sous le critère de maintenance, le statu quo coûte l'entretien perpétuel de tout ce que den fait,
y compris de ce que sbx fait maintenant à sa place.

Un poste de coût domine tous les autres, et il est récurrent : **den re-sonde `sbx create --help`
à chaque release de sbx.** C'est littéralement pourquoi la spec mère porte six relevés datés, du
§14.0 au §14.5. Un argv n'est pas un contrat. Un fichier portant un `schemaVersion`
et validé par un décodeur strict qui **nomme le type refusé** en est un.

---

## 3. Ce que la mesure a tranché

Détail complet et sorties verbatim au **§14.4** de la spec mère. Résumé des quatre inconnues
bloquantes de #89, chacune avec la mesure qui l'a réglée.

### 3.1 `kits:` préserve l'ordre déclaré — OUI

Preuve primaire : la numérotation que sbx assigne lui-même dans la VM, sous
`/etc/durable-startup.d`, mesurée une fois dans chaque sens.

```
001-startup-shell                 ← le kit de l'agent, toujours premier
002-startup-den-probe-kit-a
003-startup-den-probe-kit-b
```

Liste inversée → `002-…-kit-b`, `003-…-kit-a`. L'invariant §9.1 de den — la commande de fraîcheur
en dernière position — est donc **exprimable**.

**Et les séquences CONCATÈNENT à la fusion, elles ne remplacent pas.** C'est la vraie forme de
l'inconnue n°1, et la sémantique docker-compose aurait prédit l'inverse. Sonde discriminante : le
fichier de base déclarait un kit inexistant, la surcharge un kit valide, et c'est l'entrée du
fichier *antérieur* qui a fait échouer la résolution.

Corollaire, qui contraint la §5 : **composer ne dispense pas de contrôler l'ordre d'invocation.**
Le fichier de den doit être passé en dernier, et n'importe quel tiers ajoutant un fichier après lui
casse l'invariant de fraîcheur en silence.

### 3.2 Un `kit:` peut pointer un répertoire généré — OUI

Chemin absolu vers un répertoire écrit à la main : accepté. Un chemin absent : refusé **avant tout
effet de bord** — aucune sandbox créée, aucune image tirée. Le mixin de den, qui vit sous
`<denHome>/cache/mixins/<sandbox>`, est donc adressable tel quel.

### 3.3 `.sbxenv.yaml` ne porte AUCUNE policy réseau — mais le kit, lui, la porte

Balayage exhaustif de `sbxenv.Config`, `sbxenv.SandboxOptions` et `sbxenv.MCPConfig` : aucune clé
réseau. `egress`, `network`, `permissions`, `policy`, `allow`, `deny`, `allowedDomains`,
`deniedDomains` sont toutes refusées.

En revanche un kit listé dans `kits:` porte `permissions.network.allow`, et la règle atteint bien
le moteur de policy scopé à la sandbox via `sbx env create` — mesuré par
`sbx policy check network`, allow sur l'entrée déclarée, `default deny` sur une autre.

**Conséquence structurante : `sbx env` n'offre rien qui remplace le settle-loop du §7.** La
propagation reste asynchrone, aucune sous-commande ne l'attend. C'est la mesure qui rend
l'approche « den générateur pur » incomplète par construction : den doit encore générer un kit et
attendre, quelle que soit la branche retenue.

### 3.4 La sandbox est nommable — OUI, `name:` gagne sur le chemin

Répertoire `merge/`, fichier déclarant `name: den-probe-merge` → `sbx ls --json` répond
`den-probe-merge`. Sans `name:`, le défaut est `<agent>-<basename(dir)>`.

`<nest>[.<instance>]` survit sans concession, et avec lui `den ls`/`sh`/`rm`/`ports`, la policy
scopée, le cache de mixins et la corbeille de worktrees.

### 3.5 Les autres contraintes mesurées qui pèsent sur la conception

- **`env create` n'est pas créer-ou-attacher** : il refuse un nom existant. Créer-ou-attacher,
  c'est `env run`, qui **attache** — donc la branche que le §14.2 a mesurée comme celle qui ouvre
  l'assistant `sbx setup` (`isTerminal(stdin) && isTerminal(stdout)`). den ne l'appellera jamais nu.
- **Les chemins de workspace doivent être absolus ou interpolés.** Un chemin relatif n'a résolu ni
  contre le répertoire du fichier, ni contre le cwd.
- **Un workspace inexistant déclenche une invite interactive**, puis
  `ERROR: user cancelled operation`. En automatisation, ça bloque. den doit donc garantir
  l'existence de chaque chemin avant d'appeler sbx — ce qui est déjà, mot pour mot, sa doctrine
  d'ordonnancement du §6.
- **`sandboxOptions.template` écrase l'image dérivée de l'agent** : les images de `den build` sont
  exprimables.
- **`schemaVersion` n'accepte que `1`**, et les quatre sous-commandes sont **EXPERIMENTAL**.

---

## 4. Les approches instruites

Quatre, la quatrième ajoutée en cours de conception parce qu'elle est la conséquence honnête du
critère de la §2.

### A — den garde `sbx create` (statu quo)

den continue d'assembler l'argv. `sbx env` reste observé, non utilisé.

**Pour.** Zéro travail, zéro pari sur un schéma EXPERIMENTAL, aucune régression possible.

**Contre.** Le coût récurrent de la §2 ne bouge pas : à chaque release de sbx, re-sonder la surface
de drapeaux et propager. Et den continue d'entretenir un format de manifeste, une moitié de
teardown et une part de convergence de secrets que sbx couvre désormais.

**Rejetée.** Elle est correcte sous un critère de risque, et perdante sous le critère retenu.

### B — den compile vers `.sbxenv.yaml` — **RETENUE**

den résout, fabrique les worktrees, génère le kit mixin, **écrit un `.sbxenv.yaml` résolu**, puis
appelle `sbx env create`. Il garde l'UX : l'humain tape `den up`.

**Pour.** La surface en double part réellement (§5.4). Le coût récurrent de la §2 est soldé : den
émet contre un schéma versionné au lieu de suivre un argv. Le pari EXPERIMENTAL est **localisé par
construction** : l'émetteur est un seam, une casse de schéma coûte un fichier, pas une refonte —
l'inverse exact d'aujourd'hui, où une renommée de drapeau traverse `spawn.go`.

**Contre.** Un aller-retour de sérialisation en plus. Une dépendance à un schéma EXPERIMENTAL.
`env create` refusant un nom existant, le verdict créer-ou-attacher se recode chez den (il y est
déjà : den lit `sbx ls` avant de créer).

### C — `den export <nest>` en plus, le spawn inchangé

Le statu quo, plus une commande qui émet un `.sbxenv.yaml` comme **export**.

**Pour.** Achète l'artefact lisible et le scénario « den pas installé » sans céder l'ordonnancement
du §6.

**Contre.** Ne retire rien : le coût récurrent reste entier, et il faut maintenir un second
sérialiseur en plus de l'argv. Le bénéfice est spéculatif — le scénario « den pas installé »
n'existe pas aujourd'hui, et `sbx env create -D` imprime déjà un bloc de résolution lisible.

**Rejetée**, mais elle devient gratuite sous B : l'émetteur existe, `den export` n'est plus qu'un
point d'entrée.

### D — den se rétrécit : il cède l'UX runtime

`den prepare <nest>` fabrique les worktrees, génère le kit mixin et écrit le `.sbxenv.yaml` ;
l'humain tape ensuite `sbx env run` lui-même. `den up`/`sh`/`ls`/`ports`/`rm` disparaissent ou
deviennent des alias minces.

**Pour.** La surface de den fond. Le scénario « den pas installé » devient le chemin normal. Bon
score au test de suppression.

**Contre, et c'est cher.**

1. **Le settle-loop sort du chemin utilisateur.** `sbx env run` attache dès la création ; plus
   personne n'attend la propagation de la policy. Fail-open sur la douleur #1 du §7, et rien dans
   le schéma mesuré ne permet de le rattraper.
2. **Le teardown se scinde en deux moitiés que rien ne lie** — exactement la classe de bug des
   worktrees orphelins que le manifeste (spec `2026-08-05`) avait fermée.
3. **Un `.sbxenv.yaml` périmé spawne faux en silence** : éditer le nest sans relancer `den prepare`
   n'est signalé par rien.
4. **L'avertissement de dérive du mixin disparaît** à l'attache.

**Rejetée pour ce tour.** B la rend bon marché plus tard : une fois l'émetteur en place, céder
l'UX runtime n'est plus qu'une décision.

---

## 5. La conception retenue — B

### 5.1 Le rôle

> den cesse d'être un wrapper autour d'un argv. Il devient un **compilateur** : un `nest` en entrée,
> un `.sbxenv.yaml` et des kits générés en sortie.

La façade — le vocabulaire `nest` / `stack` / `source` — reste à den. L'arrière-cuisine — assembler
la création, enregistrer ce qui a été monté côté sandbox, détruire la VM — passe à sbx.

### 5.2 La séquence de spawn

Réécriture du §6 de la spec mère. Les étapes **1 à 5 sont inchangées** ; seules 6 et suivantes
bougent.

| # | Étape | Propriétaire | Changement |
|---|---|---|---|
| 1 | Résolution de la cascade | den | inchangé |
| 2 | Sélection des repos | den | inchangé |
| 3 | Worktrees (`-w`) | den | inchangé |
| 4 | Profil agent | den | inchangé |
| 5 | Mixin généré | den | inchangé |
| **6** | **Émission du `.sbxenv.yaml`** | **den** | **neuf** — remplace l'assemblage d'argv |
| **7** | **`sbx env create <fichier>`** | **sbx** | remplace `sbx create [flags] AGENT PATH…` |
| 8 | Policy + settle-loop | den | inchangé |
| 9 | SSH | den | inchangé (via le mixin) |
| 10 | Attache `sbx exec -it` | sbx | inchangé — **jamais `sbx env run`** |

La doctrine d'ordonnancement ne bouge pas : tout ce qui est refusable à partir de la seule
configuration est refusé **avant** qu'un worktree existe. Elle gagne même une raison de plus, §3.5 :
un workspace inexistant fait poser une question interactive à sbx, et den doit fermer cette porte
en amont.

### 5.3 Un seul fichier, résolu — décision

**den émet UN `.sbxenv.yaml` déjà résolu, jamais une pile que sbx fusionnerait.**

La mesure tranche. Puisque les séquences concatènent (§3.1), une émission multi-fichiers
obligerait den à contrôler l'ordre des arguments pour toujours, et n'importe quel tiers ajoutant un
fichier après ferait sauter l'invariant du mixin en dernier. Un fichier unique rend l'invariant
**structurel** au lieu de conventionnel — la même raison qui fait que `sbx.Create` sépare
`MixinKit` de `StackKits` en deux champs plutôt qu'en une liste ordonnée par convention.

La cascade reste lisible là où elle est vraie : dans `nests/` et `stacks/`, le vocabulaire de den.

### 5.4 Ce que ça retire, et ce que ça ne retire pas

**Part réellement :**

- `internal/sbx/argv.go` — l'assemblage d'argv, remplacé par un émetteur. Échange à somme nulle en
  volume, gain net en stabilité (§2). Cela inclut les deux drapeaux `--cpus` / `--memory` que
  l'axe 3 y a ajoutés, et leur golden `create-resources.golden` : c'est la moitié **émission** de
  #90, et elle vise le moteur que ce spec retire. Elle reste en place et verte jusqu'à ce que
  l'émetteur existe — un tronc qui pilote encore `sbx create` est correct tant qu'il n'a pas de
  remplaçant, ce qui ne le serait pas, c'est de laisser un lecteur la prendre pour la conception
  visée.
- La **moitié VM** de `internal/cli/rm.go` — `sbx env rm` retire la sandbox et ses secrets scopés.
- La **moitié sbx** de `internal/manifest` — l'enregistrement de ce qui a été monté côté sandbox
  devient le `.sbxenv.yaml` émis, dans un format que sbx sait relire.

**Candidat, mais NON décidé ici :** une part de `internal/converge`. Le schéma porte bien
`secrets:`, `registries:` et `bindings:`, provisionnés au scope de la sandbox et retirés par
`env rm`. Mais le §7 dit que leur cycle de vie réel **n'est pas mesuré**, et ce spec ne verrouille
aucune suppression sur une déduction. La §5.5 point 4 interdit donc à l'émetteur d'écrire ces trois
champs pour l'instant : den continue de converger comme aujourd'hui. **Une sonde dédiée est le
préalable**, et elle mérite sa propre issue — la même règle que pour `ports:`.

**Ne part PAS, et la spec le dit explicitement parce qu'une première analyse l'avait annoncé à
tort :**

- **`internal/manifest` survit, dans sa moitié git.** Le manifeste porte `repos[].mount`, `key`,
  `origin`, `worktree` et `git_dirs` ; `.sbxenv.yaml` ne porte que des chemins de workspace
  résolus, indistinguables entre un worktree, un `.git` et le profil agent. `den rm` ne peut pas
  rendre un worktree sans cette table, et `manifest.LaxMounts` existe précisément pour ne jamais
  deviner (doctrine T13/T16). **Deux enregistrements, deux publics, aucun recouvrement de sens.**
- **La cascade `resources:` de l'axe 3 survit VERBATIM.** `config.Resources`, `mergeResources` /
  `resolveResources` et `sbx.ParseMemory` / `ValidateMemory` / `ValidateCPUs` sont de la résolution
  pure : ils refusent avant le premier effet de bord (§6 de la spec mère) et alimentent
  `sandboxOptions.cpus` / `.memory` exactement comme ils alimentent deux drapeaux aujourd'hui. Le
  changement de moteur ne les touche pas — seule l'écriture change de forme.
- **`spawn.reportResourceDrift` survit VERBATIM**, et pour une raison qui vaut d'être dite : il lit
  l'**enregistrement de création** (`internal/manifest`), jamais `sbx ls --json`. Le moteur en
  dessous lui est donc indifférent. Une VM vivante ne sait toujours pas dire avec quelle taille
  elle a été faite, et `sbx env` n'y change rien.
- `internal/worktree` — c'est du git.
- `internal/policy/settle.go` — aucun champ réseau, aucune attente de propagation (§3.3).
- `internal/agent` — `kits:` liste un kit, il n'en génère aucun et ne garantit pas la position de
  la fraîcheur.
- `internal/build` — `sandboxOptions.template` consomme une image, il n'en fabrique pas.
- `internal/ports` — publication à la demande, l'inverse de `ports:`.
- `internal/source`, `internal/lint`, `internal/doctor`.

### 5.5 L'émetteur — exigences

1. **Version épinglée.** L'émetteur refuse d'émettre contre un `schemaVersion` qu'il n'a pas été
   mesuré contre. `1` est la seule valeur supportée aujourd'hui, et den l'écrit en dur. C'est le
   mécanisme qui rend l'argument du seam vrai plutôt qu'aspirationnel : une évolution de schéma est
   un refus visible, jamais une émission silencieusement fausse.
2. **Chemins absolus, jamais d'interpolation.** den résout tout avant d'émettre. Aucun `${VAR}` ne
   sort de l'émetteur : l'interpolation est une mécanique de sbx, utile à un humain qui écrit à la
   main, inutile et dangereuse dans un fichier généré.
3. **Le mixin en dernier, structurellement.** L'émetteur reçoit les kits de stack et le mixin dans
   deux paramètres distincts, comme `sbx.Create` aujourd'hui, et c'est lui qui les met bout à bout.
4. **Aucune clé que den n'a pas mesurée.** L'émetteur n'écrit que les champs du §14.4.
5. **`ports:` n'est pas émis.** Le modèle de den est la publication à la demande, et le
   comportement de `ports:` à la création n'est pas mesuré (§7).
6. **`secrets:`, `registries:` et `bindings:` ne sont pas émis** tant que leur cycle de vie n'est
   pas mesuré. Même règle que le point 5 : den ne relaie pas un champ dont il ignore l'effet.
7. **`resources:` sort en `sandboxOptions`, et c'est la seule clé de ce bloc que den écrit.**
   La cascade résolue de l'axe 3 alimente `sandboxOptions.cpus` et `sandboxOptions.memory`. Trois
   règles, chacune héritée d'une mesure et non d'un goût :
   - **Une clé absente n'est pas écrite.** `cpus:` est un pointeur précisément pour que « rien de
     déclaré » reste distinguable de `--cpus 0`, que l'aide de sbx documente comme *auto*. Émettre
     `cpus: 0` pour une absence dirait une valeur que l'utilisateur pouvait vouloir écrire.
   - **La validation reste en amont, dans `nest.Resolve`.** Le refus d'un `memory:` trop petit est
     côté serveur et arrive **après** le tirage d'image (§14.5) ; `sbx env create` ne change pas
     cette économie, et le §6 de la spec mère veut le refus avant le premier effet de bord.
   - **`profile:` n'est JAMAIS écrit.** L'axe 3 l'a sondé : un profil vient d'une gouvernance
     distante, `sbx policy profile ls` répond `No policy profiles found`, et il n'existe aucune
     sous-commande pour en créer un. den n'a rien à quoi le faire pointer. Même règle que les
     points 5 et 6 — sauf qu'ici l'absence d'effet est *mesurée*, pas seulement non mesurée.

   `sandboxOptions.template` reste hors de ce point : il porte l'image, il est déjà émis par la
   voie du stack, et `internal/build` la fabrique (§5.4).

### 5.6 Où vit le fichier émis

`<denHome>/state/sandboxes/<sandbox>/` devient un répertoire portant les deux enregistrements :

```
state/sandboxes/<sandbox>/
  .sbxenv.yaml     # ce que sbx a reçu — relisible par sbx env rm
  manifest.yaml    # la table repos → mount, worktree, git_dirs — den seul
```

`state/` reste ce qu'il est : jamais purgé, jamais confondu avec `cache/`.

### 5.7 Un seul moteur — pas de chemin fantôme autour de `sbx env`

**Principe directeur, et il arbitre toutes les tensions qui suivront.** Si den prend `sbx env`
comme moteur, il en accepte les limitations. Il ne construit pas, en parallèle, un second chemin
qui contournerait le moteur au premier inconfort.

C'est le principe qui donne son sens au reste du spec. Un repli permanent vers `sbx create` /
`sbx rm` par le nom, « au cas où », serait un deuxième chemin de création et de destruction à
maintenir, à tester et à garder synchrone — c'est-à-dire précisément le coût que la §2 cherche à
solder. Un moteur qu'on double n'est pas un moteur, c'est une dépendance optionnelle, et une
dépendance optionnelle coûte plus cher que les deux options prises séparément.

Corollaire : **une limitation de `sbx env` se documente, elle ne se contourne pas.** Elle devient
soit une contrainte assumée du spec, soit une sonde à mener, soit une issue amont chez Docker.

### 5.8 `den rm` — le refus est la règle, `--force` est la seule échappatoire

`sbx env rm` résout la sandbox **depuis le jeu de fichiers passé** (§14.4). Le `.sbxenv.yaml` émis
est donc une entrée dure du teardown, et la §5.7 interdit de lui inventer un contournement
silencieux.

**Le chemin normal.** `den rm <sandbox>` lit `state/sandboxes/<sandbox>/.sbxenv.yaml` et appelle
`sbx env rm` avec. Fichier absent, tronqué, ou écrit par un den plus récent ⇒ **den refuse**, en
nommant le fichier fautif et le remède : `den rm --force <sandbox>`. C'est la doctrine ordinaire du
§2 de la spec mère — den refuse plutôt que de normaliser en silence.

**L'échappatoire.** `--force` bascule sur `sbx rm --force <sandbox>`, par le nom de sandbox, qui ne
dépend d'aucun fichier. den **avertit alors** que les secrets scopés n'ont pas été retirés et nomme
la commande qui le ferait. Ce repli est **concédé, pas conçu** : il existe parce qu'une VM qu'on ne
peut plus détruire est un cul-de-sac, pas parce qu'il serait souhaitable.

**Pourquoi ce n'est pas une violation de T13/T16.** La doctrine interdit à `den rm` de laisser
l'utilisateur avec une VM vivante qu'il ne peut plus détruire. Un refus qui **nomme, dans son
propre message, le drapeau qui débloque la même commande** ne produit pas cette impasse : la sortie
est immédiate, documentée, et dans la commande où on se trouve déjà. Ce que T13/T16 interdit, c'est
le refus sans issue.

**L'asymétrie avec la moitié git est délibérée.** `cleanWorktrees` garde son repli sans refus vers
la dérivation (`cleanWorktreesLegacy`) : le worktree est le domaine propre de den, il n'y a pas de
moteur externe à doubler, et rien de ce que fait ce repli n'existe ailleurs. Les deux moitiés
suivent donc deux règles différentes, et pour une raison nommée : côté sbx, un repli serait un
chemin fantôme ; côté git, c'est le chemin unique.

**Ce qui ne bouge pas :** den ne supprime jamais un fichier qu'il n'a pas su lire — il peut
appartenir à un den plus récent (§11 de la spec mère).

### 5.9 `--force` porte deux sens, et c'est assumé

Aujourd'hui `--force` veut dire « retire les worktrees même sales ». Il veut désormais dire aussi
« détruis par le nom quand l'enregistrement est illisible ». **Un seul drapeau, deux sens :
tranché le 2026-08-24.**

Rejetés : un drapeau dédié `--by-name` — une surface de plus pour un cas de panne, et `den rm` en
porte déjà deux ; et l'absence totale d'échappatoire — la lecture la plus stricte de la §5.7, qui
produit exactement l'impasse que T13/T16 interdit.

La raison du choix : c'est le seul qui n'ajoute rien. Les deux sens portent la même intention —
*passe outre, je sais ce que je fais* — et un drapeau par mode de panne ferait de `den rm` un
tableau de bord au lieu d'une commande.

**Le prix, et la contrepartie obligatoire.** L'utilisateur qui force pour la première raison hérite
de la seconde sans l'avoir demandée. den doit donc **dire lequel des deux sens il a exercé**, à
chaque fois, plutôt que de l'appliquer en silence. Deux exigences sur les messages :

1. Quand `--force` sert au second sens, den l'annonce avant d'agir : l'enregistrement est
   illisible, la destruction se fait par le nom, les secrets scopés restent en place — et la
   commande qui les retirerait est nommée.
2. Quand `--force` ne sert qu'au premier sens, den ne dit rien du second. Un avertissement qui se
   déclenche toujours n'est plus lu, et la §2 de la spec mère refuse le bruit autant que le
   silence.

Cette exigence n'est pas cosmétique : elle est ce qui rend le double sens acceptable. Sans elle,
`--force` deviendrait un interrupteur dont l'utilisateur ne connaît plus la portée.

---

## 6. L'impact sur les sources d'équipe

Extension de la spec `2026-08-04-stack-sources-design.md`. C'est là que le challenge est le plus
légitime : si sbx sait composer des fichiers, pourquoi une équipe ne distribuerait-elle pas un repo
de `.sbxenv.yaml` sans den ?

### 6.1 Ce qu'un repo de `.sbxenv.yaml` sait déjà faire

Beaucoup. Les `kits/` d'une source sont **déjà** des artefacts natifs sbx : ils passent tels quels
dans `kits:`, den ou pas den. Pour un projet mono-repo à chemin fixe, un fichier d'équipe plus une
surcharge personnelle couvrent le besoin, interpolation comprise. **Sur ce terrain, den n'apporte
rien, et il faut l'admettre.**

### 6.2 Ce qui casse, et c'est structurel

Les chemins de `workspace.path` et `additionalWorkspaces[].path` sont des **chemins hôte absolus**
(§3.5). Seule `${VAR}` permet d'y échapper, ce qui déplace la discipline sur chaque poste : chaque
coéquipier doit exporter les bonnes variables, dans le bon shell, avant chaque commande.

C'est exactement le problème que les *repo keys* résolvent. Un nest d'équipe ne porte aucun chemin
machine : `repos: [{key: kafoutche-back}]` d'un côté, le mapping personnel dans
`source-config/<source>.yaml` de l'autre. Une clé non mappée est un **refus avant tout effet de
bord**, qui nomme le fichier à corriger. Le même cas côté `.sbxenv.yaml` nu, c'est l'invite
interactive — ou pire, un `y` distrait qui crée un répertoire vide et monte du néant dans la VM.

### 6.3 Le sort de chaque répertoire d'une source

| Contenu | Sort |
|---|---|
| `kits/` | **Inchangé.** Déjà natif sbx. Le seul répertoire qu'une équipe pourrait distribuer sans den, et il le reste. |
| `nests/` | **Renforcé, non remplaçable.** C'est le vocabulaire que `sbxenv.Config` refuse : ni `repos:`, ni `key:`, ni `egress:`, ni fenêtre de ports. C'est aussi le seul niveau où une déclaration reste vraie sur toutes les machines. |
| `stacks/`, `lib/` | **Inchangés.** `sandboxOptions.template` consomme une image, il n'en fabrique pas. |
| `den-source.yaml`, `source-config/` | **Inchangés.** Le mapping clé → chemin est ce qui rend un nest partageable. |
| Adressage `<source>:<nom>` | **Intact.** `name:` est un champ du schéma, `corp:backend` continue de s'aplatir en `corp-backend`. |

### 6.4 `den lint` gagne un travail

`internal/lint` est le juge unique partagé par `den lint`, `den source add` et `den source update`
— un seul juge, pour que lint ne puisse jamais accepter ce qu'un spawn refuserait ensuite.

Sous le rôle de compilateur, cette propriété exige une vérification de plus : **un nest doit
compiler vers un `.sbxenv.yaml` légal**. Concrètement, lint vérifie que le nom de sandbox dérivé
respecte le charset de `sbx create --name`, et que chaque référence de kit résout vers un
répertoire. Sans ça, une source valide au sens actuel pourrait produire une émission que
`sbx env create` refuse — exactement la divergence que le juge unique existe pour empêcher.

### 6.5 Verdict

**La valeur des sources monte.** Ce que sbx ne sait pas partager — une déclaration indépendante de
la machine — est précisément ce qu'une source expédie. Ce qu'il sait partager — les kits — une
source l'expédiait déjà sans rien lui devoir.

---

## 7. Ce qui n'est pas mesuré, et qui pèse sur ce spec

Le dépôt atteste le comportement de `sbx`, il ne l'extrapole pas.

- **Le comportement de `ports:`** — le schéma est connu (`host`, `sandbox`, `protocol`), la
  publication à la création est **déduite**, pas observée. C'est pourquoi la §5.5 interdit de
  l'émettre : den ne relaie pas un champ dont il ignore l'effet.
- **Le cycle de vie réel de `secrets:`, `registries:` et `bindings:`** au-delà de leur forme YAML.
  C'est ce qui empêche la §5.4 de verrouiller le départ d'une part de `internal/converge` : le
  candidat est nommé, la décision attend une sonde dédiée et sa propre issue.
- **Ce que `sbx env` fait d'un kit dont la validation échoue APRÈS la création de la VM.** La
  résolution de chemin, elle, tombe avant (§3.2) ; la validation de contenu n'a pas été sondée.
- **La stabilité de `schemaVersion 1`** — c'est une prédiction, pas un fait. La §5.5 point 1 est la
  réponse de conception à cette incertitude.
- **Le coût réel de l'émetteur en lignes de code** — estimé comme un échange à somme nulle avec
  `argv.go`, non chiffré.

---

## 8. Décisions verrouillées

1. **den compile.** Le spawn passe par `.sbxenv.yaml` + `sbx env create`, pas par un argv
   `sbx create`. (§5.1, §5.2)
2. **Un seul fichier, déjà résolu.** Jamais une pile fusionnée par sbx. (§5.3)
3. **den ne perd aucune responsabilité de sécurité.** Le mixin généré, l'egress et le settle-loop
   du §7 restent à den, parce que le schéma ne sait pas les porter. (§3.3)
4. **`sbx env run` n'est jamais appelé.** den attache par `sbx exec -it`, comme aujourd'hui.
   (§3.5)
5. **`schemaVersion` est épinglé à `1` et vérifié.** Une valeur non mesurée est un refus. (§5.5)
6. **Le manifeste git survit** à côté du fichier émis. (§5.4, §5.6)
7. **Un seul moteur, aucun chemin fantôme.** Prendre `sbx env` comme moteur, c'est en accepter les
   limitations. Une limitation se documente, se sonde ou remonte chez Docker — elle ne se contourne
   pas par un second chemin permanent. (§5.7)
8. **`den rm` refuse quand l'enregistrement est illisible**, en nommant `--force` comme remède.
   `--force` bascule sur `sbx rm --force <sandbox>` par le nom et avertit que les secrets scopés
   n'ont pas été retirés. Le repli est concédé, pas conçu. La moitié git garde son repli sans
   refus, et l'asymétrie est délibérée. (§5.8)
9. **`ports:` n'est pas émis** tant que son effet n'est pas mesuré. (§5.5, §7)
10. **`secrets:`, `registries:`, `bindings:` ne sont pas émis** tant que leur cycle de vie n'est
    pas mesuré ; la part de `internal/converge` qui pourrait partir reste un candidat, pas une
    décision. (§5.4, §5.5, §7)
11. **Les sources ne changent pas de forme.** Seul `den lint` gagne une vérification. (§6)
12. **`--force` porte deux sens, sur un seul drapeau.** Contrepartie obligatoire : den annonce
    lequel des deux il exerce, et se tait sur le second quand il ne s'applique pas. (§5.9)
13. **La cascade `resources:` survit et sort en `sandboxOptions`.** Clé absente non écrite,
    validation en amont dans `nest.Resolve`, `profile:` jamais écrit. Seule la moitié **émission**
    de #90 — les deux drapeaux dans `argv.go` et leur golden — vise le moteur retiré ; elle reste
    verte jusqu'à ce que l'émetteur existe. (§5.4, §5.5 point 7)

Aucune décision de ce spec n'est laissée ouverte.

---

## 9. Ce que ce spec ne fait pas

- Aucun plan d'implémentation. Il vient après relecture humaine, jamais avant (#89).
- Aucune modification de `internal/**`, ni de `CLAUDE.md`, ni d'une sous-section §14 existante.
- Le blocage de `den exec -T` reste dans **#87**. La gouvernance et les collisions restent dans
  **#88**. Les drapeaux de `sbx create` — `--cpus`, `--memory`, `--profile` — restent dans **#90** ;
  ils deviennent sous B des champs de `sandboxOptions`, ce qui est une convergence heureuse mais ne
  déplace pas la frontière des axes.
