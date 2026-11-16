package template

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/worktree"
)

// The leading dot keeps both out of reach of any template name, as with gen/.kits.
func CopyDir(denHome, name string) string {
	return filepath.Join(denHome, "gen", ".templates", name)
}

func StagingDir(denHome, name string) string {
	return filepath.Join(denHome, "gen", ".templates", ".staging-"+name)
}

// Clone takes a fresh clone, never a fetch into a live copy. Hooks and submodules stay off: the
// repository is not trusted to run anything on the host.
func Clone(ctx context.Context, g worktree.Git, url, ref, dest string) (string, error) {
	args := []string{"-c", "core.hooksPath=/dev/null", "-c", "protocol.ext.allow=never", "clone", "--no-recurse-submodules", "--single-branch"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", url, dest)
	if _, err := g.Run(ctx, gitDir, args...); err != nil {
		return "", fmt.Errorf("cloning %s: %w", url, err)
	}
	return Head(ctx, g, dest)
}

func Head(ctx context.Context, g worktree.Git, dir string) (string, error) {
	out, err := g.Run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("reading HEAD of %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitDir is where url-level git commands run: a git launched in den's cwd would read that
// repository's .git/config, which a VM can write (insteadOf and protocol.ext.allow turn a URL into a host command).
const gitDir = "/"

const remoteHeadTimeout = 3 * time.Second

// RemoteHead answers without a prompt and within remoteHeadTimeout: `den up` calls it on every start.
func RemoteHead(ctx context.Context, g worktree.Git, url, ref string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, remoteHeadTimeout)
	defer cancel()
	pattern := ref
	if pattern == "" {
		pattern = "HEAD"
	}
	args := []string{
		"-c", "core.sshCommand=ssh -o BatchMode=yes -o ConnectTimeout=3",
		"-c", "protocol.ext.allow=never",
		"ls-remote", "--", url, pattern}
	if ref != "" {
		// git lists the peeled commit of an annotated tag only when asked for it by name
		args = append(args, ref+"^{}")
	}
	out, err := g.Run(ctx, gitDir, args...)
	if err != nil {
		return "", fmt.Errorf("asking %s for %s: %w", url, pattern, err)
	}
	lines := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		sha, name, ok := strings.Cut(strings.TrimSpace(l), "\t")
		if ok {
			lines[name] = sha
		}
	}
	// Pattern HEAD also lists refs/remotes/origin/HEAD, and a ref like main also matches
	// refs/heads/x/main: only the exact name is the commit a clone checks out.
	candidates := []string{"HEAD"}
	if ref != "" {
		// clone --branch prefers a branch over a same-named tag; a peeled line is the commit, the bare tag line the tag object
		candidates = []string{"refs/heads/" + ref, "refs/tags/" + ref + "^{}", "refs/tags/" + ref}
	}
	for _, c := range candidates {
		if sha, ok := lines[c]; ok {
			return sha, nil
		}
	}
	return "", fmt.Errorf("%s has no %s", url, pattern)
}

// DefinitionHash covers den-template.yaml and every local file the template cites, so a changed
// kit, include or step marks the built image stale. A kit that is a registry reference is not a file.
func DefinitionHash(d *config.LoadedDefinitions, name string) (string, error) {
	def, ok := d.File.Templates[name]
	if !ok {
		return "", fmt.Errorf("%s declares no template %q", d.Path, name)
	}
	h := sha256.New()
	add := func(label string, content []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(label)))
		h.Write(n[:])
		h.Write([]byte(label))
		binary.BigEndian.PutUint64(n[:], uint64(len(content)))
		h.Write(n[:])
		h.Write(content)
	}
	raw, err := os.ReadFile(d.Path)
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", d.Path, err)
	}
	add(config.DefinitionFileName, raw)
	var cited []string
	for _, k := range def.Kits {
		if filepath.IsAbs(k) {
			cited = append(cited, k)
		}
	}
	cited = append(cited, def.Build.Includes...)
	cited = append(cited, def.Build.Steps...)
	for _, root := range cited {
		// WalkDir does not follow a symlinked root; LoadDefinitions accepts one that stays under Root.
		citedRel, err := filepath.Rel(d.Root, root)
		if err != nil {
			return "", fmt.Errorf("hashing %s cited by template %q in %s: %w", root, name, d.Path, err)
		}
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", fmt.Errorf("hashing %s cited by template %q in %s: %w", root, name, d.Path, err)
		}
		err = filepath.WalkDir(real, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(real, p)
			if err != nil {
				return err
			}
			label := filepath.ToSlash(filepath.Join(citedRel, rel))
			if e.Type()&fs.ModeSymlink != 0 {
				target, err := os.Readlink(p)
				if err != nil {
					return err
				}
				add("link:"+label, []byte(target))
				return nil
			}
			if e.IsDir() {
				return nil
			}
			content, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			add(label, content)
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("hashing %s cited by template %q in %s: %w", root, name, d.Path, err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
