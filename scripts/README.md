# Scripts Package

The scripts package contains build tools, environment setup scripts, and automation utilities for the Mirror video streaming platform development and deployment.

## Overview

This directory provides essential development and deployment scripts that support:

- **Environment Setup**: SRT library configuration and development environment preparation
- **Build Automation**: Cross-platform builds with proper dependency handling
- **Deployment Scripts**: Production deployment and configuration management
- **Development Tools**: Helper scripts for common development tasks

## Scripts

### srt-env.sh

The `srt-env.sh` script configures the SRT (Secure Reliable Transport) library environment for Go CGO compilation.

#### Purpose
- Detects and configures SRT library paths
- Sets up proper CGO environment variables
- Handles different platforms (macOS, Linux, Windows)
- Provides consistent build environment across development machines

#### Usage

```bash
# Source the script to configure environment
source scripts/srt-env.sh

# The script is automatically used by Makefile targets
make build    # Uses SRT environment
make test     # Uses SRT environment
```

#### Platform Support

**macOS (Homebrew)**
```bash
# Automatically detects Homebrew SRT installation
# Sets CGO flags for /opt/homebrew/lib and /usr/local/lib
export CGO_CFLAGS="-I/opt/homebrew/include -I/usr/local/include"
export CGO_LDFLAGS="-L/opt/homebrew/lib -L/usr/local/lib -lsrt"
```

**Linux (Package Manager)**
```bash
# Configures for system-wide SRT installation
export CGO_CFLAGS="-I/usr/include"
export CGO_LDFLAGS="-L/usr/lib -L/usr/lib/x86_64-linux-gnu -lsrt"
```

**Manual Installation**
```bash
# Supports custom SRT installation paths
export SRT_ROOT="/path/to/srt"
export CGO_CFLAGS="-I$SRT_ROOT/include"
export CGO_LDFLAGS="-L$SRT_ROOT/lib -lsrt"
```

#### Environment Variables

The script sets the following environment variables:

- **CGO_ENABLED**: Enables CGO compilation (set to "1")
- **CGO_CFLAGS**: C compiler flags for header file locations
- **CGO_LDFLAGS**: Linker flags for library locations
- **PKG_CONFIG_PATH**: Package config search paths
- **SRT_ROOT**: SRT installation root (if manually set)

#### Platform Detection

```bash
# Automatic platform detection
detect_platform() {
    case "$(uname -s)" in
        Darwin*)  echo "macos" ;;
        Linux*)   echo "linux" ;;
        CYGWIN*|MINGW*|MSYS*) echo "windows" ;;
        *)        echo "unknown" ;;
    esac
}
```

#### SRT Installation Verification

```bash
# Verifies SRT library is available
verify_srt() {
    # Check for srt.h header
    if ! find_header "srt/srt.h"; then
        echo "❌ SRT headers not found"
        return 1
    fi
    
    # Check for libsrt library
    if ! find_library "libsrt"; then
        echo "❌ SRT library not found"
        return 1
    fi
    
    echo "✅ SRT library verified"
    return 0
}
```

## Installation Scripts

### install-deps.sh (Future)

Automated dependency installation script:

```bash
#!/bin/bash
# Install Mirror development dependencies

install_srt_macos() {
    if command -v brew >/dev/null 2>&1; then
        echo "Installing SRT via Homebrew..."
        brew install srt
    else
        echo "Homebrew not found. Please install manually."
        exit 1
    fi
}

install_srt_linux() {
    if command -v apt-get >/dev/null 2>&1; then
        echo "Installing SRT via apt..."
        sudo apt-get update
        sudo apt-get install -y libsrt-dev
    elif command -v yum >/dev/null 2>&1; then
        echo "Installing SRT via yum..."
        sudo yum install -y srt-devel
    else
        echo "Package manager not supported. Please install manually."
        exit 1
    fi
}
```

### setup-dev.sh (Future)

Development environment setup:

```bash
#!/bin/bash
# Setup complete development environment

setup_git_hooks() {
    echo "Setting up git hooks..."
    cp scripts/hooks/* .git/hooks/
    chmod +x .git/hooks/*
}

setup_certificates() {
    echo "Generating development certificates..."
    mkdir -p certs
    openssl req -x509 -newkey rsa:4096 -keyout certs/key.pem \
        -out certs/cert.pem -days 365 -nodes \
        -subj "/C=US/ST=CA/L=SF/O=Mirror/CN=localhost"
}

setup_config() {
    echo "Creating development configuration..."
    cp configs/development.yaml.example configs/development.yaml
}
```

## Build Scripts

### build.sh (Future)

Cross-platform build script:

```bash
#!/bin/bash
# Build Mirror for multiple platforms

PLATFORMS=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
)

for platform in "${PLATFORMS[@]}"; do
    GOOS=${platform%/*}
    GOARCH=${platform#*/}
    
    echo "Building for $GOOS/$GOARCH..."
    
    # Source SRT environment
    source scripts/srt-env.sh
    
    # Build with proper flags
    CGO_ENABLED=1 GOOS=$GOOS GOARCH=$GOARCH \
        go build -ldflags "$LDFLAGS" \
        -o "bin/mirror-$GOOS-$GOARCH" ./cmd/mirror
done
```

### package.sh (Future)

Distribution packaging script:

```bash
#!/bin/bash
# Create distribution packages

create_tarball() {
    local os=$1
    local arch=$2
    local version=$3
    
    local name="mirror-$version-$os-$arch"
    local dir="dist/$name"
    
    mkdir -p "$dir"
    
    # Copy binary
    cp "bin/mirror-$os-$arch" "$dir/mirror"
    
    # Copy configuration
    cp -r configs "$dir/"
    cp README.md "$dir/"
    cp LICENSE "$dir/"
    
    # Create tarball
    tar -czf "dist/$name.tar.gz" -C dist "$name"
    
    echo "Created dist/$name.tar.gz"
}
```

## Docker Scripts

### docker-build.sh (Future)

Docker image build automation:

```bash
#!/bin/bash
# Build Docker images with SRT support

build_image() {
    local tag=$1
    local dockerfile=$2
    
    echo "Building Docker image: $tag"
    
    docker build \
        --tag "$tag" \
        --file "$dockerfile" \
        --build-arg SRT_VERSION="1.4.4" \
        --build-arg GO_VERSION="1.21" \
        .
}

# Build production image
build_image "mirror:latest" "docker/Dockerfile"

# Build development image
build_image "mirror:dev" "docker/Dockerfile.dev"
```

### docker-push.sh (Future)

Docker registry deployment:

```bash
#!/bin/bash
# Push Docker images to registry

REGISTRY="${DOCKER_REGISTRY:-ghcr.io/zsiec}"
VERSION="${VERSION:-latest}"

images=(
    "mirror:$VERSION"
    "mirror:latest"
)

for image in "${images[@]}"; do
    echo "Pushing $REGISTRY/$image"
    docker tag "$image" "$REGISTRY/$image"
    docker push "$REGISTRY/$image"
done
```

## Development Scripts

### test.sh (Future)

Comprehensive testing script:

```bash
#!/bin/bash
# Run comprehensive test suite

run_unit_tests() {
    echo "Running unit tests..."
    source scripts/srt-env.sh
    go test -race -coverprofile=coverage.unit.out ./internal/...
}

run_integration_tests() {
    echo "Running integration tests..."
    source scripts/srt-env.sh
    go test -race -coverprofile=coverage.integration.out ./tests/...
}

run_benchmarks() {
    echo "Running benchmarks..."
    source scripts/srt-env.sh
    go test -bench=. -benchmem ./internal/ingestion/...
}

generate_coverage() {
    echo "Generating coverage report..."
    go tool cover -html=coverage.unit.out -o coverage.html
}
```

### lint.sh (Future)

Code quality checking:

```bash
#!/bin/bash
# Run code quality checks

run_golangci_lint() {
    echo "Running golangci-lint..."
    golangci-lint run --config .golangci.yml
}

run_gosec() {
    echo "Running security checks..."
    gosec ./...
}

check_mod_tidy() {
    echo "Checking go.mod..."
    go mod tidy
    if ! git diff --quiet go.mod go.sum; then
        echo "go.mod or go.sum needs updating"
        exit 1
    fi
}
```

## Deployment Scripts

### deploy.sh (Future)

Production deployment automation:

```bash
#!/bin/bash
# Deploy Mirror to production environment

deploy_kubernetes() {
    local environment=$1
    
    echo "Deploying to Kubernetes ($environment)..."
    
    # Apply configuration
    kubectl apply -f "k8s/$environment/" -n mirror
    
    # Wait for rollout
    kubectl rollout status deployment/mirror -n mirror
    
    # Verify deployment
    kubectl get pods -n mirror
}

deploy_docker_compose() {
    local environment=$1
    
    echo "Deploying with Docker Compose ($environment)..."
    
    # Pull latest images
    docker-compose -f "docker/docker-compose.$environment.yml" pull
    
    # Deploy with rolling update
    docker-compose -f "docker/docker-compose.$environment.yml" up -d
    
    # Health check
    scripts/health-check.sh
}
```

### health-check.sh (Future)

Post-deployment health verification:

```bash
#!/bin/bash
# Verify deployment health

check_health_endpoint() {
    local url=$1
    local timeout=${2:-30}
    
    echo "Checking health endpoint: $url"
    
    for i in $(seq 1 $timeout); do
        if curl -f -s "$url/health" > /dev/null; then
            echo "✅ Health check passed"
            return 0
        fi
        
        echo "⏳ Waiting for service... ($i/$timeout)"
        sleep 1
    done
    
    echo "❌ Health check failed"
    return 1
}

check_metrics() {
    local url=$1
    
    echo "Checking metrics endpoint: $url"
    
    if curl -f -s "$url/metrics" | grep -q "http_requests_total"; then
        echo "✅ Metrics available"
        return 0
    fi
    
    echo "❌ Metrics check failed"
    return 1
}
```

## Configuration Management

### config-gen.sh (Future)

Configuration file generation:

```bash
#!/bin/bash
# Generate configuration files from templates

generate_config() {
    local template=$1
    local output=$2
    local environment=$3
    
    echo "Generating $output from $template"
    
    # Substitute environment variables
    envsubst < "$template" > "$output"
    
    # Validate configuration
    if command -v yq >/dev/null 2>&1; then
        yq eval '.' "$output" > /dev/null
        if [ $? -eq 0 ]; then
            echo "✅ Configuration valid"
        else
            echo "❌ Configuration invalid"
            exit 1
        fi
    fi
}
```

## Usage Examples

### Daily Development Workflow

```bash
# Setup environment
source scripts/srt-env.sh

# Run tests
scripts/test.sh

# Build application
make build

# Run locally
make run
```

### CI/CD Pipeline Integration

```yaml
# GitHub Actions example
steps:
  - name: Setup SRT Environment
    run: source scripts/srt-env.sh

  - name: Run Tests
    run: scripts/test.sh

  - name: Build
    run: scripts/build.sh

  - name: Package
    run: scripts/package.sh
```

### Production Deployment

```bash
# Deploy to staging
scripts/deploy.sh staging

# Run health checks
scripts/health-check.sh https://staging.mirror.example.com

# Deploy to production
scripts/deploy.sh production

# Verify deployment
scripts/health-check.sh https://mirror.example.com
```

## Best Practices

### Script Development
- Use `set -euo pipefail` for error handling
- Provide verbose output with timestamps
- Include help text and usage examples
- Make scripts idempotent where possible
- Use proper error codes and exit status

### Environment Management
- Never hardcode sensitive values
- Use environment variables for configuration
- Provide sensible defaults
- Validate required dependencies

### Cross-Platform Support
- Test scripts on multiple platforms
- Use portable shell constructs
- Handle platform-specific differences
- Document platform requirements

## Future Enhancements

Planned additions to the scripts package:

- **Database Migration Scripts**: Schema updates and data migration
- **Monitoring Setup**: Automated monitoring configuration
- **Load Testing Scripts**: Performance testing automation
- **Security Scanning**: Automated vulnerability scanning
- **Backup Scripts**: Data backup and recovery automation
