package sbx

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// CheckEnvFile answers ONE question: did den write this file, and does this den
// still understand it?
//
// It is what `den rm` consults before handing the path to `sbx env rm`, which
// resolves the sandbox FROM the file set it is passed (§14.4). A file den
// cannot vouch for would therefore resolve to a sandbox den cannot predict —
// possibly another one — so a refusal here is the honest answer and `--force`
// is the documented way past it (spec §5.8).
//
// STRICT, like every other decode in den (spec §12): an unknown key is a load
// error, never a silence. The most common such file is a NEWER den's — good
// YAML, refused on a field this version does not know — and that is exactly the
// case where guessing is worst.
//
// It never repairs, never rewrites, and never deletes: den does not delete a
// file it could not read (spec §11), and this function is the reader that
// establishes it.
func CheckEnvFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
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
	return nil
}
