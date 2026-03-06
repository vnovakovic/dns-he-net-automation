# Stage 1: Download Go module dependencies (cached separately for faster rebuilds).
FROM golang:1.25 AS modules
WORKDIR /modules
COPY go.mod go.sum ./
RUN go mod download

# Stage 2: Build the Go binary and install the Playwright CLI.
FROM golang:1.25 AS builder
WORKDIR /app

# Copy cached modules from stage 1 to avoid re-downloading.
COPY --from=modules /go/pkg /go/pkg

# Copy source code.
COPY . .

# Build the server binary.
# CGO_ENABLED=0: required for modernc.org/sqlite (pure Go, no CGo).
# GOOS/GOARCH: explicit cross-compilation target.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -o /bin/server ./cmd/server

# Extract playwright-go version from go.mod and install the Playwright CLI.
# This is needed so the runtime stage can install the correct Chromium version.
RUN PW_VERSION=$(grep 'playwright-go' go.mod | awk '{print $2}') && \
    echo "Installing playwright CLI version: $PW_VERSION" && \
    go install "github.com/playwright-community/playwright-go/cmd/playwright@${PW_VERSION}"

# Stage 3: Minimal runtime image with Chromium.
# Use ubuntu:noble (not alpine) -- Chromium on alpine requires musl workarounds.
# Do NOT switch to chromedp/headless-shell -- incompatible with playwright-go (research decision).
FROM ubuntu:noble

# OCI standard image labels (OPS-05).
LABEL org.opencontainers.image.title="dns-he-net-automation"
LABEL org.opencontainers.image.description="REST API wrapper for dns.he.net via browser automation"
LABEL org.opencontainers.image.source="https://github.com/vnovakov/dns-he-net-automation"

# Install system dependencies: CA certs for HTTPS, tzdata for time zones.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata && \
    rm -rf /var/lib/apt/lists/*

# Copy the Playwright CLI from the builder stage and install Chromium with system deps.
# --with-deps installs ~50 system libraries required by Chromium (libx11, libnss3, etc.).
# Without --with-deps, Chromium fails to launch with "error while loading shared libraries".
COPY --from=builder /go/bin/playwright /usr/local/bin/playwright
RUN playwright install --with-deps chromium

# Create a non-root user for running the service (security hardening).
RUN useradd --system --no-create-home --uid 1001 server

# Create persistent data directory owned by the service user.
# WHY /data (not /etc or /var/lib):
#   /data is mounted as a Docker volume in production so the DB and auto-generated
#   TLS cert/key survive container restarts and image upgrades. Ownership by uid 1001
#   (server) allows the non-root process to write files here without sudo.
#   The [VOLUME] declaration below makes the mount point explicit in the image manifest.
RUN mkdir -p /data && chown server:server /data

# Copy the compiled server binary.
COPY --from=builder /bin/server /usr/local/bin/server

# Run as non-root user.
USER server

# Default environment variables for production deployment.
ENV PLAYWRIGHT_HEADLESS=true

# TLS certificate paths inside the container.
# WHY /data (not /etc/ssl or /app):
#   /data is the persistent volume mount point. Placing the cert here ensures it
#   survives container restarts — the server auto-generates it on first start if absent.
#   If SSL_CERT/SSL_KEY are left empty the server falls back to plain HTTP (reverse
#   proxy deployments where TLS is terminated upstream).
ENV SSL_CERT=/data/server.crt
ENV SSL_KEY=/data/server.key

# Default DB path inside the persistent volume.
ENV DB_PATH=/data/dnshenet-server.db

# Screenshot dir -- mount a volume here to enable debug screenshots.
# ENV SCREENSHOT_DIR=/screenshots

# Declare /data as a volume so operators know to mount it for persistence.
# Without a volume mount, /data is an anonymous volume that survives container
# restart but NOT image upgrade (docker pull + recreate loses the DB and certs).
VOLUME ["/data"]

# Expose the HTTPS API port.
EXPOSE 9001

ENTRYPOINT ["server"]
