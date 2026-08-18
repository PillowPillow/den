# Design — les quatre invites de den passent à `huh`

Date : 2026-08-18
Statut : validé en brainstorming, prêt pour le plan
Renverse : `2026-08-11-nest-instances-design.md`, décision 4 (« La checklist reste `bufio`, sans
TUI »). Cette spec est l'amendement de cette décision, pas son contournement.

## 1. Le problème

La checklist `-i` a été utilisée pour de vrai. Verdict de l'utilisateur, 2026-08-18 :

> j'ai testé le `-i` sur le `up`, c'est inutilisable. On ne propose pas d'ui pour le toggle qu'on
> retrouve dans tous les cli. Ça se passe avec des prompts avec des numéros.

C'est la mesure que la décision 4 attendait. Elle s'était laissé cette porte ouverte, mot pour mot :

> Si trente entrées se révèlent pénibles à l'usage, un filtre par préfixe tapé se code dans le même
> `bufio` ; hors scope ici, à chiffrer sur mesure réelle.

La mesure réelle est tombée, et elle ne dit pas « il manque un filtre ». Elle dit que l'interaction
elle-même — lire une liste numérotée, taper des numéros séparés par des espaces, relire la liste
redessinée en entier à chaque tour — n'est pas ce qu'un utilisateur de CLI attend en 2026. Le filtre
aurait raccourci la liste sans changer le geste.

**`cobra` n'y est pour rien**, et la question posée mérite sa réponse écrite : cobra route des
commandes et parse des drapeaux. Il n'a jamais eu de primitive d'affichage, de curseur ni de lecture
de touche, et n'en aura pas — ce n'est pas sa couche. Une invite riche en Go vient d'ailleurs :
`bubbletea` (+ `huh` pour les formulaires prêts à l'emploi), ou du mode brut lu à la main sur
`golang.org/x/term`.

## 2. Ce que disait l'interdit, et pourquoi il ne tient plus

`internal/spawn/interactive.go` porte la règle, en fin de godoc de `promptOptionalRepos` :

> `bufio.Scanner`, no TUI library. `cobra` and `yaml.v3` are den's only dependencies and that is a
> claimed property (a static binary, HANDOFF §8).

Trois faits, tous vérifiés le 2026-08-18 sur l'arbre courant, la vident :

**a) HANDOFF §8 ne dit pas ça.** Le §8 s'intitule « v1 est taguée — ce que ça a fermé, ce que ça n'a
pas fermé ». Il parle du tag `v1.0.0`, de `#10`, de `#21/#27/#30`, de `#31` et de deux surfaces `sbx`
non mesurées. Aucune phrase sur les dépendances, aucune sur le binaire statique. La citation est
fausse : la règle s'appuie sur une section qui ne la porte pas.

**b) La liste à deux dépendances est déjà relâchée, et sciemment.** `go.mod` requiert aujourd'hui
`cobra`, `yaml.v3`, `golang.org/x/mod` (comparaison semver) et `golang.org/x/term` (lecture d'un
secret sans écho, `internal/cli/root.go:105`). `internal/ports/hermeticity_test.go:26` écrit la
liste à cinq, pas à deux, et nomme la raison de chaque ajout.

**c) `isterminal_darwin.go` a payé `unsafe` et un ioctl brut pour éviter une dépendance déjà
présente.** Son godoc dit, en justifiant trois premières fois dans ce dépôt (fichier
plateforme-spécifique, `unsafe`, syscall brut) :

> this module allows stdlib + cobra + yaml.v3 only, which rules out `golang.org/x/term` and
> `golang.org/x/sys`

`golang.org/x/term v0.45.0` est un `require` direct de `go.mod`, importé par `internal/cli/root.go`.
La phrase était déjà fausse le jour où elle a été écrite. C'est le fait le plus dur de cette spec :
la contrainte invoquée pour interdire la TUI avait déjà été levée **dans cette même zone de code**,
pour rendre `-i` correct.

L'interdit n'est donc pas renversé par confort. Il est renversé parce que son fondement écrit
n'existe pas.

## 3. Ce qui a été mesuré

Rien ci-dessous n'est supposé. Toutes les mesures datent du **2026-08-18**, sur `huh v1.0.0`
(`go 1.23.0`), darwin/arm64.

**a) Le graphe de dépendances passe de 5 à 27 modules.** Un module vide qui ne requiert que
`github.com/charmbracelet/huh@v1.0.0` résout à 27 modules :

```
huh · bubbletea · bubbles · lipgloss · colorprofile · catppuccin/go
charmbracelet/x/{ansi,cellbuf,exp/strings,term} · atotto/clipboard
aymanbagabas/go-osc52/v2 · dustin/go-humanize · erikgeiser/coninput
lucasb-eyer/go-colorful · mattn/{go-isatty,go-localereader,go-runewidth}
mitchellh/hashstructure/v2 · muesli/{ansi,cancelreader,termenv}
rivo/uniseg · xo/terminfo · golang.org/x/{sync,sys,text}
```

`golang.org/x/sys` est déjà indirect chez den : **26 modules nouveaux**.

**b) Le binaire grossit de moins de 4 Mo — borne haute, pas mesure directe.** `task build` sur
`v1.8.1-1-g153a1a3` rend un `den` de **7 291 330 octets**. Un `main` qui ne fait qu'un
`huh.NewForm(...MultiSelect...)` et l'exécute rend **5 740 162 octets**. Ces deux chiffres sont
mesurés ; leur somme ne l'est pas, et **surestime** : les deux binaires portent chacun le runtime Go.
La borne honnête est « den + huh < 11 Mo », le surcoût réel étant inférieur d'environ 1,5 à 2 Mo. La
mesure directe se fait à la tranche 3, quand `huhui` existe. Le binaire reste statique et sans cgo —
aucun des 26 modules n'en introduit.

**c) `huh` est en ligne par défaut, pas en plein écran.** La trace d'échappement de la sonde contient
`^[[?25l` (curseur caché), `^[[?2004h` (collage encadré), `^[[?1004h` (rapport de focus) et **aucun
`^[[?1049h`** (écran alterné). Le rendu choisi — le plan de `den converge` reste à l'écran au-dessus
de la confirmation — s'obtient sans rien demander.

**d) `huh` échoue OUVERT sans terminal, PUIS ne rend pas la main. C'est la mesure qui gouverne le
§5.** Sonde lancée avec `/dev/null` sur stdin et `/dev/null` sur stdout, marqueurs sur stderr :

```
$ timeout 5 ./probe3 < /dev/null > /dev/null
MARK before-run
MARK after-run ERR=<nil> LEN=0 VAL=[]
MARK before-return
rc=124        # 124 = tué par timeout, 5 s écoulées
```

Deux pannes, pas une :

1. **Aucun refus.** `huh` déverse ses séquences ANSI brutes dans la sortie, `Run()` rend `nil`, et la
   sélection par défaut — celle que personne n'a faite — devient la réponse.
2. **Le processus ne se termine pas.** Le dernier marqueur de `main` s'imprime, et le processus est
   toujours vivant 5 s plus tard. Une variante avec `os.Exit(0)` explicite après le marqueur pend
   identiquement. Un témoin de même forme sans `huh` sort en `rc=0` instantanément : la pendaison
   vient bien de la bibliothèque. Sa cause n'a pas été cherchée — elle ne change rien au §5, qui
   n'appelle jamais `huh` sans terminal.

**Correction d'une mesure antérieure de cette même sonde :** une première lecture annonçait `rc=0`.
Ce `0` était le code de `head` en fin de tube, pas celui de la sonde. Le code de la sonde n'avait
jamais été mesuré. Il l'est ci-dessus, sans tube.

C'est exactement la forme de panne que l'issue #66 a été ouverte pour tuer, et que le godoc de
`isterminal_darwin.go` décrit : « a data-loss path with a clean exit code ». `< /dev/null` est le
stdin canonique d'une CI et d'un cron. Ici la conséquence est double, et la seconde est la pire :
une microVM créée avec un jeu de dépôts que personne n'a choisi, puis un job qui ne rend jamais la
main. Le refus que den rend aujourd'hui dit mot pour mot ce que cette pendaison ferait — « reading
anyway would block a pipe or a CI job forever ».

**f) Une `MultiSelect` sans rien de présélectionné se soumet vide.** Même sonde, options construites
sans `.Selected(true)` : `ERR=<nil> LEN=0 VAL=[]`. Il n'y a pas de plancher à une sélection ;
`(*MultiSelect).Limit(n)` existe et den ne l'appelle pas. C'est ce qui rend l'invariant 2 tenable :
un nest `select: prompt`, dont le contrat est de n'avoir aucune sélection par défaut, peut être
confirmé vide.

Le chemin `bufio` d'aujourd'hui échoue FERMÉ tout seul : sur EOF avant confirmation, il refuse en
nommant l'équivalent non interactif. Cette propriété est native aujourd'hui ; après `huh`, elle
devient quelque chose que den doit tenir. Le §5 est cette tenue.

**e) L'API porte ce qu'il faut pour l'injection et pour l'annulation.** `(*huh.Form)` expose
`WithInput(io.Reader)`, `WithOutput(io.Writer)`, `WithAccessible(bool)`, `RunWithContext(ctx)`.
`huh.ErrUserAborted` est la valeur rendue sur `ctrl+c`. `WithAccessible` est **opt-in** (faux par
défaut) : den ne l'active jamais — c'est précisément le mode dégradé qui remplacerait un refus par
une invite en clair.

## 4. La surface concernée : quatre invites, pas une

den n'a que quatre endroits qui lisent un humain. Ils passent tous les quatre, et c'est le périmètre
validé.

| # | Site | Forme actuelle | Après |
|---|---|---|---|
| 1 | `spawn.promptOptionalRepos` | checklist numérotée, `bufio.Scanner` | `MultiSelect` |
| 2 | `cli.askRepositoryRoots` (`answers.go:272`) | ligne libre, `bufio.Reader` | `Line` (voir ci-dessous) |
| 3 | `Deps.ReadSecret` (`root.go:105`) | `term.ReadPassword` | `Secret` |
| 4 | `cli.confirm` (`answers.go:291`) | `apply this plan? [y/N]`, `bufio.Reader` | `Confirm` |

**Le site 2 lit une LISTE sur une ligne.** `askRepositoryRoots` rend `[]string` : plusieurs
répertoires, séparés, expansés (`~`) puis validés, sur une seule saisie. `huh.Input` rend une
`string`. La découpe et l'expansion restent donc **chez l'appelant**, exactement où elles sont
aujourd'hui : `Prompter.Line` rend la ligne brute, et `askRepositoryRoots` garde son traitement mot
pour mot. Écrit ici pour que le plan ne le redécouvre pas et ne soit pas tenté de pousser la découpe
dans `internal/prompt` — un `Prompter` qui connaîtrait les chemins ne serait plus une couche
d'affichage.

Le précédent est déjà là et il porte le design entier : `ReadSecret` est **déjà** une fonction
injectée dans `cli.Deps`, et son godoc dit pourquoi — « the real implementation reads the terminal
directly (golang.org/x/term), which is exactly why it cannot be hard-wired: a test that inherited it
would try to put the suite's stdin into raw mode ». Cette spec généralise cette forme aux trois
autres.

## 5. Architecture

### 5.1 Le joint

Nouveau paquet feuille `internal/prompt` — l'interface et ses quatre types de requête, **aucun import
tiers** :

```go
package prompt

type Prompter interface {
	MultiSelect(MultiSelectRequest) ([]string, error)
	Confirm(ConfirmRequest) (bool, error)
	Line(LineRequest) (string, error)
	Secret(SecretRequest) (string, error)
}
```

Nouveau paquet `internal/prompt/huhui` — le **seul** paquet de den autorisé à importer
`github.com/charmbracelet/...`. Il implémente `Prompter`.

`internal/spawn` et `internal/converge` n'importent que `internal/prompt`. `cli.Deps` gagne un champ
`Prompt prompt.Prompter` ; `spawn.Deps` aussi. `Deps.ReadSecret` disparaît dans `Prompt.Secret`.
`Deps.IsTTY` **reste** : c'est la porte du §5.2.

**Le câblage se fait dans `SystemDeps()`** (`internal/cli/root.go`), avec `sbx.NewExec`,
`worktree.NewGit`, `ports.OpenURL` et les autres — c'est déjà l'endroit où den nomme ses accès
machine réels, et `cmd/den` n'y touche pas : `main` appelle `cli.Execute()` et ne construit aucun
`Deps`. Câbler ailleurs demanderait de faire remonter un paramètre à travers `Execute` et
`NewRootCmd` pour rien.

`internal/cli` importe donc `internal/prompt/huhui`. La garde du §6 n'en souffre pas et ce n'est pas
un aménagement : elle porte sur les imports **directs** de `github.com/charmbracelet/*`, et un
paquet qui importe l'adaptateur n'importe pas la bibliothèque. C'est exactement la propriété
recherchée — un seul fichier de den connaît le nom de `huh`.

### 5.2 Refermer l'échec ouvert, chez den

La mesure (d) est le centre du design. Deux couches, et les deux sont nécessaires :

**Au-dessus — inchangé.** Chaque appelant garde sa porte `d.IsTTY` et son refus déjà rédigé, avec son
texte actuel : `interactiveWithout` nomme `--only`/`--without` selon le mode du nest, `confirm` nomme
`--yes`, `collectInitialAnswers` nomme ce qu'elle nomme. Aucun de ces messages ne bouge, aucun de
leurs tests ne bouge. Un refus qui nomme le remède est la doctrine du dépôt (spec §2) et une
bibliothèque n'a pas à la réécrire.

**À l'intérieur — nouveau.** Chaque méthode de `huhui` **revérifie les deux descripteurs elle-même**
et rend `prompt.ErrNoTerminal` **avant de construire le moindre formulaire**. Un appelant qui
oublierait la porte échoue alors fermé, au lieu de créer une VM avec une sélection fantôme **puis de
pendre le job qui l'a lancée**.

Ce n'est pas de la ceinture-bretelles décorative : la porte du dessus est aujourd'hui un confort
(le `bufio` refuserait de lui-même sur EOF), et elle devient après `huh` la seule chose entre une CI
et la mesure (d). Une seule couche ferait dépendre une propriété de sûreté de la discipline de
l'appelant.

`huh.ErrUserAborted` (ctrl+c) se traduit en le refus « nothing was spawned » que den rend déjà.
`WithAccessible` n'est jamais activé.

### 5.3 Ce qui ne bouge pas

Cinq invariants, et chacun est ce qu'une réécriture perd en premier :

1. **`-i` reste une traduction vers `--without`.** `interactiveWithout` garde sa signature et rend
   toujours une liste de noms courts dans `nest.Resolve`. Seul le rendu interne change.
   `TestInteractiveProducesTheSameArgvAsTheEquivalentWithout` reste vert **sans être touché** — c'est
   la preuve qu'aucun second chemin de sélection n'est né.
2. **`-i` démarre tout coché, `select: prompt` démarre vide.** Porté par UN champ
   `MultiSelectRequest.Preselected bool`. Un paramètre, un fait : la décision 8 du 2026-08-11 survit
   telle quelle, et sa raison écrite (deux paramètres portant un seul fait sont deux choses à tenir
   d'accord) vaut encore plus ici. Tenable parce que la mesure (f) le dit : `huh` accepte une
   soumission vide et n'impose aucun plancher. Si un plancher existait, `select: prompt` serait
   inspawnable et cet invariant demanderait un contournement — il n'en demande pas.
3. **`unmappedNote` devient la description de l'option**, et nomme toujours le chemin denHome
   **résolu**, jamais le `config.yaml` nu. Annotation seulement : cocher une clé non mappée reste
   possible, et le refus qui suit reste celui de `resolveRepoKeys`, juge unique du mapping.
4. **Les dépôts requis ne sont ni listés ni numérotés** (spec §6.2).
5. **`den converge` imprime son plan, PUIS demande.** Le plan reste à l'écran au-dessus du `Confirm`
   en ligne. `internal/converge/render.go:20` appelle ce plan la frontière de confiance : « A
   confirmation prompt that hid it would be uninformed consent. » La mesure (c) rend cela gratuit.

## 6. Surface de test

Hermétique intégralement, comme le reste : aucun socket, aucun process, **aucun tty**.

- **`prompt.Fake`** — fichier de **production**, comme `internal/sbx/fake.go` et pour la même raison :
  `cli`, `spawn` et `converge` en ont tous besoin. Réponses scriptées **et** requêtes enregistrées,
  pour qu'un test affirme *ce que den a demandé*, pas seulement ce qu'il a fait — l'état initial des
  cases (invariant 2) ne se lit que là.
- **Tests portés** : ceux qui aujourd'hui poussent une chaîne dans `cmd.InOrStdin()` (`confirm`,
  `askRepositoryRoots`) scriptent le `Fake`. Leur assertion de sortie ne change pas.
- **Tests intouchés** : `TestInteractiveProducesTheSameArgvAsTheEquivalentWithout` et tous les tests
  de refus sans terminal. S'ils demandaient à être réécrits, l'invariant 1 ou le §5.2 serait cassé.
- **`huhui`** : non testé par la suite, isolé derrière l'interface — même doctrine que
  `ports.ListenScanner`, `ports.OpenURL` et `spawn.LooksInteractive` (CLAUDE.md). **Sauf sa porte** :
  le refus `ErrNoTerminal` EST testable contre `/dev/null`, un fichier régulier et un fichier fermé,
  exactement comme `internal/spawn/isterminal_test.go`. C'est la moitié qui compte, et c'est la
  mesure (d) transformée en test.
- **Garde d'import** : nouveau test à la manière d'`internal/ports/hermeticity_test.go` — analyse
  syntaxique (`go/build` + `go/parser`) des imports directs. Elle affirme **deux** choses, et la
  seconde n'est pas facultative :
  1. Seul `internal/prompt/huhui` importe `github.com/charmbracelet/*`.
  2. Seul `internal/cli` importe `internal/prompt/huhui`.

  La (1) seule ne protège rien de ce que le §5.1 promet : elle laisserait `internal/spawn` importer
  `huhui` et rendrait fausse, sans qu'aucun test bronche, la phrase « `internal/spawn` et
  `internal/converge` n'importent que `internal/prompt` ». La raison d'être de `internal/prompt`
  comme paquet feuille est de tenir `spawn` propre ; c'est la (2) qui la tient. Échec avec un message
  de graphe d'imports, comme l'existant. Limite documentée identique : `build.ImportDir` applique le
  GOOS/GOARCH de la machine.

## 7. Ordre de livraison

Tranches verticales, chacune laissant l'arbre vert :

1. `internal/prompt` — interface, quatre types de requête, `Fake`. Rien ne l'utilise encore.
2. **La checklist passe à `MultiSelect`**, derrière l'interface, avec le `Fake` en test.
   `TestInteractiveProducesTheSameArgvAsTheEquivalentWithout` vert sans retouche. *C'est la tranche
   qui répare la gêne mesurée* : elle est livrable seule.
3. `huhui` — l'adaptateur, sa porte fermée, son test `/dev/null`. Câblage dans `SystemDeps()`.
4. Portage de `confirm`, `askRepositoryRoots` et `ReadSecret`. `Deps.ReadSecret` retiré.
5. Garde d'import.
6. Documentation : amendement de la décision 4 du 2026-08-11 (renvoi vers cette spec) ; correction de
   la citation fausse de HANDOFF §8 dans `interactive.go` ; correction de l'affirmation fausse sur
   `golang.org/x/term` dans `isterminal_darwin.go` et `isterminal_linux.go`.

## 8. Hors périmètre, nommé pour ne pas être redécouvert

**L'ioctl brut de `isterminal_{darwin,linux}.go`.** Ces deux fichiers portent `unsafe` et un syscall
brut (`TIOCGETA` / `TCGETS`) parce que leur godoc affirmait que `golang.org/x/term` était interdit
— affirmation fausse le jour où elle a été écrite (§2.c). `term.IsTerminal` supprimerait les deux
fichiers, l'import `unsafe` et le premier syscall brut du dépôt. Le §7.6 corrige le **commentaire**,
pas le code : remplacer le mécanisme est un autre changement, avec sa propre mesure à refaire sur les
deux plateformes. Ticket séparé.

**Le mode `WithAccessible` de `huh`.** Jamais activé par den, et la raison est écrite ici pour qu'on
ne l'active pas « pour aider » : c'est un repli en clair, et den n'a pas de repli — il a un refus qui
nomme le drapeau équivalent.

## 9. Décisions

1. **`huh`, pas du mode brut sur `x/term`.** L'alternative — lire les touches à la main sur une
   dépendance déjà présente — coûtait 0 module et 0 Mo, et échouait fermé nativement. Elle a été
   posée sur la table avec ses chiffres, et écartée : den n'écrit pas son propre décodeur de touches
   et sa propre boucle de redessin, avec le resize et la restauration de `ctrl+c` à tenir, quand
   quatre invites doivent vivre.
2. **26 modules et ≈ +4 Mo sont acceptés**, chiffres mesurés en main au moment du choix, pas estimés.
   Le binaire reste statique et sans cgo : c'est la propriété que den annonce, et elle est intacte.
3. **Rendu en ligne, jamais en plein écran**, pour les quatre invites. La frontière de confiance de
   `den converge` est la raison, et elle vaut aussi pour les trois autres : den écrit au-dessus, il
   n'efface pas.
4. **La porte `IsTTY` reste au-dessus ET dans l'adaptateur.** Mesure (d) : sans terminal, `huh`
   confirme une sélection fantôme *et* ne rend pas la main. Le `bufio` d'aujourd'hui refusait de
   lui-même ; après cette spec, la porte est la seule chose qui tienne cette propriété, et une
   propriété de sûreté ne repose pas sur la discipline de l'appelant.
5. **Un seul paquet importe `charmbracelet`**, et un test le tient.
