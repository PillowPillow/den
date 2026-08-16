# Design — `den spawn` devient `den up` / `den run`

Date : 2026-08-16
Statut : validé en brainstorming, prêt pour le plan
Tranche : 2 sur 2. La tranche 1 est `2026-08-14-den-exec-shell-design.md` (`den exec` exige une
commande, pas de `--` ; `bash -l` déménage dans `den shell`).
Suivi : [#72](https://github.com/PillowPillow/den/issues/72)

## 1. Le problème

La tranche 1 a donné à `den exec` la forme de `docker compose exec` et a fait naître `den shell`.
Elle a laissé `den spawn` intact, et l'a écrit noir sur blanc (§ « Divergences assumées », l. 232-236) :

> `den spawn` garde `--` et garde son shell par défaut. […] Pendant un temps, deux commandes sœurs se
> lisent différemment. Ce n'est pas un coût accepté pour la durée d'une release : c'est la raison
> d'être de la tranche 2, qui peut suivre immédiatement.

L'écart se lit en deux lignes :

```
den exec  api go test          # pas de `--`, et den REFUSE `den exec api -- go test`
den spawn api -- go test       # `--` EXIGÉ
```

Et `den spawn` fait à lui seul ce que compose tient en deux verbes :

| compose | prend une commande ? | den au 2026-08-16 |
|---|---|---|
| `docker compose up` | non | `den spawn <nest>` — crée-ou-rattache, puis ouvre un shell |
| `docker compose run SERVICE [COMMAND]` | oui, optionnelle | `den spawn <nest> -- <cmd>` |

**Le `--` de `den spawn` n'est pas une coquetterie**, et c'est ce qui rend cette tranche plus dure que
la première. `den exec` prend UN nom de sandbox : la fin des positionnels est connue au premier
jeton, et `SetInterspersed(false)` suffit. `den spawn` prend `<nest> [repo...]` — variadique — donc
`den spawn api ~/dev/hotfix go test` a deux lectures réelles (deux repos ; ou un repo puis une
commande) qu'aucun réglage de parseur ne tranche. C'est l'objet du §4.

## 2. Ce qui a été mesuré

Rien ci-dessous n'est supposé. Les sept mesures datent du **2026-08-16** et chacune décide une
ligne du contrat. Les mesures (e) à (g) ont été ajoutées après une revue adverse qui a montré que
(d), telle qu'elle était écrite, autorisait une conclusion fausse.

**a) `docker compose up` est un crée-ou-rattache** (Docker Compose v5.3.1) :

```
$ docker compose up -d          # projet neuf
 Container upprobe-api-1  Created / Starting / Started
$ docker compose up -d          # le même, déjà vivant
 Container upprobe-api-1  Running          ← ni recréé, ni redémarré
```

Cette mesure existe pour une raison précise : elle retire l'objection du §2 de
`2026-08-05-spawn-command-design.md`, qui refusait `den up` en écrivant « `den up` (idiome compose)
ment sur la sémantique — c'est un spawn-**or-attach**, pas un démarrage ». La prémisse est fausse :
`compose up` est lui aussi un crée-ou-rattache, et `--no-recreate` n'existe dans son aide que parce
que le cas « les conteneurs existent déjà » est un cas normal du verbe. `up` ne ment donc sur rien
que compose ne dise déjà.

**b) `docker compose run` met les montages ad hoc derrière un drapeau répétable** :

```
docker compose run [OPTIONS] SERVICE [COMMAND] [ARGS...]
  -v, --volume stringArray   Bind mount a volume
```

compose a exactement le problème de den — une commande variadique ET des montages à la volée — et il
le tranche par un drapeau, pas par un séparateur. C'est l'argument central du §4.

**c) pflag 1.0.9, `StringArrayVar` sous `SetInterspersed(false)`**, dans la forme réelle de l'arbre
de den (sous-commande d'un root portant la persistante `--den-home`) :

| Entrée | `args` reçus | `--repo` | `--only` |
|---|---|---|---|
| `run --repo ~/dev/b --repo ~/dev/a api go test -v` | `[api go test -v]` | `[~/dev/b ~/dev/a]` | — |
| `run --repo=/x,y --only a,b api sh` | `[api sh]` | `[/x,y]` | `[a b]` |
| `--den-home /h run -w feat/x --repo /z api go build` | `[api go build]` | `[/z]` | — |
| `run api --repo /late go test` | `[api --repo /late go test]` | `[]` | — |

Quatre conséquences, toutes portantes :

1. **L'ordre de frappe est conservé.** `2026-08-04-adhoc-repos-design.md` en dépend : l'ordre des
   repos décide l'argv de `sbx` (`[positionnels…] [déclarés…] [common git dirs…] [profil agent]`).
   Un drapeau répétable le rend aussi fidèlement qu'un positionnel.
   *Précision, contre une première rédaction qui disait « et donc le `StartDir` » :* la précédence
   de `spawn.StartDir` est `--workdir` → le mount le PLUS PROFOND contenant le cwd → `mounts[0]`
   (`internal/spawn/startdir.go:17-27`). L'ordre ne décide donc que `mounts[0]`, c'est-à-dire la
   règle 3, atteinte seulement quand on spawne depuis l'extérieur de tout mount. La dépendance à
   l'ordre est réelle, sa portée est celle-là.
2. **`StringArray` ne coupe PAS sur la virgule, `StringSlice` si.** `--repo` doit être un
   `StringArrayVar` : un chemin peut contenir une virgule. `--only` / `--without` restent des
   `StringSliceVar` — ils adressent des *basenames* de repo, où la virgule n'a pas cours.
3. **La persistante `--den-home` reste lue** à gauche de la sous-commande, comme en tranche 1.
4. **Un `--repo` tapé APRÈS le nom du nest n'est pas analysé** : il part dans la VM comme un jeton
   de la commande. C'est la panne que la tranche 1 a fermée par un ensemble fermé de refus, et
   `--repo` doit y entrer (§5).

**d) La TABLE des drapeaux est dérivable — la CLASSIFICATION des jetons ne l'est pas.** Dans le
validateur `Args`, après que cobra a fusionné les persistantes :

```
den --den-home /h run -T api go test
  args = ["api" "go" "test"]
  cmd.Flags().VisitAll → --den-home | --help (-h) | --no-tty (-T, novalue) | --repo | --worktree (-w)
```

Le walk voit les drapeaux de den, la persistante du root, le raccourci de chacun, et `NoOptDefVal`
distingue un booléen d'un drapeau à valeur — c'est exactement l'information que les remèdes du §5
doivent reconstruire.

**Une première rédaction concluait de là que « l'ensemble fermé est dérivé », et c'était un pas de
trop** : disposer des NOMS ne dit pas comment reconnaître un JETON, et la mesure (e) montre que la
comparaison exacte pratiquée aujourd'hui manque toutes les orthographes courtes que `run` introduit.

Le walk voit AUSSI `--help` / `-h`, qui doivent être exclus (mesure 3 de la tranche 1 :
`den exec api --help` demande son aide au programme, dans le sandbox) — **et leur présence dépend du
chemin** : `--help` est là sous `Execute()`, ABSENT sous `Find` + `ParseFlags`, cobra ne l'ajoutant
que dans `InitDefaultHelpFlag`. L'exclusion nommée n'est donc pas une politesse : c'est ce qui fait
que den se comporte pareil sur le chemin de production et sur celui des tests.

**e) Les orthographes courtes qu'une comparaison de NOM ne peut pas classer.** Banc pflag v1.0.9,
recoupé sur un binaire `den` réel. Colonne de droite : le même jeton tapé à DROITE du nom du nest,
où `SetInterspersed(false)` le laisse arriver en positionnel — le cas que le classifieur doit traiter.

| Jeton | Ce que pflag en fait | `execFlagOf` aujourd'hui |
|---|---|---|
| `-w feat` | `worktree=feat` | **manqué** |
| `-wfeat` | `worktree=feat` (valeur attachée) | **manqué** |
| `-w=feat` | `worktree=feat` | **manqué** |
| `-iT` / `-Ti` | met les DEUX booléens | **manqué** |
| `-iTwfeat` | `i`, `T`, puis `worktree=feat` | **manqué** |
| `-iT=true` | met `-i` **ET** `-T` — le `=` se lie à la lettre PRÉCÉDENTE et termine le jeton | **manqué** |
| `-wi feat` | `worktree="i"` — une lettre à valeur AVALE le reste de la grappe | **manqué** |
| `--workdir=/srv` | `workdir=/srv` | trouvé |
| `-` seul | positionnel, aucune erreur | — |

Chacune des sept premières est jugée « pas à den », devient le nom du nest ou le premier mot de la
commande, et atteint la VM. C'est la panne que l'ensemble fermé existe pour empêcher. Conséquence :
**le classifieur s'écrit à la main et lit la table dérivée** (§5).

**f) `--` ne disparaît pas parce que la spec le retire de la grammaire.** pflag termine son parse sur
`--` quoi qu'il arrive, y compris sans `SetInterspersed(false)`. Sur `up` :

| Ligne | `args` | `ArgsLenAtDash()` |
|---|---|---|
| `up -- api` | `[api]` | 0 |
| `up api --` | `[api]` | 1 |
| `up api -- go test` | `[api go test]` | 1 |

`--` n'apparaît JAMAIS dans `args` : seul `ArgsLenAtDash()` le révèle. Un validateur qui compte les
positionnels prend `den up api -- go test` pour trois repos. Mesuré aussi : le shell développe
`--repo ~/dev/proj-*` avant den, `--repo` se lie au premier appariement et le reste arrive en
positionnels (§4, §5).

**g) Le constructeur de remèdes porte deux défauts, aujourd'hui, sur le binaire réel** :

```
$ den exec --workdir /srv -- api go build
den exec: `--` is not needed, and a sandbox name must come first — write `den exec api go build`
                                                                        ↑ --workdir /srv perdu

$ den exec api --workdir "/tmp/hot fix" true
den exec: den's flags go before the sandbox name — write `den exec --workdir /tmp/hot fix api true`
                                                          ↑ rejoué : sandbox « fix », commande « api true »
```

Préexistants, donc ni causés ni découverts par cette tranche — mais absorbés par elle, parce que le
constructeur est réécrit de toute façon et que `--repo` élargit leur portée : un remède qui perdait
un `--workdir` perdra un MONTAGE (§5).

## 3. Les noms

**`den spawn` disparaît. `den up` et `den run` naissent.** Aucun alias, aucune fenêtre de
dépréciation : den est en alpha avec un seul utilisateur au 2026-08-16, et la rupture est assumée
telle quelle.

Deux points qu'un lecteur cherchera :

- **Le §2 de `2026-08-05-spawn-command-design.md` est amendé, pas ignoré.** Il refusait `den up` sur
  la prémisse que la mesure (a) démonte, et il réservait la famille `spawn` / `agent` / `review` de
  la spec 2026-07-27 §5. Cette famille perd son premier terme : `den agent` et `den review`, s'ils
  naissent, prendront un nest sans que `up` les gêne — ce sont des verbes différents pour des gestes
  différents, pas trois orthographes d'une porte.
- **`internal/spawn` GARDE son nom**, ainsi que `spawn.Spawn`, `spawn.Options` et `spawn.Enter`. Le
  paquet nomme le geste interne (créer une microVM), que deux commandes appellent désormais. Le
  renommer coûterait un diff de plusieurs centaines de lignes pour aligner un nom de paquet sur un
  nom de commande — et den n'aligne pas ces deux espaces ailleurs non plus (`internal/sbx` sert
  `ls`, `rm`, `ports`, `exec`).

**Aucun alias `spawn`.** Deux orthographes d'une porte est le défaut que le commentaire de #60
(`internal/cli/exec.go:72-77`) et la factorisation `enterSandbox` de la tranche 1 existent pour
empêcher. La migration passe par un message (§6), pas par une seconde porte.

## 4. Le contrat

```
den up  [flags] <nest>
den run [flags] <nest> <cmd> [args...]
```

`<nest>` est la chaîne que l'utilisateur tape : `api`, `corp:backend`. Le nom de sandbox
`<nest>[.<instance>]` en dérive (`--as`, `-w`), inchangé.

- **`den up <nest>`** : crée-ou-rattache, puis `bash -l`. Le comportement est celui de
  `den spawn <nest>` d'aujourd'hui, inchangé — seule l'écriture des repos à la volée bouge.
- **`den run <nest> <cmd> [args...]`** : crée-ou-rattache, puis la commande. C'est
  `den spawn <nest> -- <cmd>` d'aujourd'hui. Tout ce qui suit le nom du nest est la commande,
  verbatim, ses propres drapeaux compris.
- **`--` n'existe plus dans la famille.** Ni sur `up` (qui ne prend aucune commande), ni sur `run`
  (qui refuse le séparateur avec un remède, comme `den exec`).
- **Les repos à la volée passent derrière `--repo <path>`, répétable, sur les deux commandes.**

**`den run` n'est PAS le `run` éphémère de compose.** `docker compose run` fabrique un conteneur
jetable, à côté du projet, que `--rm` supprime à la sortie. den n'a pas cet objet : `den run` entre
dans LA sandbox du nest, la crée si elle n'existe pas, et la laisse vivante après. Divergence
assumée et nommée ici pour qu'un lecteur de compose ne la découvre pas à l'usage ; inventer une
sandbox jetable est hors périmètre (§10).

### Quatre portes, et ce qui les distingue

Après cette tranche den a quatre entrées dans une sandbox. Le 2×2 n'a aucune case en double :

| | pas de commande | une commande |
|---|---|---|
| **sandbox vivante seulement** | `den shell <sandbox>` | `den exec <sandbox> <cmd>` |
| **crée-ou-rattache** | `den up <nest>` | `den run <nest> <cmd>` |

Écrit ici parce que la lecture facile est fausse et qu'elle coûterait une porte : interactivement,
`den up` a l'air de dominer `den shell` — il marche que la sandbox soit vivante ou non. Trois
différences portantes disent le contraire, et aucune n'était écrite nulle part avant cette ligne.

**1. Elles ne lisent pas la même chose, donc elles ne tombent pas en panne ensemble.** `den up`
traverse toute la cascade avant d'atteindre la VM : `config.LoadGlobal`
(`internal/spawn/spawn.go:261`), `source.Locate` (`:269`), `nest.LoadNest` (`:272`). `den shell` ne
lit AUCUN fichier de nest — `enterSandbox` l'écrit (`internal/cli/exec.go:267-269` : « it reads no
nest file ») — et son unique lecture du den home est consultative, avalée en cas d'échec
(`warnEmptyAgentOnReentry`, `internal/cli/exec.go:455-458` : « a broken den home never stands
between the user and a live sandbox »). Supprimez `nests/api.yaml`, désinstallez la source, cassez
le `config.yaml` : `den shell api` ouvre toujours la VM vivante, `den up api` refuse. C'est un
domaine de panne différent, pas une commodité.

**2. Refuser de créer est une fonctionnalité.** Un appelant scripté ou une étape de CI qui vise une
sandbox absente veut un échec, pas une microVM qui se fabrique sous lui. Ce refus vit dans
`enterSandbox` (`internal/cli/exec.go:278-285`) :

```go
b := sbx.Find(boxes, name)
if b == nil {
    names := liveNames(boxes)
    if len(names) == 0 {
        return fmt.Errorf("sandbox %q not found — no sandbox is running", name)
    }
    return fmt.Errorf("sandbox %q not found (live: %v)", name, names)
}
```

Ce n'est PAS `CheckAttachable` (`internal/sbx/ls.go:153`), qu'on croit facilement responsable :
celui-là est une liste blanche de STATUTS — `running` ET `stopped` passent — et il tourne après, sur
une sandbox déjà trouvée.

**3. Elles n'adressent pas la même chose.** `exec` / `shell` prennent un NOM DE SANDBOX complet, la
chaîne que `den ls` imprime : `den exec api.feat-123 go test`. Le seul traitement est l'aplatissement
d'un préfixe de source (`sandboxNameOf`, `internal/cli/reference.go:35-53`). `up` / `run` prennent
une RÉFÉRENCE DE NEST et n'ont aucun nom tant que `-w` et `--as` n'ont pas parlé : c'est `Spawn` qui
aplatit ces composants (`config.FlattenSandboxComponent`, `internal/spawn/spawn.go:405-451`) puis
appelle `sbx.SandboxName` (`:477`). Rejoindre l'instance d'un `den up api -w feat/123` demande donc
de retaper `-w feat/123` ; `den shell api.feat-123` la nomme littéralement. Deux adressages, deux
ergonomies, et c'est le second qu'un script veut.

Une PR de « simplification » qui supprimerait `shell` et `exec` supprimerait ces trois propriétés
d'un coup. C'est la raison d'être de ce tableau : la surface se lit comme quatre portes, pas comme
deux façons de faire une chose.

### Pourquoi `--repo` et pas `--`

Trois formes ont été pesées.

| Forme | Ce qu'elle coûte |
|---|---|
| **`--repo` répétable sur `up` et `run`** (retenue) | rupture sur les repos à la volée : `den spawn api ~/dev/hotfix` → `den up api --repo ~/dev/hotfix` |
| `run` garde `--` | `den run` et `den exec` continuent de se lire différemment — l'un EXIGE `--`, l'autre le REFUSE avec un message. C'est la divergence que cette tranche existe pour fermer, rendue permanente |
| césure : `up` positionnel, `run` avec `--repo` | un repo à la volée s'écrit de deux façons entre deux commandes sœurs. C'est la collision que den refuse ailleurs (`--workdir` épelé long partout, `enterSandbox` partagé), et `--only` / `--without` n'adresseraient qu'une des deux orthographes |

La forme retenue est celle de compose (mesure b), et elle rend `up` / `run` identiques à la paire
`shell` / `exec` de la tranche 1 : `Args` exactement 1 contre au moins 2, `SetInterspersed(false)`
d'un seul côté, aucun `--` nulle part.

La rupture sur les repos à la volée est réelle, l'issue #72 l'autorise, et elle ne porte PAS sur une
voie rare : la fréquence n'a jamais été mesurée, et la source dont cette tranche dépend dit le
contraire. `2026-08-04-adhoc-repos-design.md` ouvre sur « `sbx run -t <template> AGENT PATH
[PATH...]` fait ce geste **en une frappe** ; den, qui existe pour rendre `sbx` plus simple, le rend
ici plus lourd » (l. 12-13), appelle le nest scratch sans `repos:` « le **cas d'usage vedette** »
(l. 81-82), et reprend « en une frappe » pour dire ce que les positionnels rendent atteignable
(l. 171). La feature a été vendue sur la frappe unique ; c'est elle qu'on reprend.

**Ce qui est perdu, exactement.** `den spawn scratch ~/dev/proj-*` montait N dépôts d'un geste : le
shell développait le glob et chaque résultat tombait dans un positionnel. Un drapeau répétable ne
peut pas prendre de glob — vérifié dans un shell le 2026-08-16 : `--repo ~/dev/proj-*` se développe
en `--repo /…/proj-a /…/proj-b /…/proj-c`, `--repo` se lie au premier et les autres arrivent en
positionnels. C'est une propriété du shell, pas de pflag : aucun réglage du parseur ne la change.

**Ce qu'il faut taper à la place.** Deux formes récupèrent le geste ; aucune n'est une frappe.

```zsh
# zsh, distribution de paramètre — l'orthographe `--repo=<val>` est obligatoire
repos=(~/dev/proj-*); den up --repo=${^repos} scratch
```

```bash
# portable, et la seule qui survit à un espace dans un chemin
repos=(); for d in ~/dev/proj-*; do repos+=(--repo "$d"); done
den up "${repos[@]}" scratch
```

Les deux sont vérifiées le 2026-08-16. La première tient sur une ligne mais exige zsh ET la forme
collée `--repo=` ; la seconde marche partout et fait trois lignes. Le coût croît avec N dans les
deux cas, là où le positionnel était plat.

**Pourquoi la rupture est prise quand même.** compose a fait le même arbitrage sur exactement ce
problème — une commande variadique ET des montages à la volée — et il l'a tranché par un drapeau
répétable qui ne prend pas plus de glob que `--repo` : `-v, --volume stringArray` (mesure b,
revérifiée le 2026-08-16 sur Docker Compose v5.3.1). L'analogie porte sur la FORME du drapeau et
s'arrête là : le `-v` de compose monte dans un conteneur jetable, `--repo` monte dans une sandbox
dont les montages sont figés au `create` (2026-08-04, « Étape 6 — branche attach »). Ce que la forme
achète est écrit au-dessus — une seule orthographe pour un repo à la volée sur deux commandes
sœurs, et la fin du `--`. Ce qu'elle coûte est écrit ici, et ce n'est pas un gain déguisé.

Aucun raccourci d'une lettre pour `--repo`. compose épelle `-v`, que den ne peut pas reprendre sans
mentir (`-v` n'est pas un volume ici, et `-w` est déjà la worktree) ; inventer `-r` pour un drapeau
tapé rarement rouvrirait la collision de lettres que le §« `--workdir` reste épelé long » de la
tranche 1 refuse.

## 5. Matrice des drapeaux, et les refus

Le troisième état — **enregistré, toujours refusé** — n'est pas une invention : c'est le sort de `-T`
sur `den shell` (tranche 1, `internal/cli/shell.go:27-29`), au motif qu'un refus nommé vaut mieux
que `unknown flag`.

| Drapeau | `up` | `run` | Note |
|---|---|---|---|
| `--repo` (nouveau) | ✓ | ✓ | répétable, ordre conservé, `StringArrayVar` |
| `-w` / `--worktree`, `--as`, `--agent` | ✓ | ✓ | inchangés |
| `--only`, `--without`, `-i` | ✓ | ✓ | la contradiction `-i` × `--only`/`--without` est inchangée (`spawn.go:325`) |
| `--workdir` | ✓ | ✓ | reste épelé long, définitivement — `-w` est la worktree |
| `--detach` | ✓ (= `up -d` de compose) | enregistré, refusé | atteint la contradiction EXISTANTE `spawn.go:231` |
| `-T` / `--no-tty` | enregistré, refusé | ✓ | atteint la contradiction EXISTANTE `spawn.go:254` |
| `SetInterspersed(false)` | **non** | **oui** | |
| `Args` | un validateur à lui, TROIS branches ordonnées (voir plus bas) | ≥ 2 positionnels | |

**`up` n'arme pas `SetInterspersed(false)`**, et c'est une décision, pas un oubli : le
raisonnement de `internal/cli/shell.go:93-100` s'applique mot pour mot. `up` ne prend aucune
commande, donc aucun drapeau n'a de second propriétaire possible, et l'entrelacement achète une
chose — `den up api -T` atteint le refus NOMMÉ de `-T` au lieu d'être refusé pour son ARITÉ par
`exactlyOneArg`, message qui ne nomme ni le drapeau ni le remède.

### Le second positionnel de `den up`

`exactlyOneArg` (`internal/cli/root.go:263`) ne convient PAS ici, et c'est le seul endroit où cette
tranche ajoute un message plutôt que d'en déplacer un. Le geste que la rupture rend le plus probable
est la mémoire des doigts :

```
$ den up api ~/dev/hotfix
```

Sous `exactlyOneArg`, l'utilisateur lit « exactly one argument expected: 2 received — usage: … »
(`argsBetween`, `root.go:278-296`), qui ne nomme ni `--repo`, ni ce qui a changé. `den up` porte donc
son propre validateur.

**Ce validateur a trois branches, et leur ORDRE est le fond du sujet.** Une première rédaction n'en
avait qu'une, et la mesure (f) montre qu'elle produisait deux remèdes absurdes.

`--` ne disparaît pas de pflag parce que la spec le retire de la grammaire : pflag termine son parse
sur `--` quoi qu'il arrive, y compris sans `SetInterspersed(false)`. Mesuré sur `up` :

| Ligne | `args` | `ArgsLenAtDash()` |
|---|---|---|
| `up -- api` | `[api]` | **0** |
| `up api --` | `[api]` | **1** |
| `up api -- go test` | `[api go test]` | **1** |
| `up --repo /a -- api` | `[api]` | **0** |

`--` **n'apparaît jamais dans `args`** : seul `ArgsLenAtDash()` le révèle. Un validateur qui compte
les positionnels est donc aveugle, et `den up api -- go test` lui arrive comme trois positionnels —
d'où le remède ``den up --repo go --repo test api``, légal, rejouable, et proposant de monter deux
répertoires nommés `go` et `test` alors que l'utilisateur voulait `den run api go test`.

Les branches, dans l'ordre :

1. **`len(args) == 0`** → `den up: a nest expected — usage: <UseLine>`.
2. **`dash >= 0`** — un `--` a été tapé. C'est le DISCRIMINANT : l'utilisateur a écrit un séparateur,
   ce qui dans l'ancienne grammaire voulait dire « une commande suit ». Cette lecture bat celle des
   repos, donc ce test passe AVANT la branche 3.
   - queue non vide → c'est un `run` tapé `up`, et le remède nomme **`den run`, pas `--repo`** :
     ``den up: den up takes no command — write `den run api go test` ``
   - queue vide (`up -- api`, `up api --`) → le séparateur est seulement inutile :
     ``den up: `--` is not needed — write `den up api` ``
3. **`Changed("repo")` ET plus d'un positionnel** → un motif shell a été développé. `--repo` ne peut
   pas recevoir de glob : le shell développe avant que den ne voie quoi que ce soit, `--repo` se lie
   au premier appariement et le reste arrive en positionnels. Mesuré :
   `up --repo /dev/proj-a /dev/proj-b /dev/proj-c scratch` donne `repo=["/dev/proj-a"]` et
   `args=["/dev/proj-b" "/dev/proj-c" "scratch"]` — la branche 4 y prendrait `/dev/proj-b` pour le
   nest et proposerait ``den up --repo /dev/proj-c --repo scratch /dev/proj-b``, où le vrai nest
   devient un repo. den nomme donc le développement au lieu de construire un remède :
   ``den up: --repo takes one path and got a pattern that expanded to several — quote it, or repeat --repo once per path (the extra arguments were /dev/proj-b, /dev/proj-c, scratch)``
   **Aucun `os.Stat` ici** : `Changed("repo")` plus des positionnels en trop suffit, et c'est un fait
   sur la ligne de commande, pas une hypothèse sur le disque. La distinction avec le §« l'avertissement
   de `den run` » plus bas est exactement celle-là.
4. **`len(args) > 1`, sans `--`** → la mémoire des doigts, le cas pour lequel ce validateur existe :

```
den up: extra arguments — ad-hoc repos go behind --repo now — write `den up --repo ~/dev/hotfix api`
```

Le remède est construit par le constructeur partagé du §5 (les drapeaux remontent à gauche du nom),
pas recollé à la main, et il entre dans `TestRunRemediesAreThemselvesLegal` au même titre que ceux de
`run`. **La branche 2 dépend du correctif (F)** : `up --repo /a -- api` l'atteint avec `repo=["/a"]`
déjà consommé par pflag, et sans la relecture du `FlagSet` le remède serait `den up api`, perdant le
montage en silence.

### Le second positionnel de `den run` — un avertissement, pas un refus

**Le même geste sur `den run` ne peut pas être REFUSÉ, et c'est assumé ; il est AVERTI, et c'est
nouveau.** `den run api ~/dev/hotfix go test` donne au sandbox une commande `~/dev/hotfix go test`,
qui échoue à l'intérieur. den ne peut pas refuser cette ligne : `~/dev/hotfix` est un premier jeton
de commande parfaitement lisible, et la lecture « ceci est un programme » est légitime.

Ce paragraphe disait que den ne pouvait pas faire mieux, parce qu'un `os.Stat` sur ce jeton serait
« exactement la normalisation silencieuse que le §2 de la spec 2026-07-27 refuse ». Cette phrase
confond deux gestes qui n'ont pas la même nature.

- **Le `stat` qui décide du COMPORTEMENT** — monter le chemin quand même, ou le retirer de la
  commande — est bien la normalisation que le §2 refuse : den déciderait à la place de
  l'utilisateur, sur une preuve (« ce jeton existe sur l'hôte ») qui ne dit rien de son intention.
- **Le `stat` qui décide d'un AVERTISSEMENT** n'y touche pas. den exécute la ligne telle qu'elle est
  tapée — `~/dev/hotfix` part bien dans la VM comme premier mot de la commande, `sbx` la reçoit
  inchangée, le code de retour est celui de la commande — et écrit une ligne de plus sur stderr.

Le §11 de la spec 2026-07-27 a déjà tracé cette frontière, en toutes lettres (l. 934) : « Le §2
(« den refuse plutôt que de normaliser en silence ») porte sur l'**intention** de l'utilisateur ».
Le §2 gouverne ce que den FAIT d'une intention ambiguë, pas ce qu'il a le droit de REGARDER.

Le code le dit plus court qu'un argument : **den `stat` déjà ces chemins-là.** L'étape 2 de `Spawn`
boucle sur `os.Stat` pour chaque repo, et `2026-08-04-adhoc-repos-design.md` lui a donné un message
par origine — `repo not found: <p> — given on the command line`. Un `stat` sur un chemin donné en
ligne de commande est du travail existant, pas une porte qu'on ouvre ici.

**La doctrine de l'avertissement est celle du 2026-08-04**, au précédent le plus proche qui
existe — même feature, même famille de commandes (l. 196) :

> On **avertit sans refuser** — même doctrine que `reportDrift` (refuser casserait un `den <nest>`
> qui marchait hier ; recréer détruirait un travail en cours), et même forme que
> `reportMissingGitDirs` (`internal/spawn/spawn.go:451`), pas un second mécanisme.

`reportUnmountedRepos` est la forme livrée de cette doctrine (`internal/spawn/spawn.go:1997` :
« Warn, never refuse, and never recreate: the same doctrine as reportDrift »), et c'est la sienne
que cet avertissement reprend : le préfixe `!`, une ligne, aucune conséquence sur ce qui s'exécute.

**Ce que den imprime**, sur `cmd.ErrOrStderr()` — la règle d'`enterSandbox`
(`internal/cli/exec.go:291-295`) : les lignes de den vont sur stderr dès qu'un appelant peut piper
stdout, et `den run api go build | tee log` ne doit porter que la sortie de la commande. `den run`
porte TOUJOURS une commande, donc il n'a pas la branche interactive qui rend son stdout à den.

```
$ den run api ~/dev/hotfix go test
! ~/dev/hotfix is a directory on this host, and den is passing it to the sandbox as the first
  word of the command — ad-hoc repos go behind `--repo` now — write `den run --repo ~/dev/hotfix api go test`
```

La clause du milieu est **mot pour mot celle du refus de `den up`** ci-dessus : c'est la même
migration, elle ne s'apprend pas en deux formulations.

Une seconde forme existe, et elle n'est pas une variante cosmétique :

```
$ den run api ~/dev/hotfix
! ~/dev/hotfix is a directory on this host, and den is passing it to the sandbox as the first
  word of the command — ad-hoc repos go behind `--repo` now — write `den up --repo ~/dev/hotfix api`
```

Le remède nomme `den up`, pas `den run`, parce que la ligne ne porte aucune autre commande :
`den run --repo ~/dev/hotfix api` serait refusée à son tour pour « no command given ». C'est
exactement le défaut que la tranche 1 a payé une fois — un remède mort à l'arrivée — donc la ligne
proposée sort du **constructeur de remèdes partagé** du §5 (`execLine` sur une forme dont les
`flags` portent `--repo <chemin>`), pas d'un `Sprintf` local, et elle entre dans
`TestRunRemediesAreThemselvesLegal` au même titre que les refus. Un avis qui propose une ligne
illégale coûte le même aller-retour qu'un refus qui en propose une.

**Où vit le `stat`** : dans le `RunE` de `den run`, avant le corps partagé, jamais dans le
validateur `Args`. Deux raisons.

1. Après le validateur, `args[1]` EST le premier jeton de la commande par construction — la ligne
   sur laquelle `den exec` s'appuie déjà (`internal/cli/exec.go:398` : « execArgs has refused every
   other shape, so args[1:] is a real command »). Le `RunE` n'a besoin d'aucune machinerie de forme.
2. Aucun validateur de ce dépôt n'écrit sur un flux, et il ne faut pas commencer : le §5 est
   « premier défaut gagnant », donc un `Args` qui imprime collerait un conseil sous une ligne déjà
   refusée pour autre chose. C'est le raisonnement d'`enterOptions` par l'autre bord (« a probe
   carried but never consulted is the first half of a second verdict ») : un verdict et un avis qui
   sortent du même endroit finissent par se lire comme un seul.

Sur `den up` la question ne se pose pas : un second positionnel y est refusé, l'avertissement n'a
personne à prévenir.

**Rien à injecter, et la portée du §7 ne bouge pas.** `internal/cli` lit déjà le système de fichiers
sans passer par `cli.Deps` : `os.Stat` dans `rm.go:74` et `rm.go:597`, `os.Stat` dans
`reference.go:116`, `os.ReadDir` dans `nest.go:103`, `config.Home` / `config.LoadGlobal` dans
`exec.go:480-484`. `cli.Deps` injecte ce qui touche la machine AU-DELÀ du disque — un socket
(`Scanner`), un navigateur (`Open`), un `ssh-add` (`SSHAgent`), un terminal (`IsTTY`) — et
`internal/ports/hermeticity_test.go` n'interdit à `internal/cli` que `net`, `hash/fnv` et `os/exec`.
`os` n'est pas dans la liste. Un test hermétique donne un `t.TempDir()` et lit le verdict.

**Une seule règle, et elle reste cheap** : le jeton est résolu contre le cwd de den sur l'hôte — la
même base que `parseRepoArg` — puis `stat`é.

- l'erreur de `stat`, quelle qu'elle soit : silence ;
- ça existe mais ce n'est pas un répertoire : silence — `den run api ./build.sh` est une commande
  légitime ;
- c'est un répertoire : on avertit.

**Pas de test de git-ité.** Un répertoire non-git était un repo à la volée parfaitement légal — la
décision 2 du 2026-08-04 n'exige `git` que sous `-w` — et c'est ce contrôle-là, pas le `stat`, qui
ferait de l'avertissement un second résolveur.

**Pas de pré-filtre « ça ressemble à un chemin »** (un `/`, un `~`, un `.` en tête), bien qu'il
supprimerait le faux positif ci-dessous : `parseRepoArg` accepte un nom relatif nu, donc
`den spawn api hotfix` était une frappe légale, et le pré-filtre la manquerait. Un avertissement qui
se déclenche sur certaines orthographes d'un repo à la volée et pas sur d'autres est pire qu'un
avertissement qui se déclenche parfois pour rien.

**Le faux positif, chiffré honnêtement** : une commande dont le premier mot est aussi le nom d'un
répertoire du cwd. Dans un dépôt qui porte un `build/`, `den run api build` imprime une ligne d'avis
de trop. C'est le coût entier — l'argv, le code de retour et la sortie de la commande sont
inchangés — et c'est pour cette raison-là, pas pour une autre, qu'il est payable.

`den up` peut nommer la migration parce qu'il n'a AUCUNE lecture pour un second positionnel ;
`den run` en a une, légitime — et c'est pourquoi il avertit là où `up` refuse.

### Les refus de `den run`

Mêmes formes, même code que `den exec` :

```
$ den run api
den run: no command given — write `den run api go test`, or `den up api` for a shell

$ den run api --repo ~/dev/hotfix go test
den run: den's flags go before the nest name — write `den run --repo ~/dev/hotfix api go test`

$ den run api -- go test
den run: `--` is not needed — write `den run api go test`
```

`execShape` / `execRewrite` / `execLine` (`internal/cli/exec.go:57-213`) cessent d'appartenir à
`exec` et deviennent le constructeur de remèdes partagé. La propriété que la tranche 1 a durement
acquise — **un remède proposé est lui-même accepté par den** — vaut alors des deux côtés :
`TestExecRemediesAreThemselvesLegal` gagne son jumeau `run`. C'est la moitié « remède » qui avait
pourri sans qu'un `strings.Contains` s'en aperçoive ; elle ne doit pas pourrir deux fois.

### La TABLE devient dérivée ; le classifieur reste écrit à la main

`execFlags` (`internal/cli/exec.go:39-44`) énumère quatre drapeaux à la main. `den run` en porte
quatorze orthographes (`-T`, `--no-tty`, `-w`, `--worktree`, `--as`, `--agent`, `--only`,
`--without`, `-i`, `--interactive`, `--detach`, `--workdir`, `--repo`, `--den-home`), plus les formes
`--x=valeur`. Une liste manuelle de quatorze entrées se désynchronise au premier drapeau ajouté, en
silence, et rouvre la panne de la mesure (c.4).

**Une première rédaction de cette spec disait « l'ensemble est dérivé de `cmd.Flags()` », et c'était
faux.** La mesure (e), faite sur un banc pflag v1.0.9 et rejouée sur un binaire `den` réel, sépare
deux choses que cette phrase confondait.

`execFlagOf` (`exec.go:118-127`) compare le segment du jeton avant `=` à un NOM de drapeau. Cela tient
aujourd'hui pour une seule raison : le seul raccourci d'`exec` est `-T`, qui ne prend pas de valeur et
constitue donc un jeton entier. Cela tombe sur **toutes** les orthographes courtes que `run`
introduit — `-wfeat`, `-w=feat`, `-iT`, `-Ti`, `-iTw`, `-iTwfeat`, `-iT=true` : aucune n'a un
segment avant `=` égal à un nom de drapeau. Chacune est donc jugée « pas à den », devient le nom du
nest ou le premier jeton de la commande, et atteint la VM — `bash: -wfeat: command not found`,
exactement la panne que l'ensemble fermé existe pour empêcher.

Deux règles de pflag mesurées, que le raisonnement naïf inverse :

| Orthographe | Ce que pflag en fait | La règle |
|---|---|---|
| `-iT=true` | met `-i` **ET** `-T` à true | dans `parseSingleShortArg`, le test `shorthands[1] == '='` passe AVANT le test `NoOptDefVal` : le `=` se lie à la lettre qui le précède et TERMINE le jeton, quelle que soit l'arité de cette lettre |
| `-Ti=false` | met `-T` à true, `-i` à false | même règle |
| `-wi feat` | met `worktree="i"` ; `feat` reste positionnel | une lettre à valeur AVALE le reste de la grappe. `-wT` donne `worktree="T"` |
| `-` seul | positionnel, aucune erreur | ne doit PAS entrer dans le parcours de grappe |
| `--nope` | erreur dure, le validateur ne tourne jamais | hors sujet pour le classifieur |

**Ce que le walk donne vraiment, c'est la TABLE** : nom long, raccourci, et `NoOptDefVal` (vide
exactement quand le drapeau prend une valeur) — les trois données que le champ `placeholder`
d'`execFlag` encode à la main. Le classifieur qui lit cette table, lui, s'écrit :

```
1. tok == "--"                    -> le séparateur (l'appelant le retire, pose sawDash)
2. len(tok) < 2 || tok[0] != '-'  -> PAS à den   ("", "-", "api", "/tmp/x", "go")
3. préfixe "--" :
     name, _, hasEq := strings.Cut(tok[2:], "=")
     takes, ok := long[name]
     si !ok             -> PAS à den                  (--nope, --help)
     si takes && !hasEq -> à den, CONSOMME le jeton suivant comme valeur
                           (pas de suivant -> émettre le placeholder)
     sinon              -> à den, ne consomme rien
4. sinon, grappe courte ; body := tok[1:] :
     pour i, c dans body :
         takes, ok := short[c]
         si !ok -> le jeton ENTIER n'est PAS à den        // voir plus bas
         si i+1 < len(body) && body[i+1] == '=' -> à den, ne consomme rien   // CE TEST D'ABORD
         si !takes -> continue                            // booléen, on avance
         si i+1 < len(body) -> à den, ne consomme rien    // valeur attachée : -wfeat, -wi, -wT
         sinon              -> à den, CONSOMME le jeton suivant
     -> toutes les lettres étaient des booléens connus : à den, ne consomme rien   // -iT, -Ti
```

**Une lettre inconnue disqualifie la grappe ENTIÈRE** (étape 4, `!ok`). C'est une décision, et c'est
le statu quo mesuré : `den exec api -Tv go build` est accepté aujourd'hui et le reste, `-Tv` partant
à la VM. C'est aussi la seule réponse juste — `-Tv` n'est pas une orthographe den légale, donc den ne
peut pas la relever dans un remède.

**Le placeholder** (`<dir>`, `<path>`) meurt avec la liste manuelle et devient `"<" + f.Name + ">"` :
`den exec --workdir <workdir> api go test`. Aucun test n'assertant les chaînes actuelles (vérifié par
grep sur `internal/cli/*_test.go` et `testdata/`), le changement ne coûte aucune édition de test.

#### Rendre la queue à un `FlagSet` neuf — mesuré, rejeté

L'alternative évidente — reparser la queue avec un `pflag.FlagSet` jetable — a été mesurée et porte
trois défauts, dont deux silencieux :

1. **Sans `ParseErrorsAllowlist.UnknownFlags`**, la queue `-T -v go test` rend
   `unknown shorthand flag: 'v' in -v` et un `fs.Args()` **vide**. Le parse ne dit pas où il s'est
   arrêté, donc la commande est irrécupérable — or une commande portant ses propres drapeaux est
   toute la raison d'être de `SetInterspersed(false)`.
2. **Avec la liste blanche, `stripUnknownFlagValue` MANGE le jeton suivant**, ne pouvant pas connaître
   l'arité d'un drapeau étranger : `-T -v go test` → `["test"]` (`go` avalé) ; `--nope go test` →
   `["test"]`. La queue EST la commande de l'utilisateur ; la corrompre est inacceptable.
3. **`pflag.Flag` est un pointeur.** `throwaway.AddFlag(f)` sur les drapeaux rendus par `VisitAll`
   fait écrire le parse jetable dans les variables liées de la commande vivante — mesuré :
   `--workdir` est passé de `/original` à `/CLOBBERED`, et `Changed` est devenu vrai.

Le classifieur écrit à la main ne touche jamais aux jetons de la commande : il s'arrête au premier
jeton qui n'est pas à den.

#### Deux points que la dérivation doit écrire

- **`--help` et `-h` sont exclus explicitement, et la raison est plus forte que « la liste manuelle
  les omettait ».** Le contenu du walk est **dépendant du chemin** (mesuré) : `--help` est présent
  sous `Execute()`, ABSENT sous `Find` + `ParseFlags`, parce que cobra ajoute le drapeau d'aide dans
  `InitDefaultHelpFlag`, appelé pendant `execute()`. L'exclusion nommée est donc ce qui fait que den
  se comporte à l'identique sur le chemin de production et sur le chemin de l'aide de test. Elle a une
  conséquence sur les tests : `TestExecRemediesAreThemselvesLegal` passe par `validateArgs` et est
  donc **aveugle** à l'exclusion ; le seul garde-fou est `TestExecPassesHelpToTheSandbox`, qui passe
  par un vrai `Execute()`.
- **`exec` passe à la dérivation aussi.** Garder une liste manuelle d'un côté et un walk de l'autre
  serait deux mécanismes pour un contrat, sur les deux commandes que ce chantier existe à réconcilier.

#### Le constructeur de remèdes porte deux défauts PRÉEXISTANTS

Les deux sont mesurés sur le binaire réel d'aujourd'hui, pas déduits. Ils sont absorbés ici parce que
le constructeur est réécrit de toute façon, et parce que `--repo` élargit leur portée : un remède qui
perd un drapeau perdait un `--workdir`, il perdra un MONTAGE.

**(F) Le constructeur perd les drapeaux que pflag a déjà consommés.**

```
$ den exec --workdir /srv -- api go build
den exec: `--` is not needed, and a sandbox name must come first — write `den exec api go build`
```

`--workdir /srv` a disparu de la proposition : `execRewrite` ne lit que `args`, et pflag avait
consommé le drapeau avant que le validateur ne tourne. La justification écrite en
`exec.go:199-202` — l'omission n'est pas un défaut « puisque le drapeau est un que cobra a honoré » —
est fausse en ses propres termes : cobra l'a honoré sur une invocation qui est REFUSÉE et ne tourne
jamais. Le remède est une ligne que l'utilisateur retape ; elle n'a pas de `--workdir`, donc il
obtient en silence un autre répertoire que celui demandé. La ligne de test qui bénit l'omission
(`{"a flag before a leading separator", …}`) encode le défaut et s'inverse.

Le constructeur gagne donc une seconde source : les drapeaux relus depuis le `FlagSet` (`Changed`),
en plus de ceux relevés dans la queue positionnelle. **`Value.String()` est interdit pour `--repo`,
`--only` et `--without`** : mesuré, un `stringArray` portant `/a` et `/tmp/hot fix` rend la chaîne
`[/a,/tmp/hot fix]`, qui se reparse en UN chemin bidon littéralement nommé ainsi — un remède
*syntaxiquement* légal, donc passant et le `strings.Contains` et le validateur de rejeu, et
sémantiquement faux. Il faut `SliceValue.GetSlice()` et **une paire `--repo <v>` par élément**, ce qui
conserve l'ordre de frappe dont le §2 c.1 dépend.

Deux règles à écrire, sinon elles seront devinées :

- **Ordre** : drapeaux relus d'abord, dans l'ordre de `VisitAll` — qui est LEXICAL, `SortFlags` valant
  vrai par défaut (mesuré) — puis les drapeaux relevés dans la queue, dans l'ordre de frappe. L'ordre
  entre drapeaux distincts est indifférent à pflag ; à l'intérieur de `--repo`, l'ordre de la tranche
  EST l'ordre de frappe.
- **Doublons** (`den run --workdir /a api --workdir /b go test`) : pour un drapeau scalaire, le
  **relevé** gagne et le relu est jeté — c'est le dernier tapé, et celui que den apprend à déplacer.
  Pour `--repo`, la répétition a un sens : les deux sont émis, relus d'abord (nécessairement tapés à
  gauche du nest), puis relevés. Sur `up`, l'entrelacement étant actif, rien n'est jamais relevé et le
  conflit ne peut pas naître.

**(G) `execLine` joint sur une espace simple, donc un chemin à espace produit un remède illégal.**

```
$ den exec api --workdir "/tmp/hot fix" true
den exec: den's flags go before the sandbox name — write `den exec --workdir /tmp/hot fix api true`
```

Rejoué, cela lie `--workdir=/tmp/hot` et laisse les positionnels `[fix api true]` : sandbox `fix`,
commande `api true`. Légal, et absurde. Les chemins à espace sont une entrée réelle
(`internal/nest/repos.go`, `repos_test.go:137`), et `--repo` la rend courante puisqu'un chemin de repo
vient de l'utilisateur. **Le test de rejeu ne l'attrape pas** : `TestExecRemediesAreThemselvesLegal`
rejoue avec `strings.Fields`, qui coupe sur l'espace exactement comme le bug, donc la mauvaise ligne
boucle proprement.

`execLine` cite donc chaque partie au sens shell : citer un jeton vide, ou contenant un caractère hors
de `[A-Za-z0-9_@%+=:,./-]`, avec des quotes **simples** (POSIX : pas d'interpolation, donc un chemin
portant `$`, `*`, `` ` `` ou `\` est sûr), une quote simple interne s'écrivant `'\''`. Sur toutes les
parties — valeurs, nom du nest, jetons de la commande — puisque chacune peut porter une espace. Le
remède devient ``den exec --workdir '/tmp/hot fix' api true``, une ligne collable.

**Le mécanisme de rejeu du test change** : `strings.Fields` ne peut plus servir, il couperait sur
l'espace même que la citation protège et déclarerait cassé le remède CORRIGÉ. Un `shellSplit` défait
la règle de citation dans le fichier de test. **Ne pas « corriger » cela en faisant rendre un `[]string`
au constructeur pour que le test le rejoue** : la propriété testée est que *la chaîne que den imprime
est légale quand un humain la tape*. Rejouer un argv contourne la jointure et rouvre exactement ce
trou.

## 6. Où passent les deux contradictions, et la migration

Deux refus de `internal/spawn/spawn.go` nomment `` `--` `` dans leur remède. Le séparateur
disparaissant, ces deux chaînes seraient des **conseils morts** le jour de la livraison — la panne
exacte que la tranche 1 a corrigée après revue (`den shell` proposait « donne une commande après
`--` » à une famille qui refuse `--`).

| Site | Aujourd'hui | Demain |
|---|---|---|
| `spawn.go:231` — `--detach` + commande | « --detach and a command after `--` contradict each other — drop one: … » | « …`den run` runs a command inside the sandbox — use `den up --detach <nest>` » |
| `spawn.go:254` — `-T` sans commande | « …give a command after `--`, or drop -T » | « …give a command with `den run -T <nest> <cmd>`, or drop -T » |

**Les deux refus restent atteignables tôt**, et c'est vérifié plutôt que supposé : ils sont à
l'étape 0 de `Spawn`, les `if` aux lignes 231 et 254 et les chaînes aux lignes 233 et 257 (le §7
cite les secondes, ce sont les mêmes refus), tandis que `config.LoadGlobal` est ligne 260 et
`source.Locate` juste après. `den up api -T` refuse donc sur `-T` sans lire une seule ligne de
configuration — un nest cassé ou une source absente ne peut pas voler le message.

**Aucun des deux contrôles ne déménage dans `internal/cli`.** `internal/spawn` possède la
contradiction entre champs de `spawn.Options` ; un second contrôle côté cobra serait deux sources
pour un verdict, ce que la tranche 1 a déjà refusé (`enterOptions`, « un probe porté mais jamais
consulté est la première moitié d'un second verdict »). Les deux drapeaux sont donc ENREGISTRÉS sur
la commande où ils n'ont pas de sens, et le refus qu'ils atteignent est celui qui existe déjà.

Le refus de `-T` de `internal/cli/shell.go:75-79` n'est pas touché : `den shell` entre dans une
sandbox vivante, il ne spawne pas, et sa contradiction porte sur la commande elle-même.

**La migration tient dans une ligne statique.** `spawn` → `up` est à distance d'édition 5, `spawn` →
`run` à 4, tous deux au-dessus du seuil 2 de `SuggestionsFor` : cobra ne suggérera rien sur
`den spawn api`. La ligne d'`unknownCommand` (`internal/cli/root.go`), qui porte déjà la migration
de 2026-08-05, absorbe la seconde :

```
`den <nest>` and `den spawn <nest>` no longer spawn: use `den up <nest>`, or `den run <nest> <cmd>`.
```

Elle reste **statique** : elle ne lit pas le den home, pour la raison du §4 de
`2026-08-05-spawn-command-design.md` — mettre une lecture de configuration faillible sur le chemin
d'erreur le plus banal du CLI.

## 7. Portée

Plus large que la tranche 1, qui tenait dans `internal/cli`.

| Fichier | Changement |
|---|---|
| `internal/cli/spawn.go` | supprimé → `up.go` + `run.go`, partageant un corps `spawnNest` comme `exec`/`shell` partagent `enterSandbox`. Emporte `Use:` (l. 35), `Short:` (l. 36) et les neuf enregistrements de drapeaux (l. 85-97), dont l'aide de `--detach` (« after the spawn ») |
| `internal/cli/exec.go` | `execFlags` manuel (l. 39-44) → table dérivée + classifieur écrit à la main (§5) ; `execShape`/`execRewrite`/`execLine` deviennent partagés et gagnent la relecture du `FlagSet` (F) et la citation shell (G) ; `spawnArgs` (l. 215-225) disparaît avec `--`, emportant sa chaîne « a nest must be named before `--` » (l. 221) ; commentaire coupé l. 433-434 |
| `internal/cli/root.go` | deux `AddCommand` au lieu d'un (l. 173) ; ligne de migration (l. 397) ; `SuggestFor: []string{"down"}` sur `newRmCmd` (§9.7) ; `atLeastOneArg` (l. 262-269) **perd ses deux derniers appelants** — `nest show` passe au validateur de migration, `spawnArgs` disparaît — et son commentaire (« nothing caps how many a spawn may mount ») est faux dès que les repos quittent les positionnels |
| `internal/cli/nest.go` | `den nest show` migre vers `--repo` (§7bis) : `Use:` l. 131, `Args:` l. 133, `opts.Repos = args[1:]` l. 193, enregistrement du drapeau l. 226-228 ; commentaires l. 43, 90, 147, 164-165 (coupé), 177, 190 à repointer |
| `internal/cli/source.go` | l. 40, sortie de `den source add` : `den spawn %s:<nest>` → `den up %s:<nest>` |
| `internal/config/stack.go` | l. 192, refus « no `image:` » : « `den spawn` looks for it there » → « `den up` ». **Manquée par la première rédaction** : c'est la SECONDE sortie de production, pas un commentaire |
| `internal/spawn/spawn.go` | deux chaînes de remède (§6, l. 233 et 257). Aucun changement de logique |
| `internal/spawn/interactive.go` | commentaire coupé l. 19-20, qui nomme `` `den spawn -- <cmd>` `` — forme supprimée |
| `internal/nest/resolve.go` | l. 19-22, doc de `Options.Repos` : « given as positionals on the command line » devient faux. Le champ reste `[]string`, donc `StringArrayVar` s'y lie sans changement de type |
| `internal/cli/testdata/unknown-command.golden` | à la main (il n'y a pas de `-update`) : ligne `spawn` (l. 17) retirée, `run` et `up` insérées dans l'ordre alphabétique, ligne de migration (l. 20) réécrite |
| `internal/cli/spawn_test.go` | scindé en `up_test.go` / `run_test.go`. 17 jetons argv `"spawn"`, l'aide `run(t, …)` l. 72 et 447 qui préfixe `"spawn"` à tout, et deux tests qui tapent `"--", "true"` (l. 384, 409) |
| `internal/cli/root_deps_test.go` | 4 argv `"spawn"` (l. 96, 127, 164, 223) + 2 `t.Fatalf` citant `den spawn api --detach` (l. 165, 225). Mécanique : aucun n'a de commande, tous vont sur `up` |
| `internal/cli/hostile_test.go` | 5 argv `"spawn"` (l. 67, 104, 150, 225, 248). Mécanique → `"up"` |
| `internal/cli/root_test.go` | l. 206 et 246 (texte, mécanique) ; l. 287-288, la ligne de table qui gèle l'usage ENTIER, se scinde en `up` (arité) et `run` (« no command given ») ; l. 416, `strings.Contains(got, "`den spawn <nest>`")` — à réécrire ET à élargir, c'est le motif que le §8 condamne |
| `internal/cli/nest_test.go` | `TestNestShowResolvesCommandLineRepos` (l. 567) et `TestNestShowResolvesRelativeCommandLineRepos` (l. 591) : positionnel → `--repo`. Commentaires l. 64 et 395 |
| `internal/spawn/spawn_test.go` | **commentaires seuls, plus l. 4608** (message d'échec). Les tests construisent `Options{Repos: …}` directement : le champ ne bouge pas, ils survivent intacts |
| `README.md` | 13 lignes : tableau (l. 81, 89), « Options of `den spawn` » (l. 98), `-w` (l. 115), instances (l. 129-130, 134), le bloc repos à la volée (l. 164-165, 168-172, 174), `den nest show` (l. 214), reprise après arrêt (l. 221), `image:` (l. 397), build (l. 463, 474), sources (l. 492, 494, 542) |
| `CHANGELOG.md` | une entrée sous `Changed`, rupture assumée, sans fenêtre de dépréciation. Les 9 occurrences existantes sont dans des entrées **publiées** : historiques, ne pas réécrire |
| `CLAUDE.md` | l. 136-137, la note « `den <nest>` → `den spawn <nest>` le 2026-08-05 » gagne le troisième palier |
| spec `2026-07-27-den-cli-design.md` | §2 (l. 61), §4.2 (l. 176), §5 (l. 218, 220, 234, 241), §6 (l. 248, 289, 343), §9.2 (l. 750, 752), §10 (l. 817), §11 (l. 926, 938), §12 (l. 984). **§14 / §14.1 ne sont PAS réécrits** : ce sont des relevés datés contre un `sbx` réel, et changer la commande citée falsifierait une mesure |

### Le balayage, reproductible — et pourquoi une seule grep ne suffit pas

La première rédaction annonçait « 49 occurrences … une seule est une sortie ». Les deux moitiés
étaient fausses. Le compte réel est **430**, et surtout **une grep de ligne ne peut structurellement
pas voir trois classes d'occurrence** dans ce dépôt. Quatre motifs, à relancer plutôt qu'à croire :

```bash
# P1 — la forme écrite, sur une ligne
grep -rn --exclude-dir=.git --exclude-dir=worktrees -E 'den spawn|den <nest>' .

# P2 — occurrences COUPÉES par un retour à la ligne de commentaire
grep -rn --exclude-dir=.git --exclude-dir=worktrees --include='*.go' -A1 -E '`den$' . \
  | grep -E '^\S+-[0-9]+-[[:space:]]*//[[:space:]]*spawn'

# P3 — le jeton argv des tests (aucun ne contient « den spawn »)
grep -rn --exclude-dir=.git --exclude-dir=worktrees --include='*_test.go' -F '"spawn"' internal/cli

# P4 — Use:/Short:/identifiants (invisibles à P1)
grep -rn --exclude-dir=.git --exclude-dir=worktrees --include='*.go' \
  -E 'newSpawnCmd|spawnArgs|"spawn <nest>|Spawn or attach' .
```

Au 2026-08-16 : **P1 = 430**, P2 = 3 en Go vivant, **P3 = 17** — les seuls qui cassent `task test`,
et qu'aucune recherche de « den spawn » ne trouve — P4 = 18.

Sur les 430 de P1, **203 sont dans les plans, handoffs et décisions datés** sous
`docs/superpowers/`, historiques par convention (CLAUDE.md) et jamais réécrits, et 19 dans la
présente spec. **L'ensemble à relire est donc de 208**, dont 144 commentaires, 25 assertions ou argv
de test, et **11 sites de sortie utilisateur** — pas un.

Ces 11 sites sont ceux qui livreraient un conseil mort. Quatre échappent à P1 : `Use:` et `Short:`
de `spawn.go` (rendus dans `den help`, dans `cmd.UseLine()` à l'intérieur de l'erreur d'arité que
`root_test.go:288` gèle, et recopiés gelés dans `unknown-command.golden`), l'aide de `--detach`
(« after the spawn »), et la chaîne de `spawnArgs`.

`internal/sbx`, `internal/manifest`, `internal/policy`, `internal/worktree`, `internal/build`,
`internal/agent`, `internal/doctor` : commentaires seuls, aucun changement obligatoire.
**`internal/nest` n'est PAS intact** — la première rédaction le déclarait tel — puisque la doc de
`Options.Repos` décrit une origine positionnelle qui disparaît.

### `den nest show` suit, sinon la césure du §4 est livrée

`den nest show <nest> [repo...]` (`internal/cli/nest.go:131`) est le dry-run de la famille et porte
les repos à la volée en **positionnels**. Le laisser tel quel, c'est livrer exactement ce que le §4
refuse en écartant la césure — « un repo à la volée s'écrit de deux façons entre deux commandes
sœurs » —, et le README le montre à trois lignes d'écart (`den spawn api ~/dev/hotfix` l. 169,
`den nest show scratch ~/dev/a` l. 172).

`nest.Options.Repos` est déjà `[]string` (`internal/nest/resolve.go:22`), donc `StringArrayVar` s'y
lie sans changement de type, et la garde `if len(opts.Repos) > 0 { opts.Cwd, err = os.Getwd() }`
(l. 194-200) lit la longueur, pas l'origine : elle ne bouge pas.

**Le validateur.** `exactlyOneArg` ouvrirait une branche « trop d'arguments » aujourd'hui
inatteignable, dont le message — « exactly one argument expected, 2 received » — ne nomme ni
`--repo` ni ce qui a changé. C'est mot pour mot le grief que le §5 oppose à `exactlyOneArg` sur
`den up`. `den nest show` réutilise donc **le validateur de migration de `up`**, dont le remède
devient `den nest show --repo ~/dev/hotfix api` ; le dry-run ne peut pas être le seul endroit où la
migration n'est pas nommée.

**Pas de `SetInterspersed(false)`**, pour le raisonnement de `internal/cli/shell.go:93-100` mot pour
mot — `den nest show` ne prend AUCUNE commande, donc aucun drapeau n'a de second propriétaire
possible. Ce n'est pas seulement neutre, c'est **obligatoire** : trois tests existants tapent le
drapeau APRÈS le nom du nest (`nest_test.go:79`, `:318`, `:358`). Sous `SetInterspersed(false)` ces
lignes deviendraient des positionnels refusés pour leur ARITÉ.

**Les commentaires qui promettent une identité de message.** Deux visent des fonctions que `up` ET
`run` atteignent par le corps partagé : le repointage véridique nomme **les deux**.

- `nest.go:147` — « so `den nest show` and `den spawn` never resolve the SAME reference to two
  different nests » → `` `den nest show`, `den up` and `den run` ``.
- `nest.go:177-178` — « must stay word-identical between `den nest show` and `den spawn` … or the
  two would resolve » → les trois noms, et « the two » devient « the three ».
- `nest.go:190` — « The dry-run of `den spawn <nest> [repo...]` » → `` `den up <nest>` /
  `den run <nest> <cmd>` ``.
- `nest.go:164-165` — l'occurrence COUPÉE (P2), « a command `den spawn` would have rejected ».

**Aucun golden** : `internal/cli/testdata/` ne contient que `ports-*.golden` et
`unknown-command.golden`, dont aucun ne passe par `den nest show`.

## 8. Tests

**Qui s'inversent ou déménagent** — ce qu'une revue doit regarder :

| Aujourd'hui | Demain |
|---|---|
| `TestSpawnRefusesNoTTYWithNoCommand` | même nom sur `up` ; assertion sur le message ENTIER, qui doit nommer `den run` |
| `TestSpawnRefusesDetachWithACommand` | déménage sur `run` ; message ENTIER, qui doit nommer `den up --detach` |
| `TestSpawnWithoutANestNamesTheUsageLine` | se scinde : `up` (arité) et `run` (« no command given ») |
| les tests de repos positionnels d'`internal/cli/spawn_test.go` | réécrits en `--repo`, dont un qui garde l'ORDRE de frappe sur deux `--repo`. **Deux fichiers portent ce nom** : `internal/spawn/spawn_test.go` construit `Options{Repos: …}` directement et survit intact |
| `TestExecRemediesAreThemselvesLegal` | mécanisme de rejeu remplacé : `shellSplit` au lieu de `strings.Fields`, sans quoi le remède CORRIGÉ par (G) est déclaré cassé |
| la ligne `{"a flag before a leading separator", …}` | s'INVERSE : le remède attendu devient `den exec -T api go build`. Elle bénissait le défaut (F) |

L'assertion sur le message **entier** n'est pas du zèle : la tranche 1 a livré un remède mort parce
qu'un `strings.Contains(err, "-T")` ne regardait pas la moitié qui avait pourri.

**Nouveaux — le classifieur (§5)** :

- `TestRunRefusesDenFlagsAfterTheNestName` — table sur TOUTES les orthographes mesurées
  (`--workdir /srv`, `--workdir=/srv`, `-w feat`, `-wfeat`, `-w=feat`, `-iT`, `-Ti`, `-iTw feat`,
  `-iTwfeat`, `-iT=true`, `--repo /a`, `--`). Les lignes que la comparaison exacte d'aujourd'hui
  manquerait SONT le test ;
- `TestRunLiftsAShorthandClusterWithItsValue` — `den run api -iTw feat go test` propose
  `den run -iTw feat api go test` : la valeur voyage avec la grappe au lieu d'être abandonnée ;
- `TestRunTreatsAnEqualsInsideAClusterAsTheValue` — `-iT=true`, `-Ti=false`, `-wi feat` ; épingle la
  règle d'ordre de pflag que le parcours naïf inverse ;
- `TestRunPassesTheCommandsOwnFlagsThrough` — `den run api go test -v -run TestX` ; garde contre un
  classifieur qui déborde ;
- `TestRunPassesUnknownClustersToTheSandbox` — `den run api -Tv go build` accepté, `-Tv` atteint la
  VM : une lettre inconnue disqualifie la grappe entière, statu quo d'`exec` ;
- `TestRunPassesHelpToTheSandbox` — **doit passer par `executeCmdSeparateStreams`**, donc par un
  vrai `Execute()` : `--help` est absent du walk sous `Find`+`ParseFlags`, donc un test bâti sur
  `validateArgs` est aveugle à cette régression ;
- `TestExecPassesHelpToTheSandbox` — inchangé, et désormais porteur : c'est le seul garde-fou de
  l'exclusion explicite côté `exec` ;
- `TestDerivedFlagSetSeesThePersistentDenHome` — `den run api --den-home /tmp true` refusé : la
  persistante est dans la table dérivée sans être listée nulle part.

**Nouveaux — les validateurs (D, I)** :

- `TestUpRefusesADoubleDashWithACommandByNamingRun` — `den up api -- go test` ; message ENTIER
  nommant `den run api go test`, et ne nommant NI `--repo` NI « extra arguments » ;
- `TestUpRefusesAUselessDoubleDash` — `den up -- api` et `den up api --` (les cas `dash==0` et
  `dash==len(args)`) ;
- `TestUpNamesTheGlobWhenRepoGotSeveralPaths` — le message « expanded to several », et l'absence de
  tout ``write `…` `` nommant un répertoire comme nest ;
- `TestUpNamesTheRepoFlagOnASecondPositional` — `den up api ~/dev/hotfix` sans `--repo` : la branche
  du glob ne doit pas avaler le cas pour lequel ce validateur existe ;
- `TestUpKeepsInterspersedFlags` — `den up api -T` atteint le refus nommé, pas l'erreur d'arité.

**Nouveaux — le constructeur de remèdes (F, G)** :

- `TestExecRemediesCarryFlagsTypedBeforeTheSeparator` — `den exec --workdir /srv -- api go build`
  propose un remède QUI PORTE `--workdir /srv` ;
- `TestRunRemediesCarryEveryRepoTypedBeforeTheNestName` — deux `--repo` relus sortent en deux paires
  dans l'ordre de frappe ; la forme `[/a,/b]` de `Value.String()` ne doit apparaître nulle part ;
- `TestUpRemedyCarriesTheRepoAcrossADoubleDash` — `den up --repo /a -- api` → `den up --repo /a api` ;
- `TestExecRemedyPrefersTheFlagTypedAfterTheName` — épingle la règle de doublon ;
- `TestRunRemedyQuotesAValueContainingASpace` / `TestExecRemedyQuotesAValueContainingASpace` —
  `'/tmp/hot fix'`, assertion sur la chaîne ENTIÈRE ;
- `TestShellSplitUndoesTheQuotingRule` — le séparateur de rejeu lui-même ; un helper faux rend
  vacants tous les tests ci-dessus ;
- `TestRunRemediesAreThemselvesLegal` — le jumeau de la propriété de la tranche 1, rejoué par
  `shellSplit`.

**Nouveaux — l'avertissement (§5)** :

- `TestRunWarnsWhenTheFirstCommandTokenIsADirectory` — message ENTIER, plus la forme
  `den run api ~/dev/hotfix` dont le remède doit nommer `den up` ; l'avis entre dans
  `TestRunRemediesAreThemselvesLegal` au même titre qu'un refus.

**Nouveaux — le reste** :

- `TestRepoFlagDoesNotSplitOnComma` — garde la mesure (c.2), le choix `StringArrayVar` contre
  `StringSliceVar`, invisible à la lecture ;
- `TestUpStillReadsDenHomeBeforeTheSubcommand` — garde la mesure (c.3) sur la persistante.

### Trois commits, et le constructeur passe en premier

La réécriture du constructeur touche **toutes** les fonctions de la famille : `execFlags` (supprimé),
`execFlagOf` (remplacé par le classifieur), `execRewrite` (nouvelle consommation, plus la relecture
du `FlagSet`), `execLine` (citation). La suite existante d'`exec` — en particulier
`TestExecRemediesAreThemselvesLegal` — est le **seul oracle** de l'ancien comportement, et elle cesse
d'en être un dès que `up` et `run` existent : une régression peut alors se cacher derrière une
attente neuve.

1. **Commit 1 — le classifieur (E).** Sur `exec` seul, la dérivation est **conservatrice**, et c'est
   vérifiable : la table dérivée d'`exec` vaut `{workdir, no-tty/-T, den-home}` plus l'exclusion de
   l'aide, soit exactement les quatre entrées écrites à la main. **Porte : la suite d'`exec` verte
   avec ZÉRO édition de test.** Toute rupture signifie que le classifieur a changé le comportement
   d'`exec`. (Le seul changement visible est le placeholder, `<dir>` → `<workdir>` ; aucun test ne
   l'asserte, vérifié par grep.)
2. **Commit 2 — la relecture des drapeaux (F).** Inverse exactement une attente existante, et
   réécrit `exec.go:199-202` d'« omission décidée » en « défaut corrigé ». Diff sur deux fichiers.
3. **Commit 3 — la citation (G).** Remplace `strings.Fields` par `shellSplit` comme mécanisme de
   rejeu et ajoute les lignes à espace. Autonome.

`up` et `run` atterrissent ensuite sur un constructeur déjà correct, et D comme I ne sont plus que du
travail de validateur. Fondus dans la naissance de deux commandes, un bug de classifieur et un bug de
relecture sont indiscernables dans le diff, et le test qui casse ne nomme ni l'un ni l'autre.

Les conventions du dépôt tiennent sans aménagement : aucun `t.Parallel()`, aucun socket, aucun
processus, `sbx.Fake` suffit, et `worktree.NeutralizeGitEnvironment()` reste appelé dans le
`TestMain` de `cli`.

## 9. Divergences assumées

1. **`--workdir` contre le `-w` de compose.** Permanente, héritée de la tranche 1 : `-w` est la
   worktree de den, et le sens compose (workdir) ne peut pas l'avoir.
2. **`den run` n'est pas éphémère** (§4). compose jette son conteneur, den garde sa sandbox.
3. **Pas de `-v` pour `--repo`.** compose épelle `-v` un volume ; den monte des repos, et un
   raccourci d'une lettre coûterait une collision pour un drapeau rare.
4. **`--only` / `--without` n'adressent toujours QUE les repos déclarés.** Le §6 de
   `2026-08-04-adhoc-repos-design.md` le décidait pour des positionnels (« un repo à la volée se
   retire en ne le tapant pas ») ; l'argument ne dépend pas de l'orthographe et vaut mot pour mot
   pour `--repo`. Rien ne change non plus dans la fusion des listes ni dans `checkUniqueNames`, qui
   garde l'unicité des basenames. Écrit ici parce qu'un drapeau, contrairement à un positionnel,
   INVITE la question — et que l'inventer au moment du plan serait un élargissement silencieux.
5. **`den run -d` n'existe pas.** compose a `run -d` (détacher LA COMMANDE). den a
   `den up --detach`, qui est le `up -d` de compose. Hors périmètre, nommé pour qu'un lecteur ne le
   prenne pas pour un oubli.
6. **`den up` n'ouvre pas ce que `docker compose up` ouvre.** La mesure (a) n'a acheté que la moitié
   « crée-ou-rattache » du verbe ; l'autre moitié diverge, et elle est mesurée le 2026-08-16 sur
   Docker Compose v5.3.1. Un `docker compose up` au premier plan écrit `Attaching to api-1`, relaie
   la sortie des services préfixée (`api-1  | hello-from-api`), et un `SIGINT` ARRÊTE les conteneurs
   (`Compose Stopping Gracefully… / Container upfg-api-1 Stopped`, statut final `Exited (137)`). Il
   n'ouvre jamais de shell. `den up <nest>` ouvre `bash -l` — un `spawn.Command.Argv` vide vaut
   `bash -l`, `internal/spawn/enter.go:85-86` — et en sortir ne détruit rien : `spawn.Enter` est la
   DERNIÈRE instruction de `Spawn` (`internal/spawn/spawn.go:1415`), rien ne tourne après elle, et
   `den rm` est le seul destructeur. (sbx range les sandboxes inactives de son côté — ~45 s, spec
   2026-07-27 §11 — l'état est conservé et `CheckAttachable` les laisse repasser.) Un lecteur de
   compose qui tape `den up api` attend des logs et un Ctrl-C qui range ; il obtient un shell et une
   VM qui reste.
7. **compose apparie `up` avec `down` ; den n'aura pas de `down`.** Une surface en forme de compose
   entraîne à taper `den down api`, et cobra ne rattrape rien tout seul : `down` est à distance
   d'édition 3 du plus proche (`run`), 4 de `up`, `exec`, `ls`, `nest`, `ports`, `rm`, `lint`,
   `init` et `doctor`, au-dessus du seuil 2 de `SuggestionsMinimumDistance`
   (`internal/cli/root.go:120`), et il n'est le préfixe d'aucun nom — l'autre voie de
   `SuggestionsFor`. Vérifié sur l'arbre réel le 2026-08-16 : `den down` imprime la liste des
   commandes sans aucun « did you mean ». La ligne de migration, elle, est statique et ne parle que
   de `spawn` : elle ne dira jamais rien de `down`.

   Le geste est `den rm`, et c'est `SuggestFor` qui le dit — un champ de la commande `rm`, pas une
   ligne de plus dans le message :

   ```go
   SuggestFor: []string{"down"},   // sur newRmCmd
   ```

   Mesuré le 2026-08-16 sur cobra v1.10.2, l'arbre de den reproduit : `SuggestionsFor("down")` rend
   `[rm]`, donc `unknownCommandError` — qui appelle `SuggestionsFor` directement
   (`internal/cli/root.go:367`) — imprime « did you mean `den rm`? » au-dessus de la liste.
   `SuggestionsFor("dwon")` rend toujours `[]` : le champ n'élargit rien d'autre.

   Deux formes ont été écartées. Une **ligne statique** dans `unknownCommand` : cette ligne porte
   l'histoire de ce que den a RETIRÉ (`den <nest>`, `den spawn <nest>`), et un verbe que den n'a
   jamais eu n'en fait pas partie ; le commentaire d'`unknownCommandError`
   (`internal/cli/root.go:359-363`) a déjà refusé de la faire grossir par gentillesse. Une **ligne
   de §10 seule** : elle informe le lecteur de la spec, pas l'utilisateur qui tape.

   Le coût, nommé : `den rm` détruit sans confirmation (`sbx rm --force`, `internal/cli/rm.go:83`),
   donc den suggère une destruction à qui s'est trompé de verbe. Il la SUGGÈRE seulement — la ligne
   n'exécute rien — et le `Short` de `rm` (« Destroy a sandbox (the agent profile persists) »)
   s'imprime dans la même liste, quelques lignes plus bas.

## 10. Hors périmètre

- Une sandbox jetable (`--rm`) : den n'a pas cet objet, et l'inventer est un chantier à soi seul.
- `den run -d` (§9.5).
- **`den down`.** den n'a pas ce verbe et n'en aura pas : entre « la sandbox vit » et « la sandbox
  n'est plus », il n'y a rien que den possède — sbx range les inactives lui-même — donc `den rm` est
  la réponse entière. Le `SuggestFor` du §9.7 est ce qui la met devant l'utilisateur ; cette ligne
  dit seulement que l'absence est une décision.
- Le sort de `--agent` (issue #50), porté à l'identique comme en 2026-08-05.
- Les noms `den agent` / `den review` réservés par la spec 2026-07-27 §5 : ce chantier libère la
  place de `spawn` dans cette famille, il ne décide rien de ce qu'ils deviendront.
