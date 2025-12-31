# Makefile for tmux-cli
# TDD-focused Go project

.PHONY: help build test install clean coverage lint fmt vet verify-real

# Default target
.DEFAULT_GOAL := help

# Binary name
BINARY_NAME=tmux-cli
BINARY_PATH=./bin/$(BINARY_NAME)

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOCLEAN=$(GOCMD) clean
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt
GOVET=$(GOCMD) vet

# Installation path
INSTALL_PATH=$(HOME)/.local/bin

# Build flags
LDFLAGS=-ldflags "-s -w"

## help: Display this help message
help:
	@echo "Available targets:"
	@echo "  make build        - Build the tmux-cli binary"
	@echo "  make test         - Run unit tests quickly (no external deps)"
	@echo "  make test-tmux    - Run tmux-specific tests (requires tmux 2.0+)"
	@echo "  make test-mcp     - Run all MCP tests (unit + integration)"
	@echo "  make verify-mcp   - Run E2E verification of MCP concurrent servers"
	@echo "  make test-all     - Run all tests (unit + tmux + integration + MCP)"
	@echo "  make verify-real  - Build + E2E verification with real tmux (RECOMMENDED)"
	@echo "  make install      - Install binary to ~/.local/bin"
	@echo "  make clean        - Remove built binaries and test cache"
	@echo "  make coverage     - Run tests with coverage report"
	@echo "  make lint         - Run linters (fmt, vet)"
	@echo "  make fmt          - Format code"
	@echo "  make vet          - Run go vet"

tmux-kill-server:
	tmux kill-server

start:
	tmux-cli start

kill:
	tmux-cli kill

refresh: kill install start

## build: Build the complete project and generate runnable tmux-cli binary
build: fmt vet
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	$(GOBUILD) $(LDFLAGS) -o $(BINARY_PATH) ./cmd/tmux-cli
	@echo "✓ Build complete: $(BINARY_PATH)"

## test: Run all tests quickly (unit tests only)
test:
	@echo "Running tests..."
	$(GOTEST) -v -short -race ./...
	@echo "✓ All tests passed"

## test-coverage: Run tests with coverage
coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -v -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report generated: coverage.html"

## test-tmux: Run tmux-specific tests (requires tmux 2.0+)
test-tmux:
	@echo "Running tmux tests (requires tmux)..."
	$(GOTEST) -v -race -tags=tmux ./...
	@echo "✓ Tmux tests passed"

## test-mcp: Run all MCP tests (unit + integration)
test-mcp:
	@echo "Running MCP unit tests..."
	$(GOTEST) -v ./internal/mcp/...
	@echo ""
	@echo "Running MCP integration tests..."
	$(GOTEST) -tags=integration -v ./internal/mcp/...
	@echo ""
	@echo "✓ All MCP tests passed"

## verify-mcp: Run E2E verification of MCP concurrent servers
verify-mcp:
	@echo "Running MCP E2E verification script..."
	@./scripts/verify-mcp-execution.sh

## test-all: Run all tests (unit + tmux + integration + MCP)
test-all: test test-mcp
	@echo "Running all integration tests..."
	$(GOTEST) -v -race -tags=tmux,integration ./...
	@echo "✓ All test suites passed"

## test-integration: Run integration tests (requires tmux)
test-integration:
	@echo "Running integration tests..."
	$(GOTEST) -v -race -tags=integration ./...
	@echo "✓ Integration tests passed"

## install: Install app as local runnable app to user profile
install: build
	@echo "Installing $(BINARY_NAME) to $(INSTALL_PATH)..."
	@mkdir -p $(INSTALL_PATH)
	@cp $(BINARY_PATH) $(INSTALL_PATH)/$(BINARY_NAME)
	@chmod +x $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "✓ Installed to $(INSTALL_PATH)/$(BINARY_NAME)"
	@echo ""
	@echo "Make sure $(INSTALL_PATH) is in your PATH:"
	@echo "  export PATH=\"\$$PATH:$(INSTALL_PATH)\""

## clean: Remove built binaries and test cache
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	@rm -rf bin/
	@rm -f coverage.out coverage.html
	@echo "✓ Clean complete"

## fmt: Format all Go files
fmt:
	@echo "Formatting code..."
	$(GOFMT) ./...

## vet: Run go vet
vet:
	@echo "Running go vet..."
	$(GOVET) ./...

## lint: Run all linters
lint: fmt vet
	@echo "✓ Linting complete"

## deps: Download and tidy dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy
	@echo "✓ Dependencies updated"

## run: Build and run the application
run: build
	@echo "Running $(BINARY_NAME)..."
	$(BINARY_PATH)

## watch-test: Watch and run tests on file changes (requires entr)
watch-test:
	@echo "Watching for changes... (Ctrl+C to stop)"
	@find . -name "*.go" | entr -c make test

## verify-real: Build and verify with real tmux commands (E2E test)
verify-real: build
	@echo ""
	@echo "================================================"
	@echo "Real Execution Verification (E2E)"
	@echo "This verifies tmux-cli state matches tmux reality"
	@echo "================================================"
	@./scripts/verify-real-execution.sh
