GO ?= go

.PHONY: build run test fmt fmt-check vet api-check test-race check

build:
	mkdir -p bin
	$(GO) build -o bin/playground ./cmd/playground

run:
	$(GO) run ./cmd/playground

test:
	$(GO) test ./...

fmt:
	gofmt -w $$(find cmd internal api -name '*.go' -type f)

fmt-check:
	@files="$$(gofmt -l $$(find cmd internal api -name '*.go' -type f))"; if [ -n "$$files" ]; then printf '%s\n' "$$files"; exit 1; fi

vet:
	$(GO) vet ./...

api-check:
	$(GO) test ./api -count=1

test-race:
	$(GO) test -race ./...

check: fmt-check vet api-check test test-race build
