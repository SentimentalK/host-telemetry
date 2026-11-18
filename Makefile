GO ?= go

.PHONY: generate test lint print

generate:
	GOFLAGS=-mod=vendor $(GO) run ./internal/schema/gen

test:
	$(GO) test ./...

lint:
	test -z "$$(gofmt -l . | grep -v '^vendor/')" && $(GO) vet ./... && ./tools/no-literal-metric-names.sh

print:
	$(GO) run ./cmd/host-telemetry print
