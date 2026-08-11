# Project parameters
BINARY_NAME=push2vault
BUILD_DIR=bin
MODULE=github.com/dx-zone/push2vault

.PHONY: all build test clean lint release-builds help

all: test build

## build: Builds the binary for current OS/Arch
build:
	@echo "==> Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME) main.go

## test: Runs unit tests with race detection and coverage
test:
	@echo "==> Running tests..."
	go test -v -race -cover ./...

## lint: Runs golangci-lint (if installed) or go vet
lint:
	@echo "==> Running go vet..."
	go vet ./...

## release-builds: Cross-compiles binaries for Linux (amd64/arm64) and macOS
release-builds: clean
	@echo "==> Building cross-platform release binaries..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 main.go
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 main.go
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 main.go
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 main.go

## clean: Cleans build artifacts
clean:
	@echo "==> Cleaning build directory..."
	rm -rf $(BUILD_DIR)

## help: Shows available targets
help:
	@echo "Usage:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
