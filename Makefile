# `-X ...cli.Version` is what makes a binary say which code it runs; without it
# `den version` answers `dev` on a user's machine, which names nothing. The value
# comes from `git describe`, so a tagged build says `v1.0.0` and every other one
# says where it sits relative to the last tag (`v1.0.0-3-gabc1234-dirty`) — the
# release case and the working-tree case, from one expression.
#
# `$$(...)` and not `$(...)`: make expands `$(...)` itself, finds no variable of
# that name, and would substitute the empty string — shipping `Version=` silently.
# The `|| echo dev` covers the two ways describe legitimately fails (no git, or a
# tarball with no .git); an unreadable version is a worse answer than `dev`.
build:
	go build -ldflags "-X github.com/PillowPillow/den/internal/cli.Version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o den ./cmd/den

test:
	go test -count=1 ./...

typecheck:
	go build ./...

lint:
	go vet ./... && test -z "$$(gofmt -l .)"

.PHONY: build test typecheck lint
