.PHONY: build test clean run docker help fmt lint generate-certs deps dev setup

# Variables
BINARY_NAME=mirror
BUILD_DIR=./bin
GO_FILES=$(shell find . -name '*.go' -type f)
MAIN_PACKAGE=./cmd/mirror
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')

# Build flags
LDFLAGS=-ldflags "\
	-X github.com/zsiec/mirror/pkg/version.Version=$(VERSION) \
	-X github.com/zsiec/mirror/pkg/version.GitCommit=$(GIT_COMMIT) \
	-X github.com/zsiec/mirror/pkg/version.BuildTime=$(BUILD_TIME)"

# Default target
default: build

## help: Show this help message
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'

## build: Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PACKAGE)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

## test: Run unit tests
test:
	@echo "Running tests..."
	@go test -v -race -cover ./...

## test-coverage: Run tests with coverage report
test-coverage:
	@echo "Running tests with coverage..."
	@go test -v -race -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

## clean: Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out coverage.html
	@echo "Clean complete"

## run: Build and run the application
run: build
	@echo "Running $(BINARY_NAME)..."
	@$(BUILD_DIR)/$(BINARY_NAME) -config configs/development.yaml

## dev: Run with hot reload (requires air)
dev:
	@if command -v air > /dev/null; then \
		air; \
	else \
		echo "Please install air: go install github.com/air-verse/air@latest"; \
		exit 1; \
	fi

## docker: Build Docker image
docker:
	@echo "Building Docker image..."
	@docker build -f docker/Dockerfile -t mirror:latest .

## docker-compose: Run with docker-compose
docker-compose:
	@echo "Starting services with docker-compose..."
	@docker-compose -f docker/docker-compose.yml up

## fmt: Format Go code
fmt:
	@echo "Formatting code..."
	@go fmt ./...
	@gofmt -s -w $(GO_FILES)

## lint: Run linters
lint:
	@echo "Running linters..."
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run; \
	else \
		echo "Please install golangci-lint: https://golangci-lint.run/usage/install/"; \
		exit 1; \
	fi

## vet: Run go vet
vet:
	@echo "Running go vet..."
	@go vet ./...

## generate-certs: Generate self-signed certificates for development
generate-certs:
	@echo "Generating self-signed certificates..."
	@mkdir -p certs
	@openssl req -x509 -newkey rsa:4096 -nodes \
		-keyout certs/key.pem \
		-out certs/cert.pem \
		-days 365 \
		-subj "/CN=localhost"
	@openssl req -x509 -newkey rsa:4096 -nodes \
		-keyout certs/dev-key.pem \
		-out certs/dev-cert.pem \
		-days 365 \
		-subj "/CN=localhost"
	@echo "Certificates generated in certs/"

## deps: Download dependencies
deps:
	@echo "Downloading dependencies..."
	@go mod download
	@go mod tidy

## setup: Initial project setup
setup: deps generate-certs
	@echo "Setting up project..."
	@mkdir -p $(BUILD_DIR)
	@mkdir -p logs
	@cp .env.example .env 2>/dev/null || true
	@echo "Setup complete!"
	@echo ""
	@echo "Next steps:"
	@echo "  1. Start Redis: docker run -d -p 6379:6379 redis:alpine"
	@echo "  2. Update .env with your configuration"
	@echo "  3. Run: make run"

## bench: Run benchmarks
bench:
	@echo "Running benchmarks..."
	@go test -bench=. -benchmem ./...

## check: Run all checks (fmt, vet, lint, test)
check: fmt vet lint test
	@echo "All checks passed!"

## install: Install the binary to GOPATH/bin
install: build
	@echo "Installing $(BINARY_NAME)..."
	@go install $(LDFLAGS) $(MAIN_PACKAGE)

## version: Show version information
version:
	@$(BUILD_DIR)/$(BINARY_NAME) -version 2>/dev/null || echo "Please run 'make build' first"