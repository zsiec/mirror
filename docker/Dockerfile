# Build stage
FROM golang:1.23-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make ca-certificates

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN make build

# Runtime stage
FROM nvidia/cuda:12.3.1-runtime-ubuntu22.04

# Install runtime dependencies
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

# Copy configuration files
COPY --from=builder /build/configs /etc/mirror/configs

# Switch to non-root user
USER mirror

# Set working directory
WORKDIR /app

# Expose ports
EXPOSE 443/udp 9090/tcp

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/mirror", "-config", "/etc/mirror/configs/default.yaml", "-version"]

# Default command
CMD ["mirror", "-config", "/etc/mirror/configs/default.yaml"]
