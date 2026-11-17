package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/PillowPillow/den/v2/internal/hub"
	"github.com/PillowPillow/den/v2/internal/template"
)

// explainNoTemplate appends the official images to a missing-template error: the remedy is to name
// one, and the user should not have to leave the terminal to find which exist.
func explainNoTemplate(ctx context.Context, deps Deps, err error) error {
	var nt *template.NoTemplateError
	if !errors.As(err, &nt) {
		return err
	}
	return fmt.Errorf("%w\n%s", err, hub.Describe(ctx, deps.Hub))
}
