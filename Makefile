ifneq (,$(wildcard ./.env))
    include .env
    export
endif

BINARY=turnip-signaling
.PHONY: build format dev help test clean docker docker-run

dev:
	air -c .air.toml

build:
	@echo "Building the binary..."
	@go build -o $(BINARY) ./src
	@if [ -f $(BINARY) ]; then \
		echo "Build successful: $(BINARY) created."; \
	else \
		echo "Build failed."; \
		exit 1; \
	fi

format:
	@echo "Formatting the code..."
	@go fmt ./...
	@go mod tidy
	@gofmt -s -w .
	golangci-lint run ./src/...
	@echo "Code formatted successfully!"

run: ## Run the application
	go run ./src

clean: ## Clean build artifacts
	rm -f $(BINARY)
	rm -rf tmp/
	go clean

test: ## Run tests
	go test -v ./src/...
