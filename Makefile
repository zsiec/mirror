.PHONY: build test clean run docker help fmt lint generate-certs deps dev setup docker-run docker-compose docker-compose-logs docker-compose-down docker-compose-restart docker-compose-monitoring docker-clean test-coverage ffmpeg-check ffmpeg-setup deps-check b r l

# Variables
BINARY_NAME=mirror
BUILD_DIR=./bin
GO_FILES=$(shell find . -name '*.go' -type f)
MAIN_PACKAGE=./cmd/mirror
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')

# FFmpeg Availability Check (for SRT integration testing)
FFMPEG_AVAILABLE := $(shell command -v ffmpeg >/dev/null 2>&1 && echo "yes" || echo "no")
ifeq ($(FFMPEG_AVAILABLE),yes)
    FFMPEG_STATUS := ✅ Available
    FFMPEG_SRT_SUPPORT := $(shell ffmpeg -protocols 2>/dev/null | grep -q "srt" && echo "yes" || echo "no")
    ifeq ($(FFMPEG_SRT_SUPPORT),yes)
        FFMPEG_SRT_STATUS := ✅ SRT support available
    else
        FFMPEG_SRT_STATUS := ⚠️ SRT support not compiled
    endif
else
    FFMPEG_STATUS := ❌ Not found
    FFMPEG_SRT_STATUS := ❌ Not available
endif

# Build flags
LDFLAGS=-ldflags "\
	-X github.com/zsiec/mirror/pkg/version.Version=$(VERSION) \
	-X github.com/zsiec/mirror/pkg/version.GitCommit=$(GIT_COMMIT) \
	-X github.com/zsiec/mirror/pkg/version.BuildTime=$(BUILD_TIME)"

# Default target
default: build

## help: Show this help message
help:
	@echo 'Mirror Video Streaming Platform - Build Commands'
	@echo ''
	@echo 'Quick Start:'
	@echo '  make setup          # Complete project setup'
	@echo '  make test           # Run all tests'
	@echo '  make docker-compose # Start with Docker'
	@echo ''
	@echo 'Comprehensive Testing:'
	@echo '  make test-full-integration  # Run complete system test with RTP/SRT streams'
	@echo ''
	@echo 'Available Commands:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'
	@echo ''
	@echo 'Dependencies:'
	@echo '  FFmpeg: $(FFMPEG_STATUS)'
	@echo '  FFmpeg SRT: $(FFMPEG_SRT_STATUS)'
	@echo ''
	@echo 'Dependency Management:'
	@echo '  make deps-check        # Check all dependencies'
	@echo '  make ffmpeg-setup      # Install FFmpeg'
ifeq ($(FFMPEG_AVAILABLE),no)
	@echo '⚠️  FFmpeg is required for SRT integration testing'
	@echo '   Install: brew install ffmpeg (macOS) or apt install ffmpeg (Ubuntu)'
endif

## build: Build the binary
build: deps-check
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PACKAGE)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

## test: Run unit tests
test:
	@echo "Running tests..."
	@go test -v -cover $(shell go list ./... | grep -v "mirror/tests")

## test-race: Run tests with race detector
test-race:
	@echo "Running tests with race detector..."
	@go test -v -race -cover $(shell go list ./... | grep -v "mirror/tests")

## test-integration: Run integration tests
test-integration:
	@echo "Running integration tests..."
	@go test -v -count=1 -race -tags=integration ./...

## test-full-integration: Run comprehensive full system integration test with Rich dashboard
test-full-integration:
	@echo "🚀 Running Full Mirror Integration Test with Rich Dashboard..."
	@echo "✨ Features beautiful terminal UI with real-time stats and progress tracking"
	@echo ""
	@echo "📋 Test validates:"
	@echo "   - Server startup and health checks"
	@echo "   - RTP stream ingestion and processing"
	@echo "   - SRT stream testing (real FFmpeg streams when available, simulated otherwise)"
	@echo "   - API endpoint functionality"
	@echo "   - Real-time metrics collection"
	@echo "   - Stream statistics and logging"
	@echo ""
	@echo "🎨 Rich Dashboard features:"
	@echo "   - Beautiful terminal interface with colors and animations"
	@echo "   - Real-time stream statistics and metrics"
	@echo "   - Live progress tracking and phase updates"
	@echo "   - Activity logs with syntax highlighting"
	@echo "   - Controls: Press 'q' to quit, 'r' to refresh"
	@echo ""
	@echo "🔍 Auto-detecting capabilities:"
	@echo "   - FFmpeg: $(FFMPEG_STATUS)"
	@echo "   - FFmpeg SRT: $(FFMPEG_SRT_STATUS)"
	@echo ""
	@FORCE_COLOR=1 CLICOLOR_FORCE=1 go test -v -timeout=10m -count=1 -run TestFullIntegrationTest ./tests/
	@echo ""
	@echo "✅ Full integration test complete!"



## test-all: Run all tests (unit + integration)
test-all: test test-integration

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

## docker-run: Run the Docker container with all ports
docker-run:
	@echo "Running Docker container..."
	@docker run -d \
		-p 8443:8443/udp \
		-p 9090:9090 \
		-p 30000:30000/udp \
		-p 5004:5004/udp \
		-v $$(pwd)/configs:/app/configs:ro \
		-v $$(pwd)/certs:/app/certs:ro \
		-v /tmp/mirror:/tmp/mirror \
		--name mirror \
		mirror:latest

## docker-compose: Run with docker-compose
docker-compose:
	@echo "Starting services with docker-compose..."
	@docker-compose -f docker/docker-compose.yml up -d

## docker-compose-logs: Show docker-compose logs
docker-compose-logs:
	@docker-compose -f docker/docker-compose.yml logs -f

## docker-compose-down: Stop docker-compose services
docker-compose-down:
	@echo "Stopping services..."
	@docker-compose -f docker/docker-compose.yml down

## docker-compose-restart: Restart docker-compose services
docker-compose-restart: docker-compose-down docker-compose

## docker-compose-monitoring: Run with monitoring stack (Prometheus + Grafana)
docker-compose-monitoring:
	@echo "Starting services with monitoring..."
	@docker-compose -f docker/docker-compose.yml --profile monitoring up -d

## docker-clean: Remove Docker containers and images
docker-clean:
	@echo "Cleaning Docker resources..."
	@docker-compose -f docker/docker-compose.yml down -v
	@docker rm -f mirror 2>/dev/null || true
	@docker rmi mirror:latest 2>/dev/null || true

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

## deps-check: Check all dependencies (FFmpeg)
deps-check: ffmpeg-check

## ffmpeg-setup: Install FFmpeg automatically
ffmpeg-setup:
	@echo "Installing FFmpeg..."
ifeq ($(shell uname),Darwin)
	@echo "Detected macOS - using Homebrew"
	@if ! command -v brew >/dev/null 2>&1; then \
		echo "❌ Homebrew not found. Install from: https://brew.sh/"; \
		exit 1; \
	fi
	@echo "Installing FFmpeg..."
	@brew install ffmpeg
	@echo "✅ FFmpeg installation complete"
else ifeq ($(shell test -f /etc/debian_version && echo debian),debian)
	@echo "Detected Debian/Ubuntu - using apt"
	@sudo apt-get update
	@sudo apt-get install -y ffmpeg libavcodec-dev libavformat-dev libavutil-dev libswscale-dev libavfilter-dev libavdevice-dev libswresample-dev
	@echo "✅ FFmpeg installation complete"
else
	@echo "❌ Automatic installation not supported for your OS"
	@echo "Please install FFmpeg manually:"
	@echo "  From source: https://ffmpeg.org/download.html"
	@echo "  Or check your package manager for 'ffmpeg' package"
	@exit 1
endif
	@echo ""
	@echo "Verifying installation..."
	@if command -v ffmpeg >/dev/null 2>&1; then \
		echo "✅ FFmpeg verification successful"; \
		echo "Version: $$(ffmpeg -version | head -1)"; \
	else \
		echo "❌ FFmpeg verification failed"; \
		exit 1; \
	fi

## setup: Initial project setup
setup: deps ffmpeg-setup generate-certs
	@echo "Setting up project..."
	@mkdir -p $(BUILD_DIR)
	@mkdir -p logs
	@cp .env.example .env 2>/dev/null || true
	@echo "Setup complete!"
	@echo ""
	@echo "Next steps:"
	@echo "  1. Start services: make docker-compose"
	@echo "  2. View logs: make docker-compose-logs"
	@echo "  3. Or run locally: make run (requires Redis)"

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

## ffmpeg-check: Check FFmpeg availability and SRT support
ffmpeg-check:
	@echo "FFmpeg Status: $(FFMPEG_STATUS)"
	@echo "FFmpeg SRT Support: $(FFMPEG_SRT_STATUS)"
ifeq ($(FFMPEG_AVAILABLE),no)
	@echo ""
	@echo "⚠️  FFmpeg is required for SRT integration testing"
	@echo "Install FFmpeg:"
	@echo "  macOS: brew install ffmpeg"
	@echo "  Ubuntu/Debian: sudo apt install ffmpeg"
	@echo "  Other: https://ffmpeg.org/download.html"
	@echo ""
	@exit 1
endif
ifeq ($(FFMPEG_SRT_SUPPORT),no)
	@echo ""
	@echo "⚠️  FFmpeg found but SRT support not compiled"
	@echo "Install FFmpeg with SRT support:"
	@echo "  macOS: brew install ffmpeg --with-srt (if available)"
	@echo "  Or compile from source with --enable-libsrt"
	@echo "  Check available protocols: ffmpeg -protocols | grep srt"
	@echo ""
	@exit 1
endif
	@echo "✅ FFmpeg with SRT support is available"

## version: Show version information
version:
	@$(BUILD_DIR)/$(BINARY_NAME) -version 2>/dev/null || echo "Please run 'make build' first"

# Short aliases for common commands
## b: Alias for 'docker' - Build Docker image
b: docker

## r: Alias for 'docker-compose' - Run with docker-compose
r: docker-compose

## l: Alias for 'docker-compose-logs' - Show docker-compose logs
l: docker-compose-logs
