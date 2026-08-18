# Design — `den update`, le chemin de mise à jour hors Homebrew

Date : 2026-08-18
Statut : validé en brainstorming, prêt pour le plan

## 1. Le problème

den se distribue par cinq chemins, et un seul dit à l'utilisateur comment se mettre à jour :

| Chemin d'installation | Mise à jour aujourd'hui | Documentée ? |
|---|---|---|
| `brew install --cask PillowPillow/tap/den` | `brew upgrade --cask den` | par brew, pas par den |
| `go install …/cmd/den@latest` | relancer la même commande | non |
| `install.sh` | **relancer le même curl** | non |
| archive de la page releases | retélécharger à la main | non |
| `task build` depuis un checkout | `git pull && task build` | non |

La ligne qui compte est la troisième : `install.sh` **est déjà un updater**, et personne ne le sait.
Il installe dans `$INSTALL_DIR/.den.new.$$` puis fait `mv -f` — un `rename(2)` atomique, écrit
exactement pour que « a reinstall while `den` is running » ne casse rien (ETXTBSY sous Linux, binaire
tronqué en cas d'interruption). Le README ne le dit nulle part.

Le second trou est un refus. Quand une source de team déclare `requires.den: ">=1.7.0"` et que le
binaire est plus vieux, `internal/source/manifest.go:546` répond :

```
den 1.6.0 is older than the 1.7.0 required by this source — upgrade den, den installs neither binary
```

Ce message ne nomme aucune commande, alors que toute erreur de den nomme son remède. C'est la
motivation concrète de cette spec : donner à « upgrade den » une commande qui existe.

## 2. Ce qui a été mesuré

Tout ci-dessous a été vérifié le **2026-08-18**, sur cette machine (darwin 25.5.0, arm64,
go1.26.1) ou dans les fichiers du dépôt. Rien n'est supposé.

**a) Le cask et `install.sh` livrent la MÊME archive.** `.goreleaser.yaml` donne au cask
`url.template: …/releases/download/{{ .Tag }}/{{ .ArtifactName }}`, et `archives.name_template`
produit `den_{{ .Version }}_{{ .Os }}_{{ .Arch }}` — exactement le nom que `install.sh` recompose
ligne 96. Conséquence directe, et elle ferme une option avant qu'elle soit proposée : **on ne peut
pas marquer la méthode d'installation à la compilation**, par un second symbole `-X` par exemple.
Il n'y a pas deux builds à marquer différemment, il y a un seul tarball. La détection ne peut être
que faite à l'exécution.

**b) Le binaire installé par brew vit dans le Caskroom**, atteint par un symlink :

```
$ ls -l /opt/homebrew/bin/den
lrwxr-xr-x  … /opt/homebrew/bin/den -> /opt/homebrew/Caskroom/den/1.8.1/den
```

Ce chemin-là est mesuré. Les autres marqueurs de la table du §4 (`/usr/local/Homebrew`,
`/home/linuxbrew/.linuxbrew`, `~/.linuxbrew`, un composant `Cellar`) **ne le sont pas** : ce sont des
emplacements par défaut connus, pas des observations de cette machine. Le §4 les traite comme des
heuristiques et ajoute `HOMEBREW_PREFIX` / `HOMEBREW_CELLAR` pour couvrir les préfixes qu'aucune
liste n'énumère.

**c) `os.Executable()` ne résout pas le symlink sur darwin.** Mesuré avec un binaire lancé à travers
un lien :

```
Executable:   …/scratchpad/exeprobe/linkdir/exeprobe
EvalSymlinks: …/scratchpad/exeprobe/exeprobe
```

Donc `filepath.EvalSymlinks` est obligatoire, et il l'est sur la plateforme où brew domine : sans
lui, un den lancé depuis `/opt/homebrew/bin/den` serait classé « archive » et écraserait un binaire
que brew possède. Sous Linux `os.Executable()` lit `/proc/self/exe`, déjà résolu ; le même appel est
donc correct sur les deux systèmes.

**d) La garde d'hermeticité ne couvre que les imports directs de `internal/cli`.**
`internal/ports/hermeticity_test.go` fait de l'analyse syntaxique (`go/build.ImportDir` +
`go/parser`) sur les fichiers non-test **du seul paquet `internal/cli`**, et compare les chemins
d'import à `net`, `hash/fnv`, `os/exec` en égalité exacte. Un `net/http` situé dans
`internal/selfupdate` est donc hors de portée de la garde, et l'intention de la garde — « aucun
accès système brut dans la couche de câblage/affichage » — reste servie tant que `internal/cli` ne
fait qu'importer le nouveau paquet.

**e) `golang.org/x/mod/semver` est déjà une dépendance**, utilisée par `internal/source` pour les
planchers de version. La comparaison « courante vs dernière » n'ajoute donc aucune dépendance.

**f) `install.sh` évite `api.github.com` volontairement** : il lit le tag dans l'URL finale de la
redirection `/releases/latest`, pour ne pas dépendre des 60 requêtes/heure non authentifiées ni de
`jq`. `den update` hérite de cette décision plutôt que de la redécouvrir.

**g) Sur cette machine, `GOPATH=/Users/polochon/go` et `GOBIN` est vide** — le cas par défaut, celui
que la détection doit couvrir sans variable d'environnement.

**h) Une version issue de `task build` est un semver VALIDE, et il compare plus bas qu'une release.**
Mesuré avec `golang.org/x/mod/semver` :

```
v1.5.0-17-g0ec48d8-dirty     IsValid=true   Compare(v, "v1.8.1") = -1
v1.8.1-dirty                 IsValid=true   Compare(v, "v1.8.1") = -1
dev                          IsValid=false  Compare(v, "v1.8.1") = -1
```

C'est la mesure qui décide le §4 : le suffixe de `git describe` se lit comme un pré-release, donc un
binaire construit localement passerait pour « en retard » et serait écrasé par une release. Une
première rédaction gardait ce cas sous un test `version == "dev"` — ce test ne l'attrape pas, puisque
`resolveVersion` rend le tampon ldflags dès que `task build` a tourné (le `./den` de ce dépôt répond
`v1.5.0-17-g0ec48d8-dirty`).

`internal/source.releaseVersion` ne sert pas de garde non plus, malgré son nom : il **tronque** le
pré-release et rendrait `v1.5.0` avec `ok=true`. Le prédicat correct est plus strict, et il est
écrit au §4.

## 3. Le contrat

Une seule forme, aucun drapeau :

```
den update
```

Elle met le binaire courant à la dernière release, ou refuse en nommant la commande qui, elle, le
fera. Le choix du périmètre minimal est délibéré : `--check` et `--version` sont hors périmètre
(§10), et le rollback reste `DEN_VERSION=vX.Y.Z` sur `install.sh`, déjà documenté au README.

## 4. Détection, et matrice de refus

Le chemin de décision est `filepath.EvalSymlinks(os.Executable())`, jamais le `argv[0]`.

L'ordre d'évaluation est celui de la table, et la première ligne qui matche décide.

| Condition | Verdict | Message |
|---|---|---|
| version courante non canonique (§2h) | refus | nomme la version observée ; remède `task build` ou une installation de release |
| sous `$HOMEBREW_PREFIX/Cellar` ou `$HOMEBREW_PREFIX/Caskroom`, ou sous `$HOMEBREW_CELLAR`, s'ils sont définis | refus | `den was installed by Homebrew (<path>); run 'brew upgrade --cask den'` |
| un composant du chemin vaut `Caskroom` ou `Cellar`, ou le chemin est sous un préfixe brew par défaut (`/opt/homebrew`, `/usr/local/Homebrew`, `/home/linuxbrew/.linuxbrew`, `~/.linuxbrew`) | refus | idem |
| sous **l'un quelconque** de `$GOBIN`, du `GOBIN`/`GOPATH` écrit par `go env -w`, de chaque entrée de `$GOPATH`/bin, et de `~/go/bin` | refus | `run 'go install github.com/PillowPillow/den/cmd/den@latest'` |
| autre, dossier écrivable | **mise à jour** | — |
| autre, dossier non écrivable | refus | nomme le dossier, et `install.sh` avec `DEN_INSTALL_DIR` |

**Le prédicat de version est strict, et c'est la ligne la plus importante de la table.** Une version
n'autorise la mise à jour que si elle est exactement canonique : `semver.IsValid(v)`, et
`semver.Prerelease(v)` et `semver.Build(v)` tous deux vides. `dev`, `v1.5.0-17-g0ec48d8-dirty` et
`v1.8.1-dirty` sont donc tous refusés par la même règle, alors qu'un test sur la chaîne `dev` n'en
attrapait qu'un (§2h). Les binaires livrés — cask, `install.sh`, archive, `go install …@vX` — portent
tous un tag propre, donc aucun cas légitime ne tombe dans ce refus. Un futur tag de pré-release
(`v1.9.0-rc1`) y tomberait : c'est assumé au §10.

**La détection brew échoue de façon asymétrique, et le §2b dit lesquels de ses marqueurs sont
mesurés.** Un faux négatif — un préfixe brew que la liste ignore — fait écraser un binaire que brew
possède, c'est-à-dire exactement la corruption que cette table existe pour empêcher. Un faux positif
— un dossier personnel nommé `Cellar` — produit un refus qui nomme une commande inutile, ce qui est
gênant et rien de plus. C'est pourquoi `HOMEBREW_CELLAR` passe **avant** les
chemins par défaut : brew l'exporte dans tout shell passé par `brew shellenv`, et il couvre un cellar
déplacé qu'aucune liste ne peut énumérer. `HOMEBREW_PREFIX`, lui, n'est consulté que **borné à ses
deux sous-dossiers d'installation** `Cellar/` et `Caskroom/` : testé en entier il classait
`MethodHomebrew` une installation `install.sh` posée en `/usr/local/bin` sur un Mac Intel, où
`brew shellenv` exporte `HOMEBREW_PREFIX=/usr/local`. Le bornage ne perd aucune couverture brew — un
den brew résout sous `Cellar/` ou `Caskroom/`, que le scan de composants attrape sous n'importe quel
préfixe — et il rend son chemin à un utilisateur que le refus envoyait dans un mur (§11).

**La liste des dossiers `go` est une union, pas la précédence du toolchain.** La précédence répond
« où un `go install` poserait den maintenant » ; la classification doit répondre « un `go install`
a-t-il pu poser CE den ici », et les deux divergent dès que l'environnement a bougé depuis
l'installation : un `go env -w GOBIN=~/bin` d'aujourd'hui ne déplace pas le den déjà posé dans
`~/go/bin`. L'union ne fait que refuser davantage, la direction sûre. Elle inclut le fichier de
configuration du toolchain, que `os.Getenv` ne voit pas. Mesuré le 2026-08-18 dans un `GOENV` isolé,
donc sans rien déplacer sur la machine : `go env -w GOBIN=/tmp/somewhere-else` y écrit la ligne
`GOBIN=/tmp/somewhere-else`, `go env GOBIN` répond ensuite `/tmp/somewhere-else`, et `$GOBIN` dans
l'environnement du processus reste **vide** tout du long. Le fichier peut ne pas exister — c'est le
cas sur cette machine — et alors `go env GOPATH` répond le `~/go` par défaut, que l'union couvre déjà
par `Env.Home`. C'est aussi pourquoi la comparaison porte
sur des **composants de chemin** (`Caskroom`, `Cellar`) et non sur des sous-chaînes : `~/dev/MyCellar`
n'est pas une installation brew.

Trois points de plus que cette table décide et qui ne sont pas cosmétiques :

**Le refus brew est une protection, pas une politesse.** Écraser `/opt/homebrew/Caskroom/den/1.8.1/den`
laisse brew persuadé de gérer une version qu'il ne gère plus ; le prochain `brew upgrade` se bat
contre den. Refuser est la seule réponse qui ne casse pas l'état d'un autre gestionnaire, et c'est
la même doctrine que `den installs neither binary`.

**Le cas du build local est réel.** `task build` produit `v1.8.1-3-gabc1234-dirty`. Écraser ce
binaire par une release effacerait un build local sans le dire. den refuse plutôt que de normaliser
(spec §2).

**Un dossier non écrivable n'appelle jamais `sudo`.** den ne réhausse jamais ses privilèges : il
nomme le dossier et laisse l'utilisateur choisir. `/usr/local/bin` sans droits d'écriture est
exactement ce cas.

Le chemin « archive » couvre indistinctement `install.sh`, une archive dépliée à la main et un
`DEN_INSTALL_DIR` personnalisé — ce sont les mêmes octets venus du même tarball (§2a), donc la même
mise à jour.

## 5. La séquence

L'ordre suit celui du spawn (spec §6) : tout ce qui est refusable l'est **avant le premier effet de
bord**, pour qu'un refus ne laisse jamais de résidu.

1. **Classer** l'installation (§4), et refuser sur la version non canonique. Les deux refus qui ne
   demandent rien au réseau sont pris ici.
2. **Résoudre le tag** : requête `HEAD` sur `https://github.com/PillowPillow/den/releases/latest`,
   le tag est le dernier segment de l'URL finale (§2f).
3. **Comparer** avec `semver.Compare` (§2e), la version courante ayant déjà passé le prédicat
   strict du §4 — la comparaison ne voit donc que des versions canoniques des deux côtés. Égalité :
   `den v1.8.1 is already the latest release`, sortie 0, aucun téléchargement. Version courante
   **strictement supérieure** — une release retirée, ou un binaire construit depuis un tag que
   `/releases/latest` ne sert pas encore : `den v1.9.0 is ahead of the latest release v1.8.1 —
   nothing to do`, sortie 0 également. Les deux cas ne font rien ; une seule phrase pour les deux
   disait au second qu'il était « à jour », ce qui cache la seule chose intéressante.
3bis. **Sonder l'écriture** : créer et supprimer `<dossier cible>/.den.new.<pid>`, avant tout
   téléchargement mais **après** l'étape 3. La sonde répond à deux questions d'un coup — le dossier
   est-il écrivable, et le fichier d'attente vivra-t-il sur le même système de fichiers que la cible
   (§6 de cette liste). Sans elle, « dossier non écrivable » se découvre après plusieurs mégaoctets.
   Sous l'étape 3 et non au-dessus : un den déjà à jour dans `/usr/local/bin` ou sur un montage en
   lecture seule sortait en erreur non nulle alors qu'il n'avait rien à écrire, ce qui casse tout
   script d'installation qui appelle `den update` de façon idempotente. Refuser d'écrire n'est une
   nouvelle que quand den a quelque chose à écrire.
4. **Télécharger** `den_<version>_<os>_<arch>.tar.gz` et `checksums.txt`, le nom d'archive étant
   recomposé depuis `runtime.GOOS`/`runtime.GOARCH` et le tag sans le `v` initial.
5. **Vérifier** le sha256 (`crypto/sha256`) contre la ligne de `checksums.txt`. Une entrée absente
   est un changement de layout de release, pas une erreur utilisateur : le message le dit et
   renvoie aux issues. La vérification prouve l'intégrité, jamais l'authenticité — `checksums.txt`
   voyage sur le même canal TLS non signé que l'archive — et le message ne revendique que cela,
   comme celui d'`install.sh`.
6. **Extraire** l'entrée `den` (`archive/tar` + `compress/gzip`) vers `<dossier cible>/.den.new.<pid>`,
   mode 0755. Le fichier d'attente est dans le **dossier cible**, pas dans `os.TempDir()` : un
   `rename(2)` ne traverse pas un système de fichiers, et `/tmp` en est souvent un autre. Il est
   créé en `O_EXCL`, jamais en `O_CREATE|O_TRUNC` : le nom est prévisible et l'étape 3bis vient de
   le libérer, donc quiconque écrit dans ce dossier peut y poser un lien symbolique dans la fenêtre
   — que `os.WriteFile` aurait suivi, y compris pour le `chmod 0755`. `O_EXCL` transforme la course
   en refus nommant le fichier à supprimer. Le `chmod` passe par le **descripteur**, pas par le
   chemin, pour ne pas rouvrir cette même fenêtre.
7. **Échanger** par `os.Rename` par-dessus le binaire courant, après un `Sync()` du fichier
   d'attente et suivi d'un `Sync()` du dossier. C'est ce qui rend l'opération sûre pendant que den
   tourne : le lecteur obtient l'ancien ou le nouveau, jamais la moitié d'un des deux. Les deux
   `Sync` étendent cette garantie à la coupure de courant, où le `rename` peut être journalisé avant
   les blocs de données — sinon le seul den du `PATH` est un fichier vide. Celui du dossier est en
   meilleur effort : `fsync` sur un dossier est légal sur darwin et linux, mais un binaire durable
   que den n'a pas pu confirmer n'est pas une raison d'échouer une mise à jour réussie.
8. **Nettoyer** : un `defer` supprime `.den.new.<pid>` sur tout chemin d'échec, pour la raison qui a
   fait écrire le `trap` d'`install.sh` — sinon un signal reçu dans cette fenêtre laisse un résidu
   pour toujours dans le `bin` de l'utilisateur.

Sortie du succès : `den v1.8.0 → v1.8.1 (<chemin>)`.

**Pas de run de preuve.** `install.sh` termine par `"$INSTALL_DIR/den" version` parce qu'il peut
installer une archive d'une autre architecture ou sur un montage `noexec`. `den update` connaît son
propre `GOOS`/`GOARCH` et écrit à côté d'un binaire qui vient de s'exécuter depuis ce dossier ; le
run coûterait un `os/exec` pour une question déjà répondue.

## 6. Architecture

Nouveau paquet `internal/selfupdate`, coupé en deux moitiés dont une seule touche la machine.

**La moitié pure**, entièrement testable sans I/O :

- `Classify(execPath string, env Env) Method` — la table du §4.
- `Plan(current, latest string) Action` — à jour / à mettre à jour / version courante incomparable.
- Les constructeurs de refus, dont les textes sont figés par des goldens.

**La moitié impure**, derrière une interface :

```go
type Fetcher interface {
    ResolveLatest(ctx context.Context) (tag string, err error)
    Get(ctx context.Context, url string) ([]byte, error)
}
```

L'implémentation réelle est un `net/http` du même paquet ; les tests injectent un double, comme
`sbx.Fake` le fait déjà pour `policy`, `cli` et `agent`.

**Le câblage** suit exactement le patron des autres accès système : `cli.Deps` gagne un champ
`Updater selfupdate.Fetcher`, câblé réellement dans le wiring racine et laissé nil par les tests de
câblage — même raison que `Scanner`, `Open` et `SSHAgent`, dont les implémentations réelles
respectivement lient des sockets, lancent un navigateur et forkent `ssh-add`.

`internal/cli` n'importe alors que `internal/selfupdate`, et la garde d'hermeticité reste verte pour
la raison mesurée au §2d.

## 7. Les erreurs, et leur remède

Chaque échec nomme ce qui a manqué et quoi faire, jamais seulement ce qui a raté :

| Situation | Ce que le message doit contenir |
|---|---|
| pas de réseau / redirection illisible | l'hôte visé, et le repli : archives de la page releases |
| aucun tag lisible dans l'URL finale | l'URL obtenue, et le repli manuel |
| archive absente pour cet os/arch | le nom d'archive et le tag, formulé comme « cette release ne livre pas ce couple » |
| `checksums.txt` sans entrée | le layout de release a changé → ouvrir une issue |
| sha256 différent | téléchargement corrompu ou incomplet ; relancer, signaler si ça persiste |
| archive sans entrée `den` | même famille que l'entrée manquante : layout changé, issue |
| dossier non écrivable | le dossier, et `DEN_INSTALL_DIR` d'`install.sh` comme destination alternative |
| disque plein, montage en lecture seule | le chemin, sans affirmer « permission refusée » (l'erreur système est reportée telle quelle) |

## 8. Tests

Aucun test n'ouvre de socket ni ne lance de processus, conformément aux conventions du dépôt.

- Table de `Classify` : Caskroom macOS (chemin mesuré au §2b), Linuxbrew, `/usr/local/Cellar`,
  un `HOMEBREW_PREFIX` personnalisé que la liste par défaut ne contient pas, `HOMEBREW_CELLAR` seul,
  `~/dev/MyCellar` (le faux positif que le test par composant doit éviter), `$GOBIN` défini,
  `$GOPATH/bin`, `~/go/bin` par défaut, `~/.local/bin`, et un `DEN_INSTALL_DIR` exotique.
- Table du prédicat de version : `v1.8.1` accepté ; `dev`, `v1.5.0-17-g0ec48d8-dirty`, `v1.8.1-dirty`
  et `v1.9.0-rc1` refusés. Les trois chaînes du §2h y figurent telles quelles, pour que la mesure et
  le test disent la même chose.
- Table de `Plan` : plus récente, égale, plus ancienne que la locale.
- Extraction : un `.tar.gz` construit en mémoire dans le test, avec les variantes « entrée `den`
  absente » et « archive tronquée ».
- Vérification : entrée absente de `checksums.txt`, et sha256 différent.
- Échange : `Rename` réel dans un `t.TempDir()`, plus le nettoyage du `.den.new.<pid>` après échec
  injecté.
- Goldens des refus, comparés à la main comme tous les goldens du dépôt (il n'y a pas de `-update`).

**Fumée CI**, calquée sur le job `install-script` de `ci.yml`, qui ouvre déjà le réseau au motif que
« the network path is the point » : installer une release antérieure via
`DEN_VERSION=v1.0.0 sh install.sh`, lancer `den update`, vérifier que `den version` a atteint le tag
latest résolu dynamiquement.

Le tag de départ est **épinglé à `v1.0.0` et n'a pas à être bumpé** : c'est la plus ancienne release
publiée, elle reste par construction antérieure à toute release future, et le job n'a donc pas de
rituel attaché à chaque sortie de version. Il ne redevient une décision que si les archives de
`v1.0.0` disparaissent ou si leur layout cesse d'être lisible par `install.sh` — les deux cas où ce
job doit justement échouer bruyamment. C'est ce job qui casse si `archives.name_template` change — le
même contrat que le job existant surveille depuis l'autre côté.

## 9. Documentation

La section Installation du README gagne, pour chaque chemin, sa ligne de mise à jour : `brew upgrade
--cask den`, `go install …@latest`, `den update`, et le fait jusqu'ici jamais écrit que **relancer
`install.sh` met à jour**. L'aide de la commande reprend les mêmes phrases, et le tableau des
commandes disponibles gagne sa ligne.

## 10. Hors périmètre

Nommé explicitement, pour qu'aucune de ces absences ne se lise comme un oubli :

- **`--check` et `--version`** : la surface reste `den update` seul. Le pin et le rollback restent
  `DEN_VERSION=` sur `install.sh`.
- **Notice spontanée de nouvelle version** (dans `den doctor` ou en fin de commande) : ce serait un
  appel réseau non demandé dans des commandes qui n'en font pas.
- **Signature des artefacts** (cosign, minisign) : `den update` s'aligne sur `install.sh` — intégrité
  vérifiée, authenticité non. La changer est une décision de pipeline de release, pas de commande.
- **Élévation de privilèges** : jamais de `sudo`, jamais de réécriture d'un binaire possédé par un
  autre gestionnaire, même sous drapeau.
- **Tags de pré-release** : le prédicat de version du §4 refuse `v1.9.0-rc1`, parce qu'aucune règle
  ne distingue ce suffixe de celui que `git describe` produit pour un build local (§2h). den n'a
  jamais publié de tag de pré-release ; le jour où il en publie un, la distinction est une tranche à
  écrire, pas une ligne à ajouter à la hâte.
- **Windows** : les releases ne livrent que darwin et linux ; la commande refuse là où elle ne peut
  pas livrer, comme `install.sh`.
- **Modification du texte du plancher `requires.den`** : ce message couvre aussi `sbx`, que den
  n'installe pas. Le rapprocher de `den update` demanderait de distinguer les deux outils dans un
  message aujourd'hui générique — une tranche à part, si le besoin se confirme.

## 11. Écarts assumés à l'implémentation

Cette section est écrite **après** l'implémentation (2026-08-18). Le repo tient qu'une divergence
spec/code est un bug dans l'un des deux, jamais une phase ; celles-ci sont donc actées ici plutôt
que laissées silencieuses.

- **§4, l'ordre des lignes de la matrice.** La table dit « la première ligne qui matche décide » et
  place « version courante non canonique » en premier. `selfupdate.Run` classe la **méthode
  d'installation avant** la version. Les deux issues sont un refus sans le moindre effet de bord :
  seul change le message que voit un build de dev installé par brew — et lui nommer
  `brew upgrade --cask den` est plus actionnable que lui dire que sa version n'est pas une release.
  Le code garde son ordre ; la table décrit l'ensemble des refus, pas leur précédence.
- **~~§4, `HOMEBREW_PREFIX=/usr/local` sur un Mac Intel.~~ Corrigé le 2026-08-18.** Une
  installation `install.sh` posée en `DEN_INSTALL_DIR=/usr/local/bin` était classée
  `MethodHomebrew` et renvoyée vers `brew upgrade --cask den`, qui répond « not installed ». L'écart
  a d'abord été acté au titre de l'asymétrie du §4 — un faux positif ne coûte qu'un refus inutile.
  Une revue a montré que le coût n'était pas nul : sans `--force`, cet utilisateur n'a **aucun**
  chemin de mise à jour, et l'asymétrie ne justifie un faux positif que s'il achète une couverture.
  Celui-ci n'en achetait aucune : `HOMEBREW_PREFIX` en entier est redondant avec le scan de
  composants `Cellar`/`Caskroom`. Le test est désormais borné à `$HOMEBREW_PREFIX/{Cellar,Caskroom}`
  (§4), ce qui supprime le faux positif sans perdre un seul vrai positif.
- **§4, un `task build` fait sur un tag exact et un arbre propre.** `git describe --tags --always
  --dirty` répond alors `v1.5.0` tout court, que `IsUpdatableVersion` accepte, et le chemin d'un
  checkout ordinaire est classé `MethodArchive` : `den update` écrase ce binaire local, alors que
  l'aide de la commande dit refuser un build de checkout. Rien ne distingue ce binaire d'une
  release — goreleaser et le Taskfile visent le même symbole `cli.Version`, et
  `debug.ReadBuildInfo().Main.Version` répond `(devel)` pour les deux. Le distinguer demanderait un
  second ldflag posé par le seul `.goreleaser.yaml`, ce qui ferait échouer le job CI
  `update-command` (il construit son binaire en `go build` dans le job, donc sans ce marqueur)
  jusqu'à la première release qui le porte. Le coût du bug est un `./den` **gitignoré**, que
  `task build` régénère en deux secondes ; le coût du mécanisme est une CI rouge et un
  chicken-and-egg de release. Rien n'est corrigé. L'aide de la commande ne promet plus l'inverse :
  elle dit refuser « a build from a checkout, which `git describe` stamps with a commit count or
  `-dirty` », ce qui est exactement ce que le prédicat mesure.
