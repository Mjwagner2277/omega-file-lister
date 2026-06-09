GO ?= go
GOFILES := $(shell find . -name '*.go' -not -path './.git/*' -not -path './.cache/*')

.PHONY: fmt fmt-check lint test check

fmt:
	gofmt -w $(GOFILES)

fmt-check:
	@test -z "$$(gofmt -l $(GOFILES))" || (echo "gofmt needed:"; gofmt -l $(GOFILES); exit 1)

lint:
	$(GO) vet ./...

test:
	$(GO) test ./...

check: fmt-check lint test
