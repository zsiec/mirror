# Build stage
FROM golang:1.23 AS builder

# Install build dependencies including SRT and FFmpeg
RUN apt-get update && apt-get install -y \
    git \
    make \
    ca-certificates \
    gcc \
    g++ \
    pkg-config \
    cmake \
    libssl-dev \
    build-essential \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# Install SRT library from source since Alpine doesn't have it in packages
WORKDIR /tmp
RUN git clone https://github.com/Haivision/srt.git && \
    cd srt && \
    git checkout v1.5.4 && \
    mkdir build && \
    cd build && \
    cmake -DCMAKE_INSTALL_PREFIX=/usr \
          -DCMAKE_BUILD_TYPE=Release \
          -DENABLE_SHARED=ON \
          -DENABLE_STATIC=ON \
          -DENABLE_APPS=OFF \
          -DENABLE_LOGGING=OFF \
          .. && \
    make -j$(nproc) && \
    make install

# Verify SRT and FFmpeg installation in build stage
RUN ls -la /usr/include/srt/ && \
    ls -la /usr/lib/*/libsrt* && \
    pkg-config --cflags --libs srt && \
    echo "FFmpeg version:" && \
    ffmpeg -version | head -3

# Set working directory for Go build
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Enable CGO and download dependencies
ENV CGO_ENABLED=1
RUN go mod download

# Copy source code
COPY . .

# Build the application with CGO enabled
RUN make build

# Verify the binary was built successfully
RUN ls -la bin/ && \
    ls -la bin/mirror && \
    ldd bin/mirror

# Runtime stage
FROM nvidia/cuda:12.3.1-runtime-ubuntu22.04

# Install runtime dependencies including FFmpeg
RUN apt-get update && apt-get install -y \
    ca-certificates \
    ffmpeg \
    libssl3 \
    && rm -rf /var/lib/apt/lists/*

# Copy SRT library and headers from build stage
COPY --from=builder /usr/lib/aarch64-linux-gnu/libsrt.so* /usr/lib/aarch64-linux-gnu/
COPY --from=builder /usr/include/srt /usr/include/srt

# Update library cache
RUN ldconfig

# Verify SRT and FFmpeg installation in runtime
RUN ldconfig -p | grep srt && \
    ls -la /usr/include/srt/ && \
    ls -la /usr/lib/*/libsrt* && \
    echo "FFmpeg runtime verification:" && \
    ffmpeg -version | head -3

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
