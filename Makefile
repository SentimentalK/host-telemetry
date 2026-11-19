GO ?= go
HT_STATE ?= $(HOME)/.local/state/host-telemetry

export HT_STATE

.PHONY: generate test lint print run budget stack-up stack-down stack-status stack-grafana ship-events ask

generate:
	GOFLAGS=-mod=vendor $(GO) run ./internal/schema/gen

test:
	$(GO) test ./...

lint:
	test -z "$$(gofmt -l . | grep -v '^vendor/')" && $(GO) vet ./... && ./tools/no-literal-metric-names.sh

budget:
	$(GO) run ./cmd/budget --fixtures testdata --platform linux $(BUDGET_ARGS)

print:
	$(GO) run ./cmd/host-telemetry print

run:
	$(GO) run ./cmd/host-telemetry run --fixtures testdata --platform linux

stack-up:
	./tools/stack.sh start

stack-down:
	./tools/stack.sh stop

stack-status:
	./tools/stack.sh status

stack-grafana:
	./tools/stack.sh grafana

ship-events:
	./tools/ship_events.sh

ask:
	./tools/ask.sh "$(Q)"
