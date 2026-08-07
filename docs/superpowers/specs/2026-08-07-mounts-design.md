# Design — `mounts:` : monter un chemin hôte et le rendre atteignable dans la VM

**Date:** 2026-08-07
**Statut:** design validé, non implémenté
**Remplace:** rien. **Modifie:** spec §10 (modèle SSH), `internal/spawn`, `internal/agent`,
`internal/config`.

## Déclencheur

`dgdev ssh` échoue dans le nest `dg:op-inscription`. Diagnostic complet dans
`claudedocs/2026-08-07-dgdev-ssh-in-den.md`. Deux causes indépendantes, dont l'une révèle un
trou dans den lui-même :

> `ssh.mode: mount` monte `ssh.dir` dans la VM et s'arrête là. La clé arrive, permissions
> `0600` intactes — et `ssh` ne la trouve jamais.

Mesuré sur sandbox jetable `sshpathprobe` (`sbx create --name sshpathprobe shell
/Users/polochon/.ssh_sbx`, supprimée depuis) :

```
bind-9e977d4980a918dd on /Users/polochon/.ssh_sbx type virtiofs (rw,relatime)
-rw-------+ 1 agent agent 419 id_ed25519
HOME=/home/agent
/home/agent/.ssh: No such file or directory
```

`ssh` cherche dans `$HOME/.ssh`. `$HOME` vaut `/home/agent` dans la VM, alors que sbx monte
tout workspace à son chemin **hôte** (hypothèse A11 de la spec, confirmée ici). Deux endroits
différents. La spec §10 qualifie pourtant ce mode de « Simple, headless-ready » — or headless
est exactement le cas où personne n'est là pour taper `-i`.

Le spike `../sbx-devbox/docs/design/2026-07-27-spike-ssh-in-vm.md` avait déjà nommé le piège,
mais en le comptant comme un **avantage d'`agent-forward`** (ligne 125 : « pas de piège de
chemin (`$HOME` VM = `/home/agent` ≠ chemin de mount hôte) »). Le trou côté `mount` n'en a
jamais été déduit.

## Le problème réel, plus large que SSH

`ssh.dir` n'est pas un cas particulier. C'est une instance d'un besoin générique que den ne
sait pas exprimer : **faire entrer un dossier hôte dans la VM, et le rendre atteignable là où
l'outil le cherche.**

Aujourd'hui, un chemin hôte n'entre dans la VM que par quatre portes, toutes fermées à clé :
`repos:`, les worktrees, le répertoire de config de l'agent, et `ssh.dir` en mode `mount`.
Rien d'autre ne passe.

Deux cas concrets, et ils ne se résolvent pas pareil :

| | monté ? | atteignable ? | par quoi |
|---|---|---|---|
| config de l'agent | oui | **oui** | `CLAUDE_CONFIG_DIR` pointe le chemin hôte (jeton `{config_dir}`, `resolve.go:86`) |
| `ssh.dir` | oui | **non** | `ssh` n'a aucune variable d'environnement équivalente |
| `~/.digitaleo` | **non** | non | aucune porte |

L'agent s'en sort parce que Claude Code lit une variable d'environnement. `ssh` lit
`$HOME/.ssh` ou un `-F` que den ne contrôle pas — et `dgdev ssh` construit son propre argv
(`internal/remotedev/ssh.go:41`), sans `-i` ni `-F` à intercaler. La ruse de la variable ne
généralise pas.

`~/.digitaleo` cumule les deux manques. Vérifié : l'hôte y tient `dgdev.config` (514 B), les
secrets consul et le VPN ; la VM en a un **autre**, local, créé par `dgdev` lui-même. Monter
celui de l'hôte le poserait à `/Users/polochon/.digitaleo`, quand `dgdev` lit
`$HOME/.digitaleo` = `/home/agent/.digitaleo`.

### Ce qui n'est pas achetable

`sbx create --help`, relevé le **2026-08-07** :

```
Usage: sbx create [flags] AGENT PATH [PATH...]
Flags: --clone --cpus --kit --memory --name --profile --quiet --template
```

Aucun flag de cible de mount. Seul un suffixe `:ro` est documenté sur un chemin
(`sbx create claude . /path/to/docs:ro`). **den ne peut donc pas choisir où un chemin atterrit
dans la VM.** Le seul pont possible est un lien symbolique posé au démarrage.

C'est la contrainte structurante de tout ce design : on ne monte pas *à* un endroit, on monte
puis on **lie**.

## Surface de configuration

```yaml
# ~/.den/config.yaml
mounts:
  - host: ~/.digitaleo       # chemin HÔTE, expansé par den
    link: $HOME/.digitaleo   # chemin VM, expansé par la VM
  - host: ~/.aws
    link: $HOME/.aws
    ro: true
```

Trois champs. `link` et `ro` sont optionnels : un mount sans `link` n'est atteignable qu'à son
chemin hôte — ce qui suffit quand l'outil consommateur lit une variable d'environnement, comme
la config de l'agent.

### La règle d'expansion

Énoncée comme une règle, et non laissée implicite, parce que la confondre **est** le bug qui a
déclenché ce design :

- **`host:` est un chemin HÔTE**, expansé par den (`ExpandPath`, comme `repos:` et `ssh.dir`).
- **`link:` est un chemin VM**, expansé par le bash de la VM.

Deux machines, deux `$HOME` : `/Users/polochon` et `/home/agent`. Le précédent existe déjà dans
den — `internal/agent/freshness.go:45` laisse délibérément `$HOME` non échappé pour que le bash
de la VM l'expanse, avec le commentaire qui dit pourquoi.

### Global uniquement, pas la cascade

`mounts:` vit dans `~/.den/config.yaml` seul. Ni stack, ni nest.

Ce n'est pas de la paresse : den **refuse déjà** un `path:` sur un nest venu d'une source,
parce qu'un chemin hôte ne voyage pas d'une machine à l'autre — c'est toute la raison de
l'indirection `key:` de `repos:`, et `den lint` en est le juge. Un stack de la source `dg`
déclarant `host: ~/.digitaleo` réintroduirait exactement ce que ce lint existe pour refuser.

Si des mounts par stack deviennent nécessaires, il leur faudra la même indirection par clé que
`repos:` — c'est un design à part entière, pas une extension gratuite. `ssh` est déjà
global-only pour une raison comparable.

### `ssh.mode: mount` devient du sucre

`ssh.mode: mount` + `ssh.dir: X` se résout exactement en `{host: X, link: $HOME/.ssh}`, injecté
dans la liste de mounts.

Les deux clés **restent** : le basculement est réel et mérite d'exister, `agent-forward` et
`mount` étant deux postures de sécurité distinctes. Ce qui disparaît, c'est le chemin de code
privé du mode (`spawn.go:582`).

Test de suppression : retirer `mounts:` fait mourir `ssh.mode: mount` avec lui, parce que plus
rien d'autre ne le porte. Un seul mécanisme, une seule surface de test.

`ro: true` se mappe sur le suffixe `:ro` natif de sbx. Défaut : lecture-écriture. **Ne pas**
mettre `.ssh` en `ro` par défaut — `ssh` écrit `known_hosts`, et un mount en lecture seule
transforme ça en échec obscur.

## Mécanique

Deux choses se produisent par mount, à deux moments différents.

### 1. Le mount, au `sbx create`

Chaque `mounts[].host` est ajouté à la liste des workspaces dans `spawn.go`, **après** les
repos, les worktrees et le répertoire de config de l'agent. Jamais en position 0 : le premier
workspace devient le `-w` de l'attache (commentaire existant, `spawn.go:565`). `ssh.dir` occupe
cette dernière place aujourd'hui (`spawn.go:583`) ; les mounts la prennent.

Avec `ro: true`, l'entrée d'argv devient `<host>:ro`.

### 2. Le lien, au boot de la VM

La mixin de den porte déjà `commands.startup`, et cette clé est une **séquence** —
`mixin.go:109-111` la construit avec une seule entrée aujourd'hui, la commande de fraîcheur de
l'agent. La phase de liens devient une seconde entrée, émise **en premier**.

#### Mesuré, pas supposé : sbx exécute bien plusieurs entrées, dans l'ordre déclaré

`mixin.go:109` prouve la forme de sortie de **den**, pas que sbx exécute plus d'une entrée. Tous
les kits existants (`ssh-known-hosts`, `dg:base`, la mixin de den) n'en portent qu'une seule.
Si sbx ne lançait que `startup[0]`, ce design supprimerait silencieusement `claude update` —
un fail-OPEN sur la fraîcheur de l'agent, exactement ce que le garde de `RenderMixin` refuse.

Sonde `twostepprobe` (kit à deux entrées, chacune horodatant `/tmp/order.txt` ; sandbox
supprimée depuis), le 2026-08-07 :

```
FIRST  1786099450263315292
SECOND 1786099450268467792
```

Et le journal du dispatcher montre pourquoi : sbx développe **chaque entrée en son propre
script numéroté**, puis les joue dans l'ordre.

```
=== dispatcher run 2026-08-07T10:44:10Z ===
> /etc/durable-startup.d/001-startup-shell/000-cmd.sh
ok /etc/durable-startup.d/001-startup-shell/000-cmd.sh
> /etc/durable-startup.d/002-startup-twostep/000-cmd.sh
ok /etc/durable-startup.d/002-startup-twostep/000-cmd.sh
> /etc/durable-startup.d/002-startup-twostep/001-cmd.sh
ok /etc/durable-startup.d/002-startup-twostep/001-cmd.sh
=== dispatcher complete ===
```

Deux ordres en découlent, tous deux déterministes et **observables dans ce journal** : les kits
entre eux par leur position de layering (`001-`, `002-`), et les entrées d'un kit par leur index
de déclaration (`000-cmd.sh`, `001-cmd.sh`). La mixin de den étant layerée en dernier
(`stacks/base/stack.yaml` : « `kit:` … layeré en DERNIER » ; description du kit `dg:base` :
« layeré au `create` avant le mixin généré par den »), **la phase de liens passe après tous les
kits de stack** — la bonne place, puisqu'elle doit pouvoir constater ce qu'ils ont créé.

Forme retenue :

```yaml
commands:
  startup:
    - command: [bash, -c, "<phase de liens>"]   # nouveau, passe en premier
    - command: [bash, -c, "<claude update>"]    # existant, inchangé
```

En premier et non en dernier, parce que la commande de fraîcheur lance l'updater de l'agent :
`claude update` touche le réseau et git, donc il veut `~/.ssh` déjà en place. Des liens posés
après laisseraient le **tout premier** boot tourner sur des chemins non liés, et seul le second
boot serait correct. Ce genre de bug est invisible jusqu'à ce que quelqu'un redémarre et voie
le symptôme disparaître.

La phase est **un seul `bash -c`** couvrant tous les mounts, pas une entrée par mount. C'est un
étage générique dont `.ssh` se trouve être le seul consommateur au premier jour — rien dedans
ne sait ce qu'est `.ssh`.

Par mount porteur d'un `link:` : `mkdir -p "$(dirname LINK)"`, puis résolution de l'état de la
cible (table ci-dessous), puis `ln -sfn HOST LINK`.

### Détection de dérive — À CODER, contrairement à ce que ce document affirmait

**Correction du 2026-08-07, après lecture de `internal/agent/drift.go`** (le seul fichier
d'`internal/agent` que la rédaction initiale n'avait pas ouvert) : la dérive **n'est pas**
gratuite.

`ReadMixin` relit la mixin sur disque à travers un `parsedSpec` qui ne retient qu'un champ de
`commands.startup` :

```go
// drift.go:77-82
// RenderMixin only ever emits a single startup command, and freshness is …
if n := len(spec.Commands.Startup); n > 0 {
    m.Freshness = spec.Commands.Startup[n-1].Command
}
```

Deux conséquences, de gravité inégale :

1. **Le `n-1` reste juste.** Il prend la **dernière** entrée, et la fraîcheur reste la dernière
   par construction (spec §9.1). Rien à corriger là. Seul le commentaire au-dessus devient faux.
2. **`Links` n'est jamais relu, donc jamais comparé** (`drift.go:136` ne compare que
   `Freshness`). Éditer `mounts:` et relancer `den spawn` sur une sandbox vivante ne
   signalerait **rien** — exactement le silence que cette section prétendait éviter.

Il faut donc : que `ReadMixin` peuple `Links` depuis `Startup[0]` quand la séquence porte deux
entrées, et que la comparaison inclue `Links`. C'est du code, et c'est une tâche du plan.

`den doctor` non plus n'est pas gratuit : la sonde `mounts[].host` a la même forme que celle des
kits et de `ssh.dir`, mais elle reste à écrire.

### En revanche, `internal/manifest` n'a rien à apprendre — et c'est délibéré

Le manifeste `state/sandboxes/<sandbox>.yaml` n'enregistre **aucune** liste de workspaces
(`grep Workspace internal/manifest/*.go` → vide) : il enregistre les repos, parce qu'un repo
dépend du nest, que le nest peut changer, et que `den rm` doit pouvoir nettoyer un worktree sans
re-dériver quoi que ce soit.

Un mount n'a aucune de ces propriétés. Il vient de `config.yaml` global, il est re-dérivable à
tout instant, et il ne crée pas de worktree à nettoyer. Et `den ls` compte `b.Workspaces` tel
que **sbx** le rapporte (`internal/cli/ls.go:131`), pas le manifeste : les mounts y apparaissent
donc correctement sans une ligne de code.

Écrit ici plutôt que passé sous silence, la doctrine T13/T16 voulant qu'un lecteur ne retombe
sur la dérivation que lorsque l'enregistrement est absent.

### Hors périmètre, délibérément

Toute tentative d'appliquer un nouveau mount à une sandbox vivante. Les mounts sont fixés à la
création côté sbx, den ne réapplique rien à l'attache, et prétendre le contraire donne un den
qui ment sur l'état de la VM.

## Gestion d'erreur

Deux barrières, à deux moments choisis.

### Côté hôte, au spawn, avant le premier effet de bord

Tout `mounts[].host` doit exister. Même sonde que le contrôle `ssh.dir` existant
(`spawn.go:427`), nommant le chemin et le fichier à corriger. Ça se place avec les contrôles de
repos et de kits, dans le bloc dont l'objet est précisément qu'un refus ne laisse jamais un
worktree orphelin derrière lui.

**Le message d'`ssh.dir` ne doit pas mourir avec le désucrage.** Celui d'aujourd'hui est
spécifique et bon : « *in "mount" mode this directory is mounted in the sandbox, and a missing
path would mount an empty directory instead of your keys* ». Un contrôle générique sur
`mounts[].host` le remplacerait par une phrase qui ne parle plus ni de clés ni de `ssh.dir`,
et pointerait `mounts:` — une clé que l'utilisateur n'a pas écrite, puisqu'elle vient du sucre.

Donc : une entrée de mount **sait d'où elle vient**, et le refus cite la clé d'origine. Issue du
sucre → le message et le fichier restent ceux d'`ssh.dir`. Écrite à la main → ils désignent
`mounts:`. Le test qui couvre le message actuel est à conserver tel quel, et il rejoint la liste
des tests à mettre à jour.

### Côté VM, au boot — l'état de la cible, invisible depuis l'hôte

| cible | action |
|---|---|
| absente | `ln -sfn HOST LINK` |
| déjà un lien symbolique | `ln -sfn HOST LINK` — idempotent, et **réécrit** un lien qui pointait ailleurs (c'est voulu : la config fait autorité sur la VM) |
| répertoire **vide** | `rmdir LINK` puis `ln -sfn HOST LINK` |
| répertoire **non vide**, ou fichier | **refuser, fail-closed**, en nommant le chemin |

Aucun `rm -rf` n'apparaît dans cette phase. `rmdir` échoue sur un répertoire non vide : même si
le test de vacuité était contourné par une écriture concurrente, la pire issue reste un boot
refusé, jamais une donnée détruite.

Le partage sur la vacuité est le seul jugement de ce design.

Un répertoire vide est un emplacement réservé posé par l'image de base : le prendre est sans
risque, et refuser rendrait certaines cibles définitivement inutilisables, puisque le chemin de
l'outil est fixe et que l'utilisateur ne peut pas en choisir un autre.

**Vérifié pour le cas motivant** — un fail-closed qui casserait `~/.digitaleo` rendrait la
fonctionnalité inutilisable dès le premier essai. Le kit `dg:base` ne crée **pas** ce
répertoire : sa seule commande de démarrage écrit `/etc/profile.d/99-digitaleo-path.sh`, qui
*mentionne* `$HOME/.digitaleo/bin` dans le `PATH` sans jamais le `mkdir`. Rien d'autre dans la
source `dg` ne le crée non plus (`grep mkdir.*digitaleo` → aucun résultat). Sur une VM neuve la
cible est donc absente, et le lien se pose proprement. Le répertoire observé dans la sandbox
vivante a été créé **par l'usage**, après le boot, par `dgdev` lui-même.

Un répertoire non vide porte des données : `~/.claude` dans la VM est un vrai profil. Et
`ln -sfn` sur un répertoire existant ne le remplace pas — il crée silencieusement `LINK/<nom>`
**dedans**. Ce mode de défaillance se lit « l'outil ignore ma config », soit exactement le
symptôme qui a coûté la session de diagnostic. den refuse plutôt que de normaliser en silence
(spec §2).

Le fail-closed au boot est cohérent avec la commande de fraîcheur, qui tue déjà le spawn en cas
d'échec. Le coût est réel — un `link:` fautif casse le spawn — mais la barrière hôte attrape
d'abord le cas courant, si bien que ce qui survit jusqu'au boot est un vrai conflit dans la VM,
que seul l'utilisateur peut trancher.

## Tests

La phase de liens est **une chaîne que den génère**, donc l'essentiel est testable sans VM :

- goldens de `RenderMixin` avec et sans mounts — édités à la main, il n'y a pas de flag
  `-update` ;
- test de désucrage : `ssh.mode: mount` se résout identiquement à une entrée explicite ;
- mise à jour de `TestSpawnAddsNoWorkspaceOutsideMountMode`, dont l'invariant devient « les
  mounts ajoutent exactement `len(mounts)` workspaces » ;
- YAML strict : une clé inconnue **dans** une entrée de mount est une erreur de chargement
  (`KnownFields(true)` partout) ;
- ordre : un mount n'est jamais en position 0 ;
- suffixe `:ro` présent dans l'argv ;
- refus côté hôte déclenché avant tout effet de bord ;
- le test du message d'`ssh.dir` existant reste **vert sans modification** — c'est lui qui prouve
  que le désucrage n'a pas dilué le diagnostic ;
- `den doctor` signale un `mounts[].host` absent.

Aucun test ne lance de process ni n'ouvre de socket.

### Le trou, écrit plutôt que masqué

Ces tests prouvent que den **génère** le bon shell, jamais que ce shell **se comporte** bien.
La branche vide / non-vide est exactement là où ça compte.

Atténuation : garder le shell trivial, et le vérifier **une fois à la main dans une vraie
sandbox**, le résultat consigné ici. C'est le traitement hors-bande que la spec §14.0/§14.1
réserve déjà à toute affirmation sur ce qu'un vrai `sbx` a réellement répondu.

#### Mesuré le 2026-08-07 — `sbx` v0.37.1, den `v1.3.1-14-gf895ffa`

Sandbox jetable `smoke`, `DEN_HOME=/tmp/den-mount-smoke/home`, un seul mount
(`host: /tmp/den-mount-smoke/linkme`, `link: $HOME/.linkme`), détruite en fin de mesure.
Chaque ligne ci-dessous est une sortie observée, pas une reformulation.

**A11 est vraie pour cette version de `sbx`.** C'était l'hypothèse ouverte dont dépend tout le
design. `sbx` monte bien le workspace au **même chemin absolu** dans la VM :

```
lrwxrwxrwx+ 1 agent agent 27 /home/agent/.linkme -> /tmp/den-mount-smoke/linkme
marker
```

**Les trois issues du tableau vacuité, dans l'ordre.**

| cible avant boot | observé |
|---|---|
| répertoire **non vide** | `den mounts: FATAL /home/agent/.linkme is a non-empty directory (from mounts[0]) — den refuses to replace it`, puis `fail … exit=1`. Le fichier `occupied` a **survécu** : le refus n'a rien détruit. |
| répertoire **vide** | `den mounts: /home/agent/.linkme -> /tmp/den-mount-smoke/linkme`, `ok`. Le placeholder est pris, le lien reposé, `canary.txt` relu. |
| **déjà le bon lien** | `den mounts: … -> …`, `ok`. Idempotent — c'est la garde `-L` avant `-d` qui tient, un `-d` testé en premier aurait envoyé le lien correct dans la branche répertoire. |

**L'ordre des `commands.startup` est bien celui déclaré, et le dispatcher s'arrête à la
première défaillance.** Les deux entrées de den apparaissent comme deux scripts numérotés
distincts, phase de liens d'abord, fraîcheur ensuite :

```
> /etc/durable-startup.d/002-startup-den-smoke/000-cmd.sh
den mounts: /home/agent/.linkme -> /tmp/den-mount-smoke/linkme
ok /etc/durable-startup.d/002-startup-den-smoke/000-cmd.sh
> /etc/durable-startup.d/002-startup-den-smoke/001-cmd.sh
agent claude: up to date
ok /etc/durable-startup.d/002-startup-den-smoke/001-cmd.sh
```

Au boot refusé, `001-cmd.sh` **n'apparaît pas du tout** dans le journal : le dispatcher a coupé
sur `exit=1`. C'est la vérification directe de la raison pour laquelle la fraîcheur doit rester
**dernière** (spec §9.1) — et donc de la raison pour laquelle la phase de liens passe devant.

**Il n'existe pas de `sbx start`.** Le plan supposait `sbx stop && sbx start` pour rejouer le
dispatcher ; `sbx` v0.37.1 n'a pas cette commande. Mesuré à la place : **`sbx exec` sur une
sandbox arrêtée la redémarre et rejoue le dispatcher** (compteur `dispatcher run` du journal :
2 → 3). C'est ce chemin qui a servi aux trois issues ci-dessus, aucune n'a été rejouée à la
main. Fait sur `sbx` non encore consigné ailleurs.

**Un workspace en double est bénin.** Une entrée `mounts:` dont le `host` est déjà dans
`repos:` a été passée deux fois à `sbx create` : la création réussit, `sbx ls` ne rapporte le
chemin **qu'une fois**, et le répertoire reste accessible et inscriptible dans la VM. den n'a
donc pas besoin de dédupliquer.

**L'ordre des workspaces tient côté `sbx`**, dépôt d'abord et mount en dernier — c'est ce qui
protège le `-w` de l'attache :

```
smoke  shell  /tmp/den-mount-smoke/src, /tmp/den-mount-smoke/home/agents/claude, /tmp/den-mount-smoke/linkme
```

Rien n'a divergé du design. Le seul écart est côté plan, pas côté den : la commande `sbx start`
qu'il prescrivait n'existe pas.

## Ce que devient la spec §10

CLAUDE.md est explicite : une divergence entre la spec et le comportement réel est désormais
**un bug dans l'un des deux**, plus une phase assumée. Le plan doit donc porter l'édition de
§10, pas seulement le code.

Trois modifications :

1. **La ligne `mount` cesse de décrire un mécanisme et devient du sucre.** Aujourd'hui : « monte
   la **clé dédiée** dans la VM ». Demain : « équivaut à `mounts: [{host: <ssh.dir>, link:
   $HOME/.ssh}]` », avec le renvoi à la section `mounts:`.
2. **Le tableau « Ce que den fait, exactement, pour chacun des trois modes » devient faux** et
   doit être réécrit. Sa colonne « mixin » dit `inchangée` pour les trois modes ; en `mount`
   elle porte désormais la phase de liens. Sa colonne workspaces dit `+ ssh.dir, en dernier` ;
   c'est maintenant `+ les mounts, en dernier`.
3. **« Simple, headless-ready » devient vrai.** L'affirmation est aujourd'hui démentie par la
   mesure de la section « Déclencheur » ci-dessus. Ne pas la retirer — c'est l'intention, et
   c'est ce design qui la réalise. Y adjoindre la raison : le lien est ce qui rend le mode
   utilisable sans personne au clavier.

## Conséquences pour le cas déclencheur

Avec ce design, `dgdev ssh` dans `dg:op-inscription` demande :

1. `ssh.mode: mount` dans `~/.den/config.yaml` (global) ;
2. `digitaleo_id_rsa` et un `config` déposés dans `~/.ssh_sbx` — vérifié le 2026-08-07 :
   `ssh -o IdentitiesOnly=yes -i ~/.ssh/digitaleo_id_rsa … → AUTH_OK: ngaignoux@bwebdev22` ;
3. un respawn de la sandbox, les mounts étant fixés à la création.

Le `config` doit porter la réécriture de nom court, la VM n'ayant pas le domaine de recherche
`s1.digitaleo.net` (`/etc/resolv.conf` y est un bind-mount `ro`) :

```
Host *.s1.digitaleo.net
  HostName %h
  User ngaignoux
Host bweb* odep* oweb*
  HostName %h.s1.digitaleo.net
  User ngaignoux
```

Le premier bloc vient **en premier** et n'est pas décoratif : `deploy_server` vaut déjà
`odep.s1.digitaleo.net`, et le glob `odep*` seul le réécrirait en
`odep.s1.digitaleo.net.s1.digitaleo.net`. En ssh_config, le premier mot-clé rencontré gagne.

Vérifié le 2026-08-07 dans la sandbox vivante : avec ce `config` posé à la main en
`/home/agent/.ssh/config`, un vrai `dgdev ssh` atteint le serveur et n'échoue plus que sur
l'authentification — `Could not resolve hostname` a disparu.

Reste hors de ce design, et à traiter à part : `bwebdev22` n'est dans aucun `known_hosts`.
`dgdev ssh` est interactif (`ssh -t`) donc un TOFU y est répondable, mais `dgdev sh`, `logs` et
`deploy` ne le sont pas.

## Note de sécurité

`~/.digitaleo` de l'hôte contient `consul.secret` et `.consul.secret`. Le déclarer dans
`mounts:` les expose à **toute** sandbox, `mounts:` étant global. C'est un choix légitime, il
ne doit simplement pas être fait sans le savoir.

Même remarque pour `ssh.mode: mount` : la clé est alors au repos dans chaque VM, et l'agent
continue d'être forwardé par-dessus (`runner.go:42`, `cmd.Env` laissé nil) — le mode
**ajoute** de la portée, il n'en retire pas.
