# HANDOFF — Plan 2 (Spawn), reprise à la tâche 16

**Date :** 2026-07-28 · **Branche :** `main` · **HEAD :** `df8e732` · **Rien n'est poussé.**

---

## 1. Ta première action

Lis **`.superpowers/sdd/2026-07-28-den-plan2-spawn/progress.md`**. C'est le registre de la session :
il porte chaque arbitrage, chaque mesure et chaque dette, dans l'ordre. Ce handoff n'en est que la
porte d'entrée — **en cas de contradiction, le registre fait foi**, et `git log` fait foi sur le
registre.

Le plan est dans `docs/superpowers/plans/2026-07-28-den-plan2-spawn.md`. Les briefs par tâche
s'extraient avec `scripts/task-brief PLAN_FILE N` — **ne redonne jamais le plan entier à un
implémenteur**.

## 2. État exact

**Tâches 1 à 15 closes.** Suite verte : 9 paquets `ok`, `go vet`, `GOOS=darwin go vet` et
`gofmt -l` muets.

| # | Tâche | État |
|---|---|---|
| 1–9 | spec, agent, sbx, config, nest | ✅ closes |
| 10 | `internal/worktree` | ✅ close — **5 fix rounds**, le maximum de la méthode |
| 11 | `policy.Settle` | ✅ close — 3 fix rounds |
| 12 | `internal/spawn` + `den <nest>` | ✅ close — 1 fix round |
| 13 | `den ls` | ✅ close — 3 fix rounds |
| 14 | `den sh` + réconciliation spawn-or-attach | ✅ close — 2 fix rounds |
| 15a | corbeille + dette de T10 | ✅ close — 1 fix round |
| 15b | `den rm` | ✅ close — **4 fix rounds** |
| **16** | **`ListNests` tolérante** | **à faire (Sonnet)** — dette élargie, voir §4 |
| **17** | **Configs hostiles** | **à faire (Opus)** — périmètre très élargi, voir §4 |
| **18** | **`ssh.mode: agent-forward`** | **à faire** — après T17 et après le premier smoke test réel |
| — | **Revue finale de branche** | à faire sur le modèle le plus capable, puis `finishing-a-development-branch` |

L'arbre de travail ne porte que des fichiers d'avant la session : `run.sh`, le plan 2, deux handoffs
(non suivis) et `HANDOFF.md` modifié.

**T16 et T17 touchent `internal/cli` ou `internal/nest` → garde l'exécution strictement séquentielle.**

## 3. Ce que le plan 2 a livré

`den <nest>` spawne de bout en bout ; `den ls`, `den sh`, `den rm` existent. Trois acquis
structurels valent d'être connus avant de toucher quoi que ce soit :

1. **`internal/worktree` ne supprime plus jamais.** `Retire` déplace vers
   `<den_home>/trash/<horodatage>-<nest>-<repo>`, avec repli EXDEV et rétention de 30 jours.
   Vérifié sans perte par mesure : liens durs, liens absolus, fifo, socket unix, arbre profond,
   descripteur ouvert pendant le déplacement. **Ne réintroduis aucune suppression.**
2. **Une seule source pour chaque accès système.** `cli.Deps{Doctor, Sbx, Git, Policy}` : le champ
   `Sbx` est **unique** et partagé par `den ls`, `den sh`, `den rm` et le spawn. Verrouillé par
   `internal/cli/root_deps_test.go`. **Ne réintroduis ni second `sbx.Runner` ni `worktree.NewGit()`
   en dur.**
3. **Les tests n'écrivent plus dans le dépôt de l'environnement.**
   `worktree.NeutraliseEnvironnementGit()` est appelée par trois `TestMain`, et
   `internal/worktree/hermetisme_test.go` fait **échouer `go test ./...`** si un paquet lance git en
   clair sans l'appeler — en respectant les contraintes de build (`go/build.ImportDir`).

## 4. Les trois tâches restantes, avec leur dette

### T16 — `ListNests` tolérante

Le brief d'origine ne couvre pas ce que la relecture de T13 a mesuré : **un seul nest YAML cassé fait
chuter tous les autres nests déjà résolus**. Conséquence sur `den ls` : toutes les sandboxes sont
marquées `« ? »` — y compris celles dont le nest est parfaitement valide — **sans aucun signal**,
puisque `internal/cli/ls.go` avale l'erreur délibérément (choix assumé : un `~/.den` cassé ne doit
pas masquer des VM vivantes).

T16 doit distinguer **trois cas** : `~/.den` absent, un nest cassé parmi N valides, et une racine
illisible.

### T17 — configs hostiles (périmètre très élargi)

Au brief d'origine s'ajoutent, tous consignés au registre :

- `internal/sbx` **et** `internal/worktree` à ajouter à l'exclusion du grep `os/exec` (leurs tests
  lancent légitimement des processus) ;
- une 13ᵉ config hostile : aucun contrôle d'existence sur `Kit`/`Kits` ;
- une 14ᵉ : `worktree_layout: centrl` retombe **silencieusement** sur `central` ;
- `config_dir` vide qui atteint la VM ;
- `bin_dirs` contenant `$(...)` **exécuté** par le bash de la VM au boot ;
- le **plancher de version git** non déclaré (`--path-format=absolute` exige git ≥ 2.31) —
  appartient à `doctor` ;
- l'inventaire **A1→A9** des hypothèses non falsifiables sur `sbx` ;
- **collision nom-de-nest / sous-commande** : `den doctr` (typo) part en spawn d'un nest « doctr »
  au lieu de suggérer `doctor`. Contrepartie directe du choix « la racine EST la commande de spawn » ;
- messages cobra en **anglais** sur `den <nest>` (projet francophone) ;
- image de stack absente non contrôlée ; `ssh.dir` inexistant part tel quel dans l'argv ;
- la **liste blanche de statut** `{"running"}` refusera aussi un statut transitoire (`starting`,
  `booting`) — choix assumé, le premier smoke test réel dira s'il faut l'élargir.

### T18 — `ssh.mode: agent-forward`

C'est le **défaut** de `config.go` et il n'est implémenté **nulle part** : toute sandbox spawnée en
configuration par défaut sort **sans accès SSH**, silencieusement. À caler **après T17 et après le
premier smoke test réel**, le mécanisme côté `sbx` étant invérifiable ici.

## 5. Les décisions utilisateur — à ne pas re-litiger

1. **Exécution directe sur `main`** → orchestration **séquentielle**, jamais deux implémenteurs en
   parallèle. **Et jamais deux relecteurs non plus** (voir §7).
2. **Charset des noms resserré** `^[A-Za-z0-9][A-Za-z0-9+-]*$`.
3. **Branche de worktree depuis la branche par défaut** (`git symbolic-ref --short
   refs/remotes/origin/HEAD`, repli sur le HEAD courant), `--no-track`.
4. **Fichiers ignorés** : ne bloquer que les entrées ignorées **isolées** (qui ne finissent pas
   par `/`), après `git check-ignore` sur le chemin **sans** son slash final.
5. **Corbeille** — implémentée en T15a. `Retire` ne supprime plus jamais.
6. **Avertir et attacher** sur dérive de configuration — implémenté en T14.
7. **Tâche 18** pour `ssh.mode: agent-forward`.

## 6. Méthode — ce qui a marché

Un implémenteur neuf par tâche, une relecture par tâche, fix rounds en réveillant le **même**
implémenteur, puis re-relecture **scopée au diff du correctif**. Le contrôleur vérifie **lui-même**
la suite et les invariants portants avant de dispatcher, plutôt que de croire les rapports.

**La constante des quinze tâches : aucun fix round n'a jamais été déclenché par du code faux.** À
chaque fois le code était juste et c'est le **test** qui ne prouvait rien, ou une garde qui manquait.
Tous les défauts trouvés passaient une suite verte.

**Le motif dominant : une assertion verte parce qu'un TIERS rattrape `den`.** Inventaire des tiers
rencontrés — il vaut comme grille de lecture : git, `os/exec`, l'OS, **la config git corrompue du
poste**, un double trop complaisant, **Linux** masquant une régression macOS, **l'absence de `sbx`**
faisant échouer un test plus tôt que prévu, **`den` lui-même** (une colonne de sortie rattrapant deux
autres colonnes), **une assertion vraie par construction** (`Contains(err, "")`), **un témoin arrêté
par une garde antérieure à celle qu'il visait**, et **une garde satisfaite par un commentaire**.

**Ce qui a le mieux payé : exiger la MESURE, pas le raisonnement.** Et deux gestes précis :

- **Fabriquer l'occurrence** plutôt que laisser un doute ouvert. Deux doutes d'implémenteur ont été
  levés en trente secondes ainsi : les alias d'import (`pkgexec "os/exec"`) et la fragilité sous
  charge (35 exécutions à load average 32).
- **Poser la question d'architecture** quand un motif revient trois fois, au lieu d'enchaîner un
  correctif de plus. C'est ce qui a fermé T10 (la corbeille), T13 (`Sbx` unique) et T15b (la garde
  d'hermétisme).

**Le geste qui a le plus rapporté cette session** : après avoir fermé un défaut dans un paquet,
**mesurer immédiatement quels autres paquets ont la même surface**. Le balayage des neuf paquets au
cobaye a trouvé une fuite que trois relectures avaient manquée.

## 7. Pièges d'orchestration — mis à jour

- **La mise en veille d'un agent ne dit RIEN de ce qu'il a produit.** Quatre agents se sont mis en
  veille sans rendre leur travail cette session. Seuls `git log` et l'existence des fichiers font foi.
- **Avant de conclure « agent mort » sur un `git log` vide, regarder les mtimes** de l'arbre de
  travail : un agent en vol laisse des fichiers non commités qui bougent. (Faux diagnostic commis à
  la reprise de cette session.)
- **JAMAIS DEUX RELECTEURS EN PARALLÈLE** — la règle « jamais deux implémenteurs » vaut aussi pour
  eux, **parce qu'un relecteur mute le dépôt pour mesurer**. Deux relecteurs simultanés se corrompent
  mutuellement : un test déterministe a échoué 5 fois sur 5 puis réussi 30 fois, sur du code
  inchangé.
- **Interdire de déléguer, en PREMIÈRE ligne du prompt.** Un relecteur a sous-traité sa mission et
  n'a jamais rien produit ; son sous-agent avait terminé toutes ses mesures et s'est fait interrompre.
- **Une consigne relayée d'agent à agent se déforme.** Dire à un agent de changer de méthode ne
  suffit pas : lui dire aussi ce qu'il ne doit **pas** en déduire pour les autres.
- **Les messages se croisent.** Trois fois cette session, un agent a répondu à un message périmé.
  Vérifier `git log` avant de conclure à une divergence.
- **Interdire des ASSERTIONS, pas des FICHIERS.** L'élargissement d'une interface force
  légitimement un double de test à changer ; une interdiction posée sur le fichier bloque un
  changement mécanique nécessaire.
- **Les implémenteurs ont eu raison contre le contrôleur six fois**, chaque fois sur mesure. Quand
  un agent conteste une consigne **avec une mesure**, il a probablement raison.

### Les trois formes de fausse mesure rencontrées — à connaître

Toutes trois produisent la **sortie d'une vérification réussie** alors qu'elles ne vérifient rien :

1. **La mutation qui ne compile pas.** Neutraliser une condition par `if false` rend des imports
   inutilisés ; l'échec de compilation se lit comme un « rouge ». **Neutraliser par `&& false` ou
   `|| true`**, ce qui garde les symboles utilisés. *(Commis quatre fois.)*
2. **La mutation qui ne s'applique pas.** Une chaîne mal indentée ne matche pas, le test tourne sur
   le fichier intact et affiche `ok` — indiscernable d'un mutant survivant. **Faire échouer
   bruyamment quand le remplacement ne s'applique pas.**
3. **La mutation sémantiquement neutre sur le cas visé.** Remplacer `s.Statut` par `""` laisse le
   message correct pour le sous-cas `statut=""`. **Vérifier que la mutation casse bien la propriété
   du sous-cas qu'on instruit.**

## 8. Contraintes qui n'ont pas bougé

- **`sbx` n'est pas installé.** Aucun agent ne peut vérifier quoi que ce soit contre lui. Tout
  rapport affirmant qu'un spawn fonctionne est faux. En revanche **un test qui TENTE d'exécuter le
  vrai `sbx` est un défaut**.
- ⚠️ **Le `core.excludesfile` de ce poste est CORROMPU** (`/home/agent/.gitignore_global`, 5 octets
  NUL avant `.sbx`) : git y ignore **tout chemin finissant par `/`**. Neutraliser par
  `GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null` dans toute sonde touchant `.gitignore`.
- ⚠️ **`filepath.EvalSymlinks` est portant sur macOS** (`$TMPDIR` et `/var` y sont des liens) ; ce
  poste sous Linux **ne peut pas voir** les régressions correspondantes. `GOOS=darwin go vet` est le
  seul contrôle disponible — le passer.
- **Un golden ne change qu'APRÈS** que les assertions sémantiques dédiées sont vertes. Jamais de
  régénération pour faire passer un test.
- **Aucun commentaire qui affirme une propriété non vérifiée** : ou bien un test la prouve, ou bien
  on écrit la condition réelle et ce qui la casserait. *(Règle née d'un finding de T13 ; elle a
  produit six findings depuis.)*
- **Français** partout ; **messages de commit sans accents**.
- **Format d'erreur** : `contexte : détail`, nommant le chemin complet et les valeurs disponibles.
- **Aucune dépendance nouvelle** (stdlib + cobra + yaml.v3).
- Fichiers temporaires dans le scratchpad de session, **jamais** dans le dépôt.
