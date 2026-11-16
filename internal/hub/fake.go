package hub

import "context"

// Fake is the test double of Catalog; it lives in a production file for the
// same reason sbx.Fake does: cli and template tests both need it.
type Fake struct {
	Names []string
	Err   error
}

func (f Fake) Tags(context.Context) ([]string, error) { return f.Names, f.Err }
