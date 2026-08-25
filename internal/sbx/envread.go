package sbx

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// CheckEnvFile answers ONE question: did den write this file FOR sandbox, and
// does this den still understand it?
//
// It is what `den rm` consults before handing the path to `sbx env rm`, which
// resolves the sandbox FROM the file set it is passed (§14.4). A file den
// cannot vouch for would therefore resolve to a sandbox den cannot predict —
// possibly another one — so a refusal here is the honest answer and `--force`
// is the documented way past it (spec §5.8).
//
// sandbox is checked against the file's own `name:`, not merely assumed from
// the path it was read at (fix round 1, finding 2): `state/sandboxes/<X>/`
// only NAMES sandbox X, it does not GUARANTEE the file inside it describes X.
// `sbx env rm` resolves the sandbox it destroys FROM the file's content, not
// from the path den handed it — so a file at api/.sbxenv.yaml carrying
// `name: web` would pass a check that stopped at "is this valid YAML den
// wrote", and den would destroy web while the user typed `den rm api`. That
// mismatch means one thing: den did not write this file for THIS sandbox,
// which is exactly the question this function exists to answer.
//
// STRICT, like every other decode in den (spec §12): an unknown key is a load
// error, never a silence. The most common such file is a NEWER den's — good
// YAML, refused on a field this version does not know — and that is exactly the
// case where guessing is worst.
//
// It never repairs, never rewrites, and never deletes: den does not delete a
// file it could not read (spec §11), and this function is the reader that
// establishes it.
func CheckEnvFile(path, sandbox string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		// Wrapped, not swallowed: `errors.Is(returnedErr, fs.ErrNotExist)` must
		// still see through this to the real cause. `den rm` (fix round 1,
		// finding 1) reads that distinction to tell an ABSENT record — a
		// sandbox that predates the emitter — from an UNREADABLE one, and the
		// two read as different surprises to the user even though the remedy
		// is identical.
		return fmt.Errorf("reading %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(content))
	dec.KnownFields(true)
	var doc envDoc
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if doc.SchemaVersion != EnvSchemaVersion {
		return fmt.Errorf(
			"reading %s: schemaVersion %q, but this den only emits %q — the file was written by "+
				"another version of den", path, doc.SchemaVersion, EnvSchemaVersion)
	}
	if doc.Name == "" {
		return fmt.Errorf(
			"reading %s: no `name:` — den cannot tell which sandbox this file resolves to", path)
	}
	if doc.Name != sandbox {
		return fmt.Errorf(
			"reading %s: `name:` is %q, but den looked for %q — this file does not describe the "+
				"sandbox den is about to hand it to `sbx env rm` for", path, doc.Name, sandbox)
	}
	return nil
}
