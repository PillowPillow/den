# Spec — `/release` : couper une version avec un changelog écrit

**Date** : 2026-08-04
**Statut** : validé (brainstorming du 2026-08-04)
**Complète** : le pipeline goreleaser (`.goreleaser.yaml`, `.github/workflows/release.yml`) et la
spec `2026-08-04-install-script-design.md` (qui consomme les tags publiés).

## Problème

Depuis `v1.0.0`, couper une version se fait à la main : relire `git log` depuis le dernier tag,
choisir le bump, rédiger un corps de tag annoté, pousser. Deux défauts.

D'abord, **rien n'est écrit dans le repo** : l'historique lisible d'un humain n'existe que dans les
corps de tags et sur la page releases de GitHub. Ensuite, **le corps de tag rédigé n'apparaît nulle
part** : goreleaser publiait jusqu'ici sa propre liste de commits (`changelog.use: git`, filtres
`^docs|^test|^chore`), donc le texte soigné du tag était invisible et la page release donnait un
`git log` reformaté.

## Interface

Une slash-command Claude Code versionnée dans le repo : `.claude/commands/release.md`.

```
/release            # bump déduit des commits
/release minor      # bump forcé
/release v1.4.0     # version exacte
```

Elle fait tout jusqu'au tag local, **puis s'arrête** et attend un go explicite avant `git push`.
Le push déclenche `release.yml` → goreleaser → release publique + bump du cask Homebrew : c'est le
seul pas non réversible de la séquence, donc c'est là qu'est la pause.

## Une source, trois sorties

Le texte curé est rédigé **une fois** et atterrit à trois endroits :

| Sortie | Comment |
|---|---|
| `CHANGELOG.md` | section `## vX.Y.Z — YYYY-MM-DD`, la plus récente en haut |
| corps du tag annoté | même texte, sans le titre `##`, précédé d'une ligne de sujet |
| corps de la release GitHub | `release.header: {{ .TagBody }}` dans `.goreleaser.yaml` |

Rejeté : rédiger les trois séparément (dérive garantie), et rédiger uniquement le corps de tag
(l'historique reste illisible depuis un clone).

## Contraintes mesurées

Trois faits ont été établis contre le binaire goreleaser v2.12.7 et sa source, pas supposés. Ils
expliquent la configuration, et une régression sur l'un d'eux casse la page release en silence.

**`git tag -F` supprime les lignes `#`.** Le nettoyage par défaut de git traite `#` comme un
commentaire : un corps de tag contenant `### Added` arrive dans l'objet tag **sans** cette ligne,
sans avertissement. Vérifié le 2026-08-04 (`git cat-file tag`). D'où `--cleanup=verbatim`,
obligatoire et pas cosmétique — sinon la page release perd tous ses titres.

**`release.header` est rendu par le pipe release, pas par le pipe changelog.**
`internal/pipe/release/body.go:48` applique `ctx.Config.Release.Header` et assemble
`header + ReleaseNotes + footer`. Le pipe changelog, lui, ne remplit que `ctx.ReleaseNotes`. Donc
`changelog.disable: true` laisse `ReleaseNotes` vide **et le header rendu** : le corps de release
est exactement le corps du tag, rien d'ajouté.

**`filters.exclude: [".*"]` ne suffit pas.** Premier essai retenu à tort : vider la liste des
commits laisse quand même le titre `## Changelog` de goreleaser dans le corps, un titre sans rien
dessous. `disable: true` saute le pipe entier.

## Comportement

1. **Préflight, en refus** : `git fetch origin --tags` d'abord (sans quoi la comparaison à
   `origin/main` lit une référence périmée), branche `main`, aucun fichier suivi modifié — les
   non-suivis passent, ce repo en porte en permanence (`claudedocs/`) —, `origin/main`
   synchronisé, puis `make lint`, `make typecheck`, `make test` verts. `release.yml` rejoue ces
   trois portes sur le commit tagué : une porte rouge ici, c'est un tag public dont la CI échoue —
   le seul état qu'on ne peut plus nettoyer. Une fois la version connue, le tag est vérifié libre
   **des deux côtés** avant le commit : un tag déjà pris ferait échouer `git tag -a` alors que le
   commit de changelog est déjà sur `main`.
2. **Lecture des commits** depuis le dernier tag, sujet *et* corps.
3. **Version** : `feat` → mineure, sinon patch. **La majeure n'est jamais déduite** : `!` et
   `BREAKING CHANGE:` vivent dans le corps du commit, et le repo merge en squash — leur absence ne
   prouve rien. Une majeure exige `major` ou `vX.Y.Z` en argument.
4. **Rédaction** de la section, en anglais (convention du repo : l'anglais pour tout ce qui est
   lu par un utilisateur, le français réservé à `docs/superpowers/`). Une ligne par changement
   *visible*, pas par commit ; un test ou un refactor n'a pas de ligne. Le `git log` est déjà dans
   le repo, on ne le reformate pas.
5. **Commit** `release: vX.Y.Z` directement sur `main`. Écart assumé avec la règle « branche +
   PR » : ce commit est mécanique, généré, et relu à l'étape 7 avant tout push. Le travail de
   fonctionnalité continue de passer par une PR.
6. **Tag annoté** `--cleanup=verbatim`, fichier de notes écrit dans le scratchpad de session.
7. **Affichage puis arrêt** : le diff du commit et `%(contents:body)` du tag — ce que le monde
   lira. Refus → `git tag -d` + `git reset --hard HEAD~1`.
8. **Sur go** : `git push origin main vX.Y.Z`, `gh run watch`, puis comparaison de
   `gh release view --json body` avec le corps affiché à l'étape 7. Le rendu du header n'existe
   que pendant une publication réelle : l'étape 7 est l'attente, l'étape 8 la vérification. En cas
   d'écart, `gh release edit --notes-file` répare la release en place plutôt que de retaguer.

## Portée

`CHANGELOG.md` naît avec `v1.0.0` et `v1.0.1` rédigées à partir de leurs corps de tags et de leurs
commits. `v1.1.0`, taguée localement le 2026-08-04 et jamais poussée, a été supprimée : elle sera
recréée par la commande, ce qui en fait son premier essai de bout en bout.

Hors portée : générer le changelog en CI (le texte demande un jugement humain, et la CI n'a rien à
rédiger), et versionner autre chose que le tag — aucun fichier ne porte le numéro de version, il
vient du tag via `-ldflags`.
