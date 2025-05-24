# Mirror Streaming Platform - Part 7: Development Setup and CI/CD

## Table of Contents
1. [Local Development Environment](#local-development-environment)
2. [Development Scripts](#development-scripts)
3. [GitHub Actions CI/CD](#github-actions-cicd)
4. [Testing Setup](#testing-setup)
5. [Debugging and Troubleshooting](#debugging-and-troubleshooting)
6. [Release Process](#release-process)

## Local Development Environment

### Prerequisites Installation Script
```bash
#!/bin/bash
# scripts/setup-dev.sh

set -e

echo "Mirror Streaming Platform - Development Setup"
echo "============================================"

# Detect OS
OS="$(uname -s)"
ARCH="$(uname -m)"

# Install Go
install_go() {
    GO_VERSION="1.21.5"
    
    if command -v go &> /dev/null; then
        CURRENT_GO=$(go version | awk '{print $3}' | sed 's/go//')
        echo "Go $CURRENT_GO is already installed"
    else
        echo "Installing Go $GO_VERSION..."
        
        case "$OS" in
            Darwin)
                brew install go
                ;;
            Linux)
                wget "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz"
                sudo rm -rf /usr/local/go
                sudo tar -C /usr/local -xzf "go${GO_VERSION}.linux-${ARCH}.tar.gz"
                rm "go${GO_VERSION}.linux-${ARCH}.tar.gz"
                echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
                ;;
        esac
    fi
}

# Install Docker
install_docker() {
    if command -v docker &> /dev/null; then
        echo "Docker $(docker --version) is already installed"
    else
        echo "Installing Docker..."
        
        case "$OS" in
            Darwin)
                echo "Please install Docker Desktop from https://www.docker.com/products/docker-desktop"
                ;;
            Linux)
                curl -fsSL https://get.docker.com -o get-docker.sh
                sh get-docker.sh
                sudo usermod -aG docker $USER
                rm get-docker.sh
                ;;
        esac
    fi
}

# Install FFmpeg
install_ffmpeg() {
    if command -v ffmpeg &> /dev/null; then
        echo "FFmpeg $(ffmpeg -version | head -n1) is already installed"
    else
        echo "Installing FFmpeg..."
        
        case "$OS" in
            Darwin)
                brew install ffmpeg
                ;;
            Linux)
                sudo apt-get update
                sudo apt-get install -y ffmpeg
                ;;
        esac
    fi
}

# Install development tools
install_dev_tools() {
    echo "Installing development tools..."
    
    # Install Go tools
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
    go install github.com/cosmtrek/air@latest
    go install github.com/go-delve/delve/cmd/dlv@latest
    go install golang.org/x/tools/cmd/goimports@latest
    go install github.com/rakyll/gotest@latest
    
    # Install other tools
    case "$OS" in
        Darwin)
            brew install redis terraform jq yq
            ;;
        Linux)
            sudo apt-get install -y redis-server jq
            # Install terraform
            wget -O- https://apt.releases.hashicorp.com/gpg | gpg --dearmor | sudo tee /usr/share/keyrings/hashicorp-archive-keyring.gpg
            echo "deb [signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main" | sudo tee /etc/apt/sources.list.d/hashicorp.list
            sudo apt update && sudo apt install terraform
            ;;
    esac
}

# Setup project
setup_project() {
    echo "Setting up project..."
    
    # Install Go dependencies
    go mod download
    
    # Create necessary directories
    mkdir -p tmp/cache tmp/logs web/admin/js web/admin/css
    
    # Copy example configs
    if [ ! -f configs/mirror.local.yaml ]; then
        cp configs/mirror.yaml configs/mirror.local.yaml
        echo "Created configs/mirror.local.yaml - please update with your local settings"
    fi
    
    # Generate TLS certificates for local development
    if [ ! -f tmp/cert.pem ]; then
        echo "Generating self-signed certificates for local development..."
        openssl req -x509 -newkey rsa:4096 -keyout tmp/key.pem -out tmp/cert.pem \
            -days 365 -nodes -subj "/CN=localhost"
    fi
}

# Main installation flow
main() {
    install_go
    install_docker
    install_ffmpeg
    install_dev_tools
    setup_project
    
    echo ""
    echo "✅ Development environment setup complete!"
    echo ""
    echo "Next steps:"
    echo "1. Update configs/mirror.local.yaml with your settings"
    echo "2. Run 'make dev' to start development server"
    echo "3. Run 'make test' to run tests"
    echo ""
    
    if [ "$OS" = "Linux" ]; then
        echo "⚠️  You may need to log out and back in for Docker group changes to take effect"
    fi
}

main
```

### Makefile
```makefile
# Mirror Streaming Platform Makefile

.PHONY: help
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Variables
BINARY_NAME := mirror
BUILD_DIR := ./build
GO_FILES := $(shell find . -name '*.go' -type f)
VERSION := $(shell git describe --tags --always --dirty)
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT := $(shell git rev-parse HEAD)
LDFLAGS := -ldflags "-w -s -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)"

# Development
.PHONY: dev
dev: ## Run development server with hot reload
	CONFIG_FILE=configs/mirror.local.yaml air

.PHONY: dev-deps
dev-deps: ## Start development dependencies (Redis, etc.)
	docker-compose -f deployments/docker/docker-compose.dev.yml up -d

.PHONY: dev-down
dev-down: ## Stop development dependencies
	docker-compose -f deployments/docker/docker-compose.dev.yml down

# Building
.PHONY: build
build: ## Build the binary
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/mirror

.PHONY: build-linux
build-linux: ## Build for Linux
	@echo "Building $(BINARY_NAME) for Linux..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/mirror

.PHONY: build-docker
build-docker: ## Build Docker image
	@echo "Building Docker image..."
	@docker build -f deployments/docker/Dockerfile -t mirror:latest .

# Testing
.PHONY: test
test: ## Run tests
	@echo "Running tests..."
	@gotest -v -race -coverprofile=coverage.out ./...

.PHONY: test-coverage
test-coverage: test ## Run tests with coverage report
	@echo "Generating coverage report..."
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

.PHONY: test-integration
test-integration: ## Run integration tests
	@echo "Running integration tests..."
	@go test -v -tags=integration ./tests/integration/...

.PHONY: bench
bench: ## Run benchmarks
	@echo "Running benchmarks..."
	@go test -v -bench=. -benchmem ./...

# Code quality
.PHONY: lint
lint: ## Run linter
	@echo "Running linter..."
	@golangci-lint run ./...

.PHONY: fmt
fmt: ## Format code
	@echo "Formatting code..."
	@goimports -w .
	@go fmt ./...

.PHONY: vet
vet: ## Run go vet
	@echo "Running go vet..."
	@go vet ./...

# Utilities
.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out coverage.html

.PHONY: deps
deps: ## Download dependencies
	@echo "Downloading dependencies..."
	@go mod download
	@go mod tidy

.PHONY: generate
generate: ## Run go generate
	@echo "Running go generate..."
	@go generate ./...

# Deployment
.PHONY: deploy-dev
deploy-dev: build-docker ## Deploy to development environment
	@echo "Deploying to development..."
	@./scripts/deploy.sh dev

.PHONY: deploy-prod
deploy-prod: build-docker ## Deploy to production environment
	@echo "Deploying to production..."
	@./scripts/deploy.sh prod

# Streaming tests
.PHONY: stream-test-srt
stream-test-srt: ## Test SRT streaming
	@echo "Starting SRT test stream..."
	@ffmpeg -re -f lavfi -i testsrc=size=1920x1080:rate=25 \
		-f lavfi -i sine=frequency=1000:sample_rate=48000 \
		-c:v libx265 -preset ultrafast -b:v 5M \
		-c:a aac -b:a 128k \
		-f mpegts "srt://localhost:6000?streamid=test-stream-1"

.PHONY: stream-test-rtp
stream-test-rtp: ## Test RTP streaming
	@echo "Starting RTP test stream..."
	@ffmpeg -re -f lavfi -i testsrc=size=1920x1080:rate=25 \
		-c:v libx265 -preset ultrafast -b:v 5M \
		-f rtp rtp://localhost:5004

.PHONY: play-test
play-test: ## Play test stream
	@echo "Playing test stream..."
	@open "http://localhost:8080/admin/"
```

### .air.toml
```toml
# Air configuration for hot reload during development

root = "."
testdata_dir = "testdata"
tmp_dir = "tmp"

[build]
  args_bin = []
  bin = "./tmp/main"
  cmd = "go build -o ./tmp/main ./cmd/mirror"
  delay = 1000
  exclude_dir = ["assets", "tmp", "vendor", "testdata", "web", "deployments"]
  exclude_file = []
  exclude_regex = ["_test.go"]
  exclude_unchanged = false
  follow_symlink = false
  full_bin = "./tmp/main -c configs/mirror.local.yaml"
  include_dir = []
  include_ext = ["go", "tpl", "tmpl", "html"]
  kill_delay = "0s"
  log = "build-errors.log"
  send_interrupt = true
  stop_on_error = true

[color]
  app = ""
  build = "yellow"
  main = "magenta"
  runner = "green"
  watcher = "cyan"

[log]
  time = false

[misc]
  clean_on_exit = false

[screen]
  clear_on_rebuild = false
```

### docker-compose.dev.yml
```yaml
version: '3.8'

services:
  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    volumes:
      - redis-dev-data:/data
    command: redis-server --appendonly yes

  minio:
    image: minio/minio:latest
    ports:
      - "9000:9000"
      - "9001:9001"
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    volumes:
      - minio-dev-data:/data
    command: server /data --console-address ":9001"

  createbuckets:
    image: minio/mc:latest
    depends_on:
      - minio
    entrypoint: >
      /bin/sh -c "
      /usr/bin/mc config host add myminio http://minio:9000 minioadmin minioadmin;
      /usr/bin/mc mb myminio/mirror-streams || true;
      /usr/bin/mc anonymous set public myminio/mirror-streams;
      exit 0;
      "

  prometheus:
    image: prom/prometheus:latest
    ports:
      - "9090:9090"
    volumes:
      - ./configs/prometheus.dev.yml:/etc/prometheus/prometheus.yml
      - prometheus-dev-data:/prometheus
    command:
      - '--config.file=/etc/prometheus/prometheus.yml'
      - '--storage.tsdb.path=/prometheus'

  grafana:
    image: grafana/grafana:latest
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
      - GF_USERS_ALLOW_SIGN_UP=false
    volumes:
      - grafana-dev-data:/var/lib/grafana

volumes:
  redis-dev-data:
  minio-dev-data:
  prometheus-dev-data:
  grafana-dev-data:
```

## Development Scripts

### scripts/test-stream.sh
```bash
#!/bin/bash
# Test streaming script with multiple sources

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
MIRROR_HOST="${MIRROR_HOST:-localhost}"
SRT_PORT="${SRT_PORT:-6000}"
RTP_PORT="${RTP_PORT:-5004}"
ADMIN_URL="${ADMIN_URL:-http://localhost:8080}"

# Function to check if service is running
check_service() {
    if curl -s "${ADMIN_URL}/health" > /dev/null; then
        echo -e "${GREEN}✓ Mirror service is running${NC}"
        return 0
    else
        echo -e "${RED}✗ Mirror service is not running${NC}"
        echo "Please start the service with 'make dev' first"
        return 1
    fi
}

# Function to generate test stream
generate_stream() {
    local protocol=$1
    local stream_id=$2
    local duration=${3:-0}
    
    echo -e "${YELLOW}Starting $protocol test stream: $stream_id${NC}"
    
    case $protocol in
        srt)
            if [ $duration -eq 0 ]; then
                ffmpeg -re -f lavfi -i testsrc2=size=1920x1080:rate=25 \
                    -f lavfi -i sine=frequency=1000:sample_rate=48000 \
                    -c:v libx265 -preset ultrafast -b:v 5M \
                    -c:a aac -b:a 128k \
                    -f mpegts "srt://${MIRROR_HOST}:${SRT_PORT}?streamid=${stream_id}" \
                    2>/dev/null &
            else
                ffmpeg -re -f lavfi -i testsrc2=size=1920x1080:rate=25 \
                    -f lavfi -i sine=frequency=1000:sample_rate=48000 \
                    -t $duration \
                    -c:v libx265 -preset ultrafast -b:v 5M \
                    -c:a aac -b:a 128k \
                    -f mpegts "srt://${MIRROR_HOST}:${SRT_PORT}?streamid=${stream_id}" \
                    2>/dev/null
            fi
            ;;
        rtp)
            if [ $duration -eq 0 ]; then
                ffmpeg -re -f lavfi -i testsrc2=size=1920x1080:rate=25 \
                    -c:v libx265 -preset ultrafast -b:v 5M \
                    -f rtp "rtp://${MIRROR_HOST}:${RTP_PORT}" \
                    2>/dev/null &
            else
                ffmpeg -re -f lavfi -i testsrc2=size=1920x1080:rate=25 \
                    -t $duration \
                    -c:v libx265 -preset ultrafast -b:v 5M \
                    -f rtp "rtp://${MIRROR_HOST}:${RTP_PORT}" \
                    2>/dev/null
            fi
            ;;
    esac
    
    echo -e "${GREEN}✓ Stream started${NC}"
}

# Function to play stream
play_stream() {
    local stream_id=$1
    local player=${2:-ffplay}
    
    echo -e "${YELLOW}Playing stream: $stream_id${NC}"
    
    case $player in
        ffplay)
            ffplay -autoexit "http://${MIRROR_HOST}:443/streams/${stream_id}/playlist.m3u8" 2>/dev/null &
            ;;
        vlc)
            vlc "http://${MIRROR_HOST}:443/streams/${stream_id}/playlist.m3u8" 2>/dev/null &
            ;;
        browser)
            open "http://${MIRROR_HOST}:8080/admin/"
            ;;
    esac
}

# Function to load test multiple streams
load_test() {
    local num_streams=${1:-5}
    
    echo -e "${YELLOW}Starting load test with $num_streams streams${NC}"
    
    for i in $(seq 1 $num_streams); do
        generate_stream srt "load-test-$i" 0
        sleep 1
    done
    
    echo -e "${GREEN}✓ All streams started${NC}"
    echo "Press Ctrl+C to stop all streams"
    
    # Wait for interrupt
    trap "pkill -f ffmpeg; exit" INT
    wait
}

# Main menu
main() {
    if ! check_service; then
        exit 1
    fi
    
    echo ""
    echo "Mirror Streaming Test Tool"
    echo "========================="
    echo "1. Start single SRT test stream"
    echo "2. Start single RTP test stream"
    echo "3. Start multiple test streams"
    echo "4. Play test stream"
    echo "5. Load test"
    echo "6. Open admin dashboard"
    echo "0. Exit"
    echo ""
    
    read -p "Select option: " choice
    
    case $choice in
        1)
            read -p "Stream ID [test-1]: " stream_id
            stream_id=${stream_id:-test-1}
            generate_stream srt "$stream_id" 0
            echo "Stream is running. Press Ctrl+C to stop."
            wait
            ;;
        2)
            generate_stream rtp "rtp-test" 0
            echo "Stream is running. Press Ctrl+C to stop."
            wait
            ;;
        3)
            read -p "Number of streams [3]: " num
            num=${num:-3}
            for i in $(seq 1 $num); do
                generate_stream srt "multi-test-$i" 0
                sleep 1
            done
            echo "All streams running. Press Ctrl+C to stop."
            wait
            ;;
        4)
            read -p "Stream ID to play [test-1]: " stream_id
            stream_id=${stream_id:-test-1}
            play_stream "$stream_id" ffplay
            ;;
        5)
            read -p "Number of streams [10]: " num
            num=${num:-10}
            load_test $num
            ;;
        6)
            open "http://${MIRROR_HOST}:8080/admin/"
            ;;
        0)
            exit 0
            ;;
        *)
            echo "Invalid option"
            ;;
    esac
}

# Run main if not sourced
if [ "${BASH_SOURCE[0]}" == "${0}" ]; then
    main
fi
```

### scripts/build.sh
```bash
#!/bin/bash
# Build script for different platforms

set -e

# Variables
BINARY_NAME="mirror"
VERSION=$(git describe --tags --always --dirty)
BUILD_TIME=$(date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT=$(git rev-parse HEAD)
LDFLAGS="-w -s -X main.Version=$VERSION -X main.BuildTime=$BUILD_TIME -X main.GitCommit=$GIT_COMMIT"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${YELLOW}Building Mirror Streaming Platform v$VERSION${NC}"

# Clean build directory
rm -rf build/
mkdir -p build/

# Build for current platform
echo -e "${YELLOW}Building for current platform...${NC}"
go build -ldflags "$LDFLAGS" -o "build/${BINARY_NAME}" ./cmd/mirror

# Build for Linux AMD64
echo -e "${YELLOW}Building for Linux AMD64...${NC}"
GOOS=linux GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "build/${BINARY_NAME}-linux-amd64" ./cmd/mirror

# Build for Linux ARM64
echo -e "${YELLOW}Building for Linux ARM64...${NC}"
GOOS=linux GOARCH=arm64 go build -ldflags "$LDFLAGS" -o "build/${BINARY_NAME}-linux-arm64" ./cmd/mirror

# Build for Darwin AMD64
echo -e "${YELLOW}Building for Darwin AMD64...${NC}"
GOOS=darwin GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "build/${BINARY_NAME}-darwin-amd64" ./cmd/mirror

# Build for Darwin ARM64 (M1)
echo -e "${YELLOW}Building for Darwin ARM64...${NC}"
GOOS=darwin GOARCH=arm64 go build -ldflags "$LDFLAGS" -o "build/${BINARY_NAME}-darwin-arm64" ./cmd/mirror

echo -e "${GREEN}✓ Build complete!${NC}"
echo ""
echo "Binaries available in build/ directory:"
ls -lh build/
```

## GitHub Actions CI/CD

### .github/workflows/ci.yml
```yaml
name: CI

on:
  push:
    branches: [ main, develop ]
  pull_request:
    branches: [ main ]

env:
  GO_VERSION: '1.21'
  DOCKER_REGISTRY: ghcr.io

jobs:
  lint:
    name: Lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
      
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v3
        with:
          version: latest
          args: --timeout=5m

  test:
    name: Test
    runs-on: ubuntu-latest
    services:
      redis:
        image: redis:7-alpine
        options: >-
          --health-cmd "redis-cli ping"
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
        ports:
          - 6379:6379
    
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
      
      - name: Install dependencies
        run: |
          sudo apt-get update
          sudo apt-get install -y ffmpeg
      
      - name: Run tests
        run: |
          go test -v -race -coverprofile=coverage.out ./...
          go tool cover -func=coverage.out
      
      - name: Upload coverage
        uses: codecov/codecov-action@v3
        with:
          file: ./coverage.out
          flags: unittests
          name: codecov-umbrella

  build:
    name: Build
    runs-on: ubuntu-latest
    needs: [lint, test]
    strategy:
      matrix:
        os: [linux]
        arch: [amd64, arm64]
    
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
      
      - name: Build binary
        run: |
          GOOS=${{ matrix.os }} GOARCH=${{ matrix.arch }} \
          go build -ldflags "-w -s -X main.Version=${{ github.sha }}" \
          -o mirror-${{ matrix.os }}-${{ matrix.arch }} \
          ./cmd/mirror
      
      - name: Upload artifact
        uses: actions/upload-artifact@v3
        with:
          name: mirror-${{ matrix.os }}-${{ matrix.arch }}
          path: mirror-${{ matrix.os }}-${{ matrix.arch }}

  docker:
    name: Docker Build
    runs-on: ubuntu-latest
    needs: [lint, test]
    permissions:
      contents: read
      packages: write
    
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3
      
      - name: Log in to GitHub Container Registry
        uses: docker/login-action@v3
        with:
          registry: ${{ env.DOCKER_REGISTRY }}
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      
      - name: Extract metadata
        id: meta
        uses: docker/metadata-action@v5
        with:
          images: ${{ env.DOCKER_REGISTRY }}/${{ github.repository }}
          tags: |
            type=ref,event=branch
            type=ref,event=pr
            type=semver,pattern={{version}}
            type=semver,pattern={{major}}.{{minor}}
            type=sha
      
      - name: Build and push Docker image
        uses: docker/build-push-action@v5
        with:
          context: .
          file: ./deployments/docker/Dockerfile
          platforms: linux/amd64,linux/arm64
          push: ${{ github.event_name != 'pull_request' }}
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          cache-from: type=gha
          cache-to: type=gha,mode=max

  security:
    name: Security Scan
    runs-on: ubuntu-latest
    needs: [docker]
    if: github.event_name != 'pull_request'
    
    steps:
      - uses: actions/checkout@v4
      
      - name: Run Trivy vulnerability scanner
        uses: aquasecurity/trivy-action@master
        with:
          image-ref: '${{ env.DOCKER_REGISTRY }}/${{ github.repository }}:${{ github.sha }}'
          format: 'sarif'
          output: 'trivy-results.sarif'
      
      - name: Upload Trivy scan results
        uses: github/codeql-action/upload-sarif@v2
        with:
          sarif_file: 'trivy-results.sarif'
```

### .github/workflows/deploy.yml
```yaml
name: Deploy

on:
  push:
    tags:
      - 'v*'
  workflow_dispatch:
    inputs:
      environment:
        description: 'Environment to deploy to'
        required: true
        default: 'staging'
        type: choice
        options:
          - staging
          - production

env:
  AWS_REGION: us-east-1
  ECR_REPOSITORY: mirror

jobs:
  deploy:
    name: Deploy to ${{ github.event.inputs.environment || 'production' }}
    runs-on: ubuntu-latest
    environment: ${{ github.event.inputs.environment || 'production' }}
    
    steps:
      - uses: actions/checkout@v4
      
      - name: Configure AWS credentials
        uses: aws-actions/configure-aws-credentials@v4
        with:
          aws-access-key-id: ${{ secrets.AWS_ACCESS_KEY_ID }}
          aws-secret-access-key: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
          aws-region: ${{ env.AWS_REGION }}
      
      - name: Login to Amazon ECR
        id: login-ecr
        uses: aws-actions/amazon-ecr-login@v2
      
      - name: Build, tag, and push image to Amazon ECR
        env:
          ECR_REGISTRY: ${{ steps.login-ecr.outputs.registry }}
          IMAGE_TAG: ${{ github.sha }}
        run: |
          docker build -f deployments/docker/Dockerfile -t $ECR_REGISTRY/$ECR_REPOSITORY:$IMAGE_TAG .
          docker push $ECR_REGISTRY/$ECR_REPOSITORY:$IMAGE_TAG
          docker tag $ECR_REGISTRY/$ECR_REPOSITORY:$IMAGE_TAG $ECR_REGISTRY/$ECR_REPOSITORY:latest
          docker push $ECR_REGISTRY/$ECR_REPOSITORY:latest
      
      - name: Update ECS service
        run: |
          aws ecs update-service \
            --cluster mirror-${{ github.event.inputs.environment || 'production' }} \
            --service mirror-${{ github.event.inputs.environment || 'production' }} \
            --force-new-deployment
      
      - name: Wait for deployment
        run: |
          aws ecs wait services-stable \
            --cluster mirror-${{ github.event.inputs.environment || 'production' }} \
            --services mirror-${{ github.event.inputs.environment || 'production' }}
      
      - name: Notify deployment
        uses: 8398a7/action-slack@v3
        with:
          status: ${{ job.status }}
          text: 'Deployment to ${{ github.event.inputs.environment || 'production' }} ${{ job.status }}'
          webhook_url: ${{ secrets.SLACK_WEBHOOK }}
        if: always()
```

### .github/workflows/release.yml
```yaml
name: Release

on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write

jobs:
  release:
    name: Create Release
    runs-on: ubuntu-latest
    
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.21'
      
      - name: Generate changelog
        id: changelog
        run: |
          echo "CHANGELOG<<EOF" >> $GITHUB_OUTPUT
          git log --pretty=format:"- %s" $(git describe --tags --abbrev=0 HEAD^)..HEAD >> $GITHUB_OUTPUT
          echo "EOF" >> $GITHUB_OUTPUT
      
      - name: Build binaries
        run: |
          ./scripts/build.sh
          cd build && sha256sum * > checksums.txt
      
      - name: Create Release
        uses: softprops/action-gh-release@v1
        with:
          body: |
            ## Changes
            ${{ steps.changelog.outputs.CHANGELOG }}
            
            ## Docker Image
            ```
            docker pull ghcr.io/${{ github.repository }}:${{ github.ref_name }}
            ```
          files: |
            build/*
          draft: false
          prerelease: false
```

## Testing Setup

### .golangci.yml
```yaml
# golangci-lint configuration

linters:
  enable:
    - bodyclose
    - deadcode
    - depguard
    - dogsled
    - dupl
    - errcheck
    - exportloopref
    - exhaustive
    - gochecknoinits
    - goconst
    - gocritic
    - gocyclo
    - gofmt
    - goimports
    - gomnd
    - goprintffuncname
    - gosec
    - gosimple
    - govet
    - ineffassign
    - lll
    - misspell
    - nakedret
    - noctx
    - nolintlint
    - revive
    - staticcheck
    - structcheck
    - stylecheck
    - typecheck
    - unconvert
    - unparam
    - unused
    - varcheck
    - whitespace

linters-settings:
  depguard:
    list-type: blacklist
  dupl:
    threshold: 100
  exhaustive:
    default-signifies-exhaustive: false
  gocyclo:
    min-complexity: 15
  gomnd:
    settings:
      mnd:
        checks: argument,case,condition,return
  govet:
    check-shadowing: true
  lll:
    line-length: 140
  misspell:
    locale: US

issues:
  exclude-rules:
    - path: _test\.go
      linters:
        - gomnd
    - path: pkg/golinters/errcheck.go
      text: "SA1019: errCfg.Exclude is deprecated"
    - path: pkg/golinters/govet.go
      text: "SA1019: cfg.CheckShadowing is deprecated"

run:
  skip-dirs:
    - testdata
    - vendor
  timeout: 5m
```

### go.mod
```go
module github.com/zsiec/mirror

go 1.21

require (
    // Core dependencies
    github.com/datarhei/gosrt v0.5.7
    github.com/pion/rtp v1.8.3
    github.com/pion/webrtc/v4 v4.0.0-beta.19
    github.com/asticode/go-astiav v0.13.0
    github.com/etherlabsio/go-m3u8 v1.0.0
    github.com/Eyevinn/mp4ff v0.40.2
    github.com/quic-go/quic-go v0.40.1
    
    // Infrastructure
    github.com/redis/go-redis/v9 v9.3.1
    github.com/minio/minio-go/v7 v7.0.66
    github.com/prometheus/client_golang v1.18.0
    
    // Web framework
    github.com/gorilla/mux v1.8.1
    github.com/gorilla/websocket v1.5.1
    
    // Utilities
    github.com/rs/zerolog v1.31.0
    github.com/spf13/cobra v1.8.0
    github.com/spf13/viper v1.18.2
    github.com/hashicorp/golang-lru v1.0.2
    github.com/edsrzf/mmap-go v1.1.0
    
    // Testing
    github.com/stretchr/testify v1.8.4
    github.com/golang/mock v1.6.0
)
```

## Debugging and Troubleshooting

### scripts/debug.sh
```bash
#!/bin/bash
# Debug helper script

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${YELLOW}Mirror Streaming Platform - Debug Helper${NC}"
echo "========================================"

# Function to check system resources
check_resources() {
    echo -e "\n${YELLOW}System Resources:${NC}"
    
    # CPU
    echo -n "CPU Usage: "
    if command -v top &> /dev/null; then
        top -bn1 | grep "Cpu(s)" | sed "s/.*, *\([0-9.]*\)%* id.*/\1/" | awk '{print 100 - $1"%"}'
    fi
    
    # Memory
    echo -n "Memory Usage: "
    if command -v free &> /dev/null; then
        free -m | awk 'NR==2{printf "%s/%sMB (%.2f%%)\n", $3,$2,$3*100/$2 }'
    fi
    
    # GPU (if available)
    if command -v nvidia-smi &> /dev/null; then
        echo -e "\n${YELLOW}GPU Status:${NC}"
        nvidia-smi --query-gpu=name,memory.used,memory.total,utilization.gpu --format=csv,noheader
    fi
}

# Function to check service health
check_health() {
    echo -e "\n${YELLOW}Service Health:${NC}"
    
    # Check main service
    if curl -s http://localhost:8080/health | jq . 2>/dev/null; then
        echo -e "${GREEN}✓ Service is healthy${NC}"
    else
        echo -e "${RED}✗ Service health check failed${NC}"
    fi
    
    # Check Redis
    if redis-cli ping > /dev/null 2>&1; then
        echo -e "${GREEN}✓ Redis is running${NC}"
    else
        echo -e "${RED}✗ Redis is not running${NC}"
    fi
}

# Function to show logs
show_logs() {
    echo -e "\n${YELLOW}Recent Logs:${NC}"
    
    if [ -f "tmp/mirror.log" ]; then
        tail -n 50 tmp/mirror.log | grep -E "(ERROR|WARN)" || echo "No errors or warnings in recent logs"
    else
        echo "Log file not found"
    fi
}

# Function to check network
check_network() {
    echo -e "\n${YELLOW}Network Status:${NC}"
    
    # Check ports
    for port in 443 6000 5004 8080; do
        if lsof -i :$port > /dev/null 2>&1; then
            echo -e "${GREEN}✓ Port $port is listening${NC}"
        else
            echo -e "${RED}✗ Port $port is not listening${NC}"
        fi
    done
}

# Function to generate debug report
generate_report() {
    REPORT_FILE="debug-report-$(date +%Y%m%d-%H%M%S).txt"
    
    echo "Generating debug report..."
    
    {
        echo "Mirror Streaming Platform - Debug Report"
        echo "Generated: $(date)"
        echo "========================================="
        echo ""
        
        echo "System Information:"
        uname -a
        echo ""
        
        echo "Go Version:"
        go version
        echo ""
        
        echo "Docker Version:"
        docker version
        echo ""
        
        echo "Service Status:"
        systemctl status mirror 2>/dev/null || echo "Service not managed by systemd"
        echo ""
        
        echo "Environment Variables:"
        env | grep MIRROR_ || echo "No MIRROR_ environment variables set"
        echo ""
        
        echo "Recent Logs:"
        tail -n 100 tmp/mirror.log 2>/dev/null || echo "No logs available"
        
    } > "$REPORT_FILE"
    
    echo -e "${GREEN}✓ Debug report saved to: $REPORT_FILE${NC}"
}

# Main menu
echo ""
echo "1. Check system resources"
echo "2. Check service health"
echo "3. Show recent logs"
echo "4. Check network status"
echo "5. Generate full debug report"
echo "6. Connect with debugger (dlv)"
echo "0. Exit"
echo ""

read -p "Select option: " choice

case $choice in
    1) check_resources ;;
    2) check_health ;;
    3) show_logs ;;
    4) check_network ;;
    5) generate_report ;;
    6) dlv attach $(pgrep mirror) ;;
    0) exit 0 ;;
    *) echo "Invalid option" ;;
esac
```

## Release Process

### scripts/release.sh
```bash
#!/bin/bash
# Release automation script

set -e

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

# Get current version
CURRENT_VERSION=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
echo -e "${YELLOW}Current version: $CURRENT_VERSION${NC}"

# Prompt for new version
read -p "Enter new version (e.g., v1.0.1): " NEW_VERSION

# Validate version format
if ! [[ $NEW_VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo -e "${RED}Invalid version format. Must be vX.Y.Z${NC}"
    exit 1
fi

# Check if on main branch
CURRENT_BRANCH=$(git branch --show-current)
if [ "$CURRENT_BRANCH" != "main" ]; then
    echo -e "${RED}Releases must be created from main branch${NC}"
    echo "Current branch: $CURRENT_BRANCH"
    exit 1
fi

# Check for uncommitted changes
if ! git diff-index --quiet HEAD --; then
    echo -e "${RED}Uncommitted changes detected${NC}"
    echo "Please commit or stash changes before releasing"
    exit 1
fi

# Run tests
echo -e "${YELLOW}Running tests...${NC}"
make test

# Build binaries
echo -e "${YELLOW}Building binaries...${NC}"
./scripts/build.sh

# Create changelog
echo -e "${YELLOW}Generating changelog...${NC}"
CHANGELOG=$(git log --pretty=format:"- %s" ${CURRENT_VERSION}..HEAD)

# Create tag
echo -e "${YELLOW}Creating tag $NEW_VERSION...${NC}"
git tag -a "$NEW_VERSION" -m "Release $NEW_VERSION

$CHANGELOG"

# Push tag
echo -e "${YELLOW}Pushing tag to origin...${NC}"
git push origin "$NEW_VERSION"

echo -e "${GREEN}✓ Release $NEW_VERSION created successfully!${NC}"
echo ""
echo "GitHub Actions will now:"
echo "1. Build and test the code"
echo "2. Create Docker images"
echo "3. Create GitHub release"
echo "4. Deploy to production (if configured)"
```

### README.md
```markdown
# Mirror Streaming Platform

High-performance streaming platform for ingesting, transcoding, and delivering live video streams.

## Features

- **Multi-Protocol Ingestion**: SRT and RTP support for professional streaming
- **Hardware Acceleration**: NVIDIA NVENC for efficient transcoding
- **Low-Latency HLS**: Sub-second latency with CMAF chunks and HTTP/3
- **Multi-View Support**: Efficient bandwidth usage for viewing multiple streams
- **DVR Recording**: Automatic recording and playback of streams
- **Admin Dashboard**: Real-time monitoring and control

## Quick Start

### Prerequisites

- Go 1.21+
- Docker & Docker Compose
- FFmpeg with HEVC support
- NVIDIA GPU (optional, for hardware acceleration)

### Development Setup

```bash
# Clone the repository
git clone https://github.com/zsiec/mirror.git
cd mirror

# Run setup script
./scripts/setup-dev.sh

# Start development dependencies
make dev-deps

# Run the service
make dev

# In another terminal, start a test stream
make stream-test-srt
```

### Building

```bash
# Build for current platform
make build

# Build Docker image
make build-docker

# Build for all platforms
./scripts/build.sh
```

### Testing

```bash
# Run unit tests
make test

# Run integration tests
make test-integration

# Run benchmarks
make bench

# Generate coverage report
make test-coverage
```

## Configuration

Configuration can be set via:
1. Configuration file (`configs/mirror.yaml`)
2. Environment variables (prefix: `MIRROR_`)
3. Command-line flags

Example configuration:

```yaml
server:
  http3_port: 443
  admin_port: 8080

ingestion:
  srt:
    enabled: true
    port: 6000
  max_streams: 25

transcoding:
  use_gpu: true
  output_bitrate: 400000
```

## Deployment

### Docker Compose

```bash
docker-compose -f deployments/docker/docker-compose.yml up -d
```

### AWS ECS

```bash
cd deployments/terraform
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

## API Documentation

### Streaming Endpoints

- `GET /streams/{id}/playlist.m3u8` - LL-HLS playlist
- `GET /streams/{id}/segment_{n}.m4s` - Video segments
- `GET /streams/{id}/init.mp4` - Initialization segment

### Admin API

- `GET /api/v1/streams` - List active streams
- `GET /api/v1/streams/{id}` - Get stream details
- `DELETE /api/v1/streams/{id}` - Stop stream
- `GET /api/v1/health` - Health check
- `GET /api/v1/stats` - System statistics

## Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details.
```

## Summary

This completes the comprehensive design document for the Mirror Streaming Platform. The seven parts cover:

1. **Part 1**: System overview and architecture
2. **Part 2**: Core implementation with main.go and configuration
3. **Part 3**: Stream ingestion (SRT/RTP) components
4. **Part 4**: HLS packaging with LL-HLS and CMAF
5. **Part 5**: Admin API and monitoring dashboard
6. **Part 6**: Infrastructure and AWS deployment
7. **Part 7**: Development setup and CI/CD

The platform is designed to be:
- **Single Binary**: All components in one deployable unit
- **Hardware Accelerated**: NVIDIA GPU support for transcoding
- **Ultra-Low Latency**: CMAF chunks with HTTP/3 push
- **Production Ready**: Complete monitoring, logging, and deployment automation
- **Developer Friendly**: Hot reload, testing tools, and comprehensive documentation

Ready to start implementation!