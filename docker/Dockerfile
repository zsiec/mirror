# Build stage
FROM golang:1.24 AS builder

# Install build dependencies (FFmpeg only — SRT is pure Go, no CGo needed)
RUN apt-get update && apt-get install -y \
    git \
    make \
    ca-certificates \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# Verify FFmpeg installation in build stage
RUN echo "FFmpeg version:" && \
    ffmpeg -version | head -3

# Set working directory for Go build
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN make build

# Verify the binary was built successfully
RUN ls -la bin/ && \
    ls -la bin/mirror

# Runtime stage
FROM nvidia/cuda:12.3.1-runtime-ubuntu22.04

# Install runtime dependencies including FFmpeg
RUN apt-get update && apt-get install -y \
    ca-certificates \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN useradd -m -u 1000 mirror

# Create necessary directories
RUN mkdir -p /etc/mirror/configs /var/log/mirror /app/certs && \
    chown -R mirror:mirror /etc/mirror /var/log/mirror /app

# Copy binary from builder
COPY --from=builder /build/bin/mirror /usr/local/bin/mirror

# Make binary executable and verify it exists
RUN chmod +x /usr/local/bin/mirror && \
    ls -la /usr/local/bin/mirror

# Copy configuration files
COPY --from=builder /build/configs /etc/mirror/configs

# Switch to non-root user
USER mirror

# Set working directory
WORKDIR /app

# Expose ports
EXPOSE 443/udp 8080/tcp 9090/tcp 30000/udp 5004/udp

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/mirror", "-config", "/etc/mirror/configs/default.yaml", "-version"]

# Default command - use full path
CMD ["/usr/local/bin/mirror", "-config", "/etc/mirror/configs/default.yaml"]
