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
// Plan already turns a missing script into a skip (or, for the stack the user
// named, into a refusal), so no plan Plan produces can reach the loop below.
// The check stays as a GUARD, in the shape agent.RenderMixin's freshness guard
// takes: Steps is a bare exported struct, and a hand-built plan must not reach
// half the chain before discovering a script that was never there.
func Execute(ctx context.Context, steps []Step, script Script, out io.Writer) error {
	for _, s := range steps {
		if !s.Build {
			continue
		}
		path := ScriptPath(s.Stack)
		info, err := os.Stat(path)
		if err != nil {
			return missingScriptError(s.Stack)
		}
		// The EXECUTABLE BIT, not just existence. den runs the script directly
		// (no `sh <path>`, so the shebang stays the script's own choice), which
		// means a build.sh committed without +x dies on `fork/exec: permission
		// denied` — measured on the bench, 2026-08-03, and measured LATE: the
		// chain had already built the stacks before it. Checking it here puts
		// that failure where every other one already is, before the first build.
		//
		// The 0o111 test PRESUMES a POSIX filesystem: os.Stat never reports
		// those bits on Windows, where this would therefore refuse every
		// build.sh — which is not a case den has, since it only runs where sbx
		// does, on darwin and linux.
		if info.Mode()&0o111 == 0 {
			return fmt.Errorf(
				"stack %q: build script not executable: %s — `chmod +x %s` (den runs it directly, "+
					"so its shebang stays its own choice)",
				s.Stack.Name, path, path)
		}
	}

	for i, s := range steps {
		if !s.Build {
			// Announced, never silent: `den build dgdevx` that printed one line
			// would leave "devx was already built" indistinguishable from "den
			// forgot devx". The reason carries its own remedy — see Step.Skipped:
			// --force rebuilds an image that is already there, and does nothing
			// at all for a stack with no build.sh.
			fmt.Fprintf(out, "[%d/%d] %s: skipped, %s\n",
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
