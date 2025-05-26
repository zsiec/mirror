#!/bin/bash
# SRT environment setup for Go compilation
#
# This script automatically configures the build environment for SRT (Secure Reliable Transport)
# library compilation on macOS and Linux systems.
#
# Usage:
#   ./scripts/srt-env.sh <command>
#   
# Examples:
#   ./scripts/srt-env.sh go test ./...
#   ./scripts/srt-env.sh go build ./cmd/mirror
#
# The script automatically:
# - Detects SRT installation via pkg-config
# - Sets appropriate CGO flags for compilation
# - Handles both Homebrew (macOS) and system installations
# - Provides helpful error messages if SRT is missing

set -e

# Function to print colored output
print_info() {
    echo -e "\033[1;34m[INFO]\033[0m $1"
}

print_error() {
    echo -e "\033[1;31m[ERROR]\033[0m $1" >&2
}

print_success() {
    echo -e "\033[1;32m[SUCCESS]\033[0m $1"
}

# Check if pkg-config is available
if ! command -v pkg-config >/dev/null 2>&1; then
    print_error "pkg-config is not installed"
    print_info "On macOS: brew install pkg-config"
    print_info "On Ubuntu/Debian: sudo apt-get install pkg-config"
    exit 1
fi

# Check if SRT is available via pkg-config
if ! pkg-config --exists srt 2>/dev/null; then
    print_error "SRT library not found via pkg-config"
    print_info "Install SRT library:"
    print_info "  macOS: brew install srt"
    print_info "  Ubuntu/Debian: sudo apt-get install libsrt-openssl-dev"
    print_info "  From source: https://github.com/Haivision/srt"
    exit 1
fi

# Set up environment for SRT compilation
export CGO_CFLAGS="$(pkg-config --cflags srt)"
export CGO_LDFLAGS="$(pkg-config --libs srt)"

# Ensure PKG_CONFIG_PATH includes common locations
if [[ "$OSTYPE" == "darwin"* ]]; then
    # macOS - add Homebrew paths
    export PKG_CONFIG_PATH="/opt/homebrew/lib/pkgconfig:/usr/local/lib/pkgconfig:$PKG_CONFIG_PATH"
elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
    # Linux - add common system paths
    export PKG_CONFIG_PATH="/usr/lib/pkgconfig:/usr/local/lib/pkgconfig:$PKG_CONFIG_PATH"
fi

# Validate that we have the required flags
if [[ -z "$CGO_CFLAGS" ]] || [[ -z "$CGO_LDFLAGS" ]]; then
    print_error "Failed to get SRT compilation flags"
    print_info "CGO_CFLAGS: '$CGO_CFLAGS'"
    print_info "CGO_LDFLAGS: '$CGO_LDFLAGS'"
    exit 1
fi

# Show environment for debugging (only if verbose)
if [[ "${VERBOSE:-}" == "1" ]] || [[ "${SRT_DEBUG:-}" == "1" ]]; then
    print_info "SRT Environment Configuration:"
    echo "  CGO_CFLAGS: $CGO_CFLAGS"
    echo "  CGO_LDFLAGS: $CGO_LDFLAGS"
    echo "  PKG_CONFIG_PATH: $PKG_CONFIG_PATH"
    echo "  SRT Version: $(pkg-config --modversion srt 2>/dev/null || echo 'unknown')"
fi

# Execute the command with proper environment
if [[ $# -eq 0 ]]; then
    print_error "No command specified"
    print_info "Usage: $0 <command> [args...]"
    print_info "Example: $0 go test ./..."
    exit 1
fi

exec "$@"
