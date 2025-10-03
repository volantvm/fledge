.PHONY: build test fmt vet lint clean install ci help

# Build variables
BINARY_NAME=fledge
VERSION?=dev
BUILD_DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION) -X main.buildDate=$(BUILD_DATE) -X main.gitCommit=$(GIT_COMMIT)"

# Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/fledge
	@echo "Build complete: ./$(BINARY_NAME)"

# Run tests
test:
	@echo "Running tests..."
	go test -v -race -coverprofile=coverage.txt -covermode=atomic ./...
	@echo "Tests complete"

# Run tests with coverage report
coverage: test
	@echo "Generating coverage report..."
	go tool cover -html=coverage.txt -o coverage.html
	@echo "Coverage report: coverage.html"

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...
	@echo "Format complete"

# Vet code
vet:
	@echo "Running go vet..."
	go vet ./...
	@echo "Vet complete"

# Lint code (requires golangci-lint)
lint:
	@echo "Running linters..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed, skipping..."; \
	fi

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f $(BINARY_NAME)
	rm -f coverage.txt coverage.html
	rm -rf dist/
	@echo "Clean complete"

# Install binary to $GOPATH/bin
install:
	@echo "Installing $(BINARY_NAME)..."
	go install $(LDFLAGS) ./cmd/fledge
	@echo "Installed to $(shell go env GOPATH)/bin/$(BINARY_NAME)"

# Run CI checks (format, vet, test)
ci: fmt vet test
	@echo "All CI checks passed"

# Show help
help:
	@echo "Fledge Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make build      Build the fledge binary"
	@echo "  make test       Run tests"
	@echo "  make coverage   Run tests with coverage report"
	@echo "  make fmt        Format code with go fmt"
	@echo "  make vet        Run go vet"
	@echo "  make lint       Run golangci-lint (if installed)"
	@echo "  make clean      Remove build artifacts"
	@echo "  make install    Install binary to GOPATH/bin"
	@echo "  make ci         Run all CI checks (fmt, vet, test)"
	@echo "  make help       Show this help message"
