package build

import (
	"context"
	"fmt"
	"io"
	"os"
)

// Execute runs the plan, in order.
//
// EVERY script is checked to exist BEFORE the first one runs. That ordering is
// the same one internal/spawn states at length: anything rejectable up front is
// rejected before the first side effect. Here the side effect is expensive
// rather than messy — `den build dgdevx` that built its base image for four
// minutes and only then discovered dgdevx has no build.sh would have burned
// that time to reach a refusal den could have made instantly.
//
// A step whose script is missing is a refusal, not a skip: a stack that
// declares an `image:` and cannot build it is a configuration den must not
// paper over (spec §2).
func Execute(ctx context.Context, steps []Step, script Script, out io.Writer) error {
	for _, s := range steps {
		if !s.Build {
			continue
		}
		path := ScriptPath(s.Stack)
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf(
				"stack %q: build script not found: %s — create it (den runs it unchanged, "+
					"it is what produces image %s)",
				s.Stack.Name, path, s.Stack.Image)
		}
	}

	for i, s := range steps {
		if !s.Build {
			// Announced, never silent: `den build dgdevx` that printed one line
			// would leave "devx was already built" indistinguishable from "den
			// forgot devx". The remedy is named because skipping is exactly what
			// a user re-running a build wants to override.
			fmt.Fprintf(out, "[%d/%d] %s: skipped, %s (rebuild it with --force)\n",
				i+1, len(steps), s.Stack.Name, s.Skipped)
			continue
		}
		fmt.Fprintf(out, "[%d/%d] %s: building %s...\n", i+1, len(steps), s.Stack.Name, s.Stack.Image)
		if err := script.Run(ctx, s.Stack, out); err != nil {
			// The step is named because the script's own output is already on
			// the same stream, several screens up: without this line the user
			// sees a wall of build log and an exit code, and has to count the
			// stages to learn which stack died.
			return fmt.Errorf("stack %q: %s failed: %w", s.Stack.Name, ScriptPath(s.Stack), err)
		}
	}
	return nil
}
