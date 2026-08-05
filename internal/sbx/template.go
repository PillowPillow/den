package sbx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Template is one image `sbx template ls --json` reports.
//
// Schema recorded 2026-07-31 against sbx v0.35.0 (spec §14.0):
//
//	{"images":[{"id","repository","tag","flavor","created_at","size"}]}
//
// This surface is what makes `den build` honest. Issue #8 was blocked on it:
// "build the ancestors only if their image is missing" rested on no attested
// command at all, and the fallbacks were a container-runtime dependency
// (`docker image inspect`) or giving up on the check entirely. `sbx template
// ls` closes that — and it is also the ONLY way den can say "run `den build
// <stack>`" instead of relaying sbx's own refusal, which is a
// `403 Forbidden: pull failed for image "X"` and speaks of authorization
// rather than of a missing build.
//
// Decoding stays tolerant of unknown fields, like Sandbox: this is a
// third-party tool's output, and a field a later sbx adds must not break
// `den build`.
type Template struct {
	// Repository and Tag are SEPARATE here, while a stack writes them as one
	// string (`image: docker.io/library/devx:v1`). Matching the two is what
	// NormalizeImageRef exists for.
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	ID         string `json:"id"`
	Flavor     string `json:"flavor"`
}

// Ref renders the template as the one string a stack's `image:` would carry.
// Used for display — the "available images" list of a refusal — never for
// comparison, which goes through NormalizeImageRef on both sides.
func (t Template) Ref() string { return t.Repository + ":" + t.Tag }

// Templates lists the images sbx knows about, in sbx's own order.
//
// NOT sorted: the caller displays what it needs and sorts there if it must.
// The order sbx returns is by creation time, which is more useful in a
// diagnostic than an alphabetical one, and sorting here would take that away
// from every caller at once.
func Templates(ctx context.Context, r Runner) ([]Template, error) {
	output, err := r.Run(ctx, "template", "ls", "--json")
	if err != nil {
		// AS IS, like Ls: ExecError already renders the binary and the full
		// argv, and a prefix here would write the subcommand twice.
		return nil, err
	}

	// Two-step decoding, the doctrine Ls states in full: a valid JSON object
	// WITHOUT the "images" key — sbx renaming it — must be an error and not a
	// silent empty list. Silence here is expensive in a precise way: an empty
	// list reads as "no image is built", so `den build` would rebuild
	// everything and `den spawn` would refuse every spawn with "run `den
	// build`" on a machine where every image is present.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(output, &fields); err != nil {
		return nil, fmt.Errorf("sbx template ls: unreadable JSON output (%w): %s", err, string(output))
	}
	raw, present := fields["images"]
	if !present {
		return nil, fmt.Errorf("sbx template ls: key %q absent from the JSON output: %s", "images", string(output))
	}

	var images []Template
	if err := json.Unmarshal(raw, &images); err != nil {
		return nil, fmt.Errorf("sbx template ls: unreadable JSON output (%w): %s", err, string(output))
	}
	return images, nil
}

// defaultRegistry and defaultNamespace complete an unqualified image
// reference, the way every OCI client does: `devx:v1` names
// `docker.io/library/devx:v1`, and `acme/devx:v1` names `docker.io/acme/devx:v1`.
const (
	defaultRegistry  = "docker.io"
	defaultNamespace = "library"
	// defaultTag is the tag an unqualified reference carries. `latest` is not
	// den's invention: it is what a registry pull of a tagless name resolves
	// to, so `image: devx` and the template `devx:latest` are the same image
	// everywhere else too.
	defaultTag = "latest"
	// indexDockerIORegistry is docker.io's other name: it is the registry host
	// that predates the shorter `docker.io` and every OCI client still
	// resolves it to the exact same registry. NormalizeImageRef folds it to
	// defaultRegistry (see below) rather than treating it as a distinct host,
	// because `sbx template ls` reports images ONLY as `docker.io/...` (spec
	// §14.0) — an unfolded `index.docker.io/devx:v1` would normalize to a
	// registry sbx never spells, and FindTemplate would then refuse a spawn
	// over an image that is actually present.
	indexDockerIORegistry = "index.docker.io"
)

// NormalizeImageRef splits an image reference into a FULLY QUALIFIED
// repository and a tag, so that the one string a stack writes can be compared
// with the two fields sbx reports.
//
// This normalization is the only real design question issue #8 had left, and
// it is written out rather than approximated because every shape below is a
// shape a user will actually type:
//
//	devx:v1                     → docker.io/library/devx      v1
//	library/devx:v1             → docker.io/library/devx      v1
//	docker.io/library/devx:v1   → docker.io/library/devx      v1
//	docker.io/devx:v1           → docker.io/library/devx      v1
//	index.docker.io/devx:v1     → docker.io/library/devx      v1
//	index.docker.io/acme/devx:v2 → docker.io/acme/devx        v2
//	devx                        → docker.io/library/devx      latest
//	ghcr.io/acme/devx:v2        → ghcr.io/acme/devx           v2
//	ghcr.io/devx:v2             → ghcr.io/devx                v2
//	localhost:5000/devx:v1      → localhost:5000/devx         v1
//	devx@sha256:e3b0c442…       → devx@sha256:e3b0c442…       (no tag)
//
// A first component is a REGISTRY when it contains a `.` or a `:`, or is
// exactly `localhost` — the same rule every OCI client applies, and the reason
// `library/devx` is a namespace while `ghcr.io/devx` is a registry. Guessing
// differently would make den's verdict disagree with what sbx actually pulls,
// which is worse than not checking at all: it would refuse a spawn over an
// image that is present.
//
// `docker.io/devx` (registry-qualified, exactly ONE component after the
// registry) still completes to the `library` namespace, same as the bare
// form: every OCI client resolves it that way, so leaving it as written would
// read `docker.io/devx` as missing against sbx's `docker.io/library/devx` and
// needlessly rebuild it — the false-refusal failure mode this file exists to
// prevent. `index.docker.io` is folded to `docker.io` first (see
// indexDockerIORegistry) so both spellings land on the one form sbx reports.
// No other registry gets this treatment: `ghcr.io/devx` has no `library`
// convention, so a single component there is a genuine one-component
// repository, not a namespace to complete.
func NormalizeImageRef(ref string) (repository, tag string) {
	repository, tag = ref, defaultTag

	// A DIGEST pin travels back as written, with NO tag, before anything else
	// looks at a colon. Its `:` sits after the last `/` — precisely the tag
	// split's trigger below — but it opens the digest's algorithm, so splitting
	// there would read `devx@sha256` plus the hex and compare that against an
	// inventory in which neither side has ever existed. An empty tag matches no
	// Template (their Ref() always carries one), which is the honest verdict
	// here: see IsDigestRef.
	if IsDigestRef(ref) {
		return ref, ""
	}

	// The tag separator is the LAST `:` that comes after the last `/`. Placed
	// before it, a colon belongs to a registry's port (`localhost:5000/devx`)
	// and is not a tag at all.
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		repository, tag = ref[:i], ref[i+1:]
		// An empty tag (`devx:`) is not "latest": it is a reference the user
		// mistyped, and completing it would silently match an image they did
		// not name. It travels back as written so the comparison fails and the
		// message quotes what they typed.
		if tag == "" {
			return ref, ""
		}
	}

	parts := strings.Split(repository, "/")
	switch {
	case len(parts) == 1:
		repository = defaultRegistry + "/" + defaultNamespace + "/" + repository
	case isRegistry(parts[0]):
		registry := parts[0]
		// index.docker.io names the exact same registry as docker.io to every
		// OCI client — folded here, unconditionally, because sbx's inventory
		// never spells it any other way (see indexDockerIORegistry). Without
		// this fold `index.docker.io/acme/devx` would keep a registry host sbx
		// never reports and could never match, silently.
		if registry == indexDockerIORegistry {
			registry = defaultRegistry
		}
		switch {
		case registry == defaultRegistry && len(parts) == 2:
			// Registry-qualified with exactly one component: still the
			// `library` namespace, same as the unqualified form above. Other
			// registries are NOT completed — `ghcr.io/devx` has no `library`
			// convention, so its one component is a genuine repository name,
			// not a namespace to insert.
			repository = registry + "/" + defaultNamespace + "/" + parts[1]
		default:
			// Already qualified beyond that: leave the components as written,
			// only folding the registry name settled above.
			repository = registry + "/" + strings.Join(parts[1:], "/")
		}
	default:
		repository = defaultRegistry + "/" + repository
	}
	return repository, tag
}

// IsDigestRef reports whether a reference pins an image by DIGEST
// (`devx@sha256:e3b0c442…`) rather than by tag.
//
// It exists because den cannot arbitrate such a reference AT ALL, and admitting
// that is the whole point: `sbx template ls --json` reports `repository` and
// `tag` and no digest whatsoever (schema surveyed 2026-07-31, spec §14.0), so
// no inventory entry can ever confirm or deny a digest pin. NormalizeImageRef
// therefore leaves it un-matchable, and the callers that must act on the fact
// ask HERE rather than re-deriving it from an empty tag — which a mistyped
// `devx:` also produces, for an entirely different reason.
//
// The two callers read the same fact and answer differently, on purpose:
//
//   - `den build <stack>`: an ancestor pinned by digest reads as absent and is
//     REBUILT (build.Plan, via Images.Has). Spending a build den could not
//     justify skipping is the cheap direction of that uncertainty.
//   - the spawn's checkStackImage: there, "absent" would be a FALSE REFUSAL on
//     an image that may well be present — the exact failure this file's
//     normalization exists to prevent — so it stays silent and lets `sbx
//     create` be the one to answer.
//
// The `@` is looked for anywhere in the reference rather than parsed further: a
// well-formed pin carries `@<algorithm>:<hex>`, and no registry, namespace or
// repository component may contain an `@` at all — so its mere presence already
// means "not a plain repository:tag", which is all any caller needs to know.
func IsDigestRef(ref string) bool { return strings.Contains(ref, "@") }

// isRegistry reports whether a reference's first component names a registry
// host rather than a namespace.
func isRegistry(component string) bool {
	return component == "localhost" ||
		strings.ContainsAny(component, ".:")
}

// FindTemplate returns the template matching this image reference, or nil.
//
// Both sides go through NormalizeImageRef, never a string comparison: sbx
// reports `docker.io/library/devx` + `v1` where the stack most often writes
// `devx:v1`, and comparing those raw would report every image as missing.
func FindTemplate(list []Template, image string) *Template {
	repo, tag := NormalizeImageRef(image)
	for i := range list {
		haveRepo, haveTag := NormalizeImageRef(list[i].Ref())
		if haveRepo == repo && haveTag == tag {
			return &list[i]
		}
	}
	return nil
}
