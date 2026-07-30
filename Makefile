test:
	go test -count=1 ./...

typecheck:
	go build ./...

lint:
	go vet ./... && test -z "$$(gofmt -l .)"

.PHONY: test typecheck lint
