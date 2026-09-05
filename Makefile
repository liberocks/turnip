ifneq (,$(wildcard ./.env))
include .env
export
endif

BINARY := bin/turnip
BUILD_VERSION ?= $(if $(TURNIP_VERSION),$(TURNIP_VERSION),dev)
BUILD_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIMESTAMP ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(BUILD_VERSION) -X main.commit=$(BUILD_COMMIT) -X main.builtAt=$(BUILD_TIMESTAMP)

.PHONY: build clean demo deps dev format help lint test test-integration test-race token vuln

build: ## Build bin/turnip with version metadata
	mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/turnip

dev: ## Run Turnip from source
	go run ./cmd/turnip

deps: ## Download and verify modules
	go mod download
	go mod verify

format: ## Format Go source
	gofmt -s -w cmd internal

test: ## Run unit tests
	go test -count=1 ./...

test-integration: ## Run Redis-backed integration tests
	@test -n "$$TURNIP_TEST_REDIS_URL" || (echo "TURNIP_TEST_REDIS_URL is required"; exit 1)
	go test -count=1 -run TestTwoInstance -v ./internal/turnip

test-race: ## Run all available tests with the race detector
	go test -race -count=1 ./...

lint: ## Run golangci-lint
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...

vuln: ## Scan reachable code for known vulnerabilities
	go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

demo: ## Start the two-instance Docker Compose demo
	docker compose up --build

token: ## Generate a development JWT (SUBJECT and ROOM required)
	@test -n "$(SUBJECT)" -a -n "$(ROOM)" || (echo "usage: make token SUBJECT=alice ROOM=demo [NAME=Alice]"; exit 1)
	go run ./cmd/turnip-token -subject "$(SUBJECT)" -room "$(ROOM)" -name "$(NAME)"

clean: ## Remove local build artifacts
	rm -rf bin tmp

help: ## Show Make targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "%-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
