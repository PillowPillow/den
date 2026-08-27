# HANDOFF — Axe 1 : converge nomme l'état sbx que personne ne déclare (issue #88)

> Pour l'agent qui prend cet axe **sans contexte de conversation**. Lis ce fichier, puis
> `CLAUDE.md`, puis le spec §14.2. Réponds en français ; écris le code, les commentaires et les
> messages utilisateur **en anglais**.
>
> Écrit le **2026-08-24**. Les handoffs datés ne sont jamais réécrits : si ce fichier décrit un état
> antérieur au tien, c'est un artefact historique. L'ordre de confiance est **le code, puis le spec,
> puis `CLAUDE.md`**, les handoffs en dernier.

## ⚠️ Tu n'es pas seul — trois axes tournent EN PARALLÈLE

Trois worktrees avancent en même temps sur trois issues issues de la même sonde (#87) :

| Axe | Issue | Branche | Nature |
|---|---|---|---|
| 1 — gouvernance | **#88** | `feat/converge-names-undeclared-sbx-state` | **toi** |
| 2 — positionnement `sbx env` | #89 | `spec/sbx-env-positioning` | spec, aucun code |
| 3 — `resources:` dans la cascade | #90 | `feat/resources-in-cascade` | code |

**Trois règles, à lire avant d'ouvrir un fichier.**

1. **Le spec §14 est en APPEND-ONLY cette semaine.** Si tu mesures quelque chose contre un `sbx`
   réel, tu ajoutes une **nouvelle sous-section datée** portant ton numéro d'issue
   (« ### Sonde du 2026-XX-XX — #88 … »). Tu ne modifies **jamais** une sous-section existante :
   l'axe 3 écrit dans le même fichier, et deux réécritures au même endroit produisent un conflit que
   personne ne sait arbitrer sans les deux contextes.
2. **Tu ne touches PAS `CLAUDE.md`.** Les trois axes voudraient tous y écrire. Les mises à jour de
   `CLAUDE.md` se font dans une PR d'intégration séparée, après les merges. Note ce que tu voudrais
   y voir dans la description de ta PR.
3. **Périmètre de fichiers : voir §4.** L'axe 3 possède `internal/sbx/argv.go`. Tu ne l'ouvres pas.

## 1. Ta mission, en une phrase

`~/.den` est censé être la source unique de vérité. sbx v0.39.0 embarque **trois autres auteurs**
qui écrivent sur la même machine sans que den le dise. Rends cet état **visible**, sans jamais rien
supprimer.

## 2. Ce qui est MESURÉ, et sur quoi tu peux t'appuyer

Tout ceci est consigné au **spec §14.2** (arrivé par la PR #91). Relis-le, il porte les tableaux.

- L'assistant `sbx setup` est **unique par machine**. Son marqueur
  `~/Library/Application Support/com.docker.sandboxes/sandboxes/first-run-import.json` porte
  `{"offeredAt": …}` — *proposé*, pas *accepté*. Un `[q]` le clôt définitivement.
- Sur cette machine, le `[q]` du 2026-08-23 **n'a rien importé** : le magasin
  `…/sandboxes/agent-skills/` est **vide**, et la configuration SSH de sbx
  (`~/.ssh/sandboxes/`) **n'existe pas**. La collision est *latente*, pas active.
- `--no-share-skills` est accepté par `sbx create` mais **inerte** : derrière un feature gate
  éteint, deux sandboxes créées avec et sans le drapeau sont indiscernables.
- `sbx secret ls -g` **n'a pas de sortie JSON** (sondé en v0.38.0). C'est pour ça que
  `parseSecretList` est un analyseur de texte ancré sur les positions de colonnes de l'en-tête.

## 3. Le problème, précisément

`converge.ReadSbxState` (`internal/converge/sbx.go`) lit déjà l'inventaire des secrets globaux.
Aujourd'hui den est **muet** sur trois surfaces :

- **secrets** — `sbx setup` / `sbx secret set` écrivent dans le magasin global que
  `ReadSbxState` parse déjà. den ne les supprime pas, et c'est bien ; mais rien ne les **nomme**
  non plus.
- **skills** — `sbx skills import` alimente un magasin persistant partagé. den provisionne
  déjà des skills par le profil agent (`~/.den/agents/claude`, monté en workspace dans chaque
  sandbox den — visible dans `sbx ls --json`). Deux propriétaires pour un concept.
- **serveurs MCP** — `sbx mcp add` tient un registre. Chaque serveur qu'une sandbox peut joindre
  est un fait réseau que l'allowlist fail-closed de den ignore.

Le silence est la mauvaise réponse : le spec §2 dit que den **refuse plutôt que de normaliser en
silence**. Ici, ni refus ni normalisation — juste un angle mort.

## 4. Ton périmètre EXACT

**Tu possèdes, en écriture :**

- `internal/converge/**` — `SbxState`, `ReadSbxState`, `parseSecretList`, le modèle, le rendu
- `internal/doctor/**` — si c'est là que le verdict s'affiche
- `internal/sbx/fake.go` — **et c'est un point de coordination, pas une simple possession** (§6)
- tout nouveau fichier `internal/sbx/<surface>.go` que tes lecteurs exigent (`mcp.go`, `skills.go`)
- `docs/superpowers/specs/…` — **en ajout seul**, nouvelle sous-section datée #88

**Tu ne touches pas :**

- `internal/sbx/argv.go` et ses goldens — **axe 3**
- `internal/config/**`, `internal/nest/**`, `internal/spawn/**` — **axe 3**
- `CLAUDE.md` — PR d'intégration
- toute sous-section §14 déjà écrite

## 5. Le travail

**Converge nomme la dérive.** Rapporte les entrées qu'aucune source ne déclare comme
**« present, undeclared »** au lieu de ne pas en parler. Rien n'est supprimé : den n'efface jamais
ce qu'il n'a pas créé.

Une seule mécanique couvre secrets, skills, MCP — et ce que sbx inventera ensuite. Cette généralité
**est** l'objectif : un correctif par surface serait à réécrire en v0.40.0.

À trancher en implémentant, et à justifier par un commentaire au point de décision (c'est le style
dominant du dépôt) :

- Le rapport va-t-il dans `den doctor`, dans la sortie de converge, ou dans les deux ?
- « present, undeclared » est-il un `[warn]` ou une simple information ? Ce n'est **pas** une
  erreur : un utilisateur peut légitimement garder des secrets hôte que den ne gère pas.
- Avant d'écrire un deuxième analyseur de texte ancré sur en-tête, **vérifie si `sbx mcp ls` et
  `sbx skills ls` acceptent `--json`**. Sonde-le, ne le suppose pas. Si oui, `internal/sbx/json.go`
  et `DecodeJSON` existent déjà. Si non, lis le commentaire de `parseSecretList` avant d'en écrire
  un autre : il explique pourquoi l'ancrage se fait sur la position de la **deuxième colonne** de
  l'en-tête et jamais sur un index de champ.
- `ReadSbxState` a une doctrine explicite : **un échec est une erreur, jamais un état vide**. Un
  état vide se lirait « rien n'est configuré », ce qui ferait proposer à den de créer des
  identifiants qui existent. Tes nouveaux lecteurs suivent la même règle. Et `Observation` distingue
  « den n'a pas pu regarder » de « den a regardé et n'a rien trouvé » — deux faits, deux remèdes.

## 6. ⚠️ Le seul vrai point de collision : `internal/sbx/fake.go`

`sbx.Fake` est un **fichier de production**, pas un `_test.go` : `policy`, `cli` et `agent` le
consomment tous (`CLAUDE.md` le dit, ne le déplace pas). Son commentaire (`fake.go:43-50`) prévient
que `Calls` **confond toutes les méthodes** et qu'un `Run` dont l'argv commence par `"exec"` est
irréconciliable.

Conséquence pour le parallélisme : **l'axe 3 ne modifie pas ce fichier, mais ses tests tournent
contre lui.** Tu peux donc casser sa suite sans toucher un fichier qu'il possède.

La règle : **ajouter** un comportement de dispatch à `Fake` est libre. **Changer** un comportement
existant est un événement « stop et coordonne » — tu préviens avant, pas après. Un conflit de merge
se résout ; une régression sémantique silencieuse chez le voisin, non.

## 7. Ce qui n'est PAS dans ton axe

- **Le blocage de `den exec -T`.** L'assistant s'ouvre quand `isTerminal(stdin) && isTerminal(stdout)`
  — les **descripteurs**, pas le drapeau `-it`. La branche Pipe de `spawn.Enter` passe les
  descripteurs du terminal tels quels, donc `den exec -T` / `den run -T` tapés sur un terminal
  **bloquent** sur une machine jamais sollicitée. C'est un bug du chemin de spawn : il reste
  dans **#87**.
- **`--no-share-skills`.** Mesuré inerte. **Reporté** par décision explicite jusqu'à ce que le
  feature gate s'ouvre — pas déplacé vers l'axe 3, abandonné pour ce tour. Livrer un drapeau sans
  effet observable n'est pas un gain, et le §14.2 porte la mesure qui le justifie.
- **Les sondes 3 et 4 de #87.** Elles exigent de répondre `[enter]`, ce qui importe pour de vrai
  13 serveurs MCP et les skills de l'hôte. C'est la seule sonde dont le prix est une pollution
  réelle — et ta mécanique répond à la même question gratuitement.
- **`sbx env`** — axe 2. **Les drapeaux de `sbx create`** — axe 3.

## 8. Invariants du dépôt qui te concernent

- **YAML strict partout** (`KnownFields(true)`) : une clé inconnue est une erreur de chargement.
- **Aucun test n'ouvre de socket, ne lance de process, ni n'appelle `t.Parallel()`.** Tes nouveaux
  lecteurs doivent être injectables derrière `sbx.Runner`.
- **`internal/ports/hermeticity_test.go` verrouille le graphe d'imports** : `internal/cli` n'importe
  ni `net`, ni `hash/fnv`, ni `os/exec`. Si ton verdict s'affiche côté CLI, ne fais pas passer un
  accès système par là.
- **Les goldens s'éditent à la main.** Vérifié le 2026-08-24 : `Taskfile.yml` n'a que `build`,
  `test`, `typecheck`, `lint`, `check`, et aucun test ne déclare de drapeau `-update`.
- **`task check`** avant chaque commit — c'est ce que la CI lance.

## 9. Démarrage

```bash
cd /Users/polochon/Development/Pillow/den
git worktree add .claude/worktrees/axe1-gouvernance \
    -b feat/converge-names-undeclared-sbx-state docs/sbx-v0.39.0-survey
cd .claude/worktrees/axe1-gouvernance
task check     # doit être vert AVANT que tu écrives quoi que ce soit
```

Tu branches sur `docs/sbx-v0.39.0-survey` et non sur `main` **volontairement** : c'est la branche
qui porte le spec §14.2, donc les pointeurs de ce handoff résolvent tout de suite. Quand la PR #91
sera mergée, un `git rebase main` suffit. Si elle est déjà mergée quand tu lis ceci, branche sur
`main`.

Ton worktree est **propre pour les greps** : `.gitignore` exclut `.claude/**` sauf `settings.json`
et `commands/`, donc ton arbre ne contient pas les worktrees des autres axes. C'est l'arbre
principal qui est pollué, pas le tien.

**Premier pas concret**, avant toute conception : sonde `sbx mcp ls --json` et `sbx skills ls --json`
sur cette machine, et consigne la réponse dans une nouvelle sous-section §14 datée #88. La forme du
lecteur en dépend entièrement.

## 10. Fini quand

- `task check` vert.
- den nomme, pour au moins une des trois surfaces, une entrée qu'aucune source ne déclare —
  et **rien n'est supprimé**.
- La mécanique est générique : ajouter une quatrième surface ne demande pas de la refondre.
- Une nouvelle sous-section §14 datée #88 porte ce que tu as mesuré contre le `sbx` réel.
- La description de PR dit ce que tu voudrais voir écrit dans `CLAUDE.md`, sans l'y écrire.
