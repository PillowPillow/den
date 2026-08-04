# Plan d'implémentation — `install.sh`

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal** : chemin d'installation `curl | sh` pour les machines sans Homebrew (distribs Linux, WSL, macOS sans brew), avec vérification sha256 fail-closed.

**Architecture** : un script POSIX sh à la racine du repo, servi via raw.githubusercontent depuis `main`. Il résout le tag via la redirection `/releases/latest`, télécharge l'archive goreleaser + `checksums.txt`, vérifie, installe dans `~/.local/bin`. CI : shellcheck + smoke contre la vraie latest release.

**Tech stack** : POSIX sh, curl, tar, sha256sum/shasum, GitHub Actions.

**Spec** : `docs/superpowers/specs/2026-08-04-install-script-design.md`.

**Modèles d'exécution** : Task 1 = opus, effort xhigh (le script est la surface d'attaque et le gros du raisonnement). Tasks 2 et 3 = sonnet (mécaniques).

## Global Constraints

- POSIX sh strict — pas de bashisme ; shellcheck (qui suit le shebang `#!/bin/sh`) doit passer sans warning.
- Code et messages en anglais ; les erreurs nomment le remède (convention repo).
- Naming d'archive : `den_<version sans v>_<os>_<arch>.tar.gz` — copie du `name_template` de `.goreleaser.yaml`. Vérifié sur la release réelle `v1.0.1` le 2026-08-04.
- `den version` répond `den <tag>` (ex. `den v1.0.1`) — vérifié sur binaire local.
- Pas de sudo, pas de dépendance jq / api.github.com.
- Commits sur la branche courante `build/release-pipeline`.

## Faits vérifiés (2026-08-04, ne pas re-deviner)

- Release `v1.0.1` publiée ; assets : `checksums.txt`, `den_1.0.1_{darwin,linux}_{amd64,arm64}.tar.gz`.
- L'archive contient `LICENSE`, `README.md`, `den` à la racine (pas de dossier wrapper).
- `curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/PillowPillow/den/releases/latest` → `https://github.com/PillowPillow/den/releases/tag/v1.0.1` ; le tag est donc `${url##*/}`.
- shellcheck est préinstallé sur les runners `ubuntu-latest`.

---

### Task 1 : `install.sh` — **agent opus, effort xhigh**

**Files:**
- Create: `install.sh` (racine du repo, à côté du Makefile)

**Interfaces:**
- Consumes: naming d'archive de `.goreleaser.yaml` (contrainte globale ci-dessus).
- Produces: `install.sh` exécutable en `sh install.sh`, honorant `DEN_VERSION` et `DEN_INSTALL_DIR` — la CI (Task 2) et le README (Task 3) s'appuient exactement sur ces deux noms de variables.

- [ ] **Step 1 : écrire `install.sh`**

Contenu complet (les commentaires « why » sont la norme du repo — les garder) :

```sh
#!/bin/sh
# Installs den from the GitHub release archives — the no-brew path (Linux
# distros, WSL, macOS without Homebrew). The sha256 checksum is verified
# before anything is installed, and the script refuses to run without a
# checksum tool: an unverifiable binary is never installed silently, the same
# fail-closed stance as the rest of den.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | sh
#   curl -fsSL ... | DEN_VERSION=v1.0.1 sh      # pin a version
#   curl -fsSL ... | DEN_INSTALL_DIR=~/bin sh   # override the destination
# (the assignment binds to sh, not curl — each pipeline command gets its own
# environment, so a prefix on curl never reaches the script)
set -eu

REPO="PillowPillow/den"
INSTALL_DIR="${DEN_INSTALL_DIR:-$HOME/.local/bin}"

fail() {
    printf 'install.sh: %s\n' "$1" >&2
    exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required — install it with your package manager and re-run"
command -v tar >/dev/null 2>&1 || fail "tar is required — install it with your package manager and re-run"

case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail "unsupported OS '$(uname -s)' — releases cover darwin and linux (amd64/arm64) only; see https://github.com/$REPO/releases" ;;
esac

case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) fail "unsupported architecture '$(uname -m)' — releases cover amd64 and arm64 only; see https://github.com/$REPO/releases" ;;
esac

# Fail closed BEFORE any download: no checksum tool means no install, not an
# unverified one.
if command -v sha256sum >/dev/null 2>&1; then
    checksum_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
    checksum_tool=shasum
else
    fail "neither sha256sum nor shasum is available — refusing to install an unverified binary; install coreutils and re-run"
fi

tag="${DEN_VERSION:-}"
if [ -z "$tag" ]; then
    # The /releases/latest redirect names the tag in its final URL. This
    # avoids api.github.com (60 unauthenticated requests/hour) and a jq
    # dependency the target machines may not have.
    final_url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") \
        || fail "cannot resolve the latest release from github.com — check your network, or pin one with DEN_VERSION=v1.0.1"
    tag="${final_url##*/}"
    case "$tag" in
        v*) ;;
        *) fail "could not parse a release tag from '$final_url' — pin one with DEN_VERSION=v1.0.1" ;;
    esac
fi

# Archive names mirror .goreleaser.yaml's name_template, which uses the
# version WITHOUT the leading v. The install-script CI job smokes this
# contract against the real latest release.
archive="den_${tag#v}_${os}_${arch}.tar.gz"
base_url="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'Downloading den %s (%s/%s)...\n' "$tag" "$os" "$arch"
curl -fsSL -o "$tmp/$archive" "$base_url/$archive" \
    || fail "download failed for $base_url/$archive — does release $tag exist and ship $os/$arch?"
curl -fsSL -o "$tmp/checksums.txt" "$base_url/checksums.txt" \
    || fail "download failed for $base_url/checksums.txt"

grep " $archive\$" "$tmp/checksums.txt" > "$tmp/expected.txt" \
    || fail "checksums.txt of $tag has no entry for $archive — the release layout changed; report this at https://github.com/$REPO/issues"
case "$checksum_tool" in
    sha256sum) (cd "$tmp" && sha256sum -c expected.txt >/dev/null 2>&1) ;;
    shasum) (cd "$tmp" && shasum -a 256 -c expected.txt >/dev/null 2>&1) ;;
esac || fail "checksum mismatch for $archive — the download is corrupted or tampered with; re-run, and report it if this persists"

tar -xzf "$tmp/$archive" -C "$tmp" den \
    || fail "could not extract 'den' from $archive — the release layout changed; report this at https://github.com/$REPO/issues"

mkdir -p "$INSTALL_DIR" || fail "cannot create $INSTALL_DIR — pick another destination with DEN_INSTALL_DIR"
install -m 755 "$tmp/den" "$INSTALL_DIR/den"

printf 'Installed %s/den\n' "$INSTALL_DIR"
case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
        printf 'warning: %s is not in your PATH — add this line to your shell rc (~/.bashrc, ~/.zshrc, ...):\n' "$INSTALL_DIR" >&2
        printf '  export PATH="%s:$PATH"\n' "$INSTALL_DIR" >&2
        ;;
esac

# Proof, not promise: the installed binary names the tag it was built from.
"$INSTALL_DIR/den" version
```

- [ ] **Step 2 : shellcheck**

Run : `shellcheck install.sh` (si absent en local : `brew install shellcheck`).
Expected : aucune sortie, exit 0. Corriger tout warning — ne jamais le désactiver par directive sans justification écrite en commentaire.

- [ ] **Step 3 : smoke local — latest**

Run : `TMP=$(mktemp -d) && DEN_INSTALL_DIR="$TMP/bin" sh install.sh && "$TMP/bin/den" version && rm -rf "$TMP"`
Expected : téléchargement, `Installed .../bin/den`, puis `den v1.0.1` (ou le tag latest du moment). Le warning PATH est attendu (destination temporaire).

- [ ] **Step 4 : smoke local — version pinnée + erreur propre**

Run : `TMP=$(mktemp -d) && DEN_INSTALL_DIR="$TMP/bin" DEN_VERSION=v1.0.1 sh install.sh && rm -rf "$TMP"`
Expected : installe v1.0.1.
Run : `DEN_VERSION=v9.9.9 sh install.sh; echo "exit=$?"`
Expected : `install.sh: download failed for .../v9.9.9/... — does release v9.9.9 exist and ship darwin/arm64?`, exit=1, aucun fichier installé.

- [ ] **Step 5 : commit**

```bash
git add install.sh
git commit -m "feat(install): curl|sh installer — checksum-verified, fail-closed, no brew required"
```

---

### Task 2 : job CI `install-script` — **agent sonnet**

**Files:**
- Modify: `.github/workflows/ci.yml` (ajouter un job après `checks`)

**Interfaces:**
- Consumes: `install.sh` à la racine, honorant `DEN_INSTALL_DIR` ; `den version` répond `den <tag>`.
- Produces: job `install-script` dans le workflow `ci`.

- [ ] **Step 1 : ajouter le job**

Ajouter à la fin de `.github/workflows/ci.yml` (même niveau que `checks:`) :

```yaml
  # install.sh is an interface between .goreleaser.yaml's archive naming and
  # whatever machine runs `curl | sh`. shellcheck locks the POSIX-sh promise;
  # the smoke run against the real latest release is the only guard on the
  # archive-name contract — nothing else tests it. This job is also the one
  # deliberate exception to "no network in the suite": the thing under test
  # IS the network path.
  install-script:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: shellcheck install.sh
      - name: smoke against the latest release
        run: |
          DEN_INSTALL_DIR="$RUNNER_TEMP/bin" sh install.sh
          final_url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/PillowPillow/den/releases/latest)
          tag="${final_url##*/}"
          got=$("$RUNNER_TEMP/bin/den" version)
          if [ "$got" != "den $tag" ]; then
            echo "den version answered '$got', expected 'den $tag'"
            exit 1
          fi
```

Mettre à jour le commentaire d'en-tête du fichier (lignes 1-4) : il affirme « no socket, no process » pour toute la CI — le restreindre au job `checks` et mentionner l'exception `install-script`. Reformulation guide (à adapter) : le job `checks` reste hermétique ; `install-script` est l'exception volontaire parce que son objet de test est le chemin réseau.

- [ ] **Step 2 : valider le YAML**

Run : `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml'))" && shellcheck install.sh`
Expected : exit 0. (Le smoke complet ne tourne qu'en CI ; il a déjà été joué localement en Task 1.)

- [ ] **Step 3 : commit**

```bash
git add .github/workflows/ci.yml
git commit -m "build(ci): install.sh gets shellcheck + a smoke against the real latest release"
```

---

### Task 3 : section README — **agent sonnet**

**Files:**
- Modify: `README.md:6-33` (section Installation)

**Interfaces:**
- Consumes: URL raw du script, variables `DEN_VERSION` / `DEN_INSTALL_DIR`.

- [ ] **Step 1 : insérer la section**

Dans `README.md`, remplacer le paragraphe lignes 20-21 (« Linux users can also grab a prebuilt archive from the releases page. ») par :

```markdown
No Homebrew (Linux distros, WSL, macOS without brew):

```bash
curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | sh
```

The script detects OS and architecture, verifies the release checksum (and
refuses to install without verification), and installs to `~/.local/bin`.
Pin a version or change the destination by placing the assignment on `sh`
(each pipeline command gets its own environment, so a prefix on `curl` never
reaches the script):

```bash
curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | DEN_VERSION=v1.0.1 sh
curl -fsSL https://raw.githubusercontent.com/PillowPillow/den/main/install.sh | DEN_INSTALL_DIR=~/bin sh
```

Prebuilt archives also sit on the
[releases page](https://github.com/PillowPillow/den/releases) if you'd rather
not pipe a script into your shell.
```

Attention à l'imbrication des fences : le bloc bash est dans le README, pas dans ce plan — reproduire le contenu, pas les fences externes.

Vérifier la phrase du README lignes 29-33 (stamping de version) : « via Homebrew, `go install` and releases » couvre déjà le script (qui installe les archives de release) — ajouter « (including `install.sh`) » après « releases » pour lever le doute.

- [ ] **Step 2 : relecture rendu**

Run : `grep -n "install.sh\|DEN_VERSION\|DEN_INSTALL_DIR" README.md`
Expected : la section apparaît une fois, les deux variables sont documentées.

- [ ] **Step 3 : commit**

```bash
git add README.md
git commit -m "docs(readme): install.sh joins the installation paths — the no-brew route"
```
