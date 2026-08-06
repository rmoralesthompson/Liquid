# Liquid — developer tasks.
#
# The example targets run an app under `liquid dev` (watch + rebuild + reload)
# on http://localhost:8080. Run one at a time — they share the port.

.PHONY: help dashboard fantasy build test fmt lint

# Default: list the targets.
help:
	@echo "Liquid make targets:"
	@echo "  make dashboard   run the dashboard example (http://localhost:8080)"
	@echo "  make fantasy     run the fantasy-football example (http://localhost:8080)"
	@echo "  make build       go build ./..."
	@echo "  make test        go test -race ./..."
	@echo "  make fmt         go fmt ./..."
	@echo "  make lint        golangci-lint run"

# Run the dashboard example with the dev loop (watch + rebuild + reload).
dashboard:
	go run ./cmd/liquid dev examples/dashboard

# Run the fantasy-football example with the dev loop.
fantasy:
	go run ./cmd/liquid dev examples/fantasy

build:
	go build ./...

test:
	go test -race ./...

fmt:
	go fmt ./...

lint:
	golangci-lint run --config .golangci-lint.yml
