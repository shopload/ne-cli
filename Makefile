VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/shopload/ne-cli/internal/client.Version=$(VERSION)

.PHONY: build install test lint meta

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o ne-cli .

install:
	go install -trimpath -ldflags "$(LDFLAGS)" .

test:
	go vet ./...
	go test ./...

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

# Regenerate the embedded endpoint catalog from the official OpenAPI spec.
meta:
	python3 scripts/gen_meta.py
