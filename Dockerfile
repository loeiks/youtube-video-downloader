# Multi-stage build: Use Go image for building, then smaller Alpine for runtime
# golang:1.24-alpine provides Go compiler + Alpine Linux (smaller than full Ubuntu/Debian)
FROM golang:1.24-alpine AS builder

# Set the working directory inside the container where we'll build our app
WORKDIR /app

# Copy dependency files first (go.mod and go.sum)
# This enables Docker layer caching - if dependencies don't change, 
# this layer is reused, making subsequent builds faster
COPY go.mod go.sum ./

# Download all Go module dependencies
# go mod verify ensures downloaded modules match expected checksums (security)
# This step is cached if go.mod/go.sum haven't changed
RUN go mod download && go mod verify

# Copy the source code (done after dependency download for better caching)
# If only source changes, we don't re-download dependencies
COPY main.go .

# Build the Go binary with optimizations:
# CGO_ENABLED=0: Disable CGO for static binary (no external C dependencies)
# GOOS=linux: Target Linux OS (even if building on Windows/Mac)
# GOARCH=amd64: Target 64-bit Intel/AMD architecture
# -ldflags='-w -s': Strip debug info and symbol table (smaller binary)
# -extldflags "-static": Create completely static binary
# -a: Force rebuild of packages
# -installsuffix cgo: Separate build cache for CGO disabled builds
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o youtube-downloader .

# Second stage: Runtime image (much smaller than build image)
# alpine:3.18 is a minimal Linux distribution (~5MB vs ~900MB for Ubuntu)
FROM alpine:3.18

# Install required packages in a single layer (reduces image size):
# ffmpeg: Required for merging video/audio streams
# ca-certificates: Required for HTTPS connections to YouTube
# curl: Lightweight tool for health checks (smaller than wget)
# tzdata: Timezone data for proper logging timestamps
# && rm -rf /var/cache/apk/*: Clean package cache to reduce image size
RUN apk add --no-cache \
    ffmpeg \
    ca-certificates \
    curl \
    tzdata \
    && rm -rf /var/cache/apk/*

# Set working directory for the application
WORKDIR /app

# Create system user and group for security (don't run as root):
# addgroup -g 1001 -S appgroup: Create group with ID 1001, system group
# adduser -u 1001 -S appuser -G appgroup: Create user with ID 1001, add to group
# mkdir -p /tmp/youtube-downloads: Create temp directory for video processing
# chown -R: Give ownership of directories to our non-root user
RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup && \
    mkdir -p /tmp/youtube-downloads && \
    chown -R appuser:appgroup /app /tmp/youtube-downloads

# Copy the compiled binary from the builder stage
# --chown=appuser:appgroup: Set correct ownership for the binary
COPY --from=builder --chown=appuser:appgroup /app/youtube-downloader .

# Switch to non-root user for security
# Running as non-root prevents privilege escalation attacks
USER appuser

# Expose port 7839 to allow external connections
# This is just documentation - actual port mapping happens in docker-compose
EXPOSE 7839

# Set environment variables with default values:
# These can be overridden by docker-compose or docker run commands
ENV TEMP_DIR="/tmp/youtube-downloads" \
    SERVER_PORT="7839" \
    MAX_VIDEO_HEIGHT="1080" \
    MAX_CONCURRENT="3" \
    FFMPEG_PRESET="veryfast"

# Health check configuration:
# --interval=30s: Check every 30 seconds
# --timeout=5s: Fail if check takes longer than 5 seconds
# --start-period=10s: Wait 10 seconds before first check (app startup time)
# --retries=2: Mark unhealthy after 2 consecutive failures
# curl -f: Fail on HTTP error codes (4xx, 5xx)
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=2 \
    CMD curl -f http://localhost:7839/health || exit 1

# Default command to run when container starts
# Runs our compiled binary
CMD ["./youtube-downloader"]
